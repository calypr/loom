import assert from 'node:assert/strict';
import test from 'node:test';
import {
  matchesRootRebaseAssessment,
  rootRebaseColumnChoiceIdentity,
  rootRebasePreservesAuthoredDocument,
  rootRebasePreservesFilterWhenAddingColumn,
  waitForRootRebaseColumnApply,
} from '../root-rebase-preservation.mjs';

const selectedOccurrenceId = 'occ_c2a93351010fdb1b0cf4771b';
const reverseEdgeId = 'e_0c471841525ed126ed02d1f6';
const patientColumnId = 'source_Il425N_patient_id';
const patientFilter = {
  columnId: patientColumnId,
  operator: 'EQUALS',
  values: [{ kind: 'STRING', string: 'Il425N' }],
};
const catalogEdges = [{
  edgeId: reverseEdgeId,
  fromNodeId: 'observation-node',
  toNodeId: 'patient-node',
  label: 'subject_Patient',
  storageDirection: 'OUTBOUND',
  populated: true,
}];
const routeRebase = [{ occurrenceId: 'base', edgeId: reverseEdgeId }];
const options = { selectedOccurrenceId, routeRebase, catalogEdges };

const retainedBefore = {
  kind: 'ExplorerBuilderDocument',
  output: { id: 'out_Il425N', title: 'Il425N' },
  rootResourceType: 'Patient',
  route: {
    occurrenceId: 'base',
    resourceType: 'Patient',
    children: [{
      occurrenceId: selectedOccurrenceId,
      resourceType: 'Observation',
      catalogEdgeId: 'patient-observation-route',
      relationship: 'subject_Patient',
      matchMode: 'OPTIONAL',
    }],
  },
  rows: { kind: 'RECORDS', records: {} },
  columns: [{
    columnId: patientColumnId,
    column: 'col_Il425N_patient_id',
    label: 'Patient ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
  }],
  construction: {
    version: 1,
    steps: [{
      id: 'filter_Il425N_patient_id',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: patientFilter },
      outputs: [{ id: patientColumnId, name: 'col_Il425N_patient_id', label: 'Patient ID', type: 'string' }],
    }],
  },
};

const retainedAfter = {
  kind: 'ExplorerBuilderDocument',
  output: { id: 'out_Il425N', title: 'Il425N' },
  rootResourceType: 'Observation',
  route: {
    occurrenceId: 'base',
    resourceType: 'Observation',
    children: [{
      occurrenceId: selectedOccurrenceId,
      resourceType: 'Patient',
      catalogEdgeId: reverseEdgeId,
      relationship: 'subject_Patient',
      matchMode: 'OPTIONAL',
    }],
  },
  rows: { kind: 'RECORDS', records: {} },
  columns: [{
    columnId: patientColumnId,
    column: 'col_Il425N_patient_id',
    label: 'Patient ID',
    logicalType: 'string',
    occurrenceId: selectedOccurrenceId,
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
  }],
  construction: {
    version: 1,
    steps: [{
      id: 'filter_Il425N_patient_id',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: { kind: 'FILTER', filter: patientFilter },
      outputs: [{ id: patientColumnId, name: 'col_Il425N_patient_id', label: 'Patient ID', type: 'string' }],
    }],
  },
};

const clone = value => structuredClone(value);

test('root assessment matcher binds the requested transition and current draft', () => {
  const expected = {
    outputId: 'out-owned',
    snapshotToken: `sha256:${'a'.repeat(64)}`,
    draftVersion: 4,
    draftDigest: `sha256:${'b'.repeat(64)}`,
    currentRootResourceType: 'Patient',
    candidateRootResourceType: 'Observation',
    rootOccurrenceId: 'occ-selected',
  };
  const request = {
    outputId: expected.outputId,
    snapshotToken: expected.snapshotToken,
    draftVersion: expected.draftVersion,
    draftDigest: expected.draftDigest,
    rootNodeId: 'node-observation',
    rootOccurrenceId: expected.rootOccurrenceId,
  };
  const response = {
    snapshotToken: expected.snapshotToken,
    draftVersion: expected.draftVersion,
    draftDigest: expected.draftDigest,
    currentRootResourceType: expected.currentRootResourceType,
    candidateRootResourceType: expected.candidateRootResourceType,
    status: 'READY',
    proposal: {
      outputId: expected.outputId,
      rootNodeId: request.rootNodeId,
      rootOccurrenceId: expected.rootOccurrenceId,
    },
  };
  const entry = {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/row-change',
    method: 'POST',
    status: 200,
    request,
    response,
  };

  assert.equal(matchesRootRebaseAssessment(entry, expected), true);
  assert.equal(matchesRootRebaseAssessment({ ...entry, request: undefined, body: request }, expected), false,
    'legacy body-only request entries must not match');
  for (const mutate of [
    candidate => { candidate.request.outputId = 'out-other'; },
    candidate => { candidate.request.draftVersion += 1; },
    candidate => { candidate.request.draftDigest = 'sha256:other'; },
    candidate => { candidate.request.rootOccurrenceId = 'occ-other'; },
    candidate => { candidate.response.currentRootResourceType = 'Observation'; },
    candidate => { candidate.response.candidateRootResourceType = 'Patient'; },
    candidate => { candidate.response.proposal.rootNodeId = 'node-other'; },
  ]) {
    const mismatch = structuredClone(entry);
    mutate(mismatch);
    assert.equal(matchesRootRebaseAssessment(mismatch, expected), false);
  }

  const blocked = structuredClone(entry);
  blocked.response.status = 'BLOCKED';
  delete blocked.response.proposal;
  blocked.response.unresolved = [{ kind: 'route', id: expected.rootOccurrenceId, code: 'NEEDS_CHOICE' }];
  assert.equal(matchesRootRebaseAssessment(blocked, expected), true,
    'a current BLOCKED assessment without a proposal must reach the repair choice');
  assert.equal(matchesRootRebaseAssessment({ ...entry, status: 503 }, expected), false);
});

test('retained Il425N Patient ID binding survives the exact Patient-to-Observation occurrence remap', () => {
  assert.equal(rootRebasePreservesAuthoredDocument(retainedBefore, retainedAfter, options), true);
  assert.equal(retainedBefore.columns[0].occurrenceId, 'base');
  assert.equal(retainedAfter.columns[0].occurrenceId, selectedOccurrenceId);
  assert.equal(retainedAfter.route.children[0].resourceType, 'Patient');
  assert.equal(retainedAfter.route.children[0].occurrenceId, selectedOccurrenceId);
  assert.deepEqual(retainedAfter.construction, retainedBefore.construction);
  assert.deepEqual(retainedAfter.rows, retainedBefore.rows);
  assert.deepEqual(retainedAfter.output, retainedBefore.output);
});

test('root rebase maps Patient and Observation columns in both directions without changing their authored fields', () => {
  const before = clone(retainedAfter);
  before.columns.push({
    columnId: 'source_Il425N_observation_id',
    column: 'col_Il425N_observation_id',
    label: 'Observation ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
  });
  const after = {
    ...clone(retainedBefore),
    route: {
      occurrenceId: 'base',
      resourceType: 'Patient',
      children: [{
        occurrenceId: selectedOccurrenceId,
        resourceType: 'Observation',
        catalogEdgeId: reverseEdgeId,
        relationship: 'subject_Patient',
        matchMode: 'OPTIONAL',
      }],
    },
    columns: [
      { ...clone(before.columns[0]), occurrenceId: 'base' },
      { ...clone(before.columns[1]), occurrenceId: selectedOccurrenceId },
    ],
  };

  assert.equal(rootRebasePreservesAuthoredDocument(before, after, options), true);
  assert.equal(after.columns[0].label, 'Patient ID');
  assert.equal(after.columns[0].occurrenceId, 'base');
  assert.equal(after.columns[1].label, 'Observation ID');
  assert.equal(after.columns[1].occurrenceId, selectedOccurrenceId);
});

test('retained no-population document stays valid while populated documents prepend only the exact inverse route', () => {
  assert.equal(Object.hasOwn(retainedBefore, 'population'), false);
  assert.equal(rootRebasePreservesAuthoredDocument(retainedBefore, retainedAfter, options), true);

  const before = clone(retainedBefore);
  before.population = {
    selectionRevisionId: 'selection-revision-Il425N',
    route: [{
      resourceType: 'Organization',
      relationship: 'managingOrganization',
      catalogEdgeId: 'organization-patient-membership',
      storageDirection: 'OUTBOUND',
    }],
  };
  const after = clone(retainedAfter);
  after.population = {
    selectionRevisionId: 'selection-revision-Il425N',
    route: [
      {
        resourceType: 'Patient',
        relationship: 'subject_Patient',
        catalogEdgeId: reverseEdgeId,
        storageDirection: 'OUTBOUND',
      },
      ...clone(before.population.route),
    ],
  };
  assert.equal(rootRebasePreservesAuthoredDocument(before, after, options), true);

  const changedRevision = clone(after);
  changedRevision.population.selectionRevisionId = 'different-selection-revision';
  assert.equal(rootRebasePreservesAuthoredDocument(before, changedRevision, options), false);

  const changedMembershipRoute = clone(after);
  changedMembershipRoute.population.route[1].relationship = 'partOf';
  assert.equal(rootRebasePreservesAuthoredDocument(before, changedMembershipRoute, options), false);
});

test('restoring a population root cancels only the exact inverse route seam', () => {
  const forwardEdgeId = 'patient-observation-route';
  const forwardEdge = {
    edgeId: forwardEdgeId,
    fromNodeId: 'patient-node',
    toNodeId: 'observation-node',
    label: 'subject_Patient',
    storageDirection: 'INBOUND',
    populated: true,
  };
  const reverseEdge = catalogEdges[0];
  const edges = [...catalogEdges, forwardEdge];
  const before = clone(retainedAfter);
  before.columns.push({
    columnId: 'source_Il425N_observation_id',
    column: 'col_Il425N_observation_id',
    label: 'Observation ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
  });
  before.construction.steps[0].outputs.push({
    id: 'source_Il425N_observation_id',
    name: 'col_Il425N_observation_id',
    label: 'Observation ID',
    type: 'string',
  });
  before.population = {
    selectionRevisionId: 'selection-revision-Il425N',
    route: [{
      resourceType: 'Patient',
      relationship: 'subject_Patient',
      catalogEdgeId: reverseEdge.edgeId,
      storageDirection: reverseEdge.storageDirection,
    }],
  };

  const after = {
    ...clone(before),
    rootResourceType: 'Patient',
    route: {
      occurrenceId: 'base',
      resourceType: 'Patient',
      children: [{
        occurrenceId: selectedOccurrenceId,
        resourceType: 'Observation',
        catalogEdgeId: forwardEdgeId,
        relationship: 'subject_Patient',
        matchMode: 'OPTIONAL',
      }],
    },
    columns: [
      { ...clone(before.columns[0]), occurrenceId: 'base' },
      { ...clone(before.columns[1]), occurrenceId: selectedOccurrenceId },
    ],
    population: { selectionRevisionId: 'selection-revision-Il425N', route: [] },
  };
  const callOptions = {
    selectedOccurrenceId,
    routeRebase: [{ occurrenceId: 'base', edgeId: forwardEdgeId }],
    catalogEdges: edges,
  };
  assert.equal(rootRebasePreservesAuthoredDocument(before, after, callOptions), true,
    'the Patient → Observation prefix and Observation → Patient population route cancel at their exact inverse seam');

  const uncollapsed = clone(after);
  uncollapsed.population.route = [
    {
      resourceType: 'Observation',
      relationship: 'subject_Patient',
      catalogEdgeId: forwardEdgeId,
      storageDirection: 'INBOUND',
    },
    ...clone(before.population.route),
  ];
  assert.equal(rootRebasePreservesAuthoredDocument(before, uncollapsed, callOptions), false,
    'a saved document that retains both inverse steps does not match the restored selected population');

  const distinctPatientEdge = {
    ...forwardEdge,
    edgeId: 'other-patient-observation-route',
    fromNodeId: 'other-patient-node',
  };
  const distinctNodeAfter = clone(after);
  distinctNodeAfter.route.children[0].catalogEdgeId = distinctPatientEdge.edgeId;
  distinctNodeAfter.population.route = [
    {
      resourceType: 'Observation',
      relationship: 'subject_Patient',
      catalogEdgeId: distinctPatientEdge.edgeId,
      storageDirection: 'INBOUND',
    },
    ...clone(before.population.route),
  ];
  const distinctNodeOptions = {
    ...callOptions,
    routeRebase: [{ occurrenceId: 'base', edgeId: distinctPatientEdge.edgeId }],
    catalogEdges: [...edges, distinctPatientEdge],
  };
  assert.equal(rootRebasePreservesAuthoredDocument(before, distinctNodeAfter, distinctNodeOptions), true,
    'matching resource types do not cancel steps whose catalog endpoints are distinct');
  distinctNodeAfter.population.route = [];
  assert.equal(rootRebasePreservesAuthoredDocument(before, distinctNodeAfter, distinctNodeOptions), false,
    'a different Patient node cannot be collapsed as the selected population anchor');
});

test('preservation predicate rejects a wrong occurrence target or any authored column, filter, row, or output change', () => {
  const reject = (mutate, callOptions = options) => {
    const after = clone(retainedAfter);
    mutate(after);
    assert.equal(rootRebasePreservesAuthoredDocument(retainedBefore, after, callOptions), false);
  };

  reject(after => { after.columns[0].occurrenceId = 'occ_wrong_target'; });
  reject(after => { after.route.children[0].occurrenceId = 'occ_wrong_target'; });
  reject(after => { after.route.children[0].relationship = 'wrong_relationship'; });
  reject(after => { after.route.children[0].matchMode = 'REQUIRED'; });
  reject(() => {}, { ...options, selectedOccurrenceId: 'occ_wrong_target' });
  reject(after => { after.columns[0].label = 'Changed label'; });
  reject(after => { after.columns[0].columnId = 'changed_column_id'; });
  reject(after => { after.columns[0].source.field.path = 'identifier'; });
  reject(after => { after.construction.steps[0].operation.filter.values[0].string = 'other-patient'; });
  reject(after => { after.rows.kind = 'GROUPS'; });
  reject(after => { after.output.id = 'different-output'; });

  const requiredBefore = clone(retainedBefore);
  requiredBefore.route.children[0].matchMode = 'REQUIRED';
  assert.equal(rootRebasePreservesAuthoredDocument(requiredBefore, retainedAfter, options), false);

  const extraNode = clone(retainedAfter);
  extraNode.route.children.push({ occurrenceId: 'occ_unexpected', resourceType: 'Organization' });
  assert.equal(rootRebasePreservesAuthoredDocument(retainedBefore, extraNode, options), false);
});

test('adding one output extends FILTER outputs while retaining the exact authored filter and prior output', () => {
  const before = clone(retainedBefore);
  const after = clone(retainedAfter);
  const addedColumn = {
    columnId: 'source_Il425N_observation_id',
    column: 'col_Il425N_observation_id',
    label: 'Observation ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
  };
  after.columns.push(addedColumn);
  after.construction.steps[0].outputs.push({
    id: addedColumn.columnId,
    name: addedColumn.column,
    label: addedColumn.label,
    type: addedColumn.logicalType,
  });

  assert.equal(rootRebasePreservesFilterWhenAddingColumn(before, after), true);

  const changedFilter = clone(after);
  changedFilter.construction.steps[0].operation.filter.values[0].string = 'different-patient';
  assert.equal(rootRebasePreservesFilterWhenAddingColumn(before, changedFilter), false);

  const changedPriorOutput = clone(after);
  changedPriorOutput.construction.steps[0].outputs[0].label = 'Changed Patient label';
  assert.equal(rootRebasePreservesFilterWhenAddingColumn(before, changedPriorOutput), false);

  const mismatchedAddedOutput = clone(after);
  mismatchedAddedOutput.construction.steps[0].outputs[1].name = 'wrong-column-name';
  assert.equal(rootRebasePreservesFilterWhenAddingColumn(before, mismatchedAddedOutput), false);

  const extraStep = clone(after);
  extraStep.construction.steps.push(clone(extraStep.construction.steps[0]));
  assert.equal(rootRebasePreservesFilterWhenAddingColumn(before, extraStep), false);
});

test('column-choice identity reads request identity and response choice from the observer entry', () => {
  const proposalRequest = {
    commandId: 'command-observation-column',
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    expectedDraftVersion: 5,
    expectedDraftDigest: 'sha256:draft-current',
    constructionChoices: [{ choiceId: 'request-choice', form: 'VALUE' }],
    limit: 50,
  };
  const proposalResponse = {
    commandId: 'command-observation-column',
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    draftVersion: 5,
    draftDigest: 'sha256:draft-current',
    constructionChoices: [{ choiceId: 'choice-current', form: 'VALUE', title: 'Observation ID' }],
    preview: { headers: ['Patient ID'], rows: [['patient-current']] },
  };
  const proposal = {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/construction-choice-proposals',
    method: 'POST',
    request: proposalRequest,
    response: proposalResponse,
    status: 200,
    completedAt: 20,
  };
  const expected = rootRebaseColumnChoiceIdentity([proposal], {
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    draftVersion: 5,
    draftDigest: 'sha256:draft-current',
  });

  assert.deepEqual(expected, {
    commandId: 'command-observation-column',
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    expectedDraftVersion: 5,
    expectedDraftDigest: 'sha256:draft-current',
    command: {
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId: 'output-current',
      constructionChoice: { choiceId: 'choice-current', form: 'VALUE' },
      title: 'Observation ID',
    },
  });
  const identity = {
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    draftVersion: 5,
    draftDigest: 'sha256:draft-current',
  };
  assert.throws(() => rootRebaseColumnChoiceIdentity([{
    ...proposal,
    request: undefined,
    body: proposalRequest,
  }], identity), /Expected one current Observation column-choice proposal/,
  'legacy body-only request entries must not match');
  for (const mutate of [
    response => { response.outputId = 'output-other'; },
    response => { response.snapshotToken = 'snapshot-other'; },
    response => { response.draftVersion += 1; },
    response => { response.draftDigest = 'sha256:other'; },
  ]) {
    const mismatch = structuredClone(proposal);
    mutate(mismatch.response);
    assert.throws(() => rootRebaseColumnChoiceIdentity([mismatch], identity),
      /did not complete successfully with its current draft identity/);
  }
});

test('successful Observation column Apply proceeds when closing the catalog cancels an optional schema page', async () => {
  const expected = {
    commandId: 'command-observation-column',
    outputId: 'output-current',
    snapshotToken: 'snapshot-current',
    expectedDraftVersion: 9,
    expectedDraftDigest: 'sha256:draft-current',
    command: {
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId: 'output-current',
      constructionChoice: { choiceId: 'choice-current', form: 'VALUE' },
      title: 'Observation ID',
    },
  };
  const applied = {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/commands',
    method: 'POST',
    requestId: 'apply-observation-column',
    browserRequestId: 'playwright-apply-observation-column',
    status: 200,
    completedAt: 20,
    request: {
      commandId: expected.commandId,
      snapshotToken: expected.snapshotToken,
      expectedDraftVersion: expected.expectedDraftVersion,
      expectedDraftDigest: expected.expectedDraftDigest,
      commands: [expected.command],
    },
    response: { commandId: expected.commandId, draftVersion: expected.expectedDraftVersion + 1 },
  };
  const legacyApply = {
    ...applied,
    request: undefined,
    body: applied.request,
    browserRequestId: 'legacy-body-only-apply',
  };
  const optionalSchemaPage = {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/schema-fields',
    method: 'POST',
    requestId: 'schema-fields-optional-page',
    browserRequestId: 'playwright-schema-fields-optional-page',
    startedAt: 15,
    request: { snapshotToken: 'snapshot-current', nodeId: 'observation-node', cursor: 'next-page' },
  };
  const requests = [legacyApply, applied, optionalSchemaPage];
  const errors = [];
  const waits = [];
  let catalogClosed = false;
  let pairedRowsObserved = false;

  const result = await waitForRootRebaseColumnApply({
    requests,
    errors,
    fromIndex: 0,
    schemaFromIndex: 0,
    expected,
    schemaIdentity: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    deadlineAt: 100,
    now: () => 30,
    waitForRequest: async (predicate, options) => {
      waits.push(options);
      const match = requests.slice(options.fromIndex).find(predicate);
      assert(match, 'only the exact chosen-column Apply mutation should be awaited');
      return match;
    },
    closeCatalog: async timeoutMs => {
      assert.equal(timeoutMs, 70, 'catalog close receives the remaining shared Apply budget');
      catalogClosed = true;
      optionalSchemaPage.failure = 'net::ERR_ABORTED';
      optionalSchemaPage.completedAt = 31;
    },
    waitForPairedRows: async timeoutMs => {
      assert(timeoutMs > 0);
      pairedRowsObserved = true;
    },
  });

  assert.equal(waits.length, 1, 'optional schema pages are diagnostic and are not drained');
  assert.equal(result.command, applied, 'the selected mutation remains the successful 200 response');
  assert.equal(result.command.request.commands[0].type, 'APPLY_CONSTRUCTION_CHOICE');
  assert.equal(catalogClosed, true);
  assert.equal(pairedRowsObserved, true, 'paired Observation and Patient values remain required');
  assert.deepEqual(result.schemaRequests, [optionalSchemaPage], 'the canceled page remains available as diagnostic evidence');
  assert.equal(result.schemaRequests[0].failure, 'net::ERR_ABORTED');
  assert.deepEqual(errors, [], 'the observer owns fatal transport and runtime failures');
});
