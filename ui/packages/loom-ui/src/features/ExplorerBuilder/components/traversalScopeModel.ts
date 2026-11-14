import type { AuthoringNetworkResponse, ExplorerBuilderDocument, ConstructionStep, ConstructionStageDescriptor, ConstructionRouteStep } from '../../../types';
import { constructionHistorySteps } from '../constructionWorkspace/constructionHistory';

export type NavigatorSourceStep = NonNullable<ExplorerBuilderDocument['importedSource']>['steps'][number];
export interface TraversalScope {
  readonly id: string;
  readonly parentId?: string;
  readonly resourceType: string;
  readonly relationship?: string;
  readonly edgeId?: string;
  readonly storageDirection?: string;
  readonly path: string;
  readonly depth: number;
}
export interface ScopedOperation {
  readonly id: string;
  readonly kind: 'source' | 'construction';
  /** Original array position, retained even when the selected scope hides other entries. */
  readonly position: number;
  readonly title: string;
  readonly summary: string;
  readonly scopeId?: string;
  readonly scopeReason?: string;
  readonly enabled: boolean;
  readonly editable: boolean;
  readonly inputs: ReadonlyArray<string>;
  readonly sourceStep?: NavigatorSourceStep;
  readonly constructionStep?: ConstructionStep;
  readonly anchorColumnId?: string;
}
export interface ScopedColumn {
  readonly id: string;
  readonly label: string;
  readonly scopeId?: string;
  readonly sourceStepId?: string;
  readonly unavailableReason?: string;
}
export interface TraversalScopeModel {
  readonly scopes: ReadonlyArray<TraversalScope>;
  readonly sourceOperations: ReadonlyArray<ScopedOperation>;
  readonly constructionOperations: ReadonlyArray<ScopedOperation>;
  readonly columns: ReadonlyArray<ScopedColumn>;
}

type ScopeResolution = { scopeId: string; catalogNodeId?: string; scopeReason?: never } | { scopeId?: never; scopeReason: string };

/** Read-only ownership projection. Source-plan positions are not compiler phase numbers. */
export function traversalScopeModel(document: ExplorerBuilderDocument,
  network?: AuthoringNetworkResponse['network'], rowIdentity?: AuthoringNetworkResponse['rowIdentity'], constructionStages?: ReadonlyArray<ConstructionStageDescriptor>): TraversalScopeModel {
  network = network?.outputId === document.output.id ? network : undefined;
  const wrongIdentityOutput = rowIdentity !== undefined && rowIdentity.outputId !== document.output.id;
  const scopes: TraversalScope[] = [];
  const visit = (route: ExplorerBuilderDocument['route'], parent?: TraversalScope) => {
    const scope = { id: route.occurrenceId, parentId: parent?.id, resourceType: route.resourceType,
      relationship: route.relationship, edgeId: route.catalogEdgeId, storageDirection: network?.occurrences.filter(item => item.occurrenceId === route.occurrenceId).length === 1 ? network.occurrences.find(item => item.occurrenceId === route.occurrenceId)?.storageDirection : undefined,
      path: parent ? `${parent.path} → ${route.relationship ?? 'Relationship unavailable'} → ${route.resourceType}` : route.resourceType,
      depth: parent ? parent.depth + 1 : 0 };
    scopes.push(scope); route.children?.forEach(child => visit(child, scope));
  };
  visit(document.route);
  const resolveOccurrence = (id: string | undefined): ScopeResolution => {
    if (!id) return { scopeReason: 'The saved operation has no occurrence identity.' };
    const matches = scopes.filter(scope => scope.id === id);
    return matches.length === 1 ? { scopeId: id } : { scopeReason: matches.length ? 'The saved occurrence identity is ambiguous.' : 'The saved occurrence is not present in this authored traversal.' };
  };
  const steps = document.importedSource?.steps ?? [];
  const sourceScope = (step: NavigatorSourceStep, seen = new Set<string>()): ScopeResolution => {
    if (seen.has(step.id)) return { scopeReason: 'The source parent chain contains a cycle.' };
    if (steps.filter(item => item.id === step.id).length !== 1) return { scopeReason: 'The source operation identity is ambiguous.' };
    seen.add(step.id);
    const parentScope = (): ScopeResolution => {
      if (!step.parentId) return resolveOccurrence(document.route.occurrenceId);
      const parents = steps.filter(item => item.id === step.parentId);
      if (parents.length !== 1) return { scopeReason: parents.length ? 'The source parent identity is ambiguous.' : 'The source parent operation is unavailable.' };
      if (parents[0].kind !== 'TRAVERSAL') return { scopeReason: 'The source parent does not identify a traversal scope.' };
      return sourceScope(parents[0], seen);
    };
    if (step.kind !== 'TRAVERSAL') return parentScope();
    const own = resolveOccurrence(step.occurrenceId);
    if (!own.scopeId) return own;
    const parent = parentScope();
    if (!parent.scopeId) return parent;
    return scopes.find(scope => scope.id === own.scopeId)?.parentId === parent.scopeId ? own
      : { scopeReason: 'The source traversal parent disagrees with the authored occurrence path.' };
  };
  const sourceOperations = steps.map((step, index): ScopedOperation => ({
    id: step.id, kind: 'source', position: index + 1, title: step.title, summary: step.summary,
    ...sourceScope(step), enabled: step.enabled, editable: step.editable, inputs: step.parentId ? [step.parentId] : [], sourceStep: step,
  }));
  const columns = document.columns.map((column): ScopedColumn => {
    const id = column.columnId ?? column.column;
    if (!document.importedRecipe) {
      const resolved = resolveOccurrence(column.occurrenceId);
      return { id, label: column.label || column.column, scopeId: resolved.scopeId, unavailableReason: resolved.scopeReason };
    }
    const bindings = network?.bindings.filter(binding => binding.columnId === id && binding.status === 'EXACT') ?? [];
    if (bindings.length !== 1) return { id, label: column.label || column.column,
      unavailableReason: network ? 'The imported field has no unique exact source binding.' : 'Exact imported source bindings have not loaded.' };
    const binding = bindings[0]; const scope = resolveOccurrence(binding.occurrenceId);
    const source = sourceOperations.filter(operation => operation.id === binding.sourceStepId);
    const reason = scope.scopeReason ?? (source.length !== 1 ? 'The field’s source operation identity is unavailable or ambiguous.'
      : source[0].scopeId !== scope.scopeId ? 'The field binding and source operation disagree on traversal scope.' : undefined);
    return { id, label: column.label || column.column, scopeId: reason ? undefined : scope.scopeId,
      sourceStepId: reason ? undefined : binding.sourceStepId, unavailableReason: reason };
  });
  const construction = document.construction?.steps ?? [];
  const history = constructionHistorySteps(document.construction, document.columns.map(column => ({
    id: column.columnId ?? column.column, name: column.column, label: column.label, type: column.logicalType,
  })));
  const inputStage = (step: ConstructionStep) => step.inputs.map(input => input.kind === 'SOURCE_PROJECTION' ? 'Source projection'
    : input.kind === 'STEP_OUTPUT' ? input.stepId : input.kind === 'WORKSPACE_OUTPUT' ? `Table ${input.outputId}` : `Published ${input.tableId}/${input.revisionId}`);
  const anchorOccurrence = (step: ConstructionStep, anchor: string): ScopeResolution => {
    if (wrongIdentityOutput) return { scopeReason: 'The input-stage identity belongs to another output.' };
    const input = step.inputs.length === 1 ? step.inputs[0] : undefined;
    const stageId = input?.kind === 'SOURCE_PROJECTION' ? 'source_projection' : input?.kind === 'STEP_OUTPUT' ? input.stepId : undefined;
    const stages = constructionStages?.filter(stage => stage.id === stageId) ?? [];
    if (stages.length > 1) return { scopeReason: 'The operation input-stage metadata is ambiguous.' };
    const anchors = stages[0]?.relatedExpandAnchors?.filter(item => item.anchorColumnId === anchor) ?? [];
    if (anchors.length > 1) return { scopeReason: 'The saved anchor has ambiguous input-stage provenance.' };
    if (anchors.length === 1 && (anchors[0].kind === 'root' || anchors[0].kind === 'rootContributors') && anchors[0].resourceType === document.rootResourceType) {
      const root = resolveOccurrence(document.route.occurrenceId);
      return root.scopeId ? { ...root, catalogNodeId: anchors[0].nodeId } : root;
    }
    if (input?.kind !== 'SOURCE_PROJECTION') return { scopeReason: 'The saved anchor’s current input-stage occurrence is unavailable.' };
    const bindings = network?.bindings.filter(binding => binding.columnId === anchor && binding.status === 'EXACT') ?? [];
    if (bindings.length === 1) return resolveOccurrence(bindings[0].occurrenceId);
    return { scopeReason: bindings.length > 1 ? 'The saved anchor has ambiguous occurrence bindings.' : 'The saved anchor has no exact source occurrence or stage provenance.' };
  };
  const routeScope = (start: ScopeResolution, route: ReadonlyArray<ConstructionRouteStep>, target: { nodeId: string; resourceType: string }): ScopeResolution => {
    if (!start.scopeId) return start;
    if (route.length === 0) return { scopeReason: 'The saved related operation has no route to its catalog target.' };
    let current = start.scopeId;
    let catalogNodeId = start.catalogNodeId;
    for (const edge of route) {
      const source = scopes.find(scope => scope.id === current);
      if (source?.resourceType !== edge.fromResourceType || (catalogNodeId && edge.fromNodeId !== catalogNodeId)) {
        return { scopeReason: 'The saved route has inconsistent source-node or resource-type provenance.' };
      }
      const matches = scopes.filter(scope => scope.parentId === current && scope.edgeId === edge.edgeId &&
        scope.resourceType === edge.toResourceType && (!scope.relationship || scope.relationship === edge.relationship) &&
        (!scope.storageDirection || scope.storageDirection === edge.storageDirection));
      if (matches.length !== 1) return { scopeReason: 'The saved operation route does not identify one authored occurrence.' };
      current = matches[0].id;
      catalogNodeId = edge.toNodeId;
    }
    if (catalogNodeId !== target.nodeId || scopes.find(scope => scope.id === current)?.resourceType !== target.resourceType) {
      return { scopeReason: 'The saved route terminal disagrees with the operation’s catalog target.' };
    }
    return resolveOccurrence(current);
  };
  const constructionScope = (step: ConstructionStep): ScopeResolution => {
    const operation = step.operation;
    if (operation.kind === 'RELATED_SOURCE') {
      const related = operation.relatedSource;
      // The existing producer/compiler contract uses a catalog node here, despite this legacy field name.
      if (related.sourceOccurrenceId !== related.source.nodeId) return { scopeReason: 'The related-source catalog node identities disagree.' };
      return routeScope(anchorOccurrence(step, related.anchorColumnId), related.route, related.source);
    }
    if (operation.kind === 'RELATED_EXPAND') return routeScope(anchorOccurrence(step, operation.relatedExpand.anchorColumnId), operation.relatedExpand.route, { nodeId: operation.relatedExpand.targetNodeId, resourceType: operation.relatedExpand.targetResourceType });
    if (operation.kind === 'RELATED_ELIGIBILITY') return routeScope(anchorOccurrence(step, operation.relatedEligibility.anchorColumnId), operation.relatedEligibility.route, { nodeId: operation.relatedEligibility.targetNodeId, resourceType: operation.relatedEligibility.targetResourceType });
    if (operation.kind === 'RELATED_FIELD') {
      const input = step.inputs.length === 1 ? step.inputs[0] : undefined;
      const parents = input?.kind === 'STEP_OUTPUT' ? construction.filter(candidate => candidate.id === input.stepId) : [];
      if (parents.length === 1 && parents[0].operation.kind === 'RELATED_EXPAND' &&
          parents[0].operation.relatedExpand.targetNodeId === operation.relatedField.source.nodeId &&
          parents[0].operation.relatedExpand.targetResourceType === operation.relatedField.source.resourceType) {
        const expansion = parents[0].operation.relatedExpand;
        return routeScope(anchorOccurrence(parents[0], expansion.anchorColumnId), expansion.route, { nodeId: expansion.targetNodeId, resourceType: expansion.targetResourceType });
      }
      return { scopeReason: 'The related field’s active-match provenance does not prove one authored occurrence.' };
    }
    return { scopeReason: 'This operation transforms the resulting table rather than one traversal branch.' };
  };
  const constructionOperations = construction.map((step, index): ScopedOperation => {
    const descriptor = history.find(item => item.id === step.id);
    const op = step.operation;
    return { id: step.id, kind: 'construction', position: index + 1,
      title: descriptor?.title ?? op.kind, summary: descriptor?.summary ?? `Inspect the saved ${op.kind} operation.`,
      ...constructionScope(step), enabled: true, editable: descriptor?.editable ?? false, inputs: inputStage(step), constructionStep: step,
      anchorColumnId: op.kind === 'RELATED_SOURCE' ? op.relatedSource.anchorColumnId : op.kind === 'RELATED_EXPAND' ? op.relatedExpand.anchorColumnId : op.kind === 'RELATED_ELIGIBILITY' ? op.relatedEligibility.anchorColumnId : undefined };
  });
  return { scopes, sourceOperations, constructionOperations, columns };
}
