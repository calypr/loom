import assert from 'node:assert/strict';

export async function collectVirtualPreviewRows({ readWindow, scrollTo, expectedCount }) {
  const initial = await readWindow();
  const collected = new Map();
  const headers = initial.headers;
  const maximumTop = Math.max(0, initial.scrollHeight - initial.clientHeight);
  const step = Math.max(1, Math.floor(initial.clientHeight / 2));
  try {
    for (let top = 0; ; top = Math.min(maximumTop, top + step)) {
      await scrollTo(top);
      const snapshot = await readWindow();
      assert.equal(snapshot.rowCount, expectedCount, 'Virtual preview row count changed during collection');
      assert.deepEqual(snapshot.headers, headers, 'Virtual preview headers changed during collection');
      for (const row of snapshot.rows) {
        assert(Number.isInteger(row.index) && row.index >= 1 && row.index <= expectedCount, 'Virtual preview exposes an invalid row number');
        assert.equal(row.cells.length, headers.length, 'Virtual preview row has missing visible columns');
        if (collected.has(row.index)) {
          assert.deepEqual(row.cells, collected.get(row.index), `Virtual preview row ${row.index} changed between windows`);
        }
        collected.set(row.index, row.cells);
      }
      if (top === maximumTop) break;
    }
    assert.equal(collected.size, expectedCount, 'Scrolling the virtual preview did not expose every row');
    return { headers, rows: Array.from({ length: expectedCount }, (_, index) => collected.get(index + 1)) };
  } finally {
    await scrollTo(initial.scrollTop);
  }
}
