import assert from 'node:assert/strict';
import test from 'node:test';
import { createReport, finishReport, recordCheck } from '../report.mjs';
import { nativeAbortSignalObservationForRequest } from '../native-abort-probe.mjs';

const origin = 'http://127.0.0.1:30008';
const project = 'loom_dev_verify_cross-format';
const explorer = 'verify-cross-format-cohort-recode';
const requestId = 'schema-fields-26300000-0000-4000-8000-000000000000';
const browserRequestId = 'playwright-request-263';
const diagnosticRequestId = 'cda-request-17';
const controllerId = 'abort-controller-24';
const requestStartedAt = 1200;
const nativeRequestObservedAt = 1205;
const applyStartedAt = 1100;
const applyFinishedAt = 1300;
const closeAt = 1500;
const ownerDetachedAt = 1510;
const controllerAbortedAt = 1520;
const fetchSettledAt = 1520;
const requestFailedAt = 1600;
const path = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/schema-fields`;

const makeCloseRetirementReport = (format = 'basic') => {
  const ownerDomAtFetch = {
    status: 'unique',
    selector: '#feature-catalog-search',
    matchCount: 1,
    capturedAt: requestStartedAt,
    anchorId: 'dom-node-9',
    connectedAtFetch: true,
    ruleOwner: 'feature-catalog-generated-fields',
    retirementAction: 'close-operation-editor',
  };
  const ownerDomAtAbort = {
    anchorId: 'dom-node-9',
    connectedAtAbort: false,
    detachedAtAbort: true,
    detachedObservedAt: ownerDetachedAt,
    observedAtAbort: controllerAbortedAt,
  };
  const closeInteraction = {
    type: 'click',
    at: closeAt,
    isTrusted: true,
    target: { tag: 'BUTTON', testId: 'construction-close-operation-editor', ariaLabel: 'Close operation editor' },
    closestButton: {
      id: 'dom-node-12',
      accessibleLabel: 'Close operation editor',
      testId: 'construction-close-operation-editor',
    },
  };
  const abortEvent = {
    kind: 'abort-controller-call',
    controllerId,
    createdAt: 1000,
    abortedAt: controllerAbortedAt,
    abortCount: 1,
    signalWasAlreadyAborted: false,
    requests: [{
      requestId,
      origin,
      path,
      method: 'POST',
      endpoint: 'schema-fields',
      explorer,
      requestIdSource: 'request-header',
      startedAt: requestStartedAt,
      fetchStateAtAbort: 'pending',
      ownerDomAtFetch,
      ownerDomAtAbort,
    }],
    trustedInteractions: [closeInteraction],
    lastTrustedInteraction: closeInteraction,
  };
  const settlementEvent = {
    kind: 'abort-controller-fetch-settlement',
    controllerId,
    observedAt: fetchSettledAt,
    requests: [{
      requestId,
      origin,
      path,
      method: 'POST',
      fetchStateAfterAbort: 'rejected',
      settledAt: fetchSettledAt,
    }],
  };
  const probeEvents = [abortEvent, settlementEvent];
  const nativeEntry = {
    requestId,
    browserRequestId,
    method: 'POST',
    origin,
    path,
    status: null,
    failure: 'net::ERR_ABORTED',
    terminalEvent: 'requestfailed',
    state: 'failed',
    complete: true,
    pageId: 'playwright-page-1',
    frameId: 'playwright-frame-1',
    frameIsMainFrame: true,
    frameIdentityStatus: 'exact',
    requestTimeline: {
      requestStartedMs: requestStartedAt,
      failedAtMs: requestFailedAt,
      durationMs: requestFailedAt - requestStartedAt,
      action: { id: 'action-apply', label: 'Apply columns' },
      mainFrameNavigations: [],
    },
    nativeEventChronology: [
      { event: 'request', browserRequestId, observedAt: nativeRequestObservedAt, objectMatch: true },
      { event: 'requestfailed', browserRequestId, observedAt: requestFailedAt, objectMatch: true, failure: 'net::ERR_ABORTED' },
    ],
  };
  const observation = nativeAbortSignalObservationForRequest({
    ...nativeEntry,
    requestCorrelationId: requestId,
    requestIdentityMatchCount: 1,
  }, probeEvents);
  assert.equal(observation.exactRequestSignalCorrelation, true, 'production signal correlator must bind the native request and Close retirement');
  assert.equal(observation.sameDocumentOwnerRetirement, true);
  assert.equal(observation.nativeTerminalObserved, true);

  const report = createReport({
    scenario: 'builder-authoring',
    caseName: 'cohort-recode',
    target: { uiUrl: origin, project, explorer },
    requiredChecks: ['native Catalog request reached a terminal Close retirement'],
  });
  const rawNetworkRow = {
    kind: 'network',
    method: 'POST',
    url: `${origin}${path}`,
    resourceType: 'fetch',
    errorText: 'net::ERR_ABORTED',
    requestDetails: { requestId },
    status: null,
    requestTimeline: nativeEntry.requestTimeline,
    ...(format === 'basic'
      ? { playwrightRequestId: browserRequestId }
      : { playwrightRequestId: diagnosticRequestId, browserRequestId }),
  };
  report.network.push(rawNetworkRow);
  report.actions.push(
    { id: 'action-apply', label: 'Apply columns', status: 'passed', startedAtEpochMs: applyStartedAt, finishedAtEpochMs: applyFinishedAt },
    { id: 'action-close', label: 'close operation editor', status: 'passed', startedAtEpochMs: 1400, finishedAtEpochMs: 1650 },
  );
  report.nativeRequestCaptureScope = {
    observedPathPrefix: `/api/v1/projects/${encodeURIComponent(project)}/explorers`,
    selectedExplorer: explorer,
  };
  report.nativeRequestTerminalLedger = { complete: true, requests: [nativeEntry] };
  report.nativeAbortProbeEvents = probeEvents;
  report.nativeAbortSignalObservations = [{
    requestId,
    origin,
    path,
    method: 'POST',
    requestIdentityMatchCount: 1,
    observation,
  }];
  recordCheck(report, 'correctness', 'native Catalog request reached a terminal Close retirement', true, {
    requestId,
    browserRequestId,
  });
  return { report, nativeEntry, rawNetworkRow };
};

const assertRetirementRejected = (report, rawNetworkRows, reason) => {
  finishReport(report);
  assert.equal(report.status, 'failed', reason);
  assert.equal(report.expectedOwnerRetirements?.length ?? 0, 0, reason);
  assert.deepEqual(report.network, rawNetworkRows, `${reason}: raw request evidence remains unchanged`);
  assert(report.errors.some(error => error.errorText === 'net::ERR_ABORTED'), reason);
};

test('finishReport classifies exact Basic and CDA Close retirements without rewriting raw network rows', () => {
  for (const format of ['basic', 'cda']) {
    const { report, rawNetworkRow } = makeCloseRetirementReport(format);
    const originalNetwork = structuredClone(report.network);
    finishReport(report);
    assert.equal(report.status, 'passed', `${format} projection: ${JSON.stringify(report.errors)}`);
    assert.deepEqual(report.missingRequiredChecks, []);
    assert.equal(report.expectedOwnerRetirements.length, 1);
    assert.equal(report.expectedOwnerRetirements[0].kind, 'same-document-owner-retirement');
    assert.equal(report.expectedOwnerRetirements[0].browserRequestId, browserRequestId,
      `${format} projection resolves to the exact native browser request object`);
    assert.deepEqual(report.network, originalNetwork, `${format} raw network error row is preserved`);
    assert.deepEqual(report.network[0], rawNetworkRow);
    assert.equal(Object.hasOwn(report.network[0], 'canceled'), false);
  }
});

test('finishReport rejects duplicate or mismatched native browser request identity', () => {
  const duplicateNativeTuple = makeCloseRetirementReport('cda');
  duplicateNativeTuple.report.nativeRequestTerminalLedger.requests.push(structuredClone(duplicateNativeTuple.nativeEntry));
  assertRetirementRejected(duplicateNativeTuple.report, structuredClone(duplicateNativeTuple.report.network),
    'a duplicate native request tuple is ambiguous');

  const duplicateDiagnosticTuple = makeCloseRetirementReport('cda');
  duplicateDiagnosticTuple.report.network.push({
    ...structuredClone(duplicateDiagnosticTuple.rawNetworkRow),
    playwrightRequestId: 'cda-request-18',
  });
  assertRetirementRejected(duplicateDiagnosticTuple.report, structuredClone(duplicateDiagnosticTuple.report.network),
    'two CDA diagnostics cannot borrow one native browser request');

  const wrongProjectedBrowserId = makeCloseRetirementReport('cda');
  wrongProjectedBrowserId.report.network[0].browserRequestId = 'playwright-request-other';
  assertRetirementRejected(wrongProjectedBrowserId.report, structuredClone(wrongProjectedBrowserId.report.network),
    'the CDA projection must provide the exact native browser request ID');

  const wrongNativeRequestObject = makeCloseRetirementReport('basic');
  wrongNativeRequestObject.nativeEntry.nativeEventChronology[1].browserRequestId = 'playwright-request-other';
  assertRetirementRejected(wrongNativeRequestObject.report, structuredClone(wrongNativeRequestObject.report.network),
    'terminal evidence from another native browser request is rejected');
});

test('finishReport rejects absent terminal/probe evidence and a pending capability request', () => {
  const absentTerminal = makeCloseRetirementReport('basic');
  absentTerminal.nativeEntry.terminalEvent = undefined;
  absentTerminal.nativeEntry.state = 'pending';
  absentTerminal.nativeEntry.complete = false;
  absentTerminal.nativeEntry.failure = undefined;
  absentTerminal.nativeEntry.nativeEventChronology = [{
    event: 'request', browserRequestId, observedAt: nativeRequestObservedAt, objectMatch: true,
  }];
  const noTerminalObservation = nativeAbortSignalObservationForRequest({
    ...absentTerminal.nativeEntry,
    requestCorrelationId: requestId,
    requestIdentityMatchCount: 1,
  }, absentTerminal.report.nativeAbortProbeEvents);
  assert.equal(noTerminalObservation.nativeTerminalObserved, false);
  assertRetirementRejected(absentTerminal.report, structuredClone(absentTerminal.report.network),
    'a correlated abort signal is not a native terminal event');

  const absentProbe = makeCloseRetirementReport('basic');
  absentProbe.report.nativeAbortProbeEvents = [];
  const noProbeObservation = nativeAbortSignalObservationForRequest({
    ...absentProbe.nativeEntry,
    requestCorrelationId: requestId,
    requestIdentityMatchCount: 1,
  }, absentProbe.report.nativeAbortProbeEvents);
  assert.equal(noProbeObservation.exactRequestSignalCorrelation, false);
  assertRetirementRejected(absentProbe.report, structuredClone(absentProbe.report.network),
    'native terminal failure without an exact signal and owner probe is rejected');

  const pendingCapability = makeCloseRetirementReport('basic');
  const capabilityPath = path.replace('/schema-fields', '/construction-capabilities');
  const capabilityRequestId = 'capability-request-77';
  pendingCapability.report.network[0].url = `${origin}${capabilityPath}`;
  pendingCapability.report.network[0].requestDetails.requestId = capabilityRequestId;
  pendingCapability.nativeEntry.requestId = capabilityRequestId;
  pendingCapability.nativeEntry.path = capabilityPath;
  pendingCapability.nativeEntry.failure = undefined;
  pendingCapability.nativeEntry.terminalEvent = undefined;
  pendingCapability.nativeEntry.state = 'pending';
  pendingCapability.nativeEntry.complete = false;
  pendingCapability.nativeEntry.nativeEventChronology = [{
    event: 'request', browserRequestId, observedAt: nativeRequestObservedAt, objectMatch: true,
  }];
  pendingCapability.report.nativeAbortSignalObservations = [];
  pendingCapability.report.nativeAbortProbeEvents = [];
  assertRetirementRejected(pendingCapability.report, structuredClone(pendingCapability.report.network),
    'a pending construction-capabilities request is not a terminal Catalog retirement');
});
