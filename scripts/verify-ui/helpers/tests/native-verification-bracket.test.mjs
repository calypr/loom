import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry, scenarioCaseFor } from '../../registry.mjs';
import {
  runNativeVerificationBracket,
  parseOfficialPlaywrightList,
  summarizeRenderCheckpoints,
  main,
} from '../../../run-native-verification-bracket.mjs';

const root = resolve(fileURLToPath(new URL('../../../..', import.meta.url)));
const buildIdentity = 'a'.repeat(64) + ':' + 'a'.repeat(64) + ':' + 'b'.repeat(64);
const retainedCda = JSON.parse(readFileSync(new URL('./fixtures/native-bracket-retained-cda-report.json', import.meta.url), 'utf8'));
const retainedBasic = JSON.parse(readFileSync(new URL('./fixtures/native-bracket-retained-basic-report.json', import.meta.url), 'utf8'));
const wave152Failure = JSON.parse(readFileSync(new URL('./fixtures/wave152-root-quantity-pivot-failure.json', import.meta.url), 'utf8'));

function makeTarget(sourceRoot) {
  return {
    project: 'owned-project',
    generation: 'generation-1',
    composeProject: 'owned-compose',
    apiContainer: 'owned-api',
    uiContainer: 'owned-ui',
    arangoContainer: 'owned-arango',
    clickhouseContainer: 'owned-clickhouse',
    apiPort: 8188,
    uiPort: 30008,
    sourceRoot,
  };
}

function writeJson(path, data) {
  const directory = path.slice(0, path.lastIndexOf('/'));
  requireDirectory(directory);
  writeFileSync(path, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 });
}

function requireDirectory(path) {
  mkdirSync(path, { recursive: true, mode: 0o700 });
}

function option(args, name) {
  const index = args.indexOf(name);
  return index < 0 ? undefined : args[index + 1];
}

function fakeRunner({ scenarioID, caseName, rootDir, browserExit = 0, browserReport = 'pass', listTotal = 1,
  afterCaptureExit = 0, afterCaptureWritesArtifacts = true, afterHealthExit = 0, afterHealthWritesArtifact = true,
  sourceChanged = false, malformedAfterSource = false, afterHealthIdentity = buildIdentity, reportScenarioID,
  reportedChecksOverride, officialTestStatus, officialTestOutcome, officialTestResultStatuses, officialTestResultRetries,
  domainReportOverride } = {}) {
  const commands = [];
  const playwrightArgs = [];
  const scenario = registry.find((entry) => entry.id === scenarioID);
  const checks = scenarioCaseFor(scenario, caseName).requiredChecks;
  const title = 'Selected native case for ' + caseName;
  const spec = scenarioCaseFor(scenario, caseName).playwrightTest;
  const specFile = basename(spec);
  const testLine = specFile + ':12:3 › Test suite › ' + title;
  const baseEnv = {
    LOOM_CDA_SOURCE_ROOT: rootDir,
    LOOM_CDA_PROJECT: 'owned-project',
    LOOM_CDA_API_ORIGIN: 'http://127.0.0.1:8188',
    LOOM_CDA_UI_ORIGIN: 'http://127.0.0.1:30008',
    LOOM_CDA_API_CONTAINER: 'owned-api',
    LOOM_CDA_COMPOSE_PROJECT: 'owned-compose',
    LOOM_CDA_ARANGO_CONTAINER: 'owned-arango',
    LOOM_CDA_CLICKHOUSE_CONTAINER: 'owned-clickhouse',
  };

  const commandRunner = async (_command, args, { cwd, env, stdoutPath, stderrPath }) => {
    commands.push(args[0] === 'scripts/node_modules/@playwright/test/cli.js'
      ? (args.includes('--list') ? 'selectionList' : 'playwright')
      : args[0] === 'scripts/owned-stack-verification.mjs'
        ? (option(args, '--mode') === 'precheck'
          ? 'precheck'
          : option(args, '--output')?.includes('health-before') ? 'healthBefore' : 'healthAfter')
        : args[0] === 'scripts/capture-owned-verification.mjs'
          ? (option(args, '--phase') === 'before' ? 'captureBefore' : 'captureAfter')
          : 'unknown');
    assert.equal(cwd, root);
    if (args[0] === 'scripts/node_modules/@playwright/test/cli.js') {
      if (args.includes('--list')) {
        playwrightArgs.push(args);
        const total = listTotal;
        return {
          exitCode: 0,
          stdoutText: 'Listing tests:\n  ' + testLine + '\nTotal: ' + total + ' test' + (total === 1 ? '' : 's') + ' in 1 file\n',
          stderrText: '',
        };
      }

      playwrightArgs.push(args);
      const outputDirectory = option(args, '--output');
      const reportPath = env.PLAYWRIGHT_JSON_OUTPUT_FILE;
      if (browserReport !== 'missing') {
        const officialStatus = browserExit === 0 ? 'expected' : 'unexpected';
        const resultStatus = browserExit === 0 ? 'passed' : 'failed';
        const domainPath = join(outputDirectory, browserReport === 'cda' ? 'cda-report.json' : 'loom-verification-report.json');
        if (browserReport !== 'missing') {
          const assertions = browserReport === 'failed'
            ? checks.map((name, index) => ({ name, status: index === 0 ? 'failed' : 'passed' }))
            : checks.map((name) => ({ name, status: 'passed' }));
          const renderAssertion = assertions.find((assertion) => assertion.name === 'all full-population native lifecycle actions complete within five seconds each');
          if (renderAssertion) {
            renderAssertion.evidence = {
              actions: [
                { name: 'initial preview to render', durationMs: 1170 },
                { name: 'full CDA quantity Pivot edit Apply to render', durationMs: 4456 },
              ],
            };
          }
          const baseDomainReport = {
            schemaVersion: 2,
            scenario: scenarioID,
            ...(reportScenarioID !== undefined ? { scenarioID: reportScenarioID } : {}),
            ...(browserReport === 'cda' ? { caseName } : { case: caseName }),
            status: browserReport === 'failed' ? 'failed' : 'passed',
            target: { kind: browserReport === 'cda' ? 'owned-cda' : 'owned-dev-fixture' },
            dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
            requiredChecks: reportedChecksOverride ?? checks,
            missingRequiredChecks: browserReport === 'failed' ? checks.slice(0, 1) : [],
            assertions,
            actions: [{ elapsedMs: 842, status: 'passed' }],
          };
          const domainReport = domainReportOverride
            ? { ...baseDomainReport, ...domainReportOverride, target: { ...baseDomainReport.target, ...domainReportOverride.target } }
            : baseDomainReport;
          writeJson(domainPath, domainReport);
        }
        writeJson(reportPath, {
          errors: [],
          suites: [{
            title: specFile,
            file: specFile,
            line: 0,
            column: 0,
            specs: [],
            suites: [{
              title: 'Test suite',
              file: specFile,
              line: 1,
              column: 1,
              suites: [],
              specs: [{
                title,
                file: specFile,
                line: 12,
                column: 3,
                tests: [{
                  status: officialTestStatus ?? officialStatus,
                  ...(officialTestOutcome ? { outcome: officialTestOutcome } : {}),
                  results: (officialTestResultStatuses ?? [resultStatus]).map((status, index) => ({
                    status,
                    retry: officialTestResultRetries?.[index] ?? index,
                    attachments: browserReport === 'missing' ? [] : [{
                      name: browserReport === 'cda' ? 'cda-domain-report.json' : 'loom-verification-report.json',
                      contentType: 'application/json',
                      path: domainPath,
                    }],
                  })),
                }],
              }],
            }],
          }],
        });
      }
      return { exitCode: browserExit, stdoutText: '', stderrText: '' };
    }

    if (args[0] === 'scripts/owned-stack-verification.mjs') {
      const mode = option(args, '--mode');
      const output = option(args, '--output');
      if (mode === 'precheck') {
        writeJson(output, {
          exitCode: 0,
          fresh: true,
          sourceDigestMatchesCurrentMountedSource: true,
          runningBinaryMatchesRecordedBuild: true,
          targetContainer: baseEnv.LOOM_CDA_API_CONTAINER,
          apiBuildIdentity: buildIdentity,
        });
      } else {
        const after = output.includes('health-after');
        if (after && !afterHealthWritesArtifact) return { exitCode: afterHealthExit || 1, stdoutText: '', stderrText: '' };
        const apiBefore = JSON.parse(readFileSync(option(args, '--identity'), 'utf8'));
        const apiBuildIdentity = after ? afterHealthIdentity : apiBefore.apiBuildIdentity;
        const samples = Array.from({ length: 3 }, () => ({
          apiStatus: 200,
          uiStatus: 200,
          uiHasDocument: true,
          apiBuildIdentity,
        }));
        writeJson(output, { status: 'PASS', apiBuildIdentity, samples });
        return { exitCode: after ? afterHealthExit : 0, stdoutText: '', stderrText: '' };
      }
      return { exitCode: 0, stdoutText: '', stderrText: '' };
    }

    if (args[0] === 'scripts/capture-owned-verification.mjs') {
      const phase = option(args, '--phase');
      if (phase === 'after' && !afterCaptureWritesArtifacts) return { exitCode: afterCaptureExit || 1, stdoutText: '', stderrText: '' };
      const target = makeTarget(rootDir);
      const outputNames = phase === 'before'
        ? [['--source-output', 'source'], ['--docs-output', 'docs'], ['--api-output', 'api'], ['--mount-output', 'mounts']]
        : [['--source-output', 'source'], ['--docs-output', 'docs'], ['--api-output', 'api'], ['--mount-output', 'mounts']];
      const sourceChangedThisRun = phase === 'after' && sourceChanged;
      const payloads = {
        source: {
          phase,
          root: rootDir,
          fingerprint: malformedAfterSource && phase === 'after'
            ? { files: 1 }
            : { sha256: sourceChangedThisRun ? '9'.repeat(64) : 'c'.repeat(64), files: 1 },
          manifest: { [sourceChangedThisRun ? 'internal/changed.go' : 'internal/fake.go']: 'd'.repeat(64) },
        },
        docs: { phase, root: rootDir, fingerprint: { sha256: 'e'.repeat(64), files: 1 }, manifest: { 'docs/fake.md': 'f'.repeat(64) } },
        api: { phase, apiBuildIdentity: buildIdentity, target },
        mounts: { phase, status: 'PASS', target },
      };
      for (const [flag, name] of outputNames) {
        const outputPath = option(args, flag);
        const key = phase === 'before' ? outputPath.includes('source-before') ? 'source'
          : outputPath.includes('docs-before') ? 'docs'
            : outputPath.includes('api-identity-before') ? 'api' : 'mounts'
          : outputPath.includes('source-after') ? 'source'
            : outputPath.includes('docs-after') ? 'docs'
              : outputPath.includes('api-identity-after') ? 'api' : 'mounts';
        writeJson(outputPath, payloads[key]);
      }
      return { exitCode: phase === 'after' ? afterCaptureExit : 0, stdoutText: '', stderrText: '' };
    }

    if (stdoutPath && !existsSync(stdoutPath)) writeFileSync(stdoutPath, '', { mode: 0o600 });
    if (stderrPath && !existsSync(stderrPath)) writeFileSync(stderrPath, '', { mode: 0o600 });
    return { exitCode: 99, stdoutText: '', stderrText: '' };
  };
  return { commandRunner, commands, playwrightArgs, env: baseEnv, specFile, title };
}

function evidenceParent() {
  return mkdtempSync(join(tmpdir(), 'native-bracket-test-'));
}

test('official Playwright list must resolve exactly one test from the registered spec', () => {
  assert.deepEqual(parseOfficialPlaywrightList(
    'Listing tests:\n  root-quantity-pivot.spec.mjs:37:3 › Root quantity › exact lifecycle\nTotal: 1 test in 1 file\n',
    'scripts/verify-ui/specs/root-quantity-pivot.spec.mjs',
  ), {
    total: 1,
    listedFileCount: 1,
    cases: [{
      file: 'root-quantity-pivot.spec.mjs',
      line: 37,
      column: 3,
      titlePath: 'Root quantity › exact lifecycle',
      title: 'exact lifecycle',
    }],
    exact: true,
    reason: null,
  });

  assert.equal(parseOfficialPlaywrightList(
    'Listing tests:\nTotal: 2 tests in 1 file\n',
    'scripts/verify-ui/specs/root-quantity-pivot.spec.mjs',
  ).exact, false);
  assert.equal(parseOfficialPlaywrightList(
    'Listing tests:\n  root-quantity-pivot.spec.mjs:37:3 › Root quantity › exact lifecycle\nTotal: 1 test in 2 files\n',
    'scripts/verify-ui/specs/root-quantity-pivot.spec.mjs',
  ).exact, false);
});

test('retained CDA report shape exposes the registered render checkpoints', () => {
  const checks = scenarioCaseFor('root-quantity-pivot', 'full-population-lifecycle').requiredChecks;
  const summary = summarizeRenderCheckpoints(retainedCda.report, checks);

  assert.equal(retainedCda.source.sha256,
    '025a3cb0c01fdaebe7f638ad141b674c66ded016446d966d0789089f82ee4cb2');
  assert.equal(retainedCda.report.status, 'passed');
  assert.equal(summary.count, 14);
  assert.equal(summary.maximumDurationMs, 4456);
  assert.equal(summary.checkpoints.at(-1).evidencePath, 'assertions[].evidence.actions[].durationMs');
});

test('retained basic report shape exposes registered render timings without claiming lifecycle success', () => {
  const checks = scenarioCaseFor('builder-combine-draft', 'group-pivot-append').requiredChecks;
  const summary = summarizeRenderCheckpoints(retainedBasic.report, checks);

  assert.equal(retainedBasic.source.sha256,
    'a63584160a1aad33328c18498b7eca1b99da386bdd359527c1ce6f0895574192');
  assert.equal(retainedBasic.report.status, 'failed');
  assert.deepEqual(retainedBasic.report.requiredChecks, checks);
  assert.equal(summary.count, 5);
  assert.equal(summary.maximumDurationMs, 566);
  assert.ok(summary.checkpoints.every((checkpoint) => checks.includes(checkpoint.checkName)));
});

test('CDA report shape closes only when all registered lifecycle checks and the after bracket pass', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserReport: 'cda',
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'Root quantity Pivot full population lifecycle matches SUM and MAX output and restores the full CDA source after reload$',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'passed', JSON.stringify({ notes: summary.notes, lifecycle: summary.lifecycle, integrity: summary.integrity, commands: summary.commands }, null, 2));
  assert.equal(summary.lifecycle.status, 'passed');
  assert.equal(summary.lifecycle.passedCheckCount, scenarioCaseFor('root-quantity-pivot', 'full-population-lifecycle').requiredChecks.length);
  assert.equal(summary.integrity.status, 'PASS');
  assert.deepEqual(fake.commands.slice(-2), ['captureAfter', 'healthAfter']);
  assert.equal(summary.commands.playwright.environmentOverrides.PLAYWRIGHT_JSON_OUTPUT_FILE,
    summary.evidence.playwrightReport);
  assert.equal(summary.lifecycle.renderCheckpointCount, 2);
  assert.equal(summary.lifecycle.maximumRenderCheckpointLatencyMs, 4456);
  assert.equal(summary.lifecycle.maximumMeasuredActionLatencyMs, 842,
    'Top-level click action latency remains separate from nested render checkpoint latency.');
  assert.equal(summary.reviewPacket.maximumRenderCheckpointLatencyMs, 4456);
  assert.equal(summary.reviewPacket.renderCheckpointCount, 2);
  assert.equal(summary.reviewPacket.firstFailureReason, null);
  assert.equal(summary.reviewPacket.failedAction, null);
  assert.deepEqual(summary.reviewPacket.pendingOwnedRequests, []);
  assert.equal(fake.playwrightArgs.length, 2);
  for (const args of fake.playwrightArgs) {
    assert.equal(option(args, '--workers'), '1');
    assert.equal(option(args, '--retries'), '0');
  }
  assert.equal(summary.runDirectory.startsWith(root + '/'), false);
  assert.equal(JSON.parse(readFileSync(summary.evidence.summary, 'utf8')).status, 'passed');
});

test('basic fixture report shape is accepted from its Playwright attachment', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'builder-combine-draft',
    caseName: 'group-pivot-append',
    rootDir: root,
    browserReport: 'basic',
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'builder-combine-draft',
    caseName: 'group-pivot-append',
    grep: 'Group→Pivot APPEND native lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'passed', JSON.stringify({ notes: summary.notes, lifecycle: summary.lifecycle, integrity: summary.integrity, commands: summary.commands }, null, 2));
  assert.equal(summary.lifecycle.status, 'passed');
  assert.equal(summary.lifecycle.requiredCheckCount,
    scenarioCaseFor('builder-combine-draft', 'group-pivot-append').requiredChecks.length);
  assert.equal(summary.integrity.status, 'PASS');
  assert.equal(summary.reviewPacket.firstFailureReason, null);
  assert.equal(summary.reviewPacket.failedAction, null);
  assert.deepEqual(summary.reviewPacket.pendingOwnedRequests, []);
});

test('conflicting scenario identity aliases cannot pass an attached report', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserReport: 'cda',
    reportScenarioID: 'different-scenario',
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.lifecycle.status, 'unverified');
  assert.equal(summary.status, 'unverified');
});

test('a report with the wrong registry required-check list cannot pass', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserReport: 'cda',
    reportedChecksOverride: ['not-a-registered-check'],
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.lifecycle.status, 'unverified');
  assert.equal(summary.lifecycle.requiredCheckListMatchesRegistry, false);
  assert.equal(summary.status, 'unverified');
});

test('a flaky Playwright result does not count as a passed lifecycle', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserReport: 'cda',
    officialTestStatus: 'flaky',
    officialTestOutcome: 'flaky',
    officialTestResultStatuses: ['failed', 'passed'],
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.lifecycle.status, 'failed');
  assert.equal(summary.status, 'failed');
});

test('a retried or repeated Playwright result cannot count as a clean pass', async (t) => {
  const variants = [
    { label: 'nonzero retry', officialTestResultStatuses: ['passed'], officialTestResultRetries: [1] },
    { label: 'multiple results', officialTestResultStatuses: ['passed', 'passed'], officialTestResultRetries: [0, 1] },
  ];
  for (const variant of variants) {
    const parent = evidenceParent();
    t.after(() => rmSync(parent, { recursive: true, force: true }));
    const fake = fakeRunner({
      scenarioID: 'root-quantity-pivot',
      caseName: 'full-population-lifecycle',
      rootDir: root,
      browserReport: 'cda',
      ...variant,
    });
    const summary = await runNativeVerificationBracket({
      scenarioID: 'root-quantity-pivot',
      caseName: 'full-population-lifecycle',
      grep: 'lifecycle',
      evidenceParent: parent,
      root,
      env: fake.env,
      commandRunner: fake.commandRunner,
    });

    assert.equal(summary.lifecycle.status, 'failed', variant.label);
    assert.equal(summary.status, 'failed', variant.label);
  }
});

test('after capture and health run after a failing browser command', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserExit: 1,
    browserReport: 'failed',
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.commands.playwright.exitCode, 1);
  assert.deepEqual(fake.commands.slice(-2), ['captureAfter', 'healthAfter']);
  assert.equal(summary.integrity.status, 'PASS');
  assert.equal(summary.status, 'failed');
});

test('missing domain report remains unverified after the bracket closes', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    browserReport: 'missing',
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'unverified');
  assert.equal(summary.lifecycle.status, 'unverified');
  assert.equal(summary.reviewPacket.firstFailureReason, null);
  assert.equal(summary.reviewPacket.failedAction, null);
  assert.equal(summary.reviewPacket.lastCompletedAction, null);
  assert.deepEqual(summary.reviewPacket.pendingOwnedRequests, []);
  assert.deepEqual(fake.commands.slice(-2), ['captureAfter', 'healthAfter']);
});

test('missing after-capture evidence stays unverified and does not skip after health', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    afterCaptureExit: 1,
    afterCaptureWritesArtifacts: false,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'unverified');
  assert.equal(summary.commands.captureAfter.exitCode, 1);
  assert.equal(summary.commands.healthAfter.exitCode, 0);
  assert.equal(existsSync(summary.evidence.sourceAfter), false);
  assert.equal(existsSync(summary.evidence.healthAfter), true);
  assert.equal(summary.integrity.status, 'UNVERIFIED');
});

test('missing after-health evidence cannot close the bracket', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    afterHealthExit: 1,
    afterHealthWritesArtifact: false,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.commands.captureAfter.exitCode, 0);
  assert.equal(summary.commands.healthAfter.exitCode, 1);
  assert.equal(existsSync(summary.evidence.healthAfter), false);
  assert.equal(summary.integrity.status, 'UNVERIFIED');
  assert.equal(summary.status, 'unverified');
});

test('malformed source fingerprint capture remains unverified', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    malformedAfterSource: true,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.integrity.dimensions.source.status, 'UNVERIFIED');
  assert.equal(summary.status, 'unverified');
});

test('health identity mismatch invalidates otherwise complete evidence', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    afterHealthIdentity: 'c'.repeat(64) + ':' + 'c'.repeat(64) + ':' + 'd'.repeat(64),
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.integrity.dimensions.healthAfter.status, 'FAIL');
  assert.equal(summary.integrity.status, 'FAIL');
  assert.equal(summary.status, 'failed');
});

test('source fingerprint changes invalidate an otherwise passing lifecycle', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    sourceChanged: true,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'failed');
  assert.equal(summary.integrity.status, 'FAIL');
  assert.equal(summary.reviewPacket.integrityStatus, 'FAIL');
  assert.equal(summary.reviewPacket.status, 'failed');
  assert.deepEqual(summary.integrity.dimensions.source.changedPaths, [
    { path: 'internal/changed.go', change: 'added' },
    { path: 'internal/fake.go', change: 'removed' },
  ]);
});

test('ambiguous official selection prevents the browser launch', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
    listTotal: 2,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'lifecycle',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'unverified');
  assert.equal(summary.commands.precheck, undefined);
  assert.equal(summary.commands.playwright, undefined);
  assert.deepEqual(fake.commands, ['selectionList']);
});

test('wave152 timeout appears in the review packet and CLI with the last action and pending owned endpoint', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'related-text-only-full-population-lifecycle';
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'cda',
    domainReportOverride: wave152Failure,
  });
  let runSummary;
  const cliOutput = [];
  const exitCode = await main([
    '--scenario', scenarioID,
    '--case', caseName,
    '--grep', 'wave152 retained failure',
  ], {
    runBracket: async (options) => {
      runSummary = await runNativeVerificationBracket({
        ...options,
        evidenceParent: parent,
        root,
        env: fake.env,
        commandRunner: fake.commandRunner,
      });
      return runSummary;
    },
    write: (value) => cliOutput.push(value),
  });

  const expectedRequest = {
    endpoint: 'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/construction-category-discoveries',
    requestID: 'wave152-category-discovery',
    status: 'pending',
  };
  assert.equal(exitCode, 1);
  assert.equal(runSummary.status, 'failed');
  assert.equal(runSummary.integrity.status, 'PASS');
  assert.equal(runSummary.reviewPacket.firstFailureReason, 'Timeout 5000ms exceeded.');
  assert.equal(runSummary.reviewPacket.failedAction, null);
  assert.deepEqual(runSummary.reviewPacket.lastCompletedAction, { label: 'Select SUM', status: 'passed' });
  assert.deepEqual(runSummary.reviewPacket.pendingOwnedRequests, [expectedRequest]);
  assert.deepEqual(JSON.parse(readFileSync(runSummary.evidence.summary, 'utf8')).reviewPacket, runSummary.reviewPacket);
  assert.equal(cliOutput.length, 1);
  const printed = JSON.parse(cliOutput[0]);
  assert.equal(printed.status, 'failed');
  assert.equal(printed.integrity, 'PASS');
  assert.equal(printed.firstFailureReason, 'Timeout 5000ms exceeded.');
  assert.equal(printed.failedAction, null);
  assert.deepEqual(printed.lastCompletedAction, { label: 'Select SUM', status: 'passed' });
  assert.deepEqual(printed.pendingOwnedRequests, [expectedRequest]);
});

test('a pending owned request does not invent a failure on a passing report', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'related-text-only-full-population-lifecycle';
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserReport: 'cda',
    domainReportOverride: {
      target: wave152Failure.target,
      actions: [{ label: 'Select SUM', status: 'passed' }],
      nativeRequests: wave152Failure.nativeRequests,
    },
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'passing report with a pending request',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.status, 'passed');
  assert.equal(summary.reviewPacket.firstFailureReason, null);
  assert.equal(summary.reviewPacket.failedAction, null);
  assert.deepEqual(summary.reviewPacket.lastCompletedAction, { label: 'Select SUM', status: 'passed' });
  assert.equal(summary.reviewPacket.pendingOwnedRequests.length, 1);
  assert.equal(summary.reviewPacket.pendingOwnedRequests[0].status, 'pending');
});

test('preparation failure reports a sanitized reason and empty domain diagnostics', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'related-text-only-full-population-lifecycle',
    rootDir: root,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'related-text-only-full-population-lifecycle',
    grep: 'preparation failure',
    evidenceParent: parent,
    root,
    env: { ...fake.env, LOOM_CDA_API_ORIGIN: '' },
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.failureCategory, 'preparation');
  assert.equal(summary.reviewPacket.firstFailureReason,
    'Missing required owned environment names: LOOM_CDA_API_ORIGIN');
  assert.equal(summary.reviewPacket.failedAction, null);
  assert.equal(summary.reviewPacket.lastCompletedAction, null);
  assert.deepEqual(summary.reviewPacket.pendingOwnedRequests, []);
  assert.deepEqual(fake.commands, []);
});

test('failed action is separate from the last completed action', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'related-text-only-full-population-lifecycle';
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'cda',
    domainReportOverride: {
      ...wave152Failure,
      failureEvidence: {
        action: { label: 'Apply Pivot', status: 'failed', error: 'Error: Apply Pivot failed' },
      },
      actions: [
        { label: 'Select SUM', status: 'passed' },
        { label: 'Apply Pivot', status: 'failed' },
      ],
    },
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'failed action',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(summary.reviewPacket.firstFailureReason, 'Error: Apply Pivot failed');
  assert.deepEqual(summary.reviewPacket.failedAction, { label: 'Apply Pivot', status: 'failed' });
  assert.deepEqual(summary.reviewPacket.lastCompletedAction, { label: 'Select SUM', status: 'passed' });
});

test('request ID reuse keeps only requests whose status is pending', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'related-text-only-full-population-lifecycle';
  const original = wave152Failure.nativeRequests[0];
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'cda',
    domainReportOverride: {
      ...wave152Failure,
      nativeRequests: [
        { ...original, status: 200, completedAt: '2026-10-07T12:00:01.000Z' },
        { ...original, status: 'pending' },
        { ...original, status: 'failed', endedAt: '2026-10-07T12:00:02.000Z' },
        { ...original, requestId: 'unknown-route', path: original.path + '/unknown', status: 'pending' },
      ],
    },
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'request ID reuse',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.deepEqual(summary.reviewPacket.pendingOwnedRequests, [{
    endpoint: 'POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/construction-category-discoveries',
    requestID: 'wave152-category-discovery',
    status: 'pending',
  }]);
});

test('review diagnostics do not expose paths, bodies, tokens, auth, or query values', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'related-text-only-full-population-lifecycle';
  const report = structuredClone(wave152Failure);
  report.failureEvidence.reason = 'Error: token=TOKEN_SENTINEL query=QUERY_SENTINEL body=BODY_SENTINEL authorization=AUTH_SENTINEL path=/private/secret/file at $CHECKOUT/scripts/private.mjs';
  report.failureEvidence.action = { label: 'Choose source:cc2.SENSITIVE_TOKEN_VALUE', status: 'failed' };
  report.nativeRequests[0] = {
    ...report.nativeRequests[0],
    path: report.nativeRequests[0].path + '?query=URL_QUERY_SENTINEL&token=URL_TOKEN_SENTINEL',
    query: { filter: 'QUERY_OBJECT_SENTINEL' },
    authorizationHeaderPresent: true,
    authorization: 'AUTH_OBJECT_SENTINEL',
    body: { content: 'BODY_OBJECT_SENTINEL' },
  };
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'cda',
    domainReportOverride: report,
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'sensitive field negative',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });
  const diagnostics = JSON.stringify({
    firstFailureReason: summary.reviewPacket.firstFailureReason,
    failedAction: summary.reviewPacket.failedAction,
    lastCompletedAction: summary.reviewPacket.lastCompletedAction,
    pendingOwnedRequests: summary.reviewPacket.pendingOwnedRequests,
  });

  for (const secret of [
    'TOKEN_SENTINEL', 'QUERY_SENTINEL', 'BODY_SENTINEL', 'AUTH_SENTINEL',
    'SENSITIVE_TOKEN_VALUE', 'URL_QUERY_SENTINEL', 'URL_TOKEN_SENTINEL',
    'QUERY_OBJECT_SENTINEL', 'AUTH_OBJECT_SENTINEL', 'BODY_OBJECT_SENTINEL',
    '$CHECKOUT', '/private/secret/file', 'scripts/private.mjs',
  ]) assert.equal(diagnostics.includes(secret), false, secret);
  assert.equal(summary.reviewPacket.firstFailureReason, 'Failure details contained sensitive values.');
  assert.match(diagnostics, /\[redacted token\]/);

  const pathOnlyReport = structuredClone(wave152Failure);
  pathOnlyReport.failureEvidence.reason = 'Error: failed at scripts/private.mjs';
  const pathOnlyFake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'cda',
    domainReportOverride: pathOnlyReport,
  });
  const pathOnlySummary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'relative path and query negative',
    evidenceParent: parent,
    root,
    env: pathOnlyFake.env,
    commandRunner: pathOnlyFake.commandRunner,
  });
  assert.equal(pathOnlySummary.reviewPacket.firstFailureReason, 'Error: failed at [redacted path]');
  assert.equal(pathOnlySummary.reviewPacket.firstFailureReason.includes('scripts/private.mjs'), false);
});
