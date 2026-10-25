import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const MODEL_PATH = new URL('../../../verification/builder-interactions/PolicyOneDisagreement.tla', import.meta.url);
const MODEL_NAME = 'PolicyOneDisagreement';
const ABSENT = 'Absent';

const EXPECTED_LABELS = [
  'GroupPreviewSubmitted', 'GroupPreviewReady', 'GroupApplySubmitted', 'GroupApplied',
  'OneSubmitted', 'OneRejected', 'SelectAll', 'AllSubmitted', 'AllReady',
  'AllApplySubmitted', 'AllApplied', 'ReloadedAll', 'RemovalPreviewSubmitted',
  'RemovalPreviewReady', 'RemovalCancelled', 'RemovalPreviewSubmitted',
  'RemovalPreviewReady', 'RemovalApplySubmitted', 'RemovalApplied', 'ReloadedSource',
];

const EXPECTED_EVENTS = [
  'preview', 'preview-response', 'begin-apply', 'finish-apply',
  'choice-preview', 'choice-preview-response', 'choice-policy-selected', 'choice-preview',
  'choice-preview-response', 'apply-construction-choice', 'apply-construction-choice-response',
  'reload', 'preview', 'preview-response', 'cancel', 'preview', 'preview-response',
  'begin-apply', 'finish-apply', 'reload',
];

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value) &&
    (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null);
}

function requireRecord(value, label) {
  assert(isRecord(value), `${label} must be an object`);
  return value;
}

function requireString(value, label) {
  assert.equal(typeof value, 'string', `${label} must be a string`);
  assert(value.length > 0, `${label} must not be empty`);
  return value;
}

function requireVersion(value, label) {
  assert(Number.isInteger(value) && value >= 0, `${label} must be a non-negative integer`);
  return value;
}

function actionIndex(trace, label, occurrence = 0) {
  const matches = trace.actions.flatMap((action, index) => action.label === label ? [index] : []);
  assert(matches.length > occurrence, `journey trace must contain ${label} occurrence ${occurrence + 1}`);
  return matches[occurrence];
}

function action(trace, label, occurrence = 0) {
  return trace.actions[actionIndex(trace, label, occurrence)];
}

function tlavalue(value) {
  if (value === null || value === undefined) return tlaString(ABSENT);
  if (typeof value === 'string') return tlaString(value);
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE';
  if (typeof value === 'number') {
    assert(Number.isFinite(value), 'TLA replay values must contain finite numbers');
    return Object.is(value, -0) ? '0' : String(value);
  }
  if (Array.isArray(value)) return `<<${value.map(tlavalue).join(', ')}>>`;
  requireRecord(value, 'TLA replay value');
  if (Object.keys(value).length === 0) return tlaString(ABSENT);
  const entries = Object.keys(value).sort().map(key => {
    assert(/^[A-Za-z][A-Za-z0-9_]*$/.test(key), `TLA record field ${key} is not an identifier`);
    return `${key} |-> ${tlavalue(value[key])}`;
  });
  return `[${entries.join(', ')}]`;
}

function tlaString(value) {
  return JSON.stringify(String(value));
}

function tlaSet(values) {
  return `{${[...new Set(values)].map(tlaString).sort().join(', ')}}`;
}

function workspaceOf(actionRecord) {
  const observation = actionRecord.observation ?? {};
  return observation.workspace ?? observation.saved?.workspace ?? observation.savedAfter?.workspace ?? null;
}

function savedSnapshotProjection(snapshot) {
  if (!isRecord(snapshot)) return { present: false };
  return {
    present: true,
    outputId: snapshot.outputId ?? ABSENT,
    rootResourceType: snapshot.rootResourceType ?? ABSENT,
    snapshotToken: snapshot.snapshotToken ?? ABSENT,
    draftVersion: snapshot.draftVersion ?? ABSENT,
    draftDigest: snapshot.draftDigest ?? ABSENT,
    sourceColumns: (snapshot.sourceColumns ?? []).map(column => ({
      id: column.id ?? ABSENT,
      label: column.label ?? ABSENT,
      sourcePath: column.sourcePath ?? ABSENT,
      projectionMode: column.projectionMode ?? ABSENT,
    })),
    group: snapshot.group === null || snapshot.group === undefined
      ? ABSENT
      : {
          stepId: snapshot.group.stepId ?? ABSENT,
          keys: snapshot.group.keys ?? [],
          aggregate: snapshot.group.aggregate ?? ABSENT,
          rowValues: snapshot.group.rowValues ?? [],
        },
  };
}

function requestFact(request) {
  if (!isRecord(request)) return { present: false, commandId: ABSENT, bodyCommandId: ABSENT };
  const body = isRecord(request.body) ? request.body : {};
  const choice = body.constructionChoices?.[0] ?? body.commands?.[0]?.constructionChoice ?? {};
  const command = body.commands?.[0] ?? {};
  return {
    present: true,
    requestId: request.requestId ?? ABSENT,
    captureId: request.captureId ?? ABSENT,
    requestObjectId: request.requestObjectId ?? ABSENT,
    commandId: request.commandId ?? ABSENT,
    bodyCommandId: body.commandId ?? ABSENT,
    outputId: body.outputId ?? command.outputId ?? ABSENT,
    snapshotToken: body.snapshotToken ?? ABSENT,
    expectedDraftVersion: body.expectedDraftVersion ?? ABSENT,
    expectedDraftDigest: body.expectedDraftDigest ?? ABSENT,
    choiceId: choice.choiceId ?? ABSENT,
    choiceField: choice.field ?? ABSENT,
    choicePolicy: choice.rowValuePolicy ?? ABSENT,
    proposalId: command.proposalId ?? ABSENT,
    candidateId: command.candidateId ?? ABSENT,
    candidateRequestId: command.candidateRequestId ?? ABSENT,
    candidateCaptureId: command.candidateCaptureId ?? ABSENT,
  };
}

function responseFact(response) {
  if (!isRecord(response)) return { present: false, commandId: ABSENT, bodyCommandId: ABSENT };
  const preview = response.preview ?? response.body?.preview ?? {};
  return {
    present: true,
    requestId: response.requestId ?? ABSENT,
    captureId: response.captureId ?? ABSENT,
    requestObjectId: response.requestObjectId ?? ABSENT,
    commandId: response.commandId ?? ABSENT,
    bodyCommandId: response.body?.commandId ?? ABSENT,
    status: response.status ?? 0,
    errorCode: response.errorCode ?? response.body?.error?.code ?? ABSENT,
    proposalId: response.proposalId ?? response.body?.proposalId ?? ABSENT,
    outputId: response.outputId ?? response.body?.outputId ?? preview.outputId ?? ABSENT,
    receiptId: preview.receiptId ?? ABSENT,
    rowCount: preview.rowCount ?? 0,
    draftVersion: response.body?.draftVersion ?? ABSENT,
    draftDigest: response.body?.draftDigest ?? ABSENT,
  };
}

function actionFact(actionRecord) {
  const observation = actionRecord.observation ?? {};
  const saved = observation.saved ?? {};
  const savedAfter = observation.savedAfter ?? {};
  const preview = observation.preview;
  const workspace = workspaceOf(actionRecord);
  return {
    label: actionRecord.label,
    event: actionRecord.event,
    hasWorkspace: workspace !== null,
    workspace: workspace ?? {},
    hasDraftVersion: Number.isInteger(observation.draftVersion ?? saved.draftVersion ?? savedAfter.draftVersion),
    draftVersion: observation.draftVersion ?? saved.draftVersion ?? savedAfter.draftVersion ?? 0,
    hasDraftDigest: typeof (observation.draftDigest ?? saved.draftDigest ?? savedAfter.draftDigest) === 'string',
    draftDigest: observation.draftDigest ?? saved.draftDigest ?? savedAfter.draftDigest ?? ABSENT,
    hasChoice: isRecord(observation.choice),
    choice: isRecord(observation.choice) ? observation.choice : {},
    hasEditor: isRecord(observation.editor),
    editor: isRecord(observation.editor) ? {
      documentId: observation.editor.documentId ?? ABSENT,
      editorId: observation.editor.editorId ?? ABSENT,
      documentTimeOrigin: String(observation.editor.documentTimeOrigin ?? ABSENT),
      mounted: observation.editor.mounted === true,
      allAvailable: observation.editor.allAvailable === true,
    } : {},
    hasPreview: isRecord(preview),
    preview: isRecord(preview) ? {
      outputId: preview.outputId ?? ABSENT,
      receiptId: preview.receiptId ?? ABSENT,
      rowCount: preview.rowCount ?? 0,
      memberIds: preview.memberIds ?? [],
      count: preview.count ?? 0,
      keys: preview.keys ?? [],
      aggregate: preview.aggregate ?? ABSENT,
    } : {},
    request: requestFact(actionRecord.request),
    response: responseFact(actionRecord.response),
    hasSavedBefore: isRecord(actionRecord.savedBefore),
    savedBefore: savedSnapshotProjection(actionRecord.savedBefore),
    hasSavedAfter: isRecord(actionRecord.savedAfter),
    savedAfter: savedSnapshotProjection(actionRecord.savedAfter),
  };
}

function validateTraceShape(trace) {
  requireRecord(trace, 'journey trace');
  assert.equal(trace.model, MODEL_NAME, `journey trace model must be ${MODEL_NAME}`);
  requireString(trace.name, 'journey trace name');
  assert(Array.isArray(trace.actions), 'journey trace actions must be an array');
  assert.equal(trace.actions.length, EXPECTED_LABELS.length,
    `journey trace must contain ${EXPECTED_LABELS.length} observed actions`);
  for (const [index, entry] of trace.actions.entries()) {
    requireRecord(entry, `journey action ${index}`);
    requireString(entry.label, `journey action ${index} label`);
    requireString(entry.event, `journey action ${index} event`);
    assert(isRecord(entry.observation), `journey action ${entry.label} must include its observation`);
  }
  const expectedCounts = new Map();
  for (const label of EXPECTED_LABELS) expectedCounts.set(label, (expectedCounts.get(label) ?? 0) + 1);
  for (const [label, count] of expectedCounts) {
    assert.equal(trace.actions.filter(entry => entry.label === label).length, count,
      `journey trace must contain ${count} ${label} action(s)`);
  }
  requireRecord(trace.initialObservation, 'initial observation');
  requireRecord(trace.initialObservation.workspace, 'initial workspace observation');
  requireVersion(trace.initialObservation.draftVersion, 'initial draft version');
  requireString(trace.initialObservation.draftDigest, 'initial draft digest');
  requireRecord(trace.initialPreview, 'initial preview');
  const patientIds = trace.initialPreview.patientIds ?? trace.initialObservation.rowIds;
  assert(Array.isArray(patientIds), 'initial preview must contain independent Patient IDs');
  assert.equal(patientIds.length, 2, 'the original two-Patient fixture precondition must remain intact');
  for (const [index, patientId] of patientIds.entries()) requireString(patientId, `Patient ID ${index}`);
  assert.equal(new Set(patientIds).size, patientIds.length, 'initial Patient IDs must be distinct');
  const sourceColumns = trace.initialObservation.workspace.sourceColumnIds;
  const sourceBindings = trace.initialObservation.workspace.sourceBindings;
  assert(Array.isArray(sourceColumns) && sourceColumns.length > 0,
    'initial workspace must expose its source column IDs');
  assert(Array.isArray(sourceBindings) && sourceBindings.length > 0,
    'initial workspace must expose its source field bindings');
  const group = action(trace, 'GroupApplied');
  const all = action(trace, 'AllApplied');
  const restored = action(trace, 'ReloadedSource');
  requireVersion(group.observation.draftVersion, 'applied Group draft version');
  requireString(group.observation.draftDigest, 'applied Group draft digest');
  requireVersion(all.observation.saved?.draftVersion, 'applied ALL draft version');
  requireString(all.observation.saved?.draftDigest, 'applied ALL draft digest');
  requireVersion(restored.observation.draftVersion, 'restored source draft version');
  requireString(restored.observation.draftDigest, 'restored source draft digest');
  assert(isRecord(action(trace, 'AllReady').observation.preview),
    'ALL response must retain its normalized preview facts');
  assert(isRecord(action(trace, 'OneRejected').response),
    'ONE rejection must retain its owned response facts');
  return trace;
}

/** Validate the canonical trace's structural envelope. TLC checks domain relations. */
export function validatePolicyOneJourneyTrace(trace) {
  return validateTraceShape(trace);
}

function deriveModelConstants(trace) {
  const sourceWorkspace = trace.initialObservation.workspace;
  const patientIds = trace.initialPreview.patientIds ?? trace.initialObservation.rowIds;
  const groupPreviewRequest = action(trace, 'GroupPreviewSubmitted').request;
  const groupPreviewResponse = action(trace, 'GroupPreviewReady').response;
  const groupApplyRequest = action(trace, 'GroupApplySubmitted').request;
  const oneRequest = action(trace, 'OneSubmitted').request;
  const allRequest = action(trace, 'AllSubmitted').request;
  const applyRequest = action(trace, 'AllApplySubmitted').request;
  const removeOneRequest = action(trace, 'RemovalPreviewSubmitted', 0).request;
  const removeOneResponse = action(trace, 'RemovalPreviewReady', 0).response;
  const removeTwoRequest = action(trace, 'RemovalPreviewSubmitted', 1).request;
  const removeTwoResponse = action(trace, 'RemovalPreviewReady', 1).response;
  const removeApplyRequest = action(trace, 'RemovalApplySubmitted').request;
  const appliedGroup = action(trace, 'GroupApplied').observation;
  const allApplied = action(trace, 'AllApplied').observation.saved;
  const restored = action(trace, 'ReloadedSource').observation;
  const editor = action(trace, 'OneSubmitted').observation.editor;
  const allChoice = action(trace, 'AllReady').observation.choice;
  const addedBinding = allApplied.group?.inputColumnBinding;
  assert(isRecord(addedBinding), 'ALL applied Group must expose its added Patient.id input binding');
  requireString(addedBinding.columnId, 'added Patient.id column ID');
  requireString(addedBinding.field, 'added Patient.id field binding');
  requireString(editor.editorId, 'actual editor identity');
  assert(Number.isFinite(editor.documentTimeOrigin), 'actual editor documentTimeOrigin must be finite');
  const preview = action(trace, 'AllReady').observation.preview;
  const removalCandidateOne = removeOneResponse.proposalId;
  const removalCandidateTwo = removeTwoResponse.proposalId;
  const rootDraftVersion = trace.initialObservation.draftVersion;
  const rootDraftDigest = trace.initialObservation.draftDigest;
  const groupDraftVersion = appliedGroup.draftVersion;
  const groupDraftDigest = appliedGroup.draftDigest;
  const allDraftVersion = allApplied.draftVersion;
  const allDraftDigest = allApplied.draftDigest;
  const requiredStrings = {
    outputId: sourceWorkspace.documentId,
    choiceId: allChoice.choiceId,
    addedColumnId: addedBinding.columnId,
    addedField: addedBinding.field,
    receiptId: preview.receiptId,
    groupProposalId: groupPreviewResponse.proposalId,
    groupApplyCommandId: groupApplyRequest.commandId ?? groupApplyRequest.body?.commandId,
    allCommandId: allRequest.commandId ?? allRequest.body?.commandId,
    removalApplyCommandId: removeApplyRequest.commandId ?? removeApplyRequest.body?.commandId,
    removalOneCandidate: removalCandidateOne,
    removalTwoCandidate: removalCandidateTwo,
  };
  for (const [label, value] of Object.entries(requiredStrings)) requireString(value, label);

  return {
    OutputId: sourceWorkspace.documentId,
    EditorId: editor.editorId,
    DocumentTimeOrigin: String(editor.documentTimeOrigin),
    ChoiceId: allChoice.choiceId,
    SourceColumnIds: sourceWorkspace.sourceColumnIds,
    SourceBindings: sourceWorkspace.sourceBindings,
    AddedPatientIdColumnId: addedBinding.columnId,
    AddedPatientIdBinding: `${addedBinding.columnId}|${addedBinding.field}`,
    AllReceiptId: preview.receiptId,
    RootDraftVersion: rootDraftVersion,
    GroupDraftVersion: groupDraftVersion,
    AllDraftVersion: allDraftVersion,
    RestoredDraftVersion: restored.draftVersion,
    RootDraftDigest: rootDraftDigest,
    GroupDraftDigest: groupDraftDigest,
    AllDraftDigest: allDraftDigest,
    RestoredDraftDigest: restored.draftDigest,
    OneRequestId: oneRequest.requestId ?? ABSENT,
    OneCaptureId: oneRequest.captureId,
    OneCommandId: oneRequest.commandId ?? oneRequest.body?.commandId ?? ABSENT,
    AllRequestId: allRequest.requestId ?? ABSENT,
    AllCaptureId: allRequest.captureId,
    AllCommandId: allRequest.commandId ?? allRequest.body?.commandId,
    ApplyRequestId: applyRequest.requestId ?? ABSENT,
    ApplyCaptureId: applyRequest.captureId,
    GroupProposalId: groupPreviewResponse.proposalId,
    GroupApplyCommandId: groupApplyRequest.commandId ?? groupApplyRequest.body?.commandId,
    RemovalOneRequestId: removeOneRequest.requestId ?? ABSENT,
    RemovalOneCaptureId: removeOneRequest.captureId,
    RemovalOneCandidateId: removalCandidateOne,
    RemovalTwoRequestId: removeTwoRequest.requestId ?? ABSENT,
    RemovalTwoCaptureId: removeTwoRequest.captureId,
    RemovalTwoCandidateId: removalCandidateTwo,
    RemovalApplyCommandId: removeApplyRequest.commandId ?? removeApplyRequest.body?.commandId,
    RemovalApplyRequestId: removeApplyRequest.requestId ?? ABSENT,
    RemovalApplyCaptureId: removeApplyRequest.captureId,
    GroupPreviewRequestId: groupPreviewRequest.requestId ?? ABSENT,
    GroupPreviewCaptureId: groupPreviewRequest.captureId,
    GroupApplyRequestId: groupApplyRequest.requestId ?? ABSENT,
    GroupApplyCaptureId: groupApplyRequest.captureId,
    SourcePatientIds: patientIds,
    ObservedLabels: trace.actions.map(entry => entry.label),
    ObservedEvents: trace.actions.map(entry => entry.event),
    ObservedActions: trace.actions.map(actionFact),
    ObservedInitialWorkspace: trace.initialObservation.workspace,
    ObservedInitialDraftVersion: trace.initialObservation.draftVersion,
    ObservedInitialDraftDigest: trace.initialObservation.draftDigest,
    ObservedInitialPreview: {
      outputId: trace.initialPreview.outputId ?? sourceWorkspace.documentId,
      rowCount: trace.initialPreview.rowCount,
      patientIds,
    },
    ObservedUnmodeledEvents: (trace.unmodeledEvents ?? []).map(event => ({
      method: String(event.method ?? '').toUpperCase(),
      path: event.path ?? ABSENT,
      commandId: event.commandId ?? ABSENT,
      commandType: event.commandType ?? ABSENT,
    })),
  };
}

function configValue(name, value) {
  if (name === 'SourceColumnIds' || name === 'SourceBindings') return `  ${name} = ${tlaSet(value)}`;
  return `  ${name} = ${tlavalue(value)}`;
}

function generateReplayModule(moduleName, observations) {
  const moduleText = [
    `---------------- MODULE ${moduleName} ----------------`,
    `EXTENDS ${MODEL_NAME}, FiniteSets, Sequences, TLC`,
    '',
    `ObservedLabels == ${tlavalue(observations.ObservedLabels)}`,
    `ObservedEvents == ${tlavalue(observations.ObservedEvents)}`,
    `ObservedActions == ${tlavalue(observations.ObservedActions)}`,
    `ObservedInitialWorkspace == ${tlavalue(observations.ObservedInitialWorkspace)}`,
    `ObservedInitialDraftVersion == ${tlavalue(observations.ObservedInitialDraftVersion)}`,
    `ObservedInitialDraftDigest == ${tlavalue(observations.ObservedInitialDraftDigest)}`,
    `ObservedInitialPreview == ${tlavalue(observations.ObservedInitialPreview)}`,
    `ObservedUnmodeledEvents == ${tlavalue(observations.ObservedUnmodeledEvents)}`,
    '',
    'ExpectedLabels == <<"GroupPreviewSubmitted", "GroupPreviewReady",',
    '  "GroupApplySubmitted", "GroupApplied", "OneSubmitted", "OneRejected",',
    '  "SelectAll", "AllSubmitted", "AllReady", "AllApplySubmitted",',
    '  "AllApplied", "ReloadedAll", "RemovalPreviewSubmitted",',
    '  "RemovalPreviewReady", "RemovalCancelled", "RemovalPreviewSubmitted",',
    '  "RemovalPreviewReady", "RemovalApplySubmitted", "RemovalApplied",',
    '  "ReloadedSource">>',
    '',
    'ExpectedEvents == <<"preview", "preview-response", "begin-apply",',
    '  "finish-apply", "choice-preview", "choice-preview-response",',
    '  "choice-policy-selected", "choice-preview", "choice-preview-response",',
    '  "apply-construction-choice", "apply-construction-choice-response",',
    '  "reload", "preview", "preview-response", "cancel", "preview",',
    '  "preview-response", "begin-apply", "finish-apply", "reload">>',
    '',
    'ActionIndex(label, occurrence) ==',
    '  CHOOSE index \\in 1..Len(ObservedActions):',
    '    /\\ ObservedActions[index].label = label',
    '    /\\ Cardinality({prior \\in 1..index : ObservedActions[prior].label = label}) = occurrence',
    '',
    'GroupPreviewSubmittedIndex == ActionIndex("GroupPreviewSubmitted", 1)',
    'GroupPreviewReadyIndex == ActionIndex("GroupPreviewReady", 1)',
    'GroupApplySubmittedIndex == ActionIndex("GroupApplySubmitted", 1)',
    'GroupAppliedIndex == ActionIndex("GroupApplied", 1)',
    'OneSubmittedIndex == ActionIndex("OneSubmitted", 1)',
    'OneRejectedIndex == ActionIndex("OneRejected", 1)',
    'SelectAllIndex == ActionIndex("SelectAll", 1)',
    'AllSubmittedIndex == ActionIndex("AllSubmitted", 1)',
    'AllReadyIndex == ActionIndex("AllReady", 1)',
    'AllApplySubmittedIndex == ActionIndex("AllApplySubmitted", 1)',
    'AllAppliedIndex == ActionIndex("AllApplied", 1)',
    'ReloadedAllIndex == ActionIndex("ReloadedAll", 1)',
    'RemovalPreviewOneSubmittedIndex == ActionIndex("RemovalPreviewSubmitted", 1)',
    'RemovalPreviewOneReadyIndex == ActionIndex("RemovalPreviewReady", 1)',
    'RemovalCancelledIndex == ActionIndex("RemovalCancelled", 1)',
    'RemovalPreviewTwoSubmittedIndex == ActionIndex("RemovalPreviewSubmitted", 2)',
    'RemovalPreviewTwoReadyIndex == ActionIndex("RemovalPreviewReady", 2)',
    'RemovalApplySubmittedIndex == ActionIndex("RemovalApplySubmitted", 1)',
    'RemovalAppliedIndex == ActionIndex("RemovalApplied", 1)',
    'ReloadedSourceIndex == ActionIndex("ReloadedSource", 1)',
    '',
    'SequenceSet(values) == {values[index] : index \\in 1..Len(values)}',
    '',
    'WorkspaceMatches(observed, actual) ==',
    '  /\\ observed.documentId = actual.documentId',
    '  /\\ observed.rootResourceType = actual.rootResourceType',
    '  /\\ SequenceSet(observed.sourceColumnIds) = actual.sourceColumnIds',
    '  /\\ SequenceSet(observed.sourceBindings) = actual.sourceBindings',
    '  /\\ SequenceSet(observed.sourceRows.patientIds) = actual.sourceRows.patientIds',
    '  /\\ observed.sourceRows.count = actual.sourceRows.count',
    '  /\\ SequenceSet(observed.additionalBindings) = actual.additionalBindings',
    '  /\\ observed.group.present = actual.group.present',
    '  /\\ observed.group.keys = actual.group.keys',
    '  /\\ observed.group.aggregate = actual.group.aggregate',
    '  /\\ observed.group.count = actual.group.count',
    '  /\\ observed.group.rowValuePolicy = actual.group.rowValuePolicy',
    '  /\\ observed.group.inputColumnId = actual.group.inputColumnId',
    '  /\\ IF ~observed.group.present',
    '       THEN /\\ SequenceSet(observed.renderedRows.patientIds) = actual.renderedRows.patientIds',
    '            /\\ observed.renderedRows.count = actual.renderedRows.count',
    '       ELSE IF observed.group.rowValuePolicy = "ALL"',
    '         THEN /\\ Len(observed.renderedRows) = 1',
    '              /\\ SequenceSet(observed.renderedRows[1].patientIds) = actual.renderedRows[1].patientIds',
    '              /\\ observed.renderedRows[1].count = actual.renderedRows[1].count',
    '         ELSE /\\ Len(observed.renderedRows) = 1',
    '              /\\ observed.renderedRows[1].count = actual.renderedRows[1].count',
    '',
    'PreviewMatches(observed, actual) ==',
    '  IF observed.outputId = NoIdentity',
    '  THEN /\\ actual.rowCount = observed.rowCount',
    '       /\\ actual.keys = observed.keys',
    '       /\\ actual.aggregate = observed.aggregate',
    '       /\\ actual.count = observed.count',
    '  ELSE /\\ observed.outputId = actual.outputId',
    '       /\\ observed.receiptId = actual.receiptId',
    '       /\\ observed.rowCount = actual.rowCount',
    '       /\\ SequenceSet(observed.memberIds) = actual.patientIds',
    '       /\\ observed.count = actual.count',
    '',
    'ChoiceMatches(observed, actual) ==',
    '  /\\ observed.field = actual.field',
    '  /\\ observed.rowValuePolicy = actual.rowValuePolicy',
    '  /\\ observed.status = actual.status',
    '',
    'EditorMatches(observed, actual) ==',
    '  IF observed.mounted',
    '  THEN /\\ actual.mounted = TRUE',
    '       /\\ observed.documentId = actual.documentId',
    '       /\\ observed.editorId = actual.editorId',
    '       /\\ observed.documentTimeOrigin = DocumentTimeOrigin',
    '       /\\ observed.allAvailable = actual.allAvailable',
    '  ELSE /\\ actual.mounted = FALSE',
    '       /\\ observed.documentId = NoIdentity',
    '       /\\ observed.editorId = NoIdentity',
    '       /\\ observed.documentTimeOrigin = DocumentTimeOrigin',
    '',
    'ObservationMatches(index, nextModel) ==',
    '  LET observed == ObservedActions[index] IN',
    '    /\\ (observed.hasWorkspace => WorkspaceMatches(observed.workspace, nextModel.workspace))',
    '    /\\ (observed.hasDraftVersion => observed.draftVersion = nextModel.draftVersion)',
    '    /\\ (observed.hasDraftDigest => observed.draftDigest = nextModel.draftDigest)',
    '    /\\ (observed.hasChoice => ChoiceMatches(observed.choice, nextModel.choice))',
    '    /\\ (observed.hasEditor => EditorMatches(observed.editor, nextModel.editor))',
    '    /\\ (observed.hasPreview =>',
    '         IF observed.label = "GroupPreviewReady"',
    '         THEN PreviewMatches(observed.preview, nextModel.groupPreview)',
    '         ELSE IF observed.label = "AllReady"',
    '           THEN PreviewMatches(observed.preview, nextModel.allPreview)',
    '           ELSE TRUE)',
    '    /\\ (observed.hasSavedBefore /\\ observed.hasSavedAfter =>',
    '         observed.savedBefore = observed.savedAfter)',
    '',
    'ResponseOwned(request, response) ==',
    '  /\\ request.present',
    '  /\\ response.present',
    '  /\\ request.requestObjectId = request.captureId',
    '  /\\ response.captureId = request.captureId',
    '  /\\ response.requestObjectId = request.requestObjectId',
    '  /\\ (request.requestId = NoIdentity => response.requestId = NoIdentity)',
    '  /\\ (request.requestId # NoIdentity => response.requestId = request.requestId)',
    '',
    'CommandIDEncodingMatches(capture) ==',
    '  capture.present /\\ capture.bodyCommandId # NoIdentity =>',
    '    capture.commandId = capture.bodyCommandId',
    '',
    'ObservedCommandIDEncoding ==',
    '  \\A index \\in 1..Len(ObservedActions):',
    '    /\\ CommandIDEncodingMatches(ObservedActions[index].request)',
    '    /\\ CommandIDEncodingMatches(ObservedActions[index].response)',
    '',
    'ObservedResponseOwnership ==',
    '  /\ ResponseOwned(ObservedActions[GroupPreviewSubmittedIndex].request, ObservedActions[GroupPreviewReadyIndex].response)',
    '  /\ ResponseOwned(ObservedActions[GroupApplySubmittedIndex].request, ObservedActions[GroupAppliedIndex].response)',
    '  /\ ResponseOwned(ObservedActions[OneSubmittedIndex].request, ObservedActions[OneRejectedIndex].response)',
    '  /\ ResponseOwned(ObservedActions[AllSubmittedIndex].request, ObservedActions[AllReadyIndex].response)',
    '  /\ ResponseOwned(ObservedActions[AllApplySubmittedIndex].request, ObservedActions[AllAppliedIndex].response)',
    '  /\ ResponseOwned(ObservedActions[RemovalPreviewOneSubmittedIndex].request, ObservedActions[RemovalPreviewOneReadyIndex].response)',
    '  /\ ResponseOwned(ObservedActions[RemovalPreviewTwoSubmittedIndex].request, ObservedActions[RemovalPreviewTwoReadyIndex].response)',
    '  /\ ResponseOwned(ObservedActions[RemovalApplySubmittedIndex].request, ObservedActions[RemovalAppliedIndex].response)',
    '',
    'ObservedProposalLinkage ==',
    '  LET groupPreview == ObservedActions[GroupPreviewReadyIndex].response',
    '      groupApply == ObservedActions[GroupApplySubmittedIndex].request',
    '      groupApplyResponse == ObservedActions[GroupAppliedIndex].response',
    '      allResponse == ObservedActions[AllReadyIndex].response',
    '      allApply == ObservedActions[AllApplySubmittedIndex].request',
    '      allApplyResponse == ObservedActions[AllAppliedIndex].response',
    '      removalPreview == ObservedActions[RemovalPreviewTwoReadyIndex].response',
    '      removalApply == ObservedActions[RemovalApplySubmittedIndex].request',
    '      removalApplyResponse == ObservedActions[RemovalAppliedIndex].response',
    '  IN /\ groupApply.proposalId = groupPreview.proposalId',
    '     /\ groupApply.commandId = groupApplyResponse.commandId',
    '     /\ allApply.commandId = allResponse.commandId',
    '     /\ allApplyResponse.commandId = allApply.commandId',
    '     /\ removalApply.proposalId = removalPreview.proposalId',
    '     /\ removalApply.commandId = removalApplyResponse.commandId',
    '     /\ groupPreview.status = 200',
    '     /\ groupApplyResponse.status = 200',
    '     /\ allResponse.status = 200',
    '     /\ allApplyResponse.status = 200',
    '     /\ removalPreview.status = 200',
    '     /\ removalApplyResponse.status = 200',
    '     /\ groupApply.outputId = OutputId',
    '     /\ allApply.outputId = OutputId',
    '     /\ removalApply.outputId = OutputId',
    '',
    'ObservedResponseStateLinkage ==',
    '  LET groupApplied == ObservedActions[GroupAppliedIndex]',
    '      allApplied == ObservedActions[AllAppliedIndex]',
    '      removed == ObservedActions[RemovalAppliedIndex]',
    '  IN /\ groupApplied.response.draftVersion = groupApplied.draftVersion',
    '     /\ groupApplied.response.draftDigest = groupApplied.draftDigest',
    '     /\ allApplied.response.draftVersion = allApplied.draftVersion',
    '     /\ allApplied.response.draftDigest = allApplied.draftDigest',
    '     /\ removed.response.draftVersion = removed.draftVersion',
    '     /\ removed.response.draftDigest = removed.draftDigest',
    '',
    'ObservedPolicyCAS ==',
    '  LET sourceRequest == ObservedActions[GroupPreviewSubmittedIndex].request',
      '      groupApply == ObservedActions[GroupApplySubmittedIndex].request',
      '      one == ObservedActions[OneSubmittedIndex].request',
      '      all == ObservedActions[AllSubmittedIndex].request',
      '      apply == ObservedActions[AllApplySubmittedIndex].request',
      '      removeOne == ObservedActions[RemovalPreviewOneSubmittedIndex].request',
      '      removeTwo == ObservedActions[RemovalPreviewTwoSubmittedIndex].request',
      '      removeApply == ObservedActions[RemovalApplySubmittedIndex].request',
      '      groupSaved == ObservedActions[GroupAppliedIndex].draftVersion',
      '      groupDigest == ObservedActions[GroupAppliedIndex].draftDigest',
      '      allSaved == ObservedActions[AllAppliedIndex].draftVersion',
      '      allDigest == ObservedActions[AllAppliedIndex].draftDigest',
      '  IN /\ sourceRequest.expectedDraftVersion = RootDraftVersion',
      '     /\ sourceRequest.outputId = OutputId',
      '     /\ sourceRequest.expectedDraftDigest = RootDraftDigest',
      '     /\ groupApply.expectedDraftVersion = RootDraftVersion',
      '     /\ groupApply.outputId = OutputId',
      '     /\ groupApply.expectedDraftDigest = RootDraftDigest',
      '     /\ groupApply.snapshotToken = sourceRequest.snapshotToken',
      '     /\ one.expectedDraftVersion = groupSaved',
      '     /\ one.outputId = OutputId',
      '     /\ one.expectedDraftDigest = groupDigest',
      '     /\ all.expectedDraftVersion = groupSaved',
      '     /\ all.outputId = OutputId',
      '     /\ all.expectedDraftDigest = groupDigest',
      '     /\ apply.expectedDraftVersion = groupSaved',
      '     /\ apply.outputId = OutputId',
      '     /\ apply.expectedDraftDigest = groupDigest',
      '     /\ one.snapshotToken = all.snapshotToken',
      '     /\ all.snapshotToken = apply.snapshotToken',
      '     /\ removeOne.expectedDraftVersion = allSaved',
      '     /\ removeOne.outputId = OutputId',
      '     /\ removeOne.expectedDraftDigest = allDigest',
      '     /\ removeTwo.expectedDraftVersion = allSaved',
      '     /\ removeTwo.outputId = OutputId',
      '     /\ removeTwo.expectedDraftDigest = allDigest',
      '     /\ removeApply.expectedDraftVersion = allSaved',
      '     /\ removeApply.outputId = OutputId',
      '     /\ removeApply.expectedDraftDigest = allDigest',
      '     /\ removeOne.snapshotToken = removeTwo.snapshotToken',
      '     /\ removeTwo.snapshotToken = removeApply.snapshotToken',
    '',
    'ObservedChoiceRepair ==',
    '  LET rejected == ObservedActions[OneRejectedIndex].choice',
      '      selected == ObservedActions[SelectAllIndex].choice',
      '      ready == ObservedActions[AllReadyIndex].choice',
      '      applyRequest == ObservedActions[AllApplySubmittedIndex].request',
      '      applied == ObservedActions[AllAppliedIndex].choice',
      '  IN /\ rejected.field = PatientIdField',
      '     /\ selected.field = rejected.field',
      '     /\ selected.rowValuePolicy = "ALL"',
      '     /\ ready.field = rejected.field',
      '     /\ ready.rowValuePolicy = "ALL"',
      '     /\ applyRequest.choiceId = ready.choiceId',
      '     /\ applyRequest.choicePolicy = "ALL"',
      '     /\ applied.field = ready.field',
      '     /\ applied.rowValuePolicy = "ALL"',
      '     /\ rejected.rowValuePolicy = "ONE"',
      '     /\ rejected.status = "error"',
      '     /\ ObservedActions[OneRejectedIndex].response.status = 422',
      '     /\ ObservedActions[OneRejectedIndex].response.errorCode = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"',
      '     /\ ObservedActions[AllReadyIndex].response.status = 200',
      '     /\ ObservedActions[AllReadyIndex].preview.rowCount = 1',
      '     /\ SequenceSet(ObservedActions[AllReadyIndex].preview.memberIds) = SourcePatientIds',
      '     /\ ObservedActions[AllReadyIndex].preview.count = 2',
      '     /\ ObservedActions[AllReadyIndex].preview.outputId = OutputId',
      '     /\ ObservedActions[AllAppliedIndex].response.status = 200',
      '     /\ ObservedActions[AllAppliedIndex].response.commandId = applyRequest.commandId',
      '     /\ applyRequest.commandId = ObservedActions[AllReadyIndex].response.commandId',
      '     /\ ObservedActions[AllReadyIndex].response.commandId = ObservedActions[AllSubmittedIndex].request.commandId',
    '',
    'ObservedUnmodeledWrites ==',
    '  \\A index \\in 1..Len(ObservedUnmodeledEvents):',
    '    ObservedUnmodeledEvents[index].method \\in {"", "GET", "HEAD", "OPTIONS"}',
    '',
    'ObservedChronology ==',
    '  /\ ObservedLabels = ExpectedLabels',
    '  /\ ObservedEvents = ExpectedEvents',
    '',
    'ObservedDraftTransitions ==',
    '  RootDraftVersion < GroupDraftVersion',
    '  /\ GroupDraftVersion < AllDraftVersion',
    '  /\ AllDraftVersion < RestoredDraftVersion',
    '',
    'InitialObservationMatches ==',
    '  /\ WorkspaceMatches(ObservedInitialWorkspace, model.workspace)',
    '  /\ ObservedInitialDraftVersion = model.draftVersion',
    '  /\ ObservedInitialDraftDigest = model.draftDigest',
    '  /\ ObservedInitialPreview.rowCount = 2',
    '  /\ SequenceSet(ObservedInitialPreview.patientIds) = SourcePatientIds',
    '',
    'VARIABLE traceIndex',
    'replayVars == <<vars, traceIndex>>',
    '',
    'ReplayInit ==',
    '  /\ Init',
    '  /\ traceIndex = 0',
    '  /\ InitialObservationMatches',
    '',
    'ReplayNext ==',
    '  /\ traceIndex < Len(ObservedActions)',
    '  /\ Next',
    '  /\ lastAction\' = ObservedActions[traceIndex + 1].label',
    '  /\ ObservationMatches(traceIndex + 1, model\')',
    '  /\ traceIndex\' = traceIndex + 1',
    '',
    'TraceSpec == ReplayInit /\ [][ReplayNext]_replayVars /\ WF_replayVars(ReplayNext)',
    'TraceProgress == traceIndex = Len(ObservedActions) \/ ENABLED ReplayNext',
    'ReplayCompletes == <> (traceIndex = Len(ObservedActions) /\ phase = "reloaded-source")',
    '',
    '====',
    '',
  ].join('\n');
  const codeStart = moduleText.indexOf('ExpectedLabels ==');
  assert(codeStart > 0, 'generated replay module must include its static action contract');
  const observedFacts = moduleText.slice(0, codeStart);
  const modelOperators = moduleText.slice(codeStart);
  const backslash = String.fromCharCode(92);
  return observedFacts + modelOperators
    .replace(' / ENABLED ReplayNext', ' __TLA_OR__ ENABLED ReplayNext')
    .replaceAll(' / ', ` /${backslash} `)
    .replace('__TLA_OR__', `${backslash}/`);
}

function generateConfig(constants) {
  const lines = [
    `SPECIFICATION TraceSpec`,
    'INVARIANT TypeOK',
    'INVARIANT GroupPreviewRetainsTwoPatientPrecondition',
    'INVARIANT RefusalPreservesSavedDraftAndWorkspace',
    'INVARIANT OneRequestHasActualRequestBinding',
    'INVARIANT AllRequestKeepsTheSameFieldAndDraft',
    'INVARIANT GroupRequestsUseObservedDraftAndCommand',
    'INVARIANT RequestResponseOwnership',
    'INVARIANT CorrectALLPreview',
    'INVARIANT ApplyAndReloadPersistALL',
    'INVARIANT RemovalCancelPreservesExactSavedState',
    'INVARIANT RemovalApplyUsesSecondCandidate',
    'INVARIANT RemovalRestoresSourceAfterReload',
    'INVARIANT ObservedResponseOwnership',
    'INVARIANT ObservedCommandIDEncoding',
    'INVARIANT ObservedProposalLinkage',
    'INVARIANT ObservedResponseStateLinkage',
    'INVARIANT ObservedPolicyCAS',
    'INVARIANT ObservedChoiceRepair',
    'INVARIANT ObservedDraftTransitions',
    'INVARIANT ObservedUnmodeledWrites',
    'INVARIANT ObservedChronology',
    'INVARIANT TraceProgress',
    'PROPERTY ReplayCompletes',
    'CHECK_DEADLOCK FALSE',
    '',
    'CONSTANTS',
  ];
  for (const [name, value] of Object.entries(constants)) {
    if (!name.startsWith('Observed') && name !== 'SourcePatientIds') lines.push(configValue(name, value));
  }
  return lines.join('\n');
}

/**
 * Compile the canonical observed journey into a TLC replay wrapper around the
 * bounded policy lifecycle model. The wrapper binds captured values as model
 * constants and synchronizes every observed action with the model's Next.
 */
export function compilePolicyOneJourneyTrace(trace, { moduleName = 'PolicyOneReplay' } = {}) {
  validateTraceShape(trace);
  assert(/^[A-Za-z][A-Za-z0-9]*$/.test(moduleName), 'TLA moduleName must be an identifier');
  const constants = deriveModelConstants(trace);
  const sourceIds = constants.SourcePatientIds;
  const baseModelText = readFileSync(MODEL_PATH, 'utf8');
  const sourceIdsPattern = /^SourcePatientIds == \{[^\n]*\}$/m;
  assert(sourceIdsPattern.test(baseModelText), 'policy model must declare its finite SourcePatientIds set');
  const boundModelText = baseModelText.replace(sourceIdsPattern, `SourcePatientIds == ${tlaSet(sourceIds)}`);
  const modelText = generateReplayModule(moduleName, constants);
  const configText = generateConfig(constants);
  return {
    moduleName,
    modelName: MODEL_NAME,
    modelText,
    configText,
    baseModelText: boundModelText,
    expectedLabels: [...EXPECTED_LABELS],
    expectedEvents: [...EXPECTED_EVENTS],
    claim: 'bounded deterministic trace replay; no concurrency or exhaustive interleaving claim',
  };
}

export const policyOneTlaReplayModelPath = fileURLToPath(MODEL_PATH);
