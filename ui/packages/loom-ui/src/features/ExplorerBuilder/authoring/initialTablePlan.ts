import type {
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
} from '../../../types';

type RowRoot = ExplorerBuilderCatalog['nodes'][number];

export type InitialTableCandidateEvidence =
  | {
      readonly kind: 'available';
      readonly candidates: ReadonlyArray<ExplorerBuilderCandidate>;
    }
  | { readonly kind: 'unavailable' };

export type InitialTablePlan =
  | {
      readonly kind: 'with-identity';
      readonly root: RowRoot;
      readonly title: string;
      readonly candidate: ExplorerBuilderCandidate;
    }
  | {
      readonly kind: 'root-only';
      readonly root: RowRoot;
      readonly title: string;
      readonly reason:
        | 'CATALOG_UNAVAILABLE'
        | 'NO_DIRECT_STRING_ID'
        | 'AMBIGUOUS_DIRECT_ID';
    };

const isExecutableDirectId = (
  candidate: ExplorerBuilderCandidate,
  root: RowRoot,
): boolean => {
  const choice = candidate.constructionChoice;
  const source = choice?.source;
  const valueOptions = choice?.options.filter(
    (option) => option.form === 'VALUE',
  ) ?? [];
  const valueOption = valueOptions[0];

  return candidate.nodeId === root.nodeId &&
    candidate.fieldPath === 'id' &&
    candidate.logicalType.toLowerCase() === 'string' &&
    (candidate.cardinality === 'optional_one' ||
      candidate.cardinality === 'required_one') &&
    candidate.repeated !== true &&
    candidate.defaultProjectionMode === 'VALUE' &&
    candidate.projectionModes.includes('VALUE') &&
    choice !== undefined &&
    choice.route.length === 0 &&
    source?.kind === 'FIELD' &&
    source.candidateId === candidate.candidateId &&
    source.nodeId === root.nodeId &&
    source.resourceType === root.resourceType &&
    source.path === 'id' &&
    source.cardinality === candidate.cardinality &&
    valueOptions.length === 1 &&
    valueOption?.decision === 'DEFAULT' &&
    valueOption.support === 'SUPPORTED' &&
    valueOption.preservation === 'PRESERVING' &&
    valueOption.rowEffect === 'PRESERVES_ROW_GRAIN';
};

export const planInitialTable = (
  root: RowRoot,
  requestedTitle: string,
  evidence: InitialTableCandidateEvidence,
): InitialTablePlan => {
  const title = requestedTitle.trim() || root.resourceType;
  if (evidence.kind === 'unavailable') {
    return { kind: 'root-only', root, title, reason: 'CATALOG_UNAVAILABLE' };
  }

  const { candidates } = evidence;
  const directIds = candidates.filter(
    (candidate) => candidate.nodeId === root.nodeId && candidate.fieldPath === 'id',
  );
  const executableIds = directIds.filter((candidate) =>
    isExecutableDirectId(candidate, root),
  );

  if (executableIds.length === 1) {
    return {
      kind: 'with-identity',
      root,
      title,
      candidate: executableIds[0]!,
    };
  }

  return {
    kind: 'root-only',
    root,
    title,
    reason: executableIds.length > 1
      ? 'AMBIGUOUS_DIRECT_ID'
      : 'NO_DIRECT_STRING_ID',
  };
};
