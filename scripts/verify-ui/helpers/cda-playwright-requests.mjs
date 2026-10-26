import assert from 'node:assert/strict';
import { createPlaywrightNetworkObserver } from './playwright-network-observer.mjs';

const defaultResponsePaths = /(?:\/commands|\/authoring\/v2\/(?:publish|save)|\/reconcile|\/preview|\/construction-proposals|\/construction-choice-proposals|\/construction-capabilities|\/row-definition-proposals|\/row-definition-choices|\/table-shape-proposals|\/cell-trace|\/population-mapping|\/row-lineage|\/selections|\/explicit-groups|\/related-expand-choices)(?:\/|$)/;

const matchesPathPrefix = (pathname, prefix) => pathname === prefix || pathname.startsWith(`${prefix.replace(/\/$/, '')}/`);

export function createCDARequestView(observer, { browserRequestOrigin, ownedPathPrefix } = {}) {
  if (!observer || !Array.isArray(observer.requests) || typeof observer.waitForResponse !== 'function') {
    throw new TypeError('CDA request view needs a shared Playwright network observer.');
  }
  if (typeof ownedPathPrefix !== 'string' || !ownedPathPrefix.startsWith('/')) {
    throw new TypeError('CDA request view needs an absolute owned path prefix.');
  }
  const origin = new URL(browserRequestOrigin).origin;
  const matches = entry => entry.origin === origin && matchesPathPrefix(entry.path, ownedPathPrefix);
  const translateFromIndex = fromIndex => {
    if (!Number.isInteger(fromIndex) || fromIndex < 0) return fromIndex;
    const scoped = observer.requests.filter(matches);
    return scoped[fromIndex]?.index ?? observer.requests.length;
  };
  return {
    get requests() { return observer.requests.filter(matches); },
    byRequest: observer.byRequest,
    waitForResponse: (predicate, options = {}) => observer.waitForResponse(
      entry => matches(entry) && predicate(entry), {
        ...options,
        fromIndex: translateFromIndex(options.fromIndex ?? 0),
      }),
    requestBody: observer.requestBody,
    responseBody: observer.responseBody,
    markExpectedValidation: observer.markExpectedValidation,
    markExpectedTransportFailure: observer.markExpectedTransportFailure,
  };
}

export function captureCDARequests(page, {
  apiOrigin,
  browserRequestOrigin = apiOrigin,
  appOrigins = [apiOrigin],
  ownedPathPrefix,
  report,
  responsePaths = defaultResponsePaths,
} = {}) {
  if (!apiOrigin || !ownedPathPrefix || !report || !Array.isArray(report.nativeRequests)) {
    throw new TypeError('CDA request capture needs an API origin, owned path prefix, and nativeRequests report array');
  }
  const origin = new URL(browserRequestOrigin).origin;
  const prefix = ownedPathPrefix;
  const observer = createPlaywrightNetworkObserver(page, {
    scopeRequest(request) {
      try {
        const url = new URL(request.url());
        return url.origin === origin && matchesPathPrefix(url.pathname, prefix);
      } catch {
        return false;
      }
    },
    appOrigins,
    responsePaths,
    report,
  });
  const view = createCDARequestView(observer, { browserRequestOrigin: origin, ownedPathPrefix: prefix });
  return Object.defineProperties(view, {
    observer: { value: observer, enumerable: true },
    stop: { value: () => observer.stop(), enumerable: true },
  });
}

export const findCompletedNativeResponse = (entries, responseFor, predicate, fromIndex = 0) =>
  entries.slice(fromIndex).find(entry => {
    if (entry.complete !== true && entry.completedAt === undefined) return false;
    const response = responseFor(entry);
    return response !== undefined && predicate(entry, response);
  });

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
  const request = entry?.request;
  const selection = request?.selection;
  const expanded = selection?.expanded;
  const response = entry?.response;
  const error = response?.error;
  const diagnostic = error?.diagnostic ?? response?.diagnostics?.find(item => item?.code === expected?.code);
  return entry?.method === 'POST' && entry.path === expected?.path &&
    entry.status === 422 && request?.snapshotToken === expected?.snapshotToken &&
    request.expectedDraftVersion === expected?.expectedDraftVersion &&
    request.expectedDraftDigest === expected?.expectedDraftDigest && request.outputId === expected?.outputId &&
    selection?.kind === 'EXPANDED' && expanded?.rowChoiceId === expected?.rowChoiceId &&
    expanded.emptyCollectionPolicy === 'ERROR' && error?.code === expected?.code &&
    diagnostic?.code === expected?.code && diagnostic?.stage === expected?.stage;
}

export function matchesExpectedEmptyCollectionValidationConsole(consoleError, entry, nativeRequests, expected) {
  if (!matchesExpectedEmptyCollectionValidation(entry, expected)) return false;
  const competingRequests = nativeRequests.filter(candidate => candidate.origin === entry.origin &&
    candidate.path === entry.path && (candidate.status === entry.status || candidate.status === undefined));
  return competingRequests.length === 1 && competingRequests[0] === entry &&
    consoleError?.kind === 'console' && consoleError.location === `${entry.origin}${entry.path}` &&
    consoleError.message === 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
}
