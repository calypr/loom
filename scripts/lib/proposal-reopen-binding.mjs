import assert from 'node:assert/strict';

/** Verify that a post-Cancel removal candidate was newly captured and is still bound to the unchanged draft. */
export const assertReopenedProposalAfterCancel = ({ previous, cancelledAt, current, state, outputId, removeStepIds }) => {
  assert(Number.isInteger(previous?.requestIndex) && Number.isInteger(current?.requestIndex),
    'Both proposal captures must retain their native request indices');
  assert(current.requestIndex > previous.requestIndex,
    'The reopened proposal must come from a new native request after the canceled proposal');
  assert(current.request?.startedAt >= cancelledAt,
    'The reopened proposal request must start after Cancel completed');
  assert(current.request?.networkTerminal && current.request?.bodyReadStatus === 'decoded' &&
    current.request?.completedAt >= current.request?.startedAt && current.request?.status === 200,
  'The reopened proposal must have a successful, fully decoded native response');
  assert.equal(current.request.path, previous.request.path);
  assert.equal(current.request.body?.snapshotToken, state.catalog.snapshotToken);
  assert.equal(current.request.body?.expectedDraftVersion, state.draftVersion,
    'The reopened request must bind to the unchanged current draft version');
  assert.equal(current.request.body?.expectedDraftDigest, state.draftDigest,
    'The reopened request must bind to the unchanged current draft digest');
  assert.equal(current.request.body?.outputId, outputId);
  assert.deepEqual(current.request.body?.removeStepIds, removeStepIds);

  const before = previous.response;
  const after = current.response;
  assert(after?.proposalId, 'The reopened response must return a candidate receipt');
  assert.equal(after.outputId, outputId);
  assert.equal(after.snapshotToken, state.catalog.snapshotToken);
  assert.equal(after.draftVersion, state.draftVersion);
  assert.equal(after.draftDigest, state.draftDigest);
  assert.equal(after.baseReceiptId, before.baseReceiptId);
  assert.equal(after.baseDocumentDigest, before.baseDocumentDigest);
  assert.equal(after.candidateWorkspaceDigest, before.candidateWorkspaceDigest);
  assert.equal(after.previewStatus, 'READY');
  assert.equal(after.preview?.receiptId, after.proposalId,
    'The reopened preview receipt must match the reopened proposal ID');
  assert.equal(after.preview?.outputId, outputId);
  assert.deepEqual(after.candidateConstruction, before.candidateConstruction);
  assert.deepEqual(after.preview?.rows, before.preview?.rows);
  return current;
};
