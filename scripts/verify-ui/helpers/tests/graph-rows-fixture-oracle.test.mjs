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
