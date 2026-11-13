import type { AuthoringNetworkResponse, ExplorerBuilderCatalog, ExplorerBuilderDocument } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { routePath } from '../constructionWorkspace/routeDisplay';
import { constructionHistorySteps, constructionRowMeaning } from '../constructionWorkspace/constructionHistory';

/** A read projection of the complete authoritative document, never a saved canvas. */
export function authoredTraversal(table: DraftTable, catalog: ExplorerBuilderCatalog) {
  const occurrences: Array<{
    id: string; parentId?: string; resourceType: string; relationship?: string;
    edgeId?: string; matchMode: 'OPTIONAL' | 'REQUIRED'; depth: number;
    path: string; catalogNodeId?: string;
  }> = [];
  function visit(route: ExplorerBuilderDocument['route'], parent?: (typeof occurrences)[number]) {
    const rootNodes = !parent ? catalog.nodes.filter((node) => node.resourceType === route.resourceType && node.rowRootEligible) : [];
    const candidates = parent?.catalogNodeId ? catalog.edges.filter((edge) =>
      edge.fromNodeId === parent.catalogNodeId &&
      (route.catalogEdgeId ? edge.edgeId === route.catalogEdgeId : edge.label === route.relationship) &&
      catalog.nodes.some((node) => node.nodeId === edge.toNodeId && node.resourceType === route.resourceType)) : [];
    const edge = candidates.length === 1 ? candidates[0] : undefined;
    const occurrence = {
      id: route.occurrenceId, parentId: parent?.id, resourceType: route.resourceType,
      relationship: route.relationship, edgeId: route.catalogEdgeId ?? edge?.edgeId,
      matchMode: route.matchMode ?? 'OPTIONAL' as const, depth: parent ? parent.depth + 1 : 0,
      path: parent ? `${parent.path} → ${route.relationship ?? 'unspecified relationship'} → ${route.resourceType}` : route.resourceType,
      catalogNodeId: parent ? edge?.toNodeId : rootNodes.length === 1 ? rootNodes[0].nodeId : undefined,
    };
    occurrences.push(occurrence);
    route.children?.forEach((child) => visit(child, occurrence));
  }
  visit(table.document.route);
  return occurrences;
}

export function authoredStages(table: DraftTable, identity?: AuthoringNetworkResponse['rowIdentity']) {
  const sourceColumns = table.document.columns.map((column) => ({ id: column.columnId ?? column.column, name: column.column, label: column.label, type: column.logicalType }));
  const histories = constructionHistorySteps(table.document.construction, sourceColumns);
  return (table.document.construction?.steps ?? []).map((step, index) => {
    const authoritative = identity?.stages.find((stage) => stage.operationId === step.id);
    const history = histories.find((item) => item.id === step.id);
    const traversal = step.operation.kind === 'RELATED_SOURCE' ? step.operation.relatedSource.route
      : step.operation.kind === 'RELATED_EXPAND' ? step.operation.relatedExpand.route
      : step.operation.kind === 'RELATED_ELIGIBILITY' ? step.operation.relatedEligibility.route : undefined;
    const input = step.inputs.map((item) => item.kind === 'STEP_OUTPUT' ? item.stepId : item.kind === 'SOURCE_PROJECTION' ? 'Source' : item.kind === 'WORKSPACE_OUTPUT' ? `Table ${item.outputId}` : `Published ${item.tableId}/${item.revisionId}`).join(', ');
    return { id: step.id, index, kind: step.operation.kind, input,
      traversal: traversal ? routePath(traversal) : undefined, edgeIds: traversal?.map((edge) => edge.edgeId),
      title: history?.title ?? step.operation.kind, summary: history?.summary ?? 'Inspect the saved operation.',
      meaning: authoritative?.meaning, effect: authoritative?.effect, unsupportedReason: authoritative?.unsupportedReason,
      anchorColumnId: authoritative?.identity.anchorColumnId ?? (step.operation.kind === 'RELATED_EXPAND' ? step.operation.relatedExpand.anchorColumnId : step.operation.kind === 'RELATED_SOURCE' ? step.operation.relatedSource.anchorColumnId : step.operation.kind === 'RELATED_ELIGIBILITY' ? step.operation.relatedEligibility.anchorColumnId : undefined),
      rowChanging: ['GROUP', 'CODED_GROUP', 'PIVOT', 'EXPAND', 'RELATED_EXPAND', 'UNPIVOT', 'COMBINE'].includes(step.operation.kind),
      listPreservation: step.operation.kind === 'RELATED_EXPAND',
      missingPolicy: step.operation.kind === 'GROUP' ? step.operation.group.missingKeyPolicy ?? 'GROUP' : step.operation.kind === 'RELATED_EXPAND' ? step.operation.relatedExpand.emptyPolicy : undefined,
    };
  });
}

export function authoredRowMeaning(table: DraftTable) {
  const rows = table.document.rows;
  const source = rows.kind === 'RECORDS' ? `One row per ${table.document.rootResourceType} record.`
    : rows.kind === 'EXPANDED' ? `One row per item in ${rows.expanded.scopePath} on occurrence ${rows.expanded.occurrenceId}; empty collections: ${rows.expanded.emptyCollectionPolicy}.`
    : 'One row per source group; inspect its source row definition.';
  return constructionRowMeaning(source, table.document.construction, table.document.columns.map((column) => ({ id: column.columnId ?? column.column, name: column.column, label: column.label, type: column.logicalType })));
}
