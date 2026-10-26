import assert from 'node:assert/strict';

const requestEvidence = request => ({
  requestId: request.requestId,
  endpoint: request.endpoint,
  pathname: request.pathname,
  status: Number.isInteger(request.status) ? request.status : null,
  requestStartedAtMs: request.requestStartedAtMs ?? null,
  responseStartedAtMs: request.responseStartedAtMs ?? null,
  responseFinishedAtMs: request.responseFinishedAtMs ?? null,
  durationMs: request.durationMs ?? null,
  response: request.response ?? null,
  responseReadError: request.responseReadError ?? null,
  loadingFailure: request.loadingFailure ?? null,
});

export const captureValidationWaitFailure = ({ requests, requestOffset, pathname, name, timeoutMs, error, visibleState, capturedAt = new Date().toISOString() }) => {
  assert(Array.isArray(requests), 'Captured authoring requests must be an array');
  assert(Number.isInteger(requestOffset) && requestOffset >= 0 && requestOffset <= requests.length, 'Validation wait request offset must address the captured request list');
  const matched = requests.slice(requestOffset).filter(request => request.endpoint === 'construction-proposals' && request.pathname === pathname);
  return {
    name,
    classification: 'unexpected-validation-wait-failure',
    expectedValidation: false,
    timeoutMs,
    capturedAt,
    failure: String(error),
    visibleState,
    requestIDs: matched.map(request => request.requestId),
    requests: matched.map(requestEvidence),
  };
};

export const refreshValidationWaitFailureRequests = (evidence, requests) => {
  assert(evidence && Array.isArray(evidence.requestIDs), 'Validation wait failure evidence must contain captured request IDs');
  assert(Array.isArray(requests), 'Captured authoring requests must be an array');
  const byID = new Map(requests.map(request => [request.requestId, request]));
  evidence.requests = evidence.requestIDs.map(requestId => {
    const request = byID.get(requestId);
    return request ? requestEvidence(request) : { requestId, missingFromCapture: true };
  });
  return evidence;
};
