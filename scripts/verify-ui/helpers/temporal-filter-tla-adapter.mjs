import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { BASIC_TEMPORAL_FILTER_CASES } from './basic-temporal-filter-oracle.mjs';
import { READ_ONLY_POST_PATH_SUFFIXES } from './playwright-network-observer.mjs';

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
const SAFE_READ_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);

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
  snapshotToken: NO,
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
const isProposalRequest = request => request.method === 'POST' &&
  PROPOSAL_SUFFIXES.some(suffix => request.path.endsWith(suffix));
const isReconcileRequest = request => request.method === 'POST' && request.path.endsWith('/reconcile');
const isPreviewRequest = request => request.method === 'POST' && request.path.endsWith('/preview');

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

const projectSnapshot = (snapshot, initialMountEpoch) => {
  if (snapshot === null || snapshot === undefined) {
    return { ...MISSING_SNAPSHOT, mountEpoch: initialMountEpoch };
  }
  assert(Array.isArray(snapshot.workspaceDocuments), 'Builder snapshots must retain complete workspace documents.');
  assert.equal(snapshot.workspaceDigest, sha256(snapshot.workspaceDocuments),
    `${snapshot.readId} must carry the SHA-256 of its complete workspace documents.`);
  return {
    readId: snapshot.readId,
    requestId: noIfAbsent(snapshot.requestId),
    mountEpoch: snapshot.mountEpoch,
    status: snapshot.status,
    catalogGeneration: noIfAbsent(snapshot.catalogGeneration),
    snapshotToken: noIfAbsent(snapshot.snapshotToken),
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

const projectUI = (ui, timeOrigin, initialMountEpoch) => {
  if (ui === null || ui === undefined) return { ...MISSING_UI, mountEpoch: initialMountEpoch };
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

const projectReadTime = (lifecycle, index, timeOrigin) => {
  if (index === null || index === undefined) {
    return { requestStartedAt: NO, capturedAt: NO };
  }
  const read = lifecycle.builderReads[index];
  return {
    requestStartedAt: read.requestStartedAtEpochMs - timeOrigin.epochMs,
    capturedAt: read.capturedAtEpochMs - timeOrigin.epochMs,
  };
};

const projectModelEvent = (event, rawStep, lifecycle, timeOrigin, initialMountEpoch) => ({
  action: event.action,
  atMonotonicMs: Math.round((event.atMonotonicMs - timeOrigin.monotonicMs) * 1_000),
  atMonotonicMsExact: String(event.atMonotonicMs),
  atEpochMs: event.atEpochMs - timeOrigin.epochMs,
  atEpochMsExact: String(event.atEpochMs),
  mountEpoch: event.mountEpoch,
  beforeReadRequestStartedAt: projectReadTime(lifecycle, rawStep.beforeReadIndex, timeOrigin).requestStartedAt,
  beforeReadCapturedAt: projectReadTime(lifecycle, rawStep.beforeReadIndex, timeOrigin).capturedAt,
  afterReadRequestStartedAt: projectReadTime(lifecycle, rawStep.afterReadIndex, timeOrigin).requestStartedAt,
  afterReadCapturedAt: projectReadTime(lifecycle, rawStep.afterReadIndex, timeOrigin).capturedAt,
  request: projectRequest(event.request),
  before: projectSnapshot(event.before, initialMountEpoch),
  after: projectSnapshot(event.after, initialMountEpoch),
  ui: projectUI(event.ui, timeOrigin, initialMountEpoch),
  filter: projectFilterEvidence(event.filter),
  proposalCaptureIds: safeArray(rawStep.proposalCaptureIds),
  reconcileCaptureIds: safeArray(rawStep.reconcileCaptureIds),
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

const snapshotAt = (capture, index) => {
  if (index === null || index === undefined) return null;
  assert(Number.isInteger(index) && index >= 0 && index < capture.builderReads.length,
    `Raw Builder read index ${String(index)} must resolve inside the captured read ledger.`);
  const read = capture.builderReads[index];
  assert(Array.isArray(read.workspaceDocuments), `Raw Builder read ${read.readId} must retain complete documents.`);
  const exactOutput = capture.outputId
    ? read.workspaceDocuments.find(document => document.output?.id === capture.outputId)
    : undefined;
  const document = exactOutput ?? read.workspaceDocuments.find(item => item.rootResourceType === capture.resourceType) ?? null;
  const columns = document?.columns ?? [];
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
    workspaceDigest: sha256(read.workspaceDocuments),
    outputId: document?.output?.id ?? null,
    columnId: temporalColumn?.columnId ?? temporalColumn?.id ?? null,
    columns,
    steps: document?.construction?.steps ?? [],
  };
};

const domAt = (capture, index) => {
  if (index === null || index === undefined) return null;
  assert(Number.isInteger(index) && index >= 0 && index < capture.domObservations.length,
    `Raw DOM observation index ${String(index)} must resolve inside the captured DOM ledger.`);
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

const explorerIdFrom = capture => {
  const created = capture.requestEvents.find(event => event.method === 'POST' &&
    event.path.endsWith('/explorers') && typeof event.response?.explorerId === 'string');
  return created?.response.explorerId ?? null;
};

const supportedPureRead = (capture, request) => {
  const path = request.path;
  if (request.method === 'POST') {
    return READ_ONLY_POST_PATH_SUFFIXES.some(suffix => path.endsWith(suffix));
  }
  if (!SAFE_READ_METHODS.has(request.method)) return false;
  const projectRoot = `/api/v1/projects/${encodeURIComponent(capture.project)}`;
  const explorersRoot = `${projectRoot}/explorers`;
  if (path === explorersRoot) return true;
  const explorerId = explorerIdFrom(capture);
  if (explorerId) {
    if (path === `${explorersRoot}/${encodeURIComponent(explorerId)}`) return true;
    const authoringRoot = `${explorersRoot}/${encodeURIComponent(explorerId)}/authoring/v2`;
    if (path === `${authoringRoot}/builder`) return true;
    if (READ_ONLY_TEMPORAL_ENDPOINTS.some(suffix => path === `${authoringRoot}${suffix}`)) return true;
  }
  return /^\/api\/v1\/datasets\/[^/]+\/generations\/[^/]+$/u.test(path);
};

const READ_ONLY_TEMPORAL_ENDPOINTS = Object.freeze(READ_ONLY_POST_PATH_SUFFIXES
  .filter(suffix => suffix.startsWith('/authoring/v2/'))
  .map(suffix => suffix.slice('/authoring/v2'.length)));

const RECONCILE_STALE_CODES = new Set([
  'STALE_CATALOG_SNAPSHOT',
  'STALE_SNAPSHOT',
  'SNAPSHOT_STALE',
  'STALE_RECEIPT',
  'RECEIPT_STALE',
  'COMPILE_RECEIPT_NOT_FOUND',
  'RECEIPT_RECOMPILE_REQUIRED',
]);

const ownerMatchesRequest = (snapshot, request) => snapshot &&
  snapshot.snapshotToken === request.request?.snapshotToken &&
  snapshot.draftVersion === request.request?.draftVersion &&
  snapshot.draftDigest === request.request?.draftDigest &&
  snapshot.outputId === request.request?.outputId;

const responseOutputIds = request => safeArray(request.response?.outputs)
  .map(output => output?.outputId);

const reconcilePossibleProducers = (previewRequest, reconcileRequests, invalidationsByReconcileId) => reconcileRequests
  .filter(reconcile => Number.isFinite(reconcile.status) && reconcile.status >= 200 && reconcile.status < 300 &&
    reconcile.request?.snapshotToken && reconcile.request?.draftVersion !== undefined &&
    reconcile.request?.draftDigest && reconcile.request?.outputId &&
    reconcile.response?.snapshotToken === reconcile.request.snapshotToken &&
    reconcile.response?.receiptId === previewRequest.request?.receiptId &&
    previewRequest.request?.outputId === reconcile.request.outputId &&
    responseOutputIds(reconcile).includes(previewRequest.request.outputId) &&
    reconcile.responseReceivedAt <= previewRequest.startedAt &&
    reconcile.mountEpoch === reconcile.responseMountEpoch &&
    previewRequest.mountEpoch === reconcile.responseMountEpoch &&
    !(invalidationsByReconcileId.get(reconcile.browserRequestId) ?? [])
      .some(invalidation => invalidation.ownerObservedAt <= previewRequest.startedAt));

const deriveReconcileTransportEvidence = (trace, lifecycle, rawRequests, rawRequestById) => {
  const reconcileRequests = rawRequests.filter(isReconcileRequest);
  const previewRequests = rawRequests.filter(isPreviewRequest);
  const semanticRequestIds = new Set(lifecycle.steps.flatMap(step => [
    step.requestCaptureId,
    ...safeArray(step.proposalCaptureIds),
  ]).filter(value => typeof value === 'string'));
  const evidenceByCaptureId = new Map();
  const producerIdsByPreviewId = new Map();

  const sameOwner = (left, right) =>
    left.request?.snapshotToken === right.request?.snapshotToken &&
    left.request?.draftVersion === right.request?.draftVersion &&
    left.request?.draftDigest === right.request?.draftDigest &&
    left.request?.outputId === right.request?.outputId;
  const actionStartedBetween = (start, end) => rawRequests.some(candidate =>
    semanticRequestIds.has(candidate.browserRequestId) && candidate.startedAt > start && candidate.startedAt < end);
  const invalidationsByReconcileId = new Map(reconcileRequests.map(request => {
    const invalidations = trace.actions.flatMap((_, index) => {
      const rawStep = lifecycle.steps[index];
      const command = rawRequestById.get(rawStep.requestCaptureId);
      if (!command?.isMutation || !Number.isFinite(command.responseReceivedAt) ||
        command.responseReceivedAt <= request.startedAt ||
        command.responseReceivedAt >= request.responseReceivedAt ||
        rawStep.afterReadIndex === null || rawStep.afterReadIndex === undefined) return [];
      const after = snapshotAt(lifecycle, rawStep.afterReadIndex);
      const afterRead = lifecycle.builderReads[rawStep.afterReadIndex];
      return afterRead.capturedAtEpochMs <= request.responseReceivedAt && !ownerMatchesRequest(after, request)
        ? [{ captureId: command.browserRequestId, ownerObservedAt: afterRead.capturedAtEpochMs }]
        : [];
    });
    return [request.browserRequestId, invalidations];
  }));
  const hasFreshBuilderRead = (reconcile, next) => lifecycle.builderReads.some(read =>
    read.status === 200 && Number.isFinite(read.capturedAtEpochMs) &&
    read.capturedAtEpochMs > reconcile.responseReceivedAt &&
    read.requestStartedAtEpochMs > reconcile.responseReceivedAt &&
    read.capturedAtEpochMs <= next.startedAt &&
    read.requestMountEpoch === next.mountEpoch && read.mountEpoch === next.mountEpoch &&
    read.snapshotToken === next.request?.snapshotToken &&
    read.draftVersion === next.request?.draftVersion &&
    read.draftDigest === next.request?.draftDigest &&
    safeArray(read.workspaceDocuments).some(document => document?.output?.id === next.request?.outputId));

  for (const preview of previewRequests) {
    producerIdsByPreviewId.set(preview.browserRequestId,
      reconcilePossibleProducers(preview, reconcileRequests, invalidationsByReconcileId)
        .map(request => request.browserRequestId));
  }

  for (const request of reconcileRequests) {
    const consumerCaptureIds = previewRequests.filter(preview =>
      (producerIdsByPreviewId.get(preview.browserRequestId) ?? []).includes(request.browserRequestId))
      .map(preview => preview.browserRequestId);
    const invalidations = invalidationsByReconcileId.get(request.browserRequestId) ?? [];
    const domObservationIndexes = lifecycle.domObservations.flatMap((observation, index) =>
      request.status >= 200 && request.status < 300 &&
      observation.receiptId === request.response?.receiptId &&
      observation.outputId === request.request?.outputId &&
      observation.mountEpoch === request.mountEpoch &&
      observation.mountEpoch === request.responseMountEpoch &&
      observation.capturedAtEpochMs >= request.responseReceivedAt &&
      !invalidations.some(invalidation => invalidation.ownerObservedAt <= observation.capturedAtEpochMs)
        ? [index]
        : []);
    const invalidatedByCaptureIds = invalidations.map(invalidation => invalidation.captureId);
    const responseCode = request.response?.code ?? request.response?.error?.code ?? null;
    const responseRetryable = Boolean(request.response?.retryable ?? request.response?.error?.retryable);
    const retryFollowupCaptureIds = reconcileRequests.filter(next => {
      if (next.browserRequestId === request.browserRequestId ||
        !Number.isFinite(next.status) || next.startedAt <= request.responseReceivedAt ||
        next.mountEpoch !== request.mountEpoch ||
        actionStartedBetween(request.responseReceivedAt, next.startedAt)) return false;
      if (RECONCILE_STALE_CODES.has(responseCode)) return hasFreshBuilderRead(request, next);
      return responseRetryable && sameOwner(request, next);
    }).map(next => next.browserRequestId);
    let disposition;
    if (request.status === null) {
      disposition = 'cancelled';
    } else if (request.status >= 200 && request.status < 300) {
      if (request.mountEpoch !== request.responseMountEpoch) {
        disposition = 'cross-mount-ignored';
      } else if (invalidatedByCaptureIds.length > 0) {
        assert.equal(consumerCaptureIds.length, 0,
          `Owner-invalidated reconcile ${request.browserRequestId} cannot be a possible producer for a later preview.`);
        disposition = 'owner-invalidated';
      } else if (consumerCaptureIds.length > 0 || domObservationIndexes.length > 0) {
        disposition = 'compatible-unattributed';
      } else {
        throw new Error(`Successful reconcile ${request.browserRequestId} has no receipt-compatible preview, DOM observation, captured owner invalidation, or cross-mount boundary.`);
      }
    } else if (RECONCILE_STALE_CODES.has(responseCode) && retryFollowupCaptureIds.length > 0) {
      disposition = 'stale-retry';
    } else if (responseRetryable && retryFollowupCaptureIds.length > 0) {
      disposition = 'retry';
    } else {
      throw new Error(`Reconcile ${request.browserRequestId} failed without a recognized stale or retryable response and an observed follow-up.`);
    }
    evidenceByCaptureId.set(request.browserRequestId, {
      disposition,
      compatiblePreviewCaptureIds: consumerCaptureIds,
      domObservationIndexes,
      invalidatedByCaptureIds,
      retryFollowupCaptureIds,
    });
  }
  return { evidenceByCaptureId, producerIdsByPreviewId };
};

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
  assert(Array.isArray(lifecycle.lateReconcileCaptureIds),
    'Raw capture must retain reconcile IDs outside the semantic-step request windows.');
  assert(lifecycle.finalDOMObservationIndex === null ||
    (Number.isInteger(lifecycle.finalDOMObservationIndex) && lifecycle.finalDOMObservationIndex >= 0 &&
      lifecycle.finalDOMObservationIndex < lifecycle.domObservations.length),
  'Late reconcile capture must retain its final DOM observation index.');

  const actionCounts = countBy(trace.actions.map(event => event.action));
  for (const [label, count] of expectedCounts) assert.equal(actionCounts.get(label), count);
  const rawRequests = lifecycle.requestEvents;
  for (const read of lifecycle.builderReads) {
    assert(Number.isFinite(read.requestStartedAtEpochMs) && Number.isFinite(read.capturedAtEpochMs),
      `Builder read ${read.readId} must retain request-start and body-observation epoch times.`);
    assert(typeof read.snapshotToken === 'string' && read.snapshotToken.length > 0,
      `Builder read ${read.readId} must retain its actual catalog snapshot token.`);
    assert(read.capturedAtEpochMs >= read.requestStartedAtEpochMs,
      `Builder read ${read.readId} response body must follow its actual GET start.`);
    assert(Number.isInteger(read.requestMountEpoch) && Number.isInteger(read.mountEpoch));
  }
  for (const observation of lifecycle.domObservations) {
    assert(Number.isFinite(observation.capturedAtEpochMs) && Number.isInteger(observation.mountEpoch),
      'Each DOM observation must retain its actual epoch time and mount.');
  }
  for (const request of rawRequests) {
    assert(typeof request.isMutation === 'boolean', 'Observer request ledger must retain mutation classification.');
    assert(typeof request.method === 'string' && typeof request.path === 'string');
    assert(Number.isFinite(request.startedAt) && Number.isFinite(request.completedAt),
      `Observer request ${request.browserRequestId} must have one settled response.`);
    if (request.status === null) {
      assert(!request.isMutation && request.readCancellationDiagnostic === true &&
        request.failure?.canceled === true &&
        (isProposalRequest(request) || isReconcileRequest(request) || isPreviewRequest(request)),
      `Observer request ${request.browserRequestId} may omit status only for a captured temporal preview or proposal cancellation.`);
      assert(request.responseReceivedAt === null || request.responseReceivedAt === undefined,
        `Canceled request ${request.browserRequestId} must not invent a response-arrival time.`);
      assert(request.responseMountEpoch === null || request.responseMountEpoch === undefined,
        `Canceled request ${request.browserRequestId} must not invent a response mount epoch.`);
      assert(Number.isInteger(request.failureMountEpoch),
        `Canceled request ${request.browserRequestId} must retain its actual failure mount epoch.`);
    } else {
      assert(Number.isInteger(request.status) && request.status >= 100 && request.status <= 599,
        `Observer request ${request.browserRequestId} must retain its actual HTTP status code.`);
      assert(Number.isFinite(request.responseReceivedAt),
        `Observer request ${request.browserRequestId} must retain the actual response-arrival time.`);
      assert(request.responseReceivedAt >= request.startedAt && request.completedAt >= request.responseReceivedAt,
        `Observer request ${request.browserRequestId} must order start, response arrival, and body-capture completion.`);
      assert(Number.isInteger(request.responseMountEpoch),
        `Observer request ${request.browserRequestId} must retain its actual response mount epoch.`);
      assert(request.failureMountEpoch === null || request.failureMountEpoch === undefined,
        `Successful response ${request.browserRequestId} must not retain a request-failure mount epoch.`);
    }
    if (!request.isMutation && !isProposalRequest(request)) {
      assert(supportedPureRead(lifecycle, request),
        `Unclassified read-only ${request.method} ${request.path} has no temporal-filter path allowlist entry.`);
    }
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
  const reconcileLinks = [];
  for (let index = 0; index < trace.actions.length; index += 1) {
    const event = trace.actions[index];
    const rawStep = lifecycle.steps[index];
    assert.equal(rawStep.label, event.action, `Canonical action ${index} must map to its same-index raw step.`);
    assert.deepEqual(event.reconcileCaptureIds, rawStep.reconcileCaptureIds,
      `${event.action} reconcile request links must remain equal to their raw step references.`);
    assert(Number.isFinite(event.atMonotonicMs) && Number.isFinite(event.atEpochMs),
      `${event.action} must retain its observed timestamp.`);
    assert(Number.isInteger(event.mountEpoch), `${event.action} must retain its mount epoch.`);
    assert.equal(event.mountEpoch, rawStep.mountEpoch, `${event.action} must preserve the raw semantic-step mount epoch.`);
    assert(rawStep.requestCaptureId === null || rawStep.requestCaptureId === undefined ||
      typeof rawStep.requestCaptureId === 'string', `${event.action} request link must preserve its actual ID.`);
    assert(Array.isArray(rawStep.reconcileCaptureIds),
      `${event.action} must retain its raw reconcile request links, including an empty list.`);
    const requestCaptureId = rawStep.requestCaptureId ?? null;
    assert.deepEqual(event.before, snapshotAt(lifecycle, rawStep.beforeReadIndex),
      `${event.action} before snapshot must resolve from its actual raw Builder read index.`);
    assert.deepEqual(event.after, snapshotAt(lifecycle, rawStep.afterReadIndex),
      `${event.action} after snapshot must resolve from its actual raw Builder read index.`);
    assert.deepEqual(event.ui, domAt(lifecycle, rawStep.domObservationIndex),
      `${event.action} DOM observation must resolve from its actual raw DOM index.`);
    const beforeRead = rawStep.beforeReadIndex === null || rawStep.beforeReadIndex === undefined
      ? null : lifecycle.builderReads[rawStep.beforeReadIndex];
    const afterRead = rawStep.afterReadIndex === null || rawStep.afterReadIndex === undefined
      ? null : lifecycle.builderReads[rawStep.afterReadIndex];
    const domObservation = rawStep.domObservationIndex === null || rawStep.domObservationIndex === undefined
      ? null : lifecycle.domObservations[rawStep.domObservationIndex];
    if (requestCaptureId === null) {
      assert.equal(event.request, null, `${event.action} without a captured request must remain request-free.`);
      if (rawStep.domObservationIndex !== null && rawStep.domObservationIndex !== undefined) {
        const observation = domObservation;
        assert.equal(event.atMonotonicMs, observation.capturedAtMonotonicMs,
          `${event.action} timestamp must come from its actual DOM observation.`);
        assert.equal(event.atEpochMs, observation.capturedAtEpochMs,
          `${event.action} epoch time must come from its actual DOM observation.`);
        assert.equal(event.mountEpoch, observation.mountEpoch,
          `${event.action} mount epoch must come from its actual DOM observation.`);
      }
      if (beforeRead && domObservation) {
        assert(beforeRead.capturedAtEpochMs <= domObservation.capturedAtEpochMs,
          `${event.action} pre-action Builder read must precede its DOM observation.`);
      }
      if (afterRead && domObservation) {
        assert(domObservation.capturedAtEpochMs >= afterRead.capturedAtEpochMs,
          `${event.action} DOM observation must follow its bound post-action Builder response.`);
      }
    } else {
      const raw = rawRequestById.get(requestCaptureId);
      assert(raw, `${event.action} must reference one raw observed request.`);
      assert.equal(event.request?.captureId, requestCaptureId);
      assert.equal(event.request?.isMutation, raw.isMutation,
        `${event.action} must preserve the observer's mutation classification.`);
      assert.equal(event.request?.requestId ?? null, raw.requestId ?? null,
        `${event.action} must preserve its actual raw request ID or its actual absence.`);
      assert.equal(event.request?.method, raw.method);
      assert.equal(event.request?.path, raw.path);
      assert.equal(event.request?.status, raw.status);
      assert(Number.isFinite(raw.status) && raw.status >= 200 && raw.status < 300,
        `${event.action} semantic request must have one successful response.`);
      assert.equal(event.atMonotonicMs, raw.atMonotonicMs);
      assert.equal(event.atEpochMs, raw.atEpochMs);
      assert.equal(event.mountEpoch, raw.mountEpoch);
      assert.deepEqual(event.request?.body, raw.request,
        `${event.action} model request must map to the actual captured request body.`);
      assert.deepEqual(event.request?.responseBody, raw.response,
        `${event.action} model response must map to the matching captured response body.`);
      assert.equal(Number.isFinite(raw.completedAt), true,
        `${event.action} request must have a settled matching response.`);
      assert(Number.isFinite(raw.responseReceivedAt),
        `${event.action} request must retain the network response-arrival boundary.`);
      if (beforeRead) {
        assert(beforeRead.capturedAtEpochMs <= raw.startedAt,
          `${event.action} precondition Builder read must complete before the bound request starts.`);
      }
      if (afterRead) {
        assert(afterRead.requestStartedAtEpochMs >= raw.responseReceivedAt,
          `${event.action} post-settlement Builder GET must start after the bound request response arrives.`);
      }
      if (domObservation) {
        assert(domObservation.capturedAtEpochMs >= raw.responseReceivedAt,
          `${event.action} DOM observation must follow the bound request response arrival.`);
      }
      if (afterRead && domObservation) {
        assert(domObservation.capturedAtEpochMs >= afterRead.capturedAtEpochMs,
          `${event.action} DOM observation must follow its actual post-settlement Builder response.`);
      }
      actionRequestIds.push(requestCaptureId);
    }
    assert.equal(event.request?.isMutation ?? false, MUTATION_ACTIONS.has(event.action),
      `${event.action} must have the mutation class defined by its semantic transition.`);
    const linkedProposals = safeArray(rawStep.proposalCaptureIds);
    proposalLinks.push(...linkedProposals);
    for (const proposalId of linkedProposals) {
      const request = rawRequestById.get(proposalId);
      assert(request, `${event.action} proposal link must identify a captured observer request.`);
      assert.equal(request.method, 'POST');
      assert(isProposalRequest(request),
        `${event.action} proposal link must be a construction proposal request.`);
      if (Number.isFinite(request.responseReceivedAt) && domObservation) {
        assert(domObservation.capturedAtEpochMs >= request.responseReceivedAt,
          `${event.action} DOM observation must follow the linked proposal response arrival.`);
      }
    }
    if (requestCaptureId !== null && event.action.startsWith('FilterProposed(')) {
      assert(linkedProposals.includes(requestCaptureId),
        `${event.action} primary request must also be linked as its proposal evidence.`);
    }

    const linkedReconciles = rawStep.reconcileCaptureIds;
    linkedReconciles.forEach(captureId => {
      const request = rawRequestById.get(captureId);
      assert(request, `${event.action} reconcile link must resolve to an actual observer request.`);
      assert(isReconcileRequest(request), `${event.action} reconcile link must identify POST /reconcile.`);
      assert.equal(request.isMutation, false,
        'POST /reconcile must preserve the observer’s read-only classification.');
      if (request.status === null) {
        assert(request.failure?.canceled === true && request.readCancellationDiagnostic === true &&
          request.responseReceivedAt === null && request.responseMountEpoch === null,
        `${event.action} canceled reconcile must preserve its actual no-response diagnostic.`);
      } else {
        assert(Number.isFinite(request.responseReceivedAt) && Number.isInteger(request.responseMountEpoch),
          `${event.action} reconcile must retain its actual response-arrival time and mount.`);
      }
    });
    if (event.action === 'Reload') {
      assert(beforeRead && afterRead && domObservation,
        'Each Reload must bind its actual pre-read, post-read, and DOM observation.');
      const linkedReloadReconciles = event.reconcileCaptureIds
        .map(captureId => rawRequestById.get(captureId))
        .filter(isReconcileRequest);
      assert(linkedReloadReconciles.some(request =>
        request.request?.snapshotToken === event.after.snapshotToken &&
        request.request?.draftVersion === event.after.draftVersion &&
        request.request?.draftDigest === event.after.draftDigest &&
        request.request?.outputId === event.after.outputId),
      'Reload reconcile request must bind the fresh saved Builder draft digest and output.');
      assert(linkedReloadReconciles.some(request =>
        request.status >= 200 && request.status < 300 &&
        request.response?.receiptId === event.ui.receiptId &&
        safeArray(request.response?.outputs).some(output => output?.outputId === event.after.outputId)),
      'Reload DOM receipt must match a successful actual reconcile response for its saved output.');
      const matchingReconciles = rawRequests.filter(request => isReconcileRequest(request) &&
        Number.isFinite(request.status) && request.status >= 200 && request.status < 300 &&
        request.request?.snapshotToken === event.after.snapshotToken &&
        request.request?.draftVersion === event.after.draftVersion &&
        request.request?.draftDigest === event.after.draftDigest &&
        request.request?.outputId === event.after.outputId &&
        request.response?.snapshotToken === event.after.snapshotToken &&
        request.response?.receiptId === event.ui.receiptId &&
        safeArray(request.response?.outputs).some(output => output?.outputId === event.after.outputId) &&
        request.mountEpoch === request.responseMountEpoch &&
        request.responseMountEpoch === event.ui.mountEpoch);
      const postSettlementReconciles = matchingReconciles.filter(request =>
        afterRead.requestStartedAtEpochMs >= request.responseReceivedAt &&
        domObservation.capturedAtEpochMs >= request.responseReceivedAt);
      assert(postSettlementReconciles.length > 0,
        'Reload post-settlement Builder GET must start and its DOM observation must follow the actual reconcile responseReceivedAt.');
      assert.equal(event.ui.outputId, event.after.outputId,
        'Reload DOM preview output must match its actual saved Builder snapshot.');
    }
    reconcileLinks.push(...linkedReconciles);
  }
  assert.equal(new Set(actionRequestIds).size, actionRequestIds.length,
    'Each semantic request must be consumed by one action only.');
  const expectedMutations = rawRequests.filter(request => request.isMutation).map(request => request.browserRequestId).sort();
  const consumedMutations = trace.actions.filter(event => event.request?.isMutation)
    .map(event => event.request.captureId).sort();
  assert.deepEqual(consumedMutations, expectedMutations,
    'Every observed mutation must be bound to exactly one semantic action.');
  const expectedProposals = rawRequests.filter(isProposalRequest)
    .map(request => request.browserRequestId).sort();
  assert.deepEqual([...proposalLinks].sort(), expectedProposals,
    'Every observer construction proposal must be linked to exactly one semantic step.');
  const expectedReconciles = rawRequests.filter(isReconcileRequest)
    .map(request => request.browserRequestId).sort();
  const allReconcileLinks = [...reconcileLinks, ...lifecycle.lateReconcileCaptureIds];
  assert.equal(new Set(allReconcileLinks).size, allReconcileLinks.length,
    'Each observed reconcile request must be linked to one step window or the explicit late-request ledger.');
  assert.deepEqual([...allReconcileLinks].sort(), expectedReconciles,
    'Every observer POST /reconcile must be retained in a step window or the explicit late-request ledger.');
  const lateReconciles = rawRequests.filter(request =>
    lifecycle.lateReconcileCaptureIds.includes(request.browserRequestId));
  if (lateReconciles.length > 0) {
    const finalDOM = lifecycle.domObservations[lifecycle.finalDOMObservationIndex];
    assert(finalDOM, 'Unassigned late reconciles must resolve to the retained final raw DOM observation.');
    const lateReceipts = new Set(lateReconciles.map(request => request.response?.receiptId)
      .filter(value => typeof value === 'string' && value.length > 0));
    const relevantResponseTimes = [
      ...lateReconciles.map(request => request.responseReceivedAt),
      ...rawRequests.filter(request => isPreviewRequest(request) &&
        lateReceipts.has(request.request?.receiptId)).map(request => request.responseReceivedAt),
    ].filter(Number.isFinite);
    if (relevantResponseTimes.length > 0) {
      assert(finalDOM.capturedAtEpochMs >= Math.max(...relevantResponseTimes),
        'Final DOM evidence must follow every captured late reconcile and matching preview response arrival.');
    }
  }
  const { evidenceByCaptureId: reconcileEvidenceByCaptureId,
    producerIdsByPreviewId: reconcileProducerIdsByPreviewId } = deriveReconcileTransportEvidence(
    trace, lifecycle, rawRequests, rawRequestById);

  const firstActionRead = trace.actions[0]?.after;
  assert(firstActionRead && trace.initialObservation,
    'Canonical trace must retain the first actual post-create Builder observation.');
  assert.equal(trace.initialObservation.readId, firstActionRead.readId);
  assert.equal(trace.initialObservation.mountEpoch, firstActionRead.mountEpoch);
  assert.equal(trace.initialObservation.draftVersion, firstActionRead.draftVersion);
  assert.equal(trace.initialObservation.draftDigest, firstActionRead.draftDigest);
  assert.equal(trace.initialObservation.snapshotToken, firstActionRead.snapshotToken);
  assert.deepEqual(trace.initialObservation.workspaceDocuments, firstActionRead.workspaceDocuments);

  const timeOrigin = {
    monotonicMs: Math.min(...trace.actions.map(event => event.atMonotonicMs)),
    epochMs: Math.min(...trace.actions.map(event => event.atEpochMs)),
  };
  const rawStepByActionIndex = lifecycle.steps;
  const initialMountEpoch = trace.actions[0].mountEpoch;
  const modelEvents = trace.actions.map((event, index) => projectModelEvent(
    event, rawStepByActionIndex[index], lifecycle, timeOrigin, initialMountEpoch));
  const observedRequests = rawRequests.map(request => ({
    captureId: request.browserRequestId,
    requestId: noIfAbsent(request.requestId),
    isMutation: Boolean(request.isMutation),
    method: request.method,
    path: request.path,
    status: noIfAbsent(request.status) === NO ? 0 : request.status,
    startedAt: Math.round(request.startedAt - timeOrigin.epochMs),
    startedAtExact: String(request.startedAt),
    responseReceivedAt: Number.isFinite(request.responseReceivedAt)
      ? Math.round(request.responseReceivedAt - timeOrigin.epochMs)
      : NO,
    responseReceivedAtExact: Number.isFinite(request.responseReceivedAt)
      ? String(request.responseReceivedAt)
      : NO,
    completedAt: Number.isFinite(request.completedAt)
      ? Math.round(request.completedAt - timeOrigin.epochMs)
      : NO,
    completedAtExact: Number.isFinite(request.completedAt) ? String(request.completedAt) : NO,
    requestMountEpoch: request.mountEpoch,
    responseMountEpoch: noIfAbsent(request.responseMountEpoch),
    failureMountEpoch: noIfAbsent(request.failureMountEpoch),
    failureCanceled: Boolean(request.failure?.canceled),
    requestReceiptId: noIfAbsent(request.request?.receiptId),
    requestOutputId: noIfAbsent(request.request?.outputId),
    outputId: noIfAbsent(request.response?.outputId),
    receiptId: noIfAbsent(request.response?.receiptId ?? request.response?.preview?.receiptId),
    responseReceiptId: noIfAbsent(request.response?.receiptId ?? request.response?.preview?.receiptId),
    responseOutputId: noIfAbsent(request.response?.outputId ?? request.response?.preview?.outputId),
    candidateColumnIds: safeArray(request.response?.candidateColumnIds),
    requestSnapshotToken: noIfAbsent(request.request?.snapshotToken),
    requestDraftVersion: noIfAbsent(request.request?.draftVersion),
    requestDraftDigest: noIfAbsent(request.request?.draftDigest),
    responseSnapshotToken: noIfAbsent(request.response?.snapshotToken ?? request.response?.catalog?.snapshotToken),
    responseDraftVersion: noIfAbsent(request.response?.draftVersion),
    responseDraftDigest: noIfAbsent(request.response?.draftDigest),
    responseOutputIds: Array.isArray(request.response?.outputs)
      ? request.response.outputs.map(output => noIfAbsent(output?.outputId))
      : Array.isArray(request.response?.workspace?.documents)
        ? request.response.workspace.documents.map(document => noIfAbsent(document?.output?.id))
        : NO,
    responseCode: noIfAbsent(request.response?.code ?? request.response?.error?.code),
    responseRetryable: Boolean(request.response?.retryable ?? request.response?.error?.retryable),
    possibleReconcileProducerCaptureIds: reconcileProducerIdsByPreviewId.get(request.browserRequestId) ?? [],
    reconcileDisposition: reconcileEvidenceByCaptureId.get(request.browserRequestId)?.disposition ?? NO,
    reconcileCompatiblePreviewCaptureIds: reconcileEvidenceByCaptureId.get(request.browserRequestId)?.compatiblePreviewCaptureIds ?? [],
    reconcileDOMObservationIndexes: reconcileEvidenceByCaptureId.get(request.browserRequestId)?.domObservationIndexes ?? [],
    reconcileInvalidatedByCaptureIds: reconcileEvidenceByCaptureId.get(request.browserRequestId)?.invalidatedByCaptureIds ?? [],
    reconcileRetryFollowupCaptureIds: reconcileEvidenceByCaptureId.get(request.browserRequestId)?.retryFollowupCaptureIds ?? [],
    requestEvidence: opaqueJSON(request.request),
    responseEvidence: opaqueJSON(request.response),
  }));
  const observedDOMObservations = lifecycle.domObservations.map((observation, index) => ({
    observationIndex: index,
    capturedAt: Math.round(observation.capturedAtEpochMs - timeOrigin.epochMs),
    capturedAtExact: String(observation.capturedAtEpochMs),
    mountEpoch: observation.mountEpoch,
    receiptId: noIfAbsent(observation.receiptId),
    outputId: noIfAbsent(observation.outputId),
    rowIds: safeArray(observation.rowIds),
  }));
  const observedBuilderReads = lifecycle.builderReads.map(read => ({
    readId: read.readId,
    requestId: noIfAbsent(read.requestId),
    status: read.status,
    requestStartedAt: Math.round(read.requestStartedAtEpochMs - timeOrigin.epochMs),
    requestStartedAtExact: String(read.requestStartedAtEpochMs),
    capturedAt: Math.round(read.capturedAtEpochMs - timeOrigin.epochMs),
    capturedAtExact: String(read.capturedAtEpochMs),
    requestMountEpoch: read.requestMountEpoch,
    mountEpoch: read.mountEpoch,
    snapshotToken: read.snapshotToken,
    draftVersion: read.draftVersion,
    draftDigest: read.draftDigest,
    outputIds: safeArray(read.workspaceDocuments)
      .map(document => noIfAbsent(document?.output?.id))
      .filter(outputId => outputId !== NO),
  }));
  const pureReadCaptureIds = rawRequests.filter(request => !request.isMutation &&
    !isProposalRequest(request) && !isReconcileRequest(request) && !isPreviewRequest(request))
    .map(request => request.browserRequestId);

  return {
    caseName,
    profile,
    initialMountEpoch,
    caseValue: buildCase(caseName, trace.actions),
    modelEvents,
    observedRequests,
    observedDOMObservations,
    observedBuilderReads,
    eventCount: modelEvents.length,
    observedRequestCount: observedRequests.length,
    observedDOMObservationCount: observedDOMObservations.length,
    observedBuilderReadCount: observedBuilderReads.length,
    mutationCount: expectedMutations.length,
    proposalCount: expectedProposals.length,
    pureReadCaptureIds,
    responseTimeBoundary: 'Use responseReceivedAt as the network response-arrival boundary; completedAt only proves observer body capture settled and may lag the UI response.',
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
  'InvReconcileEvidenceLinked',
  'InvObservedRequestChronology',
  'InvObservedBuilderReadChronology',
  'InvReconcilePossibleProducerRelations',
  'InvReconcileOwnerEvidence',
  'InvReconcileTransportSemantics',
  'InvReconcileRetryFollowups',
  'InvReconcileStepLinksBound',
  'InvReloadReconcileBindsFreshSnapshot',
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
    `TraceInitialMountEpoch == ${tlaValue(input.initialMountEpoch)}`,
    `TraceCase == ${tlaValue(input.caseValue)}`,
    `TraceEvents == ${tlaValue(input.modelEvents)}`,
    `TraceObservedRequests == ${tlaValue(input.observedRequests)}`,
    `TraceObservedDOM == ${tlaValue(input.observedDOMObservations)}`,
    `TraceObservedBuilderReads == ${tlaValue(input.observedBuilderReads)}`,
    '====',
    '',
  ].join('\n');
  const configText = [
    'SPECIFICATION Spec',
    `CONSTANT Profile = ${tlaValue(input.profile)}`,
    'CONSTANT InitialMountEpoch <- TraceInitialMountEpoch',
    'CONSTANT Case <- TraceCase',
    'CONSTANT ModelEvents <- TraceEvents',
    'CONSTANT ObservedRequests <- TraceObservedRequests',
    'CONSTANT ObservedDOM <- TraceObservedDOM',
    'CONSTANT ObservedBuilderReads <- TraceObservedBuilderReads',
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
    initialMountEpoch: input.initialMountEpoch,
    eventCount: input.eventCount,
    observedRequestCount: input.observedRequestCount,
    observedDOMObservationCount: input.observedDOMObservationCount,
    observedBuilderReadCount: input.observedBuilderReadCount,
    mutationCount: input.mutationCount,
    proposalCount: input.proposalCount,
    pureReadCaptureIds: input.pureReadCaptureIds,
    responseTimeBoundary: input.responseTimeBoundary,
    timestampEncoding: input.timestampEncoding,
  };
};

export { REQUIRED_LABELS as TEMPORAL_FILTER_TLA_ACTIONS };
