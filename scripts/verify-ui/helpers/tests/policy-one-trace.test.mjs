import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  buildPolicyOneTrace,
  decodePolicyOneTraceDiagnosticInput,
  encodePolicyOneTraceDiagnosticInput,
  POLICY_TRACE_ABSENT,
} from '../policy-one-trace.mjs';
import { createPlaywrightNetworkObserver } from '../playwright-network-observer.mjs';
import {
  buildPolicyOneMutationControls,
  compilePolicyOneJourneyTrace,
  validatePolicyOneJourneyTrace,
} from '../policy-one-tla-replay.mjs';
import {
  markPolicyOneExpectedHttp422,
  patientOneSavedState,
  policyApplyTraceExpectation,
  policyWorkspaceProjection,
} from '../../workflows/builder-patient-one-disagreement.mjs';
import { observeAuthoringAttempt } from '../fast-fixtures.mjs';
import { createPolicyOneTransportObserver } from '../policy-one-transport-observer.mjs';
import {
  makePolicyOneTraceInput,
  makeSyntheticPolicyOneJourneyTrace,
} from './fixtures/policy-one-journey.mjs';

const policyOneContract = JSON.parse(readFileSync(new URL(
  '../../../../verification/builder-interactions/builder-policy-one-contract.json', import.meta.url), 'utf8'));

const eventNames = [
  'OneSubmitted',
  'OneRejected',
  'SelectAll',
  'AllSubmitted',
  'AllReady',
  'AllApplySubmitted',
  'AllApplied',
];

const extraTransport = ({
  transportIndex,
  requestEventIndex,
  settlementEventIndex,
  method = 'GET',
  path,
  requestId = null,
  requestBody = null,
  ownerScope = 'https://fixture.invalid/api/explorers/policy-one',
  status = 200,
  response,
  responseHeaders = status === null ? null : { status, requestId },
  responseReadError = null,
  responseObservedBeforeFailure = false,
  failure = null,
}) => {
  return {
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    capturePhase: 'FORMAL',
    setupMutation: false,
    method,
    path,
    resourcePath: `${ownerScope}/authoring/v2/${path}`,
    ownerScope,
    requestId,
    requestCaptureId: null,
    requestBody,
    settlement: status === null ? 'request-failed' : 'response',
    responseOwnerIndex: status === null ? null : transportIndex,
    responseHeaders,
    responseReadError,
    responseObservedBeforeFailure,
    status,
    failure,
    response: status === null ? null : {
      status,
      requestId,
      receiptId: null,
      receiptIds: [],
      outputIds: [],
      snapshotToken: null,
      candidateWorkspaceDigest: null,
      intentDigest: null,
      ...response,
    },
  };
};

const pendingSupportCapture = ({ transportIndex, requestEventIndex, path, ownerScope, requestId, requestBody }) => ({
  transportIndex,
  requestEventIndex,
  capturePhase: 'FORMAL',
  method: 'POST',
  path,
  resourcePath: `${ownerScope}/authoring/v2/${path}`,
  ownerScope,
  requestId,
  requestCaptureId: null,
  requestBody,
  settlement: 'pending',
  responseOwnerIndex: null,
  responseHeaders: null,
  responseReadError: null,
  responseObservedBeforeFailure: false,
  status: null,
  response: null,
});

const captureMutations = [
  ['wrong request object ownership', input => { input.responses.one.requestObject = {}; }, /exact captured request object/],
  ['wrong response captureId', input => { input.responses.one.captureId = 'some-other-capture'; }, /captureId must match/],
  ['wrong ONE response status', input => { input.responses.one.status = 200; }, /response status must be 422/],
  ['wrong ONE error code', input => { input.responses.one.errorCode = 'OTHER_ERROR'; }, /must report CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES/],
  ['changed full saved workspace after rejection', input => { input.saved.afterOne.workspace.extra = true; }, /preserve the exact saved workspace/],
  ['changed saved draft identity after rejection', input => { input.saved.afterOne.draftVersion += 1; }, /preserve the saved draft version/],
  ['changed saved draft digest after rejection', input => { input.saved.afterOne.draftDigest = 'another-draft'; }, /preserve the saved draft digest/],
  ['changed saved snapshot token after rejection', input => { input.saved.afterOne.snapshotToken = 'another-snapshot'; }, /retain its snapshot token/],
  ['stale ALL draft request', input => { input.requests.all.body.expectedDraftDigest = 'older-draft'; }, /current expectedDraftDigest/],
  ['changed field choice in the ALL request', input => { input.requests.all.body.constructionChoices[0].choiceId = 'other-choice'; }, /exact selected field choice/],
  ['wrong editor identity', input => { input.dom.afterSelectAll.editorId = 'other-editor'; }, /same editor identity/],
  ['wrong selected field', input => { input.dom.afterSelectAll.selectedField = 'Patient.birthDate'; }, /exact selected field/],
  ['unselected field control', input => { input.dom.afterSelectAll.fieldSelected = false; }, /must retain the selected field/],
  ['hidden refusal after selecting ALL', input => { input.dom.afterSelectAll.errorVisible = false; }, /visible ONE refusal/],
  ['wrong editor document time origin', input => { input.dom.afterApplySubmitted.documentTimeOrigin += 1; }, /same document time origin/],
  ['missing expected member IDs', input => { delete input.responses.all.preview.rows[0].patient_ids; }, /selected member ID array/],
  ['wrong preview member IDs', input => { input.responses.all.preview.rows[0].patient_ids = ['dev-patient-001', 'dev-patient-003']; }, /match the independent fixture IDs/],
  ['wrong preview row count', input => { input.responses.all.preview.rowCount = 2; }, /rowCount must match the expected count/],
  ['wrong grouped count', input => { input.responses.all.preview.rows[0].count = 3; }, /expected grouped row count/],
  ['wrong persisted Group policy after Apply', input => { input.saved.afterApply.group.rowValuePolicy = 'ONE'; }, /saved Group must persist the ALL row-value policy/],
  ['wrong added Group input column binding', input => { input.saved.afterApply.group.inputColumnBinding.columnId = 'other-column'; }, /exact added input column binding/],
  ['wrong added Group output column binding', input => { input.saved.afterApply.group.outputColumnBinding.columnId = 'other-output'; }, /exact declared ALL output binding/],
  ['same Group row-value input and output ID', input => {
    input.expected.apply.outputColumnBinding.columnId = input.expected.apply.inputColumnBinding.columnId;
  }, /input and output column IDs must be distinct/],
  ['binding that already existed before Apply', input => {
    input.saved.before.group.inputColumnBinding = structuredClone(input.expected.apply.inputColumnBinding);
    input.saved.afterOne.group.inputColumnBinding = structuredClone(input.expected.apply.inputColumnBinding);
  }, /must add a new Group input column binding/],
  ['wrong echoed successful commandId', input => { input.responses.all.commandId = 'other-command'; }, /must match its actual request/],
  ['Apply uses a different commandId', input => {
    input.requests.apply.commandId = 'other-command';
    input.requests.apply.body.commandId = 'other-command';
  }, /reuse the successful ALL response commandId/],
  ['Apply carries the wrong choice policy', input => { input.requests.apply.body.commands[0].constructionChoice.rowValuePolicy = 'ONE'; }, /request must use ALL/],
  ['Apply response is owned by another request', input => { input.responses.apply.requestObject = {}; }, /exact captured request object/],
];

test('the Policy ONE HTTP exemption binds the exact recorder request and preserves unrelated failures', async () => {
  const origin = 'http://loom.test';
  const proposalPath = '/api/explorers/e/authoring/v2/construction-choice-proposals';
  const unrelatedPath = '/api/explorers/e/authoring/v2/commands';
  const page = new EventEmitter();
  page.url = () => `${origin}/`;
  const report = { errors: [] };
  const observer = createPlaywrightNetworkObserver(page, {
    scopeRequest: request => new URL(request.url()).origin === origin,
    appOrigins: [origin],
    responsePaths: [proposalPath],
    report,
  });
  const makeRequest = (path, body, requestId) => {
    const serializedBody = JSON.stringify(body);
    return {
      url: () => `${origin}${path}`,
      method: () => 'POST',
      headers: () => ({ 'x-request-id': requestId }),
      postData: () => serializedBody,
      postDataJSON: () => JSON.parse(serializedBody),
      resourceType: () => 'fetch',
      failure: () => null,
    };
  };
  const makeResponse = (request, status, body) => ({
    request: () => request,
    url: () => request.url(),
    status: () => status,
    body: async () => Buffer.from(JSON.stringify(body)),
    json: async () => body,
  });

  try {
    const requestBody = {
      commandId: 'command-one-422',
      snapshotToken: 'snapshot-one-422',
      expectedDraftVersion: 3,
      expectedDraftDigest: 'draft-one-422',
      outputId: 'output-one-422',
      constructionChoices: [{
        rowValuePolicy: 'ONE', choiceId: 'choice-patient-id-one', form: 'VALUE', title: 'Patient ID',
      }],
      limit: 25,
    };
    const responseBody = {
      diagnostics: [{ code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', stage: 'preview' }],
      error: { code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', message: 'multiple values' },
    };
    const request = makeRequest(proposalPath, requestBody, 'request-one-422');
    const response = makeResponse(request, 422, responseBody);
    const oneReady = observer.waitForResponse(entry => entry.path === proposalPath);
    page.emit('request', request);
    page.emit('response', response);
    page.emit('requestfinished', request);
    const oneEntry = await oneReady;

    const unrelatedRequest = makeRequest(unrelatedPath, { commandId: 'unrelated-command' }, 'request-unrelated-500');
    const unrelatedResponse = makeResponse(unrelatedRequest, 500, { error: 'server failure' });
    const unrelatedReady = observer.waitForResponse(entry => entry.path === unrelatedPath);
    page.emit('request', unrelatedRequest);
    page.emit('response', unrelatedResponse);
    page.emit('requestfinished', unrelatedRequest);
    const unrelatedEntry = await unrelatedReady;

    const wrongRequest = makeRequest(proposalPath, requestBody, 'different-physical-request');
    assert.throws(() => markPolicyOneExpectedHttp422({
      networkObserver: observer,
      report,
      request: wrongRequest,
      response,
      requestCaptureId: 'capture-one-422',
      requestBody,
      responseBody,
    }), /exact submitted request/);
    assert.equal(oneEntry.httpFailureExempted, undefined,
      'a different Request object cannot exempt the captured 422');
    assert(observer.failures.some(failure => failure.entry === oneEntry && failure.kind === 'http'));

    const expected = markPolicyOneExpectedHttp422({
      networkObserver: observer,
      report,
      request,
      response,
      requestCaptureId: 'capture-one-422',
      requestBody,
      responseBody,
    });
    assert.equal(expected.diagnostic.kind, 'http');
    assert.equal(expected.diagnostic.status, 422);
    assert.equal(expected.requestCaptureId, 'capture-one-422');
    assert.equal(expected.commandId, requestBody.commandId);
    assert.equal(expected.outputId, requestBody.outputId);
    assert.equal(expected.choiceId, requestBody.constructionChoices[0].choiceId);
    assert.equal(expected.rowValuePolicy, 'ONE');
    assert.equal(expected.responseErrorCode, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
    assert.equal(expected.diagnostic.browserRequestId, oneEntry.browserRequestId);
    assert.equal(expected.diagnostic.observerRequestIndex, oneEntry.index);
    assert.equal(oneEntry.httpFailureExempted, true);
    assert.equal(report.network.some(entry => entry.browserRequestId === oneEntry.browserRequestId), false,
      'the shared observer removes only the explicitly classified 422 diagnostic');
    assert.equal(report.errors.some(entry => entry.browserRequestId === oneEntry.browserRequestId), false);
    assert.deepEqual(observer.failures.map(failure => failure.entry), [unrelatedEntry]);
    assert.deepEqual(report.network.map(entry => ({ kind: entry.kind, status: entry.status,
      browserRequestId: entry.browserRequestId })), [{
      kind: 'http', status: 500, browserRequestId: unrelatedEntry.browserRequestId,
    }]);
    assert.equal(report.errors.filter(entry => entry.kind === 'http').length, 1,
      'an unrelated HTTP failure remains visible after the expected 422 is classified');
  } finally {
    observer.stop();
  }
});

test('a valid ONE rejection and same-choice ALL Apply persist distinct input and output bindings', () => {
  const input = makePolicyOneTraceInput();
  Object.freeze(input.expected);
  const { journeyTrace: trace } = buildPolicyOneTrace(input);
  assert.deepEqual(trace.transportLedger
    .filter(entry => entry.path === 'construction-choice-proposals')
    .map(({ classification, mutatesDraft }) => ({ classification, mutatesDraft })), [
    { classification: 'modeled', mutatesDraft: false },
    { classification: 'modeled', mutatesDraft: false },
  ], 'choice proposal POSTs remain semantically modeled even though they do not mutate the saved draft');
  assert.equal(Object.hasOwn(input.requests.apply.body, 'outputId'), false,
    'the commands API carries outputId on its command, not on the body root');
  assert.equal(input.requests.apply.body.commands[0].outputId, input.expected.outputId);
  assert.notEqual(input.expected.apply.inputColumnBinding.columnId,
    input.expected.apply.outputColumnBinding.columnId);
  const savedGroup = input.saved.afterApply.workspace.documents[0].construction.steps[0];
  const rowValue = savedGroup.rowValues[0];
  const rowValueOutput = savedGroup.outputs.find(output => output.id === rowValue.outputColumnId);
  assert.equal(rowValue.inputColumnId, input.expected.apply.inputColumnBinding.columnId);
  assert.equal(rowValue.outputColumnId, input.expected.apply.outputColumnBinding.columnId);
  assert.notEqual(rowValue.inputColumnId, rowValue.outputColumnId);
  assert.equal(rowValueOutput.type, 'array');

  assert.equal(trace.name, 'builder-authoring:patient-one-disagreement');
  assert.equal(trace.model, 'PolicyOneDisagreement');
  assert.deepEqual(trace.unmodeledEvents, [],
    'the producer must explicitly inventory that the exact modeled action and transport ledgers leave no unmodeled events');
  assert.deepEqual(trace.actions.map(({ label }) => label), [
    'GroupPreviewReady', 'GroupApplied', ...eventNames, 'ReloadedAll', 'RemovalCancelled',
  ]);
  const actions = new Map(trace.actions.map(action => [action.label, action]));
  assert.equal(actions.get('OneSubmitted').observation.request.commandId, 'command-one');
  assert.equal(actions.get('OneSubmitted').request.requestId, 'header-one');
  assert.equal(actions.get('OneSubmitted').request.requestObjectId, 'capture-one-random');
  assert.equal(actions.get('OneRejected').observation.request.commandId, 'command-one');
  assert.equal(actions.get('OneRejected').observation.request.responseCommandId, POLICY_TRACE_ABSENT,
    'the owned 422 remains valid when its payload omits commandId');
  assert.equal(actions.get('OneRejected').observation.choice.status, 'error');
  assert.equal(actions.get('SelectAll').observation.choice.rowValuePolicy, 'ALL');
  assert.equal(actions.get('SelectAll').observation.choice.status, 'error');
  assert.equal(actions.get('OneRejected').observation.editor.allAvailable, true);
  assert.equal(actions.get('AllReady').observation.request.responseCommandId, 'command-all');
  assert.deepEqual(actions.get('AllReady').observation.preview.memberIds, ['dev-patient-001', 'dev-patient-002']);
  assert.equal(actions.get('AllReady').observation.preview.rowCount, 1);
  assert.equal(actions.get('AllReady').observation.preview.count, 2);
  assert.equal(Object.hasOwn(actions.get('AllReady').observation.preview, 'patientIds'), false,
    'the adapter must expose the generic memberIds key');
  assert.equal(actions.get('AllApplied').observation.saved.policy, 'ALL');
  assert.deepEqual(trace.initialObservation.rowIds, ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(trace.outcome.savedState, { sourceColumns: ['patient-id'], group: null });
  assert(trace.actions.every(action => !Object.hasOwn(action.request ?? {}, 'requestObject') &&
    !Object.hasOwn(action.response ?? {}, 'requestObject')),
    'the model trace must not retain raw browser request objects');
  assert.deepEqual(JSON.parse(JSON.stringify(trace)), trace,
    'the model trace must contain only finite JSON values');
});

test('the Policy One adapter rejects missing or unexpected unmodeled events', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  assert.deepEqual(trace.unmodeledEvents, []);

  const missingInventory = structuredClone(trace);
  delete missingInventory.unmodeledEvents;
  assert.throws(() => validatePolicyOneJourneyTrace(missingInventory),
    /must include its unmodeled-event inventory/);

  const unexpectedEvent = structuredClone(trace);
  unexpectedEvent.unmodeledEvents.push({
    kind: 'unregistered-event',
    note: 'An event with no Policy One model transition was observed.',
    classification: 'unsupported',
    reason: 'The Policy One exact action inventory covers the supported workflow events.',
  });
  assert.throws(() => validatePolicyOneJourneyTrace(unexpectedEvent),
    /must represent every observed workflow event in its exact modeled action inventory/);
});

test('the response observation preserves an absent response request ID', () => {
  const input = makePolicyOneTraceInput();
  input.responses.one.requestId = null;
  const trace = buildPolicyOneTrace(input).journeyTrace;
  const rejected = trace.actions.find(action => action.label === 'OneRejected');

  assert.equal(rejected.response.requestId, null,
    'the captured response request ID remains absent when neither header nor body exposed one');
  assert.equal(rejected.observation.request.requestId, POLICY_TRACE_ABSENT,
    'response observation must not substitute the inbound request header ID');
});

test('the production saved-state projection supplies the trace adapter Apply contract', () => {
  const input = makePolicyOneTraceInput();
  const applied = input.saved.afterApply;
  const savedState = patientOneSavedState({
    workspace: applied.workspace,
    draftVersion: applied.draftVersion,
    draftDigest: applied.draftDigest,
    catalog: { snapshotToken: applied.snapshotToken },
  }, 'output-1');
  const rowValue = savedState.group.rowValues[0];
  const expectedApply = policyApplyTraceExpectation({
    savedState,
    inputColumnId: rowValue.inputColumnId,
    renderedRows: { headers: ['Row count'], cells: [[{ text: '2' }]] },
    status: 200,
  });

  input.expected.apply = expectedApply;
  input.saved.afterApply.group = {
    ...input.saved.afterApply.group,
    rowValuePolicy: expectedApply.policy,
    inputColumnId: rowValue.inputColumnId,
    inputColumnBinding: expectedApply.inputColumnBinding,
    outputColumnBinding: expectedApply.outputColumnBinding,
  };
  const { journeyTrace } = buildPolicyOneTrace(input);

  assert.deepEqual(expectedApply, {
    policy: 'ALL',
    status: 200,
    inputColumnBinding: { columnId: rowValue.inputColumnId, field: 'Patient.id' },
    outputColumnBinding: { columnId: rowValue.outputColumnId, type: 'array' },
  });
  assert.equal(journeyTrace.actions.find(action => action.label === 'AllApplied')
    .observation.saved.group.outputColumnBinding.columnId, rowValue.outputColumnId);
});

test('the complete diagnostic projection input round-trips opaque browser handles for offline projection', () => {
  const input = makePolicyOneTraceInput();
  const nativeShapeTrace = buildPolicyOneTrace(input).journeyTrace;
  const diagnosticBytes = Buffer.from(JSON.stringify(encodePolicyOneTraceDiagnosticInput(input)));
  const decodedDiagnostic = JSON.parse(diagnosticBytes.toString('utf8'));
  const diagnosticBeforeDecode = structuredClone(decodedDiagnostic);
  const restored = decodePolicyOneTraceDiagnosticInput(decodedDiagnostic);
  const restoredAgain = decodePolicyOneTraceDiagnosticInput(decodedDiagnostic);
  assert.deepEqual(decodedDiagnostic, diagnosticBeforeDecode,
    'offline decoding must leave the retained JSON diagnostic input unchanged');

  assert.equal(restored.requests.one.requestObject, restored.responses.one.requestObject,
    'the diagnostic reference table must restore the exact request alias shared by its response');
  assert.notEqual(restored.requests.one.requestObject, restored.responses.one.responseObject,
    'the diagnostic reference table must keep request and response handles distinct');
  assert.notEqual(restored.responses.one.responseObject, restored.responses.all.responseObject,
    'the diagnostic reference table must keep separate captured responses distinct');
  assert.deepEqual(buildPolicyOneTrace(restored).journeyTrace, nativeShapeTrace,
    'the JSON diagnostic input must reproduce the same canonical projection offline');
  assert.deepEqual(buildPolicyOneTrace(restoredAgain).journeyTrace, nativeShapeTrace,
    'the same retained JSON diagnostic input must support repeated offline projection');

  const missingReference = JSON.parse(diagnosticBytes.toString('utf8'));
  delete missingReference.opaqueBrowserObjectReferences.responses.one;
  assert.throws(() => decodePolicyOneTraceDiagnosticInput(missingReference), /map every and only captured response/);

  const unknownReference = JSON.parse(diagnosticBytes.toString('utf8'));
  unknownReference.opaqueBrowserObjectReferences.responses.one.requestCaptureId = 'unknown-capture';
  assert.throws(() => decodePolicyOneTraceDiagnosticInput(unknownReference), /must bind its request capture/);
});

test('workspace projections separate fixture counts from observed rows and require a Group count cell', () => {
  const input = makePolicyOneTraceInput();
  const fixtureIDs = [...input.context.initialObservation.rowIds];
  const before = input.saved.before;
  const groupedState = patientOneSavedState({
    workspace: before.workspace,
    draftVersion: before.draftVersion,
    draftDigest: before.draftDigest,
    catalog: { snapshotToken: before.snapshotToken },
  }, 'output-1');
  const sourceState = { ...groupedState, group: null };
  const sourceRows = [
    [{ text: fixtureIDs[0] }],
    [{ text: fixtureIDs[0] }],
    [{ text: fixtureIDs[1] }],
  ];
  const sourceProjection = policyWorkspaceProjection({
    savedState: sourceState,
    renderedRows: { headers: ['Patient ID'], cells: sourceRows },
    sourceState,
    fixtureIDs,
  });
  assert.equal(sourceProjection.sourceRows.count, 2, 'fixture membership remains an independent oracle');
  assert.equal(sourceProjection.renderedRows.count, 3, 'rendered count must use the three observed rows, including the duplicate');

  const afterApply = input.saved.afterApply;
  const allState = patientOneSavedState({
    workspace: afterApply.workspace,
    draftVersion: afterApply.draftVersion,
    draftDigest: afterApply.draftDigest,
    catalog: { snapshotToken: afterApply.snapshotToken },
  }, 'output-1');
  const groupedRows = {
    headers: ['Patient ID', 'Row count'],
    cells: [[{ text: JSON.stringify(fixtureIDs), raw: JSON.stringify(fixtureIDs) },
      { text: String(fixtureIDs.length) }]],
  };
  const groupedProjection = policyWorkspaceProjection({
    savedState: allState,
    renderedRows: groupedRows,
    sourceState,
    fixtureIDs,
  });
  assert.equal(groupedProjection.sourceRows.count, 2);
  assert.equal(groupedProjection.group.count, fixtureIDs.length,
    'the Group count comes from the observed Row count cell');
  assert.equal(groupedProjection.renderedRows[0].count, fixtureIDs.length);
  assert.throws(() => policyWorkspaceProjection({
    savedState: allState,
    renderedRows: { headers: ['Patient ID'], cells: [[{ text: JSON.stringify(fixtureIDs) }]] },
    sourceState,
    fixtureIDs,
  }), /saved Group projection must bind its observed row count/);
});

test('the Policy observer retains an in-flight setup request across formal entry and binds the actual observeAuthoringAttempt request', async () => {
  class ObserverPage extends EventEmitter {
    waitForRequest(predicate, { timeout = 1000 } = {}) {
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          this.off('request', onRequest);
          reject(new Error('timed out waiting for a matching request'));
        }, timeout);
        const onRequest = request => {
          if (!predicate(request)) return;
          clearTimeout(timer);
          this.off('request', onRequest);
          resolve(request);
        };
        this.on('request', onRequest);
      });
    }

    async evaluate() { return {}; }
  }

  const makeRequest = ({ method = 'POST', path, requestId, body }) => ({
    url: () => `http://loom.test/api/explorers/e/authoring/v2/${path}`,
    method: () => method,
    headers: () => requestId ? { 'x-request-id': requestId } : {},
    postDataJSON: () => body,
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });
  const makeResponse = (requestObject, body, { status = 200, requestId } = {}) => ({
    request: () => requestObject,
    status: () => status,
    headers: () => requestId ? { 'x-request-id': requestId } : {},
    json: async () => body,
    ok: () => status >= 200 && status < 300,
  });
  const deferred = () => {
    let resolve;
    let reject;
    const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
    return { promise, resolve, reject };
  };

  const page = new ObserverPage();
  const observer = createPolicyOneTransportObserver(page);
  const setupRequest = makeRequest({ path: 'construction-capabilities', requestId: 'setup-capabilities-physical-id',
    body: { outputId: 'output-1', expectedDraftVersion: 2, expectedDraftDigest: 'draft-source' } });
  page.emit('request', setupRequest);
  const setupIdle = observer.waitUntilIdle();
  const setupBody = deferred();
  let setupIdleSettled = false;
  setupIdle.then(() => { setupIdleSettled = true; });
  page.emit('response', makeResponse(setupRequest, setupBody.promise));
  page.emit('requestfinished', setupRequest);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(setupIdleSettled, false,
    'the setup drain must wait for the pre-boundary response body to finish reading');
  setupBody.resolve({ capabilities: [] });
  await setupIdle;
  const snapshotEventIndex = observer.eventIndex;
  const formalEntry = await observer.beginFormalSpan({
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 2, draftDigest: 'draft-source',
  }, { expectedEventIndex: snapshotEventIndex });
  assert.equal(formalEntry.afterTransportIndex, 1);
  assert.equal(formalEntry.afterEventIndex, 2);
  assert.equal(formalEntry.initialSavedState.draftVersion, 2);

  const attempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals',
    owner: { kind: 'policy-one-group-preview', attempt: 'entry-boundary-test' }, timeout: 1000,
  });
  const proposalRequest = makeRequest({
    path: 'construction-proposals', requestId: 'proposal-physical-id',
    body: { outputId: 'output-1', expectedDraftVersion: 2 },
  });
  page.emit('request', proposalRequest);
  const started = await attempt.started;
  const proposalResponse = makeResponse(proposalRequest, {
    proposalId: 'proposal-1', outputId: 'output-1', snapshotToken: 'snapshot-1',
    draftVersion: 2, draftDigest: 'draft-source', previewStatus: 'READY',
    preview: { receiptId: 'receipt-1', outputId: 'output-1', rowCount: 1, rows: [] },
  }, { requestId: 'proposal-physical-id' });
  page.emit('response', proposalResponse);
  page.emit('requestfinished', proposalRequest);
  const settled = await attempt.settled;
  assert.equal(settled.response?.captureId, started.request.captureId);
  assert.equal(settled.response?.status, 200);
  const semanticRecord = observer.bindRequestCaptureId(started.rawRequest, started.request.captureId);
  assert.equal(semanticRecord.capturePhase, 'FORMAL');
  assert.equal(semanticRecord.transportIndex, observer.transportIndexForRequest(started.rawRequest));
  assert.equal(semanticRecord.requestId, started.request.requestId);
  assert.equal(semanticRecord.requestCaptureId, settled.request.captureId);
  assert.equal(semanticRecord.responseOwnerIndex, semanticRecord.transportIndex);

  const inventoryRequest = makeRequest({ path: 'semantic-inventory', requestId: 'inventory-physical-id',
    body: { outputId: 'output-1', rowRoot: 'Patient', limit: 50 } });
  page.emit('request', inventoryRequest);
  const inventoryBody = deferred();
  page.emit('response', makeResponse(inventoryRequest, inventoryBody.promise,
    { requestId: 'inventory-physical-id' }));
  await new Promise(resolve => setImmediate(resolve));
  const inventoryRecord = observer.entryForRequest(inventoryRequest).record;
  assert.deepEqual(inventoryRecord.responseHeaders,
    { status: 200, requestId: 'inventory-physical-id' },
  'the response event must retain actual HTTP headers without marking transport completion');
  assert.equal(inventoryRecord.settlement, 'pending');
  page.emit('requestfailed', inventoryRequest);
  assert.equal(inventoryRecord.settlement, 'request-failed');
  assert.equal(inventoryRecord.responseObservedBeforeFailure, true);
  assert.equal(inventoryRecord.responseHeaders.status, 200);
  assert.equal(inventoryRecord.status, null,
    'a response-header observation followed by a failed terminal remains a request failure');
  let failedReadIdleSettled = false;
  const failedReadIdle = observer.waitUntilIdle().then(() => { failedReadIdleSettled = true; });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(failedReadIdleSettled, false,
    'the transport drain must wait for a response body read after the request failed');
  assert.throws(() => page.emit('requestfailed', inventoryRequest),
    /authoring transport 3 must settle exactly once/,
  'a second real terminal event for the same Request identity remains fatal');
  inventoryBody.reject(new Error('response body was canceled after response headers'));
  await failedReadIdle;

  const capture = await observer.finish();
  assert.deepEqual(capture.captures.map(entry => entry.capturePhase), ['SETUP', 'FORMAL', 'FORMAL']);
  assert.equal(capture.captures[0].settlement, 'response');
  assert.equal(capture.captures[1].requestCaptureId, started.request.captureId);
  assert.equal(capture.captures[1].responseOwnerIndex, capture.captures[1].transportIndex);
  assert.equal(capture.captures[2].settlement, 'request-failed');
  assert.deepEqual(capture.captures[2].responseHeaders,
    { status: 200, requestId: 'inventory-physical-id' });
  assert.equal(capture.captures[2].responseObservedBeforeFailure, true);
  assert.equal(capture.captures[2].responseReadError,
    'Error: response body was canceled after response headers');
  assert.equal(capture.captures[2].failure, 'net::ERR_ABORTED');
});

test('the Policy observer retains the bounded JSON facts from a completed frame source options response', async () => {
  class ObserverPage extends EventEmitter {
    async evaluate() { return {}; }
  }

  const page = new ObserverPage();
  const observer = createPolicyOneTransportObserver(page);
  await observer.beginFormalSpan({
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
  });
  const request = {
    url: () => 'http://loom.test/api/explorers/e/authoring/v2/frame-source-options',
    method: () => 'POST',
    headers: () => ({}),
    postDataJSON: () => ({
      snapshotToken: 'snapshot-1', outputId: 'output-1', resourceType: 'Patient', limit: 50,
    }),
  };
  page.emit('request', request);
  page.emit('response', {
    request: () => request,
    status: () => 200,
    headers: () => ({}),
    json: async () => ({
      snapshotToken: 'snapshot-1', outputId: 'output-1', complete: true, truncated: false,
      sources: [],
    }),
  });
  page.emit('requestfinished', request);

  const capture = await observer.finish();
  assert.deepEqual(capture.captures[0].requestBody, {
    snapshotToken: 'snapshot-1', outputId: 'output-1', resourceType: 'Patient', limit: 50,
  });
  assert.deepEqual(capture.captures[0].response, {
    status: 200,
    requestId: null,
    commandId: null,
    receiptId: null,
    receiptIds: [],
    outputIds: ['output-1'],
    snapshotToken: 'snapshot-1',
    draftVersion: null,
    draftDigest: null,
    candidateWorkspaceDigest: null,
    intentDigest: null,
    previewRowsPresent: false,
    frameSourceOptionsOutputId: 'output-1',
    frameSourceOptionsSnapshotToken: 'snapshot-1',
    frameSourceOptionsComplete: true,
    frameSourceOptionsTruncated: false,
    frameSourceOptionsSourceCount: 0,
    frameSourceOptionsNextCursor: null,
  });
  assert.deepEqual(JSON.parse(JSON.stringify(capture.captures)), capture.captures,
    'captured request facts must remain JSON-safe and retain their physical observer row');
});

test('the Policy observer snapshots no-header support requests as pending and detaches at formal exit', async () => {
  class ObserverPage extends EventEmitter {
    async evaluate() { return {}; }
  }
  const page = new ObserverPage();
  const observer = createPolicyOneTransportObserver(page);
  await observer.beginFormalSpan({
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 2, draftDigest: 'draft-source',
  });
  const makeRequest = (path, requestId, body) => ({
    url: () => `http://loom.test/api/explorers/e/authoring/v2/${path}`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': requestId }),
    postDataJSON: () => body,
    failure: () => null,
  });
  page.emit('request', makeRequest('semantic-inventory', 'catalog-request-physical-1', {
    outputId: 'output-1', rowRoot: 'Patient', limit: 50,
  }));
  page.emit('request', makeRequest('construction-capabilities', 'capabilities-request-physical-2', {
    outputId: 'output-1', expectedDraftVersion: 2, expectedDraftDigest: 'draft-source',
    stageId: 'source_projection',
  }));

  const captured = await observer.finish({ timeoutMs: 15 });
  assert.equal(captured.formalExit.deadlineReached, true);
  assert.equal(captured.formalExit.drainTimeoutMs, 15);
  assert.equal(captured.formalExit.afterTransportIndex, 2);
  assert.equal(captured.formalExit.afterEventIndex, 2);
  assert.deepEqual(captured.formalExit.pendingTransportIndices, [1, 2]);
  assert.deepEqual(captured.formalExit.pendingResponseReadTransportIndices, []);
  assert.deepEqual(captured.captures.map(entry => entry.settlement), ['pending', 'pending']);
  for (const entry of captured.captures) {
    assert.equal(entry.responseHeaders, null);
    assert.equal(entry.status, null);
    assert.equal(entry.response, null);
    assert.equal(entry.responseOwnerIndex, null);
    assert.equal(Object.hasOwn(entry, 'settlementEventIndex'), false);
    assert.equal(Object.hasOwn(entry, 'failure'), false);
  }
  assert.equal(page.listenerCount('request'), 0,
    'events after the recorded formal exit cannot extend or mutate its transport span');
  page.emit('request', makeRequest('semantic-inventory', 'late-request-outside-span', {
    outputId: 'output-1', rowRoot: 'Patient', limit: 50,
  }));
  assert.equal(captured.captures.length, 2);

  const headerPage = new ObserverPage();
  const headerObserver = createPolicyOneTransportObserver(headerPage);
  await headerObserver.beginFormalSpan({
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 2, draftDigest: 'draft-source',
  });
  const headerRequest = makeRequest('semantic-inventory', 'catalog-header-physical', {
    outputId: 'output-1', rowRoot: 'Patient', limit: 50,
  });
  headerPage.emit('request', headerRequest);
  headerPage.emit('response', {
    request: () => headerRequest,
    status: () => 200,
    headers: () => ({ 'x-request-id': 'catalog-header-physical' }),
    json: async () => ({ receiptId: 'uncredited-receipt', outputId: 'output-1' }),
  });
  await new Promise(resolve => setImmediate(resolve));
  await assert.rejects(headerObserver.finish({ timeoutMs: 15 }), /response headers were observed/);
  const headerRecord = headerObserver.entryForRequest(headerRequest).record;
  assert.deepEqual(headerRecord.responseHeaders, { status: 200, requestId: 'catalog-header-physical' });
  assert.equal(headerRecord.settlement, 'pending');
  assert.equal(headerRecord.status, null);
  assert.equal(headerRecord.responseOwnerIndex, null);
  assert.deepEqual(headerRecord.responseBody?.receiptIds, ['uncredited-receipt'],
    'captured body facts remain observable even though the request has no terminal response credit');
});

test('the Policy observer rejects formal entry when a setup command failed or was canceled', async () => {
  class ObserverPage extends EventEmitter {
    async evaluate() { return {}; }
  }
  const page = new ObserverPage();
  const observer = createPolicyOneTransportObserver(page);
  const failedSetupCommand = {
    url: () => 'http://loom.test/api/explorers/e/authoring/v2/commands',
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'failed-setup-command-id' }),
    postDataJSON: () => ({ commandId: 'failed-setup-command-id', commands: [{ type: 'CREATE_TABLE' }] }),
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  };
  page.emit('request', failedSetupCommand);
  page.emit('requestfailed', failedSetupCommand);
  await assert.rejects(observer.beginFormalSpan({
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 0, draftDigest: 'draft-empty',
  }), /setup mutation 1 must have a successful HTTP response/);
});

test('setup reconcile receipts expire when a same-owner setup command changes CAS across request/response orderings', () => {
  for (const setupReceiptCase of [
    'reconcile-before-command',
    'command-before-reconcile-response',
    'command-starts-first',
  ]) {
    const trace = makeSyntheticPolicyOneJourneyTrace({ setupReceiptCase });
    const staleSetupReceipt = trace.transportLedger.find(row => row.requestId === 'builder-70-1');
    const setupCommand = trace.transportLedger.find(row => row.capturePhase === 'SETUP'
      && row.classification === 'setup' && row.setupMutation);
    const currentSetupReceipt = trace.transportLedger.find(row => row.requestId === 'builder-71-1');
    const setupPreview = trace.transportLedger.find(row => row.path === 'preview'
      && row.requestBody.receiptId === currentSetupReceipt?.response.receiptId);
    const currentFormalReceipt = trace.transportLedger.find(row => row.requestId === 'builder-72-1');
    const formalPreview = trace.transportLedger.find(row => row.path === 'preview'
      && row.requestBody.receiptId === currentFormalReceipt?.response.receiptId);
    assert(staleSetupReceipt && setupCommand && currentSetupReceipt && setupPreview
      && currentFormalReceipt && formalPreview);
    assert.deepEqual(staleSetupReceipt.reconcileOwnerChangeSetupCommandIds,
      [setupCommand.requestBody.commandId],
    `${setupReceiptCase} must retain the exact successful setup command as the reconcile's owner change`);
    assert.deepEqual(staleSetupReceipt.successfulPreviewConsumerTransportIndices, [],
      `${setupReceiptCase} must not credit the stale receipt to the later preview`);
    assert.deepEqual(currentSetupReceipt.successfulPreviewConsumerTransportIndices, [setupPreview.transportIndex]);
    assert.deepEqual(setupPreview.currentReceiptProducerTransportIndices, [currentSetupReceipt.transportIndex]);
    assert.deepEqual(currentFormalReceipt.successfulPreviewConsumerTransportIndices, [formalPreview.transportIndex]);
    if (setupReceiptCase === 'command-starts-first') {
      assert.deepEqual([
        setupCommand.requestEventIndex,
        staleSetupReceipt.requestEventIndex,
        setupCommand.settlementEventIndex,
        staleSetupReceipt.settlementEventIndex,
      ], [1, 2, 3, 4], 'the overlap case must preserve the exact command-starts-first event order');
    }
    assert.equal(staleSetupReceipt.ownerInvalidationObserved,
      setupReceiptCase !== 'reconcile-before-command',
      'pre-response invalidation must follow the actual setup command settlement order');
  }
});

test('the compiler rejects a setup settlement moved across the captured formal-entry boundary at shape validation', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const setupMutation = trace.transportLedger.find(row => row.capturePhase === 'SETUP' && row.setupMutation);
  assert(setupMutation);
  const originalBoundary = trace.formalEntry.afterEventIndex;
  assert(setupMutation.settlementEventIndex <= originalBoundary);

  trace.formalEntry.afterEventIndex = setupMutation.settlementEventIndex - 1;

  assert.throws(() => validatePolicyOneJourneyTrace(trace),
    /setup transport .* settle before formal entry/);
  assert.throws(() => compilePolicyOneJourneyTrace(trace, { moduleName: 'PolicySetupSettlementCrossesEntry' }),
    /setup transport .* settle before formal entry/);
});

test('the registered Policy One contract matches captured actions, compiler events, and self-test controls', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace();
  const nativeJourney = policyOneContract.nativeJourney;
  assert.equal(nativeJourney.captureAdapter, 'policy-one-disagreement-v1');
  assert.equal(nativeJourney.nativeCaseKey, 'builder-authoring/patient-one-disagreement');
  assert.equal(nativeJourney.traceName, trace.name);
  assert.equal(nativeJourney.model, trace.model);
  assert.deepEqual(trace.actions.map(({ label, event }) => ({ label, event })), nativeJourney.actions);
  const compiled = compilePolicyOneJourneyTrace(trace);
  assert.deepEqual(compiled.expectedLabels, nativeJourney.actions.map(action => action.label));
  assert.deepEqual(compiled.expectedEvents, nativeJourney.actions.map(action => action.event));
  const requiredActions = [...new Set(nativeJourney.actions.map(action => action.label))];
  const requiredEvents = [...new Set(nativeJourney.actions.map(action => action.event))];
  const actionCounts = Object.fromEntries(requiredActions.map(label => [label,
    nativeJourney.actions.filter(action => action.label === label).length]));
  assert.deepEqual(policyOneContract.models[nativeJourney.model].actions, requiredActions);
  assert.deepEqual(policyOneContract.phase1.requiredModels, [nativeJourney.model]);
  assert.deepEqual(policyOneContract.phase1.requiredActions, requiredActions);
  assert.deepEqual(policyOneContract.phase1.requiredEvents, requiredEvents);
  assert.deepEqual(policyOneContract.phase1.requiredActionsByTrace[nativeJourney.traceName], requiredActions);
  assert.deepEqual(policyOneContract.phase1.requiredActionCountsByTrace[nativeJourney.traceName], actionCounts);
  for (const label of requiredActions) {
    assert.equal(policyOneContract.actionBindings[label]?.event,
      nativeJourney.actions.find(action => action.label === label).event);
  }
  assert.deepEqual(buildPolicyOneMutationControls(trace).map(control => control.name).sort(),
    [...nativeJourney.requiredMutationControlNames].sort());
});

for (const [name, mutate, message] of captureMutations) {
  test(`the adapter rejects ${name}`, () => {
    const input = makePolicyOneTraceInput();
    mutate(input);
    assert.throws(() => buildPolicyOneTrace(input), message);
  });
}

test('the adapter rejects response requestId mismatches even when captureIds match', () => {
  const input = makePolicyOneTraceInput();
  input.responses.all.requestId = 'not-the-request-header';
  assert.throws(() => buildPolicyOneTrace(input), /response requestId must match its request header/);
});

test('the adapter rejects an Apply command that is not linked to the ALL proposal choice', () => {
  const input = makePolicyOneTraceInput();
  input.requests.apply.body.commands[0].constructionChoice.choiceId = 'other-choice';
  assert.throws(() => buildPolicyOneTrace(input), /exact selected field choice/);
});

test('the adapter rejects an Apply command targeting another output', () => {
  const input = makePolicyOneTraceInput();
  input.requests.apply.body.commands[0].outputId = 'other-output';
  assert.throws(() => buildPolicyOneTrace(input), /Apply command must target the expected output/);
});

test('the transport ledger accounts for explicitly supported read-only routes and rejects unknown POSTs', () => {
  const input = makePolicyOneTraceInput();
  input.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'POST',
    path: 'construction-inputs',
    requestBody: { outputId: 'output-1', field: 'Patient.id' },
  }));
  const trace = buildPolicyOneTrace(input).journeyTrace;
  const readOnly = trace.transportLedger.at(-1);
  assert.equal(readOnly.classification, 'read-only');
  assert.equal(readOnly.mutatesDraft, false);
  assert.equal(readOnly.readOnlyReason, 'Construction stage inputs read');

  const readOnlyGet = makePolicyOneTraceInput();
  readOnlyGet.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'GET',
    path: 'builder',
  }));
  assert.equal(buildPolicyOneTrace(readOnlyGet).journeyTrace.transportLedger.at(-1).readOnlyReason,
    'BuilderState read');

  const canceledReadOnly = makePolicyOneTraceInput();
  canceledReadOnly.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'POST',
    path: 'semantic-inventory',
    requestBody: { outputId: 'output-1', rowRoot: 'Patient', limit: 50 },
    status: null,
    responseHeaders: { status: 200, requestId: 'inventory-header-id' },
    responseObservedBeforeFailure: true,
    responseReadError: 'response body canceled after navigation',
    failure: 'net::ERR_ABORTED',
  }));
  const canceledTransport = buildPolicyOneTrace(canceledReadOnly).journeyTrace.transportLedger.at(-1);
  assert.equal(canceledTransport.settlement, 'request-failed');
  assert.deepEqual(canceledTransport.responseHeaders,
    { status: 200, requestId: 'inventory-header-id' });
  assert.equal(canceledTransport.responseObservedBeforeFailure, true);
  assert.equal(canceledTransport.responseReadError, 'response body canceled after navigation');
  assert.equal(canceledTransport.status, null);
  assert.equal(canceledTransport.responseOwnerIndex, null);

  const unknownPost = makePolicyOneTraceInput();
  unknownPost.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'POST',
    path: 'unregistered-endpoint',
    requestBody: {},
  }));
  assert.throws(() => buildPolicyOneTrace(unknownPost), /unsupported authoring transport POST unregistered-endpoint/);

  const unknownGet = makePolicyOneTraceInput();
  unknownGet.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'GET',
    path: 'unregistered-endpoint',
  }));
  assert.throws(() => buildPolicyOneTrace(unknownGet), /unsupported authoring transport GET unregistered-endpoint/);
});

test('the adapter binds completed frame source options reads to the formal Explorer and saved snapshot', () => {
  const appendFrameSourceOptions = (input, requestBody = {
    snapshotToken: 'snapshot-1', outputId: 'output-1', resourceType: 'Patient', limit: 50,
  }, response = {
    snapshotToken: 'snapshot-1', outputIds: ['output-1'],
    frameSourceOptionsOutputId: 'output-1', frameSourceOptionsSnapshotToken: 'snapshot-1',
    frameSourceOptionsComplete: true, frameSourceOptionsTruncated: false,
    frameSourceOptionsSourceCount: 0, frameSourceOptionsNextCursor: null,
  }) => {
    input.context.transportCaptures.push(extraTransport({
      transportIndex: 4,
      requestEventIndex: 7,
      settlementEventIndex: 8,
      method: 'POST',
      path: 'frame-source-options',
      ownerScope: input.context.transportCaptures[0].ownerScope,
      requestBody,
      response,
    }));
    return input;
  };

  const input = appendFrameSourceOptions(makePolicyOneTraceInput());
  const trace = buildPolicyOneTrace(input).journeyTrace;
  const row = trace.transportLedger.at(-1);
  assert.equal(row.classification, 'read-only');
  assert.equal(row.readOnlyReason, 'Frame source options read');
  assert.equal(row.mutatesDraft, false);
  assert.equal(row.setupMutation, false);
  assert.equal(row.requestBody.outputId, 'output-1');
  assert.equal(row.requestBody.snapshotToken, 'snapshot-1');
  assert.equal(row.response.frameSourceOptionsOutputId, 'output-1');
  assert.equal(row.response.frameSourceOptionsSourceCount, 0);

  const foreignOwner = appendFrameSourceOptions(makePolicyOneTraceInput());
  const foreignRow = foreignOwner.context.transportCaptures.at(-1);
  foreignRow.ownerScope = `${foreignRow.ownerScope}/foreign`;
  foreignRow.resourcePath = `${foreignRow.ownerScope}/authoring/v2/${foreignRow.path}`;
  assert.throws(() => buildPolicyOneTrace(foreignOwner), /exact formal Explorer owner scope/);

  const staleSnapshot = appendFrameSourceOptions(makePolicyOneTraceInput(), {
    snapshotToken: 'stale-snapshot', outputId: 'output-1', resourceType: 'Patient', limit: 50,
  });
  assert.throws(() => buildPolicyOneTrace(staleSnapshot), /must use the formal entry snapshot/);

  const wrongOutputEcho = appendFrameSourceOptions(makePolicyOneTraceInput(), undefined, {
    snapshotToken: 'snapshot-1', outputIds: ['other-output'],
    frameSourceOptionsOutputId: 'other-output', frameSourceOptionsSnapshotToken: 'snapshot-1',
    frameSourceOptionsComplete: true, frameSourceOptionsTruncated: false,
    frameSourceOptionsSourceCount: 0, frameSourceOptionsNextCursor: null,
  });
  assert.throws(() => buildPolicyOneTrace(wrongOutputEcho), /response must echo its output/);

  const treatedAsMutation = makeSyntheticPolicyOneJourneyTrace({ frameSourceOptions: true });
  treatedAsMutation.transportLedger.at(-1).mutatesDraft = true;
  assert.throws(() => validatePolicyOneJourneyTrace(treatedAsMutation), /cannot be classified as a draft mutation/);
});

test('the adapter retains only exact-scope pending support reads at an observed formal exit', () => {
  const input = makePolicyOneTraceInput();
  const ownerScope = input.context.transportCaptures[0].ownerScope;
  input.context.transportCaptures.push(
    pendingSupportCapture({
      transportIndex: 4,
      requestEventIndex: 7,
      path: 'semantic-inventory',
      ownerScope,
      requestId: 'feature-catalog-unit-physical-1',
      requestBody: {
        outputId: 'output-1', snapshotToken: 'snapshot-1', rowRoot: 'Patient', limit: 50,
        selectionMode: 'SYSTEM_VERSION_CATEGORY',
      },
    }),
    pendingSupportCapture({
      transportIndex: 5,
      requestEventIndex: 8,
      path: 'construction-capabilities',
      ownerScope,
      requestId: 'construction-capabilities-unit-physical-2',
      requestBody: {
        outputId: 'output-1', snapshotToken: 'snapshot-1', expectedDraftVersion: 3,
        expectedDraftDigest: 'draft-after', stageId: 'source_projection',
      },
    }),
  );
  input.context.formalExit = {
    afterTransportIndex: 5,
    afterEventIndex: 8,
    observedAt: Date.now(),
    drainTimeoutMs: 5000,
    deadlineReached: true,
    pendingTransportIndices: [4, 5],
    pendingResponseReadTransportIndices: [],
  };

  const trace = buildPolicyOneTrace(input).journeyTrace;
  const pending = trace.transportLedger.filter(entry => entry.settlement === 'pending');
  assert.deepEqual(pending.map(entry => [entry.path, entry.classification, entry.ownerScope]), [
    ['semantic-inventory', 'read-only', ownerScope],
    ['construction-capabilities', 'read-only', ownerScope],
  ]);
  assert.deepEqual(trace.formalExit.pendingTransportIndices, [4, 5]);
  assert.deepEqual(pending.map(entry => ({
    status: entry.status,
    response: entry.response,
    responseOwnerIndex: entry.responseOwnerIndex,
    currentReceiptProducerTransportIndices: entry.currentReceiptProducerTransportIndices,
    successfulPreviewConsumerTransportIndices: entry.successfulPreviewConsumerTransportIndices,
  })), [
    { status: null, response: null, responseOwnerIndex: null,
      currentReceiptProducerTransportIndices: [], successfulPreviewConsumerTransportIndices: [] },
    { status: null, response: null, responseOwnerIndex: null,
      currentReceiptProducerTransportIndices: [], successfulPreviewConsumerTransportIndices: [] },
  ]);

  const foreignOwner = structuredClone(input);
  const foreign = foreignOwner.context.transportCaptures[3];
  foreign.ownerScope = 'https://fixture.invalid/api/explorers/other';
  foreign.resourcePath = `${foreign.ownerScope}/authoring/v2/${foreign.path}`;
  assert.throws(() => buildPolicyOneTrace(foreignOwner), /exact formal Explorer owner scope/);

  const pendingCommand = structuredClone(input);
  const command = pendingCommand.context.transportCaptures[2];
  command.settlement = 'pending';
  delete command.settlementEventIndex;
  command.status = null;
  command.responseOwnerIndex = null;
  command.responseHeaders = null;
  command.response = null;
  pendingCommand.context.transportCaptures[3].requestEventIndex = 6;
  pendingCommand.context.transportCaptures[4].requestEventIndex = 7;
  pendingCommand.context.formalExit = {
    afterTransportIndex: 5,
    afterEventIndex: 7,
    observedAt: Date.now(),
    drainTimeoutMs: 5000,
    deadlineReached: true,
    pendingTransportIndices: [3, 4, 5],
    pendingResponseReadTransportIndices: [],
  };
  assert.throws(() => buildPolicyOneTrace(pendingCommand), /explicitly supported formal read-only route/);

  const pendingReconcile = makePolicyOneTraceInput();
  pendingReconcile.context.transportCaptures.push(pendingSupportCapture({
    transportIndex: 4,
    requestEventIndex: 7,
    path: 'reconcile',
    ownerScope: pendingReconcile.context.transportCaptures[0].ownerScope,
    requestId: 'builder-9-1',
    requestBody: { snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-after' },
  }));
  pendingReconcile.context.formalExit = {
    afterTransportIndex: 4,
    afterEventIndex: 7,
    observedAt: Date.now(),
    drainTimeoutMs: 5000,
    deadlineReached: true,
    pendingTransportIndices: [4],
    pendingResponseReadTransportIndices: [],
  };
  assert.throws(() => buildPolicyOneTrace(pendingReconcile), /explicitly supported formal read-only route/);

  const missingBoundary = makePolicyOneTraceInput();
  missingBoundary.context.transportCaptures.push(pendingSupportCapture({
    transportIndex: 4,
    requestEventIndex: 7,
    path: 'semantic-inventory',
    ownerScope,
    requestId: 'feature-catalog-unit-physical-3',
    requestBody: { outputId: 'output-1', rowRoot: 'Patient', limit: 50 },
  }));
  assert.throws(() => buildPolicyOneTrace(missingBoundary), /actual formal exit boundary/);
});

test('the transport ledger rejects a missing event or a request not bijected to its raw row', () => {
  const missingEvent = makePolicyOneTraceInput();
  missingEvent.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 9,
    method: 'GET',
    path: 'builder',
  }));
  assert.throws(() => buildPolicyOneTrace(missingEvent), /every observed request and terminal event/);

  const bodyMismatch = makePolicyOneTraceInput();
  bodyMismatch.context.transportCaptures[0].requestBody = { ...bodyMismatch.context.transportCaptures[0].requestBody,
    snapshotToken: 'different-snapshot' };
  assert.throws(() => buildPolicyOneTrace(bodyMismatch), /raw request body must match its request capture/);

  const missingCaptureId = makePolicyOneTraceInput();
  missingCaptureId.context.transportCaptures[0].requestCaptureId = null;
  assert.throws(() => buildPolicyOneTrace(missingCaptureId), /transport capture ID must match its exact request capture/);
});

test('reconcile rows bind the actual owner and CAS while preserving set-valued receipt relations', () => {
  const input = makePolicyOneTraceInput();
  const sharedReceipt = {
    status: 200,
    receiptId: 'same-content-receipt',
    receiptIds: ['same-content-receipt'],
    outputIds: ['output-1'],
    snapshotToken: 'snapshot-1',
    intentDigest: 'intent-current',
  };
  const reconcile = (transportIndex, requestEventIndex, settlementEventIndex, generation) => extraTransport({
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    method: 'POST',
    path: 'reconcile',
    requestId: `builder-${generation}-1`,
    requestBody: { snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-after' },
    response: { ...sharedReceipt, requestId: `builder-${generation}-1` },
  });
  input.context.transportCaptures.push(
    reconcile(4, 7, 10, 10),
    reconcile(5, 8, 9, 11),
    extraTransport({
      transportIndex: 6,
      requestEventIndex: 11,
      settlementEventIndex: 12,
      method: 'POST',
      path: 'preview',
      requestBody: { receiptId: 'same-content-receipt', outputId: 'output-1' },
      response: {
        receiptId: 'same-content-receipt',
        receiptIds: ['same-content-receipt'],
        outputIds: ['output-1'],
      },
    }),
  );

  const rows = buildPolicyOneTrace(input).journeyTrace.transportLedger;
  const first = rows[3];
  const second = rows[4];
  const preview = rows[5];
  assert.deepEqual(first.reconcileOwner, {
    requestId: 'builder-10-1', compileGeneration: 10, attempt: 1,
  });
  assert.deepEqual(first.reconcileCAS, {
    snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-after', outputId: null,
  });
  assert.equal(first.ownerInvalidationObserved, true);
  assert.equal(first.reconcileDisposition, 'invalidated-by-captured-owner-event');
  assert.deepEqual(first.invalidatedByTransportIndices, [5],
    'only a same-explorer newer generation started before the old response invalidates its owner');
  assert.equal(second.ownerInvalidationObserved, false);
  assert.equal(second.reconcileDisposition, 'no-captured-owner-invalidation');
  assert.deepEqual(first.successfulPreviewConsumerTransportIndices, [6]);
  assert.deepEqual(second.successfulPreviewConsumerTransportIndices, [6]);
  assert.equal(preview.previewDisposition, 'successful-response');
  assert.deepEqual(preview.currentReceiptProducerTransportIndices, [4, 5],
    'identical receipt identities preserve every compatible producer relation without choosing one parent');
});

test('a later reconcile cannot retroactively invalidate an already settled owner', () => {
  const input = makePolicyOneTraceInput();
  const reconcile = (transportIndex, requestEventIndex, settlementEventIndex, generation) => extraTransport({
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    method: 'POST',
    path: 'reconcile',
    requestId: `builder-${generation}-1`,
    requestBody: { snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-before' },
    response: {
      status: 200,
      requestId: `builder-${generation}-1`,
      receiptId: `receipt-${generation}`,
      receiptIds: [`receipt-${generation}`],
      outputIds: ['output-1'],
      snapshotToken: 'snapshot-1',
      intentDigest: `intent-${generation}`,
    },
  });
  input.context.transportCaptures.push(reconcile(4, 7, 8, 20), reconcile(5, 9, 10, 21));
  const rows = buildPolicyOneTrace(input).journeyTrace.transportLedger;
  assert.equal(rows[3].ownerInvalidationObserved, false);
  assert.deepEqual(rows[3].invalidatedByTransportIndices, []);
  assert.equal(rows[4].ownerInvalidationObserved, false);
});

test('a reconcile request failure remains a settled owner/CAS ledger row', () => {
  const input = makePolicyOneTraceInput();
  input.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'POST',
    path: 'reconcile',
    requestId: 'builder-6-1',
    requestBody: { snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-before' },
    status: null,
  }));
  const row = buildPolicyOneTrace(input).journeyTrace.transportLedger.at(-1);
  assert.equal(row.classification, 'reconcile');
  assert.equal(row.settlement, 'request-failed');
  assert.equal(row.ownerInvalidationObserved, false);
  assert.equal(row.reconcileDisposition, 'request-failed');
  assert.deepEqual(row.invalidatedByTransportIndices, []);
});

test('preview HTTP failures and wrong successful echoes do not count as receipt consumption', () => {
  const addReconcileAndPreview = (input, { previewStatus, receiptResponse = 'receipt-from-reconcile' }) => {
    input.context.transportCaptures.push(
      extraTransport({
        transportIndex: 4,
        requestEventIndex: 7,
        settlementEventIndex: 8,
        method: 'POST',
        path: 'reconcile',
        requestId: 'builder-30-1',
        requestBody: { snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-after' },
        response: {
          status: 200,
          requestId: 'builder-30-1',
          receiptId: 'receipt-from-reconcile',
          receiptIds: ['receipt-from-reconcile'],
          outputIds: ['output-1'],
          snapshotToken: 'snapshot-1',
          intentDigest: 'intent-current',
        },
      }),
      extraTransport({
        transportIndex: 5,
        requestEventIndex: 9,
        settlementEventIndex: 10,
        method: 'POST',
        path: 'preview',
        requestBody: { receiptId: 'receipt-from-reconcile', outputId: 'output-1' },
        status: previewStatus,
        response: {
          receiptId: receiptResponse,
          receiptIds: [receiptResponse],
          outputIds: ['output-1'],
        },
      }),
    );
  };

  const failedPreview = makePolicyOneTraceInput();
  addReconcileAndPreview(failedPreview, { previewStatus: 500 });
  const failedTrace = buildPolicyOneTrace(failedPreview).journeyTrace;
  assert.equal(failedTrace.transportLedger[4].previewDisposition, 'http-error');
  assert.deepEqual(failedTrace.transportLedger[4].currentReceiptProducerTransportIndices, [4],
    'the submitted receipt has a current producer even when the preview request fails');
  assert.deepEqual(failedTrace.transportLedger[3].successfulPreviewConsumerTransportIndices, [],
    'HTTP 500 cannot certify consumption of the receipt');

  const wrongEcho = makePolicyOneTraceInput();
  addReconcileAndPreview(wrongEcho, { previewStatus: 200, receiptResponse: 'wrong-receipt' });
  assert.throws(() => buildPolicyOneTrace(wrongEcho), /successful response must echo its submitted receipt/);

  const wrongOwner = makePolicyOneTraceInput();
  addReconcileAndPreview(wrongOwner, { previewStatus: 200 });
  wrongOwner.context.transportCaptures[4].responseOwnerIndex = 4;
  assert.throws(() => buildPolicyOneTrace(wrongOwner), /response must retain its exact request owner/);
});

test('successful saved-CAS changes expire stale reconcile and initial receipt producers', () => {
  const trace = makeSyntheticPolicyOneJourneyTrace({ sourceReceiptCase: 'owner-change-refresh' });
  const reconciles = trace.transportLedger.filter(row => row.classification === 'reconcile');
  const oldReceipt = reconciles.find(row => row.reconcileOwner.compileGeneration === 60);
  const currentReceipt = reconciles.find(row => row.reconcileOwner.compileGeneration === 61);
  const preview = trace.transportLedger.find(row => row.path === 'preview');
  assert(oldReceipt && currentReceipt && preview);
  assert.deepEqual(oldReceipt.reconcileOwnerChangeCaptureIds, ['capture-removal-apply'],
    'the old receipt records the exact successful command owner change after its request');
  assert.deepEqual(oldReceipt.successfulPreviewConsumerTransportIndices, [],
    'the old receipt cannot claim a current successful preview consumer after saved CAS changes');
  assert.deepEqual(currentReceipt.successfulPreviewConsumerTransportIndices, [preview.transportIndex]);
  assert.deepEqual(preview.currentReceiptProducerTransportIndices, [currentReceipt.transportIndex],
    'the later same-content receipt is the current producer after the command changes saved CAS');
  assert.equal(preview.hasInitialCurrentReceiptProducer, false);

  const beforeResponse = makeSyntheticPolicyOneJourneyTrace({ sourceReceiptCase: 'owner-change-before-response' });
  const beforeReconciles = beforeResponse.transportLedger.filter(row => row.classification === 'reconcile');
  const beforeOldReceipt = beforeReconciles.find(row => row.reconcileOwner.compileGeneration === 60);
  const beforeCurrentReceipt = beforeReconciles.find(row => row.reconcileOwner.compileGeneration === 61);
  const beforeCommand = beforeResponse.transportLedger.find(row => row.requestCaptureId === 'capture-removal-apply');
  const beforePreview = beforeResponse.transportLedger.find(row => row.path === 'preview');
  assert(beforeOldReceipt && beforeCurrentReceipt && beforeCommand && beforePreview);
  assert(beforeOldReceipt.requestEventIndex < beforeCommand.settlementEventIndex);
  assert(beforeCommand.settlementEventIndex < beforeOldReceipt.settlementEventIndex,
    'the pre-response case must settle the saved-CAS change while the old reconcile is pending');
  assert.deepEqual(beforeOldReceipt.reconcileOwnerChangeCaptureIds, ['capture-removal-apply']);
  assert.deepEqual(beforeOldReceipt.invalidatedByTransportIndices, [beforeCommand.transportIndex,
    beforeCurrentReceipt.transportIndex]);
  assert.deepEqual(beforeOldReceipt.successfulPreviewConsumerTransportIndices, []);
  assert.deepEqual(beforeCurrentReceipt.successfulPreviewConsumerTransportIndices, [beforePreview.transportIndex]);
  assert.deepEqual(beforePreview.currentReceiptProducerTransportIndices, [beforeCurrentReceipt.transportIndex],
    'a mutation before the old reconcile response must also expire its receipt');

  const initialReceipt = makePolicyOneTraceInput();
  initialReceipt.context.initialObservation.draftVersion = 1;
  initialReceipt.context.initialObservation.draftDigest = 'draft-root';
  initialReceipt.context.initialPreview.binding = {
    status: 'ready', receiptId: 'initial-source-receipt', outputId: 'output-1',
    draftVersion: 1, draftDigest: 'draft-root',
  };
  initialReceipt.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 8,
    method: 'POST',
    path: 'preview',
    ownerScope: initialReceipt.context.transportCaptures[0].ownerScope,
    requestBody: { receiptId: 'initial-source-receipt', outputId: 'output-1' },
    response: {
      receiptId: 'initial-source-receipt',
      receiptIds: ['initial-source-receipt'],
      outputIds: ['output-1'],
    },
  }));
  assert.throws(() => buildPolicyOneTrace(initialReceipt),
    /must have a current captured or initial receipt producer/);
});
