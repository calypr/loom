import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
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

const MUTATION_ACTIONS = new Set([
  'ExplorerCreated',
  'SourceTableCreated',
  'TemporalColumnApplied',
  'FilterApplied(create)',
  'FilterApplied(edit)',
  'FilterRemoved',
]);

const PROPOSAL_SUFFIXES = Object.freeze([
  '/construction-proposals',
  '/construction-choice-proposals',
]);

const NO = 'No';
const MISSING_REQUEST = Object.freeze({
  captureId: NO,
  requestId: NO,
  isMutation: false,
  method: NO,
  path: NO,
  status: NO,
  body: NO,
  responseBody: NO,
});
const MISSING_SNAPSHOT = Object.freeze({
  readId: NO,
  requestId: NO,
  mountEpoch: 0,
  status: NO,
  catalogGeneration: NO,
  draftVersion: 0,
  draftDigest: NO,
  workspaceDocuments: [],
  workspaceDigest: NO,
  outputId: NO,
  columnId: NO,
  columns: [],
  steps: [],
});
const MISSING_UI = Object.freeze({
  capturedAtMonotonicMs: 0,
  capturedAtEpochMs: 0,
  mountEpoch: 0,
  proposalStatus: NO,
  proposalId: NO,
  receiptId: NO,
  outputId: NO,
  columnId: NO,
  inputType: NO,
  inputValue: NO,
  operator: NO,
  rowIds: [],
});
const MISSING_FILTER = Object.freeze({
  intent: NO,
  stepId: NO,
  outputId: NO,
  columnId: NO,
  valueKind: NO,
  operator: NO,
  requestValue: NO,
  responseValue: NO,
  savedValue: NO,
  proposalId: NO,
  receiptId: NO,
});

const canonicalJSON = value => Array.isArray(value)
  ? `[${value.map(canonicalJSON).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const opaqueJSON = value => Buffer.from(canonicalJSON(value), 'utf8').toString('base64');
const sha256 = value => createHash('sha256').update(canonicalJSON(value)).digest('hex');

const tlaString = value => {
  const text = String(value);
  assert(!/[\u0000-\u001f\u007f]/u.test(text), 'TLA string values must not contain control characters.');
  return `"${text.replaceAll('\\', '\\\\').replaceAll('"', '\\"')}"`;
};

const tlaValue = value => {
  if (value === null || value === undefined) return tlaString(NO);
  if (typeof value === 'string') return tlaString(value);
  if (typeof value === 'boolean') return value ? 'TRUE' : 'FALSE';
  if (typeof value === 'number') {
    assert(Number.isSafeInteger(value) && Math.abs(value) <= 2_147_483_647,
      `TLA input number ${value} must fit TLC's signed 32-bit integer range.`);
    return String(value);
  }
  if (Array.isArray(value)) return `<<${value.map(tlaValue).join(', ')}>>`;
  assert(value && typeof value === 'object', `Unsupported TLA input value ${String(value)}.`);
  const fields = Object.entries(value).map(([key, fieldValue]) => {
    assert(/^[A-Za-z][A-Za-z0-9_]*$/u.test(key), `TLA record key ${JSON.stringify(key)} is not a simple identifier.`);
    return `${key} |-> ${tlaValue(fieldValue)}`;
  });
  return `[${fields.join(', ')}]`;
};

const noIfAbsent = value => value === null || value === undefined ? NO : value;
const safeArray = value => Array.isArray(value) ? value : [];
const without = (value, excluded) => Object.fromEntries(Object.entries(value ?? {}).filter(([key]) => !excluded.has(key)));
const fieldId = value => value?.columnId ?? value?.id ?? NO;
const stepId = value => value?.id ?? value?.stepId ?? NO;
const filterOperation = step => step?.operation?.kind === 'FILTER' ? step.operation.filter : null;

const projectFilterValue = value => {
  assert(value && typeof value === 'object' && typeof value.kind === 'string',
    'Saved temporal filter values must retain a typed value kind.');
  if (value.kind === 'DATE') return { kind: value.kind, date: value.date };
  if (value.kind === 'DATE_TIME') return { kind: value.kind, dateTime: value.dateTime };
  throw new Error(`TLA input rejects unsupported temporal kind ${JSON.stringify(value.kind)}.`);
};

const projectFilter = filter => {
  assert(filter && typeof filter === 'object', 'FILTER steps must retain their predicate.');
  const values = safeArray(filter.values);
  return {
    columnId: noIfAbsent(filter.columnId),
    operator: noIfAbsent(filter.operator),
    values: values.map(projectFilterValue),
    evidence: opaqueJSON(without(filter, new Set(['columnId', 'operator', 'values']))),
  };
};

const projectOperation = operation => {
  assert(operation && typeof operation === 'object', 'Construction steps must retain their operation.');
  if (operation.kind === 'FILTER') {
    return { kind: 'FILTER', filter: projectFilter(operation.filter) };
  }
  return {
    kind: noIfAbsent(operation.kind),
    evidence: opaqueJSON(without(operation, new Set(['kind']))),
  };
};

const projectSteps = value => safeArray(value).map(step => ({
  id: stepId(step),
  operation: projectOperation(step?.operation),
  evidence: opaqueJSON(without(step, new Set(['id', 'stepId', 'operation']))),
}));

const projectColumns = value => safeArray(value).map(column => ({
  columnId: fieldId(column),
  evidence: opaqueJSON(without(column, new Set(['id', 'columnId']))),
}));

const projectSnapshot = snapshot => {
  if (snapshot === null || snapshot === undefined) return MISSING_SNAPSHOT;
  assert(Array.isArray(snapshot.workspaceDocuments), 'Builder snapshots must retain complete workspace documents.');
  assert.equal(snapshot.workspaceDigest, sha256(snapshot.workspaceDocuments),
    `${snapshot.readId} must carry the SHA-256 of its complete workspace documents.`);
  return {
    readId: snapshot.readId,
    requestId: noIfAbsent(snapshot.requestId),
    mountEpoch: snapshot.mountEpoch,
    status: snapshot.status,
    catalogGeneration: noIfAbsent(snapshot.catalogGeneration),
    draftVersion: noIfAbsent(snapshot.draftVersion),
    draftDigest: noIfAbsent(snapshot.draftDigest),
    workspaceDocuments: opaqueJSON(snapshot.workspaceDocuments),
    workspaceDigest: noIfAbsent(snapshot.workspaceDigest),
    outputId: noIfAbsent(snapshot.outputId),
    columnId: noIfAbsent(snapshot.columnId),
    columns: projectColumns(snapshot.columns),
    steps: projectSteps(snapshot.steps),
  };
};

const projectCommand = command => ({
  type: noIfAbsent(command?.type),
  outputId: noIfAbsent(command?.outputId),
  columnId: noIfAbsent(command?.columnId),
  proposalId: noIfAbsent(command?.proposalId),
  evidence: opaqueJSON(without(command, new Set(['type', 'outputId', 'columnId', 'proposalId']))),
});

const projectBody = body => {
  if (!body || typeof body !== 'object') return NO;
  const candidate = body.candidateConstruction;
  return {
    expectedDraftVersion: noIfAbsent(body.expectedDraftVersion),
    expectedDraftDigest: noIfAbsent(body.expectedDraftDigest),
    outputId: noIfAbsent(body.outputId),
    candidateConstruction: candidate && typeof candidate === 'object'
      ? { steps: projectSteps(candidate.steps), evidence: opaqueJSON(without(candidate, new Set(['steps']))) }
      : NO,
    commands: safeArray(body.commands).map(projectCommand),
    evidence: opaqueJSON(without(body, new Set([
      'expectedDraftVersion', 'expectedDraftDigest', 'outputId', 'candidateConstruction', 'commands',
    ]))),
  };
};

const projectResponseBody = body => {
  if (!body || typeof body !== 'object') return NO;
  const candidate = body.candidateConstruction;
  const preview = body.preview;
  return {
    explorerId: noIfAbsent(body.explorerId),
    proposalId: noIfAbsent(body.proposalId),
    outputId: noIfAbsent(body.outputId),
    draftVersion: noIfAbsent(body.draftVersion),
    draftDigest: noIfAbsent(body.draftDigest),
    accepted: noIfAbsent(body.accepted),
    candidateConstruction: candidate && typeof candidate === 'object'
      ? { steps: projectSteps(candidate.steps), evidence: opaqueJSON(without(candidate, new Set(['steps']))) }
      : NO,
    preview: preview && typeof preview === 'object'
      ? {
          receiptId: noIfAbsent(preview.receiptId),
          outputId: noIfAbsent(preview.outputId),
          evidence: opaqueJSON(without(preview, new Set(['receiptId', 'outputId']))),
        }
      : NO,
    evidence: opaqueJSON(without(body, new Set([
      'explorerId', 'proposalId', 'outputId', 'draftVersion', 'draftDigest',
      'accepted', 'candidateConstruction', 'preview',
    ]))),
  };
};

const projectRequest = request => {
  if (request === null || request === undefined) return MISSING_REQUEST;
  assert(typeof request.captureId === 'string' && request.captureId.length > 0,
    'Semantic requests must retain the observer capture ID.');
  assert(typeof request.isMutation === 'boolean', 'Semantic requests must retain observer mutation classification.');
  return {
    captureId: request.captureId,
    requestId: noIfAbsent(request.requestId),
    isMutation: request.isMutation,
    method: request.method,
    path: request.path,
    status: request.status,
    body: projectBody(request.body),
    responseBody: projectResponseBody(request.responseBody),
  };
};

const projectUI = (ui, timeOrigin) => {
  if (ui === null || ui === undefined) return MISSING_UI;
  return {
    capturedAtMonotonicMs: Math.round((ui.capturedAtMonotonicMs - timeOrigin.monotonicMs) * 1_000),
    capturedAtMonotonicMsExact: String(ui.capturedAtMonotonicMs),
    capturedAtEpochMs: ui.capturedAtEpochMs - timeOrigin.epochMs,
    capturedAtEpochMsExact: String(ui.capturedAtEpochMs),
    mountEpoch: ui.mountEpoch,
    proposalStatus: noIfAbsent(ui.proposalStatus),
    proposalId: noIfAbsent(ui.proposalId),
    receiptId: noIfAbsent(ui.receiptId),
    outputId: noIfAbsent(ui.outputId),
    columnId: noIfAbsent(ui.columnId),
    inputType: noIfAbsent(ui.inputType),
    inputValue: noIfAbsent(ui.inputValue),
    operator: noIfAbsent(ui.operator),
    rowIds: safeArray(ui.rowIds),
  };
};

const projectFilterEvidence = filter => {
  if (filter === null || filter === undefined) return MISSING_FILTER;
  return {
    intent: noIfAbsent(filter.intent),
    stepId: noIfAbsent(filter.stepId),
    outputId: noIfAbsent(filter.outputId),
    columnId: noIfAbsent(filter.columnId),
    valueKind: noIfAbsent(filter.valueKind),
    operator: noIfAbsent(filter.operator),
    requestValue: filter.requestValue === null ? NO : filter.requestValue,
    responseValue: filter.responseValue === null ? NO : filter.responseValue,
    savedValue: filter.savedValue === null ? NO : filter.savedValue,
    proposalId: noIfAbsent(filter.proposalId),
    receiptId: noIfAbsent(filter.receiptId),
  };
};

const projectModelEvent = (event, rawStep, timeOrigin) => ({
  action: event.action,
  atMonotonicMs: Math.round((event.atMonotonicMs - timeOrigin.monotonicMs) * 1_000),
  atMonotonicMsExact: String(event.atMonotonicMs),
  atEpochMs: event.atEpochMs - timeOrigin.epochMs,
  atEpochMsExact: String(event.atEpochMs),
  mountEpoch: event.mountEpoch,
  request: projectRequest(event.request),
  before: projectSnapshot(event.before),
  after: projectSnapshot(event.after),
  ui: projectUI(event.ui, timeOrigin),
  filter: projectFilterEvidence(event.filter),
  proposalCaptureIds: safeArray(rawStep.proposalCaptureIds),
});

const buildCase = (caseName, actions) => {
  const contract = BASIC_TEMPORAL_FILTER_CASES[caseName];
  const initial = contract.initial;
  const edited = contract.edited;
  const initialRequestValue = initial.requestValue ?? initial.savedValue;
  const editedRequestValue = edited.requestValue ?? edited.savedValue;
  const firstProposal = actions.find(event => event.action === 'FilterProposed(create)');
  assert(firstProposal?.ui?.inputValue, 'The first filter proposal must capture its native input value.');
  const inputValue = caseName === 'date' ? initial.inputValue : firstProposal.ui.inputValue;
  if (caseName === 'date-time') {
    assert.match(inputValue, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/u,
      'The datetime-local value must retain its browser-local wall-clock spelling.');
  }
  return {
    caseName,
    resourceType: contract.resourceType,
    fieldPath: contract.fieldPath,
    inputType: contract.inputType,
    inputValue,
    valueKind: contract.filterValueKind,
    initialOperator: initial.operator,
    editedOperator: edited.operator,
    initialRequestValue,
    initialResponseValue: initial.savedValue,
    initialSavedValue: initial.savedValue,
    editedRequestValue,
    editedResponseValue: edited.savedValue,
    editedSavedValue: edited.savedValue,
    sourceRowIds: contract.rows.map(row => row.id),
    initialRowIds: initial.ids,
    editedRowIds: edited.ids,
  };
};

const countBy = values => values.reduce((counts, value) => counts.set(value, (counts.get(value) ?? 0) + 1), new Map());
const expectedCounts = countBy(REQUIRED_LABELS);
const sortedCounts = counts => [...counts.entries()].sort(([left], [right]) => left.localeCompare(right));

export const validateTemporalFilterJourneyTrace = trace => {
  assert(trace && typeof trace === 'object', 'Temporal-filter compiler requires a canonical journey trace.');
  assert.equal(trace.model, 'TemporalFilterJourney', 'Canonical trace must select TemporalFilterJourney.');
  const match = /^basic-temporal-filter:(date|date-time)$/u.exec(trace.name ?? '');
  assert(match, `Temporal-filter compiler rejects unregistered trace name ${JSON.stringify(trace.name)}.`);
  const caseName = match[1];
  const profile = caseName === 'date' ? 'DATE' : 'DATE_TIME';
  const contract = BASIC_TEMPORAL_FILTER_CASES[caseName];
  assert(Array.isArray(trace.actions), 'Canonical temporal trace must retain an actions array.');
  assert.equal(trace.actions.length, REQUIRED_LABELS.length, 'Every semantic lifecycle action must reach TLA exactly once.');
  assert.deepEqual(sortedCounts(countBy(trace.actions.map(event => event.action))), sortedCounts(expectedCounts),
    'Canonical temporal trace must contain every required action label exactly once or as specified.');
  assert.deepEqual(trace.unmodeledEvents ?? [], [], 'Temporal TLA replay rejects unmodeled semantic events.');

  const lifecycle = trace.temporalFilterLifecycle;
  assert(lifecycle && lifecycle.schemaVersion === 1 && lifecycle.scenario === 'basic-temporal-filter',
    'Canonical trace must retain the versioned raw temporal-filter lifecycle.');
  assert.equal(lifecycle.caseName, caseName);
  assert.equal(lifecycle.resourceType, contract.resourceType);
  assert.equal(lifecycle.fieldPath, contract.fieldPath);
  assert.match(lifecycle.sourceCommit ?? '', /^[a-f0-9]{40}$/u,
    'Raw temporal evidence must retain its exact source commit.');
  assert.match(lifecycle.sourceFingerprint?.sha256 ?? '', /^[a-f0-9]{64}$/u,
    'Raw temporal evidence must retain its watched source fingerprint.');
  assert.match(lifecycle.fixtureSha256 ?? '', /^[a-f0-9]{64}$/u,
    'Raw temporal evidence must retain the exact fixture fingerprint.');
  assert(lifecycle.fixturePath && lifecycle.project?.startsWith('loom_dev_verify_') && lifecycle.generation,
    'Raw temporal evidence must retain the run-owned fixture project and generation.');
  assert(lifecycle.outputId && lifecycle.temporalColumnId,
    'Raw temporal evidence must retain its actual output and temporal-column IDs.');
  assert.deepEqual(lifecycle.rows, contract.rows,
    'Raw temporal rows must match the independent literal fixture oracle.');
  assert.deepEqual(lifecycle.ids, contract.rows.map(row => row.id),
    'Raw temporal row IDs must match the independent literal fixture oracle.');
  assert(Array.isArray(lifecycle.steps) && lifecycle.steps.length === trace.actions.length,
    'Each semantic action must map to exactly one raw capture step.');
  assert(Array.isArray(lifecycle.requestEvents), 'Raw observer request events must remain available.');
  assert(Array.isArray(lifecycle.builderReads) && Array.isArray(lifecycle.domObservations),
    'Raw Builder and DOM evidence must remain available beside the normalized trace.');

  const actionCounts = countBy(trace.actions.map(event => event.action));
  for (const [label, count] of expectedCounts) assert.equal(actionCounts.get(label), count);
  const rawRequests = lifecycle.requestEvents;
  for (const request of rawRequests) {
    assert(typeof request.isMutation === 'boolean', 'Observer request ledger must retain mutation classification.');
    assert(typeof request.method === 'string' && typeof request.path === 'string');
    assert(Number.isFinite(request.startedAt) && Number.isFinite(request.completedAt),
      `Observer request ${request.browserRequestId} must have one settled response.`);
    assert(Number.isFinite(request.status), `Observer request ${request.browserRequestId} must retain its response status.`);
    assert(Number.isFinite(request.atMonotonicMs) && Number.isFinite(request.atEpochMs));
    assert(Number.isInteger(request.mountEpoch));
  }
  const captureIds = rawRequests.map(request => request.browserRequestId);
  assert(captureIds.every(id => typeof id === 'string' && id.length > 0),
    'Every observed request must retain its actual browser request ID.');
  assert.equal(new Set(captureIds).size, captureIds.length, 'Observer request IDs must be unique.');
  const rawRequestById = new Map(rawRequests.map(request => [request.browserRequestId, request]));
  const actionRequestIds = [];
  const proposalLinks = [];
  for (let index = 0; index < trace.actions.length; index += 1) {
    const event = trace.actions[index];
    const rawStep = lifecycle.steps[index];
    assert.equal(rawStep.label, event.action, `Canonical action ${index} must map to its same-index raw step.`);
    assert(Number.isFinite(event.atMonotonicMs) && Number.isFinite(event.atEpochMs),
      `${event.action} must retain its observed timestamp.`);
    assert(Number.isInteger(event.mountEpoch), `${event.action} must retain its mount epoch.`);
    assert.equal(event.mountEpoch, rawStep.mountEpoch, `${event.action} must preserve the raw semantic-step mount epoch.`);
    assert(rawStep.requestCaptureId === null || rawStep.requestCaptureId === undefined ||
      typeof rawStep.requestCaptureId === 'string', `${event.action} request link must preserve its actual ID.`);
    const requestCaptureId = rawStep.requestCaptureId ?? null;
    if (requestCaptureId === null) {
      assert.equal(event.request, null, `${event.action} without a captured request must remain request-free.`);
    } else {
      const raw = rawRequestById.get(requestCaptureId);
      assert(raw, `${event.action} must reference one raw observed request.`);
      assert.equal(event.request?.captureId, requestCaptureId);
      assert.equal(event.request?.isMutation, raw.isMutation,
        `${event.action} must preserve the observer's mutation classification.`);
      assert.equal(event.request?.method, raw.method);
      assert.equal(event.request?.path, raw.path);
      assert.equal(event.request?.status, raw.status);
      assert.equal(event.atMonotonicMs, raw.atMonotonicMs);
      assert.equal(event.atEpochMs, raw.atEpochMs);
      assert.equal(event.mountEpoch, raw.mountEpoch);
      assert.deepEqual(event.request?.body, raw.request,
        `${event.action} model request must map to the actual captured request body.`);
      assert.deepEqual(event.request?.responseBody, raw.response,
        `${event.action} model response must map to the matching captured response body.`);
      assert.equal(Number.isFinite(raw.completedAt), true,
        `${event.action} request must have a settled matching response.`);
      actionRequestIds.push(requestCaptureId);
    }
    const linkedProposals = safeArray(rawStep.proposalCaptureIds);
    proposalLinks.push(...linkedProposals);
    for (const proposalId of linkedProposals) {
      const request = rawRequestById.get(proposalId);
      assert(request, `${event.action} proposal link must identify a captured observer request.`);
      assert.equal(request.method, 'POST');
      assert(PROPOSAL_SUFFIXES.some(suffix => request.path.endsWith(suffix)),
        `${event.action} proposal link must be a construction proposal request.`);
    }
    if (requestCaptureId !== null && event.action.startsWith('FilterProposed(')) {
      assert(linkedProposals.includes(requestCaptureId),
        `${event.action} primary request must also be linked as its proposal evidence.`);
    }
  }
  assert.equal(new Set(actionRequestIds).size, actionRequestIds.length,
    'Each semantic request must be consumed by one action only.');
  const expectedMutations = rawRequests.filter(request => request.isMutation).map(request => request.browserRequestId).sort();
  const consumedMutations = trace.actions.filter(event => event.request?.isMutation)
    .map(event => event.request.captureId).sort();
  assert.deepEqual(consumedMutations, expectedMutations,
    'Every observed mutation must be bound to exactly one semantic action.');
  const expectedProposals = rawRequests.filter(request => request.method === 'POST' &&
    PROPOSAL_SUFFIXES.some(suffix => request.path.endsWith(suffix)))
    .map(request => request.browserRequestId).sort();
  assert.deepEqual([...proposalLinks].sort(), expectedProposals,
    'Every observer construction proposal must be linked to exactly one semantic step.');

  const firstActionRead = trace.actions[0]?.after;
  assert(firstActionRead && trace.initialObservation,
    'Canonical trace must retain the first actual post-create Builder observation.');
  assert.equal(trace.initialObservation.readId, firstActionRead.readId);
  assert.equal(trace.initialObservation.mountEpoch, firstActionRead.mountEpoch);
  assert.equal(trace.initialObservation.draftVersion, firstActionRead.draftVersion);
  assert.equal(trace.initialObservation.draftDigest, firstActionRead.draftDigest);
  assert.deepEqual(trace.initialObservation.workspaceDocuments, firstActionRead.workspaceDocuments);

  const timeOrigin = {
    monotonicMs: Math.min(...trace.actions.map(event => event.atMonotonicMs)),
    epochMs: Math.min(...trace.actions.map(event => event.atEpochMs)),
  };
  const rawStepByActionIndex = lifecycle.steps;
  const modelEvents = trace.actions.map((event, index) => projectModelEvent(event, rawStepByActionIndex[index], timeOrigin));
  const observedRequests = rawRequests.map(request => ({
    captureId: request.browserRequestId,
    requestId: noIfAbsent(request.requestId),
    isMutation: Boolean(request.isMutation),
    method: request.method,
    path: request.path,
    status: noIfAbsent(request.status),
    requestEvidence: opaqueJSON(request.request),
    responseEvidence: opaqueJSON(request.response),
  }));
  const pureReadCaptureIds = rawRequests.filter(request => !request.isMutation &&
    !PROPOSAL_SUFFIXES.some(suffix => request.path.endsWith(suffix)))
    .map(request => request.browserRequestId);

  return {
    caseName,
    profile,
    caseValue: buildCase(caseName, trace.actions),
    modelEvents,
    observedRequests,
    eventCount: modelEvents.length,
    observedRequestCount: observedRequests.length,
    mutationCount: expectedMutations.length,
    proposalCount: expectedProposals.length,
    pureReadCaptureIds,
    timestampEncoding: 'relative integer ticks for TLC comparisons; exact captured timestamps remain beside each model event as strings and in raw lifecycle evidence',
  };
};

const invariantNames = Object.freeze([
  'TypeOK',
  'InvOnlyRegisteredProfiles',
  'InvOrderedTrace',
  'InvSemanticActionOrder',
  'InvAllWritesConsumed',
  'InvObservedMutationsConsumed',
  'InvProposalEvidenceLinked',
  'InvMountOwnsProposal',
  'InvMountedStateMatchesSaved',
  'InvStableColumnIdentity',
  'InvRenderMatchesSavedFilter',
  'InvProposalPreviewRowsMatch',
  'InvAllOtherStepsAndColumnsPreserved',
  'InvApplyUsesCurrentProposalAndCAS',
  'InvTypedProfileValues',
  'InvReloadRestoresPersisted',
  'InvEveryProposalEchoesOwnerAndPredicate',
  'InvPreviewRowsAreLiteral',
]);

const safeModuleName = (moduleName, fallback) => {
  const name = moduleName ?? fallback;
  assert(/^[A-Za-z][A-Za-z0-9_]*$/u.test(name), `Invalid generated TLA module name ${JSON.stringify(name)}.`);
  return name;
};

export const compileTemporalFilterJourneyTrace = (trace, { moduleName: requestedModuleName } = {}) => {
  const input = validateTemporalFilterJourneyTrace(trace);
  const defaultName = input.caseName === 'date'
    ? 'TemporalFilterJourneyDateTrace'
    : 'TemporalFilterJourneyDateTimeTrace';
  const moduleName = safeModuleName(requestedModuleName, defaultName);
  const moduleText = [
    `---------------- MODULE ${moduleName} ----------------`,
    'EXTENDS TemporalFilterJourney',
    `TraceCase == ${tlaValue(input.caseValue)}`,
    `TraceEvents == ${tlaValue(input.modelEvents)}`,
    `TraceObservedRequests == ${tlaValue(input.observedRequests)}`,
    '====',
    '',
  ].join('\n');
  const configText = [
    'SPECIFICATION Spec',
    `CONSTANT Profile = ${tlaValue(input.profile)}`,
    'CONSTANT Case <- TraceCase',
    'CONSTANT ModelEvents <- TraceEvents',
    'CONSTANT ObservedRequests <- TraceObservedRequests',
    ...invariantNames.map(name => `INVARIANT ${name}`),
    'PROPERTY EventuallyComplete',
    '',
  ].join('\n');
  return {
    moduleName,
    moduleText,
    configText,
    caseName: input.caseName,
    profile: input.profile,
    eventCount: input.eventCount,
    observedRequestCount: input.observedRequestCount,
    mutationCount: input.mutationCount,
    proposalCount: input.proposalCount,
    pureReadCaptureIds: input.pureReadCaptureIds,
    timestampEncoding: input.timestampEncoding,
  };
};

export { REQUIRED_LABELS as TEMPORAL_FILTER_TLA_ACTIONS };
