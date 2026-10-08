import assert from 'node:assert/strict';
import test from 'node:test';
import {
  expectedGroupAddFieldsRows,
  groupAddFieldsPreviewHeaders,
} from '../../workflows/verify-cda-group-add-fields-browser.mjs';

test('Group Add Columns expectations follow the three-column proposal schema and retain raw counts', () => {
  const rawGroupedRows = [
    ['raw-specimen-1', '12'],
    ['raw-specimen-2', '3'],
  ];

  const expected = expectedGroupAddFieldsRows(rawGroupedRows, 'Specimen');

  assert.deepEqual(groupAddFieldsPreviewHeaders, ['Specimen ID', 'Resource Type', 'Row count']);
  assert.deepEqual(expected, [
    ['raw-specimen-1', 'Specimen', '12'],
    ['raw-specimen-2', 'Specimen', '3'],
  ]);
  assert(expected.every(row => row.length === groupAddFieldsPreviewHeaders.length), 'Expected rows must match all three proposal columns');
  assert.notDeepEqual(expected, [
    ['raw-specimen-1', '12', 'Specimen'],
    ['raw-specimen-2', '3', 'Specimen'],
  ], 'The new source field precedes the Group aggregate in proposal order');
});
