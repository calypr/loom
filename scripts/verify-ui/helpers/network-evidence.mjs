export function capabilityBinding(request, target) {
  const url = new URL(request.url());
  const path = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(target.explorer)}/authoring/v2/construction-capabilities`;
  if (url.origin !== new URL(target.uiUrl).origin || request.method() !== 'POST' ||
      !target.explorer || url.pathname !== path) return null;
  let body;
  try { body = request.postDataJSON(); } catch { return null; }
  if (typeof body?.snapshotToken !== 'string' || !body.snapshotToken ||
      !Number.isInteger(body.expectedDraftVersion) || body.expectedDraftVersion <= 0 ||
      typeof body.expectedDraftDigest !== 'string' || !body.expectedDraftDigest ||
      typeof body.outputId !== 'string' || !body.outputId ||
      typeof body.stageId !== 'string' || !body.stageId) return null;
  return {
    route: url.origin + url.pathname,
    snapshotToken: body.snapshotToken,
    draftVersion: body.expectedDraftVersion,
    draftDigest: body.expectedDraftDigest,
    outputId: body.outputId,
    stageId: body.stageId,
  };
}

export function capabilityResponseMatches(binding, response) {
  return Boolean(binding && response && response.snapshotToken === binding.snapshotToken &&
    response.draftVersion === binding.draftVersion && response.draftDigest === binding.draftDigest &&
    response.outputId === binding.outputId && response.stageId === binding.stageId);
}

export function supersedingCapabilityRequest(failed, requests) {
  if (failed.errorText !== 'net::ERR_ABORTED' || !failed.binding) return null;
  return requests.find(replacement =>
    replacement.sequence > failed.sequence && replacement.status >= 200 && replacement.status < 300 &&
    replacement.finished === true && replacement.responseMatches === true && !replacement.failed &&
    replacement.binding?.route === failed.binding.route &&
    JSON.stringify(replacement.binding) !== JSON.stringify(failed.binding)) ?? null;
}

export function markExpectedOwnedPreviewAborts({
  network = [],
  lifecycle,
  actions = [],
  assertions = [],
  sourceOutputId,
  targetBindings = [],
  successorRequests = [],
} = {}) {
  const scope = lifecycle?.scope;
  const previewPath = scope?.paths?.preview;
  const reconcilePath = scope?.paths?.reconcile;
  const expectedPath = scope?.projectId && scope?.explorerId
    ? `/api/v1/projects/${encodeURIComponent(scope.projectId)}/explorers/${encodeURIComponent(scope.explorerId)}/authoring/v2/preview`
    : null;
  const expectedReconcilePath = scope?.projectId && scope?.explorerId
    ? `/api/v1/projects/${encodeURIComponent(scope.projectId)}/explorers/${encodeURIComponent(scope.explorerId)}/authoring/v2/reconcile`
    : null;
  if (!expectedPath || previewPath !== expectedPath || !scope.fixtureGeneration ||
      reconcilePath !== expectedReconcilePath || lifecycle.droppedRequests > 0 || lifecycle.droppedEvents > 0 ||
      lifecycle.incompleteEvidence === true || lifecycle.timedOutReads?.length > 0 || lifecycle.readErrors?.length > 0 ||
      !Number.isFinite(lifecycle.captureStartedAtMonotonicMs) ||
      !Number.isFinite(lifecycle.captureStoppedAtMonotonicMs)) return [];

  const expectedProposalPath = `/api/v1/projects/${encodeURIComponent(scope.projectId)}/explorers/${encodeURIComponent(scope.explorerId)}/authoring/v2/construction-proposals`;
  const classified = [];
  for (const failed of lifecycle.abortedPreviews ?? []) {
    const sourceCAS = failed.receiptBinding;
    if (failed.kind !== 'preview' || failed.errorText !== 'net::ERR_ABORTED' || failed.outputId !== sourceOutputId ||
        failed.receiptBindingMatchesOutput !== true ||
        !sourceCAS?.snapshotToken || !Number.isInteger(sourceCAS.draftVersion) || !sourceCAS.draftDigest ||
        !Number.isFinite(sourceCAS.reconciledAtMonotonicMs) ||
        sourceCAS.responseSnapshotToken !== sourceCAS.snapshotToken || !sourceCAS.outputIds?.includes(sourceOutputId) ||
        failed.actionAtStart?.name !== 'open native Combine and create a separate empty target' ||
        !failed.actionAtStart?.id || !Number.isFinite(failed.startedAtMonotonicMs) ||
        !Number.isFinite(failed.failedAtMonotonicMs) || failed.failedAtMonotonicMs < failed.startedAtMonotonicMs ||
        sourceCAS.reconciledAtMonotonicMs > failed.startedAtMonotonicMs ||
        failed.failedAtMonotonicMs < lifecycle.captureStartedAtMonotonicMs ||
        failed.failedAtMonotonicMs > lifecycle.captureStoppedAtMonotonicMs ||
        failed.visibleAfterFailure?.userVisiblePreviewError !== false) continue;

    const matchingFailures = network.filter((entry) => {
      if (entry.kind !== 'network' || entry.method !== 'POST' || entry.errorText !== 'net::ERR_ABORTED' ||
          entry.requestDetails?.outputId !== failed.outputId ||
          entry.requestDetails?.receiptId !== failed.receiptId ||
          entry.requestTimeline?.action?.id !== failed.actionAtStart.id ||
          (failed.networkRequestId && entry.requestDetails?.requestId !== failed.networkRequestId)) return false;
      try {
        const url = new URL(entry.rawURL ?? entry.url);
        return scope.origins.includes(url.origin) && url.pathname === previewPath;
      } catch { return false; }
    });
    if (matchingFailures.length !== 1) continue;

    for (const successorRequest of successorRequests) {
      const targetOutputId = successorRequest?.outputId;
      const targetBinding = targetBindings.find((binding) => binding.outputId === targetOutputId);
      const createCAS = targetBinding?.createCommandCAS;
      const sourceCASMatchesCreate = Boolean(createCAS &&
        createCAS.snapshotToken === sourceCAS.snapshotToken &&
        createCAS.draftVersion === sourceCAS.draftVersion &&
        createCAS.draftDigest === sourceCAS.draftDigest);
      const createActionPassed = actions.some((action) => action.id === failed.actionAtStart.id &&
        action.name === failed.actionAtStart.name && action.status === 'passed');
      const targetCreated = Boolean(targetBinding && targetOutputId !== sourceOutputId && sourceCASMatchesCreate &&
        assertions.some((assertion) => assertion.status === 'passed' &&
          assertion.name === 'native Combine uses CREATE_TABLE to make an empty rooted target without authoring a source' &&
          assertion.evidence?.outputId === targetOutputId));
      let successorOrigin;
      let successorPath;
      try {
        const parsedSuccessorURL = new URL(successorRequest?.url);
        successorOrigin = parsedSuccessorURL.origin;
        successorPath = parsedSuccessorURL.pathname;
      } catch {}
      const successorAssertion = assertions.find((assertion) => assertion.status === 'passed' &&
        assertion.name === 'automatic Combine proposal is bound to this exact draft CAS and UI proxy scope' &&
        assertion.evidence?.captureId === successorRequest?.captureId);
      const allChecksPassed = (checks) => Boolean(checks && Object.keys(checks).length > 0 &&
        Object.values(checks).every((value) => value === true));
      const successorAssertionPassed = Boolean(successorAssertion?.evidence?.responseBound &&
        allChecksPassed(successorAssertion.evidence.scopeChecks) &&
        allChecksPassed(successorAssertion.evidence.responseChecks));
      const successorActionPassed = actions.some((action) => action.id === successorRequest?.actionAtStartId &&
        action.name === successorRequest?.actionAtStartName && action.status === 'passed');
      const successorAfterAbort = Number.isFinite(successorRequest?.startedAtMonotonicMs) &&
        Number.isFinite(successorRequest?.responseAtMonotonicMs) &&
        successorRequest.responseAtMonotonicMs >= successorRequest.startedAtMonotonicMs &&
        successorRequest.startedAtMonotonicMs > failed.failedAtMonotonicMs &&
        successorRequest.responseAtMonotonicMs > failed.failedAtMonotonicMs &&
        successorRequest.startedAtMonotonicMs >= lifecycle.captureStartedAtMonotonicMs &&
        successorRequest.responseAtMonotonicMs <= lifecycle.captureStoppedAtMonotonicMs;
      const exactSelectedProposal = successorRequest?.path === expectedProposalPath &&
        successorPath === expectedProposalPath &&
        scope.origins.includes(successorOrigin) && successorRequest.status >= 200 && successorRequest.status < 300 &&
        successorRequest.responseMatchesRequest === true && successorRequest.currentDraftCASBound === true &&
        successorRequest.domOutputId === targetOutputId && successorRequest.domSelectedOutputId === targetOutputId &&
        successorRequest.responsePreviewOutputId === targetOutputId && successorRequest.responsePreviewReceiptId &&
        successorRequest.domReceiptId === successorRequest.responsePreviewReceiptId &&
        successorRequest.previewStatus === 'READY' && successorRequest.userVisiblePreviewError === false;
      if (!createActionPassed || !targetCreated || !successorActionPassed || !successorAfterAbort ||
          !successorAssertionPassed || !exactSelectedProposal) continue;

      const [entry] = matchingFailures;
      entry.canceled = true;
      entry.cancellationReason = 'native Combine CREATE_TABLE changed preview ownership; its later exact selected APPEND proposal completed successfully';
      entry.previewSuccessor = {
        captureId: successorRequest.captureId,
        outputId: targetOutputId,
        path: successorRequest.path,
        status: successorRequest.status,
        selectedOutputId: successorRequest.domSelectedOutputId,
        previewStatus: successorRequest.previewStatus,
        responseMatchedRequest: successorRequest.responseMatchesRequest,
      };
      entry.cancellationProof = {
        sourceOutputId: failed.outputId,
        sourceReceiptId: failed.receiptId,
        createCommandOutputId: targetOutputId,
        sourceReceiptCASMatchesCreateCommand: {
          snapshotIdentity: createCAS.snapshotToken === sourceCAS.snapshotToken,
          draftVersion: createCAS.draftVersion === sourceCAS.draftVersion,
          draftDigest: createCAS.draftDigest === sourceCAS.draftDigest,
        },
        createActionId: failed.actionAtStart.id,
        successorActionId: successorRequest.actionAtStartId,
        successorCaptureId: successorRequest.captureId,
        successorStartedAfterAbort: successorAfterAbort,
        successorRequestAndResponseBound: successorRequest.responseMatchesRequest,
        successorCurrentDraftCASBound: successorRequest.currentDraftCASBound,
        successorAssertionPassed,
        selectedOutputId: successorRequest.domSelectedOutputId,
        previewStatus: successorRequest.previewStatus,
        userVisiblePreviewError: successorRequest.userVisiblePreviewError,
      };
      classified.push({ failedRequestId: failed.id, successorRequestId: successorRequest.captureId, outputId: targetOutputId });
      break;
    }
  }
  return classified;
}

export function isIncidentalFavicon(url, target, status) {
  try {
    const parsed = new URL(url);
    return status === 404 && parsed.origin === new URL(target.uiUrl).origin && parsed.pathname === '/favicon.ico';
  } catch { return false; }
}

export function ownedFaultTarget(target, { method, path }) {
  if (typeof method !== 'string' || !method.trim()) throw new TypeError('fault method must be a non-empty string');
  if (typeof target.fixtureProject !== 'string' || !target.fixtureProject) {
    throw new TypeError('fault target requires the owned fixture project');
  }
  if (typeof path !== 'string' || !path.startsWith('/') || path.includes('?') || path.includes('#')) {
    throw new TypeError('fault path must be an exact pathname without a query or fragment');
  }
  const parsed = new URL(path, target.uiUrl);
  if (parsed.origin !== new URL(target.uiUrl).origin) throw new TypeError('fault path must remain on the owned UI origin');
  if (parsed.pathname !== path) throw new TypeError('fault path must be an exact pathname without encoded normalization');
  if (parsed.pathname === '/graphql/graph' && method.toUpperCase() === 'POST') {
    return { origin: parsed.origin, method: 'POST', path, bodyProject: target.fixtureProject };
  }
  const projectPrefix = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`;
  if (parsed.pathname !== projectPrefix && !parsed.pathname.startsWith(`${projectPrefix}/`)) {
    throw new TypeError('fault path must remain inside the owned fixture project');
  }
  return { origin: parsed.origin, method: method.toUpperCase(), path };
}

export function matchesOwnedFaultRequest(request, faultTarget, matchesRequest) {
  try {
    const url = new URL(request.url());
    if (url.origin !== faultTarget.origin || url.pathname !== faultTarget.path ||
        request.method() !== faultTarget.method) return false;
    if (!matchesRequest && !faultTarget.bodyProject) return true;
    let body;
    try { body = request.postDataJSON(); } catch { return false; }
    if (faultTarget.bodyProject && body?.variables?.input?.projectId !== faultTarget.bodyProject) return false;
    if (!matchesRequest) return true;
    const result = matchesRequest(body);
    return Boolean(result) && !(result && typeof result.then === 'function');
  } catch {
    return false;
  }
}

const exactInjectedRequest = (entry, fault) => Boolean(fault.matched && fault.playwrightRequestId &&
  entry.playwrightRequestId === fault.playwrightRequestId && entry.method === fault.method &&
  entry.rawURL === fault.rawURL);

const chromiumHttpResourceStatus = text => {
  const match = /^Failed to load resource: the server responded with a status of (\d{3})(?: \([^\r\n)]*\))?$/.exec(text ?? '');
  return match ? Number(match[1]) : undefined;
};

const sameURLRequest = (entry, location, status) => {
  try {
    const url = new URL(location);
    const query = Object.fromEntries(url.searchParams.entries());
    const requestQuery = entry.query ?? {};
    const sameQuery = JSON.stringify(Object.entries(query).sort(([left], [right]) => left.localeCompare(right))) ===
      JSON.stringify(Object.entries(requestQuery).sort(([left], [right]) => left.localeCompare(right)));
    return entry.origin === url.origin && entry.path === url.pathname && entry.status === status && sameQuery;
  } catch {
    return false;
  }
};

/**
 * Match fixture and request-capture console records to one exact expected HTTP response.
 * Ambiguous requests or duplicate records stay unclassified for the verification gate to reject.
 */
export function matchExpectedHttpConsole({ capturedEntry, nativeRequests = [], diagnostics = [], errors = [] } = {}) {
  const status = capturedEntry?.status;
  if (![400, 422].includes(status) || !nativeRequests.includes(capturedEntry)) return undefined;

  const matches = (entry, kind) => {
    if (entry?.kind !== kind) return false;
    const message = kind === 'console-error' ? entry.text : entry.message;
    const location = entry.rawLocation ?? entry.location;
    if (chromiumHttpResourceStatus(message) !== status) return false;
    const matchingRequests = nativeRequests.filter(request => sameURLRequest(request, location, status));
    return matchingRequests.length === 1 && matchingRequests[0].browserRequestId === capturedEntry.browserRequestId;
  };

  const fixtureMatches = diagnostics.filter(entry => matches(entry, 'console-error'));
  const reportMatches = errors.filter(entry => matches(entry, 'console'));
  const fixtureDiagnostic = fixtureMatches.length === 1 ? fixtureMatches[0] : undefined;
  const reportError = reportMatches.length === 1 ? reportMatches[0] : undefined;
  if (!fixtureDiagnostic && !reportError) return undefined;

  const observed = fixtureDiagnostic ?? reportError;
  return {
    fixtureDiagnostic,
    reportError,
    status,
    message: observed.text ?? observed.message,
    location: observed.rawLocation ?? observed.location,
  };
}

/** Apply expected-fault labels only to the one request that the fixture routed. */
export function applyInjectedFaultPolicy(entries, faults) {
  const result = entries.map(entry => ({ ...entry }));
  const consumedFaults = new Set();
  const expectedAborts = [];
  const expected422s = [];

  for (const entry of result) {
    if (entry.kind !== 'network') continue;
    const index = faults.findIndex((fault, candidate) => !consumedFaults.has(candidate) && exactInjectedRequest(entry, fault));
    if (index < 0) continue;
    const fault = faults[index];
    const injected422 = fault.action === 'fulfill' && fault.responseStatus === 422 && entry.status === 422;
    const injectedAbort = fault.action === 'abort' && entry.errorText === 'net::ERR_FAILED';
    if (!injected422 && !injectedAbort) continue;
    consumedFaults.add(index);
    entry.injectedFault = true;
    entry.injectedAction = fault.action;
    entry.injectedRequestId = fault.id;
    if (injected422) {
      entry.injectedStatus = 422;
      expected422s.push(entry);
    }
    if (injectedAbort) expectedAborts.push(entry);
  }

  const consumedConsoleRequests = new Set();
  for (let index = 0; index < result.length; index += 1) {
    const entry = result[index];
    if (entry.kind !== 'console-error') continue;
    const abortFailure = entry.text === 'Failed to load resource: net::ERR_FAILED'
      ? expectedAborts.find(candidate => !consumedConsoleRequests.has(candidate.playwrightRequestId) &&
        candidate.method && candidate.rawURL === entry.rawLocation && candidate.errorText === 'net::ERR_FAILED')
      : undefined;
    const httpFailure = !abortFailure && /^Failed to load resource: the server responded with a status of 422(?: \([^\r\n]*\))?$/.test(entry.text)
      ? expected422s.find(candidate => !consumedConsoleRequests.has(candidate.playwrightRequestId) &&
        candidate.method && candidate.rawURL === entry.rawLocation && candidate.status === 422 && candidate.injectedStatus === 422)
      : undefined;
    const failure = abortFailure ?? httpFailure;
    if (!failure) continue;
    consumedConsoleRequests.add(failure.playwrightRequestId);
    result[index] = {
      kind: 'network',
      observedAs: 'console-error',
      text: entry.text,
      location: entry.location,
      method: failure.method,
      url: entry.location,
      errorText: abortFailure ? 'net::ERR_FAILED' : undefined,
      status: httpFailure ? 422 : undefined,
      injectedStatus: httpFailure ? 422 : undefined,
      injectedFault: true,
      injectedAction: failure.injectedAction,
      injectedRequestId: failure.injectedRequestId,
    };
  }

  for (const entry of result) {
    delete entry.rawURL;
    delete entry.rawLocation;
  }
  return result;
}
