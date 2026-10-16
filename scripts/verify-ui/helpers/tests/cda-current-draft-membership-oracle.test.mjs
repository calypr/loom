import assert from 'node:assert/strict';
import test from 'node:test';
import { compareCdaSourceGroupPreview } from '../cda-current-draft-membership-oracle.mjs';

const visible = rows => rows.map(([status, count]) => [status === null ? '—' : String(status), String(count)]);

test('source status GROUP preview compares the complete under-limit multiset and renders null as an em dash', () => {
  const rawGroupRows = [
    { status: 'final', count: 2 },
    { status: null, count: 4 },
    { status: 'preliminary', count: 3 },
  ];
  const previewRows = [['preliminary', 3], [null, 4], ['final', 2]];
  const result = compareCdaSourceGroupPreview({
    previewRows,
    visibleRows: visible(previewRows),
    rawGroupRows,
    displayedRereadRows: [],
    rowCount: 3,
    sampled: false,
  });

  assert.deepEqual(result, {
    ok: true,
    comparison: 'complete-raw-group-set',
    expectedRowCount: 3,
    expectedSampled: false,
    expectedRows: [['final', '2'], ['—', '4'], ['preliminary', '3']],
    displayedStatusKeys: ['preliminary', null, 'final'],
  });
});

test('exactly 25 source status groups are sampled and compared against the complete bounded set', () => {
  const rawGroupRows = Array.from({ length: 25 }, (_, index) => ({
    status: `status-${String(index).padStart(2, '0')}`,
    count: index + 1,
  }));
  const previewRows = rawGroupRows.map(({ status, count }) => [status, count]);
  const result = compareCdaSourceGroupPreview({
    previewRows,
    visibleRows: visible(previewRows),
    rawGroupRows,
    displayedRereadRows: [],
    rowCount: 25,
    sampled: true,
  });

  assert.equal(result.ok, true);
  assert.equal(result.comparison, 'complete-raw-group-set');
  assert.equal(result.expectedRowCount, 25);
  assert.equal(result.expectedSampled, true);
  assert.equal(result.expectedRows.length, 25);
});

test('over-limit preview compares an arbitrary displayed subset with independent exact-key counts', () => {
  const rawGroupRows = Array.from({ length: 26 }, (_, index) => ({
    status: `status-${String(index).padStart(2, '0')}`,
    count: index + 1,
  }));
  const displayed = [rawGroupRows[25], ...rawGroupRows.slice(0, 24)].reverse();
  const previewRows = displayed.map(({ status, count }) => [status, count]);
  const displayedRereadRows = [...displayed].reverse();
  const result = compareCdaSourceGroupPreview({
    previewRows,
    visibleRows: visible(previewRows),
    rawGroupRows,
    displayedRereadRows,
    rowCount: 25,
    sampled: true,
  });

  assert.equal(result.ok, true);
  assert.equal(result.comparison, 'displayed-key-reread');
  assert.equal(result.expectedRowCount, 25);
  assert.equal(result.expectedSampled, true);
  assert.deepEqual(result.displayedStatusKeys, displayed.map(row => row.status));
  assert.equal(result.displayedStatusKeys.includes('status-25'), true);
  assert.equal(result.displayedStatusKeys.includes('status-24'), false);
});

test('source status GROUP comparison rejects wrong keys, counts, visible rows, and sample metadata', () => {
  const rawGroupRows = Array.from({ length: 26 }, (_, index) => ({
    status: `status-${String(index).padStart(2, '0')}`,
    count: index + 1,
  }));
  const displayed = [rawGroupRows[25], ...rawGroupRows.slice(0, 24)].reverse();
  const previewRows = displayed.map(({ status, count }) => [status, count]);
  const visibleRows = visible(previewRows);
  const input = {
    previewRows,
    visibleRows,
    rawGroupRows,
    displayedRereadRows: [...displayed],
    rowCount: 25,
    sampled: true,
  };

  assert.equal(compareCdaSourceGroupPreview({
    ...input,
    displayedRereadRows: input.displayedRereadRows.map((row, index) => index === 0
      ? { ...row, status: 'wrong-status' }
      : row),
  }).ok, false);
  assert.equal(compareCdaSourceGroupPreview({
    ...input,
    displayedRereadRows: input.displayedRereadRows.map((row, index) => index === 0
      ? { ...row, count: row.count + 1 }
      : row),
  }).ok, false);
  assert.equal(compareCdaSourceGroupPreview({ ...input, sampled: false }).ok, false);
  assert.equal(compareCdaSourceGroupPreview({
    ...input,
    visibleRows: visibleRows.map((row, index) => index === 0 ? ['wrong-status', row[1]] : row),
  }).ok, false);
});
