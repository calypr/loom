import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyInjectedFaultPolicy,
  matchesOwnedFaultRequest,
  ownedFaultTarget,
} from './network-evidence.mjs';

const target = { uiUrl: 'http://127.0.0.1:30008', fixtureProject: 'owned' };
const route = '/api/v1/projects/owned/explorers/editor/authoring/v2/commands';
const rawURL = target.uiUrl + route + '?draft=7';
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
  assert.equal(matchesOwnedFaultRequest(request({ url: () => 'http://elsewhere.invalid' + route }), owned), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => target.uiUrl + route + '/extra' }), owned), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => target.uiUrl + route.replace('/owned/', '/other/') }), owned), false);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: route + '?extra=1' }), /exact pathname/);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: '//elsewhere.invalid/' }), /owned UI origin/);
  assert.throws(() => ownedFaultTarget(target, { method: 'POST', path: route.replace('/projects/owned/', '/projects/foreign/') }), /owned fixture project/);
});

test('a request-body predicate is evaluated only after the exact owned request matches', () => {
  const owned = ownedFaultTarget(target, { method: 'POST', path: route });
  let predicateCalls = 0;
  const matches = (candidate) => {
    predicateCalls += 1;
    return candidate.expectedDraftVersion === 7 && candidate.stageId === 'stage-a';
  };
  assert.equal(matchesOwnedFaultRequest(request(), owned, matches), true);
  assert.equal(predicateCalls, 1);
  assert.equal(matchesOwnedFaultRequest(request({ method: () => 'GET' }), owned, matches), false);
  assert.equal(predicateCalls, 1);
  assert.equal(matchesOwnedFaultRequest(request({ postDataJSON: () => { throw new Error('bad JSON'); } }), owned, matches), false);
  assert.equal(predicateCalls, 1);
});

test('only the exact injected 422 response is marked expected; same-path server errors stay unmarked', () => {
  const fault = {
    id: 'injected-1', matched: true, action: 'fulfill', responseStatus: 422,
    playwrightRequestId: 'request-1', method: 'POST', rawURL,
  };
  const result = applyInjectedFaultPolicy([
    { kind: 'network', status: 422, method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-1' },
    { kind: 'network', status: 500, method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-2' },
    { kind: 'network', status: 422, method: 'POST', url: target.uiUrl + route, rawURL: target.uiUrl + route + '?other=1', playwrightRequestId: 'request-3' },
    { kind: 'network', status: 422, method: 'GET', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-4' },
  ], [fault]);
  assert.deepEqual(result.map((entry) => entry.injectedFault === true), [true, false, false, false]);
  assert.equal(result[0].injectedStatus, 422);
  assert.equal(result[1].status, 500);
  assert.equal(result.some((entry) => Object.hasOwn(entry, 'rawURL')), false);
});

test('a fulfilled 422 consumes only the matching console diagnostic once', () => {
  const fault = {
    id: 'injected-422', matched: true, action: 'fulfill', responseStatus: 422,
    playwrightRequestId: 'request-422', method: 'POST', rawURL,
  };
  const message = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const result = applyInjectedFaultPolicy([
    { kind: 'network', status: 422, method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-422' },
    { kind: 'console-error', text: message, location: target.uiUrl + route, rawLocation: rawURL },
    { kind: 'console-error', text: message, location: target.uiUrl + route, rawLocation: rawURL },
    { kind: 'console-error', text: message, location: target.uiUrl + route + '?other=1', rawLocation: target.uiUrl + route + '?other=1' },
    { kind: 'console-error', text: 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)', location: target.uiUrl + route, rawLocation: rawURL },
  ], [fault]);
  assert.equal(result[0].injectedFault, true);
  assert.equal(result[1].kind, 'network');
  assert.equal(result[1].status, 422);
  assert.equal(result[1].injectedRequestId, 'injected-422');
  assert.equal(result[2].kind, 'console-error', 'a duplicate console diagnostic remains a failure');
  assert.equal(result[3].kind, 'console-error', 'a different URL is not attributed to the injected response');
  assert.equal(result[4].kind, 'console-error', 'a 500 diagnostic remains a failure');
});

test('an injected abort consumes only its matching failure and one matching console error', () => {
  const fault = {
    id: 'injected-1', matched: true, action: 'abort', responseStatus: null,
    playwrightRequestId: 'request-1', method: 'POST', rawURL,
  };
  const result = applyInjectedFaultPolicy([
    { kind: 'console-error', text: 'Failed to load resource: net::ERR_FAILED', location: target.uiUrl + route, rawLocation: rawURL },
    { kind: 'console-error', text: 'Failed to load resource: net::ERR_FAILED', location: target.uiUrl + route, rawLocation: rawURL },
    { kind: 'console-error', text: 'Failed to load resource: net::ERR_FAILED', location: target.uiUrl + route + '?other=1', rawLocation: target.uiUrl + route + '?other=1' },
    { kind: 'network', method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-1', errorText: 'net::ERR_FAILED' },
    { kind: 'network', method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-2', errorText: 'net::ERR_FAILED' },
    { kind: 'network', method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-3', errorText: 'net::ERR_ABORTED' },
    { kind: 'network', method: 'GET', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-1', errorText: 'net::ERR_FAILED' },
  ], [fault]);
  assert.equal(result[0].kind, 'network');
  assert.equal(result[0].observedAs, 'console-error');
  assert.equal(result[0].method, 'POST');
  assert.equal(result[0].errorText, 'net::ERR_FAILED');
  assert.equal(result[0].injectedFault, true);
  assert.equal(result[1].kind, 'console-error');
  assert.equal(result[2].kind, 'console-error');
  assert.equal(result[3].injectedFault, true);
  assert.equal(result[4].injectedFault, undefined);
  assert.equal(result[5].injectedFault, undefined);
  assert.equal(result[6].injectedFault, undefined);
});

test('an injected 500 or aborted request is not reclassified as expected', () => {
  const faults = [
    { id: 'injected-1', matched: true, action: 'fulfill', responseStatus: 500, playwrightRequestId: 'request-1', method: 'POST', rawURL },
    { id: 'injected-2', matched: true, action: 'abort', responseStatus: null, playwrightRequestId: 'request-2', method: 'POST', rawURL },
  ];
  const result = applyInjectedFaultPolicy([
    { kind: 'network', status: 500, method: 'POST', rawURL, playwrightRequestId: 'request-1' },
    { kind: 'network', method: 'POST', rawURL, playwrightRequestId: 'request-2', errorText: 'net::ERR_ABORTED' },
  ], faults);
  assert.equal(result.every((entry) => entry.injectedFault !== true), true);
});

test('Viewer fault matches only the owned GraphQL project and pinned selector', () => {
  const graph = ownedFaultTarget(target, { method: 'POST', path: '/graphql/graph' });
  const graphRequest = projectId => request({
    url: () => target.uiUrl + '/graphql/graph',
    postDataJSON: () => ({ variables: { input: { projectId, selector: { recipe: 'owned-recipe' } } } }),
  });
  const selectorMatches = value => value.variables.input.selector.recipe === 'owned-recipe';
  assert.equal(matchesOwnedFaultRequest(graphRequest('owned'), graph, selectorMatches), true);
  assert.equal(matchesOwnedFaultRequest(graphRequest('foreign'), graph, selectorMatches), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest('foreign'), graph), false);
  assert.equal(matchesOwnedFaultRequest(request({ url: () => target.uiUrl + '/graphql/graph' }), graph), false);
  assert.equal(matchesOwnedFaultRequest(graphRequest('owned'), graph, () => false), false);
});
