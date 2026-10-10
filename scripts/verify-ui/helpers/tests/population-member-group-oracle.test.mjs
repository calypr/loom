import assert from 'node:assert/strict';
import test from 'node:test';
import { deriveGroupRowsFromCdaWitnesses, findMappedMemberRemovalWithGroupCountChange } from '../population-member-group-oracle.mjs';

const candidates = () => [
  { id: 'mapped-a', parents: ['Specimen/parent-a'], routeRows: [
    { id: 'observation-1', subjectReference: 'Patient/patient-1' },
    { id: 'observation-2', subjectReference: 'Patient/patient-1' },
  ] },
  { id: 'mapped-b', parents: ['Specimen/parent-b'], routeRows: [
    { id: 'observation-3', subjectReference: 'Patient/patient-1' },
    { id: 'observation-4', subjectReference: 'Patient/patient-1' },
  ] },
  { id: 'orphan', parents: [], children: ['Specimen/child'], routeRows: [] },
];

const rootRows = () => [
  { id: 'observation-1', subjectReference: 'Patient/patient-1', specimenIDs: ['specimen-a'] },
  { id: 'observation-2', subjectReference: 'Patient/patient-1', specimenIDs: ['specimen-b'] },
  { id: 'observation-3', subjectReference: 'Patient/patient-1', specimenIDs: ['specimen-c'] },
  { id: 'observation-4', subjectReference: 'Patient/patient-1', specimenIDs: ['specimen-d'] },
];

test('selects a bounded mapped-member removal that leaves a nonempty lower GROUP count', () => {
  const witness = findMappedMemberRemovalWithGroupCountChange(candidates());
  assert.deepEqual(witness, {
    removedMember: candidates()[0],
    survivingMember: candidates()[1],
    groupKey: 'Patient/patient-1',
    initialObservationIDs: ['observation-1', 'observation-2', 'observation-3', 'observation-4'],
    remainingObservationIDs: ['observation-3', 'observation-4'],
  });
});

test('rejects overlapping member routes so the raw COUNT_ROWS oracle does not assume row deduplication', () => {
  const values = candidates();
  values[1].routeRows = [{ id: 'observation-2', subjectReference: 'Patient/patient-1' }];
  assert.equal(findMappedMemberRemovalWithGroupCountChange(values), undefined);
});

test('does not claim a count-changing witness when removal leaves the same group rows', () => {
  const values = candidates();
  values[0].routeRows = [{ id: 'observation-2', subjectReference: 'Patient/patient-1' }];
  values[1].routeRows = [{ id: 'observation-2', subjectReference: 'Patient/patient-1' }];
  assert.equal(findMappedMemberRemovalWithGroupCountChange(values), undefined);
});

test('derives GROUP row multiplicity and related distinct counts from raw source witnesses', () => {
  const values = candidates();
  assert.deepEqual(deriveGroupRowsFromCdaWitnesses(values.slice(0, 2), rootRows()), [
    ['Patient/patient-1', '4', '4'],
  ]);
  assert.deepEqual(deriveGroupRowsFromCdaWitnesses([values[1]], rootRows()), [
    ['Patient/patient-1', '2', '2'],
  ]);
});

test('rejects missing raw roots instead of dropping them from the expected GROUP', () => {
  assert.throws(() => deriveGroupRowsFromCdaWitnesses(candidates().slice(0, 1), rootRows().slice(1)), /must resolve/);
});

test('rejects a mapped subject.reference that differs from the independent raw Observation reread', () => {
  const member = candidates()[0];
  member.routeRows[0].subjectReference = 'Patient/forged';
  assert.throws(() => deriveGroupRowsFromCdaWitnesses([member], rootRows()), /same subject.reference/);
});
