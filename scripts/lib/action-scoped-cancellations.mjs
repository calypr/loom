export function matchesOwnedCatalogRequest(request, endpoint, target) {
  const url = new URL(request.url());
  if (request.method() !== 'POST' || url.origin !== target.uiOrigin ||
      url.pathname !== `${target.pathPrefix}/${endpoint}`) return false;
  try {
    if (new URL(request.frame().url()).origin !== target.uiOrigin) return false;
  } catch { return false; }
  let payload;
  try { payload = request.postDataJSON(); } catch { return false; }
  if (!payload || payload.snapshotToken !== target.snapshotToken) return false;
  const requestId = request.headers()['x-request-id'] ?? '';
  if (endpoint === 'semantic-inventory') {
    return target.documents.some(document => document.rootResourceType === payload.rowRoot) &&
      (!payload.outputId || target.documents.some(document => document.output.id === payload.outputId)) &&
      (requestId.startsWith('paired-column-inventory-') || requestId.startsWith('feature-catalog-'));
  }
  if (!target.documents.some(document => document.output.id === payload.outputId)) return false;
  if (endpoint === 'frame-source-options') return requestId.startsWith('frame-source-options-');
  if (endpoint === 'construction-choice-proposals') {
    return payload.expectedDraftVersion === target.draftVersion &&
      payload.expectedDraftDigest === target.draftDigest &&
      Array.isArray(payload.constructionChoices) && payload.constructionChoices.length > 0;
  }
  return false;
}
