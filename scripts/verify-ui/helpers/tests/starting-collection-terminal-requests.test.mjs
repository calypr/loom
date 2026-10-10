import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { waitForStartingCollectionConfigurationRequests } from '../../workflows/verify-cda-starting-collection-handoff.mjs';

const origin = 'http://127.0.0.1:30008';
const apiOrigin = 'http://127.0.0.1:8188';
const explorerPath = '/api/v1/projects/owned/explorers/explorer-1';
const selectionId = 'selection-revision-1';
const outputId = 'output-1';
const snapshotToken = `sha256:${'a'.repeat(64)}`;
const selectionPath = `${explorerPath}/selections/${selectionId}`;
const rowDefinitionChoicesPath = `${explorerPath}/authoring/v2/row-definition-choices`;
const populationRoutesPath = `${explorerPath}/authoring/v2/population-routes`;

const makeRequest = (path, method, requestId, { query = '', body } = {}) => ({
  url: () => `${origin}${path}${query}`,
  method: () => method,
  headers: () => ({ 'x-request-id': requestId }),
  postData: () => method === 'POST' ? JSON.stringify(body ?? {}) : null,
  failure: () => null,
});

const makeResponse = (request, status, body) => ({
  request: () => request,
  status: () => status,
  headers: () => ({}),
  text: body,
});

const makeCapture = () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  let currentAction = 'Configure rows';
  const capture = captureCDARequests(page, {
    apiOrigin,
    browserRequestOrigin: origin,
    appOrigins: [apiOrigin, origin],
    ownedPathPrefix: explorerPath,
    report,
    currentAction: () => currentAction,
    responsePaths: /selections|row-definition-choices|population-routes/,
  });
  return { page, report, capture, setCurrentAction: value => { currentAction = value; } };
};

test('each starting-collection opener declares its native state and keeps terminal reads in the action budget', async () => {
  const source = await readFile(new URL('../../workflows/verify-cda-starting-collection-handoff.mjs', import.meta.url), 'utf8');
  const openStart = source.indexOf('const openStartingCollection = async');
  const openEnd = source.indexOf('\n  try {', openStart);
  const openAction = source.slice(openStart, openEnd);
  const started = openAction.indexOf('const configureRowsStartedAt = Date.now();');
  const requestSnapshot = openAction.indexOf('const configureRowsRequestFromIndex = cda.report.nativeRequests.length;');
  const action = openAction.indexOf("await cda.action('Configure rows'");
  const panelReady = openAction.indexOf("await wait(([id]) =>", action);
  const requestCompletion = openAction.indexOf('await waitForStartingCollectionConfigurationRequests({', panelReady);
  const elapsed = openAction.indexOf('const elapsedMs = Date.now() - configureRowsStartedAt;', requestCompletion);
  assert(openStart >= 0 && openEnd > openStart, 'the native starting-collection opener must remain an explicit workflow action');
  assert(started >= 0 && started < action, 'the request deadline must begin before the Configure rows click');
  assert(requestSnapshot > started && requestSnapshot < action,
    'the request ledger boundary must be snapshotted before the click');
  assert(action < panelReady && panelReady < requestCompletion && requestCompletion < elapsed,
    'the native action must await panel readiness and exact captured request completion before recording its duration');
  assert.match(openAction.slice(action), /after:\s*async\s*\(\)\s*=>\s*\{/,
    'both waits must stay in the existing cda.action after phase');
  const initialOpen = source.indexOf("await openStartingCollection(selection.id, outputSelector, 'unattached-handoff');");
  const restoredOpen = source.indexOf("await openStartingCollection(selection.id, outputSelector, 'saved-attachment-after-reload');");
  assert(initialOpen >= 0 && restoredOpen > initialOpen,
    'the initial and restored collection opens must declare their distinct native states explicitly');
  assert(openAction.includes('snapshotToken: builder.catalog.snapshotToken'),
    'both request variants must be bound to the exact saved draft snapshot');
});

test('the unattached handoff accepts its captured route-choice and route request shape', async () => {
  const { page, report, capture, setCurrentAction } = makeCapture();
  const startedAt = Date.now();
  setCurrentAction(undefined);
  const initialSelectionRequest = makeRequest(selectionPath, 'GET', 'initial-selection-get', { query: '?limit=1' });
  page.emit('request', initialSelectionRequest);
  page.emit('response', makeResponse(initialSelectionRequest, 200,
    async () => '{"revision":{"id":"selection-revision-1","memberCount":2},"members":[{}]}'));
  await capture.waitFor(entry => entry.requestId === 'initial-selection-get' && entry.completedAt);

  const fromIndex = report.nativeRequests.length;
  setCurrentAction('Configure rows');
  const waiting = waitForStartingCollectionConfigurationRequests({
    browserEvents: capture,
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase: 'unattached-handoff',
    fromIndex,
    startedAt,
  });
  let resolved = false;
  waiting.then(() => { resolved = true; });
  setCurrentAction('Unrelated native action');
  const wrongActionChoicesRequest = makeRequest(rowDefinitionChoicesPath, 'GET', 'wrong-route-choice-action', {
    query: `?outputId=${outputId}&snapshotToken=${encodeURIComponent(snapshotToken)}`,
  });
  page.emit('request', wrongActionChoicesRequest);
  page.emit('response', makeResponse(wrongActionChoicesRequest, 200, async () => '{"choices":[]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'an exact route-choice request under a different native action must not satisfy Configure rows');
  setCurrentAction('Configure rows');
  const wrongSnapshotChoicesRequest = makeRequest(rowDefinitionChoicesPath, 'GET', 'wrong-route-choice-snapshot', {
    query: `?outputId=${outputId}&snapshotToken=sha256%3Awrong-snapshot`,
  });
  page.emit('request', wrongSnapshotChoicesRequest);
  page.emit('response', makeResponse(wrongSnapshotChoicesRequest, 200, async () => '{"choices":[]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'route choices for a different saved draft must not satisfy the unattached handoff');
  const routeChoicesRequest = makeRequest(rowDefinitionChoicesPath, 'GET', 'row-definition-choices-get', {
    query: `?outputId=${outputId}&snapshotToken=${encodeURIComponent(snapshotToken)}`,
  });
  page.emit('request', routeChoicesRequest);
  let finishRouteChoicesBody;
  page.emit('response', makeResponse(routeChoicesRequest, 200, () => new Promise(resolve => {
    finishRouteChoicesBody = resolve;
  })));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(report.nativeRequests.find(entry => entry.requestId === 'row-definition-choices-get').completedAt, undefined,
    'response headers alone must not satisfy the initial route-choice read');

  const routesRequest = makeRequest(populationRoutesPath, 'POST', 'population-routes-post', {
    body: { snapshotToken, selectionRevisionId: selectionId, outputId, limit: 50 },
  });
  page.emit('request', routesRequest);
  let finishRoutesBody;
  page.emit('response', makeResponse(routesRequest, 200, () => new Promise(resolve => {
    finishRoutesBody = resolve;
  })));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(report.nativeRequests.find(entry => entry.requestId === 'population-routes-post').completedAt, undefined);
  const wrongSelectionRoutesRequest = makeRequest(populationRoutesPath, 'POST', 'wrong-population-routes-selection', {
    body: { snapshotToken, selectionRevisionId: 'another-selection', outputId, limit: 50 },
  });
  page.emit('request', wrongSelectionRoutesRequest);
  page.emit('response', makeResponse(wrongSelectionRoutesRequest, 200, async () => '{"choices":[]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'a population route for a different selection must not satisfy the unattached handoff');
  finishRouteChoicesBody('{"choices":[]}');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(report.nativeRequests.find(entry => entry.requestId === 'row-definition-choices-get').completedAt !== undefined, true);
  finishRoutesBody(JSON.stringify({ snapshotToken, outputId, selectionRevisionId: selectionId, choices: [] }));
  const result = await waiting;
  await capture.flush();

  assert.equal(result.phase, 'unattached-handoff');
  assert.equal(result.phaseRequest, report.nativeRequests.find(entry => entry.requestId === 'row-definition-choices-get'));
  assert.equal(result.populationRoutes, report.nativeRequests.find(entry => entry.requestId === 'population-routes-post'));
  assert.equal(result.deadline, startedAt + 5_000);
  assert.deepEqual(capture.rawResponseBody(result.phaseRequest), { choices: [] });
  assert.deepEqual(capture.rawResponseBody(result.populationRoutes), {
    snapshotToken,
    outputId,
    selectionRevisionId: selectionId,
    choices: [],
  });
  assert(report.nativeRequests.every(entry => Number.isFinite(entry.completedAt) && entry.status === 200));
  assert.deepEqual(report.errors, []);
});

test('the restored attached state waits for its exact full-page selection read and population route', async () => {
  const { page, report, capture, setCurrentAction } = makeCapture();
  const startedAt = Date.now();
  const waiting = waitForStartingCollectionConfigurationRequests({
    browserEvents: capture,
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase: 'saved-attachment-after-reload',
    fromIndex: 0,
    startedAt,
  });
  let resolved = false;
  waiting.then(() => { resolved = true; });
  setCurrentAction('Unrelated native action');
  const wrongActionSelectionRequest = makeRequest(selectionPath, 'GET', 'wrong-restored-selection-action', { query: '?limit=100' });
  page.emit('request', wrongActionSelectionRequest);
  page.emit('response', makeResponse(wrongActionSelectionRequest, 200,
    async () => '{"revision":{"id":"selection-revision-1","memberCount":2},"members":[{},{}]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'a full-page selection read under a different action must not satisfy the restored panel');
  setCurrentAction('Configure rows');
  const wrongLimitSelectionRequest = makeRequest(selectionPath, 'GET', 'wrong-restored-selection-limit', { query: '?limit=1' });
  page.emit('request', wrongLimitSelectionRequest);
  page.emit('response', makeResponse(wrongLimitSelectionRequest, 200,
    async () => '{"revision":{"id":"selection-revision-1","memberCount":2},"members":[{}]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'the first-page handoff read must not stand in for the restored full-page read');
  const selectionRequest = makeRequest(selectionPath, 'GET', 'restored-selection-get', { query: '?limit=100' });
  page.emit('request', selectionRequest);
  let finishSelectionBody;
  page.emit('response', makeResponse(selectionRequest, 200, () => new Promise(resolve => {
    finishSelectionBody = resolve;
  })));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(report.nativeRequests.find(entry => entry.requestId === 'restored-selection-get').completedAt, undefined,
    'selection response headers alone must not count as terminal response-body capture');
  finishSelectionBody('{"revision":{"id":"selection-revision-1","memberCount":2},"members":[{},{}]}');
  await new Promise(resolve => setImmediate(resolve));

  const routesRequest = makeRequest(populationRoutesPath, 'POST', 'restored-population-routes-post', {
    body: { snapshotToken, selectionRevisionId: selectionId, outputId, limit: 50 },
  });
  page.emit('request', routesRequest);
  page.emit('response', makeResponse(routesRequest, 200, async () => JSON.stringify({
    snapshotToken, outputId, selectionRevisionId: selectionId, choices: [],
  })));
  const result = await waiting;
  await capture.flush();

  assert.equal(result.phase, 'saved-attachment-after-reload');
  assert.equal(result.phaseRequest, report.nativeRequests.find(entry => entry.requestId === 'restored-selection-get'));
  assert.equal(result.populationRoutes, report.nativeRequests.find(entry => entry.requestId === 'restored-population-routes-post'));
  assert.equal(capture.rawResponseBody(result.phaseRequest).revision.id, selectionId);
  assert.equal(capture.rawResponseBody(result.phaseRequest).members.length, 2);
  assert.deepEqual(report.errors, []);
});

test('unknown starting-collection request phases fail closed', async () => {
  await assert.rejects(waitForStartingCollectionConfigurationRequests({
    browserEvents: { waitFor: () => assert.fail('unknown phases must not start request matching') },
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase: 'unknown-reopen-state',
    fromIndex: 0,
    startedAt: Date.now(),
  }), /Unknown starting-collection configuration phase/);
});

test('Configure rows spends one original deadline across the restored state exact requests', async () => {
  const startedAt = 10_000;
  const calls = [];
  let clock = startedAt;
  const entries = [
    { method: 'GET', path: selectionPath, startedAt, completedAt: startedAt + 100, status: 200 },
    {
      method: 'POST', path: populationRoutesPath, startedAt: startedAt + 4_900,
      completedAt: startedAt + 5_001, status: 200,
    },
  ];
  entries[0].query = { limit: '100' };
  entries[0].triggerAction = 'Configure rows';
  entries[1].body = { snapshotToken, selectionRevisionId: selectionId, outputId };
  entries[1].triggerAction = 'Configure rows';
  const browserEvents = {
    waitFor(predicate, options) {
      calls.push(options);
      const entry = entries[calls.length - 1];
      assert(predicate(entry));
      clock += 4_900;
      return Promise.resolve(entry);
    },
  };

  await assert.rejects(waitForStartingCollectionConfigurationRequests({
    browserEvents,
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase: 'saved-attachment-after-reload',
    fromIndex: 3,
    startedAt,
    now: () => clock,
  }), /population-routes POST did not reach captured terminal completion before its 5000 ms deadline/);
  assert.deepEqual(calls.map(call => call.timeoutMs), [5_000, 100],
    'the second exact request receives only the remainder of the original deadline');
  assert(calls.every(call => call.fromIndex === 3),
    'both requests remain scoped to events recorded after the Configure rows click');
});

test('a terminal failed population-routes request remains fatal in restored attached state', async () => {
  const { page, report, capture } = makeCapture();
  const startedAt = Date.now();
  const waiting = waitForStartingCollectionConfigurationRequests({
    browserEvents: capture,
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase: 'saved-attachment-after-reload',
    fromIndex: 0,
    startedAt,
  });

  const selectionRequest = makeRequest(selectionPath, 'GET', 'selection-get', { query: '?limit=100' });
  page.emit('request', selectionRequest);
  page.emit('response', makeResponse(selectionRequest, 200, async () => '{"revision":{"id":"selection-revision-1"}}'));
  await capture.waitFor(entry => entry.path === selectionPath && entry.status === 200);

  const routesRequest = makeRequest(populationRoutesPath, 'POST', 'population-routes-post', {
    body: { snapshotToken, selectionRevisionId: selectionId, outputId },
  });
  page.emit('request', routesRequest);
  page.emit('response', makeResponse(routesRequest, 503, async () => '{"error":"unavailable"}'));
  await assert.rejects(waiting, /population-routes POST returned HTTP 503/);
  await capture.flush();

  assert.equal(report.nativeRequests[1].status, 503);
  assert.equal(Number.isFinite(report.nativeRequests[1].completedAt), true);
  assert.equal(report.errors.some(error => error.kind === 'http' && error.status === 503), true);
});
