import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';

const MAX_BODY_BYTES = 64 * 1024;
const MAX_BODY_CAPTURE_MS = 10_000;
const MAX_DIAGNOSTIC_ITEMS = 500;
export const READ_ONLY_POST_PATH_SUFFIXES = Object.freeze([
  '/authoring/v2/schema-fields',
  '/authoring/v2/suggestions',
  '/authoring/v2/semantic-inventory',
  '/authoring/v2/frame-source-options',
  '/authoring/v2/configured-column-context',
  '/authoring/v2/construction-choices',
  '/authoring/v2/related-expand-choices',
  '/authoring/v2/related-field-choices',
  '/authoring/v2/related-expand-contributors',
  '/authoring/v2/construction-inputs',
  '/authoring/v2/construction-category-discoveries',
  '/authoring/v2/table-shape-capabilities',
  '/authoring/v2/table-shape-category-discoveries',
  '/authoring/v2/table-shape-resolutions',
  '/authoring/v2/population-routes',
  '/authoring/v2/column-source',
  '/authoring/v2/row-change',
  '/authoring/v2/row-lineage',
  '/authoring/v2/interpretation-preview',
  '/authoring/v2/population-mapping',
  '/authoring/v2/preview',
  '/authoring/v2/reconcile',
  '/authoring/v2/construction-capabilities',
  '/authoring/v2/construction-proposals',
  '/authoring/v2/construction-choice-proposals',
  '/authoring/v2/row-definition-proposals',
  '/authoring/v2/row-definition-choices',
  '/authoring/v2/table-shape-proposals',
  '/authoring/v2/cell-trace',
]);

function pathnameOf(value) {
  try {
    return new URL(String(value), 'http://observer.invalid').pathname;
  } catch {
    return '';
  }
}

function isUnambiguousGraphQLQuery(pathname, requestBody) {
  if (pathname !== '/graphql/graph' || !requestBody || typeof requestBody !== 'object' || Array.isArray(requestBody)) return false;
  const document = requestBody.query;
  if (typeof document !== 'string') return false;
  const operation = document.match(/^\uFEFF?\s*query(?:\s+([_A-Za-z][_0-9A-Za-z]*))?(?=\s|\(|\{|@)/);
  if (!operation) return false;
  const operationKinds = document.match(/\b(?:query|mutation|subscription)\b/g) ?? [];
  if (operationKinds.length !== 1 || operationKinds[0] !== 'query') return false;
  return requestBody.operationName == null || requestBody.operationName === operation[1];
}

/** Classify writes conservatively; only explicit single-operation GraphQL queries are read-only. */
export function isMutationRequest(method, pathOrURL, requestBody) {
  const normalizedMethod = String(method ?? '').toUpperCase();
  if (['GET', 'HEAD', 'OPTIONS'].includes(normalizedMethod)) return false;
  if (normalizedMethod === 'POST') {
    const pathname = pathnameOf(pathOrURL);
    if (READ_ONLY_POST_PATH_SUFFIXES.some(suffix => pathname.endsWith(suffix))) return false;
    if (isUnambiguousGraphQLQuery(pathname, requestBody)) return false;
  }
  return true;
}

function parseBodyValue(rawValue) {
  if (rawValue === undefined || rawValue === null) return undefined;
  const text = String(rawValue);
  try {
    return sanitizePayload(JSON.parse(text));
  } catch {
    return sanitizeText(text);
  }
}

function requestBodyValues(request) {
  const raw = request.postData();
  return {
    byteLength: Buffer.byteLength(raw ?? '', 'utf8'),
    privateValue: parseBodyValue(raw),
  };
}

function reportBodyValue(value, rawByteLength) {
  if (value === undefined) return undefined;
  if (rawByteLength > MAX_BODY_BYTES) return { truncated: true, byteLength: rawByteLength };
  return value;
}

function httpBodyDetails(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const details = {};
  if (Object.hasOwn(value, 'code')) details.code = sanitizePayload(value.code);
  if (typeof value.message === 'string') details.message = sanitizeText(value.message).slice(0, 2_000);
  return Object.keys(details).length ? details : undefined;
}

function responsePathMatches(responsePaths, pathname) {
  if (typeof responsePaths === 'function') return Boolean(responsePaths(pathname));
  if (responsePaths instanceof RegExp) {
    responsePaths.lastIndex = 0;
    return responsePaths.test(pathname);
  }
  const paths = Array.isArray(responsePaths) ? responsePaths : responsePaths == null ? [] : [responsePaths];
  return paths.some(candidate => {
    if (candidate instanceof RegExp) {
      candidate.lastIndex = 0;
      return candidate.test(pathname);
    }
    if (typeof candidate === 'function') return Boolean(candidate(pathname));
    return typeof candidate === 'string' && candidate === pathname;
  });
}

function normalizeOrigins(appOrigins) {
  if (!Array.isArray(appOrigins)) throw new TypeError('appOrigins must be an array of app origin URLs.');
  return new Set(appOrigins.map(value => {
    try { return new URL(value).origin; }
    catch { throw new TypeError(`appOrigins contains an invalid URL: ${String(value)}`); }
  }));
}

function requestLocation(request) {
  try {
    const url = new URL(request.url());
    return { origin: url.origin, path: url.pathname, url: `${url.origin}${url.pathname}` };
  } catch {
    return { origin: '', path: '', url: sanitizeText(request.url()).slice(0, 1_000) };
  }
}

function isExactAbortFailure(errorText) {
  return ['net::ERR_ABORTED', 'ERR_ABORTED', 'ABORT_ERR'].includes(String(errorText ?? '').trim());
}

function makeReportError(report, diagnostic) {
  if (!report) return undefined;
  report.errors ??= [];
  report.errors.push(diagnostic);
  return diagnostic;
}

/** Observe only requests accepted by the caller's exact request-scope predicate. */
export function createPlaywrightNetworkObserver(page, {
  scopeRequest,
  appOrigins,
  responsePaths = [],
  report,
} = {}) {
  if (!page || typeof page.on !== 'function' || typeof page.off !== 'function') {
    throw new TypeError('A Playwright Page is required.');
  }
  if (typeof scopeRequest !== 'function') throw new TypeError('scopeRequest must be a request predicate.');
  if (report !== undefined && (!report || typeof report !== 'object')) {
    throw new TypeError('report must be an object when provided.');
  }

  const origins = normalizeOrigins(appOrigins ?? []);
  let nextBrowserRequestId = 1;
  const requests = [];
  const failures = [];
  const diagnostics = {
    console: [],
    pageErrors: [],
    networkFailures: [],
    httpFailures: [],
    assetFailures: [],
    overflow: { console: 0, pageErrors: 0, networkFailures: 0, httpFailures: 0, assetFailures: 0 },
  };
  const addDiagnostic = (channel, item) => {
    const items = diagnostics[channel];
    if (items.length < MAX_DIAGNOSTIC_ITEMS) items.push(item);
    else diagnostics.overflow[channel] += 1;
  };
  const entriesByRequest = new WeakMap();
  const rawUrlsByEntry = new WeakMap();
  const expectedTransportFailures = new WeakMap();
  const requestBodySizesByEntry = new WeakMap();
  const responseBodySizesByEntry = new WeakMap();
  const reportEntriesByEntry = new WeakMap();
  const httpFailuresByEntry = new WeakMap();
  const bodyTimers = new Set();
  const pendingResourceEchoes = new Map();
  const waiters = new Set();
  let stopped = false;
  if (report) {
    report.nativeRequests ??= [];
    report.network ??= [];
    report.errors ??= [];
    report.assetFailures ??= [];
  }

  const syncReportEntry = entry => {
    const projection = reportEntriesByEntry.get(entry);
    if (!projection) return;
    for (const key of [
      'index', 'requestId', 'browserRequestId', 'method', 'origin', 'path', 'url', 'resourceType', 'status',
      'startedAt', 'responseReceivedAt', 'completedAt', 'isMutation', 'readCancellationDiagnostic', 'responseCapture',
      'responseCaptureComplete', 'responseBodyCaptured', 'networkComplete', 'responseCaptureError', 'httpFailureExempted',
    ]) {
      if (entry[key] === undefined) delete projection[key];
      else projection[key] = entry[key];
    }
    projection.request = reportBodyValue(entry.request, requestBodySizesByEntry.get(entry) ?? 0);
    projection.response = reportBodyValue(entry.response, responseBodySizesByEntry.get(entry) ?? 0);
    if (entry.failure === undefined) delete projection.failure;
    else projection.failure = sanitizePayload(entry.failure);
    if (entry.expectedValidation === undefined) delete projection.expectedValidation;
    else projection.expectedValidation = sanitizePayload(entry.expectedValidation);
    projection.expectedValidationMarked = entry.expectedValidation !== undefined;
    if (entry.expectedValidation === undefined) delete projection.expectedValidationStatus;
    else projection.expectedValidationStatus = entry.expectedValidation.status;
    if (entry.expectedTransportFailure === undefined) delete projection.expectedTransportFailure;
    else projection.expectedTransportFailure = sanitizePayload(entry.expectedTransportFailure);
  };

  const addReportEntry = entry => {
    if (!report) return;
    const projection = {};
    reportEntriesByEntry.set(entry, projection);
    report.nativeRequests.push(projection);
    syncReportEntry(entry);
  };

  const recordFailure = (entry, kind, detail, message) => {
    const failure = { kind, entry, ...detail };
    failures.push(failure);
    entry.failure = { kind, ...detail };
    let networkEntry;
    if (report) {
      report.network ??= [];
      networkEntry = {
        kind: 'network', url: entry.url, method: entry.method,
        requestId: entry.requestId, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
        ...(entry.status === undefined ? {} : { status: entry.status }),
        ...(detail.errorText ? { errorText: detail.errorText } : {}),
        observerRequestIndex: entry.index,
      };
      report.network.push(networkEntry);
    }
    entry.reportNetwork = networkEntry;
    entry.reportError = makeReportError(report, {
      kind: 'network',
      url: entry.url,
      method: entry.method,
      requestId: entry.requestId,
      browserRequestId: entry.browserRequestId,
      startedAt: entry.startedAt,
      ...(entry.status === undefined ? {} : { status: entry.status }),
      message,
      ...(detail.errorText ? { failure: detail.errorText } : {}),
    });
    syncReportEntry(entry);
    return failure;
  };

  const recordConsoleFailure = echo => {
    const detail = {
      kind: 'console',
      message: echo.text,
      url: echo.location,
    };
    failures.push(detail);
    addDiagnostic('console', { text: detail.message, location: detail.url });
    detail.reportError = makeReportError(report, { kind: 'console', message: detail.message, location: detail.url });
  };

  const settleResourceEchoes = (href, { failed }) => {
    const echoes = pendingResourceEchoes.get(href);
    if (!echoes) return;
    pendingResourceEchoes.delete(href);
    if (!failed) echoes.forEach(recordConsoleFailure);
  };

  const recordHttpFailure = entry => {
    entry.failure = { kind: 'http', status: entry.status };
    const failure = { kind: 'http', entry, status: entry.status };
    failures.push(failure);
    httpFailuresByEntry.set(entry, failure);
    const diagnostic = {
      url: entry.url,
      method: entry.method,
      status: entry.status,
      requestId: entry.requestId,
      browserRequestId: entry.browserRequestId,
      startedAt: entry.startedAt,
      body: { captureState: entry.responseCapture === 'pending' ? 'pending' : 'not-configured' },
      observerRequestIndex: entry.index,
    };
    addDiagnostic('httpFailures', diagnostic);
    entry.httpDiagnostic = diagnostic;
    if (report) {
      report.network ??= [];
      const networkEntry = { kind: 'http', url: entry.url, method: entry.method, status: entry.status,
        requestId: entry.requestId, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
        observerRequestIndex: entry.index };
      report.network.push(networkEntry);
      entry.reportNetwork = networkEntry;
    }
    entry.reportError = makeReportError(report, {
      kind: 'http', url: entry.url, method: entry.method, status: entry.status,
      requestId: entry.requestId, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
      message: `HTTP ${entry.status}`,
    });
    syncReportEntry(entry);
  };

  const isReady = entry => entry.networkComplete && entry.responseCaptureComplete;
  const captureResponseBody = (entry, response, { required = true } = {}) => {
    const bodyRead = Promise.resolve().then(() => response.body()).then(bytes => {
      const body = Buffer.from(bytes);
      const byteLength = body.byteLength;
      if (required) return { byteLength, privateValue: parseBodyValue(body.toString('utf8')) };
      const diagnosticValue = byteLength > MAX_BODY_BYTES
        ? { truncated: true, byteLength }
        : parseBodyValue(body.toString('utf8'));
      return { byteLength, diagnosticValue };
    }, error => ({ error: sanitizeText(error?.message ?? error).slice(0, 1_000) }));
    let timer;
    const capture = Promise.race([
      bodyRead,
      new Promise(resolve => {
        timer = setTimeout(() => resolve({ timedOut: true }), MAX_BODY_CAPTURE_MS);
        bodyTimers.add(timer);
      }),
    ]).then(outcome => {
      clearTimeout(timer);
      bodyTimers.delete(timer);
      if (outcome.timedOut) {
        if (required) {
          entry.responseCapture = 'timed-out';
          entry.responseCaptureError = `Response body capture exceeded ${MAX_BODY_CAPTURE_MS}ms.`;
        }
      } else if (outcome.error !== undefined) {
        if (required) {
          entry.responseCapture = 'failed';
          entry.responseCaptureError = outcome.error;
        }
      } else {
        if (required) {
          entry.response = outcome.privateValue;
          responseBodySizesByEntry.set(entry, outcome.byteLength);
          entry.responseBodyCaptured = true;
          entry.responseCapture = 'captured';
        }
      }
      if (entry.httpDiagnostic) {
        const completed = !outcome.timedOut && outcome.error === undefined;
        const captureState = outcome.timedOut ? 'timed-out' : outcome.error ? 'failed' : 'completed';
        const diagnosticValue = required
          ? reportBodyValue(outcome.privateValue, outcome.byteLength)
          : outcome.diagnosticValue;
        const details = httpBodyDetails(required ? outcome.privateValue : outcome.diagnosticValue);
        entry.httpDiagnostic.body = completed
          ? { captureState, body: diagnosticValue, ...(details ?? {}) }
          : { captureState, error: outcome.error ?? `Response body capture exceeded ${MAX_BODY_CAPTURE_MS}ms.` };
        if (entry.reportError) {
          entry.reportError.body = completed
            ? { diagnosticBody: diagnosticValue, ...(details ?? {}) }
            : { captureState, error: outcome.error ?? `Response body capture exceeded ${MAX_BODY_CAPTURE_MS}ms.` };
        }
      }
      if (required) {
        entry.responseCaptureComplete = true;
        entry.completedAt = Date.now();
      }
      syncReportEntry(entry);
      checkWaiters();
      return entry.response;
    });
    return capture;
  };

  const checkWaiters = () => {
    for (const waiter of [...waiters]) {
      for (const entry of requests.slice(waiter.fromIndex)) {
        let matches;
        try { matches = Boolean(waiter.predicate(entry)); }
        catch (error) {
          waiters.delete(waiter);
          clearTimeout(waiter.timer);
          waiter.reject(error);
          break;
        }
        if (!matches || !isReady(entry)) continue;
        waiters.delete(waiter);
        clearTimeout(waiter.timer);
        waiter.resolve(entry);
        break;
      }
    }
  };

  const onRequest = request => {
    let inScope = false;
    try { inScope = Boolean(scopeRequest(request)); }
    catch (error) {
      const failure = { kind: 'observer-scope', message: sanitizeText(error?.message ?? error).slice(0, 1_000) };
      failures.push(failure);
      makeReportError(report, failure);
      return;
    }
    if (!inScope) return;

    const location = requestLocation(request);
    const method = request.method().toUpperCase();
    const browserRequestId = `playwright-${nextBrowserRequestId++}`;
    let headers = {};
    try { headers = request.headers(); } catch {}
    const requestIdHeader = headers['x-request-id'];
    const requestId = typeof requestIdHeader === 'string' && requestIdHeader.trim()
      ? sanitizeText(requestIdHeader).slice(0, 500)
      : undefined;
    const bodyValues = requestBodyValues(request);
    const entry = {
      index: requests.length,
      ...(requestId === undefined ? {} : { requestId }),
      browserRequestId,
      method,
      ...location,
      resourceType: request.resourceType(),
      request: bodyValues.privateValue,
      response: undefined,
      status: undefined,
      failure: undefined,
      startedAt: Date.now(),
      completedAt: undefined,
      isMutation: isMutationRequest(method, location.path, bodyValues.privateValue),
      readCancellationDiagnostic: false,
      responseCapture: 'not-configured',
      networkComplete: false,
      responseCaptureComplete: true,
    };
    requests.push(entry);
    requestBodySizesByEntry.set(entry, bodyValues.byteLength);
    entriesByRequest.set(request, entry);
    rawUrlsByEntry.set(entry, request.url());
    addReportEntry(entry);
  };

  const onResponse = response => {
    const request = response.request();
    const entry = entriesByRequest.get(request);
    if (!entry) return;

    entry.responseReceivedAt = Date.now();
    entry.status = response.status();
    const shouldCapture = responsePathMatches(responsePaths, entry.path);
    const captureHttpDiagnostic = entry.status >= 400 && !shouldCapture;
    entry.responseCapture = shouldCapture ? 'pending' : 'not-configured';
    entry.responseCaptureComplete = !shouldCapture;
    entry.networkComplete = true;
    entry.completedAt = shouldCapture ? undefined : Date.now();
    syncReportEntry(entry);

    if (entry.status >= 400) {
      recordHttpFailure(entry);
      settleResourceEchoes(rawUrlsByEntry.get(entry), { failed: true });
    }

    if (!shouldCapture) {
      checkWaiters();
      if (captureHttpDiagnostic) void captureResponseBody(entry, response, { required: false });
      return;
    }
    void captureResponseBody(entry, response);
  };

  const onRequestFailed = request => {
    const entry = entriesByRequest.get(request);
    if (!entry) return;
    const errorText = String(request.failure()?.errorText ?? 'request failed');
    const canceled = isExactAbortFailure(errorText);
    entry.failure = {
      kind: 'transport',
      errorText: sanitizeText(errorText).slice(0, 1_000),
      canceled,
    };
    entry.completedAt = Date.now();
    entry.networkComplete = true;
    entry.responseCaptureComplete = true;
    const expectedTransportFailure = expectedTransportFailures.get(request);
    const networkDiagnostic = {
      url: entry.url,
      method: entry.method,
      requestId: entry.requestId,
      browserRequestId: entry.browserRequestId,
      startedAt: entry.startedAt,
      errorText: entry.failure.errorText,
      canceled,
      observerRequestIndex: entry.index,
    };
    addDiagnostic('networkFailures', networkDiagnostic);
    settleResourceEchoes(rawUrlsByEntry.get(entry), { failed: true });
    if (expectedTransportFailure && !entry.isMutation) {
      entry.expectedTransportFailure = expectedTransportFailure;
      networkDiagnostic.expected = true;
      networkDiagnostic.expectedTransportFailure = expectedTransportFailure;
      if (report) {
        const networkEntry = {
          kind: 'network', url: entry.url, method: entry.method, errorText: entry.failure.errorText,
          requestId: entry.requestId, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
          expected: true, expectedTransportFailure, observerRequestIndex: entry.index,
        };
        report.network.push(networkEntry);
        entry.reportNetwork = networkEntry;
      }
    } else if (canceled && !entry.isMutation) {
      entry.readCancellationDiagnostic = true;
      networkDiagnostic.readCancellationDiagnostic = true;
      if (report) {
        const networkEntry = {
          kind: 'network', url: entry.url, method: entry.method, errorText: entry.failure.errorText,
          requestId: entry.requestId, browserRequestId: entry.browserRequestId, startedAt: entry.startedAt,
          canceled: true, readCancellationDiagnostic: true, observerRequestIndex: entry.index,
        };
        report.network.push(networkEntry);
        entry.reportNetwork = networkEntry;
      }
    } else {
      recordFailure(entry, 'transport', { errorText: entry.failure.errorText }, `Request failed: ${entry.failure.errorText}`);
    }
    syncReportEntry(entry);
    checkWaiters();
  };

  const onPageError = error => {
    const detail = {
      kind: 'pageerror',
      message: sanitizeText(error?.message ?? error).slice(0, 2_000),
      stack: sanitizeText(error?.stack ?? '').slice(0, 4_000),
      url: requestLocation({ url: () => page.url() }).url,
    };
    failures.push(detail);
    addDiagnostic('pageErrors', { message: detail.message, stack: detail.stack, url: detail.url });
    detail.reportError = makeReportError(report, { kind: 'runtime', message: detail.message, stack: detail.stack });
  };

  const onConsole = message => {
    if (message.type() !== 'error') return;
    const location = message.location?.() ?? {};
    let origin = '';
    let locationURL;
    try {
      locationURL = new URL(location.url || page.url());
      origin = locationURL.origin;
    } catch {}
    if (!origins.has(origin)) return;
    const text = sanitizeText(message.text()).slice(0, 2_000);
    const genericResourceFailure = /^Failed to load resource(?::\s*(?:the server responded with a status of \d{3}(?:\s*\([^)]+\))?|net::ERR_[A-Z_]+))?\.?$/.test(text);
    if (genericResourceFailure && locationURL) {
      const genericStatus = /status of (\d{3})/.exec(text);
      if (locationURL.pathname === '/favicon.ico' && Number(genericStatus?.[1]) === 404) {
        const assetFailure = { url: `${locationURL.origin}${locationURL.pathname}`, status: 404,
          kind: 'asset-failure' };
        addDiagnostic('assetFailures', assetFailure);
        report?.assetFailures.push({ ...assetFailure });
        return;
      }
      const failedRequest = requests.find(entry => {
        if (!(entry.status >= 400 || entry.failure)) return false;
        try { return new URL(rawUrlsByEntry.get(entry)).href === locationURL.href; }
        catch { return false; }
      });
      if (failedRequest) {
        return;
      }
      const pending = pendingResourceEchoes.get(locationURL.href) ?? [];
      pending.push({ text, location: `${locationURL.origin}${locationURL.pathname}` });
      pendingResourceEchoes.set(locationURL.href, pending);
      return;
    }
    recordConsoleFailure({ text, location: requestLocation({ url: () => location.url || page.url() }).url });
  };

  const onRequestFinished = request => {
    const entry = entriesByRequest.get(request);
    if (!entry) return;
    entry.networkComplete = true;
    entry.completedAt ??= Date.now();
    if (!(entry.status >= 400 || entry.failure)) {
      settleResourceEchoes(rawUrlsByEntry.get(entry), { failed: false });
    }
    syncReportEntry(entry);
    checkWaiters();
  };

  page.on('request', onRequest);
  page.on('response', onResponse);
  page.on('requestfailed', onRequestFailed);
  page.on('requestfinished', onRequestFinished);
  page.on('pageerror', onPageError);
  page.on('console', onConsole);

  return {
    requests,
    failures,
    diagnostics,
    byRequest: entriesByRequest,
    waitForResponse(predicate, { fromIndex = 0, timeoutMs = 5_000 } = {}) {
      if (stopped) return Promise.reject(new Error('Network observer has stopped.'));
      if (typeof predicate !== 'function') return Promise.reject(new TypeError('waitForResponse requires a predicate.'));
      if (!Number.isInteger(fromIndex) || fromIndex < 0) return Promise.reject(new RangeError('fromIndex must be a non-negative integer.'));
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) return Promise.reject(new RangeError('timeoutMs must be finite and positive.'));
      return new Promise((resolve, reject) => {
        const waiter = { predicate, fromIndex, resolve, reject, timer: undefined };
        waiter.timer = setTimeout(() => {
          waiters.delete(waiter);
          reject(new Error(`Timed out waiting for the scoped response after ${timeoutMs}ms.`));
        }, timeoutMs);
        waiters.add(waiter);
        checkWaiters();
      });
    },
    requestBody(entry) {
      return entry?.request;
    },
    responseBody(entry) {
      return entry?.response;
    },
    markExpectedValidation(entry, { status, reason, proof } = {}) {
      if (!requests.includes(entry)) throw new TypeError('Expected validation must reference an observed scoped request.');
      if (!entry.networkComplete || !entry.responseCaptureComplete || !entry.responseBodyCaptured ||
        ![400, 422].includes(status) || entry.status !== status) {
        throw new RangeError('Only a captured HTTP 400 or 422 response with the same status can be exempted.');
      }
      if (typeof reason !== 'string' || !reason.trim() || proof === undefined || proof === null || proof === '') {
        throw new TypeError('Expected validation requires a non-empty reason and proof.');
      }
      entry.expectedValidation = sanitizePayload({ status, reason: reason.trim(), proof });
      if (entry.failure?.kind === 'http') {
        const httpFailure = httpFailuresByEntry.get(entry);
        const failureIndex = failures.indexOf(httpFailure);
        if (failureIndex >= 0) failures.splice(failureIndex, 1);
        const networkIndex = Array.isArray(report?.network) ? report.network.indexOf(entry.reportNetwork) : -1;
        if (networkIndex >= 0) report.network.splice(networkIndex, 1);
        if (entry.reportError && Array.isArray(report?.errors)) {
          const reportIndex = report.errors.indexOf(entry.reportError);
          if (reportIndex >= 0) report.errors.splice(reportIndex, 1);
        }
        delete entry.reportError;
        delete entry.reportNetwork;
        entry.failure = undefined;
        entry.httpFailureExempted = true;
        syncReportEntry(entry);
        return { status, reason: entry.expectedValidation.reason };
      }
      return { status, reason: entry.expectedValidation.reason };
    },
    markExpectedTransportFailure(request, { reason, action } = {}) {
      const entry = entriesByRequest.get(request);
      if (!entry) throw new TypeError('Expected transport failure must reference an observed scoped Playwright Request.');
      if (entry.isMutation) throw new Error('Expected transport failure cannot exempt a mutation request.');
      if (entry.networkComplete) throw new Error('Expected transport failure must be marked before its request fails.');
      if (typeof reason !== 'string' || !reason.trim() || action === undefined || action === null || action === '') {
        throw new TypeError('Expected transport failure requires a non-empty reason and action.');
      }
      const proof = sanitizePayload({ reason: reason.trim(), action });
      expectedTransportFailures.set(request, proof);
      return { requestIndex: entry.index, ...proof };
    },
    stop() {
      if (stopped) return;
      stopped = true;
      page.off('request', onRequest);
      page.off('response', onResponse);
      page.off('requestfailed', onRequestFailed);
      page.off('requestfinished', onRequestFinished);
      page.off('pageerror', onPageError);
      page.off('console', onConsole);
      for (const echoes of pendingResourceEchoes.values()) echoes.forEach(recordConsoleFailure);
      pendingResourceEchoes.clear();
      for (const timer of bodyTimers) clearTimeout(timer);
      bodyTimers.clear();
      for (const waiter of waiters) {
        clearTimeout(waiter.timer);
        waiter.reject(new Error('Network observer stopped before the matching response completed.'));
      }
      waiters.clear();
    },
  };
}
