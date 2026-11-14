import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertComplementaryExpandedRows, assertComplementaryEquivalence, resolveComplementaryCatalogEdge, resolveGraphRowsGroupPreviewKeys } from '../graph-rows-complementary-oracle.mjs';

const columns = { rootID: 'subject_id', relatedID: 'observation_id', patientID: 'patient_id', status: 'subject_status', list: 'existing_values', scalar: 'per_match_value' };
const rows = [
  ['rs-one', 'obs-one', 'p-one', 'active', [7], 7],
  ['rs-many', 'obs-many-A', 'p-many', 'active', [11, 13], 11],
  ['rs-many', 'obs-many-B', 'p-many', 'active', [11, 13], 13],
  ['rs-shared', 'obs-many-A', 'p-many', 'completed', [11, 13], 11],
  ['rs-shared', 'obs-many-B', 'p-many', 'completed', [11, 13], 13],
];
const preview = (keepMissing = false) => ({ sampled: false, rows: [...rows,
  ...(keepMissing ? [['rs-zero', null, 'p-zero', 'active', [], null]] : []),
].map(values => Object.fromEntries(Object.values(columns).map((name, index) => [name, structuredClone(values[index])]))).reverse() });

test('public result adapter proves all literal pairs with unchanged lists and explicit scalars', () => {
  assert.equal(assertComplementaryExpandedRows(preview(), columns).length, 5);
  assert.equal(assertComplementaryExpandedRows(preview(true), columns, { keepMissing: true }).length, 6);
});

test('complete result and explicit scalar column are required', () => {
  assert.throws(() => assertComplementaryExpandedRows({ ...preview(), sampled: true }, columns), /complete result/);
  assert.throws(() => assertComplementaryExpandedRows(preview(), { ...columns, scalar: columns.list }), /own column identity/);
  const missing = preview(); delete missing.rows[0].per_match_value;
  assert.throws(() => assertComplementaryExpandedRows(missing, columns), /Missing returned scalar/);
});

test('adapter rejects collapsing shared record pairs, implicit products and replacing lists by scalar values', () => {
  const sharedLost = preview(); sharedLost.rows.pop();
  assert.throws(() => assertComplementaryExpandedRows(sharedLost, columns), /Exact graph-rows/);
  const product = preview(); product.rows.push(structuredClone(product.rows[0]));
  assert.throws(() => assertComplementaryExpandedRows(product, columns), /Exact graph-rows/);
  const scalarized = preview(); scalarized.rows[0].existing_values = 13;
  assert.throws(() => assertComplementaryExpandedRows(scalarized, columns), /Exact graph-rows/);
});

test('missing parent policy, missing scalar and empty list remain distinct', () => {
  assert.throws(() => assertComplementaryExpandedRows(preview(true), columns), /Exact graph-rows/);
  assert.throws(() => assertComplementaryExpandedRows(preview(), columns, { keepMissing: true }), /Exact graph-rows/);
  const missing = preview(true); missing.rows[0].existing_values = null;
  assert.throws(() => assertComplementaryExpandedRows(missing, columns, { keepMissing: true }), /Exact graph-rows/);
});


const equivalentReports = () => {
  const source = { files: { 'ui/editor.tsx': 'source-hash' }, sourceFingerprint: 'a'.repeat(64),
    api: { liveSource: 'b'.repeat(64), runningBinary: 'c'.repeat(64) }, liveBuildLimits: ['2', '-p=1'],
    runtimeComposeConfigurationSHA256: 'config', runtimeConfigurationSHA256: 'air', recipeReferenceSHA256: 'pin' };
  const semantics = { connection: { parent: 'ResearchSubject', relationship: 'study', resourceType: 'ResearchStudy', storageDirection: 'OUTBOUND', matchMode: 'REQUIRED', fields: [{ fieldPath: 'ResearchStudy.id', valueMode: 'ALL' }] },
    orderedConstruction: ['existing-first', 'existing-second'], originalSourceSettings: [{ valueMode: 'FIRST' }], exactRows: [{ identity: ['rs-one'], studyIds: ['study-one'] }] };
  return ['graph-first', 'row-first'].map((mode, index) => {
    const catalog = catalogMetadata(index);
    const edge = resolveComplementaryCatalogEdge(catalog, `edge-${index}`);
    return { status: 'passed', complementaryEntryMode: mode,
      before: structuredClone(source), after: structuredClone(source),
      complementaryCanonicalSemantics: { ...structuredClone(semantics), connection: { ...structuredClone(semantics.connection), catalogEdge: edge.semantic } },
      complementaryIdentityNormalization: { catalogEdge: edge.mapping, occurrence: { actual: `project_${index}`, semanticPath: ['ResearchSubject', 'study', 'ResearchStudy'] } } };
  });
};

test('whole native equivalence permits distinct generated identities but preserves policies, values and ordered operations', () => {
  const [graph, row] = equivalentReports();
  assertComplementaryEquivalence(graph, row);
  row.complementaryCanonicalSemantics.connection.fields[0].valueMode = 'FIRST';
  assert.throws(() => assertComplementaryEquivalence(graph, row), /canonical policies/);
  const changedOrder = equivalentReports(); changedOrder[1].complementaryCanonicalSemantics.orderedConstruction.reverse();
  assert.throws(() => assertComplementaryEquivalence(...changedOrder), /canonical policies/);
  const changedValue = equivalentReports(); changedValue[1].complementaryCanonicalSemantics.exactRows[0].studyIds = [];
  assert.throws(() => assertComplementaryEquivalence(...changedValue), /canonical policies/);
});

test('partial, wrong-entry and stale-source native reports cannot establish equivalence', () => {
  const partial = equivalentReports(); partial[1].status = 'failed';
  assert.throws(() => assertComplementaryEquivalence(...partial), /entire row-first/);
  const wrongEntry = equivalentReports(); wrongEntry[1].complementaryEntryMode = 'graph-first';
  assert.throws(() => assertComplementaryEquivalence(...wrongEntry));
  const changed = equivalentReports(); changed[1].after.files['ui/editor.tsx'] = 'changed';
  assert.throws(() => assertComplementaryEquivalence(...changed), /Source changed/);
  const different = equivalentReports(); different[1].before.api.runningBinary = 'd'.repeat(64); different[1].after.api.runningBinary = 'd'.repeat(64);
  assert.throws(() => assertComplementaryEquivalence(...different));
});

// Minimal literal metadata from the real default fixture: only traversals have
// occurrenceId; the numeric projection is identified by its exact column binding.
const sourceMetadata = () => ({
  document: { columns: [{ columnId: 'numeric-column', source: { imported: { outputColumn: 'patient__observation__valueInteger' } } }], importedSource: { steps: [
    { id: 'observation-traversal', occurrenceId: 'observation-occurrence', kind: 'TRAVERSAL', editable: true },
    { id: 'numeric-projection', parentId: 'observation-traversal', kind: 'CATALOG_PROJECTION', editable: true, valueMode: 'FIRST' },
  ] } },
  network: { occurrences: [{ occurrenceId: 'observation-occurrence', resourceType: 'Observation', sourceStepId: 'observation-traversal' }], bindings: [
    { columnId: 'numeric-column', occurrenceId: 'observation-occurrence', sourceStepId: 'numeric-projection', status: 'EXACT', valueMode: 'FIRST' },
  ] },
});

test('Observation source edit follows exact binding provenance without a projection occurrenceId', async () => {
  const { resolveGraphRowsObservationSource } = await import('../../workflows/graph-rows-mixed.mjs');
  const { document, network } = sourceMetadata();
  const result = resolveGraphRowsObservationSource(document, network);
  assert.equal(result.step, document.importedSource.steps[1]);
  assert.equal(result.column, document.columns[0]);
  assert.deepEqual(result.affectedColumnIds, ['numeric-column']);
  assert.equal(result.step.occurrenceId, undefined);
});

test('Observation source edit rejects absent, ambiguous, unknown and mismatched provenance', async () => {
  const { resolveGraphRowsObservationSource } = await import('../../workflows/graph-rows-mixed.mjs');
  const controls = [
    x => { x.network.bindings = []; },
    x => { x.network.bindings.push(structuredClone(x.network.bindings[0])); },
    x => { x.network.bindings[0].status = 'UNSUPPORTED'; },
    x => { x.network.bindings[0].sourceStepId = 'unknown'; },
    x => { x.network.bindings[0].occurrenceId = 'other-occurrence'; },
    x => { x.document.importedSource.steps[1].parentId = 'other-traversal'; },
    x => { x.document.importedSource.steps[1].kind = 'PIVOT'; },
    x => { x.document.importedSource.steps[1].editable = false; },
    x => { x.document.importedSource.steps[1].valueMode = 'ALL'; },
    x => { x.network.bindings[0].valueMode = 'ALL'; },
    x => { x.document.importedSource.steps.push(structuredClone(x.document.importedSource.steps[1])); },
  ];
  for (const mutate of controls) {
    const fixture = sourceMetadata(); mutate(fixture);
    assert.throws(() => resolveGraphRowsObservationSource(fixture.document, fixture.network));
  }
});


const catalogMetadata = index => ({ snapshotToken: `snapshot-${index}`, nodes: [
  { nodeId: `root-${index}`, resourceType: 'ResearchSubject', rowGrain: 'study_enrollment', rowRootEligible: true, populated: true, documentCount: 4 },
  { nodeId: `study-${index}`, resourceType: 'ResearchStudy', rowGrain: 'resource', rowRootEligible: true, populated: true, documentCount: 2 },
], edges: [{ edgeId: `edge-${index}`, fromNodeId: `root-${index}`, toNodeId: `study-${index}`, label: 'study', storageDirection: 'OUTBOUND', populated: true }] });

test('catalog identities normalize only after unique endpoint and relationship correspondence', () => {
  const a = resolveComplementaryCatalogEdge(catalogMetadata(0), 'edge-0');
  const b = resolveComplementaryCatalogEdge(catalogMetadata(1), 'edge-1');
  assert.deepEqual(a.semantic, b.semantic);
  assert.notEqual(a.mapping.actualEdgeId, b.mapping.actualEdgeId);
  assert.notEqual(a.mapping.actualFromNodeId, b.mapping.actualFromNodeId);
  assert.equal(a.mapping.catalog.nodes[0].documentCount, 4);
  assert.equal(a.semantic.from.rowGrain, 'study_enrollment');
  const changed = catalogMetadata(2); changed.nodes[0].sourceKind = 'construction'; changed.nodes[0].role = 'stage';
  assert.notDeepEqual(resolveComplementaryCatalogEdge(changed, 'edge-2').semantic, a.semantic);
});

test('catalog mapping rejects absent and ambiguous edges or same-role node aliases', () => {
  const controls = [
    x => { x.edges = []; },
    x => { x.edges[0].edgeId = 'unknown'; },
    x => { x.edges.push(structuredClone(x.edges[0])); },
    x => { x.edges.push({ ...x.edges[0], edgeId: 'alias' }); },
    x => { x.nodes.push({ ...x.nodes[0], nodeId: 'alias', documentCount: 9 }); },
    x => { x.nodes.push(structuredClone(x.nodes[0])); },
    x => { x.nodes = x.nodes.slice(1); },
    x => { x.edges[0].toNodeId = 'unknown'; },
  ];
  for (const mutate of controls) { const catalog = catalogMetadata(0); mutate(catalog); assert.throws(() => resolveComplementaryCatalogEdge(catalog, 'edge-0')); }
});

test('whole equivalence rejects changed relationship, direction, endpoint type, grain and role', () => {
  const controls = [
    x => { x.edges[0].label = 'other'; },
    x => { x.edges[0].storageDirection = 'INBOUND'; },
    x => { x.nodes[1].resourceType = 'Observation'; },
    x => { x.nodes[0].rowGrain = 'resource'; },
    x => { x.nodes[0].sourceKind = 'construction'; },
    x => { x.nodes[0].role = 'stage'; },
  ];
  for (const mutate of controls) {
    const reports = equivalentReports(); const catalog = reports[1].complementaryIdentityNormalization.catalogEdge.catalog; mutate(catalog);
    const edge = resolveComplementaryCatalogEdge(catalog, 'edge-1'); reports[1].complementaryIdentityNormalization.catalogEdge = edge.mapping;
    reports[1].complementaryCanonicalSemantics.connection.catalogEdge = edge.semantic;
    const connection = reports[1].complementaryCanonicalSemantics.connection;
    Object.assign(connection, { parent: edge.semantic.from.resourceType, resourceType: edge.semantic.to.resourceType, relationship: edge.semantic.label, storageDirection: edge.semantic.storageDirection });
    reports[1].complementaryIdentityNormalization.occurrence.semanticPath = [connection.parent, connection.relationship, connection.resourceType];
    assert.throws(() => assertComplementaryEquivalence(...reports), /canonical policies/);
  }
  const mismatch = equivalentReports(); mismatch[1].complementaryIdentityNormalization.catalogEdge.actualFromNodeId = 'wrong';
  assert.throws(() => assertComplementaryEquivalence(...mismatch), /mapping must match/);
  const wrongFacts = equivalentReports(); wrongFacts[1].complementaryCanonicalSemantics.connection.catalogEdge.label = 'other';
  assert.throws(() => assertComplementaryEquivalence(...wrongFacts), /Canonical relationship facts/);
});


const groupPreviewMetadata = () => {
  const identity = { snapshotToken: 'snapshot', draftVersion: 3, draftDigest: 'digest', outputId: 'ResearchSubject' };
  const sourceColumns = [
    { columnId: 'source-gender', column: 'patient__gender', logicalType: 'string' },
    { columnId: 'source-status', column: 'status', logicalType: 'string' },
  ];
  const preview = { receiptId: 'candidate-receipt', outputId: 'ResearchSubject', columns: [
    { column: 'patient_gender', authoredColumns: ['patient__gender'], logicalType: 'string' },
    { column: 'status', authoredColumns: ['status'], logicalType: 'string' },
  ], rows: [{ patient_gender: null, status: 'active' }, { patient_gender: 'female', status: 'active' }] };
  return { identity, sourceColumns, preview,
    request: { snapshotToken: 'snapshot', expectedDraftVersion: 3, expectedDraftDigest: 'digest', outputId: 'ResearchSubject', changedStepId: 'group' },
    proposal: { ...identity, proposalId: 'candidate-receipt', previewStatus: 'READY', preview, changedStepId: 'group', stages: [
      { id: 'source_projection', inputStageId: '', columns: sourceColumns.map(column => ({ id: column.columnId, name: column.column, type: column.logicalType })) },
      { id: 'group', inputStageId: 'source_projection', operation: 'GROUP', columns: [{ id: 'output-gender', name: 'patient_gender', type: 'string' }, { id: 'output-status', name: 'status', type: 'string' }] },
    ], candidateConstruction: { version: 1,
      steps: [{ id: 'group', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'GROUP', group: { keys: [
        { inputColumnId: 'source-gender', outputColumnId: 'output-gender' }, { inputColumnId: 'source-status', outputColumnId: 'output-status' },
      ] } }, outputs: [{ id: 'output-gender', name: 'patient_gender', type: 'string' }, { id: 'output-status', name: 'status', type: 'string' }] }],
    } },
  };
};

test('Group preview follows exact candidate input/output/public identities and retains null keys', () => {
  const fixture = groupPreviewMetadata(); fixture.request.candidateConstruction = structuredClone(fixture.proposal.candidateConstruction);
  assert.deepEqual(resolveGraphRowsGroupPreviewKeys(fixture), [
    { inputColumnId: 'source-gender', outputColumnId: 'output-gender', name: 'patient_gender' },
    { inputColumnId: 'source-status', outputColumnId: 'output-status', name: 'status' },
  ]);
  assert.equal(fixture.preview.rows[0].patient_gender, null);
  assert.equal(Object.hasOwn(fixture.preview.rows[0], 'patient__gender'), false);
});

test('Group preview rejects stale receipts, missing or ambiguous bindings, and absent values', () => {
  const controls = [
    x => { x.request.expectedDraftVersion = 2; },
    x => { x.proposal.draftDigest = 'old'; },
    x => { x.proposal.snapshotToken = 'old'; },
    x => { x.proposal.outputId = 'other'; },
    x => { x.proposal.previewStatus = 'NEEDS_REPAIR'; },
    x => { x.proposal.preview = { ...x.preview, receiptId: 'old-receipt' }; },
    x => { x.proposal.changedStepId = 'missing'; },
    x => { x.proposal.candidateConstruction.steps.push(structuredClone(x.proposal.candidateConstruction.steps[0])); },
    x => { x.proposal.stages = x.proposal.stages.slice(1); },
    x => { x.proposal.stages.push(structuredClone(x.proposal.stages[0])); },
    x => { x.proposal.stages[0].inputStageId = 'other'; },
    x => { x.proposal.stages[0].columns = []; },
    x => { x.proposal.stages[0].columns.push(structuredClone(x.proposal.stages[0].columns[0])); },
    x => { x.proposal.stages[1].inputStageId = 'other'; },
    x => { x.proposal.stages[1].operation = 'FILTER'; },
    x => { x.proposal.stages[1].columns[0].name = 'wrong'; },
    x => { x.request.changedStepId = 'other'; },
    x => { x.proposal.proposalId = 'wrong-receipt'; },
    x => { delete x.proposal.proposalId; },
    x => { x.proposal.candidateConstruction.steps[0].operation.group.keys.reverse(); },
    x => { x.proposal.candidateConstruction.steps[0].operation.group.keys[0].outputColumnId = 'missing'; },
    x => { x.proposal.candidateConstruction.steps[0].outputs.push(structuredClone(x.proposal.candidateConstruction.steps[0].outputs[0])); },
    x => { x.preview.columns.push(structuredClone(x.preview.columns[0])); },
    x => { x.preview.columns[0].authoredColumns = ['other']; },
    x => { delete x.preview.rows[0].patient_gender; },
  ];
  for (const mutate of controls) { const fixture = groupPreviewMetadata(); fixture.request.candidateConstruction = structuredClone(fixture.proposal.candidateConstruction); mutate(fixture); assert.throws(() => resolveGraphRowsGroupPreviewKeys(fixture)); }
});
