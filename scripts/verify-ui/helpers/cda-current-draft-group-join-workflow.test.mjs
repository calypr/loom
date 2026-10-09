import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import { runAfterNativeRequestDrain } from './report.mjs';
import { flushNativeAbortProbeEvents } from './native-abort-probe.mjs';
import {
  installGroupJoinNativeCapture,
  prepareCdaGroupJoinOracle,
  registerGroupJoinNativeRequestProjection,
} from '../workflows/cda-current-draft-group-join-workflow.mjs';

test('subject.reference witness keeps overlapping Group counts 2 to 1 through Count distinct', () => {
  const rows = [
    { _id: 'Observation/1', id: '1', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/2', id: '2', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/3', id: '3', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/4', id: '4', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/left-only' },
  ];

  const oracle = prepareCdaGroupJoinOracle(rows);

  assert.equal(oracle.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.sharedGroupKey, 'Patient/shared');
  assert.equal(oracle.memberships.leftOnlyGroupKey, 'Patient/left-only');
  assert.deepEqual(oracle.expectedLeftInitial, [['Patient/left-only', 1], ['Patient/shared', 2]]);
  assert.deepEqual(oracle.expectedRight, [['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeftDistinct, [['Patient/left-only', 1], ['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeft, [
    ['Patient/left-only', 1, '—', '—'],
    ['Patient/shared', 2, 'Patient/shared', 1],
  ]);
  assert.deepEqual(oracle.expectedInner, [['Patient/shared', 2, 'Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedDistinctInner, [['Patient/shared', 1, 'Patient/shared', 1]]);
});

test('Group/Join installs the exact Explorer probe before opening the shared native request tracker', async () => {
  const events = [];
  let binding;
  let capture;
  const project = 'loom_dev_cda_fhir';
  const explorer = 'cda-cdj-fresh-91b6';
  const apiOrigin = 'http://127.0.0.1:8188';
  const uiOrigin = 'http://127.0.0.1:30008';
  const report = { target: { project, explorer: null, uiUrl: `${uiOrigin}/` }, nativeRequests: [] };
  const tracker = { shared: true };
  const probeSources = [];
  const browserContext = {
    async exposeBinding(name, handler) {
      events.push('exposeBinding');
      binding = { name, handler };
    },
    async addInitScript(source) { events.push('addInitScript'); probeSources.push(source); },
  };
  const page = {
    context: () => browserContext,
    async evaluate(source) { events.push('evaluate-current-document'); probeSources.push(source); },
  };
  const cda = {
    report,
    captureRequests(ownedPathPrefix, options) {
      events.push('captureRequests');
      capture = { ownedPathPrefix, options };
      return tracker;
    },
  };

  const result = await installGroupJoinNativeCapture({ page, cda, project, explorer, uiOrigin });

  const explorerBase = `/api/v1/projects/${project}/explorers/${explorer}`;
  assert.deepEqual(events, ['exposeBinding', 'addInitScript', 'evaluate-current-document', 'captureRequests']);
  assert.equal(binding.name, '__loomNativeAbortProbeBinding');
  assert.equal(probeSources.length, 2);
  assert.equal(probeSources[0], probeSources[1]);
  assert(probeSources.every(source => source.includes(JSON.stringify(uiOrigin))));
  assert(probeSources.every(source => !source.includes(JSON.stringify(apiOrigin))));
  assert.equal(result, tracker);
  assert.equal(report.explorer, explorer);
  assert.equal(report.target.explorer, explorer);
  assert.deepEqual(report.nativeRequestCaptureScope, {
    project,
    explorer,
    selectedExplorer: explorer,
    origin: uiOrigin,
    observedPathPrefix: `${explorerBase}/authoring/v2`,
  });
  assert.equal(capture.ownedPathPrefix, `${explorerBase}/authoring/v2`);
  assert.deepEqual(capture.options, { responsePaths: /commands|construction-proposals/ });
  for (const path of [
    `${explorerBase}/authoring/v2/construction-capabilities`,
    `${explorerBase}/authoring/v2/semantic-inventory`,
    `${explorerBase}/authoring/v2/schema-fields`,
  ]) {
    assert(path.startsWith(`${capture.ownedPathPrefix}/`), `${path} must be inside the captured Explorer scope`);
  }

  const sandbox = {
    AbortController,
    URL,
    Date,
    Error,
    Headers,
    crypto: { randomUUID: () => '12345678-1234-4123-8123-123456789abc' },
    location: { href: `${uiOrigin}/` },
    document: { addEventListener() {}, querySelectorAll: () => [] },
    MutationObserver: class { observe() {} },
    fetch(_input, init = {}) {
      const pending = new Promise((_resolve, reject) => {
        if (init.signal?.aborted) reject(new Error('The operation was aborted.'));
        else init.signal?.addEventListener('abort', () => reject(new Error('The operation was aborted.')), { once: true });
      });
      pending.catch(() => undefined);
      return pending;
    },
    __loomNativeAbortProbeBinding: payload => binding.handler({}, payload),
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(probeSources[0], sandbox);
  assert.equal(report.nativeAbortProbeEvents.length, 1);
  assert.deepEqual(
    Object.fromEntries(['kind', 'project', 'explorer', 'explorerScope'].map(key => [key, report.nativeAbortProbeEvents[0][key]])),
    { kind: 'probe-installed', project, explorer, explorerScope: 'exact' },
  );
  assert.equal(Number.isFinite(report.nativeAbortProbeEvents[0].at), true);

  const fetchAndAbort = async (url, requestId) => {
    const controller = new sandbox.AbortController();
    const pending = sandbox.fetch(url, {
      method: 'POST',
      headers: { 'X-Request-ID': requestId },
      signal: controller.signal,
    });
    controller.abort();
    await pending.catch(() => undefined);
    await new Promise(resolve => setImmediate(resolve));
  };
  const schemaFieldsPath = `${explorerBase}/authoring/v2/schema-fields`;
  const exactRequestId = 'schema-fields-11111111-1111-4111-8111-111111111111';
  const wrongOriginRequestId = 'schema-fields-22222222-2222-4222-8222-222222222222';
  const wrongExplorerRequestId = 'schema-fields-33333333-3333-4333-8333-333333333333';
  await fetchAndAbort(`${uiOrigin}${schemaFieldsPath}`, exactRequestId);
  await fetchAndAbort(`${apiOrigin}${schemaFieldsPath}`, wrongOriginRequestId);
  await fetchAndAbort(`${uiOrigin}${schemaFieldsPath.replace(explorer, 'cda-cdj-other-0b37')}`, wrongExplorerRequestId);

  const abortEvents = report.nativeAbortProbeEvents.filter(event => event.kind === 'abort-controller-call');
  const exactMatches = abortEvents.flatMap(event => event.requests ?? [])
    .filter(request => request.requestId === exactRequestId);
  assert.equal(exactMatches.length, 1);
  assert.equal(exactMatches[0].origin, uiOrigin);
  assert.equal(exactMatches[0].path, schemaFieldsPath);
  assert.equal(exactMatches[0].method, 'POST');
  assert.equal(abortEvents.find(event => event.requests?.includes(exactMatches[0])).signalWasAlreadyAborted, false);
  assert.equal(abortEvents.flatMap(event => event.requests ?? []).some(request =>
    [wrongOriginRequestId, wrongExplorerRequestId].includes(request.requestId)), false);
});

test('Group/Join projects after native drain and probe collection while preserving unresolved requests', async () => {
  const project = 'loom_dev_cda_fhir';
  const explorer = 'cda-cdj-fresh-91b6';
  const origin = 'http://127.0.0.1:30008';
  const pathFor = endpoint => `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/${endpoint}`;
  const mainFrame = {};
  const page = { mainFrame: () => mainFrame };
  const makeRequest = (requestId, path) => ({
    url: () => `${origin}${path}`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': requestId }),
    frame: () => mainFrame,
  });
  const requestA = makeRequest('schema-fields-11111111-1111-4111-8111-111111111111', pathFor('schema-fields'));
  const requestB = makeRequest('schema-fields-22222222-2222-4222-8222-222222222222', pathFor('schema-fields'));
  const capabilitiesRequest = makeRequest('cda-request-capabilities-1', pathFor('construction-capabilities'));
  const semanticRequests = Array.from({ length: 4 }, (_, index) =>
    makeRequest(`feature-catalog-semantic-${index + 1}`, pathFor('semantic-inventory')));
  const nativeRows = [
    {
      requestId: requestA.headers()['x-request-id'], browserRequestId: 'playwright-16', origin, path: pathFor('schema-fields'), method: 'POST',
      status: null,
      nativeEventChronology: [{ event: 'request', browserRequestId: 'playwright-16', observedAt: 10, objectMatch: true }],
    },
    {
      requestId: requestB.headers()['x-request-id'], browserRequestId: 'playwright-17', origin, path: pathFor('schema-fields'), method: 'POST',
      status: 200,
      nativeEventChronology: [
        { event: 'request', browserRequestId: 'playwright-17', observedAt: 40, objectMatch: true },
        { event: 'response', browserRequestId: 'playwright-17', observedAt: 50, objectMatch: true },
        { event: 'requestfinished', browserRequestId: 'playwright-17', observedAt: 60, objectMatch: true },
      ],
    },
    {
      requestId: capabilitiesRequest.headers()['x-request-id'], browserRequestId: 'playwright-18', origin,
      path: pathFor('construction-capabilities'), method: 'POST', status: null,
      nativeEventChronology: [{ event: 'request', browserRequestId: 'playwright-18', observedAt: 70, objectMatch: true }],
    },
    ...semanticRequests.map((request, index) => ({
      requestId: request.headers()['x-request-id'], browserRequestId: `playwright-${19 + index}`, origin,
      path: pathFor('semantic-inventory'), method: 'POST', status: null,
      nativeEventChronology: [{ event: 'request', browserRequestId: `playwright-${19 + index}`, observedAt: 80 + index, objectMatch: true }],
    })),
  ];
  const networkRows = nativeRows.slice(0, 2).map((entry, index) => ({
    kind: 'network',
    method: entry.method,
    resourceType: 'fetch',
    url: `${origin}${entry.path}`,
    rawURL: `${origin}${entry.path}`,
    requestId: entry.requestId,
    requestDetails: { requestId: entry.requestId },
    requestScope: { expectedProject: project, requestProject: project, requestExplorer: explorer },
    requestTimeline: { mainFrameNavigations: [] },
    playwrightRequestId: `cda-request-diagnostic-${index + 1}`,
    browserRequestId: entry.browserRequestId,
    errorText: undefined,
  }));
  const probeEvents = [];
  const lateProbeEvent = {
    kind: 'abort-controller-call',
    controllerId: 'controller-schema-fields-1',
    createdAt: 5,
    abortedAt: 25,
    signalWasAlreadyAborted: false,
    requests: [{
      requestId: requestA.headers()['x-request-id'], origin, path: pathFor('schema-fields'), method: 'POST',
      startedAt: 10, requestIdSource: 'request-header', fetchStateAtAbort: 'pending',
    }],
  };
  const drainEvidence = [{ status: 'timed-out', unresolvedRequests: [{ requestId: 'raw-pending-evidence' }] }];
  const report = {
    target: { project, explorer, uiUrl: `${origin}/` },
    nativeRequestCaptureScope: {
      project,
      explorer,
      selectedExplorer: explorer,
      origin,
      observedPathPrefix: `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2`,
    },
    nativeRequests: nativeRows,
    network: networkRows,
    nativeAbortProbeEvents: probeEvents,
    nativeRequestDrainEvidence: drainEvidence,
  };
  const tracker = {
    byRequest: new Map([
      [requestA, nativeRows[0]],
      [requestB, nativeRows[1]],
      [capabilitiesRequest, nativeRows[2]],
      ...semanticRequests.map((request, index) => [request, nativeRows[3 + index]]),
    ]),
  };
  const projections = [];
  const cda = {
    report,
    registerAfterNativeRequestDrainProjection(callback) { projections.push(callback); },
  };
  registerGroupJoinNativeRequestProjection({ cda, tracker, page });
  assert.equal(report.nativeRequestTerminalLedger, undefined);
  assert.equal(report.nativeAbortProbeCorrelations, undefined);
  const pageScope = {
    Set,
    Promise,
    Date,
    setTimeout,
    clearTimeout,
    __loomNativeAbortProbePendingBindingCalls: new Set(),
    __loomNativeAbortProbeBindingFailures: [],
  };
  pageScope.globalThis = pageScope;
  page.evaluate = expression => vm.runInNewContext(expression, pageScope);
  let pendingBodyRead;
  const finalization = await runAfterNativeRequestDrain({
    terminalDrains: [
      async () => {
        nativeRows[0].status = 200;
        nativeRows[0].failure = 'net::ERR_ABORTED';
        nativeRows[0].completedAt = 30;
        nativeRows[0].nativeEventChronology.push(
          { event: 'response', browserRequestId: 'playwright-16', observedAt: 20, objectMatch: true },
          { event: 'requestfailed', browserRequestId: 'playwright-16', observedAt: 30, objectMatch: true },
        );
        let pendingProbeCall;
        pendingProbeCall = new Promise(resolve => setTimeout(() => {
          probeEvents.push(lateProbeEvent);
          resolve();
        }, 10)).then(() => pageScope.__loomNativeAbortProbePendingBindingCalls.delete(pendingProbeCall));
        pageScope.__loomNativeAbortProbePendingBindingCalls.add(pendingProbeCall);
        pendingBodyRead = new Promise(resolve => setTimeout(() => {
          networkRows[0].status = 200;
          networkRows[0].errorText = 'net::ERR_ABORTED';
          networkRows[0].response = { body: { code: 'REQUEST_ABORTED' }, bodyNotRead: false };
          resolve();
        }, 10));
      },
    ],
    postDrainDrains: [
      async () => { await pendingBodyRead; },
      () => flushNativeAbortProbeEvents(page, { timeoutMs: 1_000 }),
    ],
    projections,
  });
  assert.deepEqual(finalization.terminalDrainResults.map(result => result.status), ['fulfilled']);
  assert.deepEqual(finalization.postDrainResults.map(result => result.status), ['fulfilled', 'fulfilled']);
  assert.deepEqual(finalization.projectionResults.map(result => result.status), ['fulfilled']);
  const projection = finalization.projectionResults[0].value;
  assert.deepEqual(networkRows[0].response, { body: { code: 'REQUEST_ABORTED' }, bodyNotRead: false },
    'response body delivery during post-drain flush precedes the projection');
  const rawEvidence = {
    nativeRequests: structuredClone(report.nativeRequests),
    network: structuredClone(report.network),
    probeEvents: structuredClone(report.nativeAbortProbeEvents),
    drainEvidence: structuredClone(report.nativeRequestDrainEvidence),
  };

  assert.deepEqual(projection.correlations.map(({ requestId, browserRequestId, playwrightRequestId, requestIdentityMatchCount, observation }) => ({
    requestId, browserRequestId, playwrightRequestId, requestIdentityMatchCount,
    exactRequestSignalCorrelation: observation.exactRequestSignalCorrelation,
  })), [
    {
      requestId: requestA.headers()['x-request-id'], browserRequestId: 'playwright-16',
      playwrightRequestId: 'cda-request-diagnostic-1', requestIdentityMatchCount: 1,
      exactRequestSignalCorrelation: true,
    },
    {
      requestId: requestB.headers()['x-request-id'], browserRequestId: 'playwright-17',
      playwrightRequestId: 'cda-request-diagnostic-2', requestIdentityMatchCount: 1,
      exactRequestSignalCorrelation: false,
    },
  ]);
  assert.equal(projection.terminalLedger.complete, false);
  assert.deepEqual(projection.terminalLedger.counts, { total: 7, finished: 1, failed: 1, pending: 5 });
  assert.deepEqual(projection.terminalLedger.requests.map(({ requestId, browserRequestId, state, frameIdentityStatus, frameIsMainFrame }) => ({
    requestId, browserRequestId, state, frameIdentityStatus, frameIsMainFrame,
  })), [
    { requestId: requestA.headers()['x-request-id'], browserRequestId: 'playwright-16', state: 'failed', frameIdentityStatus: 'exact', frameIsMainFrame: true },
    { requestId: requestB.headers()['x-request-id'], browserRequestId: 'playwright-17', state: 'finished', frameIdentityStatus: 'exact', frameIsMainFrame: true },
    { requestId: capabilitiesRequest.headers()['x-request-id'], browserRequestId: 'playwright-18', state: 'pending', frameIdentityStatus: 'exact', frameIsMainFrame: true },
    ...semanticRequests.map((request, index) => ({
      requestId: request.headers()['x-request-id'], browserRequestId: `playwright-${19 + index}`,
      state: 'pending', frameIdentityStatus: 'exact', frameIsMainFrame: true,
    })),
  ]);
  assert.equal(projection.terminalLedger.requests[0].playwrightRequestId, 'cda-request-diagnostic-1');
  assert.notEqual(projection.terminalLedger.requests[0].browserRequestId, projection.terminalLedger.requests[0].playwrightRequestId);
  assert.deepEqual(projection.terminalLedger.requests[0].nativeEventChronology.at(-1), {
    event: 'requestfailed', browserRequestId: 'playwright-16', observedAt: 30, objectMatch: true, failure: 'net::ERR_ABORTED',
  });
  assert.strictEqual(report.nativeRequests, nativeRows);
  assert.strictEqual(report.network, networkRows);
  assert.strictEqual(report.nativeAbortProbeEvents, probeEvents);
  assert.strictEqual(report.nativeRequestDrainEvidence, drainEvidence);
  assert.deepEqual(report.nativeRequests, rawEvidence.nativeRequests);
  assert.deepEqual(report.network, rawEvidence.network);
  assert.deepEqual(report.nativeAbortProbeEvents, rawEvidence.probeEvents);
  assert.deepEqual(report.nativeRequestDrainEvidence, rawEvidence.drainEvidence);
});

test('post-drain projection failures remain visible to the fixture finalizer', async () => {
  const projectionError = new Error('projection failed');
  const result = await runAfterNativeRequestDrain({
    terminalDrains: [async () => undefined],
    postDrainDrains: [async () => undefined],
    projections: [async () => { throw projectionError; }],
  });
  assert.equal(result.terminalDrainResults[0].status, 'fulfilled');
  assert.equal(result.postDrainResults[0].status, 'fulfilled');
  assert.equal(result.projectionResults[0].status, 'rejected');
  assert.strictEqual(result.projectionResults[0].reason, projectionError);
});
