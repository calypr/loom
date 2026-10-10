export const proposalCancelActionSelector = '[data-testid="construction-cancel-proposal"]';

const exactOwnedChoicesPath = path =>
  typeof path === 'string' && /^\/api\/v1\/projects\/[^/]+\/explorers\/[^/]+\/authoring\/v2\/related-expand-choices$/.test(path);

const isFiniteTime = value => Number.isFinite(value) && value >= 0;

const requestIdentityMatches = (entry, identity) => entry?.origin === identity.origin &&
  entry?.path === identity.path &&
  entry?.method === 'POST' &&
  entry?.body?.outputId === identity.outputId &&
  entry?.body?.expectedDraftVersion === identity.draftVersion &&
  entry?.body?.expectedDraftDigest === identity.draftDigest &&
  entry?.body?.stageId === identity.stageId;

const assertUniqueIdentity = (entries, field, value) => {
  if (typeof value !== 'string' || !value) throw new TypeError(`Related-choice cancellation needs a concrete ${field}.`);
  if (entries.filter(entry => entry?.[field] === value).length !== 1) {
    throw new Error(`Related-choice cancellation requires a unique captured ${field}.`);
  }
};

export function snapshotPendingRelatedExpandChoices(nativeRequests, {
  origin,
  path,
  outputId,
  draftVersion,
  draftDigest,
  stageId,
  capturedAt = Date.now(),
}) {
  if (!Array.isArray(nativeRequests)) throw new TypeError('Related-choice cancellation needs the CDA native request capture.');
  const normalizedOrigin = new URL(origin).origin;
  if (!exactOwnedChoicesPath(path)) throw new Error('Related-choice cancellation needs the exact owned choices endpoint path.');
  if (typeof outputId !== 'string' || !outputId || !Number.isInteger(draftVersion) || draftVersion < 0 ||
    typeof draftDigest !== 'string' || !draftDigest || typeof stageId !== 'string' || !stageId) {
    throw new TypeError('Related-choice cancellation needs exact output, draft version/digest, and stage identity.');
  }
  if (!isFiniteTime(capturedAt)) throw new TypeError('Related-choice cancellation needs a valid pre-Cancel snapshot time.');

  const identity = Object.freeze({
    origin: normalizedOrigin,
    path,
    outputId,
    draftVersion,
    draftDigest,
    stageId,
  });
  const pendingEntries = nativeRequests.filter(entry => requestIdentityMatches(entry, identity) &&
    isFiniteTime(entry.startedAt) && entry.startedAt <= capturedAt &&
    entry.completedAt == null && entry.responseReceivedAt == null && entry.status == null && entry.failure == null);

  const entries = pendingEntries.map(entry => {
    assertUniqueIdentity(nativeRequests, 'browserRequestId', entry.browserRequestId);
    assertUniqueIdentity(nativeRequests, 'requestId', entry.requestId);
    if (!entry.requestId.startsWith('related-expand-choices-')) {
      throw new Error('Related-choice cancellation request IDs must identify the choices query.');
    }
    return Object.freeze({
      entry,
      browserRequestId: entry.browserRequestId,
      requestId: entry.requestId,
      startedAt: entry.startedAt,
    });
  });

  return Object.freeze({ capturedAt, identity, entries: Object.freeze(entries) });
}

export function classifyPendingRelatedExpandChoicesAfterProposalCancel({
  cda,
  snapshot,
  action,
  editorClosed,
  editorClosedAt,
  reason,
}) {
  if (!snapshot || !Array.isArray(snapshot.entries) || !snapshot.identity) {
    throw new TypeError('Related-choice cancellation needs its exact pre-Cancel request snapshot.');
  }
  if (!cda || !Array.isArray(cda.nativeRequests) || typeof cda.expectCapturedCancellation !== 'function') {
    throw new TypeError('Related-choice cancellation needs the owned CDA cancellation classifier.');
  }
  if (action?.label !== proposalCancelActionSelector || !isFiniteTime(action.startedAt) ||
    !isFiniteTime(action.completedAt) || action.completedAt < action.startedAt) {
    throw new Error('Related-choice cancellation requires the explicit proposal Cancel action window.');
  }
  if (editorClosed !== true || !isFiniteTime(editorClosedAt) || editorClosedAt < action.completedAt) {
    throw new Error('Related-choice cancellation can be classified only after the editor has closed.');
  }
  if (action.startedAt < snapshot.capturedAt || action.startedAt - snapshot.capturedAt > 1000) {
    throw new Error('The pending choices snapshot must immediately precede the explicit Cancel action.');
  }
  if (editorClosedAt - action.startedAt > 5000) {
    throw new Error('Related-choice cancellation fell outside the bounded Cancel-to-close window.');
  }
  if (typeof reason !== 'string' || !reason.trim()) throw new TypeError('Related-choice cancellation needs a concrete reason.');

  const eligible = [];
  for (const item of snapshot.entries) {
    const { entry } = item;
    if (cda.nativeRequests.filter(candidate => candidate === entry).length !== 1 ||
      entry.browserRequestId !== item.browserRequestId || entry.requestId !== item.requestId ||
      entry.startedAt !== item.startedAt) {
      throw new Error('A pending related-choice request changed identity after its pre-Cancel snapshot.');
    }
    if (!requestIdentityMatches(entry, snapshot.identity)) {
      throw new Error('A pending related-choice request changed output, draft, stage, or owned route identity.');
    }
    if (entry.failure !== 'net::ERR_ABORTED' || !isFiniteTime(entry.completedAt) ||
      entry.completedAt < action.startedAt || entry.completedAt > editorClosedAt) {
      continue;
    }
    eligible.push(item);
  }

  const classified = [];
  for (const item of eligible) {
    const { entry } = item;
    const proof = {
      action: action.label,
      actionWindow: { startedAt: action.startedAt, completedAt: action.completedAt, editorClosedAt },
      preCancelSnapshotAt: snapshot.capturedAt,
      identity: snapshot.identity,
      requestId: item.requestId,
      browserRequestId: item.browserRequestId,
      requestStartedAt: item.startedAt,
      requestFailedAt: entry.completedAt,
      editorClosed: true,
    };
    cda.expectCapturedCancellation(entry, reason, proof);
    classified.push(item.browserRequestId);
  }
  return classified;
}
