export function capabilityBinding(request, target) {
  const url = new URL(request.url());
  const path = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(target.explorer)}/authoring/v2/construction-capabilities`;
  if (url.origin !== new URL(target.uiUrl).origin || request.method() !== 'POST' ||
      !target.explorer || url.pathname !== path) return null;
  let body;
  try { body = request.postDataJSON(); } catch { return null; }
  if (typeof body?.snapshotToken !== 'string' || !body.snapshotToken ||
      !Number.isInteger(body.expectedDraftVersion) || body.expectedDraftVersion <= 0 ||
      typeof body.expectedDraftDigest !== 'string' || !body.expectedDraftDigest ||
      typeof body.outputId !== 'string' || !body.outputId ||
      typeof body.stageId !== 'string' || !body.stageId) return null;
  return {
    route: url.origin + url.pathname,
    snapshotToken: body.snapshotToken,
    draftVersion: body.expectedDraftVersion,
    draftDigest: body.expectedDraftDigest,
    outputId: body.outputId,
    stageId: body.stageId,
  };
}

export function capabilityResponseMatches(binding, response) {
  return Boolean(binding && response && response.snapshotToken === binding.snapshotToken &&
    response.draftVersion === binding.draftVersion && response.draftDigest === binding.draftDigest &&
    response.outputId === binding.outputId && response.stageId === binding.stageId);
}

export function supersedingCapabilityRequest(failed, requests) {
  if (failed.errorText !== 'net::ERR_ABORTED' || !failed.binding) return null;
  return requests.find(replacement =>
    replacement.sequence > failed.sequence && replacement.status >= 200 && replacement.status < 300 &&
    replacement.finished === true && replacement.responseMatches === true && !replacement.failed &&
    replacement.binding?.route === failed.binding.route &&
    JSON.stringify(replacement.binding) !== JSON.stringify(failed.binding)) ?? null;
}

export function isIncidentalFavicon(url, target, status) {
  try {
    const parsed = new URL(url);
    return status === 404 && parsed.origin === new URL(target.uiUrl).origin && parsed.pathname === '/favicon.ico';
  } catch { return false; }
}
