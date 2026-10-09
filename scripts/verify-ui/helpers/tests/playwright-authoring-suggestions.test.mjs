import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  abortSuggestionRouteAfterUserAction,
  builderAuthoringSuggestionsWorkflow,
  classifySuggestionDiagnostics,
  isOwnedRootSuggestionRequest,
  waitForOwnedRootSuggestionRequest,
} from '../playwright-authoring-suggestions.mjs';

const target = {
  uiUrl: 'http://127.0.0.1:30008',
  fixtureProject: 'loom_dev_verify_run-1',
};
const explorer = 'explorer-new';
const suggestionsURL = `${target.uiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}` +
  `/explorers/${encodeURIComponent(explorer)}/authoring/v2/suggestions`;

const injectedSuggestionFailure = () => ({
  kind: 'network',
  method: 'POST',
  url: suggestionsURL,
  rawURL: suggestionsURL,
  errorText: 'net::ERR_FAILED',
  injectedFault: true,
  injectedAction: 'abort',
  injectedRequestId: 'suggestions-run-1',
  playwrightRequestId: 'playwright-request-9',
  requestDetails: { requestId: 'suggestions-run-1' },
});

test('suggestion abort classifier suppresses one exact correlated console diagnostic only', () => {
  const failure = injectedSuggestionFailure();
  const exactAbortConsole = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const samePathUnrelatedConsole = {
    kind: 'console-error',
    text: 'Unrelated renderer error',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const wrongPathSameMessage = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: `${target.uiUrl}/api/v1/projects/other/explorers/${explorer}/authoring/v2/suggestions`,
    rawLocation: `${target.uiUrl}/api/v1/projects/other/explorers/${explorer}/authoring/v2/suggestions`,
  };
  const unmatchedAbort = {
    kind: 'network', method: 'POST', url: suggestionsURL, rawURL: suggestionsURL,
    errorText: 'net::ERR_ABORTED', requestDetails: { requestId: 'unmatched' },
  };
  const report = {
    target: { explorer },
    network: [failure, exactAbortConsole, samePathUnrelatedConsole, wrongPathSameMessage,
      unmatchedAbort],
  };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, exactAbortConsole);
  assert.equal(exactAbortConsole.kind, 'network');
  assert.equal(exactAbortConsole.observedAs, 'console-error');
  assert.equal(exactAbortConsole.injectedRequestId, failure.injectedRequestId);
  assert.deepEqual(result.unexpected, [samePathUnrelatedConsole, wrongPathSameMessage,
    unmatchedAbort]);
});

test('suggestion abort classifier fails closed when the exact console diagnostic is duplicated', () => {
  const failure = injectedSuggestionFailure();
  const exact = () => ({
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  });
  const first = exact();
  const duplicate = exact();
  const report = { target: { explorer }, network: [failure, first, duplicate] };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, undefined);
  assert.deepEqual(result.unexpected, [first, duplicate]);
});

test('suggestion abort classifier refuses console attribution when another failed request shares the URL', () => {
  const failure = injectedSuggestionFailure();
  const exactAbortConsole = {
    kind: 'console-error',
    text: 'Failed to load resource: net::ERR_FAILED',
    location: suggestionsURL,
    rawLocation: suggestionsURL,
  };
  const competingRequest = {
    ...injectedSuggestionFailure(),
    injectedRequestId: 'other-request',
    playwrightRequestId: 'playwright-request-other',
    requestDetails: { requestId: 'other-request' },
  };
  const report = { target: { explorer }, network: [failure, competingRequest, exactAbortConsole] };

  const result = classifySuggestionDiagnostics(report, target, failure);

  assert.equal(result.expectedAbortConsoleError, undefined);
  assert.deepEqual(result.competingSameRequestURLFailures, [competingRequest]);
  assert.deepEqual(result.unexpected, [competingRequest, exactAbortConsole]);
});

test('suggestion fault ownership is limited to the active root request and abort waits for the native action', async () => {
  const request = {
    method: 'POST',
    url: suggestionsURL,
    requestId: 'suggestions-base',
    body: { snapshotToken: 'snapshot-current', nodeId: 'Patient' },
  };
  const identity = {
    expectedURL: suggestionsURL,
    rootNodeId: 'Patient',
    snapshotToken: 'snapshot-current',
  };
  assert.equal(isOwnedRootSuggestionRequest({ ...request, ...identity }), true);
  assert.equal(isOwnedRootSuggestionRequest({ ...request, ...identity, rootNodeId: 'Observation' }), false);
  assert.equal(isOwnedRootSuggestionRequest({ ...request, ...identity, snapshotToken: 'snapshot-old' }), false);
  assert.equal(isOwnedRootSuggestionRequest({ ...request, ...identity,
    url: suggestionsURL.replace(encodeURIComponent(explorer), 'other-explorer') }), false);
  assert.equal(isOwnedRootSuggestionRequest({ ...request, ...identity,
    requestId: 'first-table-suggestions-1' }), false);

  let releaseAction;
  const actionCompleted = new Promise(resolve => { releaseAction = resolve; });
  const events = [];
  const route = { abort: async reason => { events.push(`abort:${reason}`); } };
  const pendingAbort = abortSuggestionRouteAfterUserAction(route, actionCompleted);
  await Promise.resolve();
  assert.deepEqual(events, []);
  events.push('open Raw FHIR fields');
  releaseAction(true);
  assert.equal(await pendingAbort, true);
  assert.deepEqual(events, ['open Raw FHIR fields', 'abort:failed']);
});

test('missing owned request and missing native action fail within bounds and release the held route', async () => {
  const unrelated = {
    method: 'POST',
    url: suggestionsURL,
    requestId: 'suggestions-observation',
    body: { snapshotToken: 'snapshot-current', nodeId: 'Observation' },
  };
  assert.equal(isOwnedRootSuggestionRequest({ ...unrelated,
    expectedURL: suggestionsURL, rootNodeId: 'Patient', snapshotToken: 'snapshot-current' }), false);
  await assert.rejects(
    waitForOwnedRootSuggestionRequest(new Promise(() => {}), 5),
    /Timed out waiting for the owned root suggestion request\./,
  );

  const events = [];
  const route = { abort: async reason => { events.push(`abort:${reason}`); } };
  const actionNeverCompleted = new Promise(() => {});
  assert.equal(await abortSuggestionRouteAfterUserAction(route, actionNeverCompleted, 5), false);
  assert.deepEqual(events, ['abort:failed']);
});

test('production suggestions workflow retains the intercepted request through its post-action checks', async () => {
  const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture', import.meta.url));
  const workflowTarget = {
    uiUrl: target.uiUrl,
    fixtureProject: target.fixtureProject,
    fixtureGeneration: 'devloop-v1',
    fixtureDir,
    bootstrapExplorerId: 'explorer-bootstrap',
  };
  const report = { target: {}, network: [], checks: [], actions: [], timings: {} };
  const registeredRoutes = [];
  const responseWaiters = [];
  const rowIds = ['dev-patient-001', 'dev-patient-002'];
  const stopAtRetry = new Error('production workflow reached retry after using the captured request');
  let lazyRoutePromise;
  let failedAlertWaits = 0;

  const request = ({ method, url, requestId, body }) => ({
    method: () => method,
    url: () => url,
    headers: () => ({ 'x-request-id': requestId }),
    postDataJSON: () => body,
  });
  const response = (req, body = {}) => ({
    request: () => req,
    url: () => req.url(),
    status: () => 200,
    ok: () => true,
    json: async () => body,
  });
  const emitResponse = value => {
    const waiterIndex = responseWaiters.findIndex(waiter => waiter.predicate(value));
    if (waiterIndex === -1) return;
    responseWaiters.splice(waiterIndex, 1)[0].resolve(value);
  };
  const routeFor = (req, { body = {}, onAbort } = {}) => ({
    request: () => req,
    fetch: async () => response(req, body),
    fulfill: async ({ response: original }) => emitResponse(original),
    continue: async () => emitResponse(response(req)),
    abort: async () => onAbort?.(),
  });
  const dispatch = async req => {
    const url = new URL(req.url());
    const match = registeredRoutes.find(([matcher]) => matcher(url));
    assert(match, `No production route matched ${url.href}`);
    await match[1](routeFor(req, {
      body: { catalog: { candidates: [{ id: 'candidate-1' }] } },
      onAbort: () => report.network.push({
        kind: 'network', method: req.method(), url: req.url(), rawURL: req.url(),
        errorText: 'net::ERR_FAILED', playwrightRequestId: 'playwright-lazy-1',
        requestDetails: { requestId: req.headers()['x-request-id'] },
      }),
    }));
  };
  const suggestionURL = `${target.uiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}` +
    '/explorers/explorer-new/authoring/v2/suggestions';
  const builderURL = `${target.uiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}` +
    '/explorers/explorer-new/authoring/v2/builder';
  const startLazyRequest = () => {
    if (lazyRoutePromise) return;
    lazyRoutePromise = dispatch(request({
      method: 'POST', url: suggestionURL, requestId: 'suggestions-lazy-1',
      body: { nodeId: 'Patient', snapshotToken: 'snapshot-1' },
    }));
  };
  const locator = label => ({
    count: async () => 1,
    click: async () => {},
    fill: async () => {},
    check: async () => {},
    waitFor: async options => {
      if (label === 'builder-suggestions-error' && options?.state === 'visible') failedAlertWaits += 1;
    },
    isVisible: async () => true,
    isEnabled: async () => true,
    isEditable: async () => true,
    inputValue: async () => 'explorer-new',
    toString: () => String(label),
  });
  const previewRows = [
    { getByRole: () => ({ first: () => ({ innerText: async () => 'header' }) }) },
    ...rowIds.map(id => ({ getByRole: () => ({ first: () => ({ innerText: async () => id }) }) })),
  ];
  const previewScroll = {
    getByRole: role => ({ all: async () => {
      assert.equal(role, 'row');
      startLazyRequest();
      return previewRows;
    } }),
  };
  const page = {
    setDefaultTimeout: () => {},
    setDefaultNavigationTimeout: () => {},
    goto: async () => {},
    route: async (matcher, handler) => { registeredRoutes.push([matcher, handler]); },
    waitForResponse: predicate => new Promise(resolve => responseWaiters.push({ predicate, resolve })),
    waitForFunction: async () => ({ jsonValue: async () => ({}) }),
    getByText: name => locator(name),
    getByRole: (role, options) => locator(`${role}:${options?.name}`),
    getByTestId: testId => testId === 'preview-table-scroll' ? previewScroll : locator(testId),
    locator: selector => locator(selector),
  };
  const action = async (name, _target, perform, options = {}) => {
    await perform();
    if (name === 'create blank Explorer') {
      await dispatch(request({ method: 'GET', url: builderURL, requestId: 'builder-new-1' }));
    } else if (name === 'choose Patient rows') {
      await dispatch(request({
        method: 'POST', url: suggestionURL, requestId: 'first-table-suggestions-1',
        body: { nodeId: 'Patient', snapshotToken: 'snapshot-1' },
      }));
    } else if (name === 'open Raw FHIR fields') {
      await options.after?.();
      await lazyRoutePromise;
    } else if (name === 'retry finding columns') {
      throw stopAtRetry;
    }
  };

  await assert.rejects(
    builderAuthoringSuggestionsWorkflow({ page, report, action, check: () => {} }, {
      target: workflowTarget,
      runID: 'workflow-binding-regression',
    }),
    error => error === stopAtRetry,
  );
  assert.equal(failedAlertWaits, 1);
  assert.equal(report.network.length, 1);
});
