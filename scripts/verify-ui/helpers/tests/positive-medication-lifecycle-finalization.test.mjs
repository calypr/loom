import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { adjudicatePendingLifecycle, finishReport, reportDimensions } from '../report.mjs';

const makeReport = (network, lifecycleStatus = 'pending-final-adjudication') => ({
  errors: [],
  network,
  assertions: [{ name: 'native lifecycle checks', status: 'passed' }],
  dimensions: Object.fromEntries(reportDimensions.map(name => [name, { status: 'untested', evidence: [] }])),
  requiredChecks: ['native lifecycle checks'],
  missingRequiredChecks: [],
  lifecycle: { status: lifecycleStatus },
});

const assertFinalizedFailure = (report, expectedNetwork) => {
  finishReport(report);
  adjudicatePendingLifecycle(report);

  assert.equal(report.status, 'failed');
  assert.equal(report.lifecycle.status, 'failed');
  assert.equal(report.lifecycle.finalReportStatus, 'failed');
  assert.deepEqual(report.lifecycle.failure.unexpectedNetwork, [expectedNetwork]);
};

test('generic final adjudication fails a positive lifecycle on an unexpected page error', () => {
  const event = { kind: 'exception', message: 'Unexpected page error: renderer failed' };
  assertFinalizedFailure(makeReport([event]), event);
});

test('generic final adjudication fails a positive lifecycle on an HTTP 500 network error', () => {
  const event = {
    kind: 'network',
    status: 500,
    method: 'GET',
    url: 'http://127.0.0.1:30000/api/v1/projects/fixture',
    errorText: 'HTTP 500',
  };
  assertFinalizedFailure(makeReport([event]), event);
});

test('generic final adjudication marks a clean positive lifecycle passed only after report status passes', () => {
  const report = makeReport([]);

  finishReport(report);
  assert.equal(report.lifecycle.status, 'pending-final-adjudication');
  adjudicatePendingLifecycle(report);

  assert.equal(report.status, 'passed');
  assert.equal(report.lifecycle.status, 'passed');
  assert.equal(report.lifecycle.finalReportStatus, 'passed');
});

test('existing CDA lifecycle statuses are not rewritten by the generic finalizer', () => {
  const report = makeReport([{ kind: 'exception', message: 'handled by the CDA fixture' }], 'passed');
  report.status = 'failed';

  adjudicatePendingLifecycle(report);

  assert.equal(report.lifecycle.status, 'passed');
  assert.equal(report.lifecycle.finalReportStatus, undefined);
});

test('the positive workflow stays pending until the generic fixture finalizer classifies report.network', () => {
  const fixtureRunner = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  assert.ok(fixtureRunner.indexOf('finishReport(report);') < fixtureRunner.indexOf('adjudicatePendingLifecycle(report);'));

  const workflow = readFileSync(new URL('../../workflows/verify-cda-zero-column-related-medication.mjs', import.meta.url), 'utf8');
  const pendingBranch = /if \(oracleMode\.positiveFixture\) \{\s*(report\.lifecycle\.status = 'pending-final-adjudication';)/.exec(workflow)?.[0];
  assert.ok(pendingBranch, 'positive fixture must defer lifecycle status to the generic report finalizer');
  assert.doesNotMatch(pendingBranch, /status = 'passed'/);
});
