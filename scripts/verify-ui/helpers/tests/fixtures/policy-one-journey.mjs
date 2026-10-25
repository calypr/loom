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

const capturedRequest = ({ requestId, captureId, commandId, body }) => ({
  method: 'POST',
  path: body?.commands ? 'commands' : body?.constructionChoices ? 'construction-choice-proposals' : 'construction-proposals',
  requestId,
  captureId,
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

export const makeSyntheticPolicyOneJourneyTrace = () => {
  const input = makePolicyOneTraceInput();
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
  const removalRequest = (requestId, captureId) => capturedRequest({
    requestId, captureId,
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
  const removalApplyCommandId = 'command-removal-apply';
  const removalApplyRequest = capturedRequest({
    requestId: 'header-removal-apply', captureId: 'capture-removal-apply', commandId: removalApplyCommandId,
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
      request: removalRequest('header-removal-one', 'capture-removal-one'),
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
    },
    {
      label: 'RemovalPreviewReady', event: 'preview-response',
      response: removalResponse('header-removal-one', 'capture-removal-one'),
      settlement: { kind: 'http-response', captureId: 'capture-removal-one' },
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
      request: removalRequest('header-removal-two', 'capture-removal-two'),
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
    },
    {
      label: 'RemovalPreviewReady', event: 'preview-response',
      response: removalResponse('header-removal-two', 'capture-removal-two'),
      settlement: { kind: 'http-response', captureId: 'capture-removal-two' },
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
  return buildPolicyOneTrace(input).journeyTrace;
};
