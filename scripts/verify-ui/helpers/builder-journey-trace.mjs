import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { scenarioCaseFor } from '../registry.mjs';

const sourceManifestPath = 'verification/builder-interactions/builder-preview-contract.json';

const canonicalJson = value => Array.isArray(value)
  ? `[${value.map(canonicalJson).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const repositoryPath = (repositoryRoot, path) => {
  const absolutePath = resolve(repositoryRoot, path);
  const repoRelative = relative(repositoryRoot, absolutePath);
  if (!repoRelative || repoRelative.startsWith('..') || resolve(repositoryRoot, repoRelative) !== absolutePath) {
    throw new Error(`Builder journey path escapes repository root: ${path}`);
  }
  return absolutePath;
};

export const builderJourneyTraceBindingFor = (scenarioID, caseName) => {
  const registeredCase = scenarioCaseFor(scenarioID, caseName);
  const binding = registeredCase.journeyTrace;
  if (!binding || typeof binding !== 'object') {
    throw new Error(`Missing Builder journey trace binding for ${scenarioID}/${caseName}`);
  }
  for (const field of ['name', 'model', 'contract']) {
    if (typeof binding[field] !== 'string' || binding[field].trim() === '') {
      throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs ${field}`);
    }
  }
  if (!['builder-group-legacy', 'builder-cda-native-lifecycle'].includes(binding.captureKind)) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs a recognized captureKind`);
  }
  if (!Array.isArray(binding.requiredActions) || binding.requiredActions.length === 0 ||
      binding.requiredActions.some(action => typeof action !== 'string' || action.length === 0)) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} needs requiredActions`);
  }
  if (binding.requiredActionCounts !== undefined &&
      (!binding.requiredActionCounts || typeof binding.requiredActionCounts !== 'object' ||
       Object.entries(binding.requiredActionCounts).some(([action, count]) =>
         typeof action !== 'string' || !Number.isInteger(count) || count < 1))) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} has invalid requiredActionCounts`);
  }
  if (binding.minimumActionCounts !== undefined &&
      (!binding.minimumActionCounts || typeof binding.minimumActionCounts !== 'object' ||
       Object.entries(binding.minimumActionCounts).some(([action, count]) =>
         typeof action !== 'string' || !Number.isInteger(count) || count < 1))) {
    throw new Error(`Builder journey trace binding ${scenarioID}/${caseName} has invalid minimumActionCounts`);
  }
  return binding;
};

export const builderJourneyTraceReceiptIdentity = (binding, traceName, scenarioID, caseName) => {
  if (binding.captureKind === 'builder-group-legacy') return { caseKey: traceName };
  if (binding.captureKind === 'builder-cda-native-lifecycle') {
    return { traceName, nativeCaseKey: `${scenarioID}/${caseName}` };
  }
  throw new Error(`Unknown Builder journey receipt captureKind: ${String(binding.captureKind)}`);
};

const assertNativeReceiptIdentity = (receipt, traceName, nativeCaseKey, stage) => {
  assert(receipt && typeof receipt === 'object' && !Array.isArray(receipt),
    `${stage} must contain a Builder journey receipt object.`);
  assert.equal(receipt.traceName, traceName, `${stage} traceName must match the registered trace.`);
  assert.equal(receipt.nativeCaseKey, nativeCaseKey,
    `${stage} nativeCaseKey must match the selected registered case.`);
  assert(!Object.hasOwn(receipt, 'caseKey'), `${stage} must not include a legacy caseKey alias.`);
};

export const assertNativeBuilderJourneyReplayReceipt = (receipt, { traceName, nativeCaseKey, actionLabels }) => {
  assertNativeReceiptIdentity(receipt, traceName, nativeCaseKey, 'Verifier output');
  assert.equal(receipt.status, 'pending-self-test', 'Verifier receipt must await the mutation self-test.');
  assert.equal(receipt.replayStatus, 'pass', 'Verifier receipt must record a passing replay.');
  assert.equal(receipt.selfTestStatus, 'not-run', 'Verifier receipt must record that the self-test has not run.');
  assert.equal(receipt.selfTestReportPath, null, 'Verifier receipt must not claim a self-test report before it runs.');
  assert.deepEqual(receipt.actionLabels, actionLabels, 'Verifier receipt must retain the captured action chronology.');
  const coverage = receipt.requestCoverage;
  assert(coverage && typeof coverage === 'object' && !Array.isArray(coverage),
    'Verifier receipt must include requestCoverage.');
  for (const field of ['observed', 'modeled', 'unmapped', 'duplicates']) {
    assert(Number.isInteger(coverage[field]) && coverage[field] >= 0,
      `Verifier requestCoverage.${field} must be a non-negative integer.`);
  }
  assert.equal(coverage.modeled, coverage.observed,
    'Verifier request coverage must model every observed request.');
  assert.equal(coverage.unmapped, 0, 'Verifier request coverage must have no unmapped requests.');
  assert.equal(coverage.duplicates, 0, 'Verifier request coverage must have no duplicate bindings.');
  for (const field of ['requiredActionCountsByTrace', 'requiredActionMinimumsByTrace']) {
    assert(receipt[field] && typeof receipt[field] === 'object' && !Array.isArray(receipt[field]) &&
      receipt[field].mismatches && typeof receipt[field].mismatches === 'object' &&
      !Array.isArray(receipt[field].mismatches),
    `Verifier receipt must include ${field}.mismatches.`);
    assert.equal(Object.keys(receipt[field].mismatches).length, 0,
      `Verifier receipt must have no ${field} mismatches.`);
  }
  assert(receipt.unmodeledEvents && typeof receipt.unmodeledEvents === 'object' &&
    Number.isInteger(receipt.unmodeledEvents.total) && receipt.unmodeledEvents.total >= 0 &&
    receipt.unmodeledEvents.unclassified &&
    Number.isInteger(receipt.unmodeledEvents.unclassified.total) && receipt.unmodeledEvents.unclassified.total >= 0,
  'Verifier receipt must include unmodeled event totals.');
  assert.equal(receipt.unmodeledEvents.unclassified.total, 0,
    'Verifier receipt must have no unclassified unmodeled events.');
  return receipt;
};

export const assertNativeBuilderJourneySelfTestReceipt = (receipt, {
  traceName,
  nativeCaseKey,
  tracePath,
  selfTestReportPath,
}) => {
  assertNativeReceiptIdentity(receipt, traceName, nativeCaseKey, 'Self-test output');
  assert.equal(receipt.status, 'pass', 'Self-test receipt status must be pass.');
  assert.equal(receipt.selfTestStatus, 'pass', 'Self-test receipt status must be pass.');
  assert.equal(receipt.tracePath, tracePath, 'Self-test receipt must retain the captured trace path.');
  assert.equal(receipt.selfTestReportPath, selfTestReportPath,
    'Self-test receipt must identify its actual output path.');
  for (const field of ['replayStatus', 'replayReportPath', 'requestCoverage']) {
    assert(!Object.hasOwn(receipt, field), `Self-test receipt must not echo verifier field ${field}.`);
  }
  assert(Array.isArray(receipt.mutationControls) && receipt.mutationControls.length > 0,
    'Self-test receipt must include mutation control results.');
  for (const [index, control] of receipt.mutationControls.entries()) {
    assert(control && typeof control.name === 'string' && control.name.length > 0 && control.status === 'pass',
      `Self-test mutation control ${index} must have a name and pass status.`);
  }
  return receipt;
};

export const finalizeNativeBuilderJourneyReceipt = (verifierReceipt, selfTestReceipt, identity) => {
  assertNativeBuilderJourneyReplayReceipt(verifierReceipt, identity);
  assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, identity);
  return {
    ...verifierReceipt,
    mutationControls: selfTestReceipt.mutationControls,
    status: 'pass',
    replayStatus: 'pass',
    selfTestStatus: 'pass',
    selfTestReportPath: identity.selfTestReportPath,
  };
};

export const captureBuilderJourneyTraceProvenance = ({
  repositoryRoot,
  contractPath,
  reviewedSourceManifestPath = sourceManifestPath,
}) => {
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' });
  if (head.error || head.status !== 0) {
    throw new Error(`Cannot resolve Builder trace source HEAD: ${head.error?.message ?? head.stderr ?? head.stdout}`);
  }

  const manifest = JSON.parse(readFileSync(resolve(repositoryRoot, reviewedSourceManifestPath), 'utf8'));
  assert(Array.isArray(manifest.sources) && manifest.sources.length > 0,
    'Builder journey source manifest must contain source pins.');
  const seenPaths = new Set();
  const sourceEntries = manifest.sources.map((source, index) => {
    assert(source && typeof source.path === 'string' && /^[0-9a-f]{64}$/.test(source.sha256),
      `Builder journey source manifest entry ${index} needs a path and SHA-256.`);
    assert(!seenPaths.has(source.path), `Builder journey source manifest repeats ${source.path}.`);
    seenPaths.add(source.path);
    const actual = createHash('sha256').update(readFileSync(repositoryPath(repositoryRoot, source.path))).digest('hex');
    if (actual !== source.sha256) {
      throw new Error(`Builder trace source pin drift at ${source.path}: expected ${source.sha256}, found ${actual}`);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  const contract = JSON.parse(readFileSync(repositoryPath(repositoryRoot, contractPath), 'utf8'));
  assert.equal(typeof contract.sourceProfile, 'string', 'Builder journey contract needs sourceProfile.');
  assert.match(contract.sourceCommit, /^[0-9a-f]{40}$/, 'Builder journey contract needs its sourceCommit.');
  return {
    sourceProfile: contract.sourceProfile,
    sourceCommit: head.stdout.trim(),
    contractSourceCommit: contract.sourceCommit,
    sourceFingerprint: createHash('sha256').update(canonicalJson(sourceEntries)).digest('hex'),
  };
};

export const writeAndReplayBuilderJourneyTrace = ({
  scenarioID,
  caseName,
  journeyTrace,
  report,
  fixture,
  repositoryRoot,
  reviewedSourceManifestPath,
}) => {
  const binding = builderJourneyTraceBindingFor(scenarioID, caseName);
  assert.equal(journeyTrace.name, binding.name,
    `Captured Builder journey name must match the ${scenarioID}/${caseName} registry binding.`);
  assert.equal(journeyTrace.model, binding.model,
    `Captured Builder journey model must match the ${scenarioID}/${caseName} registry binding.`);
  const contractPath = binding.contract;
  const receiptIdentity = builderJourneyTraceReceiptIdentity(binding, journeyTrace.name, scenarioID, caseName);
  const actions = journeyTrace.actions ?? [];
  const labels = actions.map(action => action.label);
  for (const requiredAction of binding.requiredActions) {
    assert(labels.includes(requiredAction),
      `Captured Builder journey ${journeyTrace.name} is missing registered action ${requiredAction}.`);
  }
  for (const [requiredAction, expectedCount] of Object.entries(binding.requiredActionCounts ?? {})) {
    assert.equal(labels.filter(label => label === requiredAction).length, expectedCount,
      `Captured Builder journey ${journeyTrace.name} needs ${expectedCount} ${requiredAction} actions.`);
  }
  for (const [requiredAction, minimumCount] of Object.entries(binding.minimumActionCounts ?? {})) {
    assert(labels.filter(label => label === requiredAction).length >= minimumCount,
      `Captured Builder journey ${journeyTrace.name} needs at least ${minimumCount} ${requiredAction} actions.`);
  }

  const artifactStem = journeyTrace.name.replaceAll(':', '-');
  const tracePath = process.env.LOOM_JOURNEY_TRACE
    ? resolve(repositoryRoot, process.env.LOOM_JOURNEY_TRACE)
    : join(report.evidenceDirectory, `${artifactStem}-trace.json`);
  const replayReportPath = join(report.evidenceDirectory, `${artifactStem}-tla.json`);
  const tracePayload = {
    schemaVersion: 1,
    ...captureBuilderJourneyTraceProvenance({ repositoryRoot, contractPath, reviewedSourceManifestPath }),
    fixture,
    traces: [journeyTrace],
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(tracePayload, null, 2)}\n`);
  const replayArgs = [
    repositoryPath(repositoryRoot, 'scripts/verify-builder-traces.mjs'),
    '--traces', tracePath,
    '--contract', contractPath,
    '--output', replayReportPath,
    '--quiet',
  ];
  rmSync(replayReportPath, { force: true });
  const replay = spawnSync(process.execPath, replayArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (replay.error) throw replay.error;
  const isNativeLifecycle = binding.captureKind === 'builder-cda-native-lifecycle';
  const nativeCaseKey = `${scenarioID}/${caseName}`;
  report.target.builderJourneyTrace = {
    ...receiptIdentity,
    tracePath,
    replayReportPath,
    actionLabels: labels,
    status: 'fail',
    ...(isNativeLifecycle ? { replayStatus: 'fail', selfTestStatus: 'not-run', selfTestReportPath: null } : {}),
  };
  if (replay.status !== 0) {
    const output = `${replay.stdout ?? ''}${replay.stderr ?? ''}`.trim();
    throw new Error(`Builder journey trace replay failed for ${journeyTrace.name}: ${output.split('\n').slice(-20).join('\n')}`);
  }

  let verifierReceipt;
  if (isNativeLifecycle) {
    const replayReport = JSON.parse(readFileSync(replayReportPath, 'utf8'));
    verifierReceipt = assertNativeBuilderJourneyReplayReceipt(replayReport.builderJourneyTrace, {
      traceName: journeyTrace.name,
      nativeCaseKey,
      actionLabels: labels,
    });
    report.target.builderJourneyTrace = { ...verifierReceipt };
  } else {
    report.target.builderJourneyTrace = {
      ...report.target.builderJourneyTrace,
      status: 'pass',
      replayStatus: 'pass',
    };
  }

  const selfTestReportPath = join(report.evidenceDirectory, `${artifactStem}-self-test.json`);
  const selfTestArgs = [
    repositoryPath(repositoryRoot, 'scripts/self-test-builder-traces.mjs'),
    '--journey-only',
    '--traces', tracePath,
    '--contract', contractPath,
  ];
  if (isNativeLifecycle) selfTestArgs.push('--output', selfTestReportPath);
  if (isNativeLifecycle) rmSync(selfTestReportPath, { force: true });
  const selfTest = spawnSync(process.execPath, selfTestArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const selfTestOutput = `${selfTest.stdout ?? ''}${selfTest.stderr ?? ''}`.trim();
  let selfTestReceipt;
  let selfTestValidationError;
  if (isNativeLifecycle && existsSync(selfTestReportPath)) {
    try {
      selfTestReceipt = JSON.parse(readFileSync(selfTestReportPath, 'utf8'));
      assertNativeBuilderJourneySelfTestReceipt(selfTestReceipt, {
        traceName: journeyTrace.name,
        nativeCaseKey,
        tracePath,
        selfTestReportPath,
      });
    } catch (error) {
      selfTestValidationError = error;
    }
  }
  const selfTestStatus = !selfTest.error && selfTest.status === 0 &&
    (!isNativeLifecycle || (selfTestReceipt && !selfTestValidationError)) ? 'pass' : 'fail';
  if (!isNativeLifecycle) {
    writeFileSync(selfTestReportPath, `${JSON.stringify({
      ...receiptIdentity,
      tracePath,
      contractPath,
      command: [process.execPath, ...selfTestArgs],
      status: selfTestStatus,
      exitCode: selfTest.status,
      error: selfTest.error?.message ?? null,
      output: selfTestOutput,
    }, null, 2)}\n`);
  }
  if (isNativeLifecycle) {
    report.target.builderJourneyTrace = selfTestStatus === 'pass'
      ? finalizeNativeBuilderJourneyReceipt(verifierReceipt, selfTestReceipt, {
          traceName: journeyTrace.name,
          nativeCaseKey,
          actionLabels: labels,
          tracePath,
          selfTestReportPath,
        })
      : {
          ...verifierReceipt,
          ...(Array.isArray(selfTestReceipt?.mutationControls)
            ? { mutationControls: selfTestReceipt.mutationControls }
            : {}),
          status: selfTestStatus,
          replayStatus: 'pass',
          selfTestStatus,
          selfTestReportPath,
        };
  } else {
    report.target.builderJourneyTrace = {
      ...report.target.builderJourneyTrace,
      replayStatus: 'pass',
      status: selfTestStatus,
      selfTestStatus,
      selfTestReportPath,
    };
  }
  if (selfTestStatus !== 'pass') {
    const validationDetail = selfTestValidationError ? `\n${selfTestValidationError.message}` : '';
    throw new Error(`Builder journey trace self-test failed for ${journeyTrace.name}: ${selfTestOutput.split('\n').slice(-30).join('\n')}${validationDetail}`);
  }
  return report.target.builderJourneyTrace;
};
