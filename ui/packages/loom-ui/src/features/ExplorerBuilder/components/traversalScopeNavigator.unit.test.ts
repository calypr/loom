import { describe, expect, it } from 'vitest';
import type { AuthoringNetworkResponse, ConstructionStep, ConstructionStageDescriptor, ExplorerBuilderDocument } from '../../../types';
import { traversalScopeModel, type NavigatorSourceStep } from './traversalScopeNavigator';

const sourceStep = (id: string, kind: string, parentId?: string, occurrenceId?: string): NavigatorSourceStep => ({
  id, kind, parentId, occurrenceId, title: id, summary: `${id} source operation`, enabled: true, editable: true,
});
const document = (): ExplorerBuilderDocument => ({
  kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
  importedRecipe: { name: 'default', digest: 'a'.repeat(64), translationVersion: 'test', output: 'subjects' },
  route: { occurrenceId: 'base', resourceType: 'ResearchSubject', children: [
    { occurrenceId: 'patient_subject', resourceType: 'Patient', catalogEdgeId: 'subject', relationship: 'subject', children: [
      { occurrenceId: 'observations', resourceType: 'Observation', catalogEdgeId: 'observations', relationship: 'subject' },
    ] },
    { occurrenceId: 'patient_performer', resourceType: 'Patient', catalogEdgeId: 'performer', relationship: 'performer' },
  ] }, rows: { kind: 'RECORDS', records: {} }, columns: [],
  importedSource: { steps: [sourceStep('fields', 'FIELD_GROUP'), sourceStep('patient', 'TRAVERSAL', undefined, 'patient_subject'),
    sourceStep('patient/fields', 'FIELD_GROUP', 'patient'), sourceStep('patient/obs', 'TRAVERSAL', 'patient', 'observations'),
    sourceStep('patient/obs/pivot', 'PIVOT', 'patient/obs'), sourceStep('performer', 'TRAVERSAL', undefined, 'patient_performer'),
    sourceStep('performer/fields', 'FIELD_GROUP', 'performer')] },
});
const network = (): AuthoringNetworkResponse['network'] => ({ owner: 'IMPORTED_SOURCE', outputId: 'subjects', sourceRows: { kind: 'RECORDS', records: {} }, occurrences: [], bindings: [], operations: [] });
const filter = (id: string, input?: string): ConstructionStep => ({ id, inputs: [{ kind: input ? 'STEP_OUTPUT' : 'SOURCE_PROJECTION', ...(input ? { stepId: input } : {}) } as ConstructionStep['inputs'][number]],
  operation: { kind: 'FILTER', filter: { columnId: 'id', operator: 'EXISTS' } }, outputs: [{ id: 'id', name: 'id', label: 'ID', type: 'string' }] });

describe('actual traversal/source ownership', () => {
  it('keeps repeated resource occurrences distinct and uses exact parent traversal identities', () => {
    const model = traversalScopeModel(document());
    expect(model.scopes.filter(scope => scope.resourceType === 'Patient').map(scope => scope.id)).toEqual(['patient_subject', 'patient_performer']);
    expect(model.sourceOperations.map(operation => [operation.id, operation.scopeId, operation.position])).toEqual([
      ['fields', 'base', 1], ['patient', 'patient_subject', 2], ['patient/fields', 'patient_subject', 3],
      ['patient/obs', 'observations', 4], ['patient/obs/pivot', 'observations', 5], ['performer', 'patient_performer', 6], ['performer/fields', 'patient_performer', 7],
    ]);
  });
  it('does not silently assign missing, ambiguous or cyclic source scopes to root', () => {
    const value = document(); value.importedSource!.steps.push(sourceStep('orphan', 'PIVOT', 'missing'), sourceStep('loop', 'FIELD_GROUP', 'loop'), sourceStep('bad-traversal', 'TRAVERSAL', undefined, 'unknown'));
    const invalid = traversalScopeModel(value).sourceOperations.slice(-3);
    expect(invalid.every(operation => !operation.scopeId && operation.scopeReason)).toBe(true);
    value.route.children!.push({ occurrenceId: 'patient_subject', resourceType: 'Patient' });
    expect(traversalScopeModel(value).sourceOperations.find(operation => operation.id === 'patient')?.scopeReason).toContain('ambiguous');
  });
  it('maps fields only through one exact binding and rejects source-scope disagreement', () => {
    const value = document(); value.columns = [{ columnId: 'value', column: 'value', label: 'Value', logicalType: 'integer', occurrenceId: 'base', source: { kind: 'imported', imported: { outputColumn: 'value' } } }];
    const actual = network(); actual.bindings = [{ columnId: 'value', occurrenceId: 'observations', sourceStepId: 'patient/obs/pivot', status: 'EXACT' }];
    expect(traversalScopeModel(value, actual).columns[0]).toMatchObject({ scopeId: 'observations', sourceStepId: 'patient/obs/pivot' });
    actual.bindings[0].sourceStepId = 'performer/fields';
    expect(traversalScopeModel(value, actual).columns[0].unavailableReason).toContain('disagree');
    actual.bindings.push({ ...actual.bindings[0] });
    expect(traversalScopeModel(value, actual).columns[0].scopeId).toBeUndefined();
    actual.outputId = 'other';
    expect(traversalScopeModel(value, actual).columns[0].scopeId).toBeUndefined();
  });
  it('never mutates saved order or inputs when projecting the construction sequence', () => {
    const value = document(); value.construction = { version: 1, steps: [filter('earlier'), filter('later', 'earlier')] };
    const before = JSON.stringify(value); const model = traversalScopeModel(value);
    expect(model.constructionOperations.map(operation => [operation.id, operation.position, operation.inputs])).toEqual([
      ['earlier', 1, ['Source projection']], ['later', 2, ['earlier']],
    ]);
    expect(model.constructionOperations.every(operation => !operation.scopeId && operation.scopeReason?.includes('resulting table'))).toBe(true);
    expect(JSON.stringify(value)).toBe(before);
  });
  it('maps the actual producer’s related-source catalog node through its saved anchor and route', () => {
    const { value, stages } = relatedSummaryFixture();
    const operation = traversalScopeModel(value, undefined, undefined, stages).constructionOperations[0];
    expect(operation).toMatchObject({ scopeId: 'observations', anchorColumnId: 'saved-anchor', inputs: ['Source projection'] });
    expect(value.construction!.steps[0].operation.kind === 'RELATED_SOURCE' && value.construction!.steps[0].operation.relatedSource.sourceOccurrenceId).toBe('catalog-observation');
    expect(operation.scopeId).not.toBe('catalog-observation');
  });
});

it('uses exact input-stage and active-match provenance for expansion and per-match values', () => {
  const value = document();
  const route = [
    { edgeId: 'subject', fromNodeId: 'root-node', toNodeId: 'patient-node', fromResourceType: 'ResearchSubject', toResourceType: 'Patient', relationship: 'subject', storageDirection: 'OUTBOUND' as const, matchMode: 'OPTIONAL' as const },
    { edgeId: 'observations', fromNodeId: 'patient-node', toNodeId: 'observation-node', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject', storageDirection: 'INBOUND' as const, matchMode: 'OPTIONAL' as const },
  ];
  value.construction = { version: 1, steps: [
    { id: 'expand', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'RELATED_EXPAND', relatedExpand: {
      anchorColumnId: 'root-key', choiceId: 'explicit', targetNodeId: 'observation-node', targetResourceType: 'Observation', route,
      contributorRule: { policy: 'ALL_MATCHES' }, emptyPolicy: 'PRESERVE_PARENT', relatedRecordColumnId: 'active-observation',
    } }, outputs: [{ id: 'active-observation', name: 'active_observation', label: 'Observation', type: 'string' }] },
    { id: 'per-match', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'expand' }], operation: { kind: 'RELATED_FIELD', relatedField: {
      choiceId: 'field', source: { kind: 'FIELD', candidateId: 'value', nodeId: 'observation-node', resourceType: 'Observation', path: 'valueInteger', cardinality: 'optional_one', logicalType: 'integer' }, outputColumnId: 'scalar',
    } }, outputs: [{ id: 'scalar', name: 'scalar', label: 'Scalar', type: 'integer' }] },
  ] };
  const identity: AuthoringNetworkResponse['rowIdentity'] = { outputId: 'subjects', sourceDefinition: value.rows, relationships: [], stages: [{
    stageId: 'source_projection', identity: { kind: 'RECORD', occurrenceId: 'base', scope: 'SOURCE_RECORD' }, meaning: 'One subject', effect: 'SOURCE',
    mayExcludeRows: false, multiplicity: 'UNKNOWN', count: { kind: 'UNKNOWN' },
  }] };
  const stages: ConstructionStageDescriptor[] = [{ id: 'source_projection', inputStageId: '', columns: [], capabilities: [], relatedExpandAnchors: [{ anchorColumnId: 'root-key', kind: 'root', resourceType: 'ResearchSubject', label: 'Starting subject' }] }];
  expect(traversalScopeModel(value, undefined, identity, stages).constructionOperations.map(operation => operation.scopeId)).toEqual(['observations', 'observations']);
  expect(traversalScopeModel(value, undefined, identity).constructionOperations.every(operation => !operation.scopeId)).toBe(true);
  const wrong = structuredClone(identity); wrong.outputId = 'other';
  expect(traversalScopeModel(value, undefined, wrong, stages).constructionOperations.every(operation => !operation.scopeId)).toBe(true);
  const field = value.construction.steps[1].operation;
  if (field.kind === 'RELATED_FIELD') field.relatedField.source.nodeId = 'different-occurrence-node';
  expect(traversalScopeModel(value, undefined, identity, stages).constructionOperations[1].scopeReason).toContain('active-match provenance');
});

it('rejects a source parent link that points to a non-traversal or a different occurrence branch', () => {
  const value = document(); value.importedSource!.steps[2].parentId = 'fields';
  expect(traversalScopeModel(value).sourceOperations[2].scopeReason).toContain('does not identify a traversal');
  value.importedSource!.steps[3].parentId = 'performer';
  expect(traversalScopeModel(value).sourceOperations[3].scopeReason).toContain('disagrees');
});


function relatedSummaryFixture(): { value: ExplorerBuilderDocument; stages: ConstructionStageDescriptor[] } {
  const value = document();
  value.construction = { version: 1, steps: [{ id: 'summary', inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: { kind: 'RELATED_SOURCE', relatedSource: { anchorColumnId: 'saved-anchor', choiceId: 'choice', sourceOccurrenceId: 'catalog-observation',
      source: { kind: 'FIELD', candidateId: 'field', nodeId: 'catalog-observation', resourceType: 'Observation', path: 'valueInteger', cardinality: 'optional_one', logicalType: 'integer' },
      route: [
        { edgeId: 'subject', fromNodeId: 'catalog-root', toNodeId: 'catalog-patient', fromResourceType: 'ResearchSubject', toResourceType: 'Patient', relationship: 'subject', storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL' },
        { edgeId: 'observations', fromNodeId: 'catalog-patient', toNodeId: 'catalog-observation', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject', storageDirection: 'INBOUND', matchMode: 'OPTIONAL' },
      ], contributorRule: { policy: 'ALL_MATCHES' }, form: 'COUNT', outputColumnId: 'count' } },
    outputs: [{ id: 'count', name: 'count', label: 'Count', type: 'integer' }] }] };
  const stages: ConstructionStageDescriptor[] = [{ id: 'source_projection', inputStageId: '', columns: [], capabilities: [],
    relatedExpandAnchors: [{ anchorColumnId: 'saved-anchor', kind: 'root', resourceType: 'ResearchSubject', nodeId: 'catalog-root', label: 'Starting subjects' }] }];
  return { value, stages };
}

it('fails closed for mismatched catalog nodes, routes, targets, anchors and input stages', () => {
  const controls: Array<(fixture: ReturnType<typeof relatedSummaryFixture>) => void> = [
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') op.relatedSource.sourceOccurrenceId = 'observations'; },
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') op.relatedSource.route[1].fromNodeId = 'other-parent'; },
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') { op.relatedSource.source.nodeId = 'other-target'; op.relatedSource.sourceOccurrenceId = 'other-target'; } },
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') op.relatedSource.source.resourceType = 'Condition'; },
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') op.relatedSource.route[1].edgeId = 'other-edge'; },
    ({ value }) => { const op = value.construction!.steps[0].operation; if (op.kind === 'RELATED_SOURCE') op.relatedSource.route = []; },
    ({ stages }) => { stages[0].relatedExpandAnchors![0].nodeId = 'other-root'; },
    ({ stages }) => { stages[0].relatedExpandAnchors![0].anchorColumnId = 'other-anchor'; },
    ({ stages }) => { stages[0].id = 'other-input-stage'; },
    ({ stages }) => { stages.push(structuredClone(stages[0])); },
    ({ value }) => { value.construction!.steps[0].inputs = [{ kind: 'STEP_OUTPUT', stepId: 'other-stage' }]; },
    ({ value }) => { value.route.children![0].children!.push({ occurrenceId: 'another-observation', resourceType: 'Observation', catalogEdgeId: 'observations', relationship: 'subject' }); },
  ];
  for (const change of controls) {
    const fixture = relatedSummaryFixture(); change(fixture);
    const operation = traversalScopeModel(fixture.value, undefined, undefined, fixture.stages).constructionOperations[0];
    expect(operation.scopeId).toBeUndefined(); expect(operation.scopeReason).toBeTruthy();
  }
});

it('does not merge a same-type traversal reached through a different parent path', () => {
  const { value, stages } = relatedSummaryFixture();
  value.route.children![1].children = [{ occurrenceId: 'performer-observation', resourceType: 'Observation', catalogEdgeId: 'observations', relationship: 'subject' }];
  expect(traversalScopeModel(value, undefined, undefined, stages).constructionOperations[0].scopeId).toBe('observations');
});
