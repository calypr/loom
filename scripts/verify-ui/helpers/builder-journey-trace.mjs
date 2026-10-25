import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
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
  const replay = spawnSync(process.execPath, replayArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (replay.error) throw replay.error;
  report.target.builderJourneyTrace = {
    ...receiptIdentity,
    tracePath,
    replayReportPath,
    actionLabels: labels,
    status: replay.status === 0 ? 'pass' : 'fail',
  };
  if (replay.status !== 0) {
    const output = `${replay.stdout ?? ''}${replay.stderr ?? ''}`.trim();
    throw new Error(`Builder journey trace replay failed for ${journeyTrace.name}: ${output.split('\n').slice(-20).join('\n')}`);
  }

  const selfTestReportPath = join(report.evidenceDirectory, `${artifactStem}-self-test.json`);
  const selfTestArgs = [
    repositoryPath(repositoryRoot, 'scripts/self-test-builder-traces.mjs'),
    '--journey-only',
    '--traces', tracePath,
    '--contract', contractPath,
  ];
  const selfTest = spawnSync(process.execPath, selfTestArgs, {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  const selfTestOutput = `${selfTest.stdout ?? ''}${selfTest.stderr ?? ''}`.trim();
  const selfTestStatus = !selfTest.error && selfTest.status === 0 ? 'pass' : 'fail';
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
  report.target.builderJourneyTrace = {
    ...report.target.builderJourneyTrace,
    replayStatus: 'pass',
    status: selfTestStatus === 'pass' ? 'pass' : 'fail',
    selfTestStatus,
    selfTestReportPath,
  };
  if (selfTestStatus !== 'pass') {
    throw new Error(`Builder journey trace self-test failed for ${journeyTrace.name}: ${selfTestOutput.split('\n').slice(-30).join('\n')}`);
  }
  return report.target.builderJourneyTrace;
};
