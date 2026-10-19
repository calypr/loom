import type {
  ExplorerBuilderCatalog,
  RowChangeAssessment,
  RowChangeUnresolvedReference,
} from '../../../types';
import { relationshipLabel } from '../constructionWorkspace/routeDisplay';
import { derivedOccurrences, type DraftTable } from './model';

export type RootChangeSelection =
  | {
      readonly kind: 'authored-occurrence';
      readonly nodeId: string;
      readonly occurrenceId: string;
    }
  | {
      readonly kind: 'catalog-node';
      readonly nodeId: string;
    }
  | {
      readonly kind: 'compiler-route';
      readonly nodeId: string;
      readonly referenceId: string;
      readonly routeToken: string;
    }
  | {
      readonly kind: 'compiler-occurrence';
      readonly nodeId: string;
      readonly referenceId: string;
      readonly occurrenceId: string;
    };

export type CompilerRootPickerChoice =
  | {
      readonly kind: 'compiler-route';
      readonly key: string;
      readonly selection: Extract<RootChangeSelection, { readonly kind: 'compiler-route' }>;
      readonly label: string;
    }
  | {
      readonly kind: 'compiler-occurrence';
      readonly key: string;
      readonly selection: Extract<RootChangeSelection, { readonly kind: 'compiler-occurrence' }>;
      readonly label: string;
    }
  | {
      readonly kind: 'compiler-edge';
      readonly key: string;
      readonly referenceId: string;
      readonly nodeId: string;
      readonly edgeId: string;
      readonly label: string;
    };

export type RootChangeResolution = {
  readonly rootOccurrenceId?: string;
  readonly routeRebase?: ReadonlyArray<{
    readonly occurrenceId: string;
    readonly edgeId: string;
  }>;
};

export type RootChangeRequestIdentity = {
  readonly epoch: number;
  readonly project: string;
  readonly authResourcePath?: string;
  readonly explorerId: string;
  readonly outputId: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
};

export const sameRootChangeRequestIdentity = (
  left: RootChangeRequestIdentity,
  right: RootChangeRequestIdentity,
): boolean => left.epoch === right.epoch && left.project === right.project &&
  left.authResourcePath === right.authResourcePath && left.explorerId === right.explorerId &&
  left.outputId === right.outputId && left.snapshotToken === right.snapshotToken &&
  left.draftVersion === right.draftVersion && left.draftDigest === right.draftDigest;

export type RootChangePickerState =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'assessing';
      readonly selection: RootChangeSelection;
      readonly resolution: RootChangeResolution;
      readonly identity: RootChangeRequestIdentity;
    }
  | {
      readonly kind: 'blocked';
      readonly selection: RootChangeSelection;
      readonly resolution: RootChangeResolution;
      readonly identity: RootChangeRequestIdentity;
      readonly assessment: Extract<RowChangeAssessment, { readonly status: 'BLOCKED' }>;
    }
  | {
      readonly kind: 'previewing';
      readonly selection: RootChangeSelection;
      readonly resolution: RootChangeResolution;
      readonly identity: RootChangeRequestIdentity;
    }
  | {
      readonly kind: 'failed';
      readonly reason: 'stale' | 'unavailable' | 'request';
      readonly selection: RootChangeSelection;
      readonly resolution: RootChangeResolution;
      readonly identity: RootChangeRequestIdentity;
      readonly message: string;
    };

const edgeChoiceLabel = (
  edgeId: string,
  reference: RowChangeUnresolvedReference,
  catalog: ExplorerBuilderCatalog,
): string | undefined => {
  const edge = catalog.edges.find((candidate) => candidate.edgeId === edgeId);
  if (!edge) return undefined;
  const from = catalog.nodes.find((node) => node.nodeId === edge.fromNodeId)?.resourceType;
  const to = catalog.nodes.find((node) => node.nodeId === edge.toNodeId)?.resourceType;
  if (!from || !to) return undefined;
  const relationship = relationshipLabel({
    fromResourceType: from,
    toResourceType: to,
    relationship: edge.label,
  });
  return `Use ${relationship} between ${from} and ${to} for ${reference.message}`;
};

export const compilerRootRepairChoices = ({
  assessment,
  catalog,
  table,
  target,
  resolution,
  boundaryIdentity,
}: {
  readonly assessment: Extract<RowChangeAssessment, { readonly status: 'BLOCKED' }>;
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly target: RootChangeSelection;
  readonly resolution: RootChangeResolution;
  readonly boundaryIdentity: RootChangeRequestIdentity;
}): ReadonlyArray<CompilerRootPickerChoice> => {
  if (table.outputId !== boundaryIdentity.outputId ||
      catalog.snapshotToken !== boundaryIdentity.snapshotToken ||
      assessment.snapshotToken !== boundaryIdentity.snapshotToken ||
      assessment.draftVersion !== boundaryIdentity.draftVersion ||
      assessment.draftDigest !== boundaryIdentity.draftDigest) return [];
  const occurrences = derivedOccurrences(table, catalog);
  const authoredChoices = startingRootChoices(table, catalog);
  const choices: CompilerRootPickerChoice[] = [];
  for (const reference of assessment.unresolved) {
    if (reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE') {
      for (const alternative of reference.alternatives ?? []) {
        if (alternative === resolution.rootOccurrenceId) continue;
        if (isCatalogRootRouteSelectionToken(alternative)) {
          const route = resolveCatalogRootRouteSelection(
            alternative,
            catalog,
            table,
            target.nodeId,
            true,
          );
          if (!route) continue;
          choices.push({
            kind: 'compiler-route',
            key: `compiler-route:${reference.id}:${alternative}`,
            selection: {
              kind: 'compiler-route',
              nodeId: target.nodeId,
              referenceId: reference.id,
              routeToken: alternative,
            },
            label: `Use ${route.pathLabel} from ${route.anchorLabel}`,
          });
          continue;
        }
        const occurrence = occurrences.find((candidate) => candidate.id === alternative);
        if (!occurrence) continue;
        const pathLabel = authoredChoices.find((choice) =>
          choice.selection.kind === 'authored-occurrence' &&
          choice.selection.occurrenceId === occurrence.id,
        )?.relationship ?? occurrence.relationship ?? `${occurrence.resourceType} rows`;
        choices.push({
          kind: 'compiler-occurrence',
          key: `compiler-occurrence:${reference.id}:${alternative}`,
          selection: {
            kind: 'compiler-occurrence',
            nodeId: target.nodeId,
            referenceId: reference.id,
            occurrenceId: occurrence.id,
          },
          label: `Use ${pathLabel}`,
        });
      }
      continue;
    }
    if (reference.code !== 'AMBIGUOUS_ROUTE_REBASE_EDGE') continue;
    for (const edgeId of reference.alternatives ?? []) {
      if (resolution.routeRebase?.some((choice) =>
        choice.occurrenceId === reference.id && choice.edgeId === edgeId,
      )) continue;
      const label = edgeChoiceLabel(edgeId, reference, catalog);
      if (!label) continue;
      choices.push({
        kind: 'compiler-edge',
        key: `compiler-edge:${reference.id}:${edgeId}`,
        referenceId: reference.id,
        nodeId: target.nodeId,
        edgeId,
        label,
      });
    }
  }
  return choices;
};

export const compilerRootSelectionLabel = ({
  selection,
  catalog,
  table,
  boundaryIdentity,
}: {
  readonly selection: RootChangeSelection;
  readonly catalog: ExplorerBuilderCatalog;
  readonly table: DraftTable;
  readonly boundaryIdentity: RootChangeRequestIdentity;
}): string | undefined => {
  if (table.outputId !== boundaryIdentity.outputId ||
      catalog.snapshotToken !== boundaryIdentity.snapshotToken) return undefined;
  if (selection.kind === 'compiler-route') {
    const route = resolveCatalogRootRouteSelection(
      selection.routeToken,
      catalog,
      table,
      selection.nodeId,
      true,
    );
    return route ? `Use ${route.pathLabel} from ${route.anchorLabel}` : undefined;
  }
  if (selection.kind !== 'compiler-occurrence') return undefined;
  const occurrence = derivedOccurrences(table, catalog).find(
    (candidate) => candidate.id === selection.occurrenceId,
  );
  if (!occurrence) return undefined;
  const pathLabel = startingRootChoices(table, catalog).find((choice) =>
    choice.selection.kind === 'authored-occurrence' &&
    choice.selection.occurrenceId === occurrence.id,
  )?.relationship ?? occurrence.relationship ?? `${occurrence.resourceType} rows`;
  return `Use ${pathLabel}`;
};

export type StartingRootChoice = {
  readonly key: string;
  readonly selection: RootChangeSelection;
  readonly resourceType: string;
  readonly relationship?: string;
  readonly current: boolean;
};

export type CatalogRootRouteSelection = {
  readonly edgeIds: ReadonlyArray<string>;
  readonly anchorOccurrenceId: string;
};

export type ResolvedCatalogRootRouteSelection = CatalogRootRouteSelection & {
  readonly edges: ReadonlyArray<ExplorerBuilderCatalog['edges'][number]>;
  readonly anchorLabel: string;
  readonly pathLabel: string;
};

const routeSelectionPrefix = 'row-change-route:v1:';

export const isCatalogRootRouteSelectionToken = (value: string): boolean =>
  value.startsWith(routeSelectionPrefix);

const routeEdgeLabel = (
  catalog: ExplorerBuilderCatalog,
  edge: ExplorerBuilderCatalog['edges'][number],
): string => {
  const from = catalog.nodes.find((node) => node.nodeId === edge.fromNodeId);
  const to = catalog.nodes.find((node) => node.nodeId === edge.toNodeId);
  if (!from || !to) return '';
  return relationshipLabel({
    fromResourceType: from.resourceType,
    toResourceType: to.resourceType,
    relationship: edge.label,
  });
};

const describeEdgePath = (
  catalog: ExplorerBuilderCatalog,
  edges: ReadonlyArray<ExplorerBuilderCatalog['edges'][number]>,
): string => {
  const first = edges[0];
  if (!first) return '';
  const start = catalog.nodes.find((node) => node.nodeId === first.fromNodeId);
  if (!start) return '';
  const parts = [start.resourceType];
  for (const edge of edges) {
    const target = catalog.nodes.find((node) => node.nodeId === edge.toNodeId);
    const label = routeEdgeLabel(catalog, edge);
    if (!target || !label) return '';
    parts.push(label, target.resourceType);
  }
  return parts.join(' → ');
};

const authoredPathEdgeIds = (
  occurrenceId: string,
  occurrences: ReturnType<typeof derivedOccurrences>,
): ReadonlyArray<string> | undefined => {
  const byId = new Map(occurrences.map((occurrence) => [occurrence.id, occurrence]));
  let current = byId.get(occurrenceId);
  if (!current) return undefined;
  const reversed: string[] = [];
  while (current.parentId) {
    if (!current.incomingEdgeId) return undefined;
    reversed.push(current.incomingEdgeId);
    current = byId.get(current.parentId);
    if (!current) return undefined;
  }
  return reversed.reverse();
};

const shortestCatalogPaths = (
  catalog: ExplorerBuilderCatalog,
  startNodeId: string,
): ReadonlyMap<string, {
  readonly edges: ReadonlyArray<ExplorerBuilderCatalog['edges'][number]>;
  readonly ambiguous: boolean;
}> => {
  const maxSteps = catalog.routePolicy.maxSteps ?? catalog.nodes.length;
  const paths = new Map<string, {
    readonly edges: ReadonlyArray<ExplorerBuilderCatalog['edges'][number]>;
    ambiguous: boolean;
  }>([
    [startNodeId, { edges: [], ambiguous: false }],
  ]);
  const queue = [startNodeId];
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    const fromNodeId = queue[cursor];
    const path = paths.get(fromNodeId);
    if (!path || path.edges.length >= maxSteps) continue;
    for (const edge of catalog.edges) {
      if (edge.fromNodeId !== fromNodeId || edge.populated === false) continue;
      if (edge.fromNodeId === edge.toNodeId) continue;
      if (!catalog.edges.some((candidate) =>
        candidate.fromNodeId === edge.toNodeId && candidate.toNodeId === edge.fromNodeId,
      )) continue;
      const nextEdges = [...path.edges, edge];
      const existing = paths.get(edge.toNodeId);
      if (!existing) {
        paths.set(edge.toNodeId, { edges: nextEdges, ambiguous: path.ambiguous });
        queue.push(edge.toNodeId);
      } else if (existing.edges.length === nextEdges.length) {
        paths.set(edge.toNodeId, {
          ...existing,
          ambiguous: true,
        });
      }
    }
  }
  return paths;
};

export const startingRootChoices = (
  table: DraftTable,
  catalog: ExplorerBuilderCatalog,
): ReadonlyArray<StartingRootChoice> => {
  const occurrences = derivedOccurrences(table, catalog);
  const currentRoot = occurrences[0];
  if (!currentRoot) return [];
  const eligibleNodeIds = new Set(
    catalog.nodes
      .filter((node) => node.rowRootEligible && node.populated)
      .map((node) => node.nodeId),
  );
  if (!eligibleNodeIds.has(currentRoot.nodeId)) return [];

  const choices: StartingRootChoice[] = occurrences
    .filter((occurrence) => eligibleNodeIds.has(occurrence.nodeId))
    .map((occurrence, index) => ({
      key: index === 0 ? 'current' : `occurrence:${occurrence.id}`,
      selection: {
        kind: 'authored-occurrence',
        nodeId: occurrence.nodeId,
        occurrenceId: occurrence.id,
      },
      resourceType: occurrence.resourceType,
      relationship: index === 0
        ? occurrence.relationship
        : occurrenceLabel(catalog, occurrence.id, occurrences) ?? occurrence.relationship,
      current: index === 0,
    }));
  const authoredNodeIds = new Set(occurrences.map((occurrence) => occurrence.nodeId));
  const paths = shortestCatalogPaths(catalog, currentRoot.nodeId);
  for (const node of catalog.nodes) {
    if (
      node.nodeId === currentRoot.nodeId ||
      authoredNodeIds.has(node.nodeId) ||
      !eligibleNodeIds.has(node.nodeId)
    ) continue;
    const path = paths.get(node.nodeId);
    if (!path?.edges.length) continue;
    const fullPathLabel = describeEdgePath(catalog, path.edges);
    const pathLabels = path.edges.map((edge) => routeEdgeLabel(catalog, edge));
    if (!fullPathLabel || pathLabels.some((label) => !label)) continue;
    choices.push({
      key: `node:${node.nodeId}`,
      selection: { kind: 'catalog-node', nodeId: node.nodeId },
      resourceType: node.resourceType,
      relationship: path.ambiguous ? undefined : fullPathLabel,
      current: false,
    });
  }
  return choices;
};

export const parseCatalogRootRouteSelection = (
  token: string,
): CatalogRootRouteSelection | undefined => {
  if (!isCatalogRootRouteSelectionToken(token)) return undefined;
  const encoded = token.slice(routeSelectionPrefix.length);
  if (!encoded || !/^[A-Za-z0-9_-]+$/.test(encoded) || encoded.length % 4 === 1) {
    return undefined;
  }
  try {
    const standard = encoded.replace(/-/g, '+').replace(/_/g, '/');
    const binary = atob(standard.padEnd(Math.ceil(standard.length / 4) * 4, '='));
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    const decoded: unknown = JSON.parse(new TextDecoder().decode(bytes));
    if (!decoded || typeof decoded !== 'object' || Array.isArray(decoded)) return undefined;
    const record = decoded as Record<string, unknown>;
    const keys = Object.keys(record).sort();
    if (
      keys.length !== 2 ||
      keys[0] !== 'anchorOccurrenceId' ||
      keys[1] !== 'edgeIds' ||
      typeof record.anchorOccurrenceId !== 'string' ||
      !record.anchorOccurrenceId ||
      !Array.isArray(record.edgeIds) ||
      record.edgeIds.length === 0 ||
      record.edgeIds.some((edgeId) => typeof edgeId !== 'string' || !edgeId)
    ) return undefined;
    return {
      edgeIds: record.edgeIds as string[],
      anchorOccurrenceId: record.anchorOccurrenceId,
    };
  } catch {
    return undefined;
  }
};

const occurrenceLabel = (
  catalog: ExplorerBuilderCatalog,
  occurrenceId: string,
  occurrences: ReturnType<typeof derivedOccurrences>,
): string | undefined => {
  const occurrence = occurrences.find((candidate) => candidate.id === occurrenceId);
  const edgeIds = authoredPathEdgeIds(occurrenceId, occurrences);
  if (!occurrence || !edgeIds) return undefined;
  const edges = edgeIds.map((edgeId) => catalog.edges.find((edge) => edge.edgeId === edgeId));
  if (edges.some((edge) => !edge)) return undefined;
  if (edges.length === 0) return `${occurrence.resourceType} rows`;
  const pathLabel = describeEdgePath(catalog, edges as Array<ExplorerBuilderCatalog['edges'][number]>);
  return pathLabel || undefined;
};

export const resolveCatalogRootRouteSelection = (
  token: string,
  catalog: ExplorerBuilderCatalog,
  table: DraftTable,
  targetNodeId: string,
  snapshotIsCurrent: boolean,
): ResolvedCatalogRootRouteSelection | undefined => {
  if (!snapshotIsCurrent) return undefined;
  const selection = parseCatalogRootRouteSelection(token);
  if (!selection) return undefined;
  const occurrences = derivedOccurrences(table, catalog);
  const root = occurrences[0];
  const anchor = occurrences.find(
    (occurrence) => occurrence.id === selection.anchorOccurrenceId,
  );
  const anchorPath = authoredPathEdgeIds(selection.anchorOccurrenceId, occurrences);
  if (!root || !anchor || !anchorPath || anchorPath.length > selection.edgeIds.length) {
    return undefined;
  }
  if (anchorPath.some((edgeId, index) => selection.edgeIds[index] !== edgeId)) {
    return undefined;
  }

  const edges: Array<ExplorerBuilderCatalog['edges'][number]> = [];
  let currentNodeId = root.nodeId;
  for (const edgeId of selection.edgeIds) {
    const edge = catalog.edges.find((candidate) => candidate.edgeId === edgeId);
    if (!edge || edge.fromNodeId !== currentNodeId || edge.populated === false) {
      return undefined;
    }
    edges.push(edge);
    currentNodeId = edge.toNodeId;
  }
  if (currentNodeId !== targetNodeId) return undefined;
  const target = catalog.nodes.find((node) => node.nodeId === targetNodeId);
  if (!target?.rowRootEligible || !target.populated) return undefined;

  const anchorLabel = occurrenceLabel(catalog, selection.anchorOccurrenceId, occurrences);
  const pathLabel = describeEdgePath(catalog, edges);
  if (!anchorLabel || !pathLabel) return undefined;
  const equivalentAnchors = occurrences.filter((occurrence) => {
    const path = authoredPathEdgeIds(occurrence.id, occurrences);
    return path?.length === anchorPath.length &&
      path.every((edgeId, index) => edgeId === anchorPath[index]);
  });
  const anchorIndex = equivalentAnchors.findIndex(
    (occurrence) => occurrence.id === selection.anchorOccurrenceId,
  );
  const disambiguatedAnchor = equivalentAnchors.length > 1
    ? `${anchorLabel} (existing route ${anchorIndex + 1})`
    : anchorLabel;
  return {
    ...selection,
    edges,
    anchorLabel: disambiguatedAnchor,
    pathLabel,
  };
};
