import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  createNativeAbortProbeSource,
  nativeAbortNetworkFailureClock,
  nativeAbortProbeEvidenceForRequest,
  nativeAbortSignalObservationForRequest,
} from '../native-abort-probe.mjs';
import { classifyExpectedOwnedCancellation, nativeReadRequestMatchesExpectedScope } from '../native-request-ownership.mjs';

const project = 'loom_dev_test';
const explorer = 'abort-probe-test';
const apiOrigin = 'http://127.0.0.1:8188';
const apiPath = (endpoint) => `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/${endpoint}`;
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

const startProbe = ({ dom, origin = apiOrigin } = {}) => {
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
      const promise = new Promise((_resolve, reject) => {
        args[1]?.signal?.addEventListener?.('abort', () => reject(new Error('The operation was aborted.')));
      });
      promise.catch(() => {});
      return promise;
    },
    __loomNativeAbortProbeBinding: (payload) => events.push(JSON.parse(payload)),
  };
  sandbox.globalThis = sandbox;
  const source = createNativeAbortProbeSource({ project, explorer, apiOrigin: origin });
  assert.doesNotThrow(() => new Function(source));
  vm.runInNewContext(source, sandbox);
  return { sandbox, events, listeners, fetchCalls, advanceTime: (value) => { now = value; } };
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
