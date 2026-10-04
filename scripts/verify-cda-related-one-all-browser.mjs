import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { ApiBuildFreezeError, captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';
import { relatedSourceProposalCandidate } from './lib/related-source-capture.mjs';
import { classifyExpectedOwnedCancellation, classifyNativeRequestOwnerRetirement } from './lib/native-request-ownership.mjs';
import { installNativeAbortProbe, nativeAbortProbeEvidenceForRequest } from './lib/native-abort-probe.mjs';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

// Adapted from verify-cda-group-related-values-browser.mjs and
// verify-cda-coded-field-lifecycle-browser.mjs. The raw CDA oracle is kept
// separate from Explorer previews and bounds independent witnesses.
const mode = process.env.LOOM_RELATED_ONE_ALL_MODE ?? 'cda';
assert(['basic', 'cda'].includes(mode), 'LOOM_RELATED_ONE_ALL_MODE must be basic or cda');
const basicMode = mode === 'basic';
const project = basicMode ? (process.env.LOOM_DEV_PROJECT ?? 'loom_dev_c89a69d7e137') : (process.env.LOOM_CDA_PROJECT ?? 'loom_dev_cda_fhir');
const generation = basicMode ? (process.env.LOOM_DEV_GENERATION ?? 'fixture-v1') : (process.env.LOOM_CDA_GENERATION ?? 'cda-fhir-v1');
const fieldMode = process.env.LOOM_RELATED_ONE_ALL_FIELD ?? 'id';
assert(['id', 'status', 'specimen-reference'].includes(fieldMode),
  'LOOM_RELATED_ONE_ALL_FIELD must be id, status, or specimen-reference');
const statusFieldMode = fieldMode === 'status';
const referenceFieldMode = fieldMode === 'specimen-reference';
const relatedSourceMode = statusFieldMode || referenceFieldMode;
assert(!basicMode || statusFieldMode, 'The basic fixture mode is only defined for Observation.status');
const relatedFieldPath = statusFieldMode ? 'status' : referenceFieldMode ? 'specimen.reference' : 'id';
const relatedOutputLabel = statusFieldMode ? 'Observation status' : referenceFieldMode ? 'Observation specimen reference' : 'Observation ID';
const relatedChoiceLabel = statusFieldMode ? 'Status' : referenceFieldMode ? 'Specimen Reference' : relatedOutputLabel;
const logicalTypeExpected = 'string';
const cardinalityExpected = relatedSourceMode ? 'optional_one' : undefined;
const referenceWitness = {
  specimenKey: 'Specimen/g_00003b7dca775d32e82a19f3c3c1227d234ca8d3486df068194137e93b1f796b',
  specimenId: 'b7cad184-db67-5542-a975-10fffa3e89e7',
  patientId: 'afcfb15e-7617-5691-ae2c-ab675322fb33',
};
const rootResourceType = basicMode ? 'Patient' : 'Specimen';
const expectedRelatedRouteTypes = basicMode ? ['Patient', 'Observation'] : ['Specimen', 'Patient', 'Observation'];
const expectedReferenceRoute = [
  { fromResourceType: 'Specimen', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const patientObservationRouteLabel = basicMode
  ? `${relatedChoiceLabel}: Patient <-[subject]- Observation`
  : `${relatedChoiceLabel}: Specimen -[subject]-> Patient <-[subject]- Observation`;
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `related-one-all-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-related-one-all-${Date.now()}`;
const apiOrigin = process.env.LOOM_API_ORIGIN ?? process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_UI_ORIGIN ?? process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const apiBuildContainer = localCDAApiContainer();
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const report = {
  explorer, project, generation, protectedExplorerUntouched: true, relatedChoiceAssertions: [],
  mode: basicMode ? 'basic-fixture' : 'cda', fieldMode,
  scenario: referenceFieldMode
    ? 'Pinned current-generation Specimen→Patient→Observation witness with 29 distinct nonnull Observation.specimen.reference values and two nulls; native grouped-row ONE rejection and same-chooser ALL repair preserve one protocol value per terminal Observation identity.'
    : statusFieldMode
      ? basicMode
        ? 'Basic Patient-root fixture with exact Patient→Observation edges, a direct optional_one Observation.status scalar source, and ONE/ALL preview lifecycle.'
        : 'Patient-grouped CDA rows with the direct related Observation.status scalar source; a bounded multi-status witness predicts ONE rejection and same-chooser ALL repair.'
      : 'Patient-grouped CDA rows from one selected Specimen per witness, with related Observation ID values; a many-Observation witness must reproduce grouped-row ONE rejection and same-chooser ALL repair. Zero/one Observation witnesses are additional coverage when available.',
  started: new Date().toISOString(), cases: [], apiCalls: [], errors: [], nativeRequests: [],
  nativeAbortProbeEvents: [],
  frameNavigations: [], executionContextRetirements: [], expectedOwnerCancellations: [],
  ...(statusFieldMode ? { statusSourceContract: { path: 'Observation.status', logicalType: 'string', cardinality: 'optional_one',
    catalogProjectionMode: 'VALUE', relatedSourceForm: 'ALL',
    note: 'FHIR code primitive is represented as string per Observation; the related source retains all matching records.' } } : {}),
  ...(referenceFieldMode ? { referenceSourceContract: { path: 'Observation.specimen.reference', logicalType: 'string', cardinality: 'optional_one',
    nullable: true, route: ['Specimen', 'Patient', 'Observation'], form: 'ALL', terminalIdentity: '_id',
    duplicateValuePolicy: 'Preserve one value per distinct terminal Observation identity; do not collapse by field value.',
    note: 'The pinned oracle has 31 distinct Observation records: 29 distinct nonnull references and two null/missing references. The native ALL protocol array must retain both null entries.' } } : {}),
};
await mkdir(evidence, { recursive: true });
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
report.sourceFingerprint = { root: sourceRoot, before: sourceFingerprint(sourceRoot) };
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

let browser;
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
  ownerRetirement: classifyNativeRequestOwnerRetirement(entry, {
    frameNavigations: report.frameNavigations,
    executionContextRetirements: report.executionContextRetirements,
  }),
  laterFrameNavigations: report.frameNavigations.filter((event) => event.frameId === entry.frameId && event.at >= entry.startedAt).slice(0, 5),
  laterExecutionContextRetirements: report.executionContextRetirements.filter((event) => event.frameId === entry.frameId && event.at >= entry.startedAt).slice(0, 5),
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
  report.apiCalls.push({ path, status: response.status, body, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};
const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1',
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
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
const relatedValuesFor = (witness) => statusFieldMode ? witness.observationStatuses : referenceFieldMode ? witness.observationReferences : witness.observationIds;
const relatedDisplayValuesFor = (witness) => relatedValuesFor(witness).filter((value) => value !== null && value !== undefined).join('; ');
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const readFixtureNDJSON = async (relativePath) => (await readFile(fileURLToPath(new URL(relativePath, import.meta.url)), 'utf8'))
  .split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
const startNativeCapture = async () => {
  await installNativeAbortProbe(browser.cdp, {
    project,
    explorer,
    onEvent: (event) => report.nativeAbortProbeEvents.push(event),
  });
  const defaultContexts = new Map();
  const activeMainFrameLoader = new Map();
  browser.cdp.on('Runtime.executionContextCreated', ({ context }) => {
    const auxData = context.auxData ?? {};
    if (auxData.isDefault !== true || typeof auxData.frameId !== 'string') return;
    defaultContexts.set(context.id, {
      frameId: auxData.frameId,
      executionContextId: context.id,
      uniqueId: context.uniqueId,
      createdAt: Date.now(),
    });
  });
  browser.cdp.on('Runtime.executionContextDestroyed', ({ executionContextId, executionContextUniqueId }) => {
    const context = defaultContexts.get(executionContextId);
    if (!context) return;
    report.executionContextRetirements.push({
      ...context,
      ...(executionContextUniqueId ? { destroyedUniqueId: executionContextUniqueId } : {}),
      isDefault: true,
      eventType: 'Runtime.executionContextDestroyed',
      at: Date.now(),
    });
    defaultContexts.delete(executionContextId);
  });
  browser.cdp.on('Runtime.executionContextsCleared', () => {
    const at = Date.now();
    for (const context of defaultContexts.values()) {
      report.executionContextRetirements.push({
        ...context,
        isDefault: true,
        eventType: 'Runtime.executionContextsCleared',
        at,
      });
    }
    defaultContexts.clear();
  });
  browser.cdp.on('Page.frameNavigated', ({ frame }) => {
    if (frame.parentId) return;
    let location;
    try { location = new URL(frame.url); } catch { return; }
    if (location.origin !== uiOrigin) return;
    report.frameNavigations.push({
      frameId: frame.id,
      loaderId: frame.loaderId,
      isMainFrame: true,
      at: Date.now(),
      path: location.pathname,
      project: location.searchParams.get('project'),
      explorer: location.searchParams.get('explorer'),
      mode: location.searchParams.get('mode'),
    });
    activeMainFrameLoader.set(frame.id, { loaderId: frame.loaderId, committedAt: Date.now() });
  });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, frameId, loaderId, type, timestamp, wallTime, initiator, redirectResponse }) => {
    const url = new URL(request.url);
    if (url.pathname.includes(`/explorers/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
    if (!url.pathname.includes(`/explorers/${explorer}/authoring/v2/`)) return;
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = request.postData; }
    const entry = {
      requestId,
      method: request.method,
      url: request.url,
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      request: body,
      requestCorrelationId: Object.entries(request.headers ?? {}).find(([name]) => name.toLowerCase() === 'x-request-id')?.[1],
      scopeProject: project,
      scopeExplorer: explorer,
      frameId,
      loaderId,
      owningFrameWasCurrentMainLoader: activeMainFrameLoader.get(frameId)?.loaderId === loaderId,
      owningFrameLoaderCommittedAt: activeMainFrameLoader.get(frameId)?.committedAt,
      initiatorExecutionContexts: [...defaultContexts.values()]
        .filter((context) => context.frameId === frameId && context.createdAt <= Date.now())
        .map(({ executionContextId, uniqueId, createdAt }) => ({ executionContextId, uniqueId, createdAt })),
      resourceType: type,
      requestTimestamp: timestamp,
      requestWallTime: wallTime,
      initiator: nativeInitiatorSummary(initiator),
      ...(redirectResponse ? { redirect: { status: redirectResponse.status, url: redirectResponse.url, timestamp } } : {}),
      startedAt: Date.now(),
      status: undefined,
      response: undefined,
      networkTerminal: false,
      bodyReadStatus: 'pending',
      bodyError: undefined,
      complete: false,
    };
    nativeByRequestId.set(requestId, entry);
    report.nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response, timestamp }) => {
    const entry = nativeByRequestId.get(requestId);
    if (entry) {
      entry.status = response.status;
      entry.responseTimestamp = timestamp;
      entry.responseReceivedAt = Date.now();
    }
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId, timestamp, encodedDataLength }) => {
    const entry = nativeByRequestId.get(requestId);
    if (!entry) return;
    entry.networkTerminal = true;
    entry.loadingFinished = { timestamp, at: Date.now(), encodedDataLength };
    entry.bodyReadStatus = 'reading';
    void browser.cdp.send('Network.getResponseBody', { requestId }).then((body) => {
      const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
      if (!reportFinalized) {
        try { entry.response = JSON.parse(raw); } catch { entry.response = raw; }
        entry.bodyReadStatus = 'decoded';
        entry.complete = true;
      }
    }).catch((error) => {
      if (!reportFinalized) {
        entry.bodyError = String(error);
        entry.bodyReadStatus = 'failed';
        report.errors.push({ kind: 'native-response-body', path: entry.path, status: entry.status, message: entry.bodyError });
      }
    }).finally(() => {
      if (!reportFinalized) entry.completedAt = Date.now();
    });
  });
  browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    report.errors.push({ kind: 'runtime', message: exceptionDetails.exception?.description ?? exceptionDetails.text });
  });
  browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.errors.push({ kind: 'console', message: args.map((arg) => arg.value ?? arg.description ?? '').join(' ').slice(0, 400) });
  });
  browser.cdp.on('Network.loadingFailed', ({ requestId, type, errorText, timestamp, canceled, blockedReason, corsErrorStatus }) => {
    const entry = nativeByRequestId.get(requestId);
    if (entry) {
      entry.networkTerminal = true;
      entry.bodyReadStatus = 'failed';
      entry.bodyError = `Network.loadingFailed: ${errorText}`;
      entry.loadingFailed = {
        timestamp,
        at: Date.now(),
        type,
        errorText,
        canceled: Boolean(canceled),
        blockedReason,
        corsErrorStatus: corsErrorStatus ? { code: corsErrorStatus.code, failedParameter: corsErrorStatus.failedParameter } : undefined,
      };
      report.errors.push({ kind: 'native-request', requestId, requestCorrelationId: entry.requestCorrelationId, path: entry.path, frameId: entry.frameId,
        loaderId: entry.loaderId, resourceType: type, message: entry.bodyError, canceled: Boolean(canceled), blockedReason });
    }
    if (type === 'Script' && errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', message: errorText });
  });
};
const settleNativeResponses = async (timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  const collectAbortProbeEvidence = () => {
    for (const entry of report.nativeRequests) {
      if (!entry.loadingFailed) continue;
      entry.abortControllerProbeEvidence = nativeAbortProbeEvidenceForRequest(entry, report.nativeAbortProbeEvents);
    }
  };
  const collectExpectedOwnerCancellations = () => {
    for (const entry of report.nativeRequests) {
      if (entry.expectedOwnerCancellation || entry.bodyReadStatus !== 'failed') continue;
      const decision = classifyExpectedOwnedCancellation(entry, {
        frameNavigations: report.frameNavigations,
        executionContextRetirements: report.executionContextRetirements,
      });
      if (!decision.expected) continue;
      entry.ownerRetirement = decision.ownerRetirement;
      entry.expectedOwnerCancellation = decision;
      report.expectedOwnerCancellations.push({
        requestId: entry.requestId,
        requestCorrelationId: entry.requestCorrelationId,
        path: entry.path,
        owner: decision.ownerRetirement.owner,
        loadingFailed: entry.loadingFailed,
        ownerRetirement: decision.ownerRetirement,
        networkTerminal: entry.networkTerminal,
        bodyReadStatus: entry.bodyReadStatus,
        complete: entry.complete,
      });
      for (const error of report.errors) {
        if (error.kind === 'native-request' && error.requestId === entry.requestId) {
          error.expectedOwnerCancellation = true;
          error.ownerRetirement = decision.ownerRetirement;
        }
      }
    }
  };
  while (Date.now() < deadline) {
    collectAbortProbeEvidence();
    collectExpectedOwnerCancellations();
    const pending = report.nativeRequests.filter((entry) => !entry.networkTerminal || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
    const pendingOwnership = pending.map((entry) => ({ entry, decision: classifyNativeRequestOwnerRetirement(entry, {
      frameNavigations: report.frameNavigations,
      executionContextRetirements: report.executionContextRetirements,
    }) }));
    const unresolved = pendingOwnership.filter(({ decision }) => !decision.ownerRetired);
    if (unresolved.length === 0) {
      const retiredOwners = pendingOwnership.filter(({ decision }) => decision.ownerRetired).map(({ entry, decision }) => {
        entry.ownerRetirement = { ...decision, observedAt: Date.now(), networkCompleted: false, networkTerminal: false, bodyReadStatus: 'pending' };
        return entry;
      });
      collectExpectedOwnerCancellations();
      assert.deepEqual(report.nativeRequests.filter((entry) => entry.bodyReadStatus !== 'decoded' &&
        entry.ownerRetirement?.ownerRetired !== true && entry.expectedOwnerCancellation?.expected !== true), [],
        'Every captured native API response must have a successfully decoded body');
      report.nativeResponseDrain = {
        status: 'complete',
        decodedBodies: report.nativeRequests.filter((entry) => entry.bodyReadStatus === 'decoded').length,
        retiredIncompleteRequests: retiredOwners.map((entry) => ({
          requestId: entry.requestId,
          requestCorrelationId: entry.requestCorrelationId,
          path: entry.path,
          frameId: entry.frameId,
          loaderId: entry.loaderId,
          bodyReadStatus: entry.bodyReadStatus,
          networkTerminal: entry.networkTerminal,
          ownerRetirement: entry.ownerRetirement,
        })),
        expectedOwnerCancellations: report.expectedOwnerCancellations.map((entry) => ({
          requestId: entry.requestId,
          requestCorrelationId: entry.requestCorrelationId,
          path: entry.path,
          owner: entry.owner,
          networkTerminal: entry.networkTerminal,
          bodyReadStatus: entry.bodyReadStatus,
          loadingFailed: entry.loadingFailed,
        })),
      };
      return;
    }
    await pause(Math.min(25, Math.max(1, deadline - Date.now())));
  }
  collectAbortProbeEvidence();
  collectExpectedOwnerCancellations();
  const pending = report.nativeRequests.filter((entry) => !entry.networkTerminal || entry.bodyReadStatus === 'pending' || entry.bodyReadStatus === 'reading');
  report.nativeResponseDrain = {
    status: 'timed-out',
    timeoutMs,
    expectedOwnerCancellations: report.expectedOwnerCancellations.map((entry) => ({
      requestId: entry.requestId, path: entry.path, owner: entry.owner, loadingFailed: entry.loadingFailed,
    })),
    pending: pending.map(pendingNativeEvidence),
  };
  throw new Error(`Timed out draining native API responses: ${JSON.stringify(pending.map(pendingNativeEvidence))}`);
};
const waitNative = async (predicate, fromIndex = 0, timeoutMs = 5000) => {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const match = report.nativeRequests.slice(fromIndex).find((entry) => entry.complete && predicate(entry));
    if (match) return match;
    await pause(50);
  }
  throw new Error(`Timed out waiting for native request: ${JSON.stringify(report.nativeRequests.slice(fromIndex).map(({ method, url, path, query, status, request, bodyReadStatus, bodyError }) => ({ method, url, path, query, status, request, bodyReadStatus, bodyError })))}`);
};
const nativeRequestValue = (entry, key) => entry.request?.[key] ?? entry.query?.[key];
const record = (name, started, details = {}) => {
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `${name} took ${durationMs} ms`);
  report.cases.push({ name, durationMs, ...details });
};
const rendered = async (expectedRows) => {
  const columnCount = expectedRows[0]?.length ?? 2;
  await waitForBrowser(browser.cdp, `(() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return table?.getAttribute('aria-colcount')===${JSON.stringify(String(columnCount))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:');})()`, 5000);
  const rows = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length);`);
  assert(rows.length > 0 || expectedRows.length === 0);
  for (const row of rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `Visible row is not in the independent CDA witness: ${JSON.stringify(row)}`);
};
const openTable = async (expectedRows, name) => {
  const started = Date.now();
  const fromIndex = report.nativeRequests.length;
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`, 5000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await rendered(expectedRows);
  const preview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.request?.outputId === outputId, fromIndex);
  assert(preview.response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: preview.response.receiptId, rowCount: preview.response.rowCount });
  return preview.response;
};
const proposal = async (name, started, expectedRows) => {
  const deadline = started + 5000;
  let adopted;
  while (!adopted) {
    const proposalID = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalId;`);
    adopted = report.nativeRequests.findLast((entry) => entry.startedAt >= started && entry.complete &&
      entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === proposalID);
    if (adopted) break;
    assert(Date.now() < deadline, `${name} did not adopt a fresh native construction proposal within five seconds`);
    await pause(50);
  }
  await waitForBrowser(browser.cdp, `['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)`, 5000);
  const value = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {proposalId:panel?.dataset.proposalId,status:panel?.dataset.proposalStatus,text:panel?.innerText,rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')].map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))};`);
  assert.equal(value.status, 'ready', `${name}: ${value.text}`);
  assert.equal(value.rows.length, Math.min(25, expectedRows.length));
  for (const row of value.rows) assert(expectedRows.some((expected) => JSON.stringify(expected) === JSON.stringify(row)), `${name} differs from the raw CDA witness: ${JSON.stringify(row)}`);
  record(name, started, { proposalId: adopted.response.proposalId, rows: value.rows });
  return value;
};
const applyProposal = async (expectedRows, name) => {
  const started = Date.now();
  await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  await rendered(expectedRows);
  record(name, started);
  builder = await api(`${base}/builder`);
};
const waitRelatedProposal = async (name, started, expectedRequestPolicy, fromIndex) => {
  if (!relatedSourceMode) {
    const entry = await waitNative((candidate) => candidate.path.endsWith('/construction-choice-proposals') &&
      candidate.request?.constructionChoices?.length === 1 &&
      candidate.request.constructionChoices[0].rowValuePolicy === expectedRequestPolicy, fromIndex);
    record(name, started, { requestedRowValuePolicy: expectedRequestPolicy, status: entry.status,
      responseStatus: entry.response?.previewStatus, previewDurationMs: entry.response?.previewDurationMs });
    return entry;
  }
  const entry = await waitNative((candidate) => relatedSourceProposalCandidate(candidate, {
    candidateId: report.relatedFieldCandidate?.candidateId,
    resourceType: 'Observation',
    path: relatedFieldPath,
  }), fromIndex);
  const match = relatedSourceProposalCandidate(entry, {
    candidateId: report.relatedFieldCandidate?.candidateId,
    resourceType: 'Observation',
    path: relatedFieldPath,
  });
  assert(match, 'The native candidate must contain the exact selected Observation related source');
  assert.equal(match.related.form, 'ALL', 'RELATED_SOURCE must retain the all-matching Observation result form');
  assert.equal(match.related.contributorRule?.policy, 'ALL_MATCHES', 'RELATED_SOURCE must retain all matching records on the selected route');
  if (report.currentRelatedChoiceId) assert.equal(match.related.choiceId, report.currentRelatedChoiceId,
    'The related proposal must retain the exact signed candidate and route inspected from the authorized catalog');
  assert.equal(match.rowValuePolicy, expectedRequestPolicy,
    `The grouped-row selector requested ${expectedRequestPolicy}, but the native RELATED_SOURCE proposal encoded ${match.rowValuePolicy}`);
  assert.equal(match.related.source.logicalType, logicalTypeExpected);
  if (cardinalityExpected) assert.equal(match.related.source.cardinality, cardinalityExpected);
  assert.equal(match.related.route.length, expectedRelatedRouteTypes.length - 1);
  assert(match.related.route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
    hop.toResourceType === expectedRelatedRouteTypes[index + 1]),
  `The related source proposal lost exact route ${expectedRelatedRouteTypes.join(' → ')}`);
  if (referenceFieldMode) assert.deepEqual(match.related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
    ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
  'The nullable-reference repair must retain the exact Specimen→Patient→Observation subject route');
  record(name, started, { status: entry.status, responseStatus: entry.response?.previewStatus,
    previewDurationMs: entry.response?.previewDurationMs, relatedSourceForm: match.related.form,
    requestedRowValuePolicy: expectedRequestPolicy, relatedSourceRowValuePolicy: match.related.rowValuePolicy ?? 'ALL',
    proposalId: entry.response?.proposalId });
  return entry;
};
const waitAdoptedChoicePreview = async (entry, name) => {
  if (!relatedSourceMode) {
    const receiptId = entry.response?.preview?.receiptId;
    assert(receiptId, `${name} response has no preview receipt to adopt`);
    await waitForBrowser(browser.cdp, `(() => {const panel=document.querySelector('[data-testid="construction-choice-proposal-panel"]');const preview=document.querySelector('[data-testid="construction-preview"]');return panel?.dataset.proposalStatus==='ready'&&preview?.dataset.previewStatus==='ready'&&preview?.dataset.previewReceiptId===${JSON.stringify(receiptId)}&&preview?.dataset.previewProposalId===${JSON.stringify(receiptId)};})()`, 5000);
    return receiptId;
  }
  const proposalId = entry.response?.proposalId;
  assert(proposalId, `${name} response has no construction proposal ID`);
  await waitForBrowser(browser.cdp, `(() => {const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return panel?.dataset.proposalStatus==='ready'&&panel?.dataset.proposalId===${JSON.stringify(proposalId)};})()`, 5000);
  const active = await browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {status:panel?.dataset.proposalStatus,proposalId:panel?.dataset.proposalId};`);
  assert.deepEqual(active, { status: 'ready', proposalId }, `${name} must be the active native construction proposal`);
  return proposalId;
};
const expand = async (hop, witnesses, expectedRows) => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled===false`, 5000);
  await click(browser.cdp, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForBrowser(browser.cdp, `document.querySelector('${panel} select[aria-label="Related record type"]')?.disabled===false`, 5000);
  const started = Date.now();
  await selectOption(browser.cdp, `${panel} select[aria-label="Related record type"]`, hop.to);
  const label = hop.from + (hop.direction === 'INBOUND' ? ` <-[${hop.field}]- ` : ` -[${hop.field}]-> `) + hop.to;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`${panel} input[aria-label="${label}"]`)}))`, 5000);
  const proposalFromIndex = report.nativeRequests.length;
  const proposalStarted = Date.now();
  await click(browser.cdp, `${panel} input[aria-label="${label}"]`);
  const value = await proposal(`expand-${hop.from}-${hop.to}-preview`, proposalStarted, expectedRows);
  assert(report.nativeRequests.slice(proposalFromIndex).some((entry) => entry.response?.proposalId === value.proposalId), 'The displayed expansion preview must match its captured native proposal');
  report.cases.at(-1).witnessCount = witnesses.length;
  await applyProposal(expectedRows, `expand-${hop.from}-${hop.to}-apply-to-render`);
};
const openRelatedFieldChooser = async () => {
  const alreadyOpen = await browserEval(browser.cdp, `return Boolean(document.querySelector('[aria-label="Add columns editor"]'));`);
  if (!alreadyOpen) {
    await click(browser.cdp, '[data-testid="construction-action-add-columns"]');
    await click(browser.cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  }
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 5000);
  const relatedResourcesOpen = await browserEval(browser.cdp, `return document.querySelector('[aria-label="Related resources"]')?.open===true;`);
  if (!relatedResourcesOpen) {
    await click(browser.cdp, '[aria-label="Related resources"] summary');
    await waitForBrowser(browser.cdp, `document.querySelector('[aria-label="Related resources"]')?.open===true`, 5000);
  }
  await click(browser.cdp, '[data-testid="construction-add-columns-source-option"][aria-label="Observation, Related resource"]');
  const rawFieldsOpen = await browserEval(browser.cdp, `return document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true;`);
  if (!rawFieldsOpen) {
    await click(browser.cdp, '[data-testid="feature-catalog-raw-fields"] summary');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open===true`, 5000);
  }
  const choiceSearchFrom = report.nativeRequests.length;
  const fieldSelector = `input[aria-label=${JSON.stringify(`Select Observation.${relatedFieldPath}`)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(fieldSelector)}+':not(:disabled)'))`, 5000);
  await click(browser.cdp, fieldSelector);
  await click(browser.cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 5000);
  const otherPaths = await browserEval(browser.cdp, `return [...document.querySelectorAll('[role="dialog"] summary')].some(item=>item.innerText.includes('Other relationship paths'));`);
  if (otherPaths) await click(browser.cdp, '[role="dialog"] summary', { includes: 'Other relationship paths' });
  const routeSelector = `[role="dialog"] input[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(routeSelector)}))`, 5000);
  await click(browser.cdp, routeSelector);
  await waitForBrowser(browser.cdp, `document.querySelector(${JSON.stringify(routeSelector)})?.checked===true`, 5000);
  const formLabel = `${relatedChoiceLabel}: Keep all matching values`;
  const formSelector = `[role="dialog"] input[aria-label=${JSON.stringify(formLabel)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(formSelector)}))`, 5000);
  await click(browser.cdp, formSelector);
  const control = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return {dialog:Boolean(dialog),policyOptions:policy?[...policy.options].map(option=>({value:option.value,label:option.textContent})):[],policy:policy?.value};`);
  assert(control.dialog, 'Related field ONE/ALL chooser is not open');
  assert.deepEqual(control.policyOptions.map((option) => option.value), ['ALL', 'ONE'], 'The current Add columns chooser does not expose grouped-row ONE and ALL');
  if (relatedSourceMode) {
    const candidateIds = new Set(report.relatedFieldCandidates?.map((candidate) => candidate.candidateId));
    assert(candidateIds.size > 0, 'The related field candidate identity must be captured before opening related-choice search');
    const matchesSelectedRoute = (choice) => {
      const source = choice?.source;
      const route = choice?.route;
      return source?.kind === 'FIELD' && candidateIds.has(source.candidateId) &&
        source.resourceType === 'Observation' && source.path === relatedFieldPath &&
        Array.isArray(route) && route.length === expectedRelatedRouteTypes.length - 1 &&
        route.every((hop, index) => hop.fromResourceType === expectedRelatedRouteTypes[index] &&
          hop.toResourceType === expectedRelatedRouteTypes[index + 1]) &&
        (!referenceFieldMode || JSON.stringify(route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
          ({ fromResourceType, toResourceType, relationship, storageDirection }))) === JSON.stringify(expectedReferenceRoute));
    };
    const catalogEntry = await waitNative((entry) => entry.path.endsWith('/construction-choices') &&
      candidateIds.has(entry.request?.source?.candidateId) &&
      entry.response?.choices?.some(matchesSelectedRoute), choiceSearchFrom);
    assert.equal(catalogEntry.status, 200, JSON.stringify(catalogEntry.response));
    assert.equal(catalogEntry.request.outputId, outputId, 'The selected route must come from this owned output’s current catalog request');
    assert.equal(catalogEntry.request.snapshotToken, builder.catalog.snapshotToken, 'The selected route must come from the current pinned catalog snapshot');
    const selectedChoice = catalogEntry.response.choices.find(matchesSelectedRoute);
    assert(selectedChoice?.choiceId, 'The current authorized field catalog must contain the exact selected relationship route');
    assert(selectedChoice.options?.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED'),
      'The exact signed route choice must support preserving all matching records');
    const candidate = report.relatedFieldCandidates.find((item) => item.candidateId === selectedChoice.source.candidateId);
    assert(candidate, 'The selected related source must be one of the current catalog candidates');
    assert.equal(selectedChoice.source.resourceType, 'Observation');
    assert.equal(selectedChoice.source.path, relatedFieldPath);
    if (referenceFieldMode) assert.deepEqual(selectedChoice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
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
        snapshotToken: catalogEntry.request.snapshotToken, truncated: catalogEntry.response.truncated,
        complete: catalogEntry.response.complete } });
    report.currentRelatedChoiceId = selectedChoice.choiceId;
  }
  report.groupedRowPolicyControl = control;
};
const selectRelatedPolicy = async (policy) => {
  await selectOption(browser.cdp, '[role="dialog"] select[aria-label="Values per grouped row"]', policy);
  const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
  const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
  const state = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');return {policy:dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value,routeChecked:dialog?.querySelector(${JSON.stringify(routeSelector)})?.checked,formChecked:dialog?.querySelector(${JSON.stringify(formSelector)})?.checked};`);
  assert.equal(state.policy, policy);
  assert.equal(state.routeChecked, true, 'The exact selected Observation relationship path was lost');
  assert.equal(state.formChecked, true, 'Per-record matching Observation values must remain ALL');
  return state;
};
const clickAddRelated = async () => {
  await click(browser.cdp, '[role="dialog"] button', { name: 'Add 1 column' });
};
const cancelColumnProposal = async () => {
  if (!relatedSourceMode) {
    await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
    await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`, 5000);
    return;
  }
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
};
const readPreviewTable = async (expectedRows, expectedColumns, name) => {
  const started = Date.now();
  const nativeFrom = report.nativeRequests.length;
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`, 5000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `(() => {const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-colcount')===${JSON.stringify(String(expectedColumns))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'));})()`, 5000);
  const dom = await browserEval(browser.cdp, `const area=document.querySelector('[data-testid="preview-table-scroll"]');const table=area?.querySelector('[role="table"]');return {headers:[...area.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()),rows:[...area.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length),columnCount:table?.getAttribute('aria-colcount')};`);
  assert(dom.rows.length > 0 || expectedRows.length === 0, `${name} did not render a witness row: ${JSON.stringify(dom)}`);
  for (const row of dom.rows) assert(expectedRows.some((expected) => row.every((cell, index) => cell === expected[index])), `${name} visible rows differ from the raw witnesses: ${JSON.stringify(row)}`);
  const preview = await waitNative((entry) => entry.path.endsWith('/preview') && entry.request?.outputId === outputId, nativeFrom);
  assert(preview.response?.receiptId, `${name} native Preview has no current receipt`);
  record(name, started, { receiptId: preview.response.receiptId, headers: dom.headers, nativeRowCount: preview.response.rowCount, mountedRowCount: dom.rows.length });
  return preview.response;
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
  if (basicMode) {
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
      const fixturePatients = await readFixtureNDJSON('../testdata/devloop-fixture/Patient.ndjson');
      const fixtureObservations = await readFixtureNDJSON('../testdata/devloop-fixture/Observation.ndjson');
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
      'Pinned Observation.specimen.reference values must be nonempty strings or explicit nulls');
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
        note: 'The compiler sorts and distincts ALL by terminal _id, not by projected value. This fixture has 29 distinct nonnull strings plus two nulls: its 31 identity rows have 30 distinct projected values, so preserving both nulls proves ALL does not collapse by field value.'
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
    observationValues: (FOR o IN observations SORT o.id RETURN o),
    observationKeys: (FOR o IN observations SORT o.id RETURN o._id)
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
      .map((observation) => [observation._id, observation])).values()].sort((a, b) => a.id.localeCompare(b.id));
    const observationIds = observationValues.map((observation) => observation.id);
    assert.deepEqual(observationIds, seed.observations.map((observation) => observation.id).sort(), `${seed.category} finder and exact-membership joins disagree`);
    if (statusFieldMode) {
      assert(observationValues.every((observation) => typeof observation.status === 'string' && observation.status.length > 0),
        `${seed.category} witness contains an Observation without a scalar status code`);
      assert.deepEqual(observationValues.map((observation) => observation.status), seed.observations.map((observation) => observation.status),
        `${seed.category} finder and exact-membership status reads disagree`);
    }
    const expectedCount = seed.category === 'zero' ? 0 : seed.category === 'one' ? 1 : observationIds.length;
    assert.equal(observationIds.length, expectedCount, `${seed.category} witness cardinality changed`);
    const observationStatuses = observationValues.map((observation) => observation.status);
    return {
      category: seed.category, patient: seed.patient, members,
      observationIds, observationCount: observationIds.length,
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
    witnesses: witnesses.map(({ category, patient, members, observationIds, observationCount, observationStatuses, distinctStatusValues, expectedContributorRows }) => ({
      category, patient, observationCount, observationIds,
      ...(statusFieldMode ? { observationStatuses, distinctStatusValues } : {}),
      members: members.map(({ id, _id, patientReference }) => ({ id, _id, patientReference })), expectedContributorRows,
    })),
    selectedSpecimenIds: selectedMembers.map((member) => member.id),
  });
  }

  verificationPhase = 'builder';
  const rootTitle = `${rootResourceType} ID`;
  const tableTitle = referenceFieldMode ? 'Related Observation specimen reference QA' : statusFieldMode ? 'Related Observation status code QA' : 'Related ID ONE to ALL QA';
  await api(root, { name: explorer, title: tableTitle });
  builder = await api(`${base}/builder`);
  assert.equal(builder.catalog.generation, generation);
  if (statusFieldMode) assertStatusCandidate(builder);
  if (referenceFieldMode) assertReferenceCandidate(builder);
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
    ? witness.observationIds.map((observationId) => [member.id, member.patient.id, observationId])
    : [[member.id, member.patient.id, '—']])).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
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

  browser = await launchBrowser(evidence);
  await startNativeCapture();
  await openTable(sourceRows, 'reload-exact-available-cardinality-source-members');
  const sourceBaseline = await api(`${base}/builder`);
  assert.equal(doc(sourceBaseline).population.selectionRevisionId, selection.id);
  assert.deepEqual(doc(sourceBaseline).columns.map((column) => column.label), [rootTitle]);

  let pipelineRows = sourceRows;
  const chain = basicMode ? [] : [
    { from: 'Specimen', to: 'Patient', label: 'subject_Patient', field: 'subject', direction: 'OUTBOUND' },
    { from: 'Patient', to: 'Observation', label: 'subject_Patient', field: 'subject', direction: 'INBOUND' },
  ];
  for (let index = 0; index < chain.length; index += 1) {
    const hop = chain[index];
    const expectedRows = index === 0
      ? selectedMembers.map((member) => [member.id, member.patient.id])
      : relatedRows;
    const witnessesForHop = index === 0 ? selectedMembers : witnesses;
    await expand(hop, witnessesForHop, expectedRows);
    pipelineRows = expectedRows;
  }
  assert.equal(pipelineRows.length, relatedRows.length);
  const expandedDoc = doc();
  const populationBeforeGroup = structuredClone(expandedDoc.population);
  const patientExpansion = basicMode ? undefined : expandedDoc.construction?.steps.find((step) => step.operation.kind === 'RELATED_EXPAND' && step.operation.relatedExpand.targetResourceType === 'Patient');
  const patientGroupOutput = basicMode
    ? expandedDoc.columns.find((column) => column.label === rootTitle)
    : patientExpansion?.outputs.find((output) => output.label === 'Patient FHIR resource ID');
  assert(patientGroupOutput, basicMode ? 'The Patient root ID source must remain available as the Group key' : 'The first related expansion must retain the exact Patient ID group key');
  const groupKeyLabel = patientGroupOutput.label;
  const configureGroup = async () => {
    await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
    await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-group-rows"]')?.disabled===false`, 5000);
    await click(browser.cdp, '[data-testid="construction-action-group-rows"]');
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Group by ${groupKeyLabel}"]:not(:disabled)`)}))`, 5000);
    const started = Date.now();
    await click(browser.cdp, `input[aria-label=${JSON.stringify(`Group by ${groupKeyLabel}`)}]`);
    return started;
  };
  let groupStarted = await configureGroup();
  await proposal('group-available-patient-witnesses-preview-cancel-target', groupStarted, groupedRows);
  const beforeGroupCancel = await api(`${base}/builder`);
  const cancelGroupStarted = Date.now();
  await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  assert.deepEqual((await api(`${base}/builder`)).workspace, beforeGroupCancel.workspace, 'Cancel must preserve the exact expanded source workspace');
  record('cancel-group-proposal-preserves-source-bindings', cancelGroupStarted);
  await openTable(relatedRows, 'reload-expanded-source-after-group-cancel');

  groupStarted = await configureGroup();
  await proposal('group-available-patient-witnesses-preview', groupStarted, groupedRows);
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
  let rejectedChoiceId;
  if (basicMode) {
    await selectRelatedPolicy('ONE');
    const oneStarted = Date.now();
    const oneFromIndex = report.nativeRequests.length;
    await clickAddRelated();
    const oneProposal = await waitRelatedProposal('basic-status-one-preview', oneStarted, 'ONE', oneFromIndex);
    assert.equal(oneProposal.status, 200, JSON.stringify(oneProposal.response));
    assert.equal(oneProposal.response.previewStatus, 'READY', JSON.stringify(oneProposal.response));
    const adoptedOneProposalId = await waitAdoptedChoicePreview(oneProposal, 'Basic status ONE proposal');
    const oneRelatedStep = relatedSourceProposalCandidate(oneProposal, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: 'status',
    })?.step;
    assert(oneRelatedStep, 'The basic ONE preview must contain the selected status RELATED_SOURCE step');
    const oneOutput = oneRelatedStep.outputs.find((candidate) => candidate.id === oneRelatedStep.operation.relatedSource.outputColumnId);
    assert(oneOutput, 'The basic ONE preview must expose its candidate status output');
    const oneRow = oneProposal.response.preview.rows.find((row) => row[groupKeyName] === witnesses[0].patient.id);
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
    const oneError = oneFailure.response?.error?.code ?? oneFailure.response?.code ??
      oneFailure.response?.diagnostics?.find((diagnostic) => diagnostic.severity === 'ERROR')?.code;
    assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
    assert.equal(oneError, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES', JSON.stringify(oneFailure.response));
    assert.notEqual(oneFailure.response?.previewStatus, 'READY', 'Raw multiple-value witness must not pass grouped-row ONE');
    if (!relatedSourceMode) {
      assert.equal(oneFailure.status, 422, JSON.stringify(oneFailure));
      assert.equal(oneFailure.request.constructionChoices[0].form, 'ALL');
      assert.equal(oneFailure.request.constructionChoices[0].rowValuePolicy, 'ONE');
      assert.equal(oneFailure.request.constructionChoices[0].title, relatedChoiceLabel);
      rejectedChoiceId = oneFailure.request.constructionChoices[0].choiceId;
      assert(rejectedChoiceId, 'The ONE attempt must carry the selected signed related-field choice');
    }
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
      proposalId: oneFailure.response?.proposalId };
    if (relatedSourceMode) {
      const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
      const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
      await waitForBrowser(browser.cdp, `(() => {const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return Boolean(dialog&&policy?.value==='ONE'&&dialog.querySelector(${JSON.stringify(routeSelector)})?.checked&&dialog.querySelector(${JSON.stringify(formSelector)})?.checked);})()`, 5000);
      const retainedChooser = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return {open:Boolean(dialog),policy:policy?.value,routeChecked:dialog?.querySelector(${JSON.stringify(routeSelector)})?.checked,formChecked:dialog?.querySelector(${JSON.stringify(formSelector)})?.checked,addEnabled:[...(dialog?.querySelectorAll('button')??[])].some(button=>button.textContent.trim()==='Add 1 column'&&!button.disabled)};`);
      assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true },
        'ONE rejection must retain the same related-source chooser, exact route, form, and source selection for direct repair');
      await selectRelatedPolicy('ALL');
    } else {
      const routeSelector = `[aria-label=${JSON.stringify(patientObservationRouteLabel)}]`;
      const formSelector = `[aria-label=${JSON.stringify(`${relatedChoiceLabel}: Keep all matching values`)}]`;
      const retainedChooser = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');return {open:Boolean(dialog),policy:policy?.value,routeChecked:dialog?.querySelector(${JSON.stringify(routeSelector)})?.checked,formChecked:dialog?.querySelector(${JSON.stringify(formSelector)})?.checked,addEnabled:[...(dialog?.querySelectorAll('button')??[])].some(button=>button.textContent.trim()==='Add 1 column'&&!button.disabled)};`);
      assert.deepEqual(retainedChooser, { open: true, policy: 'ONE', routeChecked: true, formChecked: true, addEnabled: true },
        'ONE rejection must retain the exact route, source form, and editable chooser');
      await selectRelatedPolicy('ALL');
    }
  }
  const allRepairStarted = Date.now();
  const allRepairFromIndex = report.nativeRequests.length;
  await clickAddRelated();
  const firstAll = await waitRelatedProposal('same-chooser-all-repair-preview', allRepairStarted, 'ALL', allRepairFromIndex);
  assert.equal(firstAll.status, 200, JSON.stringify(firstAll.response));
  assert.equal(firstAll.response.previewStatus, 'READY', JSON.stringify(firstAll.response));
  assert(firstAll.response.previewDurationMs <= 5000, `ALL preview took ${firstAll.response.previewDurationMs} ms`);
  const firstAllReceiptId = await waitAdoptedChoicePreview(firstAll, 'Same-chooser ALL repair');
  let previewColumnId;
  if (relatedSourceMode) {
    const relatedProposal = relatedSourceProposalCandidate(firstAll, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(relatedProposal, 'Native ALL preview must propose the exact related Observation field');
    const output = relatedProposal.step.outputs.find((candidate) => candidate.id === relatedProposal.related.outputColumnId);
    assert(output, `Native ALL preview omitted the related ${relatedFieldPath} output`);
    previewColumnId = output.name;
    report.directAllRepair = { policy: relatedProposal.rowValuePolicy, form: relatedProposal.related.form,
      contributorPolicy: relatedProposal.related.contributorRule.policy, receiptId: firstAllReceiptId };
  } else {
    assert.equal(firstAll.request.constructionChoices[0].form, 'ALL');
    assert.equal(firstAll.request.constructionChoices[0].rowValuePolicy, 'ALL');
    assert.equal(firstAll.request.constructionChoices[0].choiceId, rejectedChoiceId, 'Direct ALL repair must reuse the selected related-field route choice');
    report.directAllRepair = { choiceId: firstAll.request.constructionChoices[0].choiceId, policy: firstAll.request.constructionChoices[0].rowValuePolicy, receiptId: firstAllReceiptId };
    previewColumnId = firstAll.response.candidateColumnIds?.[0];
  }
  assert(previewColumnId, `Native ALL preview did not propose the related ${relatedFieldPath} column`);
  const proposedValues = firstAll.response.preview.rows.map((row) => ({ patientReference: row[groupKeyName], values: row[previewColumnId] }));
  assert.equal(proposedValues.length, witnesses.length);
  for (const witness of witnesses) {
    const proposed = proposedValues.find((row) => row.patientReference === witness.patient.id);
    assert(proposed, `Native preview omitted ${witness.category} witness ${witness.patient.reference}`);
    if (referenceFieldMode) {
      assert.equal(proposed.values.length, witness.observationKeys.length,
        'ALL must return one protocol value per distinct terminal Observation identity');
      assert.equal(proposed.values.filter((value) => value === null).length, 2,
        'ALL protocol output must preserve both explicit nulls from Observation.specimen.reference');
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
  assert.equal(allProposal.response.previewStatus, 'READY');
  if (relatedSourceMode) {
    const reopenedRelated = relatedSourceProposalCandidate(allProposal, {
      candidateId: report.relatedFieldCandidate?.candidateId, resourceType: 'Observation', path: relatedFieldPath,
    });
    assert(reopenedRelated, 'Reopened status ALL must retain the exact typed source and relationship route');
    assert.equal(reopenedRelated.rowValuePolicy, 'ALL');
  }
  const allProposalReceiptId = await waitAdoptedChoicePreview(allProposal, 'Reopened ALL proposal');
  report.reopenedAllProposalReceiptId = allProposalReceiptId;
  const applyStarted = Date.now();
  if (relatedSourceMode) await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  else await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  const allCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
    relatedSourceMode
      ? item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === allProposal.response.proposalId
      : item.type === 'APPLY_CONSTRUCTION_CHOICE'), allApplyFromIndex);
  assert.equal(allCommand.status, 200, JSON.stringify(allCommand.response));
  assert.equal(allCommand.request.commands.length, 1);
  if (relatedSourceMode) assert.equal(allCommand.request.commands[0].proposalId, allProposal.response.proposalId);
  else {
    assert.equal(allCommand.request.commands[0].constructionChoice.form, 'ALL');
    assert.equal(allCommand.request.commands[0].constructionChoice.rowValuePolicy, 'ALL');
  }
  await waitForBrowser(browser.cdp, relatedSourceMode
    ? `!document.querySelector('[data-testid="construction-proposal-panel"]')`
    : `!document.querySelector('[data-testid="construction-choice-proposal-panel"]')`, 5000);
  const addedRows = witnesses.map((witness) => [witness.patient.id, String(witness.expectedContributorRows), relatedDisplayValuesFor(witness)]).sort((a, b) => a[0].localeCompare(b[0]));
  await rendered(addedRows);
  record('apply-related-all-to-native-table-render', applyStarted, { commandStatus: allCommand.status });
  builder = await api(`${base}/builder`);
  const addedDocument = doc();
  assert.deepEqual(addedDocument.population, groupedBaseline.population, 'ALL apply must preserve the exact source selection');
  let savedRelatedStep;
  let relatedColumn;
  if (relatedSourceMode) {
    savedRelatedStep = addedDocument.construction.steps.find((step) => step.operation?.kind === 'RELATED_SOURCE' &&
      step.operation.relatedSource?.source?.resourceType === 'Observation' &&
      step.operation.relatedSource?.source?.path === relatedFieldPath);
    assert(savedRelatedStep, `The saved construction must contain RELATED_SOURCE Observation.${relatedFieldPath}`);
    const related = savedRelatedStep.operation.relatedSource;
    assert.equal(related.form, 'ALL');
    assert.equal(related.contributorRule.policy, 'ALL_MATCHES');
    assert.equal(related.rowValuePolicy ?? 'ALL', 'ALL', 'The saved related source must retain the selected grouped-row ALL policy');
    assert.deepEqual(related.route.map((hop) => [hop.fromResourceType, hop.toResourceType]),
      expectedRelatedRouteTypes.slice(0, -1).map((resourceType, index) => [resourceType, expectedRelatedRouteTypes[index + 1]]));
    if (referenceFieldMode) assert.deepEqual(related.route.map(({ fromResourceType, toResourceType, relationship, storageDirection }) =>
      ({ fromResourceType, toResourceType, relationship, storageDirection })), expectedReferenceRoute,
    'The saved RELATED_SOURCE must retain the exact pinned subject route');
    const output = savedRelatedStep.outputs.find((candidate) => candidate.id === related.outputColumnId);
    assert(output, `The saved related step omitted output ${related.outputColumnId}`);
    relatedColumn = { column: output.name, columnId: output.id, label: output.label, logicalType: output.type };
  } else {
    const relatedColumns = addedDocument.columns.filter((column) =>
      column.source?.field?.path === relatedFieldPath && column.source.field.projectionMode === 'ALL');
    assert.equal(relatedColumns.length, 1, `Expected one saved related Observation.${relatedFieldPath} ALL source binding`);
    relatedColumn = relatedColumns[0];
    assert(relatedColumn, `The related Observation.${relatedFieldPath} source binding was not saved`);
    assert.equal(relatedColumn.source.field.path, relatedFieldPath);
    assert.equal(relatedColumn.source.field.projectionMode, 'ALL');
  }
  const proposalOutput = allProposal.response.preview?.columns.find((column) => column.column === relatedColumn.column);
  assert(proposalOutput, 'The accepted ALL proposal preview must contain the exact saved related output');
  assert.equal(relatedColumn.label, proposalOutput.label,
    'Applying the related source must retain the output label shown by its accepted proposal preview');
  if (statusFieldMode) assert.equal(relatedColumn.label, relatedOutputLabel,
    'Observation.status is titled Status in the chooser, while the generated related output defaults to Observation status');
  if (relatedSourceMode) assert.equal(relatedColumn.logicalType, logicalTypeExpected,
    `The persisted related column must match the compiler-proved Observation.${relatedFieldPath} logical type`);
  const savedGroup = addedDocument.construction.steps.find((step) => step.id === groupStepBefore.id);
  assert(savedGroup, 'The original Group step identity must remain stable');
  assert.deepEqual(savedGroup.operation.group.keys, groupStepBefore.operation.group.keys, 'ALL apply changed the authored group key binding');
  assert.deepEqual(savedGroup.operation.group.aggregates, groupStepBefore.operation.group.aggregates, 'ALL apply changed the authored Group aggregate');
  let rowValue;
  let rowValueOutput;
  if (relatedSourceMode) {
    assert.deepEqual(savedGroup.rowValues, groupStepBefore.rowValues,
      'Adding a RELATED_SOURCE must preserve existing GROUP row-value bindings; policy belongs to the related source');
    rowValueOutput = savedRelatedStep.outputs.find((output) => output.id === savedRelatedStep.operation.relatedSource.outputColumnId);
    assert.equal(rowValueOutput?.name, relatedColumn.column);
  } else {
    rowValue = savedGroup.rowValues.find((value) => value.inputColumnId === relatedColumn.columnId);
    assert(rowValue, 'The saved Group lacks the related source column binding');
    assert.equal(rowValue.policy, 'ALL');
    rowValueOutput = savedGroup.outputs.find((output) => output.id === rowValue.outputColumnId);
    assert(rowValueOutput);
    assert.equal(rowValueOutput.name, relatedColumn.column);
  }
  for (const sourceColumn of groupedBaseline.columns) {
    assert.deepEqual(addedDocument.columns.find((column) => column.columnId === sourceColumn.columnId), sourceColumn, `ALL apply changed source binding ${sourceColumn.label}`);
  }
  const applyPreviewFrom = report.nativeRequests.indexOf(allCommand);
  const savedPreviewStarted = Date.now();
  const proposalPreview = allProposal.response.preview;
  assert(proposalPreview, 'The accepted ALL proposal did not retain its candidate preview');
  assert.equal(allProposal.response.candidateWorkspaceDigest, builder.draftDigest, 'The saved builder digest must equal the applied proposal digest');
  assert.equal(allProposal.response.outputId, outputId);
  assert.equal(allCommand.response.draftVersion, builder.draftVersion);
  assert.equal(allCommand.response.draftDigest, builder.draftDigest);
  const acceptedReconcile = await waitNative((entry) => entry.path.endsWith('/reconcile') &&
    entry.request?.draftVersion === builder.draftVersion && entry.request?.draftDigest === builder.draftDigest, applyPreviewFrom);
  assert.equal(acceptedReconcile.status, 200, JSON.stringify(acceptedReconcile.response));
  assert.equal(acceptedReconcile.response.snapshotToken, allProposal.response.snapshotToken);
  assert.equal(acceptedReconcile.response.intentDigest, builder.draftDigest);
  assert(acceptedReconcile.response.outputs?.some((output) => output.outputId === outputId), 'The saved reconcile receipt does not include the edited output');
  assert.equal(proposalPreview.outputId, outputId);
  await waitForBrowser(browser.cdp, `(() => {const preview=document.querySelector('[data-testid="construction-preview"]');return preview?.dataset.previewStatus==='ready'&&preview?.dataset.previewReceiptId===${JSON.stringify(acceptedReconcile.response.receiptId)}&&preview?.dataset.previewOutputId===${JSON.stringify(outputId)}&&preview?.dataset.currentDraftVersion===${JSON.stringify(String(builder.draftVersion))}&&preview?.dataset.currentDraftDigest===${JSON.stringify(builder.draftDigest)};})()`, 5000);
  const activePreview = await browserEval(browser.cdp, `const preview=document.querySelector('[data-testid="construction-preview"]');return {status:preview?.dataset.previewStatus,receiptId:preview?.dataset.previewReceiptId,outputId:preview?.dataset.previewOutputId,draftVersion:preview?.dataset.currentDraftVersion,draftDigest:preview?.dataset.currentDraftDigest};`);
  assert.deepEqual(activePreview, {
    status: 'ready',
    receiptId: acceptedReconcile.response.receiptId,
    outputId,
    draftVersion: String(builder.draftVersion),
    draftDigest: builder.draftDigest,
  }, 'The rendered table must be the active preview for the exact saved draft and accepted receipt');
  const applyPreviewRequests = report.nativeRequests.slice(applyPreviewFrom).filter((entry) => entry.path.endsWith('/preview'));
  for (const entry of applyPreviewRequests) {
    assert(nativeRequestValue(entry, 'outputId'), `Captured ${entry.method} ${entry.path} without outputId body/query: ${JSON.stringify(entry)}`);
    const requestDeadline = Date.now() + 5000;
    while (!entry.complete && Date.now() < requestDeadline) await pause(25);
    assert(entry.complete, `Timed out waiting for captured preview response: ${JSON.stringify({ method: entry.method, url: entry.url, query: entry.query, request: entry.request })}`);
  }
  const targetPreviewRequests = applyPreviewRequests.filter((entry) => nativeRequestValue(entry, 'outputId') === outputId);
  for (const entry of targetPreviewRequests) {
    assert.equal(nativeRequestValue(entry, 'receiptId'), acceptedReconcile.response.receiptId, 'A post-Apply native preview must use the accepted saved receipt');
    assert.equal(entry.status, 200, JSON.stringify(entry.response));
    assert.equal(entry.response?.receiptId, acceptedReconcile.response.receiptId);
    assert.equal(entry.response?.outputId, outputId);
  }
  const previewSource = targetPreviewRequests.length === 0 ? 'direct-preview-for-accepted-saved-receipt' : 'post-apply-native-preview';
  const savedPreview = targetPreviewRequests.at(-1)?.response ?? await api(`${base}/preview`, {
    receiptId: acceptedReconcile.response.receiptId, outputId, limit: 25,
  });
  assert.equal(savedPreview.receiptId, acceptedReconcile.response.receiptId, 'Saved values must be read from the exact accepted receipt');
  assert.equal(savedPreview.outputId, outputId);
  report.savedPreviewVerification = {
    source: previewSource,
    outputId,
    receiptId: acceptedReconcile.response.receiptId,
    savedDraftVersion: builder.draftVersion,
    savedDraftDigest: builder.draftDigest,
    candidateWorkspaceDigest: allProposal.response.candidateWorkspaceDigest,
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
      responseReceiptId: entry.response?.receiptId,
      responseOutputId: entry.response?.outputId,
      rowCount: entry.response?.rowCount,
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
  if (statusFieldMode || referenceFieldMode) {
    const renamedLabel = `Verified ${relatedOutputLabel}`;
    const editStarted = Date.now();
    const editFrom = report.nativeRequests.length;
    await click(browser.cdp, 'button', { name: 'Columns' });
    const labelSelector = `[aria-label=${JSON.stringify(`Column name for ${relatedColumn.label}`)}]`;
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(labelSelector)}))`, 5000);
    const renameState = await browserEval(browser.cdp, `const input=document.querySelector(${JSON.stringify(labelSelector)});return {value:input?.value,disabled:input?.disabled,readOnly:input?.readOnly};`);
    assert.equal(renameState.disabled, false, 'The applied related-field output must remain editable');
    await browserEval(browser.cdp, `document.querySelector(${JSON.stringify(labelSelector)}).scrollIntoView({block:'center',inline:'nearest'});await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));`);
    await click(browser.cdp, labelSelector);
    await browserEval(browser.cdp, 'document.activeElement.select();');
    await browser.cdp.send('Input.insertText', { text: renamedLabel });
    await browser.cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    await browser.cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 });
    const editableStepId = relatedSourceMode ? savedRelatedStep.id : savedGroup.id;
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
    if (relatedSourceMode) {
      assert.equal(editedStep?.operation.relatedSource?.source?.path, relatedFieldPath,
        'Editing the label must preserve the RELATED_SOURCE field binding');
      assert.equal(editedStep?.operation.relatedSource?.rowValuePolicy ?? 'ALL', 'ALL',
        'Editing the label must preserve the related source ONE/ALL policy');
    } else {
      assert.deepEqual(editedStep?.rowValues.find((value) => value.inputColumnId === relatedColumn.columnId), rowValue,
        'Editing the output label must preserve its exact related field binding and ONE/ALL policy');
    }
    await click(browser.cdp, 'button', { name: 'Columns' });
    record('edit-related-field-output-label', editStarted, { priorLabel: relatedColumn.label, newLabel: renamedLabel, path: relatedFieldPath });
    const editedPreview = await openTable(addedRows, 'reload-edited-related-field-output-label');
    const editedHeaders = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"]')].map(cell=>cell.textContent.trim());`);
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
  if (relatedSourceMode) {
    const stepId = savedRelatedStep.id;
    await click(browser.cdp, `[data-testid="construction-history-step-${stepId}"]`);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-remove-step-${stepId}"]:not(:disabled)`)}))`, 5000);
    await click(browser.cdp, `[data-testid="construction-remove-step-${stepId}"]`);
    const removalPreview = await proposal('remove-related-source-step-preview', removeStarted, groupedRows);
    const removalProposal = report.nativeRequests.findLast((entry) => entry.startedAt >= removeStarted && entry.complete &&
      entry.path.endsWith('/construction-proposals') && entry.response?.proposalId === removalPreview.proposalId);
    assert(removalProposal, 'The visible related-step removal must match a captured native construction proposal');
    assert.equal(removalProposal.status, 200, JSON.stringify(removalProposal.response));
    assert.deepEqual(removalProposal.request?.removeStepIds, [stepId], 'The removal proposal must target only the saved RELATED_SOURCE step');
    assert.deepEqual(removalProposal.request?.candidateConstruction?.steps, groupedBaseline.construction.steps,
      'The removal proposal must restore the exact original Group construction');
    assert.equal(removalProposal.response?.previewStatus, 'READY', JSON.stringify(removalProposal.response));
    removeApplyStarted = Date.now();
    await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
    removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) =>
      item.type === 'APPLY_CONSTRUCTION_PROPOSAL' && item.proposalId === removalPreview.proposalId), removeFromIndex);
    assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
    assert.equal(removeCommand.request.commands.length, 1);
    await waitForBrowser(browser.cdp, `!document.querySelector('[data-testid="construction-proposal-panel"]')`, 5000);
  } else {
    await click(browser.cdp, 'button', { name: 'Columns' });
    const removeLabel = `Remove ${activeRelatedColumnLabel} column`;
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(`button[aria-label=${JSON.stringify(removeLabel)}]`)}))`, 5000);
    await click(browser.cdp, `button[aria-label=${JSON.stringify(removeLabel)}]`);
    removeCommand = await waitNative((entry) => entry.path.endsWith('/commands') && entry.request?.commands?.some((item) => item.type === 'REMOVE_COLUMN' && item.column === relatedColumn.column), removeFromIndex);
  }
  assert.equal(removeCommand.status, 200, JSON.stringify(removeCommand.response));
  builder = await api(`${base}/builder`);
  assert.deepEqual(doc().columns, groupedBaseline.columns, `Removing the related Observation.${relatedFieldPath} column must restore the original source columns`);
  assert.deepEqual(doc().construction, groupedBaseline.construction, 'Removing the related field column must restore the original Group construction');
  assert.deepEqual(doc().population, groupedBaseline.population, 'Removing the related field column must restore the original exact source membership');
  const restoredRows = groupedRows;
  await rendered(restoredRows);
  record(relatedSourceMode ? 'apply-remove-related-step-restores-native-group-table' : 'remove-related-column-restores-native-group-table',
    removeApplyStarted, { commandStatus: removeCommand.status });
  const restoredPreview = await openTable(restoredRows, 'reload-restored-available-witness-group-table');
  assert.equal(restoredPreview.rowCount, witnesses.length);
  assert(!restoredPreview.columns.some((column) => column.column === relatedColumn.column), 'Reloaded Group preview still contains the removed related value');
  for (const witness of witnesses) {
    const row = restoredPreview.rows.find((candidate) => candidate[groupKeyName] === witness.patient.id);
    assert(row, `Reloaded Group preview omitted ${witness.category} witness`);
    assert.equal(row.row_count, witness.expectedContributorRows, `${witness.category} grouped count differs from the raw CDA oracle`);
    assert.equal(Object.hasOwn(row, relatedColumn.column), false, `${witness.category} restored row still contains the removed related value`);
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
  const expectedFailurePath = relatedSourceMode ? '/construction-proposals' : '/construction-choice-proposals';
  const expectedHttpFailures = report.nativeRequests.filter((entry) => entry.path.endsWith(expectedFailurePath) &&
    entry.status === 422 && entry.response?.error?.code === 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  const unexpectedHttp = report.nativeRequests.filter((entry) => entry.status >= 400 && !expectedHttpFailures.includes(entry));
  assert.equal(expectedHttpFailures.length, basicMode ? 0 : 1, basicMode
    ? 'The exact single-Observation basic witness must permit ONE'
    : 'Exactly one raw-oracle-predicted ONE disagreement is expected');
  assert.deepEqual(unexpectedHttp, [], 'No unexpected browser HTTP errors are allowed');
  assert.deepEqual(report.errors.filter((error) => error.expectedOwnerCancellation !== true), [],
    'No unexpected browser runtime, console, network, or module errors are allowed');
  assert(report.protectedExplorerUntouched, `A request unexpectedly targeted protected Explorer ${protectedExplorer}`);
  report.relatedFieldLifecycle = 'passed';
  if (!basicMode) report.repairStatus = 'passed';
  if ((report.oracle.missingWitnessCategories ?? []).length > 0) {
    const categories = report.oracle.missingWitnessCategories.map(({ category }) => category);
    report.status = 'unverified';
    report.productFailure = false;
    report.unverifiedReason = `The grouped-row ONE-to-ALL lifecycle passed, but bounded raw witnesses were unavailable for: ${categories.join(', ')}.`;
    report.unverified = {
      kind: 'bounded-optional-witness-unavailable', message: report.unverifiedReason,
      diagnostics: report.oracle.fixtureAvailability,
    };
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
  report.savedBuilderAtFailure = builder ? await api(`${base}/builder`).catch((readError) => ({ readError: String(readError) })) : undefined;
  report.failureUI = browser ? await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const policy=dialog?.querySelector('select[aria-label="Values per grouped row"]');const proposal=document.querySelector('[data-testid="construction-proposal-panel"]')||document.querySelector('[data-testid="construction-choice-proposal-panel"]');return {body:document.body.innerText.slice(0,5000),chooser:{open:Boolean(dialog),policy:policy?.value},proposal:{status:proposal?.dataset.proposalStatus,text:proposal?.innerText}};`).catch(String) : undefined;
  if (!rawOracleUnavailable) process.exitCode = 1;
} finally {
  if (apiBuildCheckStarted && frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { ...report.apiBuildFreeze, unchanged: false, invalidatesRun: true,
        productFailure: false, error: String(error), reason: error.reason, before: error.before, after: error.after };
      process.exitCode = 1;
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
    process.exitCode = 1;
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
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  reportFinalized = true;
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, explorer, cases: report.cases.map(({ name, durationMs }) => ({ name, durationMs })), error: report.error }));
