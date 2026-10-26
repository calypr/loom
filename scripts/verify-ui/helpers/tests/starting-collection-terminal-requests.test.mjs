import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { waitForStartingCollectionConfigurationRequests } from '../../workflows/verify-cda-starting-collection-handoff.mjs';

const explorerPath = '/api/v1/projects/owned/explorers/explorer-1';
const selectionId = 'selection-revision-1';
const outputId = 'output-1';
const snapshotToken = `sha256:${'a'.repeat(64)}`;
const selectionPath = `${explorerPath}/selections/${selectionId}`;
const rowDefinitionChoicesPath = `${explorerPath}/authoring/v2/row-definition-choices`;
const populationRoutesPath = `${explorerPath}/authoring/v2/population-routes`;

const makeEntry = (path, method, {
  startedAt = Date.now(),
  completedAt = startedAt + 10,
  triggerAction = 'Configure rows',
  query = {},
  request = {},
  response = {},
  status = 200,
  failure,
} = {}) => ({
  path, method, startedAt, completedAt, triggerAction, query, request, response, status, failure,
});

const makeObserver = (requests, calls = []) => ({
  requests,
  calls,
  waitForResponse(predicate, options = {}) {
    calls.push(options);
    const fromIndex = options.fromIndex ?? 0;
    const match = requests.slice(fromIndex).find(predicate);
    return match ? Promise.resolve(match) : Promise.reject(new Error('No matching response was captured'));
  },
});

const wait = (browserEvents, phase, { fromIndex = 0, startedAt = Date.now() } = {}) =>
  waitForStartingCollectionConfigurationRequests({
    browserEvents,
    explorerPath,
    selectionId,
    outputId,
    snapshotToken,
    phase,
    fromIndex,
    startedAt,
  });

test('each starting-collection opener uses its observer cursor and includes required reads in the action budget', async () => {
  const source = await readFile(new URL('../../workflows/verify-cda-starting-collection-handoff.mjs', import.meta.url), 'utf8');
  const openStart = source.indexOf('const openStartingCollection = async');
  const openEnd = source.indexOf('\n  try {', openStart);
  const openAction = source.slice(openStart, openEnd);
  const started = openAction.indexOf('const configureRowsStartedAt = Date.now();');
  const requestSnapshot = openAction.indexOf('const configureRowsRequestFromIndex = browserEvents.requests.length;');
  const action = openAction.indexOf("await cda.action('Configure rows'");
  const panelReady = openAction.indexOf('await wait(([id]) =>', action);
  const requestCompletion = openAction.indexOf('await waitForStartingCollectionConfigurationRequests({', panelReady);
  const elapsed = openAction.indexOf('const elapsedMs = Date.now() - configureRowsStartedAt;', requestCompletion);
  assert(openStart >= 0 && openEnd > openStart, 'the starting-collection opener must remain an explicit workflow action');
  assert(started >= 0 && started < action, 'the request deadline must begin before the Configure rows click');
  assert(requestSnapshot > started && requestSnapshot < action,
    'the scoped observer cursor must be snapshotted before the click');
  assert(action < panelReady && panelReady < requestCompletion && requestCompletion < elapsed,
    'the native action must await panel readiness and exact required responses before recording its duration');
  assert.match(openAction.slice(action), /after:\s*async\s*\(\)\s*=>\s*\{/,
    'the panel and response waits must stay in the existing cda.action after phase');
  const initialOpen = source.indexOf("await openStartingCollection(selection.id, outputSelector, 'unattached-handoff');");
  const restoredOpen = source.indexOf("await openStartingCollection(selection.id, outputSelector, 'saved-attachment-after-reload');");
  assert(initialOpen >= 0 && restoredOpen > initialOpen,
    'the initial and restored collection opens must declare their distinct saved states');
  assert(openAction.includes('snapshotToken: builder.catalog.snapshotToken'),
    'both required request variants must use the saved draft snapshot');
});

test('the unattached handoff accepts only its exact route-choice and route requests', async () => {
  const startedAt = Date.now();
  const rows = [
    makeEntry(`${explorerPath}/selections/${selectionId}`, 'GET', { triggerAction: 'Earlier action' }),
    makeEntry(rowDefinitionChoicesPath, 'GET', {
      triggerAction: 'Unrelated native action',
      query: { outputId, snapshotToken },
    }),
    makeEntry(rowDefinitionChoicesPath, 'GET', {
      query: { outputId, snapshotToken: 'sha256:wrong' },
    }),
    makeEntry(rowDefinitionChoicesPath, 'GET', {
      query: { outputId, snapshotToken },
      response: { choices: [{ route: 'subject_Patient' }] },
    }),
    makeEntry(populationRoutesPath, 'POST', {
      request: { snapshotToken, selectionRevisionId: 'another-selection', outputId, limit: 50 },
    }),
    makeEntry(populationRoutesPath, 'POST', {
      request: { snapshotToken, selectionRevisionId: selectionId, outputId, limit: 50 },
      response: { snapshotToken, outputId, selectionRevisionId: selectionId, choices: [] },
    }),
  ];
  const calls = [];
  const result = await wait(makeObserver(rows, calls), 'unattached-handoff', { fromIndex: 1, startedAt });

  assert.equal(result.phase, 'unattached-handoff');
  assert.equal(result.phaseRequest, rows[3]);
  assert.equal(result.populationRoutes, rows[5]);
  assert.equal(result.deadline, startedAt + 5000);
  assert.deepEqual(result.phaseRequest.response, { choices: [{ route: 'subject_Patient' }] });
  assert.deepEqual(result.populationRoutes.response, {
    snapshotToken, outputId, selectionRevisionId: selectionId, choices: [],
  });
  assert.deepEqual(calls.map(({ fromIndex }) => fromIndex), [1, 1]);
});

test('the restored attached state requires the exact full-page selection read and population route', async () => {
  const startedAt = Date.now();
  const rows = [
    makeEntry(selectionPath, 'GET', { triggerAction: 'Unrelated action', query: { limit: '100' } }),
    makeEntry(selectionPath, 'GET', { query: { limit: '1' } }),
    makeEntry(selectionPath, 'GET', {
      query: { limit: '100' },
      response: { revision: { id: selectionId, memberCount: 2 }, members: [{}, {}] },
    }),
    makeEntry(populationRoutesPath, 'POST', {
      request: { snapshotToken, selectionRevisionId: selectionId, outputId, limit: 50 },
      response: { snapshotToken, outputId, selectionRevisionId: selectionId, choices: [] },
    }),
  ];
  const result = await wait(makeObserver(rows), 'saved-attachment-after-reload', { startedAt });

  assert.equal(result.phase, 'saved-attachment-after-reload');
  assert.equal(result.phaseRequest, rows[2]);
  assert.equal(result.populationRoutes, rows[3]);
  assert.equal(result.phaseRequest.response.revision.id, selectionId);
  assert.equal(result.phaseRequest.response.members.length, 2);
});

test('unknown starting-collection phases fail before request matching', async () => {
  const browserEvents = { waitForResponse: () => assert.fail('unknown phases must not start request matching') };
  await assert.rejects(wait(browserEvents, 'unknown-reopen-state'), /Unknown starting-collection configuration phase/);
});

test('Configure rows uses one original deadline across the restored-state responses', async () => {
  const startedAt = 10_000;
  let clock = startedAt;
  const calls = [];
  const entries = [
    makeEntry(selectionPath, 'GET', {
      startedAt,
      completedAt: startedAt + 100,
      query: { limit: '100' },
    }),
    makeEntry(populationRoutesPath, 'POST', {
      startedAt: startedAt + 4_900,
      completedAt: startedAt + 5_001,
      request: { snapshotToken, selectionRevisionId: selectionId, outputId },
    }),
  ];
  const browserEvents = {
    requests: entries,
    waitForResponse(predicate, options) {
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
  }), /did not reach captured terminal completion before its 5000 ms deadline/);
  assert.deepEqual(calls.map(({ timeoutMs }) => timeoutMs), [5000, 100],
    'the second required response receives only the remainder of the original deadline');
  assert(calls.every(({ fromIndex }) => fromIndex === 3),
    'both responses stay scoped to requests after the Configure rows click');
});

test('a failed population-routes response remains fatal in restored attached state', async () => {
  const rows = [
    makeEntry(selectionPath, 'GET', {
      query: { limit: '100' },
      response: { revision: { id: selectionId, memberCount: 2 }, members: [{}, {}] },
    }),
    makeEntry(populationRoutesPath, 'POST', {
      request: { snapshotToken, selectionRevisionId: selectionId, outputId },
      response: { error: 'unavailable' },
      status: 503,
    }),
  ];
  await assert.rejects(wait(makeObserver(rows), 'saved-attachment-after-reload'),
    /Configure rows population-routes POST returned HTTP 503/);
});
