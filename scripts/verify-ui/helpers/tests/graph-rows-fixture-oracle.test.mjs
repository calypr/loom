import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertGraphRowsResult, loadGraphRowsFixture, validateGraphRowsFixture } from '../graph-rows-fixture-oracle.mjs';

test('hand-authored graph fixture proves zero/one/many, shared pairs, 2x3 branches and tuple groups', () => {
  const oracle = validateGraphRowsFixture();
  assert.equal(oracle.preserving.length, 4);
  assert.equal(oracle.expandedDropMissing.length, 5);
  assert.equal(oracle.requiredObservationIdentities.length, 3);
  assert.deepEqual(oracle.expandedDropMissing.filter(row => row.identity[1] === 'obs-many-A').map(row => row.identity), [
    ['rs-many', 'obs-many-A'], ['rs-shared', 'obs-many-A'],
  ]);
});

test('result assertion rejects implicit Cartesian rows, missing pairs and arbitrary first values', () => {
  const oracle = validateGraphRowsFixture();
  assertGraphRowsResult('preserving', [...oracle.preserving].reverse(), oracle);
  const product = oracle.preserving.flatMap(row => row.identity[0] === 'rs-many' ? Array.from({ length: 6 }, () => row) : [row]);
  assert.throws(() => assertGraphRowsResult('preserving', product, oracle), /Exact graph-rows/);
  assert.throws(() => assertGraphRowsResult('expandedDropMissing', oracle.expandedDropMissing.slice(0, 4), oracle), /Exact graph-rows/);
  const arbitrary = structuredClone(oracle.preserving);
  arbitrary[2].observationValues = [11];
  assert.throws(() => assertGraphRowsResult('preserving', arbitrary, oracle), /Exact graph-rows/);
});

test('fixture validation detects changed references, numeric values and lost repeated item identity', () => {
  const wrongReference = loadGraphRowsFixture();
  wrongReference.resources.Observation[1].subject.reference = 'Patient/p-one';
  assert.throws(() => validateGraphRowsFixture(wrongReference), /literal oracle/);
  const changedValue = loadGraphRowsFixture();
  changedValue.resources.Observation[2].valueInteger = 99;
  assert.throws(() => validateGraphRowsFixture(changedValue), /literal oracle/);
  const lostItem = loadGraphRowsFixture();
  lostItem.resources.ResearchSubject[2].identifier.pop();
  assert.throws(() => validateGraphRowsFixture(lostItem));
});

test('comparison preserves value ordering, field identity and duplicate counts', () => {
  const oracle = validateGraphRowsFixture();
  const valuesReordered = structuredClone(oracle.preserving);
  valuesReordered[2].observationValues.reverse();
  assert.throws(() => assertGraphRowsResult('preserving', valuesReordered, oracle));
  const missingField = structuredClone(oracle.preserving);
  delete missingField[0].patientId;
  assert.throws(() => assertGraphRowsResult('preserving', missingField, oracle));
  assert.throws(() => assertGraphRowsResult('not-a-case', [], oracle), /Unknown exact row case/);
});

test('explicit expansion retains the complete existing list on every input/record pair', () => {
  const oracle = validateGraphRowsFixture();
  assertGraphRowsResult('expandedListsAndExplicitScalars', oracle.expandedListsAndExplicitScalars);
  assert.deepEqual(oracle.expandedListsAndExplicitScalars.filter(row => row.identity[0] === 'rs-many'), [
    { identity: ['rs-many', 'obs-many-A'], patientId: 'p-many', subjectStatus: 'active', observationValues: [11, 13], perMatchObservationValue: 11 },
    { identity: ['rs-many', 'obs-many-B'], patientId: 'p-many', subjectStatus: 'active', observationValues: [11, 13], perMatchObservationValue: 13 },
  ]);
  const scalarized = structuredClone(oracle.expandedListsAndExplicitScalars);
  scalarized[1].observationValues = 11;
  assert.throws(() => assertGraphRowsResult('expandedListsAndExplicitScalars', scalarized), /Exact graph-rows/);
  const narrowedList = structuredClone(oracle.expandedListsAndExplicitScalars);
  narrowedList[1].observationValues = [11];
  assert.throws(() => assertGraphRowsResult('expandedListsAndExplicitScalars', narrowedList), /Exact graph-rows/);
  const wrongScalar = structuredClone(oracle.expandedListsAndExplicitScalars);
  wrongScalar[2].perMatchObservationValue = 11;
  assert.throws(() => assertGraphRowsResult('expandedListsAndExplicitScalars', wrongScalar), /Exact graph-rows/);
});

test('the empty-parent expansion row retains an empty list and an explicitly missing scalar', () => {
  const oracle = validateGraphRowsFixture();
  assert.deepEqual(oracle.expandedListsKeepMissingExtra, {
    identity: ['rs-zero', null], patientId: 'p-zero', subjectStatus: 'active',
    observationValues: [], perMatchObservationValue: null,
  });
  const altered = loadGraphRowsFixture();
  altered.oracle.expandedListsKeepMissingExtra.observationValues = null;
  assert.throws(() => validateGraphRowsFixture(altered));
});


test('replacement Patient root includes the performer-only record rather than only subject-linked patients', () => {
  const oracle = validateGraphRowsFixture();
  assert.deepEqual(oracle.replacementPatientIDs, ['p-zero', 'p-one', 'p-many', 'p-performer']);
  const linkedOnly = loadGraphRowsFixture(); linkedOnly.oracle.replacementPatientIDs.pop();
  assert.throws(() => validateGraphRowsFixture(linkedOnly), /Root replacement/);
});


test('Group EXCLUDE has three literal nonmissing tuples and exact contributor identities', () => {
  const oracle = validateGraphRowsFixture();
  assert.deepEqual(oracle.groupExcludeMissing, [
    { identity: ['female', 'active'], contributorSubjectIds: ['rs-one'], count: 1, observationValues: [7] },
    { identity: ['male', 'active'], contributorSubjectIds: ['rs-many'], count: 1, observationValues: [11, 13] },
    { identity: ['male', 'completed'], contributorSubjectIds: ['rs-shared'], count: 1, observationValues: [11, 13] },
  ]);
  assert.equal(oracle.groupKeepMissing.length, 4);
  assertGraphRowsResult('groupExcludeMissing', [...oracle.groupExcludeMissing].reverse(), oracle);
  assert.throws(() => assertGraphRowsResult('groupExcludeMissing', [...oracle.groupExcludeMissing, oracle.groupKeepMissing[0]], oracle));
  assert.throws(() => assertGraphRowsResult('groupExcludeMissing', oracle.groupExcludeMissing.slice(1), oracle));
  const wrongContributor = structuredClone(oracle.groupExcludeMissing); wrongContributor[1].contributorSubjectIds = ['rs-shared'];
  assert.throws(() => assertGraphRowsResult('groupExcludeMissing', wrongContributor, oracle));
  const wrongLiteral = loadGraphRowsFixture(); wrongLiteral.oracle.groupExcludeMissing[0].identity[0] = null;
  assert.throws(() => validateGraphRowsFixture(wrongLiteral), /EXCLUDE oracle/);
});
