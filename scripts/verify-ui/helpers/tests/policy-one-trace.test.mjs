import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPolicyOneTrace, POLICY_TRACE_ABSENT } from '../policy-one-trace.mjs';
import {
  patientOneSavedState,
  policyApplyTraceExpectation,
} from '../../workflows/builder-patient-one-disagreement.mjs';
import { makePolicyOneTraceInput } from './fixtures/policy-one-journey.mjs';

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
  status = 200,
  response,
}) => {
  const origin = 'https://fixture.invalid';
  const ownerPrefix = '/api/explorers/policy-one';
  return {
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    method,
    path,
    resourcePath: `${origin}${ownerPrefix}/authoring/v2/${path}`,
    ownerScope: `${origin}${ownerPrefix}`,
    requestId,
    requestCaptureId: null,
    requestBody,
    settlement: status === null ? 'request-failed' : 'response',
    responseOwnerIndex: status === null ? null : transportIndex,
    status,
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

test('the transport ledger rejects a missing event or a request not bijected to its raw row', () => {
  const missingEvent = makePolicyOneTraceInput();
  missingEvent.context.transportCaptures.push(extraTransport({
    transportIndex: 4,
    requestEventIndex: 7,
    settlementEventIndex: 9,
    method: 'GET',
    path: 'builder',
  }));
  assert.throws(() => buildPolicyOneTrace(missingEvent), /account for every request and settlement event/);

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
    requestBody: { snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-before' },
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
    snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-before', outputId: null,
  });
  assert.equal(first.ownerInvalidationObserved, true);
  assert.equal(first.reconcileDisposition, 'invalidated-by-captured-owner-event');
  assert.deepEqual(first.invalidatedByTransportIndices, [5],
    'only a same-explorer newer generation started before the old response invalidates its owner');
  assert.equal(second.ownerInvalidationObserved, false);
  assert.equal(second.reconcileDisposition, 'no-captured-owner-invalidation');
  assert.deepEqual(first.previewConsumerTransportIndices, [6]);
  assert.deepEqual(second.previewConsumerTransportIndices, [6]);
  assert.deepEqual(preview.receiptProducerTransportIndices, [4, 5],
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
