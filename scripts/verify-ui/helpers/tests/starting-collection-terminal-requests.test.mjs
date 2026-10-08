import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { waitForStartingCollectionConfigurationRequests } from '../../workflows/verify-cda-starting-collection-handoff.mjs';

const origin = 'http://127.0.0.1:8188';
const explorerPath = '/api/v1/projects/owned/explorers/explorer-1';
const selectionId = 'selection-revision-1';
const outputId = 'output-1';
const selectionPath = `${explorerPath}/selections/${selectionId}`;
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
    apiOrigin: origin,
    ownedPathPrefix: explorerPath,
    report,
    currentAction: () => currentAction,
    responsePaths: /selections|population-routes/,
  });
  return { page, report, capture, setCurrentAction: value => { currentAction = value; } };
};

test('the native Configure rows action keeps panel readiness and request capture in its original budget', async () => {
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
});

test('Configure rows waits for exact selection and population-route response bodies before returning', async () => {
  const { page, report, capture, setCurrentAction } = makeCapture();
  const startedAt = Date.now();
  const waiting = waitForStartingCollectionConfigurationRequests({
    browserEvents: capture,
    explorerPath,
    selectionId,
    outputId,
    fromIndex: report.nativeRequests.length,
    startedAt,
  });
  let resolved = false;
  waiting.then(() => { resolved = true; });

  setCurrentAction('Unrelated native action');
  const wrongActionRequest = makeRequest(selectionPath, 'GET', 'wrong-selection-action', { query: '?limit=100' });
  page.emit('request', wrongActionRequest);
  page.emit('response', makeResponse(wrongActionRequest, 200, async () => '{"revision":{"id":"selection-revision-1"}}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'an exact endpoint completed under another native action must not satisfy Configure rows');
  setCurrentAction('Configure rows');

  for (const wrongSelection of [
    makeRequest(`${explorerPath}/selections/another-revision`, 'GET', 'wrong-selection-revision', { query: '?limit=100' }),
    makeRequest(selectionPath, 'POST', 'wrong-selection-method', {
      query: '?limit=100', body: { selectionRevisionId: selectionId, outputId },
    }),
    makeRequest(selectionPath, 'GET', 'wrong-selection-limit', { query: '?limit=1' }),
  ]) {
    page.emit('request', wrongSelection);
    page.emit('response', makeResponse(wrongSelection, 200, async () => '{"revision":{"id":"selection-revision-1"}}'));
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(resolved, false,
      'another revision, method, or page size must not satisfy the exact Configure rows selection read');
  }

  const selectionRequest = makeRequest(selectionPath, 'GET', 'selection-get', { query: '?limit=100' });
  page.emit('request', selectionRequest);
  let finishSelectionBody;
  page.emit('response', makeResponse(selectionRequest, 200, () => new Promise(resolve => {
    finishSelectionBody = resolve;
  })));
  await new Promise(resolve => setImmediate(resolve));
  const capturedSelection = report.nativeRequests.find(entry => entry.requestId === 'selection-get');
  assert.equal(capturedSelection.status, 200);
  assert.equal(capturedSelection.completedAt, undefined,
    'response headers alone must not count as captured terminal evidence');
  assert.equal(resolved, false);

  finishSelectionBody('{"revision":{"id":"selection-revision-1"}}');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false,
    'the first endpoint alone must not let Configure rows return before the population-routes request');

  const wrongRevisionRoutesRequest = makeRequest(populationRoutesPath, 'POST', 'wrong-population-routes-revision', {
    body: { selectionRevisionId: 'another-selection', outputId },
  });
  page.emit('request', wrongRevisionRoutesRequest);
  page.emit('response', makeResponse(wrongRevisionRoutesRequest, 200, async () => '{"routes":[]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'a population-route response for another selection must not satisfy the action');
  const wrongOutputRoutesRequest = makeRequest(populationRoutesPath, 'POST', 'wrong-population-routes-output', {
    body: { selectionRevisionId: selectionId, outputId: 'another-output' },
  });
  page.emit('request', wrongOutputRoutesRequest);
  page.emit('response', makeResponse(wrongOutputRoutesRequest, 200, async () => '{"routes":[]}'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(resolved, false, 'a population-route response for another output must not satisfy the action');

  const routesRequest = makeRequest(populationRoutesPath, 'POST', 'population-routes-post', {
    body: { selectionRevisionId: selectionId, outputId },
  });
  page.emit('request', routesRequest);
  let finishRoutesBody;
  page.emit('response', makeResponse(routesRequest, 200, () => new Promise(resolve => {
    finishRoutesBody = resolve;
  })));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(report.nativeRequests.find(entry => entry.requestId === 'population-routes-post').completedAt, undefined);
  assert.equal(resolved, false,
    'the route endpoint must also finish response-body capture before Configure rows returns');

  finishRoutesBody('{"routes":[]}');
  const result = await waiting;
  await capture.flush();

  assert.equal(result.selectionRead, report.nativeRequests.find(entry => entry.requestId === 'selection-get'));
  assert.equal(result.populationRoutes, report.nativeRequests.find(entry => entry.requestId === 'population-routes-post'));
  assert.equal(result.deadline, startedAt + 5_000);
  assert.equal(capture.rawResponseBody(result.selectionRead).revision.id, selectionId);
  assert.deepEqual(capture.rawResponseBody(result.populationRoutes), { routes: [] });
  assert(report.nativeRequests.every(entry => Number.isFinite(entry.completedAt) && entry.status === 200));
  assert.deepEqual(report.errors, []);
});

test('Configure rows spends one original deadline across both exact requests', async () => {
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
  entries[1].body = { selectionRevisionId: selectionId, outputId };
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
    fromIndex: 3,
    startedAt,
    now: () => clock,
  }), /population-routes POST did not reach captured terminal completion before its 5000 ms deadline/);
  assert.deepEqual(calls.map(call => call.timeoutMs), [5_000, 100],
    'the second exact request receives only the remainder of the original deadline');
  assert(calls.every(call => call.fromIndex === 3),
    'both requests remain scoped to events recorded after the Configure rows click');
});

test('a terminal failed exact request remains fatal instead of satisfying Configure rows', async () => {
  const { page, report, capture } = makeCapture();
  const startedAt = Date.now();
  const waiting = waitForStartingCollectionConfigurationRequests({
    browserEvents: capture,
    explorerPath,
    selectionId,
    outputId,
    fromIndex: 0,
    startedAt,
  });

  const selectionRequest = makeRequest(selectionPath, 'GET', 'selection-get', { query: '?limit=100' });
  page.emit('request', selectionRequest);
  page.emit('response', makeResponse(selectionRequest, 200, async () => '{"revision":{"id":"selection-revision-1"}}'));
  await capture.waitFor(entry => entry.path === selectionPath && entry.status === 200);

  const routesRequest = makeRequest(populationRoutesPath, 'POST', 'population-routes-post', {
    body: { selectionRevisionId: selectionId, outputId },
  });
  page.emit('request', routesRequest);
  page.emit('response', makeResponse(routesRequest, 503, async () => '{"error":"unavailable"}'));
  await assert.rejects(waiting, /population-routes POST returned HTTP 503/);
  await capture.flush();

  assert.equal(report.nativeRequests[1].status, 503);
  assert.equal(Number.isFinite(report.nativeRequests[1].completedAt), true);
  assert.equal(report.errors.some(error => error.kind === 'http' && error.status === 503), true);
});
