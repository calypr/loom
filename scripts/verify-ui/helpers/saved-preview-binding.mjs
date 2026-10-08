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

const completedSuccessfulResponse = entry => entry?.status === 200 && Number.isFinite(entry.completedAt) &&
  !entry.failure && !entry.responseReadError && entry.response && entry.response.bodyNotRead !== true;

/** Bind a proposal preview to the exact accepted choice command and persisted reconciliation receipt. */
export const selectAcceptedChoicePreviewBinding = (requests, {
  startIndex, proposalPath, commandPath, reconcilePath, outputId, before, after,
}) => {
  if (!Array.isArray(requests) || !Number.isInteger(startIndex) || startIndex < 0 || startIndex > requests.length) return undefined;
  if (![proposalPath, commandPath, reconcilePath, outputId].every(value => typeof value === 'string' && value.length > 0)) return undefined;
  if (!before || !after || before.outputId !== outputId || after.outputId !== outputId ||
    typeof before.snapshotToken !== 'string' || !before.snapshotToken ||
    !Number.isInteger(before.draftVersion) || before.draftVersion < 1 ||
    typeof before.draftDigest !== 'string' || !before.draftDigest ||
    after.snapshotToken !== before.snapshotToken ||
    !Number.isInteger(after.draftVersion) || after.draftVersion <= before.draftVersion ||
    typeof after.draftDigest !== 'string' || !after.draftDigest || !after.workspace?.documents ||
    after.preview?.status !== 'ready' || after.preview.outputId !== outputId ||
    after.preview.draftVersion !== String(after.draftVersion) || after.preview.draftDigest !== after.draftDigest ||
    typeof after.preview.receiptId !== 'string' || !after.preview.receiptId) return undefined;

  for (const proposal of requests.slice(startIndex)) {
    if (proposal.path !== proposalPath || proposal.method !== 'POST' || !completedSuccessfulResponse(proposal)) continue;
    const proposalBody = proposal.body;
    const proposalResponse = proposal.response;
    const choices = proposalResponse?.constructionChoices;
    const candidateColumns = proposalResponse?.candidateColumnIds;
    const preview = proposalResponse?.preview;
    if (proposalBody?.commandId !== proposalResponse?.commandId ||
      proposalBody?.snapshotToken !== before.snapshotToken || proposalBody?.expectedDraftVersion !== before.draftVersion ||
      proposalBody?.expectedDraftDigest !== before.draftDigest || proposalBody?.outputId !== outputId ||
      proposalResponse?.snapshotToken !== before.snapshotToken || proposalResponse?.draftVersion !== before.draftVersion ||
      proposalResponse?.draftDigest !== before.draftDigest || proposalResponse?.outputId !== outputId ||
      proposalResponse?.previewStatus !== 'READY' || !proposalResponse?.candidateWorkspaceDigest ||
      !isDeepStrictEqual(proposalBody?.constructionChoices, choices) ||
      !Number.isFinite(proposal.startedAt) || !Number.isFinite(proposal.responseReceivedAt) ||
      proposal.responseReceivedAt < proposal.startedAt || proposal.completedAt < proposal.responseReceivedAt ||
      !Array.isArray(choices) || choices.length === 0 ||
      !Array.isArray(candidateColumns) || candidateColumns.length === 0 ||
      new Set(candidateColumns).size !== candidateColumns.length ||
      preview?.outputId !== outputId || preview?.receiptId !== after.preview.receiptId || !Array.isArray(preview?.rows)) continue;

    const choiceIds = choices.map(choice => choice?.choiceId);
    if (choiceIds.some(choiceId => typeof choiceId !== 'string' || !choiceId) || new Set(choiceIds).size !== choiceIds.length) continue;
    const apply = requests.slice(startIndex).find(entry => {
      if (entry.path !== commandPath || entry.method !== 'POST' || !completedSuccessfulResponse(entry) ||
        !Number.isFinite(entry.responseReceivedAt) || entry.responseReceivedAt < entry.startedAt ||
        entry.completedAt < entry.responseReceivedAt) return false;
      const body = entry.body;
      const commands = body?.commands;
      return body?.commandId === proposalResponse.commandId && body?.snapshotToken === before.snapshotToken &&
        body?.expectedDraftVersion === before.draftVersion && body?.expectedDraftDigest === before.draftDigest &&
        Array.isArray(commands) && choiceIds.every(choiceId => commands.some(command =>
          command?.type === 'APPLY_CONSTRUCTION_CHOICE' && command.outputId === outputId &&
          command.constructionChoice?.choiceId === choiceId));
    });
    if (!apply) continue;
    const applyResponse = apply.response;
    if (applyResponse?.commandId !== proposalResponse.commandId ||
      applyResponse?.draftVersion !== after.draftVersion || applyResponse?.draftDigest !== after.draftDigest ||
      !Number.isFinite(apply.startedAt) || apply.startedAt < proposal.responseReceivedAt ||
      !isDeepStrictEqual(applyResponse.workspace, after.workspace) ||
      !Array.isArray(applyResponse?.results) || !candidateColumns.every(column => applyResponse.results.some(result =>
        result?.type === 'COLUMN_ADDED' && result.outputId === outputId && result.column === column)) ||
      !Array.isArray(applyResponse?.workspace?.documents) ||
      !candidateColumns.every(column => applyResponse.workspace.documents.some(document => document?.output?.id === outputId &&
        document.columns?.some(candidate => candidate.column === column))) ||
      !candidateColumns.every(column => after.workspace.documents.some(document => document?.output?.id === outputId &&
        document.columns?.some(candidate => candidate.column === column)))) continue;

    const reconcile = requests.slice(startIndex).find(entry => entry.path === reconcilePath && entry.method === 'POST' &&
      completedSuccessfulResponse(entry) && entry.body?.snapshotToken === before.snapshotToken &&
      entry.body?.draftVersion === after.draftVersion && entry.body?.draftDigest === after.draftDigest &&
      Number.isFinite(entry.startedAt) && Number.isFinite(entry.responseReceivedAt) &&
      entry.startedAt >= apply.responseReceivedAt && entry.responseReceivedAt >= entry.startedAt &&
      entry.completedAt >= entry.responseReceivedAt &&
      entry.response?.snapshotToken === before.snapshotToken &&
      entry.response?.intentDigest === proposalResponse.candidateWorkspaceDigest &&
      entry.response?.receiptId === preview.receiptId &&
      entry.response?.outputs?.some(output => output?.outputId === outputId));
    if (!reconcile) continue;
    return { proposal, apply, reconcile, preview, receiptId: preview.receiptId, outputId, commandId: proposalResponse.commandId };
  }
  return undefined;
};

/** A cached accepted choice preview survives Cancel only for the exact same current draft and visible receipt. */
export const selectCanceledAcceptedChoicePreviewBinding = (before, after) => {
  if (!before || !after || before.kind !== 'accepted-choice' || after.kind !== 'accepted-choice') return undefined;
  if (![before.receiptId, before.outputId, before.commandId, before.snapshotToken, before.draftDigest].every(value =>
    typeof value === 'string' && value.length > 0)) return undefined;
  if (!Number.isInteger(before.draftVersion) || before.draftVersion < 1) return undefined;
  if (before.receiptId !== after.receiptId || before.outputId !== after.outputId || before.commandId !== after.commandId ||
    before.snapshotToken !== after.snapshotToken || before.draftVersion !== after.draftVersion ||
    before.draftDigest !== after.draftDigest) return undefined;
  return { source: 'restored-accepted-choice-preview', receiptId: after.receiptId, outputId: after.outputId };
};
