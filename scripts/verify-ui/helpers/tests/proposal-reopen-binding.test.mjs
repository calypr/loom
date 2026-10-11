import assert from 'node:assert/strict';
import test from 'node:test';
import { assertReopenedProposalAfterCancel } from '../proposal-reopen-binding.mjs';

const state = { catalog: { snapshotToken: 'snapshot-a' }, draftVersion: 9, draftDigest: 'draft-a' };
const outputId = 'output-a';
const removeStepIds = ['related-source-step'];
const proposal = (requestIndex, startedAt) => {
  const response = {
    proposalId: 'receipt-content-addressed',
    outputId,
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
    baseReceiptId: 'base-receipt',
    baseDocumentDigest: 'base-document',
    candidateWorkspaceDigest: 'candidate-workspace',
    previewStatus: 'READY',
    candidateConstruction: { steps: [{ id: 'group', operation: { kind: 'GROUP' } }] },
    preview: { receiptId: 'receipt-content-addressed', outputId, rows: [{ row_count: 1 }] },
  };
  return {
    requestIndex,
    request: {
      path: '/api/v1/projects/p/explorers/e/authoring/v2/construction-proposals',
      startedAt,
      completedAt: startedAt + 10,
      status: 200,
      networkComplete: true,
      responseCaptureComplete: true,
      responseBodyCaptured: true,
      request: {
        snapshotToken: state.catalog.snapshotToken,
        expectedDraftVersion: state.draftVersion,
        expectedDraftDigest: state.draftDigest,
        outputId,
        removeStepIds,
      },
    },
    response,
  };
};

test('accepts a fresh post-Cancel request when identical content has the same receipt ID', () => {
  const previous = proposal(12, 100);
  const reopened = proposal(17, 250);
  assert.doesNotThrow(() => assertReopenedProposalAfterCancel({
    previous, cancelledAt: 200, current: reopened, state, outputId, removeStepIds,
  }));
});

test('rejects a stale capture even when its receipt and candidate content match', () => {
  const previous = proposal(12, 100);
  const stale = proposal(12, 150);
  assert.throws(() => assertReopenedProposalAfterCancel({
    previous, cancelledAt: 200, current: stale, state, outputId, removeStepIds,
  }), /new native request/);
});

test('rejects a fresh request bound to a changed draft or mismatched preview receipt', () => {
  const previous = proposal(12, 100);
  const changedDraft = proposal(17, 250);
  changedDraft.request.request.expectedDraftVersion += 1;
  assert.throws(() => assertReopenedProposalAfterCancel({
    previous, cancelledAt: 200, current: changedDraft, state, outputId, removeStepIds,
  }), /unchanged current draft version/);

  const mismatchedPreview = proposal(18, 260);
  mismatchedPreview.response.preview.receiptId = 'other-receipt';
  assert.throws(() => assertReopenedProposalAfterCancel({
    previous, cancelledAt: 200, current: mismatchedPreview, state, outputId, removeStepIds,
  }), /reopened preview receipt/);
});

test('requires the current native network and response-body capture to complete', () => {
  const previous = proposal(12, 100);
  for (const field of ['networkComplete', 'responseCaptureComplete', 'responseBodyCaptured']) {
    const incomplete = proposal(17, 250);
    incomplete.request[field] = false;
    assert.throws(() => assertReopenedProposalAfterCancel({
      previous, cancelledAt: 200, current: incomplete, state, outputId, removeStepIds,
    }), /successful native response with its body captured/, `${field} must be true`);
  }
});
