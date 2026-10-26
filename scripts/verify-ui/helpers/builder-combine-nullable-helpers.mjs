import { isDeepStrictEqual } from 'node:util';
import { classifyNativeBrowserApiRequest, isSameUiProxyResponse } from './native-browser-api-scope.mjs';

export const nativeResponseScopeEvidence = (requestURL, responseURL, scope) => {
  let request;
  let response;
  try {
    request = classifyNativeBrowserApiRequest(requestURL, scope);
    response = classifyNativeBrowserApiRequest(responseURL, scope);
  } catch (error) {
    return { ok: false, reason: 'invalid-url', error: String(error), requestURL, responseURL };
  }
  const sameProxyResponse = isSameUiProxyResponse(requestURL, responseURL, scope);
  const requestOwnedExplorer = request.kind === 'capture' && request.scope === 'owned-project-explorer';
  const responseOwnedExplorer = response.kind === 'capture' && response.scope === 'owned-project-explorer';
  return {
    ok: requestOwnedExplorer && responseOwnedExplorer && sameProxyResponse,
    requestOwnedExplorer,
    responseOwnedExplorer,
    requestKind: request.kind,
    requestReason: request.reason ?? null,
    responseKind: response.kind,
    responseReason: response.reason ?? null,
    sameProxyResponse,
    requestOrigin: request.url.origin,
    requestPath: request.url.pathname,
    responseOrigin: response.url.origin,
    responsePath: response.url.pathname,
  };
};

export const builderCancelStateEvidence = (before, after) => {
  const complete = (builder) => typeof builder?.draftVersion === 'number' && Number.isFinite(builder.draftVersion) && builder.draftVersion > 0 &&
    typeof builder?.draftDigest === 'string' && builder.draftDigest.trim() !== '' &&
    Array.isArray(builder?.workspace?.documents);
  const beforeComplete = complete(before);
  const afterComplete = complete(after);
  const sameVersion = before?.draftVersion === after?.draftVersion;
  const sameDigest = before?.draftDigest === after?.draftDigest;
  const sameWorkspace = isDeepStrictEqual(before?.workspace, after?.workspace);
  return {
    ok: beforeComplete && afterComplete && sameVersion && sameDigest && sameWorkspace,
    beforeComplete,
    afterComplete,
    sameVersion,
    sameDigest,
    sameWorkspace,
    beforeDraftVersion: before?.draftVersion ?? null,
    afterDraftVersion: after?.draftVersion ?? null,
    beforeDraftDigest: before?.draftDigest ?? null,
    afterDraftDigest: after?.draftDigest ?? null,
  };
};

export const targetDocumentStateEvidence = (before, after) => {
  const complete = (document) => document !== null && typeof document === 'object' && !Array.isArray(document);
  const sameDocument = complete(before) && complete(after) && isDeepStrictEqual(before, after);
  return {
    ok: sameDocument,
    sameDocument,
    beforeOutputId: before?.output?.id ?? null,
    afterOutputId: after?.output?.id ?? null,
  };
};

export const removalProposalEvidence = ({
  responseStatus,
  response,
  requestBody,
  expectedOutputId,
  expectedStepID,
  expectedSnapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
  domProposalId,
  domReceiptId,
  transportEvidence,
}) => {
  const proposalID = response?.proposalId;
  const preview = response?.preview;
  const candidate = requestBody?.candidateConstruction;
  const removalBound = Array.isArray(requestBody?.removeStepIds) &&
    isDeepStrictEqual(requestBody.removeStepIds, [expectedStepID]) &&
    Array.isArray(candidate?.steps) && candidate.steps.length === 0;
  const requestBound = requestBody?.outputId === expectedOutputId &&
    requestBody?.snapshotToken === expectedSnapshotToken &&
    requestBody?.expectedDraftVersion === expectedDraftVersion &&
    requestBody?.expectedDraftDigest === expectedDraftDigest;
  const responseBound = response?.outputId === expectedOutputId &&
    response?.snapshotToken === expectedSnapshotToken &&
    response?.draftVersion === expectedDraftVersion &&
    response?.draftDigest === expectedDraftDigest &&
    isDeepStrictEqual(response?.candidateConstruction, candidate);
  const receiptBound = Boolean(proposalID) && preview?.receiptId === proposalID &&
    domProposalId === proposalID && domReceiptId === proposalID;
  const transportBound = transportEvidence?.ok === true;
  const ready = responseStatus === 200 && response?.previewStatus === 'READY' && preview?.outputId === expectedOutputId;
  return {
    ok: removalBound && requestBound && responseBound && receiptBound && transportBound && ready,
    removalBound,
    requestBound,
    responseBound,
    receiptBound,
    transportBound,
    ready,
    responseStatus,
    proposalID: proposalID ?? null,
    outputId: preview?.outputId ?? null,
    removeStepIds: requestBody?.removeStepIds ?? null,
    candidateStepCount: Array.isArray(candidate?.steps) ? candidate.steps.length : null,
    snapshotToken: requestBody?.snapshotToken ?? null,
    draftVersion: requestBody?.expectedDraftVersion ?? null,
    draftDigest: requestBody?.expectedDraftDigest ?? null,
    transportEvidence: transportEvidence ?? null,
  };
};
