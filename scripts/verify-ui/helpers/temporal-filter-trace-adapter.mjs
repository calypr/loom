import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { BASIC_TEMPORAL_FILTER_CASES } from './basic-temporal-filter-oracle.mjs';

const REQUIRED_LABELS = Object.freeze([
  'ExplorerCreated',
  'SourceTableCreated',
  'TemporalColumnApplied',
  'FilterProposed(create)',
  'FilterCancelled',
  'Reload',
  'FilterProposed(create)',
  'FilterApplied(create)',
  'Reload',
  'FilterEditOpened',
  'FilterProposed(edit)',
  'FilterApplied(edit)',
  'Reload',
  'FilterProposed(remove)',
  'FilterRemoved',
  'Reload',
]);

const canonicalJSON = value => Array.isArray(value)
  ? `[${value.map(canonicalJSON).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const workspaceDigest = documents => createHash('sha256').update(canonicalJSON(documents)).digest('hex');
const constructionSteps = document => document?.construction?.steps ?? [];
const filterSteps = steps => steps.filter(step => step.operation?.kind === 'FILTER');
const findFilterStep = steps => filterSteps(steps)[0] ?? null;
const findFilter = step => step?.operation?.filter ?? null;
const filterValue = filter => Array.isArray(filter?.values) && filter.values.length === 1 ? filter.values[0] : null;
const commandList = body => Array.isArray(body?.commands) ? body.commands : [];
const commandTypes = body => commandList(body).map(command => command.type);
const hasCommand = (body, type) => commandTypes(body).includes(type);
const requestPathIs = (event, suffix) => event.method === 'POST' && event.path.endsWith(suffix);
const assertJSONEqual = (actual, expected, message) => assert.deepEqual(actual, expected, message);

const readAt = (capture, index) => {
  if (index === null || index === undefined) return null;
  assert(Number.isInteger(index) && index >= 0 && index < capture.builderReads.length,
    `Builder read index ${String(index)} must reference a captured read.`);
  const read = capture.builderReads[index];
  assert(Array.isArray(read.workspaceDocuments), `Builder read ${read.readId} must retain complete workspace documents.`);
  const exactOutput = capture.outputId
    ? read.workspaceDocuments.find(document => document.output?.id === capture.outputId)
    : undefined;
  const resourceDocument = exactOutput ?? read.workspaceDocuments.find(document => document.rootResourceType === capture.resourceType) ?? null;
  const columns = resourceDocument?.columns ?? [];
  const temporalColumn = columns.find(column => column.source?.kind === 'field' &&
    column.source.field?.path === capture.fieldPath) ?? null;
  return {
    readId: read.readId,
    requestId: read.requestId ?? null,
    status: read.status,
    mountEpoch: read.mountEpoch,
    catalogGeneration: read.catalogGeneration ?? null,
    snapshotToken: read.snapshotToken ?? null,
    draftVersion: read.draftVersion ?? null,
    draftDigest: read.draftDigest ?? null,
    workspaceDocuments: read.workspaceDocuments,
    workspaceDigest: workspaceDigest(read.workspaceDocuments),
    outputId: resourceDocument?.output?.id ?? null,
    columnId: temporalColumn?.columnId ?? temporalColumn?.id ?? null,
    columns,
    steps: constructionSteps(resourceDocument),
  };
};

const rawRequestFor = (capture, captureId) => {
  if (captureId === null || captureId === undefined) return null;
  const matches = capture.requestEvents.filter(event => event.browserRequestId === captureId);
  assert.equal(matches.length, 1, `Request capture ID ${captureId} must resolve to one observer event.`);
  return matches[0];
};

const requestRecord = event => event === null ? null : ({
  captureId: event.browserRequestId,
  requestId: event.requestId ?? null,
  method: event.method,
  path: event.path,
  status: event.status,
  isMutation: event.isMutation,
  body: event.request,
  responseBody: event.response,
});

const domAt = (capture, index) => {
  if (index === null || index === undefined) return null;
  assert(Number.isInteger(index) && index >= 0 && index < capture.domObservations.length,
    `DOM observation index ${String(index)} must reference a captured observation.`);
  const observation = capture.domObservations[index];
  return {
    capturedAtMonotonicMs: observation.capturedAtMonotonicMs,
    capturedAtEpochMs: observation.capturedAtEpochMs,
    mountEpoch: observation.mountEpoch,
    proposalStatus: observation.proposalStatus ?? null,
    proposalId: observation.proposalId ?? null,
    receiptId: observation.receiptId ?? null,
    outputId: observation.outputId ?? null,
    columnId: observation.columnId ?? null,
    inputType: observation.inputType ?? null,
    inputValue: observation.inputValue ?? null,
    operator: observation.operator ?? null,
    rowIds: observation.rowIds ?? [],
  };
};

const filterFromConstruction = construction => {
  const filters = filterSteps(constructionSteps({ construction }));
  assert(filters.length <= 1, 'A temporal construction proposal may contain at most one FILTER step.');
  const step = filters[0] ?? null;
  return { step, filter: findFilter(step) };
};

const proposalAssociation = (event, requests) => {
  const proposalId = commandList(event.request?.body)
    .find(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL')?.proposalId;
  assert(typeof proposalId === 'string' && proposalId, `${event.action} command must identify its current proposal.`);
  const matches = requests.filter(request => request.response?.proposalId === proposalId &&
    request.path.endsWith('/construction-proposals'));
  assert.equal(matches.length, 1, `${event.action} must bind its exact prior construction proposal response.`);
  return matches[0];
};

const buildModelEvents = capture => capture.steps.map(step => {
  const rawRequest = rawRequestFor(capture, step.requestCaptureId);
  const before = readAt(capture, step.beforeReadIndex);
  const after = readAt(capture, step.afterReadIndex);
  const ui = domAt(capture, step.domObservationIndex);
  const event = {
    action: step.label,
    atMonotonicMs: step.atMonotonicMs,
    atEpochMs: step.atEpochMs,
    mountEpoch: step.mountEpoch,
    request: requestRecord(rawRequest),
    before,
    after,
    ui,
    filter: null,
    requestCaptureId: step.requestCaptureId ?? null,
    proposalCaptureIds: step.proposalCaptureIds ?? [],
    reconcileCaptureIds: step.reconcileCaptureIds ?? [],
  };
  return event;
});

const assertCaseIdentity = capture => {
  const contract = BASIC_TEMPORAL_FILTER_CASES[capture.caseName];
  assert(contract, `Only exact DATE and DATE_TIME cases are accepted, got ${String(capture.caseName)}.`);
  assert.equal(capture.resourceType, contract.resourceType);
  assert.equal(capture.fieldPath, contract.fieldPath);
  assert.equal(capture.schemaVersion, 1);
  assert.equal(capture.scenario, 'basic-temporal-filter');
  assert.equal(capture.project?.startsWith('loom_dev_verify_'), true, 'Trace must retain its run-owned fixture project.');
  assert(capture.generation, 'Trace must retain its fixture generation.');
  assert.match(capture.sourceCommit ?? '', /^[a-f0-9]{40}$/, 'Trace must retain the exact source commit.');
  assert.match(capture.sourceFingerprint?.sha256 ?? '', /^[a-f0-9]{64}$/,
    'Trace must retain the watched source fingerprint.');
  assert.match(capture.fixtureSha256 ?? '', /^[a-f0-9]{64}$/);
  assert(capture.fixturePath, 'Trace must retain the independent fixture path.');
  assert(capture.outputId && capture.temporalColumnId, 'Trace must retain output and temporal column identities.');
  assert.deepEqual(capture.rows, contract.rows, 'Trace row literals must remain the independent temporal oracle.');
  assert.deepEqual(capture.ids, contract.rows.map(row => row.id));
  return contract;
};

const assertCaptureShape = capture => {
  assert(Array.isArray(capture.requestEvents), 'Capture must include observer request events.');
  assert(Array.isArray(capture.builderReads), 'Capture must include fresh Builder reads.');
  assert(Array.isArray(capture.domObservations), 'Capture must include read-only DOM observations.');
  assert(Array.isArray(capture.steps), 'Capture must include ordered semantic steps.');
  assertJSONEqual(capture.steps.map(step => step.label), REQUIRED_LABELS, 'Capture must retain the complete ordered temporal lifecycle.');
  const captureIDs = capture.requestEvents.map(request => request.browserRequestId);
  assert(captureIDs.every(id => typeof id === 'string' && id.length > 0), 'Every request must retain its actual browserRequestId.');
  assert.equal(new Set(captureIDs).size, captureIDs.length, 'Observer browserRequestIds must be unique.');
  for (const request of capture.requestEvents) {
    assert(typeof request.method === 'string' && typeof request.path === 'string');
    assert(Number.isFinite(request.atMonotonicMs) && Number.isFinite(request.atEpochMs));
    assert(Number.isInteger(request.mountEpoch));
    assert(Number.isFinite(request.startedAt));
    assert(request.responseReceivedAt === null || Number.isFinite(request.responseReceivedAt));
    assert(request.completedAt === null || Number.isFinite(request.completedAt));
    if (request.status === null) {
      assert(request.failure?.canceled === true && request.readCancellationDiagnostic === true,
        `Request ${request.browserRequestId} without a response must retain an actual canceled-request diagnostic.`);
      assert(request.responseReceivedAt === null && request.responseMountEpoch === null);
      assert(Number.isInteger(request.failureMountEpoch));
      assert.equal(request.isMutation, false);
      assert(requestPathIs(request, '/reconcile') || requestPathIs(request, '/preview') ||
        requestPathIs(request, '/construction-proposals') || requestPathIs(request, '/construction-choice-proposals'),
      `Canceled ${request.method} ${request.path} has no temporal-filter read-only cancellation contract.`);
    } else {
      assert(Number.isInteger(request.status) && request.status >= 100 && request.status <= 599,
        `Observed response ${request.browserRequestId} must retain its actual HTTP status code.`);
      assert(Number.isInteger(request.responseMountEpoch));
      assert.equal(request.failureMountEpoch, null);
    }
    if (request.requestId !== undefined) assert(typeof request.requestId === 'string' && request.requestId.length > 0);
  }
  const readIds = capture.builderReads.map(read => read.readId);
  assert(readIds.every(id => typeof id === 'string' && id.length > 0), 'Each Builder GET must have a unique capture ID.');
  assert.equal(new Set(readIds).size, readIds.length, 'Builder read capture IDs must be unique.');
  for (const read of capture.builderReads) {
    assert(Number.isFinite(read.requestStartedAtMonotonicMs));
    assert(Number.isFinite(read.requestStartedAtEpochMs));
    assert(Number.isInteger(read.requestMountEpoch));
    assert(Number.isFinite(read.capturedAtMonotonicMs));
    assert(Number.isFinite(read.capturedAtEpochMs));
    assert(Number.isInteger(read.mountEpoch));
    assert(typeof read.snapshotToken === 'string' && read.snapshotToken.length > 0,
      `Builder read ${read.readId} must retain its catalog snapshot token.`);
    assert(read.capturedAtMonotonicMs >= read.requestStartedAtMonotonicMs,
      `Builder read ${read.readId} response time must follow its GET request start.`);
  }
  for (const step of capture.steps) {
    assert(Number.isFinite(step.atMonotonicMs) && Number.isFinite(step.atEpochMs));
    assert(Number.isInteger(step.mountEpoch));
    assert(Array.isArray(step.reconcileCaptureIds), `${step.label} must retain its reconcile capture ID list.`);
    for (const index of [step.beforeReadIndex, step.afterReadIndex]) {
      assert(index === null || (Number.isInteger(index) && index >= 0 && index < capture.builderReads.length));
    }
    assert(step.domObservationIndex === null || (Number.isInteger(step.domObservationIndex) &&
      step.domObservationIndex >= 0 && step.domObservationIndex < capture.domObservations.length));
    if (step.requestCaptureId) {
      const request = rawRequestFor(capture, step.requestCaptureId);
      assert.equal(step.atMonotonicMs, request.atMonotonicMs, `${step.label} must use its request-event monotonic timestamp.`);
      assert.equal(step.atEpochMs, request.atEpochMs, `${step.label} must use its request-event epoch timestamp.`);
      assert.equal(step.mountEpoch, request.mountEpoch, `${step.label} must use the request-event mount epoch.`);
    } else if (step.domObservationIndex !== null) {
      const observation = capture.domObservations[step.domObservationIndex];
      assert.equal(step.atMonotonicMs, observation.capturedAtMonotonicMs,
        `${step.label} must use the timestamp of its captured DOM observation.`);
      assert.equal(step.atEpochMs, observation.capturedAtEpochMs,
        `${step.label} must use the epoch time of its captured DOM observation.`);
      assert.equal(step.mountEpoch, observation.mountEpoch,
        `${step.label} must use the mount epoch of its captured DOM observation.`);
    }
  }
  assert(Array.isArray(capture.lateReconcileCaptureIds),
    'Capture must retain reconcile requests outside all semantic-step request windows.');
  const linkedReconcileIDs = [
    ...capture.steps.flatMap(step => step.reconcileCaptureIds),
    ...capture.lateReconcileCaptureIds,
  ];
  assert.equal(new Set(linkedReconcileIDs).size, linkedReconcileIDs.length,
    'Each reconcile request must appear in one step window or the explicit late-request ledger.');
  const actualReconcileIDs = capture.requestEvents.filter(request => requestPathIs(request, '/reconcile'))
    .map(request => request.browserRequestId);
  assertJSONEqual([...linkedReconcileIDs].sort(), [...actualReconcileIDs].sort(),
    'Every actual reconcile request must remain linked or explicitly late.');
  if (capture.lateReconcileCaptureIds.length > 0) {
    assert(Number.isInteger(capture.finalDOMObservationIndex) &&
      capture.finalDOMObservationIndex >= 0 && capture.finalDOMObservationIndex < capture.domObservations.length,
    'Late reconcile requests must retain a final actual DOM observation.');
  }
};

const assertRequestBindings = (capture, events) => {
  const requestById = new Map(capture.requestEvents.map(request => [request.browserRequestId, request]));
  const mutationLabels = new Map([
    ['ExplorerCreated', event => event.request?.method === 'POST' && event.request.path.endsWith('/explorers')],
    ['SourceTableCreated', event => hasCommand(event.request?.body, 'CREATE_TABLE')],
    ['TemporalColumnApplied', event => hasCommand(event.request?.body, 'APPLY_CONSTRUCTION_CHOICE')],
    ['FilterApplied(create)', event => hasCommand(event.request?.body, 'APPLY_CONSTRUCTION_PROPOSAL')],
    ['FilterApplied(edit)', event => hasCommand(event.request?.body, 'APPLY_CONSTRUCTION_PROPOSAL')],
    ['FilterRemoved', event => hasCommand(event.request?.body, 'APPLY_CONSTRUCTION_PROPOSAL')],
  ]);
  const consumedMutations = [];
  for (const [label, matches] of mutationLabels) {
    const event = events.find(candidate => candidate.action === label);
    assert(event?.request, `${label} must bind its actual mutating request.`);
    const raw = requestById.get(event.request.captureId);
    assert(raw?.isMutation, `${label} must bind an observer-classified mutation.`);
    assert(raw.status >= 200 && raw.status < 300, `${label} mutation must have a successful captured response.`);
    assert(matches(event), `${label} request body must carry its expected native operation.`);
    consumedMutations.push(raw.browserRequestId);
  }
  const mutations = capture.requestEvents.filter(request => request.isMutation).map(request => request.browserRequestId);
  assertJSONEqual(consumedMutations.sort(), mutations.sort(), 'Every observer-classified write must be consumed exactly once.');

  const proposalCaptureIDs = capture.steps.flatMap(step => step.proposalCaptureIds ?? []);
  assert.equal(new Set(proposalCaptureIDs).size, proposalCaptureIDs.length,
    'Each read-only proposal response must be linked from one semantic step.');
  const proposals = capture.requestEvents.filter(request => request.method === 'POST' &&
    (request.path.endsWith('/construction-proposals') || request.path.endsWith('/construction-choice-proposals')));
  assertJSONEqual([...proposalCaptureIDs].sort(), proposals.map(request => request.browserRequestId).sort(),
    'Every actual read-only construction proposal must be linked to its semantic step.');
  for (const captureId of proposalCaptureIDs) assert(requestById.has(captureId), `Proposal ${captureId} must be an actual observer request.`);
};

const assertReconcileBindings = (capture, events) => {
  const requestById = new Map(capture.requestEvents.map(request => [request.browserRequestId, request]));
  const linkedIDs = capture.steps.flatMap(step => step.reconcileCaptureIds ?? []);
  const allLinkedIDs = [...linkedIDs, ...(capture.lateReconcileCaptureIds ?? [])];
  assert.equal(new Set(allLinkedIDs).size, allLinkedIDs.length,
    'Each observed reconcile request must link to one semantic step or the explicit late-request ledger.');
  const reconciles = capture.requestEvents.filter(request => request.method === 'POST' && request.path.endsWith('/reconcile'));
  assertJSONEqual([...allLinkedIDs].sort(), reconciles.map(request => request.browserRequestId).sort(),
    'Every actual POST /reconcile request must link to its semantic step or late-request ledger.');
  for (const step of capture.steps) {
    for (const captureId of step.reconcileCaptureIds ?? []) {
      const request = requestById.get(captureId);
      assert(request && requestPathIs(request, '/reconcile'), `${step.label} reconcile ID must be an actual POST /reconcile observer request.`);
      assert.equal(request.isMutation, false, 'POST /reconcile remains read-only.');
      assert.equal(request.mountEpoch, step.mountEpoch,
        `${step.label} reconcile must belong to the semantic step mount epoch.`);
    }
  }

  for (const reload of events.filter(event => event.action === 'Reload')) {
    assert(reload.before && reload.after && reload.ui, 'Reload must bind Builder state and DOM evidence.');
    const reloadRequestIDs = new Set([...reload.reconcileCaptureIds, ...(capture.lateReconcileCaptureIds ?? [])]);
    const linked = reconciles.filter(request => reloadRequestIDs.has(request.browserRequestId));
    const receipts = linked.filter(request => request.status >= 200 && request.status < 300 &&
      request.response?.receiptId && request.response.snapshotToken === reload.after.snapshotToken &&
      request.request?.snapshotToken === reload.after.snapshotToken &&
      request.request?.draftVersion === reload.after.draftVersion &&
      request.request?.draftDigest === reload.after.draftDigest &&
      (!request.request.outputId || request.request.outputId === capture.outputId) &&
      request.response.outputs?.some(output => output.outputId === capture.outputId));
    assert(receipts.length > 0,
      'Reload must retain a successful reconcile receipt for its actual Builder snapshot and output.');
    assert.equal(reload.ui.outputId, capture.outputId,
      'Reload DOM must retain the actual construction-preview output ID.');
    assert(receipts.some(request => request.response.receiptId === reload.ui.receiptId),
      'Reload DOM receipt must match the actual reconcile response receipt.');
  }
};

const assertProposalMatches = (capture, contract, event, intent) => {
  const raw = rawRequestFor(capture, event.request.captureId);
  assert(raw && requestPathIs(raw, '/construction-proposals'));
  assert.equal(raw.status, 200, `${event.action} proposal must be a successful captured response.`);
  assert.equal(raw.request?.outputId, capture.outputId);
  assert.equal(raw.response?.outputId, capture.outputId);
  assert.equal(raw.response?.preview?.outputId, capture.outputId);
  assert.equal(raw.response?.preview?.receiptId, raw.response?.proposalId,
    `${event.action} response must bind its preview receipt to its proposal ID.`);
  if (event.before) {
    assert.equal(raw.request.expectedDraftVersion, event.before.draftVersion,
      `${event.action} proposal must use the actual current draft version.`);
    assert.equal(raw.request.expectedDraftDigest, event.before.draftDigest,
      `${event.action} proposal must use the actual current draft digest.`);
    assert.equal(raw.response.draftVersion, event.before.draftVersion);
    assert.equal(raw.response.draftDigest, event.before.draftDigest);
  }
  const requestCandidate = raw.request?.candidateConstruction;
  const responseCandidate = raw.response?.candidateConstruction;
  const requestFilter = filterFromConstruction(requestCandidate);
  const responseFilter = filterFromConstruction(responseCandidate);
  const expected = intent === 'edit' ? contract.edited : contract.initial;
  const ui = event.ui;
  assert(ui, `${event.action} must retain its ready DOM observation.`);
  assert.equal(String(ui.proposalStatus).toLowerCase(), 'ready');
  assert.equal(ui.proposalId, raw.response.proposalId);
  assert.equal(ui.receiptId, raw.response.preview?.receiptId);
  assert.equal(ui.outputId, capture.outputId);
  if (intent === 'remove') {
    assert.equal(requestFilter.filter, null, 'Removal candidate must contain no FILTER step.');
    assert.equal(responseFilter.filter, null, 'Removal response candidate must contain no FILTER step.');
    assertJSONEqual([...ui.rowIds].sort(), [...capture.ids].sort(),
      'Removal proposal DOM must render every literal source row.');
  } else {
    assert(requestFilter.filter && responseFilter.filter, `${event.action} must include request and response FILTER predicates.`);
    assert.equal(raw.request.outputId, capture.outputId);
    assert.equal(raw.response.outputId, capture.outputId);
    assert.equal(requestFilter.filter.columnId, capture.temporalColumnId);
    assert.equal(responseFilter.filter.columnId, capture.temporalColumnId);
    assert.equal(requestFilter.filter.operator, expected.operator);
    assert.equal(responseFilter.filter.operator, expected.operator);
    assert.deepEqual(filterValue(requestFilter.filter), expected.requestValue ?? expected.savedValue);
    assert.deepEqual(filterValue(responseFilter.filter), expected.savedValue);
  }
  if (intent !== 'remove') {
    assert.equal(ui.columnId, capture.temporalColumnId);
    assert.equal(ui.inputType, contract.inputType);
    if (expected.inputValue !== undefined) assert.equal(ui.inputValue, expected.inputValue);
    else assert.match(ui.inputValue ?? '', /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/,
      'datetime-local DOM value must preserve a native local date/time literal.');
    assert.equal(ui.operator, expected.operator);
    assertJSONEqual([...ui.rowIds].sort(), [...expected.ids].sort(), `${event.action} rendered rows must match the literal oracle.`);
  }
  assert(raw.response.proposalId, `${event.action} must preserve the actual proposal ID.`);
  assert.equal(event.requestCaptureId, raw.browserRequestId);
  assert((event.proposalCaptureIds ?? []).includes(raw.browserRequestId), `${event.action} must list its actual proposal response capture ID.`);
};

const assertCancelAndReload = (capture, events, contract) => {
  const cancel = events.find(event => event.action === 'FilterCancelled');
  assert(cancel.before && cancel.after, 'FilterCancelled must bind its preproposal and postcancel Builder reads.');
  assertJSONEqual(cancel.after.workspaceDocuments, cancel.before.workspaceDocuments,
    'Cancel must preserve the complete saved workspace documents.');
  assert.equal(cancel.after.snapshotToken, cancel.before.snapshotToken);
  assert.equal(cancel.after.draftVersion, cancel.before.draftVersion);
  assert.equal(cancel.after.draftDigest, cancel.before.draftDigest);
  assert.notEqual(cancel.before.readId, cancel.after.readId, 'Cancel must use a fresh postcancel Builder read.');
  assertJSONEqual([...cancel.ui.rowIds].sort(), [...capture.ids].sort(), 'Cancel DOM read must show every source row.');

  const reloadEvents = events.filter(event => event.action === 'Reload');
  const persistedSource = [
    cancel.after,
    events.find(event => event.action === 'FilterApplied(create)')?.after,
    events.find(event => event.action === 'FilterApplied(edit)')?.after,
    events.find(event => event.action === 'FilterRemoved')?.after,
  ];
  const expectedRows = [capture.ids, contract.initial.ids, contract.edited.ids, capture.ids];
  assert.equal(reloadEvents.length, 4);
  for (const [index, event] of reloadEvents.entries()) {
    assert(event.before && event.after && event.ui, 'Reload must bind saved state and a fresh DOM observation.');
    assert.notEqual(event.before.mountEpoch, event.after.mountEpoch,
      `Reload ${index + 1} Builder read must belong to a new mount epoch (${event.before.mountEpoch} to ${event.after.mountEpoch}).`);
    assert.equal(event.after.mountEpoch, event.ui.mountEpoch, 'Reload DOM read must share its fresh Builder mount epoch.');
    assert.notEqual(event.before.readId, event.after.readId, 'Reload must obtain a distinct postnavigation Builder GET.');
    assertJSONEqual(event.after.workspaceDocuments, persistedSource[index].workspaceDocuments,
      'Reload must preserve the workspace saved by the preceding lifecycle state.');
    assert.equal(event.after.snapshotToken, persistedSource[index].snapshotToken);
    assert.equal(event.after.draftVersion, persistedSource[index].draftVersion);
    assert.equal(event.after.draftDigest, persistedSource[index].draftDigest);
    assert.equal(event.after.outputId, capture.outputId);
    assertJSONEqual([...event.ui.rowIds].sort(), [...expectedRows[index]].sort(),
      'Reload DOM rows must match the independently specified saved filter state.');
  }
};

const assertFilterTransitions = (capture, events, contract) => {
  const createApply = events.find(event => event.action === 'FilterApplied(create)');
  const editApply = events.find(event => event.action === 'FilterApplied(edit)');
  const removal = events.find(event => event.action === 'FilterRemoved');
  for (const event of [createApply, editApply, removal]) {
    assert(event.before && event.after, `${event.action} must bind distinct before and after Builder reads.`);
    assert.notEqual(event.before.readId, event.after.readId, `${event.action} must use a new GET after settlement.`);
    const command = commandList(event.request?.body).find(item => item.type === 'APPLY_CONSTRUCTION_PROPOSAL');
    assert(command?.proposalId, `${event.action} must consume a specific ready construction proposal.`);
    assert.equal(event.request.body.expectedDraftVersion, event.before.draftVersion,
      `${event.action} must send the exact pre-mutation draft version.`);
    assert.equal(event.request.body.expectedDraftDigest, event.before.draftDigest,
      `${event.action} must send the exact pre-mutation draft digest.`);
    const proposal = proposalAssociation(event, capture.requestEvents);
    const rawRequest = rawRequestFor(capture, event.request.captureId);
    assert(rawRequest.status >= 200 && rawRequest.status < 300,
      `${event.action} command must settle as a successful observed mutation.`);
    assert.equal(proposal.response.proposalId, command.proposalId,
      `${event.action} must apply the exact current proposal that its request captured.`);
  }

  const beforeCreate = createApply.before.steps;
  const afterCreate = createApply.after.steps;
  assert.equal(filterSteps(beforeCreate).length, 0, 'Filter creation must begin without a saved FILTER step.');
  assert.equal(filterSteps(afterCreate).length, 1, 'Filter creation must add exactly one FILTER step.');
  assert.equal(afterCreate.length, beforeCreate.length + 1, 'Filter creation must add exactly one construction step.');
  const created = findFilterStep(afterCreate);
  assert(created?.id, 'Created FILTER must retain a stable saved step ID.');
  assertJSONEqual(afterCreate.filter(step => step.id !== created.id), beforeCreate,
    'Filter creation must preserve all preexisting construction steps.');

  const beforeEdit = editApply.before.steps;
  const afterEdit = editApply.after.steps;
  const priorFilters = filterSteps(beforeEdit);
  const editedFilters = filterSteps(afterEdit);
  assert.equal(priorFilters.length, 1);
  assert.equal(editedFilters.length, 1);
  assert.equal(editedFilters[0].id, priorFilters[0].id, 'Edit must preserve the targeted FILTER step ID.');
  assertJSONEqual(afterEdit.map(step => step.id), beforeEdit.map(step => step.id),
    'Edit must preserve the complete construction step order and identities.');
  assertJSONEqual(afterEdit.filter(step => step.id !== priorFilters[0].id),
    beforeEdit.filter(step => step.id !== priorFilters[0].id), 'Edit must preserve every other step.');
  assertJSONEqual(editApply.after.columns, editApply.before.columns,
    'Edit must preserve every saved column and its identity.');
  const expectedEditedStep = structuredClone(priorFilters[0]);
  expectedEditedStep.operation.filter = structuredClone(editedFilters[0].operation.filter);
  assertJSONEqual(editedFilters[0], expectedEditedStep,
    'Edit may change only the targeted FILTER predicate.');
  assertJSONEqual(findFilter(editedFilters[0]), {
    ...findFilter(editedFilters[0]),
    columnId: capture.temporalColumnId,
    operator: contract.edited.operator,
    values: [contract.edited.savedValue],
  }, 'Edited saved FILTER must retain the exact typed predicate.');

  const beforeRemove = removal.before.steps;
  const afterRemove = removal.after.steps;
  const removable = filterSteps(beforeRemove);
  assert.equal(removable.length, 1, 'Removal must begin with exactly one saved FILTER step.');
  assert.equal(filterSteps(afterRemove).length, 0, 'Removal must remove the targeted FILTER step.');
  assert(removable[0].id, 'Removal must identify the targeted FILTER by stable step ID.');
  assertJSONEqual(afterRemove, beforeRemove.filter(step => step.id !== removable[0].id),
    'Removal must preserve every other construction step and its order.');
  assertJSONEqual(removal.after.columns, removal.before.columns,
    'Removing FILTER must preserve every saved column and its identity.');
  assertJSONEqual(removal.before.columns, createApply.after.columns,
    'Filter creation and removal must preserve the same source projection.');
  assertJSONEqual([...createApply.ui.rowIds].sort(), [...contract.initial.ids].sort(),
    'Applied creation must render the exact independent row IDs.');
  assertJSONEqual([...editApply.ui.rowIds].sort(), [...contract.edited.ids].sort(),
    'Applied edit must render the exact independent row IDs.');
  assertJSONEqual([...removal.ui.rowIds].sort(), [...capture.ids].sort(),
    'Applied removal must render the exact source row IDs.');
};

const attachFilterFields = (capture, events, contract) => {
  const proposalEvents = events.filter(event => event.action.startsWith('FilterProposed('));
  const proposalById = new Map();
  for (const event of proposalEvents) {
    const request = rawRequestFor(capture, event.requestCaptureId);
    proposalById.set(request.response?.proposalId, request);
    const intent = event.action.slice('FilterProposed('.length, -1);
    assertProposalMatches(capture, contract, event, intent);
    const requestFilter = filterFromConstruction(request.request?.candidateConstruction);
    const responseFilter = filterFromConstruction(request.response?.candidateConstruction);
    const previousFilter = findFilterStep(event.before?.steps ?? []);
    const stepId = intent === 'create'
      ? requestFilter.step?.id ?? responseFilter.step?.id ?? null
      : previousFilter?.id ?? requestFilter.step?.id ?? null;
    event.filter = {
      intent,
      proposalId: request.response?.proposalId ?? null,
      receiptId: request.response?.preview?.receiptId ?? null,
      stepId,
      outputId: capture.outputId,
      columnId: requestFilter.filter?.columnId ?? capture.temporalColumnId,
      valueKind: requestFilter.filter ? filterValue(requestFilter.filter)?.kind ?? null : findFilter(previousFilter)?.values?.[0]?.kind ?? null,
      operator: requestFilter.filter?.operator ?? findFilter(previousFilter)?.operator ?? null,
      requestValue: filterValue(requestFilter.filter),
      responseValue: filterValue(responseFilter.filter),
      savedValue: null,
    };
  }

  for (const event of events.filter(candidate => candidate.action === 'FilterApplied(create)' ||
    candidate.action === 'FilterApplied(edit)' || candidate.action === 'FilterRemoved')) {
    const proposal = proposalAssociation(event, capture.requestEvents);
    const proposalId = proposal.response.proposalId;
    assert(proposalById.has(proposalId), `${event.action} proposal must have a matching FilterProposed semantic event.`);
    const proposedFilter = filterFromConstruction(proposal.request?.candidateConstruction);
    const saved = findFilterStep(event.after?.steps ?? []);
    const prior = findFilterStep(event.before?.steps ?? []);
    const intent = event.action === 'FilterRemoved' ? 'remove' : event.action.endsWith('(edit)') ? 'edit' : 'create';
    event.filter = {
      intent,
      proposalId: proposal.response?.proposalId ?? null,
      receiptId: proposal.response?.preview?.receiptId ?? null,
      stepId: intent === 'create' ? saved?.id ?? null : prior?.id ?? null,
      outputId: capture.outputId,
      columnId: proposedFilter.filter?.columnId ?? findFilter(prior)?.columnId ?? capture.temporalColumnId,
      valueKind: proposedFilter.filter ? filterValue(proposedFilter.filter)?.kind ?? null : findFilter(prior)?.values?.[0]?.kind ?? null,
      operator: proposedFilter.filter?.operator ?? findFilter(prior)?.operator ?? null,
      requestValue: filterValue(proposedFilter.filter),
      responseValue: filterValue(filterFromConstruction(proposal.response?.candidateConstruction).filter),
      savedValue: filterValue(findFilter(saved)),
    };
    assert.equal(event.request.body.expectedDraftVersion, event.before.draftVersion);
    assert.equal(event.request.body.expectedDraftDigest, event.before.draftDigest);
    if (intent === 'create' || intent === 'edit') {
      const expected = intent === 'create' ? contract.initial : contract.edited;
      assert.equal(event.filter.columnId, capture.temporalColumnId);
      assert.equal(event.filter.operator, expected.operator);
      assert.deepEqual(event.filter.requestValue, expected.requestValue ?? expected.savedValue);
      assert.deepEqual(event.filter.responseValue, expected.savedValue);
      assert.deepEqual(event.filter.savedValue, expected.savedValue);
    } else {
      assert.equal(event.filter.savedValue, null, 'Removed FILTER must not claim a persisted predicate.');
    }
  }
};

export const adaptTemporalFilterJourney = capture => {
  assert(capture && typeof capture === 'object', 'Temporal-filter adapter requires a raw journey capture.');
  const contract = assertCaseIdentity(capture);
  assertCaptureShape(capture);
  const events = buildModelEvents(capture);
  assertRequestBindings(capture, events);
  assertReconcileBindings(capture, events);
  attachFilterFields(capture, events, contract);
  assertCancelAndReload(capture, events, contract);
  assertFilterTransitions(capture, events, contract);

  const explorerEvent = events.find(event => event.action === 'ExplorerCreated');
  const temporalColumnEvent = events.find(event => event.action === 'TemporalColumnApplied');
  assert(explorerEvent.after, 'ExplorerCreated must bind its fresh post-create Builder read.');
  assert(temporalColumnEvent.before && temporalColumnEvent.after,
    'TemporalColumnApplied must bind its actual pre- and post-apply Builder reads.');
  assert.equal(temporalColumnEvent.after.columnId, capture.temporalColumnId,
    'TemporalColumnApplied must save the exact temporal column identity.');
  for (const captureId of temporalColumnEvent.proposalCaptureIds) {
    const choiceProposal = rawRequestFor(capture, captureId);
    assert(requestPathIs(choiceProposal, '/construction-choice-proposals'));
    assert.equal(choiceProposal.status, 200);
    assert.equal(choiceProposal.request?.outputId, capture.outputId);
    assert.equal(choiceProposal.response?.outputId, capture.outputId);
    assert.equal(choiceProposal.response?.previewStatus, 'READY');
    assert(choiceProposal.response?.preview?.receiptId,
      'Each captured construction-choice response must retain its actual preview receipt.');
  }

  return {
    name: `basic-temporal-filter:${capture.caseName}`,
    model: 'TemporalFilterJourney',
    initialObservation: {
      readId: explorerEvent.after.readId,
      workspaceDocuments: explorerEvent.after.workspaceDocuments,
      draftVersion: explorerEvent.after.draftVersion,
      draftDigest: explorerEvent.after.draftDigest,
      snapshotToken: explorerEvent.after.snapshotToken,
      mountEpoch: explorerEvent.after.mountEpoch,
    },
    actions: events.map(({ requestCaptureId, proposalCaptureIds, ...event }) => event),
    unmodeledEvents: [],
    temporalFilterLifecycle: capture,
  };
};

export { REQUIRED_LABELS as TEMPORAL_FILTER_ACTION_LABELS };
