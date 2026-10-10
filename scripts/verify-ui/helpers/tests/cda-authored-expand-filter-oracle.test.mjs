import assert from 'node:assert/strict';
import test from 'node:test';
import { strictSubsetFilterOracle } from '../cda-authored-expand-filter-oracle.mjs';

const selected = [
  { id: 'obs-b', componentValues: [{ ordinal: 0, value: 'other-b' }, { ordinal: 1, value: 'common' }] },
  { id: 'obs-a', componentValues: [{ ordinal: 0, value: 'common' }, { ordinal: 1, value: 'other-a' }] },
];

test('exact value oracle returns sorted tuples with original component ordinals', () => {
  const expected = {
    predicateValue: 'common',
    allTuples: [
      ['obs-a', 0, 'common'],
      ['obs-a', 1, 'other-a'],
      ['obs-b', 0, 'other-b'],
      ['obs-b', 1, 'common'],
    ],
    matchingTuples: [
      ['obs-a', 0, 'common'],
      ['obs-b', 1, 'common'],
    ],
  };

  assert.deepEqual(strictSubsetFilterOracle(selected), expected);
  assert.deepEqual(strictSubsetFilterOracle([...selected].reverse()), expected,
    'Tuple ordering and predicate choice must not depend on selected-root order');
});

test('a value shared by different Observation roots matches globally', () => {
  const oracle = strictSubsetFilterOracle(selected);

  assert.equal(oracle.predicateValue, 'common');
  assert.deepEqual(oracle.matchingTuples.map(([id]) => id), ['obs-a', 'obs-b']);
  assert.equal(oracle.matchingTuples.length, 2);
});

test('oracle returns null when no exact value selects a strict subset', () => {
  assert.equal(strictSubsetFilterOracle([]), null);
  assert.equal(strictSubsetFilterOracle([
    { id: 'obs-one', componentValues: [{ ordinal: 0, value: 'only' }] },
  ]), null, 'A single tuple cannot be a strict subset');
  assert.equal(strictSubsetFilterOracle([
    { id: 'obs-one', componentValues: [{ ordinal: 0, value: 'same' }, { ordinal: 1, value: 'same' }] },
  ]), null, 'An exact value matching every tuple is not a strict subset');
});
