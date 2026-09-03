import type { ExplorerBuilderCatalog } from '../../../types';
import { derivedOccurrences, type DraftTable } from './model';

/**
 * The graph is allowed to inspect the full catalog, but authoring may only
 * use edges that leave the selected authored occurrence. Keeping this rule pure makes
 * it reusable by the canvas, the explicit route controls, and tests.
 */
export const legalOutgoingEdges = (
  catalog: ExplorerBuilderCatalog,
  table: DraftTable | undefined,
  parentOccurrenceId: string,
): ExplorerBuilderCatalog['edges'] => {
  const occurrences = derivedOccurrences(table, catalog);
  const parent = occurrences.find(
    (occurrence) => occurrence.id === parentOccurrenceId,
  );
  if (!table || !parent) return [];
  const allowsSelfLoops =
    catalog.routePolicy.allowSelfLoops ??
    catalog.routePolicy.selfLoops ??
    false;
  const maxSteps = catalog.routePolicy.maxSteps;
  if (maxSteps && parent.depth + 1 > maxSteps) return [];
  const usedEdgeIds = new Set(
    occurrences.map((occurrence) => occurrence.incomingEdgeId),
  );
  return catalog.edges.filter((edge) => {
    if (edge.fromNodeId !== parent.nodeId) return false;
    if (edge.fromNodeId === edge.toNodeId && !allowsSelfLoops) return false;
    return !usedEdgeIds.has(edge.edgeId);
  });
};

export const replaceableIncomingEdges = (
  catalog: ExplorerBuilderCatalog,
  table: DraftTable | undefined,
  occurrenceId: string,
): ExplorerBuilderCatalog['edges'] => {
  const occurrences = derivedOccurrences(table, catalog);
  const occurrence = occurrences.find((item) => item.id === occurrenceId);
  const parent = occurrences.find((item) => item.id === occurrence?.parentId);
  if (!occurrence || !parent || !occurrence.incomingEdgeId) return [];
  const otherEdgeIds = new Set(
    occurrences
      .filter((item) => item.id !== occurrence.id)
      .map((item) => item.incomingEdgeId),
  );
  return catalog.edges.filter(
    (edge) =>
      edge.fromNodeId === parent.nodeId &&
      edge.toNodeId === occurrence.nodeId &&
      !otherEdgeIds.has(edge.edgeId),
  );
};

export const legalEdgesToNode = (
  catalog: ExplorerBuilderCatalog,
  table: DraftTable | undefined,
  parentOccurrenceId: string,
  nodeId: string | undefined,
): ExplorerBuilderCatalog['edges'] => {
  if (!nodeId) return [];
  return legalOutgoingEdges(catalog, table, parentOccurrenceId).filter(
    (edge) => edge.toNodeId === nodeId,
  );
};

export const isLegalRouteExtension = (
  catalog: ExplorerBuilderCatalog,
  table: DraftTable | undefined,
  parentOccurrenceId: string,
  edgeId: string,
  targetNodeId: string,
): boolean =>
  legalOutgoingEdges(catalog, table, parentOccurrenceId).some(
    (edge) => edge.edgeId === edgeId && edge.toNodeId === targetNodeId,
  );
