import { buildPolicyOneTrace } from '../../policy-one-trace.mjs';

export const makePolicyOneTraceInput = () => {
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
  const group = { present: true, keys: [], aggregate: 'COUNT_ROWS', rowCount: 1, count: 2 };
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
      inputColumnBinding: { columnId: 'patient-id-values', field: 'Patient.id' },
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
        inputColumnBinding: { columnId: 'patient-id-values', field: 'Patient.id' },
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


const makeWorkspace = ({ group, additionalBindings = [], renderedRows }) => ({
  documentId: 'output-1',
  rootResourceType: 'Patient',
  sourceColumnIds: ['patient-id', 'patient-family', 'patient-gender'],
  sourceBindings: [
    'patient-id|Patient.id',
    'patient-family|Patient.name[].family',
    'patient-gender|Patient.gender',
  ],
  sourceRows: { patientIds: ['dev-patient-001', 'dev-patient-002'], count: 2 },
  additionalBindings,
  group,
  renderedRows,
});

const noGroup = {
  present: false, keys: [], aggregate: 'NO_AGGREGATE', count: 0,
  rowValuePolicy: 'NONE', inputColumnId: 'NoColumn',
};
const countGroup = {
  present: true, keys: [], aggregate: 'COUNT_ROWS', count: 2,
  rowValuePolicy: 'NONE', inputColumnId: 'NoColumn',
};
const allGroup = {
  present: true, keys: [], aggregate: 'COUNT_ROWS', count: 2,
  rowValuePolicy: 'ALL', inputColumnId: 'patient-id-values',
};
const sourceWorkspace = makeWorkspace({
  group: noGroup,
  renderedRows: { patientIds: ['dev-patient-001', 'dev-patient-002'], count: 2 },
});
const groupWorkspace = makeWorkspace({ group: countGroup, renderedRows: [{ count: 2 }] });
const allWorkspace = makeWorkspace({
  group: allGroup,
  additionalBindings: ['patient-id-values|Patient.id'],
  renderedRows: [{ patientIds: ['dev-patient-001', 'dev-patient-002'], count: 2 }],
});
const restoredWorkspace = makeWorkspace({
  group: noGroup,
  additionalBindings: ['patient-id-values|Patient.id'],
  renderedRows: { patientIds: ['dev-patient-001', 'dev-patient-002'], count: 2 },
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
  input.saved.before.projection = groupWorkspace;
  input.saved.afterOne.projection = groupWorkspace;
  input.saved.afterApply.projection = allWorkspace;
  input.saved.before.group = { ...countGroup, rowCount: 1, inputColumnBinding: null };
  input.saved.afterOne.group = { ...countGroup, rowCount: 1, inputColumnBinding: null };
  input.saved.afterApply.group = {
    ...allGroup,
    rowCount: 1,
    inputColumnBinding: { columnId: 'patient-id-values', field: 'Patient.id' },
  };
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
      outputId: 'output-1',
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
      outputId: 'output-1',
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
    outputId: 'output-1', rowCount: 2, patientIds: ['dev-patient-001', 'dev-patient-002'],
  };
  input.context.actions = [
    {
      label: 'GroupPreviewSubmitted', event: 'preview', request: groupPreviewRequest,
      observation: { workspace: sourceWorkspace, draftVersion: rootDraft.version, draftDigest: rootDraft.digest },
    },
    {
      label: 'GroupPreviewReady', event: 'preview-response', response: groupPreviewResponse,
      settlement: { kind: 'http-response', captureId: groupPreviewRequest.captureId },
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
      observation: { workspace: groupWorkspace, draftVersion: groupDraft.version, draftDigest: groupDraft.digest },
    },
    {
      label: 'ReloadedAll', event: 'reload',
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
      observation: { workspace: allWorkspace, candidateId: removalCandidateID,
        previewRows: [{ patientId: 'dev-patient-001' }, { patientId: 'dev-patient-002' }] },
    },
    {
      label: 'RemovalCancelled', event: 'cancel',
      observation: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest,
        savedBefore: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest },
        savedAfter: { workspace: allWorkspace, draftVersion: allDraft.version, draftDigest: allDraft.digest } },
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
      observation: { workspace: restoredWorkspace, draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest },
    },
    {
      label: 'ReloadedSource', event: 'reload',
      observation: { workspace: restoredWorkspace, draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest },
    },
  ];
  input.context.outcome = {
    workspace: restoredWorkspace, patientIds: ['dev-patient-001', 'dev-patient-002'],
    draftVersion: restoredDraft.version, draftDigest: restoredDraft.digest,
  };
  return buildPolicyOneTrace(input).journeyTrace;
};
