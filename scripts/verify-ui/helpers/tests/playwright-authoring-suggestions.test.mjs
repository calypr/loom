import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  builderAuthoringSuggestionsWorkflow,
  captureSuggestionUiState,
  classifySuggestionDiagnostics,
  isOwnedRootSuggestionRequest,
  releaseSuggestionRouteAfterGate,
  sanitizedSuggestionRequestBinding,
  waitForOwnedRootSuggestionRequest,
} from '../playwright-authoring-suggestions.mjs';

const requireFromLoomUI = createRequire(new URL('../../../../ui/packages/loom-ui/package.json', import.meta.url));
const { JSDOM } = requireFromLoomUI('jsdom');

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

test('suggestion fault ownership is limited to the active root request and abort waits for the root-table gate', async () => {
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
  const pendingAbort = releaseSuggestionRouteAfterGate(route, actionCompleted);
  await Promise.resolve();
  assert.deepEqual(events, []);
  events.push('choose Patient rows and settle the exact preview');
  releaseAction(true);
  assert.equal(await pendingAbort, true);
  assert.deepEqual(events, ['choose Patient rows and settle the exact preview', 'abort:failed']);
});

test('wrong root binding is ignored and a missing root-table action continues the held route within bounds', async () => {
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
  const chronology = {};
  const route = {
    abort: async reason => { events.push(`abort:${reason}`); },
    continue: async () => { events.push('continue'); },
  };
  const actionNeverCompleted = new Promise(() => {});
  const gateStartedAt = Date.now();
  assert.equal(await releaseSuggestionRouteAfterGate(route, actionNeverCompleted, 5, chronology), false);
  assert.equal(Date.now() - gateStartedAt < 1_000, true);
  assert.deepEqual(events, ['continue']);
  assert.equal(chronology.releaseAction, 'continue');
  assert.equal(chronology.abortStartedAtMs, undefined);
  assert.equal(chronology.releaseStartedAtMs > 0, true);
  assert.equal(chronology.routeReleasedAtMs >= chronology.releaseStartedAtMs, true);
});

test('sanitized suggestion binding retains request identity without the raw snapshot token', () => {
  const binding = sanitizedSuggestionRequestBinding({
    kind: 'lazy', requestId: 'suggestions-base',
    body: { nodeId: 'n_root_123', snapshotToken: 'secret-snapshot-token' },
    observedAtMs: 1234,
  });

  assert.deepEqual(binding, {
    kind: 'lazy', endpoint: '/authoring/v2/suggestions', method: 'POST',
    attemptId: 'lazy:suggestions-base', requestId: 'suggestions-base', nodeId: 'n_root_123',
    snapshotTokenPresent: true,
    snapshotTokenSHA256: createHash('sha256').update('secret-snapshot-token').digest('hex'),
    observedAtMs: 1234,
  });
  assert.equal(JSON.stringify(binding).includes('secret-snapshot-token'), false);
});

test('suggestion UI capture reads Explorer, Raw FHIR details, and failure controls from the DOM', async () => {
  const capture = async ({ rawFieldsOpen, failureMarkup = '' }) => {
    const dom = new JSDOM(`
      <select aria-label="Explorer">
        <option value="explorer-old">Earlier run</option>
        <option value="explorer-new" selected>Verify Suggestions</option>
      </select>
      <main>
        <h1>Editing Patients</h1>
        <div role="group" aria-label="Column types">
          <button aria-pressed="true">Fields and related data</button>
        </div>
        <div role="group" aria-label="Search scope">
          <button aria-pressed="true">All accessible resources</button>
        </div>
        <details data-testid="feature-catalog-raw-fields" ${rawFieldsOpen ? 'open' : ''}>
          <summary>Raw FHIR fields</summary>
        </details>
        ${failureMarkup}
      </main>
    `);
    const { document } = dom.window;
    for (const selector of ['[data-testid="builder-suggestions-error"]',
      '[data-testid="builder-suggestions-retry"]']) {
      const element = document.querySelector(selector);
      if (element) element.getClientRects = () => [{ width: 1, height: 1 }];
    }

    const priorDocument = Object.getOwnPropertyDescriptor(globalThis, 'document');
    Object.defineProperty(globalThis, 'document', { configurable: true, value: document });
    try {
      return await captureSuggestionUiState({ evaluate: async readDom => readDom() });
    } finally {
      if (priorDocument) Object.defineProperty(globalThis, 'document', priorDocument);
      else delete globalThis.document;
      dom.window.close();
    }
  };
  const stateFields = state => {
    const { capturedAtMs, ...fields } = state;
    assert.equal(Number.isSafeInteger(capturedAtMs), true);
    return fields;
  };

  assert.deepEqual(stateFields(await capture({ rawFieldsOpen: false })), {
    status: 'captured',
    explorerId: 'explorer-new',
    explorerTitle: 'Verify Suggestions',
    tableHeading: 'Editing Patients',
    fieldsModeSelected: true,
    searchScope: 'All accessible resources',
    rawFieldsOpen: false,
    selectedOccurrenceId: null,
    selectedOccurrenceObservable: false,
    failureAlertVisible: false,
    failureAlertText: null,
    failureAlertCode: null,
    retryVisible: false,
    retryEnabled: null,
  });
  assert.deepEqual(stateFields(await capture({
    rawFieldsOpen: true,
    failureMarkup: '<div data-testid="builder-suggestions-error">Suggestion unavailable (FETCH_ERROR)</div>' +
      '<button data-testid="builder-suggestions-retry">Retry suggestions</button>',
  })), {
    status: 'captured',
    explorerId: 'explorer-new',
    explorerTitle: 'Verify Suggestions',
    tableHeading: 'Editing Patients',
    fieldsModeSelected: true,
    searchScope: 'All accessible resources',
    rawFieldsOpen: true,
    selectedOccurrenceId: null,
    selectedOccurrenceObservable: false,
    failureAlertVisible: true,
    failureAlertText: 'Suggestion unavailable (FETCH_ERROR)',
    failureAlertCode: 'FETCH_ERROR',
    retryVisible: true,
    retryEnabled: true,
  });
});

test('production suggestions workflow gates the lazy abort on the root action and settled preview', async () => {
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
  const missingAlert = new Error('builder-suggestions-error did not appear');
  let lazyRoutePromise;
  let failedAlertWaits = 0;
  const routeEvents = [];
  const actionNames = [];
  let uiPhase = 'before-root-table';
  const uiStates = {
    'before-root-table': {
      explorerId: 'explorer-new', explorerTitle: 'Verify root-table-gate suggestions',
      tableHeading: 'Build your first table', fieldsModeSelected: null, searchScope: null,
      rawFieldsOpen: false, selectedOccurrenceId: null, selectedOccurrenceObservable: false,
      failureAlertVisible: false, failureAlertText: null, failureAlertCode: null,
      retryVisible: false, retryEnabled: null,
    },
    'root-table-action': {
      explorerId: 'explorer-new', explorerTitle: 'Verify root-table-gate suggestions',
      tableHeading: 'Editing Patients', fieldsModeSelected: true, searchScope: 'All accessible resources',
      rawFieldsOpen: false, selectedOccurrenceId: null, selectedOccurrenceObservable: false,
      failureAlertVisible: false, failureAlertText: null, failureAlertCode: null,
      retryVisible: false, retryEnabled: null,
    },
    'after-failure': {
      explorerId: 'explorer-new', explorerTitle: 'Verify root-table-gate suggestions',
      tableHeading: 'Editing Patients', fieldsModeSelected: true, searchScope: 'All accessible resources',
      rawFieldsOpen: false, selectedOccurrenceId: null, selectedOccurrenceObservable: false,
      failureAlertVisible: false, failureAlertText: null, failureAlertCode: null,
      retryVisible: false, retryEnabled: null,
    },
  };

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
    abort: async reason => {
      routeEvents.push(`abort:${reason}`);
      uiPhase = 'after-failure';
      return onAbort?.();
    },
  });
  const dispatch = async req => {
    const url = new URL(req.url());
    const match = registeredRoutes.find(([matcher]) => matcher(url));
    assert(match, `No production route matched ${url.href}`);
    await match[1](routeFor(req, {
      body: { catalog: { candidates: [{ id: 'candidate-1' }] } },
      onAbort: () => report.network.push({
        kind: 'network', method: req.method(), url: req.url(), rawURL: req.url(),
        errorText: 'net::ERR_FAILED', injectedFault: true, injectedAction: 'abort',
        playwrightRequestId: 'playwright-lazy-1',
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
      method: 'POST', url: suggestionURL, requestId: 'suggestions-base',
      body: { nodeId: 'Patient', snapshotToken: 'snapshot-1' },
    }));
  };
  const locator = label => ({
    count: async () => 1,
    click: async () => {},
    fill: async () => {},
    check: async () => {},
    waitFor: async options => {
      if (label === 'feature-catalog-raw-fields' && options?.state === 'visible') {
        rawFieldsVisibleWaits += 1;
      }
      if (label === 'builder-suggestions-error' && options?.state === 'visible') {
        failedAlertWaits += 1;
        await lazyRoutePromise;
        throw missingAlert;
      }
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
    evaluate: async () => uiStates[uiPhase],
    getByText: name => locator(name),
    getByRole: (role, options) => locator(`${role}:${options?.name}`),
    getByTestId: testId => testId === 'preview-table-scroll' ? previewScroll : locator(testId),
    locator: selector => locator(selector),
  };
  const action = async (name, _target, perform, options = {}) => {
    actionNames.push(name);
    await perform();
    if (name === 'create blank Explorer') {
      await dispatch(request({ method: 'GET', url: builderURL, requestId: 'builder-new-1' }));
    } else if (name === 'choose Patient rows') {
      await dispatch(request({
        method: 'POST', url: suggestionURL, requestId: 'first-table-suggestions-1',
        body: { nodeId: 'Patient', snapshotToken: 'snapshot-1' },
      }));
      uiPhase = 'root-table-action';
      startLazyRequest();
    }
  };

  await assert.rejects(
    builderAuthoringSuggestionsWorkflow({
      page, report, action,
      check: (dimension, name, passed) => report.checks.push({ dimension, name, passed }),
    }, {
      target: workflowTarget,
      runID: 'workflow-binding-regression',
    }),
    error => error === missingAlert,
  );
  assert.equal(failedAlertWaits, 1);
  assert.equal(report.network.length, 1);
  assert.deepEqual(routeEvents, ['abort:failed']);
  assert.equal(actionNames.includes('open Add columns'), false,
    'the lazy root request is gated before any later Add columns or Raw FHIR actions');
  const chronology = report.target.suggestionsFaultChronology;
  assert.equal(chronology.firstTable.requestId, 'first-table-suggestions-1');
  assert.equal(chronology.firstTable.responseStatus, 200);
  assert.deepEqual(chronology.rootTableAction, {
    name: 'choose Patient rows',
    startedAtMs: chronology.rootTableAction.startedAtMs,
    startedSequence: chronology.rootTableAction.startedSequence,
    actionCompleted: true,
    firstTableRequestId: 'first-table-suggestions-1',
    firstTableResponseRequestId: 'first-table-suggestions-1',
    firstTableResponseStatus: 200,
    firstTableResponseOK: true,
    previewIds: ['dev-patient-001', 'dev-patient-002'],
    previewMatchesFixture: true,
    completed: true,
    completedAtMs: chronology.rootTableAction.completedAtMs,
    completedSequence: chronology.rootTableAction.completedSequence,
  });
  assert.equal(chronology.lazy.attemptId, 'lazy:suggestions-base');
  assert.equal(chronology.lazy.nodeId, chronology.firstTable.nodeId);
  assert.equal(chronology.lazy.snapshotTokenSHA256, chronology.firstTable.snapshotTokenSHA256);
  assert.equal(chronology.lazy.sameRootNodeAsFirstTable, true);
  assert.equal(chronology.lazy.sameSnapshotAsFirstTable, true);
  assert.equal(chronology.lazy.requestDuringRootTableAction, true);
  assert.equal(chronology.lazy.sequence < chronology.rootTableAction.completedSequence, true);
  assert.equal(chronology.lazy.actionCompletedBeforeAbort, true);
  assert.equal(chronology.lazy.releaseAction, 'abort');
  assert.equal(chronology.lazy.uiBeforeAbort.rootTableActionCompleted, true);
  assert.equal(chronology.lazy.uiBeforeAbort.firstTableResponseStatus, 200);
  assert.deepEqual(chronology.lazy.uiBeforeAbort.previewIds, ['dev-patient-001', 'dev-patient-002']);
  assert.equal(chronology.lazy.uiBeforeAbort.previewMatchesFixture, true);
  assert.equal(chronology.rootTableAction.completedSequence < chronology.lazy.uiBeforeAbortSequence, true);
  assert.equal(chronology.lazy.abortAction, 'failed');
  assert.equal(chronology.lazy.routeReleased, true);
  assert.equal(chronology.lazy.routeReleasedAtMs >= chronology.lazy.abortStartedAtMs, true);
  assert.equal(chronology.lazy.failureRecordCaptured, true, JSON.stringify({
    lazyRequestId: chronology.lazy.requestId,
    network: report.network,
  }));
  assert.equal(chronology.lazy.requestOccurrenceId, 'base');
  assert.equal(chronology.lazy.uiAtRequest.explorerId, 'explorer-new');
  assert.equal(chronology.lazy.uiAtRequest.explorerMatchesRequest, true);
  assert.equal(chronology.lazy.catchOutcome.requestFailureObserved, true);
  assert.equal(chronology.lazy.catchOutcome.failureAlertVisible, false);
  assert.equal(chronology.lazy.catchOutcome.appErrorCodeFromVisibleAlert, null);
  assert.equal(chronology.lazy.catchOutcome.requestIdentity.occurrenceIdFromRequestId, 'base');
  assert.equal(chronology.lazy.catchOutcome.catchVisibleIdentity.explorerMatchesRequest, true);
  assert.equal(chronology.lazy.catchOutcome.catchVisibleIdentity.selectedOccurrenceObservable, false);
  assert.equal(chronology.lazy.catchOutcome.catchVisibleIdentity.snapshotTokenObservable, false);
  assert.equal(report.checks.some(check => check.name === 'injected lazy route was released after the exact root preview settled' && check.passed), true);
  assert.equal(report.checks.some(check => check.name === 'native Patient root preview renders exact independent fixture IDs before candidate selection' && check.passed), true);
  assert.equal(report.checks.some(check => check.name === 'lazy suggestion failure UI state was captured for the owned request' && check.passed), true);
  assert.equal(report.checks.some(check => check.name === 'one failed lazy suggestion request exposes an actionable retry control' && !check.passed), true);
  assert.deepEqual(chronology.lazy.catchOutcome, {
    observedAtMs: chronology.lazy.catchOutcome.observedAtMs,
    sequence: chronology.lazy.catchOutcome.sequence,
    requestFailureObserved: true,
    requestErrorText: 'net::ERR_FAILED',
    failureAlertVisible: false,
    failureAlertText: null,
    appErrorCodeFromVisibleAlert: null,
    retryVisible: false,
    requestIdentity: {
      projectId: target.fixtureProject,
      explorerId: 'explorer-new',
      occurrenceIdFromRequestId: 'base',
      nodeId: 'Patient',
      snapshotTokenSHA256: createHash('sha256').update('snapshot-1').digest('hex'),
    },
    catchVisibleIdentity: {
      explorerId: 'explorer-new',
      explorerMatchesRequest: true,
      selectedOccurrenceId: null,
      selectedOccurrenceObservable: false,
      snapshotTokenSHA256: null,
      snapshotTokenObservable: false,
    },
  });
  assert.equal(JSON.stringify(report).includes('snapshot-1'), false);
});
