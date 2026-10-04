import test from 'node:test';
import assert from 'node:assert/strict';
import { assertVisibleRowsMatchOracle } from './cda-row-oracle.mjs';

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
