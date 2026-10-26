import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';

function canonicalJSON(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

export function hashScopedPayloadRows(rows, expectedIDs) {
  assert(Array.isArray(rows), 'Scoped payload rows must be an array.');
  assert(Array.isArray(expectedIDs) && expectedIDs.every(id => typeof id === 'string' && id.length > 0),
    'Expected payload IDs must be non-empty strings.');
  assert.equal(new Set(expectedIDs).size, expectedIDs.length, 'Expected payload IDs must be unique.');
  const actualIDs = rows.map(row => row?.id);
  assert(actualIDs.every(id => typeof id === 'string'), 'Each scoped payload row must have a string ID.');
  assert.equal(new Set(actualIDs).size, actualIDs.length, 'Scoped payload rows must not duplicate IDs.');
  assert.deepEqual([...actualIDs].sort(), [...expectedIDs].sort(),
    'Payload hashes must cover exactly the selected source IDs.');
  return rows.map(({ id, payload }) => {
    assert(payload !== null && typeof payload === 'object' && !Array.isArray(payload),
      `Source payload ${id} must be a JSON object.`);
    const serialized = canonicalJSON(payload);
    const payloadHash = createHash('sha256').update(serialized).digest('hex');
    return { id, payloadHash };
  }).sort((left, right) => left.id.localeCompare(right.id));
}
