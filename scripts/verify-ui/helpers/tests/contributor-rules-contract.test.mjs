import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { assertExactContributorRows, deriveContributorRuleOracle } from '../contributor-rule-oracle.mjs';
import { createReport, recordCheck, reportDimensions } from '../report.mjs';

const workflowURL = new URL('../../workflows/contributor-rules-workflow.mjs', import.meta.url);

const witnesses = () => [
  { bucket: 'zero', patient: { id: 'patient-zero', _id: 'Patient/zero' }, observations: [] },
  { bucket: 'one', patient: { id: 'patient-one', _id: 'Patient/one' }, observations: [
    { id: 'observation-one', _id: 'Observation/one' },
  ] },
  { bucket: 'many', patient: { id: 'patient-many', _id: 'Patient/many' }, observations: [
    { id: 'observation-selected', _id: 'Observation/selected' },
    { id: 'observation-other', _id: 'Observation/other' },
  ] },
];

test('EQUALS strict diagnostic check records through a supported report dimension', async () => {
  const workflow = await readFile(workflowURL, 'utf8');
  const checkName = 'Only proven contributor request supersessions are expected; unrelated diagnostics remain fatal';
  const checkCall = workflow.match(/recordCheck\('([^']+)', 'Only proven contributor request supersessions are expected; unrelated diagnostics remain fatal'/);
  assert(checkCall, 'the EQUALS workflow must emit its strict diagnostic check');
  assert.equal(checkCall[1], 'correctness');
  assert(reportDimensions.includes(checkCall[1]), 'the workflow check dimension must be supported by the report schema');

  const report = createReport({ scenario: 'cda-contributor-equals', caseName: 'contributor-equals' });
  assert.equal(recordCheck(report, checkCall[1], checkName, true, { unexpectedErrorCount: 0 }), true);
  assert.deepEqual(report.assertions, [{
    dimension: 'correctness', name: checkName, status: 'passed', evidence: { unexpectedErrorCount: 0 },
  }]);
  assert.equal(report.dimensions.correctness.status, 'passed');
});

test('EQUALS oracle derives exact PRESERVE_PARENT and EXCLUDE rows from scoped Patient witnesses', () => {
  assert.deepEqual(deriveContributorRuleOracle(witnesses()), {
    selectedObservationId: 'observation-selected',
    baselineRows: [['patient-many'], ['patient-one'], ['patient-zero']],
    preserveParentRows: [
      ['patient-many', 'observation-selected'],
      ['patient-one', '—'],
      ['patient-zero', '—'],
    ],
    excludeRows: [['patient-many', 'observation-selected']],
    countByBucket: { many: 2, one: 1, zero: 0 },
  });
});

test('EQUALS oracle rejects a selected Observation ID shared across Patient witnesses', () => {
  const duplicated = witnesses();
  duplicated[1].observations[0] = { id: 'observation-selected', _id: 'Observation/selected' };
  assert.throws(() => deriveContributorRuleOracle(duplicated), /must identify exactly one raw related source record/);
});

test('preview comparison accepts row reordering but rejects duplicate, missing, or extra rows', () => {
  const expected = [
    ['patient-many', 'observation-selected'],
    ['patient-one', '—'],
    ['patient-zero', '—'],
  ];
  assert.deepEqual(assertExactContributorRows([expected[2], expected[0], expected[1]], expected, 'reordered preview'),
    [expected[2], expected[0], expected[1]]);
  assert.throws(() => assertExactContributorRows([expected[0], expected[0], expected[2]], expected, 'duplicate preview'),
    /complete multiset/);
  assert.throws(() => assertExactContributorRows([expected[0], expected[2]], expected, 'short preview'),
    /complete multiset/);
  assert.throws(() => assertExactContributorRows([...expected, ['unexpected', 'observation']], expected, 'extra preview'),
    /complete multiset/);
});
