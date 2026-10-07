const belongsToPath = (pathname, prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`);

/** Classify browser API traffic against the local UI proxy and one owned Explorer. */
export const classifyNativeBrowserApiRequest = (requestUrl, {
  uiOrigin,
  apiOrigin,
  project,
  explorer,
  protectedExplorer,
}) => {
  const url = new URL(requestUrl);
  if (!url.pathname.startsWith('/api/')) return { kind: 'ignore', url };

  const projectPath = `/api/v1/projects/${encodeURIComponent(project)}`;
  const explorersPath = `${projectPath}/explorers`;
  const protectedPath = `${explorersPath}/${encodeURIComponent(protectedExplorer)}`;
  if (belongsToPath(url.pathname, protectedPath) || url.pathname.includes(`/explorers/${encodeURIComponent(protectedExplorer)}/`)) {
    return { kind: 'reject', reason: 'protected-explorer', url };
  }
  if (url.origin === new URL(apiOrigin).origin) return { kind: 'reject', reason: 'direct-api-origin', url };
  if (url.origin !== new URL(uiOrigin).origin) return { kind: 'reject', reason: 'unexpected-origin', url };

  const ownedExplorerPath = `${explorersPath}/${encodeURIComponent(explorer)}`;
  if (url.pathname === explorersPath || belongsToPath(url.pathname, ownedExplorerPath)) {
    return { kind: 'capture', url, scope: 'owned-project-explorer' };
  }
  if (url.pathname.startsWith(`${projectPath}/`) && !url.pathname.includes(`${explorersPath}/`)) {
    return { kind: 'capture', url, scope: 'owned-project' };
  }
  return { kind: 'reject', reason: 'outside-project-explorer-scope', url };
};

/** Require the proxied response to remain bound to the captured UI request URL. */
export const isSameUiProxyResponse = (requestUrl, responseUrl, options) => {
  let request;
  let response;
  try {
    request = classifyNativeBrowserApiRequest(requestUrl, options);
    response = classifyNativeBrowserApiRequest(responseUrl, options);
  } catch {
    return false;
  }
  return request.kind === 'capture' && response.kind === 'capture' &&
    request.url.origin === new URL(options.uiOrigin).origin &&
    response.url.origin === new URL(options.uiOrigin).origin &&
    request.url.pathname === response.url.pathname &&
    request.url.search === response.url.search;
};

/** Accept a bound response or a fully classified owner cancellation before response headers. */
export const nativeRequestsHaveOwnedTransportOutcomes = (entries, uiOrigin) => {
  if (!Array.isArray(entries) || entries.length === 0 || typeof uiOrigin !== 'string') return false;
  return entries.every((entry) => {
    if (!entry || entry.origin !== uiOrigin ||
        !['owned-project', 'owned-project-explorer'].includes(entry.transportScope) ||
        entry.authorizationHeaderPresent !== false) return false;

    const response = entry.responseBinding;
    if (response?.matchesCapturedUiProxyRequest === true) {
      return response.origin === uiOrigin && response.path === entry.path &&
        entry.networkTerminal === true && entry.terminalState === 'finished' &&
        entry.bodyReadStatus === 'decoded' && Number.isInteger(entry.status);
    }

    return entry.transportScope === 'owned-project-explorer' &&
      entry.expectedOwnerCancellation?.expected === true &&
      entry.expectedOwnerCancellation.networkTerminal === true &&
      entry.expectedOwnerCancellation.bodyReadStatus === 'failed' &&
      entry.networkTerminal === true && entry.terminalState === 'failed' &&
      entry.bodyReadStatus === 'failed' && entry.cancelled === true &&
      entry.loadingFailure?.errorText === 'net::ERR_ABORTED' && entry.loadingFailure?.canceled === true &&
      entry.loadingFailed?.errorText === 'net::ERR_ABORTED' && entry.loadingFailed?.canceled === true &&
      !entry.loadingFailed.blockedReason && !entry.loadingFailed.corsErrorStatus &&
      typeof entry.requestCorrelationId === 'string' && entry.requestCorrelationId.length > 0 &&
      typeof entry.cdpRequestId === 'string' && entry.cdpRequestId.length > 0 &&
      entry.cdpRequestMatchCount === 1 && entry.status === undefined &&
      entry.responseReceivedAt === undefined && entry.responseHeadersAt === undefined &&
      entry.responseBinding === undefined;
  });
};
