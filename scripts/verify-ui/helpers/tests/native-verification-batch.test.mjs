import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry, scenarioCaseFor } from '../../registry.mjs';
import { main, runNativeVerificationBracket } from '../../../run-native-verification-bracket.mjs';

const root = resolve(fileURLToPath(new URL('../../../..', import.meta.url)));
const cases = [
  { scenario: 'builder-controls', case: 'first-table', target: { mode: 'environment' } },
  { scenario: 'builder-load', case: 'list', target: { mode: 'environment' } },
];
const unannotatedCase = {
  scenario: 'builder-controls',
  case: 'tables',
  target: { mode: 'environment' },
};
const customContractCase = {
  scenario: 'viewer-query',
  case: 'output',
  target: { mode: 'environment' },
};
const configTargetCases = [
  { scenario: 'root-quantity-pivot', case: 'full-population-lifecycle', target: { mode: 'config', path: 'owned-target.json' } },
  { scenario: 'root-quantity-pivot', case: 'related-text-only-full-population-lifecycle', target: { mode: 'config', path: 'owned-target.json' } },
];
const baseEnvironment = {
  LOOM_CDA_SOURCE_ROOT: root,
  LOOM_CDA_DATASET_DIR: root,
  LOOM_CDA_PROJECT: 'owned-project',
  LOOM_CDA_GENERATION: 'generation-1',
  LOOM_CDA_API_ORIGIN: 'http://127.0.0.1:8188',
  LOOM_CDA_UI_ORIGIN: 'http://127.0.0.1:30008',
  LOOM_CDA_API_CONTAINER: 'owned-api',
  LOOM_CDA_COMPOSE_PROJECT: 'owned-compose',
  LOOM_CDA_ARANGO_CONTAINER: 'owned-arango',
  LOOM_CDA_CLICKHOUSE_CONTAINER: 'owned-clickhouse',
  LOOM_CDA_NO_AUTH: '1',
  LOOM_DEV_SOURCE_ROOT: root,
  LOOM_DEV_PROJECT: 'owned-project',
  LOOM_DEV_GENERATION: 'generation-1',
  LOOM_DEV_API_URL: 'http://127.0.0.1:8188',
  LOOM_DEV_UI_URL: 'http://127.0.0.1:30008',
  LOOM_DEV_API_PORT: '8188',
  LOOM_DEV_UI_PORT: '30008',
  LOOM_CDA_API_PORT: '8188',
  LOOM_CDA_UI_PORT: '30008',
};
const buildIdentity = 'a'.repeat(64) + ':' + 'a'.repeat(64) + ':' + 'b'.repeat(64);

function temporaryDirectory(prefix = 'native-batch-test-') {
  return mkdtempSync(join(tmpdir(), prefix));
}

function writeJson(path, value) {
  mkdirSync(join(path, '..'), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
}

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

function existingFilePaths(value, result = []) {
  if (typeof value === 'string' && existsSync(value)) result.push(value);
  else if (Array.isArray(value)) value.forEach((child) => existingFilePaths(child, result));
  else if (value && typeof value === 'object') Object.values(value).forEach((child) => existingFilePaths(child, result));
  return result;
}

function manifestFile(directory, manifest, name = 'batch.json') {
  const path = join(directory, name);
  writeJson(path, manifest);
  return path;
}

function scenarioContract(scenarioID, caseName) {
  const scenario = registry.find((entry) => entry.id === scenarioID);
  assert.ok(scenario, `registered scenario ${scenarioID}`);
  return scenarioCaseFor(scenario, caseName);
}

function createCaseSourceTree(directory, contract) {
  const rootPath = join(directory, 'source');
  const paths = [contract.playwrightTest];
  for (const group of contract.focusedChecks ?? []) {
    const offset = group.command[0] === 'node-test' ? 1 : 4;
    paths.push(...group.command.slice(offset), ...(group.sourceFiles ?? []));
  }
  for (const path of new Set(paths)) {
    const absolutePath = join(rootPath, path);
    mkdirSync(join(absolutePath, '..'), { recursive: true, mode: 0o700 });
    writeFileSync(absolutePath, 'fixture input\n', { mode: 0o600 });
  }
  return rootPath;
}

function batchCommandRunner({
  events = [],
  browserFailures = new Set(),
  browserTimeouts = new Set(),
  missingDiagnosticReports = new Set(),
  failedDiagnosticReports = new Set(),
  disconnectedRequiredCheckReports = new Set(),
  customRequiredCheckReports = new Set(),
  focusedFailures = new Set(),
  selectionFailures = new Set(),
  ambiguousSelections = new Set(),
  spawnFailures = new Set(),
  sharedFailure = null,
  interruptController = null,
  browserGate = null,
  observedEnvironments = [],
  sourceChanged = false,
  apiChanged = false,
  mountsFailed = false,
  malformedAfterSource = false,
  missingAfterDocs = false,
} = {}) {
  const knownCases = [...cases, unannotatedCase, customContractCase, ...configTargetCases].map(({ scenario, case: caseName }) => ({
    key: `${scenario}/${caseName}`,
    scenario,
    caseName,
    contract: scenarioContract(scenario, caseName),
  }));
  let activeCase = null;

  return async (_command, args, options = {}) => {
    const { env = {}, cwd, stdoutPath, stderrPath } = options;
    const command = args[0];
    const flag = (name) => args[args.indexOf(name) + 1];
    const specArgument = (contract) => contract.playwrightTest.replace(/^scripts\//, '');
    const matchesSpec = (contract) => args.includes(contract.playwrightTest)
      || args.includes(specArgument(contract));
    const matchesSelection = (contract) => matchesSpec(contract)
      && (!contract.playwrightGrep || contract.playwrightGrep === flag('--grep'));
    const keyForArgs = () => knownCases.find(({ contract }) => matchesSelection(contract))?.key
      ?? activeCase?.key ?? null;
    if (cwd) assert.equal(cwd, root, 'native commands stay in the current checkout');

    if (command === '--test') {
      const selectedFocusedCase = knownCases.find(({ contract }) => (contract.focusedChecks ?? []).some((group) => {
        const offset = group.command[0] === 'node-test' ? 1 : 4;
        return group.command.slice(offset).some((path) => args.includes(path));
      }));
      const key = keyForArgs() ?? selectedFocusedCase?.key ?? activeCase?.key ?? null;
      events.push(`focused:${key}:start`);
      const failed = focusedFailures.has(key);
      events.push(`focused:${key}:close`);
      return { exitCode: failed ? 1 : 0, stdoutText: failed ? 'focused assertion failed\n' : '', stderrText: '' };
    }

    if (command === 'scripts/node_modules/@playwright/test/cli.js') {
      const selected = knownCases.find(({ contract }) => matchesSelection(contract))
        ?? knownCases.find(({ contract }) => matchesSpec(contract));
      assert.ok(selected, `a registered Playwright spec is selected: ${args.join(' ')}`);
      activeCase = selected;
      if (args.includes('--list')) {
        events.push(`selection:${selected.key}`);
        const specFile = basename(selected.contract.playwrightTest);
        const title = `Selected native case for ${selected.caseName}`;
        const listedFile = selectionFailures.has(selected.key) ? 'unregistered.spec.mjs' : specFile;
        if (ambiguousSelections.has(selected.key)) {
          return {
            exitCode: 0,
            stdoutText: `Listing tests:\n  ${specFile}:12:3 › Test suite › ${title}\n  ${specFile}:13:3 › Test suite › ${title} duplicate\nTotal: 2 tests in 1 file\n`,
            stderrText: '',
          };
        }
        return {
          exitCode: 0,
          stdoutText: `Listing tests:\n  ${listedFile}:12:3 › Test suite › ${title}\nTotal: 1 test in 1 file\n`,
          stderrText: '',
        };
      }

      const key = selected.key;
      events.push(`browser:${key}:start`);
      if (spawnFailures.has(key)) throw new Error('controlled browser spawn exception');
      observedEnvironments.push({ key, env: { ...env } });
      if (browserGate) await browserGate.arrive(key);
      if (interruptController && key === knownCases[0].key) interruptController.abort();
      const timedOut = browserTimeouts.has(key);
      const failed = browserFailures.has(key);
      const browserStatus = failed ? 'failed' : 'passed';
      const results = [{ status: browserStatus, retry: 0 }];
      const reportPath = env.PLAYWRIGHT_JSON_OUTPUT_FILE;
      assert.ok(reportPath, 'Playwright receives an isolated report path');
      const outputDirectory = flag('--output');
      const domainReportPath = join(outputDirectory, 'native-domain-report.json');
      const selectedSpec = {
        title: `Selected native case for ${selected.caseName}`,
        file: basename(selected.contract.playwrightTest),
        line: 12,
        column: 3,
        tests: [{ status: failed ? 'unexpected' : 'expected', results }],
      };
      if (!missingDiagnosticReports.has(key)) {
        const reportStatus = failedDiagnosticReports.has(key) ? 'failed' : 'passed';
        const reportChecks = customRequiredCheckReports.has(key)
          ? scenarioCaseFor(registry.find((entry) => entry.id === selected.scenario), selected.caseName, true).requiredChecks
          : selected.contract.requiredChecks;
        writeJson(domainReportPath, {
          schemaVersion: 2,
          scenario: selected.scenario,
          case: selected.caseName,
          status: reportStatus,
          target: { kind: customRequiredCheckReports.has(key) ? 'read-only-custom' : 'owned-dev-fixture' },
          dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
          requiredChecks: disconnectedRequiredCheckReports.has(key)
            ? reportChecks.slice(1)
            : reportChecks,
          missingRequiredChecks: reportStatus === 'failed' ? reportChecks.slice(0, 1) : [],
          assertions: reportChecks.map((name, index) => ({
            name,
            status: reportStatus === 'failed' && index === 0 ? 'failed' : 'passed',
          })),
          actions: [],
        });
        results[0].attachments = [{
          name: 'loom-verification-report.json',
          contentType: 'application/json',
          path: domainReportPath,
        }];
      }
      writeJson(reportPath, {
        errors: [],
        suites: [{
          title: basename(selected.contract.playwrightTest),
          file: basename(selected.contract.playwrightTest),
          specs: [],
          suites: [{
            title: 'Test suite',
            file: basename(selected.contract.playwrightTest),
            suites: [],
            specs: [selectedSpec],
          }],
        }],
      });
      if (timedOut) {
        events.push(`browser:${key}:close`);
        events.push(`child-drained:${key}`);
        return { exitCode: null, timedOut: true, stdoutText: '', stderrText: '' };
      }
      events.push(`browser:${key}:close`);
      events.push(`child-drained:${key}`);
      return { exitCode: failed ? 1 : 0, stdoutText: '', stderrText: '' };
    }

    if (command === 'scripts/owned-stack-verification.mjs') {
      const mode = flag('--mode');
      const output = flag('--output');
      if (mode === 'precheck') {
        events.push('shared:precheck');
        if (sharedFailure === 'precheck') return { exitCode: 1, stdoutText: '', stderrText: '' };
        writeJson(output, {
          exitCode: 0,
          fresh: true,
          sourceDigestMatchesCurrentMountedSource: true,
          runningBinaryMatchesRecordedBuild: true,
          targetContainer: env.LOOM_CDA_API_CONTAINER,
          apiBuildIdentity: buildIdentity,
        });
        return { exitCode: 0, stdoutText: '', stderrText: '' };
      }
      const phase = output.includes('health-before') ? 'before' : 'after';
      events.push(`shared:health-${phase}`);
      if (sharedFailure === `health-${phase}`) return { exitCode: 1, stdoutText: '', stderrText: '' };
      writeJson(output, {
        status: 'PASS',
        apiBuildIdentity: buildIdentity,
        samples: Array.from({ length: 3 }, () => ({
          apiStatus: 200,
          uiStatus: 200,
          uiHasDocument: true,
          apiBuildIdentity: buildIdentity,
        })),
      });
      return { exitCode: 0, stdoutText: '', stderrText: '' };
    }

    if (command === 'scripts/capture-owned-verification.mjs') {
      const phase = flag('--phase');
      events.push(`shared:capture-${phase}`);
      if (sharedFailure === `capture-${phase}`) return { exitCode: 1, stdoutText: '', stderrText: '' };
      const sourceRoot = env.LOOM_CDA_SOURCE_ROOT;
      const apiPort = Number(new URL(env.LOOM_CDA_API_ORIGIN).port);
      const uiPort = Number(new URL(env.LOOM_CDA_UI_ORIGIN).port);
      const target = {
        project: env.LOOM_CDA_PROJECT,
        generation: env.LOOM_CDA_GENERATION ?? 'generation-1',
        composeProject: env.LOOM_CDA_COMPOSE_PROJECT,
        apiContainer: env.LOOM_CDA_API_CONTAINER,
        uiContainer: 'owned-ui',
        arangoContainer: env.LOOM_CDA_ARANGO_CONTAINER,
        clickhouseContainer: env.LOOM_CDA_CLICKHOUSE_CONTAINER,
        apiPort,
        uiPort,
        sourceRoot,
      };
      const outputs = [
        ['--source-output', {
          phase,
          root: sourceRoot,
          fingerprint: malformedAfterSource && phase === 'after'
            ? { files: 1 }
            : { sha256: sourceChanged && phase === 'after' ? '9'.repeat(64) : 'c'.repeat(64), files: 1 },
          manifest: { [sourceChanged && phase === 'after' ? 'internal/changed.go' : 'internal/fake.go']: 'd'.repeat(64) },
        }],
        ['--docs-output', {
          phase,
          root: sourceRoot,
          fingerprint: { sha256: 'e'.repeat(64), files: 1 },
          manifest: { 'docs/fake.md': 'f'.repeat(64) },
        }],
        ['--api-output', { phase, apiBuildIdentity: apiChanged && phase === 'after' ? 'f'.repeat(64) : buildIdentity, target }],
        ['--mount-output', { phase, status: mountsFailed && phase === 'after' ? 'FAIL' : 'PASS', target }],
      ];
      for (const [name, value] of outputs) {
        if (missingAfterDocs && phase === 'after' && name === '--docs-output') continue;
        writeJson(flag(name), value);
      }
      if (sharedFailure === 'write-capture-before' && phase === 'before') {
        throw new Error('controlled shared evidence write failure');
      }
      return { exitCode: 0, stdoutText: '', stderrText: '' };
    }

    if (stdoutPath && !existsSync(stdoutPath)) writeFileSync(stdoutPath, '', { mode: 0o600 });
    if (stderrPath && !existsSync(stderrPath)) writeFileSync(stderrPath, '', { mode: 0o600 });
    events.push(`unexpected:${command}`);
    return { exitCode: 99, stdoutText: '', stderrText: '' };
  };
}

function startTestBatch(directory, {
  manifestCases = cases,
  environment = baseEnvironment,
  commandOptions = {},
  checksOnly = false,
  signal,
  atomicWriteJson,
  targetLoader,
  concurrency,
} = {}) {
  const manifestPath = manifestFile(directory, { schemaVersion: 1, cases: manifestCases });
  const events = commandOptions.events ?? [];
  const commandRunner = batchCommandRunner({ ...commandOptions, events });
  const printed = [];
  const args = [
    '--batch', manifestPath,
    '--evidence-parent', directory,
  ];
  if (checksOnly) args.push('--checks-only');
  if (concurrency !== undefined) args.push('--concurrency', String(concurrency));
  const promise = main(args, {
    env: { ...environment },
    commandRunner,
    ...(signal ? { signal } : {}),
    ...(atomicWriteJson ? { atomicWriteJson } : {}),
    ...(targetLoader ? { targetLoader } : {}),
    write: (value) => printed.push(value),
  });
  return { promise, printed, events, manifestPath };
}

async function writeTestBatch(directory, options = {}) {
  const started = startTestBatch(directory, options);
  const exitCode = await started.promise;
  return { ...started, exitCode };
}

test('batch CLI help and exclusivity preserve the single-case command shape', async () => {
  const help = [];
  assert.equal(await main(['--help'], { write: (value) => help.push(value) }), 0);
  assert.match(help.join('\n'), /--batch <manifest\.json>/);
  assert.match(help.join('\n'), /--concurrency 1\.\.3/);

  await assert.rejects(main(['--batch', '/tmp/batch.json', '--scenario', 'builder-controls']),
    undefined);
});

test('checks-only batches run each focused group without starting registered browser or shared stages', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, { checksOnly: true });

  assert.equal(exitCode, 0);
  const batch = JSON.parse(printed.at(-1));
  assert.equal(batch.status, 'passed');
  assert.equal(batch.nativeStatus, 'not-run');
  assert.equal(batch.integrityStatus, 'NOT_RUN');
  assert.equal(batch.sharedBracket.status, 'NOT_RUN');
  assert.equal(batch.cases.length, 2);
  assert.deepEqual(batch.cases.map(({ scenario, case: caseName, status }) => [scenario, caseName, status]), [
    ['builder-controls', 'first-table', 'checks-passed'],
    ['builder-load', 'list', 'checks-passed'],
  ]);
  for (const entry of batch.cases) {
    const summary = readJson(entry.summaryPath);
    assert.equal(summary.mode, 'checks-only');
    assert.equal(summary.nativeStatus, 'not-run');
    assert.equal(summary.focusedChecks.status, 'passed');
    assert.ok(summary.focusedChecks.groups.length > 0);
    assert.ok(summary.focusedChecks.groups.every((group) => group.status === 'passed' && group.exitCode === 0));
    assert.equal(summary.commands.playwright, undefined);
  }
  assert.ok(events.every((event) => event.startsWith('focused:')));

  const failed = await writeTestBatch(directory, {
    checksOnly: true,
    commandOptions: { focusedFailures: new Set(['builder-load/list']) },
  });
  assert.equal(failed.exitCode, 1);
  const failedBatch = JSON.parse(failed.printed.at(-1));
  assert.equal(failedBatch.status, 'failed', 'a later focused-check failure prevents a passing batch');
  assert.deepEqual(failedBatch.cases.map(({ status }) => status), ['checks-passed', 'failed']);
  assert.equal(failedBatch.nativeStatus, 'not-run');
  assert.ok(failed.events.every((event) => event.startsWith('focused:')));
});

test('batch concurrency validates its range before loading targets or starting runtime commands', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const manifestPath = manifestFile(directory, {
    schemaVersion: 1,
    cases: [{
      scenario: 'root-quantity-pivot',
      case: 'full-population-lifecycle',
      target: { mode: 'config', path: 'target.json' },
    }],
  });
  for (const value of ['0', '4', '1.5', 'fast']) {
    let targetLoads = 0;
    let runtimeCalls = 0;
    await assert.rejects(main([
      '--batch', manifestPath,
      '--evidence-parent', directory,
      '--concurrency', value,
    ], {
      env: {},
      targetLoader: async () => { targetLoads += 1; throw new Error('invalid concurrency reached target loading'); },
      runBatch: async () => { runtimeCalls += 1; throw new Error('invalid concurrency reached batch execution'); },
      commandRunner: async () => { runtimeCalls += 1; return { exitCode: 0 }; },
      write: () => {},
    }), undefined, `invalid concurrency ${value}`);
    assert.equal(targetLoads, 0, `concurrency ${value} is rejected before target loading`);
    assert.equal(runtimeCalls, 0, `concurrency ${value} is rejected before runtime`);
  }

  let singleCaseCalls = 0;
  await assert.rejects(main([
    '--scenario', 'builder-controls',
    '--case', 'first-table',
    '--target-from-environment',
    '--concurrency', '2',
  ], {
    env: { ...baseEnvironment },
    runBracket: async () => { singleCaseCalls += 1; throw new Error('single-case concurrency reached execution'); },
    commandRunner: async () => { singleCaseCalls += 1; return { exitCode: 0 }; },
    write: () => {},
  }), undefined, 'concurrency is batch-only');
  assert.equal(singleCaseCalls, 0);
});

test('strict manifest rejects unknown fields, duplicate or empty selection, and unknown cases before runtime', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const invalid = [
    { label: 'unknown top-level field', manifest: { schemaVersion: 1, cases, extra: true } },
    { label: 'unknown case field', manifest: { schemaVersion: 1, cases: [{ ...cases[0], grep: 'broader' }] } },
    { label: 'duplicate selected case', manifest: { schemaVersion: 1, cases: [cases[0], cases[0]] } },
    { label: 'empty selection', manifest: { schemaVersion: 1, cases: [] } },
    { label: 'unknown registered case', manifest: { schemaVersion: 1, cases: [{ ...cases[0], case: 'does-not-exist' }] } },
  ];

  for (const { label, manifest } of invalid) {
    const manifestPath = manifestFile(directory, manifest, `${label.replaceAll(' ', '-')}.json`);
    let runBatchCalls = 0;
    let commandCalls = 0;
    await assert.rejects(main(['--batch', manifestPath, '--evidence-parent', directory], {
      env: { ...baseEnvironment },
      runBatch: async () => { runBatchCalls += 1; throw new Error('invalid manifest reached runtime'); },
      commandRunner: async () => { commandCalls += 1; return { exitCode: 0 }; },
      write: () => {},
    }), undefined, label);
    assert.equal(runBatchCalls, 0, `${label} must be rejected before the batch engine`);
    assert.equal(commandCalls, 0, `${label} must be rejected before a native command starts`);
  }
});

test('same fully resolved environment target runs serially with one shared bracket and isolated case artifacts', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const observedEnvironments = [];
  const { exitCode, printed, events } = await writeTestBatch(directory, { commandOptions: { observedEnvironments } });
  assert.equal(exitCode, 0, `${printed.at(-1)}\n${events.join('\n')}`);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'passed');
  assert.equal(summary.cases.length, 2);
  assert.deepEqual(summary.cases.map(({ scenario, case: caseName, status }) => [scenario, caseName, status]), [
    ['builder-controls', 'first-table', 'passed'],
    ['builder-load', 'list', 'passed'],
  ]);
  assert.equal(summary.sharedBracket.status, 'PASS');
  const sharedStart = Date.parse(summary.sharedBracket.startedAt);
  const sharedEnd = Date.parse(summary.sharedBracket.finishedAt);
  assert.ok(Number.isFinite(sharedStart) && Number.isFinite(sharedEnd) && sharedStart <= sharedEnd);
  assert.ok(Number.isFinite(summary.sharedBracket.durationMs) && summary.sharedBracket.durationMs >= 0);
  assert.ok(summary.sharedBracket.durationMs <= sharedEnd - sharedStart + 1000,
    'shared duration is bounded by its recorded timestamps');
  assert.ok(existsSync(summary.summary), 'the batch summary is retained as an actual file');
  const persistedBatchSummary = readJson(summary.summary);
  assert.equal(persistedBatchSummary.status, summary.status);
  assert.equal(persistedBatchSummary.cases.length, summary.cases.length);

  const firstBrowserClose = events.indexOf('browser:builder-controls/first-table:close');
  const secondBrowserStart = events.indexOf('browser:builder-load/list:start');
  const beforePrecheck = events.indexOf('shared:precheck');
  const captureBefore = events.indexOf('shared:capture-before');
  const healthBefore = events.indexOf('shared:health-before');
  const captureAfter = events.indexOf('shared:capture-after');
  const healthAfter = events.indexOf('shared:health-after');
  events.forEach((event, index) => {
    if (event.startsWith('selection:') || event.includes('focused:')) {
      assert.ok(index < beforePrecheck, `${event} must complete before shared precheck`);
    }
  });
  assert.ok(beforePrecheck < captureBefore && captureBefore < healthBefore, events.join('\n'));
  events.forEach((event, index) => {
    if (event.startsWith('browser:') && event.endsWith(':start')) {
      assert.ok(index > healthBefore, `${event} must start only after shared before-health`);
    }
  });
  assert.ok(firstBrowserClose >= 0 && secondBrowserStart > firstBrowserClose, events.join('\n'));
  assert.ok(captureAfter > events.indexOf('browser:builder-load/list:close'), events.join('\n'));
  assert.ok(captureAfter > events.indexOf('child-drained:builder-controls/first-table'));
  assert.ok(captureAfter > events.indexOf('child-drained:builder-load/list'));
  assert.ok(healthAfter > captureAfter, events.join('\n'));
  assert.equal(events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.equal(events.filter((event) => event === 'shared:health-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:health-after').length, 1);

  const caseSummaries = summary.cases.map(({ summaryPath }) => readJson(summaryPath));
  assert.ok(summary.cases.every(({ durationScope, timingInterpretation }) =>
    durationScope === 'case-preparation-through-shared-closure' && timingInterpretation === 'serial'));
  assert.equal(new Set(caseSummaries.map(({ runDirectory }) => runDirectory)).size, 2);
  assert.equal(new Set(caseSummaries.map(({ evidence }) => evidence.playwrightReport)).size, 2);
  assert.equal(observedEnvironments.length, 2);
  for (const { env } of observedEnvironments) {
    assert.equal(env.LOOM_CDA_SOURCE_ROOT, root);
    assert.equal(env.LOOM_CDA_PROJECT, 'owned-project');
    assert.equal(env.LOOM_CDA_API_ORIGIN, 'http://127.0.0.1:8188');
    assert.equal(env.LOOM_CDA_API_CONTAINER, 'owned-api');
  }
  assert.equal(caseSummaries[0].lifecycle.reportStatus, 'passed');
  assert.equal(caseSummaries[0].status, 'passed');
  assert.equal(caseSummaries[1].lifecycle.reportStatus, 'passed');
  assert.equal(caseSummaries[1].status, 'passed');
  const sharedProofPaths = existingFilePaths(summary.sharedBracket.evidence);
  assert.ok(sharedProofPaths.length > 0, 'shared proof points to retained artifact files');
  for (const path of sharedProofPaths) {
    assert.ok(caseSummaries.some((caseSummary) => existingFilePaths(caseSummary).includes(path)),
      `each case summary references the shared artifact itself: ${path}`);
  }
});

test('registered required checks reject missing, failed, or disconnected lifecycle reports', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const variants = [
    ['missing report', { missingDiagnosticReports: new Set(['builder-controls/first-table']) }],
    ['failed assertion', { failedDiagnosticReports: new Set(['builder-controls/first-table']) }],
    ['disconnected check list', { disconnectedRequiredCheckReports: new Set(['builder-controls/first-table']) }],
  ];

  for (const [label, commandOptions] of variants) {
    const { exitCode, printed } = await writeTestBatch(directory, { commandOptions });
    assert.equal(exitCode, 1, label);
    const batch = JSON.parse(printed.at(-1));
    assert.equal(batch.status, 'failed', label);
    const failedCase = batch.cases.find(({ scenario, case: caseName }) =>
      scenario === 'builder-controls' && caseName === 'first-table');
    assert.equal(failedCase.status, 'failed', label);
    const summary = readJson(failedCase.summaryPath);
    assert.equal(summary.failureCategory, 'required-check', label);
    assert.notEqual(summary.status, 'passed', label);
  }
});

test('report target metadata cannot select a weaker required-check contract', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const summary = await runNativeVerificationBracket({
    scenarioID: customContractCase.scenario,
    caseName: customContractCase.case,
    grep: '@viewer-query-output',
    env: { ...baseEnvironment },
    evidenceParent: directory,
    commandRunner: batchCommandRunner({
      customRequiredCheckReports: new Set(['viewer-query/output']),
    }),
  });
  assert.equal(summary.status, 'failed');
  assert.equal(summary.lifecycle.requiredCheckCount, 11,
    'the selected environment target keeps the registry owned check set');
  assert.equal(summary.lifecycle.requiredCheckListMatchesRegistry, false,
    'a report claiming a custom target cannot replace the configured required checks');
  assert.equal(summary.failureCategory, 'required-check');
});

test('a focused command that changes a declared input cannot pass on exit code alone', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const contract = scenarioContract('builder-controls', 'first-table');
  const sourceRoot = realpathSync(createCaseSourceTree(directory, contract));
  let caseRunDirectory = null;
  t.after(() => { if (caseRunDirectory) rmSync(caseRunDirectory, { recursive: true, force: true }); });
  const commandRunner = async (_command, args, { cwd }) => {
    assert.equal(cwd, sourceRoot);
    assert.equal(args[0], '--test');
    const testPath = args.slice(1).find((path) => /\.test\.(?:cjs|js|mjs)$/i.test(path));
    assert.ok(testPath, `node-test command names a test file: ${args.join(' ')}`);
    writeFileSync(join(sourceRoot, testPath), 'changed while the test ran\n', { mode: 0o600 });
    return { exitCode: 0, stdoutText: 'tests reported success\n', stderrText: '' };
  };
  const summary = await runNativeVerificationBracket({
    scenarioID: 'builder-controls',
    caseName: 'first-table',
    checksOnly: true,
    root: sourceRoot,
    env: { ...baseEnvironment, LOOM_CDA_SOURCE_ROOT: sourceRoot },
    evidenceParent: join(directory, 'evidence'),
    commandRunner,
  });
  caseRunDirectory = summary.runDirectory;

  assert.equal(summary.focusedChecks.status, 'failed');
  assert.equal(summary.focusedChecks.groups[0].exitCode, 0);
  assert.equal(summary.focusedChecks.groups[0].inputsUnchanged, false);
  assert.equal(summary.focusedChecks.groups[0].status, 'failed');
  assert.equal(summary.nativeStatus, 'not-run');
  assert.equal(summary.commands.playwright, undefined);
});

test('two config-bound cases accept one identical fully resolved target and mapped environment', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const requestedDatasetDir = join(directory, 'dataset');
  mkdirSync(requestedDatasetDir, { recursive: true, mode: 0o700 });
  const datasetDir = realpathSync(requestedDatasetDir);
  const configPath = join(directory, 'owned-target.json');
  writeJson(configPath, {
    schemaVersion: 1,
    sourceRoot: root,
    datasetDir,
    composeProject: 'loom-batch-test',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    apiOrigin: 'http://127.0.0.1:8188',
    uiOrigin: 'http://127.0.0.1:30008',
    apiContainer: 'loom-batch-api',
    arangoContainer: 'loom-batch-arango',
    arangoDatabase: 'loom_dev',
    clickhouseContainer: 'loom-batch-clickhouse',
    noAuth: true,
  });
  const manifestCases = configTargetCases.map((entry) => ({
    ...entry,
    target: { mode: 'config', path: configPath },
  }));
  const observedEnvironments = [];
  const { exitCode, printed } = await writeTestBatch(directory, {
    manifestCases,
    environment: {},
    commandOptions: { observedEnvironments },
  });
  assert.equal(exitCode, 0, `${printed.at(-1)}\n${observedEnvironments.map(({ key }) => key).join(',')}`);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'passed');
  assert.deepEqual(summary.cases.map(({ scenario, case: caseName, status }) => [scenario, caseName, status]), [
    ['root-quantity-pivot', 'full-population-lifecycle', 'passed'],
    ['root-quantity-pivot', 'related-text-only-full-population-lifecycle', 'passed'],
  ]);
  const targetEnvironmentNames = Object.keys(observedEnvironments[0].env)
    .filter((name) => name.startsWith('LOOM_CDA_') || name.startsWith('LOOM_DEV_') || name === 'LOOM_ARANGO_DATABASE');
  assert.ok(targetEnvironmentNames.includes('LOOM_CDA_DATASET_DIR'));
  assert.ok(targetEnvironmentNames.includes('LOOM_ARANGO_DATABASE'));
  const mappedEnvironments = observedEnvironments.map(({ env }) => Object.fromEntries(
    targetEnvironmentNames.map((name) => [name, env[name]]),
  ));
  assert.deepEqual(mappedEnvironments, [mappedEnvironments[0], mappedEnvironments[0]],
    'same config-resolved runtime fields are bound for both selected cases');
  assert.equal(mappedEnvironments[0].LOOM_CDA_SOURCE_ROOT, root);
  assert.equal(mappedEnvironments[0].LOOM_CDA_DATASET_DIR, datasetDir);
  assert.equal(mappedEnvironments[0].LOOM_CDA_PROJECT, 'loom_dev_cda_fhir');
  assert.equal(mappedEnvironments[0].LOOM_CDA_GENERATION, 'cda-fhir-v1');
  assert.equal(mappedEnvironments[0].LOOM_CDA_NO_AUTH, '1');
  assert.equal(mappedEnvironments[0].LOOM_ARANGO_DATABASE, 'loom_dev');
});

test('matching environment and config targets share one resolved identity without live runtime services', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const requestedDatasetDir = join(directory, 'dataset');
  mkdirSync(requestedDatasetDir, { recursive: true, mode: 0o700 });
  const datasetDir = realpathSync(requestedDatasetDir);
  const configPath = join(directory, 'owned-target.json');
  writeJson(configPath, {
    schemaVersion: 1,
    sourceRoot: root,
    datasetDir,
    composeProject: 'loom-mixed-target-test',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    apiOrigin: 'http://127.0.0.1:8188',
    uiOrigin: 'http://127.0.0.1:30008',
    apiContainer: 'loom-mixed-api',
    arangoContainer: 'loom-mixed-arango',
    arangoDatabase: 'loom_dev',
    clickhouseContainer: 'loom-mixed-clickhouse',
    noAuth: true,
  });
  const environment = {
    LOOM_CDA_SOURCE_ROOT: root,
    LOOM_CDA_DATASET_DIR: datasetDir,
    LOOM_CDA_COMPOSE_PROJECT: 'loom-mixed-target-test',
    LOOM_CDA_PROJECT: 'loom_dev_cda_fhir',
    LOOM_CDA_GENERATION: 'cda-fhir-v1',
    LOOM_CDA_API_ORIGIN: 'http://127.0.0.1:8188',
    LOOM_CDA_UI_ORIGIN: 'http://127.0.0.1:30008',
    LOOM_CDA_API_PORT: '8188',
    LOOM_CDA_UI_PORT: '30008',
    LOOM_CDA_API_CONTAINER: 'loom-mixed-api',
    LOOM_CDA_ARANGO_CONTAINER: 'loom-mixed-arango',
    LOOM_CDA_CLICKHOUSE_CONTAINER: 'loom-mixed-clickhouse',
    LOOM_CDA_NO_AUTH: '1',
    LOOM_DEV_SOURCE_ROOT: root,
    LOOM_DEV_COMPOSE_PROJECT: 'loom-mixed-target-test',
    LOOM_DEV_PROJECT: 'loom_dev_cda_fhir',
    LOOM_DEV_GENERATION: 'cda-fhir-v1',
    LOOM_DEV_API_URL: 'http://127.0.0.1:8188',
    LOOM_DEV_UI_URL: 'http://127.0.0.1:30008',
    LOOM_DEV_API_PORT: '8188',
    LOOM_DEV_UI_PORT: '30008',
    LOOM_ARANGO_DATABASE: 'loom_dev',
  };
  const observedEnvironments = [];
  const manifestCases = [
    { scenario: 'builder-controls', case: 'first-table', target: { mode: 'environment' } },
    {
      scenario: 'root-quantity-pivot',
      case: 'full-population-lifecycle',
      target: { mode: 'config', path: configPath },
    },
  ];
  const { exitCode, printed } = await writeTestBatch(directory, {
    manifestCases,
    environment,
    commandOptions: { observedEnvironments },
  });
  assert.equal(exitCode, 0, printed.at(-1));
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'passed');
  assert.equal(summary.cases.length, 2);
  assert.equal(summary.sharedBracket.status, 'PASS');
  assert.equal(observedEnvironments.length, 2);
  const mappedNames = Object.keys(environment);
  const mapped = observedEnvironments.map(({ env }) => Object.fromEntries(
    mappedNames.map((name) => [name, env[name]]),
  ));
  assert.deepEqual(mapped, [environment, environment],
    'both target modes bind the same complete runtime environment for fake command execution');
});

test('approved fresh fixture projects overlap, while an unannotated case waits for the wave to drain', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  let releaseBrowsers;
  let markBothStarted;
  const release = new Promise((resolveRelease) => { releaseBrowsers = resolveRelease; });
  const bothStarted = new Promise((resolveStarted) => { markBothStarted = resolveStarted; });
  const starts = [];
  const browserGate = {
    async arrive(key) {
      starts.push(key);
      if (starts.length === 2) markBothStarted();
      await release;
    },
  };
  const result = startTestBatch(directory, {
    manifestCases: [...cases, unannotatedCase],
    commandOptions: { browserGate },
    concurrency: 2,
  });
  const firstWave = await Promise.race([
    bothStarted.then(() => ({ started: true })),
    result.promise.then(
      (exitCode) => ({ finished: true, exitCode }),
      (error) => ({ finished: true, error: String(error?.message ?? error) }),
    ),
  ]);
  assert.equal(firstWave.started, true,
    `batch finished before both approved browsers started: ${JSON.stringify(firstWave)}\n${result.events.join('\n')}`);
  assert.deepEqual(starts, ['builder-controls/first-table', 'builder-load/list']);
  assert.equal(result.events.includes('browser:builder-controls/tables:start'), false,
    'the unannotated case cannot enter the active isolation wave');
  assert.equal(result.events.includes('shared:capture-after'), false,
    'shared after-evidence waits for all selected cases');
  releaseBrowsers();

  const exitCode = await result.promise;
  const summary = JSON.parse(result.printed.at(-1));
  assert.equal(exitCode, 0, `${result.printed.at(-1)}\n${result.events.join('\n')}`);
  assert.equal(summary.status, 'passed');
  const unannotatedStart = result.events.indexOf('browser:builder-controls/tables:start');
  assert.ok(unannotatedStart > result.events.indexOf('browser:builder-load/list:close'));
  assert.ok(unannotatedStart > result.events.indexOf('browser:builder-controls/first-table:close'));
  assert.ok(result.events.indexOf('shared:capture-after')
    > result.events.indexOf('browser:builder-controls/tables:close'));
  assert.equal(result.events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(result.events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.equal(summary.cases[0].timingInterpretation, 'concurrent-latency-provisional');
  assert.equal(summary.cases[1].timingInterpretation, 'concurrent-latency-provisional');
});

test('ordinary native failure continues to the next case and keeps both final verdicts', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: {
      browserFailures: new Set(['builder-controls/first-table']),
      failedDiagnosticReports: new Set(['builder-load/list']),
    },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'failed');
  assert.deepEqual(summary.cases.map(({ status, nativeStatus }) => [status, nativeStatus]), [
    ['failed', 'failed'],
    ['failed', 'passed'],
  ]);
  const failedReportCase = summary.cases[1];
  assert.equal(readJson(failedReportCase.summaryPath).failureCategory, 'required-check');
  assert.equal(summary.sharedBracket.status, 'PASS');
  assert.equal(events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.ok(events.indexOf('browser:builder-load/list:start') > events.indexOf('browser:builder-controls/first-table:close'));
  assert.equal(summary.cases[1].integrityStatus, 'PASS');
});

test('focused failure skips only that native case and later selected cases still run', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: { focusedFailures: new Set(['builder-controls/first-table']) },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.deepEqual(summary.cases.map(({ status, nativeStatus }) => [status, nativeStatus]), [
    ['failed', 'not-run'],
    ['passed', 'passed'],
  ]);
  assert.equal(summary.sharedBracket.status, 'PASS');
  assert.equal(events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.equal(events.some((event) => event === 'browser:builder-controls/first-table:start'), false);
  assert.ok(events.includes('browser:builder-load/list:start'));
});

test('an inexact selected test suppresses only its case', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: { selectionFailures: new Set(['builder-controls/first-table']) },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'unverified');
  assert.equal(summary.cases[0].status, 'unverified');
  assert.equal(summary.cases[0].nativeStatus, 'not-run');
  const caseSummary = readJson(summary.cases[0].summaryPath);
  assert.equal(caseSummary.failureCategory, 'preparation');
  assert.equal(caseSummary.selection.exact, false);
  assert.equal(caseSummary.focusedChecks.status, 'not-run');
  assert.equal(summary.cases[1].status, 'passed');
  assert.equal(summary.sharedBracket.status, 'PASS');
  assert.equal(events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.equal(events.includes('browser:builder-controls/first-table:start'), false);
  assert.ok(events.includes('browser:builder-load/list:start'));
});

test('an ambiguous selected test suppresses only its case', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: { ambiguousSelections: new Set(['builder-controls/first-table']) },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'unverified');
  assert.equal(summary.cases[0].status, 'unverified');
  assert.equal(summary.cases[0].nativeStatus, 'not-run');
  const caseSummary = readJson(summary.cases[0].summaryPath);
  assert.equal(caseSummary.failureCategory, 'preparation');
  assert.equal(caseSummary.selection.exact, false);
  assert.equal(caseSummary.focusedChecks.status, 'not-run');
  assert.equal(summary.cases[1].status, 'passed');
  assert.equal(summary.sharedBracket.status, 'PASS');
  assert.equal(events.filter((event) => event === 'shared:capture-before').length, 1);
  assert.equal(events.filter((event) => event === 'shared:capture-after').length, 1);
  assert.equal(events.includes('browser:builder-controls/first-table:start'), false);
  assert.ok(events.includes('browser:builder-load/list:start'));
});

test('single-case preparation failures return before any shared bracket or Playwright runtime command', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const [label, commandOptions, expectedExit, expectedStatus] of [
    ['ambiguous selection', { ambiguousSelections: new Set(['builder-controls/first-table']) }, 1, 'unverified'],
    ['focused check failure', { focusedFailures: new Set(['builder-controls/first-table']) }, 1, 'failed'],
  ]) {
    const { exitCode, printed, events } = await writeTestBatch(directory, {
      manifestCases: [cases[0]],
      commandOptions,
    });
    assert.equal(exitCode, expectedExit, label);
    const summary = JSON.parse(printed.at(-1));
    assert.equal(summary.status, expectedStatus, label);
    assert.equal(summary.cases[0].status, expectedStatus, label);
    assert.equal(summary.sharedBracket.status, 'NOT_RUN', label);
    assert.deepEqual(summary.sharedBracket.commands, {}, `${label} starts no shared bracket commands`);
    if (label === 'focused check failure') {
      assert.equal(summary.cases[0].nativeStatus, 'not-run',
        'a failed focused prerequisite is not a browser verdict');
    }
    assert.equal(events.some((event) => event.startsWith('shared:')), false,
      `${label} must return before the shared before/after bracket`);
    assert.equal(events.some((event) => event.startsWith('browser:')), false,
      `${label} must not launch browser execution`);
    assert.ok(events.some((event) => event.startsWith('selection:')),
      `${label} still performs exact selection preparation`);
    if (label === 'focused check failure') {
      assert.ok(events.some((event) => event.startsWith('focused:')),
        'focused-check failures occur after successful exact selection');
    }
  }
});

test('shared before failure prevents browser execution', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: { sharedFailure: 'health-before' },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'unverified');
  assert.equal(summary.sharedBracket.status, 'UNVERIFIED');
  assert.match(summary.sharedBracket.firstFailureReason, /before health/i);
  assert.equal(events.some((event) => event.startsWith('browser:')), false);
});

test('shared command and evidence write failures stop scheduling before any browser case', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const failure of ['precheck', 'write-capture-before']) {
    const { exitCode, printed, events } = await writeTestBatch(directory, {
      commandOptions: { sharedFailure: failure },
    });
    assert.equal(exitCode, 1, failure);
    const summary = JSON.parse(printed.at(-1));
    assert.equal(summary.status, 'unverified', failure);
    assert.equal(events.some((event) => event.startsWith('browser:')), false, failure);
  }
});

test('after capture failure still runs after health and prevents a passing batch', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const { exitCode, printed, events } = await writeTestBatch(directory, {
    commandOptions: { sharedFailure: 'capture-after' },
  });
  assert.equal(exitCode, 1);
  const summary = JSON.parse(printed.at(-1));
  assert.equal(summary.status, 'unverified');
  assert.equal(events.filter((event) => event.startsWith('browser:') && event.endsWith(':start')).length, 2);
  assert.ok(events.indexOf('shared:health-after') > events.indexOf('shared:capture-after'));
  assert.equal(summary.sharedBracket.status, 'UNVERIFIED');
});

test('source, API, mount changes and missing or malformed after evidence cannot pass', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const variants = [
    ['source changed', { sourceChanged: true }],
    ['API identity changed', { apiChanged: true }],
    ['mount check failed', { mountsFailed: true }],
    ['after docs missing', { missingAfterDocs: true }],
    ['after source malformed', { malformedAfterSource: true }],
  ];
  for (const [label, commandOptions] of variants) {
    const { exitCode, printed, events } = await writeTestBatch(directory, { commandOptions });
    assert.equal(exitCode, 1, label);
    const summary = JSON.parse(printed.at(-1));
    assert.notEqual(summary.status, 'passed', label);
    assert.notEqual(summary.sharedBracket.integrity.status, 'PASS', label);
    assert.ok(events.includes('shared:health-after'), `${label} still receives the after-health attempt`);
  }
});

test('timeout and spawn exception remain distinct case failures, while interruption halts later scheduling', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const timeout = await writeTestBatch(directory, {
    manifestCases: cases,
    commandOptions: { browserTimeouts: new Set(['builder-controls/first-table']) },
  });
  assert.equal(timeout.exitCode, 1);
  const timeoutSummary = JSON.parse(timeout.printed.at(-1));
  assert.notEqual(timeoutSummary.status, 'passed');
  const timeoutCaseSummary = readJson(timeoutSummary.cases[0].summaryPath);
  assert.equal(timeoutCaseSummary.commands.playwright.timedOut, true,
    'the timed out process is retained as a timeout');
  assert.ok(timeout.events.indexOf('browser:builder-load/list:start')
    > timeout.events.indexOf('child-drained:builder-controls/first-table'));
  assert.equal(timeoutSummary.cases[1].status, 'passed');

  const spawnFailure = await writeTestBatch(directory, {
    commandOptions: { spawnFailures: new Set(['builder-controls/first-table']) },
  });
  assert.notEqual(spawnFailure.exitCode, 0);
  const spawnSummary = JSON.parse(spawnFailure.printed.at(-1));
  assert.notEqual(spawnSummary.status, 'passed');
  const spawnCaseSummary = readJson(spawnSummary.cases[0].summaryPath);
  assert.equal(spawnCaseSummary.commands.playwright.exitCode, null);
  assert.notEqual(spawnCaseSummary.commands.playwright.timedOut, true,
    'a spawn exception remains distinct from a process timeout');
  assert.ok(spawnFailure.events.includes('browser:builder-load/list:start'),
    'a spawn exception is recorded as a case failure and the ready case proceeds');
  assert.equal(spawnSummary.cases[1].status, 'passed');

  const controller = new AbortController();
  const interrupted = await writeTestBatch(directory, {
    manifestCases: cases,
    signal: controller.signal,
    commandOptions: { interruptController: controller },
  });
  const interruptedSummary = JSON.parse(interrupted.printed.at(-1));
  assert.equal(interrupted.exitCode, 130);
  assert.equal(readJson(interruptedSummary.summary).interrupted, true);
  assert.equal(interruptedSummary.status, 'unverified');
  assert.equal(interrupted.events.includes('browser:builder-load/list:start'), false);
  assert.ok(interrupted.events.includes('shared:capture-after'));
  assert.ok(interrupted.events.includes('shared:health-after'));
});

test('interruption halts launches, drains both active cases, then closes shared evidence', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  let releaseBrowsers;
  let markBothStarted;
  const release = new Promise((resolveRelease) => { releaseBrowsers = resolveRelease; });
  const bothStarted = new Promise((resolveStarted) => { markBothStarted = resolveStarted; });
  const starts = [];
  const browserGate = {
    async arrive(key) {
      starts.push(key);
      if (starts.length === 2) markBothStarted();
      await release;
    },
  };
  const controller = new AbortController();
  const result = startTestBatch(directory, {
    manifestCases: [...cases, unannotatedCase],
    signal: controller.signal,
    commandOptions: { browserGate },
    concurrency: 2,
  });
  await bothStarted;
  controller.abort();
  releaseBrowsers();
  const exitCode = await result.promise;
  const summary = JSON.parse(result.printed.at(-1));
  assert.equal(exitCode, 130);
  assert.equal(readJson(summary.summary).interrupted, true);
  assert.equal(summary.status, 'unverified');
  assert.equal(result.events.includes('browser:builder-controls/tables:start'), false);
  assert.ok(result.events.indexOf('shared:capture-after')
    > result.events.indexOf('browser:builder-controls/first-table:close'));
  assert.ok(result.events.indexOf('shared:capture-after')
    > result.events.indexOf('browser:builder-load/list:close'));
  assert.ok(result.events.indexOf('shared:capture-after')
    > result.events.indexOf('child-drained:builder-controls/first-table'));
  assert.ok(result.events.indexOf('shared:capture-after')
    > result.events.indexOf('child-drained:builder-load/list'));
  assert.ok(result.events.includes('shared:health-after'));
});

test('batch summary write failure cannot be reported as a pass', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const manifestPath = manifestFile(directory, { schemaVersion: 1, cases });
  const events = [];
  let attemptedSummaryPath;
  const result = await main(['--batch', manifestPath, '--evidence-parent', directory], {
    env: { ...baseEnvironment },
    commandRunner: batchCommandRunner({ events }),
    atomicWriteJson: async (path, value) => {
      if (basename(path) === 'batch-summary.json' && value.finishedAt) {
        attemptedSummaryPath = path;
        throw new Error('controlled summary write failure');
      }
      writeJson(path, value);
    },
    write: () => {},
  }).then((exitCode) => ({ exitCode }), (error) => ({ error }));
  assert.ok(result.error || result.exitCode !== 0, 'a failed atomic writer cannot produce a successful exit');
  assert.ok(events.includes('browser:builder-controls/first-table:close'));
  assert.ok(events.includes('shared:health-after'));
  assert.ok(attemptedSummaryPath);
  assert.ok(existsSync(attemptedSummaryPath), 'the initial batch record remains available after final publication fails');
  assert.notEqual(readJson(attemptedSummaryPath).status, 'passed',
    'a failed final atomic write cannot leave a published pass verdict');
});

test('target identity mismatches in project, generation, source root, endpoints, containers, or mapped env fail before runtime', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const registeredCases = [
    { scenario: 'root-quantity-pivot', case: 'full-population-lifecycle', target: { mode: 'config', path: 'one.json' } },
    { scenario: 'root-quantity-pivot', case: 'related-text-only-full-population-lifecycle', target: { mode: 'config', path: 'two.json' } },
  ];
  const baseTarget = {
    sourceRoot: root,
    datasetDir: root,
    composeProject: 'owned-compose',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    apiOrigin: 'http://127.0.0.1:8188',
    apiPort: '8188',
    uiOrigin: 'http://127.0.0.1:30008',
    uiPort: '30008',
    apiContainer: 'owned-api',
    arangoContainer: 'owned-arango',
    arangoDatabase: 'loom_dev',
    clickhouseContainer: 'owned-clickhouse',
    noAuth: true,
  };
  const toEnvironment = (target) => ({
    LOOM_CDA_SOURCE_ROOT: target.sourceRoot,
    LOOM_CDA_DATASET_DIR: target.datasetDir,
    LOOM_CDA_COMPOSE_PROJECT: target.composeProject,
    LOOM_CDA_PROJECT: target.project,
    LOOM_CDA_GENERATION: target.generation,
    LOOM_CDA_API_ORIGIN: target.apiOrigin,
    LOOM_CDA_UI_ORIGIN: target.uiOrigin,
    LOOM_CDA_API_PORT: target.apiPort,
    LOOM_CDA_UI_PORT: target.uiPort,
    LOOM_CDA_API_CONTAINER: target.apiContainer,
    LOOM_CDA_ARANGO_CONTAINER: target.arangoContainer,
    LOOM_CDA_CLICKHOUSE_CONTAINER: target.clickhouseContainer,
    LOOM_CDA_NO_AUTH: target.noAuth ? '1' : '0',
    LOOM_DEV_SOURCE_ROOT: target.sourceRoot,
    LOOM_DEV_COMPOSE_PROJECT: target.composeProject,
    LOOM_DEV_PROJECT: target.project,
    LOOM_DEV_GENERATION: target.generation,
    LOOM_DEV_API_URL: target.apiOrigin,
    LOOM_DEV_UI_URL: target.uiOrigin,
    LOOM_DEV_API_PORT: target.apiPort,
    LOOM_DEV_UI_PORT: target.uiPort,
    ...(target.arangoDatabase === undefined ? {} : { LOOM_ARANGO_DATABASE: target.arangoDatabase }),
  });

  for (const name of Object.keys(baseTarget)) {
    const label = `target ${name}`;
    const manifestPath = manifestFile(directory, { schemaVersion: 1, cases: registeredCases }, `${label.replaceAll(' ', '-')}.json`);
    let runtimeCalls = 0;
    const targetLoader = async ({ targetPath }) => {
      const target = String(targetPath).endsWith('one.json') ? baseTarget : {
        ...baseTarget,
        [name]: typeof baseTarget[name] === 'boolean'
          ? !baseTarget[name]
          : `${baseTarget[name]}-mismatch`,
      };
      return {
        target,
        environment: toEnvironment(target),
        configPath: join(directory, targetPath),
        validationScope: 'configuration-only',
        runtimeDatasetIdentity: 'not-checked',
      };
    };
    await assert.rejects(main(['--batch', manifestPath, '--evidence-parent', directory], {
      env: {},
      targetLoader,
      commandRunner: async () => { runtimeCalls += 1; return { exitCode: 0 }; },
      write: () => {},
    }), undefined, `${label} target mismatch`);
    assert.equal(runtimeCalls, 0, `${label} mismatch is rejected before runtime`);
  }

  const mappedEnvironment = toEnvironment(baseTarget);
  for (const name of Object.keys(mappedEnvironment)) {
    const manifestPath = manifestFile(directory, { schemaVersion: 1, cases: registeredCases }, `mapped-${name}.json`);
    let runtimeCalls = 0;
    const mappedEnvironmentLoader = async ({ targetPath }) => {
      const target = baseTarget;
      const environment = toEnvironment(target);
      if (String(targetPath).endsWith('two.json')) environment[name] += '-mismatch';
      return { target, environment, configPath: join(directory, targetPath), validationScope: 'configuration-only' };
    };
    await assert.rejects(main(['--batch', manifestPath, '--evidence-parent', directory], {
      env: {},
      targetLoader: mappedEnvironmentLoader,
      commandRunner: async () => { runtimeCalls += 1; return { exitCode: 0 }; },
      write: () => {},
    }), undefined, `mapped runtime environment mismatch: ${name}`);
    assert.equal(runtimeCalls, 0, `mapped runtime environment mismatch ${name} is rejected before runtime`);
  }
});

test('batch target loading preserves inherited environment conflict checks', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const datasetDir = join(directory, 'dataset');
  mkdirSync(datasetDir, { recursive: true, mode: 0o700 });
  const configPath = join(directory, 'owned-target.json');
  writeJson(configPath, {
    schemaVersion: 1,
    sourceRoot: root,
    datasetDir,
    composeProject: 'loom-test-compose',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    apiOrigin: 'http://127.0.0.1:8188',
    uiOrigin: 'http://127.0.0.1:30008',
    apiContainer: 'loom-test-api',
    arangoContainer: 'loom-test-arango',
    arangoDatabase: 'loom_dev',
    clickhouseContainer: 'loom-test-clickhouse',
    noAuth: true,
  });
  const manifestPath = manifestFile(directory, {
    schemaVersion: 1,
    cases: [
      { scenario: 'root-quantity-pivot', case: 'full-population-lifecycle', target: { mode: 'config', path: configPath } },
      { scenario: 'root-quantity-pivot', case: 'related-text-only-full-population-lifecycle', target: { mode: 'config', path: configPath } },
    ],
  });
  let runtimeCalls = 0;
  await assert.rejects(main(['--batch', manifestPath, '--evidence-parent', directory], {
    env: { LOOM_CDA_PROJECT: 'inherited-conflict' },
    commandRunner: async () => { runtimeCalls += 1; return { exitCode: 0 }; },
    write: () => {},
  }), /Conflicting inherited target environment variable LOOM_CDA_PROJECT/);
  assert.equal(runtimeCalls, 0);
});

test('single-case target loading failure leaves no owned SIGINT listener behind', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const originalSigintListeners = process.listenerCount('SIGINT');
  let targetLoads = 0;
  let bracketCalls = 0;
  let commandCalls = 0;

  await assert.rejects(main([
    '--scenario', 'root-quantity-pivot',
    '--case', 'full-population-lifecycle',
    '--target', join(directory, 'owned-target.json'),
  ], {
    env: {},
    targetLoader: async () => {
      targetLoads += 1;
      throw new Error('controlled target loading failure');
    },
    runBracket: async () => {
      bracketCalls += 1;
      throw new Error('target loading failure reached bracket execution');
    },
    commandRunner: async () => {
      commandCalls += 1;
      return { exitCode: 0 };
    },
    write: () => {},
  }), /controlled target loading failure/);

  assert.equal(targetLoads, 1);
  assert.equal(bracketCalls, 0);
  assert.equal(commandCalls, 0);
  assert.equal(process.listenerCount('SIGINT'), originalSigintListeners,
    'a failed target lookup must remove any listener installed by main');
});

test('main keeps the one-case checks-only CLI and exit behavior', async (t) => {
  const directory = temporaryDirectory();
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const output = [];
  const commands = [];
  const exitCode = await main([
    '--scenario', 'builder-controls',
    '--case', 'first-table',
    '--checks-only',
    '--evidence-parent', directory,
  ], {
    env: { ...baseEnvironment },
    commandRunner: async (_command, args) => {
      commands.push(args[0]);
      return { exitCode: 0, stdoutText: '', stderrText: '' };
    },
    targetLoader: async () => { throw new Error('checks-only must bypass target loading'); },
    write: (value) => output.push(value),
  });
  assert.equal(exitCode, 0);
  assert.ok(commands.length > 0);
  assert.ok(commands.every((command) => command === '--test'));
  const printed = JSON.parse(output.at(-1));
  assert.equal(printed.status, 'checks-passed');
  assert.equal(printed.scenario, 'builder-controls');
  assert.equal(printed.case, 'first-table');
  assert.ok(existsSync(printed.summary));
  const savedSummary = readJson(printed.summary);
  assert.equal(savedSummary.status, 'checks-passed');
  assert.equal(savedSummary.nativeStatus, 'not-run');
});
