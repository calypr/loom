import { sanitizeBody, sanitizeText } from './playwright-browser.mjs';

const maxBodyLength = 32768;
const parseBody = body => {
  const text = String(body ?? '');
  if (text.length > maxBodyLength) return { truncated: true, length: text.length };
  const sanitized = sanitizeBody(text);
  try {
    return JSON.parse(sanitized);
  } catch {
    return sanitized;
  }
};

export function captureCDARequests(page, { apiOrigin, appOrigins = [apiOrigin], ownedPathPrefix, report, responsePaths = /commands|selections|explicit-groups|row-definition-proposals|construction-choice-proposals|construction-proposals|construction-capabilities|row-lineage|population-mapping|preview/ } = {}) {
  if (!apiOrigin || !ownedPathPrefix || !report || !Array.isArray(report.nativeRequests)) {
    throw new TypeError('CDA request capture needs an API origin, owned path prefix, and nativeRequests report array');
  }
  const apiURL = new URL(apiOrigin);
  const appOriginSet = new Set(appOrigins.map(origin => new URL(origin).origin));
  const byRequest = new Map();
  const pendingReads = new Set();
  const waiters = new Set();
  let nextBrowserRequestId = 1;
  const notify = () => {
    for (const waiter of [...waiters]) {
      const match = report.nativeRequests.slice(waiter.fromIndex).find(entry => entry.completedAt && waiter.predicate(entry));
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
      ...(body !== undefined ? { body } : {}),
    };
    byRequest.set(request, entry);
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
    const read = Promise.resolve().then(async () => {
      try {
        if (readResponse) entry.response = parseBody(await response.text());
        else entry.response = { bodyNotRead: true };
      } catch (error) {
        entry.responseReadError = sanitizeText(error?.message ?? error);
      } finally {
        entry.completedAt = Date.now();
        pendingReads.delete(read);
        notify();
      }
    });
    pendingReads.add(read);
    if (response.status() >= 400) {
      const errorEntry = { kind: 'http', origin: entry.origin, path: entry.path, url: `${entry.origin}${entry.path}`, status: response.status(), requestId: entry.requestId, browserRequestId: entry.browserRequestId, method: entry.method, startedAt: entry.startedAt, request: entry.body };
      const diagnostic = read.then(() => {
        if (entry.response !== undefined) errorEntry.response = entry.response;
        report.errors.push(errorEntry);
      });
      let pendingDiagnostic;
      pendingDiagnostic = diagnostic.finally(() => pendingReads.delete(pendingDiagnostic));
      pendingReads.add(pendingDiagnostic);
    }
  });

  page.on('requestfailed', request => {
    const entry = byRequest.get(request);
    if (!entry) return;
    entry.completedAt = Date.now();
    entry.failure = sanitizeText(request.failure()?.errorText);
    report.errors.push({ kind: 'network', origin: entry.origin, path: entry.path, url: `${entry.origin}${entry.path}`, requestId: entry.requestId, browserRequestId: entry.browserRequestId, method: entry.method, startedAt: entry.startedAt, error: entry.failure });
    notify();
  });
  page.on('pageerror', error => report.errors.push({ kind: 'runtime', message: sanitizeText(error.message) }));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location && !appOriginSet.has(new URL(location).origin)) return;
    if (location && new URL(location).pathname === '/favicon.ico' && /404 \(Not Found\)/.test(message.text())) {
      (report.assetFailures ??= []).push({ kind: 'console', url: location, status: 404 });
      return;
    }
    report.errors.push({ kind: 'console', message: sanitizeText(message.text()) });
  });

  return {
    byRequest,
    pendingReads,
    waitFor(predicate, { fromIndex = 0, timeoutMs = 5000 } = {}) {
      const match = report.nativeRequests.slice(fromIndex).find(entry => entry.completedAt && predicate(entry));
      if (match) return Promise.resolve(match);
      return new Promise((resolve, reject) => {
        const waiter = { predicate, fromIndex, resolve, timer: undefined };
        waiter.timer = setTimeout(() => {
          waiters.delete(waiter);
          const observed = report.nativeRequests.slice(fromIndex).map(({ origin, path, method, status, completedAt, failure }) => ({ origin, path, method, status, completed: Boolean(completedAt), failure }));
          reject(new Error(`Timed out waiting for owned CDA request: ${JSON.stringify(observed)}`));
        }, timeoutMs);
        waiters.add(waiter);
      });
    },
    async flush() {
      while (pendingReads.size) await Promise.allSettled([...pendingReads]);
    },
  };
}
