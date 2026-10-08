import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createWriteStream, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import { finished } from 'node:stream/promises';
import { tmpdir } from 'node:os';
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { performance } from 'node:perf_hooks';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { registry, scenarioCaseFor } from './verify-ui/registry.mjs';
import { assertCapturedTargetMatches, parseCapturedBuildIdentity } from './verify-ui/helpers/owned-stack-health.mjs';
import { sourceFingerprintChangedPaths } from './verify-ui/helpers/source-fingerprint.mjs';
import { classifyEvidence } from './verify-ui/helpers/coverage-status.mjs';
import { planFocusedCheckGroups } from './verify-ui/helpers/focused-check-groups.mjs';

const repositoryRoot = realpathSync(resolve(dirname(fileURLToPath(import.meta.url)), '..'));
const requiredCaptureEnvironment = [
  'LOOM_CDA_SOURCE_ROOT',
  'LOOM_CDA_PROJECT',
  'LOOM_CDA_API_ORIGIN',
  'LOOM_CDA_UI_ORIGIN',
  'LOOM_CDA_API_CONTAINER',
  'LOOM_CDA_COMPOSE_PROJECT',
  'LOOM_CDA_ARANGO_CONTAINER',
  'LOOM_CDA_CLICKHOUSE_CONTAINER',
];
const FOCUSED_CHECK_TIMEOUT_MS = 60_000;
const PREPARATION_STAGE_TIMEOUT_MS = 90_000;
const BROWSER_STAGE_TIMEOUT_MS = 15 * 60_000;
const FOCUSED_CHECK_OUTPUT_PREVIEW_BYTES = 8 * 1024;
const MAX_CONCURRENT_FOCUSED_CHECKS = 2;
const lifecycleDimensionNames = ['usability', 'correctness', 'persistence', 'performance'];

const usage = [
  'Usage:',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --target <config-path> [--grep <native-test-regex>]',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --target-from-environment --grep <native-test-regex>',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --checks-only',
  '',
  'Use --target for cases with a registered identity. --target-from-environment is',
  'explicitly registry-unbound and relies on the selected case to validate scope.',
  'Registered focused checks run before the native bracket; checks-only runs them',
  'without Docker or Playwright.',
].join('\n');

function isWithin(parent, candidate) {
  const path = relative(parent, candidate);
  return path === '' || (path !== '..' && !path.startsWith('..' + sep) && !isAbsolute(path));
}

function argValue(args, name) {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
}

function requiredEnvironmentError(env) {
  const missing = requiredCaptureEnvironment.filter((name) => !String(env[name] ?? '').trim());
  return missing.length ? 'Missing required owned environment names: ' + missing.join(', ') : null;
}

function createEvidenceDirectory(root, evidenceParent, scenarioID, caseName) {
  const parent = resolve(evidenceParent ?? join(tmpdir(), 'loom-verification-brackets'));
  mkdirSync(parent, { recursive: true, mode: 0o700 });
  const realParent = realpathSync(parent);
  assert(!isWithin(root, realParent), 'Evidence parent must be outside the watched repository root.');
  const prefix = 'run-' + String(scenarioID).replace(/[^a-zA-Z0-9_-]/g, '-') + '-'
    + String(caseName).replace(/[^a-zA-Z0-9_-]/g, '-') + '-';
  const directory = mkdtempSync(join(realParent, prefix));
  const realDirectory = realpathSync(directory);
  assert(!isWithin(root, realDirectory), 'Evidence directory must be outside the watched repository root.');
  return realDirectory;
}

function retainPreview(chunks, state, chunk, limit = 1024 * 1024) {
  if (state.bytes >= limit) return;
  const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
  const remaining = limit - state.bytes;
  const retained = buffer.length <= remaining ? buffer : buffer.subarray(0, remaining);
  chunks.push(retained);
  state.bytes += retained.length;
}

function signalOwnedProcessTree(child, signal) {
  if (process.platform !== 'win32' && Number.isInteger(child?.pid) && child.pid > 0) {
    try {
      process.kill(-child.pid, signal);
      return;
    } catch {
      // If a process group is already gone or unavailable, fall back to the owned child.
    }
  }
  try { child?.kill(signal); } catch { /* The child may have exited between timeout and signal. */ }
}

function ownedProcessGroupExists(child) {
  if (process.platform === 'win32' || !Number.isInteger(child?.pid) || child.pid <= 0) return false;
  try {
    process.kill(-child.pid, 0);
    return true;
  } catch (error) {
    return error?.code === 'EPERM';
  }
}

export async function runProcess(command, args, {
  cwd, env, stdoutPath, stderrPath, timeoutMs = PREPARATION_STAGE_TIMEOUT_MS,
  outputPreviewLimit = 1024 * 1024,
}) {
  mkdirSync(dirname(stdoutPath), { recursive: true, mode: 0o700 });
  const stdoutFile = createWriteStream(stdoutPath, { flags: 'wx', mode: 0o600 });
  const stderrFile = createWriteStream(stderrPath, { flags: 'wx', mode: 0o600 });
  const stdoutChunks = [];
  const stderrChunks = [];
  const stdoutState = { bytes: 0, totalBytes: 0 };
  const stderrState = { bytes: 0, totalBytes: 0 };
  const started = performance.now();
  let child;
  let timedOut = false;
  let timeoutHandle;
  let killHandle;
  let killEscalation;
  let finishKillEscalation;
  try {
    child = spawn(command, args, {
      cwd,
      env,
      detached: process.platform !== 'win32',
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch (error) {
    stdoutFile.end();
    stderrFile.end();
    await Promise.allSettled([finished(stdoutFile), finished(stderrFile)]);
    return { exitCode: null, durationMs: performance.now() - started, error: String(error?.message ?? error), stdoutText: '', stderrText: '', timedOut: false };
  }

  child.stdout.pipe(stdoutFile);
  child.stderr.pipe(stderrFile);
  child.stdout.on('data', (chunk) => {
    stdoutState.totalBytes += Buffer.byteLength(chunk);
    retainPreview(stdoutChunks, stdoutState, chunk, outputPreviewLimit);
  });
  child.stderr.on('data', (chunk) => {
    stderrState.totalBytes += Buffer.byteLength(chunk);
    retainPreview(stderrChunks, stderrState, chunk, outputPreviewLimit);
  });
  const closed = await new Promise((resolveClose) => {
    let spawnError = null;
    child.once('error', (error) => { spawnError = String(error?.message ?? error); });
    child.once('close', (code, signal) => resolveClose({
      exitCode: spawnError ? null : code,
      signal,
      ...(spawnError ? { error: spawnError } : {}),
    }));
    if (Number.isFinite(timeoutMs) && timeoutMs > 0) {
      timeoutHandle = setTimeout(() => {
        timedOut = true;
        signalOwnedProcessTree(child, 'SIGTERM');
        killEscalation = new Promise((resolveKill) => { finishKillEscalation = resolveKill; });
        killHandle = setTimeout(() => {
          signalOwnedProcessTree(child, 'SIGKILL');
          finishKillEscalation?.();
        }, 1000);
      }, timeoutMs);
    }
  });
  clearTimeout(timeoutHandle);
  if (killHandle) {
    if (ownedProcessGroupExists(child)) await killEscalation;
    else {
      clearTimeout(killHandle);
      finishKillEscalation?.();
    }
  }
  clearTimeout(killHandle);
  await Promise.allSettled([finished(stdoutFile), finished(stderrFile)]);
  return {
    ...closed,
    durationMs: performance.now() - started,
    stdoutText: Buffer.concat(stdoutChunks).toString('utf8'),
    stderrText: Buffer.concat(stderrChunks).toString('utf8'),
    stdoutTruncated: stdoutState.totalBytes > outputPreviewLimit,
    stderrTruncated: stderrState.totalBytes > outputPreviewLimit,
    timedOut,
  };
}

function saveInjectedLogs(result, stdoutPath, stderrPath) {
  if (!existsSync(stdoutPath)) writeFileSync(stdoutPath, String(result.stdoutText ?? result.stdout ?? ''), { flag: 'wx', mode: 0o600 });
  if (!existsSync(stderrPath)) writeFileSync(stderrPath, String(result.stderrText ?? result.stderr ?? ''), { flag: 'wx', mode: 0o600 });
}

function parseListedSelection(stdout, expectedSpecPath) {
  const lines = String(stdout ?? '').split(/\r?\n/);
  const totalLine = lines.find((line) => /^\s*Total:\s*/.test(line));
  const totalMatch = totalLine?.match(/Total:\s*(\d+)\s+tests?\s+in\s+(\d+)\s+files?/i);
  const caseLines = lines.map((line) => {
    const match = line.match(/^\s+([^:\s]+\.spec\.mjs):(\d+):(\d+)\s+›\s+(.+?)\s*$/);
    if (!match) return null;
    const titlePath = match[4];
    return {
      file: match[1],
      line: Number(match[2]),
      column: Number(match[3]),
      titlePath,
      title: titlePath.split(' › ').at(-1),
    };
  }).filter(Boolean);
  const expectedFile = basename(expectedSpecPath);
  return {
    total: totalMatch ? Number(totalMatch[1]) : null,
    listedFileCount: totalMatch ? Number(totalMatch[2]) : null,
    cases: caseLines,
    exact: totalMatch !== null
      && Number(totalMatch[1]) === 1
      && Number(totalMatch[2]) === 1
      && caseLines.length === 1
      && caseLines[0].file === expectedFile,
    reason: !totalMatch
      ? 'Official Playwright --list output had no parseable Total line.'
      : Number(totalMatch[1]) !== 1 || Number(totalMatch[2]) !== 1 || caseLines.length !== 1
        ? 'Official Playwright --list must select exactly one test in one file.'
        : caseLines[0].file !== expectedFile
          ? 'Official Playwright --list selected a test outside the registered spec.'
          : null,
  };
}

function parseCli(argv) {
  const parsed = parseArgs({
    args: argv,
    options: {
      scenario: { type: 'string' },
      case: { type: 'string' },
      target: { type: 'string' },
      'target-from-environment': { type: 'boolean' },
      grep: { type: 'string' },
      'checks-only': { type: 'boolean' },
      'evidence-parent': { type: 'string' },
      help: { type: 'boolean', short: 'h' },
    },
    allowPositionals: false,
    strict: true,
  });
  if (parsed.values.help) return { help: true };
  assert(parsed.values.scenario?.trim(), 'Provide --scenario.');
  assert(parsed.values.case?.trim(), 'Provide --case.');
  const checksOnly = parsed.values['checks-only'] === true;
  const targetPath = parsed.values.target?.trim();
  const targetFromEnvironment = parsed.values['target-from-environment'] === true;
  if (checksOnly) {
    assert(!targetPath && !targetFromEnvironment, '--checks-only does not accept target options.');
  } else {
    assert(Boolean(targetPath) !== targetFromEnvironment,
      'Choose exactly one of --target <config-path> or --target-from-environment.');
  }
  return {
    scenarioID: parsed.values.scenario.trim(),
    caseName: parsed.values.case.trim(),
    targetPath,
    targetFromEnvironment,
    grep: parsed.values.grep?.trim() || undefined,
    checksOnly,
    evidenceParent: parsed.values['evidence-parent'],
  };
}

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

function focusedFilesForGroup(group) {
  const testFiles = group.command[0] === 'node-test' ? group.command.slice(1) : group.command.slice(4);
  return [...testFiles, ...(group.sourceFiles ?? [])];
}

function sha256File(path) {
  if (!existsSync(path) || !statSync(path).isFile()) return null;
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function boundedText(value, limit = 4000) {
  const text = String(value ?? '').replace(/\u0000/g, '\\0');
  return text.length > limit ? text.slice(0, limit) + '\n[truncated]' : text;
}

function focusedPlansForContract(contract, root) {
  const groups = Array.isArray(contract.focusedChecks) ? contract.focusedChecks : [];
  const plans = planFocusedCheckGroups(groups, root);
  return plans.map((plan, index) => {
    const files = focusedFilesForGroup(groups[index]);
    const inputs = files.map((file) => {
      const absolute = resolve(root, file);
      assert(isWithin(root, absolute), 'Focused check input must remain inside the repository: ' + file);
      assert(existsSync(absolute) && statSync(absolute).isFile(), 'Focused check input is missing: ' + file);
      return { path: file, sha256: sha256File(absolute) };
    });
    const declaredInputsHash = createHash('sha256').update(JSON.stringify(inputs)).digest('hex');
    return { ...plan, inputs, declaredInputsHash };
  });
}

function focusedCheckFailureReason(group) {
  if (group.timedOut) return 'Focused check ' + group.id + ' timed out after ' + group.timeoutMs + 'ms.';
  const lines = [group.stderrPreview, group.stdoutPreview]
    .filter(Boolean)
    .join('\n')
    .split(/\r?\n/)
    .filter((line) => line.trim());
  const failureLine = lines.find((line) => /^\s*FAIL\s+\S+.*\s>\s/.test(line))
    ?? lines.find((line) => /^\s*not ok\s+\d+\s*-/.test(line))
    ?? lines.find((line) => /\b(?:AssertionError|Error:|Expected:|Received:)\b/i.test(line))
    ?? lines.find((line) => /\b(?:FAIL|FAILED)\b/i.test(line));
  const firstLine = failureLine ?? lines[0];
  return firstLine
    ? 'Focused check ' + group.id + ' exited ' + (group.exitCode ?? 'unknown') + ': ' + diagnosticLine(firstLine)
    : 'Focused check ' + group.id + ' exited ' + (group.exitCode ?? 'unknown') + '.';
}

function collectSpecs(suites, result = []) {
  for (const suite of suites ?? []) {
    for (const spec of suite.specs ?? []) {
      result.push({ ...spec, file: spec.file ?? suite.file });
    }
    collectSpecs(suite.suites, result);
  }
  return result;
}

function selectedOfficialTest(playwrightReport, selection) {
  if (!playwrightReport || !selection?.cases?.length) return null;
  const listed = selection.cases[0];
  const matching = collectSpecs(playwrightReport.suites).filter((spec) =>
    basename(spec.file ?? '') === listed.file
    && spec.line === listed.line
    && spec.column === listed.column
    && spec.title === listed.title);
  if (matching.length !== 1) return null;
  const tests = matching[0].tests ?? [];
  if (tests.length !== 1) return null;
  return { spec: matching[0], test: tests[0] };
}

function testPassed(test) {
  if (!test) return false;
  const identities = [test.status, test.outcome].filter((value) => value !== undefined);
  if (identities.length && identities.some((value) => value !== 'expected')) return false;
  const results = Array.isArray(test.results) ? test.results : [];
  if (results.length !== 1) return false;
  if (results[0]?.status !== 'passed' || results[0]?.retry !== 0) return false;
  return identities.includes('expected') || results.length === 1;
}

function selectedPlaywrightFailureReason(test) {
  for (const result of Array.isArray(test?.results) ? test.results : []) {
    if (!['failed', 'timedOut', 'interrupted'].includes(result?.status)) continue;
    const messages = [
      result.error?.message,
      ...(Array.isArray(result.errors) ? result.errors.map((error) => error?.message) : []),
    ];
    for (const message of messages) {
      const reason = diagnosticLine(message);
      if (reason) return reason;
    }
  }
  return null;
}

function compactDimensions(dimensions) {
  const compact = {};
  for (const name of lifecycleDimensionNames) {
    const value = dimensions?.[name];
    compact[name] = value && typeof value === 'object' && !Array.isArray(value)
      ? value.status ?? 'unknown'
      : value ?? 'unknown';
  }
  return compact;
}

function attachmentReports(selectionRecord, playwrightReportPath, root) {
  const candidates = [];
  const acceptedNames = new Set(['cda-domain-report.json', 'loom-verification-report.json', 'cda-report.json']);
  const add = (data, path, name) => {
    if (!data || typeof data !== 'object' || Array.isArray(data)) return;
    const key = path ? 'path:' + path : 'json:' + JSON.stringify(data);
    if (!candidates.some((candidate) => candidate.key === key)) candidates.push({ key, data, path, name });
  };
  const matched = selectedOfficialTest(readJson(playwrightReportPath), selectionRecord);
  for (const result of matched?.test?.results ?? []) {
    for (const attachment of result.attachments ?? []) {
      if (!acceptedNames.has(attachment.name)) continue;
      if (typeof attachment.path === 'string') {
        const path = resolve(root, attachment.path);
        if (existsSync(path) && statSync(path).isFile()) {
          try { add(readJson(path), path, attachment.name); } catch { /* Invalid report is summarized as unavailable. */ }
        } else if (typeof attachment.body === 'string') {
          try {
            const content = Buffer.from(attachment.body, 'base64').toString('utf8');
            add(JSON.parse(content), null, attachment.name);
          } catch { /* Invalid attachment is summarized as unavailable. */ }
        }
      } else if (typeof attachment.body === 'string') {
        try {
          const content = Buffer.from(attachment.body, 'base64').toString('utf8');
          add(JSON.parse(content), null, attachment.name);
        } catch { /* Invalid attachment is summarized as unavailable. */ }
      }
    }
  }
  return { matchedTest: matched, candidates };
}

function reportIdentity(report) {
  const scenario = report?.scenario ?? report?.scenarioID;
  const caseValue = report?.case ?? report?.caseName;
  const scenarioConflict = report?.scenario !== undefined
    && report?.scenarioID !== undefined
    && report.scenario !== report.scenarioID;
  const caseConflict = report?.case !== undefined
    && report?.caseName !== undefined
    && report.case !== report.caseName;
  if (scenarioConflict || caseConflict) return { scenario, caseName: caseValue, valid: false };
  return { scenario, caseName: caseValue, valid: true };
}

function diagnosticLine(value) {
  if (typeof value !== 'string' || !value.trim()) return null;
  const firstLine = value.split(/\r?\n/).find((line) => line.trim() && !/^\s*at\s/.test(line));
  if (!firstLine) return null;
  const timeout = firstLine.match(/Timeout\s+(\d+)\s*ms\s+exceeded\.?/i);
  if (timeout) return 'Timeout ' + timeout[1] + 'ms exceeded.';
  if (/["']?(?:authorization|auth|access[_-]?token|refresh[_-]?token|token|query|body)["']?\s*[:=]/i.test(firstLine)) {
    return 'Failure details contained sensitive values.';
  }
  return firstLine.trim()
    .replace(/\b(?:Bearer|Basic)\s+[^\s,;]+/gi, '[redacted credentials]')
    .replace(/\b(path|file|source)\s*[:=]\s*\/[^\s,;]+/gi, '$1=[redacted path]')
    .replace(/https?:\/\/[^\s)]+/gi, '[redacted URL]')
    .replace(/\?[^\s)]+/g, '?[redacted query]')
    .replace(/\bcc[0-9]\.\S+/gi, '[redacted token]')
    .replace(/\$[A-Z_][A-Z0-9_]*\/[^\s)]+/g, '[redacted path]')
    .replace(/(?:\/[\w.-]+){2,}/g, '[redacted path]')
    .replace(/\b(?:[\w.-]+\/)+[\w.-]+/g, '[redacted path]')
    .replace(/[A-Za-z]:\\(?:[^\\\s]+\\?)+/g, '[redacted path]')
    .slice(0, 240);
}

function actionLabel(action) {
  if (typeof action === 'string') return diagnosticLine(action);
  if (!action || typeof action !== 'object' || Array.isArray(action)) return null;
  for (const key of ['label', 'name', 'title', 'description']) {
    const label = diagnosticLine(action[key]);
    if (label) return label;
  }
  return null;
}

function summarizeFailedAction(report) {
  const evidenceAction = report?.failureEvidence?.action;
  const evidenceLabel = actionLabel(evidenceAction);
  if (evidenceLabel) {
    const evidenceStatus = String(evidenceAction?.status ?? '').toLowerCase();
    return {
      label: evidenceLabel,
      status: ['failed', 'running', 'current'].includes(evidenceStatus) ? evidenceStatus : 'failed',
    };
  }
  const failed = (Array.isArray(report?.actions) ? report.actions : [])
    .filter((action) => ['failed', 'running', 'current'].includes(String(action?.status ?? '').toLowerCase()))
    .at(-1);
  const label = actionLabel(failed);
  return label ? { label, status: String(failed.status).toLowerCase() } : null;
}

function summarizeLastCompletedAction(report) {
  const completed = (Array.isArray(report?.actions) ? report.actions : [])
    .filter((action) => ['passed', 'completed', 'complete', 'succeeded', 'success'].includes(String(action?.status ?? '').toLowerCase()))
    .at(-1);
  const label = actionLabel(completed);
  return label ? { label, status: String(completed.status).toLowerCase() } : null;
}

function endpointForNativeRequest(request, report, scenario) {
  if (!request || typeof request !== 'object' || Array.isArray(request)) return null;
  const method = typeof request.method === 'string' ? request.method.toUpperCase() : '';
  if (!method) return null;
  const origin = typeof request.origin === 'string' ? request.origin : report?.target?.uiUrl;
  let requestUrl;
  let uiOrigin;
  try {
    requestUrl = new URL(request.path ?? request.url, origin);
    uiOrigin = new URL(report?.target?.uiUrl).origin;
  } catch {
    return null;
  }
  if (requestUrl.origin !== uiOrigin) return null;
  const pathSegments = requestUrl.pathname.split('/').filter(Boolean);
  const projectIndex = pathSegments.indexOf('projects');
  const explorerIndex = pathSegments.indexOf('explorers');
  if (projectIndex < 0 || explorerIndex !== projectIndex + 2) return null;
  const project = report?.target?.project;
  const explorer = report?.target?.explorer;
  if (typeof project !== 'string' || typeof explorer !== 'string'
    || pathSegments[projectIndex + 1] !== project
    || pathSegments[explorerIndex + 1] !== explorer) return null;

  for (const endpoint of scenario?.endpoints ?? []) {
    const [registeredMethod, template] = String(endpoint).split(/\s+/, 2);
    if (registeredMethod !== method || !template) continue;
    const templateSegments = template.split('/').filter(Boolean);
    if (templateSegments.length !== pathSegments.length) continue;
    const matches = templateSegments.every((segment, index) =>
      /^\{[^/{}]+\}$/.test(segment) || segment === pathSegments[index]);
    if (matches) return method + ' ' + template;
  }
  return null;
}

function pendingRequestStatus(request) {
  const status = request?.status;
  if (typeof status === 'string' && status.trim()) {
    return ['pending', 'started', 'running', 'in-progress', 'in_progress'].includes(status.trim().toLowerCase())
      ? 'pending'
      : null;
  }
  if (status !== undefined && status !== null) return null;
  return request?.completedAt || request?.endedAt || request?.finishedAt ? null : 'pending';
}

function summarizePendingOwnedRequests(report, scenario) {
  const requests = [];
  for (const request of Array.isArray(report?.nativeRequests) ? report.nativeRequests : []) {
    const status = pendingRequestStatus(request);
    if (!status) continue;
    const endpoint = endpointForNativeRequest(request, report, scenario);
    const requestID = request?.requestID ?? request?.requestId;
    if (!endpoint || typeof requestID !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(requestID)) continue;
    requests.push({ endpoint, requestID, status });
  }
  return requests;
}

function summarizeFirstFailureReason(report, workflowFailure, selectedTest) {
  const failedReportAction = (Array.isArray(report?.actions) ? report.actions : [])
    .filter((action) => ['failed', 'running', 'current'].includes(String(action?.status ?? '').toLowerCase()))
    .at(-1);
  const evidenceAction = report?.failureEvidence?.action;
  const candidates = [
    report?.failureEvidence?.reason,
    report?.failureEvidence?.error,
    report?.failureEvidence?.message,
    evidenceAction?.error,
    evidenceAction?.message,
    failedReportAction?.error,
    failedReportAction?.message,
    workflowFailure,
    report ? null : selectedPlaywrightFailureReason(selectedTest),
  ];
  for (const candidate of candidates) {
    const reason = diagnosticLine(candidate);
    if (reason) return reason;
  }
  return null;
}

function summarizeFailureContext(report) {
  const context = report?.failureEvidence?.locator?.context;
  if (!context || typeof context !== 'object' || Array.isArray(context)) return null;
  const containers = (Array.isArray(context.containers) ? context.containers : [])
    .slice(0, 3)
    .map((container) => ({
      ...(diagnosticLine(container?.kind) ? { kind: diagnosticLine(container.kind) } : {}),
      ...(diagnosticLine(container?.text) ? { text: diagnosticLine(container.text) } : {}),
    }))
    .filter((container) => Object.keys(container).length);
  const alerts = (Array.isArray(context.alerts) ? context.alerts : [])
    .slice(0, 3)
    .map(diagnosticLine)
    .filter(Boolean);
  const state = diagnosticLine(context.state);
  if (!state && containers.length === 0 && alerts.length === 0) return null;
  return {
    ...(state ? { state } : {}),
    containers,
    alerts,
  };
}

export function summarizeRenderCheckpoints(report, registeredChecks) {
  const registered = new Set(Array.isArray(registeredChecks) ? registeredChecks : []);
  const checkpoints = [];
  for (const assertion of Array.isArray(report?.assertions) ? report.assertions : []) {
    if (!registered.has(assertion?.name)) continue;
    if (!/within five seconds|within budget|action-to-render/i.test(assertion.name)) continue;
    const evidence = assertion.evidence;
    if (Array.isArray(evidence?.actions)) {
      for (const action of evidence.actions) {
        if (!Number.isFinite(action?.durationMs) || action.durationMs < 0) continue;
        checkpoints.push({
          checkName: assertion.name,
          name: typeof action.name === 'string' ? action.name : null,
          durationMs: action.durationMs,
          evidencePath: 'assertions[].evidence.actions[].durationMs',
        });
      }
    } else if (Number.isFinite(evidence?.elapsedMs ?? evidence?.durationMs)
      && (evidence.elapsedMs ?? evidence.durationMs) >= 0) {
      const durationMs = evidence.elapsedMs ?? evidence.durationMs;
      checkpoints.push({
        checkName: assertion.name,
        name: assertion.name,
        durationMs,
        evidencePath: Number.isFinite(evidence.elapsedMs)
          ? 'assertions[].evidence.elapsedMs'
          : 'assertions[].evidence.durationMs',
      });
    }
  }
  return {
    count: checkpoints.length,
    maximumDurationMs: checkpoints.length ? Math.max(...checkpoints.map((checkpoint) => checkpoint.durationMs)) : null,
    checkpoints,
  };
}

function summarizeLifecycle(report, expectedScenarioID, expectedCaseName) {
  if (!report) return { status: 'unverified', reason: 'No domain verification report was attached or found.' };
  const identity = reportIdentity(report);
  if (!identity.valid || identity.scenario !== expectedScenarioID || identity.caseName !== expectedCaseName) {
    return {
      status: 'unverified',
      reason: 'Domain report scenario/case does not match the selected registry case.',
      reportIdentity: { scenario: identity.scenario ?? null, caseName: identity.caseName ?? null },
    };
  }

  const custom = report.target?.kind === 'read-only-custom';
  let requiredChecks;
  try {
    requiredChecks = scenarioCaseFor(expectedScenarioID, expectedCaseName, custom).requiredChecks;
  } catch (error) {
    return { status: 'unverified', reason: 'Could not resolve registered required checks: ' + String(error?.message ?? error) };
  }
  const assertions = Array.isArray(report.assertions) ? report.assertions : [];
  const reportedChecks = report.requiredChecks;
  const reportedNames = Array.isArray(reportedChecks) ? reportedChecks : null;
  const passedNames = requiredChecks.filter((name) => assertions.some((assertion) => assertion?.name === name && assertion?.status === 'passed'));
  const failedNames = requiredChecks.filter((name) => assertions.some((assertion) => assertion?.name === name && assertion?.status === 'failed'));
  const missingNames = requiredChecks.filter((name) => !assertions.some((assertion) => assertion?.name === name && ['passed', 'failed'].includes(assertion?.status)));
  const exactCheckList = reportedNames !== null
    && reportedNames.length === requiredChecks.length
    && reportedNames.every((name, index) => name === requiredChecks[index])
    && new Set(reportedNames).size === requiredChecks.length;
  const evidenceStatus = classifyEvidence(report, requiredChecks);
  const missingReported = Array.isArray(report.missingRequiredChecks) ? report.missingRequiredChecks : null;
  const passed = report.status === 'passed'
    && evidenceStatus === 'passed'
    && exactCheckList
    && missingReported !== null
    && missingReported.length === 0
    && passedNames.length === requiredChecks.length
    && failedNames.length === 0
    && missingNames.length === 0;
  const actionDurations = (Array.isArray(report.actions) ? report.actions : [])
    .map((action) => action?.elapsedMs)
    .filter((value) => Number.isFinite(value) && value >= 0);
  const renderCheckpoints = summarizeRenderCheckpoints(report, requiredChecks);
  return {
    status: passed ? 'passed' : report.status === 'failed' || evidenceStatus === 'failed' ? 'failed' : 'unverified',
    reportStatus: report.status ?? null,
    evidenceStatus,
    reportIdentity: { scenario: identity.scenario, caseName: identity.caseName },
    requiredCheckCount: requiredChecks.length,
    passedCheckCount: passedNames.length,
    failedCheckNames: failedNames,
    missingCheckNames: missingNames,
    requiredCheckListMatchesRegistry: exactCheckList,
    missingRequiredCheckCount: missingReported?.length ?? null,
    dimensions: compactDimensions(report.dimensions),
    actionCount: Array.isArray(report.actions) ? report.actions.length : null,
    maximumMeasuredActionLatencyMs: actionDurations.length ? Math.max(...actionDurations) : null,
    renderCheckpointCount: renderCheckpoints.count,
    maximumRenderCheckpointLatencyMs: renderCheckpoints.maximumDurationMs,
    renderCheckpoints: renderCheckpoints.checkpoints,
  };
}

function fingerprintCaptureIssue(value, expectedPhase, expectedRoot) {
  if (!value) return 'Capture is missing.';
  if (value.phase !== expectedPhase || value.root !== expectedRoot) return 'Capture phase or source root does not match.';
  const fingerprint = value.fingerprint;
  const manifest = value.manifest;
  if (!/^[a-f0-9]{64}$/i.test(fingerprint?.sha256 ?? '')
    || !Number.isInteger(fingerprint?.files)
    || fingerprint.files < 0
    || manifest === null
    || typeof manifest !== 'object'
    || Array.isArray(manifest)) return 'Fingerprint or manifest shape is invalid.';
  const hashes = Object.values(manifest);
  if (hashes.length !== fingerprint.files || hashes.some((hash) => !/^[a-f0-9]{64}$/i.test(hash))) {
    return 'Fingerprint file count or manifest hashes are invalid.';
  }
  return null;
}

function healthDimension(value, expectedIdentity) {
  if (!value) return { status: 'UNVERIFIED', reason: 'Health capture is missing.' };
  if (value.status !== 'PASS') return { status: 'FAIL', sampleCount: value.samples?.length ?? null };
  if (!Array.isArray(value.samples) || value.samples.length !== 3) {
    return { status: 'UNVERIFIED', reason: 'Health capture does not contain exactly three samples.', sampleCount: value.samples?.length ?? null };
  }
  let expected;
  let captured;
  try {
    expected = parseCapturedBuildIdentity(expectedIdentity);
    captured = parseCapturedBuildIdentity(value.apiBuildIdentity);
  } catch (error) {
    return { status: 'UNVERIFIED', reason: String(error?.message ?? error), sampleCount: value.samples.length };
  }
  if (captured !== expected) return { status: 'FAIL', reason: 'Health API identity does not match the captured API.', sampleCount: value.samples.length };
  for (const sample of value.samples) {
    let sampleIdentity;
    try { sampleIdentity = parseCapturedBuildIdentity(sample?.apiBuildIdentity); }
    catch (error) { return { status: 'UNVERIFIED', reason: String(error?.message ?? error), sampleCount: value.samples.length }; }
    if (sample.apiStatus !== 200 || sample.uiStatus !== 200 || sample.uiHasDocument !== true || sampleIdentity !== expected) {
      return { status: 'FAIL', reason: 'A health sample does not match the captured healthy API/UI identity.', sampleCount: value.samples.length };
    }
  }
  return { status: 'PASS', sampleCount: value.samples.length, apiBuildIdentity: captured };
}

function compareCaptureArtifacts({ before, after, precheck, healthBefore, healthAfter, expectedRoot }) {
  const dimensions = {};
  const compareFingerprint = (name, beforeValue, afterValue) => {
    const beforeIssue = fingerprintCaptureIssue(beforeValue, 'before', expectedRoot);
    const afterIssue = fingerprintCaptureIssue(afterValue, 'after', expectedRoot);
    if (beforeIssue || afterIssue) {
      dimensions[name] = {
        status: 'UNVERIFIED',
        reason: [beforeIssue && `before: ${beforeIssue}`, afterIssue && `after: ${afterIssue}`].filter(Boolean).join(' '),
      };
      return;
    }
    const changedPaths = sourceFingerprintChangedPaths(beforeValue.manifest, afterValue.manifest);
    const equal = beforeValue.fingerprint.sha256 === afterValue.fingerprint.sha256
      && beforeValue.fingerprint.files === afterValue.fingerprint.files
      && changedPaths.length === 0;
    dimensions[name] = {
      status: equal ? 'PASS' : 'FAIL',
      before: beforeValue.fingerprint,
      after: afterValue.fingerprint,
      changedPaths,
    };
  };
  compareFingerprint('source', before.source, after.source);
  compareFingerprint('docs', before.docs, after.docs);

  const beforeApi = before.api;
  const afterApi = after.api;
  if (!beforeApi || !afterApi) {
    dimensions.apiBuildIdentity = { status: 'UNVERIFIED', reason: 'Before or after API capture is missing.' };
  } else if (beforeApi.phase !== 'before' || afterApi.phase !== 'after'
    || beforeApi.target?.sourceRoot !== expectedRoot || afterApi.target?.sourceRoot !== expectedRoot) {
    dimensions.apiBuildIdentity = { status: 'UNVERIFIED', reason: 'API capture phase or source root does not match.' };
  } else {
    let beforeIdentity;
    let afterIdentity;
    let precheckIdentity;
    try {
      beforeIdentity = parseCapturedBuildIdentity(beforeApi.apiBuildIdentity);
      afterIdentity = parseCapturedBuildIdentity(afterApi.apiBuildIdentity);
      precheckIdentity = parseCapturedBuildIdentity(precheck?.apiBuildIdentity);
    } catch (error) {
      dimensions.apiBuildIdentity = { status: 'UNVERIFIED', reason: String(error?.message ?? error) };
    }
    if (beforeIdentity && afterIdentity && precheckIdentity) {
      const precheckMatches = precheck?.exitCode === 0
        && precheck?.fresh === true
        && precheck?.sourceDigestMatchesCurrentMountedSource === true
        && precheck?.runningBinaryMatchesRecordedBuild === true
        && precheckIdentity === beforeIdentity;
      let targetUnchanged = true;
      try { assertCapturedTargetMatches(beforeApi.target, afterApi.target); }
      catch { targetUnchanged = false; }
      dimensions.apiBuildIdentity = {
        status: beforeIdentity === afterIdentity && targetUnchanged && precheckMatches ? 'PASS' : 'FAIL',
        before: beforeIdentity,
        after: afterIdentity,
        precheckMatches,
        targetUnchanged,
      };
    }
  }

  const beforeMounts = before.mounts;
  const afterMounts = after.mounts;
  if (!beforeMounts || !afterMounts || !beforeApi || !afterApi) {
    dimensions.ownedMounts = { status: 'UNVERIFIED', reason: 'Before or after owned-mount/API capture is missing.' };
  } else if (beforeMounts.phase !== 'before' || afterMounts.phase !== 'after'
    || beforeMounts.target?.sourceRoot !== expectedRoot || afterMounts.target?.sourceRoot !== expectedRoot) {
    dimensions.ownedMounts = { status: 'UNVERIFIED', reason: 'Mount capture phase or source root does not match.' };
  } else {
    try {
      assert.equal(beforeMounts.status, 'PASS');
      assert.equal(afterMounts.status, 'PASS');
      assertCapturedTargetMatches(beforeApi.target, beforeMounts.target);
      assertCapturedTargetMatches(afterApi.target, afterMounts.target);
      assertCapturedTargetMatches(beforeMounts.target, afterMounts.target);
      dimensions.ownedMounts = { status: 'PASS', targetUnchanged: true, target: afterMounts.target };
    } catch (error) {
      dimensions.ownedMounts = { status: 'FAIL', reason: String(error?.message ?? error) };
    }
  }

  const expectedIdentity = beforeApi?.apiBuildIdentity;
  dimensions.healthBefore = healthDimension(healthBefore, expectedIdentity);
  dimensions.healthAfter = healthDimension(healthAfter, expectedIdentity);
  const statuses = Object.values(dimensions).map((dimension) => dimension.status);
  return {
    status: statuses.every((status) => status === 'PASS') ? 'PASS'
      : statuses.some((status) => status === 'FAIL') ? 'FAIL' : 'UNVERIFIED',
    dimensions,
  };
}

function loadJsonIfPresent(path) {
  try { return readJson(path); } catch { return null; }
}

function stageSuccess(stage, name) {
  if (stage?.timedOut) throw new Error(name + ' stage timed out after ' + stage.timeoutMs + 'ms.');
  if (!stage || stage.exitCode !== 0) throw new Error(name + ' stage did not exit successfully (exit=' + (stage?.exitCode ?? 'unknown') + ').');
}

function expectedCommandPaths(runDirectory) {
  const playwrightOutputPath = join(runDirectory, 'playwright-results');
  return {
    precheck: join(runDirectory, 'api-build-precheck.json'),
    sourceBefore: join(runDirectory, 'source-before.json'),
    docsBefore: join(runDirectory, 'docs-before.json'),
    apiBefore: join(runDirectory, 'api-identity-before.json'),
    mountsBefore: join(runDirectory, 'owned-mounts-before.json'),
    healthBefore: join(runDirectory, 'health-before.json'),
    sourceAfter: join(runDirectory, 'source-after.json'),
    docsAfter: join(runDirectory, 'docs-after.json'),
    apiAfter: join(runDirectory, 'api-identity-after.json'),
    mountsAfter: join(runDirectory, 'owned-mounts-after.json'),
    healthAfter: join(runDirectory, 'health-after.json'),
    playwrightOutputPath,
    playwrightReportPath: join(runDirectory, 'playwright-report.json'),
  };
}

export async function runNativeVerificationBracket({
  scenarioID,
  caseName,
  grep,
  targetPath,
  checksOnly = false,
  targetValidation = null,
  evidenceParent,
  root = repositoryRoot,
  env = process.env,
  commandRunner = runProcess,
} = {}) {
  assert(typeof scenarioID === 'string' && scenarioID.trim(), 'Provide --scenario.');
  assert(typeof caseName === 'string' && caseName.trim(), 'Provide --case.');
  const canonicalRoot = realpathSync(resolve(root));
  const scenario = registry.find((candidate) => candidate.id === scenarioID);
  assert(scenario, 'Unknown registered scenario: ' + scenarioID);
  const contract = scenarioCaseFor(scenario, caseName);
  const focusedPlans = focusedPlansForContract(contract, canonicalRoot);
  const specPath = contract.playwrightTest;
  const specAbsolute = resolve(canonicalRoot, specPath);
  assert(isWithin(canonicalRoot, specAbsolute), 'Registered Playwright spec must be inside the repository root.');
  assert(existsSync(specAbsolute), 'Registered Playwright spec is missing: ' + specPath);
  const resolvedGrep = typeof grep === 'string' && grep.trim() ? grep.trim() : contract.playwrightGrep;
  if (!checksOnly) assert(typeof resolvedGrep === 'string' && resolvedGrep.trim(),
    'Case has no registered default Playwright selection; provide --grep.');

  const runDirectory = createEvidenceDirectory(canonicalRoot, evidenceParent, scenarioID, caseName);
  const paths = expectedCommandPaths(runDirectory);
  const startedAt = new Date().toISOString();
  const started = performance.now();
  const summary = {
    schemaVersion: 1,
    status: 'unverified',
    startedAt,
    scenario: scenarioID,
    case: caseName,
    nativeSpec: specPath,
    explicitGrep: grep ?? null,
    resolvedGrep: resolvedGrep ?? null,
    mode: checksOnly ? 'checks-only' : 'browser',
    targetIdentity: contract.expectedIdentity ?? null,
    targetConfigPath: targetPath ?? null,
    targetValidation: targetValidation ?? (checksOnly ? null : {
      scope: 'environment-only',
      runtimeDatasetIdentity: 'not-checked',
    }),
    runDirectory,
    commands: {},
    evidence: {
      summary: join(runDirectory, 'summary.json'),
      playwrightReport: paths.playwrightReportPath,
      domainReport: null,
      sourceBefore: paths.sourceBefore,
      docsBefore: paths.docsBefore,
      apiBefore: paths.apiBefore,
      mountsBefore: paths.mountsBefore,
      sourceAfter: paths.sourceAfter,
      docsAfter: paths.docsAfter,
      apiAfter: paths.apiAfter,
      mountsAfter: paths.mountsAfter,
      precheck: paths.precheck,
      healthBefore: paths.healthBefore,
      healthAfter: paths.healthAfter,
    },
    selection: null,
    focusedCheckCoverage: focusedPlans.length ? 'registered' : 'browser-only',
    focusedChecks: { status: focusedPlans.length ? 'pending' : 'browser-only', groups: [] },
    integrity: { status: 'UNVERIFIED', dimensions: {} },
    lifecycle: { status: 'unverified', reason: 'Browser case has not produced a domain report.' },
    failureCategory: null,
    notes: [],
  };
  const logsDirectory = join(runDirectory, 'logs');
  mkdirSync(logsDirectory, { recursive: true, mode: 0o700 });
  let shouldCloseBracket = false;
  let browserAttempted = false;
  let beforeArtifacts = {};
  let afterArtifacts = {};
  let precheckRecord = null;
  let healthBefore = null;
  let healthAfter = null;
  let selection = null;
  let browserStage = null;
  let domainReportPath = null;
  let workflowFailure = null;
  let baseEnv = { ...env };
  const exec = async (name, args, overrides = {}, { cwd = canonicalRoot, timeoutMs = PREPARATION_STAGE_TIMEOUT_MS,
    outputPreviewLimit = 1024 * 1024 } = {}) => {
    const stdoutPath = join(logsDirectory, name + '.stdout.log');
    const stderrPath = join(logsDirectory, name + '.stderr.log');
    const runStarted = performance.now();
    let result;
    try {
      result = await commandRunner(process.execPath, args, {
        cwd,
        env: { ...baseEnv, ...overrides },
        stdoutPath,
        stderrPath,
        timeoutMs,
        outputPreviewLimit,
      });
    } catch (error) {
      result = { exitCode: null, error: String(error?.message ?? error) };
    }
    saveInjectedLogs(result ?? {}, stdoutPath, stderrPath);
    const stage = {
      executable: process.execPath,
      arguments: args,
      cwd,
      timeoutMs,
      ...(Object.keys(overrides).length ? { environmentOverrides: overrides } : {}),
      exitCode: Number.isInteger(result?.exitCode) ? result.exitCode : null,
      ...(result?.signal ? { signal: result.signal } : {}),
      ...(result?.timedOut ? { timedOut: true } : {}),
      ...(result?.stdoutTruncated ? { stdoutTruncated: true } : {}),
      ...(result?.stderrTruncated ? { stderrTruncated: true } : {}),
      durationMs: Math.round(Number.isFinite(result?.durationMs) ? result.durationMs : performance.now() - runStarted),
      stdoutPath,
      stderrPath,
      ...(result?.error ? { spawnError: String(result.error).slice(0, 400) } : {}),
    };
    summary.commands[name] = stage;
    return {
      ...result,
      exitCode: stage.exitCode,
      timeoutMs: stage.timeoutMs,
      timedOut: Boolean(stage.timedOut),
      stdoutText: String(result?.stdoutText ?? result?.stdout ?? ''),
      stderrText: String(result?.stderrText ?? result?.stderr ?? ''),
    };
  };
  const captureArgs = (phase) => [
    'scripts/capture-owned-verification.mjs', '--phase', phase,
    ...(phase === 'before' ? ['--precheck-input', paths.precheck] : []),
    '--source-output', phase === 'before' ? paths.sourceBefore : paths.sourceAfter,
    '--docs-output', phase === 'before' ? paths.docsBefore : paths.docsAfter,
    '--api-output', phase === 'before' ? paths.apiBefore : paths.apiAfter,
    '--mount-output', phase === 'before' ? paths.mountsBefore : paths.mountsAfter,
  ];
  const runAfterClosure = async () => {
    let captureStage;
    try {
      captureStage = await exec('captureAfter', captureArgs('after'));
    } catch (error) {
      summary.notes.push('After capture failed: ' + String(error?.message ?? error).slice(0, 300));
    }
    let healthStage;
    try {
      healthStage = await exec('healthAfter', [
        'scripts/owned-stack-verification.mjs', '--mode', 'health',
        '--output', paths.healthAfter, '--identity', paths.apiBefore,
      ]);
    } catch (error) {
      summary.notes.push('After health failed: ' + String(error?.message ?? error).slice(0, 300));
    }
    if (captureStage?.exitCode !== 0) summary.notes.push('After capture did not exit successfully.');
    if (healthStage?.exitCode !== 0) summary.notes.push('After health did not exit successfully.');
  };

  const writeEarlySummary = (status, failureCategory, firstFailureReason = null) => {
    summary.status = status;
    summary.failureCategory = failureCategory;
    summary.finishedAt = new Date().toISOString();
    summary.durationMs = Math.round(performance.now() - started);
    summary.workflowError = firstFailureReason;
    summary.reviewPacket = {
      status,
      scenario: scenarioID,
      case: caseName,
      focusedCheckCoverage: summary.focusedCheckCoverage,
      focusedChecks: summary.focusedChecks,
      firstFailureReason,
      pendingOwnedRequests: [],
      report: null,
      playwrightJson: null,
      summary: summary.evidence.summary,
    };
    writeFileSync(summary.evidence.summary, JSON.stringify(summary, null, 2) + '\n', { mode: 0o600 });
    return summary;
  };

  if (focusedPlans.length) {
    const focusedResults = [];
    for (let offset = 0; offset < focusedPlans.length; offset += MAX_CONCURRENT_FOCUSED_CHECKS) {
      const batch = focusedPlans.slice(offset, offset + MAX_CONCURRENT_FOCUSED_CHECKS);
      const batchResults = await Promise.all(batch.map(async (plan) => {
        const stageName = 'focused-' + plan.id;
        const result = await exec(stageName, plan.args, {}, {
          cwd: plan.cwd,
          timeoutMs: FOCUSED_CHECK_TIMEOUT_MS,
          outputPreviewLimit: FOCUSED_CHECK_OUTPUT_PREVIEW_BYTES,
        });
        const stage = summary.commands[stageName];
        const stdoutPreview = boundedText(result.stdoutText, 3000);
        const stderrPreview = boundedText(result.stderrText, 3000);
        const inputsUnchanged = plan.inputs.every((input) => sha256File(resolve(canonicalRoot, input.path)) === input.sha256);
        const passed = stage.exitCode === 0 && !stage.timedOut && inputsUnchanged;
        return {
          id: plan.id,
          runner: plan.runner,
          status: passed ? 'passed' : 'failed',
          exitCode: stage.exitCode,
          durationMs: stage.durationMs,
          timeoutMs: stage.timeoutMs,
          timedOut: stage.timedOut ?? false,
          declaredInputsHash: plan.declaredInputsHash,
          inputs: plan.inputs,
          inputsUnchanged,
          evidence: { stdout: stage.stdoutPath, stderr: stage.stderrPath },
          ...(!passed && stdoutPreview ? { stdoutPreview } : {}),
          ...(!passed && stderrPreview ? { stderrPreview } : {}),
        };
      }));
      focusedResults.push(...batchResults);
    }
    summary.focusedChecks = {
      status: focusedResults.every((result) => result.status === 'passed') ? 'passed' : 'failed',
      groups: focusedResults,
    };
  }

  const failedFocusedCheck = summary.focusedChecks.groups.find((group) => group.status === 'failed');
  if (failedFocusedCheck) {
    const reason = !failedFocusedCheck.inputsUnchanged
      ? 'Focused check inputs changed while the group was running: ' + failedFocusedCheck.id + '.'
      : focusedCheckFailureReason(failedFocusedCheck);
    summary.notes.push(reason);
    return writeEarlySummary('failed', 'focused-check', reason);
  }
  if (checksOnly) {
    if (!focusedPlans.length) {
      const reason = 'This registered case is browser-only; it has no focused prerequisite group.';
      summary.notes.push(reason);
      return writeEarlySummary('browser-only', 'no-focused-checks', reason);
    }
    return writeEarlySummary('checks-passed', null);
  }

  try {
    const sourceRootEnv = env.LOOM_CDA_SOURCE_ROOT;
    assert(sourceRootEnv?.trim(), 'Set LOOM_CDA_SOURCE_ROOT in the validated owned environment.');
    assert.equal(realpathSync(resolve(sourceRootEnv)), canonicalRoot,
      'LOOM_CDA_SOURCE_ROOT must resolve to the repository containing this runner.');
    const missingEnv = requiredEnvironmentError(env);
    assert.equal(missingEnv, null, missingEnv ?? 'Owned environment validation failed.');

    const playwrightCli = join(canonicalRoot, 'scripts/node_modules/@playwright/test/cli.js');
    assert(existsSync(playwrightCli), 'Install the existing scripts workspace dependencies before running Playwright.');
    const selectionArgs = [
      'scripts/node_modules/@playwright/test/cli.js',
      'test', '--config', 'scripts/playwright.config.mjs',
      '--workers', '1', '--retries', '0',
      specPath, '--grep', resolvedGrep, '--list',
    ];
    const selectionStage = await exec('selectionList', selectionArgs);
    stageSuccess(selectionStage, 'Official Playwright --list');
    selection = parseListedSelection(selectionStage.stdoutText, specPath);
    summary.selection = selection;
    if (!selection.exact) throw new Error(selection.reason);

    shouldCloseBracket = true;
    const precheckStage = await exec('precheck', [
      'scripts/owned-stack-verification.mjs', '--mode', 'precheck', '--output', paths.precheck,
    ]);
    stageSuccess(precheckStage, 'API build precheck');
    precheckRecord = loadJsonIfPresent(paths.precheck);
    assert(precheckRecord, 'API build precheck artifact is missing or invalid.');

    const beforeCaptureStage = await exec('captureBefore', captureArgs('before'));
    stageSuccess(beforeCaptureStage, 'Before capture');
    beforeArtifacts = {
      source: loadJsonIfPresent(paths.sourceBefore),
      docs: loadJsonIfPresent(paths.docsBefore),
      api: loadJsonIfPresent(paths.apiBefore),
      mounts: loadJsonIfPresent(paths.mountsBefore),
    };
    assert(Object.values(beforeArtifacts).every(Boolean), 'Before capture did not produce all four evidence artifacts.');

    const beforeHealthStage = await exec('healthBefore', [
      'scripts/owned-stack-verification.mjs', '--mode', 'health',
      '--output', paths.healthBefore, '--identity', paths.apiBefore,
    ]);
    stageSuccess(beforeHealthStage, 'Before health');
    healthBefore = loadJsonIfPresent(paths.healthBefore);
    assert(healthBefore, 'Before health artifact is missing or invalid.');
    assert.equal(healthBefore.status, 'PASS', 'Before health did not pass.');
    assert.equal(healthBefore.samples?.length, 3, 'Before health did not record three samples.');

    browserAttempted = true;
    const playwrightEnvPath = paths.playwrightReportPath;
    browserStage = await exec('playwright', [
      'scripts/node_modules/@playwright/test/cli.js',
      'test', '--config', 'scripts/playwright.config.mjs',
      '--workers', '1', '--retries', '0',
      '--output', paths.playwrightOutputPath, '--reporter=json',
      specPath, '--grep', resolvedGrep,
    ], { PLAYWRIGHT_JSON_OUTPUT_FILE: playwrightEnvPath }, { timeoutMs: BROWSER_STAGE_TIMEOUT_MS });
    if (browserStage.exitCode !== 0) {
      summary.failureCategory = 'browser';
      summary.notes.push('Official Playwright test exited with code ' + (browserStage.exitCode ?? 'unknown') + '.');
    }
  } catch (error) {
    workflowFailure = String(error?.message ?? error);
    summary.notes.push(workflowFailure.slice(0, 500));
    if (!summary.failureCategory) summary.failureCategory = browserAttempted ? 'browser-or-harness' : 'preparation';
  } finally {
    if (shouldCloseBracket) await runAfterClosure();
  }

  afterArtifacts = {
    source: loadJsonIfPresent(paths.sourceAfter),
    docs: loadJsonIfPresent(paths.docsAfter),
    api: loadJsonIfPresent(paths.apiAfter),
    mounts: loadJsonIfPresent(paths.mountsAfter),
  };
  healthAfter = loadJsonIfPresent(paths.healthAfter);
  const allCommandsSucceeded = Object.values(summary.commands).every((command) => command.exitCode === 0);
  if (shouldCloseBracket) {
    summary.integrity = compareCaptureArtifacts({
      before: beforeArtifacts,
      after: afterArtifacts,
      precheck: precheckRecord,
      healthBefore,
      healthAfter,
      expectedRoot: canonicalRoot,
    });
  }

  const officialJson = loadJsonIfPresent(paths.playwrightReportPath);
  let attachmentData = { matchedTest: null, candidates: [] };
  if (officialJson) {
    try {
      attachmentData = attachmentReports(selection, paths.playwrightReportPath, canonicalRoot);
    } catch (error) {
      summary.notes.push('Could not inspect the official Playwright report: ' + String(error?.message ?? error).slice(0, 300));
    }
  }
  const matchedCandidates = attachmentData.candidates.filter((candidate) => {
    const identity = reportIdentity(candidate.data);
    return identity.valid && identity.scenario === scenarioID && identity.caseName === caseName;
  });
  let domainReportData = null;
  if (matchedCandidates.length === 1) {
    domainReportData = matchedCandidates[0].data;
    domainReportPath = matchedCandidates[0].path;
    summary.evidence.domainReport = domainReportPath;
    summary.lifecycle = summarizeLifecycle(domainReportData, scenarioID, caseName);
    if (summary.lifecycle.status === 'passed' && !testPassed(attachmentData.matchedTest?.test)) {
      summary.lifecycle.status = 'failed';
      summary.lifecycle.reason = 'Official Playwright result did not pass.';
    }
    if (summary.lifecycle.status === 'failed' && !summary.failureCategory) summary.failureCategory = 'lifecycle';
  } else if (matchedCandidates.length > 1) {
    summary.lifecycle = { status: 'unverified', reason: 'More than one matching domain report was found.' };
  } else if (!officialJson) {
    summary.lifecycle = { status: 'unverified', reason: 'Official Playwright JSON report is missing.' };
  } else if (!attachmentData.matchedTest) {
    summary.lifecycle = { status: 'unverified', reason: 'Official JSON report does not contain the exact selected test result.' };
  } else if (attachmentData.candidates.length) {
    summary.lifecycle = { status: 'unverified', reason: 'Domain report scenario/case does not match the selected registry case.' };
  } else {
    summary.lifecycle = { status: 'unverified', reason: 'No domain verification report was attached or found.' };
  }

  const browserPassed = browserStage?.exitCode === 0 && testPassed(attachmentData.matchedTest?.test);
  const afterEvidenceComplete = shouldCloseBracket
    && summary.commands.captureAfter?.exitCode === 0
    && summary.commands.healthAfter?.exitCode === 0
    && summary.integrity.status !== 'UNVERIFIED';
  if (!browserAttempted || !afterEvidenceComplete || summary.lifecycle.status === 'unverified') {
    summary.status = 'unverified';
  } else if (!browserPassed || summary.lifecycle.status === 'failed' || summary.integrity.status === 'FAIL' || !allCommandsSucceeded) {
    summary.status = 'failed';
  } else if (summary.lifecycle.status === 'passed' && summary.integrity.status === 'PASS') {
    summary.status = 'passed';
  } else {
    summary.status = 'unverified';
  }
  if (!summary.failureCategory) {
    if (summary.integrity.status === 'FAIL') summary.failureCategory = 'integrity';
    else if (summary.status === 'unverified' && !browserAttempted) summary.failureCategory = 'preparation';
    else if (summary.status === 'unverified' && !afterEvidenceComplete) summary.failureCategory = 'after-evidence';
    else if (summary.status === 'unverified' && summary.lifecycle.status === 'unverified') summary.failureCategory = 'missing-or-mismatched-report';
    else if (summary.status === 'unverified') summary.failureCategory = 'unverified-evidence';
  }

  summary.finishedAt = new Date().toISOString();
  summary.durationMs = Math.round(performance.now() - started);
  summary.workflowError = workflowFailure;
  const failedAction = summarizeFailedAction(domainReportData);
  summary.reviewPacket = {
    status: summary.status,
    scenario: scenarioID,
    case: caseName,
    listedTest: selection?.cases?.[0] ?? null,
    browserExitCode: browserStage?.exitCode ?? null,
    lifecycleStatus: summary.lifecycle.status,
    integrityStatus: summary.integrity.status,
    firstFailureReason: summarizeFirstFailureReason(domainReportData, workflowFailure, attachmentData.matchedTest?.test),
    failureContext: summarizeFailureContext(domainReportData),
    failedAction,
    lastCompletedAction: summarizeLastCompletedAction(domainReportData),
    pendingOwnedRequests: summarizePendingOwnedRequests(domainReportData, scenario),
    requiredCheckCount: summary.lifecycle.requiredCheckCount ?? null,
    passedCheckCount: summary.lifecycle.passedCheckCount ?? null,
    failedCheckNames: summary.lifecycle.failedCheckNames ?? [],
    missingCheckNames: summary.lifecycle.missingCheckNames ?? [],
    maximumMeasuredActionLatencyMs: summary.lifecycle.maximumMeasuredActionLatencyMs ?? null,
    renderCheckpointCount: summary.lifecycle.renderCheckpointCount ?? null,
    maximumRenderCheckpointLatencyMs: summary.lifecycle.maximumRenderCheckpointLatencyMs ?? null,
    report: summary.evidence.domainReport,
    playwrightJson: summary.evidence.playwrightReport,
    summary: summary.evidence.summary,
  };
  writeFileSync(summary.evidence.summary, JSON.stringify(summary, null, 2) + '\n', { mode: 0o600 });
  return summary;
}

export async function main(argv, {
  runBracket = runNativeVerificationBracket,
  write = console.log,
  env = process.env,
  targetLoader = async (input) => {
    const { loadOwnedCdaTargetConfig } = await import('./verify-ui/helpers/owned-cda-target-config.mjs');
    return loadOwnedCdaTargetConfig(input);
  },
} = {}) {
  const options = parseCli(argv);
  if (options.help) {
    write(usage);
    return 0;
  }
  if (!options.checksOnly) {
    const scenario = registry.find((candidate) => candidate.id === options.scenarioID);
    assert(scenario, 'Unknown registered scenario: ' + options.scenarioID);
    const contract = scenarioCaseFor(scenario, options.caseName);
    if (options.targetPath) {
      assert(contract.expectedIdentity,
        'This case has no registered target identity. Use --target-from-environment only when its case oracle validates scope.');
      const loadedTarget = await targetLoader({
        targetPath: options.targetPath,
        repositoryRoot,
        env,
        expectedIdentity: contract.expectedIdentity,
      });
      options.env = { ...env, ...loadedTarget.environment };
      options.targetValidation = {
        scope: loadedTarget.validationScope ?? 'configuration-only',
        registryBinding: 'bound',
        runtimeDatasetIdentity: loadedTarget.runtimeDatasetIdentity ?? 'not-checked',
        configPath: loadedTarget.configPath ?? options.targetPath,
        project: loadedTarget.target?.project ?? contract.expectedIdentity.project,
        generation: loadedTarget.target?.generation ?? contract.expectedIdentity.generation,
      };
    } else {
      assert(options.targetFromEnvironment, 'Choose an explicit target mode.');
      assert(!contract.expectedIdentity,
        'This case has a registered target identity and must use --target <config-path>.');
      options.env = { ...env };
      options.targetValidation = {
        scope: 'environment-only',
        registryBinding: 'unbound',
        runtimeDatasetIdentity: 'not-checked',
        configPath: null,
        project: null,
        generation: null,
      };
    }
  }
  const summary = await runBracket(options);
  write(JSON.stringify({
    status: summary.status,
    scenario: summary.scenario,
    case: summary.case,
    durationMs: summary.durationMs,
    runDirectory: summary.runDirectory,
    summary: summary.evidence.summary,
    report: summary.evidence.domainReport,
    integrity: summary.integrity?.status ?? 'NOT_RUN',
    targetValidation: summary.targetValidation,
    focusedCheckCoverage: summary.focusedCheckCoverage,
    focusedChecks: summary.focusedChecks,
    failureCategory: summary.failureCategory,
    firstFailureReason: summary.reviewPacket.firstFailureReason,
    failureContext: summary.reviewPacket.failureContext,
    failedAction: summary.reviewPacket.failedAction,
    lastCompletedAction: summary.reviewPacket.lastCompletedAction,
    pendingOwnedRequests: summary.reviewPacket.pendingOwnedRequests,
  }, null, 2));
  return summary.status === 'passed' || summary.status === 'checks-passed' ? 0 : 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.exitCode = await main(process.argv.slice(2));
  } catch (error) {
    console.error(String(error?.message ?? error));
    console.error(usage);
    process.exitCode = 2;
  }
}

export const parseOfficialPlaywrightList = parseListedSelection;
