import assert from 'node:assert/strict';
import { buildPolicyOneTrace } from '../../policy-one-trace.mjs';
import {
  patientOneSavedState,
  policySnapshot,
  policyWorkspaceProjection,
} from '../../../workflows/builder-patient-one-disagreement.mjs';

const addedInputColumnId = 'patient-id-values';
const addedOutputColumnId = 'row_value_d953ae59403369dab2d0011c';

const groupStep = (rowValues = []) => ({
  id: 'group-step',
  inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: {
    kind: 'GROUP',
    group: {
      constructionId: 'group-step',
      keys: [],
      aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'row-count' }],
    },
  },
  outputs: [
    { id: 'row-count', name: 'count', label: 'Row count', type: 'integer' },
    ...rowValues.map(value => ({
      id: value.outputColumnId,
      name: 'patient_id_values',
      label: 'Patient ID values',
      type: value.policy === 'ALL' ? 'array' : 'string',
    })),
  ],
  ...(rowValues.length ? { rowValues: structuredClone(rowValues) } : {}),
});

const testTransportCapture = (transportIndex, request, response, eventOrder = {}) => {
  const requestPath = new URL(request.path, 'https://fixture.invalid');
  if (!requestPath.pathname.includes('/authoring/v2/')) {
    requestPath.pathname = `/api/explorers/policy-one/authoring/v2/${requestPath.pathname.replace(/^\/+/, '')}`;
  }
  const prefixIndex = requestPath.pathname.indexOf('/authoring/v2/');
  const resourcePath = `${requestPath.origin}${requestPath.pathname}`;
  const defaultRequestEvent = transportIndex * 2 - 1;
  const defaultSettlementEvent = transportIndex * 2;
  return {
    transportIndex,
    requestEventIndex: eventOrder.requestEventIndex ?? defaultRequestEvent,
    settlementEventIndex: eventOrder.settlementEventIndex ?? defaultSettlementEvent,
    capturePhase: 'FORMAL',
    method: request.method,
    path: request.path,
    resourcePath,
    ownerScope: `${requestPath.origin}${requestPath.pathname.slice(0, prefixIndex)}`,
    requestId: request.requestId,
    requestCaptureId: request.captureId,
    requestBody: request.body,
    settlement: 'response',
    responseOwnerIndex: transportIndex,
    responseHeaders: { status: response.status, requestId: response.requestId ?? null },
    responseReadError: null,
    responseObservedBeforeFailure: false,
    status: response.status,
    response: {
      status: response.status,
      requestId: response.requestId ?? null,
      commandId: response.commandId ?? response.body?.commandId ?? null,
      receiptId: response.proposalId ?? response.preview?.receiptId ?? response.body?.receiptId ?? response.body?.preview?.receiptId ?? null,
      receiptIds: [...new Set([
        response.proposalId,
        response.preview?.receiptId,
        response.body?.receiptId,
        response.body?.preview?.receiptId,
      ].filter(value => typeof value === 'string'))],
      outputIds: [...new Set([
        response.proposalOutputId,
        response.preview?.outputId,
        response.body?.outputId,
        response.body?.preview?.outputId,
        ...(Array.isArray(response.body?.outputs)
          ? response.body.outputs.map(output => output?.outputId)
          : []),
      ].filter(value => typeof value === 'string'))],
      snapshotToken: response.proposalSnapshotToken ?? response.body?.snapshotToken ?? null,
      draftVersion: response.body?.draftVersion ?? null,
      draftDigest: response.body?.draftDigest ?? null,
      candidateWorkspaceDigest: response.body?.candidateWorkspaceDigest ?? null,
      intentDigest: response.body?.intentDigest ?? null,
    },
  };
};

export const makePolicyOneTraceInput = () => {
  const oneRequestObject = {};
  const allRequestObject = {};
  const applyRequestObject = {};
  const oneResponseObject = {};
  const allResponseObject = {};
  const applyResponseObject = {};
  const beforeWorkspace = {
    documents: [{
      output: { id: 'output-1', title: 'Patient rows' },
      rootResourceType: 'Patient',
      columns: [{ columnId: 'patient-id', column: 'patient_id', label: 'Patient ID',
        source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } }],
      construction: { version: 1, steps: [groupStep()] },
    }],
  };
  const beforeProjection = {
    documents: [{ id: 'document-1', sourceColumnIds: ['patient-id'], group: { keys: [], aggregate: 'COUNT_ROWS', count: 2 } }],
  };
  const afterApplyWorkspace = {
    documents: [{
      output: { id: 'output-1', title: 'Patient rows' },
      rootResourceType: 'Patient',
      columns: [
        { columnId: 'patient-id', column: 'patient_id', label: 'Patient ID',
          source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
        { columnId: addedInputColumnId, column: 'patient_id_values', label: 'Patient ID',
          source: { kind: 'field', field: { path: 'id', projectionMode: 'ALL' } } },
      ],
      construction: {
        version: 1,
        steps: [groupStep([{
          inputColumnId: addedInputColumnId,
          outputColumnId: addedOutputColumnId,
          policy: 'ALL',
        }])],
      },
    }],
  };
  const afterApplyProjection = {
    documents: [{ id: 'document-1', sourceColumnIds: ['patient-id'], allColumnId: addedInputColumnId, group: { keys: [], aggregate: 'COUNT_ROWS', count: 2 } }],
  };
  const group = {
    present: true,
    keys: [],
    aggregate: 'COUNT_ROWS',
    rowCount: 1,
    count: 2,
    rowValuePolicy: 'NONE',
    inputColumnId: 'NoColumn',
    inputColumnBinding: null,
    outputColumnBinding: { columnId: 'NoColumn', type: 'NoType' },
  };
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
    group: {
      ...structuredClone(group),
      rowValuePolicy: 'ALL',
      inputColumnId: addedInputColumnId,
      inputColumnBinding: { columnId: addedInputColumnId, field: 'Patient.id' },
      outputColumnBinding: { columnId: addedOutputColumnId, type: 'array' },
    },
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

  const input = {
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
      apply: {
        policy: 'ALL', status: 200,
        inputColumnBinding: { columnId: addedInputColumnId, field: 'Patient.id' },
        outputColumnBinding: { columnId: addedOutputColumnId, type: 'array' },
      },
      editorIdentity: { editorId: 'editor-mount-1', documentTimeOrigin: 1730137200000 },
    },
    requests: {
      one: {
        requestObject: oneRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/construction-choice-proposals',
        requestId: 'header-one',
        captureId: 'capture-one-random',
        transportIndex: 1,
        commandId: 'command-one',
        body: proposalBody('ONE', 'command-one'),
      },
      all: {
        requestObject: allRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/construction-choice-proposals',
        requestId: 'header-all',
        captureId: 'capture-all-random',
        transportIndex: 2,
        commandId: 'command-all',
        body: proposalBody('ALL', 'command-all'),
      },
      apply: {
        requestObject: applyRequestObject,
        method: 'POST',
        path: '/api/explorers/e/authoring/v2/commands',
        requestId: 'header-apply',
        captureId: 'capture-apply-random',
        transportIndex: 3,
        commandId: 'command-all',
        body: {
          commandId: 'command-all',
          semanticsVersion: 10,
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
        fieldSelected: true,
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
        fieldSelected: true,
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
        fieldSelected: true,
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
        fieldSelected: true,
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
        fieldSelected: true,
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
        fieldSelected: true,
        form: 'RELATED_SOURCE',
        policy: 'ALL',
        availablePolicies: ['ONE', 'ALL'],
      },
      afterApply: {
        mounted: false,
        documentId: null,
        editorId: null,
        documentTimeOrigin: 1730137200000,
        selectedField: null,
        fieldSelected: false,
      },
    },
    fixtureIDs: ['dev-patient-001', 'dev-patient-002'],
    context: {
      name: 'builder-authoring:patient-one-disagreement',
      model: 'PolicyOneDisagreement',
      initialObservation: {
        workspace: sourceWorkspace,
        outputId: 'output-1',
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'draft-root',
        savedState: { groupKeys: [], aggregate: 'COUNT_ROWS', count: 2 },
        rowIds: ['dev-patient-001', 'dev-patient-002'],
      },
      formalEntry: {
        afterTransportIndex: 0,
        afterEventIndex: 0,
        initialSavedState: {
          ...sourceState,
          snapshotToken: 'snapshot-1',
          workspaceProjection: sourceWorkspace,
        },
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
      transportCaptures: [],
      outcome: {
        savedState: { sourceColumns: ['patient-id'], group: null },
        rowIds: ['dev-patient-001', 'dev-patient-002'],
      },
    },
  };
  input.context.transportCaptures = [
    testTransportCapture(1, input.requests.one, input.responses.one),
    testTransportCapture(2, input.requests.all, input.responses.all),
    testTransportCapture(3, input.requests.apply, input.responses.apply),
  ];
  return input;
}


const fixtureIDs = ['dev-patient-001', 'dev-patient-002'];
const sourceColumns = [
  { columnId: 'patient-id', column: 'patient-id', label: 'Patient ID', occurrenceId: 'base',
    logicalType: 'string', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
  { columnId: 'patient-family', column: 'patient-family', label: 'Family name', occurrenceId: 'base',
    logicalType: 'string', source: { kind: 'field', field: { path: 'name[].family', projectionMode: 'ALL' } } },
  { columnId: 'patient-gender', column: 'patient-gender', label: 'Gender', occurrenceId: 'base',
    logicalType: 'string', source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } } },
];

const makeBuilderState = ({ draftVersion, draftDigest, hasGroup = false, rowValues = [], hasAddedPatientID = false }) => {
  const columns = [
    ...structuredClone(sourceColumns),
    ...(hasAddedPatientID ? [{
      columnId: addedInputColumnId,
      column: 'patient_id_values',
      label: 'Patient ID',
      occurrenceId: 'base',
      logicalType: 'string',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'ALL' } },
    }] : []),
  ];
  const steps = hasGroup ? [{
    id: 'group-step',
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: 'group-step',
        keys: [],
        aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'row-count' }],
      },
    },
    outputs: [
      { id: 'row-count', name: 'count', label: 'Row count', type: 'integer' },
      ...rowValues.map(value => ({
        id: value.outputColumnId,
        name: 'patient_id_values',
        label: 'Patient ID values',
        type: value.policy === 'ALL' ? 'array' : 'string',
      })),
    ],
    ...(rowValues.length ? { rowValues: structuredClone(rowValues) } : {}),
  }] : [];
  return {
    workspace: {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderWorkspace',
      semanticsVersion: 10,
      explorer: { title: 'Patient rows' },
      documents: [{
        kind: 'ExplorerBuilderDocument',
        output: { id: 'output-1', title: 'Patient rows' },
        rootResourceType: 'Patient',
        route: { occurrenceId: 'base', resourceType: 'Patient' },
        rows: { kind: 'RECORDS', records: {} },
        columns,
        construction: { version: 1, steps },
      }],
      tabs: [{ id: 'patients', title: 'Patient rows', outputId: 'output-1', order: 0, visible: true }],
    },
    draftVersion,
    draftDigest,
    catalog: { snapshotToken: 'snapshot-1', generation: 'patient-one-fixture' },
  };
};

const sourceBuilder = makeBuilderState({ draftVersion: 1, draftDigest: 'draft-root' });
const sourceState = patientOneSavedState(sourceBuilder, 'output-1');
const groupBuilder = makeBuilderState({
  draftVersion: 3, draftDigest: 'draft-before', hasGroup: true,
});
const groupState = patientOneSavedState(groupBuilder, 'output-1');
const allBuilder = makeBuilderState({
  draftVersion: 4,
  draftDigest: 'draft-after',
  hasGroup: true,
  hasAddedPatientID: true,
  rowValues: [{ inputColumnId: addedInputColumnId, outputColumnId: addedOutputColumnId, policy: 'ALL' }],
});
const allState = patientOneSavedState(allBuilder, 'output-1');
const restoredBuilder = makeBuilderState({
  draftVersion: 5, draftDigest: 'draft-restored', hasAddedPatientID: true,
});
const restoredState = patientOneSavedState(restoredBuilder, 'output-1');

const sourceGrid = {
  headers: sourceColumns.map(column => column.label),
  cells: fixtureIDs.map((patientID, index) => [
    { text: patientID },
    { text: index === 0 ? 'Able' : 'Baker' },
    { text: index === 0 ? 'female' : 'male' },
  ]),
};
const groupedGrid = { headers: ['Row count'], cells: [[{ text: '2' }]] };
const allGrid = {
  headers: ['Patient ID', 'Row count'],
  cells: [[{ text: '', raw: JSON.stringify(fixtureIDs) }, { text: '2' }]],
};
const sourceWorkspace = policyWorkspaceProjection({
  savedState: sourceState, renderedRows: sourceGrid, sourceState, fixtureIDs,
});
const groupWorkspace = policyWorkspaceProjection({
  savedState: groupState, renderedRows: groupedGrid, sourceState, fixtureIDs,
});
const allWorkspace = policyWorkspaceProjection({
  savedState: allState, renderedRows: allGrid, sourceState, fixtureIDs, addedColumnId: addedInputColumnId,
});
const restoredWorkspace = policyWorkspaceProjection({
  savedState: restoredState, renderedRows: sourceGrid, sourceState, fixtureIDs, addedColumnId: addedInputColumnId,
});

const capturedRequest = ({ requestId, captureId, commandId, transportIndex, body }) => ({
  method: 'POST',
  path: body?.commands ? 'commands' : body?.constructionChoices ? 'construction-choice-proposals' : 'construction-proposals',
  requestId,
  captureId,
  transportIndex,
  requestObjectId: captureId,
  ...(commandId ? { commandId } : {}),
  body,
});

const capturedResponse = ({ requestId, captureId, commandId, status, body }) => ({
  requestId,
  captureId,
  requestObjectId: captureId,
  commandId: commandId ?? null,
  status,
  proposalId: body?.proposalId ?? null,
  proposalOutputId: body?.outputId ?? null,
  proposalSnapshotToken: body?.snapshotToken ?? null,
  proposalDraftVersion: body?.draftVersion ?? null,
  proposalDraftDigest: body?.draftDigest ?? null,
  proposalPreviewStatus: body?.previewStatus ?? null,
  preview: body?.preview ? {
    receiptId: body.preview.receiptId,
    outputId: body.preview.outputId,
    rowCount: body.preview.rowCount,
    rowsPresent: Array.isArray(body.preview.rows),
  } : null,
  errorCode: body?.error?.code ?? null,
  body,
});

const proposalBody = (draftVersion, draftDigest, candidateConstruction, extra = {}) => ({
  outputId: 'output-1',
  snapshotToken: 'snapshot-1',
  expectedDraftVersion: draftVersion,
  expectedDraftDigest: draftDigest,
  candidateConstruction,
  ...extra,
});

const twoSourceRowsPreview = ({ outputId = 'output-1', receiptId, rowCount = 2 }) => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  receiptId,
  outputId,
  columns: [{ column: 'patient_id', label: 'Patient ID', logicalType: 'string' }],
  rows: [{ patient_id: 'dev-patient-001' }, { patient_id: 'dev-patient-002' }],
  rowCount,
  diagnostics: [],
});

export const makeSyntheticPolicyOneJourneyTrace = ({
  sourceReceiptCase = 'none',
  previewStatus = 200,
  previewResponseReceiptId,
  setupReceiptCase = 'none',
  pendingSupportReads = false,
  reversedSupportCompletionOrder = false,
} = {}) => {
  assert(['none', 'initial-only', 'initial-and-captured', 'owner-change-refresh',
    'owner-change-before-response'].includes(sourceReceiptCase),
    `unsupported synthetic source receipt case ${sourceReceiptCase}`);
  assert(['none', 'reconcile-before-command', 'command-before-reconcile-response', 'command-starts-first'].includes(setupReceiptCase),
    `unsupported synthetic setup receipt case ${setupReceiptCase}`);
  const input = makePolicyOneTraceInput();
  input.requests.one.transportIndex = 3;
  input.requests.all.transportIndex = 4;
  input.requests.apply.transportIndex = 5;
  assert.deepEqual(allState.group.rowValues, [
    { inputColumnId: addedInputColumnId, outputColumnId: addedOutputColumnId, policy: 'ALL' },
  ], 'the saved Builder DTO exposes grouped-row values at ConstructionStep.rowValues');
  assert.equal(allWorkspace.group.rowValuePolicy, 'ALL',
    'the production workspace projection must read the saved top-level rowValues');
  input.saved = {
    before: policySnapshot({
      builder: groupBuilder, outputId: 'output-1', sourceState, fixtureIDs, renderedRows: groupedGrid,
    }),
    afterApply: policySnapshot({
      builder: allBuilder,
      outputId: 'output-1',
      sourceState,
      fixtureIDs,
      renderedRows: allGrid,
      addedColumnId: addedInputColumnId,
      choiceId: input.expected.choiceId,
    }),
  };
  input.saved.afterOne = structuredClone(input.saved.before);
  input.responses.apply.body.workspace = allBuilder.workspace;
  const rootDraft = { version: 1, digest: 'draft-root' };
  const groupDraft = { version: 3, digest: 'draft-before' };
  const allDraft = { version: 4, digest: 'draft-after' };
  const restoredDraft = { version: 5, digest: 'draft-restored' };
  const groupPreviewID = 'proposal-group-empty-key';
  const removalCandidateID = 'proposal-removal-same-content';
  const groupConstruction = { version: 1, steps: [{ id: 'group-step' }] };
  const groupPreviewRequest = capturedRequest({
    requestId: 'header-group-preview', captureId: 'capture-group-preview',
    transportIndex: 1,
    body: proposalBody(rootDraft.version, rootDraft.digest, groupConstruction),
  });
  const groupPreviewResponse = capturedResponse({
    requestId: 'header-group-preview', captureId: 'capture-group-preview', status: 200,
    body: {
      proposalId: groupPreviewID, outputId: 'output-1', snapshotToken: 'snapshot-1',
      draftVersion: rootDraft.version, draftDigest: rootDraft.digest, previewStatus: 'READY',
      preview: {
        ...twoSourceRowsPreview({ receiptId: groupPreviewID, rowCount: 1 }),
        columns: [{ column: 'row_count', label: 'Row count', logicalType: 'integer' }],
        rows: [{ row_count: 2 }],
      },
    },
  });
  const groupApplyCommandId = 'command-group-apply';
  const groupApplyRequest = capturedRequest({
    requestId: 'header-group-apply', captureId: 'capture-group-apply', commandId: groupApplyCommandId,
    transportIndex: 2,
    body: {
      commandId: groupApplyCommandId,
      semanticsVersion: 10,
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: rootDraft.version,
      expectedDraftDigest: rootDraft.digest,
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: 'output-1', proposalId: groupPreviewID }],
    },
  });
  const groupApplyResponse = capturedResponse({
    requestId: 'header-group-apply', captureId: 'capture-group-apply', commandId: groupApplyCommandId,
    status: 200,
    body: { commandId: groupApplyCommandId, draftVersion: groupDraft.version, draftDigest: groupDraft.digest },
  });
  const removalRequest = (requestId, captureId, transportIndex) => capturedRequest({
    requestId, captureId,
    transportIndex,
    body: proposalBody(allDraft.version, allDraft.digest, { version: 1, steps: [] }, { removeStepIds: ['group-step'] }),
  });
  const removalResponse = (requestId, captureId) => capturedResponse({
    requestId, captureId, status: 200,
    body: {
      proposalId: removalCandidateID, outputId: 'output-1', snapshotToken: 'snapshot-1',
      draftVersion: allDraft.version, draftDigest: allDraft.digest, previewStatus: 'READY',
      preview: twoSourceRowsPreview({ receiptId: removalCandidateID }),
    },
  });
  const removalOneRequest = removalRequest('header-removal-one', 'capture-removal-one', 6);
  const removalOneResponse = removalResponse('header-removal-one', 'capture-removal-one');
  const removalTwoRequest = removalRequest('header-removal-two', 'capture-removal-two', 7);
  const removalTwoResponse = removalResponse('header-removal-two', 'capture-removal-two');
  const removalApplyCommandId = 'command-removal-apply';
  const removalApplyRequest = capturedRequest({
    requestId: 'header-removal-apply', captureId: 'capture-removal-apply', commandId: removalApplyCommandId,
    transportIndex: 8,
    body: {
      commandId: removalApplyCommandId,
      semanticsVersion: 10,
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: allDraft.version,
      expectedDraftDigest: allDraft.digest,
      commands: [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId: 'output-1', proposalId: removalCandidateID }],
    },
  });
  const removalApplyResponse = capturedResponse({
    requestId: 'header-removal-apply', captureId: 'capture-removal-apply', commandId: removalApplyCommandId,
    status: 200,
    body: { commandId: removalApplyCommandId, draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest },
  });

  input.context.initialObservation = {
    workspace: sourceWorkspace, draftVersion: rootDraft.version, draftDigest: rootDraft.digest,
    outputId: 'output-1', snapshotToken: 'snapshot-1',
  };
  input.context.initialPreview = {
    outputId: 'output-1',
    binding: {
      status: 'ready', receiptId: 'receipt-source', outputId: 'output-1',
      draftVersion: rootDraft.version, draftDigest: rootDraft.digest,
    },
    headers: sourceGrid.headers,
    cells: sourceGrid.cells,
    rowCount: 2,
    patientIds: fixtureIDs,
  };
  input.context.actions = [
    {
      label: 'GroupPreviewSubmitted', event: 'preview', request: groupPreviewRequest,
      observation: { workspace: sourceWorkspace, draftVersion: rootDraft.version, draftDigest: rootDraft.digest },
    },
    {
      label: 'GroupPreviewReady', event: 'preview-response', response: groupPreviewResponse,
      settlement: { kind: 'http-response', captureId: groupPreviewRequest.captureId },
      savedBefore: sourceState,
      savedAfter: sourceState,
      observation: { workspace: sourceWorkspace, preview: { rowCount: 1, keys: [], aggregate: 'COUNT_ROWS', count: 2 } },
    },
    {
      label: 'GroupApplySubmitted', event: 'begin-apply', request: groupApplyRequest,
      observation: { workspace: sourceWorkspace, draftVersion: rootDraft.version, draftDigest: rootDraft.digest,
        candidateId: groupPreviewID },
    },
    {
      label: 'GroupApplied', event: 'finish-apply', response: groupApplyResponse,
      settlement: { kind: 'http-response', captureId: groupApplyRequest.captureId },
      savedBefore: sourceState,
      savedAfter: groupState,
      observation: { workspace: groupWorkspace, draftVersion: groupDraft.version, draftDigest: groupDraft.digest },
    },
    {
      label: 'ReloadedAll', event: 'reload',
      savedBefore: allState,
      savedAfter: allState,
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
    },
    {
      label: 'RemovalPreviewSubmitted', event: 'preview',
      request: removalOneRequest,
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
    },
    {
      label: 'RemovalPreviewReady', event: 'preview-response',
      response: removalOneResponse,
      settlement: { kind: 'http-response', captureId: removalOneRequest.captureId },
      savedBefore: allState,
      observation: { workspace: allWorkspace, candidateId: removalCandidateID,
        previewRows: [{ patientId: 'dev-patient-001' }, { patientId: 'dev-patient-002' }] },
    },
    {
      label: 'RemovalCancelled', event: 'cancel',
      savedBefore: allState,
      savedAfter: allState,
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest,
        savedBefore: allState,
        savedAfter: allState },
    },
    {
      label: 'RemovalPreviewSubmitted', event: 'preview',
      request: removalTwoRequest,
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
    },
    {
      label: 'RemovalPreviewReady', event: 'preview-response',
      response: removalTwoResponse,
      settlement: { kind: 'http-response', captureId: removalTwoRequest.captureId },
      savedBefore: allState,
      observation: { workspace: allWorkspace, candidateId: removalCandidateID,
        previewRows: [{ patientId: 'dev-patient-001' }, { patientId: 'dev-patient-002' }] },
    },
    {
      label: 'RemovalApplySubmitted', event: 'begin-apply', request: removalApplyRequest,
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest,
        candidateId: removalCandidateID },
    },
    {
      label: 'RemovalApplied', event: 'finish-apply', response: removalApplyResponse,
      settlement: { kind: 'http-response', captureId: removalApplyRequest.captureId },
      savedBefore: allState,
      savedAfter: restoredState,
      observation: { workspace: restoredWorkspace, draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest },
    },
    {
      label: 'ReloadedSource', event: 'reload',
      savedBefore: restoredState,
      savedAfter: restoredState,
      observation: { workspace: restoredWorkspace, draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest },
    },
  ];
  input.context.outcome = {
    workspace: restoredWorkspace, patientIds: ['dev-patient-001', 'dev-patient-002'],
    draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest,
  };
  input.context.transportCaptures = [
    testTransportCapture(1, groupPreviewRequest, groupPreviewResponse),
    testTransportCapture(2, groupApplyRequest, groupApplyResponse),
    testTransportCapture(3, input.requests.one, input.responses.one),
    testTransportCapture(4, input.requests.all, input.responses.all),
    testTransportCapture(5, input.requests.apply, input.responses.apply),
    testTransportCapture(6, removalOneRequest, removalOneResponse),
    testTransportCapture(7, removalTwoRequest, removalTwoResponse),
    testTransportCapture(8, removalApplyRequest, removalApplyResponse),
  ];
  const receiptIdForPreview = previewResponseReceiptId ??
    (sourceReceiptCase.startsWith('owner-change-') ? 'receipt-removal-same-content' : 'receipt-source');
  const makeReconcileCapture = ({
    transportIndex, requestId, draftVersion, draftDigest, receiptId, intentDigest, eventOrder,
  }) => {
    const request = {
      method: 'POST',
      path: 'reconcile',
      requestId,
      captureId: `capture-${requestId}`,
      transportIndex,
      body: { snapshotToken: 'snapshot-1', draftVersion, draftDigest },
    };
    const response = {
      status: 200,
      requestId,
      body: {
        receiptId,
        snapshotToken: 'snapshot-1',
        intentDigest,
        outputs: [{ outputId: 'output-1' }],
      },
    };
    return testTransportCapture(transportIndex, request, response, eventOrder);
  };
  const makePreviewCapture = transportIndex => {
    const request = {
      method: 'POST',
      path: 'preview',
      requestId: 'header-receipt-preview',
      captureId: 'capture-receipt-preview',
      transportIndex,
      body: { receiptId: receiptIdForPreview, outputId: 'output-1' },
    };
    const response = {
      status: previewStatus,
      requestId: request.requestId,
      preview: {
        receiptId: previewResponseReceiptId ?? receiptIdForPreview,
        outputId: 'output-1',
      },
    };
    return testTransportCapture(transportIndex, request, response);
  };

  if (sourceReceiptCase === 'initial-only' || sourceReceiptCase === 'initial-and-captured') {
    const prefixCount = sourceReceiptCase === 'initial-only' ? 1 : 2;
    const semanticRequests = [
      groupPreviewRequest,
      groupApplyRequest,
      input.requests.one,
      input.requests.all,
      input.requests.apply,
      removalOneRequest,
      removalTwoRequest,
      removalApplyRequest,
    ];
    for (const request of semanticRequests) request.transportIndex += prefixCount;
    const shifted = input.context.transportCaptures.map(capture => ({
      ...capture,
      transportIndex: capture.transportIndex + prefixCount,
      responseOwnerIndex: capture.responseOwnerIndex + prefixCount,
      requestEventIndex: capture.requestEventIndex + (prefixCount * 2),
      settlementEventIndex: capture.settlementEventIndex + (prefixCount * 2),
    }));
    const prefix = [];
    let nextIndex = 1;
    let nextEvent = 1;
    if (sourceReceiptCase === 'initial-and-captured') {
      prefix.push(makeReconcileCapture({
        transportIndex: nextIndex,
        requestId: 'builder-50-1',
        draftVersion: rootDraft.version,
        draftDigest: rootDraft.digest,
        receiptId: 'receipt-source',
        intentDigest: 'intent-source-receipt',
      }));
      prefix.at(-1).requestEventIndex = nextEvent;
      prefix.at(-1).settlementEventIndex = nextEvent + 1;
      nextIndex += 1;
      nextEvent += 2;
    }
    const previewCapture = makePreviewCapture(nextIndex);
    previewCapture.requestEventIndex = nextEvent;
    previewCapture.settlementEventIndex = nextEvent + 1;
    prefix.push(previewCapture);
    input.context.transportCaptures = [...prefix, ...shifted];
  } else if (sourceReceiptCase === 'owner-change-refresh'
    || sourceReceiptCase === 'owner-change-before-response') {
    const ownerChangesBeforeResponse = sourceReceiptCase === 'owner-change-before-response';
    input.context.initialPreview.binding.receiptId = receiptIdForPreview;
    removalApplyRequest.transportIndex = 9;
    const baseBeforeRemovalApply = input.context.transportCaptures.slice(0, 7);
    const oldReceipt = makeReconcileCapture({
      transportIndex: 8,
      requestId: 'builder-60-1',
      draftVersion: allDraft.version,
      draftDigest: allDraft.digest,
      receiptId: receiptIdForPreview,
      intentDigest: 'intent-before-removal',
      ...(ownerChangesBeforeResponse ? {
        eventOrder: { requestEventIndex: 15, settlementEventIndex: 20 },
      } : {}),
    });
    const removalCommand = testTransportCapture(9, removalApplyRequest, removalApplyResponse,
      ownerChangesBeforeResponse ? { requestEventIndex: 16, settlementEventIndex: 17 } : {});
    const currentReceipt = makeReconcileCapture({
      transportIndex: 10,
      requestId: 'builder-61-1',
      draftVersion: restoredDraft.version,
      draftDigest: restoredDraft.digest,
      receiptId: receiptIdForPreview,
      intentDigest: 'intent-after-removal',
      ...(ownerChangesBeforeResponse ? {
        eventOrder: { requestEventIndex: 18, settlementEventIndex: 19 },
      } : {}),
    });
    const previewCapture = makePreviewCapture(11);
    input.context.transportCaptures = [
      ...baseBeforeRemovalApply,
      oldReceipt,
      removalCommand,
      currentReceipt,
      previewCapture,
    ];
  }
  const setupCommandRequest = {
    method: 'POST', path: '/api/explorers/policy-one/authoring/v2/commands',
    requestId: 'setup-command-physical-id', captureId: null, transportIndex: 1,
    body: { commandId: 'setup-command-physical-id', commands: [{ type: 'CREATE_SOURCE_TABLE' }] },
  };
  const setupCommandResponse = {
    status: 200, requestId: 'setup-command-physical-id',
    body: { commandId: 'setup-command-physical-id', draftVersion: rootDraft.version, draftDigest: rootDraft.digest },
  };
  const setupCommandCapture = testTransportCapture(1, setupCommandRequest, setupCommandResponse);
  setupCommandCapture.capturePhase = 'SETUP';
  setupCommandCapture.setupMutation = true;
  const setupReceipt = (transportIndex, { requestEventIndex, settlementEventIndex, generation, receiptId, draftDigest }) => {
    const requestId = `builder-${generation}-1`;
    const request = {
      method: 'POST', path: 'reconcile', requestId, captureId: null, transportIndex,
      body: { snapshotToken: 'snapshot-1', draftVersion: rootDraft.version, draftDigest, outputId: 'output-1' },
    };
    const response = {
      status: 200, requestId,
      body: { receiptId, snapshotToken: 'snapshot-1', intentDigest: `intent-${receiptId}`,
        outputs: [{ outputId: 'output-1' }] },
    };
    const capture = testTransportCapture(transportIndex, request, response, {
      requestEventIndex, settlementEventIndex,
    });
    capture.capturePhase = 'SETUP';
    capture.setupMutation = false;
    return capture;
  };
  let setupCaptures = [setupCommandCapture];
  if (setupReceiptCase !== 'none') {
    const commandStartsFirst = setupReceiptCase === 'command-starts-first';
    const commandBeforeReconcileResponse = setupReceiptCase !== 'reconcile-before-command';
    const oldReceipt = setupReceipt(commandStartsFirst ? 2 : 1, {
      requestEventIndex: commandStartsFirst ? 2 : 1,
      settlementEventIndex: commandBeforeReconcileResponse ? 4 : 2,
      generation: 70,
      receiptId: 'receipt-setup-A',
      draftDigest: 'draft-before-setup-command',
    });
    setupCommandCapture.transportIndex = commandStartsFirst ? 1 : 2;
    setupCommandCapture.responseOwnerIndex = setupCommandCapture.transportIndex;
    setupCommandCapture.requestEventIndex = commandStartsFirst ? 1
      : commandBeforeReconcileResponse ? 2 : 3;
    setupCommandCapture.settlementEventIndex = commandBeforeReconcileResponse ? 3 : 4;
    const currentReceipt = setupReceipt(3, {
      requestEventIndex: 5,
      settlementEventIndex: 6,
      generation: 71,
      receiptId: 'receipt-setup-B',
      draftDigest: rootDraft.digest,
    });
    setupCaptures = commandStartsFirst
      ? [setupCommandCapture, oldReceipt, currentReceipt]
      : [oldReceipt, setupCommandCapture, currentReceipt];
  }
  const setupTransportOffset = setupCaptures.length;
  const setupEventOffset = setupTransportOffset * 2;
  const setupReceiptPreviewOffset = setupReceiptCase === 'none' ? 0 : 1;
  const formalTransportOffset = setupTransportOffset + setupReceiptPreviewOffset;
  const formalEventOffset = setupEventOffset + (setupReceiptPreviewOffset * 2);
  let setupReceiptPreviewCapture;
  if (setupReceiptPreviewOffset) {
    const request = {
      method: 'POST', path: 'preview', requestId: 'header-setup-receipt-preview', captureId: null,
      transportIndex: setupTransportOffset + 1,
      body: { receiptId: 'receipt-setup-B', outputId: 'output-1' },
    };
    const response = {
      status: 200, requestId: request.requestId,
      body: { receiptId: 'receipt-setup-B', outputId: 'output-1', rowCount: 2,
        rows: [{ patientId: fixtureIDs[0] }, { patientId: fixtureIDs[1] }] },
    };
    setupReceiptPreviewCapture = testTransportCapture(request.transportIndex, request, response, {
      requestEventIndex: setupEventOffset + 1,
      settlementEventIndex: setupEventOffset + 2,
    });
  }
  for (const request of [groupPreviewRequest, groupApplyRequest, input.requests.one, input.requests.all,
    input.requests.apply, removalOneRequest, removalTwoRequest, removalApplyRequest]) {
    request.transportIndex += formalTransportOffset;
  }
  const shiftedCaptures = input.context.transportCaptures.map(capture => ({
    ...capture,
    transportIndex: capture.transportIndex + formalTransportOffset,
    responseOwnerIndex: capture.responseOwnerIndex + formalTransportOffset,
    requestEventIndex: capture.requestEventIndex + formalEventOffset,
    settlementEventIndex: capture.settlementEventIndex + formalEventOffset,
  }));
  input.context.transportCaptures = [
    ...setupCaptures,
    ...(setupReceiptPreviewCapture ? [setupReceiptPreviewCapture] : []),
    ...shiftedCaptures,
  ];
  input.context.formalEntry = {
    afterTransportIndex: setupTransportOffset,
    afterEventIndex: setupEventOffset,
    initialSavedState: { ...sourceState, snapshotToken: 'snapshot-1', workspaceProjection: sourceWorkspace },
  };
  if (setupReceiptCase !== 'none') {
    let nextTransportIndex = Math.max(...input.context.transportCaptures.map(capture => capture.transportIndex)) + 1;
    let nextEventIndex = Math.max(...input.context.transportCaptures.flatMap(capture =>
      [capture.requestEventIndex, capture.settlementEventIndex])) + 1;
    const freshReceiptId = 'receipt-formal-after-setup';
    const freshReconcileRequestId = 'builder-72-1';
    const freshReconcileRequest = {
      method: 'POST', path: 'reconcile', requestId: freshReconcileRequestId, captureId: null,
      transportIndex: nextTransportIndex,
      body: { snapshotToken: 'snapshot-1', draftVersion: rootDraft.version,
        draftDigest: rootDraft.digest, outputId: 'output-1' },
    };
    const freshReconcileResponse = {
      status: 200, requestId: freshReconcileRequestId,
      body: { receiptId: freshReceiptId, snapshotToken: 'snapshot-1',
        intentDigest: `intent-${freshReceiptId}`, outputs: [{ outputId: 'output-1' }] },
    };
    input.context.transportCaptures.push(testTransportCapture(nextTransportIndex,
      freshReconcileRequest, freshReconcileResponse, {
        requestEventIndex: nextEventIndex, settlementEventIndex: nextEventIndex + 1,
      }));
    nextTransportIndex += 1;
    nextEventIndex += 2;
    const formalPreviewRequest = {
      method: 'POST', path: 'preview', requestId: 'header-formal-setup-receipt-preview', captureId: null,
      transportIndex: nextTransportIndex,
      body: { receiptId: freshReceiptId, outputId: 'output-1' },
    };
    const formalPreviewResponse = {
      status: 200, requestId: 'header-formal-setup-receipt-preview',
      body: { receiptId: freshReceiptId, outputId: 'output-1', rowCount: 1,
        rows: [{ patientIds: fixtureIDs }] },
    };
    input.context.transportCaptures.push(testTransportCapture(nextTransportIndex,
      formalPreviewRequest, formalPreviewResponse, {
        requestEventIndex: nextEventIndex, settlementEventIndex: nextEventIndex + 1,
      }));
  }
  if (reversedSupportCompletionOrder) {
    const ownerScope = input.context.transportCaptures.find(capture =>
      capture.requestCaptureId === 'capture-group-preview')?.ownerScope;
    assert(ownerScope, 'synthetic read responses require the observed formal actor scope');
    const transportOffset = Math.max(...input.context.transportCaptures.map(capture => capture.transportIndex));
    const eventOffset = Math.max(...input.context.transportCaptures.flatMap(capture => [
      capture.requestEventIndex, capture.settlementEventIndex,
    ]));
    const supportRequest = (transportIndex, path, requestId, body) => ({
      method: 'POST', path, requestId, captureId: null, transportIndex, body,
    });
    const inventoryRequest = supportRequest(transportOffset + 1, 'semantic-inventory',
      'feature-catalog-reverse-completion', {
        outputId: input.expected.outputId,
        snapshotToken: input.context.initialObservation.snapshotToken,
        rowRoot: 'Patient',
        limit: 50,
        selectionMode: 'SYSTEM_VERSION_CATEGORY',
      });
    const capabilitiesRequest = supportRequest(transportOffset + 2, 'construction-capabilities',
      'construction-capabilities-reverse-completion', {
        outputId: input.expected.outputId,
        snapshotToken: input.context.initialObservation.snapshotToken,
        expectedDraftVersion: input.context.initialObservation.draftVersion,
        expectedDraftDigest: input.context.initialObservation.draftDigest,
        stageId: 'source_projection',
      });
    input.context.transportCaptures.push(
      testTransportCapture(transportOffset + 1, inventoryRequest, {
        status: 200,
        requestId: inventoryRequest.requestId,
        body: { outputId: input.expected.outputId, fields: [] },
      }, {
        requestEventIndex: eventOffset + 1,
        settlementEventIndex: eventOffset + 4,
      }),
      testTransportCapture(transportOffset + 2, capabilitiesRequest, {
        status: 200,
        requestId: capabilitiesRequest.requestId,
        body: { outputId: input.expected.outputId, capabilities: [] },
      }, {
        requestEventIndex: eventOffset + 2,
        settlementEventIndex: eventOffset + 3,
      }),
    );
  }
  if (pendingSupportReads) {
    const ownerScope = input.context.transportCaptures.find(capture =>
      capture.requestCaptureId === 'capture-group-preview')?.ownerScope;
    assert(ownerScope, 'synthetic pending support reads require the observed formal actor scope');
    const removalApply = input.context.transportCaptures.find(capture =>
      capture.requestCaptureId === 'capture-removal-apply');
    assert(removalApply, 'synthetic pending support reads require the captured removal command CAS');
    const transportOffset = Math.max(...input.context.transportCaptures.map(capture => capture.transportIndex));
    const eventOffset = Math.max(...input.context.transportCaptures.flatMap(capture => [
      capture.requestEventIndex, capture.settlementEventIndex,
    ]));
    const pendingRequest = (transportIndex, requestEventIndex, path, requestId, requestBody) => ({
      transportIndex,
      requestEventIndex,
      capturePhase: 'FORMAL',
      origin: 'https://fixture.invalid',
      resourcePath: `${ownerScope}/authoring/v2/${path}`,
      ownerScope,
      method: 'POST',
      path,
      requestId,
      requestBody,
      settlement: 'pending',
      status: null,
      responseOwnerIndex: null,
      responseHeaders: null,
      responseReadError: null,
      responseObservedBeforeFailure: false,
      response: null,
    });
    input.context.transportCaptures.push(
      pendingRequest(transportOffset + 1, eventOffset + 1, 'semantic-inventory',
        'feature-catalog-unit-physical-1', {
          outputId: input.expected.outputId,
          snapshotToken: input.context.initialObservation.snapshotToken,
          rowRoot: 'Patient',
          limit: 50,
          selectionMode: 'SYSTEM_VERSION_CATEGORY',
        }),
      pendingRequest(transportOffset + 2, eventOffset + 2, 'construction-capabilities',
        'construction-capabilities-unit-physical-2', {
          outputId: input.expected.outputId,
          snapshotToken: removalApply.requestBody.snapshotToken,
          expectedDraftVersion: removalApply.requestBody.expectedDraftVersion,
          expectedDraftDigest: removalApply.requestBody.expectedDraftDigest,
          stageId: 'source_projection',
        }),
    );
  }
  const pendingTransportIndices = input.context.transportCaptures
    .filter(capture => capture.settlement === 'pending')
    .map(capture => capture.transportIndex);
  const eventIndices = input.context.transportCaptures.flatMap(capture => [
    capture.requestEventIndex,
    ...(capture.settlement === 'pending' ? [] : [capture.settlementEventIndex]),
  ]);
  input.context.formalExit = {
    afterTransportIndex: Math.max(0, ...input.context.transportCaptures.map(capture => capture.transportIndex)),
    afterEventIndex: Math.max(0, ...eventIndices),
    observedAt: Date.now(),
    drainTimeoutMs: 5000,
    deadlineReached: pendingTransportIndices.length > 0,
    pendingTransportIndices,
    pendingResponseReadTransportIndices: [],
  };
  return buildPolicyOneTrace(input).journeyTrace;
};
