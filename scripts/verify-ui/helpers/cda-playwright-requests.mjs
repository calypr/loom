import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';
import { createPendingResponseReads } from './pending-response-reads.mjs';

const maxBodyLength = 32768;
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

export const findCompletedNativeResponse = (entries, responseFor, predicate, fromIndex = 0) =>
  entries.slice(fromIndex).find(entry => {
    if (entry.complete !== true && entry.completedAt === undefined) return false;
    const response = responseFor(entry);
    return response !== undefined && predicate(entry, response);
  });

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
    rawBodies.set(entry, { request: request.postData() === null ? undefined : parseRawBody(request.postData()) });
    report.nativeRequests.push(entry);
  });

  page.on('response', response => {
    const entry = byRequest.get(response.request());
    if (!entry) return;
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

  page.on('requestfailed', request => {
    const entry = byRequest.get(request);
    if (!entry) return;
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
    async flush({ timeoutMs = 5_000 } = {}) {
      await responseReads.flush({ timeoutMs, label: 'owned CDA response reads' });
      return report.nativeRequests;
    },
  };
}
