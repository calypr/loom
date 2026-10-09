import assert from 'node:assert/strict';
import test from 'node:test';
import { assertExactContributorRows, deriveContributorRuleOracle } from '../contributor-rule-oracle.mjs';

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
