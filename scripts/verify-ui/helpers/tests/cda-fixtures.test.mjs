import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import { correlateRequestFailure } from '../network-timing.mjs';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import {
  captureCdaActionEpoch,
  classifyExpectedCdaCancellation,
  environmentSnapshot,
  finalizeCdaExplorerMetadata,
  gateFailure,
} from '../cda-fixtures.mjs';

const greenCdaReport = (nativeRequests = []) => ({
  runnerStatus: 'passed',
  requiredChecks: ['required interaction completed'],
  missingRequiredChecks: [],
  assertions: [{ name: 'required interaction completed', status: 'passed' }],
  network: [],
  errors: [],
  nativeRequests,
});

test('CDA action epoch records the actual wall-clock interval around a trusted Close click', () => {
  const startedAt = 1_791_548_796_304;
  const trustedCloseAt = 1_791_548_796_412;
  let clockSamples = 0;
  const epoch = captureCdaActionEpoch(startedAt, () => {
    clockSamples += 1;
    return 1_791_548_796_447;
  });

  assert.equal(clockSamples, 1, 'the final epoch must sample the controlled clock exactly once');
  assert.deepEqual(epoch, {
    startedAtEpochMs: startedAt,
    finishedAtEpochMs: 1_791_548_796_447,
  });
  assert(epoch.startedAtEpochMs <= trustedCloseAt && trustedCloseAt <= epoch.finishedAtEpochMs,
    'the measured action interval must enclose the trusted native Close click');
});

test('CDA action epoch does not fabricate missing or invalid wall-clock bounds from elapsed time', () => {
  const elapsedMs = 5_000;
  assert.equal(captureCdaActionEpoch(undefined, () => 1_791_548_796_447, elapsedMs), undefined);
  assert.equal(captureCdaActionEpoch(Number.NaN, () => 1_791_548_796_447, elapsedMs), undefined);
  assert.equal(captureCdaActionEpoch(1_791_548_796_500, () => 1_791_548_796_447, elapsedMs), undefined);
  assert.equal(captureCdaActionEpoch(1_791_548_796_304, () => Number.NaN, elapsedMs), undefined);
});

test('CDA environment snapshot retains the explicit oracle database without forwarding host credentials', () => {
  const keys = ['LOOM_ARANGO_DATABASE', 'LOOM_ARANGO_USER', 'LOOM_ARANGO_PASSWORD', 'UNRELATED_SECRET'];
  const original = new Map(keys.map(key => [key, process.env[key]]));
  try {
    process.env.LOOM_ARANGO_DATABASE = 'synthetic_oracle_database';
    process.env.LOOM_ARANGO_USER = 'synthetic_user';
    process.env.LOOM_ARANGO_PASSWORD = 'synthetic_password';
    process.env.UNRELATED_SECRET = 'synthetic_unrelated_secret';
    const snapshot = environmentSnapshot({ project: 'synthetic_cda_project', generation: '' });

    assert.equal(snapshot.LOOM_ARANGO_DATABASE, 'synthetic_oracle_database');
    assert.equal(snapshot.LOOM_CDA_PROJECT, 'synthetic_cda_project');
    assert.equal(Object.isFrozen(snapshot), true);
    for (const key of ['LOOM_ARANGO_USER', 'LOOM_ARANGO_PASSWORD', 'UNRELATED_SECRET', 'LOOM_CDA_GENERATION', 'LOOM_CDA_DATASET_DIR']) {
      assert.equal(Object.hasOwn(snapshot, key), false, `Snapshot must omit ${key}`);
    }

    process.env.LOOM_ARANGO_DATABASE = '';
    assert.equal(Object.hasOwn(environmentSnapshot({}), 'LOOM_ARANGO_DATABASE'), false);
    delete process.env.LOOM_ARANGO_DATABASE;
    assert.equal(Object.hasOwn(environmentSnapshot({}), 'LOOM_ARANGO_DATABASE'), false);
    assert.equal(snapshot.LOOM_ARANGO_DATABASE, 'synthetic_oracle_database');
  } finally {
    for (const [key, value] of original) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
});

test('green CDA evidence rejects the retained WoYfih request with no terminal outcome', () => {
  const report = greenCdaReport();
  report.nativeRequests = new Array(75);
  report.nativeRequests[74] = {
    requestId: 'playwright-75',
    browserRequestId: 'playwright-75',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-authored-expand-1791465817471/authoring/v2/construction-capabilities',
    startedAt: 1791465845711,
    authorizationHeaderPresent: false,
  };

  const failure = gateFailure(report);
  assert(failure, 'a passing assertion and empty errors array must not validate an unfinished native request');
  const details = JSON.parse(failure.message.replace(/^CDA verification evidence is incomplete: /, ''));
  assert.deepEqual(details.unfinishedNativeRequests, [{
    index: 74,
    requestId: 'playwright-75',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/cda-authored-expand-1791465817471/authoring/v2/construction-capabilities',
  }]);
});

test('completed native responses have terminal status and completion time', () => {
  const report = greenCdaReport([{
    requestId: 'playwright-1',
    path: '/construction-capabilities',
    status: 200,
    completedAt: 1791465845711,
    nativeEventChronology: [
      { event: 'request' }, { event: 'response' }, { event: 'requestfinished' },
    ],
  }]);

  assert.equal(gateFailure(report), undefined);
});

test('response status and body completion without a Playwright terminal event remain unfinished', () => {
  const report = greenCdaReport([{
    requestId: 'playwright-2',
    path: '/construction-capabilities',
    status: 200,
    completedAt: 1791465845711,
    nativeEventChronology: [{ event: 'request' }, { event: 'response' }],
  }]);

  const failure = gateFailure(report);
  assert(failure);
  const details = JSON.parse(failure.message.replace(/^CDA verification evidence is incomplete: /, ''));
  assert.deepEqual(details.unfinishedNativeRequests, [{
    index: 0, requestId: 'playwright-2', path: '/construction-capabilities',
  }]);
});

test('an explicit completed request failure remains fatal through the existing errors gate', () => {
  const report = greenCdaReport([{
    requestId: 'playwright-1',
    path: '/construction-capabilities',
    failure: 'net::ERR_ABORTED',
    completedAt: 1791465845711,
    nativeEventChronology: [{ event: 'request' }, { event: 'requestfailed' }],
  }]);
  report.errors.push({ kind: 'network', requestId: 'playwright-1', error: 'net::ERR_ABORTED' });

  const failure = gateFailure(report);
  assert(failure);
  const details = JSON.parse(failure.message.replace(/^CDA verification evidence is incomplete: /, ''));
  assert.deepEqual(details.unfinishedNativeRequests, []);
  assert.equal(details.unexpectedErrors.length, 1);
  assert.equal(details.unexpectedErrors[0].kind, 'network');
});

test('malformed status, completion time, or empty failure is not terminal evidence', () => {
  const report = greenCdaReport([
    { requestId: 'string-status', path: '/string-status', status: '200', completedAt: 1 },
    { requestId: 'invalid-status', path: '/invalid-status', status: 99, completedAt: 1 },
    { requestId: 'infinite-time', path: '/infinite-time', status: 200, completedAt: Infinity },
    { requestId: 'empty-failure', path: '/empty-failure', failure: '  ', completedAt: 1 },
  ]);

  const failure = gateFailure(report);
  assert(failure);
  const details = JSON.parse(failure.message.replace(/^CDA verification evidence is incomplete: /, ''));
  assert.deepEqual(details.unfinishedNativeRequests.map(entry => entry.index), [0, 1, 2, 3]);
});

test('response headers followed by requestfailed need exact retirement proof at the final gate', async () => {
  const captureRetiredResponse = async classifyRetirement => {
    const page = new EventEmitter();
    const report = greenCdaReport();
    const requestFailures = new WeakMap();
    const trackers = new Set();
    const url = 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/construction-capabilities';
    const reason = 'This exact request was retired by the next native navigation.';
    const proof = { action: 'navigate after the accepted operation', nextAction: 'open saved Builder state' };
    let capture;
    const request = {
      url: () => url,
      method: () => 'POST',
      headers: () => ({ 'x-request-id': 'retired-capabilities-request' }),
      postData: () => JSON.stringify({ outputId: 'output-exact' }),
      failure: () => ({ errorText: 'net::ERR_ABORTED' }),
    };

    page.on('requestfailed', failedRequest => {
      const entry = capture.byRequest.get(failedRequest);
      const failure = {
        method: failedRequest.method(),
        url: failedRequest.url(),
        requestId: entry.requestId,
        playwrightRequestId: `cda-request-${entry.browserRequestId}`,
        errorText: failedRequest.failure().errorText,
      };
      requestFailures.set(failedRequest, failure);
      report.network.push({ kind: 'network', browserRequestId: entry.browserRequestId, errorText: failure.errorText });
      if (classifyRetirement) {
        classifyExpectedCdaCancellation({ request: failedRequest, reason, proof, report, requestFailures, trackers });
      }
    });

    capture = captureCDARequests(page, {
      apiOrigin: 'http://127.0.0.1:8188',
      ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
      report,
      responsePaths: /construction-capabilities/,
    });
    trackers.add(capture);
    page.emit('request', request);
    page.emit('response', {
      request: () => request,
      status: () => 200,
      headers: () => ({}),
      text: () => Promise.reject(new Error('response.text: Protocol error (Network.getResponseBody): No data found for resource with given identifier\nResponse body is not available for a response that was navigated away from. Read response.body() before triggering any navigation.')),
    });
    page.emit('requestfailed', request);
    return { capture, report, entry: report.nativeRequests[0], reason };
  };

  const unclassified = await captureRetiredResponse(false);
  assert.equal(unclassified.entry.status, 200);
  assert.equal(unclassified.entry.failure, 'net::ERR_ABORTED');
  await assert.rejects(unclassified.capture.flush(), /Failed owned CDA response reads/);
  const unclassifiedFailure = gateFailure(unclassified.report);
  assert(unclassifiedFailure);
  const unclassifiedDetails = JSON.parse(unclassifiedFailure.message.replace(/^CDA verification evidence is incomplete: /, ''));
  assert.deepEqual(unclassifiedDetails.unfinishedNativeRequests, [], 'status plus requestfailed is terminally observed');
  assert.equal(unclassifiedDetails.unexpectedErrors.length, 1, 'the unclassified owned failure remains fatal');

  const classified = await captureRetiredResponse(true);
  assert.equal(classified.entry.status, 200);
  assert.equal(classified.entry.failure, 'net::ERR_ABORTED');
  await classified.capture.flush();
  assert.equal(classified.entry.expectedCancellation.reason, classified.reason);
  assert.equal(gateFailure(classified.report), undefined, 'only the exact, body-read-proven cancellation may retire this response');
});

test('CDA report retains explicitly workflow-owned Explorer identity at teardown', () => {
  const fixtureSource = readFileSync(new URL('../cda-fixtures.mjs', import.meta.url), 'utf8');
  assert.match(fixtureSource, /finalizeCdaExplorerMetadata\(report, target\)/);
  assert.doesNotMatch(fixtureSource, /report\.explorer = target\.explorer/);
  const report = {
    explorer: 'collection-repair-fresh-113',
    target: { explorer: null },
    nativeRequests: [{ requestScope: { requestExplorer: 'unrelated-request-explorer' } }],
  };
  const target = { explorer: 'configured-existing-explorer' };

  assert.equal(finalizeCdaExplorerMetadata(report, target), 'collection-repair-fresh-113');
  assert.equal(report.explorer, 'collection-repair-fresh-113');
  assert.equal(report.target.explorer, 'collection-repair-fresh-113');
});

test('CDA report uses explicit target metadata as fallback without inferring Explorer from request traffic', () => {
  const workflowSelected = { target: { explorer: 'workflow-selected-explorer' }, nativeRequests: [] };
  assert.equal(finalizeCdaExplorerMetadata(workflowSelected, { explorer: 'configured-existing-explorer' }), 'workflow-selected-explorer');
  assert.equal(workflowSelected.explorer, 'workflow-selected-explorer');

  const configured = { target: { explorer: null }, nativeRequests: [{ requestScope: { requestExplorer: 'incidental-explorer' } }] };
  assert.equal(finalizeCdaExplorerMetadata(configured, { explorer: 'configured-existing-explorer' }), 'configured-existing-explorer');
  assert.equal(configured.target.explorer, 'configured-existing-explorer');

  const unresolved = { target: { explorer: null }, nativeRequests: [{ requestScope: { requestExplorer: 'incidental-explorer' } }] };
  assert.equal(finalizeCdaExplorerMetadata(unresolved, { explorer: null }), null);
  assert.equal(unresolved.explorer, null);
  assert.equal(unresolved.target.explorer, null);
});

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


test('actual CDA handlers use the workflow-created Explorer in late owned request diagnostics', () => {
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
  const requestedExplorer = 'cda-authored-expand-requested';
  const createdExplorer = 'cda-authored-expand-1791465817471';
  const failingRequestPath = `/api/v1/projects/loom_dev_cda_fhir/explorers/${createdExplorer}/authoring/v2/reconcile`;
  const report = { target: { explorer: createdExplorer }, network: [] };
  const target = {
    fixtureProject: 'loom_dev_cda_fhir', project: 'loom_dev_cda_fhir',
    fixtureGeneration: 'cda-fhir-v1', explorer: requestedExplorer,
  };
  const handlers = makeHandlers({ report, target, page: { mainFrame: () => mainFrame },
    performance: { now: () => now }, correlateRequestFailure });
  const makeRequest = (index, navigation = false) => ({
    url: () => `http://127.0.0.1:8188${failingRequestPath}`,
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
  assert.equal(new URL(failure.rawURL).pathname, failingRequestPath, 'the failed request path must identify the workflow-created Explorer');
  assert.deepEqual(failure.requestScope, {
    expectedProject: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', configuredExplorer: createdExplorer,
    requestProject: 'loom_dev_cda_fhir', requestExplorer: createdExplorer,
  });
  assert(gateFailure(report), 'a native request failure remains fatal after diagnostic scope enrichment');
  assert.deepEqual(failure.requestTimeline, {
    requestStartedMs: 450, failedAtMs: 500, durationMs: 50,
    action: { id: 'cda-action-9', label: 'reload after upstream edit' },
    mainFrameNavigations: [{ id: 'navigation-2', atMs: 470, url: 'http://127.0.0.1:30008/builder' }],
  });
  assert.equal(failure.triggerAction, 'reload after upstream edit');
  assert.equal(failure.requestAction.id, 'cda-action-9');
  assert.equal(requestStartedAt, 1450, 'the last request was recorded after 449 earlier request events');
  assert.deepEqual(report.navigationTimings, [
    { id: 'navigation-1', atMs: 1, url: `http://127.0.0.1:8188${failingRequestPath}`, phase: 'request-start' },
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
