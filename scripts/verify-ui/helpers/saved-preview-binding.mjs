import { isDeepStrictEqual } from 'node:util';

/** Select a successful native Preview request for one action/reload window. */
export const selectSavedPreviewRequest = (requests, { startIndex, path, receiptId, outputId }) => {
  if (!Array.isArray(requests) || !Number.isInteger(startIndex) || startIndex < 0 || startIndex > requests.length) return undefined;
  if (![path, receiptId, outputId].every(value => typeof value === 'string' && value.length > 0)) return undefined;
  return requests.slice(startIndex).find(entry =>
    entry.path === path && entry.status === 200 && entry.completedAt &&
    entry.body?.receiptId === receiptId && entry.body?.outputId === outputId &&
    entry.response?.receiptId === receiptId && entry.response?.outputId === outputId
  );
};

/** Reuse a saved Preview only when Cancel restores the same draft and visible receipt. */
export const selectCanceledSavedPreviewRequest = (requests, { startIndex, path, outputId, before, after }) => {
  if (!Array.isArray(requests) || !before || !after || !Number.isInteger(startIndex) || startIndex < 0 || startIndex > requests.length) return undefined;
  if (before.outputId !== outputId || after.outputId !== outputId) return undefined;
  if (typeof before.snapshotToken !== 'string' || !before.snapshotToken ||
    typeof before.draftDigest !== 'string' || !before.draftDigest ||
    !Number.isInteger(before.draftVersion) || !Array.isArray(before.construction?.steps)) return undefined;
  if (before.snapshotToken !== after.snapshotToken || before.draftVersion !== after.draftVersion ||
    before.draftDigest !== after.draftDigest || !isDeepStrictEqual(before.construction, after.construction)) return undefined;

  const previewMatchesDraft = state => state.preview?.status === 'ready' &&
    state.preview.outputId === outputId &&
    state.preview.draftVersion === String(state.draftVersion) &&
    state.preview.draftDigest === state.draftDigest &&
    typeof state.preview.receiptId === 'string' && state.preview.receiptId.length > 0;
  if (!previewMatchesDraft(before) || !previewMatchesDraft(after)) return undefined;

  const currentReceipt = selectSavedPreviewRequest(requests, {
    startIndex, path, receiptId: after.preview.receiptId, outputId,
  });
  if (currentReceipt) return { request: currentReceipt, source: 'after-cancel' };

  const cached = selectSavedPreviewRequest(requests, {
    startIndex: 0, path, receiptId: after.preview.receiptId, outputId,
  });
  return cached ? { request: cached, source: 'restored-saved-preview' } : undefined;
};
