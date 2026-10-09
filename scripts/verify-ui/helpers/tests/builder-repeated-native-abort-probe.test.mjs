import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  assertRepeatedEmptyNativeRequestLedgerComplete,
  retainRepeatedEmptyNativeRequestLedger,
} from '../../workflows/builder-repeated.mjs';
import { createReport, finishReport } from '../report.mjs';

const workflowSource = readFileSync(new URL('../../workflows/builder-repeated.mjs', import.meta.url), 'utf8');

const repeatedEmptyOwnerRetirementFixture = () => {
  const project = 'loom_dev_verify_mv138hht-da1741c';
  const explorer = 'verify-ht-da1741c-repeated-empty';
  const origin = 'http://127.0.0.1:30008';
  const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/schema-fields`;
  const requestId = 'schema-fields-328c1a11-3f1d-453c-8264-8078388f0d46';
  const browserRequestId = 'request-139';
  const action = { id: 'action-16', label: 'click Fields and related data', mainFrameNavigations: [] };
  const request = {
    requestId, browserRequestId, origin, path, method: 'POST', status: null,
    failure: 'net::ERR_ABORTED', terminalEvent: 'requestfailed', state: 'failed', complete: true,
    frameIdentityStatus: 'exact', frameIsMainFrame: true, pageId: 'page-1', frameId: 'frame-1',
    requestTimeline: { action, mainFrameNavigations: [] },
    nativeEventChronology: [
      { event: 'request', browserRequestId, observedAt: 1791557767995, objectMatch: true },
      { event: 'requestfailed', browserRequestId, observedAt: 1791557768736, objectMatch: true, failure: 'net::ERR_ABORTED' },
    ],
  };
  const nativeEntry = {
    requestDetails: { requestId }, origin, path, method: 'POST',
    terminalEvent: 'requestfailed', failure: 'net::ERR_ABORTED',
  };
  const report = createReport({
    scenario: 'builder-authoring', caseName: 'repeated-empty',
    target: { uiUrl: origin, project, explorer }, requiredChecks: ['fixture check'],
  });
  report.network.push({
    kind: 'network', method: 'POST', url: `${origin}${path}`, resourceType: 'fetch',
    errorText: 'net::ERR_ABORTED', playwrightRequestId: browserRequestId,
    requestDetails: { requestId }, requestTimeline: structuredClone(request.requestTimeline),
  });
  report.actions.push(
    { id: 'action-16', label: 'click Fields and related data', status: 'passed', startedAtEpochMs: 1791557767895, finishedAtEpochMs: 1791557767998 },
    { id: 'action-23', label: 'click Close operation editor', status: 'passed', startedAtEpochMs: 1791557768611, finishedAtEpochMs: 1791557768744 },
  );
  const ownerAtFetch = {
    status: 'unique', selector: '#feature-catalog-search', matchCount: 1,
    capturedAt: 1791557767983, anchorId: 'dom-node-17', connectedAtFetch: true,
    ruleOwner: 'feature-catalog-generated-fields', retirementAction: 'close-operation-editor',
  };
  const ownerAtAbort = {
    anchorId: 'dom-node-17', detachedAtAbort: true, connectedAtAbort: false,
    detachedObservedAt: 1791557768724, observedAtAbort: 1791557768731,
  };
  report.nativeAbortProbeEvents = [
    {
      kind: 'abort-controller-call', controllerId: 'abort-controller-36', createdAt: 1791557767983,
      abortedAt: 1791557768731, signalWasAlreadyAborted: false,
      requests: [{
        requestId, origin, path, method: 'POST', requestIdSource: 'request-header', startedAt: 1791557767983,
        fetchStateAtAbort: 'pending', ownerDomAtFetch: ownerAtFetch, ownerDomAtAbort: ownerAtAbort,
      }],
      trustedInteractions: [{
        type: 'click', at: 1791557768687, isTrusted: true,
        closestButton: { testId: 'construction-close-operation-editor', accessibleLabel: 'Close operation editor' },
      }],
    },
    {
      kind: 'abort-controller-fetch-settlement', controllerId: 'abort-controller-36', observedAt: 1791557768731,
      requests: [{ requestId, origin, path, method: 'POST', fetchStateAfterAbort: 'rejected', settledAt: 1791557768731 }],
    },
  ];
  const ledger = {
    nativeRequests: [nativeEntry], nativeRequestDrainEvidence: [], excludedNativeRequests: [],
    excludedNativeRequestDrainEvidence: [], nativeRequestCorrelationErrors: [], incompleteRequests: [],
    nativeRequestTerminalLedger: { scope: 'exact Explorer', complete: true, requests: [request] },
  };
  retainRepeatedEmptyNativeRequestLedger({ report, ledger, project, origin, explorer });
  return { report, ledger, nativeEntry, project, explorer, origin, path, requestId, browserRequestId };
};

test('CASE-010 installs the exact-project probe before the Builder document constructs its client', () => {
  assert.match(workflowSource, /import\s*\{[^}]*\binstallNativeAbortProbe\b[^}]*\}\s*from\s*['"]\.\.\/helpers\/native-abort-probe\.mjs['"]/);

  const probeInstall = workflowSource.indexOf('await installNativeAbortProbe({');
  const explorerBoundary = workflowSource.indexOf('const { explorer } = await createBlankExplorer(');
  const firstSourceSelection = workflowSource.indexOf("action: () => click(workflow, 'button', { name: 'Choose Observation rows' })");
  assert(probeInstall >= 0 && explorerBoundary > probeInstall && firstSourceSelection > explorerBoundary,
    'the init script must install before page.goto creates the app client, before the fresh-Explorer workflow and source selection');

  const installation = workflowSource.slice(probeInstall, workflowSource.indexOf('});', probeInstall) + 3);
  assert.match(installation, /page,/);
  assert.match(installation, /report,/);
  assert.match(installation, /project:\s*context\.target\.fixtureProject/);
  assert.match(installation, /explorer:\s*\{\s*mode:\s*'project-routes'\s*\}/);
  assert.match(installation, /apiOrigin:\s*new URL\(context\.target\.uiUrl\)\.origin/);

  const scopeOpen = workflowSource.indexOf('nativeRequestLedger.openScope({');
  const lifecycleRun = workflowSource.indexOf('await runRepeatedEmptyWorkflow(workflow, context);');
  const ledgerFlush = workflowSource.indexOf('await nativeRequestLedger.flush(scope, {');
  assert(scopeOpen >= 0 && scopeOpen < lifecycleRun && lifecycleRun < ledgerFlush,
    'the project/origin ledger scope must open before the page lifecycle and flush after it');
  const scopeCall = workflowSource.slice(scopeOpen, workflowSource.indexOf('});', scopeOpen) + 3);
  assert.match(scopeCall, /project,\s*origin/);
  assert.match(workflowSource.slice(ledgerFlush, workflowSource.indexOf('});', ledgerFlush) + 3),
    /explorer:\s*report\.target\?\.explorer[\s\S]*timeoutMs:\s*5000/);
  assert.match(workflowSource, /if \(!ledger\?\.nativeRequestTerminalLedger\?\.complete\)/);
  assert.match(workflowSource, /entry\.state === 'failed'/);
});

test('CASE-008/009 also install the project-scoped probe before their Builder navigation', () => {
  const callers = [
    {
      path: '../../workflows/builder-authoring.mjs',
      firstNavigation: 'await page.goto(browserURL(',
    },
    {
      path: '../../workflows/builder-cohort-expand.mjs',
      firstNavigation: "const { explorer } = await createBlankExplorer(page, workflow, context.target, context.runID, 'cohort-expand'",
    },
  ];
  for (const { path, firstNavigation } of callers) {
    const source = readFileSync(new URL(path, import.meta.url), 'utf8');
    const install = source.indexOf('await installNativeAbortProbe({');
    const navigation = source.indexOf(firstNavigation);
    assert(install >= 0 && navigation > install, `${path} must install before its first Builder navigation`);
    const options = source.slice(install, source.indexOf('});', install) + 3);
    assert.match(options, /project:\s*context\.target\.fixtureProject/);
    assert.match(options, /explorer:\s*\{\s*mode:\s*'project-routes'\s*\}/);
    assert.match(options, /apiOrigin:\s*new URL\(context\.target\.uiUrl\)\.origin/);
  }
});

test('CASE-010 persists exact schema-fields signal correlations separately without classifying cancellation', () => {
  const project = 'loom_dev_verify_repeat';
  const explorer = 'fresh-repeated-empty';
  const origin = 'http://127.0.0.1:30008';
  const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/schema-fields`;
  const requestId = 'schema-fields-11111111-2222-4333-8444-555555555555';
  const exactEntry = {
    requestDetails: { requestId }, origin, path, method: 'POST',
    terminalEvent: 'requestfailed', failure: 'net::ERR_ABORTED',
  };
  const ledger = {
    nativeRequests: [
      exactEntry,
      { requestDetails: { requestId }, origin: 'http://127.0.0.1:30009', path, method: 'POST' },
      { requestDetails: { requestId }, origin, path: `${path}/other`, method: 'POST' },
      { requestDetails: { requestId }, origin, path, method: 'GET' },
      { requestDetails: { requestId: 'other-request' }, origin, path: `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/semantic-inventory`, method: 'POST' },
    ],
    nativeRequestDrainEvidence: [],
    excludedNativeRequests: [],
    excludedNativeRequestDrainEvidence: [],
    nativeRequestCorrelationErrors: [],
    nativeRequestTerminalLedger: { scope: 'exact Explorer', complete: true, requests: [] },
    incompleteRequests: [],
  };
  const probeEvents = [{
    kind: 'abort-controller-call', controllerId: 'controller-1', createdAt: 10, abortedAt: 30,
    signalWasAlreadyAborted: false,
    requests: [{ requestId, origin, path, method: 'POST', requestIdSource: 'request-header',
      ownerDomAtFetch: { status: 'unique', ruleOwner: 'feature-catalog-generated-fields' },
      ownerDomAtAbort: { status: 'unique', connectedAtAbort: true },
    }],
  }];
  const report = { nativeAbortProbeEvents: probeEvents };

  retainRepeatedEmptyNativeRequestLedger({ report, ledger, project, origin, explorer });

  assert.equal(report.nativeRequests, ledger.nativeRequests);
  assert.equal(report.nativeRequestDrainEvidence, ledger.nativeRequestDrainEvidence);
  assert.equal(report.excludedNativeRequests, ledger.excludedNativeRequests);
  assert.equal(report.excludedNativeRequestDrainEvidence, ledger.excludedNativeRequestDrainEvidence);
  assert.equal(report.nativeRequestCorrelationErrors, ledger.nativeRequestCorrelationErrors);
  assert.equal(report.nativeRequestTerminalLedger, ledger.nativeRequestTerminalLedger);
  assert.equal(report.nativeRequestIncompleteRequests, ledger.incompleteRequests);
  assert.equal(report.nativeAbortProbeEvents, probeEvents);
  assert.deepEqual(report.nativeAbortProbeCorrelations.map(({ nativeRequestIndex, requestId: id, requestIdentityMatchCount, observation }) => ({
    nativeRequestIndex, requestId: id, requestIdentityMatchCount,
    exactRequestSignalCorrelation: observation.exactRequestSignalCorrelation,
    classificationEffect: observation.classificationEffect,
  })), [{
    nativeRequestIndex: 0,
    requestId,
    requestIdentityMatchCount: 1,
    exactRequestSignalCorrelation: true,
    classificationEffect: 'diagnostic only; does not make an unfinished native request terminal or expected',
  }]);
  assert.equal(Object.hasOwn(exactEntry, 'expected'), false);
  assert.equal(Object.hasOwn(exactEntry, 'canceled'), false);
  assert.equal(Object.hasOwn(exactEntry, 'abortControllerSignalObservation'), false);

  const duplicateReport = { nativeAbortProbeEvents: probeEvents };
  retainRepeatedEmptyNativeRequestLedger({
    report: duplicateReport,
    ledger: { ...ledger, nativeRequests: [...ledger.nativeRequests, { ...exactEntry }] },
    project,
    origin,
    explorer,
  });
  assert.deepEqual(duplicateReport.nativeAbortProbeCorrelations.map(({ requestIdentityMatchCount, observation }) => ({
    requestIdentityMatchCount, exactRequestSignalCorrelation: observation.exactRequestSignalCorrelation,
  })), [
    { requestIdentityMatchCount: 2, exactRequestSignalCorrelation: false },
    { requestIdentityMatchCount: 2, exactRequestSignalCorrelation: false },
  ]);
});

test('CASE-010 accepts only the exact production-classified schema-fields Close retirement', () => {
  const positive = repeatedEmptyOwnerRetirementFixture();
  const originalNetwork = structuredClone(positive.report.network);
  const classifiedReport = structuredClone(positive.report);
  finishReport(classifiedReport);
  assert.deepEqual(classifiedReport.expectedOwnerRetirements?.map(({ requestId, browserRequestId, project, explorer, closeActionId }) => ({
    requestId, browserRequestId, project, explorer, closeActionId,
  })), [{
    requestId: positive.requestId,
    browserRequestId: positive.browserRequestId,
    project: positive.project,
    explorer: positive.explorer,
    closeActionId: 'action-23',
  }], 'the existing report finalizer recognizes this exact retained Close retirement');
  assert.equal(assertRepeatedEmptyNativeRequestLedgerComplete(positive.ledger, positive.report), true);
  assert.deepEqual(positive.report.network, originalNetwork, 'the live report keeps the raw ERR_ABORTED network row unchanged');
  assert.equal(Object.hasOwn(positive.report, 'expectedOwnerRetirements'), false,
    'classification is derived without finalizing or mutating the live report');

  assert.equal(assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: true, requests: [{ state: 'finished' }] },
  }), true);
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: false, requests: [{ state: 'pending' }] }, incompleteRequests: [{ state: 'pending' }],
  }), /complete terminal state/);

  const noProof = repeatedEmptyOwnerRetirementFixture();
  noProof.report.nativeAbortProbeEvents = [];
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete(noProof.ledger, noProof.report), /failed native requests/,
    'a failed schema-fields row remains fatal without complete production owner evidence');

  const wrongRoute = repeatedEmptyOwnerRetirementFixture();
  wrongRoute.ledger.nativeRequestTerminalLedger.requests[0].path = `${wrongRoute.path}/commands`;
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete(wrongRoute.ledger, wrongRoute.report), /failed native requests/,
    'the retirement cannot be borrowed by a different failed endpoint');

  const wrongBrowserRequest = repeatedEmptyOwnerRetirementFixture();
  wrongBrowserRequest.ledger.nativeRequestTerminalLedger.requests[0].browserRequestId = 'request-140';
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete(wrongBrowserRequest.ledger, wrongBrowserRequest.report), /failed native requests/,
    'a different browser Request object cannot borrow the report classification');

  const duplicateIdentity = repeatedEmptyOwnerRetirementFixture();
  duplicateIdentity.ledger.nativeRequestTerminalLedger.requests.push(
    structuredClone(duplicateIdentity.ledger.nativeRequestTerminalLedger.requests[0]),
  );
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete(duplicateIdentity.ledger, duplicateIdentity.report), /failed native requests/,
    'the same report proof cannot be reused for an ambiguous native request identity');

  const unrelatedFailure = repeatedEmptyOwnerRetirementFixture();
  unrelatedFailure.ledger.nativeRequestTerminalLedger.requests.push({
    ...structuredClone(unrelatedFailure.ledger.nativeRequestTerminalLedger.requests[0]),
    requestId: 'builder-command-140', browserRequestId: 'request-140', path: '/api/v1/projects/other/commands',
  });
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete(unrelatedFailure.ledger, unrelatedFailure.report), /failed native requests/,
    'a separately failed request remains fatal even when the exact schema-fields retirement is present');

  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: true, requests: [{ state: 'failed', requestId: 'failed-request' }] },
  }), /failed native requests/);
});

test('CASE-010 waits for the exact restored-draft capabilities response inside the Apply-to-render budget before reload', () => {
  const apply = workflowSource.indexOf("name: 'apply source-row removal and restore all three fixture Observation IDs'");
  const reload = workflowSource.indexOf('await reload(page, mainReadyExpression(3));', apply);
  const applyBlock = workflowSource.slice(apply, reload);
  const watcher = workflowSource.lastIndexOf('const capabilitiesReadiness = watchConstructionCapabilitiesReadiness({', apply);
  const watcherEnd = workflowSource.indexOf('});', watcher) + 3;
  const watcherBlock = workflowSource.slice(watcher, watcherEnd);
  assert(apply >= 0 && reload > apply);
  assert(watcher >= 0 && watcher < apply && watcherEnd > watcher);
  assert.match(workflowSource, /import\s*\{[^}]*readBuilderCapabilitiesIdentity[^}]*watchConstructionCapabilitiesReadiness[^}]*\}\s*from\s*['"]\.\.\/helpers\/construction-capabilities-readiness\.mjs['"]/);
  assert.match(watcherBlock, /page,[\s\S]*?project:\s*context\.target\.fixtureProject[\s\S]*?explorer,[\s\S]*?apiOrigin:\s*context\.target\.uiUrl/);
  assert.match(applyBlock, /capabilitiesReadiness\.markActionStarted\(\)[\s\S]*?locator\.click\(\)/);
  assert.match(applyBlock, /after:\s*mainReadyExpression\(3\)/);
  assert.match(applyBlock, /settle:\s*async\s*\(\{\s*remainingMs\s*\}\)[\s\S]*?requirePairs\([\s\S]*?readBuilderCapabilitiesIdentity\([\s\S]*?timeoutMs:\s*remainingMs\(\)[\s\S]*?capabilitiesReadiness\.waitFor\([\s\S]*?timeoutMs:\s*remainingMs\(\)/);
  assert.match(applyBlock, /capabilitiesReadiness\.dispose\(\)/);
  assert.match(applyBlock, /budget:\s*5000/);
});
