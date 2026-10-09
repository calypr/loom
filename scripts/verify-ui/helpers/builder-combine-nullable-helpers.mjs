import { isDeepStrictEqual } from 'node:util';
import { classifyNativeBrowserApiRequest, isSameUiProxyResponse } from './native-browser-api-scope.mjs';

const projectExplorerCollectionPath = (project) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers`;

const freezeSnapshot = (value) => {
  if (!value || typeof value !== 'object' || Object.isFrozen(value)) return value;
  for (const child of Object.values(value)) freezeSnapshot(child);
  return Object.freeze(value);
};

export const buildNullableNativeRequestLedger = ({ project, explorer, nativeRequests = [], nativeRequestDrainEvidence = [] }) => {
  const projectPath = projectExplorerCollectionPath(project);
  const explorerPath = typeof explorer === 'string' && explorer.trim()
    ? `${projectPath}/${encodeURIComponent(explorer)}`
    : undefined;
  const ownedRequests = nativeRequests.filter((entry) =>
    (explorerPath && (entry.path === explorerPath || entry.path.startsWith(`${explorerPath}/`))) ||
    (entry.method === 'POST' && entry.path === projectPath));
  const ownedIndices = new Set(ownedRequests.map((entry) => nativeRequests.indexOf(entry)));
  const ownedDrainEvidence = nativeRequestDrainEvidence.map((evidence) => ({
    ...evidence,
    unresolvedRequests: (evidence.unresolvedRequests ?? []).filter((entry) => ownedIndices.has(entry.index)),
  })).filter((evidence) => evidence.unresolvedRequests.length > 0);
  const excludedRequests = nativeRequests.filter((entry) => !ownedIndices.has(nativeRequests.indexOf(entry)));
  const excludedIndices = new Set(excludedRequests.map((entry) => nativeRequests.indexOf(entry)));
  const excludedDrainEvidence = nativeRequestDrainEvidence.map((evidence) => ({
    ...evidence,
    unresolvedRequests: (evidence.unresolvedRequests ?? []).filter((entry) => excludedIndices.has(entry.index)),
  })).filter((evidence) => evidence.unresolvedRequests.length > 0);
  const terminalLedgerRequests = ownedRequests.map((entry) => {
    const terminalEvent = (entry.nativeEventChronology ?? []).find(({ event }) =>
      event === 'requestfinished' || event === 'requestfailed')?.event ?? null;
    const state = terminalEvent === 'requestfinished' ? 'finished' :
      terminalEvent === 'requestfailed' ? 'failed' : 'pending';
    const hasTerminalPayload = Number.isInteger(entry.status) ||
      (typeof entry.failure === 'string' && entry.failure.trim().length > 0);
    return {
      requestId: entry.requestId,
      browserRequestId: entry.browserRequestId,
      method: entry.method,
      path: entry.path,
      status: entry.status ?? null,
      failure: entry.failure ?? null,
      terminalEvent,
      state,
      complete: state !== 'pending' && hasTerminalPayload,
    };
  });
  const incompleteRequests = terminalLedgerRequests.filter((entry) => !entry.complete);
  const hasCreateRequest = ownedRequests.some((entry) => entry.method === 'POST' && entry.path === projectPath);
  const hasExplorerRequest = Boolean(explorerPath) && ownedRequests.some((entry) =>
    entry.path === explorerPath || entry.path.startsWith(`${explorerPath}/`));
  const complete = Boolean(explorerPath) && hasCreateRequest && hasExplorerRequest &&
    ownedRequests.length > 0 && incompleteRequests.length === 0 && ownedDrainEvidence.length === 0;

  return freezeSnapshot({
    nativeRequests: structuredClone(ownedRequests),
    nativeRequestDrainEvidence: structuredClone(ownedDrainEvidence),
    excludedNativeRequests: structuredClone(excludedRequests),
    excludedNativeRequestDrainEvidence: structuredClone(excludedDrainEvidence),
    nativeRequestTerminalLedger: {
      scope: 'fresh Explorer native routes and its project-scoped create request',
      project,
      explorer: explorer ?? null,
      complete,
      counts: {
        total: terminalLedgerRequests.length,
        finished: terminalLedgerRequests.filter((entry) => entry.state === 'finished').length,
        failed: terminalLedgerRequests.filter((entry) => entry.state === 'failed').length,
        pending: incompleteRequests.length,
      },
      requests: terminalLedgerRequests,
    },
    incompleteRequests,
  });
};

const diagnosticLocation = (url) => {
  try {
    const parsed = new URL(url);
    return { origin: parsed.origin, pathname: parsed.pathname };
  } catch {
    return null;
  }
};

const hasStableRequestId = (requestId) => typeof requestId === 'string' && requestId.trim() !== '' &&
  !/^playwright-\d+$/.test(requestId);

const matchesFixtureNetworkDiagnostic = (captureDiagnostic, fixtureDiagnostic) => {
  if (!captureDiagnostic || !fixtureDiagnostic) return false;
  if (captureDiagnostic.kind === 'request-capture-correlation') return false;

  if (captureDiagnostic.kind === 'network' || captureDiagnostic.kind === 'http') {
    const captureLocation = diagnosticLocation(captureDiagnostic.url);
    const fixtureLocation = diagnosticLocation(fixtureDiagnostic.url);
    if (fixtureDiagnostic.kind !== 'network' || captureDiagnostic.method !== fixtureDiagnostic.method ||
        !captureLocation || !fixtureLocation || captureLocation.origin !== fixtureLocation.origin ||
        captureLocation.pathname !== fixtureLocation.pathname) return false;
    if (captureDiagnostic.kind === 'http' && captureDiagnostic.status !== fixtureDiagnostic.status) return false;
    if (captureDiagnostic.kind === 'network' && captureDiagnostic.error !== fixtureDiagnostic.errorText) return false;
    const captureRequestId = captureDiagnostic.requestId;
    const fixtureRequestId = fixtureDiagnostic.requestId;
    return hasStableRequestId(captureRequestId) && hasStableRequestId(fixtureRequestId) && captureRequestId === fixtureRequestId;
  }
  return false;
};

/** Preserve the raw supplemental capture while relying on the fixture's existing exact diagnostic policy. */
export const retainNullableNativeCaptureErrors = (report, captureErrors = []) => {
  if (!Array.isArray(captureErrors)) throw new TypeError('Nullable native capture errors must be an array.');
  const usedFixtureDiagnostics = new Set();
  const retained = captureErrors.map((captureDiagnostic) => {
    const candidates = (report.network ?? []).flatMap((fixtureDiagnostic, index) =>
      !usedFixtureDiagnostics.has(index) && matchesFixtureNetworkDiagnostic(captureDiagnostic, fixtureDiagnostic)
        ? [{ fixtureDiagnostic, index }]
        : []);
    const match = candidates.length === 1 ? candidates[0] : null;
    if (match) usedFixtureDiagnostics.add(match.index);
    const existingPolicy = match?.fixtureDiagnostic?.expectedCancellation ? 'expected-cancellation' :
      match?.fixtureDiagnostic?.expectedHttpFailure ? 'expected-http-failure' : null;
    return {
      diagnostic: structuredClone(captureDiagnostic),
      fixtureDiagnosticMatch: match ? {
        kind: match.fixtureDiagnostic.kind,
        playwrightRequestId: match.fixtureDiagnostic.playwrightRequestId ?? null,
        requestId: match.fixtureDiagnostic.requestId ?? null,
        policy: existingPolicy,
      } : null,
    };
  });
  report.nullableNativeRequestCaptureDiagnostics = retained;
  for (const entry of retained) {
    if (entry.fixtureDiagnosticMatch) continue;
    // Capture correlation failures and ambiguous/unmatched diagnostics have no trusted fixture classification.
    report.errors.push({ ...entry.diagnostic, expected: false });
  }
  return {
    observed: retained.length,
    representedByFixtureDiagnostics: retained.filter((entry) => entry.fixtureDiagnosticMatch).length,
    unrepresented: retained.filter((entry) => !entry.fixtureDiagnosticMatch).length,
  };
};

export const assertNullableNativeRequestLedgerComplete = (ledger) => {
  if (!ledger.nativeRequestTerminalLedger.complete) {
    throw new Error('Nullable Combine native request capture did not reach a complete owned terminal ledger: ' +
      JSON.stringify({ incompleteRequests: ledger.incompleteRequests, nativeRequestDrainEvidence: ledger.nativeRequestDrainEvidence }));
  }
};

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
