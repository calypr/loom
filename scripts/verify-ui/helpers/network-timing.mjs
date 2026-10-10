export const correlateRequestFailure = ({
  requestStartedAt,
  failedAt,
  workflowStartedAt,
  action,
  navigationSequenceAtStart = 0,
  navigations = [],
}) => ({
  requestStartedMs: Math.max(0, Math.round(requestStartedAt - workflowStartedAt)),
  failedAtMs: Math.max(0, Math.round(failedAt - workflowStartedAt)),
  durationMs: Math.max(0, Math.round(failedAt - requestStartedAt)),
  action: action ? { id: action.id, label: action.label } : null,
  mainFrameNavigations: navigations
    .filter(navigation => navigation.sequence > navigationSequenceAtStart
      && navigation.startedAt >= requestStartedAt && navigation.startedAt <= failedAt)
    .map(({ id, atMs, url }) => ({ id, atMs, url })),
});
