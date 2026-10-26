import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { readScope001RepeatedPivotOracle } from '../scope-001-repeated-pivot-oracle.mjs';
import { isRootComponentChoiceLabel } from '../../workflows/scope-001-repeated-expand-pivot.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/scope-001-repeated-pivot', import.meta.url));

test('SCOPE-001 oracle retains every expanded component and sums duplicate categories per Observation', () => {
  const oracle = readScope001RepeatedPivotOracle(fixtureDir);
  assert.deepEqual(oracle.patientIDs, ['scope-001-patient-1']);
  assert.deepEqual(oracle.observationIDs, [
    'scope-001-observation-a',
    'scope-001-observation-b',
    'scope-001-observation-c',
  ]);
  assert.deepEqual(oracle.observationCodeTexts, [
    'Basic fixture Observation',
    'Basic fixture Observation',
    'Basic fixture Observation',
  ]);
  assert.deepEqual(oracle.expandedRows, [
    { id: 'scope-001-observation-a', ordinal: 0, category: 'alpha', value: 2 },
    { id: 'scope-001-observation-a', ordinal: 1, category: 'beta', value: 7 },
    { id: 'scope-001-observation-a', ordinal: 2, category: 'alpha', value: 3 },
    { id: 'scope-001-observation-b', ordinal: 0, category: 'alpha', value: 5 },
    { id: 'scope-001-observation-b', ordinal: 1, category: 'gamma', value: 11 },
    { id: 'scope-001-observation-c', ordinal: 0, category: 'beta', value: 1 },
    { id: 'scope-001-observation-c', ordinal: 1, category: 'gamma', value: 4 },
  ]);
  assert.deepEqual(oracle.categories, ['alpha', 'beta', 'gamma']);
  assert.deepEqual(oracle.pivotRows, [
    { id: 'scope-001-observation-a', alpha: 5, beta: 7, gamma: null },
    { id: 'scope-001-observation-b', alpha: 5, beta: null, gamma: 11 },
    { id: 'scope-001-observation-c', alpha: null, beta: 1, gamma: 4 },
  ]);
});

test('native chooser label matches only the component[] base-root option', () => {
  assert.equal(isRootComponentChoiceLabel('Component results · Occurrence base (root) (component[])'), true);
  assert.equal(isRootComponentChoiceLabel('Component results · Occurrence nested (component[])'), false);
  assert.equal(isRootComponentChoiceLabel('Observation.component[]'), false);
});
