import assert from 'node:assert/strict';

const key = (row) => JSON.stringify(row.map((value) => Array.isArray(value)
  ? { array: value.map((item) => item).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))) }
  : value));

/** Compare complete bounded row populations, including duplicate multiplicity. */
export const assertExactMultiset = (actualRows, expectedRows, label = 'rows') => {
  assert(Array.isArray(actualRows), `${label}: actual rows must be an array`);
  assert(Array.isArray(expectedRows), `${label}: expected rows must be an array`);
  const counts = (rows) => {
    const result = new Map();
    for (const row of rows) {
      assert(Array.isArray(row), `${label}: every row must be an array`);
      const serialized = key(row);
      result.set(serialized, (result.get(serialized) ?? 0) + 1);
    }
    return result;
  };
  const actual = counts(actualRows);
  const expected = counts(expectedRows);
  assert.deepEqual([...actual].sort(([left], [right]) => left.localeCompare(right)),
    [...expected].sort(([left], [right]) => left.localeCompare(right)), `${label}: row values or multiplicities differ`);
};
