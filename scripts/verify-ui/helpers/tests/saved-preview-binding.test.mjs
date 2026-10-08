import assert from 'node:assert/strict';
import test from 'node:test';
import { selectCanceledSavedPreviewRequest, selectSavedPreviewRequest } from '../saved-preview-binding.mjs';

const stalePreview = {
  path: '/preview', status: 200, completedAt: 10,
  body: { receiptId: 'receipt-before-repair', outputId: 'output-a' },
  response: { receiptId: 'receipt-before-repair', outputId: 'output-a' },
};

test('saved preview selection rejects a successful receipt from before the action window', () => {
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 1, path: '/preview', receiptId: 'receipt-before-repair', outputId: 'output-a',
  }), undefined);
});

test('saved preview selection rejects a receipt from a different draft/output context', () => {
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 0, path: '/preview', receiptId: 'receipt-after-repair', outputId: 'output-a',
  }), undefined);
  assert.equal(selectSavedPreviewRequest([stalePreview], {
    startIndex: 0, path: '/preview', receiptId: 'receipt-before-repair', outputId: 'output-b',
  }), undefined);
});

const savedState = (receiptId = 'receipt-a') => ({
  snapshotToken: 'snapshot-a',
  draftVersion: 4,
  draftDigest: 'draft-a',
  outputId: 'output-a',
  construction: { version: 1, steps: [{ id: 'expand-a', operation: { kind: 'EXPAND' } }] },
  preview: {
    status: 'ready', receiptId, outputId: 'output-a',
    draftVersion: '4', draftDigest: 'draft-a',
  },
});

const savedRequest = (receiptId = 'receipt-a', outputId = 'output-a', path = '/preview') => ({
  path, status: 200, completedAt: 10,
  body: { receiptId, outputId },
  response: { receiptId, outputId, rows: [{ __loom_row_id: 'expand-row-1' }] },
});

test('Cancel reuses the exact after-state saved receipt only when draft, snapshot, output, and construction are unchanged', () => {
  const preview = savedRequest();
  const state = savedState();
  const selected = selectCanceledSavedPreviewRequest([preview], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before: state, after: structuredClone(state),
  });
  assert.deepEqual(selected, { request: preview, source: 'restored-saved-preview' });
  assert.deepEqual(selected.request.response.rows, [{ __loom_row_id: 'expand-row-1' }]);
});

test('Cancel restores a cached saved Preview when the visible before receipt belongs to the proposal', () => {
  const saved = savedRequest('saved-receipt');
  const proposal = {
    path: '/construction-proposals', status: 200, completedAt: 11,
    body: { outputId: 'output-a' },
    response: {
      proposalId: 'proposal-receipt',
      preview: { receiptId: 'proposal-receipt', outputId: 'output-a', rows: [{ __loom_row_id: 'proposal-row' }] },
    },
  };
  const selected = selectCanceledSavedPreviewRequest([saved, proposal], {
    startIndex: 2,
    path: '/preview',
    outputId: 'output-a',
    before: savedState('proposal-receipt'),
    after: savedState('saved-receipt'),
  });
  assert.deepEqual(selected, { request: saved, source: 'restored-saved-preview' });
});

test('Cancel uses a fresh saved receipt when one arrives in the action window', () => {
  const previous = savedRequest('receipt-a');
  const current = savedRequest('receipt-b');
  const before = savedState('proposal-receipt');
  const after = savedState('receipt-b');
  const selected = selectCanceledSavedPreviewRequest([previous, current], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after,
  });
  assert.deepEqual(selected, { request: current, source: 'after-cancel' });
});

test('Cancel refuses stale drafts, wrong outputs, and proposal-embedded preview rows', () => {
  const cached = savedRequest();
  const before = savedState();
  const changedDraft = savedState();
  changedDraft.draftDigest = 'draft-b';
  changedDraft.preview.draftDigest = 'draft-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedDraft,
  }), undefined);

  const changedSnapshot = savedState();
  changedSnapshot.snapshotToken = 'snapshot-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedSnapshot,
  }), undefined);

  const changedConstruction = savedState();
  changedConstruction.construction.steps[0].operation.kind = 'FILTER';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: changedConstruction,
  }), undefined);

  const wrongOutput = savedState();
  wrongOutput.preview.outputId = 'output-b';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: wrongOutput,
  }), undefined);

  const staleDomDigest = savedState();
  staleDomDigest.preview.draftDigest = 'stale-draft';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: staleDomDigest,
  }), undefined);

  const staleDomVersion = savedState();
  staleDomVersion.preview.draftVersion = '3';
  assert.equal(selectCanceledSavedPreviewRequest([cached], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: staleDomVersion,
  }), undefined);

  const proposalCache = savedRequest('receipt-a', 'output-a', '/construction-proposals');
  assert.equal(selectCanceledSavedPreviewRequest([proposalCache], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: structuredClone(before),
  }), undefined);

  const proposalOnlyResponse = {
    path: '/construction-proposals', status: 200, completedAt: 12,
    body: { outputId: 'output-a' },
    response: { preview: { receiptId: 'receipt-a', outputId: 'output-a', rows: [{ __loom_row_id: 'row-a' }] } },
  };
  assert.equal(selectCanceledSavedPreviewRequest([proposalOnlyResponse], {
    startIndex: 1, path: '/preview', outputId: 'output-a', before, after: structuredClone(before),
  }), undefined);
});
