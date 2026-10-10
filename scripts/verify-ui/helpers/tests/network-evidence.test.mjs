import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyInjectedFaultPolicy,
  captureInjectedFaultRequest,
  markExpectedOwnedPreviewAborts,
  matchesOwnedFaultRequest,
  matchExpectedHttpConsole,
  ownedFaultTarget,
} from '../network-evidence.mjs';
import { classifyNetworkRecord } from '../report.mjs';

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
  const diagnostics = [
    { kind: 'network', status: 422, method: 'POST', url: target.uiUrl + route, rawURL, playwrightRequestId: 'request-422', source: 'http-request' },
    { kind: 'console-error', text: message, location: target.uiUrl + route, rawLocation: rawURL, source: 'expected-console' },
    { kind: 'console-error', text: message, location: target.uiUrl + route, rawLocation: rawURL, source: 'duplicate-console' },
    { kind: 'console-error', text: message, location: target.uiUrl + route + '?other=1', rawLocation: target.uiUrl + route + '?other=1', source: 'other-url-console' },
    { kind: 'console-error', text: 'Failed to load resource: the server responded with a status of 500 (Internal Server Error)', location: target.uiUrl + route, rawLocation: rawURL, source: 'fatal-console' },
    { kind: 'exception', message: 'later diagnostic', source: 'tail' },
  ];
  const result = applyInjectedFaultPolicy(diagnostics, [fault]);
  assert.equal(result.length, diagnostics.length,
    'fixture Request identity association remains a one-to-one projection, including console replacement');
  assert.equal(result[0].source, 'http-request', 'the HTTP diagnostic remains at its original Request-associated position');
  assert.equal(result[1].injectedRequestId, 'injected-422', 'the console replacement stays in its source position');
  assert.equal(result[2].source, 'duplicate-console', 'an unconsumed duplicate stays at its source position');
  assert.equal(result[3].source, 'other-url-console', 'an unrelated URL stays at its source position');
  assert.equal(result[4].source, 'fatal-console', 'a fatal HTTP console diagnostic stays at its source position');
  assert.equal(result[5].source, 'tail', 'later diagnostics are not shifted onto another Request');
  assert.equal(result[0].injectedFault, true);
  assert.equal(result[1].kind, 'network');
  assert.equal(result[1].status, 422);
  assert.equal(result[1].injectedRequestId, 'injected-422');
  assert.equal(result[1].playwrightRequestId, 'request-422',
    'the uniquely paired console observation retains the exact native Request identity');
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

test('captured list and builder-load aborts retain exact request identity for strict fault classification', () => {
  const cases = [
    {
      name: 'Explorer list',
      path: '/api/v1/projects/owned/explorers',
      rawURL: `${target.uiUrl}/api/v1/projects/owned/explorers?limit=50`,
    },
    {
      name: 'Builder state',
      path: '/api/v1/projects/owned/explorers/loom-dev-bootstrap/authoring/v2/builder',
      rawURL: `${target.uiUrl}/api/v1/projects/owned/explorers/loom-dev-bootstrap/authoring/v2/builder?draft=1`,
    },
  ];

  for (const [index, candidate] of cases.entries()) {
    const playwrightRequestId = `request-${candidate.name.replaceAll(' ', '-')}`;
    const fault = {
      id: `injected-${index + 1}`,
      ...ownedFaultTarget(target, { method: 'GET', path: candidate.path }),
      matched: false,
      action: 'abort',
      responseStatus: null,
    };
    captureInjectedFaultRequest(fault, request({
      url: () => candidate.rawURL,
      method: () => 'GET',
    }), playwrightRequestId);

    const result = applyInjectedFaultPolicy([
      {
        kind: 'network', method: 'GET', url: target.uiUrl + candidate.path,
        rawURL: candidate.rawURL, playwrightRequestId, errorText: 'net::ERR_FAILED',
      },
      {
        kind: 'console-error', text: 'Failed to load resource: net::ERR_FAILED',
        location: target.uiUrl + candidate.path, rawLocation: candidate.rawURL,
      },
      {
        kind: 'network', method: 'GET', url: target.uiUrl + candidate.path,
        rawURL: candidate.rawURL, playwrightRequestId: `${playwrightRequestId}-other`, errorText: 'net::ERR_FAILED',
      },
      {
        kind: 'network', method: 'GET', url: target.uiUrl + candidate.path,
        rawURL: `${candidate.rawURL}&other=1`, playwrightRequestId, errorText: 'net::ERR_FAILED',
      },
      {
        kind: 'network', method: 'GET', url: target.uiUrl + candidate.path,
        rawURL: candidate.rawURL, playwrightRequestId: `${playwrightRequestId}-aborted`, errorText: 'net::ERR_ABORTED',
      },
    ], [fault]);

    assert.equal(fault.rawURL, candidate.rawURL, `${candidate.name} fault capture keeps the exact unsanitized URL`);
    assert.equal(result[0].injectedFault, true, `${candidate.name} exact failed request is expected`);
    assert.equal(result[1].injectedFault, true, `${candidate.name} exact matching console record is paired once`);
    assert.equal(result[2].injectedFault, undefined, `${candidate.name} same-URL request with another identity stays fatal`);
    assert.equal(result[3].injectedFault, undefined, `${candidate.name} same-identity request with another URL stays fatal`);
    assert.equal(result[4].injectedFault, undefined, `${candidate.name} ERR_ABORTED is not treated as the injected ERR_FAILED`);
  }
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


test('expected HTTP console handling consumes only one console record for one exact captured response', () => {
  const path = '/api/v1/projects/owned/explorers/editor/authoring/v2/construction-proposals';
  const capturedEntry = {
    origin: target.uiUrl,
    path,
    query: {},
    status: 422,
    method: 'POST',
    browserRequestId: 'playwright-expected',
  };
  const duplicateRequest = { ...capturedEntry, browserRequestId: 'playwright-duplicate' };
  const message = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const fixtureDiagnostic = {
    kind: 'console-error',
    text: message,
    location: target.uiUrl + path,
    rawLocation: target.uiUrl + path,
  };
  const reportError = {
    kind: 'console',
    message,
    location: target.uiUrl + path,
  };
  const nativeRequests = [capturedEntry];
  const diagnostics = [fixtureDiagnostic];
  const errors = [reportError];

  const match = matchExpectedHttpConsole({ capturedEntry, nativeRequests, diagnostics, errors });
  assert.deepEqual(match, {
    fixtureDiagnostic,
    reportError,
    status: 422,
    message,
    location: target.uiUrl + path,
  });

  assert.equal(matchExpectedHttpConsole({
    capturedEntry,
    nativeRequests: [capturedEntry, duplicateRequest],
    diagnostics,
    errors,
  }), undefined, 'ambiguous same-route responses must remain unexpected');
  assert.equal(matchExpectedHttpConsole({
    capturedEntry,
    nativeRequests,
    diagnostics: [{ ...fixtureDiagnostic, text: 'Failed to load resource: status 422' }],
    errors: [],
  }), undefined, 'non-Chromium resource messages must remain unexpected');
  assert.equal(matchExpectedHttpConsole({
    capturedEntry,
    nativeRequests,
    diagnostics: [{ ...fixtureDiagnostic, location: target.uiUrl + path + '?other=1', rawLocation: target.uiUrl + path + '?other=1' }],
    errors: [],
  }), undefined, 'a different request URL must remain unexpected');
  assert.equal(matchExpectedHttpConsole({
    capturedEntry,
    nativeRequests,
    diagnostics: [fixtureDiagnostic, { ...fixtureDiagnostic }],
    errors: [],
  }), undefined, 'duplicate console records must remain unexpected');
});

test('only a source preview abort owned by CREATE_TABLE and followed by its selected APPEND proposal is canceled', () => {
  const previewPath = '/api/v1/projects/owned/explorers/editor/authoring/v2/preview';
  const proposalPath = previewPath.replace(/\/authoring\/v2\/preview$/, '/authoring/v2/construction-proposals');
  const createAction = { id: 'action-create', name: 'open native Combine and create a separate empty target' };
  const proposalAction = { id: 'action-proposal', name: 'configure Group→Pivot APPEND and render its selected proposal' };
  const proposal = {
    captureId: 'proposal-capture-1', outputId: 'target-output', path: proposalPath,
    url: 'http://127.0.0.1:30008' + proposalPath, status: 200,
    startedAtMonotonicMs: 200, responseAtMonotonicMs: 300,
    actionAtStartId: proposalAction.id, actionAtStartName: proposalAction.name,
    responseMatchesRequest: true, currentDraftCASBound: true,
    responsePreviewOutputId: 'target-output', responsePreviewReceiptId: 'target-receipt',
    domOutputId: 'target-output', domSelectedOutputId: 'target-output', domReceiptId: 'target-receipt',
    previewStatus: 'READY', userVisiblePreviewError: false,
  };
  const failed = {
    id: 'preview-request-2', networkRequestId: 'source-preview-request',
    kind: 'preview', outputId: 'source-output', receiptId: 'source-receipt',
    errorText: 'net::ERR_ABORTED', startedAtMonotonicMs: 100, failedAtMonotonicMs: 120,
    receiptBinding: {
      snapshotToken: 'source-snapshot', draftVersion: 5, draftDigest: 'source-digest',
      responseSnapshotToken: 'source-snapshot', outputIds: ['source-output'],
      reconciledAtMonotonicMs: 90,
    },
    receiptBindingMatchesOutput: true,
    actionAtStart: { ...createAction, startedAtMonotonicMs: 110 },
    visibleAfterFailure: { selectedOutputId: 'target-output', userVisiblePreviewError: false },
  };
  const networkRecord = () => ({
    kind: 'network', method: 'POST', url: 'http://127.0.0.1:30008' + previewPath,
    rawURL: 'http://127.0.0.1:30008' + previewPath, errorText: 'net::ERR_ABORTED',
    requestDetails: { requestId: 'source-preview-request', outputId: 'source-output', receiptId: 'source-receipt' },
    requestTimeline: { action: { id: 'action-create' } },
  });
  const createEvidence = () => ({
    lifecycle: {
      scope: {
        projectId: 'owned', explorerId: 'editor', fixtureGeneration: 'generation-1',
        origins: ['http://127.0.0.1:30008'], paths: {
          preview: previewPath,
          reconcile: previewPath.replace(/\/preview$/, '/reconcile'),
        },
      },
      droppedRequests: 0,
      droppedEvents: 0,
      captureStartedAtMonotonicMs: 0,
      captureStoppedAtMonotonicMs: 400,
      abortedPreviews: [structuredClone(failed)],
    },
    network: [networkRecord()],
    actions: [
      { ...createAction, status: 'passed' },
      { ...proposalAction, status: 'passed' },
    ],
    targetBindings: [{ outputId: 'target-output', createCommandCAS: {
      snapshotToken: 'source-snapshot', draftVersion: 5, draftDigest: 'source-digest',
    } }],
    successorRequests: [structuredClone(proposal)],
    assertions: [
      {
        status: 'passed',
        name: 'native Combine uses CREATE_TABLE to make an empty rooted target without authoring a source',
        evidence: { outputId: 'target-output' },
      },
      {
        status: 'passed',
        name: 'automatic Combine proposal is bound to this exact draft CAS and UI proxy scope',
        evidence: {
          captureId: proposal.captureId, responseBound: true,
          scopeChecks: { route: true, snapshot: true }, responseChecks: { response: true, selected: true },
        },
      },
    ],
  });
  const classify = (evidence) => markExpectedOwnedPreviewAborts({
    ...evidence, sourceOutputId: 'source-output',
  });

  const accepted = createEvidence();
  assert.deepEqual(classify(accepted), [{
    failedRequestId: 'preview-request-2', successorRequestId: 'proposal-capture-1', outputId: 'target-output',
  }]);
  assert.equal(accepted.network[0].canceled, true);
  assert.equal(accepted.network[0].errorText, 'net::ERR_ABORTED', 'classification preserves the original browser failure');
  assert.equal(classifyNetworkRecord(accepted.network[0]), 'cancelled');
  const reportWithoutBrowserRequestId = createEvidence();
  reportWithoutBrowserRequestId.lifecycle.abortedPreviews[0].networkRequestId = null;
  reportWithoutBrowserRequestId.network[0].requestDetails.requestId = null;
  assert.equal(classify(reportWithoutBrowserRequestId).length, 1,
    'the retained wave138 request has no browser request ID, so unique route/output/receipt/action evidence remains sufficient');

  const rejectedCases = [
    ['no selected APPEND proposal', (e) => { e.successorRequests = []; }],
    ['later proposal did not reach READY', (e) => { e.successorRequests[0].previewStatus = 'FAILED'; }],
    ['proposal begins before abort', (e) => { e.successorRequests[0].startedAtMonotonicMs = 119; }],
    ['proposal response is outside collector interval', (e) => { e.successorRequests[0].responseAtMonotonicMs = 401; }],
    ['wrong proposal route', (e) => { e.successorRequests[0].path = previewPath; }],
    ['proposal from an unowned origin', (e) => { e.successorRequests[0].url = 'http://elsewhere.invalid' + proposalPath; }],
    ['visible preview error', (e) => { e.successorRequests[0].userVisiblePreviewError = true; }],
    ['different selected table', (e) => { e.successorRequests[0].domSelectedOutputId = 'other-output'; }],
    ['selected proposal has a different preview receipt', (e) => { e.successorRequests[0].domReceiptId = 'other-receipt'; }],
    ['proposal request/response CAS is not bound', (e) => { e.successorRequests[0].currentDraftCASBound = false; }],
    ['failed source receipt binding', (e) => { e.lifecycle.abortedPreviews[0].receiptBindingMatchesOutput = false; }],
    ['source receipt was reconciled after its preview request', (e) => { e.lifecycle.abortedPreviews[0].receiptBinding.reconciledAtMonotonicMs = 101; }],
    ['unrelated preview abort', (e) => { e.network[0].requestDetails.outputId = 'unrelated-output'; }],
    ['wrong request receipt', (e) => { e.network[0].requestDetails.receiptId = 'other-receipt'; }],
    ['wrong network request ID', (e) => { e.network[0].requestDetails.requestId = 'different-request'; }],
    ['CREATE_TABLE used a different draft CAS', (e) => { e.targetBindings[0].createCommandCAS.draftVersion += 1; }],
    ['proposal from another target', (e) => { e.successorRequests[0].outputId = 'other-output'; }],
    ['proposal has a different action owner', (e) => { e.successorRequests[0].actionAtStartId = 'action-unknown'; }],
    ['failed CREATE_TABLE action', (e) => { e.actions[0].status = 'failed'; }],
    ['failed CREATE_TABLE assertion', (e) => { e.assertions[0].status = 'failed'; }],
    ['failed selected proposal action', (e) => { e.actions[1].status = 'failed'; }],
    ['failed selected proposal assertion', (e) => { e.assertions[1].status = 'failed'; }],
    ['ambiguous network match', (e) => { e.network.push(networkRecord()); }],
    ['timed-out or missing diagnostic reads', (e) => {
      e.lifecycle.incompleteEvidence = true;
      e.lifecycle.timedOutReads = [{ requestId: 'preview-request-2', kind: 'reconcile-response-body' }];
    }],
    ['dropped lifecycle evidence', (e) => { e.lifecycle.droppedRequests = 1; }],
    ['dropped event evidence', (e) => { e.lifecycle.droppedEvents = 1; }],
  ];
  for (const [reason, mutate] of rejectedCases) {
    const rejected = createEvidence();
    mutate(rejected);
    assert.deepEqual(classify(rejected), [], reason);
    assert.equal(rejected.network[0].canceled, undefined, reason);
    assert.equal(rejected.network[0].errorText, 'net::ERR_ABORTED', reason);
    assert.equal(classifyNetworkRecord(rejected.network[0]), 'unexpected-error', reason);
  }
});
