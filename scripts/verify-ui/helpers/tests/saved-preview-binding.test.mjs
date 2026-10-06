import assert from 'node:assert/strict';
import test from 'node:test';
import { selectSavedPreviewRequest } from '../saved-preview-binding.mjs';

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
