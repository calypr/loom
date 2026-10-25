import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createWriteStream, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, renameSync, statSync, unlinkSync, writeFileSync } from 'node:fs';
import { finished } from 'node:stream/promises';
import { tmpdir } from 'node:os';
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { performance } from 'node:perf_hooks';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { coverageDrift, registry, scenarioCaseFor, unmappedLifecycleCoverage } from './verify-ui/registry.mjs';
import { assertCapturedTargetMatches, assertFreshApiBuildPrecheck, parseCapturedBuildIdentity } from './verify-ui/helpers/owned-stack-health.mjs';
import { sourceFingerprintChangedPaths } from './verify-ui/helpers/source-fingerprint.mjs';
import { classifyNetworkRecord } from './verify-ui/helpers/report.mjs';
import { summarizeRenderCheckpoints } from './verify-ui/helpers/coverage-status.mjs';
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
  '  node scripts/run-native-verification-bracket.mjs --batch <manifest.json> [--checks-only] [--evidence-parent <dir>] [--concurrency 1..3]',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --target <config-path> [--grep <native-test-regex>]',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --target-from-environment --grep <native-test-regex>',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --checks-only [--target <config-path>]',
  '',
  'Use --target for cases with a registered identity. --target-from-environment is',
  'explicitly registry-unbound and relies on the selected case to validate scope.',
  'Registered focused checks run before the native bracket; checks-only runs them',
  'without Docker or a browser. With --target, it validates target identity and',
  'the exact registered Playwright selection first.',
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

const mappedTargetEnvironmentNames = [
  ...requiredCaptureEnvironment,
  'LOOM_CDA_GENERATION', 'LOOM_CDA_DATASET_DIR', 'LOOM_CDA_NO_AUTH',
  'LOOM_CDA_API_PORT', 'LOOM_CDA_UI_PORT', 'LOOM_DEV_SOURCE_ROOT',
  'LOOM_DEV_COMPOSE_PROJECT', 'LOOM_DEV_PROJECT', 'LOOM_DEV_GENERATION',
  'LOOM_DEV_API_URL', 'LOOM_DEV_UI_URL', 'LOOM_DEV_API_PORT', 'LOOM_DEV_UI_PORT',
  'LOOM_ARANGO_DATABASE', 'LOOM_CDA_UI_CONTAINER',
];
async function bindCaseTarget({ contract, targetPath, targetFromEnvironment, targetLoader, root, env }) {
  if (targetPath) {
    assert(contract.expectedIdentity,
      'This case has no registered target identity. Use --target-from-environment only when its case oracle validates scope.');
    const loadedTarget = await targetLoader({
      targetPath,
      repositoryRoot: root,
      env,
      expectedIdentity: contract.expectedIdentity,
    });
    return {
      targetPath: loadedTarget.configPath ?? targetPath,
      targetObject: loadedTarget.target ?? null,
      mappedTargetEnvironment: loadedTarget.environment ?? {},
      env: { ...env, ...loadedTarget.environment },
      targetValidation: {
        scope: loadedTarget.validationScope ?? 'configuration-only',
        registryBinding: 'bound',
        runtimeDatasetIdentity: loadedTarget.runtimeDatasetIdentity ?? 'not-checked',
        configPath: loadedTarget.configPath ?? targetPath,
        project: loadedTarget.target?.project ?? contract.expectedIdentity.project,
        generation: loadedTarget.target?.generation ?? contract.expectedIdentity.generation,
      },
    };
  }

  assert(targetFromEnvironment, 'Choose an explicit target mode.');
  assert(!contract.expectedIdentity,
    'This case has a registered target identity and must use --target <config-path>.');
  return {
    targetPath: null,
    targetObject: null,
    mappedTargetEnvironment: {},
    env: { ...env },
    targetValidation: {
      scope: 'environment-only',
      registryBinding: 'unbound',
      runtimeDatasetIdentity: 'not-checked',
      configPath: null,
      project: null,
      generation: null,
    },
  };
}

function parseBatchManifest(path) {
  const manifest = readJson(resolve(path));
  assert(manifest && typeof manifest === 'object' && !Array.isArray(manifest), 'Batch manifest must be a JSON object.');
  const unknownManifestFields = Object.keys(manifest).filter((field) => !['schemaVersion', 'cases'].includes(field));
  assert.equal(unknownManifestFields.length, 0, 'Unsupported batch manifest fields: ' + unknownManifestFields.join(', ') + '.');
  assert.equal(manifest.schemaVersion, 1, 'Batch manifest schemaVersion must be 1.');
  assert(Array.isArray(manifest.cases) && manifest.cases.length > 0, 'Batch manifest cases must be a non-empty array.');
  const seen = new Set();
  return manifest.cases.map((entry, index) => {
    assert(entry && typeof entry === 'object' && !Array.isArray(entry), 'Batch case ' + index + ' must be an object.');
    const unknownCaseFields = Object.keys(entry).filter((field) => !['scenario', 'case', 'target'].includes(field));
    assert.equal(unknownCaseFields.length, 0, 'Unsupported batch case fields: ' + unknownCaseFields.join(', ') + '.');
    assert(typeof entry.scenario === 'string' && entry.scenario.trim(), 'Batch case ' + index + ' requires a scenario.');
    assert(typeof entry.case === 'string' && entry.case.trim(), 'Batch case ' + index + ' requires a case.');
    assert(entry.scenario === entry.scenario.trim() && entry.case === entry.case.trim(),
      'Batch scenario and case names must not contain surrounding whitespace.');
    const caseKey = entry.scenario + '\u0000' + entry.case;
    assert(!seen.has(caseKey), 'Batch manifest contains a duplicate scenario/case: ' + entry.scenario + '/' + entry.case + '.');
    seen.add(caseKey);
    const target = entry.target;
    assert(target && typeof target === 'object' && !Array.isArray(target),
      'Batch case ' + entry.scenario + '/' + entry.case + ' requires a target object.');
    if (target.mode === 'environment') {
      const unknownTargetFields = Object.keys(target).filter((field) => field !== 'mode');
      assert.equal(unknownTargetFields.length, 0, 'Unsupported environment target fields: ' + unknownTargetFields.join(', ') + '.');
    } else if (target.mode === 'config') {
      const unknownTargetFields = Object.keys(target).filter((field) => !['mode', 'path'].includes(field));
      assert.equal(unknownTargetFields.length, 0, 'Unsupported config target fields: ' + unknownTargetFields.join(', ') + '.');
      assert(typeof target.path === 'string' && target.path.trim(),
        'Config target for ' + entry.scenario + '/' + entry.case + ' requires a path.');
      assert(target.path === target.path.trim(), 'Config target path must not contain surrounding whitespace.');
    } else {
      throw new Error('Batch target mode must be "environment" or "config".');
    }
    return { scenario: entry.scenario, case: entry.case, target };
  });
}

function validateBatchCaseSelection(scenarioID, caseName, registeredScenarios = registry) {
  const scenario = registeredScenarios.find((candidate) => candidate.id === scenarioID);
  assert(scenario, 'Unknown registered scenario: ' + scenarioID);
  return { scenario, contract: scenarioCaseFor(scenario, caseName) };
}

function resolvedTargetIdentity(caseInput, canonicalRoot) {
  const env = caseInput.env;
  const sourceRoot = String(env.LOOM_CDA_SOURCE_ROOT ?? '').trim();
  const target = caseInput.targetObject ?? {};
  return {
    target: {
      sourceRoot: target.sourceRoot ?? env.LOOM_CDA_SOURCE_ROOT ?? null,
      datasetDir: target.datasetDir ?? env.LOOM_CDA_DATASET_DIR ?? null,
      composeProject: target.composeProject ?? env.LOOM_CDA_COMPOSE_PROJECT ?? null,
      project: target.project ?? env.LOOM_CDA_PROJECT ?? null,
      generation: target.generation ?? env.LOOM_CDA_GENERATION ?? null,
      apiOrigin: target.apiOrigin ?? env.LOOM_CDA_API_ORIGIN ?? null,
      apiPort: target.apiPort ?? env.LOOM_CDA_API_PORT ?? null,
      uiOrigin: target.uiOrigin ?? env.LOOM_CDA_UI_ORIGIN ?? null,
      uiPort: target.uiPort ?? env.LOOM_CDA_UI_PORT ?? null,
      apiContainer: target.apiContainer ?? env.LOOM_CDA_API_CONTAINER ?? null,
      uiContainer: env.LOOM_CDA_UI_CONTAINER ?? null,
      arangoContainer: target.arangoContainer ?? env.LOOM_CDA_ARANGO_CONTAINER ?? null,
      arangoDatabase: env.LOOM_ARANGO_DATABASE ?? target.arangoDatabase ?? null,
      clickhouseContainer: target.clickhouseContainer ?? env.LOOM_CDA_CLICKHOUSE_CONTAINER ?? null,
      noAuth: env.LOOM_CDA_NO_AUTH ?? (typeof target.noAuth === 'boolean' ? (target.noAuth ? '1' : '0') : null),
    },
    environment: Object.fromEntries(mappedTargetEnvironmentNames.map((name) => [name, env[name] ?? null])),
    sourceRoot: sourceRoot ? realpathSync(resolve(sourceRoot)) : null,
    repositoryRoot: canonicalRoot,
  };
}

function writeJsonAtomically(path, value) {
  const temporaryPath = path + '.' + process.pid + '.' + Math.random().toString(16).slice(2) + '.tmp';
  try {
    writeFileSync(temporaryPath, JSON.stringify(value, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
    renameSync(temporaryPath, path);
  } catch (error) {
    try { unlinkSync(temporaryPath); } catch { /* The temporary file may not have been created. */ }
    throw error;
  }
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
  outputPreviewLimit = 1024 * 1024, signal,
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
  let aborted = false;
  let terminationStarted = false;
  let abortHandler;
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
    const terminate = (reason) => {
      if (terminationStarted) return;
      terminationStarted = true;
      timedOut = reason === 'timeout';
      aborted = reason === 'abort';
      signalOwnedProcessTree(child, 'SIGTERM');
      killEscalation = new Promise((resolveKill) => { finishKillEscalation = resolveKill; });
      killHandle = setTimeout(() => {
        signalOwnedProcessTree(child, 'SIGKILL');
        finishKillEscalation?.();
      }, 1000);
    };
    child.once('error', (error) => { spawnError = String(error?.message ?? error); });
    child.once('close', (code, signal) => resolveClose({
      exitCode: spawnError ? null : code,
      signal,
      ...(spawnError ? { error: spawnError } : {}),
    }));
    if (signal) {
      abortHandler = () => terminate('abort');
      if (signal.aborted) abortHandler();
      else signal.addEventListener('abort', abortHandler, { once: true });
    }
    if (Number.isFinite(timeoutMs) && timeoutMs > 0) {
      timeoutHandle = setTimeout(() => {
        terminate('timeout');
      }, timeoutMs);
    }
  });
  if (signal && abortHandler) signal.removeEventListener('abort', abortHandler);
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
    ...(aborted ? { aborted: true } : {}),
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
      batch: { type: 'string' },
      'evidence-parent': { type: 'string' },
      concurrency: { type: 'string' },
      help: { type: 'boolean', short: 'h' },
    },
    allowPositionals: false,
    strict: true,
  });
  if (parsed.values.help) return { help: true };
  if (parsed.values.batch !== undefined) {
    assert(parsed.values.batch.trim(), 'Provide a manifest path for --batch.');
    assert(parsed.values['evidence-parent'] === undefined || parsed.values['evidence-parent'].trim(),
      '--evidence-parent requires a directory.');
    const concurrencyText = parsed.values.concurrency ?? '1';
    assert(/^\d+$/.test(concurrencyText), '--concurrency must be an integer from 1 through 3.');
    const concurrency = Number(concurrencyText);
    assert(Number.isInteger(concurrency) && concurrency >= 1 && concurrency <= 3,
      '--concurrency must be an integer from 1 through 3.');
    const singleCaseOptions = [
      'scenario', 'case', 'target', 'target-from-environment', 'grep',
    ];
    const conflicts = singleCaseOptions.filter((name) => Object.hasOwn(parsed.values, name));
    assert.equal(conflicts.length, 0, '--batch cannot be combined with ' + conflicts.map((name) => '--' + name).join(', ') + '.');
    return {
      batchPath: parsed.values.batch.trim(),
      evidenceParent: parsed.values['evidence-parent'],
      maxConcurrency: concurrency,
      checksOnly: parsed.values['checks-only'] === true,
    };
  }
  assert(parsed.values.concurrency === undefined, '--concurrency is only available with --batch.');
  assert(parsed.values['evidence-parent'] === undefined || parsed.values['evidence-parent'].trim(),
    '--evidence-parent requires a directory.');
  assert(parsed.values.scenario?.trim(), 'Provide --scenario.');
  assert(parsed.values.case?.trim(), 'Provide --case.');
  const checksOnly = parsed.values['checks-only'] === true;
  const targetPath = parsed.values.target?.trim();
  const targetFromEnvironment = parsed.values['target-from-environment'] === true;
  if (checksOnly) {
    assert(!targetFromEnvironment, '--checks-only does not accept --target-from-environment.');
    assert(parsed.values.target === undefined || Boolean(targetPath), '--target requires a config path.');
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

function endpointForNativeRequest(request, report, scenario, allowUnregistered = false) {
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
  if (typeof project !== 'string' || pathSegments[projectIndex + 1] !== project) return null;
  if (typeof explorer === 'string' && pathSegments[explorerIndex + 1] !== explorer) return null;
  if (explorer !== null && typeof explorer !== 'string') return null;

  for (const endpoint of scenario?.endpoints ?? []) {
    const [registeredMethod, template] = String(endpoint).split(/\s+/, 2);
    if (registeredMethod !== method || !template) continue;
    const templateSegments = template.split('/').filter(Boolean);
    if (templateSegments.length !== pathSegments.length) continue;
    const matches = templateSegments.every((segment, index) =>
      /^\{[^/{}]+\}$/.test(segment) || segment === pathSegments[index]);
    if (matches) return method + ' ' + template;
  }
  if (!allowUnregistered) return null;
  const templateSegments = pathSegments.map((segment, index) =>
    index === projectIndex + 1 ? '{project}'
      : index === explorerIndex + 1 ? '{explorer}'
        : segment);
  return method + ' /' + templateSegments.join('/');
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

function unfinishedRequestEvidence(report) {
  const directEvidence = report?.failureEvidence?.unfinishedNativeRequests;
  let failureRequests = Array.isArray(directEvidence) ? directEvidence : [];
  if (!Array.isArray(directEvidence)) {
    const reason = report?.failureEvidence?.reason;
    const marker = typeof reason === 'string' ? reason.indexOf('CDA verification evidence is incomplete:') : -1;
    if (marker >= 0 && reason.length <= 32_768) {
      const start = reason.indexOf('{', marker);
      const end = reason.lastIndexOf('}');
      if (start >= 0 && end >= start) {
        try {
          const evidence = JSON.parse(reason.slice(start, end + 1));
          failureRequests = Array.isArray(evidence.unfinishedNativeRequests) ? evidence.unfinishedNativeRequests : [];
        } catch {
          failureRequests = [];
        }
      }
    }
  }

  const drainedRequests = (Array.isArray(report?.nativeRequestDrainEvidence) ? report.nativeRequestDrainEvidence : [])
    .filter((drain) => drain?.status === 'timed-out')
    .flatMap((drain) => (Array.isArray(drain.unresolvedRequests) ? drain.unresolvedRequests : [])
      .map((request) => ({ ...request, reason: request?.reason ?? drain.reason })));
  return [...failureRequests, ...drainedRequests];
}

function summarizePendingOwnedRequests(report, scenario, expectedIdentity) {
  if (expectedIdentity && (report?.target?.project !== expectedIdentity.project
    || report?.target?.generation !== expectedIdentity.generation)) return [];
  const requests = [];
  const unfinishedByID = new Map(unfinishedRequestEvidence(report)
    .filter((request) => typeof request?.requestId === 'string')
    .map((request) => [request.requestId, request]));
  for (const request of Array.isArray(report?.nativeRequests) ? report.nativeRequests : []) {
    const requestID = request?.requestID ?? request?.requestId;
    const gateEvidence = unfinishedByID.get(requestID);
    const status = gateEvidence ? 'pending' : pendingRequestStatus(request);
    if (!status) continue;
    const endpoint = endpointForNativeRequest(request, report, scenario, Boolean(gateEvidence));
    if (!endpoint || typeof requestID !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(requestID)) continue;
    const reason = gateEvidence
      ? diagnosticLine(gateEvidence.reason) ?? diagnosticLine(request?.reason)
        ?? 'No completion event was recorded before verification finished.'
      : null;
    requests.push({ endpoint, requestID, status, ...(reason ? { reason } : {}) });
  }
  return requests;
}

function isExpectedReportError(error) {
  return error?.expected === true
    || Boolean(error?.expectedCancellation)
    || Boolean(error?.expectedInjectedFault)
    || Boolean(error?.expectedHttpFailure)
    || (error?.kind === 'network' && classifyNetworkRecord(error) === 'expected-injected')
    || (error?.kind === 'expected-injected' && error?.injectedFault === true);
}

function summarizeFirstFailureReason(report, workflowFailure, selectedTest) {
  const failedReportAction = (Array.isArray(report?.actions) ? report.actions : [])
    .filter((action) => ['failed', 'running', 'current'].includes(String(action?.status ?? '').toLowerCase()))
    .at(-1);
  const evidenceAction = report?.failureEvidence?.action;
  const selectedFailure = selectedPlaywrightFailureReason(selectedTest);
  const reportErrors = Array.isArray(report?.errors) ? report.errors : [];
  const unexpectedReportErrors = reportErrors.filter((error) => !isExpectedReportError(error));
  const reportErrorReasons = unexpectedReportErrors
    .map((error) => diagnosticLine(typeof error === 'string' ? error : error?.message ?? error?.error ?? error?.reason))
    .filter(Boolean);
  const genericReportErrorReason = unexpectedReportErrors.length && reportErrorReasons.length === 0
    ? 'Domain report contains errors.'
    : null;
  const actionFailure = failedReportAction || summarizeFailedAction(report)
    || diagnosticLine(evidenceAction?.error) || diagnosticLine(evidenceAction?.message);
  if (report?.status === 'passed' && unexpectedReportErrors.length === 0 && !actionFailure
    && !diagnosticLine(workflowFailure) && !selectedFailure) return null;
  const candidates = [
    workflowFailure,
    selectedFailure,
    evidenceAction?.error,
    evidenceAction?.message,
    failedReportAction?.error,
    failedReportAction?.message,
    ...reportErrorReasons,
    report?.failureEvidence?.reason,
    report?.failureEvidence?.error,
    report?.failureEvidence?.message,
    genericReportErrorReason,
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

export { summarizeRenderCheckpoints };

function registryValidationSummary(entries = registry) {
  const problems = coverageDrift(entries);
  if (problems.length) {
    throw new Error('Registered verification coverage is invalid; refusing to start the native bracket:\n'
      + problems.map((problem) => '- ' + problem).join('\n'));
  }
  const unmapped = unmappedLifecycleCoverage(entries);
  return {
    status: 'valid',
    unmappedLifecycleCount: unmapped.count,
    rows: unmapped.rows,
  };
}

function summarizeLifecycle(report, expectedScenarioID, expectedCaseName, caseContract, {
  attachmentCount = report ? 1 : 0,
  reason = 'No domain verification report was attached or found.',
} = {}) {
  assert(Array.isArray(caseContract?.requiredChecks) && caseContract.requiredChecks.length > 0,
    'Lifecycle summaries require the selected registry case contract.');
  const identity = report ? reportIdentity(report) : null;
  const requiredChecks = caseContract.requiredChecks;
  if (!report) {
    return {
      reportStatus: null,
      attachmentCount,
      reason,
      reportIdentityMatchesSelection: false,
      requiredCheckCount: requiredChecks.length,
      passedCheckCount: 0,
      failedCheckNames: [],
      missingCheckNames: [...requiredChecks],
      requiredCheckListMatchesRegistry: false,
    };
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
  const missingReported = Array.isArray(report.missingRequiredChecks) ? report.missingRequiredChecks : null;
  const actionDurations = (Array.isArray(report.actions) ? report.actions : [])
    .map((action) => action?.elapsedMs)
    .filter((value) => Number.isFinite(value) && value >= 0);
  const summarizedDimensions = compactDimensions(report.dimensions);
  const renderCheckpoints = summarizeRenderCheckpoints(report, {
    performanceCheckNames: caseContract.performanceCheckName ? [caseContract.performanceCheckName] : [],
    requiredCheckNames: requiredChecks,
  });
  return {
    reportStatus: report.status ?? null,
    attachmentCount,
    reportIdentity: { scenario: identity.scenario, caseName: identity.caseName },
    reportIdentityMatchesSelection: identity.valid
      && identity.scenario === expectedScenarioID
      && identity.caseName === expectedCaseName,
    reportIdentityAliasesConsistent: identity.valid,
    ...(!identity.valid || identity.scenario !== expectedScenarioID || identity.caseName !== expectedCaseName
      ? { reason: 'Domain report identity does not match the selected registry case.' }
      : {}),
    requiredCheckCount: requiredChecks.length,
    passedCheckCount: passedNames.length,
    failedCheckNames: failedNames,
    missingCheckNames: missingNames,
    requiredCheckListMatchesRegistry: exactCheckList,
    missingRequiredCheckCount: missingReported?.length ?? null,
    dimensions: summarizedDimensions,
    actionCount: Array.isArray(report.actions) ? report.actions.length : null,
    maximumMeasuredActionLatencyMs: actionDurations.length ? Math.max(...actionDurations) : null,
    renderCheckpointStatus: renderCheckpoints.status,
    renderCheckpointIssues: renderCheckpoints.issues,
    renderCheckpointCount: renderCheckpoints.count,
    maximumRenderCheckpointLatencyMs: renderCheckpoints.maximumDurationMs,
    renderCheckpoints: renderCheckpoints.checkpoints,
  };
}

function requiredCheckFailureReason(lifecycle) {
  if (!Number.isInteger(lifecycle?.requiredCheckCount) || lifecycle.requiredCheckCount <= 0) return null;
  if (lifecycle.attachmentCount !== 1) return 'A registered required-check report is missing or ambiguous.';
  if (lifecycle.reportStatus !== 'passed') return 'The registered required-check report did not pass.';
  if (lifecycle.reportIdentityMatchesSelection !== true) return 'The required-check report belongs to a different selected case.';
  if (lifecycle.requiredCheckListMatchesRegistry !== true) return 'The required-check report does not list the exact registered checks.';
  if (lifecycle.failedCheckNames?.length) return 'A registered required check failed.';
  if (lifecycle.missingCheckNames?.length) return 'A registered required check is missing.';
  return null;
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
  if (stage?.aborted) throw new Error(name + ' stage was aborted by SIGINT or cancellation.');
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

async function prepareNativeVerificationCase({
  scenarioID,
  caseName,
  contract: providedContract = null,
  grep,
  targetPath,
  checksOnly = false,
  targetValidation = null,
  evidenceParent,
  root = repositoryRoot,
  env = process.env,
  commandRunner = runProcess,
  signal,
  registryValidation: providedRegistryValidation = null,
  deferSummaryWrite = false,
} = {}) {
  const registryValidation = providedRegistryValidation ?? registryValidationSummary(registry);
  assert(typeof scenarioID === 'string' && scenarioID.trim(), 'Provide --scenario.');
  assert(typeof caseName === 'string' && caseName.trim(), 'Provide --case.');
  const canonicalRoot = realpathSync(resolve(root));
  const scenario = registry.find((candidate) => candidate.id === scenarioID);
  assert(scenario, 'Unknown registered scenario: ' + scenarioID);
  const contract = providedContract ?? scenarioCaseFor(scenario, caseName);
  const focusedPlans = focusedPlansForContract(contract, canonicalRoot);
  const specPath = contract.playwrightTest;
  const specAbsolute = resolve(canonicalRoot, specPath);
  assert(isWithin(canonicalRoot, specAbsolute), 'Registered Playwright spec must be inside the repository root.');
  assert(existsSync(specAbsolute), 'Registered Playwright spec is missing: ' + specPath);
  const playwrightSpecPath = relative(join(canonicalRoot, 'scripts'), specAbsolute);
  const resolvedGrep = typeof grep === 'string' && grep.trim() ? grep.trim() : contract.playwrightGrep;
  if (!checksOnly || targetPath) assert(typeof resolvedGrep === 'string' && resolvedGrep.trim(),
    'Case has no registered default Playwright selection; provide --grep.');

  const runDirectory = createEvidenceDirectory(canonicalRoot, evidenceParent, scenarioID, caseName);
  const paths = expectedCommandPaths(runDirectory);
  const startedAt = new Date().toISOString();
  const targetIdentity = targetValidation?.registryBinding === 'bound'
    ? { project: targetValidation.project, generation: targetValidation.generation }
    : contract.expectedIdentity ?? null;
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
    targetIdentity,
    targetConfigPath: targetPath ?? null,
    targetValidation: targetValidation ?? (checksOnly ? null : {
      scope: 'environment-only',
      runtimeDatasetIdentity: 'not-checked',
    }),
    registryValidation,
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
    lifecycle: { reportStatus: null, reason: 'Browser case has not produced a domain report.' },
    failureCategory: null,
    notes: [],
  };
  Object.defineProperty(summary, '_startedMonotonic', { value: started, configurable: true });
  const logsDirectory = join(runDirectory, 'logs');
  mkdirSync(logsDirectory, { recursive: true, mode: 0o700 });
  let browserAttempted = false;
  let selection = null;
  let browserStage = null;
  let domainReportPath = null;
  let workflowFailure = null;
  let baseEnv = { ...env };
  const exec = async (name, args, overrides = {}, { cwd = canonicalRoot, timeoutMs = PREPARATION_STAGE_TIMEOUT_MS,
    outputPreviewLimit = 1024 * 1024, signal: commandSignal = signal } = {}) => {
    const stdoutPath = join(logsDirectory, name + '.stdout.log');
    const stderrPath = join(logsDirectory, name + '.stderr.log');
    const runStarted = performance.now();
    let result;
    try {
      if (commandSignal?.aborted) {
        result = { exitCode: null, aborted: true, error: 'Process start aborted.' };
      } else {
        result = await commandRunner(process.execPath, args, {
          cwd,
          env: { ...baseEnv, ...overrides },
          stdoutPath,
          stderrPath,
          timeoutMs,
          outputPreviewLimit,
          ...(commandSignal ? { signal: commandSignal } : {}),
        });
      }
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
      ...(result?.aborted ? { aborted: true } : {}),
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
  const writeEarlySummary = (status, failureCategory, firstFailureReason = null) => {
    summary.status = status;
    summary.failureCategory = failureCategory;
    if (signal?.aborted) {
      summary.interrupted = true;
      summary.notes.push('Run was interrupted by SIGINT or an abort request.');
    }
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
    if (!deferSummaryWrite) writeFileSync(summary.evidence.summary, JSON.stringify(summary, null, 2) + '\n', { mode: 0o600 });
    return summary;
  };

  let browserPreparationError = null;
  let browserPreparationFatal = false;
  if (!checksOnly || targetPath) {
    try {
      const sourceRootEnv = env.LOOM_CDA_SOURCE_ROOT;
      assert(sourceRootEnv?.trim(), 'Set LOOM_CDA_SOURCE_ROOT in the validated owned environment.');
      assert.equal(realpathSync(resolve(sourceRootEnv)), canonicalRoot,
        'LOOM_CDA_SOURCE_ROOT must resolve to the repository containing this runner.');
      const missingEnv = requiredEnvironmentError(env);
      if (missingEnv) throw new Error(missingEnv);
      const playwrightCli = join(canonicalRoot, 'scripts/node_modules/@playwright/test/cli.js');
      if (commandRunner === runProcess) {
        assert(existsSync(playwrightCli), 'Install the existing scripts workspace dependencies before running Playwright.');
      }
      const selectionArgs = [
        'scripts/node_modules/@playwright/test/cli.js',
        'test', '--config', 'scripts/playwright.config.mjs',
        '--workers', '1', '--retries', '0',
        playwrightSpecPath, '--grep', resolvedGrep, '--list',
      ];
      const selectionStage = await exec('selectionList', selectionArgs);
      stageSuccess(selectionStage, 'Official Playwright --list');
      selection = parseListedSelection(selectionStage.stdoutText, specPath);
      summary.selection = selection;
      if (!selection.exact) throw new Error(selection.reason);
    } catch (error) {
      browserPreparationError = error;
      browserPreparationFatal = !env.LOOM_CDA_SOURCE_ROOT?.trim()
        || (env.LOOM_CDA_SOURCE_ROOT?.trim()
          && (() => {
            try { return realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT)) !== canonicalRoot; } catch { return true; }
          })());
      summary.workflowError = String(error?.message ?? error).slice(0, 500);
      summary.failureCategory = 'preparation';
      if (browserPreparationFatal) summary.notes.push(summary.workflowError);
      summary.focusedChecks = focusedPlans.length
        ? {
          status: 'not-run',
          groups: focusedPlans.map((plan) => ({
            id: plan.id,
            runner: plan.runner,
            status: 'not-run',
            declaredInputsHash: plan.declaredInputsHash,
            inputs: plan.inputs,
            reason: 'Skipped because browser preparation failed.',
          })),
        }
        : { status: 'browser-only', groups: [] };
      if (focusedPlans.length) summary.notes.push('Focused checks were not run because browser preparation failed.');
    }
  }

  if (!browserPreparationError && focusedPlans.length) {
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
    return { summary: writeEarlySummary('failed', 'focused-check', reason), context: null };
  }
  if (checksOnly) {
    if (browserPreparationError) {
      const reason = String(browserPreparationError?.message ?? browserPreparationError);
      return { summary: writeEarlySummary('unverified', 'preparation', reason), context: null };
    }
    if (!focusedPlans.length) {
      const reason = 'This registered case is browser-only; it has no focused prerequisite group.';
      summary.notes.push(reason);
      return { summary: writeEarlySummary('browser-only', 'no-focused-checks', reason), context: null };
    }
    return { summary: writeEarlySummary('checks-passed', null), context: null };
  }
  return {
    summary,
    context: {
      summary,
      paths,
      canonicalRoot,
      scenario,
      scenarioID,
      caseName,
      contract,
      playwrightSpecPath,
      resolvedGrep,
      selection,
      browserPreparationError,
      browserPreparationFatal,
      exec,
      signal,
    },
  };
}

async function executePreparedNativeCase(context) {
  const {
    summary, paths, canonicalRoot, scenario, scenarioID, caseName, contract,
    playwrightSpecPath, resolvedGrep, selection, browserPreparationError, exec, signal,
  } = context;
  let browserAttempted = false;
  let browserStage = null;
  let workflowFailure = null;
  if (summary.focusedChecks?.status === 'failed') return { summary, schedulingFailure: false };
  try {
    if (browserPreparationError) throw browserPreparationError;
    browserAttempted = true;
    const playwrightEnvPath = paths.playwrightReportPath;
    browserStage = await exec('playwright', [
      'scripts/node_modules/@playwright/test/cli.js',
      'test', '--config', 'scripts/playwright.config.mjs',
      '--workers', '1', '--retries', '0',
      '--output', paths.playwrightOutputPath, '--reporter=json',
      playwrightSpecPath, '--grep', resolvedGrep,
    ], { PLAYWRIGHT_JSON_OUTPUT_FILE: playwrightEnvPath }, { timeoutMs: BROWSER_STAGE_TIMEOUT_MS });
    if (browserStage.aborted) {
      workflowFailure = 'Browser stage was aborted by SIGINT or cancellation.';
      summary.failureCategory = 'interrupted';
      summary.notes.push(workflowFailure);
    } else if (browserStage.exitCode !== 0) {
      summary.failureCategory = 'browser';
      summary.notes.push('Official Playwright test exited with code ' + (browserStage.exitCode ?? 'unknown') + '.');
    }
  } catch (error) {
    workflowFailure = String(error?.message ?? error);
    summary.notes.push(workflowFailure.slice(0, 500));
    if (!summary.failureCategory) summary.failureCategory = browserAttempted ? 'browser-or-harness' : 'preparation';
  }

  summary.integrity = { status: 'UNVERIFIED', dimensions: {} };
  const officialJson = loadJsonIfPresent(paths.playwrightReportPath);
  let attachmentData = { matchedTest: null, candidates: [] };
  if (officialJson) {
    try {
      attachmentData = attachmentReports(selection, paths.playwrightReportPath, canonicalRoot);
    } catch (error) {
      summary.notes.push('Could not inspect the official Playwright report: ' + String(error?.message ?? error).slice(0, 300));
    }
  }
  let domainReportData = null;
  summary.lifecycle.attachmentCount = attachmentData.candidates.length;
  if (attachmentData.candidates.length === 1) {
    domainReportData = attachmentData.candidates[0].data;
    summary.evidence.domainReport = attachmentData.candidates[0].path;
    summary.lifecycle = summarizeLifecycle(domainReportData, scenarioID, caseName, contract);
  } else if (attachmentData.candidates.length > 1) {
    summary.lifecycle = summarizeLifecycle(null, scenarioID, caseName, contract, {
      attachmentCount: attachmentData.candidates.length,
      reason: 'More than one domain report was attached to the selected Playwright result.',
    });
  } else if (!officialJson) {
    summary.lifecycle = summarizeLifecycle(null, scenarioID, caseName, contract, {
      reason: 'Official Playwright JSON report is missing.',
    });
  } else if (!attachmentData.matchedTest) {
    summary.lifecycle = summarizeLifecycle(null, scenarioID, caseName, contract, {
      reason: 'Official JSON report does not contain the exact selected test result.',
    });
  } else {
    summary.lifecycle = summarizeLifecycle(null, scenarioID, caseName, contract);
  }
  if (signal?.aborted) {
    summary.interrupted = true;
    workflowFailure ??= 'Run was interrupted by SIGINT or an abort request.';
    if (!summary.notes.some((note) => /interrupted by SIGINT or an abort request/.test(note))) {
      summary.notes.push('Run was interrupted by SIGINT or an abort request.');
    }
  }
  summary.workflowError = workflowFailure;
  const failedAction = summarizeFailedAction(domainReportData);
  summary.reviewPacket = {
    status: 'pending-shared-closure',
    scenario: scenarioID,
    case: caseName,
    listedTest: selection?.cases?.[0] ?? null,
    browserExitCode: browserStage?.exitCode ?? null,
    browserTestPassed: testPassed(attachmentData.matchedTest?.test),
    domainReportStatus: summary.lifecycle.reportStatus ?? null,
    domainReportReason: summary.lifecycle.reason ?? null,
    domainReportIdentityMatchesSelection: summary.lifecycle.reportIdentityMatchesSelection ?? null,
    integrityStatus: 'UNVERIFIED',
    firstFailureReason: summarizeFirstFailureReason(domainReportData, workflowFailure, attachmentData.matchedTest?.test),
    failureContext: summarizeFailureContext(domainReportData),
    failedAction,
    lastCompletedAction: summarizeLastCompletedAction(domainReportData),
    pendingOwnedRequests: summarizePendingOwnedRequests(domainReportData, scenario, summary.targetIdentity),
    requiredCheckCount: summary.lifecycle.requiredCheckCount ?? null,
    passedCheckCount: summary.lifecycle.passedCheckCount ?? null,
    failedCheckNames: summary.lifecycle.failedCheckNames ?? [],
    missingCheckNames: summary.lifecycle.missingCheckNames ?? [],
    maximumMeasuredActionLatencyMs: summary.lifecycle.maximumMeasuredActionLatencyMs ?? null,
    renderCheckpointCount: summary.lifecycle.renderCheckpointCount ?? null,
    maximumRenderCheckpointLatencyMs: summary.lifecycle.maximumRenderCheckpointLatencyMs ?? null,
    renderCheckpointStatus: summary.lifecycle.renderCheckpointStatus ?? null,
    renderCheckpointIssues: summary.lifecycle.renderCheckpointIssues ?? [],
    renderCheckpoints: summary.lifecycle.renderCheckpoints ?? [],
    report: summary.evidence.domainReport,
    playwrightJson: summary.evidence.playwrightReport,
    summary: summary.evidence.summary,
  };
  const browserCommand = summary.commands.playwright;
  const schedulingFailure = Boolean(browserCommand?.timedOut || browserCommand?.spawnError || browserCommand?.aborted);
  return { summary, schedulingFailure };
}

function createSharedVerificationBracket({
  root,
  runDirectory,
  env,
  commandRunner,
  signal,
  batchId,
}) {
  const canonicalRoot = realpathSync(resolve(root));
  const paths = expectedCommandPaths(runDirectory);
  const logsDirectory = join(runDirectory, 'logs');
  mkdirSync(logsDirectory, { recursive: true, mode: 0o700 });
  const started = performance.now();
  const shared = {
    batchId,
    runDirectory,
    paths,
    commands: {},
    notes: [],
    beforeReady: false,
    beforeFailure: null,
    integrity: { status: 'UNVERIFIED', dimensions: {} },
    started: false,
    startedAt: null,
    finishedAt: null,
    durationMs: null,
  };
  const baseEnv = { ...env };
  const exec = async (name, args, { timeoutMs = PREPARATION_STAGE_TIMEOUT_MS, signal: commandSignal = signal } = {}) => {
    const stdoutPath = join(logsDirectory, name + '.stdout.log');
    const stderrPath = join(logsDirectory, name + '.stderr.log');
    const runStarted = performance.now();
    let result;
    try {
      result = commandSignal?.aborted
        ? { exitCode: null, aborted: true, error: 'Process start aborted.' }
        : await commandRunner(process.execPath, args, {
          cwd: canonicalRoot,
          env: baseEnv,
          stdoutPath,
          stderrPath,
          timeoutMs,
          outputPreviewLimit: 1024 * 1024,
          ...(commandSignal ? { signal: commandSignal } : {}),
        });
    } catch (error) {
      result = { exitCode: null, error: String(error?.message ?? error) };
    }
    saveInjectedLogs(result ?? {}, stdoutPath, stderrPath);
    const stage = {
      executable: process.execPath,
      arguments: args,
      cwd: canonicalRoot,
      timeoutMs,
      exitCode: Number.isInteger(result?.exitCode) ? result.exitCode : null,
      ...(result?.signal ? { signal: result.signal } : {}),
      ...(result?.timedOut ? { timedOut: true } : {}),
      ...(result?.aborted ? { aborted: true } : {}),
      durationMs: Math.round(Number.isFinite(result?.durationMs) ? result.durationMs : performance.now() - runStarted),
      stdoutPath,
      stderrPath,
      ...(result?.error ? { spawnError: String(result.error).slice(0, 400) } : {}),
    };
    shared.commands[name] = stage;
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
    try { captureStage = await exec('captureAfter', captureArgs('after'), { signal: null }); }
    catch (error) { shared.notes.push('After capture failed: ' + String(error?.message ?? error).slice(0, 300)); }
    let healthStage;
    try {
      healthStage = await exec('healthAfter', [
        'scripts/owned-stack-verification.mjs', '--mode', 'health',
        '--output', paths.healthAfter, '--identity', paths.apiBefore,
      ], { signal: null });
    } catch (error) { shared.notes.push('After health failed: ' + String(error?.message ?? error).slice(0, 300)); }
    if (captureStage?.exitCode !== 0) shared.notes.push('After capture did not exit successfully.');
    if (healthStage?.exitCode !== 0) shared.notes.push('After health did not exit successfully.');
  };

  let precheckRecord = null;
  let beforeArtifacts = {};
  let healthBefore = null;
  let healthAfter = null;
  let closed = false;
  shared.open = async () => {
    try {
      const missingEnvironment = requiredEnvironmentError(env);
      if (missingEnvironment) throw new Error(missingEnvironment);
      assert.equal(realpathSync(resolve(env.LOOM_CDA_SOURCE_ROOT)), canonicalRoot,
        'LOOM_CDA_SOURCE_ROOT must resolve to the repository containing this runner.');
      if (signal?.aborted) throw new Error('Run was interrupted before the shared before gate started.');

      shared.started = true;
      shared.startedAt = new Date().toISOString();
      shared.startedMonotonic = performance.now();
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
      assert.equal(beforeArtifacts.source.phase, 'before', 'Source capture phase must be before.');
      assert.equal(beforeArtifacts.docs.phase, 'before', 'Docs capture phase must be before.');
      assert.equal(beforeArtifacts.api.phase, 'before', 'API capture phase must be before.');
      assert.equal(beforeArtifacts.mounts.phase, 'before', 'Mount capture phase must be before.');
      assert.equal(beforeArtifacts.source.root, canonicalRoot, 'Source capture must describe the repository root.');
      assert.equal(beforeArtifacts.docs.root, canonicalRoot, 'Docs capture must describe the repository root.');
      assert.equal(beforeArtifacts.api.target?.sourceRoot, canonicalRoot, 'API capture target must describe the repository root.');
      assert.equal(beforeArtifacts.mounts.status, 'PASS', 'Before owned mounts must pass.');

      const apiOrigin = new URL(env.LOOM_CDA_API_ORIGIN);
      const uiOrigin = new URL(env.LOOM_CDA_UI_ORIGIN);
      assert.equal(String(beforeArtifacts.api.target?.apiPort), apiOrigin.port,
        'API capture port must match LOOM_CDA_API_ORIGIN.');
      assert.equal(String(beforeArtifacts.api.target?.uiPort), uiOrigin.port,
        'API capture port must match LOOM_CDA_UI_ORIGIN.');
      const expectedCaptureTarget = {
        project: env.LOOM_CDA_PROJECT,
        generation: env.LOOM_CDA_GENERATION ?? beforeArtifacts.api.target?.generation,
        composeProject: env.LOOM_CDA_COMPOSE_PROJECT,
        apiContainer: env.LOOM_CDA_API_CONTAINER,
        uiContainer: beforeArtifacts.api.target.uiContainer,
        arangoContainer: env.LOOM_CDA_ARANGO_CONTAINER,
        clickhouseContainer: env.LOOM_CDA_CLICKHOUSE_CONTAINER,
        apiPort: beforeArtifacts.api.target.apiPort,
        uiPort: beforeArtifacts.api.target.uiPort,
        sourceRoot: canonicalRoot,
      };
      assertCapturedTargetMatches(beforeArtifacts.api.target, expectedCaptureTarget);
      assertCapturedTargetMatches(beforeArtifacts.api.target, beforeArtifacts.mounts.target);
      assertFreshApiBuildPrecheck(precheckRecord, {
        targetContainer: env.LOOM_CDA_API_CONTAINER,
        apiBuildIdentity: beforeArtifacts.api.apiBuildIdentity,
      });

      const beforeHealthStage = await exec('healthBefore', [
        'scripts/owned-stack-verification.mjs', '--mode', 'health',
        '--output', paths.healthBefore, '--identity', paths.apiBefore,
      ]);
      stageSuccess(beforeHealthStage, 'Before health');
      healthBefore = loadJsonIfPresent(paths.healthBefore);
      assert(healthBefore, 'Before health artifact is missing or invalid.');
      assert.equal(healthBefore.status, 'PASS', 'Before health did not pass.');
      assert.equal(healthBefore.samples?.length, 3, 'Before health did not record three samples.');
      assert.equal(healthDimension(healthBefore, beforeArtifacts.api.apiBuildIdentity).status, 'PASS',
        'Before health must prove the captured API/UI identity.');
      shared.beforeReady = true;
    } catch (error) {
      shared.beforeFailure = String(error?.message ?? error).slice(0, 500);
      shared.notes.push(shared.beforeFailure);
    }
    return shared;
  };
  shared.close = async () => {
    if (closed) return shared;
    closed = true;
    if (!shared.started) {
      shared.status = 'NOT_RUN';
      return shared;
    }
    await runAfterClosure();
    const afterArtifacts = {
      source: loadJsonIfPresent(paths.sourceAfter),
      docs: loadJsonIfPresent(paths.docsAfter),
      api: loadJsonIfPresent(paths.apiAfter),
      mounts: loadJsonIfPresent(paths.mountsAfter),
    };
    healthAfter = loadJsonIfPresent(paths.healthAfter);
    shared.integrity = compareCaptureArtifacts({
      before: beforeArtifacts,
      after: afterArtifacts,
      precheck: precheckRecord,
      healthBefore,
      healthAfter,
      expectedRoot: canonicalRoot,
    });
    const closureComplete = shared.commands.captureAfter?.exitCode === 0
      && shared.commands.healthAfter?.exitCode === 0
      && shared.integrity.status !== 'UNVERIFIED';
    shared.status = shared.integrity.status === 'FAIL' ? 'FAIL'
      : shared.beforeReady && closureComplete ? 'PASS' : 'UNVERIFIED';
    shared.finishedAt = new Date().toISOString();
    shared.durationMs = Math.round(performance.now() - shared.startedMonotonic);
    return shared;
  };
  return shared;
}

function finalizeNativeCase(summary, shared, {
  batchSummaryPath,
  batchId,
  interrupted,
  concurrent = false,
  caseIsolation = null,
}) {
  if (summary.mode === 'checks-only') {
    summary.nativeStatus = 'not-run';
    summary.durationScope = 'checks-only-preparation';
    summary.finishedAt = new Date().toISOString();
    summary.durationMs = Math.round(performance.now() - summary._startedMonotonic);
    summary.reviewPacket.status = summary.status;
    delete summary._startedMonotonic;
    return summary;
  }

  const sharedEvidence = {
    batchSummary: batchSummaryPath,
    precheck: shared.paths.precheck,
    sourceBefore: shared.paths.sourceBefore,
    docsBefore: shared.paths.docsBefore,
    apiBefore: shared.paths.apiBefore,
    mountsBefore: shared.paths.mountsBefore,
    healthBefore: shared.paths.healthBefore,
    sourceAfter: shared.paths.sourceAfter,
    docsAfter: shared.paths.docsAfter,
    apiAfter: shared.paths.apiAfter,
    mountsAfter: shared.paths.mountsAfter,
    healthAfter: shared.paths.healthAfter,
  };
  Object.assign(summary.evidence, {
    precheck: shared.paths.precheck,
    sourceBefore: shared.paths.sourceBefore,
    docsBefore: shared.paths.docsBefore,
    apiBefore: shared.paths.apiBefore,
    mountsBefore: shared.paths.mountsBefore,
    healthBefore: shared.paths.healthBefore,
    sourceAfter: shared.paths.sourceAfter,
    docsAfter: shared.paths.docsAfter,
    apiAfter: shared.paths.apiAfter,
    mountsAfter: shared.paths.mountsAfter,
    healthAfter: shared.paths.healthAfter,
    sharedBracket: sharedEvidence,
  });
  summary.commands = { ...summary.commands, ...shared.commands };
  summary.integrity = shared.integrity;
  summary.sharedBracket = {
    batchId,
    status: shared.status,
    summary: batchSummaryPath,
    evidence: sharedEvidence,
    commands: Object.fromEntries(Object.entries(shared.commands).map(([name, stage]) => [name, {
      exitCode: stage.exitCode,
      timedOut: Boolean(stage.timedOut),
      stdoutPath: stage.stdoutPath,
      stderrPath: stage.stderrPath,
    }])),
  };
  const nativeNotRun = summary.nativeStatus === 'not-run';
  summary.nativeStatus = nativeNotRun || summary.commands.playwright === undefined ? 'not-run'
    : summary.reviewPacket?.browserTestPassed === true
    && summary.commands.playwright?.exitCode === 0
    ? 'passed'
    : 'failed';
  const caseCommandFailures = Object.entries(summary.commands)
    .filter(([name]) => !['precheck', 'captureBefore', 'healthBefore', 'captureAfter', 'healthAfter'].includes(name))
    .some(([, stage]) => stage.exitCode !== 0 || stage.timedOut || stage.aborted);
  const browserAttempted = summary.commands.playwright !== undefined;
  const browserPassed = browserAttempted
    && summary.commands.playwright.exitCode === 0
    && summary.reviewPacket?.browserTestPassed === true;
  const requiredCheckFailure = browserAttempted ? requiredCheckFailureReason(summary.lifecycle) : null;
  const afterEvidenceComplete = shared.commands.captureAfter?.exitCode === 0
    && shared.commands.healthAfter?.exitCode === 0
    && shared.integrity.status !== 'UNVERIFIED';

  if (interrupted) {
    summary.workflowError ??= 'Run was interrupted by SIGINT or an abort request.';
  } else if (!browserAttempted && !summary.workflowError && shared.beforeFailure && shared.status !== 'NOT_RUN') {
    summary.workflowError = shared.beforeFailure;
  }
  if (interrupted) {
    summary.status = 'unverified';
    summary.failureCategory = 'interrupted';
    summary.interrupted = true;
  } else if (summary.focusedChecks?.status === 'failed') {
    summary.status = 'failed';
    summary.failureCategory = 'focused-check';
  } else if (requiredCheckFailure) {
    summary.status = 'failed';
    summary.failureCategory = 'required-check';
    summary.notes.push(requiredCheckFailure);
  } else if (!browserAttempted || !afterEvidenceComplete || !shared.beforeReady) {
    summary.status = 'unverified';
  } else if (!browserPassed || shared.integrity.status === 'FAIL' || caseCommandFailures) {
    summary.status = 'failed';
  } else if (shared.integrity.status === 'PASS') {
    summary.status = 'passed';
  } else {
    summary.status = 'unverified';
  }
  if (!summary.failureCategory || summary.failureCategory === 'after-evidence') {
    if (interrupted) summary.failureCategory = 'interrupted';
    else if (shared.integrity.status === 'FAIL') summary.failureCategory = 'integrity';
    else if (browserAttempted && !browserPassed) summary.failureCategory = 'browser-result';
    else if (summary.status === 'unverified' && !browserAttempted) summary.failureCategory = 'preparation';
    else if (summary.status === 'unverified' && !afterEvidenceComplete) summary.failureCategory = 'after-evidence';
    else if (summary.status === 'unverified') summary.failureCategory = 'unverified-evidence';
  }
  summary.reviewPacket ??= {};
  summary.reviewPacket.status = summary.status;
  summary.reviewPacket.integrityStatus = shared.integrity.status;
  summary.reviewPacket.firstFailureReason ??= summary.workflowError ?? null;
  summary.reviewPacket.failedAction ??= null;
  summary.reviewPacket.lastCompletedAction ??= null;
  summary.reviewPacket.pendingOwnedRequests ??= [];
  summary.reviewPacket.domainReportStatus ??= summary.lifecycle?.reportStatus ?? null;
  summary.reviewPacket.domainReportReason ??= summary.lifecycle?.reason ?? null;
  if (!summary.reviewPacket.browserTestPassed) summary.reviewPacket.browserTestPassed = false;
  if (concurrent) {
    summary.timingInterpretation = 'concurrent-latency-provisional';
    summary.caseIsolation = caseIsolation;
  } else {
    summary.timingInterpretation = 'serial';
    if (caseIsolation) summary.caseIsolation = caseIsolation;
  }
  summary.finishedAt = new Date().toISOString();
  summary.durationMs = Math.round(performance.now() - summary._startedMonotonic);
  summary.durationScope = 'case-preparation-through-shared-closure';
  summary.batchId = batchId;
  summary.reviewPacket.status = summary.status;
  summary.reviewPacket.integrityStatus = shared.integrity.status;
  return summary;
}

function skippedBatchCase({ caseInput, reason, canonicalRoot, evidenceParent, batchId, batchSummaryPath, shared, interrupted }) {
  const runDirectory = createEvidenceDirectory(canonicalRoot, evidenceParent, caseInput.scenarioID, caseInput.caseName);
  const paths = expectedCommandPaths(runDirectory);
  const summary = {
    schemaVersion: 1,
    status: 'unverified',
    nativeStatus: 'not-run',
    startedAt: new Date().toISOString(),
    finishedAt: new Date().toISOString(),
    scenario: caseInput.scenarioID,
    case: caseInput.caseName,
    mode: 'browser',
    targetIdentity: null,
    targetConfigPath: caseInput.targetPath ?? null,
    targetValidation: caseInput.targetValidation ?? null,
    registryValidation: null,
    runDirectory,
    commands: { ...shared.commands },
    evidence: {
      summary: join(runDirectory, 'summary.json'),
      playwrightReport: paths.playwrightReportPath,
      domainReport: null,
      ...Object.fromEntries([
        ['precheck', shared.paths.precheck], ['sourceBefore', shared.paths.sourceBefore],
        ['docsBefore', shared.paths.docsBefore], ['apiBefore', shared.paths.apiBefore],
        ['mountsBefore', shared.paths.mountsBefore], ['healthBefore', shared.paths.healthBefore],
        ['sourceAfter', shared.paths.sourceAfter], ['docsAfter', shared.paths.docsAfter],
        ['apiAfter', shared.paths.apiAfter], ['mountsAfter', shared.paths.mountsAfter],
        ['healthAfter', shared.paths.healthAfter],
      ]),
      sharedBracket: { batchSummary: batchSummaryPath, batchId },
    },
    selection: null,
    focusedCheckCoverage: 'not-run',
    focusedChecks: { status: 'not-run', groups: [] },
    integrity: shared.integrity,
    lifecycle: { reportStatus: null, reason: 'Browser case was not started.' },
    failureCategory: interrupted ? 'interrupted' : 'shared-bracket',
    notes: [reason],
    interrupted: Boolean(interrupted),
    workflowError: reason,
    reviewPacket: {
      status: 'unverified',
      scenario: caseInput.scenarioID,
      case: caseInput.caseName,
      firstFailureReason: reason,
      pendingOwnedRequests: [],
      report: null,
      playwrightJson: null,
      summary: join(runDirectory, 'summary.json'),
    },
    batchId,
  };
  Object.defineProperty(summary, '_startedMonotonic', { value: performance.now(), configurable: true });
  return summary;
}

async function defaultTargetLoader(input) {
  const { loadOwnedCdaTargetConfig } = await import('./verify-ui/helpers/owned-cda-target-config.mjs');
  return loadOwnedCdaTargetConfig(input);
}

function caseSummaryEntry(summary, caseIsolation) {
  return {
    scenario: summary.scenario,
    case: summary.case,
    status: summary.status,
    nativeStatus: summary.nativeStatus,
    integrityStatus: summary.integrity?.status ?? 'UNVERIFIED',
    failureCategory: summary.failureCategory,
    runDirectory: summary.runDirectory,
    summaryPath: summary.evidence.summary,
    durationMs: summary.durationMs ?? null,
    playwrightDurationMs: summary.commands?.playwright?.durationMs ?? null,
    startedAt: summary.startedAt ?? null,
    finishedAt: summary.finishedAt ?? null,
    durationScope: summary.durationScope ?? 'case-preparation-through-shared-closure',
    timingInterpretation: summary.timingInterpretation ?? 'serial',
    caseIsolation: caseIsolation ?? null,
  };
}

async function runNativeVerificationEngine({
  resolvedCases,
  root = repositoryRoot,
  evidenceParent,
  commandRunner = runProcess,
  signal,
  registryValidation,
  maxConcurrency = 1,
  atomicWriteJson = writeJsonAtomically,
} = {}) {
  assert(Array.isArray(resolvedCases) && resolvedCases.length > 0, 'Provide at least one registered case to verify.');
  assert(Number.isInteger(maxConcurrency) && maxConcurrency >= 1 && maxConcurrency <= 3,
    'Batch maxConcurrency must be an integer from 1 through 3.');
  const canonicalRoot = realpathSync(resolve(root));
  assert(resolvedCases.every((caseInput) => caseInput?.contract && caseInput?.env && caseInput?.targetValidation),
    'The internal verification engine requires resolved case targets.');
  const checkOnlyCount = resolvedCases.filter((caseInput) => caseInput.checksOnly).length;
  assert(checkOnlyCount === 0 || checkOnlyCount === resolvedCases.length,
    'A checks-only batch cannot mix focused-only and browser cases.');

  const targetIdentities = resolvedCases.map((caseInput) => resolvedTargetIdentity(caseInput, canonicalRoot));
  const identityJSON = JSON.stringify(targetIdentities[0]);
  assert(targetIdentities.every((identity) => JSON.stringify(identity) === identityJSON),
    'Batch cases resolve to mixed capture targets; split them into separate batches.');
  for (const caseInput of resolvedCases) {
    assert(['fresh-fixture-project', undefined].includes(caseInput.contract.parallelIsolation),
      'Unsupported registry parallel-isolation value for ' + caseInput.scenarioID + '/' + caseInput.caseName + '.');
  }

  const startedAt = new Date().toISOString();
  const started = performance.now();
  const runDirectory = createEvidenceDirectory(canonicalRoot, evidenceParent, 'batch', String(Date.now()));
  const batchId = basename(runDirectory);
  const paths = expectedCommandPaths(runDirectory);
  const batchSummaryPath = join(runDirectory, 'batch-summary.json');
  const batchSummary = {
    schemaVersion: 1,
    status: 'unverified',
    batchId,
    startedAt,
    runDirectory,
    summaryPath: batchSummaryPath,
    caseCount: resolvedCases.length,
    targetIdentity: targetIdentities[0],
    targetValidation: resolvedCases[0].targetValidation,
    concurrency: {
      limit: maxConcurrency,
      maxObserved: 0,
      eligibleCaseCount: resolvedCases.filter((caseInput) => caseInput.contract.parallelIsolation === 'fresh-fixture-project').length,
    },
    sharedBracket: {
      status: 'pending',
      firstFailureReason: null,
      startedAt: null,
      finishedAt: null,
      durationMs: null,
      integrity: { status: 'UNVERIFIED', dimensions: {} },
      evidence: {
        precheck: paths.precheck,
        sourceBefore: paths.sourceBefore,
        docsBefore: paths.docsBefore,
        apiBefore: paths.apiBefore,
        mountsBefore: paths.mountsBefore,
        healthBefore: paths.healthBefore,
        sourceAfter: paths.sourceAfter,
        docsAfter: paths.docsAfter,
        apiAfter: paths.apiAfter,
        mountsAfter: paths.mountsAfter,
        healthAfter: paths.healthAfter,
      },
      commands: {},
    },
    cases: [],
    notes: [],
  };
  const writeBatch = async () => atomicWriteJson(batchSummaryPath, batchSummary);
  try {
    await writeBatch();
  } catch (error) {
    batchSummary.failureCategory = 'evidence-write';
    batchSummary.notes.push('Could not write the initial batch summary: ' + String(error?.message ?? error).slice(0, 300));
    batchSummary.finishedAt = new Date().toISOString();
    batchSummary.durationMs = Math.round(performance.now() - started);
    const unavailableShared = {
      batchId, runDirectory, paths, commands: {}, notes: [], beforeReady: false,
      beforeFailure: 'Batch summary could not be written before runtime work.',
      integrity: { status: 'UNVERIFIED', dimensions: {} }, status: 'UNVERIFIED', started: false,
    };
    const skipped = resolvedCases.map((caseInput) => finalizeNativeCase(skippedBatchCase({
      caseInput,
      reason: unavailableShared.beforeFailure,
      canonicalRoot,
      evidenceParent,
      batchId,
      batchSummaryPath,
      shared: unavailableShared,
      interrupted: Boolean(signal?.aborted),
    }), unavailableShared, {
      batchSummaryPath, batchId, interrupted: Boolean(signal?.aborted),
      caseIsolation: caseInput.contract.parallelIsolation ?? null,
    }));
    batchSummary.sharedBracket.status = 'UNVERIFIED';
    batchSummary.sharedBracket.firstFailureReason = unavailableShared.beforeFailure;
    batchSummary.cases = skipped.map((summary) => caseSummaryEntry(summary, summary.caseIsolation));
    Object.defineProperty(batchSummary, 'caseSummaries', { value: skipped, configurable: true });
    return batchSummary;
  }

  const checkOnly = resolvedCases.every((caseInput) => caseInput.checksOnly === true);
  const preparedCases = [];
  for (const caseInput of resolvedCases) {
    try {
      const prepared = await prepareNativeVerificationCase({
        ...caseInput,
        root: canonicalRoot,
        env: caseInput.env,
        commandRunner,
        signal,
        registryValidation,
        deferSummaryWrite: true,
      });
      preparedCases.push(prepared);
    } catch (error) {
      if (caseInput.checksOnly) throw error;
      preparedCases.push({
        summary: null,
        context: null,
        error: String(error?.message ?? error).slice(0, 400),
      });
    }
  }

  let shared;
  const fatalPreparation = preparedCases.some((prepared) => prepared.context?.browserPreparationFatal);
  const readyIndices = preparedCases.map((prepared, index) =>
    prepared.context
      && !prepared.context.browserPreparationError
      && prepared.summary?.focusedChecks?.status !== 'failed'
      ? index : -1).filter((index) => index >= 0);
  batchSummary.concurrency.eligibleCaseCount = readyIndices.filter((index) =>
    resolvedCases[index].contract.parallelIsolation === 'fresh-fixture-project').length;
  if (checkOnly) {
    shared = {
      batchId,
      runDirectory,
      paths,
      commands: {},
      notes: [],
      beforeReady: true,
      beforeFailure: null,
      integrity: { status: 'UNVERIFIED', dimensions: {} },
      status: 'NOT_RUN',
      started: false,
    };
    batchSummary.sharedBracket.status = 'NOT_RUN';
    batchSummary.sharedBracket.firstFailureReason = null;
  } else if (fatalPreparation || readyIndices.length === 0) {
    const failure = preparedCases.find((prepared) => prepared.context?.browserPreparationFatal);
    const noReadyReason = failure?.summary?.workflowError
      ?? 'No prepared native case was ready to run; shared verification commands were not started.';
    shared = {
      batchId,
      runDirectory,
      paths,
      commands: {},
      notes: [noReadyReason],
      beforeReady: false,
      beforeFailure: noReadyReason,
      integrity: { status: 'UNVERIFIED', dimensions: {} },
      status: 'NOT_RUN',
      started: false,
    };
    batchSummary.sharedBracket.status = 'NOT_RUN';
    batchSummary.sharedBracket.firstFailureReason = shared.beforeFailure;
  } else {
    shared = createSharedVerificationBracket({
      root: canonicalRoot,
      runDirectory,
      env: resolvedCases[0].env,
      commandRunner,
      signal,
      batchId,
    });
    let activeCaseExecutions = 0;
    const executeReadyCase = async (caseIndex) => {
      activeCaseExecutions += 1;
      batchSummary.concurrency.maxObserved = Math.max(batchSummary.concurrency.maxObserved, activeCaseExecutions);
      try {
        return await executePreparedNativeCase(preparedCases[caseIndex].context);
      } catch (error) {
        const summary = preparedCases[caseIndex].summary;
        summary.failureCategory ??= 'browser-or-harness';
        summary.workflowError = String(error?.message ?? error).slice(0, 400);
        summary.notes.push(summary.workflowError);
        return { summary, schedulingFailure: false };
      } finally {
        activeCaseExecutions -= 1;
      }
    };
    try {
      await shared.open();
      if (shared.beforeReady) {
        let cursor = 0;
        while (cursor < readyIndices.length && !signal?.aborted) {
          const index = readyIndices[cursor];
          const current = resolvedCases[index];
          if (maxConcurrency > 1 && current.contract.parallelIsolation === 'fresh-fixture-project') {
            const wave = [];
            while (cursor < readyIndices.length && wave.length < maxConcurrency && !signal?.aborted) {
              const nextIndex = readyIndices[cursor];
              if (resolvedCases[nextIndex].contract.parallelIsolation !== 'fresh-fixture-project') break;
              wave.push(nextIndex);
              cursor += 1;
            }
            const results = await Promise.allSettled(wave.map((waveIndex) => executeReadyCase(waveIndex)));
            wave.forEach((waveIndex, offset) => {
              const result = results[offset];
              if (result.status === 'fulfilled') preparedCases[waveIndex].summary = result.value.summary;
              else {
                preparedCases[waveIndex].summary.failureCategory = 'browser-or-harness';
                preparedCases[waveIndex].summary.workflowError = String(result.reason?.message ?? result.reason).slice(0, 400);
                preparedCases[waveIndex].summary.notes.push(preparedCases[waveIndex].summary.workflowError);
              }
              preparedCases[waveIndex].concurrent = wave.length > 1;
            });
          } else {
            cursor += 1;
            const result = await executeReadyCase(index);
            preparedCases[index].summary = result.summary;
          }
        }
      }
    } catch (error) {
      shared.beforeFailure ??= String(error?.message ?? error).slice(0, 500);
      shared.notes.push(shared.beforeFailure);
    } finally {
      await shared.close();
    }
    batchSummary.sharedBracket.status = shared.status;
    batchSummary.sharedBracket.firstFailureReason = shared.beforeFailure;
    batchSummary.sharedBracket.integrity = shared.integrity;
    batchSummary.sharedBracket.commands = shared.commands;
    batchSummary.sharedBracket.startedAt = shared.startedAt ?? null;
    batchSummary.sharedBracket.finishedAt = shared.finishedAt ?? null;
    batchSummary.sharedBracket.durationMs = shared.durationMs ?? null;
    batchSummary.notes.push(...shared.notes);
  }

  const caseSummaries = new Array(resolvedCases.length);
  for (let index = 0; index < resolvedCases.length; index += 1) {
    const prepared = preparedCases[index];
    if (prepared.summary) {
      caseSummaries[index] = prepared.summary;
    } else {
      const reason = signal?.aborted
        ? 'Case preparation stopped by interruption.'
        : prepared.error ?? (shared.beforeReady
          ? 'Case preparation did not produce a verification context.'
          : shared.beforeFailure ?? 'Shared before gate did not pass; native cases were not started.');
      caseSummaries[index] = skippedBatchCase({
        caseInput: resolvedCases[index],
        reason,
        canonicalRoot,
        evidenceParent,
        batchId,
        batchSummaryPath,
        shared,
        interrupted: Boolean(signal?.aborted),
      });
    }
  }

  for (let index = 0; index < caseSummaries.length; index += 1) {
    const summary = finalizeNativeCase(caseSummaries[index], shared, {
      batchSummaryPath,
      batchId,
      interrupted: Boolean(signal?.aborted || caseSummaries[index].interrupted),
      concurrent: preparedCases[index].concurrent === true,
      caseIsolation: resolvedCases[index].contract.parallelIsolation ?? null,
    });
    try {
      await atomicWriteJson(summary.evidence.summary, summary);
    } catch (error) {
      summary.status = 'unverified';
      summary.failureCategory = 'evidence-write';
      summary.notes.push('Could not write the case summary: ' + String(error?.message ?? error).slice(0, 300));
      summary.reviewPacket.status = 'unverified';
      batchSummary.notes.push('Could not write summary for ' + summary.scenario + '/' + summary.case + '.');
    }
  }

  batchSummary.cases = caseSummaries.map((summary, index) => caseSummaryEntry(
    summary,
    resolvedCases[index].contract.parallelIsolation ?? null,
  ));
  const allCasesPassed = caseSummaries.length > 0 && caseSummaries.every((summary) => summary.status === 'passed');
  if (checkOnly) {
    batchSummary.status = caseSummaries.length > 0 && caseSummaries.every((summary) => summary.status === 'checks-passed') ? 'passed'
      : caseSummaries.some((summary) => summary.status === 'failed') ? 'failed' : 'unverified';
    batchSummary.nativeStatus = 'not-run';
    batchSummary.integrityStatus = 'NOT_RUN';
  } else if (signal?.aborted) {
    batchSummary.status = 'unverified';
  } else if (shared.status === 'FAIL' || caseSummaries.some((summary) => summary.status === 'failed')) {
    batchSummary.status = 'failed';
  } else if (shared.status === 'UNVERIFIED' || !shared.beforeReady) {
    batchSummary.status = 'unverified';
  } else {
    batchSummary.status = allCasesPassed ? 'passed' : 'unverified';
  }
  if (signal?.aborted) {
    batchSummary.interrupted = true;
    batchSummary.failureCategory = 'interrupted';
    batchSummary.notes.push('Run was interrupted by SIGINT or an abort request.');
  }
  if (caseSummaries.some((summary) => summary.failureCategory === 'evidence-write')) {
    batchSummary.status = 'unverified';
    batchSummary.failureCategory = 'evidence-write';
  }
  batchSummary.finishedAt = new Date().toISOString();
  batchSummary.durationMs = Math.round(performance.now() - started);
  batchSummary.durationScope = 'batch-invocation-through-shared-closure';
  if (!checkOnly) batchSummary.integrityStatus = shared.integrity.status;
  if (!checkOnly) {
    const nativeStatuses = caseSummaries.map((summary) => summary.nativeStatus).filter((status) => status !== 'not-run');
    batchSummary.nativeStatus = nativeStatuses.length === 0 ? 'not-run'
      : nativeStatuses.every((status) => status === 'passed') ? 'passed'
        : nativeStatuses.some((status) => status === 'failed') ? 'failed' : 'unverified';
  }
  try {
    await writeBatch();
  } catch (error) {
    batchSummary.status = 'unverified';
    batchSummary.failureCategory = 'evidence-write';
    batchSummary.notes.push('Could not write the final batch summary: ' + String(error?.message ?? error).slice(0, 300));
    for (const summary of caseSummaries) {
      summary.status = 'unverified';
      summary.failureCategory = 'evidence-write';
      summary.reviewPacket.status = 'unverified';
      summary.notes.push('Batch summary write failed; the batch verdict is unverified.');
      try { await atomicWriteJson(summary.evidence.summary, summary); } catch { /* Preserve the last safe summary if rewriting also fails. */ }
    }
    batchSummary.cases = caseSummaries.map((summary, index) => caseSummaryEntry(
      summary,
      resolvedCases[index].contract.parallelIsolation ?? null,
    ));
  }
  Object.defineProperty(batchSummary, 'caseSummaries', { value: caseSummaries, configurable: true });
  return batchSummary;
}

async function resolveBatchCases(cases, { root, env, targetLoader, registryValidation, checksOnly }) {
  assert(Array.isArray(cases) && cases.length > 0, 'Batch manifest cases must be a non-empty array.');
  const seen = new Set();
  const resolvedCases = [];
  for (let index = 0; index < cases.length; index += 1) {
    const input = cases[index];
    assert(input && typeof input === 'object' && !Array.isArray(input), 'Batch case ' + index + ' must be an object.');
    const unknownFields = Object.keys(input).filter((field) => !['scenario', 'case', 'target'].includes(field));
    assert.equal(unknownFields.length, 0, 'Unsupported batch case fields: ' + unknownFields.join(', ') + '.');
    const scenarioID = input.scenario;
    const caseName = input.case;
    assert(typeof scenarioID === 'string' && scenarioID.trim(), 'Batch case ' + index + ' requires a scenario.');
    assert(typeof caseName === 'string' && caseName.trim(), 'Batch case ' + index + ' requires a case.');
    assert(scenarioID === scenarioID.trim() && caseName === caseName.trim(),
      'Batch scenario and case names must not contain surrounding whitespace.');
    const caseKey = scenarioID + '\u0000' + caseName;
    assert(!seen.has(caseKey), 'Batch contains a duplicate scenario/case: ' + scenarioID + '/' + caseName + '.');
    seen.add(caseKey);
    const { scenario, contract: defaultContract } = validateBatchCaseSelection(scenarioID, caseName);
    const target = input.target;
    assert(target && typeof target === 'object' && !Array.isArray(target),
      'Batch case ' + scenarioID + '/' + caseName + ' requires a target object.');
    let targetPath = null;
    let targetFromEnvironment = false;
    if (target.mode === 'environment') {
      const unknownTargetFields = Object.keys(target).filter((field) => field !== 'mode');
      assert.equal(unknownTargetFields.length, 0, 'Unsupported environment target fields: ' + unknownTargetFields.join(', ') + '.');
      targetFromEnvironment = true;
    } else if (target.mode === 'config') {
      const unknownTargetFields = Object.keys(target).filter((field) => !['mode', 'path'].includes(field));
      assert.equal(unknownTargetFields.length, 0, 'Unsupported config target fields: ' + unknownTargetFields.join(', ') + '.');
      assert(typeof target.path === 'string' && target.path.trim() && target.path === target.path.trim(),
        'Config target for ' + scenarioID + '/' + caseName + ' requires a path without surrounding whitespace.');
      targetPath = target.path;
    } else {
      throw new Error('Batch target mode must be "environment" or "config".');
    }
    const bound = checksOnly
      ? {
        targetPath: null,
        targetObject: null,
        mappedTargetEnvironment: {},
        env: { ...env },
        targetValidation: {
          scope: 'checks-only',
          registryBinding: 'unbound',
          runtimeDatasetIdentity: 'not-checked',
          configPath: null,
          project: null,
          generation: null,
        },
      }
      : await bindCaseTarget({
        contract: defaultContract,
        targetPath,
        targetFromEnvironment,
        targetLoader,
        root,
        env,
      });
    const contract = checksOnly
      ? defaultContract
      : scenarioCaseFor(scenario, caseName, bound.targetObject?.kind === 'read-only-custom');
    resolvedCases.push({
      ...bound,
      scenarioID,
      caseName,
      checksOnly: checksOnly === true,
      contract,
      env: bound.env,
      registryValidation,
    });
  }
  return resolvedCases;
}

export async function runNativeVerificationBatch({
  cases,
  checksOnly = false,
  root = repositoryRoot,
  env = process.env,
  evidenceParent,
  commandRunner = runProcess,
  signal,
  targetLoader = defaultTargetLoader,
  registryValidation: providedRegistryValidation = null,
  maxConcurrency = 1,
  atomicWriteJson = writeJsonAtomically,
} = {}) {
  assert(Number.isInteger(maxConcurrency) && maxConcurrency >= 1 && maxConcurrency <= 3,
    'Batch maxConcurrency must be an integer from 1 through 3.');
  const canonicalRoot = realpathSync(resolve(root));
  const registryValidation = providedRegistryValidation ?? registryValidationSummary(registry);
  const resolvedCases = await resolveBatchCases(cases, {
    root: canonicalRoot,
    env,
    targetLoader,
    registryValidation,
    checksOnly,
  });
  return runNativeVerificationEngine({
    resolvedCases,
    root: canonicalRoot,
    evidenceParent,
    commandRunner,
    signal,
    registryValidation,
    maxConcurrency,
    atomicWriteJson,
  });
}

export async function runNativeVerificationBracket(options = {}) {
  const root = realpathSync(resolve(options.root ?? repositoryRoot));
  const scenarioID = options.scenarioID;
  const caseName = options.caseName;
  assert(typeof scenarioID === 'string' && scenarioID.trim(), 'Provide --scenario.');
  assert(typeof caseName === 'string' && caseName.trim(), 'Provide --case.');
  const scenario = registry.find((candidate) => candidate.id === scenarioID);
  assert(scenario, 'Unknown registered scenario: ' + scenarioID);
  const contract = options.contract ?? scenarioCaseFor(scenario, caseName, options.targetObject?.kind === 'read-only-custom');
  const env = options.env ?? process.env;
  const targetPath = options.targetPath ?? options.targetValidation?.configPath ?? null;
  const targetValidation = options.targetValidation ?? {
    scope: 'environment-only',
    registryBinding: 'unbound',
    runtimeDatasetIdentity: 'not-checked',
    configPath: null,
    project: null,
    generation: null,
  };
  const resolvedCases = [{
    scenarioID,
    caseName,
    grep: options.grep,
    checksOnly: options.checksOnly === true,
    targetPath,
    targetObject: options.targetObject ?? null,
    mappedTargetEnvironment: options.mappedTargetEnvironment ?? {},
    env,
    targetValidation,
    contract,
    registryValidation: options.registryValidation ?? registryValidationSummary(registry),
  }];
  const batch = await runNativeVerificationEngine({
    resolvedCases,
    root,
    evidenceParent: options.evidenceParent,
    commandRunner: options.commandRunner ?? runProcess,
    signal: options.signal,
    registryValidation: options.registryValidation ?? registryValidationSummary(registry),
    maxConcurrency: 1,
    atomicWriteJson: options.atomicWriteJson ?? writeJsonAtomically,
  });
  return batch.caseSummaries[0];
}

export async function main(argv, {
  runBracket = runNativeVerificationBracket,
  runBatch = runNativeVerificationBatch,
  write = console.log,
  env = process.env,
  registryForValidation = registry,
  targetLoader = defaultTargetLoader,
  commandRunner = runProcess,
  atomicWriteJson = writeJsonAtomically,
  signal: providedSignal,
  root = repositoryRoot,
} = {}) {
  const options = parseCli(argv);
  if (options.help) {
    write(usage);
    return 0;
  }
  const registryValidation = registryValidationSummary(registryForValidation);
  const canonicalRoot = realpathSync(resolve(root));
  const interruptController = providedSignal ? null : new AbortController();
  const signal = providedSignal ?? interruptController.signal;
  const handleInterrupt = () => interruptController?.abort();
  if (options.batchPath) {
    let batchSummary;
    try {
      const cases = parseBatchManifest(options.batchPath);
      for (const entry of cases) {
        validateBatchCaseSelection(entry.scenario, entry.case, registryForValidation);
      }
      if (interruptController) process.on('SIGINT', handleInterrupt);
      batchSummary = await runBatch({
        cases,
        root: canonicalRoot,
        env,
        evidenceParent: options.evidenceParent,
        commandRunner,
        signal,
        targetLoader,
        registryValidation,
        maxConcurrency: options.maxConcurrency,
        checksOnly: options.checksOnly,
        atomicWriteJson,
      });
    } finally {
      if (interruptController) process.off('SIGINT', handleInterrupt);
    }
    write(JSON.stringify({
      status: batchSummary.status,
      nativeStatus: batchSummary.nativeStatus ?? null,
      integrityStatus: batchSummary.integrityStatus ?? batchSummary.sharedBracket?.integrity?.status ?? 'UNVERIFIED',
      failureCategory: batchSummary.failureCategory ?? null,
      batchId: batchSummary.batchId,
      runDirectory: batchSummary.runDirectory,
      summary: batchSummary.summaryPath,
      durationMs: batchSummary.durationMs ?? null,
      durationScope: batchSummary.durationScope ?? null,
      concurrency: batchSummary.concurrency ?? null,
      sharedBracket: batchSummary.sharedBracket ?? null,
      cases: batchSummary.cases ?? [],
      notes: batchSummary.notes ?? [],
    }, null, 2));
    return signal?.aborted ? 130 : batchSummary.status === 'passed' ? 0 : 1;
  }
  if (!options.checksOnly || options.targetPath) {
    const scenario = registry.find((candidate) => candidate.id === options.scenarioID);
    assert(scenario, 'Unknown registered scenario: ' + options.scenarioID);
    const defaultContract = scenarioCaseFor(scenario, options.caseName);
    const bound = await bindCaseTarget({
      contract: defaultContract,
      targetPath: options.targetPath,
      targetFromEnvironment: options.targetFromEnvironment,
      targetLoader,
      root: canonicalRoot,
      env,
    });
    const contract = scenarioCaseFor(scenario, options.caseName, bound.targetObject?.kind === 'read-only-custom');
    Object.assign(options, bound, {
      contract,
      targetValidation: bound.targetValidation,
      registryValidation,
    });
  }
  options.root = canonicalRoot;
  options.env ??= env;
  options.signal = signal;
  options.registryValidation = registryValidation;
  options.commandRunner = commandRunner;
  options.atomicWriteJson = atomicWriteJson;
  let summary;
  if (interruptController) process.on('SIGINT', handleInterrupt);
  try {
    summary = await runBracket(options);
  } finally {
    if (interruptController) process.off('SIGINT', handleInterrupt);
  }
  if (signal?.aborted) {
    summary.interrupted = true;
    summary.notes ??= [];
    if (!summary.notes.some((note) => /interrupted by SIGINT or an abort request/.test(note))) {
      summary.notes.push('Run was interrupted by SIGINT or an abort request.');
    }
  }
  const focusedGroups = Array.isArray(summary.focusedChecks?.groups)
    ? summary.focusedChecks.groups
    : [];
  const focusedChecks = summary.focusedChecks
    ? {
      status: summary.focusedChecks.status ?? null,
      groups: focusedGroups.map(({ stdoutPreview, stderrPreview, ...group }) => group),
    }
    : null;
  const sourceDimension = summary.integrity?.dimensions?.source;
  const apiIdentityDimension = summary.integrity?.dimensions?.apiBuildIdentity;
  const sourceIntegrity = sourceDimension ? {
    status: sourceDimension.status ?? 'unknown',
    before: sourceDimension.before ?? null,
    after: sourceDimension.after ?? null,
    changedPaths: sourceDimension.changedPaths ?? null,
    evidence: {
      before: summary.evidence?.sourceBefore ?? null,
      after: summary.evidence?.sourceAfter ?? null,
    },
  } : null;
  const apiBuildIdentity = apiIdentityDimension ? {
    status: apiIdentityDimension.status ?? 'unknown',
    before: apiIdentityDimension.before ?? null,
    after: apiIdentityDimension.after ?? null,
    precheckMatches: apiIdentityDimension.precheckMatches ?? null,
    targetUnchanged: apiIdentityDimension.targetUnchanged ?? null,
    evidence: {
      before: summary.evidence?.apiBefore ?? null,
      after: summary.evidence?.apiAfter ?? null,
      precheck: summary.evidence?.precheck ?? null,
    },
  } : null;
  const commandPhases = Object.fromEntries(Object.entries(summary.commands ?? {}).map(([name, command]) => [name, {
    durationMs: command.durationMs ?? null,
    exitCode: command.exitCode ?? null,
    timedOut: Boolean(command.timedOut),
    stdoutPath: command.stdoutPath ?? null,
    stderrPath: command.stderrPath ?? null,
  }]));
  const browserCommand = summary.commands?.playwright;
  const replayArgv = {
    browser: browserCommand ? {
      referenceOnly: true,
      executable: browserCommand.executable ?? null,
      argv: browserCommand.arguments ?? null,
      cwd: browserCommand.cwd ?? null,
      environment: {
        requiredNames: requiredCaptureEnvironment,
        overrideNames: Object.keys(browserCommand.environmentOverrides ?? {}),
        valuesIncluded: false,
      },
    } : null,
    focusedChecks: summary.focusedCheckCoverage === 'registered'
      ? focusedGroups.filter((group) => summary.commands?.['focused-' + group.id]).map((group) => {
        const command = summary.commands?.['focused-' + group.id];
        return {
          id: group.id,
          referenceOnly: true,
          executable: command?.executable ?? null,
          argv: command?.arguments ?? null,
          cwd: command?.cwd ?? null,
          declaredInputsHash: group.declaredInputsHash ?? null,
          inputs: group.inputs ?? [],
        };
      })
      : null,
  };
  write(JSON.stringify({
    status: summary.status,
    scenario: summary.scenario,
    case: summary.case,
    durationMs: summary.durationMs,
    runDirectory: summary.runDirectory,
    summary: summary.evidence.summary,
    report: summary.evidence.domainReport,
    integrity: summary.integrity?.status ?? 'NOT_RUN',
    sourceIntegrity,
    apiBuildIdentity,
    targetValidation: summary.targetValidation,
    registryValidation: summary.registryValidation,
    browserTestPassed: summary.reviewPacket.browserTestPassed,
    domainReportStatus: summary.reviewPacket.domainReportStatus,
    focusedCheckCoverage: summary.focusedCheckCoverage,
    focusedChecks,
    replayArgv,
    commandPhases,
    humanPhaseTimings: {
      diagnosisMs: null,
      implementationMs: null,
      reviewMs: null,
      status: 'unmeasured',
    },
    reviewPacket: summary.reviewPacket ?? null,
    failureCategory: summary.failureCategory,
    firstFailureReason: summary.reviewPacket.firstFailureReason,
    failureContext: summary.reviewPacket.failureContext,
    failedAction: summary.reviewPacket.failedAction,
    lastCompletedAction: summary.reviewPacket.lastCompletedAction,
    pendingOwnedRequests: summary.reviewPacket.pendingOwnedRequests,
  }, null, 2));
  return signal?.aborted ? 130
    : summary.status === 'passed' || summary.status === 'checks-passed' ? 0 : 1;
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
