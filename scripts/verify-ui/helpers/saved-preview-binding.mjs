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
