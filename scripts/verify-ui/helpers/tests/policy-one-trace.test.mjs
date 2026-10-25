import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { buildPolicyOneTrace, POLICY_TRACE_ABSENT } from '../policy-one-trace.mjs';
import {
  buildPolicyOneMutationControls,
  compilePolicyOneJourneyTrace,
} from '../policy-one-tla-replay.mjs';
import {
  patientOneSavedState,
  policyApplyTraceExpectation,
  policyWorkspaceProjection,
} from '../../workflows/builder-patient-one-disagreement.mjs';
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
}) => {
  return {
    transportIndex,
    requestEventIndex,
    settlementEventIndex,
    method,
    path,
    resourcePath: `${ownerScope}/authoring/v2/${path}`,
    ownerScope,
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
