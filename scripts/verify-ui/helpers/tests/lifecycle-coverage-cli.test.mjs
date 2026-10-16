import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { scenarioCaseFor } from '../../registry.mjs';

const scenario = 'standalone-reshape-related-source-after-pivot';
const caseName = 'related-source-count-after-pivot';
const casePath = `${scenario}/${caseName}`;
const caseContract = scenarioCaseFor(scenario, caseName);
const lifecycleEvidence = caseContract.lifecycleEvidence;
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');
const coverageStatusCli = resolve(repoRoot, 'scripts/verify-ui/helpers/coverage-status.mjs');
const requiredChecks = caseContract.requiredChecks;
const persistenceChecks = lifecycleEvidence.persistence.checks;
const performanceCheck = lifecycleEvidence.performance.check;
const checkpointBudgetMs = lifecycleEvidence.performance.checkpointBudgetMs;
const retainedTimingEvidence = JSON.parse(readFileSync(
  new URL('./fixtures/post-pivot-count-qzzOck-timings.json', import.meta.url),
  'utf8',
));

assert.equal(retainedTimingEvidence.scenario, scenario);
assert.equal(retainedTimingEvidence.case, caseName);
assert.equal(retainedTimingEvidence.cases.length, 26);
assert.match(retainedTimingEvidence.sourceSummarySha256, /^[a-f0-9]{64}$/);
assert.match(retainedTimingEvidence.sourceReportSha256, /^[a-f0-9]{64}$/);
assert.ok(persistenceChecks.length > 0);
assert.ok(persistenceChecks.every((name) => requiredChecks.includes(name)));
assert.ok(requiredChecks.includes(performanceCheck));

const workflowCheckpoints = retainedTimingEvidence.cases.map(({ name, elapsedMs }) => ({
  name,
  durationMs: elapsedMs,
}));

const reportFor = ({
  checkpointEvidence = 'canonical',
  dimensions = {},
  failedCheck,
  missingCheck,
} = {}) => {
  const performanceEvidence = {
    actionCount: retainedTimingEvidence.actionCount,
    maxActionMs: retainedTimingEvidence.maxActionMs,
    workflowCheckpointProvenance: {
      sourceSummarySha256: retainedTimingEvidence.sourceSummarySha256,
      sourceReportSha256: retainedTimingEvidence.sourceReportSha256,
    },
  };
  if (checkpointEvidence === 'canonical') {
    performanceEvidence.measuredTransitionCount = workflowCheckpoints.length;
    performanceEvidence.workflowCheckpoints = workflowCheckpoints;
  } else if (checkpointEvidence === 'mismatched') {
    performanceEvidence.measuredTransitionCount = workflowCheckpoints.length;
    performanceEvidence.workflowCheckpoints = workflowCheckpoints.slice(0, -1);
  }
  const assertions = requiredChecks
    .filter((name) => name !== missingCheck)
    .map((name) => ({
      name,
      status: name === failedCheck ? 'failed' : 'passed',
      ...(name === performanceCheck
        ? { evidence: performanceEvidence }
        : {}),
    }));
  return {
    schemaVersion: 2,
    status: 'passed',
    scenario,
    case: caseName,
    finishedAt: '2026-10-08T20:58:00.000Z',
    target: { kind: 'owned-cda', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' },
    dimensions: {
      usability: { status: 'passed' },
      correctness: { status: 'passed' },
      persistence: { status: 'untested' },
      performance: { status: 'untested' },
      ...dimensions,
    },
    requiredChecks: [...requiredChecks],
    assertions,
    cases: retainedTimingEvidence.cases,
  };
};

const runCoverageCli = (report) => {
  const reportDirectory = mkdtempSync(resolve(tmpdir(), 'loom-lifecycle-coverage-'));
  try {
    writeFileSync(resolve(reportDirectory, 'qzzOck-count-report.json'), JSON.stringify(report));
    const result = spawnSync(process.execPath, [coverageStatusCli, reportDirectory], {
      cwd: repoRoot,
      encoding: 'utf8',
    });
    assert.equal(result.status, 0, result.stderr || result.stdout);
    const matchingRows = result.stdout.split(/\r?\n/)
      .filter((line) => line.split('\t')[2] === casePath);
    assert.equal(matchingRows.length, 1, `expected one ${casePath} CLI row; got:\n${result.stdout}`);
    return matchingRows[0].split('\t')[0];
  } finally {
    rmSync(reportDirectory, { recursive: true, force: true });
  }
};

test('coverage CLI passes retained COUNT evidence with registered persistence and exact checkpoint evidence', () => {
  assert.ok(lifecycleEvidence.persistence.checks.length > 0);
  assert.equal(lifecycleEvidence.performance.checkpointBudgetMs, checkpointBudgetMs);
  assert.equal(runCoverageCli(reportFor()), 'passed');
});

test('coverage CLI keeps a lifecycle case partial when a declared persistence check is missing', () => {
  assert.equal(runCoverageCli(reportFor({ missingCheck: persistenceChecks[0] })), 'partial');
});

test('coverage CLI keeps a lifecycle case partial when action-to-render timings are absent or mismatched', () => {
  assert.equal(runCoverageCli(reportFor({ checkpointEvidence: 'missing' })), 'partial');
  assert.equal(runCoverageCli(reportFor({ checkpointEvidence: 'mismatched' })), 'partial');
});

test('coverage CLI reports failed and unknown declared lifecycle dimensions without a false pass', () => {
  assert.equal(runCoverageCli(reportFor({ dimensions: { performance: { status: 'failed' } } })), 'failed');
  assert.equal(runCoverageCli(reportFor({
    checkpointEvidence: 'missing',
    dimensions: { performance: { status: 'unknown' } },
  })), 'partial');
});

test('coverage CLI reports a failed required COUNT assertion as failed', () => {
  assert.equal(runCoverageCli(reportFor({ failedCheck: requiredChecks[0] })), 'failed');
});
