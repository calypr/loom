export const summarizeCodedPivotNativeRequests = requests => {
  if (!Array.isArray(requests)) throw new TypeError('Coded Pivot request evidence must be an array.');
  const details = requests.map(request => {
    const explicitFailure = typeof request.failure === 'string' && request.failure.length > 0
      ? request.failure
      : typeof request.responseReadError === 'string' && request.responseReadError.length > 0
        ? request.responseReadError
        : undefined;
    const terminal = Number.isFinite(request.completedAt) || Boolean(explicitFailure);
    return {
      browserRequestId: request.browserRequestId,
      requestId: request.requestId,
      path: request.path,
      method: request.method,
      status: request.status,
      terminal,
      ...(explicitFailure ? { failure: explicitFailure } : {}),
      ...(request.expectedCancellation ? { expectedCancellation: request.expectedCancellation } : {}),
    };
  });
  const pending = details.filter(request => !request.terminal);
  const terminalFailures = details.filter(request => request.failure);
  const invalidStatuses = details.filter(request => !Number.isFinite(request.status) || request.status >= 400);
  return {
    total: details.length,
    pending,
    terminalFailures,
    invalidStatuses,
    passed: details.length > 0 && pending.length === 0 && terminalFailures.length === 0 && invalidStatuses.length === 0,
  };
};
