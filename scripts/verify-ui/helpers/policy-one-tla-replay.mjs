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
const MODELED_TRANSPORT_PATHS = new Map([
  ['POST construction-proposals', false],
  ['POST construction-choice-proposals', false],
  ['POST commands', true],
]);
const READ_ONLY_TRANSPORT_PATHS = new Map([
  ['GET builder', 'BuilderState read'],
  ['GET capability', 'Explorer capability read'],
  ['GET row-definition-choices', 'Row-definition choices read'],
  ['POST suggestions', 'Source suggestions read'],
  ['POST semantic-inventory', 'Semantic inventory read'],
  ['POST configured-column-context', 'Configured-column context read'],
  ['POST construction-capabilities', 'Construction capabilities read'],
  ['POST construction-inputs', 'Construction stage inputs read'],
  ['POST construction-choices', 'Construction source choices read'],
  ['POST preview', 'Receipt-backed preview read'],
]);
const MODELED_REQUEST_EVENTS = new Set(['preview', 'begin-apply', 'choice-preview', 'apply-construction-choice']);

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

function stableJSON(value) {
  const ordered = entry => {
    if (Array.isArray(entry)) return entry.map(ordered);
    if (entry !== null && typeof entry === 'object') {
      return Object.fromEntries(Object.keys(entry).sort().map(key => [key, ordered(entry[key])]));
    }
    return entry;
  };
  return JSON.stringify(ordered(value));
}

function endpointPath(path, label) {
  requireString(path, `${label} path`);
  let pathname;
  try {
    pathname = new URL(path, 'http://loom.invalid').pathname;
  } catch {
    assert.fail(`${label} path must be a valid URL or pathname`);
  }
  const marker = pathname.indexOf('/authoring/v2/');
  return marker < 0 ? pathname.replace(/^\/+/, '') : pathname.slice(marker + '/authoring/v2/'.length);
}

function ownerScopeForResourcePath(resourcePath, label) {
  requireString(resourcePath, `${label} resourcePath`);
  let url;
  try {
    url = new URL(resourcePath, 'http://loom.invalid');
  } catch {
    assert.fail(`${label} resourcePath must be a valid URL or pathname`);
  }
  const marker = url.pathname.indexOf('/authoring/v2/');
  assert(marker >= 0, `${label} resourcePath must retain its authoring owner scope`);
  return `${url.origin}${url.pathname.slice(0, marker)}`;
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
  const sourceColumns = snapshot.sourceColumns ?? [];
  return {
    present: true,
    outputId: snapshot.outputId ?? ABSENT,
    rootResourceType: snapshot.rootResourceType ?? ABSENT,
    snapshotToken: snapshot.snapshotToken ?? ABSENT,
    draftVersion: snapshot.draftVersion ?? ABSENT,
    draftDigest: snapshot.draftDigest ?? ABSENT,
    sourceColumnIds: sourceColumns.map(column => column.id ?? ABSENT),
    sourceBindings: sourceColumns
      .filter(column => typeof column.sourcePath === 'string' && column.sourcePath.length > 0)
      .map(column => `${column.id}|${snapshot.rootResourceType}.${column.sourcePath}`),
    sourceColumns: sourceColumns.map(column => ({
      id: column.id ?? ABSENT,
      label: column.label ?? ABSENT,
      sourcePath: column.sourcePath ?? ABSENT,
      projectionMode: column.projectionMode ?? ABSENT,
    })),
    group: snapshot.group === null || snapshot.group === undefined
      ? {
          present: false,
          stepId: ABSENT,
          keys: [],
          aggregate: 'NO_AGGREGATE',
          rowValues: [],
          outputs: [],
        }
      : {
          present: true,
          stepId: snapshot.group.stepId ?? ABSENT,
          keys: snapshot.group.keys ?? [],
          aggregate: snapshot.group.aggregate ?? ABSENT,
          rowValues: (snapshot.group.rowValues ?? []).map(rowValue => ({
            inputColumnId: rowValue.inputColumnId ?? ABSENT,
            outputColumnId: rowValue.outputColumnId ?? ABSENT,
            policy: rowValue.policy ?? ABSENT,
          })),
          outputs: (snapshot.group.outputs ?? []).map(output => ({
            id: output.id ?? ABSENT,
            type: output.type ?? ABSENT,
          })),
        },
  };
}

function requestFact(request) {
  if (!isRecord(request)) return {
    present: false,
    requestId: ABSENT,
    captureId: ABSENT,
    requestObjectId: ABSENT,
    transportIndex: 0,
    bodyFingerprint: ABSENT,
    commandId: ABSENT,
    bodyCommandId: ABSENT,
    outputId: ABSENT,
    snapshotToken: ABSENT,
    expectedDraftVersion: 0,
    expectedDraftDigest: ABSENT,
    choiceId: ABSENT,
    choiceField: ABSENT,
    choicePolicy: ABSENT,
    proposalId: ABSENT,
    removeStepIds: [],
    candidateId: ABSENT,
    candidateRequestId: ABSENT,
    candidateCaptureId: ABSENT,
  };
  const body = isRecord(request.body) ? request.body : {};
  const choice = body.constructionChoices?.[0] ?? body.commands?.[0]?.constructionChoice ?? {};
  const command = body.commands?.[0] ?? {};
  return {
    present: true,
    requestId: request.requestId ?? ABSENT,
    captureId: request.captureId ?? ABSENT,
    requestObjectId: request.requestObjectId ?? ABSENT,
    transportIndex: request.transportIndex ?? 0,
    bodyFingerprint: stableJSON(request.body ?? null),
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
    removeStepIds: body.removeStepIds ?? [],
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

function transportFact(entry) {
  const body = isRecord(entry.requestBody) ? entry.requestBody : {};
  const command = body.commands?.[0] ?? {};
  const choice = body.constructionChoices?.[0] ?? command.constructionChoice ?? {};
  const response = isRecord(entry.response) ? entry.response : {};
  const reconcileOwner = entry.reconcileOwner ?? {};
  const reconcileCAS = entry.reconcileCAS ?? {};
  return {
    transportIndex: entry.transportIndex,
    requestEventIndex: entry.requestEventIndex,
    settlementEventIndex: entry.settlementEventIndex ?? 0,
    resourcePath: entry.resourcePath,
    ownerScope: entry.ownerScope,
    method: entry.method,
    path: entry.path,
    classification: entry.classification,
    capturePhase: entry.capturePhase,
    mutatesDraft: entry.mutatesDraft,
    setupMutation: entry.setupMutation === true,
    actionLabel: entry.actionLabel ?? ABSENT,
    actionEvent: entry.actionEvent ?? ABSENT,
    requestId: entry.requestId ?? ABSENT,
    requestCaptureId: entry.requestCaptureId ?? ABSENT,
    requestBodyFingerprint: stableJSON(entry.requestBody ?? null),
    commandId: body.commandId ?? command.commandId ?? ABSENT,
    outputId: body.outputId ?? command.outputId ?? ABSENT,
    snapshotToken: body.snapshotToken ?? ABSENT,
    expectedDraftVersion: body.expectedDraftVersion ?? body.draftVersion ?? 0,
    expectedDraftDigest: body.expectedDraftDigest ?? body.draftDigest ?? ABSENT,
    choiceId: choice.choiceId ?? ABSENT,
    choicePolicy: choice.rowValuePolicy ?? ABSENT,
    removeStepIds: body.removeStepIds ?? [],
    receiptId: body.receiptId ?? ABSENT,
    currentReceiptProducerTransportIndices: entry.currentReceiptProducerTransportIndices ?? [],
    hasInitialCurrentReceiptProducer: entry.hasInitialCurrentReceiptProducer === true,
    previewDisposition: entry.previewDisposition ?? ABSENT,
    responseOwnerIndex: entry.responseOwnerIndex ?? 0,
    responseStatus: entry.status ?? 0,
    savedCASAfterDraftVersion: entry.savedCASAfter?.draftVersion ?? 0,
    savedCASAfterDraftDigest: entry.savedCASAfter?.draftDigest ?? ABSENT,
    responseRequestId: response.requestId ?? ABSENT,
    responseCommandId: response.commandId ?? ABSENT,
    responseDraftVersion: response.draftVersion ?? 0,
    responseDraftDigest: response.draftDigest ?? ABSENT,
    responseReceiptId: response.receiptId ?? ABSENT,
    responseReceiptIds: response.receiptIds ?? [],
    responseOutputIds: response.outputIds ?? [],
    responseSnapshotToken: response.snapshotToken ?? ABSENT,
    candidateWorkspaceDigest: response.candidateWorkspaceDigest ?? ABSENT,
    intentDigest: response.intentDigest ?? ABSENT,
    ownerRequestId: reconcileOwner.requestId ?? ABSENT,
    ownerGeneration: reconcileOwner.compileGeneration ?? 0,
    ownerAttempt: reconcileOwner.attempt ?? 0,
    reconcileSnapshotToken: reconcileCAS.snapshotToken ?? ABSENT,
    reconcileDraftVersion: reconcileCAS.draftVersion ?? 0,
    reconcileDraftDigest: reconcileCAS.draftDigest ?? ABSENT,
    reconcileOutputId: reconcileCAS.outputId ?? ABSENT,
    compatibleProposalTransportIndices: entry.compatibleProposalTransportIndices ?? [],
    successfulPreviewConsumerTransportIndices: entry.successfulPreviewConsumerTransportIndices ?? [],
    invalidatedByTransportIndices: entry.invalidatedByTransportIndices ?? [],
    reconcileOwnerChangeCaptureIds: entry.reconcileOwnerChangeCaptureIds ?? [],
    reconcileOwnerChangeSetupCommandIds: entry.reconcileOwnerChangeSetupCommandIds ?? [],
    ownerInvalidationObserved: entry.ownerInvalidationObserved === true,
    reconcileDisposition: entry.reconcileDisposition ?? ABSENT,
    readOnlyReason: entry.readOnlyReason ?? ABSENT,
    settlement: entry.settlement,
  };
}

function validateTraceShape(trace) {
  requireRecord(trace, 'journey trace');
  assert.equal(trace.model, MODEL_NAME, `journey trace model must be ${MODEL_NAME}`);
  requireString(trace.name, 'journey trace name');
  assert(Array.isArray(trace.actions), 'journey trace actions must be an array');
  assert.equal(trace.actions.length, EXPECTED_LABELS.length,
    `journey trace must contain ${EXPECTED_LABELS.length} observed actions`);
  assert(Array.isArray(trace.transportLedger), 'journey trace must include its raw transport ledger');
  assert(trace.transportLedger.length > 0, 'raw transport ledger must not be empty');
  const formalEntry = requireRecord(trace.formalEntry, 'formal entry boundary');
  assert(Number.isInteger(formalEntry.afterTransportIndex) && formalEntry.afterTransportIndex >= 0,
    'formal entry must retain its exact final setup transport index');
  assert(Number.isInteger(formalEntry.afterEventIndex) && formalEntry.afterEventIndex >= 0,
    'formal entry must retain its exact final setup settlement event index');
  const initialSavedState = requireRecord(formalEntry.initialSavedState,
    'formal entry initial saved Builder snapshot');
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
  const transportEventIndices = [];
  let lastSetupTransportIndex = 0;
  let lastSetupEventIndex = 0;
  for (const [index, entry] of trace.transportLedger.entries()) {
    requireRecord(entry, `transport ledger row ${index}`);
    assert.equal(entry.transportIndex, index + 1,
      'transport ledger rows must be ordered by contiguous observer index');
    assert(Number.isInteger(entry.requestEventIndex) && entry.requestEventIndex > 0,
      `transport ledger row ${index} must retain its request event index`);
    assert(Number.isInteger(entry.settlementEventIndex)
      && entry.settlementEventIndex > entry.requestEventIndex,
    `transport ledger row ${index} must retain its ordered settlement event`);
    requireString(entry.method, `transport ledger row ${index} method`);
    assert.equal(entry.method, entry.method.toUpperCase(),
      `transport ledger row ${index} method must be normalized`);
    requireString(entry.path, `transport ledger row ${index} path`);
    requireString(entry.resourcePath, `transport ledger row ${index} resourcePath`);
    requireString(entry.ownerScope, `transport ledger row ${index} ownerScope`);
    assert.equal(endpointPath(entry.resourcePath, `transport ledger row ${index}`), entry.path,
      `transport ledger row ${index} resourcePath must match its endpoint`);
    assert.equal(ownerScopeForResourcePath(entry.resourcePath, `transport ledger row ${index}`), entry.ownerScope,
      `transport ledger row ${index} ownerScope must be derived from its resourcePath`);
    assert(['response', 'request-failed'].includes(entry.settlement),
      `transport ledger row ${index} must have a settled response or failure`);
    assert(['SETUP', 'FORMAL'].includes(entry.capturePhase),
      `transport ledger row ${index} must retain its capture phase`);
    assert.equal(typeof entry.setupMutation, 'boolean',
      `transport ledger row ${index} must classify whether a setup mutation occurred`);
    if (entry.capturePhase === 'SETUP') {
      lastSetupTransportIndex = entry.transportIndex;
      lastSetupEventIndex = Math.max(lastSetupEventIndex, entry.settlementEventIndex);
      assert(entry.transportIndex <= formalEntry.afterTransportIndex,
        `setup transport ${entry.transportIndex} must precede formal entry`);
      assert(entry.settlementEventIndex <= formalEntry.afterEventIndex,
        `setup transport ${entry.transportIndex} must settle before formal entry`);
    } else {
      assert(entry.transportIndex > formalEntry.afterTransportIndex,
        `formal transport ${entry.transportIndex} must follow setup transport indexes`);
      assert(entry.requestEventIndex > formalEntry.afterEventIndex,
        `formal transport ${entry.transportIndex} must follow setup settlements`);
    }
    if (entry.settlement === 'response') {
      assert.equal(entry.responseOwnerIndex, entry.transportIndex,
        `transport ledger row ${index} response must retain its exact request owner`);
      assert(Number.isInteger(entry.status) && entry.status > 0,
        `transport ledger row ${index} response must retain its HTTP status`);
      assert(isRecord(entry.response), `transport ledger row ${index} must retain normalized response facts`);
      assert.equal(entry.response.status, entry.status,
        `transport ledger row ${index} response status must match the settlement`);
    } else {
      assert.equal(entry.responseOwnerIndex, null,
        `failed transport ledger row ${index} cannot claim a response owner`);
      assert.equal(entry.status, null, `failed transport ledger row ${index} cannot have a response status`);
      assert.equal(entry.response ?? null, null, `failed transport ledger row ${index} cannot have response facts`);
    }
    const route = `${entry.method} ${entry.path}`;
    const modeledMutation = MODELED_TRANSPORT_PATHS.get(route);
    if (entry.capturePhase === 'SETUP' && route === 'POST commands') {
      assert.equal(entry.classification, 'setup', 'pre-entry commands must be declared SETUP mutations');
      assert.equal(entry.mutatesDraft, true, 'SETUP commands must declare their saved-draft mutation');
      assert.equal(entry.setupMutation, true, 'SETUP commands must retain their explicit mutation classification');
    } else if (modeledMutation !== undefined) {
      assert.equal(entry.capturePhase, 'FORMAL', `${route} semantic requests must follow formal entry`);
      assert.equal(entry.classification, 'modeled', `${route} must remain semantically modeled`);
      assert.equal(entry.mutatesDraft, modeledMutation, `${route} draft-mutation classification must match its contract`);
      assert.equal(entry.setupMutation, false, `${route} formal requests cannot be setup mutations`);
      if (route === 'POST commands' && entry.status >= 200 && entry.status < 300) {
        requireRecord(entry.savedCASAfter,
          `successful command transport ${entry.transportIndex} after-Builder CAS`);
        requireVersion(entry.savedCASAfter.draftVersion,
          `successful command transport ${entry.transportIndex} after-Builder draft version`);
        requireString(entry.savedCASAfter.draftDigest,
          `successful command transport ${entry.transportIndex} after-Builder draft digest`);
        assert.equal(entry.response.draftVersion, entry.savedCASAfter.draftVersion);
        assert.equal(entry.response.draftDigest, entry.savedCASAfter.draftDigest);
        const responseAction = trace.actions.find(action =>
          action.response?.requestObjectId === entry.requestCaptureId && action.response?.status >= 200
            && action.response?.status < 300);
        assert(responseAction,
          `successful command transport ${entry.transportIndex} must bind its exact response action`);
        const observedSaved = responseAction.savedAfter ?? responseAction.observation?.saved ?? {};
        assert.equal(observedSaved.draftVersion ?? responseAction.observation?.draftVersion,
          entry.savedCASAfter.draftVersion,
        `successful command transport ${entry.transportIndex} must match the after-Builder draft version`);
        assert.equal(observedSaved.draftDigest ?? responseAction.observation?.draftDigest,
          entry.savedCASAfter.draftDigest,
        `successful command transport ${entry.transportIndex} must match the after-Builder draft digest`);
      }
    } else if (route === 'POST reconcile') {
      assert.equal(entry.classification, 'reconcile', 'reconcile POSTs must retain owner/CAS modeling');
      assert.equal(entry.setupMutation, false, 'reconcile reads cannot be setup mutations');
      const ownerMatch = /^builder-(\d+)-(\d+)$/.exec(entry.requestId ?? '');
      assert(ownerMatch, `reconcile transport ${entry.transportIndex} must retain its Builder owner generation and attempt`);
      const body = requireRecord(entry.requestBody, `reconcile transport ${entry.transportIndex} request body`);
      assert.equal(entry.reconcileOwner?.requestId, entry.requestId);
      assert.equal(entry.reconcileOwner?.compileGeneration, Number(ownerMatch[1]));
      assert.equal(entry.reconcileOwner?.attempt, Number(ownerMatch[2]));
      assert.deepEqual(entry.reconcileCAS, {
        snapshotToken: body.snapshotToken,
        draftVersion: body.draftVersion,
        draftDigest: body.draftDigest,
        outputId: body.outputId ?? null,
      });
      assert.equal(entry.reconcileCAS.snapshotToken, body.snapshotToken);
      assert.equal(entry.reconcileCAS.draftVersion, body.draftVersion);
      assert.equal(entry.reconcileCAS.draftDigest, body.draftDigest);
      assert(Array.isArray(entry.reconcileOwnerChangeCaptureIds),
        `reconcile transport ${entry.transportIndex} must retain its owner-change capture IDs`);
      assert(Array.isArray(entry.reconcileOwnerChangeSetupCommandIds),
        `reconcile transport ${entry.transportIndex} must retain its setup command IDs`);
      assert(Array.isArray(entry.successfulPreviewConsumerTransportIndices),
        `reconcile transport ${entry.transportIndex} must retain its successful preview consumers`);
      if (entry.settlement === 'response' && entry.status === 200) {
        requireString(entry.response.receiptId, `reconcile transport ${entry.transportIndex} receiptId`);
        assert.equal(entry.response.snapshotToken, body.snapshotToken);
        assert(Array.isArray(entry.response.outputIds) && entry.response.outputIds.length > 0,
          `reconcile transport ${entry.transportIndex} must retain output IDs`);
        assert.equal(typeof entry.ownerInvalidationObserved, 'boolean');
      } else {
        assert.equal(entry.ownerInvalidationObserved, false);
      }
    } else {
      const readOnlyReason = READ_ONLY_TRANSPORT_PATHS.get(route);
      assert(readOnlyReason, `unsupported authoring transport ${route}`);
      assert.equal(entry.classification, 'read-only', `${route} must be explicitly classified as read-only`);
      assert.equal(entry.mutatesDraft, false, `${route} cannot be classified as a draft mutation`);
      assert.equal(entry.setupMutation, false, `${route} cannot be a setup mutation`);
      assert.equal(entry.readOnlyReason, readOnlyReason, `${route} must retain its registered read-only reason`);
      if (route === 'POST preview') {
        assert(['successful-response', 'http-error', 'request-failed'].includes(entry.previewDisposition),
          `preview transport ${entry.transportIndex} must retain its response disposition`);
        assert(Array.isArray(entry.currentReceiptProducerTransportIndices),
          `preview transport ${entry.transportIndex} must retain its current receipt producers`);
        assert.equal(typeof entry.hasInitialCurrentReceiptProducer, 'boolean');
        const expectedDisposition = entry.settlement === 'request-failed'
          ? 'request-failed'
          : entry.status >= 200 && entry.status < 300
            ? 'successful-response'
            : 'http-error';
        assert.equal(entry.previewDisposition, expectedDisposition);
        if (entry.previewDisposition === 'successful-response') {
          assert(entry.response.receiptIds?.includes(entry.requestBody.receiptId),
            `preview transport ${entry.transportIndex} response must echo its submitted receipt`);
          assert(entry.response.outputIds?.includes(entry.requestBody.outputId),
            `preview transport ${entry.transportIndex} response must echo its submitted output`);
        }
      }
    }
    transportEventIndices.push(entry.requestEventIndex, entry.settlementEventIndex);
  }
  assert.equal(new Set(transportEventIndices).size, transportEventIndices.length,
    'each observed transport request and settlement must have a distinct event index');
  assert.deepEqual([...transportEventIndices].sort((left, right) => left - right),
    Array.from({ length: transportEventIndices.length }, (_, index) => index + 1),
    'transport ledger must retain every request and settlement event in its scope');
  assert.equal(formalEntry.afterTransportIndex, lastSetupTransportIndex,
    'formal entry must retain the actual final setup transport index');
  assert.equal(formalEntry.afterEventIndex, lastSetupEventIndex,
    'formal entry must retain the actual final setup settlement event index');

  const actionTransportIndices = trace.actions
    .filter(entry => MODELED_REQUEST_EVENTS.has(entry.event))
    .map(entry => entry.request?.transportIndex);
  assert(actionTransportIndices.every(index => Number.isInteger(index) && index > 0),
    'every modeled request action must retain its raw transport index');
  assert.equal(new Set(actionTransportIndices).size, actionTransportIndices.length,
    'modeled request actions must bind distinct raw transport indexes');
  const ledgerModeledIndices = trace.transportLedger
    .filter(entry => entry.classification === 'modeled')
    .map(entry => entry.transportIndex);
  assert.deepEqual([...ledgerModeledIndices].sort((left, right) => left - right),
    [...actionTransportIndices].sort((left, right) => left - right),
    'modeled raw transport rows must biject to modeled request actions');
  const modeledActionByTransport = new Map(trace.actions
    .filter(entry => MODELED_REQUEST_EVENTS.has(entry.event))
    .map(entry => [entry.request.transportIndex, entry]));
  for (const entry of trace.transportLedger.filter(row => row.classification === 'modeled')) {
    const actionRecord = modeledActionByTransport.get(entry.transportIndex);
    assert(actionRecord, `modeled transport ${entry.transportIndex} must bind one action request`);
    assert.equal(entry.actionLabel, actionRecord.label);
    assert.equal(entry.actionEvent, actionRecord.event);
    assert.equal(entry.requestId ?? null, actionRecord.request.requestId ?? null);
    assert.equal(entry.requestCaptureId ?? null, actionRecord.request.captureId ?? null);
  }
  requireRecord(trace.initialObservation, 'initial observation');
  requireRecord(trace.initialObservation.workspace, 'initial workspace observation');
  requireString(trace.initialObservation.outputId, 'initial output ID');
  requireString(trace.initialObservation.snapshotToken, 'initial saved snapshot token');
  requireVersion(trace.initialObservation.draftVersion, 'initial draft version');
  requireString(trace.initialObservation.draftDigest, 'initial draft digest');
  assert.equal(initialSavedState.outputId, trace.initialObservation.outputId);
  assert.equal(initialSavedState.snapshotToken, trace.initialObservation.snapshotToken);
  assert.equal(initialSavedState.draftVersion, trace.initialObservation.draftVersion);
  assert.equal(initialSavedState.draftDigest, trace.initialObservation.draftDigest);
  assert.deepEqual(initialSavedState.workspaceProjection, trace.initialObservation.workspace,
    'formal entry saved snapshot projection must equal the initial observed workspace');
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

export function buildPolicyOneMutationControls(trace) {
  validateTraceShape(trace);
  const controls = [];
  const add = (name, invariant, mutate) => {
    const mutatedTrace = structuredClone(trace);
    mutate(mutatedTrace);
    controls.push({
      name,
      trace: mutatedTrace,
      expectedFailure: { stage: 'TLC', invariant },
    });
  };

  const originalOneResponse = action(trace, 'OneRejected').response;
  const otherRequestObjectId = action(trace, 'GroupApplied').response.requestObjectId;
  requireString(originalOneResponse.requestObjectId, 'ONE response requestObjectId');
  requireString(otherRequestObjectId, 'other response requestObjectId');
  assert.notEqual(originalOneResponse.requestObjectId, otherRequestObjectId,
    'mutation controls require distinct captured response owners');
  add('wrong-owner', 'ObservedResponseOwnership', mutated => {
    action(mutated, 'OneRejected').response.requestObjectId = otherRequestObjectId;
  });

  const actualAllRequest = action(trace, 'AllSubmitted').request.body;
  const staleRequest = action(trace, 'GroupPreviewSubmitted').request.body;
  assert.notEqual(actualAllRequest.expectedDraftVersion, staleRequest.expectedDraftVersion,
    'mutation controls require a captured older draft version');
  assert.notEqual(actualAllRequest.expectedDraftDigest, staleRequest.expectedDraftDigest,
    'mutation controls require a captured older draft digest');
  add('stale-cas', 'ObservedPolicyCAS', mutated => {
    const body = action(mutated, 'AllSubmitted').request.body;
    body.expectedDraftVersion = staleRequest.expectedDraftVersion;
    body.expectedDraftDigest = staleRequest.expectedDraftDigest;
    const transport = mutated.transportLedger.find(entry =>
      entry.transportIndex === action(mutated, 'AllSubmitted').request.transportIndex);
    assert(transport, 'stale-CAS mutation must retain its matching raw transport row');
    transport.requestBody.expectedDraftVersion = staleRequest.expectedDraftVersion;
    transport.requestBody.expectedDraftDigest = staleRequest.expectedDraftDigest;
  });

  const allSubmittedIndex = actionIndex(trace, 'AllSubmitted');
  const allReadyIndex = actionIndex(trace, 'AllReady');
  add('out-of-order-repair', 'ObservedChronology', mutated => {
    [mutated.actions[allSubmittedIndex], mutated.actions[allReadyIndex]] =
      [mutated.actions[allReadyIndex], mutated.actions[allSubmittedIndex]];
  });

  const actualAllState = action(trace, 'AllApplied').observation.saved;
  const originalRestoredState = action(trace, 'RemovalApplied').savedAfter;
  assert.notEqual(originalRestoredState.draftVersion, actualAllState.draftVersion,
    'mutation controls require a restored version distinct from the captured ALL version');
  assert.notEqual(originalRestoredState.draftDigest, actualAllState.draftDigest,
    'mutation controls require a restored digest distinct from the captured ALL digest');
  add('stale-restoration', 'TraceProgress', mutated => {
    const removalApplied = action(mutated, 'RemovalApplied');
    const savedAfter = removalApplied.savedAfter;
    savedAfter.draftVersion = actualAllState.draftVersion;
    savedAfter.draftDigest = actualAllState.draftDigest;
    removalApplied.response.body.draftVersion = actualAllState.draftVersion;
    removalApplied.response.body.draftDigest = actualAllState.draftDigest;
    removalApplied.observation.draftVersion = actualAllState.draftVersion;
    removalApplied.observation.draftDigest = actualAllState.draftDigest;
    const removalRequest = action(mutated, 'RemovalApplySubmitted').request;
    const removalTransport = mutated.transportLedger.find(entry =>
      entry.transportIndex === removalRequest.transportIndex);
    assert(removalTransport, 'stale-restoration mutation must retain its matching raw command row');
    removalTransport.savedCASAfter = {
      draftVersion: actualAllState.draftVersion,
      draftDigest: actualAllState.draftDigest,
    };
    removalTransport.response.draftVersion = actualAllState.draftVersion;
    removalTransport.response.draftDigest = actualAllState.draftDigest;
  });

  add('request-body-ledger-drift', 'ObservedTransportLedgerInvariant', mutated => {
    action(mutated, 'GroupPreviewSubmitted').request.body.observationProbe = true;
  });

  const setupMutation = trace.transportLedger.find(entry => entry.capturePhase === 'SETUP' && entry.setupMutation);
  assert(setupMutation, 'mutation controls require an observed successful setup command');
  const actualFormalCommandId = action(trace, 'GroupApplySubmitted').request.commandId;
  requireString(actualFormalCommandId, 'actual formal Group Apply commandId');
  assert.notEqual(setupMutation.response.commandId, actualFormalCommandId,
    'setup response owner control requires distinct captured command IDs');
  add('setup-command-response-owner', 'ObservedTransportLedgerInvariant', mutated => {
    const row = mutated.transportLedger.find(entry => entry.transportIndex === setupMutation.transportIndex);
    assert(row, 'setup response owner mutation must retain its exact setup transport');
    row.response.commandId = actualFormalCommandId;
  });

  const appliedAllGroup = action(trace, 'RemovalApplied').savedBefore.group;
  const rowValue = appliedAllGroup.rowValues.find(value => value.policy === 'ALL');
  assert(rowValue, 'mutation controls require the captured ALL Group row value');
  const rowValueOutput = appliedAllGroup.outputs.find(output => output.id === rowValue.outputColumnId);
  const distinctOutput = appliedAllGroup.outputs.find(output => output.id !== rowValue.outputColumnId);
  assert(rowValueOutput, 'mutation controls require the declared ALL row-value output');
  assert(distinctOutput, 'mutation controls require another captured Group output');
  assert.notEqual(rowValueOutput.type, distinctOutput.type,
    'mutation controls require the captured array and scalar output types to differ');
  add('wrong-row-value-output', 'TraceProgress', mutated => {
    const savedGroup = action(mutated, 'RemovalApplied').savedBefore.group;
    const savedRowValue = savedGroup.rowValues.find(value => value.policy === 'ALL');
    const otherOutput = savedGroup.outputs.find(output => output.id !== savedRowValue.outputColumnId);
    savedRowValue.outputColumnId = otherOutput.id;
  });
  add('non-array-row-value-output', 'TraceProgress', mutated => {
    const savedGroup = action(mutated, 'RemovalApplied').savedBefore.group;
    const savedRowValue = savedGroup.rowValues.find(value => value.policy === 'ALL');
    const savedOutput = savedGroup.outputs.find(output => output.id === savedRowValue.outputColumnId);
    const otherOutput = savedGroup.outputs.find(output => output.id !== savedRowValue.outputColumnId);
    savedOutput.type = otherOutput.type;
  });

  assert.equal(new Set(controls.map(control => control.name)).size, controls.length,
    'Policy One mutation control names must be unique');
  return controls;
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
  const addedOutputBinding = allApplied.group?.outputColumnBinding;
  assert(isRecord(addedBinding), 'ALL applied Group must expose its added Patient.id input binding');
  requireString(addedBinding.columnId, 'added Patient.id column ID');
  requireString(addedBinding.field, 'added Patient.id field binding');
  assert(isRecord(addedOutputBinding), 'ALL applied Group must expose its declared row-value output binding');
  requireString(addedOutputBinding.columnId, 'added Patient.id row-value output column ID');
  assert.equal(addedOutputBinding.type, 'array', 'ALL applied Group output must be declared as an array');
  assert.notEqual(addedBinding.columnId, addedOutputBinding.columnId,
    'ALL row-value input and output column IDs must be distinct');
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
    AddedPatientIdOutputColumnId: addedOutputBinding.columnId,
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
    ObservedTransportLedger: trace.transportLedger.map(transportFact),
    ObservedInitialWorkspace: trace.initialObservation.workspace,
    ObservedInitialDraftVersion: trace.initialObservation.draftVersion,
    ObservedInitialDraftDigest: trace.initialObservation.draftDigest,
    ObservedInitialPreview: {
      outputId: trace.initialPreview.outputId ?? sourceWorkspace.documentId,
      rowCount: trace.initialPreview.rowCount,
      patientIds,
      receiptId: trace.initialPreview.binding?.receiptId ?? ABSENT,
    },
    ObservedFormalEntry: {
      afterTransportIndex: trace.formalEntry.afterTransportIndex,
      afterEventIndex: trace.formalEntry.afterEventIndex,
      outputId: trace.formalEntry.initialSavedState.outputId,
      snapshotToken: trace.formalEntry.initialSavedState.snapshotToken,
      draftVersion: trace.formalEntry.initialSavedState.draftVersion,
      draftDigest: trace.formalEntry.initialSavedState.draftDigest,
      workspace: trace.formalEntry.initialSavedState.workspaceProjection,
    },
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
    `ObservedTransportLedger == ${tlavalue(observations.ObservedTransportLedger)}`,
    `ObservedInitialWorkspace == ${tlavalue(observations.ObservedInitialWorkspace)}`,
    `ObservedInitialDraftVersion == ${tlavalue(observations.ObservedInitialDraftVersion)}`,
    `ObservedInitialDraftDigest == ${tlavalue(observations.ObservedInitialDraftDigest)}`,
    `ObservedInitialPreview == ${tlavalue(observations.ObservedInitialPreview)}`,
    `ObservedFormalEntry == ${tlavalue(observations.ObservedFormalEntry)}`,
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
    '  /\\ observed.group.outputColumnBinding.columnId = actual.group.outputColumnId',
    '  /\\ observed.group.outputColumnBinding.type = actual.group.outputColumnType',
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
    'SnapshotGroupMatches(observed, expected) ==',
    '  /\\ observed.present = expected.present',
    '  /\\ (observed.present =>',
    '       /\\ observed.keys = expected.keys',
    '       /\\ observed.aggregate = expected.aggregate',
    '       /\\ IF Len(observed.rowValues) = 0',
    '            THEN /\\ expected.rowValuePolicy = NoPolicy',
    '                 /\\ expected.inputColumnId = NoColumn',
    '                 /\\ expected.outputColumnId = NoColumn',
    '            ELSE /\\ Len(observed.rowValues) = 1',
    '                 /\\ observed.rowValues[1].inputColumnId = expected.inputColumnId',
    '                 /\\ observed.rowValues[1].outputColumnId = expected.outputColumnId',
    '                 /\\ observed.rowValues[1].policy = expected.rowValuePolicy',
    '                 /\\ \\E outputIndex \\in 1..Len(observed.outputs):',
    '                      /\\ observed.outputs[outputIndex].id = expected.outputColumnId',
    '                      /\\ observed.outputs[outputIndex].type = expected.outputColumnType)',
    '',
    'SnapshotMatchesModel(snapshot, stateModel) ==',
    '  /\\ snapshot.present',
    '  /\\ snapshot.outputId = stateModel.workspace.documentId',
    '  /\\ snapshot.rootResourceType = stateModel.workspace.rootResourceType',
    '  /\\ snapshot.draftVersion = stateModel.draftVersion',
    '  /\\ snapshot.draftDigest = stateModel.draftDigest',
    '  /\\ SequenceSet(snapshot.sourceColumnIds) =',
    '       stateModel.workspace.sourceColumnIds \\cup',
    '         (IF stateModel.workspace.additionalBindings = NoAdditionalBindings',
    '          THEN {} ELSE {AddedPatientIdColumnId})',
    '  /\\ SequenceSet(snapshot.sourceBindings) =',
    '       stateModel.workspace.sourceBindings \\cup stateModel.workspace.additionalBindings',
    '  /\\ SnapshotGroupMatches(snapshot.group, stateModel.workspace.group)',
    '',
    'NonMutatingSavedEvents ==',
    '  {\"preview\", \"preview-response\", \"choice-preview\",',
    '   \"choice-preview-response\", \"choice-policy-selected\", \"cancel\", \"reload\"}',
    '',
    'ObservationMatches(index, currentModel, nextModel) ==',
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
    '    /\\ (observed.hasSavedBefore =>',
    '         SnapshotMatchesModel(observed.savedBefore, currentModel))',
    '    /\\ (observed.hasSavedAfter =>',
    '         SnapshotMatchesModel(observed.savedAfter, nextModel))',
    '    /\\ (observed.event \\in NonMutatingSavedEvents /\\',
    '        observed.hasSavedBefore /\\ observed.hasSavedAfter =>',
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
    '      groupSnapshot == ObservedActions[GroupAppliedIndex].savedAfter',
    '      removalPreview == ObservedActions[RemovalPreviewTwoReadyIndex].response',
    '      removeOne == ObservedActions[RemovalPreviewOneSubmittedIndex].request',
    '      removeTwo == ObservedActions[RemovalPreviewTwoSubmittedIndex].request',
    '      removalApply == ObservedActions[RemovalApplySubmittedIndex].request',
    '      removalApplyResponse == ObservedActions[RemovalAppliedIndex].response',
    '  IN /\ groupApply.proposalId = groupPreview.proposalId',
    '     /\ groupApply.commandId = groupApplyResponse.commandId',
    '     /\ allApply.commandId = allResponse.commandId',
    '     /\ allApplyResponse.commandId = allApply.commandId',
    '     /\ removalApply.proposalId = removalPreview.proposalId',
    '     /\ removalApply.commandId = removalApplyResponse.commandId',
    '     /\ removeOne.removeStepIds = <<groupSnapshot.group.stepId>>',
    '     /\ removeTwo.removeStepIds = <<groupSnapshot.group.stepId>>',
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
    'ModeledRequestEvents == {"preview", "begin-apply", "choice-preview", "apply-construction-choice"}',
    '',
    'ModeledRequestActionRows ==',
    '  {index \\in 1..Len(ObservedActions) :',
    '    ObservedActions[index].event \\in ModeledRequestEvents}',
    '',
    'ActionTransportIndices ==',
    '  {ObservedActions[index].request.transportIndex : index \\in ModeledRequestActionRows}',
    '',
    'LedgerModeledRows ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    ObservedTransportLedger[index].classification = "modeled"}',
    '',
    'LedgerModeledIndices ==',
    '  {ObservedTransportLedger[index].transportIndex : index \\in LedgerModeledRows}',
    '',
    'LedgerTransportIndices ==',
    '  {ObservedTransportLedger[index].transportIndex : index \\in 1..Len(ObservedTransportLedger)}',
    '',
    'TransportActionMatches(entry) ==',
    '  \\E actionIndex \\in 1..Len(ObservedActions):',
    '    LET action == ObservedActions[actionIndex] IN',
    '      /\\ action.label = entry.actionLabel',
    '      /\\ action.event = entry.actionEvent',
    '      /\\ action.request.present',
    '      /\\ action.request.transportIndex = entry.transportIndex',
    '      /\\ action.request.bodyFingerprint = entry.requestBodyFingerprint',
    '      /\\ action.request.requestId = entry.requestId',
    '      /\\ action.request.captureId = entry.requestCaptureId',
    '      /\\ action.request.commandId = entry.commandId',
    '      /\\ action.request.outputId = entry.outputId',
    '      /\\ action.request.snapshotToken = entry.snapshotToken',
    '      /\\ action.request.expectedDraftVersion = entry.expectedDraftVersion',
    '      /\\ action.request.expectedDraftDigest = entry.expectedDraftDigest',
    '      /\\ action.request.choiceId = entry.choiceId',
    '      /\\ action.request.choicePolicy = entry.choicePolicy',
    '      /\\ action.request.removeStepIds = entry.removeStepIds',
    '',
    'ReadOnlyTransportSupported(entry) ==',
    '  \\/ /\\ entry.method = "GET"',
    '       /\\ entry.path \\in {"builder", "capability", "row-definition-choices"}',
    '  \\/ /\\ entry.method = "POST"',
    '       /\\ entry.path \\in {"suggestions", "semantic-inventory", "configured-column-context",',
    '          "construction-capabilities", "construction-inputs", "construction-choices", "preview"}',
    '',
    'TransportRequestEventIndices ==',
    '  {ObservedTransportLedger[index].requestEventIndex : index \\in 1..Len(ObservedTransportLedger)}',
    '',
    'TransportSettlementEventIndices ==',
    '  {ObservedTransportLedger[index].settlementEventIndex : index \\in 1..Len(ObservedTransportLedger)}',
    '',
    'TransportEventIndices == TransportRequestEventIndices \\cup TransportSettlementEventIndices',
    '',
    'TransportCaptureValid(entry, index) ==',
    '  /\\ entry.transportIndex = index',
    '  /\\ entry.capturePhase \\in {"SETUP", "FORMAL"}',
    '  /\\ entry.setupMutation \\in BOOLEAN',
    '  /\\ entry.requestEventIndex > 0',
    '  /\\ entry.settlement \\in {"response", "request-failed"}',
    '  /\\ entry.settlementEventIndex > entry.requestEventIndex',
    '  /\\ IF entry.settlement = "response"',
    '       THEN /\\ entry.responseOwnerIndex = index',
    '            /\\ entry.responseStatus > 0',
    '       ELSE /\\ entry.responseOwnerIndex = 0',
    '            /\\ entry.responseStatus = 0',
    '  /\\ IF index = 1',
    '       THEN TRUE',
    '       ELSE ObservedTransportLedger[index - 1].requestEventIndex < entry.requestEventIndex',
    '',
    'SetupMutationRows ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].capturePhase = "SETUP"',
    '    /\\ ObservedTransportLedger[index].setupMutation}',
    '',
    'SetupMutationsMatchFormalEntry ==',
    '  IF Cardinality(SetupMutationRows) = 0',
    '  THEN TRUE',
    '  ELSE \\E index \\in SetupMutationRows:',
    '    /\\ \\A other \\in SetupMutationRows: other <= index',
    '    /\\ ObservedTransportLedger[index].responseDraftVersion = ObservedFormalEntry.draftVersion',
    '    /\\ ObservedTransportLedger[index].responseDraftDigest = ObservedFormalEntry.draftDigest',
    '',
    'FormalEntryBoundaryValid ==',
    '  /\\ ObservedFormalEntry.afterTransportIndex \\in 0..Len(ObservedTransportLedger)',
    '  /\\ ObservedFormalEntry.afterEventIndex \\in 0..(2 * Len(ObservedTransportLedger))',
    '  /\\ ObservedFormalEntry.outputId = OutputId',
    '  /\\ ObservedFormalEntry.snapshotToken = ObservedActions[GroupPreviewSubmittedIndex].request.snapshotToken',
    '  /\\ ObservedFormalEntry.draftVersion = RootDraftVersion',
    '  /\\ ObservedFormalEntry.draftDigest = RootDraftDigest',
    '  /\\ ObservedFormalEntry.workspace = ObservedInitialWorkspace',
    '  /\\ SetupMutationsMatchFormalEntry',
    '  /\\ {index \\in 1..Len(ObservedTransportLedger) :',
    '       ObservedTransportLedger[index].capturePhase = "SETUP"}',
    '       = 1..ObservedFormalEntry.afterTransportIndex',
    '  /\\ {index \\in 1..Len(ObservedTransportLedger) :',
    '       ObservedTransportLedger[index].capturePhase = "FORMAL"}',
    '       = (ObservedFormalEntry.afterTransportIndex + 1)..Len(ObservedTransportLedger)',
    '  /\\ \\A index \\in 1..ObservedFormalEntry.afterTransportIndex:',
    '       ObservedTransportLedger[index].settlementEventIndex <= ObservedFormalEntry.afterEventIndex',
    '  /\\ \\A index \\in (ObservedFormalEntry.afterTransportIndex + 1)..Len(ObservedTransportLedger):',
    '       ObservedTransportLedger[index].requestEventIndex > ObservedFormalEntry.afterEventIndex',
    '',
    'CommandSavedCASMatches(entry) ==',
    '  \\E actionIndex \\in 1..Len(ObservedActions):',
    '    LET action == ObservedActions[actionIndex] IN',
    '      /\\ action.response.present',
    '      /\\ action.response.requestObjectId = entry.requestCaptureId',
    '      /\\ action.response.status \\in 200..299',
    '      /\\ action.hasDraftVersion',
    '      /\\ action.hasDraftDigest',
    '      /\\ action.draftVersion = entry.savedCASAfterDraftVersion',
    '      /\\ action.draftDigest = entry.savedCASAfterDraftDigest',
    '',
    'CommandAfterDraftVersion(entry) ==',
    '  IF entry.classification = "setup"',
    '  THEN entry.responseDraftVersion',
    '  ELSE entry.savedCASAfterDraftVersion',
    '',
    'CommandAfterDraftDigest(entry) ==',
    '  IF entry.classification = "setup"',
    '  THEN entry.responseDraftDigest',
    '  ELSE entry.savedCASAfterDraftDigest',
    '',
    'CommandChangesOwner(entry, ownerDraftVersion, ownerDraftDigest) ==',
    '  /\\ entry.classification \\in {"modeled", "setup"}',
    '  /\\ entry.path = "commands"',
    '  /\\ entry.settlement = "response"',
    '  /\\ entry.responseStatus \\in 200..299',
    '  /\\ (IF entry.classification = "setup"',
    '       THEN /\\ entry.setupMutation',
    '            /\\ entry.responseCommandId = entry.commandId',
    '       ELSE CommandSavedCASMatches(entry))',
    '  /\\ (CommandAfterDraftVersion(entry) # ownerDraftVersion',
    '    \\/ CommandAfterDraftDigest(entry) # ownerDraftDigest)',
    '',
    'OwnerChangeRows(ownerScope, ownerDraftVersion, ownerDraftDigest, afterEvent, beforeEvent) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].ownerScope = ownerScope',
    '    /\\ ObservedTransportLedger[index].settlementEventIndex > afterEvent',
    '    /\\ ObservedTransportLedger[index].settlementEventIndex < beforeEvent',
    '    /\\ CommandChangesOwner(ObservedTransportLedger[index], ownerDraftVersion, ownerDraftDigest)}',
    '',
    'OwnerChangedBefore(ownerScope, ownerDraftVersion, ownerDraftDigest, afterEvent, beforeEvent) ==',
    '  Cardinality(OwnerChangeRows(ownerScope, ownerDraftVersion, ownerDraftDigest, afterEvent, beforeEvent)) > 0',
    '',
    'InitialReceiptCurrent(preview) ==',
    '  /\\ ObservedInitialPreview.receiptId = preview.receiptId',
    '  /\\ ObservedInitialPreview.outputId = preview.outputId',
    '  /\\ ~OwnerChangedBefore(preview.ownerScope, ObservedInitialDraftVersion,',
    '       ObservedInitialDraftDigest, 0, preview.settlementEventIndex)',
    '',
    'CompatibleReceiptProducerRows(preview) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].classification = "reconcile"',
    '    /\\ ObservedTransportLedger[index].settlement = "response"',
    '    /\\ ObservedTransportLedger[index].responseStatus \\in 200..299',
    '    /\\ ObservedTransportLedger[index].settlementEventIndex < preview.requestEventIndex',
    '    /\\ ObservedTransportLedger[index].ownerScope = preview.ownerScope',
    '    /\\ preview.receiptId \\in SequenceSet(ObservedTransportLedger[index].responseReceiptIds)',
    '    /\\ preview.outputId \\in SequenceSet(ObservedTransportLedger[index].responseOutputIds)',
    '    /\\ ~OwnerChangedBefore(preview.ownerScope,',
    '         ObservedTransportLedger[index].expectedDraftVersion,',
    '         ObservedTransportLedger[index].expectedDraftDigest,',
    '         ObservedTransportLedger[index].requestEventIndex,',
    '         preview.settlementEventIndex)}',
    '',
    'CompatibleReceiptProducers(preview) ==',
    '  (IF InitialReceiptCurrent(preview) THEN {0} ELSE {})',
    '  \\cup {ObservedTransportLedger[index].transportIndex :',
    '    index \\in CompatibleReceiptProducerRows(preview)}',
    '',
    'PreviewTransportValid(entry) ==',
    '  /\\ (Len(entry.currentReceiptProducerTransportIndices) > 0 \\/ entry.hasInitialCurrentReceiptProducer)',
    '  /\\ ((IF entry.hasInitialCurrentReceiptProducer THEN {0} ELSE {})',
    '       \\cup SequenceSet(entry.currentReceiptProducerTransportIndices))',
    '       = CompatibleReceiptProducers(entry)',
    '  /\\ entry.previewDisposition =',
    '       (IF entry.settlement = "request-failed" THEN "request-failed"',
    '        ELSE IF entry.responseStatus \\in 200..299 THEN "successful-response"',
    '        ELSE "http-error")',
    '  /\\ (entry.previewDisposition = "successful-response" =>',
    '       /\\ entry.responseOwnerIndex = entry.transportIndex',
    '       /\\ entry.receiptId \\in SequenceSet(entry.responseReceiptIds)',
    '       /\\ entry.outputId \\in SequenceSet(entry.responseOutputIds))',
    '',
    'ReadOnlyTransportRowValid(entry) ==',
    '  /\\ ReadOnlyTransportSupported(entry)',
    '  /\\ ~entry.mutatesDraft',
    '  /\\ ~entry.setupMutation',
    '  /\\ TransportCaptureValid(entry, entry.transportIndex)',
    '  /\\ IF entry.path = "preview"',
    '       THEN PreviewTransportValid(entry)',
    '       ELSE entry.readOnlyReason # "Absent"',
    '',
    'TransportRowValid(entry) ==',
    '  IF entry.classification = "setup"',
    '  THEN /\\ entry.capturePhase = "SETUP"',
    '       /\\ entry.method = "POST"',
    '       /\\ entry.path = "commands"',
    '       /\\ entry.setupMutation',
    '       /\\ entry.mutatesDraft',
    '       /\\ entry.actionLabel = "Absent"',
    '       /\\ entry.actionEvent = "Absent"',
    '       /\\ TransportCaptureValid(entry, entry.transportIndex)',
    '       /\\ entry.settlement = "response"',
    '       /\\ entry.responseStatus \\in 200..299',
    '       /\\ entry.commandId # "Absent"',
    '       /\\ entry.responseCommandId = entry.commandId',
    '       /\\ entry.responseDraftVersion > 0',
    '       /\\ entry.responseDraftDigest # "Absent"',
    '  ELSE IF entry.classification = "modeled"',
    '  THEN /\\ entry.method = "POST"',
    '       /\\ entry.path \\in {"construction-proposals", "construction-choice-proposals", "commands"}',
    '       /\\ entry.capturePhase = "FORMAL"',
    '       /\\ ~entry.setupMutation',
    '       /\\ entry.mutatesDraft = (entry.path = "commands")',
    '       /\\ TransportActionMatches(entry)',
    '       /\\ TransportCaptureValid(entry, entry.transportIndex)',
    '       /\\ ((entry.path = "commands" /\\ entry.settlement = "response"',
    '             /\\ entry.responseStatus \\in 200..299)',
    '            => /\\ entry.savedCASAfterDraftVersion > 0',
    '                /\\ entry.savedCASAfterDraftDigest # "Absent"',
    '                /\\ entry.responseOwnerIndex = entry.transportIndex',
    '                /\\ CommandSavedCASMatches(entry))',
    '  ELSE IF entry.classification = "read-only"',
    '    THEN ReadOnlyTransportRowValid(entry)',
    '  ELSE IF entry.classification = "reconcile"',
    '    THEN /\\ entry.method = "POST"',
    '         /\\ entry.path = "reconcile"',
    '         /\\ ~entry.mutatesDraft',
    '         /\\ ~entry.setupMutation',
    '         /\\ TransportCaptureValid(entry, entry.transportIndex)',
    '         /\\ entry.ownerRequestId = entry.requestId',
    '         /\\ entry.ownerGeneration > 0',
    '         /\\ entry.ownerAttempt > 0',
    '         /\\ entry.reconcileSnapshotToken = entry.snapshotToken',
    '         /\\ entry.reconcileDraftVersion = entry.expectedDraftVersion',
    '         /\\ entry.reconcileDraftDigest = entry.expectedDraftDigest',
    '         /\\ IF entry.settlement = "request-failed"',
    '              THEN /\\ ~entry.ownerInvalidationObserved',
    '                   /\\ entry.reconcileDisposition = "request-failed"',
    '              ELSE IF entry.responseStatus = 200',
    '                THEN /\\ entry.responseOwnerIndex = entry.transportIndex',
    '                     /\\ entry.responseReceiptId # "Absent"',
    '                     /\\ entry.responseSnapshotToken = entry.snapshotToken',
    '                     /\\ Len(entry.responseOutputIds) > 0',
    '                     /\\ (entry.reconcileOutputId # "Absent" =>',
    '                          entry.reconcileOutputId \\in SequenceSet(entry.responseOutputIds))',
    '                     /\\ entry.reconcileDisposition =',
    '                          IF entry.ownerInvalidationObserved',
    '                          THEN "invalidated-by-captured-owner-event"',
    '                          ELSE "no-captured-owner-invalidation"',
    '                ELSE /\\ ~entry.ownerInvalidationObserved',
    '                     /\\ entry.reconcileDisposition = "rejected"',
    '  ELSE FALSE',
    '',
    'CompatibleProposalReceiptRows(entry) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].classification = "modeled"',
    '    /\\ ObservedTransportLedger[index].path',
    '      \\in {"construction-proposals", "construction-choice-proposals"}',
    '    /\\ ObservedTransportLedger[index].ownerScope = entry.ownerScope',
    '    /\\ ObservedTransportLedger[index].settlement = "response"',
    '    /\\ ObservedTransportLedger[index].responseStatus = 200',
    '    /\\ ObservedTransportLedger[index].settlementEventIndex < entry.settlementEventIndex',
    '    /\\ entry.responseReceiptId',
    '      \\in SequenceSet(ObservedTransportLedger[index].responseReceiptIds)',
    '    /\\ ObservedTransportLedger[index].responseSnapshotToken = entry.responseSnapshotToken',
    '    /\\ entry.intentDigest # "Absent"',
    '    /\\ ObservedTransportLedger[index].candidateWorkspaceDigest = entry.intentDigest',
    '    /\\ (entry.reconcileOutputId = "Absent"',
    '      \\/ entry.reconcileOutputId',
    '        \\in SequenceSet(ObservedTransportLedger[index].responseOutputIds))}',
    '',
    'CompatibleProposalReceipts(entry) ==',
    '  {ObservedTransportLedger[index].transportIndex : index \\in CompatibleProposalReceiptRows(entry)}',
    '',
    'CompatiblePreviewConsumerRows(entry) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].classification = "read-only"',
    '    /\\ ObservedTransportLedger[index].path = "preview"',
    '    /\\ ObservedTransportLedger[index].previewDisposition = "successful-response"',
    '    /\\ ObservedTransportLedger[index].settlement = "response"',
    '    /\\ ObservedTransportLedger[index].responseStatus \\in 200..299',
    '    /\\ ObservedTransportLedger[index].responseOwnerIndex',
    '      = ObservedTransportLedger[index].transportIndex',
    '    /\\ ObservedTransportLedger[index].ownerScope = entry.ownerScope',
    '    /\\ ObservedTransportLedger[index].requestEventIndex > entry.settlementEventIndex',
    '    /\\ ObservedTransportLedger[index].receiptId = entry.responseReceiptId',
    '    /\\ (entry.reconcileOutputId = "Absent"',
    '      \\/ ObservedTransportLedger[index].outputId = entry.reconcileOutputId)',
    '    /\\ entry.responseReceiptId',
    '      \\in SequenceSet(ObservedTransportLedger[index].responseReceiptIds)',
    '    /\\ ObservedTransportLedger[index].outputId',
    '      \\in SequenceSet(ObservedTransportLedger[index].responseOutputIds)',
    '    /\\ ObservedTransportLedger[index].outputId',
    '      \\in SequenceSet(entry.responseOutputIds)',
    '    /\\ ~OwnerChangedBefore(entry.ownerScope, entry.reconcileDraftVersion,',
    '         entry.reconcileDraftDigest, entry.requestEventIndex,',
    '         ObservedTransportLedger[index].settlementEventIndex)}',
    '',
    'CompatiblePreviewConsumers(entry) ==',
    '  {ObservedTransportLedger[index].transportIndex : index \\in CompatiblePreviewConsumerRows(entry)}',
    '',
    'CompatibleInvalidationRows(entry) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].ownerScope = entry.ownerScope',
    '    /\\ ObservedTransportLedger[index].requestEventIndex > entry.requestEventIndex',
    '    /\\ ObservedTransportLedger[index].requestEventIndex < entry.settlementEventIndex',
    '    /\\ ((/\\ ObservedTransportLedger[index].classification \\in {"modeled", "setup"}',
    '          /\\ ObservedTransportLedger[index].path = "commands"',
    '          /\\ ObservedTransportLedger[index].settlementEventIndex < entry.settlementEventIndex',
    '          /\\ CommandChangesOwner(ObservedTransportLedger[index],',
    '               entry.reconcileDraftVersion, entry.reconcileDraftDigest))',
    '      \\/ (/\\ ObservedTransportLedger[index].classification = "reconcile"',
    '          /\\ ObservedTransportLedger[index].ownerGeneration > entry.ownerGeneration))}',
    '',
    'CompatibleInvalidations(entry) ==',
    '  {ObservedTransportLedger[index].transportIndex : index \\in CompatibleInvalidationRows(entry)}',
    '',
    'ReconcileOwnerChangeRows(entry) ==',
    '  {index \\in 1..Len(ObservedTransportLedger) :',
    '    /\\ ObservedTransportLedger[index].ownerScope = entry.ownerScope',
    '    /\\ ObservedTransportLedger[index].settlementEventIndex > entry.requestEventIndex',
    '    /\\ CommandChangesOwner(ObservedTransportLedger[index],',
    '         entry.reconcileDraftVersion, entry.reconcileDraftDigest)}',
    '',
    'ReconcileOwnerChangeFormalRows(entry) ==',
    '  {index \\in ReconcileOwnerChangeRows(entry) :',
    '    ObservedTransportLedger[index].classification = "modeled"}',
    '',
    'ReconcileOwnerChangeSetupRows(entry) ==',
    '  {index \\in ReconcileOwnerChangeRows(entry) :',
    '    ObservedTransportLedger[index].classification = "setup"}',
    '',
    'ReconcileOwnerChangeCaptureIds(entry) ==',
    '  {ObservedTransportLedger[index].requestCaptureId : index \\in ReconcileOwnerChangeFormalRows(entry)}',
    '',
    'ReconcileOwnerChangeSetupCommandIds(entry) ==',
    '  {ObservedTransportLedger[index].commandId : index \\in ReconcileOwnerChangeSetupRows(entry)}',
    '',
    'ReconcileConsumptionValid(entry) ==',
    '  IF entry.settlement = "response" /\\ entry.responseStatus = 200',
    '  THEN /\\ entry.ownerInvalidationObserved = (Cardinality(CompatibleInvalidations(entry)) > 0)',
    '       /\\ SequenceSet(entry.reconcileOwnerChangeCaptureIds) = ReconcileOwnerChangeCaptureIds(entry)',
    '       /\\ Cardinality(SequenceSet(entry.reconcileOwnerChangeCaptureIds)) = Len(entry.reconcileOwnerChangeCaptureIds)',
    '       /\\ SequenceSet(entry.reconcileOwnerChangeSetupCommandIds) = ReconcileOwnerChangeSetupCommandIds(entry)',
    '       /\\ Cardinality(SequenceSet(entry.reconcileOwnerChangeSetupCommandIds)) = Len(entry.reconcileOwnerChangeSetupCommandIds)',
    '       /\\ SequenceSet(entry.compatibleProposalTransportIndices) = CompatibleProposalReceipts(entry)',
    '       /\\ Cardinality(SequenceSet(entry.compatibleProposalTransportIndices)) = Len(entry.compatibleProposalTransportIndices)',
    '       /\\ SequenceSet(entry.successfulPreviewConsumerTransportIndices) = CompatiblePreviewConsumers(entry)',
    '       /\\ Cardinality(SequenceSet(entry.successfulPreviewConsumerTransportIndices)) = Len(entry.successfulPreviewConsumerTransportIndices)',
    '       /\\ SequenceSet(entry.invalidatedByTransportIndices) = CompatibleInvalidations(entry)',
    '       /\\ Cardinality(SequenceSet(entry.invalidatedByTransportIndices)) = Len(entry.invalidatedByTransportIndices)',
    '  ELSE /\\ Len(entry.compatibleProposalTransportIndices) = 0',
    '       /\\ Len(entry.successfulPreviewConsumerTransportIndices) = 0',
    '       /\\ Len(entry.invalidatedByTransportIndices) = 0',
    '       /\\ Len(entry.reconcileOwnerChangeCaptureIds) = 0',
    '       /\\ Len(entry.reconcileOwnerChangeSetupCommandIds) = 0',
    '',
    'ObservedTransportLedgerInvariant ==',
    '  /\\ Len(ObservedTransportLedger) > 0',
    '  /\\ LedgerTransportIndices = 1..Len(ObservedTransportLedger)',
    '  /\\ Cardinality(TransportRequestEventIndices) = Len(ObservedTransportLedger)',
    '  /\\ Cardinality(TransportSettlementEventIndices) = Len(ObservedTransportLedger)',
    '  /\\ TransportEventIndices = 1..(2 * Len(ObservedTransportLedger))',
    '  /\\ LedgerModeledIndices = ActionTransportIndices',
    '  /\\ FormalEntryBoundaryValid',
    '  /\\ Cardinality(ActionTransportIndices) =',
    '       Cardinality({index \\in 1..Len(ObservedActions) :',
    '         ObservedActions[index].event \\in ModeledRequestEvents})',
    '  /\\ \\A index \\in 1..Len(ObservedTransportLedger):',
    '       /\\ TransportRowValid(ObservedTransportLedger[index])',
    '       /\\ (ObservedTransportLedger[index].classification = "reconcile" =>',
    '            ReconcileConsumptionValid(ObservedTransportLedger[index]))',
    '',
    'ObservedFormalEntryBoundary == FormalEntryBoundaryValid',
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
    '  /\ ObservationMatches(traceIndex + 1, model, model\')',
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
    'INVARIANT ObservedTransportLedgerInvariant',
    'INVARIANT ObservedFormalEntryBoundary',
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
