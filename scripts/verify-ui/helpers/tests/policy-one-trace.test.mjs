import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPolicyOneTrace, POLICY_TRACE_ABSENT } from '../policy-one-trace.mjs';
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
