import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { assertBoundedPreviewCount, assertPreviewRowsMatchRawObservations, assertReloadPreviewContext } from '../root-quantity-raw-preview.mjs';

test('bounded native preview count remains distinct from the full raw source total', () => {
  const preview = { rowCount: 25, rows: Array.from({ length: 25 }, (_, index) => ({ id: `obs-${index}` })) };
  assert.deepEqual(assertBoundedPreviewCount(preview, 815261), {
    rawSourceRows: 815261,
    boundedPreviewRows: 25,
    limit: 25,
  });
  assert.throws(() => assertBoundedPreviewCount({ rowCount: 815261, rows: [] }, 815261), /bounded rows consumed/);
});

test('bounded preview tuples preserve exact IDs and distinguish MISSING, NULL, and scalar code from raw records', () => {
  const previewRows = [
    { id: 'obs-d', code: 'd', quantity: 2 },
    { id: 'obs-missing', code: null, quantity: 3 },
    { id: 'obs-null', code: null, quantity: 5 },
  ];
  const rawRows = [
    { id: 'obs-d', status: 'final', codePresent: true, codeValue: 'd', valuePresent: true, value: 2 },
    { id: 'obs-missing', status: 'final', codePresent: false, codeValue: null, valuePresent: true, value: 3 },
    { id: 'obs-null', status: 'final', codePresent: true, codeValue: null, valuePresent: true, value: 5 },
  ];
  const rawAggregateGroups = [
    { status: 'final', present: true, value: 'd', rowCount: 1 },
    { status: 'final', present: false, value: null, rowCount: 1 },
    { status: 'final', present: true, value: null, rowCount: 1 },
  ];
  assert.deepEqual(assertPreviewRowsMatchRawObservations({
    previewRows,
    rawRows,
    idColumn: 'id',
    categoryColumn: 'code',
    valueColumn: 'quantity',
    rawAggregateGroups,
  }).map(row => row.category).sort(), ['MISSING', 'NULL', JSON.stringify({ kind: 'STRING', string: 'd' })].sort());
});

test('raw tuple mismatch rejects a bounded preview even when its ID is correct', () => {
  assert.throws(() => assertPreviewRowsMatchRawObservations({
    previewRows: [{ id: 'obs-1', code: 'd', quantity: 2 }],
    rawRows: [{ id: 'obs-1', status: 'final', codePresent: true, codeValue: 'x', valuePresent: true, value: 2 }],
    idColumn: 'id',
    categoryColumn: 'code',
    valueColumn: 'quantity',
    rawAggregateGroups: [{ status: 'final', present: true, value: 'x', rowCount: 1 }],
  }), /quantity\.code value differs/);
});

test('reloaded preview uses its own fresh bounded sample and exact reload receipt context', () => {
  const basePath = '/api/v1/projects/cda/explorers/root-quantity/authoring/v2';
  const snapshotToken = 'sha256:current-snapshot';
  const draftDigest = 'sha256:current-draft';
  const requests = [
    {
      endpoint: 'reconcile',
      pathname: `${basePath}/reconcile`,
      status: 200,
      body: { snapshotToken, draftVersion: 8, draftDigest },
      response: {
        receiptId: 'reload-receipt', snapshotToken, generation: 'cda-v1',
        outputs: [{ outputId: 'out-root' }],
      },
    },
    {
      endpoint: 'preview',
      pathname: `${basePath}/preview`,
      status: 200,
      body: { receiptId: 'reload-receipt', outputId: 'out-root', limit: 25 },
      response: {
        receiptId: 'reload-receipt', outputId: 'out-root', rowCount: 1,
        columns: [{ column: 'id' }, { column: 'code' }, { column: 'quantity' }],
        rows: [{ id: 'obs-after-reload', code: 'd', quantity: 7 }],
      },
    },
  ];
  const fresh = assertReloadPreviewContext({
    requests, requestOffset: 0, basePath, outputId: 'out-root', generation: 'cda-v1',
    snapshotToken, draftVersion: 8, draftDigest,
  });
  assert.notDeepEqual(fresh.preview.rows.map(row => row.id), ['obs-before-reload'], 'Reload may legitimately return a different bounded row sample');
  assert.deepEqual(assertPreviewRowsMatchRawObservations({
    previewRows: fresh.preview.rows,
    rawRows: [{ id: 'obs-after-reload', status: 'final', codePresent: true, codeValue: 'd', valuePresent: true, value: 7 }],
    idColumn: 'id', categoryColumn: 'code', valueColumn: 'quantity',
    rawAggregateGroups: [{ status: 'final', present: true, value: 'd', rowCount: 3 }],
  }).map(row => row.id), ['obs-after-reload']);
  assert.throws(() => assertPreviewRowsMatchRawObservations({
    previewRows: [{ ...fresh.preview.rows[0], quantity: 8 }],
    rawRows: [{ id: 'obs-after-reload', status: 'final', codePresent: true, codeValue: 'd', valuePresent: true, value: 7 }],
    idColumn: 'id', categoryColumn: 'code', valueColumn: 'quantity',
    rawAggregateGroups: [{ status: 'final', present: true, value: 'd', rowCount: 3 }],
  }), /quantity\.value differs/);
  assert.throws(() => assertReloadPreviewContext({
    requests, requestOffset: 0, basePath, outputId: 'out-root', generation: 'cda-v1',
    snapshotToken, draftVersion: 7, draftDigest,
  }), /exact current draft version/);
});

test('full-population removal timing is locally declared before independent raw queries', async () => {
  const source = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  const start = source.indexOf('const runFullPopulationLifecycle = async');
  const end = source.indexOf('\n};', start);
  assert(start >= 0 && end > start, 'Full-population lifecycle function must be present in the native workflow');
  const lifecycle = source.slice(start, end);
  const declaration = lifecycle.indexOf('const removePreviewRenderedAt = Date.now();');
  const measure = lifecycle.indexOf("measure('full CDA quantity Pivot removal preview', removeStarted, removePreviewRenderedAt);");
  const rawOracle = lifecycle.indexOf('runRawObservationTupleOracle(expectedGeneration, boundedIDs)');
  assert(declaration >= 0, 'Removal render timestamp must be declared inside the full-population lifecycle scope');
  assert(measure > declaration, 'Removal preview measurement must use its locally captured render timestamp');
  assert(rawOracle > declaration, 'Removal render timestamp must be captured before independent raw tuple queries');
});
