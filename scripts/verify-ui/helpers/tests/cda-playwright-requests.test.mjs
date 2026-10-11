import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';
import {
  captureCDARequests,
  createCDARequestView,
  matchesExpectedEmptyCollectionValidation,
  matchesNativeConstructionRemovalProposal,
} from '../cda-playwright-requests.mjs';

const makeEntry = (index, path, origin = 'http://owned.test') => ({
  index, origin, path, method: 'POST', status: 200,
});

test('CDA request view stays live and translates checkpoints past unrelated observer requests', async () => {
  const origin = 'http://owned.test';
  const observer = {
    requests: [
      makeEntry(0, '/api/projects/owned/explorers/one/commands'),
      makeEntry(1, '/api/projects/other/explorers/one/preview'),
      makeEntry(2, '/api/projects/owned/explorers/one/commands'),
      makeEntry(3, '/api/projects/other/explorers/one/commands'),
    ],
    byRequest: new WeakMap(),
    waiters: [],
    requestBody: async entry => entry.request,
    responseBody: async entry => entry.response,
    markExpectedValidation() {},
    markExpectedTransportFailure() {},
    waitForResponse(predicate, options) {
      const match = () => this.requests.slice(options.fromIndex).find(predicate);
      const existing = match();
      if (existing) return Promise.resolve(existing);
      return new Promise(resolve => this.waiters.push({ predicate, options, resolve }));
    },
  };
  const view = createCDARequestView(observer, {
    browserRequestOrigin: origin,
    ownedPathPrefix: '/api/projects/owned/explorers/one',
  });

  const fromIndex = view.requests.length;
  assert.equal(fromIndex, 2);
  const response = view.waitForResponse(entry => entry.path.endsWith('/commands'), { fromIndex });
  assert.equal(observer.waiters[0].options.fromIndex, 4,
    'the tracker-relative checkpoint must map after all four global observer entries');

  observer.requests.push(makeEntry(4, '/api/projects/other/explorers/one/commands'));
  for (const waiter of observer.waiters) {
    const match = observer.requests.slice(waiter.options.fromIndex).find(waiter.predicate);
    if (match) waiter.resolve(match);
  }
  observer.requests.push(makeEntry(5, '/api/projects/owned/explorers/one/commands'));
  for (const waiter of observer.waiters) {
    const match = observer.requests.slice(waiter.options.fromIndex).find(waiter.predicate);
    if (match) waiter.resolve(match);
  }

  assert.equal((await response).index, 5,
    'an earlier scoped response and intervening unrelated response cannot satisfy the later checkpoint');
  assert.equal(view.requests.length, 3, 'the requests getter reflects requests observed after view creation');
});

test('captureCDARequests preserves the live request getter on its returned adapter', () => {
  const page = new EventEmitter();
  page.url = () => 'http://owned.test/builder';
  const report = { nativeRequests: [], network: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://owned.test',
    ownedPathPrefix: '/api/projects/owned/explorers/one',
    report,
    responsePaths: [],
  });
  assert.equal(capture.requests.length, 0);
  const request = {
    url: () => 'http://owned.test/api/projects/owned/explorers/one/commands',
    method: () => 'POST',
    resourceType: () => 'fetch',
    postData: () => JSON.stringify({ operation: 'save' }),
    headers: () => ({ 'x-request-id': 'request-1' }),
  };
  page.emit('request', request);
  assert.equal(capture.requests.length, 1);
  assert.equal(capture.requests[0].request.operation, 'save');
  capture.stop();
});

test('numeric-filter and source-fields workflows reread the fresh native request view', () => {
  const observer = { requests: [], waitForResponse() {} };
  const view = createCDARequestView(observer, {
    browserRequestOrigin: 'http://owned.test',
    ownedPathPrefix: '/api/projects/owned/explorers/one',
  });
  const firstView = view.requests;
  const ownedEntry = {
    origin: 'http://owned.test', path: '/api/projects/owned/explorers/one/preview', status: 200,
  };
  observer.requests.push(ownedEntry);
  const currentView = view.requests;
  assert.notEqual(currentView, firstView, 'the adapter getter must return its newly filtered request view');
  assert.equal(firstView.length, 0, 'an earlier getter result remains a stale snapshot');
  assert.deepEqual(currentView, [ownedEntry], 'a fresh getter observes the newly retained native request');

  const numericFilter = readFileSync(new URL('../../workflows/verify-cda-group-numeric-filter-browser.mjs', import.meta.url), 'utf8');
  const sourceFields = readFileSync(new URL('../../workflows/source-fields-workflow.mjs', import.meta.url), 'utf8');
  assert.doesNotMatch(numericFilter, /nativeRequests\s*=\s*capture\.requests/,
    'numeric-filter selectors and action windows must not retain one getter result');
  assert.doesNotMatch(sourceFields, /nativeRequests\s*=\s*tracker\.requests/,
    'source-fields wait and Cancel checks must not retain one getter result');
  assert.match(numericFilter, /selectSavedPreviewRequest\(capture\.requests,/);
  assert.match(sourceFields, /browserEvents\.requests\.slice\(fromIndex\)\.filter/);
});

test('source-fields local assertions populate the registered CDA report checks', () => {
  const sourceFields = readFileSync(new URL('../../workflows/source-fields-workflow.mjs', import.meta.url), 'utf8');
  const names = [...sourceFields.matchAll(/recordAssertion\('(correctness|persistence)',\s*'([^']+)'/g)]
    .map(([, , name]) => name);
  const registered = scenarioCaseFor('cda-source-fields', 'source-fields').requiredChecks;
  assert.equal(new Set(names).size, names.length, 'each registered source-fields outcome must be reported once');
  assert.deepEqual([...names].sort(), [...registered].sort());
  assert.match(sourceFields, /const recordAssertion = \(dimension, name, evidenceValue\) => \{\s*cda\.check\(dimension, name, true, evidenceValue\);/);
});

test('proposal matcher binds the exact request and decoded response', () => {
  const expected = {
    outputId: 'output-1', snapshotToken: 'snapshot-1', draftVersion: 8,
    draftDigest: 'digest-8', stepId: 'step-1',
  };
  const entry = {
    method: 'POST', path: '/construction-proposals',
    request: { outputId: expected.outputId, snapshotToken: expected.snapshotToken,
      expectedDraftVersion: expected.draftVersion, expectedDraftDigest: expected.draftDigest,
      removeStepIds: [expected.stepId] },
  };
  const response = { proposalId: 'proposal-1', outputId: expected.outputId,
    snapshotToken: expected.snapshotToken, draftVersion: expected.draftVersion, draftDigest: expected.draftDigest };
  assert.equal(matchesNativeConstructionRemovalProposal(entry, response, expected), true);
  assert.equal(matchesNativeConstructionRemovalProposal(entry, { ...response, draftVersion: 7 }, expected), false);
});

test('empty-collection validation matcher reads the report-safe decoded request and response fields', () => {
  const expected = {
    path: '/related-expand-choices', snapshotToken: 'snapshot-1', expectedDraftVersion: 8,
    expectedDraftDigest: 'digest-8', outputId: 'output-1', rowChoiceId: 'row-choice-1',
    code: 'EMPTY_COLLECTION', stage: 'materialize',
  };
  const entry = {
    method: 'POST', path: expected.path, status: 422,
    request: {
      snapshotToken: expected.snapshotToken, expectedDraftVersion: expected.expectedDraftVersion,
      expectedDraftDigest: expected.expectedDraftDigest, outputId: expected.outputId,
      selection: { kind: 'EXPANDED', expanded: { rowChoiceId: expected.rowChoiceId, emptyCollectionPolicy: 'ERROR' } },
    },
    response: { error: { code: expected.code, diagnostic: { code: expected.code, stage: expected.stage } } },
  };
  assert.equal(matchesExpectedEmptyCollectionValidation(entry, expected), true);
  assert.equal(matchesExpectedEmptyCollectionValidation({ ...entry, request: { ...entry.request, outputId: 'other' } }, expected), false);
});
