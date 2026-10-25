import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import { makeTemporalFilterJourneyCaptureFixture } from './fixtures/temporal-filter-journey-schema-fixture.mjs';
import {
  compileTemporalFilterJourneyTrace,
  createTemporalFilterMutationControls,
  TEMPORAL_FILTER_ACTION_EVENT_PAIRS,
  TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS,
  TEMPORAL_FILTER_MUTATION_CONTROL_NAMES,
  validateTemporalFilterJourneyTrace,
} from '../temporal-filter-tla-adapter.mjs';

const testDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(testDirectory, '../../../../');
const modelPath = resolve(repositoryRoot, 'verification/builder-interactions/TemporalFilterJourney.tla');
const dateContractPath = resolve(repositoryRoot, 'verification/builder-interactions/builder-temporal-filter-date-contract.json');
const dateTimeContractPath = resolve(repositoryRoot, 'verification/builder-interactions/builder-temporal-filter-date-time-contract.json');
const tlcJar = process.env.TLA2TOOLS_JAR;

const makeJourney = caseName => adaptTemporalFilterJourney(makeTemporalFilterJourneyCaptureFixture(caseName));

test('DATE and DATE_TIME native contracts bind the exact TLA action/event chronology', () => {
  const requiredCounts = {
    ExplorerCreated: 1,
    SourceTableCreated: 1,
    TemporalColumnApplied: 1,
    'FilterProposed(create)': 2,
    FilterCancelled: 1,
    Reload: 4,
    'FilterApplied(create)': 1,
    FilterEditOpened: 1,
    'FilterProposed(edit)': 1,
    'FilterApplied(edit)': 1,
    'FilterProposed(remove)': 1,
    FilterRemoved: 1,
  };
  const cases = [
    { path: dateContractPath, caseName: 'date', nativeCaseKey: 'basic-temporal-filter/date', valueKind: 'DATE', resourceType: 'Patient', fieldPath: 'birthDate' },
    { path: dateTimeContractPath, caseName: 'date-time', nativeCaseKey: 'basic-temporal-filter/date-time', valueKind: 'DATE_TIME', resourceType: 'Observation', fieldPath: 'effectiveDateTime' },
  ];
  const expectedActions = makeJourney('date').actions.map(({ label, event }) => ({ label, event }));
  assert.deepEqual(expectedActions, TEMPORAL_FILTER_ACTION_EVENT_PAIRS);
  for (const { path, caseName, nativeCaseKey, valueKind, resourceType, fieldPath } of cases) {
    const contract = JSON.parse(readFileSync(path, 'utf8'));
    const nativeJourney = contract.nativeJourney;
    const traceName = `basic-temporal-filter:${caseName}`;
    assert.equal(nativeJourney.captureAdapter, 'temporal-filter-journey-v1');
    assert.equal(nativeJourney.captureKind, 'builder-temporal-native-lifecycle');
    assert.equal(nativeJourney.formalProofAssertion, TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS[caseName]);
    assert.equal(nativeJourney.nativeCaseKey, nativeCaseKey);
    assert.equal(nativeJourney.traceName, traceName);
    assert.equal(nativeJourney.model, 'TemporalFilterJourney');
    assert.equal(nativeJourney.valueKind, valueKind);
    assert.equal(nativeJourney.resourceType, resourceType);
    assert.equal(nativeJourney.fieldPath, fieldPath);
    assert.match(nativeJourney.coverageBasis, /observer request identity, response status, owner\/CAS and chronology/u);
    assert.match(nativeJourney.coverageBasis, /Builder read snapshots/u);
    assert.match(nativeJourney.coverageBasis, /mounted DOM receipt\/output\/row IDs/u);
    assert.equal(nativeJourney.exactActionCounts, true);
    assert.deepEqual(nativeJourney.requiredMutationControlNames, TEMPORAL_FILTER_MUTATION_CONTROL_NAMES);
    assert.deepEqual(nativeJourney.actions, expectedActions);
    assert.deepEqual(contract.models.TemporalFilterJourney.actions, Object.keys(requiredCounts));
    assert.deepEqual(contract.phase1.requiredActionsByTrace[traceName], Object.keys(requiredCounts),
      'The common required-action inventory must be unique; exact chronology belongs to nativeJourney.actions.');
    assert.equal(nativeJourney.actions.length, 16, 'The native contract must preserve every ordered action occurrence.');
    assert.deepEqual(contract.phase1.requiredActionCountsByTrace[traceName], requiredCounts);
    assert.deepEqual(contract.phase1.requiredEvents, Object.keys(requiredCounts));
  }
});

test('compiler and TLA retain read-only bootstrap capability requests before the first semantic action', { timeout: 30_000 }, () => {
  const capture = makeTemporalFilterJourneyCaptureFixture('date');
  const bootstrapExplorer = capture.bootstrapExplorerId;
  const firstEpoch = Math.min(...capture.requestEvents.map(request => request.startedAt)) - 10;
  const firstMonotonic = Math.min(...capture.requestEvents.map(request => request.atMonotonicMs)) - 10;
  const bootstrapRequests = [
    {
      browserRequestId: 'schema-fixture-bootstrap-capability-get',
      requestId: 'builder-bootstrap-capability-1',
      method: 'GET',
      path: `/api/v1/projects/${capture.project}/explorers/${bootstrapExplorer}/authoring/v2/capability`,
      request: null,
      response: {},
      startedAt: firstEpoch,
      atMonotonicMs: firstMonotonic,
      atEpochMs: firstEpoch,
    },
    {
      browserRequestId: 'schema-fixture-bootstrap-construction-capabilities',
      requestId: 'builder-bootstrap-capabilities-1',
      method: 'POST',
      path: `/api/v1/projects/${capture.project}/explorers/${bootstrapExplorer}/authoring/v2/construction-capabilities`,
      request: { outputId: 'bootstrap-output' },
      response: { capabilities: [] },
      startedAt: firstEpoch + 3,
      atMonotonicMs: firstMonotonic + 3,
      atEpochMs: firstEpoch + 3,
    },
  ].map((request, index) => ({
    index,
    ...request,
    status: 200,
    responseReceivedAt: request.startedAt + 1,
    completedAt: request.startedAt + 2,
    responseMountEpoch: 1,
    failureMountEpoch: null,
    isMutation: false,
    failure: null,
    readCancellationDiagnostic: false,
    responseCapture: 'captured',
    responseCaptureComplete: true,
    responseBodyCaptured: true,
    mountEpoch: 0,
  }));
  capture.requestEvents = [...bootstrapRequests, ...capture.requestEvents]
    .map((request, index) => ({ ...request, index }));

  const trace = adaptTemporalFilterJourney(capture);
  const input = validateTemporalFilterJourneyTrace(trace);
  assert.deepEqual(input.pureReadCaptureIds.slice(0, 2), bootstrapRequests.map(request => request.browserRequestId));
  assert.equal(input.observedRequests.find(request => request.captureId === bootstrapRequests[0].browserRequestId).startedAt, 0,
    'TLA relative time origin must include the request that began before the first semantic action.');
  const result = runTlc(trace, 'BootstrapCapabilityRequests');
  assert.equal(result.status, 0, result.output);
});

test('compiler rejects read-only POST capability paths outside the recorded project or Explorer', () => {
  const appendUnownedCapability = (trace, path, requestName) => {
    const capture = trace.temporalFilterLifecycle;
    const startedAt = Math.max(...capture.requestEvents.map(request => request.startedAt)) + 10;
    const atMonotonicMs = Math.max(...capture.requestEvents.map(request => request.atMonotonicMs)) + 10;
    const request = {
      index: capture.requestEvents.length,
      browserRequestId: `schema-fixture-browser-request-${requestName}`,
      requestId: `schema-fixture-raw-request-${requestName}`,
      method: 'POST',
      path,
      status: 200,
      request: {},
      response: {},
      startedAt,
      responseReceivedAt: startedAt + 1,
      completedAt: startedAt + 2,
      responseMountEpoch: 5,
      failureMountEpoch: null,
      isMutation: false,
      failure: null,
      readCancellationDiagnostic: false,
      responseCapture: 'captured',
      responseCaptureComplete: true,
      responseBodyCaptured: true,
      atMonotonicMs,
      atEpochMs: startedAt,
      mountEpoch: 5,
    };
    capture.requestEvents.push(request);
    trace.nativeRequests.push({
      index: request.index,
      browserRequestId: request.browserRequestId,
      requestId: request.requestId,
      method: request.method,
      path: request.path,
      startedAt: request.startedAt,
      isMutation: request.isMutation,
    });
  };
  const wrongProject = makeJourney('date');
  appendUnownedCapability(wrongProject,
    `/api/v1/projects/another-project/explorers/${wrongProject.temporalFilterLifecycle.bootstrapExplorerId}/authoring/v2/construction-capabilities`,
    'wrong-project-bootstrap-capabilities');
  assert.throws(() => compileTemporalFilterJourneyTrace(wrongProject), /no temporal-filter path allowlist entry/u);

  const wrongExplorer = makeJourney('date');
  const { project } = wrongExplorer.temporalFilterLifecycle;
  appendUnownedCapability(wrongExplorer,
    `/api/v1/projects/${project}/explorers/unowned-explorer/authoring/v2/construction-capabilities`,
    'wrong-explorer-bootstrap-capabilities');
  assert.throws(() => compileTemporalFilterJourneyTrace(wrongExplorer), /no temporal-filter path allowlist entry/u);
});

const runTlc = (trace, moduleSuffix, { mutateModel } = {}) => {
  assert(tlcJar, 'Set TLA2TOOLS_JAR to the verified TLC 1.7.4 jar for this focused check.');
  const compiled = compileTemporalFilterJourneyTrace(trace, { moduleName: `TemporalFilter${moduleSuffix}` });
  const directory = mkdtempSync(join(tmpdir(), 'loom-temporal-filter-tla-'));
  const metadataDirectory = join(directory, 'states');
  mkdirSync(metadataDirectory);
  try {
    const modelText = readFileSync(modelPath, 'utf8');
    writeFileSync(join(directory, 'TemporalFilterJourney.tla'),
      mutateModel ? mutateModel(modelText) : modelText);
    writeFileSync(join(directory, `${compiled.moduleName}.tla`), compiled.moduleText);
    writeFileSync(join(directory, `${compiled.moduleName}.cfg`), compiled.configText);
    const result = spawnSync('java', [
      '-XX:+UseParallelGC', '-cp', tlcJar, 'tlc2.TLC', '-workers', '1',
      '-metadir', metadataDirectory, '-config', `${compiled.moduleName}.cfg`,
      `${compiled.moduleName}.tla`,
    ], { cwd: directory, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
    return { ...result, output: `${result.stdout ?? ''}${result.stderr ?? ''}`, compiled };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

const rawRequestFor = (trace, event) => trace.temporalFilterLifecycle.requestEvents.find(request =>
  request.browserRequestId === event.request.captureId);

const appendObservedRequest = (capture, {
  name,
  method = 'POST',
  path = '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/reconcile',
  body,
  response,
  status = 200,
  startedAt,
  responseAt,
  mountEpoch = 5,
  failure = null,
}) => {
  const timeOrigin = capture.requestEvents[0].atEpochMs - capture.requestEvents[0].atMonotonicMs;
  const requestEvent = {
    browserRequestId: `schema-fixture-browser-request-${name}`,
    requestId: `schema-fixture-raw-request-${name}`,
    method,
    path,
    status,
    request: structuredClone(body),
    response: structuredClone(response),
    startedAt: timeOrigin + startedAt,
    responseReceivedAt: status === null ? null : timeOrigin + responseAt,
    completedAt: timeOrigin + (status === null ? startedAt + 2 : responseAt + 2),
    responseMountEpoch: status === null ? null : mountEpoch,
    failureMountEpoch: status === null ? mountEpoch : null,
    isMutation: false,
    failure: structuredClone(failure),
    readCancellationDiagnostic: status === null,
    responseCapture: status === null ? 'not-configured' : 'captured',
    responseCaptureComplete: status !== null,
    responseBodyCaptured: status !== null,
    atMonotonicMs: startedAt,
    atEpochMs: timeOrigin + startedAt,
    mountEpoch,
  };
  capture.requestEvents.push(requestEvent);
  capture.requestEvents.sort((left, right) => left.startedAt - right.startedAt);
  capture.requestEvents.forEach((request, index) => { request.index = index; });
  return requestEvent.browserRequestId;
};

const appendObservedDOM = (capture, { receiptId = null, outputId = 'temporal-output', at, mountEpoch = 5 }) => {
  const timeOrigin = capture.requestEvents[0].atEpochMs - capture.requestEvents[0].atMonotonicMs;
  capture.domObservations.push({
    capturedAtMonotonicMs: at,
    capturedAtEpochMs: timeOrigin + at,
    mountEpoch,
    proposalStatus: null,
    proposalId: null,
    receiptId,
    outputId,
    columnId: null,
    inputType: null,
    inputValue: null,
    operator: null,
    rowIds: [],
  });
  capture.finalDOMObservationIndex = capture.domObservations.length - 1;
};

const appendLateReconcile = (capture, requestId) => {
  capture.lateReconcileCaptureIds.push(requestId);
};

test('compiler binds synthetic adapter-produced DATE events, snapshots, DOM references, and request ledger', () => {
  const trace = makeJourney('date');
  const validated = validateTemporalFilterJourneyTrace(trace);
  const compiled = compileTemporalFilterJourneyTrace(trace);
  assert.equal(validated.eventCount, 16);
  assert.equal(validated.mutationCount, 6);
  assert.equal(validated.proposalCount, 6);
  assert.equal(validated.initialMountEpoch, 1);
  assert.equal(compiled.moduleName, 'TemporalFilterJourneyDateTrace');
  assert.match(compiled.moduleText, /EXTENDS TemporalFilterJourney/u);
  assert.match(compiled.moduleText, /TraceInitialMountEpoch == 1/u);
  assert.match(compiled.moduleText, /TraceEvents == <<\[action \|-> "ExplorerCreated"/u);
  assert.match(compiled.configText, /CONSTANT InitialMountEpoch <- TraceInitialMountEpoch/u);
  assert.match(compiled.configText, /CONSTANT ObservedRequests <- TraceObservedRequests/u);
  assert.match(compiled.configText, /CONSTANT ObservedDOM <- TraceObservedDOM/u);
  assert.match(compiled.configText, /CONSTANT ObservedBuilderReads <- TraceObservedBuilderReads/u);
  assert.match(compiled.configText, /INVARIANT InvObservedMutationsConsumed/u);
  assert.match(compiled.configText, /INVARIANT InvReconcileEvidenceLinked/u);
  assert.match(compiled.configText, /INVARIANT InvReconcilePossibleProducerRelations/u);
  assert.match(compiled.configText, /INVARIANT InvReconcileTransportSemantics/u);
  assert.match(compiled.configText, /INVARIANT InvPreviewTransportSemantics/u);
  assert.match(compiled.configText, /INVARIANT InvPreviewRecoveryFollowups/u);
  assert.match(compiled.configText, /INVARIANT InvReloadReconcileBindsFreshSnapshot/u);
  assert.match(compiled.configText, /PROPERTY EventuallyComplete/u);
  assert.match(compiled.responseTimeBoundary, /responseReceivedAt/u);
  assert.equal(compiled.eventCount, trace.actions.length);
});

test('compiler binds synthetic DATE_TIME typed instants and the adapter-captured native input', () => {
  const trace = makeJourney('date-time');
  const input = validateTemporalFilterJourneyTrace(trace);
  assert.equal(input.profile, 'DATE_TIME');
  assert.equal(input.caseValue.inputValue, trace.actions.find(event =>
    event.action === 'FilterProposed(create)').ui.inputValue);
  assert.equal(input.caseValue.initialRequestValue.dateTime, '2026-01-02T00:00:00.000Z');
  assert.equal(input.caseValue.initialResponseValue.dateTime, '2026-01-02T00:00:00Z');
  assert.deepEqual(input.caseValue.initialRowIds, []);
  assert.deepEqual(input.caseValue.editedRowIds, [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
  ]);
});

test('compiler rejects missing actions, unlinked mutations, unknown read-only POSTs, and raw-reference tampering', () => {
  const missingAction = makeJourney('date');
  missingAction.actions.pop();
  assert.throws(() => compileTemporalFilterJourneyTrace(missingAction), /Every semantic lifecycle action/u);

  const labelMismatch = makeJourney('date');
  labelMismatch.actions[0].label = 'SourceTableCreated';
  assert.throws(() => compileTemporalFilterJourneyTrace(labelMismatch), /common label must exactly match/u);

  const eventMismatch = makeJourney('date');
  eventMismatch.actions[0].event = 'SourceTableCreated';
  assert.throws(() => compileTemporalFilterJourneyTrace(eventMismatch), /event must equal its exact TLA transition label/u);

  const rawIndexTamper = makeJourney('date');
  rawIndexTamper.temporalFilterLifecycle.requestEvents[0].index += 1;
  assert.throws(() => compileTemporalFilterJourneyTrace(rawIndexTamper), /Common nativeRequests must preserve/u);

  const ownerMismatch = makeJourney('date');
  const ownedAction = ownerMismatch.actions.find(event => event.owner?.kind === 'proposal');
  ownedAction.owner.mountEpoch += 1;
  assert.throws(() => compileTemporalFilterJourneyTrace(ownerMismatch), /common proposal owner must resolve/u);

  const missingMutation = makeJourney('date');
  const tableAction = missingMutation.actions.find(event => event.action === 'SourceTableCreated');
  tableAction.request.isMutation = false;
  assert.throws(() => compileTemporalFilterJourneyTrace(missingMutation), /preserve the observer's mutation classification/u);

  const unknownFalsePost = makeJourney('date');
  unknownFalsePost.temporalFilterLifecycle.requestEvents.push({
    index: unknownFalsePost.temporalFilterLifecycle.requestEvents.length,
    browserRequestId: 'unclassified-false-post',
    requestId: 'raw-unclassified-false-post',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unclassified-read',
    status: 200,
    request: {},
    response: {},
    startedAt: 9,
    responseReceivedAt: 10,
    completedAt: 11,
    responseMountEpoch: 1,
    failureMountEpoch: null,
    isMutation: false,
    atMonotonicMs: 10,
    atEpochMs: 10,
    mountEpoch: 1,
  });
  unknownFalsePost.nativeRequests.push({
    index: unknownFalsePost.temporalFilterLifecycle.requestEvents.at(-1).index,
    browserRequestId: 'unclassified-false-post',
    requestId: 'raw-unclassified-false-post',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unclassified-read',
    startedAt: 9,
    isMutation: false,
  });
  assert.throws(() => compileTemporalFilterJourneyTrace(unknownFalsePost), /no temporal-filter path allowlist entry/u);

  const rawReadTamper = makeJourney('date');
  rawReadTamper.temporalFilterLifecycle.builderReads[5].draftDigest = 'altered-without-normalized-update';
  assert.throws(() => compileTemporalFilterJourneyTrace(rawReadTamper), /before snapshot must resolve from its actual raw Builder read index/u);

  const rawDOMTamper = makeJourney('date');
  rawDOMTamper.temporalFilterLifecycle.domObservations[3].rowIds = ['dev-patient-001'];
  assert.throws(() => compileTemporalFilterJourneyTrace(rawDOMTamper), /DOM observation must resolve from its actual raw DOM index/u);

  const builderReadBeforeCommandResponse = makeTemporalFilterJourneyCaptureFixture('date');
  const sourceCommand = builderReadBeforeCommandResponse.requestEvents.find(request =>
    request.browserRequestId === 'schema-fixture-browser-request-source-table');
  sourceCommand.responseReceivedAt = builderReadBeforeCommandResponse.builderReads[2].requestStartedAtEpochMs + 1;
  sourceCommand.completedAt = sourceCommand.responseReceivedAt + 1;
  const beforeReadTrace = adaptTemporalFilterJourney(builderReadBeforeCommandResponse);
  assert.throws(() => compileTemporalFilterJourneyTrace(beforeReadTrace), /post-settlement Builder GET must start after/u);

  const readyDOMBeforeProposalResponse = makeTemporalFilterJourneyCaptureFixture('date');
  const firstProposal = readyDOMBeforeProposalResponse.requestEvents.find(request =>
    request.browserRequestId === 'schema-fixture-browser-request-first-create');
  const firstProposalDOM = readyDOMBeforeProposalResponse.domObservations[
    readyDOMBeforeProposalResponse.steps[3].domObservationIndex];
  firstProposal.responseReceivedAt = firstProposalDOM.capturedAtEpochMs + 1;
  firstProposal.completedAt = firstProposal.responseReceivedAt + 1;
  const beforeReadyTrace = adaptTemporalFilterJourney(readyDOMBeforeProposalResponse);
  assert.throws(() => compileTemporalFilterJourneyTrace(beforeReadyTrace), /DOM observation must follow the bound request response arrival/u);
});

test('compiler binds reload DOM and Builder state to each actual reconcile request and response', () => {
  const trace = makeJourney('date');
  const validated = validateTemporalFilterJourneyTrace(trace);
  const reloads = validated.modelEvents.filter(event => event.action === 'Reload');
  const reconcileRequests = validated.observedRequests.filter(request => request.path.endsWith('/reconcile'));
  assert.equal(reloads.length, 4);
  assert.deepEqual(reconcileRequests.map(request => request.captureId).sort(),
    trace.temporalFilterLifecycle.requestEvents
      .filter(request => request.method === 'POST' && request.path.endsWith('/reconcile'))
      .map(request => request.browserRequestId).sort(),
    'The model ledger must retain every captured reconcile request.');
  for (const event of reloads) {
    assert(event.reconcileCaptureIds.length > 0, 'Reload must bind its actual reconcile browser request ID or IDs.');
    const request = reconcileRequests.find(candidate => event.reconcileCaptureIds.includes(candidate.captureId) &&
      candidate.requestSnapshotToken === event.after.snapshotToken &&
      candidate.requestDraftVersion === event.after.draftVersion &&
      candidate.requestDraftDigest === event.after.draftDigest &&
      candidate.requestOutputId === event.after.outputId &&
      candidate.responseSnapshotToken === candidate.requestSnapshotToken &&
      candidate.receiptId === event.ui.receiptId);
    assert(request, 'Reload must bind a response for its actual saved Builder CAS and DOM receipt.');
    assert.equal(request.requestSnapshotToken, event.after.snapshotToken);
    assert.equal(request.requestDraftVersion, event.after.draftVersion);
    assert.equal(request.requestDraftDigest, event.after.draftDigest);
    assert.equal(request.requestOutputId, event.after.outputId);
    assert.equal(request.responseSnapshotToken, request.requestSnapshotToken);
    assert(request.responseOutputIds.includes(event.after.outputId));
    assert.equal(request.receiptId, event.ui.receiptId);
    assert.equal(validated.pureReadCaptureIds.includes(request.captureId), false,
      'A reconcile used by Reload belongs to the model request ledger, not the pure-read sidecar.');
  }

  const staleCAS = makeJourney('date');
  const staleCASRaw = staleCAS.temporalFilterLifecycle.requestEvents.find(request =>
    request.browserRequestId === staleCAS.temporalFilterLifecycle.steps[5].reconcileCaptureIds[0]);
  staleCASRaw.request.draftDigest = 'stale-digest';
  assert.throws(() => compileTemporalFilterJourneyTrace(staleCAS), /fresh saved Builder draft digest/u);

  const staleReceipt = makeJourney('date');
  const staleReceiptRaw = staleReceipt.temporalFilterLifecycle.requestEvents.find(request =>
    request.browserRequestId === staleReceipt.temporalFilterLifecycle.steps[5].reconcileCaptureIds[0]);
  staleReceiptRaw.response.receiptId = 'detached-receipt';
  assert.throws(() => compileTemporalFilterJourneyTrace(staleReceipt), /DOM receipt must match a successful actual reconcile response/u);

  const missingReloadLink = makeJourney('date');
  missingReloadLink.temporalFilterLifecycle.steps[5].reconcileCaptureIds = [];
  assert.throws(() => compileTemporalFilterJourneyTrace(missingReloadLink), /reconcile request links must remain equal to their raw step references/u);

  const responseAfterReloadRead = makeJourney('date');
  const rawStep = responseAfterReloadRead.temporalFilterLifecycle.steps[5];
  const raw = responseAfterReloadRead.temporalFilterLifecycle.requestEvents.find(request =>
    request.browserRequestId === rawStep.reconcileCaptureIds[0]);
  raw.responseReceivedAt = responseAfterReloadRead.temporalFilterLifecycle.builderReads[
    rawStep.afterReadIndex].requestStartedAtEpochMs + 1;
  raw.completedAt = raw.responseReceivedAt + 1;
  assert.throws(() => compileTemporalFilterJourneyTrace(responseAfterReloadRead), /Reload post-settlement Builder GET must start/u);
});

test('same-owner reconcile responses with equal receipts remain ambiguous possible producers', { timeout: 30_000 }, () => {
  const capture = makeTemporalFilterJourneyCaptureFixture('date');
  const reconcile = {
    snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId: 'temporal-output',
  };
  const firstId = appendObservedRequest(capture, {
    name: 'duplicate-reconcile-first', body: reconcile,
    response: { snapshotToken: 'snapshot-6', receiptId: 'same-content-receipt', outputs: [{ outputId: 'temporal-output' }] },
    startedAt: 1480, responseAt: 1484,
  });
  const secondId = appendObservedRequest(capture, {
    name: 'duplicate-reconcile-second', body: reconcile,
    response: { snapshotToken: 'snapshot-6', receiptId: 'same-content-receipt', outputs: [{ outputId: 'temporal-output' }] },
    startedAt: 1481, responseAt: 1486,
  });
  const previewId = appendObservedRequest(capture, {
    name: 'duplicate-reconcile-preview',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: 'same-content-receipt', outputId: 'temporal-output' },
    response: { receiptId: 'same-content-receipt', outputId: 'temporal-output' },
    startedAt: 1490, responseAt: 1492,
  });
  appendLateReconcile(capture, firstId);
  appendLateReconcile(capture, secondId);
  appendObservedDOM(capture, { receiptId: 'same-content-receipt', at: 1500 });

  const trace = adaptTemporalFilterJourney(capture);
  const input = validateTemporalFilterJourneyTrace(trace);
  const observedPreview = input.observedRequests.find(request => request.captureId === previewId);
  assert.deepEqual(observedPreview.possibleReconcileProducerCaptureIds.sort(), [firstId, secondId].sort());
  for (const requestId of [firstId, secondId]) {
    const request = input.observedRequests.find(candidate => candidate.captureId === requestId);
    assert.equal(request.reconcileDisposition, 'compatible-unattributed');
    assert.deepEqual(request.reconcileCompatiblePreviewCaptureIds, [previewId]);
  }

  const result = runTlc(trace, 'DuplicateReceiptAmbiguity');
  assert.equal(result.status, 0, result.output);
  assert.match(result.output, /Model checking completed\. No error has been found\./u, result.output);
});

test('reconcile transport ledger proves owner invalidation, stale retry refresh, and actual cancellation', { timeout: 30_000 }, () => {
  const invalidatedCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const oldReceipt = 'late-old-owner-receipt';
  const invalidatedId = appendObservedRequest(invalidatedCapture, {
    name: 'late-old-owner-reconcile',
    body: { snapshotToken: 'snapshot-2', draftVersion: 2, draftDigest: 'draft-2', outputId: 'temporal-output' },
    response: { snapshotToken: 'snapshot-2', receiptId: oldReceipt, outputs: [{ outputId: 'temporal-output' }] },
    startedAt: 1075, responseAt: 1100, mountEpoch: 1,
  });
  const oldPreviewId = appendObservedRequest(invalidatedCapture, {
    name: 'late-old-owner-preview',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: oldReceipt, outputId: 'temporal-output' },
    response: { receiptId: oldReceipt, outputId: 'temporal-output' },
    startedAt: 1110, responseAt: 1112, mountEpoch: 1,
  });
  appendLateReconcile(invalidatedCapture, invalidatedId);
  appendObservedDOM(invalidatedCapture, { at: 1150, mountEpoch: 1 });

  const invalidatedTrace = adaptTemporalFilterJourney(invalidatedCapture);
  const invalidated = validateTemporalFilterJourneyTrace(invalidatedTrace);
  const oldResponse = invalidated.observedRequests.find(request => request.captureId === invalidatedId);
  assert(oldResponse.reconcileOwnerChangeCaptureIds.includes(
    'schema-fixture-browser-request-choice-apply'));
  assert.equal(oldResponse.reconcileDisposition, 'owner-invalidated-before-response');
  assert.deepEqual(oldResponse.reconcileCompatiblePreviewCaptureIds, []);
  const invalidatedPreview = invalidated.observedRequests.find(request => request.captureId === oldPreviewId);
  assert.deepEqual(invalidatedPreview.possibleReconcileProducerCaptureIds, []);
  assert.equal(invalidatedPreview.previewDisposition, 'owner-superseded');
  const invalidatedResult = runTlc(invalidatedTrace, 'OwnerInvalidatedReconcile');
  assert.equal(invalidatedResult.status, 0, invalidatedResult.output);
  const missingOwnerGuard = runTlc(invalidatedTrace, 'OwnerInvalidationGuardRemoved', {
    mutateModel: source => {
      const guard = '    /\\ ~ReconcileOwnerChangedBefore(request, preview.responseReceivedAt)}';
      assert.equal(source.split(guard).length - 1, 1,
        'the owner-invalidation producer guard must remain a unique mutation-control target');
      return source.replace(guard, '    /\\ TRUE}');
    },
  });
  assert.notEqual(missingOwnerGuard.status, 0, missingOwnerGuard.output);
  assert.match(missingOwnerGuard.output,
    /InvReconcilePossibleProducerRelations is equal to FALSE/u, missingOwnerGuard.output);

  const supersededCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const supersededReceipt = 'response-first-old-receipt';
  const responseFirstId = appendObservedRequest(supersededCapture, {
    name: 'response-first-reconcile',
    body: { snapshotToken: 'snapshot-3', draftVersion: 3, draftDigest: 'draft-3', outputId: 'temporal-output' },
    response: { snapshotToken: 'snapshot-3', receiptId: supersededReceipt,
      outputs: [{ outputId: 'temporal-output' }] },
    startedAt: 1171, responseAt: 1174, mountEpoch: 2,
  });
  const supersededPreviewId = appendObservedRequest(supersededCapture, {
    name: 'preview-after-owner-change',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: supersededReceipt, outputId: 'temporal-output' },
    response: { receiptId: supersededReceipt, outputId: 'temporal-output' },
    startedAt: 1240, responseAt: 1242, mountEpoch: 2,
  });
  appendLateReconcile(supersededCapture, responseFirstId);
  supersededCapture.finalDOMObservationIndex = supersededCapture.domObservations.length - 1;
  const supersededTrace = adaptTemporalFilterJourney(supersededCapture);
  const superseded = validateTemporalFilterJourneyTrace(supersededTrace);
  const responseFirst = superseded.observedRequests.find(request => request.captureId === responseFirstId);
  assert(responseFirst.reconcileOwnerChangeCaptureIds.includes(
    'schema-fixture-browser-request-create-apply-command'));
  assert.equal(responseFirst.reconcileDisposition, 'owner-superseded-unconsumed');
  const supersededPreview = superseded.observedRequests.find(request => request.captureId === supersededPreviewId);
  assert.deepEqual(supersededPreview.possibleReconcileProducerCaptureIds, []);
  assert.equal(supersededPreview.previewDisposition, 'owner-superseded');
  const responseFirstResult = runTlc(supersededTrace, 'ResponseFirstOwnerSupersession');
  assert.equal(responseFirstResult.status, 0, responseFirstResult.output);
  const missingLateOwnerGuard = runTlc(supersededTrace, 'LateOwnerGuardRemoved', {
    mutateModel: source => {
      const guard = '    /\\ ~ReconcileOwnerChangedBefore(request, preview.responseReceivedAt)}';
      assert.equal(source.split(guard).length - 1, 1,
        'the response-to-consumer owner guard must remain a unique mutation-control target');
      return source.replace(guard, '    /\\ TRUE}');
    },
  });
  assert.notEqual(missingLateOwnerGuard.status, 0, missingLateOwnerGuard.output);
  assert.match(missingLateOwnerGuard.output,
    /InvReconcilePossibleProducerRelations is equal to FALSE/u, missingLateOwnerGuard.output);

  const retryCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const staleId = appendObservedRequest(retryCapture, {
    name: 'stale-reconcile',
    body: { snapshotToken: 'snapshot-5', draftVersion: 5, draftDigest: 'draft-5', outputId: 'temporal-output' },
    response: { code: 'STALE_SNAPSHOT' },
    status: 409,
    startedAt: 1418, responseAt: 1424, mountEpoch: 4,
  });
  appendObservedRequest(retryCapture, {
    name: 'stale-reconcile-builder-refresh',
    method: 'GET',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/builder',
    body: null,
    response: { catalog: { snapshotToken: 'snapshot-6', generation: 'basic-temporal-generation' },
      draftVersion: 6, draftDigest: 'draft-6', workspace: { documents: [{ output: { id: 'temporal-output' } }] } },
    status: 200,
    startedAt: 1425, responseAt: 1427,
    mountEpoch: 4,
  });
  const retryId = appendObservedRequest(retryCapture, {
    name: 'fresh-reconcile-retry',
    body: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId: 'temporal-output' },
    response: { snapshotToken: 'snapshot-6', receiptId: 'fresh-retry-receipt', outputs: [{ outputId: 'temporal-output' }] },
    startedAt: 1435, responseAt: 1440, mountEpoch: 4,
  });
  appendObservedRequest(retryCapture, {
    name: 'fresh-retry-preview',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: 'fresh-retry-receipt', outputId: 'temporal-output' },
    response: { receiptId: 'fresh-retry-receipt', outputId: 'temporal-output' },
    startedAt: 1441, responseAt: 1444, mountEpoch: 4,
  });
  const canceledId = appendObservedRequest(retryCapture, {
    name: 'canceled-reconcile',
    body: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId: 'temporal-output' },
    response: null,
    status: null,
    startedAt: 1465,
    mountEpoch: 5,
    failure: { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true },
  });
  appendLateReconcile(retryCapture, staleId);
  appendLateReconcile(retryCapture, retryId);
  appendLateReconcile(retryCapture, canceledId);
  appendObservedDOM(retryCapture, { receiptId: 'fresh-retry-receipt', at: 1470, mountEpoch: 5 });

  const retryTrace = adaptTemporalFilterJourney(retryCapture);
  const retryInput = validateTemporalFilterJourneyTrace(retryTrace);
  const stale = retryInput.observedRequests.find(request => request.captureId === staleId);
  assert.equal(stale.reconcileDisposition, 'stale-retry');
  assert.deepEqual(stale.reconcileRetryFollowupCaptureIds, [retryId]);
  const canceled = retryInput.observedRequests.find(request => request.captureId === canceledId);
  assert.equal(canceled.status, 0);
  assert.equal(canceled.reconcileDisposition, 'cancelled');
  const retryResult = runTlc(retryTrace, 'StaleRetryAndCancel');
  assert.equal(retryResult.status, 0, retryResult.output);

  const unknownFailure = makeTemporalFilterJourneyCaptureFixture('date');
  const unknownId = appendObservedRequest(unknownFailure, {
    name: 'unknown-failed-reconcile',
    body: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId: 'temporal-output' },
    response: { code: 'INTERNAL_ERROR' },
    status: 500,
    startedAt: 1480, responseAt: 1482,
    mountEpoch: 5,
  });
  appendLateReconcile(unknownFailure, unknownId);
  appendObservedDOM(unknownFailure, { at: 1490, mountEpoch: 5 });
  assert.throws(() => compileTemporalFilterJourneyTrace(adaptTemporalFilterJourney(unknownFailure)),
    /failed without a recognized stale or retryable response/u);
});

test('preview ledger requires echoed success, captures retry and stale refresh, and excludes old-owner consumers', { timeout: 30_000 }, () => {
  const retryCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const receiptId = 'reconcile-receipt-remove';
  const outputId = 'temporal-output';
  const firstAttemptId = appendObservedRequest(retryCapture, {
    name: 'preview-transient-first-attempt',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId, outputId },
    response: { code: 'TEMPORARY_PREVIEW_FAILURE', receiptId, outputId },
    status: 500,
    startedAt: 1480, responseAt: 1482,
    mountEpoch: 5,
  });
  const recoveredPreviewId = appendObservedRequest(retryCapture, {
    name: 'preview-transient-retry',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId, outputId },
    response: { receiptId, outputId },
    status: 200,
    startedAt: 1490, responseAt: 1492,
    mountEpoch: 5,
  });
  const canceledPreviewId = appendObservedRequest(retryCapture, {
    name: 'preview-canceled',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: 'canceled-old-receipt', outputId },
    response: null,
    status: null,
    startedAt: 1510,
    mountEpoch: 5,
    failure: { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true },
  });
  appendObservedDOM(retryCapture, { receiptId, outputId, at: 1520, mountEpoch: 5 });

  const retryTrace = adaptTemporalFilterJourney(retryCapture);
  const retryInput = validateTemporalFilterJourneyTrace(retryTrace);
  const firstAttempt = retryInput.observedRequests.find(request => request.captureId === firstAttemptId);
  assert.equal(firstAttempt.status, 500);
  assert.equal(firstAttempt.responseRetryable, true);
  assert.equal(firstAttempt.previewDisposition, 'retry');
  assert.deepEqual(firstAttempt.possibleReconcileProducerCaptureIds, []);
  assert.deepEqual(firstAttempt.previewRetryFollowupCaptureIds, [recoveredPreviewId]);
  const recovered = retryInput.observedRequests.find(request => request.captureId === recoveredPreviewId);
  assert.equal(recovered.previewDisposition, 'compatible-unattributed');
  assert(recovered.possibleReconcileProducerCaptureIds.includes(
    'schema-fixture-browser-request-reconcile-remove-reload'));
  const canceledPreview = retryInput.observedRequests.find(request => request.captureId === canceledPreviewId);
  assert.equal(canceledPreview.previewDisposition, 'cancelled');
  const retryResult = runTlc(retryTrace, 'PreviewRetryAndCancel');
  assert.equal(retryResult.status, 0, retryResult.output);
  const failedPreviewCountedAsProducer = runTlc(retryTrace, 'FailedPreviewProducerGuardRemoved', {
    mutateModel: source => {
      const guard = '    /\\ preview.status \\in 200..299';
      assert.equal(source.split(guard).length - 1, 1,
        'preview producer success status must remain a unique mutation-control target');
      return source.replace(guard, '    /\\ TRUE');
    },
  });
  assert.notEqual(failedPreviewCountedAsProducer.status, 0, failedPreviewCountedAsProducer.output);
  assert.match(failedPreviewCountedAsProducer.output,
    /InvReconcilePossibleProducerRelations is equal to FALSE/u, failedPreviewCountedAsProducer.output);

  const staleCapture = makeTemporalFilterJourneyCaptureFixture('date');
  appendObservedRequest(staleCapture, {
    name: 'preview-stale-catalog',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: 'stale-preview-receipt', outputId },
    response: { code: 'STALE_SNAPSHOT' },
    status: 409,
    startedAt: 1470, responseAt: 1472,
    mountEpoch: 5,
  });
  const builderRefreshId = appendObservedRequest(staleCapture, {
    name: 'preview-stale-builder-refresh',
    method: 'GET',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/builder',
    body: null,
    response: { catalog: { snapshotToken: 'snapshot-6', generation: 'basic-temporal-generation' },
      draftVersion: 6, draftDigest: 'draft-6', workspace: { documents: [{ output: { id: outputId } }] } },
    status: 200,
    startedAt: 1474, responseAt: 1476,
    mountEpoch: 5,
  });
  const staleRecoveryId = appendObservedRequest(staleCapture, {
    name: 'preview-stale-reconcile-refresh',
    body: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId },
    response: { snapshotToken: 'snapshot-6', receiptId: 'refreshed-preview-receipt', outputs: [{ outputId }] },
    status: 200,
    startedAt: 1480, responseAt: 1482,
    mountEpoch: 5,
  });
  const staleRecoveryPreviewId = appendObservedRequest(staleCapture, {
    name: 'preview-stale-recovered',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId: 'refreshed-preview-receipt', outputId },
    response: { receiptId: 'refreshed-preview-receipt', outputId },
    status: 200,
    startedAt: 1490, responseAt: 1492,
    mountEpoch: 5,
  });
  appendLateReconcile(staleCapture, staleRecoveryId);
  appendObservedDOM(staleCapture, { receiptId: 'refreshed-preview-receipt', outputId, at: 1500, mountEpoch: 5 });
  const staleTrace = adaptTemporalFilterJourney(staleCapture);
  const staleInput = validateTemporalFilterJourneyTrace(staleTrace);
  const stalePreview = staleInput.observedRequests.find(request =>
    request.captureId === 'schema-fixture-browser-request-preview-stale-catalog');
  assert.equal(stalePreview.previewDisposition, 'stale-refresh');
  assert.deepEqual(stalePreview.previewRecoveryReconcileCaptureIds, [staleRecoveryId]);
  assert.deepEqual(stalePreview.previewRecoveryPreviewCaptureIds, [staleRecoveryPreviewId]);
  assert.deepEqual(stalePreview.previewRecoveryBuilderReadCaptureIds, [builderRefreshId]);
  const staleResult = runTlc(staleTrace, 'PreviewStaleRefresh');
  assert.equal(staleResult.status, 0, staleResult.output);

  const wrongEcho = makeTemporalFilterJourneyCaptureFixture('date');
  appendObservedRequest(wrongEcho, {
    name: 'preview-success-wrong-receipt',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId, outputId },
    response: { receiptId: 'wrong-receipt', outputId },
    status: 200,
    startedAt: 1480, responseAt: 1482,
    mountEpoch: 5,
  });
  assert.throws(() => compileTemporalFilterJourneyTrace(adaptTemporalFilterJourney(wrongEcho)),
    /successful response must echo its exact receipt ID/u);

  const retryLimitMismatch = makeTemporalFilterJourneyCaptureFixture('date');
  appendObservedRequest(retryLimitMismatch, {
    name: 'preview-retry-limit-first',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId, outputId, limit: 25 },
    response: { code: 'TEMPORARY_PREVIEW_FAILURE' },
    status: 500,
    startedAt: 1480, responseAt: 1482,
    mountEpoch: 5,
  });
  appendObservedRequest(retryLimitMismatch, {
    name: 'preview-retry-limit-changed',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/preview',
    body: { receiptId, outputId, limit: 100 },
    response: { receiptId, outputId },
    status: 200,
    startedAt: 1490, responseAt: 1492,
    mountEpoch: 5,
  });
  assert.throws(() => compileTemporalFilterJourneyTrace(adaptTemporalFilterJourney(retryLimitMismatch)),
    /failed without a recognized observed cancellation, stale recovery, or retry follow-up/u);
});

test('synthetic DATE and DATE_TIME adapter captures pass their generated TLA trace instances', { timeout: 30_000 }, () => {
  for (const [caseName, suffix] of [['date', 'DatePositive'], ['date-time', 'DateTimePositive']]) {
    const result = runTlc(makeJourney(caseName), suffix);
    assert.equal(result.status, 0, result.output);
    assert.match(result.output, /Model checking completed\. No error has been found\./u, result.output);
    const stateCount = /([0-9,]+) states generated, ([0-9,]+) distinct states found/u.exec(result.output);
    assert(stateCount, result.output);
    console.log(`synthetic ${caseName} trace: ${stateCount[1]} states, ${stateCount[2]} distinct; all semantic actions reached completion.`);
  }
});

test('TLA rejects stale proposal ownership, stale draft CAS, and reordered adapter events', { timeout: 30_000 }, () => {
  const wrongOwner = makeJourney('date');
  const ownerApply = wrongOwner.actions.find(event => event.action === 'FilterApplied(create)');
  ownerApply.request.body.commands[0].proposalId = 'stale-proposal';
  rawRequestFor(wrongOwner, ownerApply).request.commands[0].proposalId = 'stale-proposal';
  const ownerResult = runTlc(wrongOwner, 'WrongOwner');
  assert.notEqual(ownerResult.status, 0, ownerResult.output);
  assert.match(ownerResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u, ownerResult.output);
  assert.equal([...ownerResult.output.matchAll(/cursor \|-> (\d+)/gu)].at(-1)?.[1], '7', ownerResult.output);

  const staleCAS = makeJourney('date');
  const casApply = staleCAS.actions.find(event => event.action === 'FilterApplied(create)');
  casApply.request.body.expectedDraftVersion += 1;
  rawRequestFor(staleCAS, casApply).request.expectedDraftVersion += 1;
  const casResult = runTlc(staleCAS, 'WrongCas');
  assert.notEqual(casResult.status, 0, casResult.output);
  assert.match(casResult.output, /Temporal property EventuallyComplete violated|Deadlock reached/u, casResult.output);
  assert.equal([...casResult.output.matchAll(/cursor \|-> (\d+)/gu)].at(-1)?.[1], '7', casResult.output);

  const wrongOrder = makeJourney('date');
  [wrongOrder.actions[3], wrongOrder.actions[4]] = [wrongOrder.actions[4], wrongOrder.actions[3]];
  [wrongOrder.temporalFilterLifecycle.steps[3], wrongOrder.temporalFilterLifecycle.steps[4]] =
    [wrongOrder.temporalFilterLifecycle.steps[4], wrongOrder.temporalFilterLifecycle.steps[3]];
  const orderResult = runTlc(wrongOrder, 'WrongOrder');
  assert.notEqual(orderResult.status, 0, orderResult.output);
  assert.match(orderResult.output, /InvSemanticActionOrder is equal to FALSE/u, orderResult.output);
});

test('Temporal mutation factory returns fixed compiler-valid controls rejected by TLC', { timeout: 60_000 }, () => {
  assert.deepEqual(TEMPORAL_FILTER_MUTATION_CONTROL_NAMES, [
    'proposal-owner-drift',
    'proposal-cas-drift',
    'semantic-action-order-drift',
  ]);
  for (const caseName of ['date', 'date-time']) {
    const controls = createTemporalFilterMutationControls(makeJourney(caseName));
    assert.deepEqual(controls.map(control => control.name), TEMPORAL_FILTER_MUTATION_CONTROL_NAMES);
    for (const control of controls) {
      assert.equal(control.expectedFailure.stage, 'TLC');
      validateTemporalFilterJourneyTrace(control.trace);
      const result = runTlc(control.trace, `${caseName.replace('-', '')}${control.name.replaceAll('-', '')}`);
      assert.notEqual(result.status, 0, `${caseName} ${control.name} should fail TLC.`);
      const expectedOutput = control.expectedFailure.invariant === 'Deadlock'
        ? /Error: Deadlock reached\./u
        : new RegExp(`${control.expectedFailure.invariant} is equal to FALSE`, 'u');
      assert.match(result.output, expectedOutput,
        `${caseName} ${control.name} failed at an unexpected stage:\n${result.output}`);
    }
  }
});
