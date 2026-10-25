const CASES = Object.freeze({
  date: Object.freeze({
    resourceType: 'Patient',
    fieldPath: 'birthDate',
    inputType: 'date',
    temporalColumnId: 'birth-date-column',
    rows: [
      { id: 'dev-patient-001', value: '1980-01-02' },
      { id: 'dev-patient-002', value: '1985-03-04' },
    ],
    initial: { operator: 'GT', inputValue: '1980-01-02', requestValue: { kind: 'DATE', date: '1980-01-02' },
      savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-002'] },
    edited: { operator: 'GTE', inputValue: '1980-01-02', requestValue: { kind: 'DATE', date: '1980-01-02' },
      savedValue: { kind: 'DATE', date: '1980-01-02' }, ids: ['dev-patient-001', 'dev-patient-002'] },
  }),
  'date-time': Object.freeze({
    resourceType: 'Observation',
    fieldPath: 'effectiveDateTime',
    inputType: 'datetime-local',
    temporalColumnId: 'effective-datetime-column',
    rows: [
      { id: 'dev-observation-001', value: '2026-01-02T00:00:00Z' },
      { id: 'dev-observation-002', value: '2026-01-02T00:00:00Z' },
      { id: 'dev-observation-003', value: '2026-01-02T00:00:00Z' },
    ],
    initial: { operator: 'GT', inputValue: '2026-01-02T00:00',
      requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' },
      savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' }, ids: [] },
    edited: { operator: 'GTE', inputValue: '2026-01-02T00:00',
      requestValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' },
      savedValue: { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' },
      ids: ['dev-observation-001', 'dev-observation-002', 'dev-observation-003'] },
  }),
});

const clone = value => structuredClone(value);
export const TEMPORAL_FILTER_FIXTURE_BASE_TIME = 1_000;
const baseTime = TEMPORAL_FILTER_FIXTURE_BASE_TIME;
const epochOffset = 1_700_000_000_000;

const makeFilterStep = (stepId, spec, predicate = spec.savedValue) => ({
  id: stepId,
  operation: {
    kind: 'FILTER',
    filter: { columnId: spec.temporalColumnId, operator: spec.operator, values: [clone(predicate)] },
  },
});

const makeDocument = (spec, withTemporalColumn, filterStep = null) => ({
  rootResourceType: spec.resourceType,
  output: { id: 'temporal-output' },
  columns: [
    { columnId: 'source-id-column', source: { kind: 'field', field: { path: 'id' } } },
    ...(withTemporalColumn ? [{
      columnId: spec.temporalColumnId,
      source: { kind: 'field', field: { path: spec.fieldPath } },
    }] : []),
  ],
  construction: { steps: [{ id: 'source-step', operation: { kind: 'SOURCE' } }, ...(filterStep ? [clone(filterStep)] : [])] },
});

const filterConstruction = (spec, intent, predicate = spec.initial.savedValue) => ({
  steps: [
    { id: 'source-step', operation: { kind: 'SOURCE' } },
    ...(intent === 'remove' ? [] : [makeFilterStep(intent === 'edit' ? 'saved-filter-step' : 'candidate-filter-step', {
      temporalColumnId: spec.temporalColumnId,
      operator: intent === 'edit' ? spec.edited.operator : spec.initial.operator,
      savedValue: predicate,
    }, predicate)]),
  ],
});

export const makeTemporalFilterJourneyCaptureFixture = (caseName = 'date', { pendingCapabilities = false } = {}) => {
  const spec = CASES[caseName];
  const outputId = 'temporal-output';
  const filterStepInitial = makeFilterStep('saved-filter-step', {
    temporalColumnId: spec.temporalColumnId, operator: spec.initial.operator, savedValue: spec.initial.savedValue,
  });
  const filterStepEdited = makeFilterStep('saved-filter-step', {
    temporalColumnId: spec.temporalColumnId, operator: spec.edited.operator, savedValue: spec.edited.savedValue,
  });
  const document = {
    blank: [],
    root: [makeDocument(spec, false)],
    temporal: [makeDocument(spec, true)],
    initial: [makeDocument(spec, true, filterStepInitial)],
    edited: [makeDocument(spec, true, filterStepEdited)],
    removed: [makeDocument(spec, true)],
  };
  const builderStates = [
    ['blank', 1, 'draft-1', 1, 1005, 1010],
    ['blank', 1, 'draft-1', 1, 1015, 1020],
    ['root', 2, 'draft-2', 1, 1035, 1040],
    ['root', 2, 'draft-2', 1, 1055, 1060],
    ['temporal', 3, 'draft-3', 1, 1085, 1090],
    ['temporal', 3, 'draft-3', 1, 1105, 1110],
    ['temporal', 3, 'draft-3', 1, 1135, 1140],
    ['temporal', 3, 'draft-3', 2, 1167, 1170],
    ['temporal', 3, 'draft-3', 2, 1195, 1200],
    ['initial', 4, 'draft-4', 2, 1225, 1230],
    ['initial', 4, 'draft-4', 3, 1257, 1260],
    ['initial', 4, 'draft-4', 3, 1265, 1270],
    ['initial', 4, 'draft-4', 3, 1315, 1320],
    ['edited', 5, 'draft-5', 3, 1345, 1350],
    ['edited', 5, 'draft-5', 4, 1377, 1380],
    ['edited', 5, 'draft-5', 4, 1405, 1410],
    ['removed', 6, 'draft-6', 4, 1425, 1430],
    ['removed', 6, 'draft-6', 5, 1457, 1460],
  ];
  const builderReads = builderStates.map(([state, draftVersion, draftDigest, epoch, requestAt, capturedAt], index) => ({
    readId: `schema-fixture-builder-read-${index}`,
    requestId: `schema-fixture-api-read-${index}`,
    status: 200,
    requestStartedAtMonotonicMs: requestAt,
    requestStartedAtEpochMs: epochOffset + requestAt,
    requestMountEpoch: epoch,
    capturedAtMonotonicMs: capturedAt,
    capturedAtEpochMs: epochOffset + capturedAt,
    mountEpoch: epoch,
    catalogGeneration: 'basic-temporal-generation',
    snapshotToken: `snapshot-${draftVersion}`,
    draftVersion,
    draftDigest,
    workspaceDocuments: clone(document[state]),
  }));

  const requestEvents = [];
  const addRequest = ({ name, path, body, response, isMutation, status = 200, mountEpoch = 1,
    atMonotonicMs = baseTime + requestEvents.length * 10,
    responseReceivedAt,
    failure = null, readCancellationDiagnostic = false, responseCapture = 'captured', responseCaptureComplete = true,
    responseBodyCaptured = true }) => {
    const captureId = `schema-fixture-browser-request-${name}`;
    const atEpochMs = epochOffset + atMonotonicMs;
    const observedResponseAt = responseReceivedAt === undefined
      ? status === null ? null : atEpochMs + 1
      : responseReceivedAt;
    requestEvents.push({
      index: requestEvents.length,
      browserRequestId: captureId,
      requestId: `schema-fixture-raw-request-${name}`,
      method: 'POST',
      origin: 'http://127.0.0.1:30000',
      path,
      status,
      request: clone(body),
      response: clone(response),
      startedAt: atEpochMs,
      responseReceivedAt: observedResponseAt,
      completedAt: atEpochMs + (status === null ? 1 : 3),
      responseMountEpoch: status === null ? null : mountEpoch,
      failureMountEpoch: status === null ? mountEpoch : null,
      isMutation,
      networkComplete: true,
      settlement: status === null ? 'request-failed' : 'response',
      failure: clone(failure),
      readCancellationDiagnostic,
      responseCapture,
      responseCaptureComplete,
      responseBodyCaptured,
      atMonotonicMs,
      atEpochMs,
      mountEpoch,
    });
    return captureId;
  };
  const commandPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/commands';
  const proposalPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/construction-proposals';
  const choiceProposalPath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/construction-choice-proposals';
  const reconcilePath = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/reconcile';
  const choiceCommandId = 'temporal-choice-command';
  const choiceSnapshotToken = 'temporal-choice-snapshot';
  const choiceSelection = { choiceId: 'temporal-field-choice', form: 'VALUE' };
  const explorerRequestId = addRequest({
    name: 'explorer-create', path: '/api/v1/projects/loom_dev_verify_temporal/explorers',
    body: { title: 'temporal fixture Explorer' }, response: { explorerId: 'explorer-temporal' },
    isMutation: true, status: 201, atMonotonicMs: 1000,
  });
  const sourceRequestId = addRequest({ name: 'source-table', path: commandPath,
    body: { commandId: 'source-table-command', snapshotToken: 'source-table-snapshot',
      expectedDraftVersion: 1, expectedDraftDigest: 'draft-1', commands: [{ type: 'CREATE_TABLE' }] },
    response: { accepted: true }, isMutation: true, atMonotonicMs: 1030 });
  const choiceProposalId = addRequest({ name: 'choice-proposal', path: choiceProposalPath,
    body: { commandId: choiceCommandId, snapshotToken: choiceSnapshotToken,
      expectedDraftVersion: 2, expectedDraftDigest: 'draft-2', outputId, constructionChoices: [choiceSelection] },
    response: { commandId: choiceCommandId, snapshotToken: choiceSnapshotToken,
      draftVersion: 2, draftDigest: 'draft-2', outputId,
      constructionChoices: [choiceSelection], candidateWorkspaceDigest: 'choice-candidate-workspace',
      candidateColumnIds: [spec.temporalColumnId], previewStatus: 'READY', previewDurationMs: 1,
      preview: { receiptId: 'column-receipt', outputId } }, isMutation: false, atMonotonicMs: 1070 });
  const choiceApplyId = addRequest({ name: 'choice-apply', path: commandPath,
    body: { commandId: choiceCommandId, snapshotToken: choiceSnapshotToken,
      expectedDraftVersion: 2, expectedDraftDigest: 'draft-2', commands: [{
        type: 'APPLY_CONSTRUCTION_CHOICE', outputId, constructionChoice: choiceSelection,
      }] }, response: { accepted: true }, isMutation: true, atMonotonicMs: 1080 });

  const addFilterProposal = (name, intent, specValue, stepId, epoch, draftVersion, draftDigest, atMonotonicMs) => {
    const predicate = clone(specValue.requestValue);
    const requestCandidate = filterConstruction(spec, intent, predicate);
    const responseCandidate = filterConstruction(spec, intent,
      intent === 'edit' ? spec.edited.savedValue : spec.initial.savedValue);
    if (intent !== 'remove') {
      requestCandidate.steps[1].id = stepId;
      responseCandidate.steps[1].id = stepId;
    }
    const proposalId = `proposal-${name}`;
    return addRequest({
      name, path: proposalPath,
      body: { outputId, expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest,
        candidateConstruction: requestCandidate },
      response: {
        proposalId,
        outputId,
        draftVersion,
        draftDigest,
        previewStatus: 'READY',
        candidateConstruction: responseCandidate,
        preview: { receiptId: proposalId, outputId },
      },
      isMutation: false,
      mountEpoch: epoch,
      atMonotonicMs,
    });
  };
  const firstProposalId = addFilterProposal('first-create', 'create', spec.initial,
    'candidate-create-filter', 1, 3, 'draft-3', 1120);
  const cancelReloadReconcileId = addRequest({ name: 'reconcile-cancel-reload', path: reconcilePath,
    body: { snapshotToken: 'snapshot-3', draftVersion: 3, draftDigest: 'draft-3', outputId },
    response: { receiptId: 'reconcile-receipt-cancel', snapshotToken: 'snapshot-3', outputs: [{ outputId }] },
    isMutation: false, mountEpoch: 2, atMonotonicMs: 1165 });
  const secondProposalId = addFilterProposal('second-create-proposal', 'create', spec.initial,
    'saved-filter-step', 2, 3, 'draft-3', 1180);
  const applyCreateId = addRequest({ name: 'create-apply-command', path: commandPath,
    body: { expectedDraftVersion: 3, expectedDraftDigest: 'draft-3', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-second-create-proposal',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 2, atMonotonicMs: 1220 });
  const createReloadReconcileId = addRequest({ name: 'reconcile-create-reload', path: reconcilePath,
    body: { snapshotToken: 'snapshot-4', draftVersion: 4, draftDigest: 'draft-4', outputId },
    response: { receiptId: 'reconcile-receipt-create', snapshotToken: 'snapshot-4', outputs: [{ outputId }] },
    isMutation: false, mountEpoch: 3, atMonotonicMs: 1255 });
  const editOpenCancellationId = addRequest({ name: 'edit-open-cancellation', path: proposalPath,
    body: { outputId, expectedDraftVersion: 4, expectedDraftDigest: 'draft-4' }, response: null,
    isMutation: false, status: null, mountEpoch: 3, atMonotonicMs: 1280,
    failure: { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true },
    readCancellationDiagnostic: true, responseCapture: 'not-configured', responseBodyCaptured: false });
  const editProposalId = addFilterProposal('edit', 'edit', spec.edited, 'saved-filter-step', 3, 4, 'draft-4', 1300);
  const applyEditId = addRequest({ name: 'apply-edit', path: commandPath,
    body: { expectedDraftVersion: 4, expectedDraftDigest: 'draft-4', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-edit',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 3, atMonotonicMs: 1340 });
  const editReloadReconcileId = addRequest({ name: 'reconcile-edit-reload', path: reconcilePath,
    body: { snapshotToken: 'snapshot-5', draftVersion: 5, draftDigest: 'draft-5', outputId },
    response: { receiptId: 'reconcile-receipt-edit', snapshotToken: 'snapshot-5', outputs: [{ outputId }] },
    isMutation: false, mountEpoch: 4, atMonotonicMs: 1375 });
  const removeProposalId = addFilterProposal('remove', 'remove', spec.edited,
    'saved-filter-step', 4, 5, 'draft-5', 1390);
  const applyRemoveId = addRequest({ name: 'apply-remove', path: commandPath,
    body: { expectedDraftVersion: 5, expectedDraftDigest: 'draft-5', commands: [{
      type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: 'proposal-remove',
    }] }, response: { accepted: true }, isMutation: true, mountEpoch: 4, atMonotonicMs: 1420 });
  const removeReloadReconcileId = addRequest({ name: 'reconcile-remove-reload', path: reconcilePath,
    body: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId },
    response: { receiptId: 'reconcile-receipt-remove', snapshotToken: 'snapshot-6', outputs: [{ outputId }] },
    isMutation: false, mountEpoch: 5, atMonotonicMs: 1455 });

  const domObservations = [];
  const addDOM = ({ epoch, proposalId = null, receiptId = null, status = null, columnId = null,
    inputType = null, inputValue = null, operator = null, rowIds, atMonotonicMs }) => {
    const capturedAtMonotonicMs = atMonotonicMs ?? baseTime + domObservations.length * 10;
    domObservations.push({
      capturedAtMonotonicMs,
      capturedAtEpochMs: epochOffset + capturedAtMonotonicMs,
      mountEpoch: epoch,
      proposalStatus: status,
      proposalId,
      receiptId,
      outputId,
      columnId,
      inputType,
      inputValue,
      operator,
      rowIds: [...rowIds],
    });
    return domObservations.length - 1;
  };
  const allIDs = spec.rows.map(row => row.id);
  const domIndexes = {
    source: addDOM({ epoch: 1, rowIds: allIDs, atMonotonicMs: 1050 }),
    column: addDOM({ epoch: 1, rowIds: allIDs, atMonotonicMs: 1100 }),
    proposeFirst: addDOM({ epoch: 1, proposalId: 'proposal-first-create', receiptId: 'proposal-first-create',
      status: 'ready', columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.initial.inputValue,
      operator: spec.initial.operator, rowIds: spec.initial.ids, atMonotonicMs: 1130 }),
    cancel: addDOM({ epoch: 1, rowIds: allIDs, atMonotonicMs: 1150 }),
    reloadCancel: addDOM({ epoch: 2, receiptId: 'reconcile-receipt-cancel', rowIds: allIDs, atMonotonicMs: 1170 }),
    proposeCreate: addDOM({ epoch: 2, proposalId: 'proposal-second-create-proposal', receiptId: 'proposal-second-create-proposal',
      status: 'ready', columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.initial.inputValue,
      operator: spec.initial.operator, rowIds: spec.initial.ids, atMonotonicMs: 1190 }),
    applyCreate: addDOM({ epoch: 2, rowIds: spec.initial.ids, atMonotonicMs: 1240 }),
    reloadCreate: addDOM({ epoch: 3, receiptId: 'reconcile-receipt-create', rowIds: spec.initial.ids, atMonotonicMs: 1260 }),
    editOpened: addDOM({ epoch: 3, columnId: spec.temporalColumnId, inputType: spec.inputType,
      inputValue: spec.initial.inputValue, operator: spec.initial.operator, rowIds: spec.initial.ids, atMonotonicMs: 1290 }),
    proposeEdit: addDOM({ epoch: 3, proposalId: 'proposal-edit', receiptId: 'proposal-edit', status: 'ready',
      columnId: spec.temporalColumnId, inputType: spec.inputType, inputValue: spec.edited.inputValue,
      operator: spec.edited.operator, rowIds: spec.edited.ids, atMonotonicMs: 1310 }),
    applyEdit: addDOM({ epoch: 3, rowIds: spec.edited.ids, atMonotonicMs: 1360 }),
    reloadEdit: addDOM({ epoch: 4, receiptId: 'reconcile-receipt-edit', rowIds: spec.edited.ids, atMonotonicMs: 1380 }),
    proposeRemove: addDOM({ epoch: 4, proposalId: 'proposal-remove', receiptId: 'proposal-remove',
      status: 'ready', rowIds: allIDs, atMonotonicMs: 1400 }),
    remove: addDOM({ epoch: 4, rowIds: allIDs, atMonotonicMs: 1440 }),
    reloadRemove: addDOM({ epoch: 5, receiptId: 'reconcile-receipt-remove', rowIds: allIDs, atMonotonicMs: 1460 }),
  };

  const step = (label, { requestCaptureId = null, proposalCaptureIds = [], reconcileCaptureIds = [], beforeReadIndex = null,
    afterReadIndex = null, domObservationIndex = null, mountEpoch = 1 }) => {
    const request = requestEvents.find(event => event.browserRequestId === requestCaptureId);
    const dom = domObservationIndex === null ? null : domObservations[domObservationIndex];
    const timestamp = request?.atMonotonicMs ?? dom?.capturedAtMonotonicMs ?? baseTime;
    const epochTimestamp = request?.atEpochMs ?? dom?.capturedAtEpochMs ?? baseTime;
    const capturedMountEpoch = request?.mountEpoch ?? dom?.mountEpoch ?? mountEpoch;
    return {
      label,
      atMonotonicMs: timestamp,
      atEpochMs: epochTimestamp,
      mountEpoch: capturedMountEpoch,
      requestCaptureId,
      proposalCaptureIds,
      reconcileCaptureIds,
      beforeReadIndex,
      afterReadIndex,
      domObservationIndex,
    };
  };
  const steps = [
    step('ExplorerCreated', { requestCaptureId: explorerRequestId, afterReadIndex: 0, mountEpoch: 1 }),
    step('SourceTableCreated', { requestCaptureId: sourceRequestId, beforeReadIndex: 1, afterReadIndex: 2,
      domObservationIndex: domIndexes.source, mountEpoch: 1 }),
    step('TemporalColumnApplied', { requestCaptureId: choiceApplyId, proposalCaptureIds: [choiceProposalId],
      beforeReadIndex: 3, afterReadIndex: 4, domObservationIndex: domIndexes.column, mountEpoch: 1 }),
    step('FilterProposed(create)', { requestCaptureId: firstProposalId, proposalCaptureIds: [firstProposalId],
      beforeReadIndex: 5, domObservationIndex: domIndexes.proposeFirst, mountEpoch: 1 }),
    step('FilterCancelled', { beforeReadIndex: 5, afterReadIndex: 6, domObservationIndex: domIndexes.cancel, mountEpoch: 1 }),
    step('Reload', { beforeReadIndex: 6, afterReadIndex: 7, domObservationIndex: domIndexes.reloadCancel,
      reconcileCaptureIds: [cancelReloadReconcileId], mountEpoch: 2 }),
    step('FilterProposed(create)', { requestCaptureId: secondProposalId, proposalCaptureIds: [secondProposalId],
      beforeReadIndex: 7, domObservationIndex: domIndexes.proposeCreate, mountEpoch: 2 }),
    step('FilterApplied(create)', { requestCaptureId: applyCreateId, beforeReadIndex: 8, afterReadIndex: 9,
      domObservationIndex: domIndexes.applyCreate, mountEpoch: 2 }),
    step('Reload', { beforeReadIndex: 9, afterReadIndex: 10, domObservationIndex: domIndexes.reloadCreate,
      reconcileCaptureIds: [createReloadReconcileId], mountEpoch: 3 }),
    step('FilterEditOpened', { beforeReadIndex: 11, proposalCaptureIds: [editOpenCancellationId],
      domObservationIndex: domIndexes.editOpened, mountEpoch: 3 }),
    step('FilterProposed(edit)', { requestCaptureId: editProposalId, proposalCaptureIds: [editProposalId],
      beforeReadIndex: 11, domObservationIndex: domIndexes.proposeEdit, mountEpoch: 3 }),
    step('FilterApplied(edit)', { requestCaptureId: applyEditId, beforeReadIndex: 12, afterReadIndex: 13,
      domObservationIndex: domIndexes.applyEdit, mountEpoch: 3 }),
    step('Reload', { beforeReadIndex: 13, afterReadIndex: 14, domObservationIndex: domIndexes.reloadEdit,
      reconcileCaptureIds: [editReloadReconcileId], mountEpoch: 4 }),
    step('FilterProposed(remove)', { requestCaptureId: removeProposalId, proposalCaptureIds: [removeProposalId],
      beforeReadIndex: 14, domObservationIndex: domIndexes.proposeRemove, mountEpoch: 4 }),
    step('FilterRemoved', { requestCaptureId: applyRemoveId, beforeReadIndex: 15, afterReadIndex: 16,
      domObservationIndex: domIndexes.remove, mountEpoch: 4 }),
    step('Reload', { beforeReadIndex: 16, afterReadIndex: 17, domObservationIndex: domIndexes.reloadRemove,
      reconcileCaptureIds: [removeReloadReconcileId], mountEpoch: 5 }),
  ];

  const project = 'loom_dev_verify_temporal';
  const explorerId = 'explorer-temporal';
  const uiOrigin = 'http://127.0.0.1:30000';
  const browserExplorerRoot = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal';
  if (pendingCapabilities) {
    const lastRead = builderReads.at(-1);
    const atMonotonicMs = 1_500;
    const atEpochMs = epochOffset + atMonotonicMs;
    const capabilityRequest = {
      snapshotToken: lastRead.snapshotToken,
      expectedDraftVersion: lastRead.draftVersion,
      expectedDraftDigest: lastRead.draftDigest,
      outputId,
      stageId: 'source_projection',
    };
    requestEvents.push({
      index: 70,
      browserRequestId: 'playwright-71',
      requestId: 'construction-capabilities-6da28800-fa44-4dae-9b23-2c8e43788e3d',
      method: 'POST',
      origin: uiOrigin,
      path: `${browserExplorerRoot}/authoring/v2/construction-capabilities`,
      status: null,
      request: clone(capabilityRequest),
      response: null,
      startedAt: atEpochMs,
      responseReceivedAt: null,
      completedAt: null,
      responseMountEpoch: null,
      failureMountEpoch: null,
      isMutation: false,
      failure: null,
      readCancellationDiagnostic: false,
      responseCapture: 'not-configured',
      responseCaptureComplete: true,
      responseBodyCaptured: false,
      networkComplete: false,
      settlement: 'pending',
      atMonotonicMs,
      atEpochMs,
      mountEpoch: 5,
    });
    requestEvents.push({
      index: 76,
      browserRequestId: 'playwright-77',
      requestId: 'construction-capabilities-59e05fd0-815e-458e-8e26-11259c5ec9d7',
      method: 'POST',
      origin: uiOrigin,
      path: `${browserExplorerRoot}/authoring/v2/construction-capabilities`,
      status: 200,
      request: clone(capabilityRequest),
      response: {
        snapshotToken: lastRead.snapshotToken,
        draftVersion: lastRead.draftVersion,
        draftDigest: lastRead.draftDigest,
        outputId,
      },
      startedAt: atEpochMs + 5,
      responseReceivedAt: atEpochMs + 10,
      completedAt: atEpochMs + 12,
      responseMountEpoch: 5,
      failureMountEpoch: null,
      isMutation: false,
      failure: null,
      readCancellationDiagnostic: false,
      responseCapture: 'captured',
      responseCaptureComplete: true,
      responseBodyCaptured: true,
      networkComplete: true,
      settlement: 'response',
      atMonotonicMs: atMonotonicMs + 5,
      atEpochMs: atEpochMs + 5,
      mountEpoch: 5,
    });
  }
  const pendingIndexes = requestEvents.filter(request => request.settlement === 'pending').map(request => request.index);
  const pendingCaptureIds = requestEvents.filter(request => request.settlement === 'pending')
    .map(request => request.browserRequestId);
  const captureCheckpointId = `schema-fixture-capture-checkpoint-${caseName}`;
  const pendingReadScope = { uiOrigin, project, explorerId, browserExplorerRoot, outputId };
  const drainStartedAtEpochMs = epochOffset + 2_000;
  const captureExit = {
    checkpointId: captureCheckpointId,
    drainStartedAtEpochMs,
    drainStartedAtMonotonicMs: 2_000,
    observedAtEpochMs: drainStartedAtEpochMs + (pendingIndexes.length > 0 ? 5_000 : 1),
    observedAtMonotonicMs: 2_000 + (pendingIndexes.length > 0 ? 5_000 : 1),
    drainTimeoutMs: 5_000,
    deadlineReached: pendingIndexes.length > 0,
    afterObserverIndex: requestEvents.at(-1)?.index ?? -1,
    requestCount: requestEvents.length,
    pendingObserverIndexes: pendingIndexes,
    pendingBrowserRequestIds: pendingCaptureIds,
    pendingResponseReadObserverIndexes: [],
    scope: pendingReadScope,
  };

  return {
    schemaVersion: 1,
    scenario: 'basic-temporal-filter',
    caseName,
    sourceCommit: 'a'.repeat(40),
    sourceFingerprint: { sha256: 'b'.repeat(64), files: 48 },
    project,
    generation: 'basic-temporal-generation',
    resourceType: spec.resourceType,
    fieldPath: spec.fieldPath,
    fixturePath: `testdata/basic-temporal-filter/${spec.resourceType}.ndjson`,
    fixtureSha256: 'c'.repeat(64),
    bootstrapExplorerId: 'schema-fixture-bootstrap-explorer',
    explorerId,
    uiOrigin,
    browserExplorerRoot,
    checkpointId: captureCheckpointId,
    pendingReadScope,
    rows: clone(spec.rows),
    ids: spec.rows.map(row => row.id),
    outputId,
    temporalColumnId: spec.temporalColumnId,
    requestEvents,
    builderReads,
    domObservations,
    steps,
    captureExit,
    lateReconcileCaptureIds: [],
    finalDOMObservationIndex: null,
  };
};
