import type { DescribeAuthoringNetworkArgs } from '../src/api';
import type { AuthoringNetworkResponse, ExplorerBuilderDocument } from '../src/types';

// A read-only mock of the ordinary-document adapter. Every occurrence, binding
// and operation comes from the test's current saved document; it supplies no
// inferred imported provenance and no result-row oracle.
export function ordinaryNetworkFixture(document: ExplorerBuilderDocument, args: DescribeAuthoringNetworkArgs): AuthoringNetworkResponse {
  if (document.importedRecipe) throw new Error('Pinned sources require an explicit server-network fixture.');
  type Stage = AuthoringNetworkResponse['rowIdentity']['stages'][number];
  const rows = document.rows ?? { kind: 'RECORDS' as const, records: {} };
  const occurrences: AuthoringNetworkResponse['network']['occurrences'] = [];
  const walk = (node: ExplorerBuilderDocument['route'], parentOccurrenceId?: string, requiredPrefix = true) => {
    const matchMode = node.matchMode ?? 'OPTIONAL';
    occurrences.push({ occurrenceId: node.occurrenceId, parentOccurrenceId, resourceType: node.resourceType,
      label: node.resourceType, labelMissing: false, catalogEdgeId: node.catalogEdgeId, relationship: node.relationship,
      matchMode, enabled: true, preservesRowIdentity: true,
      canExcludeRows: !!parentOccurrenceId && requiredPrefix && matchMode === 'REQUIRED' });
    for (const child of node.children ?? []) walk(child, node.occurrenceId, requiredPrefix && (!parentOccurrenceId || matchMode === 'REQUIRED'));
  };
  walk(document.route);
  const sourceIdentity = (): Stage['identity'] => {
    if (rows.kind === 'EXPANDED') return { kind: 'SOURCE_ITEM', occurrenceId: rows.expanded.occurrenceId,
      scopePath: rows.expanded.scopePath, scope: 'PER_SOURCE_RECORD', missingPolicy: rows.expanded.emptyCollectionPolicy };
    if (rows.kind === 'GROUPS' && rows.groups.source.kind === 'EXPLICIT') return { kind: 'NAMED_GROUP',
      groupRevisionId: rows.groups.source.explicit.revisionId, scope: 'GROUP_REVISION', missingPolicy: rows.groups.source.explicit.unassignedMemberPolicy };
    return { kind: 'RECORD', occurrenceId: document.route.occurrenceId, resourceType: document.rootResourceType, scope: 'SOURCE_RECORD' };
  };
  const stage = (stageId: string, identity: Stage['identity'], meaning: string, effect: Stage['effect'], operationId?: string): Stage => ({
    stageId, operationId, identity, meaning, effect, mayExcludeRows: false, multiplicity: 'UNKNOWN', count: { kind: 'UNKNOWN' },
  });
  const deferredGroup = rows.kind === 'GROUPS' && !!rows.groups.afterStepId;
  const sourceMeaning = !deferredGroup && rows.kind === 'EXPANDED'
    ? 'One row per repeated-field item within its source record.'
    : !deferredGroup && rows.kind === 'GROUPS' ? 'One row per saved named group, including empty groups.'
      : `One row per ${document.rootResourceType} record.`;
  const source = stage('source_projection', deferredGroup
    ? { kind: 'RECORD', occurrenceId: document.route.occurrenceId, resourceType: document.rootResourceType, scope: 'SOURCE_RECORD' }
    : sourceIdentity(), sourceMeaning, 'SOURCE');
  source.mayExcludeRows = occurrences.some((occurrence) => occurrence.canExcludeRows)
    || (!deferredGroup && rows.kind === 'EXPANDED' && rows.expanded.emptyCollectionPolicy === 'EXCLUDE')
    || (!deferredGroup && rows.kind === 'GROUPS' && rows.groups.source.kind === 'EXPLICIT' && rows.groups.source.explicit.unassignedMemberPolicy === 'EXCLUDE');
  const stages: Stage[] = [source];
  const identities = new Map<string, Stage['identity']>([['source_projection', source.identity]]);
  for (const step of document.construction?.steps ?? []) {
    const inputStageId = step.inputs[0]?.kind === 'STEP_OUTPUT' ? step.inputs[0].stepId : 'source_projection';
    const input = identities.get(inputStageId) ?? { kind: 'OPERATION_DEFINED', inputStageId, scope: 'INPUT_STAGE' };
    const current = stage(step.id, input, 'The input row identity is retained.', 'PRESERVE', step.id);
    const operation = step.operation;
    if (operation.kind === 'GROUP') {
      current.identity = { kind: 'KEY_TUPLE', inputStageId, keyColumnIds: (operation.group.keys ?? []).map((key) => key.outputColumnId),
        scope: 'INPUT_STAGE', missingPolicy: operation.group.missingKeyPolicy ?? 'GROUP' };
      current.mayExcludeRows = operation.group.missingKeyPolicy === 'EXCLUDE';
      current.effect = 'GROUP'; current.meaning = 'One row per complete Group key tuple.';
    } else if (operation.kind === 'RELATED_EXPAND') {
      current.identity = { kind: 'RELATED_RECORD_PAIR', inputStageId, anchorColumnId: operation.relatedExpand.anchorColumnId,
        relatedRecordColumnId: operation.relatedExpand.relatedRecordColumnId, resourceType: operation.relatedExpand.targetResourceType,
        scope: 'PER_INPUT_ROW', missingPolicy: operation.relatedExpand.emptyPolicy };
      current.mayExcludeRows = operation.relatedExpand.emptyPolicy === 'EXCLUDE';
      current.effect = 'EXPAND_RELATED'; current.meaning = 'One row per input row and distinct related record; upstream values repeat.';
    } else if (operation.kind === 'EXPAND') {
      current.identity = { kind: 'COLUMN_ITEM', inputStageId, scopePath: operation.expand.inputColumnId, scope: 'PER_INPUT_ROW', missingPolicy: operation.expand.emptyPolicy };
      current.mayExcludeRows = operation.expand.emptyPolicy === 'EXCLUDE';
      current.effect = 'EXPAND_ITEMS'; current.meaning = 'One row per list item within each input row.';
    } else if (operation.kind === 'FILTER' || operation.kind === 'RELATED_ELIGIBILITY') {
      current.effect = 'FILTER'; current.mayExcludeRows = true;
    } else if (!['DERIVE', 'RELATED_SOURCE', 'RELATED_FIELD'].includes(operation.kind)) {
      current.identity = { kind: 'OPERATION_DEFINED', inputStageId, scope: 'INPUT_STAGE' };
      current.effect = 'OPERATION_DEFINED'; current.meaning = 'This operation defines its own row identity.';
      current.unsupportedReason = `Inspect the existing ${operation.kind} operation contract.`;
    }
    identities.set(step.id, current.identity); stages.push(current);
    if (rows.kind === 'GROUPS' && rows.groups.afterStepId === step.id) stages.push(stage('group_rows', { ...sourceIdentity(), inputStageId: step.id }, 'One row per saved named group, including empty groups.', 'GROUP_NAMED'));
  }
  return { snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
    network: { owner: 'DOCUMENT', outputId: document.output.id, sourceRows: rows, occurrences,
      bindings: document.columns.filter((column) => column.columnId).map((column) => ({ columnId: column.columnId!, occurrenceId: column.occurrenceId,
        valueMode: column.source.kind === 'field' ? column.source.field.projectionMode : undefined, status: 'EXACT' })),
      operations: (document.construction?.steps ?? []).map((step) => ({ operationId: step.id, kind: step.operation.kind,
        inputStepIds: step.inputs.flatMap((input) => input.kind === 'STEP_OUTPUT' ? [input.stepId] : []),
        anchorColumnId: step.operation.kind === 'RELATED_SOURCE' ? step.operation.relatedSource.anchorColumnId
          : step.operation.kind === 'RELATED_EXPAND' ? step.operation.relatedExpand.anchorColumnId
            : step.operation.kind === 'RELATED_ELIGIBILITY' ? step.operation.relatedEligibility.anchorColumnId : undefined })) },
    rowIdentity: { outputId: document.output.id, sourceDefinition: rows, stages,
      relationships: occurrences.filter((occurrence) => occurrence.parentOccurrenceId).map((occurrence) => ({
        occurrenceId: occurrence.occurrenceId, catalogEdgeId: occurrence.catalogEdgeId, matchMode: occurrence.matchMode ?? 'OPTIONAL',
        preservesIdentity: true, mayExcludeRows: occurrence.canExcludeRows, multiplicity: 'UNKNOWN' })) } };
}
