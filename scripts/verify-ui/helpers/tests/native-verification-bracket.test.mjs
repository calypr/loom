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
  runProcess,
  main,
} from '../../../run-native-verification-bracket.mjs';

const root = resolve(fileURLToPath(new URL('../../../..', import.meta.url)));
const buildIdentity = 'a'.repeat(64) + ':' + 'a'.repeat(64) + ':' + 'b'.repeat(64);
const retainedCda = JSON.parse(readFileSync(new URL('./fixtures/native-bracket-retained-cda-report.json', import.meta.url), 'utf8'));
const retainedBasic = JSON.parse(readFileSync(new URL('./fixtures/native-bracket-retained-basic-report.json', import.meta.url), 'utf8'));
const retainedPostPivotCountTimings = JSON.parse(readFileSync(new URL('./fixtures/post-pivot-count-qzzOck-timings.json', import.meta.url), 'utf8'));
const wave152Failure = JSON.parse(readFileSync(new URL('./fixtures/wave152-root-quantity-pivot-failure.json', import.meta.url), 'utf8'));
const groupAddFieldsPerformanceCheckName = 'All native Group-add-fields lifecycle actions complete within five seconds';
const groupAddFieldsLifecycleCheckpoints = [
  { name: 'load-to-render', durationMs: 1356 },
  { name: 'expand-Specimen-Patient', durationMs: 913 },
  { name: 'apply-to-render', durationMs: 589 },
  { name: 'expand-Patient-Observation', durationMs: 939 },
  { name: 'apply-to-render', durationMs: 565 },
  { name: 'load-to-render', durationMs: 1297 },
  { name: 'related-many-group-preview', durationMs: 723 },
  { name: 'group-cancel-to-render', durationMs: 228 },
  { name: 'confirmed-related-many-group-preview', durationMs: 715 },
  { name: 'apply-to-render', durationMs: 477 },
  { name: 'load-to-render', durationMs: 1289 },
  { name: 'group-source-field-preview', durationMs: 468 },
  { name: 'add-fields-cancel-to-render', durationMs: 196 },
  { name: 'group-source-field-preview', durationMs: 595 },
  { name: 'group-source-field-apply', durationMs: 505 },
  { name: 'load-to-render', durationMs: 1298 },
  { name: 'native-label-edit-to-exact-rows', durationMs: 940 },
  { name: 'load-to-render', durationMs: 1293 },
  { name: 'remove-group-source-field', durationMs: 406 },
  { name: 'load-to-render', durationMs: 1279 },
];
const groupAddFieldsPerformanceEvidence = {
  nativeActionCount: 40,
  maximumNativeActionDurationMs: 217,
  failedActions: [],
  lifecycleCheckpointDurations: groupAddFieldsLifecycleCheckpoints,
  maximumCheckpointDurationMs: 1356,
};
const groupOnePerformanceCheckName = 'all Group ONE action-to-render checkpoints complete within five seconds';
const groupOneTimingCheckpoints = [
  { name: 'initial-specimen-load-to-render', durationMs: 1323, budgetMs: 5000, passed: true },
  { name: 'specimen-to-patient-preview', durationMs: 914, budgetMs: 5000, passed: true },
  { name: 'specimen-to-patient-apply-to-render', durationMs: 561, budgetMs: 5000, passed: true },
  { name: 'after-expansion-load-to-render', durationMs: 1312, budgetMs: 5000, passed: true },
  { name: 'patient-group-preview-cancelled', durationMs: 699, budgetMs: 5000, passed: true },
  { name: 'patient-group-preview-confirmed', durationMs: 696, budgetMs: 5000, passed: true },
  { name: 'patient-group-apply-to-render', durationMs: 493, budgetMs: 5000, passed: true },
  { name: 'after-group-load-to-render', durationMs: 1310, budgetMs: 5000, passed: true },
  { name: 'one-disagreement-diagnostic', durationMs: 1015, budgetMs: 5000, passed: true },
  { name: 'all-repair-preview', durationMs: 413, budgetMs: 5000, passed: true },
  { name: 'all-repair-apply-to-render', durationMs: 530, budgetMs: 5000, passed: true },
  { name: 'after-all-repair-load-to-render', durationMs: 1292, budgetMs: 5000, passed: true },
  { name: 'remove-repaired-field', durationMs: 402, budgetMs: 5000, passed: true },
  { name: 'after-removal-load-to-render', durationMs: 1278, budgetMs: 5000, passed: true },
];
const relatedUnpivotPerformanceCheckName = 'All native action and action-to-render checkpoints complete within five seconds';
const relatedUnpivotWorkflowCheckpoints = [
  { name: 'load-to-render', durationMs: 1368 },
  { name: 'expand-Specimen-Patient', durationMs: 1129 },
  { name: 'apply-to-render', durationMs: 558 },
  { name: 'expand-Patient-Condition', durationMs: 1123 },
  { name: 'apply-to-render', durationMs: 571 },
  { name: 'expand-Condition-Observation', durationMs: 1159 },
  { name: 'apply-to-render', durationMs: 591 },
  { name: 'expand-Observation-Patient', durationMs: 1198 },
  { name: 'apply-to-render', durationMs: 613 },
  { name: 'load-to-render', durationMs: 1299 },
  { name: 'related-chain-unpivot-preview', durationMs: 939 },
  { name: 'Cancel Unpivot to exact Related rows', durationMs: 726 },
  { name: 'confirmed-related-chain-unpivot-preview', durationMs: 865 },
  { name: 'apply-to-render', durationMs: 510 },
  { name: 'load-to-render', durationMs: 1306 },
  { name: 'edit-unpivot-policy-preview', durationMs: 1188 },
  { name: 'apply-to-render', durationMs: 514 },
  { name: 'load-to-render', durationMs: 1315 },
  { name: 'unpivot-value-missing-preview', durationMs: 961 },
  { name: 'Cancel Unpivot Value Filter to exact saved Unpivot rows', durationMs: 726 },
  { name: 'confirmed-unpivot-value-missing-preview', durationMs: 975 },
  { name: 'apply-to-render', durationMs: 546 },
  { name: 'load-to-render', durationMs: 1323 },
  { name: 'unpivot-value-equality-preview', durationMs: 1248 },
  { name: 'apply-to-render', durationMs: 512 },
  { name: 'load-to-render', durationMs: 1325 },
  { name: 'remove-unpivot-and-dependent-filter-preview', durationMs: 868 },
  { name: 'Cancel Unpivot removal to exact saved rows', durationMs: 1624 },
  { name: 'remove-unpivot-and-dependent-filter-preview', durationMs: 858 },
  { name: 'apply-to-render', durationMs: 454 },
  { name: 'load-to-render', durationMs: 1291 },
];
const retainedMembershipSetupFailure = {
  errors: [],
  suites: [{
    title: 'cda-current-draft-membership.spec.mjs',
    file: 'cda-current-draft-membership.spec.mjs',
    specs: [],
    suites: [{
      title: 'CDA current-draft GROUP to GROUP Membership',
      file: 'cda-current-draft-membership.spec.mjs',
      specs: [{
        title: 'native INCLUDE and EXCLUDE Membership use two exact grouped Observation ID populations',
        file: 'cda-current-draft-membership.spec.mjs',
        line: 11,
        column: 3,
        tests: [{
          status: 'unexpected',
          results: [{
            status: 'failed',
            retry: 0,
            error: {
              message: 'Error: development UI defaults do not target this session fixture and bootstrap Explorer',
              stack: 'Error: development UI defaults do not target this session fixture and bootstrap Explorer\n    at inspectOwnedResources (/private/tmp/loom-construction-implementation/scripts/loom-dev.mjs:860:17)\n    at Object.cda (/private/tmp/loom-construction-implementation/scripts/verify-ui/helpers/cda-fixtures.mjs:331:7)',
            },
            errors: [{
              message: 'Error: development UI defaults do not target this session fixture and bootstrap Explorer',
            }],
          }],
        }],
      }],
    }],
  }],
};

function makeTarget(sourceRoot, expectedIdentity) {
  return {
    project: expectedIdentity?.project ?? 'owned-project',
    generation: expectedIdentity?.generation ?? 'generation-1',
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
  domainReportOverride, playwrightReportOverride, listedTestLine, unrelatedTimingAssertion = false } = {}) {
  const commands = [];
  const playwrightArgs = [];
  const scenario = registry.find((entry) => entry.id === scenarioID);
  const contract = scenarioCaseFor(scenario, caseName);
  const checks = contract.requiredChecks;
  const title = 'Selected native case for ' + caseName;
  const spec = scenarioCaseFor(scenario, caseName).playwrightTest;
  const specFile = basename(spec);
  const testLine = listedTestLine ?? specFile + ':12:3 › Test suite › ' + title;
  const baseEnv = {
    LOOM_CDA_SOURCE_ROOT: rootDir,
    LOOM_CDA_PROJECT: contract.expectedIdentity?.project ?? 'owned-project',
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
      if (browserReport !== 'missing' || playwrightReportOverride) {
        const officialStatus = browserExit === 0 ? 'expected' : 'unexpected';
        const resultStatus = browserExit === 0 ? 'passed' : 'failed';
        const domainPath = join(outputDirectory, browserReport === 'cda' ? 'cda-report.json' : 'loom-verification-report.json');
        if (browserReport !== 'missing') {
          const assertions = browserReport === 'failed'
            ? checks.map((name, index) => ({ name, status: index === 0 ? 'failed' : 'passed' }))
            : checks.map((name) => ({ name, status: 'passed' }));
          const renderAssertion = assertions.find((assertion) =>
            /full-population/i.test(assertion.name) && /action-to-render|within five seconds|within budget/i.test(assertion.name));
          if (renderAssertion) {
            renderAssertion.evidence = {
              actions: [
                { name: 'initial preview to render', durationMs: 1170 },
                { name: 'full CDA quantity Pivot edit Apply to render', durationMs: 4456 },
              ],
            };
          }
          if (unrelatedTimingAssertion) {
            assertions[0].evidence = { elapsedMs: 9001 };
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
        writeJson(reportPath, playwrightReportOverride ?? {
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
      const target = makeTarget(rootDir, contract.expectedIdentity);
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
        const filename = basename(outputPath);
        const key = phase === 'before' ? filename.includes('source-before') ? 'source'
          : filename.includes('docs-before') ? 'docs'
            : filename.includes('api-identity-before') ? 'api' : 'mounts'
          : filename.includes('source-after') ? 'source'
            : filename.includes('docs-after') ? 'docs'
              : filename.includes('api-identity-after') ? 'api' : 'mounts';
        writeJson(outputPath, payloads[key]);
      }
      return { exitCode: phase === 'after' ? afterCaptureExit : 0, stdoutText: '', stderrText: '' };
    }

    if (args[0] === '--test') {
      return { exitCode: 0, stdoutText: 'registered node tests passed\n', stderrText: '' };
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

test('retained CDA report shape exposes its recorded render checkpoints', () => {
  const checks = retainedCda.report.requiredChecks;
  const summary = summarizeRenderCheckpoints(retainedCda.report, {
    performanceCheckNames: [retainedCda.report.assertions[0].name],
    requiredCheckNames: checks,
  });

  assert.equal(retainedCda.source.sha256,
    '025a3cb0c01fdaebe7f638ad141b674c66ded016446d966d0789089f82ee4cb2');
  assert.equal(retainedCda.report.status, 'passed');
  assert.equal(summary.count, 14);
  assert.equal(summary.maximumDurationMs, 4456);
  assert.equal(summary.checkpoints.at(-1).evidencePath, 'assertions[].evidence.actions[].durationMs');
});

test('COUNT lifecycle summary derives declared dimensions from the retained qzzOck timings', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'standalone-reshape-related-source-after-pivot';
  const caseName = 'related-source-count-after-pivot';
  const contract = scenarioCaseFor(scenarioID, caseName);
  const assertions = contract.requiredChecks.map((name) => ({ name, status: 'passed' }));
  assertions.find((assertion) => assertion.name === contract.lifecycleEvidence.performance.check).evidence = {
    actionCount: retainedPostPivotCountTimings.actionCount,
    measuredTransitionCount: retainedPostPivotCountTimings.cases.length,
    maxActionMs: retainedPostPivotCountTimings.maxActionMs,
    workflowCheckpoints: retainedPostPivotCountTimings.cases.map(({ name, elapsedMs }) => ({ name, durationMs: elapsedMs })),
  };
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserReport: 'cda',
    domainReportOverride: {
      schemaVersion: 2,
      scenario: scenarioID,
      case: caseName,
      status: 'passed',
      target: { kind: 'owned-cda' },
      dimensions: {
        usability: { status: 'passed' },
        correctness: { status: 'passed' },
        persistence: { status: 'untested' },
        performance: { status: 'untested' },
      },
      requiredChecks: contract.requiredChecks,
      missingRequiredChecks: [],
      assertions,
      cases: retainedPostPivotCountTimings.cases,
    },
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: contract.playwrightGrep,
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.equal(retainedPostPivotCountTimings.sourceReportSha256,
    '5587cd186fbb00f1c8bd34470e9799522d8c7974c8faa6deb4603b6bd553b829');
  assert.equal(retainedPostPivotCountTimings.sourceSummarySha256,
    '19262bd541a31ae09a0134c9b02e04476ff8b04121edfde59253536deffb309c');
  assert.equal(retainedPostPivotCountTimings.cases.length, 26);
  assert.equal(summary.status, 'passed', JSON.stringify({ lifecycle: summary.lifecycle, integrity: summary.integrity, notes: summary.notes }, null, 2));
  assert.equal(summary.lifecycle.dimensions.persistence, 'passed');
  assert.equal(summary.lifecycle.dimensions.performance, 'passed');
  assert.equal(summary.lifecycle.renderCheckpointCount, 26);
  assert.equal(summary.lifecycle.maximumRenderCheckpointLatencyMs, 1417);
  assert.deepEqual(summary.lifecycle.dimensionEvidence.persistence.checks.map(({ status }) => status),
    ['passed', 'passed', 'passed', 'passed']);
  assert.equal(summary.lifecycle.dimensionEvidence.performance.evidencePaths[0],
    'assertions[].evidence.workflowCheckpoints[].durationMs');
  assert.equal(JSON.parse(readFileSync(summary.evidence.summary, 'utf8')).lifecycle.renderCheckpointCount, 26);
});

test('Group-add-fields performance evidence contributes its complete lifecycle checkpoint list', () => {
  const checkName = groupAddFieldsPerformanceCheckName;
  const report = {
    assertions: [{
      name: checkName,
      dimension: 'performance',
      status: 'passed',
      evidence: groupAddFieldsPerformanceEvidence,
    }],
  };
  const checks = scenarioCaseFor('standalone-reshape-group-add-fields', 'group-add-fields').requiredChecks;
  const summary = summarizeRenderCheckpoints(report, {
    performanceCheckNames: [checkName],
    requiredCheckNames: checks,
  });

  assert.ok(checks.includes(checkName));
  assert.equal(summary.count, 20);
  assert.equal(summary.maximumDurationMs, 1356);
  assert.deepEqual(summary.checkpoints.map(({ name, durationMs }) => ({ name, durationMs })), groupAddFieldsLifecycleCheckpoints);
  assert.equal(summary.checkpoints[0].evidencePath,
    'assertions[].evidence.lifecycleCheckpointDurations[].durationMs');
});

test('Group ONE timingCheckpoints contribute the declared action-to-render measurements', () => {
  const report = {
    assertions: [{
      name: groupOnePerformanceCheckName,
      dimension: 'performance',
      status: 'passed',
      evidence: {
        expectedCheckpoints: groupOneTimingCheckpoints.map(({ name }) => name),
        actualCheckpoints: groupOneTimingCheckpoints.map(({ name }) => name),
        timingCheckpoints: groupOneTimingCheckpoints,
        maximumDurationMs: 1323,
        budgetMs: 5000,
      },
    }],
  };
  const summary = summarizeRenderCheckpoints(report, {
    performanceCheckNames: [groupOnePerformanceCheckName],
    requiredCheckNames: [groupOnePerformanceCheckName],
  });

  assert.equal(summary.count, 14);
  assert.equal(summary.maximumDurationMs, 1323);
  assert.deepEqual(summary.checkpoints.map(({ name, durationMs }) => ({ name, durationMs })),
    groupOneTimingCheckpoints.map(({ name, durationMs }) => ({ name, durationMs })));
  assert.equal(summary.checkpoints[0].evidencePath,
    'assertions[].evidence.timingCheckpoints[].durationMs');
});

test('Related Unpivot workflowCheckpoints contribute their exact render measurements', () => {
  const report = {
    assertions: [{
      name: relatedUnpivotPerformanceCheckName,
      dimension: 'performance',
      status: 'passed',
      evidence: {
        workflowCheckpoints: relatedUnpivotWorkflowCheckpoints,
        maximumNativeActionMs: 206,
      },
    }],
  };
  const summary = summarizeRenderCheckpoints(report, {
    performanceCheckNames: [relatedUnpivotPerformanceCheckName],
    requiredCheckNames: [relatedUnpivotPerformanceCheckName],
  });

  assert.equal(summary.count, 31);
  assert.equal(summary.maximumDurationMs, 1624);
  assert.deepEqual(summary.checkpoints.map(({ name, durationMs }) => ({ name, durationMs })),
    relatedUnpivotWorkflowCheckpoints);
  assert.equal(summary.checkpoints[0].evidencePath,
    'assertions[].evidence.workflowCheckpoints[].durationMs');
});

test('malformed or negative timingCheckpoints remain unverified', () => {
  const invalidEvidence = [
    { timingCheckpoints: '14 timing checkpoints' },
    { timingCheckpoints: [] },
    { timingCheckpoints: [{ name: 'valid', durationMs: 50, budgetMs: 5000, passed: true }, { durationMs: 40 }] },
    { timingCheckpoints: [{ name: 'negative duration', durationMs: -1, budgetMs: 5000, passed: false }] },
    { timingCheckpoints: [{ name: 'invalid duration', durationMs: 'fast', budgetMs: 5000, passed: true }] },
  ];

  for (const evidence of invalidEvidence) {
    const summary = summarizeRenderCheckpoints({
      assertions: [{ name: groupOnePerformanceCheckName, evidence }],
    }, {
      performanceCheckNames: [groupOnePerformanceCheckName],
      requiredCheckNames: [groupOnePerformanceCheckName],
    });
    assert.equal(summary.count, 0);
    assert.equal(summary.maximumDurationMs, null);
    assert.deepEqual(summary.checkpoints, []);
  }
});

test('missing or malformed checkpoint lists remain unverified', () => {
  const checkName = groupAddFieldsPerformanceCheckName;
  const checks = scenarioCaseFor('standalone-reshape-group-add-fields', 'group-add-fields').requiredChecks;
  const invalidEvidence = [
    { maximumCheckpointDurationMs: 1356 },
    { lifecycleCheckpointDurations: '20 lifecycle checkpoints', maximumCheckpointDurationMs: 1356 },
    { lifecycleCheckpointDurations: [] },
    { lifecycleCheckpointDurations: [{ name: 'valid', durationMs: 50 }, { name: 'invalid', durationMs: 'slow' }] },
    { workflowCheckpoints: '31 workflow checkpoints' },
    { workflowCheckpoints: [] },
    { workflowCheckpoints: [{ name: 'valid', durationMs: 50 }, { name: 'negative', durationMs: -1 }] },
    { workflowCheckpoints: [{ name: 'invalid', durationMs: 'slow' }] },
  ];

  for (const evidence of invalidEvidence) {
    const summary = summarizeRenderCheckpoints({ assertions: [{ name: checkName, evidence }] }, {
      performanceCheckNames: [checkName],
      requiredCheckNames: checks,
    });
    assert.equal(summary.count, 0);
    assert.equal(summary.maximumDurationMs, null);
    assert.deepEqual(summary.checkpoints, []);
  }
});

test('retained basic report shape exposes registered render timings without claiming lifecycle success', () => {
  const checks = scenarioCaseFor('builder-combine-draft', 'group-pivot-append').requiredChecks;
  const summary = summarizeRenderCheckpoints(retainedBasic.report, {
    performanceCheckNames: retainedBasic.report.assertions.map(({ name }) => name),
    requiredCheckNames: checks,
  });

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
    unrelatedTimingAssertion: true,
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
  assert.equal(summary.lifecycle.renderCheckpointCount, 2, JSON.stringify(summary.lifecycle.renderCheckpoints, null, 2));
  assert.equal(summary.lifecycle.maximumRenderCheckpointLatencyMs, 4456);
  assert.equal(summary.lifecycle.renderCheckpoints.some(({ checkName }) => checkName ===
    scenarioCaseFor('root-quantity-pivot', 'full-population-lifecycle').requiredChecks[0]), false,
  'Elapsed time on the required raw-oracle assertion is setup evidence, not a render checkpoint.');
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

test('focused checks run every registered group with at most two groups in flight', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const contract = scenarioCaseFor('cda-current-draft-membership', 'membership');
  const focusedGroups = contract.focusedChecks;
  const originalGroups = focusedGroups.slice();
  while (focusedGroups.length < 3) {
    focusedGroups.push({ ...focusedGroups.at(-1), id: 'membership-batch-regression-' + focusedGroups.length });
  }
  t.after(() => focusedGroups.splice(0, focusedGroups.length, ...originalGroups));

  const expectedIDs = focusedGroups.map((group) => group.id);
  let active = 0;
  let maximumActive = 0;
  const started = [];
  const summary = await runNativeVerificationBracket({
    scenarioID: 'cda-current-draft-membership',
    caseName: 'membership',
    checksOnly: true,
    evidenceParent: parent,
    root,
    commandRunner: async (_command, args) => {
      active += 1;
      maximumActive = Math.max(maximumActive, active);
      started.push(args.at(-1));
      await new Promise((resolve) => setTimeout(resolve, 10));
      active -= 1;
      return { exitCode: 0, stdoutText: '', stderrText: '' };
    },
  });

  assert.equal(summary.status, 'checks-passed');
  assert.equal(summary.focusedChecks.status, 'passed');
  assert.deepEqual(summary.focusedChecks.groups.map((group) => group.id), expectedIDs);
  assert.equal(started.length, expectedIDs.length);
  assert.equal(maximumActive, 2);
  assert.equal(active, 0);
});

test('selected Playwright setup failure appears in the summary and CLI without a domain report', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'cda-current-draft-membership';
  const caseName = 'membership';
  const selectedError = 'Error: development UI defaults do not target this session fixture and bootstrap Explorer';
  const playwrightReport = structuredClone(retainedMembershipSetupFailure);
  playwrightReport.suites[0].suites[0].specs.unshift({
    title: 'unselected setup helper test',
    file: 'cda-current-draft-membership.spec.mjs',
    line: 8,
    column: 3,
    tests: [{
      status: 'unexpected',
      results: [{ status: 'failed', retry: 0, error: { message: 'Error: unrelated unselected test failure' } }],
    }],
  });
  const listedTestLine = 'cda-current-draft-membership.spec.mjs:11:3 › CDA current-draft GROUP to GROUP Membership › native INCLUDE and EXCLUDE Membership use two exact grouped Observation ID populations';
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserExit: 1,
    browserReport: 'missing',
    playwrightReportOverride: playwrightReport,
    listedTestLine,
  });
  const targetEnvironment = {
    ...fake.env,
    LOOM_CDA_PROJECT: 'loom_dev_cda_fhir',
    LOOM_CDA_GENERATION: 'cda-fhir-v1',
  };
  let runSummary;
  const cliOutput = [];
  const exitCode = await main([
    '--scenario', scenarioID,
    '--case', caseName,
    '--target', '.codex/owned-cda-target.json',
  ], {
    env: fake.env,
    targetLoader: async () => ({
      environment: targetEnvironment,
      target: { ...makeTarget(root), project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' },
      configPath: '/tmp/test-owned-cda-target.json',
      validationScope: 'configuration-only',
      runtimeDatasetIdentity: 'not-checked',
    }),
    runBracket: async (options) => {
      runSummary = await runNativeVerificationBracket({
        ...options,
        evidenceParent: parent,
        root,
        env: options.env,
        commandRunner: (command, args, details) => details.cwd === root
          ? fake.commandRunner(command, args, details)
          : { exitCode: 0, stdoutText: '', stderrText: '' },
      });
      return runSummary;
    },
    write: (value) => cliOutput.push(value),
  });

  assert.equal(exitCode, 1);
  assert.equal(runSummary.status, 'unverified');
  assert.equal(runSummary.lifecycle.status, 'unverified');
  assert.equal(runSummary.integrity.status, 'PASS');
  assert.equal(runSummary.commands.playwright.exitCode, 1);
  assert.equal(runSummary.failureCategory, 'browser');
  assert.equal(runSummary.evidence.domainReport, null);
  assert.equal(runSummary.reviewPacket.firstFailureReason, selectedError);
  assert.equal(JSON.stringify(runSummary.reviewPacket).includes('unrelated unselected test failure'), false);
  assert.equal(JSON.stringify(runSummary.reviewPacket).includes('/private/tmp/loom-construction-implementation'), false);
  assert.equal(JSON.parse(readFileSync(runSummary.evidence.summary, 'utf8')).reviewPacket.firstFailureReason, selectedError);
  assert.equal(cliOutput.length, 1);
  assert.equal(JSON.parse(cliOutput[0]).firstFailureReason, selectedError);
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
    '--target-from-environment',
    '--grep', 'wave152 retained failure',
  ], {
    env: fake.env,
    targetLoader: async () => ({
      environment: fake.env,
      target: makeTarget(root),
      configPath: '/tmp/test-owned-cda-target.json',
      validationScope: 'configuration-only',
      runtimeDatasetIdentity: 'not-checked',
    }),
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
  assert.equal(printed.targetValidation.registryBinding, 'unbound');
  assert.equal(printed.targetValidation.runtimeDatasetIdentity, 'not-checked');
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
  report.failureEvidence.locator = {
    context: {
      state: 'visible',
      containers: [{ kind: 'dialog', text: 'Editor error token=CONTEXT_TOKEN_SENTINEL path=/private/context/file' }],
      alerts: ['Alert query=CONTEXT_QUERY_SENTINEL'],
    },
  };
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
    failureContext: summary.reviewPacket.failureContext,
    failedAction: summary.reviewPacket.failedAction,
    lastCompletedAction: summary.reviewPacket.lastCompletedAction,
    pendingOwnedRequests: summary.reviewPacket.pendingOwnedRequests,
  });

  for (const secret of [
    'TOKEN_SENTINEL', 'QUERY_SENTINEL', 'BODY_SENTINEL', 'AUTH_SENTINEL',
    'SENSITIVE_TOKEN_VALUE', 'URL_QUERY_SENTINEL', 'URL_TOKEN_SENTINEL',
    'QUERY_OBJECT_SENTINEL', 'AUTH_OBJECT_SENTINEL', 'BODY_OBJECT_SENTINEL',
    'CONTEXT_TOKEN_SENTINEL', 'CONTEXT_QUERY_SENTINEL',
    '$CHECKOUT', '/private/secret/file', 'scripts/private.mjs',
  ]) assert.equal(diagnostics.includes(secret), false, secret);
  assert.equal(summary.reviewPacket.firstFailureReason, 'Failure details contained sensitive values.');
  assert.equal(summary.reviewPacket.failureContext.state, 'visible');
  assert.equal(summary.reviewPacket.failureContext.containers[0].text, 'Failure details contained sensitive values.');
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

test('checks-only runs the registered focused groups without loading a target or launching Playwright', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const calls = [];
  const output = [];
  let summary;
  const registeredGroups = scenarioCaseFor('cda-current-draft-membership', 'membership').focusedChecks;
  const expectedGroupIDs = registeredGroups.map((group) => group.id);
  const expectedRunners = registeredGroups.map((group) => group.command[0]).sort();
  const exitCode = await main([
    '--scenario', 'cda-current-draft-membership',
    '--case', 'membership',
    '--checks-only',
  ], {
    targetLoader: async () => { throw new Error('checks-only must bypass target loading'); },
    runBracket: async (options) => {
      summary = await runNativeVerificationBracket({
        ...options,
        evidenceParent: parent,
        root,
        commandRunner: async (_command, args, options) => {
          calls.push({ args, cwd: options.cwd });
          return { exitCode: 0, stdoutText: 'focused group passed\n', stderrText: '' };
        },
      });
      return summary;
    },
    write: (value) => output.push(value),
  });

  assert.equal(exitCode, 0);
  assert.equal(summary.status, 'checks-passed');
  assert.equal(summary.mode, 'checks-only');
  assert.equal(summary.focusedCheckCoverage, 'registered');
  assert.deepEqual(summary.focusedChecks.groups.map((group) => group.id), expectedGroupIDs);
  assert.ok(summary.focusedChecks.groups.every((group) => group.status === 'passed' && group.declaredInputsHash));
  assert.equal(calls.length, expectedGroupIDs.length);
  const actualRunners = calls.map(({ args }) => args[0] === '--test' ? 'node-test'
    : args[0] === '../../node_modules/vitest/vitest.mjs' ? 'vitest' : 'unknown').sort();
  assert.deepEqual(actualRunners, expectedRunners);
  assert.equal(summary.commands.selectionList, undefined);
  assert.equal(summary.commands.precheck, undefined);
  assert.equal(summary.commands.playwright, undefined);
  assert.equal(summary.commands.captureAfter, undefined);
  assert.equal(JSON.parse(output[0]).focusedCheckCoverage, 'registered');
});

test('checks-only names browser-only cases instead of reporting an empty pass', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const output = [];
  let summary;
  const exitCode = await main([
    '--scenario', 'root-quantity-pivot',
    '--case', 'full-population-lifecycle',
    '--checks-only',
  ], {
    targetLoader: async () => { throw new Error('checks-only must bypass target loading'); },
    runBracket: async (options) => {
      summary = await runNativeVerificationBracket({ ...options, evidenceParent: parent, root });
      return summary;
    },
    write: (value) => output.push(value),
  });

  assert.equal(exitCode, 1);
  assert.equal(summary.status, 'browser-only');
  assert.equal(summary.focusedCheckCoverage, 'browser-only');
  assert.match(summary.reviewPacket.firstFailureReason, /no focused prerequisite group/);
  assert.equal(JSON.parse(output[0]).focusedCheckCoverage, 'browser-only');
});

test('a failed focused prerequisite prevents all browser bracket commands', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const calls = [];
  const expectedGroupIDs = scenarioCaseFor('cda-current-draft-membership', 'membership')
    .focusedChecks.map((group) => group.id);
  const summary = await runNativeVerificationBracket({
    scenarioID: 'cda-current-draft-membership',
    caseName: 'membership',
    evidenceParent: parent,
    root,
    commandRunner: async (_command, args) => {
      calls.push(args);
      const broken = args.some((arg) => arg.endsWith('ConstructionReshapeEditor.unit.test.tsx'));
      return {
        exitCode: broken ? 1 : 0,
        stdoutText: broken ? 'SOURCE_PROJECTION regression\n' : '',
        stderrText: '',
      };
    },
  });

  assert.equal(summary.status, 'failed');
  assert.equal(summary.failureCategory, 'focused-check');
  assert.match(summary.reviewPacket.firstFailureReason, /membership-source-group exited 1/);
  assert.equal(summary.commands.precheck, undefined);
  assert.equal(summary.commands.selectionList, undefined);
  assert.equal(summary.commands.playwright, undefined);
  assert.equal(summary.commands.captureAfter, undefined);
  assert.deepEqual(summary.focusedChecks.groups.map((group) => group.id), expectedGroupIDs);
  assert.equal(calls.length, expectedGroupIDs.length);
});

test('a stalled preparation stage is bounded and still attempts after-capture and health', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const fake = fakeRunner({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    rootDir: root,
  });
  const commandRunner = async (command, args, options) => {
    if (args[0] === 'scripts/owned-stack-verification.mjs'
      && option(args, '--mode') === 'health'
      && option(args, '--output')?.includes('health-before')) {
      return { exitCode: null, timedOut: true, durationMs: options.timeoutMs, stdoutText: '', stderrText: '' };
    }
    return fake.commandRunner(command, args, options);
  };
  const summary = await runNativeVerificationBracket({
    scenarioID: 'root-quantity-pivot',
    caseName: 'full-population-lifecycle',
    grep: 'preparation timeout',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner,
  });

  assert.equal(summary.status, 'unverified');
  assert.equal(summary.commands.healthBefore.timedOut, true);
  assert.equal(summary.commands.healthBefore.timeoutMs, 90_000);
  assert.equal(summary.commands.playwright, undefined);
  assert.equal(summary.commands.captureAfter.exitCode, 0);
  assert.equal(summary.commands.healthAfter.exitCode, 0);
  assert.match(summary.workflowError, /Before health stage timed out after 90000ms/);
});

test('runProcess terminates a stalled child within its own timeout', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'native-bracket-process-timeout-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const result = await runProcess(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {
    cwd: root,
    env: process.env,
    stdoutPath: join(directory, 'stdout.log'),
    stderrPath: join(directory, 'stderr.log'),
    timeoutMs: 75,
    outputPreviewLimit: 100,
  });

  assert.equal(result.timedOut, true);
  assert.ok(result.durationMs < 3000);
  assert.notEqual(result.exitCode, 0);
});

test('runProcess kills an inherited-pipe grandchild with its owned process group', { skip: process.platform === 'win32' }, async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'native-bracket-process-group-'));
  const pidPath = join(directory, 'grandchild.pid');
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const parent = [
    "const { spawn } = require('node:child_process');",
    "const { writeFileSync } = require('node:fs');",
    "const grandchild = spawn(process.execPath, ['-e', 'process.on(\"SIGTERM\", () => {}); setInterval(() => {}, 1000);'], { stdio: 'inherit' });",
    'writeFileSync(process.env.OWNED_TEST_GRANDCHILD_PID, String(grandchild.pid));',
    "process.on('SIGTERM', () => process.exit(0));",
    'setInterval(() => {}, 1000);',
  ].join('\n');
  const result = await runProcess(process.execPath, ['-e', parent], {
    cwd: root,
    env: { ...process.env, OWNED_TEST_GRANDCHILD_PID: pidPath },
    stdoutPath: join(directory, 'stdout.log'),
    stderrPath: join(directory, 'stderr.log'),
    timeoutMs: 250,
    outputPreviewLimit: 100,
  });

  assert.equal(result.timedOut, true);
  assert.ok(result.durationMs >= 900 && result.durationMs < 3000);
  assert.ok(existsSync(pidPath), 'the test must create an inherited-pipe grandchild');
  const grandchildPID = Number(readFileSync(pidPath, 'utf8'));
  assert.ok(Number.isInteger(grandchildPID) && grandchildPID > 0);
  let processGone = false;
  const deadline = Date.now() + 1500;
  while (Date.now() < deadline) {
    try {
      process.kill(grandchildPID, 0);
      await new Promise((resolveWait) => setTimeout(resolveWait, 25));
    } catch (error) {
      if (error?.code !== 'ESRCH') throw error;
      processGone = true;
      break;
    }
  }
  assert.equal(processGone, true, 'the inherited-pipe grandchild must be gone after timeout cleanup');
});

test('runProcess escalates after the child closes when a stdio-ignored grandchild remains', { skip: process.platform === 'win32' }, async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'native-bracket-process-group-ignore-'));
  const pidPath = join(directory, 'grandchild.pid');
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const parent = [
    "const { spawn } = require('node:child_process');",
    "const { writeFileSync } = require('node:fs');",
    "const grandchild = spawn(process.execPath, ['-e', 'process.on(\"SIGTERM\", () => {}); setInterval(() => {}, 1000);'], { stdio: 'ignore' });",
    'writeFileSync(process.env.OWNED_TEST_GRANDCHILD_PID, String(grandchild.pid));',
    "process.on('SIGTERM', () => process.exit(0));",
    'setInterval(() => {}, 1000);',
  ].join('\n');
  const result = await runProcess(process.execPath, ['-e', parent], {
    cwd: root,
    env: { ...process.env, OWNED_TEST_GRANDCHILD_PID: pidPath },
    stdoutPath: join(directory, 'stdout.log'),
    stderrPath: join(directory, 'stderr.log'),
    timeoutMs: 250,
    outputPreviewLimit: 100,
  });

  assert.equal(result.timedOut, true);
  assert.ok(result.durationMs >= 900 && result.durationMs < 3000);
  assert.ok(existsSync(pidPath), 'the test must create a stdio-ignored grandchild');
  const grandchildPID = Number(readFileSync(pidPath, 'utf8'));
  assert.ok(Number.isInteger(grandchildPID) && grandchildPID > 0);
  let processGone = false;
  const deadline = Date.now() + 1500;
  while (Date.now() < deadline) {
    try {
      process.kill(grandchildPID, 0);
      await new Promise((resolveWait) => setTimeout(resolveWait, 25));
    } catch (error) {
      if (error?.code !== 'ESRCH') throw error;
      processGone = true;
      break;
    }
  }
  assert.equal(processGone, true, 'the stdio-ignored grandchild must be gone after timeout cleanup');
});

test('runProcess closes output logs after an executable cannot be started', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'native-bracket-spawn-error-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const stdoutPath = join(directory, 'stdout.log');
  const stderrPath = join(directory, 'stderr.log');
  const result = await runProcess(join(directory, 'missing-executable'), [], {
    cwd: root,
    env: process.env,
    stdoutPath,
    stderrPath,
    timeoutMs: 1000,
  });

  assert.equal(result.exitCode, null);
  assert.match(result.error, /ENOENT/);
  assert.equal(result.timedOut, false);
  assert.ok(result.durationMs < 1000);
  assert.equal(readFileSync(stdoutPath, 'utf8'), '');
  assert.equal(readFileSync(stderrPath, 'utf8'), '');
});

test('lifecycle summary retains dimension statuses without duplicating dimension evidence', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'root-quantity-pivot';
  const caseName = 'full-population-lifecycle';
  const huge = 'x'.repeat(50_000);
  const fake = fakeRunner({
    scenarioID,
    caseName,
    rootDir: root,
    browserReport: 'cda',
    domainReportOverride: {
      dimensions: Object.fromEntries(['usability', 'correctness', 'persistence', 'performance']
        .map((name) => [name, { status: 'passed', evidence: huge }])),
    },
  });
  const summary = await runNativeVerificationBracket({
    scenarioID,
    caseName,
    grep: 'compact dimensions',
    evidenceParent: parent,
    root,
    env: fake.env,
    commandRunner: fake.commandRunner,
  });

  assert.deepEqual(summary.lifecycle.dimensions, {
    usability: 'passed',
    correctness: 'passed',
    persistence: 'passed',
    performance: 'passed',
  });
  assert.equal(JSON.stringify(summary).includes(huge), false);
  assert.ok(summary.evidence.domainReport);
});

test('normal selected-case CLI loads the explicit target against registered identity before the bracket', async () => {
  const output = [];
  let loaderInput;
  let bracketOptions;
  const exitCode = await main([
    '--scenario', 'cda-current-draft-membership',
    '--case', 'membership',
    '--target', '.codex/owned-cda-target.json',
  ], {
    targetLoader: async (input) => {
      loaderInput = input;
      return {
        target: { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' },
        environment: { LOOM_CDA_PROJECT: 'loom_dev_cda_fhir', LOOM_CDA_GENERATION: 'cda-fhir-v1' },
        configPath: '/machine-local/owned-cda-target.json',
        validationScope: 'configuration-only',
        runtimeDatasetIdentity: 'not-checked',
      };
    },
    runBracket: async (options) => {
      bracketOptions = options;
      return {
        status: 'checks-passed',
        scenario: options.scenarioID,
        case: options.caseName,
        durationMs: 0,
        runDirectory: null,
        evidence: { summary: null, domainReport: null },
        integrity: { status: 'UNVERIFIED' },
        targetValidation: options.targetValidation,
        focusedCheckCoverage: 'registered',
        focusedChecks: { status: 'passed', groups: [] },
        failureCategory: null,
        reviewPacket: {
          firstFailureReason: null,
          failureContext: null,
          failedAction: null,
          lastCompletedAction: null,
          pendingOwnedRequests: [],
        },
      };
    },
    write: (value) => output.push(value),
  });

  assert.equal(exitCode, 0);
  assert.equal(loaderInput.targetPath, '.codex/owned-cda-target.json');
  assert.deepEqual(loaderInput.expectedIdentity, {
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
  });
  assert.equal(bracketOptions.env.LOOM_CDA_PROJECT, 'loom_dev_cda_fhir');
  assert.equal(bracketOptions.env.LOOM_CDA_GENERATION, 'cda-fhir-v1');
  assert.deepEqual(bracketOptions.targetValidation, {
    scope: 'configuration-only',
    registryBinding: 'bound',
    runtimeDatasetIdentity: 'not-checked',
    configPath: '/machine-local/owned-cda-target.json',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
  });
  assert.equal(bracketOptions.grep, undefined);
  assert.equal(JSON.parse(output[0]).targetValidation.runtimeDatasetIdentity, 'not-checked');
});

test('explicit environment mode keeps a browser-only case registry-unbound and reaches selection and precheck', async (t) => {
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const scenarioID = 'builder-controls';
  const caseName = 'tables';
  const fake = fakeRunner({ scenarioID, caseName, rootDir: root });
  const output = [];
  let runOptions;
  const exitCode = await main([
    '--scenario', scenarioID,
    '--case', caseName,
    '--target-from-environment',
    '--grep', 'explicit environment target smoke selection',
  ], {
    env: fake.env,
    targetLoader: async () => { throw new Error('environment mode must not load a registry-bound target file'); },
    runBracket: async (options) => {
      runOptions = options;
      return runNativeVerificationBracket({
        ...options,
        evidenceParent: parent,
        root,
        commandRunner: fake.commandRunner,
      });
    },
    write: (value) => output.push(value),
  });

  assert.equal(exitCode, 0);
  assert.deepEqual(runOptions.targetValidation, {
    scope: 'environment-only',
    registryBinding: 'unbound',
    runtimeDatasetIdentity: 'not-checked',
    configPath: null,
    project: null,
    generation: null,
  });
  assert.equal(runOptions.env.LOOM_CDA_SOURCE_ROOT, root);
  assert.ok(fake.commands.includes('selectionList'));
  assert.ok(fake.commands.includes('precheck'));
  assert.equal(JSON.parse(output[0]).targetValidation.registryBinding, 'unbound');
});

test('environment mode rejects missing or wrong source ownership before selection or precheck', async (t) => {
  const scenarioID = 'builder-controls';
  const caseName = 'tables';
  const fake = fakeRunner({ scenarioID, caseName, rootDir: root });
  const parent = evidenceParent();
  t.after(() => rmSync(parent, { recursive: true, force: true }));

  for (const [name, env, expected] of [
    ['missing source root', {}, /Set LOOM_CDA_SOURCE_ROOT/],
    ['wrong source root', { ...fake.env, LOOM_CDA_SOURCE_ROOT: tmpdir() }, /must resolve to the repository/],
  ]) {
    const result = await runNativeVerificationBracket({
      scenarioID,
      caseName,
      grep: 'explicit environment target smoke selection',
      evidenceParent: parent,
      root,
      env,
      commandRunner: fake.commandRunner,
    });
    assert.equal(result.status, 'unverified', name);
    assert.equal(result.failureCategory, 'preparation', name);
    assert.match(result.workflowError, expected, name);
    assert.equal(result.commands.selectionList, undefined, name);
    assert.equal(result.commands.precheck, undefined, name);
  }
  assert.deepEqual(fake.commands, []);
});
