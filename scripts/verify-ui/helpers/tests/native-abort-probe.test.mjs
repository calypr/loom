import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  createNativeAbortProbeSource,
  flushNativeAbortProbeEvents,
  installNativeAbortProbe,
  nativeAbortNetworkFailureClock,
  nativeAbortProbeEvidenceForRequest,
  nativeAbortSignalObservationForRequest,
} from '../native-abort-probe.mjs';
import { classifyExpectedOwnedCancellation, nativeReadRequestMatchesExpectedScope } from '../native-request-ownership.mjs';
import { createReport, finishReport, recordCheck } from '../report.mjs';
import { createFixtureNativeRequestLedger, finalizeFixtureNativeRequestReport } from '../native-request-ledger.mjs';
import { assertRepeatedEmptyNativeRequestLedgerComplete } from '../../workflows/builder-repeated.mjs';

const project = 'loom_dev_test';
const explorer = 'abort-probe-test';
const apiOrigin = 'http://127.0.0.1:8188';
const apiPathFor = (scopeProject, scopeExplorer, endpoint) =>
  `/api/v1/projects/${encodeURIComponent(scopeProject)}/explorers/${encodeURIComponent(scopeExplorer)}/authoring/v2/${endpoint}`;
const apiPath = (endpoint) => apiPathFor(project, explorer, endpoint);
const requestId = (prefix, tail) => `${prefix}${tail}-0000-4000-8000-000000000000`;
const withCDPIdentity = (entry) => ({
  ...entry,
  origin: entry.origin ?? apiOrigin,
  cdpRequestId: entry.cdpRequestId ?? `cdp-${entry.requestId}`,
  cdpRequestMatchCount: entry.cdpRequestMatchCount ?? 1,
});

const matchesSelector = (element, selector) => {
  if (selector === 'button') return element.tagName === 'BUTTON';
  if (selector === 'button[aria-pressed="true"]') return element.tagName === 'BUTTON' && element.getAttribute('aria-pressed') === 'true';
  if (selector === '[aria-label="Column types"]') return element.getAttribute('aria-label') === 'Column types';
  if (selector === '[role="dialog"][aria-label="Row definition settings"]') {
    return element.getAttribute('role') === 'dialog' && element.getAttribute('aria-label') === 'Row definition settings';
  }
  if (selector === '[data-testid="frame-source-panel"]') return element.getAttribute('data-testid') === 'frame-source-panel';
  if (selector === '[data-testid^="frame-categories-"]') return element.getAttribute('data-testid')?.startsWith('frame-categories-') === true;
  if (selector === '[data-testid="paired-column-suggestions"]') return element.getAttribute('data-testid') === 'paired-column-suggestions';
  if (selector === '[data-testid="construction-related-expand-editor"]') return element.getAttribute('data-testid') === 'construction-related-expand-editor';
  if (selector === '[data-testid^="construction-table-"][aria-current="page"]') {
    return element.getAttribute('data-testid')?.startsWith('construction-table-') === true &&
      element.getAttribute('aria-current') === 'page';
  }
  if (selector === '[aria-label="Starting collection"]') return element.getAttribute('aria-label') === 'Starting collection';
  if (selector === '#feature-catalog-search') return element.getAttribute('id') === 'feature-catalog-search';
  return false;
};

const fakeNode = ({ tagName = 'DIV', attributes = {}, text = '', parent, connected = true } = {}) => {
  let ownConnected = connected;
  const element = {
    tagName,
    attributes,
    textContent: text,
    parentElement: parent,
    children: [],
    get isConnected() { return ownConnected && (!parent || parent.isConnected); },
    set isConnected(value) { ownConnected = value; },
    getAttribute(name) { return attributes[name] ?? null; },
    closest(selector) {
      for (let current = element; current; current = current.parentElement) {
        if (matchesSelector(current, selector)) return current;
      }
      return null;
    },
    querySelectorAll(selector) {
      const matches = [];
      const visit = (current) => {
        for (const child of current.children) {
          if (matchesSelector(child, selector)) matches.push(child);
          visit(child);
        }
      };
      visit(element);
      return matches;
    },
  };
  parent?.children.push(element);
  return element;
};

const codedTabDom = () => {
  const group = fakeNode({ attributes: { 'aria-label': 'Column types' } });
  const coded = fakeNode({ tagName: 'BUTTON', attributes: { 'aria-pressed': 'true' }, text: 'Coded values', parent: group });
  const fields = fakeNode({ tagName: 'BUTTON', attributes: { 'aria-pressed': 'false' }, text: 'Fields and related data', parent: group });
  const owner = fakeNode({ attributes: { 'data-testid': 'paired-column-suggestions' } });
  return {
    nodes: [group, coded, fields, owner],
    owner,
    group,
    coded,
    fields,
    selectFields() {
      coded.attributes['aria-pressed'] = 'false';
      fields.attributes['aria-pressed'] = 'true';
      owner.isConnected = false;
    },
  };
};

const startingCollectionDom = () => {
  const dialog = fakeNode({ attributes: { role: 'dialog', 'aria-label': 'Row definition settings' } });
  const owner = fakeNode({ attributes: { 'aria-label': 'Starting collection' }, parent: dialog });
  const groupAction = fakeNode({ tagName: 'BUTTON', attributes: {
    'data-testid': 'construction-action-group-rows',
  }, text: 'Combine rows into groups', parent: dialog });
  return { nodes: [dialog, owner, groupAction], dialog, owner, groupAction };
};

const relatedExpandDom = () => {
  const owner = fakeNode({ attributes: {
    'data-testid': 'construction-related-expand-editor',
    'data-related-stage-id': 'related-stage-1',
    'data-related-output-id': 'output-1',
  } });
  const apply = fakeNode({ tagName: 'BUTTON', attributes: { 'data-testid': 'construction-apply-proposal' }, text: 'Apply' });
  return { nodes: [owner, apply], owner, apply };
};

const featureCatalogDom = (label = 'Add 1 selected feature') => {
  const owner = fakeNode({ attributes: { id: 'feature-catalog-search' } });
  const add = fakeNode({ tagName: 'BUTTON', text: label });
  return { nodes: [owner, add], owner, add };
};

const constructionCapabilitiesDom = (outputId = 'out_fcb5bc77cf3b4ac9b41498b4') => {
  const table = fakeNode({ tagName: 'BUTTON', attributes: {
    'data-testid': `construction-table-${outputId}`,
    'aria-current': 'page',
    'aria-pressed': 'true',
  } });
  return { nodes: [table], table };
};

const startProbe = ({ dom, origin = apiOrigin, scopeProject = project, scopeExplorer = explorer, source, bindingCallback, installSource = true } = {}) => {
  const events = [];
  const listeners = new Map();
  let now = 100;
  class FakeDate extends Date { static now() { return now; } }
  class FakeMutationObserver {
    constructor(callback) { this.callback = callback; }
    observe() {}
  }
  class FakeAbortController {
    constructor() {
      const listeners = new Set();
      this.signal = {
        aborted: false,
        addEventListener: (_type, listener) => listeners.add(listener),
      };
      this.listeners = listeners;
    }
    abort() {
      this.signal.aborted = true;
      for (const listener of this.listeners) listener();
    }
  }
  class FakeHeaders {
    constructor(input) {
      this.values = new Map();
      const entries = input instanceof FakeHeaders ? [...input.values] : Object.entries(input ?? {});
      for (const [name, value] of entries) this.set(name, value);
    }
    get(name) { return this.values.get(String(name).toLowerCase()) ?? null; }
    set(name, value) { this.values.set(String(name).toLowerCase(), String(value)); }
  }
  const fetchCalls = [];
  const document = {
    addEventListener: (type, listener) => listeners.set(type, listener),
    querySelectorAll: (selector) => (dom?.nodes ?? []).filter((element) => matchesSelector(element, selector)),
  };
  const sandbox = {
    AbortController: FakeAbortController,
    URL,
    Date: FakeDate,
    Error,
    Object,
    WeakMap,
    Set,
    Reflect,
    JSON,
    Number,
    String,
    Promise,
    Headers: FakeHeaders,
    crypto: { randomUUID: () => '12345678-1234-4123-8123-123456789abc' },
    location: { href: `${origin}/` },
    document,
    MutationObserver: FakeMutationObserver,
    fetch: (...args) => {
      fetchCalls.push(args);
      const signal = args[1]?.signal;
      const promise = new Promise((_resolve, reject) => {
        if (signal?.aborted) reject(new Error('The operation was aborted.'));
        else signal?.addEventListener?.('abort', () => reject(new Error('The operation was aborted.')));
      });
      promise.catch(() => {});
      return promise;
    },
    __loomNativeAbortProbeBinding: (payload) => {
      events.push(JSON.parse(payload));
      bindingCallback?.({}, payload);
    },
  };
  sandbox.globalThis = sandbox;
  const probeSource = source ?? createNativeAbortProbeSource({ project: scopeProject, explorer: scopeExplorer, apiOrigin: origin });
  assert.doesNotThrow(() => new Function(probeSource));
  const installProbe = () => vm.runInNewContext(probeSource, sandbox);
  if (installSource) installProbe();
  return { sandbox, events, listeners, fetchCalls, advanceTime: (value) => { now = value; }, installProbe };
};

test('probe links an exact scoped authoring fetch to the AbortSignal that was canceled', () => {
  const { sandbox, events } = startProbe();
  const controller = new sandbox.AbortController();
  const id = requestId('paired-column-choices-', '11111111');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('construction-choices')}`, {
    method: 'POST',
    headers: { 'X-Request-ID': id, Authorization: 'must-not-be-captured' },
    body: JSON.stringify({ snapshotToken: 'must-not-be-captured' }),
    signal: controller.signal,
  });
  controller.abort();

  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert(event, 'probe did not capture AbortController.abort()');
  assert.equal(event.signalWasAlreadyAborted, false);
  assert.deepEqual(event.requests.map(({ requestId, path, method, endpoint }) => ({ requestId, path, method, endpoint })), [
    { requestId: id, path: apiPath('construction-choices'), method: 'POST', endpoint: 'construction-choices' },
  ]);
  assert(!JSON.stringify(event).includes('Authorization'));
  assert(!JSON.stringify(event).includes('must-not-be-captured'));
});

test('pre-navigation project route probe captures a bound Builder client and keeps request scope exact', () => {
  const lateProbe = startProbe({ scopeExplorer: { mode: 'project-routes' }, installSource: false });
  const clientFetchBoundBeforeProbe = lateProbe.sandbox.fetch.bind(lateProbe.sandbox);
  lateProbe.installProbe();
  const lateController = new lateProbe.sandbox.AbortController();
  const lateID = requestId('schema-fields-', '33333333');
  clientFetchBoundBeforeProbe(`http://127.0.0.1:8188${apiPathFor(project, 'fresh-explorer-created-by-this-run', 'schema-fields')}`, {
    method: 'POST', headers: { 'X-Request-ID': lateID }, signal: lateController.signal,
  });
  lateController.abort();
  assert.deepEqual(lateProbe.events.find((event) => event.kind === 'abort-controller-call').requests, [],
    'a Builder client that bound fetch before the probe bypasses later fetch wrapping');

  const dynamicExplorerProbe = startProbe({ scopeExplorer: { mode: 'project-routes' } });
  // Builder's Loom client binds global fetch when the client is constructed.
  // This models page.addInitScript running before the document constructs it.
  const builderClientFetch = dynamicExplorerProbe.sandbox.fetch.bind(dynamicExplorerProbe.sandbox);
  const controller = new dynamicExplorerProbe.sandbox.AbortController();
  const id = requestId('schema-fields-', '11111111');
  const path = apiPathFor(project, 'fresh-explorer-created-by-this-run', 'schema-fields');

  builderClientFetch(`http://127.0.0.1:8188${apiPathFor('loom_dev_other_run', 'wrong-project-explorer', 'schema-fields')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  builderClientFetch(`http://127.0.0.1:30009${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  builderClientFetch(`http://127.0.0.1:8188${apiPathFor(project, 'another-explorer-in-same-run', 'schema-fields')}`, {
    method: 'POST', headers: { 'X-Request-ID': 'schema-fields-22222222-0000-4000-8000-000000000000' }, signal: controller.signal,
  });
  builderClientFetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  controller.abort();

  const abortEvent = dynamicExplorerProbe.events.find((event) => event.kind === 'abort-controller-call');
  assert.deepEqual(abortEvent.requests.map(({ requestId, origin: requestOrigin, path: requestPath, explorer }) =>
    ({ requestId, origin: requestOrigin, path: requestPath, explorer })), [
    {
      requestId: 'schema-fields-22222222-0000-4000-8000-000000000000',
      origin: apiOrigin,
      path: apiPathFor(project, 'another-explorer-in-same-run', 'schema-fields'),
      explorer: 'another-explorer-in-same-run',
    },
    { requestId: id, origin: apiOrigin, path, explorer: 'fresh-explorer-created-by-this-run' },
  ], 'wrong origin and project identities are excluded while each same-project Explorer retains its own exact path');

  const nativeEntry = {
    requestId: 'cdp-request-44444444', requestDetails: { requestId: id }, origin: apiOrigin, path, method: 'POST',
    requestIdentityMatchCount: 1, cdpRequestId: 'cdp-request-44444444', cdpRequestMatchCount: 1,
    requestTimestamp: 1, requestWallTime: 1, loadingFailed: { timestamp: 1.127, at: 1127 },
    terminalEvent: 'requestfailed', failure: 'net::ERR_ABORTED',
  };
  assert.equal(nativeAbortSignalObservationForRequest(nativeEntry, dynamicExplorerProbe.events).exactRequestSignalCorrelation, true);
  assert.equal(nativeAbortSignalObservationForRequest({
    ...nativeEntry,
    path: apiPathFor(project, 'another-explorer-in-same-run', 'schema-fields'),
  }, dynamicExplorerProbe.events).exactRequestSignalCorrelation, false,
  'an otherwise matching request ID cannot be attributed to another Explorer path');
});

test('pre-navigation probe correlates an exact fetch that uses a signal aborted before fetch', () => {
  const probe = startProbe({ scopeExplorer: { mode: 'project-routes' } });
  const clientFetchBoundBeforeNavigation = probe.sandbox.fetch.bind(probe.sandbox);
  const controller = new probe.sandbox.AbortController();
  const id = requestId('schema-fields-', '44444444');
  const path = apiPathFor(project, 'fresh-explorer-created-by-this-run', 'schema-fields');

  controller.abort();
  const abortEvent = probe.events.find((event) => event.kind === 'abort-controller-call');
  assert(abortEvent, 'the signal abort event must be retained even before its request starts');
  assert.equal(abortEvent.signalWasAlreadyAborted, false);
  assert.deepEqual(abortEvent.requests, [], 'the request did not exist at AbortController.abort() time');

  probe.advanceTime(127);
  clientFetchBoundBeforeNavigation(`http://127.0.0.1:8188${apiPathFor('loom_dev_other_run', 'wrong-explorer', 'schema-fields')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  clientFetchBoundBeforeNavigation(`http://127.0.0.1:30009${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  clientFetchBoundBeforeNavigation(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });

  const observations = probe.events.filter((event) => event.kind === 'abort-controller-fetch-observed-after-abort');
  assert.equal(observations.length, 1, 'only the exact project and origin request may be retained');
  assert.deepEqual({
    requestId: observations[0].request.requestId,
    origin: observations[0].request.origin,
    path: observations[0].request.path,
    method: observations[0].request.method,
    controllerId: observations[0].controllerId,
    controllerAbortedAt: observations[0].controllerAbortedAt,
    observedAt: observations[0].observedAt,
    signalWasAlreadyAborted: observations[0].signalWasAlreadyAborted,
  }, {
    requestId: id,
    origin: apiOrigin,
    path,
    method: 'POST',
    controllerId: abortEvent.controllerId,
    controllerAbortedAt: abortEvent.abortedAt,
    observedAt: 127,
    signalWasAlreadyAborted: true,
  });

  const nativeEntry = {
    requestId: 'cdp-request-44444444', requestDetails: { requestId: id }, origin: apiOrigin, path, method: 'POST',
    requestIdentityMatchCount: 1, cdpRequestId: 'cdp-request-44444444', cdpRequestMatchCount: 1,
    requestTimestamp: 1, requestWallTime: 1, loadingFailed: { timestamp: 1.127, at: 1127 },
    terminalEvent: 'requestfailed', failure: 'net::ERR_ABORTED',
  };
  const observation = nativeAbortSignalObservationForRequest(nativeEntry, probe.events);
  assert.equal(observation.exactRequestSignalCorrelation, true);
  assert.equal(observation.requestObservedAfterAbort, true);
  assert.equal(observation.signalWasAlreadyAborted, true);
  assert.equal(observation.controllerId, abortEvent.controllerId);
  assert.equal(observation.controllerAbortedAt, abortEvent.abortedAt);
  assert.equal(observation.nativeTerminalObserved, true);
  assert.equal(observation.classificationEffect,
    'diagnostic only; does not make an unfinished native request terminal or expected');
  assert.equal(nativeAbortSignalObservationForRequest(nativeEntry, observations).exactRequestSignalCorrelation, false,
    'a fetch-after-abort record without its matching AbortController call is insufficient attribution');
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(nativeEntry, probe.events), [],
    'an abort-before-fetch correlation must not be reclassified as a proven owner-retirement cancellation');
  assert.equal(nativeAbortSignalObservationForRequest({
    ...nativeEntry,
    path: apiPathFor(project, 'different-explorer', 'schema-fields'),
  }, probe.events).exactRequestSignalCorrelation, false,
  'the same request ID cannot correlate to a different Explorer path');
});

test('one suggestions owner records each exact fetch sharing its signal', () => {
  const { sandbox, events } = startProbe();
  const controller = new sandbox.AbortController();
  for (const suffix of ['11111111', '22222222']) {
    sandbox.fetch(`http://127.0.0.1:8188${apiPath('construction-choices')}`, {
      method: 'POST',
      headers: { 'X-Request-ID': requestId('paired-column-choices-', suffix) },
      signal: controller.signal,
    });
  }
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert.deepEqual(event.requests.map((request) => request.requestId), [
    requestId('paired-column-choices-', '11111111'),
    requestId('paired-column-choices-', '22222222'),
  ]);
});

test('probe scopes the four observed background-read endpoint families and their request-ID owners', () => {
  const { sandbox, events } = startProbe();
  const controller = new sandbox.AbortController();
  const cases = [
    ['semantic-inventory', 'paired-column-inventory-', '11111111'],
    ['population-routes', 'population-routes-', '22222222'],
    ['frame-source-options', 'frame-source-options-', '33333333'],
    ['construction-choices', 'paired-column-choices-', '44444444'],
  ];
  for (const [endpoint, prefix, suffix] of cases) {
    sandbox.fetch(`http://127.0.0.1:8188${apiPath(endpoint)}`, {
      method: 'POST', headers: { 'X-Request-ID': requestId(prefix, suffix) }, signal: controller.signal,
    });
  }
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert.deepEqual(event.requests.map(({ endpoint, requestId }) => ({ endpoint, requestId })), cases.map(([endpoint, prefix, suffix]) => ({
    endpoint, requestId: requestId(prefix, suffix),
  })));
});

test('probe ignores wrong-scope, unknown-endpoint, and unrecognized-request-id fetches', () => {
  const { sandbox, events } = startProbe();
  const controller = new sandbox.AbortController();
  const requests = [
    [`http://127.0.0.1:8188${apiPath('construction-choices').replace(explorer, 'other-explorer')}`, requestId('paired-column-choices-', '11111111')],
    [`http://127.0.0.1:8188${apiPath('commands')}`, requestId('paired-column-choices-', '22222222')],
    [`http://127.0.0.1:8188${apiPath('construction-choices')}`, 'unrecognized-request'],
  ];
  for (const [url, id] of requests) sandbox.fetch(url, { method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal });
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert(event);
  assert.deepEqual(event.requests, []);
});

test('shared installer captures retained schema-fields IDs on the active page and rejects scope or owner mismatches', async () => {
  const uiOrigin = 'http://127.0.0.1:30008';
  const retainedCases = [
    {
      project: 'loom_dev_verify_mv0tf40v-45d6120',
      explorer: 'verify-0v-45d6120-cohort-recode',
      requestId: 'schema-fields-33875306-8d49-4e3d-8baf-e0f0155eee4e',
    },
    {
      project: 'loom_dev_verify_mv0tf56y-4add698',
      explorer: 'verify-6y-4add698-cohort-expand',
      requestId: 'schema-fields-a355125f-9b3f-4854-9582-7dc8aa54cbe8',
    },
  ];
  const installOnCurrentPage = async ({ scope, dom = featureCatalogDom() }) => {
    const calls = [];
    let binding;
    let currentPageSource;
    const browserContext = {
      exposeBinding: async (name, callback) => {
        calls.push(['binding', name]);
        binding = callback;
      },
      addInitScript: async (source) => calls.push(['init', source]),
    };
    const page = {
      context: () => browserContext,
      evaluate: async (source) => {
        calls.push(['current-page', source]);
        currentPageSource = source;
      },
    };
    const report = {};
    const events = await installNativeAbortProbe({
      page,
      report,
      project: scope.project,
      explorer: scope.explorer,
      apiOrigin: `${uiOrigin}/ignored-path`,
    });
    assert.deepEqual(calls.map(([kind]) => kind), ['binding', 'init', 'current-page']);
    assert.equal(calls[0][1], '__loomNativeAbortProbeBinding');
    assert.equal(calls[1][1], currentPageSource);
    assert.equal(events, report.nativeAbortProbeEvents);

    const probe = startProbe({
      dom,
      origin: uiOrigin,
      scopeProject: scope.project,
      scopeExplorer: scope.explorer,
      source: currentPageSource,
      bindingCallback: binding,
    });
    return { probe, report };
  };

  for (const retained of retainedCases) {
    const { probe, report } = await installOnCurrentPage({ scope: retained });
    const path = apiPathFor(retained.project, retained.explorer, 'schema-fields');
    const controller = new probe.sandbox.AbortController();
    probe.sandbox.fetch(`${uiOrigin}${path}`, {
      method: 'POST', headers: { 'X-Request-ID': retained.requestId }, signal: controller.signal,
    });
    controller.abort();

    const abortEvent = probe.events.find((event) => event.kind === 'abort-controller-call');
    const observation = nativeAbortSignalObservationForRequest({
      requestCorrelationId: retained.requestId,
      requestIdentityMatchCount: 1,
      origin: uiOrigin,
      path,
      method: 'POST',
    }, [abortEvent]);
    assert.equal(observation.exactRequestSignalCorrelation, true);
    assert.equal(observation.requestId, retained.requestId);
    assert.equal(observation.ownerDomAtFetch.ruleOwner, 'feature-catalog-generated-fields');
    assert.equal(observation.ownerDomAtFetch.status, 'unique');
    assert(report.nativeAbortProbeEvents.some((event) => event.kind === 'probe-installed'));
    assert(report.nativeAbortProbeEvents.some((event) => event.kind === 'abort-controller-call'));

    const wrongScopeFetches = [
      `${apiOrigin}${path}`,
      `${uiOrigin}${apiPathFor('other-project', retained.explorer, 'schema-fields')}`,
      `${uiOrigin}${apiPathFor(retained.project, 'other-explorer', 'schema-fields')}`,
    ];
    for (const url of wrongScopeFetches) {
      const unownedController = new probe.sandbox.AbortController();
      probe.sandbox.fetch(url, {
        method: 'POST', headers: { 'X-Request-ID': retained.requestId }, signal: unownedController.signal,
      });
      unownedController.abort();
      const mismatchEvent = probe.events.at(-1);
      assert.equal(mismatchEvent.kind, 'abort-controller-call');
      assert.deepEqual(mismatchEvent.requests, [], `request escaped the configured page/project/Explorer scope: ${url}`);
      assert.equal(nativeAbortSignalObservationForRequest({
        requestCorrelationId: retained.requestId,
        requestIdentityMatchCount: 1,
        origin: uiOrigin,
        path,
        method: 'POST',
      }, [mismatchEvent]).exactRequestSignalCorrelation, false);
    }
  }

  const retained = retainedCases[0];
  const domWithoutGeneratedFieldOwner = { nodes: [] };
  const closeEditor = fakeNode({ tagName: 'BUTTON', attributes: {
    'data-testid': 'construction-close-operation-editor',
  }, text: 'Close operation editor' });
  domWithoutGeneratedFieldOwner.nodes.push(closeEditor);
  const { probe } = await installOnCurrentPage({ scope: retained, dom: domWithoutGeneratedFieldOwner });
  const path = apiPathFor(retained.project, retained.explorer, 'schema-fields');
  const controller = new probe.sandbox.AbortController();
  probe.sandbox.fetch(`${uiOrigin}${path}`, {
    method: 'POST', headers: { 'X-Request-ID': retained.requestId }, signal: controller.signal,
  });
  probe.advanceTime(101);
  probe.listeners.get('click')({ isTrusted: true, target: closeEditor });
  probe.advanceTime(102);
  controller.abort();

  const abortEvent = probe.events.find((event) => event.kind === 'abort-controller-call');
  const entry = withCDPIdentity({
    requestId: 'request-retained-case008-schema-fields',
    requestCorrelationId: retained.requestId,
    origin: uiOrigin,
    path,
    method: 'POST',
    requestTimestamp: 1,
    requestWallTime: 0.1,
    cdpRequestMatchCount: 1,
    loadingFailed: { timestamp: 1.012, errorText: 'net::ERR_ABORTED', canceled: true },
  });
  const evidence = nativeAbortProbeEvidenceForRequest(entry, [abortEvent]);
  assert.equal(evidence.length, 1, 'the exact request signal remains observable without an owner');
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].ownerDomAtFetch.status, 'missing');
  assert.equal(evidence[0].sameDocumentOwnerRetirement, false,
    'a matching request ID and close action do not prove retirement when the request had no unique captured owner');
});

test('configured-column-context capture is scoped and remains diagnostic without an owner rule', async () => {
  const scopedProject = 'loom_dev_verify_configured_context';
  const scopedExplorer = 'verify-configured-context';
  const configuredRequestId = 'configured-column-context-11111111-2222-4333-8444-555555555555';
  const uiOrigin = 'http://127.0.0.1:30008';
  const path = apiPathFor(scopedProject, scopedExplorer, 'configured-column-context');
  let binding;
  let source;
  const browserContext = {
    exposeBinding: async (_name, callback) => { binding = callback; },
    addInitScript: async (initSource) => { source = initSource; },
  };
  const page = {
    context: () => browserContext,
    evaluate: async (currentSource) => { source = currentSource; },
  };
  const report = {};
  await installNativeAbortProbe({
    page,
    report,
    project: scopedProject,
    explorer: scopedExplorer,
    apiOrigin: uiOrigin,
  });

  const probe = startProbe({
    dom: { nodes: [] },
    origin: uiOrigin,
    scopeProject: scopedProject,
    scopeExplorer: scopedExplorer,
    source,
    bindingCallback: binding,
  });
  const controller = new probe.sandbox.AbortController();
  probe.sandbox.fetch(`${uiOrigin}${path}`, {
    method: 'POST', headers: { 'X-Request-ID': configuredRequestId }, signal: controller.signal,
  });
  controller.abort();

  const abortEvent = probe.events.find((event) => event.kind === 'abort-controller-call');
  assert.equal(abortEvent.requests.length, 1);
  assert.equal(abortEvent.requests[0].requestId, configuredRequestId);
  assert.equal(abortEvent.requests[0].endpoint, 'configured-column-context');
  assert.equal(abortEvent.requests[0].ownerDomAtFetch, undefined,
    'this request has no proven page owner selector');
  assert.equal(abortEvent.requests[0].ownerDomAtAbort, undefined,
    'this request has no proven owner retirement action');

  const nativeEntry = withCDPIdentity({
    requestId: 'request-configured-column-context',
    requestCorrelationId: configuredRequestId,
    origin: uiOrigin,
    path,
    method: 'POST',
    requestTimestamp: 1,
    requestWallTime: 0.1,
    cdpRequestMatchCount: 1,
    loadingFailed: { timestamp: 1.012, errorText: 'net::ERR_ABORTED', canceled: true },
  });
  const signalObservation = nativeAbortSignalObservationForRequest({
    requestCorrelationId: configuredRequestId,
    requestIdentityMatchCount: 1,
    origin: uiOrigin,
    path,
    method: 'POST',
  }, [abortEvent]);
  assert.equal(signalObservation.exactRequestSignalCorrelation, true);
  assert.equal(signalObservation.ownerDomAtFetch, undefined);
  assert.equal(signalObservation.classificationEffect, 'diagnostic only; does not make an unfinished native request terminal or expected');

  const failureEvidence = nativeAbortProbeEvidenceForRequest(nativeEntry, [abortEvent]);
  assert.equal(failureEvidence.length, 1);
  assert.equal(failureEvidence[0].exactRequestSignalCorrelation, true);
  assert.equal(failureEvidence[0].sameDocumentOwnerRetirement, false,
    'an exact signal without an owner rule cannot be classified as an expected cancellation');

  const mismatches = [
    {
      url: `${uiOrigin}${apiPathFor(scopedProject, scopedExplorer, 'schema-fields')}`,
      requestId: configuredRequestId,
      label: 'wrong endpoint',
    },
    {
      url: `${uiOrigin}${path}`,
      requestId: 'schema-fields-11111111-2222-4333-8444-555555555555',
      label: 'wrong request ID prefix',
    },
    {
      url: `${uiOrigin}${apiPathFor('other-project', scopedExplorer, 'configured-column-context')}`,
      requestId: configuredRequestId,
      label: 'wrong project',
    },
    {
      url: `${uiOrigin}${apiPathFor(scopedProject, 'other-explorer', 'configured-column-context')}`,
      requestId: configuredRequestId,
      label: 'wrong Explorer',
    },
    {
      url: `${apiOrigin}${path}`,
      requestId: configuredRequestId,
      label: 'wrong origin',
    },
  ];
  for (const mismatch of mismatches) {
    const unownedController = new probe.sandbox.AbortController();
    probe.sandbox.fetch(mismatch.url, {
      method: 'POST', headers: { 'X-Request-ID': mismatch.requestId }, signal: unownedController.signal,
    });
    unownedController.abort();
    const mismatchEvent = probe.events.at(-1);
    assert.equal(mismatchEvent.kind, 'abort-controller-call');
    assert.deepEqual(mismatchEvent.requests, [], `${mismatch.label} must not be captured`);
    assert.equal(nativeAbortSignalObservationForRequest({
      requestCorrelationId: configuredRequestId,
      requestIdentityMatchCount: 1,
      origin: uiOrigin,
      path,
      method: 'POST',
    }, [mismatchEvent]).exactRequestSignalCorrelation, false, `${mismatch.label} must not match the retained request`);
  }
  assert(report.nativeAbortProbeEvents.some((event) => event.kind === 'abort-controller-call'));
});

test('probe assigns a scoped exact ID to an untagged capabilities signal and reports it without terminal classification', async () => {
  const dom = constructionCapabilitiesDom();
  const { sandbox, events, fetchCalls } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const path = apiPath('construction-capabilities');
  const body = JSON.stringify({
    snapshotToken: 'snapshot-current',
    expectedDraftVersion: 10,
    expectedDraftDigest: 'sha256:440a0bd-current',
    outputId: 'out_fcb5bc77cf3b4ac9b41498b4',
    stageId: 'source_projection',
  });
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body, signal: controller.signal,
  });

  const [forwardedUrl, forwardedInit] = fetchCalls[0];
  const id = forwardedInit.headers.get('X-Request-ID');
  assert.match(id, /^cda-request-[0-9a-f-]{36}$/i);
  assert.equal(forwardedUrl, `http://127.0.0.1:8188${path}`);
  assert.equal(forwardedInit.signal, controller.signal);
  assert.equal(forwardedInit.body, body, 'The probe adds only its diagnostic header and preserves the JSON body.');
  assert.equal(forwardedInit.headers.get('Content-Type'), 'application/json');

  dom.table.attributes['aria-current'] = undefined;
  dom.table.attributes['aria-pressed'] = 'false';
  controller.abort();
  await new Promise((resolve) => setImmediate(resolve));
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert.equal(event.requests.length, 1);
  assert.equal(event.requests[0].requestId, id);
  assert.equal(event.requests[0].requestIdSource, 'probe-injected');
  assert.equal(event.requests[0].requestContext.outputId, 'out_fcb5bc77cf3b4ac9b41498b4');
  assert.equal(event.requests[0].requestContext.expectedDraftVersion, 10);
  assert.equal(event.requests[0].ownerDomAtFetch.ruleOwner, 'construction-lifecycle-capabilities');
  assert.equal(event.requests[0].ownerDomAtFetch.ownerAttributes.selectedTableTestId,
    'construction-table-out_fcb5bc77cf3b4ac9b41498b4');
  assert.equal(event.requests[0].ownerOutputBindingAtFetch, true);

  const nativeEntry = { requestDetails: { requestId: id }, requestIdentityMatchCount: 1, origin: apiOrigin, path, method: 'POST' };
  const observation = nativeAbortSignalObservationForRequest(nativeEntry, events);
  assert.equal(observation.exactRequestSignalCorrelation, true);
  assert.equal(observation.requestId, id);
  assert.equal(observation.signalWasAlreadyAborted, false);
  assert.equal(observation.ownerOutputBindingAtFetch, true);
  assert.equal(observation.ownerOutputBindingAtAbort, true);
  assert.equal(observation.selectedOwnerStateAtAbort.ariaCurrent, undefined);
  assert.equal(observation.selectedOwnerStateAtAbort.ariaPressed, 'false');
  assert.equal(observation.nativeTerminalObserved, false);
  assert.equal(observation.fetchStateAfterAbort, 'rejected');
  assert.match(observation.classificationEffect, /does not make an unfinished native request terminal/);
  assert.equal(nativeAbortSignalObservationForRequest({ ...nativeEntry, requestDetails: { requestId: 'wrong-id' } }, events)
    .exactRequestSignalCorrelation, false);
  assert.equal(nativeAbortSignalObservationForRequest({ ...nativeEntry, path: apiPath('semantic-inventory') }, events)
    .exactRequestSignalCorrelation, false);
  assert.equal(nativeAbortSignalObservationForRequest({ ...nativeEntry, origin: 'http://127.0.0.1:8189' }, events)
    .exactRequestSignalCorrelation, false);
  assert.equal(nativeAbortSignalObservationForRequest({ ...nativeEntry, requestIdentityMatchCount: 2 }, events)
    .exactRequestSignalCorrelation, false);
  assert.equal(nativeAbortSignalObservationForRequest({ requestId: id, requestIdentityMatchCount: 1, path, method: 'POST' }, events)
    .exactRequestSignalCorrelation, false, 'A Playwright fallback request ID cannot substitute for the exact X-Request-ID.');
  const duplicateEvent = events.find((item) => item.kind === 'abort-controller-call');
  const ambiguous = nativeAbortSignalObservationForRequest(nativeEntry, [duplicateEvent, structuredClone(duplicateEvent)]);
  assert.equal(ambiguous.exactRequestSignalCorrelation, false);
  assert.equal(ambiguous.matchCount, 2);
});

test('probe does not add a diagnostic ID to an unowned or out-of-scope capabilities request', () => {
  const { sandbox, fetchCalls } = startProbe({ dom: constructionCapabilitiesDom() });
  const path = apiPath('construction-capabilities');
  const unownedSignal = { aborted: false };
  const controller = new sandbox.AbortController();
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', signal: unownedSignal,
  });
  sandbox.fetch(`http://127.0.0.1:8188${path.replace(explorer, 'other-explorer')}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', signal: unownedSignal,
  });
  sandbox.fetch(`http://127.0.0.1:8189${path}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}', signal: controller.signal,
  });
  assert.equal(fetchCalls.length, 3);
  for (const [, init] of fetchCalls) {
    assert.equal(Object.keys(init.headers).some((name) => name.toLowerCase() === 'x-request-id'), false);
  }
});

test('probe preserves a valid capabilities request ID supplied by the application', () => {
  const { sandbox, events, fetchCalls } = startProbe({ dom: constructionCapabilitiesDom() });
  const controller = new sandbox.AbortController();
  const id = requestId('cda-request-', 'abcdefab');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('construction-capabilities')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, body: '{}', signal: controller.signal,
  });
  assert.equal(fetchCalls[0][1].headers['X-Request-ID'], id);
  controller.abort();
  const record = events.find((item) => item.kind === 'abort-controller-call').requests[0];
  assert.equal(record.requestId, id);
  assert.equal(record.requestIdSource, 'request-header');
});

test('probe preserves the API-boundary construction-capabilities ID only on the exact endpoint', () => {
  const { sandbox, events, fetchCalls } = startProbe({ dom: constructionCapabilitiesDom() });
  const path = apiPath('construction-capabilities');
  const id = requestId('construction-capabilities-', 'abcdef01');
  const controller = new sandbox.AbortController();
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, body: '{}', signal: controller.signal,
  });
  assert.equal(fetchCalls[0][1].headers['X-Request-ID'], id);
  controller.abort();
  assert.equal(events.find(event => event.kind === 'abort-controller-call').requests[0].requestId, id);

  const wrongRouteId = requestId('construction-capabilities-', 'abcdef02');
  const wrongRouteController = new sandbox.AbortController();
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('schema-fields')}`, {
    method: 'POST', headers: { 'X-Request-ID': wrongRouteId }, body: '{}', signal: wrongRouteController.signal,
  });
  wrongRouteController.abort();
  const wrongRouteEvent = events.filter(event => event.kind === 'abort-controller-call').at(-1);
  assert.deepEqual(wrongRouteEvent.requests, [], 'the new ID prefix does not widen the exact capabilities route rule');
});

test('captured trusted interaction is metadata-only and tied to the later abort event', () => {
  const { sandbox, events, listeners } = startProbe();
  const controller = new sandbox.AbortController();
  sandbox.__loomNativeAbortAction = { id: 'action-7', name: 'Open related chooser', selector: '[data-testid="construction-action-add-columns"]', startedAt: 10 };
  listeners.get('click')({ isTrusted: true, target: {
    tagName: 'BUTTON',
    getAttribute: (name) => ({ 'aria-label': 'Add columns', 'data-testid': 'construction-action-add-columns', role: 'button' })[name] ?? null,
  } });
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert.equal(event.actionEnvelope.id, 'action-7');
  assert.equal(event.lastTrustedInteraction.target.testId, 'construction-action-add-columns');
  assert.equal(event.lastTrustedInteraction.target.ariaLabel, 'Add columns');
  assert.equal(event.lastTrustedInteraction.target.label, undefined);
});

test('probe binds the exact captured DOM node to a trusted coded-to-fields tab retirement', () => {
  const dom = codedTabDom();
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('paired-column-inventory-', '55555555');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('semantic-inventory')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
    body: 'page content must not be recorded',
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.fields });
  dom.selectFields();
  advanceTime(111);
  controller.abort();

  const event = events.find((item) => item.kind === 'abort-controller-call');
  const request = event.requests[0];
  assert.equal(request.ownerDomAtFetch.status, 'unique');
  assert.equal(request.ownerDomAtFetch.connectedAtFetch, true);
  assert.equal(request.ownerDomAtFetch.selectedTabAtFetch, 'Coded values');
  assert.equal(request.ownerDomAtAbort.anchorId, request.ownerDomAtFetch.anchorId);
  assert.equal(request.ownerDomAtAbort.connectedAtAbort, false);
  assert.equal(request.ownerDomAtAbort.detachedAtAbort, true);
  assert.equal(request.ownerDomAtAbort.selectedTabAtAbort, 'Fields and related data');
  assert.equal(event.trustedInteractions[0].isTrusted, true);
  assert.equal(event.trustedInteractions[0].closestButton.accessibleLabel, 'Fields and related data');
  const entry = {
    requestId: 'cdp-coded-request', requestCorrelationId: id, path: apiPath('semantic-inventory'), method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  const probeEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [event]);
  assert.equal(probeEvidence[0].exactRequestSignalCorrelation, true);
  assert.equal(probeEvidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(probeEvidence[0].networkRequestId, withCDPIdentity(entry).cdpRequestId);
  assert.equal(probeEvidence[0].networkFailureClockBasis, 'request-wall-time-calibrated-cdp-monotonic');
  assert.ok(Math.abs(probeEvidence[0].networkFailureObservedAt - 112) < 1e-6);
  assert.equal(probeEvidence[0].ownerRetirementAction.isTrusted, true);
  assert(!JSON.stringify(event).includes('page content must not be recorded'));
});

for (const actionId of ['construction-action-group-rows', 'construction-action-related-rows']) {
test(`probe links Starting collection retirement to trusted ${actionId} inside its exact row dialog`, () => {
  const dom = startingCollectionDom();
  dom.groupAction.attributes['data-testid'] = actionId;
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('population-routes-', 'aaaaaaaa');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('population-routes')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.groupAction });
  dom.dialog.isConnected = false;
  advanceTime(111);
  controller.abort();

  const event = events.find((item) => item.kind === 'abort-controller-call');
  const request = event.requests[0];
  assert.equal(request.ownerDomAtFetch.status, 'unique');
  assert.equal(request.ownerDomAtFetch.ruleOwner, 'population-route-options');
  assert.equal(request.ownerDomAtFetch.dialogConnectedAtFetch, true);
  assert.equal(request.ownerDomAtAbort.anchorId, request.ownerDomAtFetch.anchorId);
  assert.equal(request.ownerDomAtAbort.detachedAtAbort, true);
  assert.equal(request.ownerDomAtAbort.dialogId, request.ownerDomAtFetch.dialogId);
  assert.equal(request.ownerDomAtAbort.dialogConnectedAtAbort, false);
  assert.equal(event.trustedInteractions[0].closestButton.testId, actionId);
  assert.equal(event.trustedInteractions[0].closestButton.dialogId, request.ownerDomAtFetch.dialogId);

  const entry = {
    requestId: 'cdp-population-request', requestCorrelationId: id, path: apiPath('population-routes'), method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [event]);
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
});

}

test('probe fails closed when the owner selector is missing or ambiguous at fetch time', () => {
  for (const nodes of [[], [
    fakeNode({ attributes: { 'data-testid': 'paired-column-suggestions' } }),
    fakeNode({ attributes: { 'data-testid': 'paired-column-suggestions' } }),
  ]]) {
    const dom = { nodes: [
      fakeNode({ attributes: { 'aria-label': 'Column types' } }),
      ...nodes,
    ] };
    const { sandbox, events } = startProbe({ dom });
    const controller = new sandbox.AbortController();
    const id = requestId('paired-column-inventory-', nodes.length ? '66666666' : '77777777');
    sandbox.fetch(`http://127.0.0.1:8188${apiPath('semantic-inventory')}`, {
      method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
    });
    controller.abort();
    const record = events.find((item) => item.kind === 'abort-controller-call').requests[0];
    assert.equal(record.ownerDomAtFetch.status, nodes.length ? 'ambiguous' : 'missing');
    assert.equal(record.ownerDomAtFetch.connectedAtFetch, false);
  }
});

test('selector replacement cannot substitute for the node captured at fetch start', () => {
  const dom = codedTabDom();
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('paired-column-inventory-', '88888888');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('semantic-inventory')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.fields });
  dom.coded.attributes['aria-pressed'] = 'false';
  dom.fields.attributes['aria-pressed'] = 'true';
  const replacement = fakeNode({ attributes: { 'data-testid': 'paired-column-suggestions' } });
  dom.nodes.splice(dom.nodes.indexOf(dom.owner), 1, replacement);
  advanceTime(111);
  controller.abort();

  const record = events.find((item) => item.kind === 'abort-controller-call').requests[0];
  assert.equal(record.ownerDomAtAbort.anchorId, record.ownerDomAtFetch.anchorId);
  assert.equal(record.ownerDomAtAbort.connectedAtAbort, true);
  const entry = {
    requestId: 'cdp-replaced-request', requestCorrelationId: id, path: apiPath('semantic-inventory'), method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  assert.equal(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), events)[0].sameDocumentOwnerRetirement, false);
});

test('same-document clock calibration requires the exact CDP request and valid monotonic pair', () => {
  const valid = {
    requestId: 'cdp-clock-request', requestTimestamp: 12.5, requestWallTime: 1_700_000_000.25,
    loadingFailed: { timestamp: 12.75, at: -1 },
  };
  assert.deepEqual(nativeAbortNetworkFailureClock(valid), {
    at: 1_700_000_000_500,
    basis: 'request-wall-time-calibrated-cdp-monotonic',
  });
  for (const invalid of [
    { ...valid, requestId: undefined },
    { ...valid, requestTimestamp: 0 },
    { ...valid, requestTimestamp: undefined },
    { ...valid, requestWallTime: 0 },
    { ...valid, requestWallTime: undefined },
    { ...valid, loadingFailed: { timestamp: 12.49, at: 1_700_000_001 } },
  ]) {
    assert.notEqual(nativeAbortNetworkFailureClock(invalid).basis, 'request-wall-time-calibrated-cdp-monotonic');
  }
  assert.equal(nativeAbortNetworkFailureClock({ ...valid, loadingFailed: { at: 1_700_000_001 } }).basis,
    'host-wall-clock-at-cdp-callback');
});

test('related-expansion cancellation proves the exact editor node, stage/output, trusted Apply, and request signal', () => {
  const dom = relatedExpandDom();
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('related-expand-choices-', 'aaaaaaaa');
  const path = apiPath('related-expand-choices');
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.apply });
  dom.owner.isConnected = false;
  advanceTime(111);
  controller.abort();

  const event = events.find((item) => item.kind === 'abort-controller-call');
  const request = event.requests[0];
  assert.equal(request.ownerDomAtFetch.ruleOwner, 'related-expand-choice-editor');
  assert.equal(request.ownerDomAtFetch.ownerAttributes.stageId, 'related-stage-1');
  assert.equal(request.ownerDomAtFetch.ownerAttributes.outputId, 'output-1');
  assert.deepEqual(request.ownerDomAtAbort.ownerAttributes, request.ownerDomAtFetch.ownerAttributes);
  assert.equal(request.ownerDomAtAbort.anchorId, request.ownerDomAtFetch.anchorId);

  const entry = {
    requestId: 'cdp-related-expand-request', requestCorrelationId: id, path, method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [event]);
  assert.equal(evidence.length, 1);
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(evidence[0].ownerRetirementAction.closestButton.testId, 'construction-apply-proposal');

  const attached = relatedExpandDom();
  const attachedRun = startProbe({ dom: attached });
  const attachedController = new attachedRun.sandbox.AbortController();
  attachedRun.sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: attachedController.signal,
  });
  attachedRun.advanceTime(110);
  attachedRun.listeners.get('click')({ isTrusted: true, target: attached.apply });
  attachedRun.advanceTime(111);
  attachedController.abort();
  const attachedEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry),
    [attachedRun.events.find((item) => item.kind === 'abort-controller-call')]);
  assert.equal(attachedEvidence[0].sameDocumentOwnerRetirement, false, 'same editor node remains attached');

  const wrongAction = relatedExpandDom();
  wrongAction.apply.attributes['data-testid'] = 'construction-cancel-proposal';
  const wrongRun = startProbe({ dom: wrongAction });
  const wrongController = new wrongRun.sandbox.AbortController();
  wrongRun.sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: wrongController.signal,
  });
  wrongRun.advanceTime(110);
  wrongRun.listeners.get('click')({ isTrusted: true, target: wrongAction.apply });
  wrongAction.owner.isConnected = false;
  wrongRun.advanceTime(111);
  wrongController.abort();
  const wrongActionEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry),
    [wrongRun.events.find((item) => item.kind === 'abort-controller-call')]);
  assert.equal(wrongActionEvidence[0].sameDocumentOwnerRetirement, false, 'unrelated action cannot retire the editor');

  const changedScope = relatedExpandDom();
  const changedRun = startProbe({ dom: changedScope });
  const changedController = new changedRun.sandbox.AbortController();
  changedRun.sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: changedController.signal,
  });
  changedScope.owner.attributes['data-related-stage-id'] = 'different-stage';
  changedRun.advanceTime(110);
  changedRun.listeners.get('click')({ isTrusted: true, target: changedScope.apply });
  changedScope.owner.isConnected = false;
  changedRun.advanceTime(111);
  changedController.abort();
  const changedEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry),
    [changedRun.events.find((item) => item.kind === 'abort-controller-call')]);
  assert.equal(changedEvidence[0].sameDocumentOwnerRetirement, false, 'stage identity changed before owner retirement');
});

test('ConceptCatalog read retires only after the exact single-feature Add action closes its captured input', () => {
  const dom = featureCatalogDom();
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('construction-choices-', 'bbbbbbbb');
  const path = apiPath('construction-choices');
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.add });
  dom.owner.isConnected = false;
  advanceTime(111);
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  const entry = {
    requestId: 'cdp-construction-choice-request', requestCorrelationId: id, path, method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [event]);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(evidence[0].ownerDomAtFetch.selector, '#feature-catalog-search');
  assert.equal(evidence[0].ownerDomAtAbort.anchorId, evidence[0].ownerDomAtFetch.anchorId,
    'the input detached must be the exact input captured when the read started');
  assert.equal(evidence[0].ownerDomAtAbort.detachedAtAbort, true);
  assert.equal(evidence[0].ownerRetirementAction.closestButton.accessibleLabel, 'Add 1 selected feature');

  const attachedDom = featureCatalogDom();
  const attachedRun = startProbe({ dom: attachedDom });
  const attachedController = new attachedRun.sandbox.AbortController();
  attachedRun.sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: attachedController.signal,
  });
  attachedRun.advanceTime(110);
  attachedRun.listeners.get('click')({ isTrusted: true, target: attachedDom.add });
  attachedRun.advanceTime(111);
  attachedController.abort();
  const attachedEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry),
    [attachedRun.events.find((item) => item.kind === 'abort-controller-call')]);
  assert.equal(attachedEvidence[0].sameDocumentOwnerRetirement, false,
    'an Add click does not justify abort while the captured search input remains mounted');

  const wrongActionDom = featureCatalogDom('Add 2 selected features');
  const wrongActionRun = startProbe({ dom: wrongActionDom });
  const wrongController = new wrongActionRun.sandbox.AbortController();
  wrongActionRun.sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: wrongController.signal,
  });
  wrongActionRun.advanceTime(110);
  wrongActionRun.listeners.get('click')({ isTrusted: true, target: wrongActionDom.add });
  wrongActionDom.owner.isConnected = false;
  wrongActionRun.advanceTime(111);
  wrongController.abort();
  const wrongEvidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry),
    [wrongActionRun.events.find((item) => item.kind === 'abort-controller-call')]);
  assert.equal(wrongEvidence[0].sameDocumentOwnerRetirement, false, 'a different catalog action does not count');
});

test('schema-fields probe records exact generated-field owner retirement on Close operation editor', () => {
  const dom = featureCatalogDom();
  const close = fakeNode({ tagName: 'BUTTON', attributes: {
    'data-testid': 'construction-close-operation-editor',
  }, text: 'Close operation editor' });
  dom.nodes.push(close);
  const uiProxyOrigin = 'http://127.0.0.1:30008';
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom, origin: uiProxyOrigin });
  const controller = new sandbox.AbortController();
  const id = requestId('schema-fields-', '8ba1b927');
  const path = apiPath('schema-fields');
  sandbox.fetch(`${uiProxyOrigin}${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: close });
  dom.owner.isConnected = false;
  advanceTime(111);
  controller.abort();

  const event = events.find((item) => item.kind === 'abort-controller-call');
  const entry = {
    requestId: 'cdp-schema-fields-request', requestCorrelationId: id,
    origin: uiProxyOrigin, path, method: 'POST', requestTimestamp: 1, requestWallTime: 0.1,
    loadingFailed: { timestamp: 1.012, at: 999, errorText: 'net::ERR_ABORTED', canceled: true },
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [event]);
  assert.equal(evidence.length, 1);
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(evidence[0].request.ownerDomAtFetch.ruleOwner, 'feature-catalog-generated-fields');
  assert.equal(evidence[0].ownerRetirementAction.closestButton.testId, 'construction-close-operation-editor');

  const wrongOrigin = startProbe({ dom: featureCatalogDom(), origin: apiOrigin });
  const wrongOriginController = new wrongOrigin.sandbox.AbortController();
  wrongOrigin.sandbox.fetch(`${uiProxyOrigin}${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: wrongOriginController.signal,
  });
  wrongOriginController.abort();
  const wrongOriginAbort = wrongOrigin.events.find((item) => item.kind === 'abort-controller-call');
  assert.deepEqual(wrongOriginAbort.requests, [], 'the probe must not widen its endpoint scope to another origin');
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [wrongOriginAbort]), [],
    'an out-of-origin fetch cannot inherit this native request identity');

  const wrongActionDom = featureCatalogDom();
  const wrongActionRun = startProbe({ dom: wrongActionDom, origin: uiProxyOrigin });
  const wrongActionController = new wrongActionRun.sandbox.AbortController();
  wrongActionRun.sandbox.fetch(`${uiProxyOrigin}${path}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: wrongActionController.signal,
  });
  wrongActionRun.advanceTime(110);
  wrongActionRun.listeners.get('click')({ isTrusted: true, target: wrongActionDom.add });
  wrongActionDom.owner.isConnected = false;
  wrongActionRun.advanceTime(111);
  wrongActionController.abort();
  const wrongActionEvent = wrongActionRun.events.find((item) => item.kind === 'abort-controller-call');
  assert.equal(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [wrongActionEvent])[0].sameDocumentOwnerRetirement, false,
    'an unrelated catalog action cannot explain generated-field cancellation');
});

test('related-expand rule classifies only the exact scoped request after trusted Apply detaches its same stage/output owner', () => {
  const stageId = 'related-stage-1';
  const outputId = 'output-1';
  const draft = {
    snapshotToken: 'snapshot-1', outputId, expectedDraftVersion: 7, expectedDraftDigest: 'draft-7',
    stageId, anchorColumnId: 'anchor-column-1', targetResourceType: 'Patient',
  };
  const expected = { origin: 'http://127.0.0.1:8188', project, explorer, ...draft };
  const id = requestId('related-expand-choices-', 'cccccccc');
  const path = apiPath('related-expand-choices');
  const entry = {
    requestId: 'cdp-related-expand-request', requestCorrelationId: id, origin: expected.origin,
    path, method: 'POST', resourceType: 'Fetch', scopeProject: project, scopeExplorer: explorer,
    request: draft, requestTimestamp: 1, requestWallTime: 0.1, startedAt: 100,
    loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, timestamp: 1.012 },
    networkTerminal: true, bodyReadStatus: 'failed', complete: false,
    initiator: { type: 'script', stack: [
      { url: 'http://localhost/ui/api.ts', functionName: 'searchRelatedExpandChoices' },
      { url: 'http://localhost/ui/constructionOperations/RelatedExpandEditor.tsx', functionName: 'loadChoices' },
    ] },
  };
  const run = ({ detach = true, action = 'construction-apply-proposal', changedStage } = {}) => {
    const dom = relatedExpandDom();
    const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
    const controller = new sandbox.AbortController();
    sandbox.fetch(`http://127.0.0.1:8188${path}`, {
      method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
    });
    advanceTime(110);
    dom.apply.attributes['data-testid'] = action;
    listeners.get('click')({ isTrusted: true, target: dom.apply });
    if (changedStage) dom.owner.attributes['data-related-stage-id'] = changedStage;
    if (detach) dom.owner.isConnected = false;
    advanceTime(111);
    controller.abort();
    const abort = events.find((item) => item.kind === 'abort-controller-call');
    const probe = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [abort]);
    const candidate = withCDPIdentity({ ...entry, abortControllerProbeEvidence: probe });
    const inScope = nativeReadRequestMatchesExpectedScope(candidate, expected);
    const result = inScope
      ? classifyExpectedOwnedCancellation(candidate, { frameNavigations: [], executionContextRetirements: [] })
      : { expected: false, reason: 'request-does-not-match-independent-ui-scope' };
    return { abort, probe, result, inScope };
  };

  const accepted = run();
  assert.equal(accepted.inScope, true);
  assert.equal(accepted.probe[0].sameDocumentOwnerRetirement, true);
  assert.equal(accepted.result.expected, true, JSON.stringify(accepted.result));
  assert.equal(accepted.result.ownerRetirement.owner, 'related-expand-choice-editor');

  for (const mismatch of [
    { outputId: 'other-output' },
    { stageId: 'other-stage' },
    { expectedDraftVersion: 8 },
    { expectedDraftDigest: 'other-digest' },
  ]) {
    const candidate = withCDPIdentity({ ...entry, request: { ...entry.request, ...mismatch } });
    const probe = nativeAbortProbeEvidenceForRequest(withCDPIdentity(candidate), [accepted.abort]);
    assert.equal(nativeReadRequestMatchesExpectedScope(candidate, expected), false,
      `reject request/UI mismatch ${JSON.stringify(mismatch)}`);
    const gatedResult = nativeReadRequestMatchesExpectedScope(candidate, expected)
      ? classifyExpectedOwnedCancellation({ ...candidate, abortControllerProbeEvidence: probe }, {})
      : { expected: false };
    assert.equal(gatedResult.expected, false,
      `do not classify request/UI mismatch ${JSON.stringify(mismatch)}`);
  }
  assert.equal(run({ action: 'construction-cancel-proposal' }).result.expected, false, 'wrong Apply action');
  assert.equal(run({ detach: false }).result.expected, false, 'same captured editor remains mounted');
  assert.equal(run({ changedStage: 'replacement-stage' }).result.expected, false, 'captured owner stage changes before detach');
});

test('synthetic events are omitted from trusted interaction evidence', () => {
  const dom = codedTabDom();
  const { sandbox, events, listeners } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('semantic-inventory')}`, {
    method: 'POST', headers: { 'X-Request-ID': requestId('paired-column-inventory-', '99999999') }, signal: controller.signal,
  });
  listeners.get('click')({ isTrusted: false, target: dom.fields });
  controller.abort();
  const event = events.find((item) => item.kind === 'abort-controller-call');
  assert.deepEqual(event.trustedInteractions, []);
  assert.equal(event.lastTrustedInteraction, undefined);
});

test('serialized trusted-interaction list remains explicit provenance when older reports omit isTrusted fields', () => {
  const dom = codedTabDom();
  const { sandbox, events, listeners, advanceTime } = startProbe({ dom });
  const controller = new sandbox.AbortController();
  const id = requestId('paired-column-inventory-', 'abababab');
  sandbox.fetch(`http://127.0.0.1:8188${apiPath('semantic-inventory')}`, {
    method: 'POST', headers: { 'X-Request-ID': id }, signal: controller.signal,
  });
  advanceTime(110);
  listeners.get('click')({ isTrusted: true, target: dom.fields });
  dom.selectFields();
  advanceTime(111);
  controller.abort();
  const original = events.find((item) => item.kind === 'abort-controller-call');
  const serializedLegacyEvent = structuredClone(original);
  for (const interaction of serializedLegacyEvent.trustedInteractions) delete interaction.isTrusted;
  delete serializedLegacyEvent.lastTrustedInteraction.isTrusted;
  const entry = {
    requestId: 'cdp-legacy-trust-request', requestCorrelationId: id, path: apiPath('semantic-inventory'), method: 'POST',
    requestTimestamp: 1, requestWallTime: 0.1, loadingFailed: { timestamp: 1.012, at: 999 },
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [serializedLegacyEvent]);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(evidence[0].ownerRetirementAction.trustEvidence, 'trusted-interaction-list-membership');

  const withoutTrustedList = { ...serializedLegacyEvent, trustedInteractions: undefined };
  const unproven = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [withoutTrustedList]);
  assert.equal(unproven[0].sameDocumentOwnerRetirement, false);
});

test('request evidence requires exact ID, path, and abort-before-CDP-failure and never classifies a pass', () => {
  const entry = {
    requestId: 'cdp-correlation-request',
    requestCorrelationId: requestId('population-routes-', '11111111'),
    origin: apiOrigin,
    path: apiPath('population-routes'),
    method: 'POST',
    requestTimestamp: 10,
    requestWallTime: 0.1,
    loadingFailed: { timestamp: 10.1, at: 200, errorText: 'net::ERR_ABORTED', canceled: true },
  };
  const abortEvent = {
    kind: 'abort-controller-call', controllerId: 'abort-controller-1', createdAt: 100, abortedAt: 180,
    signalWasAlreadyAborted: false,
    requests: [{ requestId: entry.requestCorrelationId, origin: entry.origin, path: entry.path, method: 'POST', startedAt: 150 }],
  };
  const evidence = nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [abortEvent]);
  assert.equal(evidence.length, 1);
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].networkFailureObservedSeparately, true);
  assert.equal(evidence[0].networkRequestId, 'cdp-cdp-correlation-request');
  assert.ok(Math.abs(evidence[0].abortToNetworkFailureMs - 20) < 1e-6);
  assert.equal(nativeAbortProbeEvidenceForRequest(entry, [abortEvent])[0].sameDocumentOwnerRetirement, false,
    'the X-Request-ID alone cannot stand in for the CDP Network.requestId');

  assert.deepEqual(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [{
    ...abortEvent,
    abortedAt: 201,
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], requestId: 'different-id' }],
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], path: apiPath('construction-choices') }],
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(withCDPIdentity(entry), [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], startedAt: 181 }],
  }]), []);
  assert.equal(entry.loadingFailed.errorText, 'net::ERR_ABORTED');
});


const schemaFieldsCloseReport = (overrides = {}) => {
  const config = {
    project: 'loom_dev_verify_fixture-a',
    explorer: 'verify-fixture-a-cohort-recode',
    requestId: 'schema-fields-9cc92c67-46c0-4340-8628-f0ad83feaa9e',
    browserRequestId: 'request-263',
    networkPlaywrightRequestId: undefined,
    networkBrowserRequestId: undefined,
    projectedBrowserRequestId: undefined,
    controllerId: 'abort-controller-24',
    fetchStartedAt: 2500,
    nativeRequestObservedAt: 2500,
    requestStartedMs: 2500,
    requestFailedMs: 3040,
    requestFailedAt: 3040,
    closeAt: 3000,
    controllerCreatedAt: 1000,
    controllerAbortedAt: 3020,
    ownerDetachedAt: 3018,
    fetchSettledAt: 3020,
    fetchSettlementObservedAt: 3020,
    status: null,
    networkStatus: undefined,
    responseObservedAt: null,
    requestAction: { id: 'action-apply', label: 'Apply exact generated field' },
    actions: [
      { id: 'action-apply', label: 'Apply exact generated field', status: 'passed',
        startedAtEpochMs: 2400, finishedAtEpochMs: 2800 },
      { id: 'action-close', label: 'close operation editor', status: 'passed',
        startedAtEpochMs: 2900, finishedAtEpochMs: 3050 },
    ],
    ...overrides,
  };
  const closeProject = config.project;
  const closeExplorer = config.explorer;
  const closeOrigin = 'http://127.0.0.1:30008';
  const closePath = `/api/v1/projects/${encodeURIComponent(closeProject)}/explorers/${encodeURIComponent(closeExplorer)}/authoring/v2/schema-fields`;
  const { requestId, browserRequestId, controllerId } = config;
  const requestStartedAt = config.fetchStartedAt;
  const closeAt = config.closeAt;
  const controllerAbortedAt = config.controllerAbortedAt;
  const requestFailedAt = config.requestFailedAt;
  const ownerDomAtFetch = {
    status: 'unique', selector: '#feature-catalog-search', matchCount: 1, capturedAt: requestStartedAt,
    anchorId: 'dom-node-9', connectedAtFetch: true, ruleOwner: 'feature-catalog-generated-fields',
    retirementAction: 'close-operation-editor',
  };
  const ownerDomAtAbort = {
    anchorId: 'dom-node-9', connectedAtAbort: false, detachedAtAbort: true,
    detachedObservedAt: config.ownerDetachedAt, observedAtAbort: controllerAbortedAt,
  };
  const closeInteraction = {
    type: 'click', at: closeAt, isTrusted: true,
    target: { tag: 'BUTTON', testId: 'construction-close-operation-editor', ariaLabel: 'Close operation editor' },
    closestButton: {
      id: 'dom-node-12', accessibleLabel: 'Close operation editor',
      testId: 'construction-close-operation-editor',
    },
  };
  const event = {
    kind: 'abort-controller-call', controllerId, createdAt: config.controllerCreatedAt, abortedAt: controllerAbortedAt,
    abortCount: 1, signalWasAlreadyAborted: false,
    requests: [{
      requestId, origin: closeOrigin, path: closePath, method: 'POST', endpoint: 'schema-fields',
      explorer: closeExplorer, requestIdSource: 'request-header', startedAt: requestStartedAt,
      fetchStateAtAbort: 'pending', ownerDomAtFetch, ownerDomAtAbort,
    }],
    trustedInteractions: [closeInteraction], lastTrustedInteraction: closeInteraction,
  };
  const settlement = {
    kind: 'abort-controller-fetch-settlement', controllerId, observedAt: config.fetchSettlementObservedAt,
    requests: [{
      requestId, origin: closeOrigin, path: closePath, method: 'POST',
      fetchStateAfterAbort: 'rejected', settledAt: config.fetchSettledAt,
    }],
  };
  const request = {
    requestId, browserRequestId, method: 'POST', origin: closeOrigin, path: closePath,
    status: config.status, failure: 'net::ERR_ABORTED', terminalEvent: 'requestfailed', state: 'failed', complete: true,
    pageId: 'playwright-page-1', frameId: 'playwright-frame-1', frameIsMainFrame: true,
    frameIdentityStatus: 'exact',
    requestTimeline: {
      requestStartedMs: config.requestStartedMs, failedAtMs: config.requestFailedMs,
      durationMs: config.requestFailedMs - config.requestStartedMs,
      action: config.requestAction, mainFrameNavigations: [],
    },
    nativeEventChronology: [
      { event: 'request', browserRequestId, observedAt: config.nativeRequestObservedAt, objectMatch: true },
      ...(config.responseObservedAt === null ? [] : [{
        event: 'response', browserRequestId, observedAt: config.responseObservedAt, objectMatch: true, status: 200,
      }]),
      { event: 'requestfailed', browserRequestId, observedAt: requestFailedAt, objectMatch: true, failure: 'net::ERR_ABORTED' },
    ],
  };
  const entry = { ...request, requestCorrelationId: requestId, requestIdentityMatchCount: 1 };
  const observation = nativeAbortSignalObservationForRequest(entry, [event, settlement]);
  const report = createReport({
    scenario: 'builder-authoring', caseName: 'cohort-recode',
    target: { uiUrl: closeOrigin, project: closeProject, explorer: closeExplorer },
    requiredChecks: ['Apply exact generated field completed'],
  });
  report.network.push({
    kind: 'network', method: 'POST', url: `${closeOrigin}${closePath}`, resourceType: 'fetch',
    errorText: 'net::ERR_ABORTED', playwrightRequestId: config.networkPlaywrightRequestId ?? browserRequestId,
    ...(config.networkBrowserRequestId === undefined ? {} : { browserRequestId: config.networkBrowserRequestId }),
    requestDetails: { requestId }, status: config.networkStatus, requestTimeline: request.requestTimeline,
  });
  report.actions.push(...structuredClone(config.actions));
  report.nativeRequestCaptureScope = {
    observedPathPrefix: `/api/v1/projects/${encodeURIComponent(closeProject)}/explorers`,
    selectedExplorer: closeExplorer,
  };
  report.nativeRequestTerminalLedger = { complete: true, requests: [request] };
  report.nativeAbortProbeEvents = [event, settlement];
  report.nativeAbortSignalObservations = [{
    requestId, origin: closeOrigin, path: closePath, method: 'POST', requestIdentityMatchCount: 1,
    ...(config.projectedBrowserRequestId === undefined ? {} : { browserRequestId: config.projectedBrowserRequestId }),
    observation,
  }];
  recordCheck(report, 'correctness', 'Apply exact generated field completed', true, {
    outputId: 'out-fixture', receiptId: 'receipt-fixture', renderedValues: ['dev-patient-001', 'dev-patient-002'],
  });
  return { report, request, closeInteraction };
};

test('finishReport accepts only a terminal schema-fields abort after its exact Catalog owner closes', () => {
  const { report } = schemaFieldsCloseReport();
  const originalNetwork = structuredClone(report.network);
  finishReport(report);
  assert.equal(report.status, 'passed', JSON.stringify({ expected: report.expectedOwnerRetirements, errors: report.errors, failed: report.assertions.find(item => item.name === 'no unexpected network, module, or browser errors') }));
  assert.deepEqual(report.missingRequiredChecks, []);
  assert.deepEqual(report.expectedOwnerRetirements, [{
    kind: 'same-document-owner-retirement', endpoint: 'schema-fields',
    requestId: 'schema-fields-9cc92c67-46c0-4340-8628-f0ad83feaa9e', browserRequestId: 'request-263',
    project: 'loom_dev_verify_fixture-a', explorer: 'verify-fixture-a-cohort-recode',
    owner: 'feature-catalog-generated-fields', selector: '#feature-catalog-search',
    controllerId: 'abort-controller-24', controllerAbortedAt: 3020, fetchStartedAt: 2500,
    nativeRequestObservedAt: 2500, closeAt: 3000, requestFailedAt: 3040,
    associatedAction: { id: 'action-apply', status: 'passed', finishedAtEpochMs: 2800 },
    closeActionId: 'action-close',
    reason: 'the exact generated-field catalog request was terminalized after trusted Close retired its observed owner with no unresolved action',
  }]);
  assert.deepEqual(report.network, originalNetwork, 'the original failed request stays in raw network evidence');
  assert.equal(Object.hasOwn(report.network[0], 'canceled'), false, 'classification does not rewrite the raw failure');
});

test('finishReport replays the retained 57ceb Apply-overlap and fresh CASE-008/009 Close terminal evidence shapes', () => {
  const retainedReports = [
    {
      name: '57ceb CASE-008 request began during passed Apply and failed after Close',
      config: {
        project: 'loom_dev_verify_mv0wf60k-870ed96',
        explorer: 'verify-0k-870ed96-cohort-recode',
        requestId: 'schema-fields-9cc92c67-46c0-4340-8628-f0ad83feaa9e',
        fetchStartedAt: 1791546329451,
        nativeRequestObservedAt: 1791546329453,
        requestStartedMs: 8770,
        requestFailedMs: 9118,
        requestFailedAt: 1791546329800,
        closeAt: 1791546329766,
        controllerCreatedAt: 1791546327984,
        controllerAbortedAt: 1791546329795,
        ownerDetachedAt: 1791546329793,
        fetchSettledAt: 1791546329795,
        fetchSettlementObservedAt: 1791546329795,
        requestAction: { id: 'action-17', label: 'apply Patient.id member field with ALL' },
        actions: [
          { id: 'action-17', label: 'apply Patient.id member field with ALL', status: 'passed',
            startedAtEpochMs: 1791546329180, finishedAtEpochMs: 1791546329655 },
          { id: 'action-18', label: 'close operation editor', status: 'passed',
            startedAtEpochMs: 1791546329655, finishedAtEpochMs: 1791546329805 },
        ],
      },
      associatedActionId: 'action-17',
    },
    {
      name: 'fresh CASE-008 request began during the trusted Close wrapper',
      config: {
        project: 'loom_dev_verify_mv0xw1xd-7fd926e',
        explorer: 'verify-xd-7fd926e-cohort-recode',
        requestId: 'schema-fields-0ffc09d4-4d4e-41f8-b4d8-81d80beccb52',
        fetchStartedAt: 1791548796411,
        nativeRequestObservedAt: 1791548796413,
        requestStartedMs: 8247,
        requestFailedMs: 8277,
        requestFailedAt: 1791548796443,
        closeAt: 1791548796412,
        controllerCreatedAt: 1791548795026,
        controllerAbortedAt: 1791548796438,
        ownerDetachedAt: 1791548796435,
        fetchSettledAt: 1791548796438,
        fetchSettlementObservedAt: 1791548796438,
        requestAction: { id: 'action-18', label: 'close operation editor' },
        actions: [
          { id: 'action-17', label: 'apply Patient.id member field with ALL', status: 'passed',
            startedAtEpochMs: 1791548795799, finishedAtEpochMs: 1791548796304 },
          { id: 'action-18', label: 'close operation editor', status: 'passed',
            startedAtEpochMs: 1791548796304, finishedAtEpochMs: 1791548796447 },
        ],
      },
      associatedActionId: 'action-18',
    },
    {
      name: 'fresh CASE-009 idle catalog request returned headers after abort before native failure',
      config: {
        project: 'loom_dev_verify_mv0xw3zn-6442e88',
        explorer: 'verify-zn-6442e88-cohort-expand',
        requestId: 'schema-fields-975e44ad-ec76-4d54-843f-b91c5e0693af',
        fetchStartedAt: 1791548797830,
        nativeRequestObservedAt: 1791548797832,
        requestStartedMs: 7403,
        requestFailedMs: 7869,
        requestFailedAt: 1791548798298,
        closeAt: 1791548798262,
        controllerCreatedAt: 1791548796451,
        controllerAbortedAt: 1791548798290,
        ownerDetachedAt: 1791548798288,
        fetchSettledAt: 1791548798290,
        fetchSettlementObservedAt: 1791548798290,
        status: 200,
        responseObservedAt: 1791548798295,
        requestAction: null,
        actions: [
          { id: 'action-17', label: 'click Apply columns', status: 'passed',
            startedAtEpochMs: 1791548797432, finishedAtEpochMs: 1791548797560 },
          { id: 'action-18', label: 'click construction close operation editor', status: 'passed',
            startedAtEpochMs: 1791548798215, finishedAtEpochMs: 1791548798299 },
        ],
      },
      associatedActionId: undefined,
    },
  ];

  for (const { name, config, associatedActionId } of retainedReports) {
    const { report } = schemaFieldsCloseReport(config);
    const rawNetwork = structuredClone(report.network);
    finishReport(report);
    assert.equal(report.status, 'passed', `${name}: ${JSON.stringify(report.errors)}`);
    assert.equal(report.expectedOwnerRetirements.length, 1, name);
    assert.equal(report.expectedOwnerRetirements[0].associatedAction?.id, associatedActionId, name);
    assert.equal(report.expectedOwnerRetirements[0].closeAt, config.closeAt, name);
    assert.equal(report.expectedOwnerRetirements[0].fetchStartedAt, config.fetchStartedAt, name);
    assert.equal(report.expectedOwnerRetirements[0].requestFailedAt, config.requestFailedAt, name);
    assert.deepEqual(report.network, rawNetwork, `${name}: raw ERR_ABORTED evidence remains intact`);
  }

  const responseBeforeAbort = schemaFieldsCloseReport(retainedReports[2].config).report;
  responseBeforeAbort.nativeRequestTerminalLedger.requests[0].nativeEventChronology[1].observedAt =
    retainedReports[2].config.controllerAbortedAt - 1;
  finishReport(responseBeforeAbort);
  assert.equal(responseBeforeAbort.status, 'failed', 'an ordinary 200 response before owner abort cannot be relabeled as retirement');
  assert.equal(responseBeforeAbort.expectedOwnerRetirements?.length ?? 0, 0);

  const responseWrongRequest = schemaFieldsCloseReport(retainedReports[2].config).report;
  responseWrongRequest.nativeRequestTerminalLedger.requests[0].nativeEventChronology[1].browserRequestId = 'request-other';
  finishReport(responseWrongRequest);
  assert.equal(responseWrongRequest.status, 'failed', 'a response from another browser Request cannot be borrowed');
  assert.equal(responseWrongRequest.expectedOwnerRetirements?.length ?? 0, 0);

  const unexpectedResponseStatus = schemaFieldsCloseReport(retainedReports[2].config).report;
  unexpectedResponseStatus.nativeRequestTerminalLedger.requests[0].status = 503;
  unexpectedResponseStatus.nativeRequestTerminalLedger.requests[0].nativeEventChronology[1].status = 503;
  finishReport(unexpectedResponseStatus);
  assert.equal(unexpectedResponseStatus.status, 'failed', 'non-200 HTTP responses remain fatal');
  assert.equal(unexpectedResponseStatus.expectedOwnerRetirements?.length ?? 0, 0);

  const completedInterveningActions = schemaFieldsCloseReport({
    ...retainedReports[2].config,
    requestAction: { id: 'action-fields', label: 'click Fields and related data' },
    actions: [
      { id: 'action-fields', label: 'click Fields and related data', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt - 100,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 30 },
      { id: 'action-17', label: 'click Raw FHIR fields (advanced)', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 40,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 80 },
      { id: 'action-18', label: 'click Select Observation.component[].code.text', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 90,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 120 },
      { id: 'action-19', label: 'click Add 1 selected feature', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 130,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 160 },
      { id: 'action-20', label: 'click Component Code Text: Use the first value', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 170,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 200 },
      { id: 'action-21', label: 'click Add 1 column', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 210,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 240 },
      { id: 'action-22', label: 'click Apply columns', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.fetchStartedAt + 250,
        finishedAtEpochMs: retainedReports[2].config.fetchStartedAt + 280 },
      { id: 'action-close', label: 'close operation editor', status: 'passed',
        startedAtEpochMs: retainedReports[2].config.closeAt - 20,
        finishedAtEpochMs: retainedReports[2].config.closeAt + 40 },
    ],
  }).report;
  const unmodifiedNetwork = structuredClone(completedInterveningActions.network);
  finishReport(completedInterveningActions);
  assert.equal(completedInterveningActions.status, 'passed',
    'completed CASE-010 catalog and column actions between fetch and trusted Close do not block exact retirement');
  assert.equal(completedInterveningActions.expectedOwnerRetirements[0].associatedAction.id, 'action-fields');
  assert.deepEqual(completedInterveningActions.network, unmodifiedNetwork,
    'the original network error remains unchanged while the separate proof classifies it');
});

test('finishReport requires all non-owner actions to be passed and complete by trusted Close', () => {
  const actionStartedAt = 2600;
  const closeAt = 3000;
  const invalidActions = [
    ['failed before Close', { id: 'action-failed', status: 'failed',
      startedAtEpochMs: actionStartedAt, finishedAtEpochMs: actionStartedAt + 20 }],
    ['failed action without a start timestamp cannot be placed after Close', { id: 'action-failed-untimed', status: 'failed' }],
    ['still running at Close', { id: 'action-running', status: 'running',
      startedAtEpochMs: actionStartedAt }],
    ['marked passed only after Close', { id: 'action-late', status: 'passed',
      startedAtEpochMs: actionStartedAt, finishedAtEpochMs: closeAt + 1 }],
  ];
  for (const [description, action] of invalidActions) {
    const { report } = schemaFieldsCloseReport();
    report.actions.push(action);
    finishReport(report);
    assert.equal(report.status, 'failed', description);
    assert.equal(report.expectedOwnerRetirements?.length ?? 0, 0, description);
    assert.equal(report.assertions.find(item => item.name === 'no unexpected network, module, or browser errors')?.status,
      'failed', description);
  }
});

test('finishReport resolves a CDA projection browser ID without rewriting its raw network ID', () => {
  const networkPlaywrightRequestId = 'cda-request-raw-preserved';
  const projectedBrowserRequestId = 'playwright-browser-request-263';
  const { report } = schemaFieldsCloseReport({
    browserRequestId: projectedBrowserRequestId,
    networkPlaywrightRequestId,
    networkBrowserRequestId: projectedBrowserRequestId,
    projectedBrowserRequestId,
  });
  const rawNetwork = structuredClone(report.network);
  finishReport(report);
  assert.equal(report.status, 'passed');
  assert.equal(report.expectedOwnerRetirements[0].browserRequestId, projectedBrowserRequestId);
  assert.equal(report.nativeAbortSignalObservations[0].browserRequestId, projectedBrowserRequestId);
  assert.deepEqual(report.network, rawNetwork);
  assert.equal(report.network[0].playwrightRequestId, networkPlaywrightRequestId);

  const rawBrowserIDReport = schemaFieldsCloseReport({
    browserRequestId: projectedBrowserRequestId,
    networkPlaywrightRequestId,
    networkBrowserRequestId: projectedBrowserRequestId,
    projectedBrowserRequestId,
  }).report;
  const rawBrowserIDNetwork = structuredClone(rawBrowserIDReport.network);
  finishReport(rawBrowserIDReport);
  assert.equal(rawBrowserIDReport.status, 'passed', 'a raw exact browserRequestId takes precedence over the CDA correlation label');
  assert.equal(rawBrowserIDReport.expectedOwnerRetirements[0].browserRequestId, projectedBrowserRequestId);
  assert.deepEqual(rawBrowserIDReport.network, rawBrowserIDNetwork);

  const mismatchedBasicRawId = schemaFieldsCloseReport({
    networkPlaywrightRequestId: 'playwright-basic-raw-request',
    projectedBrowserRequestId: 'request-263',
  }).report;
  finishReport(mismatchedBasicRawId);
  assert.equal(mismatchedBasicRawId.status, 'failed', 'a CDA-style projection cannot replace a mismatched Basic raw Playwright request ID');
  assert.equal(mismatchedBasicRawId.expectedOwnerRetirements?.length ?? 0, 0);

  const mismatchedProjection = schemaFieldsCloseReport({
    networkPlaywrightRequestId,
    networkBrowserRequestId: projectedBrowserRequestId,
    projectedBrowserRequestId: 'playwright-other-request',
  }).report;
  finishReport(mismatchedProjection);
  assert.equal(mismatchedProjection.status, 'failed', 'a projection from another browser Request cannot classify this raw error');
  assert.equal(mismatchedProjection.expectedOwnerRetirements?.length ?? 0, 0);
});

test('CASE-050 request-only schema failures remain fatal beside one proven owner retirement', () => {
  const { report } = schemaFieldsCloseReport();
  const origin = new URL(report.target.uiUrl).origin;
  const project = encodeURIComponent(report.target.project);
  const explorer = encodeURIComponent(report.target.explorer);
  const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/schema-fields`;
  const requestOnlyFailures = Array.from({ length: 5 }, (_, index) => ({
    kind: 'network', method: 'POST', url: `${origin}${path}`, resourceType: 'fetch',
    errorText: 'net::ERR_ABORTED', playwrightRequestId: `cda-request-only-${index + 1}`,
    browserRequestId: `playwright-request-only-${index + 1}`,
    requestDetails: { requestId: `schema-fields-request-only-${index + 1}` },
    requestTimeline: { mainFrameNavigations: [] },
  }));
  const originalRequestOnlyFailures = structuredClone(requestOnlyFailures);
  report.network.push(...requestOnlyFailures);
  finishReport(report);
  assert.equal(report.status, 'failed');
  assert.equal(report.expectedOwnerRetirements.length, 1, 'only the separately proven request is classified');
  assert.deepEqual(report.network.slice(1), originalRequestOnlyFailures, 'all five raw request-only records stay unchanged');
  assert.equal(report.assertions.find(item => item.name === 'no unexpected network, module, or browser errors')?.evidence.count, 5);
});

test('schema-fields Close classification rejects incomplete Apply, untrusted owner retirement, or identity drift', () => {
  const mutations = [
    ['the action that began with the request did not complete', report => { report.actions[0].status = 'failed'; }],
    ['the action that began with the request completed after Close', report => { report.actions[0].finishedAtEpochMs = 3001; }],
    ['the Close target is not the construction editor', report => {
      report.nativeAbortProbeEvents[0].trustedInteractions[0].closestButton.testId = 'other-close';
    }],
    ['the Catalog owner remained attached', report => {
      report.nativeAbortProbeEvents[0].requests[0].ownerDomAtAbort.detachedAtAbort = false;
      report.nativeAbortProbeEvents[0].requests[0].ownerDomAtAbort.connectedAtAbort = true;
    }],
    ['the request correlation ID is different', report => { report.nativeAbortProbeEvents[0].requests[0].requestId = 'schema-fields-other'; }],
    ['the retained projection cannot omit the exact signal identity', report => {
      report.nativeAbortSignalObservations[0].observation = {};
    }],
    ['the UI origin is different', report => { report.nativeAbortSignalObservations[0].origin = 'http://127.0.0.1:30009'; }],
    ['the native request did not fail with ERR_ABORTED', report => {
      report.nativeRequestTerminalLedger.requests[0].nativeEventChronology[1].failure = 'net::ERR_FAILED';
    }],
    ['the native ledger has no terminal request event', report => {
      const entry = report.nativeRequestTerminalLedger.requests[0];
      entry.nativeEventChronology = entry.nativeEventChronology.slice(0, 1);
      entry.status = null;
      entry.failure = null;
      entry.terminalEvent = null;
      entry.state = 'pending';
      entry.complete = false;
    }],
    ['the browser Request object identity differs from the terminal event', report => {
      report.nativeRequestTerminalLedger.requests[0].nativeEventChronology.at(-1).objectMatch = false;
    }],
    ['a second native ledger row cannot share the browser Request identity', report => {
      report.nativeRequestTerminalLedger.requests.push({
        ...structuredClone(report.nativeRequestTerminalLedger.requests[0]), requestId: 'schema-fields-second',
      });
    }],
    ['a duplicate exact request projection cannot establish one-to-one identity', report => {
      report.nativeAbortSignalObservations.push(structuredClone(report.nativeAbortSignalObservations[0]));
    }],
    ['a duplicate raw browser request identity cannot borrow the same owner proof', report => {
      report.network.push(structuredClone(report.network[0]));
    }],
  ];
  for (const [description, mutate] of mutations) {
    const { report } = schemaFieldsCloseReport();
    mutate(report);
    finishReport(report);
    assert.equal(report.status, 'failed', description);
    assert.equal(report.expectedOwnerRetirements?.length ?? 0, 0, description);
    assert.equal(report.assertions.find(item => item.name === 'no unexpected network, module, or browser errors')?.status,
      'failed', description);
  }
});

test('schema-fields Close classification never exempts a failed mutation request', () => {
  const { report } = schemaFieldsCloseReport();
  const mutationURL = `http://127.0.0.1:30008/api/v1/projects/${encodeURIComponent(report.target.project)}/explorers/${encodeURIComponent(report.target.explorer)}/authoring/v2/commands`;
  report.network.push({
    kind: 'network', method: 'POST', url: mutationURL, resourceType: 'fetch',
    errorText: 'net::ERR_ABORTED', playwrightRequestId: 'request-mutation-failed',
    requestDetails: { requestId: 'builder-command-failed' },
    requestTimeline: { action: { id: 'action-apply', label: 'Apply exact generated field' }, mainFrameNavigations: [] },
  });
  finishReport(report);
  assert.equal(report.expectedOwnerRetirements.length, 1, 'only the separately proven schema-fields retirement is classified');
  assert.equal(report.status, 'failed', 'a failed mutation route remains fatal');
  assert(report.errors.some(error => error.url === mutationURL && error.errorText === 'net::ERR_ABORTED'));
});

test('schema-fields Close classification leaves a request-only capability drain pending and incomplete', async () => {
  const pendingProject = 'loom_dev_verify_mv0wf7xk-2d5b8e8';
  const pendingExplorer = 'verify-xk-2d5b8e8-repeated-empty';
  const pendingOrigin = 'http://127.0.0.1:30008';
  const pendingExplorerPath = `/api/v1/projects/${pendingProject}/explorers/${pendingExplorer}`;
  const pageFrame = {};
  const page = { mainFrame: () => pageFrame };
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project: pendingProject, origin: pendingOrigin });
  const requestFor = (path, method, id) => ({
    url: () => `${pendingOrigin}${path}`,
    method: () => method,
    headers: () => ({ 'x-request-id': id }),
    resourceType: () => 'fetch',
    frame: () => pageFrame,
  });
  const recordFinished = (path, method, id, status) => {
    const request = requestFor(path, method, id);
    ledger.recordRequest(request, {
      requestId: id, browserRequestId: `request-${id}`, startedAt: Date.now(),
      ...ledger.frameIdentityForRequest(request, page),
    });
    ledger.recordResponse(request, { status, observedAt: Date.now() + 1 });
    ledger.recordFinished(request, { observedAt: Date.now() + 2 });
  };
  recordFinished(`/api/v1/projects/${pendingProject}/explorers`, 'POST', 'create-explorer', 201);
  recordFinished(`${pendingExplorerPath}/authoring/v2/builder`, 'GET', 'builder-state', 200);
  const pendingID = 'cda-request-64539307-f9ee-4059-8e8d-a66e7852f2fb';
  const pendingRequest = requestFor(`${pendingExplorerPath}/authoring/v2/construction-capabilities`, 'POST', pendingID);
  ledger.recordRequest(pendingRequest, {
    requestId: pendingID, browserRequestId: 'request-396', startedAt: 1791546332191,
    ...ledger.frameIdentityForRequest(pendingRequest, page),
  });
  const snapshot = await ledger.flush(scope, { explorer: pendingExplorer, timeoutMs: 1 });
  const pendingReport = createReport({
    scenario: 'builder-authoring', caseName: 'repeated-empty',
    target: { uiUrl: pendingOrigin, project: pendingProject, explorer: pendingExplorer },
  });
  await finalizeFixtureNativeRequestReport({ report: pendingReport, ledger, project: pendingProject });
  finishReport(pendingReport);

  assert.equal(snapshot.nativeRequestTerminalLedger.complete, false);
  assert.equal(snapshot.nativeRequestTerminalLedger.counts.pending, 1);
  assert.deepEqual(snapshot.nativeRequestTerminalLedger.requests.find(item => item.requestId === pendingID).nativeEventChronology
    .map(event => event.event), ['request'], 'the capability request has no fabricated terminal event');
  assert.equal(snapshot.nativeRequestDrainEvidence[0].status, 'timed-out');
  assert.deepEqual(snapshot.nativeRequestDrainEvidence[0].unresolvedRequests.map(item => item.browserRequestId), ['request-396']);
  assert.equal(pendingReport.nativeRequestTerminalLedger.complete, false, 'report finalization retains the incomplete ledger');
  assert.equal(pendingReport.nativeRequestTerminalLedger.counts.pending, 1);
  assert.equal(pendingReport.nativeRequestDrainEvidence[0].status, 'timed-out');
  assert.equal(pendingReport.expectedOwnerRetirements, undefined, 'a pending capability read is not a schema-fields retirement');
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: pendingReport.nativeRequestTerminalLedger,
    incompleteRequests: snapshot.incompleteRequests,
    nativeRequestDrainEvidence: pendingReport.nativeRequestDrainEvidence,
    nativeRequestCorrelationErrors: pendingReport.nativeRequestCorrelationErrors,
  }), /did not reach a complete terminal state/, 'the production repeated-empty gate still rejects the request-only capability drain');
});

test('native abort probe flush waits for exposed binding messages before report projection', async () => {
  const received = [];
  const sandbox = {
    AbortController,
    URL,
    Date,
    Error,
    Headers,
    setTimeout,
    clearTimeout,
    crypto: { randomUUID: () => '12345678-1234-4123-8123-123456789abc' },
    location: { href: 'http://127.0.0.1:30008/' },
    document: { addEventListener() {}, querySelectorAll: () => [] },
    MutationObserver: class { observe() {} },
    fetch: async () => ({ ok: true }),
    __loomNativeAbortProbeBinding: payload => new Promise(resolve => {
      setTimeout(() => { received.push(JSON.parse(payload)); resolve(); }, 10);
    }),
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(createNativeAbortProbeSource({
    project: 'loom_dev_test', explorer: 'close-drain-test', apiOrigin,
  }), sandbox);
  assert.equal(received.length, 0, 'probe event is in flight before the fixture finalizer joins it');

  const page = { evaluate: expression => vm.runInNewContext(expression, sandbox) };
  const result = await flushNativeAbortProbeEvents(page, { timeoutMs: 1_000 });
  assert.equal(received.length, 1);
  assert.equal(received[0].kind, 'probe-installed');
  assert.deepEqual({ pending: result.pending, failures: result.failures }, { pending: 0, failures: 0 });
});

test('native abort probe binding failures reject the bounded report flush', async () => {
  const sandbox = {
    AbortController,
    URL,
    Date,
    Error,
    Headers,
    setTimeout,
    clearTimeout,
    crypto: { randomUUID: () => '12345678-1234-4123-8123-000000000000' },
    location: { href: 'http://127.0.0.1:30008/' },
    document: { addEventListener() {}, querySelectorAll: () => [] },
    MutationObserver: class { observe() {} },
    fetch: async () => ({ ok: true }),
    __loomNativeAbortProbeBinding: async () => { throw new Error('binding unavailable'); },
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(createNativeAbortProbeSource({
    project: 'loom_dev_test', explorer: 'close-drain-test', apiOrigin,
  }), sandbox);
  const page = { evaluate: expression => vm.runInNewContext(expression, sandbox) };
  await assert.rejects(flushNativeAbortProbeEvents(page, { timeoutMs: 1_000 }), /binding unavailable/);
});
