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
