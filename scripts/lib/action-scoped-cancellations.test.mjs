import assert from 'node:assert/strict';
import test from 'node:test';
import { actionScopeContains, cancellationScope, matchesOwnedCatalogRequest } from './action-scoped-cancellations.mjs';

const target = {
  uiOrigin: 'http://127.0.0.1:30102',
  pathPrefix: '/api/v1/projects/owned/explorers/current/authoring/v2',
  snapshotToken: 'snapshot-current',
  documents: [{ rootResourceType: 'Patient', output: { id: 'output-current' } }],
  draftVersion: 2,
  draftDigest: 'digest-current',
};
const request = (endpoint, payload, requestId = '') => ({
  method: () => 'POST',
  url: () => `${target.uiOrigin}${target.pathPrefix}/${endpoint}`,
  frame: () => ({ url: () => `${target.uiOrigin}/?mode=builder` }),
  postDataJSON: () => payload,
  headers: () => ({ 'x-request-id': requestId }),
});

test('exact owned semantic inventory allows paired suggestions without outputId', () => {
  const candidate = request('semantic-inventory', { snapshotToken: target.snapshotToken, rowRoot: 'Patient' }, 'paired-column-inventory-123');
  assert.equal(matchesOwnedCatalogRequest(candidate, 'semantic-inventory', target), true);
  assert.equal(matchesOwnedCatalogRequest(request('semantic-inventory', { snapshotToken: target.snapshotToken, rowRoot: 'Patient' }, 'feature-catalog-123'), 'semantic-inventory', target), true);
  assert.equal(matchesOwnedCatalogRequest({ ...candidate, frame: () => { throw new Error('frame detached on abort'); } }, 'semantic-inventory', target), true);
  assert.equal(matchesOwnedCatalogRequest({ ...candidate, url: () => candidate.url().replace('30102', '8282') }, 'semantic-inventory', target), false);
  assert.equal(matchesOwnedCatalogRequest({ ...candidate, url: () => candidate.url().replace('/current/', '/other/') }, 'semantic-inventory', target), false);
  assert.equal(matchesOwnedCatalogRequest({ ...candidate, postDataJSON: () => ({ snapshotToken: 'stale', rowRoot: 'Patient' }) }, 'semantic-inventory', target), false);
  assert.equal(matchesOwnedCatalogRequest(request('semantic-inventory', { snapshotToken: target.snapshotToken, rowRoot: 'Patient' }, 'unrelated-123'), 'semantic-inventory', target), false);
  assert.equal(matchesOwnedCatalogRequest(request('semantic-inventory', { snapshotToken: target.snapshotToken, rowRoot: 'Specimen' }, 'feature-catalog-123'), 'semantic-inventory', target), false);
});

test('proposal cancellation requires saved draft, output, and construction choices', () => {
  const payload = { snapshotToken: target.snapshotToken, outputId: 'output-current', expectedDraftVersion: 2,
    expectedDraftDigest: 'digest-current', constructionChoices: [{ choiceId: 'choice-a' }] };
  assert.equal(matchesOwnedCatalogRequest(request('construction-choice-proposals', payload), 'construction-choice-proposals', target), true);
  assert.equal(matchesOwnedCatalogRequest(request('construction-choice-proposals', { ...payload, outputId: 'other' }), 'construction-choice-proposals', target), false);
  assert.equal(matchesOwnedCatalogRequest(request('construction-choice-proposals', { ...payload, expectedDraftDigest: 'stale' }), 'construction-choice-proposals', target), false);
  assert.equal(matchesOwnedCatalogRequest(request('construction-choice-proposals', { ...payload, constructionChoices: [] }), 'construction-choice-proposals', target), false);
  assert.equal(matchesOwnedCatalogRequest({ ...request('construction-choice-proposals', payload), method: () => 'GET' }, 'construction-choice-proposals', target), false);
});

test('action cancellation scope retains exact endpoints and pending request identities', () => {
  const pending = {};
  const scope = cancellationScope('close dialog', ['semantic-inventory'], [pending], 100);
  assert.equal(scope.action, 'close dialog');
  assert.deepEqual(scope.endpoints, ['semantic-inventory']);
  assert.equal(scope.requests.has(pending), true);
  assert.equal(scope.requests.has({}), false);
  assert.equal(scope.armedAt, 100);
  assert.equal(actionScopeContains(scope, pending, 'close dialog', 5100), true);
  assert.equal(actionScopeContains(scope, pending, 'other action', 101), false);
  assert.equal(actionScopeContains(scope, {}, 'close dialog', 101), false);
  assert.equal(actionScopeContains(scope, pending, 'close dialog', 5101), false);
});
