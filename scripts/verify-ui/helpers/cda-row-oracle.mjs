import assert from 'node:assert/strict';

export function assertVisibleRowsMatchOracle(actualRows, oracleRows, { limit = 25, label = 'visible rows', exactWindow = false } = {}) {
  assert(Array.isArray(actualRows), `${label}: browser result must be an array`);
  assert(Array.isArray(oracleRows), `${label}: independent oracle must be an array`);
  const expectedCount = Math.min(limit, oracleRows.length);
  assert.equal(actualRows.length, expectedCount, `${label}: visible row count must match the bounded preview`);
  if (exactWindow) {
    assert.deepEqual(actualRows, oracleRows.slice(0, limit), `${label}: visible rows must match the source-ordered oracle window`);
    return;
  }

  const remaining = new Map();
  for (const row of oracleRows) {
    const key = JSON.stringify(row);
    remaining.set(key, (remaining.get(key) ?? 0) + 1);
  }
  for (const row of actualRows) {
    const key = JSON.stringify(row);
    const count = remaining.get(key) ?? 0;
    assert(count > 0, `${label}: row is not present in the independent oracle: ${key}`);
    remaining.set(key, count - 1);
  }
  if (oracleRows.length <= limit) {
    assert([...remaining.values()].every(count => count === 0), `${label}: visible rows omit an oracle row`);
  }
}

const completePopulationRowKey = row => JSON.stringify(row.map((value) => Array.isArray(value)
  ? [...value].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)))
  : value));

/** Compare complete row populations independent of row order and ALL cell item order. */
export function assertCompletePopulationRowsMatchOracle(actualRows, oracleRows, { label = 'complete row population' } = {}) {
  assert(Array.isArray(actualRows), `${label}: browser result must be an array`);
  assert(Array.isArray(oracleRows), `${label}: independent oracle must be an array`);

  const frequencies = rows => {
    const counts = new Map();
    for (const row of rows) {
      assert(Array.isArray(row), `${label}: every row must be an array`);
      const key = completePopulationRowKey(row);
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return [...counts].sort(([left], [right]) => left.localeCompare(right));
  };

  assert.deepEqual(frequencies(actualRows), frequencies(oracleRows), `${label}: row values or multiplicities differ`);
}
