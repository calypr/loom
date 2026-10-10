export const createBuilderAuthoringRequestEntries = ({ apiRoot, uiUrl }) => {
  let requestSequence = 0;
  const previewEntries = [];
  const previewByRequest = new Map();
  const choiceProposalEntries = [];
  const commandEntries = [];
  const reconciliationEntries = [];
  const lifecycleByRequest = new Map();

  const entryFor = request => {
    let url;
    try { url = new URL(request.url()); } catch { return undefined; }
    if (url.origin !== new URL(uiUrl).origin || !url.pathname.startsWith(`${apiRoot}/`)) return undefined;
    const path = url.pathname;
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    const requestObjectIdentity = `playwright-request-${++requestSequence}`;
    const requestId = request.headers()['x-request-id'] ?? null;
    const requestOrigin = url.origin;
    if (path.endsWith('/preview')) {
      const entry = { identity: `preview-${requestSequence}`, requestObjectIdentity, requestId, origin: requestOrigin, method: request.method(), path, outputId: body?.outputId, request, status: undefined, response: undefined };
      previewByRequest.set(request, entry);
      previewEntries.push(entry);
      return entry;
    }
    const collection = path.endsWith('/construction-choice-proposals') ? choiceProposalEntries
      : path.endsWith('/commands') ? commandEntries
        : path.endsWith('/reconcile') ? reconciliationEntries
          : undefined;
    if (!collection) return undefined;
    const entry = { identity: `${collection === choiceProposalEntries ? 'proposal' : collection === commandEntries ? 'command' : 'reconcile'}-${requestSequence}`, requestObjectIdentity, requestId, origin: requestOrigin, method: request.method(), path, body, request, startedAt: Date.now(), status: undefined, response: undefined };
    lifecycleByRequest.set(request, entry);
    collection.push(entry);
    return entry;
  };

  return {
    previewEntries, previewByRequest, choiceProposalEntries, commandEntries, reconciliationEntries,
    lifecycleByRequest, entryFor,
  };
};
