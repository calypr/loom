import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {
  createNativeAbortProbeSource,
  nativeAbortProbeEvidenceForRequest,
} from '../native-abort-probe.mjs';
import { classifyExpectedOwnedCancellation } from '../native-request-ownership.mjs';

const project = 'loom_dev_group_edit_test';
const explorer = 'group-edit-cancel-test';
const endpoint = 'population-routes';
const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/${endpoint}`;
const correlationId = 'population-routes-11111111-0000-4000-8000-000000000000';
const scope = { project, explorer };

const selectorMatches = (node, selector) =>
  (selector === '[aria-label="Starting collection"]' && node.getAttribute('aria-label') === 'Starting collection') ||
  (selector === '[role="dialog"][aria-label="Row definition settings"]' &&
    node.getAttribute('role') === 'dialog' && node.getAttribute('aria-label') === 'Row definition settings') ||
  (selector === 'button' && node.tagName === 'BUTTON');

const node = ({ tagName = 'DIV', attributes = {}, text = '', parent } = {}) => {
  const element = {
    tagName,
    attributes,
    textContent: text,
    parentElement: parent,
    children: [],
    isConnected: true,
    getAttribute(name) { return attributes[name] ?? null; },
    closest(selector) {
      for (let current = element; current; current = current.parentElement) {
        if (selectorMatches(current, selector)) return current;
      }
      return null;
    },
  };
  parent?.children.push(element);
  return element;
};

const runPageProbe = ({ trusted = true, action = 'construction-action-group-rows', detachOwner = true,
  requestSignal = 'owned' } = {}) => {
  let now = 100;
  const events = [];
  const listeners = new Map();
  const dialog = node({ attributes: { role: 'dialog', 'aria-label': 'Row definition settings' } });
  const owner = node({ attributes: { 'aria-label': 'Starting collection' }, parent: dialog });
  const button = node({ tagName: 'BUTTON', attributes: { 'data-testid': action }, text: 'Continue', parent: dialog });
  const document = {
    addEventListener(type, listener) { listeners.set(type, listener); },
    querySelectorAll(selector) {
      return [dialog, owner, button].filter((element) => selectorMatches(element, selector));
    },
  };
  class FakeDate extends Date { static now() { return now; } }
  class FakeMutationObserver { constructor() {} observe() {} }
  class FakeAbortController {
    constructor() { this.signal = { aborted: false }; }
    abort() { this.signal.aborted = true; }
  }
  const sandbox = {
    AbortController: FakeAbortController,
    Date: FakeDate,
    Error,
    JSON,
    MutationObserver: FakeMutationObserver,
    Number,
    Object,
    Promise,
    Reflect,
    Set,
    String,
    URL,
    WeakMap,
    document,
    fetch: () => new Promise(() => {}),
    location: { href: 'http://127.0.0.1:30008/' },
    __loomNativeAbortProbeBinding: (payload) => events.push(JSON.parse(payload)),
  };
  sandbox.globalThis = sandbox;
  vm.runInNewContext(createNativeAbortProbeSource(scope), sandbox);

  const requestController = new sandbox.AbortController();
  const abortController = requestSignal === 'owned' ? requestController : new sandbox.AbortController();
  sandbox.fetch(`http://127.0.0.1:8188${path}`, {
    method: 'POST',
    headers: { 'X-Request-ID': correlationId },
    signal: requestController.signal,
  });
  now = 105;
  listeners.get('click')({ isTrusted: trusted, target: button });
  now = 108;
  if (detachOwner) {
    owner.isConnected = false;
    dialog.isConnected = false;
  }
  now = 110;
  abortController.abort();
  return events.find((event) => event.kind === 'abort-controller-call');
};

const canceledEntry = (overrides = {}) => ({
  requestId: '87.13',
  cdpRequestId: 'cdp-87.13',
  cdpRequestMatchCount: 1,
  requestCorrelationId: correlationId,
  method: 'POST',
  path,
  scopeProject: project,
  scopeExplorer: explorer,
  resourceType: 'Fetch',
  request: {
    snapshotToken: 'snapshot-opaque',
    outputId: 'output-opaque',
    selectionRevisionId: 'selection-opaque',
  },
  frameId: 'main-frame',
  loaderId: 'active-loader',
  startedAt: 100,
  requestTimestamp: 1,
  requestWallTime: 0.1,
  status: undefined,
  responseReceivedAt: undefined,
  networkTerminal: true,
  bodyReadStatus: 'failed',
  complete: false,
  loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true, timestamp: 1.012 },
  initiator: { type: 'script', stack: [
    { url: 'http://localhost/ui/api.ts', functionName: 'searchPopulationRoutes' },
    { url: 'http://localhost/ui/components/PopulationPanel.tsx', functionName: 'loadPage' },
  ] },
  ...overrides,
});

const cancellationDecision = (entry, events) => {
  const probeEvidence = nativeAbortProbeEvidenceForRequest(entry, events);
  const result = classifyExpectedOwnedCancellation({ ...entry, abortControllerProbeEvidence: probeEvidence }, {
    frameNavigations: [],
    executionContextRetirements: [],
  });
  return { probeEvidence, result };
};

test('group-edit read cancellation needs one exact trusted owner-retirement chain', () => {
  const event = runPageProbe();
  const entry = canceledEntry();
  const { probeEvidence, result } = cancellationDecision(entry, [event]);

  assert.equal(event.signalWasAlreadyAborted, false);
  assert.deepEqual(event.requests.map(({ requestId, path: requestPath, method }) => ({
    requestId, path: requestPath, method,
  })), [{ requestId: correlationId, path, method: 'POST' }]);
  assert.equal(probeEvidence.length, 1);
  assert.equal(probeEvidence[0].networkRequestId, entry.cdpRequestId);
  assert.equal(probeEvidence[0].exactRequestSignalCorrelation, true);
  assert.equal(probeEvidence[0].networkFailureObservedSeparately, true);
  assert.equal(probeEvidence[0].sameDocumentOwnerRetirement, true);
  assert.equal(probeEvidence[0].ownerDomAtAbort.anchorId, probeEvidence[0].ownerDomAtFetch.anchorId);
  assert.equal(probeEvidence[0].ownerDomAtFetch.ruleOwner, 'population-route-options');
  assert.equal(probeEvidence[0].ownerRetirementAction.closestButton.testId, 'construction-action-group-rows');
  assert.equal(probeEvidence[0].ownerRetirementAction.trustEvidence, 'native-event-isTrusted-true');
  assert.equal(result.expected, true);
  assert.equal(result.ownerRetirement.owner, 'population-route-options');
  assert.equal(result.ownerRetirement.networkCompleted, false);

  const wrongRequestId = canceledEntry({ requestCorrelationId: 'population-routes-22222222-0000-4000-8000-000000000000' });
  assert.equal(cancellationDecision(wrongRequestId, [event]).result.expected, false, 'different X-Request-ID');
  const wrongProject = canceledEntry({ path: path.replace(`/projects/${project}/`, '/projects/other-project/') });
  assert.equal(cancellationDecision(wrongProject, [event]).result.expected, false, 'different project path');
  const wrongPath = canceledEntry({ path: path.replace(`/explorers/${explorer}/`, '/explorers/other-explorer/') });
  assert.equal(cancellationDecision(wrongPath, [event]).result.expected, false, 'different project/explorer path');
  const wrongMethod = canceledEntry({ method: 'GET' });
  assert.equal(cancellationDecision(wrongMethod, [event]).result.expected, false, 'different method');
  const wrongScope = canceledEntry({ scopeExplorer: 'other-explorer' });
  assert.equal(cancellationDecision(wrongScope, [event]).result.expected, false, 'CDP scope disagrees with request path');
  const missingCAS = canceledEntry({ request: { snapshotToken: 'snapshot-opaque', outputId: 'output-opaque' } });
  assert.equal(cancellationDecision(missingCAS, [event]).result.expected, false, 'request omits selection revision CAS');

  const untrusted = runPageProbe({ trusted: false });
  assert.equal(cancellationDecision(entry, [untrusted]).result.expected, false, 'synthetic click');
  const wrongAction = runPageProbe({ action: 'unrelated-button' });
  assert.equal(cancellationDecision(entry, [wrongAction]).result.expected, false, 'unrelated trusted click');
  const attachedOwner = runPageProbe({ detachOwner: false });
  assert.equal(cancellationDecision(entry, [attachedOwner]).result.expected, false, 'captured owner remains attached');

  const wrongOwner = structuredClone(event);
  wrongOwner.requests[0].ownerDomAtAbort.anchorId = 'replacement-owner-node';
  assert.equal(cancellationDecision(entry, [wrongOwner]).result.expected, false, 'different node reported at abort');

  const noSignalOwner = runPageProbe({ requestSignal: 'different' });
  assert.equal(cancellationDecision(entry, [noSignalOwner]).result.expected, false, 'abort controller did not own fetch signal');
  const noClock = canceledEntry({ requestTimestamp: undefined, loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true } });
  assert.equal(cancellationDecision(noClock, [event]).result.expected, false, 'missing request-calibrated CDP clock');
});
