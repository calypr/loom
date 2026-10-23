import test from 'node:test';
import assert from 'node:assert/strict';
import { assertCompletePopulationRowsMatchOracle, assertVisibleRowsMatchOracle } from '../cda-row-oracle.mjs';

test('complete row populations ignore row order and ALL cell item order', () => {
  const expected = [['Patient/1', '2'], ['Patient/2', ['preliminary', 'final']]];
  assertCompletePopulationRowsMatchOracle([['Patient/2', ['final', 'preliminary']], ['Patient/1', '2']], expected);
});

test('complete row populations preserve duplicate row and ALL cell item multiplicity', () => {
  const expected = [['Patient/1', '2'], ['Patient/2', '1']];
  assert.throws(
    () => assertCompletePopulationRowsMatchOracle([expected[0], expected[0]], expected, { label: 'duplicate cannot hide missing row' }),
    /row values or multiplicities differ/,
  );
  assert.throws(
    () => assertCompletePopulationRowsMatchOracle([expected[0]], expected, { label: 'missing row' }),
    /row values or multiplicities differ/,
  );
  assert.throws(
    () => assertCompletePopulationRowsMatchOracle([...expected, expected[1]], expected, { label: 'extra row' }),
    /row values or multiplicities differ/,
  );
  assert.throws(
    () => assertCompletePopulationRowsMatchOracle(
      [['Patient/1', ['final', 'final']]],
      [['Patient/1', ['final', 'preliminary']]],
      { label: 'duplicate ALL item cannot hide missing item' },
    ),
    /row values or multiplicities differ/,
  );
});

test('row comparison retains duplicate multiplicity and detects missing rows', () => {
  const expected = [['id-a', null], ['id-a', null], ['id-b', 'value']];
  assertVisibleRowsMatchOracle(expected, expected);
  assert.throws(() => assertVisibleRowsMatchOracle([expected[0], expected[2]], expected), /row count/);
  assert.throws(() => assertVisibleRowsMatchOracle([expected[0], expected[2], expected[2]], expected), /not present/);
});

test('bounded previews reject duplicate substitutions and rows absent from the oracle', () => {
  const expected = Array.from({ length: 30 }, (_, index) => [`id-${index}`]);
  assertVisibleRowsMatchOracle(expected.slice(0, 25), expected);
  assert.throws(() => assertVisibleRowsMatchOracle([...expected.slice(0, 24), expected[0]], expected), /not present/);
  assert.throws(() => assertVisibleRowsMatchOracle(expected.slice(0, 24), expected), /row count/);
});

test('source ordered bounded windows reject rows outside the first page and order changes', () => {
  const expected = Array.from({ length: 30 }, (_, index) => [`id-${index}`]);
  assertVisibleRowsMatchOracle(expected.slice(0, 25), expected, { exactWindow: true });
  assert.throws(() => assertVisibleRowsMatchOracle([...expected.slice(1, 25), expected[0]], expected, { exactWindow: true }), /source-ordered/);
  assert.throws(() => assertVisibleRowsMatchOracle(expected.slice(0, 24), expected, { exactWindow: true }), /row count/);
});
