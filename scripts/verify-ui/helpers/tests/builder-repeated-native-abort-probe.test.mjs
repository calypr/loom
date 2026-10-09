import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  assertRepeatedEmptyNativeRequestLedgerComplete,
  retainRepeatedEmptyNativeRequestLedger,
} from '../../workflows/builder-repeated.mjs';

const workflowSource = readFileSync(new URL('../../workflows/builder-repeated.mjs', import.meta.url), 'utf8');

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

test('CASE-010 keeps pending and failed native requests fatal', () => {
  assert.equal(assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: true, requests: [{ state: 'finished' }] },
  }), true);
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: false, requests: [{ state: 'pending' }] }, incompleteRequests: [{ state: 'pending' }],
  }), /complete terminal state/);
  assert.throws(() => assertRepeatedEmptyNativeRequestLedgerComplete({
    nativeRequestTerminalLedger: { complete: true, requests: [{ state: 'failed', requestId: 'failed-request' }] },
  }), /observed failed native requests/);
});
