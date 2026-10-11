import test from 'node:test';
import assert from 'node:assert/strict';
import { matchesOwnedFaultRequest, ownedFaultTarget } from '../network-evidence.mjs';

const target = { uiUrl: 'http://127.0.0.1:30008', fixtureProject: 'owned' };
const route = '/api/v1/projects/owned/explorers/editor/authoring/v2/commands';
const rawURL = `${target.uiUrl}${route}?draft=7`;
const body = { expectedDraftVersion: 7, outputId: 'output-a', stageId: 'stage-a' };
const request = (overrides = {}) => ({
  url: () => rawURL,
  method: () => 'POST',
  postDataJSON: () => body,
  ...overrides,
});

test('fault targets stay on the owned origin and require exact method and pathname', () => {
  const owned = ownedFaultTarget(target, { method: 'post', path: route });
  assert.deepEqual(owned, { origin: target.uiUrl, method: 'POST', path: route });
  assert.equal(matchesOwnedFaultRequest(request(), owned), true);
  assert.equal(matchesOwnedFaultRequest(request({ method: () => 'GET' }), owned), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => `http://elsewhere.invalid${route}` }), owned), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => `${target.uiUrl}${route}/extra` }), owned), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => `${target.uiUrl}${route.replace('/owned/', '/other/')}` }), owned), false);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: `${route}?extra=1` }), /exact pathname/);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: '//elsewhere.invalid/' }), /owned UI origin/);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: route.replace('/projects/owned/', '/projects/foreign/') }), /owned fixture project/);
});

test('a request-body predicate runs only after exact ownership and rejects async predicates', () => {
  const owned = ownedFaultTarget(target, { method: 'POST', path: route });
  let predicateCalls = 0;
  const matches = candidate => {
    predicateCalls += 1;
    return candidate.expectedDraftVersion === 7 && candidate.stageId === 'stage-a';
  };
  assert.equal(matchesOwnedFaultRequest(request(), owned, matches), true);
  assert.equal(predicateCalls, 1);
  assert.equal(matchesOwnedFaultRequest(request({ method: () => 'GET' }), owned, matches), false);
  assert.equal(predicateCalls, 1);
  assert.equal(matchesOwnedFaultRequest(request({ postDataJSON: () => { throw new Error('bad JSON'); } }), owned, matches), false);
  assert.equal(predicateCalls, 1);
  assert.equal(matchesOwnedFaultRequest(request(), owned, async () => true), false);
});

test('GraphQL fault matching is pinned to the owned project and caller selector', () => {
  const projectAlias = 'loom_dev_verify_mv3ae371-0b1cdcf';
  const canonicalProject = 'loom_dev_verify_mv3ae371/0b1cdcf';
  const graphTarget = { ...target, fixtureProject: projectAlias };
  const graph = ownedFaultTarget(graphTarget, { method: 'POST', path: '/graphql/graph' });
  const graphRequest = (projectId, recipe = 'owned-recipe', overrides = {}) => request({
    url: () => `${target.uiUrl}/graphql/graph`,
    method: () => 'POST',
    postDataJSON: () => ({ variables: { input: { projectId, selector: { recipe } } } }),
    ...overrides,
  });
  const selectorMatches = value => value.variables.input.selector.recipe === 'owned-recipe';
  assert.deepEqual(graph, {
    origin: target.uiUrl,
    method: 'POST',
    path: '/graphql/graph',
    bodyProject: canonicalProject,
  });
  assert.equal(matchesOwnedFaultRequest(graphRequest(canonicalProject), graph, selectorMatches), true);
  assert.equal(matchesOwnedFaultRequest(graphRequest('loom_dev_verify_mv3ae371/other'), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest(canonicalProject, 'other-recipe'), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest(canonicalProject, 'owned-recipe', {
    method: () => 'GET',
  }), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest(canonicalProject, 'owned-recipe', {
    url: () => `http://elsewhere.invalid/graphql/graph`,
  }), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest(canonicalProject, 'owned-recipe', {
    url: () => `${target.uiUrl}/graphql/other`,
  }), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest('loom_dev_verify_mv3ae371/other'), graph), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => `${target.uiUrl}/graphql/graph` }), graph), false);
});

test('owned fault targets match the canonical double-encoded browser project route', () => {
  const alias = 'loom_dev_verify_mv42n2lv-f5143a0';
  const targetWithAlias = { ...target, fixtureProject: alias };
  const path = '/api/v1/projects/loom_dev_verify_mv42n2lv%252Ff5143a0/explorers';
  const owned = ownedFaultTarget(targetWithAlias, { method: 'GET', path });
  assert.deepEqual(owned, { origin: target.uiUrl, method: 'GET', path });

  const browserRequest = (requestPath, origin = target.uiUrl, method = 'GET') => request({
    url: () => `${origin}${requestPath}`,
    method: () => method,
  });
  assert.equal(matchesOwnedFaultRequest(browserRequest(path), owned), true);
  assert.equal(matchesOwnedFaultRequest(browserRequest(path.replace('f5143a0', 'foreign')), owned), false);
  assert.equal(matchesOwnedFaultRequest(browserRequest(path, 'http://elsewhere.invalid'), owned), false);
  assert.equal(matchesOwnedFaultRequest(browserRequest(path, target.uiUrl, 'POST'), owned), false);
  assert.throws(() => ownedFaultTarget(targetWithAlias, {
    method: 'GET',
    path: '/api/v1/projects/loom_dev_verify_mv42n2lv%2Ff5143a0/explorers',
  }), /owned fixture project/);
});
