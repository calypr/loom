import type { ExplorerBuilderCandidate, ExplorerBuilderCatalog, ExplorerBuilderColumn } from '../../../types';

export type InterpretationConcept = NonNullable<ExplorerBuilderCandidate['conceptCandidates']>[number];

export interface InterpretationBinding {
  readonly candidate: ExplorerBuilderCandidate;
  readonly concept?: InterpretationConcept;
  readonly resourceType: string;
  readonly sourceProfile?: string;
  readonly sourceCanonical?: string;
  readonly owningScope?: string;
  readonly system?: string;
  readonly code?: string;
  readonly extensionUrlPath?: ReadonlyArray<string>;
  readonly logicalType: string;
  readonly cardinality: string;
}

const normalizedPath = (value: string): string => value.replace(/^root\./, '');

const sourcePaths = (column: ExplorerBuilderColumn): ReadonlyArray<string> => {
  const source = column.source;
  if (source.kind === 'field') return [normalizedPath(source.field.path)];
  if (source.kind === 'aggregate') return source.aggregate.path ? [normalizedPath(source.aggregate.path)] : [];
  if (source.kind === 'projectId') return [];
  const lookup = source.lookup;
  if ('extension' in lookup) return [normalizedPath(lookup.extension.valuePath)];
  if ('binding' in lookup) return [normalizedPath(lookup.binding.valuePath)];
  return lookup.path ? [normalizedPath(lookup.path)] : [];
};

export const interpretationSourceSystem = (column: ExplorerBuilderColumn): string | undefined => {
  const source = column.source;
  if (source.kind === 'identifierBySystem' || source.kind === 'codingBySystem' || source.kind === 'observationComponentByCode') {
    if ('key' in source.lookup) return source.lookup.key.system;
  }
  return undefined;
};

export const interpretationSourceCode = (column: ExplorerBuilderColumn): string | undefined => {
  const source = column.source;
  if (source.kind === 'identifierBySystem' || source.kind === 'codingBySystem' || source.kind === 'observationComponentByCode') {
    if ('key' in source.lookup) return source.lookup.key.code;
  }
  return undefined;
};

export const interpretationSourceExtensionPath = (column: ExplorerBuilderColumn): ReadonlyArray<string> | undefined => {
  if (column.source.kind === 'extensionByUrl' && 'extension' in column.source.lookup) return column.source.lookup.extension.urlPath;
  return undefined;
};

export const matchingInterpretationConcepts = (
  column: ExplorerBuilderColumn,
  candidate: ExplorerBuilderCandidate,
): ReadonlyArray<InterpretationConcept> => {
  const paths = sourcePaths(column);
  const system = interpretationSourceSystem(column);
  const code = interpretationSourceCode(column);
  const extensionPath = interpretationSourceExtensionPath(column);
  return (candidate.conceptCandidates ?? []).filter((concept) =>
    (paths.length === 0 || paths.includes(normalizedPath(concept.sourcePath ?? ''))) &&
    (!system || concept.system === system) &&
    (!code || concept.code === code) &&
    (!extensionPath || JSON.stringify(extensionPath) === JSON.stringify(concept.extensionUrlPath ?? [])),
  );
};

export const resolveInterpretationConcept = (
  column: ExplorerBuilderColumn,
  candidate: ExplorerBuilderCandidate,
): InterpretationConcept | undefined => {
  const concepts = matchingInterpretationConcepts(column, candidate);
  if (concepts.length !== 1) return undefined;
  const [concept] = concepts;
  return concept;
};

/**
 * Resolve a configured feature to one catalog candidate at its occurrence.
 * Ambiguous capability/concept matches are deliberately unavailable in the
 * UI; the compiler applies the same fail-closed rule at the authoring boundary.
 */
export const resolveInterpretationCandidate = (
  column: ExplorerBuilderColumn,
  catalog: ExplorerBuilderCatalog,
  occurrenceNodeId: string | undefined,
): ExplorerBuilderCandidate | undefined => {
  return resolveInterpretationBinding(column, catalog, occurrenceNodeId)?.candidate;
};

export const resolveInterpretationBinding = (
  column: ExplorerBuilderColumn,
  catalog: ExplorerBuilderCatalog,
  occurrenceNodeId: string | undefined,
): InterpretationBinding | undefined => {
  if (!occurrenceNodeId) return undefined;
  const paths = sourcePaths(column);
  if (paths.length === 0) return undefined;
  const matches = (catalog.candidates ?? []).filter((candidate) =>
    candidate.nodeId === occurrenceNodeId && paths.includes(normalizedPath(candidate.fieldPath)),
  );
  if (matches.length !== 1) return undefined;
  const [candidate] = matches;
  const concepts = matchingInterpretationConcepts(column, candidate);
  if (concepts.length > 1) return undefined;
  const [concept] = concepts;
  const resourceType = catalog.nodes.find((node) => node.nodeId === candidate.nodeId)?.resourceType;
  if (!resourceType) return undefined;
  return {
    candidate,
    concept,
    resourceType,
    ...(concept?.sourceProfile ? { sourceProfile: concept.sourceProfile } : {}),
    ...(concept?.sourceCanonical ? { sourceCanonical: concept.sourceCanonical } : {}),
    ...(concept?.owningScope ? { owningScope: concept.owningScope } : {}),
    ...((concept?.system ?? interpretationSourceSystem(column)) ? { system: concept?.system ?? interpretationSourceSystem(column) } : {}),
    ...((concept?.code ?? interpretationSourceCode(column)) ? { code: concept?.code ?? interpretationSourceCode(column) } : {}),
    ...((concept?.extensionUrlPath ?? interpretationSourceExtensionPath(column)) ? { extensionUrlPath: concept?.extensionUrlPath ?? interpretationSourceExtensionPath(column) } : {}),
    logicalType: concept?.logicalType || candidate.logicalType,
    cardinality: candidate.cardinality,
  };
};
