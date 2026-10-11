import { canonicalProjectID } from '../../loom-dev.mjs';

export function ownedFaultTarget(target, { method, path }) {
  if (typeof method !== 'string' || !method.trim()) throw new TypeError('fault method must be a non-empty string');
  if (typeof target.fixtureProject !== 'string' || !target.fixtureProject) {
    throw new TypeError('fault target requires the owned fixture project');
  }
  if (typeof path !== 'string' || !path.startsWith('/') || path.includes('?') || path.includes('#')) {
    throw new TypeError('fault path must be an exact pathname without a query or fragment');
  }
  const parsed = new URL(path, target.uiUrl);
  if (parsed.origin !== new URL(target.uiUrl).origin) throw new TypeError('fault path must remain on the owned UI origin');
  if (parsed.pathname !== path) throw new TypeError('fault path must be an exact pathname without encoded normalization');
  if (parsed.pathname === '/graphql/graph' && method.toUpperCase() === 'POST') {
    return { origin: parsed.origin, method: 'POST', path, bodyProject: canonicalProjectID(target.fixtureProject) };
  }
  const projectPrefix = `/api/v1/projects/${encodeURIComponent(encodeURIComponent(canonicalProjectID(target.fixtureProject)))}`;
  if (parsed.pathname !== projectPrefix && !parsed.pathname.startsWith(`${projectPrefix}/`)) {
    throw new TypeError('fault path must remain inside the owned fixture project');
  }
  return { origin: parsed.origin, method: method.toUpperCase(), path };
}

export function matchesOwnedFaultRequest(request, faultTarget, matchesRequest) {
  try {
    const url = new URL(request.url());
    if (url.origin !== faultTarget.origin || url.pathname !== faultTarget.path ||
        request.method() !== faultTarget.method) return false;
    if (!matchesRequest && !faultTarget.bodyProject) return true;
    let body;
    try { body = request.postDataJSON(); } catch { return false; }
    if (faultTarget.bodyProject && body?.variables?.input?.projectId !== faultTarget.bodyProject) return false;
    if (!matchesRequest) return true;
    const result = matchesRequest(body);
    return Boolean(result) && !(result && typeof result.then === 'function');
  } catch {
    return false;
  }
}
