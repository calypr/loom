import assert from 'node:assert/strict';
import test from 'node:test';
import { collectVirtualPreviewRows } from '../virtual-preview-rows.mjs';

function virtualTable({ omitLast = false, changingRow = false } = {}) {
  let top = 20;
  const positions = [];
  return {
    positions,
    scrollTo: async value => { top = value; positions.push(value); },
    readWindow: async () => ({
      headers: ['ID', 'Value'], rowCount: 25, clientHeight: 40, scrollHeight: 250, scrollTop: top,
      rows: Array.from({ length: 25 }, (_, i) => ({ index: i + 1, cells: [`id-${i}`, changingRow && top > 0 && i === 2 ? 'changed' : String(i)] }))
        .filter(row => (row.index - 1) * 10 >= top - 20 && (row.index - 1) * 10 < top + 60 && !(omitLast && row.index === 25)),
    }),
  };
}

test('collects every row across overlapping virtual windows and restores scroll', async () => {
  const table = virtualTable();
  const result = await collectVirtualPreviewRows({ ...table, expectedCount: 25 });
  assert.deepEqual(result.rows, Array.from({ length: 25 }, (_, i) => [`id-${i}`, String(i)]));
  assert.equal(table.positions.at(-1), 20);
  assert(table.positions.includes(210));
});

test('missing rows remain fatal and scroll restoration still occurs', async () => {
  const table = virtualTable({ omitLast: true });
  await assert.rejects(collectVirtualPreviewRows({ ...table, expectedCount: 25 }), /did not expose every row/);
  assert.equal(table.positions.at(-1), 20);
});

test('a reused row number with changed values fails instead of masking stale DOM', async () => {
  const table = virtualTable({ changingRow: true });
  await assert.rejects(collectVirtualPreviewRows({ ...table, expectedCount: 25 }), /changed between windows/);
});
