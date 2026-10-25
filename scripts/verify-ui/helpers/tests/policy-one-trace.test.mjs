import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPolicyOneTrace, POLICY_TRACE_ABSENT } from '../policy-one-trace.mjs';

const eventNames = [
  'OneSubmitted',
  'OneRejected',
  'SelectAll',
  'AllSubmitted',
  'AllReady',
  'AllApplySubmitted',
  'AllApplied',
];

function makeObservation() {
  const oneRequestObject = {};
  const allRequestObject = {};
  const applyRequestObject = {};
  const oneResponseObject = {};
  const allResponseObject = {};
  const applyResponseObject = {};
  const beforeWorkspace = {
    documents: [{
      id: 'document-1',
      columns: [{ column: 'patient-id', binding: 'Patient.id' }],
      construction: { group: { keys: [], aggregate: 'COUNT_ROWS', rowCount: 1, count: 2 } },
    }],
  };
  const beforeProjection = {
    documents: [{ id: 'document-1', sourceColumnIds: ['patient-id'], group: { keys: [], aggregate: 'COUNT_ROWS', count: 2 } }],
  };
  const afterApplyWorkspace = {
    documents: [{
      id: 'document-1',
      columns: [
        { column: 'patient-id', binding: 'Patient.id' },
        { column: 'patient-id-values', binding: 'Patient.id', rowValuePolicy: 'ALL' },
      ],
      construction: { group: { keys: [], aggregate: 'COUNT_ROWS', rowCount: 1, count: 2 } },
    }],
  };
  const afterApplyProjection = {
    documents: [{ id: 'document-1', sourceColumnIds: ['patient-id'], allColumnId: 'patient-id-values', group: { keys: [], aggregate: 'COUNT_ROWS', count: 2 } }],
  };
  const group = { keys: [], aggregate: 'COUNT_ROWS', rowCount: 1, count: 2 };
  const before = {
    workspace: beforeWorkspace,
    projection: beforeProjection,
    group,
    snapshotToken: 'snapshot-1',
    draftVersion: 3,
    draftDigest: 'draft-before',
  };
  const afterOne = structuredClone(before);
  const afterApply = {
    workspace: afterApplyWorkspace,
    projection: afterApplyProjection,
    group: structuredClone(group),
    snapshotToken: 'snapshot-1',
    draftVersion: 4,
    draftDigest: 'draft-after',
    policy: 'ALL',
    choiceId: 'choice-patient-id',
    outputId: 'output-1',
  };
  const choice = policy => ({
    choiceId: 'choice-patient-id',
    form: 'RELATED_SOURCE',
    rowValuePolicy: policy,
  });
  const proposalBody = (policy, commandId) => ({
    commandId,
    outputId: 'output-1',
    snapshotToken: 'snapshot-1',
    expectedDraftVersion: 3,
    expectedDraftDigest: 'draft-before',
    constructionChoices: [choice(policy)],
  });

  return {
    expected: {
      proposalPath: '/authoring/v2/construction-choice-proposals',
      commandPath: '/authoring/v2/commands',
      outputId: 'output-1',
      choiceId: 'choice-patient-id',
      field: 'Patient.id',
      form: 'RELATED_SOURCE',
      snapshotToken: 'snapshot-1',
      draftVersion: 3,
      draftDigest: 'draft-before',
      one: { policy: 'ONE', status: 422, errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' },
      all: {
        policy: 'ALL', status: 200, previewStatus: 'READY', rowCount: 1,
        memberIdsField: 'patient_ids', countField: 'count', count: 2,
      },
      apply: { policy: 'ALL', status: 200 },
      editorIdentity: { editorId: 'editor-mount-1', documentTimeOrigin: 1730137200000 },
    },
    requests: {
      one: {
        requestObject: oneRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/construction-choice-proposals',
        requestId: 'header-one',
        captureId: 'capture-one-random',
        commandId: 'command-one',
        body: proposalBody('ONE', 'command-one'),
      },
      all: {
        requestObject: allRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/construction-choice-proposals',
        requestId: 'header-all',
        captureId: 'capture-all-random',
        commandId: 'command-all',
        body: proposalBody('ALL', 'command-all'),
      },
      apply: {
        requestObject: applyRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/commands',
        requestId: 'header-apply',
        captureId: 'capture-apply-random',
        commandId: 'command-all',
        body: {
          commandId: 'command-all',
          outputId: 'output-1',
          snapshotToken: 'snapshot-1',
          expectedDraftVersion: 3,
          expectedDraftDigest: 'draft-before',
          commands: [{
            type: 'APPLY_CONSTRUCTION_CHOICE',
            outputId: 'output-1',
            constructionChoice: choice('ALL'),
          }],
        },
      },
    },
    responses: {
      one: {
        responseObject: oneResponseObject,
        requestObject: oneRequestObject,
        requestId: 'header-one',
        captureId: 'capture-one-random',
        status: 422,
        errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
      },
      all: {
        responseObject: allResponseObject,
        requestObject: allRequestObject,
        requestId: 'header-all',
        captureId: 'capture-all-random',
        commandId: 'command-all',
        status: 200,
        preview: {
          status: 'READY',
          receiptId: 'receipt-all',
          outputId: 'output-1',
          rowCount: 1,
          rows: [{ patient_ids: ['dev-patient-002', 'dev-patient-001'], count: 2 }],
        },
      },
      apply: {
        responseObject: applyResponseObject,
        requestObject: applyRequestObject,
        requestId: 'header-apply',
        captureId: 'capture-apply-random',
        commandId: 'command-all',
        status: 200,
        body: {
          draftVersion: 4,
          draftDigest: 'draft-after',
          workspace: afterApplyWorkspace,
        },
      },
    },
    saved: { before, afterOne, afterApply },
    dom: {
      beforeOne: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ONE',
        availablePolicies: ['ONE', 'ALL'],
      },
      afterOne: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ONE',
        availablePolicies: ['ONE', 'ALL'],
        errorVisible: true,
      },
      afterSelectAll: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ALL',
        availablePolicies: ['ONE', 'ALL'],
        errorVisible: true,
      },
      afterAllSubmitted: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ALL',
        availablePolicies: ['ONE', 'ALL'],
      },
      afterAllReady: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ALL',
        availablePolicies: ['ONE', 'ALL'],
      },
      afterApplySubmitted: {
        mounted: true,
        documentId: 'output-1',
        editorId: 'editor-mount-1',
        documentTimeOrigin: 1730137200000,
        selectedField: 'Patient.id',
        fieldChecked: true,
        form: 'RELATED_SOURCE',
        policy: 'ALL',
        availablePolicies: ['ONE', 'ALL'],
      },
    },
    fixtureIDs: ['dev-patient-001', 'dev-patient-002'],
    context: {
      name: 'builder-authoring:patient-one-disagreement',
      model: 'PolicyOneDisagreement',
      initialObservation: {
        savedState: { groupKeys: [], aggregate: 'COUNT_ROWS', count: 2 },
        rowIds: ['dev-patient-001', 'dev-patient-002'],
      },
      initialPreview: {
        outputId: 'output-1', rowCount: 2, rows: [
          { patientId: 'dev-patient-001' }, { patientId: 'dev-patient-002' },
        ],
      },
      actions: [
        { label: 'GroupPreviewReady', event: 'preview-response', observation: { rowCount: 1, count: 2 } },
        { label: 'GroupApplied', event: 'apply-command-response', observation: { draftVersion: 3 } },
        { label: 'ReloadedAll', event: 'reload', observation: { policy: 'ALL' } },
        { label: 'RemovalCancelled', event: 'group-removal-cancel', observation: { unchanged: true } },
      ],
      unmodeledEvents: [],
      backendCalls: [],
      outcome: {
        savedState: { sourceColumns: ['patient-id'], group: null },
        rowIds: ['dev-patient-001', 'dev-patient-002'],
      },
    },
  };
}

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
  ['hidden refusal after selecting ALL', input => { input.dom.afterSelectAll.errorVisible = false; }, /visible ONE refusal/],
  ['wrong editor document time origin', input => { input.dom.afterApplySubmitted.documentTimeOrigin += 1; }, /same document time origin/],
  ['missing expected member IDs', input => { delete input.responses.all.preview.rows[0].patient_ids; }, /selected member ID array/],
  ['wrong preview member IDs', input => { input.responses.all.preview.rows[0].patient_ids = ['dev-patient-001', 'dev-patient-003']; }, /match the independent fixture IDs/],
  ['wrong preview row count', input => { input.responses.all.preview.rowCount = 2; }, /rowCount must match the expected count/],
  ['wrong grouped count', input => { input.responses.all.preview.rows[0].count = 3; }, /expected grouped row count/],
  ['wrong echoed successful commandId', input => { input.responses.all.commandId = 'other-command'; }, /must match its actual request/],
  ['Apply uses a different commandId', input => {
    input.requests.apply.commandId = 'other-command';
    input.requests.apply.body.commandId = 'other-command';
  }, /reuse the successful ALL response commandId/],
  ['Apply carries the wrong choice policy', input => { input.requests.apply.body.commands[0].constructionChoice.rowValuePolicy = 'ONE'; }, /request must use ALL/],
  ['Apply response is owned by another request', input => { input.responses.apply.requestObject = {}; }, /exact captured request object/],
];

test('a valid ONE rejection, same-editor ALL repair, and linked Apply produce a finite model trace', () => {
  const input = makeObservation();
  Object.freeze(input.expected);
  const { journeyTrace: trace } = buildPolicyOneTrace(input);

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
    const input = makeObservation();
    mutate(input);
    assert.throws(() => buildPolicyOneTrace(input), message);
  });
}

test('the adapter rejects response requestId mismatches even when captureIds match', () => {
  const input = makeObservation();
  input.responses.all.requestId = 'not-the-request-header';
  assert.throws(() => buildPolicyOneTrace(input), /response requestId must match its request header/);
});

test('the adapter rejects an Apply command that is not linked to the ALL proposal choice', () => {
  const input = makeObservation();
  input.requests.apply.body.commands[0].constructionChoice.choiceId = 'other-choice';
  assert.throws(() => buildPolicyOneTrace(input), /exact selected field choice/);
});
