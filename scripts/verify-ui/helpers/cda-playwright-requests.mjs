import assert from 'node:assert/strict';
import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';
import { createPendingResponseReads } from './pending-response-reads.mjs';

const maxBodyLength = 32768;
const nativeRequestTerminalStates = new WeakMap();
const nativeRequestTerminalEvents = new Set(['requestfinished', 'requestfailed']);
const nativeRequestTerminalTimeoutReason = 'No Playwright requestfinished or requestfailed event was observed before the bounded native request drain deadline.';
const parseBody = body => {
  const text = String(body ?? '');
  if (text.length > maxBodyLength) return { truncated: true, length: text.length };
  try {
    return sanitizePayload(JSON.parse(text));
  } catch {
    return sanitizeText(text);
  }
};
const parseRawBody = body => {
  try { return JSON.parse(String(body ?? '')); }
  catch { return String(body ?? ''); }
};
const retiredResponseBodyReadError = /^response\.text: Protocol error \(Network\.getResponseBody\): No data found for resource with given identifier\nResponse body is not available for a response that was navigated away from\. Read response\.body\(\) before triggering any navigation\.$/;

function nativeRequestTerminalState(request) {
  let state = nativeRequestTerminalStates.get(request);
  if (!state) {
    state = { terminalEvent: undefined, terminalObservedAt: undefined, waitPromise: undefined,
      waitOutcome: undefined, waitDeadlineAt: undefined, waitStartedAt: undefined, waitTimeoutMs: undefined,
      resolveWait: undefined, timer: undefined };
    nativeRequestTerminalStates.set(request, state);
  }
  return state;
}

function waitForNativeRequestTerminal(request, startedAt, deadlineAt, timeoutMs) {
  const state = nativeRequestTerminalState(request);
  if (state.waitOutcome) return Promise.resolve(state.waitOutcome);
  if (state.terminalEvent) {
    return Promise.resolve({ status: 'terminal', event: state.terminalEvent, observedAt: state.terminalObservedAt });
  }
  if (state.waitPromise) return state.waitPromise;

  state.waitDeadlineAt = deadlineAt;
  state.waitStartedAt = startedAt;
  state.waitTimeoutMs = timeoutMs;
  state.waitPromise = new Promise(resolve => {
    const settle = outcome => {
      if (state.waitOutcome) return;
      state.waitOutcome = outcome;
      clearTimeout(state.timer);
      state.timer = undefined;
      state.resolveWait = undefined;
      resolve(outcome);
    };
    state.resolveWait = observedAt => {
      if (observedAt <= state.waitDeadlineAt) {
        settle({ status: 'terminal', event: state.terminalEvent, observedAt });
      } else {
        settle({ status: 'timed-out', startedAt: state.waitStartedAt, deadlineAt: state.waitDeadlineAt,
          timeoutMs: state.waitTimeoutMs, terminalEventAfterDeadline: state.terminalEvent,
          terminalObservedAt: observedAt, reason: nativeRequestTerminalTimeoutReason });
      }
    };
    const remainingMs = Math.max(0, state.waitDeadlineAt - Date.now());
    state.timer = setTimeout(() => settle({ status: 'timed-out', startedAt: state.waitStartedAt,
      deadlineAt: state.waitDeadlineAt, timeoutMs: state.waitTimeoutMs,
      reason: nativeRequestTerminalTimeoutReason }), remainingMs);
  });
  return state.waitPromise;
}

function recordNativeRequestTerminal(request, event, observedAt) {
  if (!nativeRequestTerminalEvents.has(event)) return;
  const state = nativeRequestTerminalState(request);
  if (!state.terminalEvent) {
    state.terminalEvent = event;
    state.terminalObservedAt = observedAt;
  }
  state.resolveWait?.(observedAt);
}

export const findCompletedNativeResponse = (entries, responseFor, predicate, fromIndex = 0) =>
  entries.slice(fromIndex).find(entry => {
    if (entry.complete !== true && entry.completedAt === undefined) return false;
    const response = responseFor(entry);
    return response !== undefined && predicate(entry, response);
  });

export async function navigateAfterOwnedConstructionCapabilities(cda, tracker, getNativeRequests, capabilityPath, timeout, navigate) {
  const waitStartedAt = Date.now();
  const deadline = waitStartedAt + timeout;
  const settled = new Set();
  for (;;) {
    const pending = getNativeRequests().filter(entry => entry.path === capabilityPath
      && entry.method === 'POST' && !Number.isFinite(entry.completedAt));
    if (pending.length === 0) break;
    for (const entry of pending) settled.add(entry);
    const remaining = deadline - Date.now();
    assert(remaining > 0, 'The navigation budget expired before owned construction-capabilities requests settled.');
    await Promise.all(pending.map(entry =>
      cda.waitForCapturedResponse(tracker, candidate => candidate === entry, remaining)));
  }
  for (const entry of settled) {
    assert(!entry.failure && Number.isInteger(entry.status) && entry.status >= 200 && entry.status < 300,
      `Owned construction-capabilities request must reach a successful terminal response before navigation: ${JSON.stringify({
        requestId: entry.requestId, browserRequestId: entry.browserRequestId,
        status: entry.status, failure: entry.failure,
      })}`);
  }
  const navigationResult = await navigate();
  return { navigationResult, settledEntries: [...settled] };
}

export const matchesNativeConstructionRemovalProposal = (entry, response, expected) => {
  const request = entry?.request;
  return entry?.method === 'POST' && entry.path?.endsWith('/construction-proposals') &&
    typeof response?.proposalId === 'string' && response.proposalId.length > 0 &&
    request?.outputId === expected.outputId && request.snapshotToken === expected.snapshotToken &&
    request.expectedDraftVersion === expected.draftVersion && request.expectedDraftDigest === expected.draftDigest &&
    Array.isArray(request.removeStepIds) && request.removeStepIds.length === 1 &&
    request.removeStepIds[0] === expected.stepId && response.outputId === expected.outputId &&
    response.snapshotToken === expected.snapshotToken && response.draftVersion === expected.draftVersion &&
    response.draftDigest === expected.draftDigest;
};

export function matchesExpectedEmptyCollectionValidation(entry, expected) {
  const request = entry?.body;
  const selection = request?.selection;
  const expanded = selection?.expanded;
  const response = entry?.response;
  const error = response?.error;
  const diagnostic = error?.diagnostic ?? response?.diagnostics?.find(item => item?.code === expected?.code);
  return entry?.method === 'POST' && entry.path === expected?.path &&
    entry.status === 422 &&
    request?.snapshotToken === expected?.snapshotToken &&
    request.expectedDraftVersion === expected?.expectedDraftVersion &&
    request.expectedDraftDigest === expected?.expectedDraftDigest &&
    request.outputId === expected?.outputId &&
    selection?.kind === 'EXPANDED' && expanded?.rowChoiceId === expected?.rowChoiceId &&
    expanded.emptyCollectionPolicy === 'ERROR' &&
    error?.code === expected?.code && diagnostic?.code === expected?.code &&
    diagnostic?.stage === expected?.stage;
}

export function matchesExpectedEmptyCollectionValidationConsole(consoleError, entry, nativeRequests, expected) {
  if (!matchesExpectedEmptyCollectionValidation(entry, expected)) return false;
  const competingRequests = nativeRequests.filter(candidate => candidate.origin === entry.origin &&
    candidate.path === entry.path && (candidate.status === entry.status || candidate.status === undefined));
  return competingRequests.length === 1 && competingRequests[0] === entry &&
    consoleError?.kind === 'console' && consoleError.location === `${entry.origin}${entry.path}` &&
    consoleError.message === 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
}

export function captureCDARequests(page, { apiOrigin, browserRequestOrigin = apiOrigin, appOrigins = [apiOrigin], ownedPathPrefix, report, currentAction = () => undefined, responsePaths = /commands|selections|explicit-groups|row-definition-proposals|construction-choice-proposals|construction-proposals|construction-capabilities|row-lineage|population-mapping|preview/, shouldReportHttpError = () => true } = {}) {
  if (!apiOrigin || !ownedPathPrefix || !report || !Array.isArray(report.nativeRequests)) {
    throw new TypeError('CDA request capture needs an API origin, owned path prefix, and nativeRequests report array');
  }
  const apiURL = new URL(browserRequestOrigin);
  const appOriginSet = new Set(appOrigins.map(origin => new URL(origin).origin));
  const byRequest = new Map();
  const rawBodies = new WeakMap();
  const responseReads = createPendingResponseReads();
  const { pendingReads } = responseReads;
  const waiters = new Set();
  const terminalDrainEvidenceKeys = new Set();
  let nextBrowserRequestId = 1;
  const findOwnedMatch = (fromIndex, predicate) => report.nativeRequests.slice(fromIndex)
    .find(entry => rawBodies.has(entry) && entry.completedAt && predicate(entry));
  const notify = () => {
    for (const waiter of [...waiters]) {
      const match = findOwnedMatch(waiter.fromIndex, waiter.predicate);
      if (!match) continue;
      waiters.delete(waiter);
      clearTimeout(waiter.timer);
      waiter.resolve(match);
    }
  };
  const owns = rawURL => {
    try {
      const url = new URL(rawURL);
      return url.origin === apiURL.origin && (url.pathname === ownedPathPrefix || url.pathname.startsWith(`${ownedPathPrefix}/`));
    } catch {
      return false;
    }
  };
  const recordNativeEvent = (event, request, entry) => {
    const observedAt = Date.now();
    if (entry) {
      (entry.nativeEventChronology ??= []).push({
        event,
        browserRequestId: entry.browserRequestId,
        observedAt,
        objectMatch: true,
      });
      return observedAt;
    }
    let url;
    try {
      url = new URL(request.url());
    } catch {
      return;
    }
    if (!owns(url.href)) return;
    const diagnostic = {
      kind: 'request-capture-correlation',
      event,
      browserRequestId: null,
      method: sanitizeText(request.method()),
      path: sanitizeText(url.pathname),
      observedAt,
      objectMatch: false,
      message: `Playwright ${event} request object did not match an exact captured request object`,
    };
    (report.errors ??= []).push(diagnostic);
    return observedAt;
  };

  page.on('request', request => {
    if (!owns(request.url())) return;
    const url = new URL(request.url());
    const headers = request.headers();
    let body;
    if (request.postData() !== null) body = parseBody(request.postData());
    const browserRequestId = `playwright-${nextBrowserRequestId++}`;
    const entry = {
      requestId: headers['x-request-id'] ?? browserRequestId,
      browserRequestId,
      path: url.pathname,
      method: request.method(),
      origin: url.origin,
      query: parseBody(JSON.stringify(Object.fromEntries(url.searchParams.entries()))),
      authorizationHeaderPresent: Object.keys(headers).some(header => header.toLowerCase() === 'authorization'),
      startedAt: Date.now(),
      ...(currentAction() ? { triggerAction: sanitizeText(currentAction()) } : {}),
      ...(body !== undefined ? { body } : {}),
    };
    byRequest.set(request, entry);
    nativeRequestTerminalState(request);
    rawBodies.set(entry, { request: request.postData() === null ? undefined : parseRawBody(request.postData()) });
    report.nativeRequests.push(entry);
    recordNativeEvent('request', request, entry);
  });

  page.on('response', response => {
    const request = response.request();
    const entry = byRequest.get(request);
    if (!entry) {
      recordNativeEvent('response', request);
      return;
    }
    recordNativeEvent('response', request, entry);
    const headers = response.headers();
    entry.status = response.status();
    entry.responseReceivedAt = Date.now();
    entry.serverRequestId = headers['x-request-id'];
    const readResponse = responsePaths.test(entry.path) || /related-expand-choices/.test(entry.path);
    const httpFailure = response.status() >= 400;
    const expectedHttpFailure = httpFailure && shouldReportHttpError(entry.path, response.status(), entry) === false;
    if (expectedHttpFailure) entry.expectedHttpFailure = true;
    const read = Promise.resolve().then(async () => {
      if (readResponse) {
        const body = await response.text();
        rawBodies.get(entry).response = parseRawBody(body);
        entry.response = parseBody(body);
      } else entry.response = { bodyNotRead: true };
    }).catch(error => {
      entry.responseReadError = sanitizeText(error?.message ?? error);
      throw error;
    }).finally(() => {
      if (!httpFailure) entry.completedAt = Date.now();
    });
    const trackedRead = responseReads.track(read, {
      phase: 'response-body', browserRequestId: entry.browserRequestId, requestId: entry.requestId,
      method: entry.method, path: entry.path, status: entry.status,
    });
    if (httpFailure) {
      const errorEntry = { kind: 'http', origin: entry.origin, path: entry.path, url: `${entry.origin}${entry.path}`, status: response.status(), requestId: entry.requestId, browserRequestId: entry.browserRequestId, method: entry.method, startedAt: entry.startedAt, request: entry.body };
      const pendingDiagnostic = trackedRead.then(() => {
        if (entry.response !== undefined) errorEntry.response = entry.response;
        if (!expectedHttpFailure) report.errors.push(errorEntry);
        entry.completedAt = Date.now();
      }, error => {
        errorEntry.responseReadError = entry.responseReadError ?? sanitizeText(error?.message ?? error);
        if (!expectedHttpFailure) report.errors.push(errorEntry);
        entry.completedAt = Date.now();
      }).finally(() => {
        notify();
      });
      responseReads.track(pendingDiagnostic, {
        phase: 'http-diagnostic', browserRequestId: entry.browserRequestId, requestId: entry.requestId,
        method: entry.method, path: entry.path, status: entry.status,
      });
    } else void trackedRead.then(notify, notify);
  });

  page.on('requestfinished', request => {
    const entry = byRequest.get(request);
    if (!entry) {
      recordNativeEvent('requestfinished', request);
      return;
    }
    const observedAt = recordNativeEvent('requestfinished', request, entry);
    recordNativeRequestTerminal(request, 'requestfinished', observedAt);
  });

  page.on('requestfailed', request => {
    const entry = byRequest.get(request);
    if (!entry) {
      recordNativeEvent('requestfailed', request);
      return;
    }
    const observedAt = recordNativeEvent('requestfailed', request, entry);
    recordNativeRequestTerminal(request, 'requestfailed', observedAt);
    entry.completedAt = Date.now();
    entry.failure = sanitizeText(request.failure()?.errorText);
    report.errors.push({ kind: 'network', origin: entry.origin, path: entry.path, url: `${entry.origin}${entry.path}`, requestId: entry.requestId, browserRequestId: entry.browserRequestId, method: entry.method, startedAt: entry.startedAt, error: entry.failure,
      ...(entry.expected === true ? { expected: true } : {}),
      ...(entry.expectedCancellation ? { expectedCancellation: entry.expectedCancellation } : {}),
    });
    notify();
  });
  page.on('pageerror', error => report.errors.push({ kind: 'runtime', message: sanitizeText(error.message) }));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location && !appOriginSet.has(new URL(location).origin)) return;
    const resourceFailureStatus = /^Failed to load resource: the server responded with a status of (\d{3}) \([^)]+\)$/.exec(message.text())?.[1];
    if (location && resourceFailureStatus) {
      const url = new URL(location);
      const matchingRequests = report.nativeRequests.filter(entry => entry.origin === url.origin && entry.path === url.pathname && entry.status === Number(resourceFailureStatus));
      const expectedHttpFailureMatches = entry => {
        const evidence = entry.expectedHttpFailure;
        if (evidence === true) return true;
        return evidence !== null && typeof evidence === 'object' &&
          evidence.browserRequestId === entry.browserRequestId &&
          evidence.requestId === entry.requestId &&
          evidence.method === entry.method &&
          evidence.path === entry.path &&
          evidence.status === entry.status &&
          typeof evidence.reason === 'string' && evidence.reason.trim().length > 0 &&
          evidence.proof !== null && typeof evidence.proof === 'object';
      };
      if (matchingRequests.length === 1 && expectedHttpFailureMatches(matchingRequests[0]) && !matchingRequests[0].expectedHttpConsoleConsumed) {
        matchingRequests[0].expectedHttpConsoleConsumed = true;
        return;
      }
    }
    if (location && new URL(location).pathname === '/favicon.ico' && /404 \(Not Found\)/.test(message.text())) {
      (report.assetFailures ??= []).push({ kind: 'console', url: location, status: 404 });
      return;
    }
    report.errors.push({ kind: 'console', message: sanitizeText(message.text()), ...(location ? { location: sanitizeText(location) } : {}) });
  });

  return {
    byRequest,
    pendingReads,
    rawRequestBody: entry => rawBodies.get(entry)?.request,
    rawResponseBody: entry => rawBodies.get(entry)?.response,
    waitFor(predicate, { fromIndex = 0, timeoutMs, timeout } = {}) {
      const deadlineMs = timeoutMs ?? timeout ?? 5000;
      const match = findOwnedMatch(fromIndex, predicate);
      if (match) return Promise.resolve(match);
      return new Promise((resolve, reject) => {
        const waiter = { predicate, fromIndex, resolve, timer: undefined };
        waiter.timer = setTimeout(() => {
          waiters.delete(waiter);
          clearTimeout(waiter.timer);
          const observed = report.nativeRequests.slice(fromIndex).filter(entry => rawBodies.has(entry))
            .map(({ origin, path, method, status, completedAt, failure }) => ({ origin, path, method, status, completed: Boolean(completedAt), failure }));
          reject(new Error(`Timed out waiting for owned CDA request: ${JSON.stringify(observed)}`));
        }, deadlineMs);
        waiters.add(waiter);
      });
    },
    async flush({ timeoutMs = 5_000, waitForNativeRequestTerminals = false } = {}) {
      if (waitForNativeRequestTerminals) {
        if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
          throw new RangeError('Native request terminal-event flush timeout must be a finite positive number of milliseconds.');
        }
        const pendingNativeRequests = [...byRequest.entries()].filter(([, entry]) =>
          !(entry.nativeEventChronology ?? []).some(({ event }) => nativeRequestTerminalEvents.has(event)));
        if (pendingNativeRequests.length) {
          const startedAt = Date.now();
          const deadlineAt = startedAt + timeoutMs;
          const outcomes = await Promise.all(pendingNativeRequests.map(([request]) =>
            waitForNativeRequestTerminal(request, startedAt, deadlineAt, timeoutMs)));
          const timedOutByDeadline = new Map();
          for (let index = 0; index < outcomes.length; index += 1) {
            const outcome = outcomes[index];
            if (outcome.status !== 'timed-out') continue;
            const deadline = outcome.deadlineAt;
            const entries = timedOutByDeadline.get(deadline) ?? [];
            entries.push({ entry: pendingNativeRequests[index][1], outcome });
            timedOutByDeadline.set(deadline, entries);
          }
          for (const [deadline, unresolved] of timedOutByDeadline) {
            const evidence = {
              status: 'timed-out',
              startedAt: unresolved[0].outcome.startedAt,
              deadlineAt: deadline,
              timeoutMs: unresolved[0].outcome.timeoutMs,
              reason: nativeRequestTerminalTimeoutReason,
              unresolvedRequests: unresolved.map(({ entry, outcome }) => ({
                index: report.nativeRequests.indexOf(entry),
                requestId: entry.requestId ?? null,
                browserRequestId: entry.browserRequestId ?? null,
                method: entry.method ?? null,
                path: entry.path ?? null,
                ...(outcome.terminalEventAfterDeadline ? {
                  terminalEventAfterDeadline: outcome.terminalEventAfterDeadline,
                  terminalObservedAt: outcome.terminalObservedAt,
                } : {}),
              })),
            };
            const evidenceKey = JSON.stringify(evidence);
            const keys = terminalDrainEvidenceKeys;
            if (keys.has(evidenceKey)) continue;
            keys.add(evidenceKey);
            (report.nativeRequestDrainEvidence ??= []).push(evidence);
          }
        }
      }
      await responseReads.flush({
        timeoutMs,
        label: 'owned CDA response reads',
        filter: details => {
          if (details.phase !== 'response-body' || details.status !== 200) return true;
          const matching = report.nativeRequests.filter(entry => entry.browserRequestId === details.browserRequestId);
          if (matching.length !== 1) return true;
          const entry = matching[0];
          const cancellation = entry.expectedCancellation;
          const exactRetirement = entry.expected === true
            && entry.canceled === true
            && entry.failure === 'net::ERR_ABORTED'
            && cancellation?.browserRequestId === entry.browserRequestId
            && cancellation.requestId === entry.requestId
            && cancellation.method === entry.method
            && cancellation.url === `${entry.origin}${entry.path}`
            && typeof cancellation.reason === 'string' && cancellation.reason.trim().length > 0
            && cancellation.proof !== null && typeof cancellation.proof === 'object'
            && !Array.isArray(cancellation.proof)
            && details.requestId === entry.requestId
            && details.method === entry.method
            && details.path === entry.path
            && details.status === entry.status
            && retiredResponseBodyReadError.test(entry.responseReadError ?? '');
          return !exactRetirement;
        },
      });
      return report.nativeRequests;
    },
  };
}
