import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { createInterface } from 'node:readline';
import assert from 'node:assert/strict';

const sha256 = value => createHash('sha256').update(value).digest('hex');

export async function readNDJSONResourceIdentityOracle({ path, project, generation, resourceType }) {
  for (const [name, value] of Object.entries({ path, project, generation, resourceType })) {
    if (typeof value !== 'string' || value.length === 0) throw new Error(`${name} is required for the independent resource oracle`);
  }
  const input = createReadStream(path);
  const contentHash = createHash('sha256');
  input.on('data', chunk => contentHash.update(chunk));
  const ids = [];
  for await (const line of createInterface({ input, crlfDelay: Infinity })) {
    if (!line.trim()) continue;
    const record = JSON.parse(line);
    if (record.resourceType !== resourceType || typeof record.id !== 'string' || !record.id) {
      throw new Error(`Raw source must contain ${resourceType} records with non-empty ids`);
    }
    ids.push(record.id);
  }
  if (ids.length === 0) throw new Error(`Independent raw ${resourceType} source must not be empty`);
  if (new Set(ids).size !== ids.length) throw new Error(`Raw ${resourceType} identities must be unique`);
  const storageKey = id => sha256(['vertex', project, generation, resourceType, id, ''].join('\0'));
  const ordered = ids.map(id => ({ id, key: storageKey(id) }))
    .sort((left, right) => left.key.localeCompare(right.key))
    .map(entry => entry.id);
  return {
    path,
    sha256: contentHash.digest('hex'),
    count: ids.length,
    ids: ordered,
    ordering: 'SHA-256 Arango vertex storage key for the exact project, generation, resource type, and source ID',
  };
}

export function assertVisibleRowsMatchOracle({ rows, sourceIds, ariaRowCount, previewLimit = 25 }) {
  assert(Array.isArray(sourceIds) && sourceIds.length > 0, 'Independent source identities are required');
  assert(Array.isArray(rows) && rows.length > 0, 'At least one visible result row is required');
  assert(Number.isInteger(previewLimit) && previewLimit > 0, 'Preview limit must be a positive integer');
  const expectedPreviewRows = Math.min(previewLimit, sourceIds.length);
  assert.equal(Number(ariaRowCount), expectedPreviewRows + 1,
    `Preview must report ${expectedPreviewRows} data rows plus its header`);
  assert(rows.length <= expectedPreviewRows, 'Visible virtualized rows cannot exceed the independent preview row count');
  const expected = rows.map((row, index) => ({ ordinal: index + 1, cells: [sourceIds[index]] }));
  assert(expected.every(row => typeof row.cells[0] === 'string'), 'Visible ordinal must map to an independent source identity');
  assert.deepEqual(rows, expected, 'Visible rows must be contiguous from the start of the independent source preview window');
  return expected;
}
