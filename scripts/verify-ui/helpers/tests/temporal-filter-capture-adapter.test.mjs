import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { chromium } from 'playwright';
import { waitForBuilderRenderedGrid } from '../builder-rendered-grid.mjs';
import { createTemporalFilterCapture } from '../temporal-filter-capture.mjs';
import { adaptTemporalFilterJourney } from '../temporal-filter-trace-adapter.mjs';
import { fixtureProjectExplorerCollectionPath } from '../fixture-context.mjs';
import { matchesTemporalExplorerCreateResponse } from '../../workflows/basic-temporal-filter.mjs';
import {
  makeTemporalFilterJourneyCaptureFixture,
  TEMPORAL_FILTER_FIXTURE_BASE_TIME,
} from './fixtures/temporal-filter-journey-schema-fixture.mjs';

test('Temporal lifecycle recorder is installed before the first navigation can issue authoring requests', () => {
  const source = readFileSync(new URL('../../workflows/basic-temporal-filter.mjs', import.meta.url), 'utf8');
  const captureStart = source.indexOf('createTemporalFilterCapture({ page, networkObserver, initialMountEpoch: 0 })');
  const firstNavigation = source.indexOf('await page.goto(');
  assert(captureStart >= 0 && firstNavigation >= 0 && captureStart < firstNavigation,
    'Capture event handlers must be installed before the bootstrap Builder navigation starts.');
});

test('DATE Explorer creation matcher accepts the retained canonical browser route and rejects the storage alias route', () => {
  const project = 'loom_dev_verify_mvo2jf3p-3be4623';
  const title = 'Verify 3p-3be4623 Patient birthDate filter';
  const uiOrigin = 'http://127.0.0.1:30000';
  const response = {
    url: () => `${uiOrigin}/api/v1/projects/loom_dev_verify_mvo2jf3p%252F3be4623/explorers`,
    status: () => 201,
    request: () => ({
      method: () => 'POST',
      postDataJSON: () => ({ name: title, title }),
    }),
  };
  const browserPath = fixtureProjectExplorerCollectionPath(project);
  const storageAliasPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;

  assert.equal(response.status(), 201, 'The retained browser observation is the successful Explorer creation response.');
  assert.equal(new URL(response.url()).pathname, browserPath);
  assert.notEqual(storageAliasPath, browserPath,
    'The direct API storage alias route is not the canonical browser-proxy path for this fixture project.');
  assert.equal(matchesTemporalExplorerCreateResponse(response, { uiOrigin, explorerCollectionPath: browserPath, title }), true);
  assert.equal(matchesTemporalExplorerCreateResponse(response, { uiOrigin, explorerCollectionPath: storageAliasPath, title }), false);
  assert.equal(matchesTemporalExplorerCreateResponse(response, { uiOrigin: 'http://127.0.0.1:8180', explorerCollectionPath: browserPath, title }), false);
  assert.equal(matchesTemporalExplorerCreateResponse(response, { uiOrigin, explorerCollectionPath: browserPath, title: 'other Explorer' }), false);
});

test('native preview grid gate accepts CSS-uppercased Patient ID and rejects wrong tables or rows', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(async () => browser.close());
  const page = await browser.newPage();
  await page.setContent(`
    <style>
      .preview-header-row { text-transform: uppercase; }
    </style>
    <section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="patient-output">
      <div data-testid="preview-table-scroll">
        <div role="table" aria-rowcount="3">
          <div role="row" class="preview-header-row"><div role="columnheader"><span>Patient ID</span></div></div>
          <div role="row"><div role="cell">dev-patient-001</div></div>
          <div role="row"><div role="cell">dev-patient-002</div></div>
        </div>
      </div>
    </section>
    <section data-testid="other-preview">
      <div data-testid="preview-table-scroll">
        <div role="table" aria-rowcount="3">
          <div role="row" class="preview-header-row"><div role="columnheader"><span>Patient ID</span></div></div>
          <div role="row"><div role="cell">foreign-patient-001</div></div>
          <div role="row"><div role="cell">foreign-patient-002</div></div>
        </div>
      </div>
    </section>
  `);
  const bodyText = await page.locator('body').innerText();
  assert.equal(bodyText.includes('Patient ID'), false,
    'CSS text-transform reproduces the retained uppercase body text that broke the broad substring check.');
  assert.equal(bodyText.includes('PATIENT ID'), true);

  const expectedRows = [
    { 'Patient ID': 'dev-patient-001' },
    { 'Patient ID': 'dev-patient-002' },
  ];
  const tableSelector = '[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]';
  await waitForBuilderRenderedGrid(page, { tableSelector, expectedRows }, 500);
  await assert.rejects(
    () => waitForBuilderRenderedGrid(page, {
      tableSelector: '[data-testid="other-preview"] [data-testid="preview-table-scroll"] [role="table"]',
      expectedRows,
    }, 100),
    /Timeout/,
    'A different Preview table with foreign Patient IDs must not satisfy the DATE source oracle.',
  );
  await page.locator('[data-testid="construction-preview"] [role="cell"]').first()
    .evaluate(cell => { cell.textContent = 'dev-patient-999'; });
  await assert.rejects(
    () => waitForBuilderRenderedGrid(page, { tableSelector, expectedRows }, 100),
    /Timeout/,
    'A wrong Patient ID must not satisfy the exact DATE source oracle.',
  );
});

test('capture retains and drains a construction-capabilities request that settles after first navigation', async () => {
  const page = new EventEmitter();
  const mainFrame = {};
  page.mainFrame = () => mainFrame;
  const request = {};
  const now = Date.now();
  const entry = {
    index: 0,
    browserRequestId: 'bootstrap-construction-capabilities',
    requestId: 'builder-bootstrap-1',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/bootstrap-temporal/authoring/v2/construction-capabilities',
    status: 200,
    request: { outputId: 'bootstrap-output' },
    response: { capabilities: [] },
    startedAt: now,
    responseReceivedAt: null,
    completedAt: null,
    responseMountEpoch: null,
    failureMountEpoch: null,
    isMutation: false,
    failure: null,
    readCancellationDiagnostic: false,
    responseCapture: 'pending',
    responseCaptureComplete: false,
    responseBodyCaptured: false,
    atMonotonicMs: 10,
    atEpochMs: now,
    mountEpoch: 0,
  };
  let settled = false;
  let waitForSettlement;
  const networkObserver = {
    requests: [],
    byRequest: new Map(),
    requestBody: observed => observed.request,
    responseBody: observed => observed.response,
    waitForResponse: async (predicate, { fromIndex = 0 } = {}) => {
      const observed = networkObserver.requests.slice(fromIndex).find(predicate);
      assert(observed, 'The pending bootstrap request must be present in the network observer ledger.');
      if (settled) return observed;
      return new Promise(resolve => { waitForSettlement = () => resolve(observed); });
    },
  };
  const capture = createTemporalFilterCapture({ page, networkObserver, initialMountEpoch: 0 });
  networkObserver.requests.push(entry);
  networkObserver.byRequest.set(request, entry);
  page.emit('request', request);
  page.emit('framenavigated', mainFrame);
  const finished = capture.finish({ resourceType: 'Patient', bootstrapExplorerId: 'bootstrap-temporal' });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(settled, false, 'Capture.finish must wait for the outstanding request instead of dropping it.');
  assert.equal(typeof waitForSettlement, 'function', 'Capture.finish must register for the request settlement.');
  entry.responseReceivedAt = now + 1;
  entry.completedAt = now + 2;
  entry.responseMountEpoch = 1;
  entry.responseCapture = 'captured';
  entry.responseCaptureComplete = true;
  entry.responseBodyCaptured = true;
  settled = true;
  page.emit('response', { request: () => request });
  waitForSettlement();

  const raw = await finished;
  assert.deepEqual(raw.requestEvents.map(observed => observed.browserRequestId), ['bootstrap-construction-capabilities']);
  assert.equal(raw.requestEvents[0].mountEpoch, 0);
  assert.equal(raw.requestEvents[0].responseMountEpoch, 1);
  assert.equal(raw.requestEvents[0].status, 200);
});

test('capture windows retain late reconcile IDs and their response-mount DOM evidence', async () => {
  const page = new EventEmitter();
  const mainFrame = {};
  page.mainFrame = () => mainFrame;
  page.evaluate = async () => ({
    proposalStatus: null,
    proposalId: null,
    receiptId: 'late-reconcile-receipt',
    outputId: 'temporal-output',
    columnId: null,
    inputType: null,
    inputValue: null,
    operator: null,
    rowIds: ['dev-patient-001', 'dev-patient-002'],
  });
  const networkObserver = {
    requests: [],
    byRequest: new Map(),
    requestBody: entry => entry.request,
    responseBody: entry => entry.response,
    waitForResponse: async (predicate, { fromIndex = 0 } = {}) => {
      const entry = networkObserver.requests.slice(fromIndex).find(predicate);
      assert(entry, 'The capture must settle an actual observed request.');
      return entry;
    },
  };
  const capture = createTemporalFilterCapture({ page, networkObserver, initialMountEpoch: 2 });
  const addReconcile = (name, at) => {
    const request = {};
    const entry = {
      index: networkObserver.requests.length,
      browserRequestId: name,
      requestId: `request-${name}`,
      method: 'POST',
      path: '/api/v1/projects/p/explorers/e/authoring/v2/reconcile',
      request: { snapshotToken: 'snapshot-3', draftVersion: 3, draftDigest: 'draft-3', outputId: 'temporal-output' },
      response: { snapshotToken: 'snapshot-3', receiptId: 'late-reconcile-receipt',
        outputs: [{ outputId: 'temporal-output' }] },
      status: 200,
      startedAt: at,
      responseReceivedAt: at + 1,
      completedAt: at + 2,
      isMutation: false,
      failure: null,
      readCancellationDiagnostic: false,
      responseCapture: 'captured',
      responseCaptureComplete: true,
      responseBodyCaptured: true,
      atMonotonicMs: at,
      atEpochMs: at,
      mountEpoch: 2,
      request,
    };
    networkObserver.requests.push(entry);
    networkObserver.byRequest.set(request, entry);
    page.emit('request', request);
    return { entry, request };
  };

  const first = addReconcile('late-reconcile-1', 10);
  const firstWindow = capture.requestWindow(0);
  assert.equal(firstWindow.endIndex, 1);
  assert.deepEqual(firstWindow.entries.map(entry => entry.browserRequestId), ['late-reconcile-1']);
  const second = addReconcile('late-reconcile-2', 20);
  const nextWindow = capture.requestWindow(firstWindow.endIndex);
  assert.equal(nextWindow.endIndex, 2);
  assert.deepEqual(nextWindow.entries.map(entry => entry.browserRequestId), ['late-reconcile-2']);

  page.emit('framenavigated', mainFrame);
  for (const request of [first, second]) {
    page.emit('response', { request: () => request.request });
  }
  const rawCapture = await capture.finish({ resourceType: 'Patient' });
  assert.deepEqual(rawCapture.lateReconcileCaptureIds, ['late-reconcile-1', 'late-reconcile-2']);
  assert.equal(rawCapture.finalDOMObservationIndex, 0);
  assert.equal(rawCapture.domObservations[0].receiptId, 'late-reconcile-receipt');
  assert.equal(rawCapture.domObservations[0].mountEpoch, 3);
  assert.equal(rawCapture.requestEvents[0].mountEpoch, 2);
  assert.equal(rawCapture.requestEvents[0].responseMountEpoch, 3);
  assert.equal(rawCapture.requestEvents[0].failureMountEpoch, null);
});

test('adapter schema fixtures preserve DATE evidence as canonical events without claiming browser proof', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  assert.equal(journeyTrace.model, 'TemporalFilterJourney');
  assert.equal(journeyTrace.name, 'basic-temporal-filter:date');
  assert.equal(journeyTrace.captureKind, 'builder-temporal-native-lifecycle');
  assert.equal(journeyTrace.actions.length, 16);
  assert(journeyTrace.actions.every(action => action.label === action.action),
    'Every action must expose the exact common label alias expected by the shared verifier.');
  assert(journeyTrace.actions.every(action => action.event === action.action),
    'Every common event binding must name the exact TLA transition derived from the raw step.');
  assert.deepEqual(journeyTrace.nativeRequests, rawCapture.requestEvents.map(request => ({
    index: request.index,
    browserRequestId: request.browserRequestId,
    requestId: request.requestId ?? null,
    method: request.method,
    path: request.path,
    startedAt: request.startedAt,
    isMutation: request.isMutation,
  })), 'The common request ledger must retain the actual observer identity and order.');
  assert.deepEqual(journeyTrace.temporalFilterLifecycle, rawCapture);
  assert.equal(journeyTrace.initialObservation.snapshotToken, 'snapshot-1');
  assert(rawCapture.builderReads.every(read => read.snapshotToken === `snapshot-${read.draftVersion}`));
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposal.filter, {
    intent: 'create',
    proposalId: 'proposal-first-create',
    receiptId: 'proposal-first-create',
    stepId: 'candidate-create-filter',
    outputId: 'temporal-output',
    columnId: 'birth-date-column',
    valueKind: 'DATE',
    operator: 'GT',
    requestValue: { kind: 'DATE', date: '1980-01-02' },
    responseValue: { kind: 'DATE', date: '1980-01-02' },
    savedValue: null,
  });
  assert.equal(proposal.request.captureId, 'schema-fixture-browser-request-first-create');
  assert.equal(proposal.request.requestId, 'schema-fixture-raw-request-first-create');
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').request.isMutation, true);
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').filter.proposalId,
    'proposal-second-create-proposal');
  assert.equal(journeyTrace.actions.find(event => event.action === 'FilterApplied(create)').filter.receiptId,
    'proposal-second-create-proposal');
  assert.equal(proposal.request.isMutation, false);
  assert.equal(journeyTrace.actions.find(event => event.action === 'Reload').request, null);
  const edited = journeyTrace.actions.find(event => event.action === 'FilterApplied(edit)');
  assert.equal(edited.filter.proposalId, 'proposal-edit');
  assert.equal(edited.filter.receiptId, 'proposal-edit');
  const editProposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(edit)');
  assert.equal(editProposal.filter.proposalId, 'proposal-edit');
  assert.equal(editProposal.filter.receiptId, 'proposal-edit');
  const removed = journeyTrace.actions.find(event => event.action === 'FilterRemoved');
  assert.equal(removed.filter.proposalId, 'proposal-remove');
  assert.equal(removed.filter.receiptId, 'proposal-remove');
  const removeProposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(remove)');
  assert.equal(removeProposal.filter.proposalId, 'proposal-remove');
  assert.equal(removeProposal.filter.receiptId, 'proposal-remove');
  const choiceProposal = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-choice-proposal');
  assert.deepEqual(choiceProposal.response.candidateColumnIds, ['birth-date-column']);
  assert.equal(choiceProposal.request.expectedDraftVersion, 2);
  assert.equal(choiceProposal.request.expectedDraftDigest, 'draft-2');
  const choiceApply = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-choice-apply').request.commands[0];
  assert.deepEqual(choiceApply.constructionChoice, { choiceId: 'temporal-field-choice', form: 'VALUE' });
  assert.equal(Object.hasOwn(choiceApply, 'columnId'), false);
  const sourceCreate = rawCapture.requestEvents.find(event => event.browserRequestId ===
    'schema-fixture-browser-request-source-table');
  assert.equal(sourceCreate.request.expectedDraftVersion, 1);
  assert.equal(sourceCreate.request.expectedDraftDigest, 'draft-1');
  const ordinaryProposalRequests = rawCapture.requestEvents.filter(request =>
    request.method === 'POST' && request.path.endsWith('/construction-proposals'));
  const ownerActions = journeyTrace.actions.filter(action => action.owner?.kind === 'proposal');
  assert.deepEqual(ownerActions.map(action => action.owner.browserRequestId).sort(),
    ordinaryProposalRequests.map(request => request.browserRequestId).sort(),
    'Every ordinary proposal owner must be derived from its physical observer request.');
  for (const action of ownerActions) {
    const request = ordinaryProposalRequests.find(candidate => candidate.browserRequestId === action.owner.browserRequestId);
    assert.equal(action.owner.mountEpoch, request.mountEpoch);
  }
});

test('adapter keeps repeated wire request IDs distinct by observer index and browser request ID', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const reconciles = rawCapture.requestEvents.filter(request => request.path.endsWith('/reconcile'));
  assert(reconciles.length >= 2);
  reconciles[0].requestId = 'builder-1-1';
  reconciles[1].requestId = 'builder-1-1';
  assert.notEqual(reconciles[0].mountEpoch, reconciles[1].mountEpoch);
  assert.notEqual(reconciles[0].request.draftVersion, reconciles[1].request.draftVersion);

  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const repeatedWireRequests = journeyTrace.nativeRequests.filter(request => request.requestId === 'builder-1-1');
  assert.equal(repeatedWireRequests.length, 2);
  assert.notEqual(repeatedWireRequests[0].browserRequestId, repeatedWireRequests[1].browserRequestId);
  assert.notEqual(repeatedWireRequests[0].index, repeatedWireRequests[1].index);
});

test('adapter binds each reload to its actual reconcile request, CAS snapshot, and DOM receipt', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const reloads = journeyTrace.actions.filter(event => event.action === 'Reload');
  const reconcileRequests = rawCapture.requestEvents.filter(event => event.method === 'POST' &&
    event.path.endsWith('/reconcile'));
  assert.equal(reloads.length, 4);
  assert.equal(reconcileRequests.length, 4);
  assert.equal(new Set(reloads.flatMap(event => event.reconcileCaptureIds)).size, 4);

  const precedingProposals = [
    journeyTrace.actions.find(event => event.action === 'FilterProposed(create)'),
    journeyTrace.actions.filter(event => event.action === 'FilterProposed(create)')[1],
    journeyTrace.actions.find(event => event.action === 'FilterProposed(edit)'),
    journeyTrace.actions.find(event => event.action === 'FilterProposed(remove)'),
  ];
  for (const [index, reload] of reloads.entries()) {
    assert.equal(reload.reconcileCaptureIds.length, 1);
    const captureId = reload.reconcileCaptureIds[0];
    const request = reconcileRequests.find(entry => entry.browserRequestId === captureId);
    assert(request, 'Reload reconcile link must use the real observer browserRequestId.');
    assert.equal(request.isMutation, false);
    assert.equal(request.mountEpoch, reload.mountEpoch);
    assert.equal(request.request.snapshotToken, reload.after.snapshotToken);
    assert.equal(request.request.draftVersion, reload.after.draftVersion);
    assert.equal(request.request.draftDigest, reload.after.draftDigest);
    assert.equal(request.request.outputId, rawCapture.outputId);
    assert.equal(request.response.snapshotToken, reload.after.snapshotToken);
    assert(request.response.outputs.some(output => output.outputId === rawCapture.outputId));
    const afterRead = rawCapture.builderReads.find(read => read.readId === reload.after.readId);
    const domObservation = rawCapture.domObservations.find(observation =>
      observation.capturedAtEpochMs === reload.ui.capturedAtEpochMs);
    assert(afterRead && domObservation, 'Reload must bind its exact Builder and DOM observations.');
    assert(request.responseReceivedAt < afterRead.requestStartedAtEpochMs,
      'Reload reconcile response must arrive before the actual postreload Builder GET starts.');
    assert(request.responseReceivedAt < domObservation.capturedAtEpochMs,
      'Reload reconcile response must arrive before its DOM observation.');
    assert.equal(reload.ui.receiptId, request.response.receiptId);
    assert.notEqual(reload.ui.receiptId, precedingProposals[index].filter.receiptId,
      'Reload DOM must preserve its actual reconcile receipt, distinct from the prior proposal receipt.');
  }
});

test('adapter retains an unassigned late reconcile and its actual final DOM observation', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const captureId = 'schema-fixture-browser-request-late-reconcile';
  const finalReload = rawCapture.steps.at(-1);
  const finalDOM = {
    ...rawCapture.domObservations[finalReload.domObservationIndex],
    capturedAtMonotonicMs: 1480,
    capturedAtEpochMs: 1_700_000_001_480,
    receiptId: 'late-final-receipt',
  };
  rawCapture.domObservations.push(finalDOM);
  rawCapture.finalDOMObservationIndex = rawCapture.domObservations.length - 1;
  rawCapture.lateReconcileCaptureIds = [captureId];
  rawCapture.requestEvents.push({
    index: rawCapture.requestEvents.length,
    browserRequestId: captureId,
    requestId: 'schema-fixture-raw-request-late-reconcile',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/explorers/explorer-temporal/authoring/v2/reconcile',
    status: 200,
    request: { snapshotToken: 'snapshot-6', draftVersion: 6, draftDigest: 'draft-6', outputId: rawCapture.outputId },
    response: { snapshotToken: 'snapshot-6', receiptId: 'late-final-receipt', outputs: [{ outputId: rawCapture.outputId }] },
    startedAt: 1_700_000_001_470,
    responseReceivedAt: 1_700_000_001_471,
    completedAt: 1_700_000_001_473,
    responseMountEpoch: 5,
    failureMountEpoch: null,
    isMutation: false,
    failure: null,
    readCancellationDiagnostic: false,
    responseCapture: 'captured',
    responseCaptureComplete: true,
    responseBodyCaptured: true,
    atMonotonicMs: 1470,
    atEpochMs: 1_700_000_001_470,
    mountEpoch: 5,
  });

  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  assert.deepEqual(journeyTrace.temporalFilterLifecycle.lateReconcileCaptureIds, [captureId]);
  assert.equal(journeyTrace.temporalFilterLifecycle.finalDOMObservationIndex, rawCapture.domObservations.length - 1);
  assert.equal(journeyTrace.temporalFilterLifecycle.domObservations.at(-1).receiptId, 'late-final-receipt');
  assert(rawCapture.requestEvents.some(request => request.browserRequestId === captureId),
    'The final raw request ledger must preserve the late reconcile ID.');
});

test('adapter schema fixtures keep DATE_TIME request, response, and saved literals separate', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date-time');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposed = journeyTrace.actions.filter(event => event.action === 'FilterProposed(create)');
  assert.deepEqual(proposed[0].ui.rowIds, [], 'The empty DATE_TIME proposal must retain no data row IDs.');
  assert.equal(proposed[0].filter.proposalId, 'proposal-first-create');
  assert.equal(proposed[0].filter.receiptId, 'proposal-first-create');
  assert.deepEqual(proposed[0].filter.requestValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00.000Z' });
  assert.deepEqual(proposed[0].filter.responseValue,
    { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  const applied = journeyTrace.actions.find(event => event.action === 'FilterApplied(create)');
  assert.deepEqual(applied.filter.savedValue, { kind: 'DATE_TIME', dateTime: '2026-01-02T00:00:00Z' });
  assert.equal(journeyTrace.actions.some(event => event.filter?.valueKind === 'Instant'), false);
});

test('adapter rejects an observer write that no semantic action consumes', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  rawCapture.requestEvents.push({
    index: rawCapture.requestEvents.length,
    browserRequestId: 'schema-fixture-browser-request-unexpected-write',
    requestId: 'schema-fixture-raw-request-unexpected-write',
    method: 'POST',
    path: '/api/v1/projects/loom_dev_verify_temporal/unexpected-write',
    status: 200,
    request: {},
    response: {},
    startedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    responseReceivedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1001,
    completedAt: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1002,
    responseMountEpoch: 5,
    failureMountEpoch: null,
    isMutation: true,
    atMonotonicMs: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    atEpochMs: TEMPORAL_FILTER_FIXTURE_BASE_TIME + 1000,
    mountEpoch: 5,
  });
  assert.throws(() => adaptTemporalFilterJourney(rawCapture), /Every observer-classified write must be consumed exactly once/);
});

test('adapter preserves an absent raw request header as null in normalized evidence', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  delete rawCapture.requestEvents.find(event => event.browserRequestId === 'schema-fixture-browser-request-first-create').requestId;
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const proposal = journeyTrace.actions.find(event => event.action === 'FilterProposed(create)');
  assert.equal(proposal.request.requestId, null);
});

test('adapter schema fixture retains failed read-only request diagnostics', () => {
  const rawCapture = makeTemporalFilterJourneyCaptureFixture('date');
  const journeyTrace = adaptTemporalFilterJourney(rawCapture);
  const request = journeyTrace.temporalFilterLifecycle.requestEvents.find(event =>
    event.browserRequestId === 'schema-fixture-browser-request-edit-open-cancellation');
  assert.deepEqual(request.failure, { kind: 'transport', errorText: 'net::ERR_ABORTED', canceled: true });
  assert.equal(request.readCancellationDiagnostic, true);
  assert.equal(request.responseCapture, 'not-configured');
  assert.equal(request.responseCaptureComplete, true);
  assert.equal(request.responseBodyCaptured, false);
  assert.equal(request.responseReceivedAt, null, 'A canceled proposal has no observer response event.');
  const successful = rawCapture.requestEvents.find(event =>
    event.browserRequestId === 'schema-fixture-browser-request-first-create');
  assert(Number.isFinite(successful.responseReceivedAt), 'A successful proposal must retain its observer response event time.');
  assert(successful.completedAt > successful.responseReceivedAt,
    'Response-body completion must remain distinct from response receipt.');
});

test('adapter schema fixture evidence follows the ordered lifecycle timestamps and read indexes', () => {
  const capture = makeTemporalFilterJourneyCaptureFixture('date');
  const sorted = values => assert.deepEqual(values, [...values].sort((left, right) => left - right));
  sorted(capture.requestEvents.map(event => event.atMonotonicMs));
  sorted(capture.builderReads.map(read => read.capturedAtMonotonicMs));
  sorted(capture.domObservations.map(observation => observation.capturedAtMonotonicMs));
  sorted(capture.steps.map(step => step.atMonotonicMs));
  for (const step of capture.steps) {
    if (step.beforeReadIndex !== null) {
      assert(capture.builderReads[step.beforeReadIndex].capturedAtMonotonicMs <= step.atMonotonicMs,
        `${step.label} before read must precede its semantic event.`);
    }
    if (step.requestCaptureId && step.afterReadIndex !== null) {
      assert(capture.builderReads[step.afterReadIndex].capturedAtMonotonicMs >= step.atMonotonicMs,
        `${step.label} post-request read must follow its request event.`);
    }
    if (step.requestCaptureId && step.domObservationIndex !== null) {
      assert(capture.domObservations[step.domObservationIndex].capturedAtMonotonicMs > step.atMonotonicMs,
        `${step.label} DOM observation must follow its request event.`);
    }
  }
});
