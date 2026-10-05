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
