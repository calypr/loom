import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyEvidence, summarizeCoverage } from '../coverage-status.mjs';
import { registry, requiredChecksFor } from '../registry.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));

test('a scenario pass with untested dimensions is only partial evidence', () => {
  assert.equal(classifyEvidence({ status: 'passed', dimensions: { ...complete, persistence: { status: 'untested' } } }), 'partial');
  assert.equal(classifyEvidence({ status: 'passed', dimensions: complete }), 'passed');
});

test('current reports use passing named requirements while keeping optional dimension gaps out of case status', () => {
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    assertions: [{ name: 'required transition', status: 'passed' }],
    dimensions: { ...complete, persistence: { status: 'untested' } },
  }, ['required transition']), 'passed');
});

test('latest case result controls coverage and missing cases remain untested', () => {
  const scenarios = [{ id: 'builder', cases: ['load', 'edit'] }];
  const reports = [
    { path: 'old.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-01', status: 'passed', dimensions: complete } },
    { path: 'new.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-02', status: 'failed', dimensions: complete } },
  ];
  assert.deepEqual(summarizeCoverage(scenarios, reports).map(({ status, report }) => [status, report]), [['failed', 'new.json'], ['untested', null]]);
});

const pivotScenario = registry.find((scenario) => scenario.id === 'root-quantity-pivot');
if (!pivotScenario) throw new Error('Expected the registered standalone root quantity Pivot scenario.');
const pivotCase = 'full-population-lifecycle';
const pivotChecks = requiredChecksFor(pivotScenario, pivotCase);
const summarizePivot = (assertions) => summarizeCoverage([pivotScenario], [{
  path: 'root-quantity-pivot-full-population-lifecycle.json',
  report: {
    schemaVersion: 2,
    scenario: 'root-quantity-pivot',
    case: pivotCase,
    finishedAt: '2026-10-04T00:00:00.000Z',
    status: 'passed',
    target: { kind: 'local-cda' },
    assertions,
  },
}]).find((entry) => entry.path === 'root-quantity-pivot/full-population-lifecycle');

test('standalone root Pivot reports use the registered named lifecycle requirements', () => {
  const assertions = pivotChecks.map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizePivot(assertions)?.status, 'passed');
});

test('standalone root Pivot reports stay partial when a registered requirement is absent', () => {
  const assertions = pivotChecks.slice(0, -1).map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizePivot(assertions)?.status, 'partial');
});
