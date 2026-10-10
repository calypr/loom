import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertExactTableNames,
  assertPreviewShape,
  tableManagementActions,
} from '../verify-cda-builder-table-management-contract.mjs';

test('table-management dispatch includes only its assigned actions', () => {
  assert.deepEqual([...tableManagementActions].sort(), [
    'Cleanup orphan Patient table',
    'Duplicate table',
    'Inspect tables',
    'Rename reorder undo table',
    'Switch explorers',
    'Verify duplicate and delete',
  ].sort());
});

test('table inventory assertion rejects lost duplicate persistence', () => {
  assertExactTableNames(['Specimen', 'Specimen copy'], ['Specimen', 'Specimen copy']);
  assert.throws(() => assertExactTableNames(['Specimen'], ['Specimen', 'Specimen copy']), /table inventory differs/);
});

test('preview shape assertion rejects incorrect visible results', () => {
  assertPreviewShape({ columnCount: '5', headers: ['id', 'subject', 'status', 'class', 'date'] }, 5);
  assert.throws(() => assertPreviewShape({ columnCount: '4', headers: ['id', 'subject', 'status', 'date'] }, 5), /Preview column count differs/);
  assert.throws(() => assertPreviewShape({ columnCount: '5', headers: ['id', 'subject'] }, 5), /Preview header count differs/);
});
