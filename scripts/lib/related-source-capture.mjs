export const relatedSourceProposalCandidate = (
  entry,
  { candidateId, resourceType, path } = {},
) => {
  if (!entry?.path?.endsWith('/construction-proposals')) return undefined;
  const steps = entry.request?.candidateConstruction?.steps ?? [];
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
