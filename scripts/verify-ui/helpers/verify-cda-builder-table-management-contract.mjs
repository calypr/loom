import assert from 'node:assert/strict';

export const tableManagementActions = new Set([
  'Verify duplicate and delete',
  'Cleanup orphan Patient table',
  'Duplicate table',
  'Rename reorder undo table',
  'Inspect tables',
  'Switch explorers',
]);

export function assertExactTableNames(actual, expected, label = 'table inventory') {
  assert.deepEqual(actual, expected, `${label} differs`);
}

export function assertPreviewShape(preview, expectedColumnCount) {
  assert(preview, 'Preview table is missing');
  const actualColumnCount = Number(preview.columnCount);
  assert.equal(actualColumnCount, expectedColumnCount, 'Preview column count differs');
  assert.equal(preview.headers.length, expectedColumnCount, 'Preview header count differs');
  return true;
}
