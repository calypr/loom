import assert from 'node:assert/strict';

export function assertVisibleRowsMatchOracle(actualRows, oracleRows, { limit = 25, label = 'visible rows' } = {}) {
  assert(Array.isArray(actualRows), `${label}: browser result must be an array`);
  assert(Array.isArray(oracleRows), `${label}: independent oracle must be an array`);
  const expectedCount = Math.min(limit, oracleRows.length);
  assert.equal(actualRows.length, expectedCount, `${label}: visible row count must match the bounded preview`);

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
