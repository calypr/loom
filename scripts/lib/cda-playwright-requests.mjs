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

export function captureCDARequests(page, { apiOrigin, appOrigins = [apiOrigin], ownedPathPrefix, report, responsePaths = /commands|selections|explicit-groups|row-definition-proposals|construction-choice-proposals|construction-proposals|construction-capabilities|row-lineage|population-mapping|preview/, shouldReportHttpError = () => true } = {}) {
  if (!apiOrigin || !ownedPathPrefix || !report || !Array.isArray(report.nativeRequests)) {
    throw new TypeError('CDA request capture needs an API origin, owned path prefix, and nativeRequests report array');
  }
  const apiURL = new URL(apiOrigin);
  const appOriginSet = new Set(appOrigins.map(origin => new URL(origin).origin));
  const byRequest = new Map();
  const pendingReads = new Set();
  let nextBrowserRequestId = 1;
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
    const read = (async () => {
      try {
        if (readResponse) entry.response = parseBody(await response.text());
        else entry.response = { bodyNotRead: true };
      } catch (error) {
        entry.responseReadError = sanitizeText(error?.message ?? error);
      } finally {
        entry.completedAt = Date.now();
        pendingReads.delete(read);
      }
    })();
    pendingReads.add(read);
    if (response.status() >= 400 && shouldReportHttpError(entry.path, response.status())) {
      const errorEntry = { kind: 'http', url: `${entry.origin}${entry.path}`, status: response.status() };
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
    report.errors.push({ kind: 'network', url: `${entry.origin}${entry.path}`, error: entry.failure });
  });
  page.on('pageerror', error => report.errors.push({ kind: 'runtime', message: sanitizeText(error.message) }));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const location = message.location().url;
    if (location && !appOriginSet.has(new URL(location).origin)) return;
    report.errors.push({ kind: 'console', message: sanitizeText(message.text()) });
  });

  return {
    byRequest,
    pendingReads,
    async flush() {
      while (pendingReads.size) await Promise.allSettled([...pendingReads]);
    },
  };
}
