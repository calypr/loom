#!/usr/bin/env node
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import {
  existsSync,
  copyFileSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const ROOT = realpathSync(resolve(SCRIPT_DIR, '..'));
const MANIFEST_RELATIVE = 'verification/builder-interactions/source-contract.json';
const MODEL_DIR = 'verification/builder-interactions';
const MODEL_MODULE = 'ConstructionLifecycle';
const TLC_VERSION = '1.7.4';
const TLC_SHA256 = '936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88';
const TLC_URL = 'https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar';
const TESTS = [
  'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.unit.test.tsx',
  'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx',
];
const REPLAY_INPUTS = [
  MANIFEST_RELATIVE,
  'verification/builder-interactions/ConstructionLifecycle.tla',
  'verification/builder-interactions/ConstructionLifecycle.cfg',
  'scripts/verify-builder-interactions.mjs',
  '.codex/skills/builder-interaction-formal-verification/SKILL.md',
  ...TESTS.map((file) => 'ui/packages/loom-ui/' + file),
];

function fail(message) {
  throw new Error(message);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function fileBytes(root, relativePath) {
  return readFileSync(resolve(root, relativePath));
}

function readManifest(root) {
  const manifest = JSON.parse(fileBytes(root, MANIFEST_RELATIVE).toString('utf8'));
  if (manifest.schemaVersion !== 1 || !Array.isArray(manifest.sources) || manifest.sources.length === 0) {
    fail('source contract manifest has an unsupported shape');
  }
  return manifest;
}

function sourceHashes(root, manifest) {
  return manifest.sources.map((source) => ({
    path: source.path,
    expected: source.sha256,
    actual: sha256(fileBytes(root, source.path)),
  }));
}

function requirePinnedSources(root, manifest) {
  const drift = sourceHashes(root, manifest).filter((entry) => entry.expected !== entry.actual);
  if (drift.length > 0) {
    const details = drift.map((entry) =>
      '  ' + entry.path + '\n    expected ' + entry.expected + '\n    found    ' + entry.actual,
    ).join('\n');
    fail(
      'modeled Builder source drifted. Review the source-to-model mapping, model, and controller schedules, then deliberately update source-contract.json:\n' +
      details,
    );
  }
}

function gitHead(root) {
  const result = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' });
  if (result.status !== 0) fail('cannot resolve Git HEAD in ' + root + ': ' + (result.stderr || '').trim());
  return result.stdout.trim();
}

function checkMutationReference(referencePath, manifest) {
  const referenceRoot = realpathSync(referencePath);
  if (referenceRoot === ROOT) fail('mutation reference must be a different physical worktree');
  if (gitHead(referenceRoot) !== gitHead(ROOT)) fail('mutation and pristine reference worktrees must have the same Git HEAD');
  const referenceManifestBytes = fileBytes(referenceRoot, MANIFEST_RELATIVE);
  const currentManifestBytes = fileBytes(ROOT, MANIFEST_RELATIVE);
  if (!referenceManifestBytes.equals(currentManifestBytes)) {
    fail('mutation worktree changed the source contract manifest');
  }
  requirePinnedSources(referenceRoot, manifest);

  const changedSources = sourceHashes(ROOT, manifest).filter((entry) => entry.expected !== entry.actual);
  if (changedSources.length !== 1) {
    fail('mutation probe requires exactly one changed fingerprinted production source file; found ' + changedSources.length);
  }
  for (const relativePath of REPLAY_INPUTS) {
    if (!fileBytes(ROOT, relativePath).equals(fileBytes(referenceRoot, relativePath))) {
      fail('mutation probe changed verification input ' + relativePath + '; only one production source file may differ');
    }
  }
  console.log('Mutation isolation verified: one fingerprinted production source differs (' + changedSources[0].path + '); model, tests, manifest, skill, runner, and Git HEAD match the pristine worktree.');
}

function verifyJava() {
  const result = spawnSync('java', ['-version'], { encoding: 'utf8' });
  if (result.error) fail('Java is required to run TLC 1.7.4: ' + result.error.message);
  if (result.status !== 0) fail('Java is required to run TLC 1.7.4: ' + (result.stderr || result.stdout || '').trim());
  const version = (result.stderr || result.stdout || '').trim().split('\n')[0];
  console.log('Java: ' + version);
}

function verifyJar(path) {
  const absolute = realpathSync(path);
  if (!existsSync(absolute)) fail('TLC jar does not exist at ' + absolute);
  const actual = sha256(readFileSync(absolute));
  if (actual !== TLC_SHA256) {
    fail('TLC ' + TLC_VERSION + ' checksum mismatch at ' + absolute + ': expected ' + TLC_SHA256 + ', found ' + actual);
  }
  return absolute;
}

async function acquireTlcJar() {
  if (process.env.TLA2TOOLS_JAR) {
    const path = verifyJar(process.env.TLA2TOOLS_JAR);
    console.log('TLC ' + TLC_VERSION + ': ' + path + ' (pinned SHA-256 verified)');
    return path;
  }

  const cacheDirectory = join(tmpdir(), 'loom-builder-interactions-tools');
  const cachePath = join(cacheDirectory, 'tla2tools-' + TLC_VERSION + '.jar');
  if (existsSync(cachePath)) {
    const path = verifyJar(cachePath);
    console.log('TLC ' + TLC_VERSION + ': ' + path + ' (pinned SHA-256 verified)');
    return path;
  }

  console.log('Downloading TLC ' + TLC_VERSION + ' from the official TLA+ release URL.');
  const response = await fetch(TLC_URL, { signal: AbortSignal.timeout(30000) });
  if (!response.ok) fail('TLC download failed with HTTP ' + response.status);
  const bytes = Buffer.from(await response.arrayBuffer());
  const actual = sha256(bytes);
  if (actual !== TLC_SHA256) {
    fail('downloaded TLC checksum mismatch: expected ' + TLC_SHA256 + ', found ' + actual);
  }
  mkdirSync(cacheDirectory, { recursive: true });
  const temporaryPath = cachePath + '.partial-' + process.pid + '-' + randomUUID();
  writeFileSync(temporaryPath, bytes, { flag: 'wx' });
  renameSync(temporaryPath, cachePath);
  const path = verifyJar(cachePath);
  console.log('TLC ' + TLC_VERSION + ': ' + path + ' (official download and pinned SHA-256 verified)');
  return path;
}

function runCaptured(label, command, arguments_, cwd) {
  console.log('\n' + label);
  console.log(cwd + '$ ' + [command, ...arguments_].join(' '));
  const result = spawnSync(command, arguments_, {
    cwd,
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  return {
    status: result.status,
    error: result.error,
    output: (result.stdout || '') + (result.stderr || ''),
  };
}

function runTlc(jarPath) {
  const runDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-interactions-tlc-'));
  const modelDirectory = join(runDirectory, 'model');
  const metadataDirectory = join(runDirectory, 'states');
  mkdirSync(modelDirectory);
  mkdirSync(metadataDirectory);
  copyFileSync(resolve(ROOT, MODEL_DIR, MODEL_MODULE + '.tla'), join(modelDirectory, MODEL_MODULE + '.tla'));
  copyFileSync(resolve(ROOT, MODEL_DIR, MODEL_MODULE + '.cfg'), join(modelDirectory, MODEL_MODULE + '.cfg'));
  const result = runCaptured(
    'TLC finite-model check',
    'java',
    ['-XX:+UseParallelGC', '-cp', jarPath, 'tlc2.TLC', '-workers', '1', '-metadir', metadataDirectory, '-config', MODEL_MODULE + '.cfg', MODEL_MODULE + '.tla'],
    modelDirectory,
  );
  console.log('TLC model copy, state, and trace artifacts: ' + runDirectory);
  if (result.error || result.status !== 0 || !result.output.includes('Model checking completed. No error has been found.')) {
    return false;
  }
  return true;
}

function runControllerTests() {
  const packageRoot = resolve(ROOT, 'ui/packages/loom-ui');
  const vitestPath = resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs');
  if (!existsSync(vitestPath)) {
    fail('UI dependencies are missing. Install the existing workspace dependencies with npm install in ui/.');
  }
  const reportDirectory = mkdtempSync(join(tmpdir(), 'loom-builder-interactions-vitest-'));
  const reportPath = join(reportDirectory, 'vitest.json');
  const tests = runCaptured(
    'Real useConstructionLifecycle conformance tests',
    process.execPath,
    [realpathSync(vitestPath), 'run', '--reporter=json', '--outputFile=' + reportPath, ...TESTS],
    packageRoot,
  );
  if (tests.error) {
    return { ...tests, passed: false, assertionFailure: false };
  }
  let report;
  try {
    report = JSON.parse(readFileSync(reportPath, 'utf8'));
  } catch {
    console.error('Vitest did not produce its JSON report at ' + reportPath);
    return { ...tests, passed: false, assertionFailure: false };
  }
  const packageTests = TESTS.map((file) => realpathSync(resolve(packageRoot, file)));
  const suitePaths = report.testResults.map((suite) => realpathSync(suite.name));
  const exactSuites = suitePaths.length === packageTests.length &&
    packageTests.every((path) => suitePaths.includes(path));
  const assertions = report.testResults.flatMap((suite) =>
    suite.assertionResults.map((assertion) => ({ suite: realpathSync(suite.name), assertion })),
  );
  const noSkippedOrTodo = report.numPendingTests === 0 &&
    report.numTodoTests === 0 &&
    assertions.every(({ assertion }) => assertion.status === 'passed' || assertion.status === 'failed');
  const everySuiteRanAssertions = report.testResults.every((suite) => suite.assertionResults.length > 0);
  const allTestsReported = assertions.length === report.numTotalTests &&
    report.numPassedTests + report.numFailedTests + report.numPendingTests + report.numTodoTests === report.numTotalTests;
  const passed = tests.status === 0 &&
    report.success === true &&
    exactSuites &&
    everySuiteRanAssertions &&
    allTestsReported &&
    noSkippedOrTodo &&
    report.numTotalTests > 0 &&
    report.numPassedTests === report.numTotalTests &&
    report.numFailedTests === 0;
  const formalSuite = realpathSync(resolve(packageRoot, TESTS[1]));
  const failedAssertions = assertions.filter(({ assertion }) => assertion.status === 'failed');
  const assertionFailuresHaveEvidence = failedAssertions.length > 0 && failedAssertions.every(({ assertion }) =>
    Array.isArray(assertion.failureMessages) &&
    assertion.failureMessages.length > 0 &&
    assertion.failureMessages.every((message) => /AssertionError/.test(message)),
  );
  const assertionFailure = tests.status !== 0 &&
    exactSuites &&
    everySuiteRanAssertions &&
    allTestsReported &&
    noSkippedOrTodo &&
    assertionFailuresHaveEvidence &&
    report.testResults.every((suite) => suite.status === 'passed' || !suite.message?.trim()) &&
    failedAssertions.every(({ suite }) => suite === formalSuite) &&
    report.numFailedTests === failedAssertions.length;
  console.log(
    'Vitest JSON summary: ' + report.numPassedTests + '/' + report.numTotalTests +
    ' passed; ' + report.numPendingTests + ' skipped; ' + report.numTodoTests +
    ' todo. Report: ' + reportPath,
  );
  for (const suite of report.testResults.filter((entry) => entry.status !== 'passed')) {
    console.error(suite.message);
  }
  return {
    ...tests,
    passed,
    assertionFailure,
  };
}

function parseArgs() {
  const args = process.argv.slice(2);
  if (args.length === 0) return { mutationReference: undefined };
  if (args.length === 2 && args[0] === '--allow-source-drift-for-mutation' && args[1]) {
    return { mutationReference: args[1] };
  }
  fail(
    'usage: node scripts/verify-builder-interactions.mjs [--allow-source-drift-for-mutation <pristine-worktree>]',
  );
}

async function main() {
  const options = parseArgs();
  if (realpathSync(process.cwd()) !== ROOT) {
    fail('run this command from the repository root that contains the current Builder implementation: ' + ROOT);
  }
  const manifest = readManifest(ROOT);
  if (options.mutationReference) {
    checkMutationReference(options.mutationReference, manifest);
  } else {
    requirePinnedSources(ROOT, manifest);
    for (const source of sourceHashes(ROOT, manifest)) {
      console.log('Pinned source: ' + source.path + ' SHA-256 ' + source.actual);
    }
  }

  verifyJava();
  const jarPath = await acquireTlcJar();
  const modelPassed = runTlc(jarPath);
  const tests = runControllerTests();

  if (options.mutationReference) {
    if (!modelPassed) fail('the model must pass during a real-source mutation probe');
    if (tests.assertionFailure) {
      console.log('\nPASS mutation probe: the real-source mutant reached and failed a behavioral assertion in the formal hook suite.');
      return;
    }
    if (tests.passed) fail('the behavioral mutant survived the real hook conformance suite');
    fail('the mutation probe did not reach a behavioral assertion; inspect the test output for a harness or module failure');
  }

  if (!modelPassed || !tests.passed) {
    fail('Builder interaction verification failed. Use the TLC trace or failing hook schedule to separate a model mismatch from a source behavior finding.');
  }
  console.log('\nPASS Builder interaction verification.');
}

main().catch((error) => {
  console.error('\nFAIL Builder interaction verification: ' + error.message);
  process.exitCode = 1;
});
