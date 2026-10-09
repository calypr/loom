const capabilitiesEndpoint = 'construction-capabilities';
const requestIDPattern = /^(?:cda-request-|construction-capabilities-)[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;

const exactRequestScope = (request, { project, explorer, origin }) => {
  let url;
  try { url = new URL(request.url()); } catch { return undefined; }
  const path = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/${capabilitiesEndpoint}`;
  if (url.origin !== origin || url.pathname !== path || request.method() !== 'POST') return undefined;

  let headers;
  try { headers = request.headers(); } catch { return undefined; }
  const requestId = Object.entries(headers ?? {}).find(([name]) => name.toLowerCase() === 'x-request-id')?.[1];
  if (typeof requestId !== 'string' || !requestIDPattern.test(requestId)) return undefined;

  let body;
  try { body = JSON.parse(request.postData() ?? ''); } catch { return undefined; }
  if (!body || typeof body !== 'object' || Array.isArray(body) ||
      typeof body.snapshotToken !== 'string' || !Number.isSafeInteger(body.expectedDraftVersion) ||
      typeof body.expectedDraftDigest !== 'string' || typeof body.outputId !== 'string') return undefined;

  return {
    request,
    requestId,
    origin: url.origin,
    path: url.pathname,
    method: 'POST',
    outputId: body.outputId,
    snapshotToken: body.snapshotToken,
    draftVersion: body.expectedDraftVersion,
    draftDigest: body.expectedDraftDigest,
    startedAt: Date.now(),
    responseStatus: undefined,
    responseReceivedAt: undefined,
    terminalEvent: undefined,
    completedAt: undefined,
    failure: undefined,
  };
};

const matchesExpectedIdentity = (record, expected) => record.outputId === expected.outputId &&
  record.snapshotToken === expected.snapshotToken && record.draftVersion === expected.draftVersion &&
  record.draftDigest === expected.draftDigest;

const validateScope = ({ page, project, explorer, apiOrigin }) => {
  if (!page || typeof page.on !== 'function' || typeof page.off !== 'function' ||
      typeof project !== 'string' || !project || typeof explorer !== 'string' || !explorer ||
      typeof apiOrigin !== 'string' || !apiOrigin) {
    throw new TypeError('Capabilities readiness requires the exact Playwright page, project, Explorer, and UI origin.');
  }
  let origin;
  try { origin = new URL(apiOrigin).origin; } catch {
    throw new TypeError('Capabilities readiness UI origin must be a valid URL.');
  }
  return { page, project, explorer, origin };
};

/** Extract the one output's authoritative saved snapshot/draft identity from a Builder response. */
export const builderCapabilitiesIdentityFromState = (state, { outputId } = {}) => {
  if (!state || typeof state !== 'object' || typeof outputId !== 'string' || !outputId) {
    throw new TypeError('Saved Builder identity requires a response object and exact output ID.');
  }
  const documents = state?.workspace?.documents;
  const matches = Array.isArray(documents) ? documents.filter(document => document?.output?.id === outputId) : [];
  if (matches.length !== 1) {
    throw new Error(`Saved Builder identity must contain exactly one requested output ${outputId}; got ${matches.length}.`);
  }
  const identity = {
    outputId,
    snapshotToken: state?.catalog?.snapshotToken,
    draftVersion: state?.draftVersion,
    draftDigest: state?.draftDigest,
  };
  if (typeof identity.snapshotToken !== 'string' || !identity.snapshotToken ||
      !Number.isSafeInteger(identity.draftVersion) || typeof identity.draftDigest !== 'string' || !identity.draftDigest) {
    throw new Error('Saved Builder identity is missing the exact snapshot, draft version, or draft digest.');
  }
  return identity;
};

/** Observe only the exact post-Apply capabilities request and its Playwright response/terminal events. */
export const watchConstructionCapabilitiesReadiness = (scopeInput) => {
  const scope = validateScope(scopeInput);
  const recordsByRequest = new Map();
  const listeners = new Set();
  let actionStartSequence;
  let eventSequence = 0;

  const notify = () => {
    for (const listener of [...listeners]) listener();
    listeners.clear();
  };
  const onRequest = request => {
    const sequence = ++eventSequence;
    const record = exactRequestScope(request, scope);
    if (!record) return;
    record.sequence = sequence;
    recordsByRequest.set(request, record);
    notify();
  };
  const onResponse = response => {
    const record = recordsByRequest.get(response.request());
    if (!record) return;
    record.responseStatus = response.status();
    record.responseReceivedAt = Date.now();
    notify();
  };
  const onRequestFinished = request => {
    const record = recordsByRequest.get(request);
    if (!record) return;
    record.terminalEvent = 'requestfinished';
    record.completedAt = Date.now();
    notify();
  };
  const onRequestFailed = request => {
    const record = recordsByRequest.get(request);
    if (!record) return;
    record.terminalEvent = 'requestfailed';
    record.completedAt = Date.now();
    try { record.failure = request.failure()?.errorText ?? 'requestfailed'; }
    catch { record.failure = 'requestfailed'; }
    notify();
  };

  scope.page.on('request', onRequest);
  scope.page.on('response', onResponse);
  scope.page.on('requestfinished', onRequestFinished);
  scope.page.on('requestfailed', onRequestFailed);

  return {
    markActionStarted() {
      if (actionStartSequence !== undefined) throw new Error('Capabilities readiness action start may be marked only once.');
      actionStartSequence = eventSequence;
      return Date.now();
    },
    async waitFor(expected, { timeoutMs } = {}) {
      if (actionStartSequence === undefined) throw new Error('Mark the Apply action before waiting for capabilities readiness.');
      if (!expected || typeof expected.outputId !== 'string' || !expected.outputId ||
          typeof expected.snapshotToken !== 'string' || !expected.snapshotToken ||
          !Number.isSafeInteger(expected.draftVersion) || typeof expected.draftDigest !== 'string' || !expected.draftDigest) {
        throw new TypeError('Capabilities readiness requires exact output, snapshot, draft version, and draft digest identity.');
      }
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Error('No Apply-to-settled budget remains for capabilities readiness.');
      const deadline = Date.now() + timeoutMs;

      while (true) {
        const matching = [...recordsByRequest.values()].filter(record =>
          record.sequence > actionStartSequence && matchesExpectedIdentity(record, expected));
        const failed = matching.find(record => record.terminalEvent === 'requestfailed' ||
          (record.responseStatus !== undefined && (record.responseStatus < 200 || record.responseStatus >= 300)));
        if (failed) {
          throw new Error(`Exact post-Apply capabilities request ${failed.requestId} failed: ${failed.failure ?? `HTTP ${failed.responseStatus}`}`);
        }
        if (matching.length > 0 && matching.every(record => record.terminalEvent === 'requestfinished' &&
            record.responseStatus >= 200 && record.responseStatus < 300 &&
            record.responseReceivedAt <= record.completedAt)) {
          return {
            project: scope.project,
            explorer: scope.explorer,
            ...expected,
            requests: matching.map(({ request: _request, sequence: _sequence, ...record }) => ({ ...record })),
          };
        }

        const remainingMs = deadline - Date.now();
        if (remainingMs <= 0) {
          const states = matching.map(record => ({
            requestId: record.requestId,
            responseStatus: record.responseStatus,
            terminalEvent: record.terminalEvent,
            failure: record.failure,
          }));
          throw new Error(`Timed out waiting for exact post-Apply capabilities response and requestfinished within the remaining action budget: ${JSON.stringify(states)}`);
        }
        await new Promise(resolve => {
          let settled = false;
          const finish = () => {
            if (settled) return;
            settled = true;
            clearTimeout(timer);
            listeners.delete(finish);
            resolve();
          };
          const timer = setTimeout(finish, remainingMs);
          listeners.add(finish);
        });
      }
    },
    dispose() {
      scope.page.off('request', onRequest);
      scope.page.off('response', onResponse);
      scope.page.off('requestfinished', onRequestFinished);
      scope.page.off('requestfailed', onRequestFailed);
      notify();
    },
  };
};

/** Read the authoritative saved draft identity used to match a browser capabilities request. */
export const readBuilderCapabilitiesIdentity = async ({
  apiOrigin, project, explorer, outputId, timeoutMs, fetchImpl = globalThis.fetch,
} = {}) => {
  if (typeof apiOrigin !== 'string' || typeof project !== 'string' || typeof explorer !== 'string' ||
      typeof outputId !== 'string' || !outputId || !Number.isFinite(timeoutMs) || timeoutMs <= 0 ||
      typeof fetchImpl !== 'function') {
    throw new TypeError('Reading capabilities identity requires the exact API origin, project, Explorer, output, and remaining timeout.');
  }
  const url = new URL(
    `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/builder`,
    apiOrigin,
  );
  const response = await fetchImpl(url, { signal: AbortSignal.timeout(Math.max(1, Math.floor(timeoutMs))) });
  let state;
  try { state = await response.json(); }
  catch (error) { throw new Error(`Saved Builder identity response was not JSON: ${String(error?.message ?? error)}`); }
  if (!response.ok) throw new Error(`Saved Builder identity returned HTTP ${response.status}.`);
  return builderCapabilitiesIdentityFromState(state, { outputId });
};
