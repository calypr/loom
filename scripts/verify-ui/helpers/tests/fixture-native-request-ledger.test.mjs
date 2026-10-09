import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  createFixtureNativeRequestLedger,
  finalizeFixtureNativeRequestReport,
  openBasicFixtureNativeRequestScope,
  projectFixtureNetworkDiagnostics,
} from '../native-request-ledger.mjs';
import { classifyNetworkRecord, createReport, finishReport, recordCheck } from '../report.mjs';

const origin = 'http://127.0.0.1:30008';
const project = 'fixture-ledger-project';
const explorer = 'selected-explorer';
const explorerPath = `/api/v1/projects/${project}/explorers/${explorer}`;
const projectExplorerPath = `/api/v1/projects/${project}/explorers`;

const fakeRequest = ({ origin: requestOrigin = origin, path, method = 'GET', requestId, frame }) => ({
  url: () => `${requestOrigin}${path}`,
  method: () => method,
  headers: () => ({ 'x-request-id': requestId }),
  resourceType: () => 'fetch',
  frame: () => frame,
});

const requestDetails = (request, requestId, overrides = {}) => ({
  requestId,
  browserRequestId: `browser-${requestId}`,
  method: request.method(),
  resourceType: request.resourceType(),
  url: request.url(),
  requestDetails: { requestId },
  startedAt: 1,
  ...overrides,
});

const recordSuccessful = (ledger, request, details, { status = 200, serverRequestId = `server-${details.requestId}` } = {}) => {
  ledger.recordRequest(request, details);
  ledger.recordResponse(request, { status, serverRequestId, observedAt: 2 });
  ledger.recordFinished(request, { observedAt: 3 });
};

const createRequest = (ledger, requestId = 'create-selected') => {
  const request = fakeRequest({ path: projectExplorerPath, method: 'POST', requestId });
  recordSuccessful(ledger, request, requestDetails(request, requestId), { status: 201 });
  return request;
};

test('fixture native request ledger completes the selected Explorer while retaining other Explorer drain evidence', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  createRequest(ledger);

  const selected = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, requestId: 'builder-selected' });
  recordSuccessful(ledger, selected, requestDetails(selected, 'builder-selected'));

  const failedSelected = fakeRequest({ path: `${explorerPath}/authoring/v2/construction-capabilities`, method: 'POST', requestId: 'capability-selected' });
  ledger.recordRequest(failedSelected, requestDetails(failedSelected, 'capability-selected'));
  ledger.recordFailed(failedSelected, { failure: 'net::ERR_ABORTED', observedAt: 4 });

  const otherExplorerRequest = fakeRequest({
    path: `/api/v1/projects/${project}/explorers/other-explorer/authoring/v2/builder`,
    requestId: 'builder-other',
  });
  ledger.recordRequest(otherExplorerRequest, requestDetails(otherExplorerRequest, 'builder-other'));

  const directApiRequest = fakeRequest({
    origin: 'http://127.0.0.1:8188',
    path: `${explorerPath}/authoring/v2/builder`,
    requestId: 'builder-direct-api',
  });
  recordSuccessful(ledger, directApiRequest, requestDetails(directApiRequest, 'builder-direct-api'));

  const startedAt = performance.now();
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 20 });
  const elapsedMs = performance.now() - startedAt;

  assert(elapsedMs >= 10, `flush must drain the project prefix, including an excluded Explorer; elapsed ${elapsedMs}ms`);
  assert(elapsedMs < 1_000, `flush must stop at its bounded deadline; elapsed ${elapsedMs}ms`);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.deepEqual(snapshot.nativeRequests.map(({ requestId }) => requestId), [
    'create-selected', 'builder-selected', 'capability-selected',
  ]);
  assert.equal(snapshot.nativeRequestTerminalLedger.counts.finished, 2);
  assert.equal(snapshot.nativeRequestTerminalLedger.counts.failed, 1);
  assert.equal(snapshot.nativeRequestTerminalLedger.counts.pending, 0);
  assert.deepEqual(snapshot.nativeRequestDrainEvidence, []);
  assert.deepEqual(snapshot.excludedNativeRequests.map(({ requestId }) => requestId), ['builder-other']);
  assert.deepEqual(snapshot.excludedNativeRequestDrainEvidence[0].unresolvedRequests.map(({ requestId }) => requestId), ['builder-other']);
  assert.deepEqual(snapshot.nativeRequestCorrelationErrors, []);
  assert.equal(ledger.snapshotAll()[0].nativeRequestTerminalLedger.complete, true);
});

test('fixture native request ledger keeps an empty selected scope incomplete', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });

  assert.equal(snapshot.nativeRequests.length, 0);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.deepEqual(snapshot.incompleteRequests, []);
});

test('the Basic scope is opened by the production workflow fixture before the test body', () => {
  const fixtureSource = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  const scopeIndex = fixtureSource.indexOf('openBasicFixtureNativeRequestScope(nativeRequestLedger, target)');
  const bodyIndex = fixtureSource.indexOf('await use({ ...loomContext, ...browserDiagnostics, page, check, action, fault, nativeRequestLedger })');
  const finalizerIndex = fixtureSource.indexOf('await finalizeFixtureNativeRequestReport({');

  assert(scopeIndex >= 0, 'the real workflow fixture opens the Basic scope');
  assert(bodyIndex > scopeIndex, 'the default scope exists before the test can perform its first page action');
  assert(finalizerIndex > bodyIndex, 'the real workflow fixture finalizes after the case body');
});

test('automatic Basic fixture scope is limited to fresh isolated verification projects', () => {
  const ledger = createFixtureNativeRequestLedger();
  const target = { kind: 'isolated', fixtureProject: 'loom_dev_verify_basic-run-1', uiUrl: `${origin}/builder` };
  const scope = openBasicFixtureNativeRequestScope(ledger, target);

  assert.equal(scope.project, target.fixtureProject);
  assert.equal(scope.origin, origin);
  assert.equal(scope.automatic, true);
  assert.equal(scope.requireProjectCreate, false,
    'the Basic project and bootstrap Explorer are seeded before Playwright starts');
  assert.equal(openBasicFixtureNativeRequestScope(ledger, {
    ...target, kind: 'owned-cda', fixtureProject: 'loom_dev_cda_fhir',
  }), null, 'the automatic scope must never attach to the shared CDA project');
  assert.equal(openBasicFixtureNativeRequestScope(ledger, {
    ...target, fixtureProject: 'loom_dev_c89a69d7e137',
  }), null, 'the automatic scope must not attach to the named bootstrap project');
});

test('automatic Basic scope finalizes its selected Explorer without a browser create request and retains sibling traffic', async () => {
  const basicProject = 'loom_dev_verify_basic-run-2';
  const basicExplorer = 'selected-basic-explorer';
  const basicExplorerPath = `/api/v1/projects/${basicProject}/explorers/${basicExplorer}`;
  const ledger = createFixtureNativeRequestLedger();
  const scope = openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
  });
  const selected = fakeRequest({ path: `${basicExplorerPath}/authoring/v2/builder`, requestId: 'basic-builder' });
  recordSuccessful(ledger, selected, requestDetails(selected, 'basic-builder'));
  const sibling = fakeRequest({
    path: `/api/v1/projects/${basicProject}/explorers/sibling-basic-explorer/authoring/v2/builder`,
    requestId: 'basic-sibling-builder',
  });
  recordSuccessful(ledger, sibling, requestDetails(sibling, 'basic-sibling-builder'));
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'automatic-basic-scope',
    target: { fixtureProject: basicProject, explorer: basicExplorer }, requiredChecks: [],
  });
  report.target.explorer = basicExplorer;

  const snapshot = await finalizeFixtureNativeRequestReport({
    report, ledger, project: basicProject, explorer: basicExplorer, timeoutMs: 20,
  });

  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.equal(snapshot.nativeRequestTerminalLedger.projectCreateRequestRequired, false);
  assert.deepEqual(report.nativeRequests.map(({ requestId }) => requestId), ['basic-builder']);
  assert.deepEqual(report.excludedNativeRequests.map(({ requestId }) => requestId), ['basic-sibling-builder']);
  assert.equal(report.assertions.find(({ name }) => name ===
    'basic fixture native request ledger observed a complete selected Explorer lifecycle')?.status, 'passed');
});

test('automatic Basic scope fails its fixture check when no selected Explorer request was observed', async () => {
  const basicProject = 'loom_dev_verify_basic-run-3';
  const basicExplorer = 'selected-basic-explorer';
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
  });
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'empty-automatic-basic-scope',
    target: { fixtureProject: basicProject, explorer: basicExplorer }, requiredChecks: [],
  });
  report.target.explorer = basicExplorer;

  const snapshot = await finalizeFixtureNativeRequestReport({
    report, ledger, project: basicProject, explorer: basicExplorer, timeoutMs: 20,
  });
  finishReport(report);

  assert.deepEqual(snapshot.nativeRequests, []);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.assertions.find(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle')?.status, 'failed');
  assert.equal(report.status, 'failed');
});

test('automatic Basic scope selects only its observed bootstrap Explorer and reports the resolved ID', async () => {
  const basicProject = 'loom_dev_verify_basic-run-5';
  const bootstrapExplorer = 'seeded-bootstrap-explorer';
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
    bootstrapExplorerId: bootstrapExplorer,
  });
  const request = fakeRequest({
    path: `/api/v1/projects/${basicProject}/explorers/${bootstrapExplorer}/authoring/v2/builder`,
    requestId: 'bootstrap-builder',
  });
  recordSuccessful(ledger, request, requestDetails(request, 'bootstrap-builder'));
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'automatic-bootstrap-fallback',
    target: { fixtureProject: basicProject }, requiredChecks: [],
  });

  const snapshot = await finalizeFixtureNativeRequestReport({ report, ledger, project: basicProject, timeoutMs: 20 });

  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, bootstrapExplorer);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorerSelection, 'selected');
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
  assert.equal(report.assertions.find(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle')?.evidence.explorer,
  bootstrapExplorer);
});

test('automatic Basic scope keeps multiple observed Explorers ambiguous instead of guessing the bootstrap', async () => {
  const basicProject = 'loom_dev_verify_basic-run-6';
  const bootstrapExplorer = 'seeded-bootstrap-explorer';
  const otherExplorer = 'second-observed-explorer';
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
    bootstrapExplorerId: bootstrapExplorer,
  });
  for (const id of [bootstrapExplorer, otherExplorer]) {
    const request = fakeRequest({
      path: `/api/v1/projects/${basicProject}/explorers/${id}/authoring/v2/builder`,
      requestId: `builder-${id}`,
    });
    recordSuccessful(ledger, request, requestDetails(request, `builder-${id}`));
  }
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'automatic-ambiguous-explorers',
    target: { fixtureProject: basicProject }, requiredChecks: [],
  });

  const snapshot = await finalizeFixtureNativeRequestReport({ report, ledger, project: basicProject, timeoutMs: 20 });

  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, null);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorerSelection, 'ambiguous');
  assert.equal(snapshot.nativeRequestTerminalLedger.observedExplorerCount, 2);
  assert.equal(snapshot.nativeRequestTerminalLedger.applicable, true);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.assertions.find(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle')?.status, 'failed');
});

test('automatic Basic finalization rejects pending requests and ignores wrong-origin and wrong-project lookalikes', async () => {
  const basicProject = 'loom_dev_verify_basic-run-7';
  const selectedExplorer = 'selected-basic-explorer';
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
    bootstrapExplorerId: selectedExplorer,
  });
  const pending = fakeRequest({
    path: `/api/v1/projects/${basicProject}/explorers/${selectedExplorer}/authoring/v2/construction-capabilities`,
    requestId: 'pending-capabilities',
  });
  ledger.recordRequest(pending, requestDetails(pending, 'pending-capabilities'));
  const wrongOrigin = fakeRequest({
    origin: 'http://127.0.0.1:8188',
    path: `/api/v1/projects/${basicProject}/explorers/${selectedExplorer}/authoring/v2/builder`,
    requestId: 'wrong-origin-builder',
  });
  recordSuccessful(ledger, wrongOrigin, requestDetails(wrongOrigin, 'wrong-origin-builder'));
  const wrongProject = fakeRequest({
    path: `/api/v1/projects/loom_dev_verify_other-run/explorers/${selectedExplorer}/authoring/v2/builder`,
    requestId: 'wrong-project-builder',
  });
  recordSuccessful(ledger, wrongProject, requestDetails(wrongProject, 'wrong-project-builder'));
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'automatic-pending-and-scope-negatives',
    target: { fixtureProject: basicProject, explorer: selectedExplorer }, requiredChecks: [],
  });
  report.target.explorer = selectedExplorer;

  const snapshot = await finalizeFixtureNativeRequestReport({ report, ledger, project: basicProject, timeoutMs: 20 });

  assert.deepEqual(snapshot.nativeRequests.map(({ requestId }) => requestId), ['pending-capabilities']);
  assert.equal(snapshot.nativeRequestTerminalLedger.counts.pending, 1);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(snapshot.nativeRequestDrainEvidence[0].unresolvedRequests[0].requestId, 'pending-capabilities');
  assert.equal(report.errors.some(error => error.requestId === 'wrong-origin-builder' ||
    error.requestId === 'wrong-project-builder'), false);
  assert.equal(report.assertions.find(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle')?.status, 'failed');
});

test('explicit strict finalization still requires the project Explorer create request', async () => {
  const strictProject = 'loom_dev_verify_strict-run-1';
  const strictExplorer = 'strict-selected-explorer';
  const path = `/api/v1/projects/${strictProject}/explorers/${strictExplorer}`;
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project: strictProject, origin });
  const builder = fakeRequest({ path: `${path}/authoring/v2/builder`, requestId: 'strict-builder-without-create' });
  recordSuccessful(ledger, builder, requestDetails(builder, 'strict-builder-without-create'));
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'explicit-scope-missing-create',
    target: { fixtureProject: strictProject, explorer: strictExplorer }, requiredChecks: [],
  });
  report.target.explorer = strictExplorer;

  const snapshot = await finalizeFixtureNativeRequestReport({ report, ledger, project: strictProject, timeoutMs: 20 });

  assert.equal(snapshot.nativeRequestTerminalLedger.automaticScope, false);
  assert.equal(snapshot.nativeRequestTerminalLedger.projectCreateRequestRequired, true);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, strictExplorer);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(scope.requireProjectCreate, true);
  assert.equal(report.assertions.some(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle'), false);
});

test('automatic Basic scope with no Explorer traffic stays explicitly not applicable', async () => {
  const basicProject = 'loom_dev_verify_basic-run-8';
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
  });
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'non-builder-basic-workflow',
    target: { fixtureProject: basicProject }, requiredChecks: [],
  });

  const snapshot = await finalizeFixtureNativeRequestReport({ report, ledger, project: basicProject, timeoutMs: 20 });

  assert.equal(snapshot.nativeRequestTerminalLedger.explorer, null);
  assert.equal(snapshot.nativeRequestTerminalLedger.explorerSelection, 'not-observed');
  assert.equal(snapshot.nativeRequestTerminalLedger.applicable, false);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(report.assertions.some(({ name }) =>
    name === 'basic fixture native request ledger observed a complete selected Explorer lifecycle'), false);
});

test('explicit strict scope takes precedence over the automatic Basic scope', async () => {
  const basicProject = 'loom_dev_verify_basic-run-4';
  const basicExplorer = 'selected-basic-explorer';
  const path = `/api/v1/projects/${basicProject}/explorers/${basicExplorer}`;
  const ledger = createFixtureNativeRequestLedger();
  openBasicFixtureNativeRequestScope(ledger, {
    kind: 'isolated', fixtureProject: basicProject, uiUrl: `${origin}/builder`,
  });
  const explicit = ledger.openScope({ project: basicProject, origin });
  const create = fakeRequest({ path: `/api/v1/projects/${basicProject}/explorers`, method: 'POST', requestId: 'basic-create' });
  recordSuccessful(ledger, create, requestDetails(create, 'basic-create'), { status: 201 });
  const builder = fakeRequest({ path: `${path}/authoring/v2/builder`, requestId: 'explicit-builder' });
  recordSuccessful(ledger, builder, requestDetails(builder, 'explicit-builder'));
  const report = createReport({
    scenario: 'fixture-ledger-test', caseName: 'explicit-basic-scope',
    target: { fixtureProject: basicProject, explorer: basicExplorer }, requiredChecks: [],
  });
  report.target.explorer = basicExplorer;

  const snapshot = await finalizeFixtureNativeRequestReport({
    report, ledger, project: basicProject, explorer: basicExplorer, timeoutMs: 20,
  });

  assert.equal(explicit.automatic, false);
  assert.equal(snapshot.nativeRequestTerminalLedger.automaticScope, false);
  assert.equal(snapshot.nativeRequestTerminalLedger.projectCreateRequestRequired, true);
  assert.deepEqual(report.nativeRequests.map(({ requestId }) => requestId), ['basic-create', 'explicit-builder']);
  assert.equal(new Set(report.nativeRequests.map(({ browserRequestId }) => browserRequestId)).size, 2,
    'the shared Request ledger stores each browser Request exactly once');
  assert.equal(report.assertions.some(({ name }) => name ===
    'basic fixture native request ledger observed a complete selected Explorer lifecycle'), false,
    'explicit scopes keep their existing caller-owned completion check');
});

test('native request and navigation capture share exact Frame identity while pending stays fatal', async () => {
  const mainFrame = {};
  const childFrame = {};
  const page = { mainFrame: () => mainFrame };
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  const request = fakeRequest({
    path: `${explorerPath}/authoring/v2/semantic-inventory`,
    requestId: 'semantic-inventory-pending',
    frame: mainFrame,
  });
  const requestFrame = ledger.frameIdentityForRequest(request, page);
  const sameFrameNavigation = ledger.frameIdentityForFrame(mainFrame, page);
  const otherFrameNavigation = ledger.frameIdentityForFrame(childFrame, page);
  const unavailableFrameRequest = ledger.frameIdentityForRequest({ frame: () => { throw new Error('service-worker request'); } }, page);

  assert.equal(requestFrame.frameIdentityStatus, 'exact');
  assert.equal(requestFrame.frameIsMainFrame, true);
  assert.equal(requestFrame.frameId, sameFrameNavigation.frameId,
    'the same Playwright Frame object must join a request to its later navigation record');
  assert.notEqual(requestFrame.frameId, otherFrameNavigation.frameId,
    'a different Frame object must not inherit the request owner identity');
  assert.equal(unavailableFrameRequest.frameIdentityStatus, 'unavailable');
  assert.equal(unavailableFrameRequest.frameId, null,
    'requests without an exact Playwright Frame must remain explicitly unlinked');

  createRequest(ledger);
  ledger.recordRequest(request, {
    ...requestDetails(request, 'semantic-inventory-pending'),
    ...requestFrame,
    navigationSequenceAtStart: 11,
  });
  const report = createReport({
    scenario: 'fixture-ledger-test',
    caseName: 'same-frame-navigation-diagnostic',
    target: { fixtureProject: project },
    requiredChecks: [],
  });
  report.navigationTimings = [
    { id: 'navigation-12', sequence: 12, phase: 'request-start', ...sameFrameNavigation },
    { id: 'navigation-13', sequence: 13, phase: 'frame-navigated', ...sameFrameNavigation },
    { id: 'navigation-14', sequence: 14, phase: 'frame-navigated', ...otherFrameNavigation },
  ];
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });
  await finalizeFixtureNativeRequestReport({ report, ledger, project });
  const pending = snapshot.nativeRequests.find(entry => entry.requestId === 'semantic-inventory-pending');
  const terminal = snapshot.nativeRequestTerminalLedger.requests.find(entry => entry.requestId === 'semantic-inventory-pending');
  const projected = report.nativeRequests.find(entry => entry.requestId === 'semantic-inventory-pending');
  const projectedTerminal = report.nativeRequestTerminalLedger.requests.find(entry => entry.requestId === 'semantic-inventory-pending');

  assert.equal(pending.pageId, 'playwright-page-1');
  assert.equal(pending.frameId, sameFrameNavigation.frameId);
  assert.equal(terminal.frameId, sameFrameNavigation.frameId);
  assert.equal(terminal.frameIsMainFrame, true);
  assert.equal(terminal.navigationSequenceAtStart, 11);
  assert.equal(terminal.state, 'pending');
  assert.equal(terminal.terminalEvent, null);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false,
    'same-frame navigation correlation is diagnostic evidence, not a terminal-event waiver');
  assert.deepEqual(projected.sameFrameNavigationEventsAfterStart, [
    { id: 'navigation-12', sequence: 12, phase: 'request-start' },
    { id: 'navigation-13', sequence: 13, phase: 'frame-navigated' },
  ], 'the actual report finalizer must link only later navigation events from the exact same Frame');
  assert.deepEqual(projectedTerminal.sameFrameNavigationEventsAfterStart, projected.sameFrameNavigationEventsAfterStart,
    'the same-frame chronology must reach the terminal-ledger view');
  assert.equal(projectedTerminal.state, 'pending');
  assert.equal(report.nativeRequestTerminalLedger.complete, false,
    'the report finalizer must not infer terminality from a later same-frame navigation');
});

test('same-URL Request objects cannot borrow the recorded request terminal state', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  createRequest(ledger);

  const original = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, requestId: 'same-request-id' });
  ledger.recordRequest(original, requestDetails(original, 'same-request-id', { browserRequestId: 'same-browser-id' }));
  const differentObject = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, requestId: 'same-request-id' });
  assert.equal(ledger.linkDiagnostic(differentObject, { expected: true, injectedFault: true }), false,
    'a same-URL object cannot inherit policy from a different Request object');
  ledger.recordResponse(differentObject, { status: 200, serverRequestId: 'same-server-id', observedAt: 2 });
  ledger.recordFinished(differentObject, { observedAt: 3 });

  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });
  const originalRecord = snapshot.nativeRequests.find(({ requestId }) => requestId === 'same-request-id');
  assert.equal(originalRecord.status, undefined, 'the uncorrelated response cannot add a status to the captured request');
  const originalTerminalRecord = snapshot.nativeRequestTerminalLedger.requests.find(({ requestId }) => requestId === 'same-request-id');
  assert.equal(originalTerminalRecord.status, null);
  assert.equal(originalTerminalRecord.terminalEvent, null);
  assert.equal(originalTerminalRecord.state, 'pending');
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(snapshot.incompleteRequests.length, 1);
  assert.deepEqual(snapshot.nativeRequestCorrelationErrors.map(({ event }) => event), ['diagnostic', 'response', 'requestfinished']);
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false,
    'the retained request-correlation failures make the owned ledger fatal');
});

test('linked diagnostics reflect fixture classification mutations made after linking', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  createRequest(ledger);

  const request = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, method: 'POST', requestId: 'validation-422' });
  ledger.recordRequest(request, requestDetails(request, 'validation-422'));
  ledger.recordResponse(request, { status: 422, serverRequestId: 'server-validation-422', observedAt: 2 });
  ledger.recordFinished(request, { observedAt: 3 });
  const diagnostic = {
    kind: 'network',
    status: 422,
    method: 'POST',
    url: request.url(),
    requestId: 'validation-422',
    browserRequestId: 'browser-validation-422',
  };
  assert.equal(ledger.linkDiagnostic(request, diagnostic), true);

  const expectedHttpFailure = { reason: 'the exact fixture validation was expected', proof: { action: 'submit-invalid-choice' } };
  const expectedInjectedFault = { reason: 'exact fixture fault request', proof: { faultId: 'validation-422' } };
  diagnostic.expected = true;
  diagnostic.expectedHttpFailure = expectedHttpFailure;
  diagnostic.expectedInjectedFault = expectedInjectedFault;
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });
  const captured = snapshot.nativeRequests.find(({ requestId }) => requestId === 'validation-422');
  assert.equal(captured.expected, true);
  assert.deepEqual(captured.expectedHttpFailure, expectedHttpFailure);
  const terminalCapture = snapshot.nativeRequestTerminalLedger.requests.find(({ requestId }) => requestId === 'validation-422');
  assert.equal(terminalCapture.expected, true);
  assert.deepEqual(terminalCapture.expectedHttpFailure, expectedHttpFailure,
    'the terminal view receives the same final Request-linked fixture classification');
  assert.deepEqual(terminalCapture.expectedInjectedFault, expectedInjectedFault);
});

test('a completed HTTP 500 remains fatal under the existing fixture report classifier', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  createRequest(ledger);

  const request = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, method: 'POST', requestId: 'builder-http-500' });
  ledger.recordRequest(request, requestDetails(request, 'builder-http-500'));
  ledger.recordResponse(request, { status: 500, serverRequestId: 'server-builder-http-500', observedAt: 2 });
  ledger.recordFinished(request, { observedAt: 3 });
  const diagnostic = {
    kind: 'network',
    status: 500,
    method: 'POST',
    url: request.url(),
    requestId: 'builder-http-500',
    playwrightRequestId: 'cda-builder-http-500',
    browserRequestId: 'browser-builder-http-500',
  };
  ledger.linkDiagnostic(request, diagnostic);
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });

  const report = {
    network: [diagnostic],
  };
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true, 'terminal capture completeness does not declare an HTTP failure successful');
  const captured = snapshot.nativeRequests.find(({ requestId }) => requestId === 'builder-http-500');
  assert.equal(captured.terminalEvent, 'requestfinished');
  assert.equal(captured.status, 500);
  assert.equal(classifyNetworkRecord(report.network[0]), 'unexpected-error',
    'the existing report classifier keeps a completed HTTP 500 fatal');
});

test('fixture policy projection links exact callback Requests into the finalized report gate', async () => {
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  createRequest(ledger);

  const expected422 = fakeRequest({ path: `${explorerPath}/authoring/v2/builder/validate`, method: 'POST', requestId: 'validation-422' });
  ledger.recordRequest(expected422, requestDetails(expected422, 'validation-422'));
  ledger.recordResponse(expected422, { status: 422, serverRequestId: 'server-validation-422', observedAt: 2 });
  ledger.recordFinished(expected422, { observedAt: 3 });
  const expectedDiagnostic = {
    kind: 'network', status: 422, method: 'POST', url: expected422.url(), rawURL: expected422.url(),
    requestId: 'validation-422', playwrightRequestId: 'browser-validation-422',
  };
  ledger.associateDiagnostic(expected422, expectedDiagnostic);

  const fatal500 = fakeRequest({ path: `${explorerPath}/authoring/v2/builder`, method: 'POST', requestId: 'builder-500' });
  ledger.recordRequest(fatal500, requestDetails(fatal500, 'builder-500'));
  ledger.recordResponse(fatal500, { status: 500, serverRequestId: 'server-builder-500', observedAt: 4 });
  ledger.recordFinished(fatal500, { observedAt: 5 });
  const fatalDiagnostic = {
    kind: 'network', status: 500, method: 'POST', url: fatal500.url(), rawURL: fatal500.url(),
    requestId: 'builder-500', playwrightRequestId: 'browser-builder-500',
  };
  ledger.associateDiagnostic(fatal500, fatalDiagnostic);

  const report = createReport({
    scenario: 'fixture-ledger-test',
    caseName: 'fixture-policy-projection',
    target: { fixtureProject: project },
    requiredChecks: ['controlled validation reached'],
  });
  report.network = [expectedDiagnostic, fatalDiagnostic];
  const fault = {
    id: 'intentional-validation-422',
    matched: true,
    playwrightRequestId: 'browser-validation-422',
    method: 'POST',
    rawURL: expected422.url(),
    action: 'fulfill',
    responseStatus: 422,
  };

  // This is the production fixture sequence: apply policy to callback diagnostics,
  // attach results to those same Requests, then serialize the owned ledger.
  projectFixtureNetworkDiagnostics({ report, ledger, faults: [fault] });
  const snapshot = await ledger.flush(scope, { explorer, timeoutMs: 10 });
  await finalizeFixtureNativeRequestReport({ report, ledger, project });
  recordCheck(report, 'correctness', 'controlled validation reached', true);
  finishReport(report);

  const requestById = new Map(report.nativeRequests.map(entry => [entry.requestId, entry]));
  assert.equal(report.nativeRequestTerminalLedger.complete, true);
  assert.equal(requestById.get('validation-422').injectedFault, true);
  assert.equal(requestById.get('validation-422').injectedRequestId, 'intentional-validation-422');
  assert.equal(requestById.get('validation-422').injectedStatus, 422);
  assert.equal(requestById.get('builder-500').injectedFault, undefined,
    'the adjacent fatal Request cannot inherit the expected fault policy');
  const terminal422 = report.nativeRequestTerminalLedger.requests.find(entry => entry.requestId === 'validation-422');
  assert.equal(terminal422.injectedFault, true);
  assert.equal(terminal422.injectedRequestId, 'intentional-validation-422');
  assert.equal(classifyNetworkRecord(report.network[0]), 'expected-injected');
  assert.equal(classifyNetworkRecord(report.network[1]), 'unexpected-error');
  assert.equal(report.status, 'failed', 'the actual finalized report gate still rejects an unrelated completed HTTP 500');
  assert.equal(snapshot.nativeRequestTerminalLedger.complete, true);
});
