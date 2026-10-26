#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { cpus, tmpdir } from 'node:os';
import { basename, dirname, join, relative, resolve, sep } from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const PROBES = 'verification/builder-interactions/mutation-probes.json';
const EVIDENCE = 'verification/builder-interactions/mutation-evidence.json';
const RUNNER_PATH = resolve(ROOT, 'scripts/run-builder-mutation-probe.mjs');
const CLASSIFIER_VERSION = 'selected-assertion-v3';
const args = process.argv.slice(2);
const requested = args.find((arg) => arg === '--all' || !arg.startsWith('--'));
if (!requested || requested === '--help') {
  console.log('usage: node scripts/run-builder-mutation-probe.mjs <probe-id|--all> [--force] [--jobs=N]');
  process.exit(requested ? 0 : 2);
}
const force = args.includes('--force');
const jobsArg = args.find((arg) => arg.startsWith('--jobs='));
const jobs = Math.max(1, Math.min(16, Number(jobsArg?.slice(7) ?? process.env.LOOM_MUTATION_JOBS ?? Math.min(4, cpus().length)) || 1));

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const fileHash = (path) => existsSync(path) ? sha256(readFileSync(path)) : null;
const payload = JSON.parse(readFileSync(resolve(ROOT, PROBES), 'utf8'));
const probes = requested === '--all' ? payload.probes : payload.probes.filter((probe) => probe.id === requested);
if (probes.length === 0) {
  console.error('unknown probe ' + requested);
  process.exit(2);
}

const sourceCommit = await spawnCaptured('git', ['rev-parse', 'HEAD'], { cwd: ROOT }).then((result) => result.output.trim() || null);
const evidence = existsSync(resolve(ROOT, EVIDENCE))
  ? JSON.parse(readFileSync(resolve(ROOT, EVIDENCE), 'utf8'))
  : { schemaVersion: 2, probes: [] };
evidence.schemaVersion = 2;
evidence.sourceCommit = sourceCommit;
evidence.probes ??= [];
const evidenceById = new Map(evidence.probes.map((entry) => [entry.id, entry]));

function spawnCaptured(command, commandArgs, options) {
  return new Promise((resolvePromise) => {
    const child = spawn(command, commandArgs, { ...options, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let bytes = 0;
    const capture = (chunks) => (chunk) => {
      if (bytes >= 32 * 1024 * 1024) return;
      bytes += chunk.length;
      chunks.push(chunk);
    };
    child.stdout.on('data', capture(stdout));
    child.stderr.on('data', capture(stderr));
    const result = (status, error) => {
      const stdoutBytes = Buffer.concat(stdout);
      const stderrBytes = Buffer.concat(stderr);
      resolvePromise({
        status,
        error,
        stdoutBytes,
        stderrBytes,
        stdoutText: stdoutBytes.toString('utf8'),
        stderrText: stderrBytes.toString('utf8'),
        output: Buffer.concat([stdoutBytes, stderrBytes]).toString('utf8'),
      });
    };
    child.on('error', (error) => result(null, error));
    child.on('close', (status) => result(status));
  });
}

function probeHash(probe) {
  return sha256(JSON.stringify({
    runner: probe.runner ?? 'vitest',
    file: probe.file,
    test: probe.test,
    testNamePattern: probe.testNamePattern ?? null,
    find: probe.find,
    replace: probe.replace,
    expectedFailure: probe.expectedFailure,
    equivalent: probe.equivalent ?? null,
  }));
}

function testHash(probe) {
  if ((probe.runner ?? 'vitest') === 'go' || probe.test.startsWith('./')) {
    const packagePath = resolve(ROOT, probe.test ?? './internal/server');
    if (!existsSync(packagePath)) return null;
    if (!statSync(packagePath).isDirectory()) return fileHash(packagePath);
    const files = readdirSync(packagePath).filter((name) => name.endsWith('.go')).sort();
    return sha256(files.map((name) => name + '\n' + readFileSync(join(packagePath, name), 'utf8')).join('\n') + '\n' + (probe.testNamePattern ?? ''));
  }
  const packageRoot = resolve(ROOT, 'ui/packages/loom-ui');
  const testPath = resolve(packageRoot, probe.test);
  if (!testPath.startsWith(packageRoot + sep)) return null;
  return sha256([
    fileHash(testPath) ?? '',
    fileHash(resolve(packageRoot, 'vitest.config.ts')) ?? '',
    fileHash(resolve(packageRoot, 'vitest.setup.ts')) ?? '',
    probe.testNamePattern ?? '',
  ].join('\n'));
}

function hashesFor(probe) {
  return {
    sourceHash: fileHash(resolve(ROOT, probe.file)),
    testHash: testHash(probe),
    probeHash: probeHash(probe),
  };
}

const normalizedCommandFor = (probe, phase) => {
  const runner = probe.runner ?? 'vitest';
  const args = runner === 'go'
    ? ['test', '-json', '-count=1']
    : ['run', probe.test, '--reporter=json', '--outputFile=<json-report>'];
  if (runner === 'go' && phase === 'mutant') args.push('-overlay', '<mutation-overlay>');
  if (probe.testNamePattern) args.push(runner === 'go' ? '-run' : '-t',
    runner === 'go' ? '^(' + probe.testNamePattern + ')$' : probe.testNamePattern);
  if (runner === 'go') {
    args.push(probe.test ?? './internal/server');
    return 'GOCACHE=<shared> go ' + args.join(' ');
  }
  return 'vitest ' + args.join(' ');
};

const commandIdentityFor = (probe, phase) => sha256(normalizedCommandFor(probe, phase));

function isCached(entry, hashes, probe) {
  if (force || !entry || !['killed', 'equivalent'].includes(entry.status)) return false;
  const sameProbeInputs = entry.sourceHash === hashes.sourceHash
    && entry.testHash === hashes.testHash
    && entry.probeHash === hashes.probeHash;
  if (!sameProbeInputs) return false;
  if (entry.status !== 'killed') return true;
  const baseline = entry.baseline;
  const runnerHash = fileHash(RUNNER_PATH);
  return baseline?.status === 'passed'
    && Number.isInteger(baseline.passedTestCount) && baseline.passedTestCount > 0
    && baseline.failedTestCount === 0 && baseline.hashJoinValid === true
    && baseline.sourceHash === hashes.sourceHash
    && baseline.testHash === hashes.testHash
    && baseline.probeHash === hashes.probeHash
    && entry.runnerHash === runnerHash && baseline.runnerHash === runnerHash
    && baseline.runnerHashValid === true
    && entry.classifierVersion === CLASSIFIER_VERSION && baseline.classifierVersion === CLASSIFIER_VERSION
    && entry.commandIdentity === commandIdentityFor(probe, 'mutant')
    && baseline.commandIdentity === commandIdentityFor(probe, 'baseline')
    && entry.command === normalizedCommandFor(probe, 'mutant')
    && baseline.command === normalizedCommandFor(probe, 'baseline')
    && baseline.commandIdentityValid === true;
}

const readVitestReport = (path) => {
  try {
    return { report: JSON.parse(readFileSync(path, 'utf8')), reportError: null };
  } catch (error) {
    return { report: null, reportError: String(error?.message ?? error) };
  }
};

async function runVitest(probe, target, mutated = null) {
  const temporary = mkdtempSync(join(tmpdir(), 'loom-mutation-vitest-'));
  try {
    const mutant = mutated === null ? null : join(temporary, basename(probe.file));
    if (mutant) writeFileSync(mutant, mutated);
    const reportPath = join(temporary, 'vitest-report.json');
    const testArgs = ['run', probe.test, '--reporter=json', '--outputFile=' + reportPath];
    if (probe.testNamePattern) testArgs.push('-t', probe.testNamePattern);
    const environment = { ...process.env };
    delete environment.LOOM_MUTATION_TARGET;
    delete environment.LOOM_MUTATION_SOURCE;
    const result = await spawnCaptured(process.execPath, [
      resolve(ROOT, 'ui/node_modules/vitest/vitest.mjs'),
      ...testArgs,
    ], {
      cwd: resolve(ROOT, 'ui/packages/loom-ui'),
      env: {
        ...environment,
        LOOM_VITEST_CACHE_DIR: join(temporary, 'vite-cache'),
        ...(mutant ? { LOOM_MUTATION_TARGET: target, LOOM_MUTATION_SOURCE: mutant } : {}),
      },
    });
    const recordedArgs = testArgs.map((argument) => argument.startsWith('--outputFile=')
      ? '--outputFile=<json-report>'
      : argument);
    return { ...result, ...readVitestReport(reportPath), command: 'vitest ' + recordedArgs.join(' ') };
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

const goReportFromOutput = (output) => {
  const events = String(output ?? '').split(/\r?\n/).flatMap((line) => {
    try { return [JSON.parse(line)]; } catch { return []; }
  });
  const testEvents = events.filter((event) => typeof event.Test === 'string' && ['pass', 'fail'].includes(event.Action));
  const failedTests = new Set(testEvents.filter((event) => event.Action === 'fail').map((event) => event.Test));
  const failedOutput = events.filter((event) => event.Action === 'output' && failedTests.has(event.Test))
    .map((event) => event.Output).join('');
  return {
    report: {
      success: testEvents.length > 0 && failedTests.size === 0,
      numPassedTests: testEvents.filter((event) => event.Action === 'pass').length,
      numFailedTests: failedTests.size,
      failedOutput,
      events,
    },
    reportError: testEvents.length ? null : 'go test -json emitted no named test results',
  };
};

async function runGo(probe, target, mutated = null) {
  const temporary = mkdtempSync(join(tmpdir(), 'loom-mutation-go-'));
  try {
    const replacement = mutated === null ? null : join(temporary, basename(probe.file));
    const testArgs = ['test', '-json', '-count=1'];
    let overlayPath = null;
    if (replacement) {
      writeFileSync(replacement, mutated);
      overlayPath = join(temporary, 'overlay.json');
      writeFileSync(overlayPath, JSON.stringify({ Replace: { [target]: replacement } }));
      testArgs.push('-overlay', overlayPath);
    }
    const goCache = process.env.LOOM_MUTATION_GO_CACHE
      ?? (existsSync(resolve(ROOT, 'ui/node_modules'))
        ? resolve(ROOT, 'ui/node_modules/.cache/loom-mutation-go')
        : join(tmpdir(), 'loom-mutation-go-cache'));
    mkdirSync(goCache, { recursive: true });
    if (probe.testNamePattern) testArgs.push('-run', '^(' + probe.testNamePattern + ')$');
    testArgs.push(probe.test ?? './internal/server');
    const result = await spawnCaptured('go', testArgs, {
      cwd: ROOT,
      env: { ...process.env, GOCACHE: goCache },
    });
    const recordedArgs = testArgs.map((argument) => argument === overlayPath ? '<mutation-overlay>' : argument);
    return { ...result, ...goReportFromOutput(result.stdoutText), command: 'GOCACHE=<shared> go ' + recordedArgs.join(' ') };
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

const diagnosticText = (message) => String(message ?? '').split(/\r?\n/)
  .filter((line) => !/^\s*(?:>\s*)?\d+\s*[|│]/u.test(line) && !/^\s*\^+\s*$/u.test(line))
  .join('\n')
  .split(/\n\s*(?:at\s|❯\s)/u, 1)[0];

const matchesSelectedTest = (fullName, pattern) => {
  if (!pattern) return true;
  try { return new RegExp(pattern).test(String(fullName ?? '')); }
  catch { return String(fullName ?? '').includes(pattern); }
};

const matcherAssertionLines = (probe) => {
  if (!/^(?:toHaveBeenCalledWith|toHaveBeenLastCalledWith|toHaveBeenCalledTimes)$/u.test(probe.expectedFailure ?? '') ||
      !probe.testNamePattern || (probe.runner ?? 'vitest') !== 'vitest') return [];
  const testPath = resolve(ROOT, 'ui/packages/loom-ui', probe.test);
  if (!existsSync(testPath)) return [];
  const sourceLines = readFileSync(testPath, 'utf8').split(/\r?\n/);
  let selectedTest = false;
  const lines = [];
  for (let index = 0; index < sourceLines.length; index += 1) {
    const declaration = sourceLines[index].match(/\b(?:it|test)\s*\(\s*(['"`])([^'"`]+)\1/u);
    if (declaration) selectedTest = matchesSelectedTest(declaration[2], probe.testNamePattern);
    if (selectedTest && sourceLines[index].includes(probe.expectedFailure)) lines.push(index + 1);
  }
  return lines;
};

const sourceLocationLines = (message, probe, location) => {
  const lines = [];
  if (Number.isInteger(location?.line)) lines.push(location.line);
  const fileName = basename(probe.test);
  const escaped = fileName.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&');
  for (const line of String(message ?? '').split(/\r?\n/)) {
    if (!/^\s*(?:at\s|❯\s)/u.test(line)) continue;
    const match = line.match(new RegExp(escaped + ':(\\d+):(\\d+)', 'u'));
    if (match) lines.push(Number(match[1]));
  }
  return lines;
};

const failureMessageText = (messages) => Array.isArray(messages)
  ? messages.join('\n')
  : String(messages ?? '');

const expectedFailureObserved = (probe, report) => {
  if (!report || !Number.isInteger(report.numFailedTests) || report.numFailedTests < 1) return false;
  const failures = [];
  for (const suite of report.testResults ?? []) {
    for (const assertion of suite.assertionResults ?? []) {
      if (assertion.status === 'failed' && matchesSelectedTest(assertion.fullName, probe.testNamePattern)) {
        failures.push({ message: failureMessageText(assertion.failureMessages), location: assertion.location });
      }
    }
  }
  if (Array.isArray(report.events)) {
    const failed = new Set(report.events.filter((event) => event.Test && event.Action === 'fail').map((event) => event.Test));
    const relevant = report.events.filter((event) => event.Action === 'output' && failed.has(event.Test));
    failures.push({ message: relevant.map((event) => event.Output).join(''), location: null });
  }
  const expectedLines = matcherAssertionLines(probe);
  return failures.some(({ message, location }) => {
    const diagnostic = diagnosticText(message);
    if (diagnostic.includes(probe.expectedFailure)) return true;
    if (expectedLines.length === 0) return false;
    const lines = sourceLocationLines(message, probe, location);
    const looksLikeMatcherAssertion = /\b(?:AssertionError|expected|Received|Expected)\b/u.test(diagnostic)
      && !/(?:TestingLibraryElementError|Unable to find|No element found)/iu.test(diagnostic);
    return looksLikeMatcherAssertion && lines.some((line) => expectedLines.includes(line));
  });
};

const baselineSummary = (probe, result, hashes, runnerHash) => {
  const report = result.report;
  const passedTestCount = Number(report?.numPassedTests ?? 0);
  const failedTestCount = Number(report?.numFailedTests ?? 0);
  const passed = result.status === 0 && result.reportError === null && report?.success === true &&
    Number.isInteger(passedTestCount) && passedTestCount > 0 && failedTestCount === 0;
  return {
    status: passed ? 'passed' : 'failed',
    passedTestCount,
    failedTestCount,
    sourceHash: hashes.sourceHash,
    testHash: hashes.testHash,
    probeHash: hashes.probeHash,
    hashJoinValid: Boolean(hashes.sourceHash && hashes.testHash && hashes.probeHash),
    runnerHash,
    classifierVersion: CLASSIFIER_VERSION,
    commandIdentity: commandIdentityFor(probe, 'baseline'),
    commandIdentityValid: result.command === normalizedCommandFor(probe, 'baseline'),
    command: result.command,
    ...(result.reportError ? { reportError: result.reportError } : {}),
  };
};

const sameHashJoin = (left, right) => left.sourceHash === right.sourceHash &&
  left.testHash === right.testHash && left.probeHash === right.probeHash &&
  Boolean(left.sourceHash && left.testHash && left.probeHash);

const retainProbeLogs = (probe, phase, result) => {
  const directory = process.env.LOOM_MUTATION_LOG_DIRECTORY?.trim();
  if (!directory) return;
  const outputDirectory = resolve(directory);
  const relativeToRoot = relative(ROOT, outputDirectory);
  if (relativeToRoot === '' || (relativeToRoot !== '..' && !relativeToRoot.startsWith('..' + sep) && !relativeToRoot.startsWith(sep))) {
    throw new Error('LOOM_MUTATION_LOG_DIRECTORY must be outside the repository root');
  }
  mkdirSync(outputDirectory, { recursive: true });
  const base = join(outputDirectory, probe.id + '.' + phase);
  writeFileSync(base + '.stdout.log', result.stdoutBytes ?? Buffer.from(result.stdoutText ?? ''));
  writeFileSync(base + '.stderr.log', result.stderrBytes ?? Buffer.from(result.stderrText ?? ''));
  if (result.report) writeFileSync(base + '.report.json', JSON.stringify(result.report, null, 2) + '\n');
  else if (result.reportError) writeFileSync(base + '.report-error.txt', result.reportError + '\n');
};

async function runProbe(probe) {
  const runner = probe.runner ?? 'vitest';
  if (!['vitest', 'go'].includes(runner)) return { probe, error: 'unknown runner ' + runner };
  const target = resolve(ROOT, probe.file);
  if (!existsSync(target)) return { probe, error: 'target does not exist: ' + probe.file };
  const source = readFileSync(target, 'utf8');
  if (!source.includes(probe.find)) return { probe, error: 'mutation target not found' };
  const mutated = source.replace(probe.find, probe.replace);
  if (mutated === source) return { probe, error: 'mutation did not change the source' };
  if (!probe.expectedFailure) return { probe, error: 'expectedFailure is required' };

  const hashes = hashesFor(probe);
  const runnerHash = fileHash(RUNNER_PATH);
  const baselineResult = runner === 'go'
    ? await runGo(probe, target)
    : await runVitest(probe, target);
  retainProbeLogs(probe, 'baseline', baselineResult);
  if (baselineResult.error) return {
    probe,
    errorKind: 'baseline-process-failed',
    error: 'unmodified baseline process failed: ' + baselineResult.error.message,
  };
  const baselineHashesAfter = hashesFor(probe);
  const baseline = baselineSummary(probe, baselineResult, hashes, runnerHash);
  baseline.hashJoinValid = sameHashJoin(hashes, baselineHashesAfter) && sameHashJoin(hashes, baseline);
  baseline.runnerHashValid = runnerHash === fileHash(RUNNER_PATH);
  if (baseline.status !== 'passed' || !baseline.hashJoinValid || !baseline.runnerHashValid ||
      !baseline.commandIdentityValid) {
    return {
      probe,
      errorKind: 'baseline-failed',
      baseline,
      error: 'unmodified baseline must pass at least one test with stable source/test/probe, runner, and command identities; ' +
        JSON.stringify({ baseline, status: baselineResult.status, report: baselineResult.report ?? null }),
    };
  }

  const result = runner === 'go'
    ? await runGo(probe, target, mutated)
    : await runVitest(probe, target, mutated);
  retainProbeLogs(probe, 'mutant', result);
  if (result.error) return { probe, errorKind: 'mutant-process-failed', error: result.error.message };
  const currentHashes = hashesFor(probe);
  const mutantCommandIdentity = commandIdentityFor(probe, 'mutant');
  if (!sameHashJoin(hashes, currentHashes) || !sameHashJoin(hashes, baseline) ||
      runnerHash !== fileHash(RUNNER_PATH) || result.command !== normalizedCommandFor(probe, 'mutant')) {
    return { probe, errorKind: 'identity-changed', error: 'source/test/probe/runner/command identity changed during baseline or mutant execution' };
  }
  const killed = result.status !== 0 && expectedFailureObserved(probe, result.report);
  if (result.status !== 0 && !killed) {
    return {
      probe,
      errorKind: 'mutant-failed',
      error: 'test failed without the expected assertion `' + probe.expectedFailure + '`; output:\n' + result.output.split('\n').slice(0, 40).join('\n'),
    };
  }
  const status = killed ? 'killed' : probe.equivalent ? 'equivalent' : 'survived';
  const failureDiagnostics = (result.report?.testResults ?? []).flatMap((suite) => suite.assertionResults ?? [])
    .filter((assertion) => assertion.status === 'failed')
    .flatMap((assertion) => assertion.failureMessages ?? [])
    .map(diagnosticText);
  const failureLine = failureDiagnostics.find((message) => message.includes(probe.expectedFailure)) ??
    failureDiagnostics.find((message) => message.trim()) ??
    (result.report?.failedOutput ? diagnosticText(result.report.failedOutput) : '');
  return {
    probe,
    entry: {
      id: probe.id,
      status,
      runner,
      source: probe.file,
      sourceHash: hashes.sourceHash,
      testHash: hashes.testHash,
      probeHash: hashes.probeHash,
      sourceCommit,
      modelAction: probe.modelAction ?? null,
      ...(probe.modelActionRationale ? { modelActionRationale: probe.modelActionRationale } : {}),
      mutation: 'Replace `' + probe.find.replaceAll('\n', '\\n') + '` with `' + probe.replace.replaceAll('\n', '\\n') + '`.',
      command: result.command,
      expectedFailure: probe.expectedFailure,
      exitCode: result.status,
      failure: failureLine.trim(),
      baseline,
      runnerHash,
      classifierVersion: CLASSIFIER_VERSION,
      commandIdentity: mutantCommandIdentity,
      ...(probe.equivalent ? { equivalent: probe.equivalent } : {}),
    },
  };
}

const pending = [];
const skipped = [];
for (const probe of probes) {
  const hashes = hashesFor(probe);
  const cached = evidenceById.get(probe.id);
  if (isCached(cached, hashes, probe)) skipped.push(probe);
  else pending.push(probe);
}

const results = new Array(pending.length);
let next = 0;
await Promise.all(Array.from({ length: Math.min(jobs, pending.length) }, async () => {
  while (true) {
    const index = next++;
    if (index >= pending.length) return;
    results[index] = await runProbe(pending[index]);
  }
}));

let failures = 0;
for (const result of results) {
  if (result.error) {
    failures += 1;
    const previous = evidenceById.get(result.probe.id);
    if (previous?.status === 'killed') {
      evidenceById.set(result.probe.id, {
        ...previous,
        status: result.errorKind === 'baseline-failed' ? 'baseline-failed' : 'verification-failed',
        previousStatus: previous.status,
        revalidationErrorKind: result.errorKind ?? 'verification-failed',
        revalidationFailure: result.error,
        ...(result.baseline ? { failedBaseline: result.baseline } : {}),
      });
    }
    console.error('FAIL ' + result.probe.id + ': ' + result.error);
  } else {
    evidenceById.set(result.probe.id, result.entry);
    console.log(result.entry.status.toUpperCase() + ' ' + result.probe.id);
  }
}
for (const probe of skipped) console.log('SKIP ' + probe.id + ' (unchanged evidence)');
evidence.probes = [...evidenceById.values()];
writeFileSync(resolve(ROOT, EVIDENCE), JSON.stringify(evidence, null, 2) + '\n');
process.exit(failures === 0 ? 0 : 1);
