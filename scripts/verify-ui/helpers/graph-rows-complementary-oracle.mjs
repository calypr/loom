import assert from 'node:assert/strict';
import { isDeepStrictEqual } from 'node:util';
import { assertGraphRowsNativeIdentity } from './graph-rows-native-identity.mjs';
import { assertGraphRowsResult, validateGraphRowsFixture } from './graph-rows-fixture-oracle.mjs';

// Native callers supply names resolved from the saved public column identities,
// never from result values. This is only a result adapter; literal expectations
// remain independent of compiler output and the editing projection under test.
export function assertComplementaryExpandedRows(preview, columns, { keepMissing = false } = {}) {
  assert.equal(preview.sampled, false, 'The independent fixture must return its complete result');
  assert(Array.isArray(preview.rows));
  const keys = ['rootID', 'relatedID', 'patientID', 'status', 'list', 'scalar'];
  assert.deepEqual(Object.keys(columns).sort(), [...keys].sort(), 'Resolve every expected column explicitly');
  for (const key of keys) assert.equal(typeof columns[key], 'string', `Missing public column for ${key}`);
  assert(keys.every(key => columns[key].length > 0));
  assert.equal(new Set(Object.values(columns)).size, keys.length, 'A per-match scalar must have its own column identity');
  const actual = preview.rows.map(row => {
    for (const key of keys) assert(Object.hasOwn(row, columns[key]), `Missing returned ${key} column`);
    return { identity: [row[columns.rootID], row[columns.relatedID]], patientId: row[columns.patientID],
      subjectStatus: row[columns.status], observationValues: row[columns.list], perMatchObservationValue: row[columns.scalar] };
  });
  const oracle = validateGraphRowsFixture();
  const expected = [...oracle.expandedListsAndExplicitScalars,
    ...(keepMissing ? [oracle.expandedListsKeepMissingExtra] : [])];
  assertGraphRowsResult('expandedListsAndExplicitScalars', actual, { expandedListsAndExplicitScalars: expected });
  return actual;
}

// Catalog IDs are generated from the project/snapshot identity. Preserve their
// exact provenance and normalize only after unique semantic correspondence is proven.
export function resolveComplementaryCatalogEdge(catalog, actualEdgeId) {
  assert.equal(typeof catalog.snapshotToken, 'string'); assert(catalog.snapshotToken.length > 0);
  assert(Array.isArray(catalog.nodes) && Array.isArray(catalog.edges));
  const nodeFacts = node => { const { nodeId, documentCount, populated, ...facts } = node; return facts; };
  const edgeFacts = edge => { const { edgeId, fromNodeId, toNodeId, ...facts } = edge; return facts; };
  const node = id => {
    const matches = catalog.nodes.filter(item => item.nodeId === id);
    assert.equal(matches.length, 1, 'Catalog endpoint identity must resolve one node');
    assert.equal(typeof matches[0].resourceType, 'string'); assert(matches[0].resourceType.length > 0);
    assert.equal(typeof matches[0].rowGrain, 'string'); assert(matches[0].rowGrain.length > 0);
    assert.equal(typeof matches[0].rowRootEligible, 'boolean');
    const facts = nodeFacts(matches[0]);
    assert.equal(catalog.nodes.filter(item => isDeepStrictEqual(nodeFacts(item), facts)).length, 1, 'Catalog endpoint semantics must be unambiguous');
    return matches[0];
  };
  const matches = catalog.edges.filter(item => item.edgeId === actualEdgeId);
  assert.equal(matches.length, 1, 'The selected actual catalog edge must exist exactly once');
  const edge = matches[0]; const from = node(edge.fromNodeId); const to = node(edge.toNodeId);
  assert.equal(typeof edge.label, 'string'); assert(edge.label.length > 0);
  assert.equal(typeof edge.storageDirection, 'string'); assert(edge.storageDirection.length > 0);
  const semantic = { ...edgeFacts(edge), from: nodeFacts(from), to: nodeFacts(to) };
  const equivalent = catalog.edges.filter(item => isDeepStrictEqual({ ...edgeFacts(item), from: nodeFacts(node(item.fromNodeId)), to: nodeFacts(node(item.toNodeId)) }, semantic));
  assert.equal(equivalent.length, 1, 'Catalog relationship semantics must be unambiguous');
  return { semantic, mapping: { snapshotToken: catalog.snapshotToken, actualEdgeId,
    actualFromNodeId: from.nodeId, actualToNodeId: to.nodeId,
    catalog: { snapshotToken: catalog.snapshotToken, nodes: structuredClone(catalog.nodes), edges: structuredClone(catalog.edges) } } };
}

// Consume the actual whole-case evidence attachments after both native runs.
// A same-looking screen or a successful partial action prefix is insufficient.
export function assertComplementaryEquivalence(graphFirst, rowFirst) {
  for (const [report, mode] of [[graphFirst, 'graph-first'], [rowFirst, 'row-first']]) {
    assert.equal(report.status, 'passed', `The entire ${mode} native case must pass`);
    assert.equal(report.complementaryEntryMode, mode);
    assert.match(report.before?.sourceFingerprint ?? '', /^[a-f0-9]{64}$/);
    assert.match(report.before?.api?.runningBinary ?? '', /^[a-f0-9]{64}$/);
    assertGraphRowsNativeIdentity(report.before, report.after);
    assert(report.complementaryIdentityNormalization?.occurrence?.actual, 'Retain the original generated identity mapping');
    const mapping = report.complementaryIdentityNormalization.catalogEdge;
    assert(mapping, 'Retain exact snapshot-bound catalog edge and endpoint identities');
    const resolved = resolveComplementaryCatalogEdge(mapping.catalog, mapping.actualEdgeId);
    assert.deepEqual(mapping, resolved.mapping, 'Catalog identity mapping must match its actual snapshot facts');
    assert.deepEqual(report.complementaryCanonicalSemantics.connection.catalogEdge, resolved.semantic, 'Canonical relationship facts must match the exact catalog mapping');
    const connection = report.complementaryCanonicalSemantics.connection;
    assert.equal(connection.parent, resolved.semantic.from.resourceType);
    assert.equal(connection.resourceType, resolved.semantic.to.resourceType);
    assert.equal(connection.relationship, resolved.semantic.label);
    assert.equal(connection.storageDirection, resolved.semantic.storageDirection);
    assert.deepEqual(report.complementaryIdentityNormalization.occurrence.semanticPath, [connection.parent, connection.relationship, connection.resourceType]);
    assert(report.complementaryCanonicalSemantics?.exactRows?.length > 0, 'Retain exact native result rows');
  }
  assertGraphRowsNativeIdentity(graphFirst.before, rowFirst.before);
  assert.deepEqual(graphFirst.complementaryCanonicalSemantics, rowFirst.complementaryCanonicalSemantics,
    'Graph-first and row-first must preserve the same canonical policies, ordered operations, source settings and exact values');
  return graphFirst.complementaryCanonicalSemantics;
}


// Group outputs have their own authored identities and public names. Resolve
// them from this exact READY candidate, never by reusing source names or labels.
export function resolveGraphRowsGroupPreviewKeys({ request, proposal, preview, sourceColumns, identity }) {
  assert.equal(request.snapshotToken, identity.snapshotToken);
  assert.equal(request.expectedDraftVersion, identity.draftVersion);
  assert.equal(request.expectedDraftDigest, identity.draftDigest);
  assert.equal(request.outputId, identity.outputId);
  assert.equal(proposal.previewStatus, 'READY');
  assert.equal(proposal.snapshotToken, identity.snapshotToken);
  assert.equal(proposal.draftVersion, identity.draftVersion);
  assert.equal(proposal.draftDigest, identity.draftDigest);
  assert.equal(proposal.outputId, identity.outputId);
  assert.equal(preview.outputId, identity.outputId);
  assert.equal(typeof proposal.proposalId, 'string'); assert(proposal.proposalId.length > 0);
  assert.equal(proposal.proposalId, preview.receiptId);
  assert.equal(proposal.preview?.receiptId, preview.receiptId);
  assert.equal(proposal.preview?.outputId, identity.outputId);
  const steps = proposal.candidateConstruction.steps.filter(step => step.id === proposal.changedStepId);
  assert.equal(steps.length, 1, 'The exact candidate must identify one changed Group');
  const step = steps[0]; assert.equal(step.operation.kind, 'GROUP');
  assert.equal(request.changedStepId, proposal.changedStepId);
  const requestedSteps = request.candidateConstruction.steps.filter(item => item.id === request.changedStepId);
  assert.equal(requestedSteps.length, 1); assert.deepEqual(requestedSteps[0], step, 'The READY Group must retain the exact requested operation and outputs');
  assert.deepEqual(step.inputs, [{ kind: 'SOURCE_PROJECTION' }]);
  assert.deepEqual(step.operation.group.keys.map(key => key.inputColumnId), sourceColumns.map(column => column.columnId), 'Group key order and source identity must stay exact');
  const sources = proposal.stages.filter(stage => stage.id === 'source_projection');
  assert.equal(sources.length, 1, 'The public proposal must expose one exact source stage');
  assert.equal(sources[0].inputStageId, '');
  const groupStages = proposal.stages.filter(stage => stage.id === step.id);
  assert.equal(groupStages.length, 1, 'The READY proposal must expose one exact Group result stage');
  assert.equal(groupStages[0].inputStageId, 'source_projection'); assert.equal(groupStages[0].operation, 'GROUP');
  assert.deepEqual(groupStages[0].columns.map(({ id, name, type }) => ({ id, name, type })), step.outputs.map(({ id, name, type }) => ({ id, name, type })), 'Public Group stage outputs must correspond to the authored candidate outputs');
  const bindings = sourceColumns.map(source => {
    const inputs = sources[0].columns.filter(column => column.id === source.columnId);
    assert.equal(inputs.length, 1, 'Each Group input must exist once in the candidate source stage');
    assert.equal(inputs[0].name, source.column); assert.equal(inputs[0].type, source.logicalType);
    const keys = step.operation.group.keys.filter(key => key.inputColumnId === source.columnId);
    assert.equal(keys.length, 1, 'Each source Group key must resolve exactly once');
    const outputs = step.outputs.filter(output => output.id === keys[0].outputColumnId);
    assert.equal(outputs.length, 1, 'Each Group key must resolve one authored output');
    const output = outputs[0]; assert.equal(output.type, source.logicalType);
    const publicColumns = preview.columns.filter(column => column.column === output.name);
    assert.equal(publicColumns.length, 1, 'Each authored Group output needs one public preview column');
    assert.equal(publicColumns[0].logicalType, source.logicalType);
    assert(publicColumns[0].authoredColumns?.includes(source.column), 'Public Group column must retain explicit source-column correspondence');
    assert(preview.rows.every(row => Object.hasOwn(row, output.name)), 'Missing Group output cannot be interpreted as a null key');
    return { inputColumnId: source.columnId, outputColumnId: output.id, name: output.name };
  });
  assert.equal(new Set(bindings.map(binding => binding.outputColumnId)).size, bindings.length);
  assert.equal(new Set(bindings.map(binding => binding.name)).size, bindings.length);
  return bindings;
}
