import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  createNativeAbortProbeSource,
  nativeAbortNetworkFailureClock,
  nativeAbortProbeEvidenceForRequest,
} from './native-abort-probe.mjs';

const project = 'loom_dev_test';
const explorer = 'abort-probe-test';
const apiPath = (endpoint) => `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/${endpoint}`;
const requestId = (prefix, tail) => `${prefix}${tail}-0000-4000-8000-000000000000`;

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

const startProbe = ({ dom } = {}) => {
  const events = [];
  const listeners = new Map();
  let now = 100;
  class FakeDate extends Date { static now() { return now; } }
  class FakeMutationObserver {
    constructor(callback) { this.callback = callback; }
    observe() {}
  }
  class FakeAbortController {
    constructor() { this.signal = { aborted: false }; }
    abort() { this.signal.aborted = true; }
  }
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
    location: { href: 'http://127.0.0.1:30008/' },
    document,
    MutationObserver: FakeMutationObserver,
    fetch: () => new Promise(() => {}),
    __loomNativeAbortProbeBinding: (payload) => events.push(JSON.parse(payload)),
  };
  sandbox.globalThis = sandbox;
  const source = createNativeAbortProbeSource({ project, explorer });
  assert.doesNotThrow(() => new Function(source));
  vm.runInNewContext(source, sandbox);
  return { sandbox, events, listeners, advanceTime: (value) => { now = value; } };
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
  const probeEvidence = nativeAbortProbeEvidenceForRequest(entry, [event]);
  assert.equal(probeEvidence[0].exactRequestSignalCorrelation, true);
  assert.equal(probeEvidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(probeEvidence[0].networkRequestId, entry.requestId);
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
  const evidence = nativeAbortProbeEvidenceForRequest(entry, [event]);
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
  assert.equal(nativeAbortProbeEvidenceForRequest(entry, events)[0].sameDocumentOwnerRetirement, false);
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
  const evidence = nativeAbortProbeEvidenceForRequest(entry, [serializedLegacyEvent]);
  assert.equal(evidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(evidence[0].ownerRetirementAction.trustEvidence, 'trusted-interaction-list-membership');

  const withoutTrustedList = { ...serializedLegacyEvent, trustedInteractions: undefined };
  const unproven = nativeAbortProbeEvidenceForRequest(entry, [withoutTrustedList]);
  assert.equal(unproven[0].sameDocumentOwnerRetirement, false);
});

test('request evidence requires exact ID, path, and abort-before-CDP-failure and never classifies a pass', () => {
  const entry = {
    requestId: 'cdp-correlation-request',
    requestCorrelationId: requestId('population-routes-', '11111111'),
    path: apiPath('population-routes'),
    method: 'POST',
    requestTimestamp: 10,
    requestWallTime: 0.1,
    loadingFailed: { timestamp: 10.1, at: 200, errorText: 'net::ERR_ABORTED', canceled: true },
  };
  const abortEvent = {
    kind: 'abort-controller-call', controllerId: 'abort-controller-1', createdAt: 100, abortedAt: 180,
    signalWasAlreadyAborted: false,
    requests: [{ requestId: entry.requestCorrelationId, path: entry.path, method: 'POST', startedAt: 150 }],
  };
  const evidence = nativeAbortProbeEvidenceForRequest(entry, [abortEvent]);
  assert.equal(evidence.length, 1);
  assert.equal(evidence[0].exactRequestSignalCorrelation, true);
  assert.equal(evidence[0].networkFailureObservedSeparately, true);
  assert.ok(Math.abs(evidence[0].abortToNetworkFailureMs - 20) < 1e-6);

  assert.deepEqual(nativeAbortProbeEvidenceForRequest(entry, [{
    ...abortEvent,
    abortedAt: 201,
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(entry, [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], requestId: 'different-id' }],
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(entry, [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], path: apiPath('construction-choices') }],
  }]), []);
  assert.deepEqual(nativeAbortProbeEvidenceForRequest(entry, [{
    ...abortEvent,
    requests: [{ ...abortEvent.requests[0], startedAt: 181 }],
  }]), []);
  assert.equal(entry.loadingFailed.errorText, 'net::ERR_ABORTED');
});
