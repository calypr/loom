import assert from 'node:assert/strict';
import test from 'node:test';
import { assertGroupTwicePreview } from '../group-twice-journey.mjs';

test('second Group preview must contain only a count of one', () => {
  const preview = { rowCount: 1, columns: [{ column: 'row_count' }], rows: [{ __loom_row_id: 'row', row_count: 1 }] };
  assert.equal(assertGroupTwicePreview(preview), 1);
  assert.throws(() => assertGroupTwicePreview({ ...preview, rows: [{ row_count: 2 }] }));
  assert.throws(() => assertGroupTwicePreview({ ...preview, rows: [{ row_count: 1, extra: 'unexpected' }] }));
  assert.throws(() => assertGroupTwicePreview({ ...preview, rows: [] }));
});
