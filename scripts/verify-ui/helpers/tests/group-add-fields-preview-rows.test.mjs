import assert from 'node:assert/strict';
import test from 'node:test';
import {
  expectedGroupAddFieldsRows,
  groupAddFieldsPreviewHeaders,
} from '../../workflows/verify-cda-group-add-fields-browser.mjs';

test('Group Add Columns expectations follow the applied presentation order and retain raw counts', () => {
  const rawGroupedRows = [
    ['raw-specimen-1', '12'],
    ['raw-specimen-2', '3'],
  ];

  const expected = expectedGroupAddFieldsRows(rawGroupedRows, 'Specimen');

  assert.deepEqual(groupAddFieldsPreviewHeaders, ['Specimen ID', 'Row count', 'Resource Type']);
  assert.deepEqual(expected, [
    ['raw-specimen-1', '12', 'Specimen'],
    ['raw-specimen-2', '3', 'Specimen'],
  ]);
  assert(expected.every(row => row.length === groupAddFieldsPreviewHeaders.length), 'Expected rows must match all three displayed columns');
  assert.notDeepEqual(expected, [
    ['raw-specimen-1', 'Specimen', '12'],
    ['raw-specimen-2', 'Specimen', '3'],
  ], 'The applied Group aggregate precedes the row-value field in output order');
});
