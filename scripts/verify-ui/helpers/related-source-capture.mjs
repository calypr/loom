export const relatedSourceProposalCandidate = (
  entry,
  { candidateId, resourceType, path } = {},
) => {
  if (!entry?.path?.endsWith('/construction-proposals')) return undefined;
  const steps = Array.isArray(entry.request?.candidateConstruction?.steps)
    ? entry.request.candidateConstruction.steps : [];
  const step = steps.find((candidate) => {
    const related = candidate.operation?.relatedSource;
    return candidate.operation?.kind === 'RELATED_SOURCE' &&
      (!candidateId || related?.source?.candidateId === candidateId) &&
      (!resourceType || related?.source?.resourceType === resourceType) &&
      (!path || related?.source?.path === path);
  });
  if (!step) return undefined;
  return {
    step,
    related: step.operation.relatedSource,
    rowValuePolicy: step.operation.relatedSource.rowValuePolicy ?? 'ALL',
  };
};

export const selectedRelatedSourceProposal = (entry, expected = {}) => {
  const { outputId, candidateId, nodeId, choiceId, snapshotToken, resourceType, path, routeTypes } = expected;
  if (!outputId || !candidateId || !nodeId || !choiceId || !snapshotToken || !resourceType || !path ||
      !Array.isArray(routeTypes) || routeTypes.length < 2 ||
      entry?.request?.outputId !== outputId || entry.request.snapshotToken !== snapshotToken) return undefined;
  const selected = relatedSourceProposalCandidate(entry, { candidateId, resourceType, path });
  if (!selected || !selected.step?.id || !entry.request.changedStepId ||
      selected.step.id !== entry.request.changedStepId || selected.related.source.nodeId !== nodeId ||
      selected.related.choiceId !== choiceId || selected.related.form !== 'ALL' ||
      selected.related.contributorRule?.policy !== 'ALL_MATCHES' || !Array.isArray(selected.related.route) ||
      selected.related.route.length !== routeTypes.length - 1 ||
      !selected.related.route.every((hop, index) => hop && typeof hop === 'object' &&
        hop.fromResourceType === routeTypes[index] &&
        hop.toResourceType === routeTypes[index + 1])) return undefined;
  return selected;
};

export const expectedRelatedSourceOneValidation = (entry, expected) => {
  const diagnostics = Array.isArray(entry?.response?.diagnostics) ? entry.response.diagnostics : [];
  const code = entry?.response?.error?.code ?? entry?.response?.code ??
    diagnostics.find((item) => String(item?.severity).toUpperCase() === 'ERROR')?.code;
  return entry?.status === 422 && code === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' &&
    selectedRelatedSourceProposal(entry, expected)?.rowValuePolicy === 'ONE';
};
const relatedSourceValidationCode = entry => entry?.response?.error?.code ?? entry?.response?.code ??
  entry?.response?.diagnostics?.find(item => String(item?.severity).toUpperCase() === 'ERROR')?.code;

export const matchesExpectedRelatedSourceOneValidationCapture = (captured, expected) =>
  typeof expected?.requestId === 'string' && expected.requestId.trim().length > 0 &&
  expected.method === 'POST' && expected.status === 422 &&
  expected.path?.endsWith('/construction-proposals') &&
  relatedSourceValidationCode(expected) === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' &&
  captured?.path === expected.path && captured.method === expected.method &&
  captured.requestId === expected.requestId && captured.status === 422 &&
  relatedSourceValidationCode(captured) === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' &&
  JSON.stringify(captured.request ?? null) === JSON.stringify(expected.request ?? null);
