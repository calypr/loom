import assert from 'node:assert/strict';
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

const usage = [
  'Usage:',
  '  node scripts/run-native-verification-bracket.mjs --scenario <id> --case <name> --grep <native-test-regex>',
  '',
  'Load the validated owned LOOM_CDA_* environment before running. The wrapper',
  'requires the official Playwright --list result to select exactly one test.',
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

async function runProcess(command, args, { cwd, env, stdoutPath, stderrPath }) {
  mkdirSync(dirname(stdoutPath), { recursive: true, mode: 0o700 });
  const stdoutFile = createWriteStream(stdoutPath, { flags: 'wx', mode: 0o600 });
  const stderrFile = createWriteStream(stderrPath, { flags: 'wx', mode: 0o600 });
  const stdoutChunks = [];
  const stderrChunks = [];
  const stdoutState = { bytes: 0 };
  const stderrState = { bytes: 0 };
  const started = performance.now();
  let child;
  try {
    child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
  } catch (error) {
    stdoutFile.end();
    stderrFile.end();
    await Promise.allSettled([finished(stdoutFile), finished(stderrFile)]);
    return { exitCode: null, durationMs: performance.now() - started, error: String(error?.message ?? error), stdoutText: '', stderrText: '' };
  }

  child.stdout.pipe(stdoutFile);
  child.stderr.pipe(stderrFile);
  child.stdout.on('data', (chunk) => retainPreview(stdoutChunks, stdoutState, chunk));
  child.stderr.on('data', (chunk) => retainPreview(stderrChunks, stderrState, chunk));
  const closed = await new Promise((resolveClose) => {
    child.once('error', (error) => resolveClose({ exitCode: null, error: String(error?.message ?? error) }));
    child.once('close', (code, signal) => resolveClose({ exitCode: code, signal }));
  });
  await Promise.allSettled([finished(stdoutFile), finished(stderrFile)]);
  return {
    ...closed,
    durationMs: performance.now() - started,
    stdoutText: Buffer.concat(stdoutChunks).toString('utf8'),
    stderrText: Buffer.concat(stderrChunks).toString('utf8'),
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
      grep: { type: 'string' },
      'evidence-parent': { type: 'string' },
      help: { type: 'boolean', short: 'h' },
    },
    allowPositionals: false,
    strict: true,
  });
  if (parsed.values.help) return { help: true };
  assert(parsed.values.scenario?.trim(), 'Provide --scenario.');
  assert(parsed.values.case?.trim(), 'Provide --case.');
  assert(parsed.values.grep?.trim(), 'Provide an explicit --grep native-test regex.');
  return {
    scenarioID: parsed.values.scenario.trim(),
    caseName: parsed.values.case.trim(),
    grep: parsed.values.grep,
    evidenceParent: parsed.values['evidence-parent'],
  };
}

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
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
    dimensions: report.dimensions ?? null,
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
  evidenceParent,
  root = repositoryRoot,
  env = process.env,
  commandRunner = runProcess,
} = {}) {
  assert(typeof scenarioID === 'string' && scenarioID.trim(), 'Provide --scenario.');
  assert(typeof caseName === 'string' && caseName.trim(), 'Provide --case.');
  assert(typeof grep === 'string' && grep.trim(), 'Provide an explicit --grep native-test regex.');
  const canonicalRoot = realpathSync(resolve(root));
  const scenario = registry.find((candidate) => candidate.id === scenarioID);
  assert(scenario, 'Unknown registered scenario: ' + scenarioID);
  const contract = scenarioCaseFor(scenario, caseName);
  const specPath = contract.playwrightTest;
  const specAbsolute = resolve(canonicalRoot, specPath);
  assert(isWithin(canonicalRoot, specAbsolute), 'Registered Playwright spec must be inside the repository root.');
  assert(existsSync(specAbsolute), 'Registered Playwright spec is missing: ' + specPath);

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
    explicitGrep: grep,
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
  const baseEnv = { ...env };
  const exec = async (name, args, overrides = {}) => {
    const stdoutPath = join(logsDirectory, name + '.stdout.log');
    const stderrPath = join(logsDirectory, name + '.stderr.log');
    const runStarted = performance.now();
    let result;
    try {
      result = await commandRunner(process.execPath, args, {
        cwd: canonicalRoot,
        env: { ...baseEnv, ...overrides },
        stdoutPath,
        stderrPath,
      });
    } catch (error) {
      result = { exitCode: null, error: String(error?.message ?? error) };
    }
    saveInjectedLogs(result ?? {}, stdoutPath, stderrPath);
    const stage = {
      executable: process.execPath,
      arguments: args,
      ...(Object.keys(overrides).length ? { environmentOverrides: overrides } : {}),
      exitCode: Number.isInteger(result?.exitCode) ? result.exitCode : null,
      ...(result?.signal ? { signal: result.signal } : {}),
      durationMs: Math.round(Number.isFinite(result?.durationMs) ? result.durationMs : performance.now() - runStarted),
      stdoutPath,
      stderrPath,
      ...(result?.error ? { spawnError: String(result.error).slice(0, 400) } : {}),
    };
    summary.commands[name] = stage;
    return { ...result, exitCode: stage.exitCode, stdoutText: String(result?.stdoutText ?? result?.stdout ?? ''), stderrText: String(result?.stderrText ?? result?.stderr ?? '') };
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
      specPath, '--grep', grep, '--list',
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
      specPath, '--grep', grep,
    ], { PLAYWRIGHT_JSON_OUTPUT_FILE: playwrightEnvPath });
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
  if (matchedCandidates.length === 1) {
    const domainReportData = matchedCandidates[0].data;
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
  summary.reviewPacket = {
    status: summary.status,
    scenario: scenarioID,
    case: caseName,
    listedTest: selection?.cases?.[0] ?? null,
    browserExitCode: browserStage?.exitCode ?? null,
    lifecycleStatus: summary.lifecycle.status,
    integrityStatus: summary.integrity.status,
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

async function main(argv) {
  const options = parseCli(argv);
  if (options.help) {
    console.log(usage);
    return 0;
  }
  const summary = await runNativeVerificationBracket(options);
  console.log(JSON.stringify({
    status: summary.status,
    scenario: summary.scenario,
    case: summary.case,
    durationMs: summary.durationMs,
    runDirectory: summary.runDirectory,
    summary: summary.evidence.summary,
    report: summary.evidence.domainReport,
    integrity: summary.integrity.status,
  }, null, 2));
  return summary.status === 'passed' ? 0 : 1;
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
