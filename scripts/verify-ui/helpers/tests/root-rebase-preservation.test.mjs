import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
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

function rootRebaseColumnChoiceRequest() {
  return {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/construction-choice-proposals',
    method: 'POST',
    status: 200,
    completedAt: 10,
    body: {
      commandId: 'choice-command',
      snapshotToken: 'snapshot-current',
      expectedDraftVersion: 8,
      expectedDraftDigest: 'sha256:draft-current',
      outputId: 'output-current',
      constructionChoices: [{ choiceId: 'choice-current', form: 'VALUE', title: 'Observation ID' }],
    },
  };
}

function rootRebaseColumnApplyEntry(expected, browserRequestId = 'apply-current') {
  return {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/commands',
    method: 'POST',
    browserRequestId,
    requestId: browserRequestId,
    status: 200,
    completedAt: 20,
    body: {
      commandId: expected.commandId,
      snapshotToken: expected.snapshotToken,
      expectedDraftVersion: expected.expectedDraftVersion,
      expectedDraftDigest: expected.expectedDraftDigest,
      commands: [expected.command],
    },
  };
}

function rootRebaseSchemaRequest(browserRequestId, status) {
  return {
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/schema-fields',
    method: 'POST',
    browserRequestId,
    requestId: browserRequestId,
    body: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    ...status,
  };
}

function requestWaiter(requests) {
  const waiters = new Set();
  const find = waiter => requests.slice(waiter.fromIndex).find(entry =>
    Number.isFinite(entry.completedAt) && waiter.predicate(entry));
  return {
    waitForRequest(predicate, { fromIndex, timeoutMs }) {
      const waiter = { predicate, fromIndex };
      const match = find(waiter);
      if (match) return Promise.resolve(match);
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          waiters.delete(waiter);
          reject(new Error(`Timed out after ${timeoutMs}ms waiting for a native request.`));
        }, timeoutMs);
        waiters.add({ ...waiter, resolve, reject, timer });
      });
    },
    notify() {
      for (const waiter of [...waiters]) {
        const match = find(waiter);
        if (!match) continue;
        clearTimeout(waiter.timer);
        waiters.delete(waiter);
        waiter.resolve(match);
      }
    },
    get pendingCount() { return waiters.size; },
  };
}

test('row assessment waits for the fresh matching root transition and current draft', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const tracker = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/project/explorers/owned/authoring/v2',
    report,
    responsePaths: /row-change/,
  });
  const snapshotToken = `sha256:${'a'.repeat(64)}`;
  const draftDigest = `sha256:${'b'.repeat(64)}`;
  const outputId = 'out-owned';
  const rootOccurrenceId = 'occ-selected';
  const body = (draftVersion, rootNodeId) => ({
    snapshotToken, draftVersion, draftDigest, outputId, rootNodeId, rootOccurrenceId,
  });
  const identity = (currentRootResourceType, candidateRootResourceType, draftVersion) => ({
    outputId, snapshotToken, draftVersion, draftDigest,
    currentRootResourceType, candidateRootResourceType, rootOccurrenceId,
  });
  const assessment = (requestBody, currentRootResourceType, candidateRootResourceType, status = 'READY') => ({
    snapshotToken, draftVersion: requestBody.draftVersion, draftDigest,
    currentRootResourceType, candidateRootResourceType,
    preservedFeatureKeys: ['patient-id'], diagnostics: [],
    ...(status === 'READY' ? {
      status,
      proposal: {
        outputId, rootNodeId: requestBody.rootNodeId, rootOccurrenceId,
        sourceDocumentDigest: `sha256:${'c'.repeat(64)}`, routeRebase: [{ occurrenceId: 'base', edgeId: 'edge' }],
        preservedFeatureKeys: ['patient-id'],
      },
      unresolved: [],
    } : { status, unresolved: [{ kind: 'route', id: 'occ-selected', code: 'NEEDS_CHOICE', message: 'Choose the relationship.' }] }),
  });
  const emit = async (id, requestBody, responseBody) => {
    const request = {
      url: () => 'http://127.0.0.1:8188/api/v1/projects/project/explorers/owned/authoring/v2/row-change',
      method: () => 'POST', headers: () => ({ 'x-request-id': id }),
      postData: () => JSON.stringify(requestBody),
    };
    const fromIndex = report.nativeRequests.length;
    const captured = tracker.waitFor(entry => entry.requestId === id && entry.status === 200,
      { fromIndex, timeoutMs: 1000 });
    page.emit('request', request);
    page.emit('response', {
      request: () => request, status: () => 200, headers: () => ({ 'x-request-id': `${id}-response` }),
      text: async () => JSON.stringify(responseBody),
    });
    return captured;
  };

  const forward = identity('Patient', 'Observation', 4);
  const oldForwardBody = body(4, 'node-observation');
  const oldForward = await emit('old-forward', oldForwardBody,
    assessment(oldForwardBody, 'Patient', 'Observation'));
  const freshForwardFrom = report.nativeRequests.length;
  const freshForwardWait = tracker.waitFor(entry => matchesRootRebaseAssessment(entry, forward),
    { fromIndex: freshForwardFrom, timeoutMs: 1000 });
  const freshForwardBody = body(4, 'node-observation');
  const freshForwardCapture = emit('fresh-forward', freshForwardBody,
    assessment(freshForwardBody, 'Patient', 'Observation'));
  const freshForward = await freshForwardWait;
  await freshForwardCapture;
  assert.notEqual(oldForward, freshForward);
  assert.equal(matchesRootRebaseAssessment(oldForward, forward), true,
    'Cancel followed by a fresh proposal can reuse the same draft identity, so freshness comes from the request start index');

  const restoration = identity('Observation', 'Patient', 6);
  const restorationFrom = report.nativeRequests.length;
  const restorationWait = tracker.waitFor(entry => matchesRootRebaseAssessment(entry, restoration),
    { fromIndex: restorationFrom, timeoutMs: 1000 });
  const restorationBody = body(6, 'node-patient');
  const restorationResponse = assessment(restorationBody, 'Observation', 'Patient');
  const restorationEntryPromise = emit('restore-patient', restorationBody, restorationResponse);
  const restorationEntry = await restorationWait;
  await restorationEntryPromise;
  assert.equal(matchesRootRebaseAssessment(oldForward, restoration), false,
    'A prior Patient-to-Observation response cannot satisfy an Observation-to-Patient restoration');
  assert.equal(matchesRootRebaseAssessment(restorationEntry, restoration), true);

  for (const mutate of [
    entry => { entry.body.outputId = 'out-other'; },
    entry => { entry.body.draftVersion += 1; },
    entry => { entry.body.draftDigest = 'sha256:other'; },
    entry => { entry.body.rootNodeId = 'node-observation'; },
    entry => { entry.response.currentRootResourceType = 'Patient'; },
    entry => { entry.response.candidateRootResourceType = 'Observation'; },
  ]) {
    const mismatch = structuredClone(restorationEntry);
    mutate(mismatch);
    assert.equal(matchesRootRebaseAssessment(mismatch, restoration), false);
  }

  const blockedBody = body(6, 'node-patient');
  assert.equal(matchesRootRebaseAssessment({
    path: '/api/v1/projects/project/explorers/owned/authoring/v2/row-change', method: 'POST', status: 200,
    body: blockedBody, response: assessment(blockedBody, 'Observation', 'Patient', 'BLOCKED'),
  }, restoration), true, 'A current BLOCKED assessment without a proposal must reach the repair choice');
  await tracker.flush();
});

test('column Apply rejects stale commands, closes the catalog, then drains only active schema requests before exact paired rows', async () => {
  const choiceRequest = rootRebaseColumnChoiceRequest();
  const expected = rootRebaseColumnChoiceIdentity([choiceRequest], {
    outputId: 'output-current', snapshotToken: 'snapshot-current',
    draftVersion: 8, draftDigest: 'sha256:draft-current',
  });
  const staleCommand = rootRebaseColumnApplyEntry(expected, 'apply-stale');
  staleCommand.completedAt = 15;
  const currentCommand = rootRebaseColumnApplyEntry(expected);
  const firstSchemaRequest = rootRebaseSchemaRequest('schema-first');
  const requests = [staleCommand, currentCommand, firstSchemaRequest];
  const waiter = requestWaiter(requests);
  let catalogOpen = true;
  let continuationStarts = 0;
  let pairedRowsWaited = false;
  const resultPromise = waitForRootRebaseColumnApply({
    requests,
    errors: [],
    fromIndex: 1,
    schemaFromIndex: 0,
    expected,
    schemaIdentity: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    deadlineAt: Date.now() + 5000,
    waitForRequest: waiter.waitForRequest,
    closeCatalog: async timeoutMs => {
      assert(timeoutMs > 0);
      catalogOpen = false;
    },
    waitForPairedRows: async timeoutMs => {
      assert(timeoutMs > 0);
      pairedRowsWaited = true;
    },
  });

  await new Promise(resolve => setImmediate(resolve));
  assert.equal(catalogOpen, false, 'the native catalog close happens before the active request drains');
  assert.equal(waiter.pendingCount, 1, 'the request active when the catalog closes must be awaited');
  Object.assign(firstSchemaRequest, { status: 200, completedAt: 30 });
  waiter.notify();
  if (catalogOpen) {
    requests.push(rootRebaseSchemaRequest('schema-continuation'));
    continuationStarts += 1;
  }
  assert.equal(continuationStarts, 0, 'closing the catalog stops its paginated continuation before the drain');
  assert.deepEqual(requests.map(request => request.browserRequestId), ['apply-stale', 'apply-current', 'schema-first']);

  const result = await resultPromise;
  assert.equal(result.command.browserRequestId, 'apply-current', 'the prior matching command is outside the Apply request window');
  assert.deepEqual(result.schemaRequests.map(request => request.browserRequestId), ['schema-first']);
  assert.equal(pairedRowsWaited, true, 'exact paired source rows are part of the same Apply deadline');
});

test('column Apply fails on an unexpected schema response or network failure', async () => {
  const expected = rootRebaseColumnChoiceIdentity([rootRebaseColumnChoiceRequest()], {
    outputId: 'output-current', snapshotToken: 'snapshot-current',
    draftVersion: 8, draftDigest: 'sha256:draft-current',
  });
  const command = rootRebaseColumnApplyEntry(expected);
  const failedSchema = rootRebaseSchemaRequest('schema-error', { status: 503, completedAt: 25 });
  const errors = [{ kind: 'http', browserRequestId: 'schema-error', status: 503 }];
  await assert.rejects(waitForRootRebaseColumnApply({
    requests: [command, failedSchema], errors,
    fromIndex: 0, schemaFromIndex: 0, expected,
    schemaIdentity: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    deadlineAt: Date.now() + 5000,
    waitForRequest: async (predicate, { fromIndex }) => {
      const match = [command, failedSchema].slice(fromIndex).find(entry => Number.isFinite(entry.completedAt) && predicate(entry));
      assert(match, 'the exact current Apply or schema request should be available');
      return match;
    },
    closeCatalog: async () => assert.fail('failed schema requests must not reach the native catalog close'),
    waitForPairedRows: async () => assert.fail('failed schema requests must not reach the paired-row gate'),
  }), /Observation schema-fields request did not complete successfully/);

  const cancelledSchema = rootRebaseSchemaRequest('schema-cancelled', {
    failure: 'net::ERR_ABORTED', completedAt: 26,
  });
  await assert.rejects(waitForRootRebaseColumnApply({
    requests: [command, cancelledSchema],
    errors: [{ kind: 'network', browserRequestId: 'schema-cancelled', error: 'net::ERR_ABORTED' }],
    fromIndex: 0, schemaFromIndex: 0, expected,
    schemaIdentity: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    deadlineAt: Date.now() + 5000,
    waitForRequest: async (predicate, { fromIndex }) => {
      const match = [command, cancelledSchema].slice(fromIndex)
        .find(entry => Number.isFinite(entry.completedAt) && predicate(entry));
      assert(match, 'the exact current Apply or schema request should be available');
      return match;
    },
    closeCatalog: async () => assert.fail('failed network requests must not reach the native catalog close'),
    waitForPairedRows: async () => assert.fail('cancelled schema requests must not reach the paired-row gate'),
  }), /Observation schema-fields request did not complete successfully/);
});

test('column Apply expires the shared deadline while waiting for schema completion', async () => {
  const expected = rootRebaseColumnChoiceIdentity([rootRebaseColumnChoiceRequest()], {
    outputId: 'output-current', snapshotToken: 'snapshot-current',
    draftVersion: 8, draftDigest: 'sha256:draft-current',
  });
  const command = rootRebaseColumnApplyEntry(expected);
  const pendingSchema = rootRebaseSchemaRequest('schema-pending');
  let now = 100;
  let schemaWaitBudget;
  await assert.rejects(waitForRootRebaseColumnApply({
    requests: [command, pendingSchema], errors: [],
    fromIndex: 0, schemaFromIndex: 0, expected,
    schemaIdentity: { snapshotToken: 'snapshot-current', nodeId: 'observation-node' },
    deadlineAt: 110,
    now: () => now,
    waitForRequest: async (predicate, { fromIndex, timeoutMs }) => {
      const match = [command, pendingSchema].slice(fromIndex).find(entry => Number.isFinite(entry.completedAt) && predicate(entry));
      if (match) return match;
      schemaWaitBudget = timeoutMs;
      now = 110;
      Object.assign(pendingSchema, { status: 200, completedAt: now });
      return pendingSchema;
    },
    closeCatalog: async timeoutMs => assert.equal(timeoutMs, 10, 'native close receives only the remaining Apply budget'),
    waitForPairedRows: async () => assert.fail('an expired deadline must stop before preview polling'),
  }), /5s action deadline/);
  assert.equal(schemaWaitBudget, 10, 'schema completion receives only the remaining Apply budget');
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
