import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { sanitizeBody, sanitizePayload, sanitizeText } from '../helpers/playwright-browser.mjs';
import { findCompletedNativeResponse } from '../helpers/cda-playwright-requests.mjs';
import { createPendingResponseReads } from '../helpers/pending-response-reads.mjs';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { sourceFingerprint } from '../helpers/source-fingerprint.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp } from '../helpers/api-build-freeze.mjs';
import { expectedRelatedSourceOneValidation, relatedSourceProposalCandidate, selectedRelatedSourceProposal as matchSelectedRelatedSourceProposal } from '../helpers/related-source-capture.mjs';
import { createNativeAbortProbeSource, nativeAbortProbeEvidenceForRequest, nativeAbortDomOwnerRules } from '../helpers/native-abort-probe.mjs';

// Adapted from verify-cda-group-related-values-browser.mjs and
// verify-cda-coded-field-lifecycle-browser.mjs. The raw CDA oracle is kept
// separate from Explorer previews and bounds independent witnesses.
export async function relatedOneAllWorkflow({
  page: nativePage, cda, mode: requestedMode, fieldMode: requestedFieldMode, witnessMode: requestedWitnessMode,
}) {
const mode = requestedMode ?? process.env.LOOM_RELATED_ONE_ALL_MODE ?? 'cda';
assert(['basic', 'cda'].includes(mode), 'LOOM_RELATED_ONE_ALL_MODE must be basic or cda');
const basicMode = mode === 'basic';
const project = basicMode ? process.env.LOOM_DEV_PROJECT : cda.project;
const generation = basicMode ? process.env.LOOM_DEV_GENERATION : cda.generation ?? process.env.LOOM_CDA_GENERATION;
const fieldMode = requestedFieldMode ?? process.env.LOOM_RELATED_ONE_ALL_FIELD ?? 'id';
assert(['id', 'status', 'specimen-reference'].includes(fieldMode),
  'LOOM_RELATED_ONE_ALL_FIELD must be id, status, or specimen-reference');
const witnessMode = requestedWitnessMode ?? process.env.LOOM_RELATED_ONE_ALL_WITNESS ?? 'default';
assert(['default', 'zero'].includes(witnessMode), 'LOOM_RELATED_ONE_ALL_WITNESS must be default or zero');
const zeroObservationMode = witnessMode === 'zero';
assert(!zeroObservationMode || (mode === 'cda' && fieldMode === 'id'),
  'The zero Observation witness mode requires the CDA Observation.id workflow');
const statusFieldMode = fieldMode === 'status';
const referenceFieldMode = fieldMode === 'specimen-reference';
assert(!basicMode || statusFieldMode, 'The basic fixture mode is only defined for Observation.status');
const relatedFieldPath = statusFieldMode ? 'status' : referenceFieldMode ? 'specimen.reference' : 'id';
const relatedOutputLabel = statusFieldMode ? 'Observation status' : referenceFieldMode ? 'Observation specimen reference' : 'Observation ID';
const relatedChoiceLabel = statusFieldMode ? 'Status' : referenceFieldMode ? 'Specimen Reference' : relatedOutputLabel;
const logicalTypeExpected = 'string';
const cardinalityExpected = 'optional_one';
const referenceWitness = {
  specimenKey: 'Specimen/g_00003b7dca775d32e82a19f3c3c1227d234ca8d3486df068194137e93b1f796b',
  specimenId: 'b7cad184-db67-5542-a975-10fffa3e89e7',
  patientId: 'afcfb15e-7617-5691-ae2c-ab675322fb33',
};
const rootResourceType = basicMode || zeroObservationMode ? 'Patient' : 'Specimen';
const expectedRelatedRouteTypes = basicMode || zeroObservationMode ? ['Patient', 'Observation'] : ['Specimen', 'Patient', 'Observation'];
const expectedPatientObservationRoute = [
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const expectedReferenceRoute = [
  { fromResourceType: 'Specimen', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const patientObservationRouteLabel = basicMode || zeroObservationMode
  ? `${relatedChoiceLabel}: Patient <-[subject]- Observation`
  : `${relatedChoiceLabel}: Specimen -[subject]-> Patient <-[subject]- Observation`;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `related-one-all-${Date.now()}`;
const evidence = cda.evidence;
const apiOrigin = process.env.LOOM_API_ORIGIN ?? cda.apiOrigin;
const uiOrigin = process.env.LOOM_UI_ORIGIN ?? cda.uiOrigin;
const apiBuildContainer = process.env.LOOM_CDA_API_CONTAINER;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const arangoDatabase = process.env.LOOM_ARANGO_DATABASE;
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
assert(project && generation && apiOrigin && uiOrigin, 'Set the explicit isolated project, generation, API origin, and UI origin.');
assert(apiBuildContainer && arangoContainer && arangoDatabase && process.env.LOOM_CDA_COMPOSE_PROJECT,
  'Set explicit isolated CDA API/Arango containers, Arango database, and Compose project.');
const sourceRoot = cda.target.sourceRoot ?? fileURLToPath(new URL('../../..', import.meta.url));
const report = {
  explorer, project, generation, protectedExplorerUntouched: true, relatedChoiceAssertions: [],
  mode: basicMode ? 'basic-fixture' : 'cda', fieldMode, witnessMode,
  scenario: zeroObservationMode
    ? 'Bounded exact project/generation Patient root with an independently reread empty incoming typed Observation.subject_Patient edge set; native ONE/ALL is checked through the current PRESERVE_PARENT expansion and full saved lifecycle.'
    : referenceFieldMode
    ? 'Pinned current-generation Specimen→Patient→Observation witness with 29 distinct nonnull Observation.specimen.reference values and two null/missing references; native grouped-row ONE rejection and same-chooser ALL repair preserve one protocol value per terminal Observation identity.'
    : statusFieldMode
      ? basicMode
        ? 'Basic Patient-root fixture with exact Patient→Observation edges, a direct optional_one Observation.status scalar source, and ONE/ALL preview lifecycle.'
        : 'Patient-grouped CDA rows with the direct related Observation.status scalar source; a bounded multi-status witness predicts ONE rejection and same-chooser ALL repair.'
      : 'Patient-grouped CDA rows from one selected Specimen per witness, with related Observation ID values; a many-Observation witness must reproduce grouped-row ONE rejection and same-chooser ALL repair. Zero/one Observation witnesses are additional coverage when available.',
  started: new Date().toISOString(), cases: [], apiCalls: [], errors: [], nativeRequests: [],
  nativeAbortProbeEvents: [],
  frameNavigations: [], executionContextRetirements: [], expectedOwnerCancellations: [],
  requestOwnershipEvidence: {
    status: 'partial',
    transport: 'Playwright Request object identity and public request/response/frame events',
    proven: ['exact owned API Request-to-response correlation', 'response body and HTTP status association',
      'same-document owner cancellation only when exact request signal, trusted Playwright action, and DOM detachment all correlate'],
    gap: 'Playwright does not expose Chromium loader IDs or execution-context creation/destruction events; no navigation-time context retirement is inferred.',
  },
  ...(statusFieldMode ? { statusSourceContract: { path: 'Observation.status', logicalType: 'string', cardinality: 'optional_one',
    catalogProjectionMode: 'VALUE', relatedSourceForm: 'ALL',
    note: 'FHIR code primitive is represented as string per Observation; the related source retains all matching records.' } } : {}),
  ...(referenceFieldMode ? { referenceSourceContract: { path: 'Observation.specimen.reference', logicalType: 'string', cardinality: 'optional_one',
    nullable: true, route: ['Specimen', 'Patient', 'Observation'], form: 'ALL', terminalIdentity: '_id',
    duplicateValuePolicy: 'Preserve one value per distinct terminal Observation identity; do not collapse by field value.',
    note: 'The pinned oracle has 31 distinct Observation records: 29 distinct nonnull references and two null/missing references, which the exact AQL projection normalizes to null. The native ALL protocol array must retain both normalized null entries.' } } : {}),
};
await mkdir(evidence, { recursive: true });
report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

const officialRequestCapture = cda.captureRequests(`${root}/${encodeURIComponent(explorer)}`);
let builder;
let outputId;
let verificationPhase = 'raw-oracle';
let frozenApiBuild;
let apiBuildCheckStarted = false;
const nativeByRequestId = new Map();
let reportFinalized = false;
const nativeRequestSummary = (request) => {
  if (!request || typeof request !== 'object') return request;
  const diagnosticKeys = [
    'rowRoot', 'resourceType', 'outputId', 'sourceChoiceId', 'frameId', 'frameSourceId',
    'query', 'cursor', 'limit', 'requestId', 'selectionRevisionId', 'expectedDraftVersion',
    'changedStepId', 'removeStepIds',
  ];
  return Object.fromEntries(diagnosticKeys.filter((key) => Object.hasOwn(request, key)).map((key) => [key, request[key]]));
};
const nativeInitiatorSummary = (initiator) => initiator ? {
  type: initiator.type,
  url: initiator.url,
  requestId: initiator.requestId,
  stack: (initiator.stack?.callFrames ?? []).slice(0, 4).map((frame) => ({
    functionName: frame.functionName,
    url: frame.url,
    lineNumber: frame.lineNumber,
    columnNumber: frame.columnNumber,
  })),
} : undefined;
const pendingNativeEvidence = (entry) => ({
  requestId: entry.requestId,
  requestCorrelationId: entry.requestCorrelationId,
  method: entry.method,
  path: entry.path,
  ageMs: Date.now() - entry.startedAt,
  frameId: entry.frameId,
  loaderId: entry.loaderId,
  scopeProject: entry.scopeProject,
  scopeExplorer: entry.scopeExplorer,
  owningFrameWasCurrentMainLoader: entry.owningFrameWasCurrentMainLoader,
  owningFrameLoaderCommittedAt: entry.owningFrameLoaderCommittedAt,
  initiatorExecutionContexts: entry.initiatorExecutionContexts,
  resourceType: entry.resourceType,
  initiator: entry.initiator,
  request: nativeRequestSummary(entry.request),
  requestTimestamp: entry.requestTimestamp,
  requestWallTime: entry.requestWallTime,
  status: entry.status,
  responseTimestamp: entry.responseTimestamp,
  responseReceivedAt: entry.responseReceivedAt,
  networkTerminal: entry.networkTerminal,
  bodyReadStatus: entry.bodyReadStatus,
  loadingFailed: entry.loadingFailed,
  ownerRetirement: entry.ownerRetirement,
  ownershipEvidenceGap: entry.loadingFailed?.canceled && !entry.expectedOwnerCancellation
    ? 'Playwright exposes request failure and Request identity but not Chromium loader or execution-context retirement IDs.' : undefined,
  laterSamePayload: report.nativeRequests.filter((candidate) => candidate.requestId !== entry.requestId &&
    candidate.path === entry.path && JSON.stringify(candidate.request ?? null) === JSON.stringify(entry.request ?? null) &&
    candidate.startedAt >= entry.startedAt).slice(0, 5).map((candidate) => ({
      requestId: candidate.requestId,
      requestCorrelationId: candidate.requestCorrelationId,
      startedAt: candidate.startedAt,
      frameId: candidate.frameId,
      loaderId: candidate.loaderId,
      owningFrameWasCurrentMainLoader: candidate.owningFrameWasCurrentMainLoader,
      initiatorExecutionContexts: candidate.initiatorExecutionContexts,
      resourceType: candidate.resourceType,
      status: candidate.status,
      networkTerminal: candidate.networkTerminal,
      bodyReadStatus: candidate.bodyReadStatus,
      loadingFailed: candidate.loadingFailed,
    })),
});
const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-one-all-${randomUUID()}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.apiCalls.push({ path, status: response.status,
    body: body === undefined ? undefined : parseSanitizedBody(JSON.stringify(body)),
    response: parseSanitizedBody(JSON.stringify(value)) });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', arangoDatabase, '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango returned no JSON array: ${result.stdout.slice(-1000)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};
const command = async (commands) => {
  await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await api(`${base}/builder`);
  return builder;
};
const doc = (state = builder) => state.workspace.documents.find((document) => document.output.id === outputId);
const assertStatusCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'status');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.status candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, logicalTypeExpected, 'Observation.status must retain the current compiler-proved string logical type');
    assert.equal(candidate.cardinality, cardinalityExpected, 'Observation.status must retain its compiler-proved scalar cardinality');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.status must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The native catalog must advertise the compiler-proved scalar VALUE projection');
    const choice = candidate.constructionChoice;
    assert(choice, 'Observation.status must carry its nested compiler-proved field choice');
    assert.equal(choice.source?.kind, 'FIELD');
    assert.equal(choice.source?.nodeId, candidate.nodeId);
    assert.equal(choice.source?.resourceType, 'Observation');
    assert.equal(choice.source?.path, 'status');
    assert.equal(choice.source?.cardinality, cardinalityExpected);
    assert(choice.options?.some((option) => option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'),
      'The nested field choice must expose a supported scalar VALUE form');
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      scalarOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'VALUE' && option.shape === 'SCALAR'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const assertReferenceCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'specimen.reference');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.specimen.reference candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, 'string', 'Observation.specimen.reference must retain its compiler-proved string logical type');
    assert.equal(candidate.cardinality, 'optional_one', 'Observation.specimen.reference must remain a nullable scalar');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.specimen.reference must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The current catalog must expose scalar VALUE projection for Observation.specimen.reference');
    const choice = candidate.constructionChoice;
    assert(choice, 'Observation.specimen.reference must carry its nested compiler-proved field choice');
    assert.equal(choice.source?.kind, 'FIELD');
    assert.equal(choice.source?.nodeId, candidate.nodeId);
    assert.equal(choice.source?.resourceType, 'Observation');
    assert.equal(choice.source?.path, 'specimen.reference');
    assert.equal(choice.source?.cardinality, 'optional_one');
    assert(choice.options?.some((option) => option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'),
      'The nested field choice must expose a supported scalar VALUE form');
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      allOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'ALL'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const assertRelatedIdCandidate = (state) => {
  const resourceTypeByNode = new Map((state.catalog.nodes ?? []).map((node) => [node.nodeId, node.resourceType]));
  const candidates = state.catalog.candidates.filter((candidate) =>
    resourceTypeByNode.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'id');
  assert(candidates.length > 0, 'The current authorized catalog has no Observation.id candidate on an Observation node');
  for (const candidate of candidates) {
    assert.equal(candidate.logicalType, 'string', 'Observation.id must retain its compiler-proved string logical type');
    assert.equal(candidate.cardinality, 'optional_one', 'Observation.id must retain its compiler-proved scalar cardinality');
    assert.deepEqual(candidate.repeatedBoundaries ?? [], [], 'Observation.id must have no repeated boundary');
    assert(candidate.projectionModes.includes('VALUE'), 'The native catalog must advertise Observation.id VALUE projection');
    assert.equal(candidate.constructionChoice?.source?.kind, 'FIELD');
    assert.equal(candidate.constructionChoice?.source?.nodeId, candidate.nodeId);
    assert.equal(candidate.constructionChoice?.source?.resourceType, 'Observation');
    assert.equal(candidate.constructionChoice?.source?.path, 'id');
    assert.equal(candidate.constructionChoice?.source?.cardinality, 'optional_one');
    assert(candidate.constructionChoice?.options?.some((option) =>
      option.form === 'VALUE' && option.shape === 'SCALAR' && option.support === 'SUPPORTED'));
  }
  report.relatedFieldCandidates = candidates.map((candidate) => ({
    candidateId: candidate.candidateId, nodeId: candidate.nodeId, resourceType: resourceTypeByNode.get(candidate.nodeId),
    path: candidate.fieldPath, logicalType: candidate.logicalType, cardinality: candidate.cardinality,
    projectionModes: candidate.projectionModes, constructionChoice: {
      source: candidate.constructionChoice?.source,
      scalarOptions: candidate.constructionChoice?.options?.filter((option) => option.form === 'VALUE' && option.shape === 'SCALAR'),
    }, repeatedBoundaries: candidate.repeatedBoundaries ?? [],
  }));
  return candidates;
};
const relatedValuesFor = (witness) => statusFieldMode ? witness.observationStatuses : referenceFieldMode ? witness.observationReferences : witness.observationIds;
const relatedDisplayValuesFor = (witness) => {
  const values = relatedValuesFor(witness).filter((value) => value !== null && value !== undefined);
  return zeroObservationMode && values.length === 0 ? '—' : values.join('; ');
};
const readFixtureNDJSON = async (relativePath) => (await readFile(fileURLToPath(new URL(relativePath, import.meta.url)), 'utf8'))
  .split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const nativeByRequest = new Map();
const nativeProtocolResponses = new WeakMap();
const protocolResponse = entry => nativeProtocolResponses.get(entry);
const nativeResponseReads = createPendingResponseReads();
const nativeRequestWaiters = new Set();
let nextNativeRequestId = 1;
const notifyNativeRequestChange = () => {
  for (const resolve of nativeRequestWaiters) resolve();
  nativeRequestWaiters.clear();
};
const waitForNativeRequestChange = timeoutMs => new Promise(resolve => {
  let done = false;
  const finish = () => {
    if (done) return;
    done = true;
    clearTimeout(timer);
    nativeRequestWaiters.delete(finish);
    resolve();
  };
  const timer = setTimeout(finish, timeoutMs);
  nativeRequestWaiters.add(finish);
});
const appOrigins = new Set([apiOrigin, uiOrigin].map(value => new URL(value).origin));
const uiApiOrigin = new URL(uiOrigin).origin;
const ownedApiPath = `${base}/`;
const requestURL = request => {
  try { return new URL(request.url()); } catch { return undefined; }
};
const isOwnedNativeRequest = request => {
  const url = requestURL(request);
  return Boolean(url && url.origin === uiApiOrigin && url.pathname.startsWith(ownedApiPath));
};
const parseSanitizedBody = value => {
  const text = String(value ?? '');
  if (text.length > 12_000) return { diagnosticBodyTruncated: true, length: text.length };
  try { return sanitizePayload(JSON.parse(text)); } catch { return sanitizeBody(text); }
};
const selectedRelatedSourceEvidence = () => {
  const candidate = report.relatedFieldCandidate;
  const choiceId = report.currentRelatedChoiceId;
  const choice = report.relatedChoiceAssertions.find(item => item.choiceId === choiceId);
  return {
    outputId, candidateId: candidate?.candidateId, nodeId: candidate?.nodeId,
    choiceId, snapshotToken: choice?.choiceRequest?.snapshotToken,
    resourceType: 'Observation', path: relatedFieldPath, routeTypes: expectedRelatedRouteTypes,
  };
};
const selectedRelatedSourceProposal = entry => matchSelectedRelatedSourceProposal(entry, selectedRelatedSourceEvidence());
const expectedHttpValidation = entry => expectedRelatedSourceOneValidation(entry, selectedRelatedSourceEvidence());
const annotateExpectedOwnerCancellation = entry => {
  if (entry.expectedOwnerCancellation || entry.bodyReadStatus !== 'failed' ||
      entry.loadingFailed?.errorText !== 'net::ERR_ABORTED' || entry.loadingFailed?.canceled !== true ||
      entry.status !== undefined || entry.responseReceivedAt !== undefined) return false;
  const evidence = nativeAbortProbeEvidenceForRequest(entry, report.nativeAbortProbeEvents);
  entry.abortControllerProbeEvidence = evidence;
  const proven = evidence.find(candidate => {
    const request = candidate.request;
    const before = candidate.ownerDomAtFetch;
    const after = candidate.ownerDomAtAbort;
    const rule = nativeAbortDomOwnerRules.find(item => item.owner === before?.ruleOwner && item.endpoint === request?.endpoint);
    return candidate.networkRequestId === entry.requestId &&
      candidate.networkFailureObservedSeparately === true &&
      Number.isFinite(candidate.networkFailureObservedAt) && candidate.controllerAbortedAt <= candidate.networkFailureObservedAt &&
      candidate.signalWasAlreadyAborted === false && candidate.exactRequestSignalCorrelation === true &&
      request?.requestId === entry.requestCorrelationId && request.path === entry.path && request.method === entry.method &&
      rule && before?.status === 'unique' && before.connectedAtFetch === true && Boolean(before.anchorId) &&
      after?.anchorId === before.anchorId && after.connectedAtAbort === false && after.detachedAtAbort === true &&
      candidate.ownerRetirementAction?.isTrusted === true && candidate.ownerRetirementAction.type === 'click' &&
      ['native-event-isTrusted-true', 'trusted-interaction-list-membership'].includes(candidate.ownerRetirementAction.trustEvidence);
  });
  if (!proven) return false;
  const ownerRetirement = {
    ownerRetired: true,
    reason: 'same-document-owner-detached-after-trusted-Playwright-action-and-exact-AbortController-signal',
    owner: proven.ownerDomAtFetch.ruleOwner,
    endpoint: proven.request.endpoint,
    requestCorrelationId: entry.requestCorrelationId,
    requestId: entry.requestId,
    componentAnchor: proven.ownerDomAtFetch.selector,
    componentAnchorId: proven.ownerDomAtFetch.anchorId,
    componentAnchorCapturedAt: proven.ownerDomAtFetch.capturedAt,
    componentAnchorDetachedAtAbort: proven.controllerAbortedAt,
    componentAbortControllerId: proven.controllerId,
    componentAbortAt: proven.controllerAbortedAt,
    trustedActionAt: proven.ownerRetirementAction.at,
    trustedActionLabel: proven.ownerRetirementAction.closestButton?.accessibleLabel,
    networkFailureAt: proven.networkFailureObservedAt,
    evidence: 'Playwright requestfailed event plus page-side trusted-event/AbortController/DOM-owner evidence',
  };
  entry.ownerRetirement = ownerRetirement;
  entry.expectedOwnerCancellation = { expected: true, reason: ownerRetirement.reason, ownerRetirement };
  cda.expectCanceledRequest(nativeByRequestId.get(entry.requestId), ownerRetirement.reason, {
    requestId: entry.requestId,
    requestCorrelationId: entry.requestCorrelationId,
    action: proven.ownerRetirementAction,
    detachedOwner: ownerRetirement.owner,
    probeEvidence: evidence,
  });
  report.expectedOwnerCancellations.push({
    requestId: entry.requestId, requestCorrelationId: entry.requestCorrelationId, path: entry.path,
    owner: ownerRetirement.owner, loadingFailed: entry.loadingFailed, ownerRetirement,
    networkTerminal: entry.networkTerminal, bodyReadStatus: entry.bodyReadStatus, complete: entry.complete,
  });
  const error = report.errors.find(item => item.kind === 'native-request' && item.requestId === entry.requestId);
  if (error) { error.expectedOwnerCancellation = true; error.ownerRetirement = ownerRetirement; }
  return true;
};
const startNativeCapture = async () => {
  await nativePage.context().exposeBinding('__loomNativeAbortProbeBinding', (_source, payload) => {
    let event;
    try { event = JSON.parse(payload); }
    catch { report.nativeAbortProbeEvents.push({ kind: 'probe-payload-invalid', payloadLength: String(payload).length }); return; }
    report.nativeAbortProbeEvents.push(event);
    notifyNativeRequestChange();
  });
  await nativePage.context().addInitScript(createNativeAbortProbeSource({ project, explorer }));
  nativePage.on('framenavigated', frame => {
    if (frame !== nativePage.mainFrame()) return;
    let url;
    try { url = new URL(frame.url()); } catch { return; }
    if (url.origin !== new URL(uiOrigin).origin) return;
    report.frameNavigations.push({
      frameId: 'playwright-main-frame', isMainFrame: true, at: Date.now(), path: url.pathname,
      project: url.searchParams.get('project'), explorer: url.searchParams.get('explorer'), mode: url.searchParams.get('mode'),
      identitySource: 'Playwright Frame.url; loader ID is not exposed by Playwright',
    });
  });
  nativePage.on('request', request => {
    const url = requestURL(request);
    if (!url) return;
    if (url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
    const owned = isOwnedNativeRequest(request);
    if (!owned) return;
    const headers = request.headers();
    let body;
    try { if (request.postData() !== null) body = request.postDataJSON(); }
    catch { body = parseSanitizedBody(request.postData() ?? ''); }
    const browserRequestId = `playwright-${nextNativeRequestId++}`;
    nativeByRequestId.set(browserRequestId, request);
    const entry = {
      requestId: browserRequestId,
      browserRequestId,
      method: request.method(),
      url: `${url.origin}${url.pathname}`,
      origin: url.origin,
      path: url.pathname,
      query: parseSanitizedBody(JSON.stringify(Object.fromEntries(url.searchParams))),
      request: body,
      requestCorrelationId: headers['x-request-id'],
      scopeProject: project,
      scopeExplorer: explorer,
      resourceType: request.resourceType() === 'fetch' ? 'Fetch' : request.resourceType(),
      startedAt: Date.now(),
      status: undefined,
      response: undefined,
      responseReceivedAt: undefined,
      networkTerminal: false,
      bodyReadStatus: 'pending',
      complete: false,
      transport: 'Playwright Request object identity',
    };
    nativeByRequest.set(request, entry);
    report.nativeRequests.push(entry);
    notifyNativeRequestChange();
  });
  nativePage.on('response', response => {
    const request = response.request();
    const entry = nativeByRequest.get(request);
    const url = requestURL(request);
    if (!entry && (!url || !appOrigins.has(url.origin))) return;
    const status = response.status();
    if (!entry && status >= 400) {
      const failure = { kind: 'http', status, path: url.pathname, origin: url.origin };
      if (url.pathname.endsWith('/favicon.ico') && status === 404) report.assetFailures.push(failure);
      else report.errors.push(failure);
      return;
    }
    if (!entry) return;
    entry.status = status;
    entry.responseReceivedAt = Date.now();
    entry.networkTerminal = true;
    entry.bodyReadStatus = 'reading';
    const read = (async () => {
      try {
        const body = await response.text();
        try { nativeProtocolResponses.set(entry, JSON.parse(body)); } catch { /* Keep malformed response text only in the bounded diagnostic projection. */ }
        entry.response = parseSanitizedBody(body);
        entry.bodyReadStatus = 'decoded';
        entry.complete = true;
        if (status >= 400) {
          const expectedValidation = expectedHttpValidation({ ...entry, response: protocolResponse(entry) });
          report.errors.push({ kind: 'native-http', requestId: entry.requestId,
            requestCorrelationId: entry.requestCorrelationId, path: entry.path, status,
            response: entry.response, expectedValidation });
        }
      } catch (error) {
        entry.bodyReadStatus = 'failed';
        entry.bodyError = sanitizeText(error?.message ?? error);
        report.errors.push({ kind: 'native-response-body', requestId: entry.requestId,
          requestCorrelationId: entry.requestCorrelationId, path: entry.path, status, message: entry.bodyError });
      } finally {
        entry.completedAt = Date.now();
        notifyNativeRequestChange();
      }
    })();
    nativeResponseReads.track(read, {
      phase: 'native-api-response-body',
      browserRequestId: entry.browserRequestId,
      requestId: entry.requestCorrelationId ?? entry.requestId,
      requestCorrelationId: entry.requestCorrelationId,
      method: entry.method,
      path: entry.path,
      status,
    });
  });
  nativePage.on('requestfailed', request => {
    const failure = request.failure()?.errorText ?? 'unknown request failure';
    const entry = nativeByRequest.get(request);
    if (entry) {
      entry.networkTerminal = true;
      entry.bodyReadStatus = 'failed';
      entry.bodyError = `Playwright requestfailed: ${failure}`;
      entry.loadingFailed = { errorText: failure, canceled: failure === 'net::ERR_ABORTED', at: Date.now(), clockBasis: 'playwright-requestfailed-wall-clock' };
      entry.complete = true;
      entry.completedAt = Date.now();
      const error = { kind: 'native-request', requestId: entry.requestId,
        requestCorrelationId: entry.requestCorrelationId, path: entry.path,
        message: entry.bodyError, canceled: entry.loadingFailed.canceled };
      report.errors.push(error);
      annotateExpectedOwnerCancellation(entry);
    } else {
      const url = requestURL(request);
      if (url && appOrigins.has(url.origin)) report.errors.push({ kind: request.resourceType() === 'script' ? 'module' : 'network',
        path: url.pathname, method: request.method(), message: sanitizeText(failure) });
    }
    notifyNativeRequestChange();
  });
  nativePage.on('pageerror', error => report.errors.push({ kind: 'runtime', message: sanitizeText(error.message) }));
};
const settleNativeResponses = async (timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await nativeResponseReads.flush({
        timeoutMs: Math.max(1, deadline - Date.now()),
        label: 'native API response body reads',
      });
    } catch (error) {
      for (const entry of report.nativeRequests) annotateExpectedOwnerCancellation(entry);
      const pending = report.nativeRequests.filter(entry => !entry.networkTerminal || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
      const responseReadError = sanitizeText(error?.message ?? error);
      const timedOut = responseReadError.includes('Timed out flushing native API response body reads');
      report.nativeResponseDrain = {
        status: timedOut ? 'timed-out' : 'failed',
        timeoutMs,
        pending: pending.map(pendingNativeEvidence),
        responseReadError,
        expectedOwnerCancellations: report.expectedOwnerCancellations.map(({ requestId, requestCorrelationId, path, owner, loadingFailed }) =>
          ({ requestId, requestCorrelationId, path, owner, loadingFailed })),
      };
      throw new Error(`${timedOut ? 'Timed out' : 'Failed'} draining native API responses: ${JSON.stringify(report.nativeResponseDrain)}`);
    }
    for (const entry of report.nativeRequests) annotateExpectedOwnerCancellation(entry);
    const pending = report.nativeRequests.filter(entry => !entry.networkTerminal || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
    if (pending.length === 0) {
      const unexpectedIncomplete = report.nativeRequests.filter(entry => entry.bodyReadStatus !== 'decoded' && entry.expectedOwnerCancellation?.expected !== true);
      if (unexpectedIncomplete.length) {
        report.nativeResponseDrain = {
          status: 'failed',
          timeoutMs,
          decodedBodies: report.nativeRequests.filter(entry => entry.bodyReadStatus === 'decoded').length,
          failedBodies: unexpectedIncomplete.map(pendingNativeEvidence),
          expectedOwnerCancellations: report.expectedOwnerCancellations.map(({ requestId, requestCorrelationId, path, owner, loadingFailed }) =>
            ({ requestId, requestCorrelationId, path, owner, loadingFailed })),
        };
      }
      assert.deepEqual(unexpectedIncomplete, [],
        'Every captured native API response must decode or match exact trusted same-document owner retirement evidence');
      report.nativeResponseDrain = {
        status: 'complete',
        decodedBodies: report.nativeRequests.filter(entry => entry.bodyReadStatus === 'decoded').length,
        retiredIncompleteRequests: [],
        expectedOwnerCancellations: report.expectedOwnerCancellations.map(({ requestId, requestCorrelationId, path, owner, loadingFailed }) =>
          ({ requestId, requestCorrelationId, path, owner, loadingFailed })),
        ownershipTransport: 'Playwright Request identity and public lifecycle events; no loader/context ownership inferred',
      };
      return;
    }
    await waitForNativeRequestChange(Math.max(1, Math.min(100, deadline - Date.now())));
  }
  const pending = report.nativeRequests.filter(entry => !entry.networkTerminal || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
  report.nativeResponseDrain = { status: 'timed-out', timeoutMs, pending: pending.map(pendingNativeEvidence),
    expectedOwnerCancellations: report.expectedOwnerCancellations.map(({ requestId, path, owner, loadingFailed }) => ({ requestId, path, owner, loadingFailed })) };
  throw new Error(`Timed out draining native API responses: ${JSON.stringify(pending.map(pendingNativeEvidence))}`);
};
const waitNative = async (predicate, fromIndex = 0, timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const match = findCompletedNativeResponse(report.nativeRequests, protocolResponse, predicate, fromIndex);
    if (match) return match;
    await waitForNativeRequestChange(Math.max(1, Math.min(100, deadline - Date.now())));
  }
  throw new Error(`Timed out waiting for native request: ${JSON.stringify(report.nativeRequests.slice(fromIndex).map(({ method, url, path, query, status, request, bodyReadStatus, bodyError }) => ({ method, url, path, query, status, request, bodyReadStatus, bodyError })))}`);
};
const nativeRequestValue = (entry, key) => entry.request?.[key] ?? entry.query?.[key];
const inspectPage = (page, inspect, argument) => page.evaluate(inspect, argument);
const waitForObservable = (page, predicate, argumentOrTimeout, timeoutArgument = 5000) => {
  const argument = typeof argumentOrTimeout === 'number' ? undefined : argumentOrTimeout;
  const timeout = Math.min(5000, typeof argumentOrTimeout === 'number' ? argumentOrTimeout : timeoutArgument);
  return page.waitForFunction(predicate, argument, { timeout }).then(() => undefined);
};
const waitForVisible = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'visible', timeout: Math.min(5000, timeout) });
const waitForHidden = (page, selector, timeout = 5000) => page.locator(selector).waitFor({ state: 'hidden', timeout: Math.min(5000, timeout) });
const gotoPage = (_page, url) => cda.navigate(url);
const clickNative = (page, selector, identity = {}) => {
  const locator = identity.name !== undefined
    ? page.getByRole('button', { name: identity.name, exact: true })
    : identity.includes !== undefined
      ? page.getByRole('button', { name: new RegExp(identity.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') })
      : page.locator(selector);
  return cda.action(`click ${identity.name ?? identity.includes ?? selector}`, locator,
    target => target.click({ timeout: 5000 }), { timeout: 5000 });
};
const selectNative = async (page, selector, value) => {
  const locator = page.locator(selector);
  await cda.action(`select ${selector}`, locator, target => target.selectOption(value, { timeout: 5000 }), { timeout: 5000 });
  assert.equal(await locator.inputValue(), String(value), `Selected value must be applied to ${selector}`);
};
const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};
const rendered = async (expectedRows) => {
  const columnCount = expectedRows[0]?.length ?? 2;
  await waitForObservable(nativePage, ({ columnCount }) => {
    const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-colcount')===String(columnCount)&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:');
  }, { columnCount }, 5000);
  const rows = await inspectPage(nativePage, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
    .slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length));
  assert(rows.length > 0 || expectedRows.length === 0);
  for (const row of rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `Visible row is not in the independent CDA witness: ${JSON.stringify(row)}`);
};
const openTable = async (expectedRows, name) => {
  const started = Date.now();
  const fromIndex = report.nativeRequests.length;
  await gotoPage(nativePage, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForVisible(nativePage, `[data-testid="construction-table-${outputId}"]`, 5000);
  await clickNative(nativePage, `[data-testid="construction-table-${outputId}"]`);
  await rendered(expectedRows);
  const preview = await waitNative((entry, response) => entry.path.endsWith('/preview') &&
    entry.request?.outputId === outputId && response !== undefined, fromIndex);
  assert.equal(preview.status, 200, `${name} native Preview must complete successfully: ${JSON.stringify(preview.response)}`);
  const response = protocolResponse(preview);
  assert(response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: response.receiptId, rowCount: response.rowCount });
  return response;
};
const proposal = async (name, started, expectedRows, fromIndex) => {
  const adopted = await waitNative((entry, response) => entry.startedAt >= started && entry.path.endsWith('/construction-proposals') && response?.proposalId, fromIndex, 5000);
  const response = protocolResponse(adopted);
  await waitForObservable(nativePage, ({ proposalId }) => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready','error','needs-repair'].includes(panel?.dataset.proposalStatus) && panel?.dataset.proposalId === proposalId;
  }, { proposalId: response.proposalId }, Math.max(1, started + 5000 - Date.now()));
  const value = await inspectPage(nativePage, () => {
    const panel=document.querySelector('[data-testid="construction-proposal-panel"]');
    return {proposalId:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus,text:panel?.innerText,
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};
  });
  assert.equal(value.status, 'ready', `${name}: ${value.text}`);
  assert.equal(value.rows.length, Math.min(25, expectedRows.length));
  for (const row of value.rows) assert(expectedRows.some((expected) => JSON.stringify(expected) === JSON.stringify(row)), `${name} differs from the raw CDA witness: ${JSON.stringify(row)}`);
  record(name, started, { proposalId: response.proposalId, rows: value.rows });
  return value;
};
const applyProposal = async (expectedRows, name) => {
  const started = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  await rendered(expectedRows);
  record(name, started);
  builder = await api(`${base}/builder`);
};
const waitRelatedProposal = async (name, started, expectedRequestPolicy, fromIndex) => {
  const entry = await waitNative((candidate, response) => candidate.startedAt >= started && response !== undefined &&
    selectedRelatedSourceProposal({ ...candidate, response }), fromIndex);
  const response = protocolResponse(entry);
  const match = selectedRelatedSourceProposal({ ...entry, response });
  assert(match, 'The native candidate must contain the exact selected Observation related source');
  assert.equal(match.related.form, 'ALL', 'RELATED_SOURCE must retain the all-matching Observation result form');
  assert.equal(match.related.contributorRule?.policy, 'ALL_MATCHES', 'RELATED_SOURCE must retain all matching records on the selected route');
  assert.equal(match.related.choiceId, report.currentRelatedChoiceId,
    'The related proposal must retain the exact signed candidate and route inspected from the authorized catalog');
  assert.equal(match.related.source.nodeId, report.relatedFieldCandidate.nodeId,
    'The related proposal must retain the exact selected Observation node');
  assert.equal(match.rowValuePolicy, expectedRequestPolicy,
    `The grouped-row selector requested ${expectedRequestPolicy}, but the native RELATED_SOURCE proposal encoded ${match.rowValuePolicy}`);
  assert.equal(match.related.source.logicalType, logicalTypeExpected);
  if (cardinalityExpected) assert.equal(match.related.source.cardinality, cardinalityExpected);
  assert.equal(match.related.route.length, expectedRelatedRouteTypes.length - 1);
  assert(match.related.route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
    hop.toResourceType === expectedRelatedRouteTypes[index + 1]),
  `The related source proposal lost exact route ${expectedRelatedRouteTypes.join(' → ')}`);
  if (zeroObservationMode) assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The zero-match proposal must use the exact inbound Patient <-[subject_Patient]- Observation route');
  if (referenceFieldMode) assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The nullable-reference repair must retain the exact Specimen→Patient→Observation subject route');
  record(name, started, { status: entry.status, responseStatus: response?.previewStatus,
    previewDurationMs: response?.previewDurationMs, relatedSourceForm: match.related.form,
    requestedRowValuePolicy: expectedRequestPolicy, relatedSourceRowValuePolicy: match.related.rowValuePolicy ?? 'ALL',
    proposalId: response?.proposalId });
  return entry;
};
const waitAdoptedChoicePreview = async (entry, name) => {
  const proposalId = protocolResponse(entry)?.proposalId;
  assert(proposalId, `${name} response has no construction proposal ID`);
  await waitForObservable(nativePage, ({ proposalId }) => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.dataset.proposalStatus === 'ready' && panel?.dataset.proposalId === proposalId;
  }, { proposalId }, 5000);
  const active = await inspectPage(nativePage, () => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { status: panel?.dataset.proposalStatus, proposalId: panel?.dataset.proposalId };
  });
  assert.deepEqual(active, { status: 'ready', proposalId }, `${name} must be the active native construction proposal`);
  return proposalId;
};
const expand = async (hop, witnesses, expectedRows, sourcePopulationBaseline) => {
  await clickNative(nativePage, '[data-testid="construction-rows-settings-trigger"]');
  await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false, 5000);
  await clickNative(nativePage, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="Related record type"]')?.disabled === false, 5000);
  const started = Date.now();
  await selectNative(nativePage, `${panel} select[aria-label="Related record type"]`, hop.to);
  const label = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
  const relationshipChoice = nativePage.locator(`${panel} input[aria-label=${JSON.stringify(label)}]`);
  await relationshipChoice.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await relationshipChoice.count(), 1, 'The relationship choice must be unique');
  const proposalFromIndex = report.nativeRequests.length;
  const proposalStarted = Date.now();
  await clickNative(nativePage, `${panel} input[aria-label="${label}"]`);
  let rowsFromProposal = expectedRows;
  let zeroExpandEvidence;
  let zeroExpansionStepID;
  if (zeroObservationMode) {
    const expansionEntry = await waitNative((entry, response) => entry.startedAt >= proposalStarted &&
      entry.path.endsWith('/construction-proposals') && response?.proposalId, proposalFromIndex);
    const expansionResponse = protocolResponse(expansionEntry);
    const expansionStep = expansionEntry.request?.candidateConstruction?.steps?.find((step) =>
      step.operation?.kind === 'RELATED_EXPAND' && step.operation.relatedExpand?.targetResourceType === 'Observation');
    assert(expansionStep, 'The direct Patient-root proposal must contain the selected Observation expansion');
    zeroExpansionStepID = expansionStep.id;
    assert.equal(expansionStep.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT',
      'The zero-match lifecycle must use the current native RelatedExpand empty policy');
    const expansionOutput = expansionStep.outputs?.find((output) =>
      output.id === expansionStep.operation.relatedExpand.relatedRecordColumnId);
    assert(expansionOutput, 'The PRESERVE_PARENT proposal must expose its nullable Observation identity output');
    const preview = expansionResponse?.preview;
    assert.equal(preview?.rowCount, 1, 'The native PRESERVE_PARENT proposal must retain the exact zero-match Patient row');
    assert.equal(preview?.rows?.length, 1, 'The zero-match proposal must return the retained parent row');
    const parentColumn = preview.columns.find((column) => column.label === 'Patient ID');
    assert(parentColumn, 'The proposal must retain the native Patient identity column');
    assert.equal(preview.rows[0][parentColumn.column], witnesses[0].patient.id,
      'The native proposal must preserve the exact selected Patient under its current empty policy');
    assert.equal(preview.rows[0][expansionOutput.name], null,
      'PRESERVE_PARENT must represent the unmatched related Observation identity as a null value');
    rowsFromProposal = preview.rows.map((row) => preview.columns.map((column) =>
      row[column.column] === null || row[column.column] === undefined ? '—' : String(row[column.column])));
    assert.deepEqual(rowsFromProposal, expectedRows,
      'The native proposal rows must agree with the independently reread Patient root and zero-edge oracle');
    zeroExpandEvidence = { emptyPolicy: expansionStep.operation.relatedExpand.emptyPolicy,
      previewRowCount: preview.rowCount, unmatchedObservationOutputIsNull: true };
  }
  const value = await proposal(`expand-${hop.from}-${hop.to}-preview`, proposalStarted, rowsFromProposal, proposalFromIndex);
  assert(report.nativeRequests.slice(proposalFromIndex).some((entry) => protocolResponse(entry)?.proposalId === value.proposalId), 'The displayed expansion preview must match its captured native proposal');
  report.cases.at(-1).witnessCount = witnesses.length;
  await applyProposal(rowsFromProposal, `expand-${hop.from}-${hop.to}-apply-to-render`);
  if (zeroObservationMode) {
    const savedExpansion = doc().construction.steps.find((step) => step.id === zeroExpansionStepID);
    assert.equal(savedExpansion?.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT',
      'Applying the zero-match expansion must save the proposal’s PRESERVE_PARENT policy');
    assert.deepEqual(doc().population, sourcePopulationBaseline,
      'Applying the zero-match expansion must preserve the exact one-Patient source population');
    report.zeroObservationExpansion = { ...zeroExpandEvidence, savedPolicy: savedExpansion.operation.relatedExpand.emptyPolicy };
  }
};
const openRelatedFieldChooser = async () => {
  const alreadyOpen = await nativePage.locator('[aria-label="Add columns editor"]').count() === 1;
  if (!alreadyOpen) {
    await clickNative(nativePage, '[data-testid="construction-action-add-columns"]');
    await clickNative(nativePage, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  }
  await waitForVisible(nativePage, '[data-testid="construction-add-columns-source"]', 5000);
  const relatedResourcesOpen = await nativePage.locator('[aria-label="Related resources"]').evaluate(node => node.open);
  if (!relatedResourcesOpen) {
    await clickNative(nativePage, '[aria-label="Related resources"] summary');
    await waitForObservable(nativePage, () => document.querySelector('[aria-label="Related resources"]')?.open === true, 5000);
  }
  await clickNative(nativePage, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  const rawFieldsOpen = await nativePage.locator('[data-testid="feature-catalog-raw-fields"]').evaluate(node => node.open);
  if (!rawFieldsOpen) {
    await clickNative(nativePage, '[data-testid="feature-catalog-raw-fields"] > summary');
    await waitForObservable(nativePage, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true, 5000);
  }
  const choiceSearchFrom = report.nativeRequests.length;
  const fieldSelector = `input[aria-label=${JSON.stringify(`Select Observation.${relatedFieldPath}`)}]`;
  const fieldControl = nativePage.locator(fieldSelector);
  await fieldControl.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await fieldControl.count(), 1, 'The related field option must be unique');
  await fieldControl.waitFor({ state: 'attached', timeout: 5000 });
  assert.equal(await fieldControl.isEnabled(), true, 'The related field option must be enabled');
  await clickNative(nativePage, fieldSelector);
  await clickNative(nativePage, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForVisible(nativePage, '[role="dialog"]', 5000);
  const otherPathDisclosure = nativePage.locator('[role="dialog"] summary').filter({ hasText: 'Other relationship paths' });
  if (await otherPathDisclosure.count() > 0) {
    await cda.action('click Other relationship paths', otherPathDisclosure,
      target => target.click({ timeout: 5000 }), { timeout: 5000 });
  }
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  await waitForVisible(nativePage, routeSelector, 5000);
  await clickNative(nativePage, routeSelector);
  await nativePage.locator(routeSelector).waitFor({ state: 'visible', timeout: 5000 });
  await waitForObservable(nativePage, ({ selector }) => document.querySelector(selector)?.checked === true, { selector: routeSelector }, 5000);
  const formLabel = `${relatedChoiceLabel}: Keep all matching values`;
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForVisible(nativePage, formSelector, 5000);
  await clickNative(nativePage, formSelector);
  const control = await inspectPage(nativePage, () => {
    const dialog = document.querySelector('[role="dialog"]');
    const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
    return { dialog: Boolean(dialog), policyOptions: policy ? [...policy.options].map(option => ({ value: option.value, label: option.textContent })) : [], policy: policy?.value };
  });
  assert(control.dialog, 'Related field ONE/ALL chooser is not open');
  assert.deepEqual(control.policyOptions.map((option) => option.value), ['ALL', 'ONE'], 'The current Add columns chooser does not expose grouped-row ONE and ALL');
  const candidateIds = new Set(report.relatedFieldCandidates?.map((candidate) => candidate.candidateId));
  assert(candidateIds.size > 0, 'The related field candidate identity must be captured before opening related-choice search');
  const matchesSelectedRoute = (choice) => {
    const source = choice?.source;
    const route = choice?.route;
    const candidate = report.relatedFieldCandidates.find((item) => item.candidateId === source?.candidateId);
    return source?.kind === 'FIELD' && candidateIds.has(source.candidateId) && candidate?.nodeId === source.nodeId &&
      source.resourceType === 'Observation' && source.path === relatedFieldPath &&
      Array.isArray(route) && route.length === expectedRelatedRouteTypes.length - 1 &&
      route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
        hop.toResourceType === expectedRelatedRouteTypes[index + 1]) &&
      (!zeroObservationMode || JSON.stringify(route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
        ({ fromResourceType, toResourceType, relationship, storageDirection }))) === JSON.stringify(expectedPatientObservationRoute)) &&
      (!referenceFieldMode || JSON.stringify(route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
        ({ fromResourceType, toResourceType, relationship, storageDirection }))) === JSON.stringify(expectedReferenceRoute));
  };
  const catalogEntry = await waitNative((entry, response) => entry.path.endsWith('/construction-choices') &&
    candidateIds.has(entry.request?.source?.candidateId) &&
    response?.choices?.some(matchesSelectedRoute), choiceSearchFrom);
  const catalogResponse = protocolResponse(catalogEntry);
  assert.equal(catalogEntry.status, 200, JSON.stringify(catalogEntry.response));
  assert.equal(catalogEntry.request.outputId, outputId, 'The selected route must come from this owned output’s current catalog request');
  assert.equal(catalogEntry.request.snapshotToken, builder.catalog.snapshotToken, 'The selected route must come from the current pinned catalog snapshot');
  const selectedChoice = catalogResponse.choices.find(matchesSelectedRoute);
  assert(selectedChoice?.choiceId, 'The current authorized field catalog must contain the exact selected relationship route');
  assert(selectedChoice.options?.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED'),
    'The exact signed route choice must support preserving all matching records');
  const candidate = report.relatedFieldCandidates.find((item) => item.candidateId === selectedChoice.source.candidateId);
  assert(candidate, 'The selected related source must be one of the current catalog candidates');
  assert.equal(selectedChoice.source.nodeId, candidate.nodeId);
  assert.equal(selectedChoice.source.resourceType, 'Observation');
  assert.equal(selectedChoice.source.path, relatedFieldPath);
  if (zeroObservationMode) assert.deepEqual(selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The authorized catalog choice must represent the exact incoming typed Observation edge');
  else if (!basicMode) assert.deepEqual(selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The selected catalog choice must preserve the exact Specimen→Patient→Observation subject route');
  report.relatedFieldCandidate = candidate;
  report.relatedChoiceAssertions.push({ candidateId: candidate.candidateId, choiceId: selectedChoice.choiceId,
    resourceType: selectedChoice.source.resourceType, path: selectedChoice.source.path,
    logicalType: candidate.logicalType, cardinality: candidate.cardinality, form: 'ALL',
    supportedForms: selectedChoice.options.filter((option) => option.support === 'SUPPORTED').map((option) => option.form),
    route: selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
      ({ fromResourceType, toResourceType, relationship, storageDirection })),
    choiceRequest: { status: catalogEntry.status, outputId: catalogEntry.request.outputId,
      snapshotToken: catalogEntry.request.snapshotToken, truncated: catalogResponse.truncated,
      complete: catalogResponse.complete } });
  report.currentRelatedChoiceId = selectedChoice.choiceId;
  assert(report.relatedFieldCandidate?.candidateId && report.currentRelatedChoiceId,
    'The selected related field must be bound to its authorized candidate and signed route choice');
  report.groupedRowPolicyControl = control;
};
const selectRelatedPolicy = async (policy) => {
  await selectNative(nativePage, '[role="dialog"] select[aria-label="Values per grouped row"]', policy);
  const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
  const state = await inspectPage(nativePage, ({ routeSelector, formSelector }) => {
    const dialog = document.querySelector('[role="dialog"]');
    return { policy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value,
      routeChecked: dialog?.querySelector(routeSelector)?.checked, formChecked: dialog?.querySelector(formSelector)?.checked };
  }, { routeSelector, formSelector });
  assert.equal(state.policy, policy);
  assert.equal(state.routeChecked, true, 'The exact selected Observation relationship path was lost');
  assert.equal(state.formChecked, true, 'Per-record matching Observation values must remain ALL');
  return state;
};
const clickAddRelated = async () => {
  await clickNative(nativePage, '[role="dialog"] button', { name: 'Add 1 column' });
};
const cancelColumnProposal = async () => {
  await clickNative(nativePage, '[data-testid="construction-cancel-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
};
const readPreviewTable = async (expectedRows, expectedColumns, name) => {
  const started = Date.now();
  const nativeFrom = report.nativeRequests.length;
  await gotoPage(nativePage, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForVisible(nativePage, `[data-testid="construction-table-${outputId}"]`, 5000);
  await clickNative(nativePage, `[data-testid="construction-table-${outputId}"]`);
  await waitForObservable(nativePage, ({ expectedColumns }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-colcount') === String(expectedColumns) &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
  }, { expectedColumns }, 5000);
  const dom = await inspectPage(nativePage, () => {
    const area = document.querySelector('[data-testid="preview-table-scroll"]');
    const table = area?.querySelector('[role="table"]');
    return { headers: [...(area?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: [...(area?.querySelectorAll('[role="row"]') ?? [])].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
      columnCount: table?.getAttribute('aria-colcount') };
  });
  assert(dom.rows.length > 0 || expectedRows.length === 0, `${name} did not render a witness row: ${JSON.stringify(dom)}`);
  for (const row of dom.rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `${name} visible rows differ from the raw witnesses: ${JSON.stringify(row)}`);
  const preview = await waitNative((entry, response) => entry.path.endsWith('/preview') &&
    entry.request?.outputId === outputId && response !== undefined, nativeFrom);
  assert.equal(preview.status, 200, `${name} native Preview must complete successfully: ${JSON.stringify(preview.response)}`);
  const response = protocolResponse(preview);
  assert(response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: response.receiptId, headers: dom.headers, nativeRowCount: response.rowCount, mountedRowCount: dom.rows.length });
  return response;
};

try {
  assert.notEqual(explorer, protectedExplorer, 'Only a fresh QA Explorer may be used');
  apiBuildCheckStarted = true;
  report.apiBuildFreeze = { target: 'running local API build stamp', container: apiBuildContainer, invalidatesRun: true, productFailure: false };
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiBuildContainer));
  report.apiBuildFreeze.initial = frozenApiBuild.initial;
  let witnesses;
  let selectedMembers;
  let manyWitness;
  let rereadZeroObservationWitness;
  if (zeroObservationMode) {
    const patientCandidateLimit = 2000;
    const zeroWitnessQuery = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)}
      AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
      AND scopedPatient.resourceType == "Patient"
      AND scopedPatient.payload.resourceType == "Patient"
    SORT scopedPatient.id
    LIMIT ${patientCandidateLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET matchingObservations = (
    FOR edge IN fhir_edge
      FILTER edge._to == p._id AND edge.from_type == "Observation" AND edge.to_type == "Patient"
        AND STARTS_WITH(edge._from, "Observation/") AND edge.label == "subject_Patient"
        AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
      LET observation = DOCUMENT(edge._from)
      FILTER observation != null AND observation.project == ${JSON.stringify(project)}
        AND observation.dataset_generation == ${JSON.stringify(generation)}
        AND observation.resourceType == "Observation"
        AND observation.payload.resourceType == "Observation"
      COLLECT observationKey = observation._id
      RETURN observationKey
  )
  FILTER LENGTH(matchingObservations) == 0
  SORT p.id
  LIMIT 1
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) } }
`;
    report.oracle = {
      kind: 'bounded scoped Patient finder for no incoming typed Observation.subject_Patient edge, followed by an exact selected-Patient reread',
      searchBounds: {
        patientCandidates: { project, generation, sortedBy: 'id', limit: patientCandidateLimit },
        edge: { fromResourceType: 'Observation', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
        targetDocumentScope: { project, generation },
        authorizationScope: 'local unrestricted project/generation reads; no restricted-auth claim',
        missingWitnessMeaning: 'No witness in this bounded candidate scan is unavailability, not proof of global absence.',
      },
      fixtureAvailability: { zeroIncomingObservationWitnessAvailable: false, patientCandidateLimit },
      missingWitnessCategories: [],
    };
    const [zeroSeed] = rawQuery(zeroWitnessQuery);
    if (!zeroSeed) {
      const meaning = `No Patient with zero scoped incoming Observation.subject_Patient edges was found among the first ${patientCandidateLimit} current-generation Patient candidates. This is bounded witness unavailability, not proof of global absence.`;
      report.oracle.fixtureAvailability.meaning = meaning;
      report.oracle.missingWitnessCategories.push({ category: 'zero-observation', patientCandidateLimit, meaning });
      throw new Error(meaning);
    }

    const exactZeroMembershipQuery = `
LET patientKey = ${JSON.stringify(zeroSeed.patient._id)}
LET patient = DOCUMENT(patientKey)
FILTER patient != null AND patient._id == patientKey
  AND patient.id == ${JSON.stringify(zeroSeed.patient.id)}
  AND patient.project == ${JSON.stringify(project)}
  AND patient.dataset_generation == ${JSON.stringify(generation)}
  AND patient.resourceType == "Patient"
  AND patient.payload.resourceType == "Patient"
LET matchingObservations = (
  FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND edge.from_type == "Observation" AND edge.to_type == "Patient"
      AND STARTS_WITH(edge._from, "Observation/") AND edge.label == "subject_Patient"
      AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
    LET observation = DOCUMENT(edge._from)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.resourceType == "Observation"
      AND observation.payload.resourceType == "Observation"
    COLLECT observationKey = observation._id
    RETURN observationKey
)
RETURN {
  patient: { id: patient.id, _id: patient._id, reference: CONCAT("Patient/", patient.id) },
  matchingObservationCount: LENGTH(matchingObservations)
}
`;
    const [exactZero] = rawQuery(exactZeroMembershipQuery);
    assert(exactZero, 'The exact selected Patient and its project/generation scope must survive the independent reread');
    assert.equal(exactZero.matchingObservationCount, 0,
      'The independent exact Patient reread found a scoped incoming Observation.subject_Patient match');
    assert.equal(exactZero.patient.id, zeroSeed.patient.id, 'The bounded finder and exact Patient reread disagree');
    assert.equal(exactZero.patient._id, zeroSeed.patient._id, 'The exact Patient identity changed between finder and reread');
    assert.equal(exactZero.patient.reference, zeroSeed.patient.reference, 'The exact Patient reference changed between finder and reread');

    const witness = {
      category: 'zero-observation', patient: exactZero.patient,
      members: [{ id: exactZero.patient.id, _id: exactZero.patient._id,
        patientReference: exactZero.patient.reference, patient: exactZero.patient }],
      observationIds: [], observationKeys: [], observationCount: 0,
      observationStatuses: [], distinctStatusValues: [], expectedContributorRows: 1,
    };
    witnesses = [witness];
    selectedMembers = witness.members;
    rereadZeroObservationWitness = () => rawQuery(exactZeroMembershipQuery);
    Object.assign(report.oracle, {
      exactMembershipScope: { project, generation, rootResourceType: 'Patient', selectedRootCount: 1,
        incomingObservationEdgeCount: 0, exactRootRereadPerformed: true,
        authorizationScope: 'unrestricted local Arango scope' },
      fixtureAvailability: { zeroIncomingObservationWitnessAvailable: true, patientCandidateLimit },
      witnessSummary: { category: witness.category, rootResourceType: 'Patient', rootCount: 1,
        distinctIncomingObservationCount: 0 },
    });
  } else if (basicMode) {
    const patientCandidateLimit = 2000;
    const basicWitnessQuery = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)} AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
    SORT scopedPatient.id
    LIMIT ${patientCandidateLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
        AND IS_STRING(o.payload.status)
      SORT o.id
      RETURN { id: o.id, _id: o._id, status: o.payload.status }
  )
  FILTER LENGTH(observations) == 1
  SORT p.id
  LIMIT 1
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }
`;
    const [basicSeed] = rawQuery(basicWitnessQuery);
    report.oracle = {
      kind: 'bounded exact project/generation Patient→Observation fhir_edge witness plus an exact selected-Patient edge reread',
      searchBounds: { patientCandidates: { project, generation, sortedBy: 'id', limit: patientCandidateLimit },
        selectedObservationCount: 1, missingWitnessMeaning: 'No fixture witness in this bounded scan is unavailability, not global absence.' },
      requiredRelatedField: { resourceType: 'Observation', path: 'status', logicalType: 'code', cardinality: 'optional_one' },
      witnessQuery: basicWitnessQuery,
    };
    if (!basicSeed) {
      report.oracle.fixtureAvailability = { requiredSingleObservationWitnessAvailable: false, patientCandidateLimit };
      throw new Error(`No scoped Patient with exactly one linked Observation carrying a scalar status was found among the first ${patientCandidateLimit} current-generation Patients.`);
    }
    const patientKey = basicSeed.patient._id;
    const exactBasicMembershipQuery = `
FOR p IN Patient
  FILTER p._id == ${JSON.stringify(patientKey)} AND p.project == ${JSON.stringify(project)} AND p.dataset_generation == ${JSON.stringify(generation)}
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      SORT o.id
      RETURN { id: o.id, _id: o._id, status: o.payload.status }
  )
  RETURN { patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) }, observations }
`;
    const [exactBasic] = rawQuery(exactBasicMembershipQuery);
    assert(exactBasic, 'The selected basic Patient is no longer visible in the requested project/generation');
    assert.deepEqual(exactBasic.observations.map(({ _id }) => _id), basicSeed.observations.map(({ _id }) => _id),
      'The bounded finder and exact Patient→Observation edge reread disagree');
    assert.equal(exactBasic.observations.length, 1, 'The basic status case requires exactly one linked Observation');
    assert.equal(exactBasic.observations[0].status, basicSeed.observations[0].status,
      'The exact raw Observation.status value differs from the bounded finder');
    if (project === 'loom_dev_c89a69d7e137' && generation === 'fixture-v1') {
      const fixturePatients = await readFixtureNDJSON('../../../testdata/devloop-fixture/Patient.ndjson');
      const fixtureObservations = await readFixtureNDJSON('../../../testdata/devloop-fixture/Observation.ndjson');
      assert(fixturePatients.some((patient) => patient.id === exactBasic.patient.id),
        'The raw-selected Patient is absent from the checked-in devloop fixture');
      const fixtureStatuses = fixtureObservations
        .filter((observation) => observation.subject?.reference === exactBasic.patient.reference)
        .map(({ id, status }) => ({ id, status })).sort((left, right) => left.id.localeCompare(right.id));
      const databaseStatuses = exactBasic.observations.map(({ id, status }) => ({ id, status }))
        .sort((left, right) => left.id.localeCompare(right.id));
      assert.deepEqual(databaseStatuses, fixtureStatuses,
        'The exact raw Patient→Observation witness/status differs from the checked-in NDJSON fixture');
      report.oracle.literalFixtureCrossCheck = {
        fixture: 'testdata/devloop-fixture/Patient.ndjson + Observation.ndjson',
        patientId: exactBasic.patient.id, observations: fixtureStatuses,
      };
    } else {
      report.oracle.literalFixtureCrossCheck = { available: false, reason: 'The selected project/generation is not the default devloop fixture.' };
    }
    witnesses = [{
      category: 'one-observation', patient: exactBasic.patient,
      members: [{ id: exactBasic.patient.id, _id: exactBasic.patient._id, patientReference: exactBasic.patient.reference }],
      observationIds: exactBasic.observations.map((observation) => observation.id),
      observationStatuses: exactBasic.observations.map((observation) => observation.status),
      distinctStatusValues: [...new Set(exactBasic.observations.map((observation) => observation.status))],
      expectedContributorRows: 1,
    }];
    selectedMembers = witnesses.flatMap((witness) => witness.members);
    report.oracle.exactMembershipQuery = exactBasicMembershipQuery;
    report.oracle.witness = witnesses[0];
    report.oracle.fixtureAvailability = { requiredSingleObservationWitnessAvailable: true, patientCandidateLimit };
    report.oracle.exactScope = { project, generation, patientKey, observationKeys: exactBasic.observations.map(({ _id }) => _id) };
  } else if (referenceFieldMode) {
    const exactReferenceMembershipQuery = `
LET specimen = DOCUMENT(${JSON.stringify(referenceWitness.specimenKey)})
FILTER specimen != null
  AND specimen.id == ${JSON.stringify(referenceWitness.specimenId)}
  AND specimen.project == ${JSON.stringify(project)}
  AND specimen.dataset_generation == ${JSON.stringify(generation)}
LET patientKeys = (
  FOR edge IN fhir_edge
    FILTER edge._from == specimen._id AND edge.label == "subject_Patient"
      AND edge.project == ${JSON.stringify(project)} AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT patientKey = edge._to
    RETURN patientKey
)
LET patients = (
  FOR patientKey IN patientKeys
    LET patient = DOCUMENT(patientKey)
    FILTER patient != null AND patient.project == ${JSON.stringify(project)}
      AND patient.dataset_generation == ${JSON.stringify(generation)}
      AND specimen.payload.subject.reference == CONCAT("Patient/", patient.id)
    RETURN { id: patient.id, _id: patient._id, reference: CONCAT("Patient/", patient.id) }
)
FILTER LENGTH(patients) == 1 AND FIRST(patients).id == ${JSON.stringify(referenceWitness.patientId)}
LET patient = FIRST(patients)
LET observations = (
  FOR edge IN fhir_edge
    FILTER edge._to == patient._id AND STARTS_WITH(edge._from, "Observation/")
      AND edge.label == "subject_Patient" AND edge.project == ${JSON.stringify(project)}
      AND edge.dataset_generation == ${JSON.stringify(generation)}
    COLLECT observationKey = edge._from
    LET observation = DOCUMENT(observationKey)
    FILTER observation != null AND observation.project == ${JSON.stringify(project)}
      AND observation.dataset_generation == ${JSON.stringify(generation)}
      AND observation.payload.subject.reference == patient.reference
    SORT observation._id
    LIMIT 32
    RETURN {
      id: observation.id,
      _id: observation._id,
      specimenReference: observation.payload.specimen.reference == null
        ? null
        : observation.payload.specimen.reference
    }
)
RETURN {
  specimen: { id: specimen.id, _id: specimen._id, patientReference: specimen.payload.subject.reference },
  patient,
  observations
}
`;
    const [exactReference] = rawQuery(exactReferenceMembershipQuery);
    assert(exactReference, 'The pinned Specimen→Patient route is no longer visible in the exact CDA project/generation');
    assert.equal(exactReference.specimen._id, referenceWitness.specimenKey);
    assert.equal(exactReference.specimen.id, referenceWitness.specimenId);
    assert.equal(exactReference.patient.id, referenceWitness.patientId);
    assert.equal(exactReference.specimen.patientReference, exactReference.patient.reference);
    const observations = exactReference.observations;
    assert.equal(observations.length, 31, 'Pinned nullable-reference witness must contain exactly 31 distinct linked Observation documents (32 is the overflow sentinel)');
    assert.equal(new Set(observations.map((observation) => observation._id)).size, 31,
      'The bounded raw oracle must deduplicate repeated source edges by terminal Observation identity');
    const observationReferences = observations.map((observation) => observation.specimenReference);
    assert(observationReferences.every((value) => value === null || (typeof value === 'string' && value.length > 0)),
      'Pinned Observation.specimen.reference values must be nonempty strings or null/missing references normalized to null by the raw oracle');
    const presentReferences = observationReferences.filter((value) => value !== null);
    assert.equal(presentReferences.length, 29, 'Pinned witness must retain 29 nonnull Observation.specimen.reference values');
    assert.equal(new Set(presentReferences).size, 29, 'Pinned witness must retain 29 distinct nonnull Observation.specimen.reference values');
    assert.equal(observationReferences.filter((value) => value === null).length, 2,
      'Pinned witness must retain two null/missing Observation.specimen.reference values');
    assert.equal(new Set(observationReferences).size, 30,
      'The two null observations are the fixture duplicate and must remain separate terminal-identity values in ALL');
    const witness = {
      category: 'many-distinct-nullable-references',
      patient: exactReference.patient,
      members: [{ id: exactReference.specimen.id, _id: exactReference.specimen._id,
        patientReference: exactReference.specimen.patientReference, patient: exactReference.patient }],
      observationIds: observations.map((observation) => observation.id),
      observationKeys: observations.map((observation) => observation._id),
      observationReferences,
      observationCount: observations.length,
      distinctObservationReferenceValues: [...new Set(presentReferences)].sort(),
      nullObservationReferenceCount: observationReferences.filter((value) => value === null).length,
      expectedContributorRows: observations.length,
    };
    witnesses = [witness];
    selectedMembers = witness.members;
    manyWitness = witness;
    report.oracle = {
      kind: 'pinned exact raw current-generation Specimen→Patient→Observation edge witness; no global or first-N search',
      searchBounds: {
        project, generation, pinnedSpecimenKey: referenceWitness.specimenKey,
        pinnedSpecimenId: referenceWitness.specimenId, pinnedPatientId: referenceWitness.patientId,
        maximumDistinctObservationDocuments: 32, overflowSentinel: '32 documents fails the exact 31-document witness assertion',
        edgeAndResourceProjectGenerationFilters: true,
      },
      requiredRelatedField: { resourceType: 'Observation', path: 'specimen.reference', logicalType: 'string', cardinality: 'optional_one' },
      exactMembershipQuery: exactReferenceMembershipQuery,
      exactScope: { project, generation, specimenKey: exactReference.specimen._id,
        specimenId: exactReference.specimen.id, patientKey: exactReference.patient._id,
        patientId: exactReference.patient.id, observationKeys: witness.observationKeys },
      fixtureAvailability: { requiredManyWitnessAvailable: true, observationCount: 31,
        presentDistinctValueCount: 29, nullOrMissingValueCount: 2, broadPatientScanPerformed: false },
      witnessSeeds: [witness], missingWitnessCategories: [],
      witnesses: [{ ...witness, members: witness.members.map((member) => ({ ...member })) }],
      selectedSpecimenIds: [exactReference.specimen.id],
      allProtocolSemantics: {
        terminalIdentity: '_id', perTerminalIdentityValues: true, deduplicatesByFieldValue: false,
        rawNullEntriesPreserved: true, expectedProtocolValues: observationReferences,
        sourceObservationKeysInExpectedOrder: witness.observationKeys,
        distinctProjectedValueCountIncludingNull: new Set(observationReferences).size,
        duplicateProjectedValueOccurrenceCount: observationReferences.length - new Set(observationReferences).size,
        duplicateNullValueOccurrenceCount: observationReferences.filter((value) => value === null).length - 1,
        note: 'The compiler sorts and distincts ALL by terminal _id, not by projected value. This fixture has 29 distinct nonnull strings plus two null/missing references normalized to null: its 31 identity rows have 30 distinct projected values, so preserving both nulls proves ALL does not collapse by field value.'
      },
    };
    report.referenceOracleAssertions = {
      sourceProject: project, sourceGeneration: generation,
      specimenId: exactReference.specimen.id, patientId: exactReference.patient.id,
      observationCount: observations.length, distinctObservationIdentityCount: new Set(observations.map(({ _id }) => _id)).size,
      nonnullReferenceCount: presentReferences.length, distinctNonnullReferenceCount: new Set(presentReferences).size,
      nullOrMissingReferenceCount: observationReferences.filter((value) => value === null).length,
      distinctProtocolValueCountIncludingNull: new Set(observationReferences).size,
      duplicateNullValueOccurrencesBeyondFirst: observationReferences.filter((value) => value === null).length - 1,
      rawProtocolExpectedValues: observationReferences,
      rawProtocolExpectedObservationKeys: witness.observationKeys,
      rawExpectedRelatedExpansionRows: observations.map((observation) =>
        [exactReference.specimen.id, exactReference.patient.id, observation.id]),
    };
  } else {
  const patientWitnessLimit = 2000;
  const specimensPerPatient = 1;
  const observationWitnessLimit = 11;
  const categories = statusFieldMode
    ? [{ name: 'many-distinct-statuses', predicate: 'LENGTH(observations) >= 2 AND LENGTH(observations) <= 10 AND LENGTH(distinctStatuses) >= 2' }]
    : [
      { name: 'zero', predicate: 'LENGTH(observations) == 0' },
      { name: 'one', predicate: 'LENGTH(observations) == 1' },
      { name: 'many', predicate: 'LENGTH(observations) >= 2 AND LENGTH(observations) <= 10' },
    ];
  const witnessQueries = {};
  const witnessSeeds = [];
  report.oracle = {
    kind: statusFieldMode
      ? 'bounded current-generation raw Patient→Specimen and Patient→Observation fhir_edge witness finder with a separate exact-membership reread of selected Specimens and primitive Observation.status values'
      : 'bounded current-generation raw fhir_edge witness finder plus a separate exact-membership join over selected Specimen keys',
    searchBounds: {
      patientCandidates: { project, generation, sortedBy: 'id', limit: patientWitnessLimit },
      selectedSpecimensPerPatient: specimensPerPatient,
      distinctObservationsPerCandidate: { deduplicateBy: '_id', cap: observationWitnessLimit, manyMaximum: 10, capSemantics: '11 distinct documents is a sentinel for more than 10 and is excluded; selected zero/one/many witnesses therefore have at most 10 and are reread exactly.' },
      requiredRelatedField: statusFieldMode ? { resourceType: 'Observation', path: 'status', logicalType: 'code', cardinality: 'optional_one' } : undefined,
      missingWitnessMeaning: 'A missing category is bounded fixture unavailability, not proof that no such witness exists elsewhere in the project or generation.',
    },
    witnessQueries, witnessSeeds: [], missingWitnessCategories: [],
  };
  for (const category of categories) {
    const query = `
FOR p IN (
  FOR scopedPatient IN Patient
    FILTER scopedPatient.project == ${JSON.stringify(project)} AND scopedPatient.dataset_generation == ${JSON.stringify(generation)}
    SORT scopedPatient.id
    LIMIT ${patientWitnessLimit}
    RETURN { id: scopedPatient.id, _id: scopedPatient._id }
)
  LET specimens = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Specimen/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT specimenKey = e._from
      LET s = DOCUMENT(specimenKey)
      FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
        AND s.payload.subject.reference == CONCAT("Patient/", p.id)
      SORT s.id
      LIMIT ${specimensPerPatient}
      RETURN { id: s.id, _id: s._id, patientReference: s.payload.subject.reference }
  )
  FILTER LENGTH(specimens) == ${specimensPerPatient}
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      COLLECT observationKey = e._from
      LET o = DOCUMENT(observationKey)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      SORT o.id
      LIMIT ${observationWitnessLimit}
      RETURN { id: o.id, _id: o._id${statusFieldMode ? ', status: o.payload.status' : ''} }
  )
  LET distinctStatuses = (
    FOR observation IN observations
      FILTER IS_STRING(observation.status)
      COLLECT status = observation.status
      SORT status
      RETURN status
  )
  FILTER ${category.predicate}
  SORT p.id
  LIMIT 1
  RETURN {
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    specimens, observations: (FOR o IN observations SORT o.id RETURN o)
  }
`;
    witnessQueries[category.name] = query;
    const [seed] = rawQuery(query);
    if (!seed) {
      const boundedAbsence = statusFieldMode
        ? `No Patient with a linked Specimen and at least two distinct direct Observation.status values was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates. This is bounded fixture absence, not proof of global absence.`
        : `No ${category.name}-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates. The query selects one Specimen per Patient for the exact membership reread. This is bounded fixture absence, not proof that no such witness exists elsewhere in the project or generation.`;
      report.oracle.boundedAbsence = [...(report.oracle.boundedAbsence ?? []), {
        category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence,
      }];
      report.oracle.missingWitnessCategories.push({ category: category.name, patientCandidateLimit: patientWitnessLimit,
        selectedSpecimensPerPatient: specimensPerPatient, meaning: boundedAbsence });
      continue;
    }
    const witnessSeed = { category: category.name, ...seed };
    witnessSeeds.push(witnessSeed);
    report.oracle.witnessSeeds.push(witnessSeed);
  }
  const manySeed = witnessSeeds.find((seed) => seed.category === (statusFieldMode ? 'many-distinct-statuses' : 'many'));
  manyWitness = manySeed;
  report.oracle.fixtureAvailability = {
    requiredForRepair: [statusFieldMode ? 'many-distinct-statuses' : 'many'], requiredManyWitnessAvailable: Boolean(manySeed),
    availableCategories: witnessSeeds.map((seed) => seed.category),
    missingCategories: report.oracle.missingWitnessCategories.map(({ category }) => category),
    completeZeroOneManyCoverage: !statusFieldMode && report.oracle.missingWitnessCategories.length === 0,
  };
  if (!manySeed) {
    throw new Error(statusFieldMode
      ? `No bounded Patient→Specimen→Patient→Observation witness with at least two distinct Observation.status values was found among the first ${patientWitnessLimit} current-generation Patient candidates; this status ONE-to-ALL case is unavailable in this bounded fixture scan.`
      : `No many-Observation witness with at least one linked Specimen was found among the first ${patientWitnessLimit} scoped current-generation Patient candidates; the ONE-to-ALL repair cannot be exercised. The witness query selects one Specimen per Patient.`);
  }
  assert.equal(new Set(witnessSeeds.map((seed) => seed.patient.id)).size, witnessSeeds.length,
    'Every available zero/one/many category must use an independent Patient witness');

  const selectedKeys = witnessSeeds.flatMap((seed) => seed.specimens.map((specimen) => specimen._id)).sort();
  const exactMembershipQuery = `
LET selectedKeys = ${JSON.stringify(selectedKeys)}
FOR s IN Specimen
  FILTER s._id IN selectedKeys AND s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(generation)}
  LET patientEdge = FIRST(
    FOR e IN fhir_edge
      FILTER e._from == s._id AND e.label == "subject_Patient" AND e.project == ${JSON.stringify(project)}
        AND e.dataset_generation == ${JSON.stringify(generation)}
      RETURN e
  )
  FILTER patientEdge != null
  LET p = DOCUMENT(patientEdge._to)
  LET observations = (
    FOR e IN fhir_edge
      FILTER e._to == p._id AND STARTS_WITH(e._from, "Observation/") AND e.label == "subject_Patient"
        AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
      LET o = DOCUMENT(e._from)
      FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)}
      RETURN DISTINCT { id: o.id, _id: o._id${statusFieldMode ? ', status: o.payload.status' : ''} }
  )
  RETURN {
    id: s.id, _id: s._id, patientReference: s.payload.subject.reference,
    patient: { id: p.id, _id: p._id, reference: CONCAT("Patient/", p.id) },
    observationIds: (FOR o IN observations SORT o.id RETURN o.id),
    observationValues: (FOR o IN observations SORT o._id RETURN o),
    observationKeys: (FOR o IN observations SORT o._id RETURN o._id)
  }
`;
  const exactMembers = rawQuery(exactMembershipQuery);
  assert.deepEqual(exactMembers.map((member) => member._id).sort(), selectedKeys,
    `The independent exact-membership oracle did not resolve exactly the ${selectedKeys.length} selected Specimens`);
  witnesses = witnessSeeds.map((seed) => {
    const members = exactMembers.filter((member) => member.patient.id === seed.patient.id).sort((a, b) => a.id.localeCompare(b.id));
    assert.equal(members.length, specimensPerPatient);
    assert(members.every((member) => member.patientReference === seed.patient.reference));
    const observationValues = [...new Map(members.flatMap((member) => member.observationValues)
      .map((observation) => [observation._id, observation])).values()].sort((a, b) => a._id.localeCompare(b._id));
    const observationIds = observationValues.map((observation) => observation.id);
    assert.deepEqual([...observationIds].sort((a, b) => a.localeCompare(b)),
      seed.observations.map((observation) => observation.id).sort((a, b) => a.localeCompare(b)),
      `${seed.category} finder and exact-membership joins disagree`);
    if (statusFieldMode) {
      assert(observationValues.every((observation) => typeof observation.status === 'string' && observation.status.length > 0),
        `${seed.category} witness contains an Observation without a scalar status code`);
      assert.deepEqual(observationValues.map(({ id, status }) => ({ id, status })).sort((a, b) => a.id.localeCompare(b.id)),
        seed.observations.map(({ id, status }) => ({ id, status })).sort((a, b) => a.id.localeCompare(b.id)),
        `${seed.category} finder and exact-membership status reads disagree`);
    }
    const expectedCount = seed.category === 'zero' ? 0 : seed.category === 'one' ? 1 : observationIds.length;
    assert.equal(observationIds.length, expectedCount, `${seed.category} witness cardinality changed`);
    const observationStatuses = observationValues.map((observation) => observation.status);
    return {
      category: seed.category, patient: seed.patient, members,
      observationIds, observationKeys: observationValues.map((observation) => observation._id), observationCount: observationIds.length,
      observationStatuses,
      distinctStatusValues: [...new Set(observationStatuses)].sort(),
      expectedContributorRows: members.length * Math.max(1, observationIds.length),
    };
  });
  manyWitness = witnesses.find((witness) => witness.category === (statusFieldMode ? 'many-distinct-statuses' : 'many'));
  assert(manyWitness && manyWitness.observationIds.length > 1, 'The independent many witness must predict an actual ONE disagreement');
  selectedMembers = witnesses.flatMap((witness) => witness.members);
  Object.assign(report.oracle, {
    exactMembershipQuery, exactMembershipScope: { selectedSpecimenCount: selectedKeys.length,
      selectedSpecimensPerPatient: specimensPerPatient,
      maximumExpectedDistinctObservationsPerPatient: 10, observationSetsReadCompletelyForSelectedAtMostTenWitnesses: true },
    witnesses: witnesses.map(({ category, patient, members, observationIds, observationKeys, observationCount, observationStatuses, distinctStatusValues, expectedContributorRows }) => ({
      category, patient, observationCount, observationIds, observationKeys,
      ...(statusFieldMode ? { observationStatuses, distinctStatusValues } : {}),
      members: members.map(({ id, _id, patientReference }) => ({ id, _id, patientReference })), expectedContributorRows,
    })),
    selectedSpecimenIds: selectedMembers.map((member) => member.id),
  });
  }

  verificationPhase = 'builder';
  const rootTitle = `${rootResourceType} ID`;
  const tableTitle = zeroObservationMode ? 'Zero Observation related ID ONE to ALL QA' : referenceFieldMode ? 'Related Observation specimen reference QA' : statusFieldMode ? 'Related Observation status code QA' : 'Related ID ONE to ALL QA';
  await api(root, { name: explorer, title: tableTitle });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation);
  if (statusFieldMode) assertStatusCandidate(builder);
  else if (referenceFieldMode) assertReferenceCandidate(builder);
  else assertRelatedIdCandidate(builder);
  const rootNode = builder.catalog.nodes.find((node) => node.resourceType === rootResourceType && node.rowRootEligible);
  assert(rootNode, `Current catalog has no authorized ${rootResourceType} row root`);
  const created = await command([{ type: 'CREATE_TABLE', title: tableTitle, rootNodeId: rootNode.nodeId }]);
  assert.equal(created.workspace.documents.length, 1);
  outputId = created.workspace.documents[0].output.id;
  const idField = builder.catalog.candidates.find((candidate) => candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
  assert(idField, `The current ${rootResourceType} catalog must expose its id field`);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: rootTitle }]);
  const selection = await api(`${base.replace('/authoring/v2', '')}/selections`, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: selectedMembers.map((member) => ({ project, generation, resourceType: rootResourceType, id: member.id })) } },
  });
  assert.equal(selection.memberCount, selectedMembers.length, 'Population selection must preserve the exact independent witness membership');
  const routes = await api(`${base}/population-routes`, { snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50 });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'Exact selected Specimen members have no direct population route');
  await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
  report.ownedWorkspace = { explorer, outputId, selectionRevisionId: selection.id, memberCount: selection.memberCount };
  const sourceRows = selectedMembers.map((member) => [member.id]);
  const groupedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows)]).sort((a, b) => a[0].localeCompare(b[0]));
  const relatedRows = basicMode ? sourceRows : witnesses.flatMap((witness) => witness.members.flatMap((member) => witness.observationIds.length
    ? witness.observationIds.map((observationId) => zeroObservationMode
      ? [member.id, observationId]
      : [member.id, member.patient.id, observationId])
    : [zeroObservationMode ? [member.id, '—'] : [member.id, member.patient.id, '—']])).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  if (referenceFieldMode) {
    const actualRawRelatedRows = relatedRows.map((row) => JSON.stringify(row)).sort();
    const expectedRawRelatedRows = report.referenceOracleAssertions.rawExpectedRelatedExpansionRows
      .map((row) => JSON.stringify(row)).sort();
    assert.deepEqual(actualRawRelatedRows, expectedRawRelatedRows,
      'The shared related-row oracle must preserve the exact pinned Specimen, Patient, and Observation identities from the independent raw membership query');
    assert.equal(relatedRows.length, report.referenceOracleAssertions.observationCount,
      'The shared related-row oracle must produce one row for each distinct raw Observation identity');
    report.referenceOracleAssertions.sharedRelatedRowsMatchRawRoute = true;
  }

  await startNativeCapture();
  await openTable(sourceRows, 'reload-exact-available-cardinality-source-members');
  const sourceBaseline = await api(`${base}/builder`);
  assert.equal(doc(sourceBaseline).population.selectionRevisionId, selection.id);
  assert.deepEqual(doc(sourceBaseline).columns.map((column) => column.label), [rootTitle]);

  let pipelineRows = sourceRows;
  const chain = basicMode ? [] : zeroObservationMode
    ? [{ from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' }]
    : [
      { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
      { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  ];
  for (let index = 0; index < chain.length; index += 1) {
    const hop = chain[index];
    const firstSpecimenPatientHop = index === 0 && !zeroObservationMode;
    const expectedRows = firstSpecimenPatientHop
      ? selectedMembers.map((member) => [member.id, member.patient.id])
      : relatedRows;
    const witnessesForHop = firstSpecimenPatientHop ? selectedMembers : witnesses;
    await expand(hop, witnessesForHop, expectedRows, zeroObservationMode ? doc(sourceBaseline).population : undefined);
    pipelineRows = expectedRows;
  }
  assert.equal(pipelineRows.length, relatedRows.length);
  const expandedDoc = doc();
  const populationBeforeGroup = structuredClone(expandedDoc.population);
  const patientExpansion = basicMode ? undefined : expandedDoc.construction?.steps.find((step) => step.operation.kind === 'RELATED_EXPAND' && step.operation.relatedExpand.targetResourceType === 'Patient');
  const patientGroupOutput = basicMode || zeroObservationMode
    ? expandedDoc.columns.find((column) => column.label === rootTitle)
    : patientExpansion?.outputs.find((output) => output.label === 'Patient FHIR resource ID');
  assert(patientGroupOutput, basicMode ? 'The Patient root ID source must remain available as the Group key' : 'The first related expansion must retain the exact Patient ID group key');
  const groupKeyLabel = patientGroupOutput.label;
  const configureGroup = async () => {
    await clickNative(nativePage, '[data-testid="construction-rows-settings-trigger"]');
    await waitForObservable(nativePage, () => document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled === false, 5000);
    await clickNative(nativePage, '[data-testid="construction-action-group-rows"]');
    const groupKeyControl = nativePage.locator(`input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    await groupKeyControl.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await groupKeyControl.count(), 1, 'The group key control must be unique');
    await groupKeyControl.waitFor({ state: 'attached', timeout: 5000 });
    assert.equal(await groupKeyControl.isEnabled(), true, 'The group key control must be enabled');
    const fromIndex = report.nativeRequests.length;
    const started = Date.now();
    await clickNative(nativePage, `input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    return { started, fromIndex };
  };
  let groupAttempt = await configureGroup();
  await proposal('group-available-patient-witnesses-preview-cancel-target', groupAttempt.started, groupedRows, groupAttempt.fromIndex);
  const beforeGroupCancel = await api(`${base}/builder`);
  const cancelGroupStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-cancel-proposal"]');
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  assert.deepEqual((await api(`${base}/builder`)).workspace, beforeGroupCancel.workspace, 'Cancel must preserve the exact expanded source workspace');
  record('cancel-group-proposal-preserves-source-bindings', cancelGroupStarted);
  await openTable(relatedRows, 'reload-expanded-source-after-group-cancel');

  groupAttempt = await configureGroup();
  await proposal('group-available-patient-witnesses-preview', groupAttempt.started, groupedRows, groupAttempt.fromIndex);
  await applyProposal(groupedRows, 'group-available-patient-witnesses-apply-to-render');
  const groupedWorkspace = structuredClone(builder.workspace);
  const groupedBaseline = doc();
  assert.deepEqual(groupedBaseline.population, populationBeforeGroup, 'Grouping must preserve exact selected member scope');
  const groupStepBefore = groupedBaseline.construction.steps.find((step) => step.operation.kind === 'GROUP');
  assert(groupStepBefore, 'The Group operation was not saved');
  const savedGroupKeyOutput = groupStepBefore.outputs.find((output) => output.label === groupKeyLabel);
  assert(savedGroupKeyOutput, 'The saved Group output lost its Patient resource key');
  const groupKeyName = savedGroupKeyOutput.name;
  await openTable(groupedRows, 'reload-independent-grouped-witnesses');

  await openRelatedFieldChooser();
  const beforeOne = await api(`${base}/builder`);
  assert.deepEqual(doc(beforeOne).construction, groupedBaseline.construction);
  assert.deepEqual(doc(beforeOne).population, groupedBaseline.population);
  if (zeroObservationMode) {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = report.nativeRequests.length;
    await clickAddRelated();
    const oneProposal = await waitRelatedProposal('zero-observation-one-preview', oneStarted, 'ONE', oneFromIndex);
    const oneResponse = protocolResponse(oneProposal);
    assert.equal(oneProposal.status, 200, JSON.stringify(oneProposal.response));
    assert.equal(oneResponse.previewStatus, 'READY', JSON.stringify(oneProposal.response));
    await waitAdoptedChoicePreview(oneProposal, 'Zero Observation ONE proposal');
    const oneRelatedStep = relatedSourceProposalCandidate({ ...oneProposal, response: oneResponse }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: 'id',
    })?.step;
    assert(oneRelatedStep, 'The zero-match ONE proposal must retain the selected Observation.id RELATED_SOURCE step');
    const oneOutput = oneRelatedStep.outputs.find((candidate) => candidate.id === oneRelatedStep.operation.relatedSource.outputColumnId);
    assert(oneOutput, 'The zero-match ONE proposal must expose its related ID output');
    assert.equal(oneResponse.preview?.rowCount, 1, 'ONE must preserve the grouped Patient row when the exact raw route has no Observation matches');
    const oneRow = oneResponse.preview.rows.find((row) => row[groupKeyName] === witnesses[0].patient.id);
    assert(oneRow, 'The zero-match ONE preview omitted the exact scoped Patient root');
    assert.equal(oneRow[oneOutput.name], null,
      'The typed nullable ONE source must return null for zero matching Observation values');
    report.zeroObservationOne = { status: oneProposal.status, previewStatus: oneResponse.previewStatus,
      rowCount: oneResponse.preview.rowCount, nullableValueIsNull: true };

    const cancelOneStarted = Date.now();
    await cancelColumnProposal();
    await rendered(groupedRows);
    assert.deepEqual((await api(`${base}/builder`)).workspace, groupedWorkspace,
      'Canceling zero-match ONE must preserve the exact grouped source workspace');
    record('cancel-zero-observation-one-preserves-group', cancelOneStarted);
    await openRelatedFieldChooser();
    await selectRelatedPolicy('ALL');
  } else if (basicMode) {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = report.nativeRequests.length;
    await clickAddRelated();
    const oneProposal = await waitRelatedProposal('basic-status-one-preview', oneStarted, 'ONE', oneFromIndex);
    const oneProposalResponse = protocolResponse(oneProposal);
    assert.equal(oneProposal.status, 200, JSON.stringify(oneProposal.response));
    assert.equal(oneProposalResponse.previewStatus, 'READY', JSON.stringify(oneProposal.response));
    const adoptedOneProposalId = await waitAdoptedChoicePreview(oneProposal, 'Basic status ONE proposal');
    const oneRelatedStep = relatedSourceProposalCandidate({ ...oneProposal, response: protocolResponse(oneProposal) }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: 'status',
    })?.step;
    assert(oneRelatedStep, 'The basic ONE preview must contain the selected status RELATED_SOURCE step');
    const oneOutput = oneRelatedStep.outputs.find((candidate) => candidate.id === oneRelatedStep.operation.relatedSource.outputColumnId);
    assert(oneOutput, 'The basic ONE preview must expose its candidate status output');
    const oneRow = oneProposalResponse.preview.rows.find((row) => row[groupKeyName] === witnesses[0].patient.id);
    assert(oneRow, 'The basic ONE preview omitted the independently selected Patient');
    assert.equal(oneRow[oneOutput.name], witnesses[0].observationStatuses[0], 'The basic ONE value differs from the exact raw Observation.status');
    report.basicOneStatus = { status: oneProposal.status, proposalId: adoptedOneProposalId,
      source: report.relatedFieldCandidate, rawValue: witnesses[0].observationStatuses[0], candidateValue: oneRow[oneOutput.name] };
    const cancelOneStarted = Date.now();
    await cancelColumnProposal();
    await rendered(groupedRows);
    assert.deepEqual((await api(`${base}/builder`)).workspace, groupedWorkspace, 'Canceling basic ONE must preserve the exact Group workspace');
    record('cancel-basic-status-one-proposal-preserves-group', cancelOneStarted);
    await openRelatedFieldChooser();
    await selectRelatedPolicy('ALL');
  } else {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = report.nativeRequests.length;
    await clickAddRelated();
    const oneFailure = await waitRelatedProposal('related-observation-one-disagreement', oneStarted, 'ONE', oneFromIndex);
    const oneFailureResponse = protocolResponse(oneFailure);
    const oneError = oneFailureResponse?.error?.code ?? oneFailureResponse?.code ??
      oneFailureResponse?.diagnostics?.find((diagnostic) => diagnostic.severity === 'ERROR')?.code;
    assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
    assert.equal(oneError, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', JSON.stringify(oneFailure.response));
    assert.notEqual(protocolResponse(oneFailure)?.previewStatus, 'READY', 'Raw multiple-value witness must not pass grouped-row ONE');
    assert(manyWitness.observationIds.length > 1, 'The raw many witness must independently predict the ONE conflict');
    if (statusFieldMode) assert(manyWitness.distinctStatusValues.length > 1,
      'The status witness must independently prove distinct ONE values');
    if (referenceFieldMode) {
      assert(manyWitness.distinctObservationReferenceValues.length > 1,
        'The raw nullable-reference witness must independently prove more than one distinct present ONE value');
      assert.equal(manyWitness.nullObservationReferenceCount, 2,
        'The source oracle must retain two null values while predicting ONE rejection from the 29 distinct present references');
    }
    const afterOne = await api(`${base}/builder`);
    assert.deepEqual(afterOne.workspace, beforeOne.workspace, 'Rejected ONE preflight must not mutate source membership, route, or Group');
    report.oneRejection = { status: oneFailure.status, errorCode: oneError,
      manyObservationIds: manyWitness.observationIds,
      ...(statusFieldMode ? { distinctStatusValues: manyWitness.distinctStatusValues } : {}),
      ...(referenceFieldMode ? { distinctObservationReferenceValues: manyWitness.distinctObservationReferenceValues,
        nullObservationReferenceCount: manyWitness.nullObservationReferenceCount } : {}),
      proposalId: protocolResponse(oneFailure)?.proposalId };
    const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
    const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
    await waitForObservable(nativePage, ({ routeSelector, formSelector }) => {
      const dialog = document.querySelector('[role="dialog"]');
      const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
      return Boolean(dialog && policy?.value === 'ONE' && dialog.querySelector(routeSelector)?.checked && dialog.querySelector(formSelector)?.checked);
    }, { routeSelector, formSelector }, 5000);
    const retainedChooser = await inspectPage(nativePage, ({ routeSelector, formSelector }) => {
      const dialog = document.querySelector('[role="dialog"]');
      const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
      return { open: Boolean(dialog), policy: policy?.value, routeChecked: dialog?.querySelector(routeSelector)?.checked,
        formChecked: dialog?.querySelector(formSelector)?.checked,
        addEnabled: [...(dialog?.querySelectorAll('button') ?? [])].some(button => button.textContent.trim() === 'Add 1 column' && !button.disabled) };
    }, { routeSelector, formSelector });
    assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true },
      'ONE rejection must retain the same related-source chooser, exact route, form, and source selection for direct repair');
    await selectRelatedPolicy('ALL');
  }
  const allRepairStarted = Date.now();
  const allRepairFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const firstAll = await waitRelatedProposal('same-chooser-all-repair-preview', allRepairStarted, 'ALL', allRepairFromIndex);
  const firstAllResponse = protocolResponse(firstAll);
  assert.equal(firstAll.status, 200, JSON.stringify(firstAll.response));
  assert.equal(firstAllResponse.previewStatus, 'READY', JSON.stringify(firstAll.response));
  assert(firstAllResponse.previewDurationMs <= 5000, `ALL preview took ${firstAllResponse.previewDurationMs} ms`);
  const firstAllReceiptId = await waitAdoptedChoicePreview(firstAll, 'Same-chooser ALL repair');
  let previewColumnId;
  {
    const relatedProposal = relatedSourceProposalCandidate({ ...firstAll, response: protocolResponse(firstAll) }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(relatedProposal, 'Native ALL preview must propose the exact related Observation field');
    const output = relatedProposal.step.outputs.find((candidate) => candidate.id === relatedProposal.related.outputColumnId);
    assert(output, `Native ALL preview omitted the related ${relatedFieldPath} output`);
    previewColumnId = output.name;
    report.directAllRepair = { policy: relatedProposal.rowValuePolicy, form: relatedProposal.related.form,
      contributorPolicy: relatedProposal.related.contributorRule.policy, receiptId: firstAllReceiptId };
  }
  assert(previewColumnId, `Native ALL preview did not propose the related ${relatedFieldPath} column`);
  const proposedValues = firstAllResponse.preview.rows.map((row) => ({ patientReference: row[groupKeyName], values: row[previewColumnId] }));
  assert.equal(proposedValues.length, witnesses.length);
  for (const witness of witnesses) {
    const proposed = proposedValues.find((row) => row.patientReference === witness.patient.id);
    assert(proposed, `Native preview omitted ${witness.category} witness ${witness.patient.reference}`);
    if (zeroObservationMode) {
      assert(Array.isArray(proposed.values), 'The typed ALL output must remain an array when its exact raw route has zero terminals');
      assert.equal(proposed.values.length, 0,
        'The typed ALL output must contain no fabricated value when the exact raw route has zero terminals');
      report.zeroObservationAll = { previewStatus: firstAllResponse.previewStatus, previewValueIsEmptyArray: true,
        sourceContract: 'RELATED_SOURCE ALL returns a typed array subplan with empty-on-null behavior when no terminal record matches' };
    }
    if (referenceFieldMode) {
      assert.equal(proposed.values.length, witness.observationKeys.length,
        'ALL must return one protocol value per distinct terminal Observation identity');
      assert.equal(proposed.values.filter((value) => value === null).length, 2,
        'ALL protocol output must preserve both null/missing Observation.specimen.reference entries normalized by the raw oracle');
      assert.equal(new Set(proposed.values).size, 30,
        'ALL must preserve the two null values from distinct Observation identities instead of deduplicating by field value');
    }
    assert.deepEqual(proposed.values, relatedValuesFor(witness), `${witness.category} ALL output differs from the independent raw oracle`);
  }
  const cancelAllStarted = Date.now();
  await cancelColumnProposal();
  await rendered(groupedRows);
  assert.deepEqual((await api(`${base}/builder`)).workspace, groupedWorkspace, 'Canceling the successful ALL preview must preserve the exact grouped workspace');
  record('cancel-related-all-proposal-preserves-group', cancelAllStarted);

  await openRelatedFieldChooser();
  await selectRelatedPolicy('ALL');
  const allApplyStarted = Date.now();
  const allApplyFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const allProposal = await waitRelatedProposal('reopened-all-preview-before-apply', allApplyStarted, 'ALL', allApplyFromIndex);
  assert.equal(allProposal.status, 200);
  const allProposalResponse = protocolResponse(allProposal);
  assert.equal(allProposalResponse.previewStatus, 'READY');
  {
    const reopenedRelated = relatedSourceProposalCandidate({ ...allProposal, response: protocolResponse(allProposal) }, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(reopenedRelated, 'Reopened status ALL must retain the exact typed source and relationship route');
    assert.equal(reopenedRelated.rowValuePolicy, 'ALL');
  }
  const allProposalReceiptId = await waitAdoptedChoicePreview(allProposal, 'Reopened ALL proposal');
  report.reopenedAllProposalReceiptId = allProposalReceiptId;
  const applyStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  const allCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
    item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === allProposalResponse.proposalId), allApplyFromIndex);
  assert.equal(allCommand.status, 200, JSON.stringify(allCommand.response));
  assert.equal(allCommand.request.commands.length, 1);
  assert.equal(allCommand.request.commands[0].proposalId, allProposalResponse.proposalId);
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  const addedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows), relatedDisplayValuesFor(witness)]).sort((a, b) => a[0].localeCompare(b[0]));
  await rendered(addedRows);
  record('apply-related-all-to-native-table-render', applyStarted, { commandStatus: allCommand.status });
  builder = await api(`${base}/builder`);
  const addedDocument = doc();
  assert.deepEqual(addedDocument.population, groupedBaseline.population, 'ALL apply must preserve the exact source selection');
  const savedRelatedStep = addedDocument.construction.steps.find((step) => step.operation?.kind === 'RELATED_SOURCE' &&
    step.operation.relatedSource?.source?.candidateId === report.relatedFieldCandidate.candidateId &&
    step.operation.relatedSource?.source?.nodeId === report.relatedFieldCandidate.nodeId &&
    step.operation.relatedSource?.source?.resourceType === 'Observation' &&
    step.operation.relatedSource?.source?.path === relatedFieldPath &&
    step.operation.relatedSource?.choiceId === report.currentRelatedChoiceId);
  assert(savedRelatedStep, `The saved construction must retain the selected RELATED_SOURCE Observation.${relatedFieldPath} candidate and route choice`);
  const related = savedRelatedStep.operation.relatedSource;
  assert.equal(related.form, 'ALL');
  assert.equal(related.contributorRule.policy, 'ALL_MATCHES');
  assert.equal(related.rowValuePolicy ?? 'ALL', 'ALL', 'The saved related source must retain the selected grouped-row ALL policy');
  assert.deepEqual(related.route.map((hop) => [hop.fromResourceType, hop.toResourceType]),
    expectedRelatedRouteTypes.slice(0, -1).map((resourceType, index) => [resourceType, expectedRelatedRouteTypes[index + 1]]));
  if (zeroObservationMode) assert.deepEqual(related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedPatientObservationRoute,
  'The saved RELATED_SOURCE must retain the exact inbound typed Observation edge');
  else if (!basicMode) assert.deepEqual(related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The saved RELATED_SOURCE must retain the exact Specimen→Patient→Observation subject route');
  const output = savedRelatedStep.outputs.find((candidate) => candidate.id === related.outputColumnId);
  assert(output, `The saved related step omitted output ${related.outputColumnId}`);
  const relatedColumn = { column: output.name, columnId: output.id, label: output.label, logicalType: output.type };
  const proposalOutput = allProposalResponse.preview?.columns.find((column) => column.column === relatedColumn.column);
  assert(proposalOutput, 'The accepted ALL proposal preview must contain the exact saved related output');
  assert.equal(relatedColumn.label, proposalOutput.label,
    'Applying the related source must retain the output label shown by its accepted proposal preview');
  if (statusFieldMode) assert.equal(relatedColumn.label, relatedOutputLabel,
    'Observation.status is titled Status in the chooser, while the generated related output defaults to Observation status');
  assert.equal(relatedColumn.logicalType, logicalTypeExpected,
    `The persisted related column must match the compiler-proved Observation.${relatedFieldPath} logical type`);
  const savedGroup = addedDocument.construction.steps.find((step) => step.id === groupStepBefore.id);
  assert(savedGroup, 'The original Group step identity must remain stable');
  assert.deepEqual(savedGroup.operation.group.keys, groupStepBefore.operation.group.keys, 'ALL apply changed the authored group key binding');
  assert.deepEqual(savedGroup.operation.group.aggregates, groupStepBefore.operation.group.aggregates, 'ALL apply changed the authored Group aggregate');
  assert.deepEqual(savedGroup.rowValues, groupStepBefore.rowValues,
    'Adding a RELATED_SOURCE must preserve existing GROUP row-value bindings; policy belongs to the related source');
  const rowValueOutput = savedRelatedStep.outputs.find((candidate) => candidate.id === related.outputColumnId);
  assert.equal(rowValueOutput?.name, relatedColumn.column);
  for (const sourceColumn of groupedBaseline.columns) {
    assert.deepEqual(addedDocument.columns.find((column) => column.columnId === sourceColumn.columnId), sourceColumn, `ALL apply changed source binding ${sourceColumn.label}`);
  }
  const applyPreviewFrom = report.nativeRequests.indexOf(allCommand);
  const savedPreviewStarted = Date.now();
  const proposalPreview = allProposalResponse.preview;
  assert(proposalPreview, 'The accepted ALL proposal did not retain its candidate preview');
  assert.equal(allProposalResponse.candidateWorkspaceDigest, builder.draftDigest, 'The saved builder digest must equal the applied proposal digest');
  assert.equal(allProposalResponse.outputId, outputId);
  const allCommandResponse = protocolResponse(allCommand);
  assert.equal(allCommandResponse?.draftVersion, builder.draftVersion);
  assert.equal(allCommandResponse?.draftDigest, builder.draftDigest);
  const acceptedReconcile = await waitNative((entry) => entry.path.endsWith('/reconcile') &&
    entry.request?.draftVersion === builder.draftVersion && entry.request?.draftDigest === builder.draftDigest, applyPreviewFrom);
  assert.equal(acceptedReconcile.status, 200, JSON.stringify(acceptedReconcile.response));
  const acceptedReconcileResponse = protocolResponse(acceptedReconcile);
  assert.equal(acceptedReconcileResponse?.snapshotToken, allProposalResponse.snapshotToken);
  assert.equal(acceptedReconcileResponse?.intentDigest, builder.draftDigest);
  assert(acceptedReconcileResponse?.outputs?.some((output) => output.outputId === outputId), 'The saved reconcile receipt does not include the edited output');
  assert.equal(proposalPreview.outputId, outputId);
  const expectedActivePreview = { receiptId: acceptedReconcileResponse.receiptId, outputId,
    draftVersion: String(builder.draftVersion), draftDigest: builder.draftDigest };
  await waitForObservable(nativePage, expected => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && preview?.dataset.previewReceiptId === expected.receiptId &&
      preview?.dataset.previewOutputId === expected.outputId && preview?.dataset.currentDraftVersion === expected.draftVersion &&
      preview?.dataset.currentDraftDigest === expected.draftDigest;
  }, expectedActivePreview, 5000);
  const activePreview = await inspectPage(nativePage, () => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return { status: preview?.dataset.previewStatus, receiptId: preview?.dataset.previewReceiptId,
      outputId: preview?.dataset.previewOutputId, draftVersion: preview?.dataset.currentDraftVersion,
      draftDigest: preview?.dataset.currentDraftDigest };
  });
  assert.deepEqual(activePreview, {
    status: 'ready',
    receiptId: acceptedReconcileResponse.receiptId,
    outputId,
    draftVersion: String(builder.draftVersion),
    draftDigest: builder.draftDigest,
  }, 'The rendered table must be the active preview for the exact saved draft and accepted receipt');
  const applyPreviewRequests = report.nativeRequests.slice(applyPreviewFrom).filter((entry) => entry.path.endsWith('/preview'));
  for (const entry of applyPreviewRequests) {
    assert(nativeRequestValue(entry, 'outputId'), `Captured ${entry.method} ${entry.path} without outputId body/query: ${JSON.stringify(entry)}`);
    const requestDeadline = Date.now() + 5000;
    while (!entry.complete && Date.now() < requestDeadline) {
      await waitForNativeRequestChange(Math.max(1, Math.min(100, requestDeadline - Date.now())));
    }
    assert(entry.complete, `Timed out waiting for captured preview response: ${JSON.stringify({ method: entry.method, url: entry.url, query: entry.query, request: entry.request })}`);
  }
  const targetPreviewRequests = applyPreviewRequests.filter((entry) => nativeRequestValue(entry, 'outputId') === outputId);
  for (const entry of targetPreviewRequests) {
    assert.equal(nativeRequestValue(entry, 'receiptId'), acceptedReconcileResponse.receiptId, 'A post-Apply native preview must use the accepted saved receipt');
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    const response = protocolResponse(entry);
    assert.equal(response?.receiptId, acceptedReconcileResponse.receiptId);
    assert.equal(response?.outputId, outputId);
  }
  const previewSource = targetPreviewRequests.length === 0 ? 'direct-preview-for-accepted-saved-receipt' : 'post-apply-native-preview';
  const savedPreview = protocolResponse(targetPreviewRequests.at(-1)) ?? await api(`${base}/preview`, {
    receiptId: acceptedReconcileResponse.receiptId, outputId, limit: 25,
  });
  assert.equal(savedPreview.receiptId, acceptedReconcileResponse.receiptId, 'Saved values must be read from the exact accepted receipt');
  assert.equal(savedPreview.outputId, outputId);
  report.savedPreviewVerification = {
    source: previewSource,
    outputId,
    receiptId: acceptedReconcileResponse.receiptId,
    savedDraftVersion: builder.draftVersion,
    savedDraftDigest: builder.draftDigest,
    candidateWorkspaceDigest: allProposalResponse.candidateWorkspaceDigest,
    proposalRequestId: allProposal.requestId,
    reconcileRequestId: acceptedReconcile.requestId,
    activePreview,
    postApplyPreviewRequests: applyPreviewRequests.map((entry) => ({
      requestId: entry.requestId,
      method: entry.method,
      url: entry.url,
      path: entry.path,
      query: entry.query,
      request: entry.request,
      status: entry.status,
      responseReceiptId: protocolResponse(entry)?.receiptId,
      responseOutputId: protocolResponse(entry)?.outputId,
      rowCount: protocolResponse(entry)?.rowCount,
      complete: entry.complete,
    })),
  };
  assert.equal(savedPreview.rowCount, witnesses.length);
  for (const witness of witnesses) {
    const row = savedPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Saved native Preview omitted ${witness.category} witness`);
    if (referenceFieldMode) {
      assert.equal(row[relatedColumn.column].length, witness.observationKeys.length,
        'Saved ALL preview must contain one value per distinct Observation identity');
      assert.equal(row[relatedColumn.column].filter((value) => value === null).length, 2,
        'Saved ALL preview must preserve both null array entries');
      assert.equal(new Set(row[relatedColumn.column]).size, 30,
        'Saved ALL preview must retain the duplicate null from two distinct Observation identities');
    }
    assert.deepEqual(row[relatedColumn.column], relatedValuesFor(witness), `${witness.category} saved values differ from the independent raw oracle`);
  }
  record('native-preview-matches-exact-related-field-oracle', savedPreviewStarted, {
    receiptId: savedPreview.receiptId,
    source: previewSource,
    postApplyPreviewRequestCount: targetPreviewRequests.length,
  });

  const reloadedPreview = await openTable(addedRows, 'reload-related-all-available-cardinality-values');
  for (const witness of witnesses) {
    const row = reloadedPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Reloaded Preview omitted ${witness.category} witness`);
    if (referenceFieldMode) {
      assert.equal(row[relatedColumn.column].filter((value) => value === null).length, 2,
        'Reloaded ALL preview must preserve both null array entries');
      assert.equal(new Set(row[relatedColumn.column]).size, 30,
        'Reloaded ALL preview must retain the duplicate null from distinct Observation identities');
    }
    assert.deepEqual(row[relatedColumn.column], relatedValuesFor(witness));
  }

  let activeRelatedColumnLabel = relatedColumn.label;
  {
    const renamedLabel = `Verified ${relatedOutputLabel}`;
    const editStarted = Date.now();
    const editFrom = report.nativeRequests.length;
    await clickNative(nativePage, 'button', { name: 'Columns' });
    const labelSelector = `[aria-label=${JSON.stringify(`Column name for ${relatedColumn.label}`)}]`;
    const renameInput = nativePage.locator(labelSelector);
    await renameInput.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await renameInput.count(), 1, 'The output label editor must be unique');
    const renameState = await renameInput.evaluate(input => ({ value: input.value, disabled: input.disabled, readOnly: input.readOnly }));
    assert.equal(renameState.disabled, false, 'The applied related-field output must remain editable');
    assert.equal(renameState.readOnly, false, 'The applied related-field output must not be read-only');
    await cda.action(`fill output label ${relatedColumn.label}`, renameInput,
      target => target.fill(renamedLabel, { timeout: 5000 }), { timeout: 5000, editable: true });
    await cda.action(`commit output label ${relatedColumn.label}`, renameInput,
      target => target.press('Enter', { timeout: 5000 }), { timeout: 5000, editable: true });
    const editableStepId = savedRelatedStep.id;
    const rename = await waitNative((entry) => entry.path.endsWith('/commands') &&
      entry.request?.commands?.some((item) => item.type === 'UPDATE_CONSTRUCTION_OUTPUT' &&
        item.constructionOutput?.stepId === editableStepId && item.constructionOutput?.columnId === rowValueOutput.id), editFrom);
    assert.equal(rename.status, 200, JSON.stringify(rename.response));
    assert.equal(rename.request.commands[0].constructionOutput.label, renamedLabel);
    builder = await api(`${base}/builder`);
    const editedStep = doc().construction.steps.find((step) => step.id === editableStepId);
    const editedOutput = editedStep?.outputs.find((output) => output.id === rowValueOutput.id);
    assert.equal(editedOutput?.name, rowValueOutput.name, 'Editing a display label must preserve the stable output name');
    assert.equal(editedOutput?.label, renamedLabel, 'The saved construction output must retain its edited label');
    assert.deepEqual(editedStep?.operation.relatedSource?.source, related.source,
      'Editing the label must preserve the exact RELATED_SOURCE candidate and field binding');
    assert.equal(editedStep?.operation.relatedSource?.choiceId, report.currentRelatedChoiceId,
      'Editing the label must preserve the selected signed route choice');
    assert.deepEqual(editedStep?.operation.relatedSource?.route, related.route,
      'Editing the label must preserve the exact related source route');
    assert.equal(editedStep?.operation.relatedSource?.rowValuePolicy ?? 'ALL', 'ALL',
      'Editing the label must preserve the related source ONE/ALL policy');
    await clickNative(nativePage, 'button', { name: 'Columns' });
    await waitForObservable(nativePage, ({ columnCount, label, rowCount }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const headers = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.textContent.trim());
      const rows = [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length);
      return table?.getAttribute('aria-colcount') === String(columnCount) && headers.includes(label) &&
        rows.length === rowCount && !document.body.innerText.includes('Loading your table…') &&
        !document.body.innerText.includes('Preview failed:');
    }, { columnCount: addedRows[0]?.length ?? 2, label: renamedLabel, rowCount: addedRows.length });
    const editedCurrentTable = await inspectPage(nativePage, () => ({
      headers: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
        .map(cell => cell.textContent.trim()),
      rows: [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')]
        .slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length),
    }));
    assert(editedCurrentTable.headers.includes(renamedLabel),
      `Current saved table header did not render the edited output label: ${JSON.stringify(editedCurrentTable.headers)}`);
    assert.equal(editedCurrentTable.rows.length, addedRows.length,
      'Current saved table must render the exact expected number of rows after the label edit');
    const orderedRows = rows => rows.map(row => JSON.stringify(row)).sort();
    assert.deepEqual(orderedRows(editedCurrentTable.rows), orderedRows(addedRows),
      'Current saved table rows and cell values must exactly match the raw CDA oracle after the label edit');
    record('edit-related-field-output-label', editStarted, { priorLabel: relatedColumn.label, newLabel: renamedLabel, path: relatedFieldPath, rowCount: editedCurrentTable.rows.length });
    const editedPreview = await openTable(addedRows, 'reload-edited-related-field-output-label');
    const editedHeaders = await inspectPage(nativePage, () => [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')]
      .map(cell => cell.textContent.trim()));
    assert(editedHeaders.includes(renamedLabel), `Reloaded table header text did not retain the exact edited output label: ${JSON.stringify(editedHeaders)}`);
    for (const witness of witnesses) {
      const row = editedPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
      assert(row, `Edited/reloaded Preview omitted ${witness.category} witness`);
      assert.deepEqual(row[relatedColumn.column], relatedValuesFor(witness), 'Renaming the Group output changed its source values');
    }
    activeRelatedColumnLabel = renamedLabel;
  }

  const removeStarted = Date.now();
  const removeFromIndex = report.nativeRequests.length;
  let removeCommand;
  let removeApplyStarted = removeStarted;
  const stepId = savedRelatedStep.id;
  assert.equal(savedRelatedStep.operation.relatedSource.source.candidateId, report.relatedFieldCandidate.candidateId);
  assert.equal(savedRelatedStep.operation.relatedSource.choiceId, report.currentRelatedChoiceId);
  await clickNative(nativePage, `[data-testid="construction-history-step-${stepId}"]`);
  const removeStepButton = nativePage.locator(`[data-testid="construction-remove-step-${stepId}"]`);
  await removeStepButton.waitFor({ state: 'visible', timeout: 5000 });
  assert.equal(await removeStepButton.count(), 1, 'The construction step removal control must be unique');
  assert.equal(await removeStepButton.isEnabled(), true, 'The construction step removal control must be enabled');
  const removalProposalFromIndex = report.nativeRequests.length;
  await clickNative(nativePage, `[data-testid="construction-remove-step-${stepId}"]`);
  const removalPreview = await proposal('remove-related-source-step-preview', removeStarted, groupedRows, removalProposalFromIndex);
  const removalProposal = report.nativeRequests.findLast((entry) => entry.startedAt >= removeStarted && entry.complete &&
    entry.path.endsWith('/construction-proposals') && protocolResponse(entry)?.proposalId === removalPreview.proposalId);
  assert(removalProposal, 'The visible related-step removal must match a captured native construction proposal');
  assert.equal(removalProposal.status, 200, JSON.stringify(removalProposal.response));
  assert.deepEqual(removalProposal.request?.removeStepIds, [stepId], 'The removal proposal must target only the saved RELATED_SOURCE step');
  assert.deepEqual(removalProposal.request?.candidateConstruction?.steps, groupedBaseline.construction.steps,
    'The removal proposal must restore the exact original Group construction');
  assert.equal(protocolResponse(removalProposal)?.previewStatus, 'READY', JSON.stringify(removalProposal.response));
  removeApplyStarted = Date.now();
  await clickNative(nativePage, '[data-testid="construction-apply-proposal"]');
  removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
    item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === removalPreview.proposalId), removeFromIndex);
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  assert.equal(removeCommand.request.commands.length, 1);
  await waitForHidden(nativePage, '[data-testid="construction-proposal-panel"]', 5000);
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  builder = await api(`${base}/builder`);
  assert.deepEqual(doc().columns, groupedBaseline.columns, `Removing the related Observation.${relatedFieldPath} column must restore the original source columns`);
  assert.deepEqual(doc().construction, groupedBaseline.construction, 'Removing the related field column must restore the original Group construction');
  assert.deepEqual(doc().population, groupedBaseline.population, 'Removing the related field column must restore the original exact source membership');
  const restoredRows = groupedRows;
  await rendered(restoredRows);
  record('apply-remove-related-step-restores-native-group-table', removeApplyStarted, { commandStatus: removeCommand.status });
  const restoredPreview = await openTable(restoredRows, 'reload-restored-available-witness-group-table');
  assert.equal(restoredPreview.rowCount, witnesses.length);
  assert(!restoredPreview.columns.some((column) => column.column === relatedColumn.column), 'Reloaded Group preview still contains the removed related value');
  for (const witness of witnesses) {
    const row = restoredPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Reloaded Group preview omitted ${witness.category} witness`);
    assert.equal(row.row_count, witness.expectedContributorRows, `${witness.category} grouped count differs from the raw CDA oracle`);
    assert.equal(Object.hasOwn(row, relatedColumn.column), false, `${witness.category} restored row still contains the removed related value`);
  }

  if (zeroObservationMode) {
    const [finalWitness] = rereadZeroObservationWitness();
    assert(finalWitness, 'The selected Patient disappeared during the native lifecycle');
    assert.equal(finalWitness.matchingObservationCount, 0,
      'The independent exact Patient reread found an incoming Observation edge after the native lifecycle');
    assert.equal(finalWitness.patient.id, witnesses[0].patient.id,
      'The exact scoped Patient identity changed during the native lifecycle');
    report.oracle.finalScopedRereadMatched = true;
  }
  if (referenceFieldMode) {
    const [finalReference] = rawQuery(report.oracle.exactMembershipQuery);
    assert(finalReference, 'The pinned source witness disappeared during the native lifecycle');
    assert.deepEqual(finalReference.observations.map(({ _id, specimenReference }) => ({ _id, specimenReference })),
      manyWitness.observationKeys.map((_id, index) => ({ _id, specimenReference: manyWitness.observationReferences[index] })),
      'The independent raw Observation reference membership changed during the native Explorer lifecycle');
    report.referenceOracleAssertions.finalRawRereadMatched = true;
  }
  await settleNativeResponses();
  await officialRequestCapture.flush();
  const expectedFailurePath = '/construction-proposals';
  const expectedHttpFailures = report.nativeRequests.filter(entry =>
    expectedHttpValidation({ ...entry, response: protocolResponse(entry) }));
  for (const expectedFailure of expectedHttpFailures) {
    const fixtureEntry = cda.nativeRequests.findLast(entry => entry.path === expectedFailure.path &&
      entry.requestId === expectedFailure.requestCorrelationId && entry.status === 422);
    assert(fixtureEntry, 'The official CDA fixture must capture the same exact expected ONE validation response');
    await cda.waitForCapturedResponse(officialRequestCapture, entry => entry === fixtureEntry, 5000);
    cda.expectHttpFailure(fixtureEntry, 'The independent raw CDA witness predicts multiple related values for grouped-row ONE', {
      outputPath: expectedFailure.path,
      method: expectedFailure.method,
      status: expectedFailure.status,
      requestId: expectedFailure.requestCorrelationId,
      errorCode: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES',
      fieldMode,
      witnessObservationIds: manyWitness.observationIds,
    });
  }
  const unexpectedHttp = report.nativeRequests.filter((entry) => entry.status >= 400 && !expectedHttpFailures.includes(entry));
  const expectedOneFailureCount = basicMode || zeroObservationMode ? 0 : 1;
  assert.equal(expectedHttpFailures.length, expectedOneFailureCount, basicMode
    ? 'The exact single-Observation basic witness must permit ONE'
    : zeroObservationMode
      ? 'The exact zero-Observation root must permit ONE with the nullable result defined by the source contract'
      : 'Exactly one raw-oracle-predicted ONE disagreement is expected');
  assert.deepEqual(unexpectedHttp, [], 'No unexpected browser HTTP errors are allowed');
  assert.deepEqual(report.errors.filter((error) => error.expectedOwnerCancellation !== true && error.expectedValidation !== true), [],
    'No unexpected browser runtime, console, network, or module errors are allowed');
  report.browserDiagnostics = cda.diagnostics;
  const unexpectedPlaywrightHttp = cda.diagnostics.httpFailures.filter((failure) =>
    !(failure.status === 422 && failure.url.endsWith(expectedFailurePath) &&
      failure.body?.includes('CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES')));
  assert.deepEqual(unexpectedPlaywrightHttp, [], 'Playwright observed an unexpected application HTTP failure');
  const unexpectedPlaywrightNetwork = cda.diagnostics.networkFailures.filter((failure) =>
    !(failure.expected === true && typeof failure.browserRequestId === 'string' &&
      failure.expectedCancellation?.browserRequestId === failure.browserRequestId));
  assert.deepEqual(unexpectedPlaywrightNetwork, [], 'Playwright observed an unowned application request failure');
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Playwright observed an unexpected page error');
  assert.deepEqual(cda.diagnostics.console, [], 'Playwright observed an unexpected application console error');
  assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
  report.relatedFieldLifecycle = 'passed';
  if (!basicMode && !zeroObservationMode) report.repairStatus = 'passed';
  if ((report.oracle.missingWitnessCategories ?? []).length > 0) {
    const categories = report.oracle.missingWitnessCategories.map(({ category }) => category);
    report.status = 'unverified';
    report.productFailure = false;
    report.unverifiedReason = `The grouped-row ONE-to-ALL lifecycle passed, but bounded raw witnesses were unavailable for: ${categories.join(', ')}.`;
    report.unverified = {
      kind: 'bounded-optional-witness-unavailable', message: report.unverifiedReason,
      diagnostics: report.oracle.fixtureAvailability,
    };
    report.skipReason = report.unverifiedReason;
  } else {
    report.status = 'passed';
  }
} catch (error) {
  const apiBuildInvalidation = error instanceof ApiBuildFreezeError || error?.invalidatesRun === true;
  const rawOracleUnavailable = verificationPhase === 'raw-oracle' && !apiBuildInvalidation;
  report.status = apiBuildInvalidation ? 'invalidated' : rawOracleUnavailable ? 'unverified' : 'failed';
  if (apiBuildInvalidation) {
    report.productFailure = false;
    report.apiBuildFreeze = { ...report.apiBuildFreeze, invalidatesRun: true, productFailure: false,
      error: String(error), reason: error.reason, before: error.before, after: error.after };
  }
  if (rawOracleUnavailable) {
    report.productFailure = false;
    report.skipReason = report.unverifiedReason ?? String(error?.message ?? error);
    report.unverifiedReason = error.message;
    report.unverified = {
      kind: report.oracle?.fixtureAvailability ? 'raw-witness-oracle' : 'raw-witness-oracle-or-query',
      message: error.message,
      diagnostics: {
        fixtureAvailability: report.oracle?.fixtureAvailability,
        searchBounds: report.oracle?.searchBounds,
        boundedAbsence: report.oracle?.boundedAbsence,
        witnessQueries: report.oracle?.witnessQueries,
      },
    };
  }
  report.error = String(error.stack ?? error);
  report.failureUI = nativePage ? await inspectPage(nativePage, () => {
    const dialog = document.querySelector('[role="dialog"]');
    const policy = dialog?.querySelector('select[aria-label="Values per grouped row"]');
    const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
    return { body: document.body.innerText.slice(0, 5000), chooser: { open: Boolean(dialog), policy: policy?.value },
      proposal: { status: proposal?.dataset.proposalStatus, text: proposal?.innerText } };
  }).catch(captureError => sanitizeText(captureError.message)) : undefined;
  report.savedBuilderAtFailure = builder ? await api(`${base}/builder`).catch((readError) => ({ readError: sanitizeText(readError.message) })) : undefined;
  if (!rawOracleUnavailable) report.status = report.status === 'invalidated' ? 'invalidated' : 'failed';
} finally {
  if (apiBuildCheckStarted && frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true,
        productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after };
      report.status = 'invalidated';
    }
  }
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    report.status = 'invalidated';
  }
  const sourceFingerprintAfter = sourceFingerprint(sourceRoot);
  report.sourceFingerprint.after = sourceFingerprintAfter;
  report.sourceFingerprint.unchanged = report.sourceFingerprint.before.sha256 === sourceFingerprintAfter.sha256
    && report.sourceFingerprint.before.files === sourceFingerprintAfter.files;
  report.sourceFingerprint.invalidatesRun = !report.sourceFingerprint.unchanged;
  report.sourceFingerprint.productFailure = false;
  if (!report.sourceFingerprint.unchanged) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFingerprint.changed = true;
    report.status = 'invalidated';
  }
  report.finished = new Date().toISOString();
  reportFinalized = true;
  cda.report.standaloneCdaRows = report;
  await cda.attachReport('standalone-cda-related-one-all.json', report);
  if (report.status === 'passed' || (report.status === 'unverified' && report.relatedFieldLifecycle === 'passed')) {
    const checkName = zeroObservationMode
      ? 'zero Patient Observation ONE/ALL preserves the exact scoped parent through the native lifecycle'
      : 'related ONE/ALL lifecycle preserves exact raw-source values and row identities';
    cda.check('correctness', checkName, true, {
      mode, fieldMode, witnessMode, cases: report.cases.map(({ name }) => name),
      ...(zeroObservationMode ? {
        scopedZeroMatch: report.oracle.finalScopedRereadMatched === true,
        emptyPolicy: report.zeroObservationExpansion?.savedPolicy,
        oneValueIsNull: report.zeroObservationOne?.nullableValueIsNull === true,
        allValueIsEmptyArray: report.zeroObservationAll?.previewValueIsEmptyArray === true,
        parentRowCount: report.oracle.witnessSummary?.rootCount,
        expectedOneValidationCount: 0,
      } : {}),
      exactRawOracle: true, oneValidationCount: report.nativeRequests.filter(entry => entry.status === 422).length,
    });
  }
  if (report.status === 'failed' || report.status === 'invalidated') {
    throw new Error(`Related ONE/ALL workflow ${report.status}: ${report.error ?? JSON.stringify(report.apiBuildFreeze ?? report.sourceFreeze)}`);
  }
}
return report;
}
