import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import { correlateRequestFailure } from '../network-timing.mjs';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { classifyExpectedCdaCancellation } from '../cda-fixtures.mjs';

test('late request-failure copies inherit exact cancellation evidence and leave other request IDs unexpected', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [], network: [] };
  const requestFailures = new WeakMap();
  const trackers = new Set();
  const reason = 'A same-action replacement superseded this contributor lookup.';
  const proof = { action: 'Only records meeting a condition', query: 'same action and owned route' };
  const url = 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/related-expand-contributors';
  let capture;
  let expectedRequest;

  page.on('requestfailed', failedRequest => {
    const capturedEntry = capture.byRequest.get(failedRequest);
    const failure = {
      method: failedRequest.method(),
      url: failedRequest.url(),
      requestId: 'contributor-request-shared',
      playwrightRequestId: `cda-request-${capturedEntry.browserRequestId}`,
      errorText: 'net::ERR_ABORTED',
    };
    requestFailures.set(failedRequest, failure);
    report.network.push({ kind: 'network', browserRequestId: capturedEntry.browserRequestId });
    if (failedRequest !== expectedRequest) return;
    classifyExpectedCdaCancellation({ request: failedRequest, reason, proof, report, requestFailures, trackers });
  });

  capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  trackers.add(capture);
  const makeRequest = () => ({
    url: () => url,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'contributor-request-shared' }),
    postData: () => JSON.stringify({ outputId: 'out-owned', snapshotToken: 'sha256:owned' }),
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });
  expectedRequest = makeRequest();
  page.emit('request', expectedRequest);
  const capturedEntry = capture.byRequest.get(expectedRequest);
  assert(capturedEntry?.browserRequestId);
  page.emit('requestfailed', expectedRequest);

  const lateCapturedError = report.errors.find(error => error.browserRequestId === capturedEntry.browserRequestId);
  assert(lateCapturedError, 'the request-capture listener must append its error after the fixture listener');
  assert.equal(lateCapturedError.expected, true, 'the late row must copy the exact request’s earlier expected classification');
  assert.equal(lateCapturedError.expectedCancellation.browserRequestId, capturedEntry.browserRequestId);
  assert.equal(lateCapturedError.expectedCancellation.reason, reason);

  // Exercise same-reason idempotency against an additional late duplicate from this exact request.
  const duplicateLateError = { kind: 'network', origin: capturedEntry.origin, path: capturedEntry.path,
    url: `${capturedEntry.origin}${capturedEntry.path}`, requestId: capturedEntry.requestId,
    browserRequestId: capturedEntry.browserRequestId, method: capturedEntry.method,
    startedAt: capturedEntry.startedAt, error: capturedEntry.failure };
  report.errors.push(duplicateLateError);
  const reconciled = classifyExpectedCdaCancellation({ request: expectedRequest, reason, proof, report, requestFailures, trackers });
  assert.equal(reconciled.reason, reason);
  assert.equal(duplicateLateError.expected, true);
  assert.equal(duplicateLateError.expectedCancellation.browserRequestId, capturedEntry.browserRequestId);
  assert.equal(report.network[0].expected, true);
  assert.equal(report.expectedCancellations.length, 1, 'same-reason reconciliation must not duplicate the cancellation ledger');

  // A matching path and request header do not make a second browser Request expected.
  const unexpectedRequest = makeRequest();
  page.emit('request', unexpectedRequest);
  const unexpectedEntry = capture.byRequest.get(unexpectedRequest);
  assert.notEqual(unexpectedEntry.browserRequestId, capturedEntry.browserRequestId);
  page.emit('requestfailed', unexpectedRequest);
  const unexpectedError = report.errors.find(error => error.browserRequestId === unexpectedEntry.browserRequestId);
  assert(unexpectedError, 'the unrelated native request failure must remain in the report');
  assert.equal(unexpectedError.expected, undefined, 'a different browser request ID must remain unexpected');
  assert.equal(unexpectedError.expectedCancellation, undefined);
  await capture.flush();
});


test('actual CDA handlers retain scope, body allowlist, action, navigation and timing for late owned request 450', () => {
  const source = readFileSync(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  const sourceOf = (startMarker, endMarker) => {
    const start = source.indexOf(startMarker);
    const end = source.indexOf(endMarker, start);
    assert.ok(start >= 0 && end > start, `expected actual handler source ${startMarker}`);
    return source.slice(start, end);
  };
  const requestDiagnosticSource = sourceOf('function requestDiagnostic(request) {', 'function safeAttachmentPath');
  const requestIdSource = sourceOf('    const playwrightRequestId = request => {', '    const capturedEntryFor');
  const requestHandlersSource = sourceOf('    const onRequest = request => {', '    const onConsole = message => {');
  const failureHandlerSource = sourceOf('    const onRequestFailed = request => {', '    const onResponse = response => {');
  const navigationReportSource = sourceOf('      report.navigationTimings = mainFrameNavigations.map(', '      report.browserLifecycle = {');
  const makeHandlers = new Function('deps', `
    const { report, target, page, performance, correlateRequestFailure } = deps;
    const requestIDs = new WeakMap();
    const requestMetadata = new WeakMap();
    const requestFailures = new WeakMap();
    const pendingRequests = new Set();
    const cancellationScopes = [];
    const trackers = new Set();
    const diagnostics = { networkFailures: [] };
    const ownedOrigins = new Set(['http://127.0.0.1:8188', 'http://127.0.0.1:30008']);
    const workflowStartedAt = 1000;
    const mainFrameNavigations = [];
    let navigationSequence = 0;
    let droppedNavigationTimings = 0;
    let requestSequence = 0;
    let activeAction = 'reload after upstream edit';
    let activeActionContext = { id: 'cda-action-9', label: activeAction };
    let retainedDiagnostics = 0;
    const MAX_DIAGNOSTICS = 100;
    const safeText = value => value;
    const safeURL = raw => { const u = new URL(raw); return u.origin + u.pathname; };
    const sanitizePayload = value => value;
    const localRequest = request => ownedOrigins.has(new URL(request.url()).origin);
    const addNetworkDiagnostic = entry => { report.network.push(entry); retainedDiagnostics += 1; };
    const matchesCancellationScope = () => false;
    const capturedEntryFor = () => undefined;
    const expectCanceledRequest = () => {};
    const actionSnapshot = () => activeActionContext ? { id: activeActionContext.id, label: activeActionContext.label } : null;
    ${requestDiagnosticSource}
    ${requestIdSource}
    ${requestHandlersSource}
    ${failureHandlerSource}
    return { onRequest, onFrameNavigated, onRequestFinished, onRequestFailed,
      retainNavigationTimings: () => { ${navigationReportSource} },
      setActionContext: (label, context) => { activeAction = label; activeActionContext = context; } };
  `);
  let now = 1000;
  const mainFrame = { url: () => 'http://127.0.0.1:30008/builder' };
  const report = { target: { explorer: 'explorer-owned' }, network: [] };
  const target = {
    fixtureProject: 'loom_dev_cda_fhir', project: 'loom_dev_cda_fhir',
    fixtureGeneration: 'cda-fhir-v1', explorer: null,
  };
  const handlers = makeHandlers({ report, target, page: { mainFrame: () => mainFrame },
    performance: { now: () => now }, correlateRequestFailure });
  const makeRequest = (index, navigation = false) => ({
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/explorer-owned/authoring/v2/reconcile',
    method: () => 'POST', resourceType: () => 'fetch',
    headers: () => ({ 'x-request-id': `builder-${index}` }),
    postDataJSON: () => ({ expectedDraftVersion: 14, expectedDraftDigest: 'sha256:expected-14',
      outputId: 'out-append', stageId: 'append', authorization: 'must-not-be-captured' }),
    isNavigationRequest: () => navigation, frame: () => mainFrame,
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });
  now += 1;
  handlers.onRequest(makeRequest(1, true));
  let lateRequest;
  for (let index = 2; index <= 450; index += 1) {
    now += 1;
    lateRequest = makeRequest(index);
    handlers.onRequest(lateRequest);
  }
  const requestStartedAt = now;
  now += 20;
  handlers.onFrameNavigated(mainFrame);
  now += 30;
  handlers.onRequestFailed(lateRequest);
  handlers.retainNavigationTimings();

  const failure = report.network.at(-1);
  assert.equal(failure.playwrightRequestId, 'cda-request-450');
  assert.equal(failure.requestDetails.draftVersion, 14);
  assert.equal(failure.requestDetails.draftDigest, 'sha256:expected-14');
  assert.equal(failure.requestDetails.outputId, 'out-append');
  assert.equal(failure.requestDetails.stageId, 'append');
  assert.equal('authorization' in failure.requestDetails, false);
  assert.equal(failure.expected, false, 'diagnostic enrichment must leave a native failure unexpected');
  assert.deepEqual(failure.requestScope, {
    expectedProject: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', configuredExplorer: 'explorer-owned',
    requestProject: 'loom_dev_cda_fhir', requestExplorer: 'explorer-owned',
  });
  assert.deepEqual(failure.requestTimeline, {
    requestStartedMs: 450, failedAtMs: 500, durationMs: 50,
    action: { id: 'cda-action-9', label: 'reload after upstream edit' },
    mainFrameNavigations: [{ id: 'navigation-2', atMs: 470, url: 'http://127.0.0.1:30008/builder' }],
  });
  assert.equal(failure.triggerAction, 'reload after upstream edit');
  assert.equal(failure.requestAction.id, 'cda-action-9');
  assert.equal(requestStartedAt, 1450, 'the last request was recorded after 449 earlier request events');
  assert.deepEqual(report.navigationTimings, [
    { id: 'navigation-1', atMs: 1, url: 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/explorer-owned/authoring/v2/reconcile', phase: 'request-start' },
    { id: 'navigation-2', atMs: 470, url: 'http://127.0.0.1:30008/builder', phase: 'commit' },
  ]);

  handlers.setActionContext(null, null);
  now += 1;
  const startedWithoutAction = makeRequest(451);
  handlers.onRequest(startedWithoutAction);
  handlers.setActionContext('later validation', { id: 'cda-action-10', label: 'later validation' });
  now += 1;
  handlers.onRequestFailed(startedWithoutAction);
  const boundaryFailure = report.network.at(-1);
  assert.equal(boundaryFailure.requestTimeline.action, null);
  assert.equal(boundaryFailure.requestAction, null, 'a failure-time action must not be attributed to a request that started outside an action');
  assert.deepEqual(boundaryFailure.failureAction, { id: 'cda-action-10', label: 'later validation' });
  assert.equal(boundaryFailure.triggerAction, 'later validation', 'legacy triggerAction retains its failure-time meaning');
});
