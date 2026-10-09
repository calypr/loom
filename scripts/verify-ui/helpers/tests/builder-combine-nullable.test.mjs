import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { caseNamesFor, hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { assertNullableNativeRequestLedgerComplete, buildNullableNativeRequestLedger, builderCancelStateEvidence, nativeResponseScopeEvidence, removalProposalEvidence, retainNullableNativeCaptureErrors, targetDocumentStateEvidence } from '../builder-combine-nullable-helpers.mjs';
import { gateFailure } from '../cda-fixtures.mjs';
import { captureCDARequests, findCompletedNativeResponse } from '../cda-playwright-requests.mjs';
import { classifyNativeBrowserApiRequest } from '../native-browser-api-scope.mjs';

const driver = readFileSync(new URL('../../workflows/builder-combine-nullable.mjs', import.meta.url), 'utf8');

test('nullable lifecycle request collector keeps finished, failed, replacement, and request-only terminal states distinct', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const origin = 'http://127.0.0.1:30008';
  const explorerRoot = '/api/v1/projects/nullable-fixture/explorers/owned';
  const capture = captureCDARequests(page, { apiOrigin: origin, ownedPathPrefix: '/api/v1/projects/nullable-fixture/explorers', report });
  const makeRequest = (id, path, body) => ({
    url: () => origin + explorerRoot + path,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': id }),
    postData: () => JSON.stringify(body),
    failure: () => null,
  });
  const emitResponse = (request, status, body) => page.emit('response', {
    request: () => request,
    status: () => status,
    headers: () => ({ 'x-request-id': `server-${request.headers()['x-request-id']}` }),
    text: async () => JSON.stringify(body),
  });

  const finishedRequest = makeRequest('builder-finished', '/authoring/v2/builder', { outputId: 'out-nullable' });
  page.emit('request', finishedRequest);
  emitResponse(finishedRequest, 200, { draftVersion: 1 });
  page.emit('requestfinished', finishedRequest);

  const abortedRequest = makeRequest('capability-aborted', '/authoring/v2/construction-capabilities', {
    stageId: 'source_projection', expectedDraftVersion: 6, expectedDraftDigest: 'draft-6', outputId: 'out-nullable',
  });
  page.emit('request', abortedRequest);
  abortedRequest.failure = () => ({ errorText: 'net::ERR_ABORTED' });
  page.emit('requestfailed', abortedRequest);

  const replacementRequest = makeRequest('capability-replacement', '/authoring/v2/construction-capabilities', {
    stageId: 'source_projection', expectedDraftVersion: 7, expectedDraftDigest: 'draft-7', outputId: 'out-nullable',
  });
  page.emit('request', replacementRequest);
  emitResponse(replacementRequest, 200, { draftVersion: 7, outputId: 'out-nullable' });
  page.emit('requestfinished', replacementRequest);

  const pendingRequest = makeRequest('builder-pending', '/authoring/v2/builder/pending', { outputId: 'out-nullable' });
  page.emit('request', pendingRequest);
  await capture.flush({ timeoutMs: 20, waitForNativeRequestTerminals: true });

  const [finished, aborted, replacement, pending] = report.nativeRequests;
  const terminalEvent = (entry) => entry.nativeEventChronology.find(({ event }) =>
    event === 'requestfinished' || event === 'requestfailed')?.event ?? null;
  assert.equal(terminalEvent(finished), 'requestfinished');
  assert.equal(finished.status, 200);
  assert.equal(terminalEvent(aborted), 'requestfailed');
  assert.equal(aborted.failure, 'net::ERR_ABORTED');
  assert.equal(aborted.status, undefined, 'an abort without a response is a terminal failure, not a completed response');
  assert.equal(terminalEvent(replacement), 'requestfinished');
  assert.equal(replacement.status, 200);
  assert.equal(findCompletedNativeResponse(report.nativeRequests, capture.rawResponseBody,
    (entry, response) => entry.requestId === 'capability-replacement' && response?.draftVersion === 7), replacement,
  'the later exact replacement remains the completed response candidate');
  assert.equal(terminalEvent(pending), null);
  assert.equal(pending.completedAt, undefined);
  assert.deepEqual(report.nativeRequestDrainEvidence.map((entry) => entry.unresolvedRequests.map((request) => request.path)), [
    [`${explorerRoot}/authoring/v2/builder/pending`],
  ]);
});

test('nullable native request ledger includes only the fresh Explorer and requires complete creation and route evidence', () => {
  const project = 'nullable-fixture';
  const explorer = 'owned';
  const projectPath = `/api/v1/projects/${project}/explorers`;
  const explorerPath = `${projectPath}/${explorer}`;
  const finished = (requestId, method, path, status) => ({
    requestId,
    browserRequestId: `playwright-${requestId}`,
    method,
    path,
    status,
    nativeEventChronology: [
      { event: 'request' },
      { event: 'response' },
      { event: 'requestfinished' },
    ],
  });
  const createRequest = finished('create-owned', 'POST', projectPath, 201);
  const explorerRequest = finished('builder-owned', 'GET', `${explorerPath}/authoring/v2/builder`, 200);
  const failedExplorerRequest = {
    requestId: 'capability-aborted-owned',
    browserRequestId: 'playwright-capability-aborted-owned',
    method: 'POST',
    path: `${explorerPath}/authoring/v2/construction-capabilities`,
    failure: 'net::ERR_ABORTED',
    nativeEventChronology: [{ event: 'request' }, { event: 'requestfailed' }],
  };
  const otherExplorerRequest = {
    requestId: 'builder-other',
    browserRequestId: 'playwright-builder-other',
    method: 'GET',
    path: `${projectPath}/other/authoring/v2/builder`,
    nativeEventChronology: [{ event: 'request' }],
  };
  const requests = [createRequest, explorerRequest, failedExplorerRequest, otherExplorerRequest];
  const drainEvidence = [{
    status: 'timed-out',
    unresolvedRequests: [{ index: 3, requestId: otherExplorerRequest.requestId, path: otherExplorerRequest.path }],
  }];

  const ledger = buildNullableNativeRequestLedger({ project, explorer, nativeRequests: requests, nativeRequestDrainEvidence: drainEvidence });
  assert.equal(ledger.nativeRequestTerminalLedger.complete, true);
  assert.deepEqual(ledger.nativeRequests.map(({ requestId }) => requestId), ['create-owned', 'builder-owned', 'capability-aborted-owned']);
  assert.equal(ledger.nativeRequestTerminalLedger.counts.failed, 1);
  assert.deepEqual(ledger.nativeRequestDrainEvidence, []);
  assert.deepEqual(ledger.excludedNativeRequests.map(({ requestId }) => [requestId]), [['builder-other']]);
  assert.deepEqual(ledger.excludedNativeRequestDrainEvidence[0].unresolvedRequests.map(({ requestId }) => requestId), ['builder-other'],
    'a prefix-wide flush may observe another Explorer, but the selected-Explorer ledger retains that excluded drain evidence explicitly');
  assert.equal(Object.isFrozen(ledger.nativeRequestTerminalLedger.requests[0].nativeEventChronology), true);
  assert.doesNotThrow(() => assertNullableNativeRequestLedgerComplete(ledger));

  const emptyLedger = buildNullableNativeRequestLedger({ project, explorer, nativeRequests: [] });
  assert.equal(emptyLedger.nativeRequestTerminalLedger.complete, false, 'zero owned requests cannot establish a complete lifecycle');
  assert.throws(() => assertNullableNativeRequestLedgerComplete(emptyLedger), /complete owned terminal ledger/);

  const missingExplorerLedger = buildNullableNativeRequestLedger({ project, nativeRequests: [createRequest] });
  assert.equal(missingExplorerLedger.nativeRequestTerminalLedger.complete, false, 'a project create request without the selected Explorer cannot complete');
  assert.throws(() => assertNullableNativeRequestLedgerComplete(missingExplorerLedger), /complete owned terminal ledger/);

  const pendingRequest = {
    requestId: 'builder-pending',
    browserRequestId: 'playwright-builder-pending',
    method: 'GET',
    path: `${explorerPath}/authoring/v2/builder/pending`,
    nativeEventChronology: [{ event: 'request' }],
  };
  const pendingLedger = buildNullableNativeRequestLedger({
    project,
    explorer,
    nativeRequests: [createRequest, pendingRequest],
    nativeRequestDrainEvidence: [{ status: 'timed-out', unresolvedRequests: [{ index: 1, requestId: pendingRequest.requestId, path: pendingRequest.path }] }],
  });
  assert.equal(pendingLedger.nativeRequestTerminalLedger.complete, false);
  assert.deepEqual(pendingLedger.nativeRequestDrainEvidence[0].unresolvedRequests.map(({ requestId }) => requestId), ['builder-pending']);
  assert.equal(pendingLedger.nativeRequestTerminalLedger.counts.pending, 1);
  assert.throws(() => assertNullableNativeRequestLedgerComplete(pendingLedger), /complete owned terminal ledger/);
});

test('nullable request capture preserves diagnostics and reuses only exact existing fixture classifications', () => {
  const terminalRequest = (status = 200) => ({
    requestId: 'terminal-request', status, completedAt: 10,
    nativeEventChronology: [{ event: 'request' }, { event: 'response' }, { event: 'requestfinished' }],
  });
  const makeReport = (network = [], nativeRequests = [terminalRequest()]) => ({
    errors: [],
    network,
    nativeRequests,
    nativeRequestDrainEvidence: [],
    assertions: [],
    requiredChecks: [],
    missingRequiredChecks: [],
  });
  const http500 = {
    kind: 'http', origin: 'http://127.0.0.1:30008', url: 'http://127.0.0.1:30008/api/v1/projects/nullable-fixture/explorers/owned/authoring/v2/builder',
    path: '/api/v1/projects/nullable-fixture/explorers/owned/authoring/v2/builder', status: 500,
    requestId: 'request-http-500', browserRequestId: 'playwright-2', method: 'POST', request: { outputId: 'out-1' },
  };
  const httpReport = makeReport([{
    kind: 'network', status: 500, method: 'POST', url: http500.url, requestId: 'request-http-500',
    playwrightRequestId: 'cda-request-1', requestDetails: { requestId: 'request-http-500' },
  }], [terminalRequest(500)]);
  const httpResult = retainNullableNativeCaptureErrors(httpReport, [http500]);
  assert.deepEqual(httpResult, { observed: 1, representedByFixtureDiagnostics: 1, unrepresented: 0 });
  assert.deepEqual(httpReport.nullableNativeRequestCaptureDiagnostics[0].diagnostic, http500,
    'the supplemental collector raw HTTP diagnostic remains in the final report');
  assert.match(gateFailure(httpReport)?.message ?? '', /unexpectedNetwork/,
    'an HTTP 500 remains fatal through the fixture’s existing network gate');

  const correlation = {
    kind: 'request-capture-correlation', event: 'response', browserRequestId: null,
    method: 'POST', path: http500.path, objectMatch: false,
    message: 'Playwright response request object did not match an exact captured request object',
  };
  const correlationReport = makeReport();
  const correlationResult = retainNullableNativeCaptureErrors(correlationReport, [correlation]);
  assert.deepEqual(correlationResult, { observed: 1, representedByFixtureDiagnostics: 0, unrepresented: 1 });
  assert.deepEqual(correlationReport.errors, [{ ...correlation, expected: false }]);
  assert.match(gateFailure(correlationReport)?.message ?? '', /unexpectedErrors/,
    'an uncorrelated capture error stays fatal even if no request remains pending');

  const cancellation = {
    kind: 'network', origin: 'http://127.0.0.1:30008', url: 'http://127.0.0.1:30008/api/v1/projects/nullable-fixture/explorers/owned/authoring/v2/construction-capabilities',
    path: '/api/v1/projects/nullable-fixture/explorers/owned/authoring/v2/construction-capabilities',
    requestId: 'server-capability-7', browserRequestId: 'playwright-1', method: 'POST', error: 'net::ERR_ABORTED',
  };
  const proof = { requestId: 'server-capability-7', playwrightRequestId: 'cda-request-1', reason: 'newer draft superseded the request', proof: { draftVersion: 7 } };
  const cancellationReport = makeReport([{
    kind: 'network', method: 'POST', url: cancellation.url, requestId: 'server-capability-7',
    playwrightRequestId: 'cda-request-1', errorText: 'net::ERR_ABORTED', canceled: true,
    expected: true, expectedCancellation: proof,
  }]);
  const cancellationResult = retainNullableNativeCaptureErrors(cancellationReport, [cancellation]);
  assert.deepEqual(cancellationResult, { observed: 1, representedByFixtureDiagnostics: 1, unrepresented: 0 });
  assert.equal(cancellationReport.nullableNativeRequestCaptureDiagnostics[0].fixtureDiagnosticMatch.policy, 'expected-cancellation',
    'only an exact unique fixture diagnostic can carry forward its existing cancellation proof');
  assert.equal(gateFailure(cancellationReport), undefined,
    'the supplemental collector does not classify the exact cancellation a second time or turn it into a duplicate fatal error');

  const unmatchedCancellationCases = [
    {
      label: 'missing stable request identity',
      captureRequestId: 'playwright-7',
      fixtureRequestId: null,
      fixtureURL: cancellation.url,
    },
    {
      label: 'different origin',
      captureRequestId: 'server-capability-7',
      fixtureRequestId: 'server-capability-7',
      fixtureURL: cancellation.url.replace('30008', '8188'),
    },
    {
      label: 'different stable request IDs',
      captureRequestId: 'server-capability-8',
      fixtureRequestId: 'server-capability-7',
      fixtureURL: cancellation.url,
    },
  ];
  for (const scenario of unmatchedCancellationCases) {
    const unmatchedReport = makeReport([{
      kind: 'network', method: 'POST', url: scenario.fixtureURL, requestId: scenario.fixtureRequestId,
      playwrightRequestId: 'cda-request-1', errorText: 'net::ERR_ABORTED', canceled: true,
      expected: true, expectedCancellation: proof,
    }]);
    const unmatchedDiagnostic = { ...cancellation, requestId: scenario.captureRequestId };
    const unmatchedResult = retainNullableNativeCaptureErrors(unmatchedReport, [unmatchedDiagnostic]);
    assert.deepEqual(unmatchedResult, { observed: 1, representedByFixtureDiagnostics: 0, unrepresented: 1 }, scenario.label);
    assert.equal(unmatchedReport.errors[0].expected, false, `${scenario.label} must not inherit another request's expected-cancellation marker`);
    assert.match(gateFailure(unmatchedReport)?.message ?? '', /unexpectedErrors/, `${scenario.label} stays fatal`);
  }
});

test('nullable KEY_JOIN is registered as a separate owned native lifecycle', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  assert.ok(scenario);
  assert.equal(scenario.script, 'builder-combine-nullable.mjs');
  assert.deepEqual(caseNamesFor(scenario), ['lifecycle']);
  const required = scenarioCaseFor(scenario, 'lifecycle').requiredChecks;
  for (const name of [
    'both published subject.reference fields are nullable scalar strings',
    'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value',
    'native CREATE_TABLE request and response bind to the owned UI proxy project and Explorer route',
    'INNER preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'nullable-key fixture proves two duplicate rows per side for one shared key, NULL on both sides, and one left-only key',
    'INNER nullable Join preserves all four pairs from the 2x2 duplicate key and never matches NULL to NULL',
    'INNER applied rows preserve all four duplicate-key pairs',
    'INNER duplicate-key pairs survive Builder reload',
    'LEFT preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'LEFT preview preserves four duplicate-key pairs, both unmatched left rows, and NULL non-equality',
    'LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows',
    'LEFT duplicate-key multiplicity and null projections survive Builder reload',
    'nullable KEY_JOIN removal preview receipt binds the exact scoped request, removed step, target, snapshot, and DOM receipt',
    'Cancel leaves saved INNER nullable Join rows unchanged after reload',
    'Cancel leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'Cancel removal leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document',
    'LEFT duplicate-key multiplicity and null projections survive Builder reload',
    'both published nullable-key source tables remain byte-structured unchanged',
  ]) assert.ok(required.includes(name), 'registry must require ' + name);
});

test('nullable lifecycle is discovered by the official Playwright Test runner', () => {
  const spec = readFileSync(new URL('../../specs/nullable-combine.spec.mjs', import.meta.url), 'utf8');
  assert.match(driver, /export const nullableJoinWorkflow = async \(\{ page, report, action \}, context\) =>/);
  assert.match(driver, /import \{ expect, test \} from '@playwright\/test'/);
  assert.doesNotMatch(driver, /runPlaywrightCase|executeScenario|runNullableJoin/);
  assert.doesNotMatch(driver, /activeAction|page\.locator\('body'\)|await Promise\.all\(entries\.map\(entry => entry\.responsePromise\)/);
  assert.match(driver, /test\.step\(name, async \(\) =>/);
  assert.match(driver, /\}, \{ timeout: STEP_TIMEOUT_MS \}\)/);
  assert.equal((driver.match(/await expect\.poll\(/g) ?? []).length, 3);
  assert.match(spec, /import \{ test \} from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(spec, /test\.use\(\{ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata\/verify-combine-nullable-duplicates' \}\)/);
  assert.match(spec, /test\('nullable KEY_JOIN duplicate-key multiplicity and NULL non-equality lifecycle'/);
  assert.match(spec, /nullableJoinWorkflow\(\{ page, report: workflow\.report, action: workflow\.action \}, loomContext\)/);
});

test('nullable native case authors the exact optional source paths and checks the bound proposal receipt', () => {
  assert.match(driver, /Observation:\s*\['status',\s*'valueInteger',\s*'subject\.reference'\]/);
  assert.match(driver, /DiagnosticReport:\s*\['status',\s*'subject\.reference'\]/);
  assert.match(driver, /nullable-key fixture proves two duplicate rows per side for one shared key, NULL on both sides, and one left-only key/);
  assert.match(driver, /duplicatedObservationIDs\.length !== 2 \|\| duplicatedReportIDs\.length !== 2/);
  assert.match(driver, /duplicateKeyInnerMultiplicity: `\$\{duplicatedObservationIDs\.length\}x\$\{duplicatedReportIDs\.length\}`/);
  assert.match(driver, /column\.clickhouseType === 'Nullable\(String\)' && column\.nullable === true && column\.repeated === false/);
  assert.match(driver, /constructionProposalPreviewEvidence\(/);
  assert.match(driver, /leftColumnId === expectedKeyIDs\[0\].*rightColumnId === expectedKeyIDs\[1\]/s);
  assert.match(driver, /nullMatchesNull: false/);
});

test('nullable Join reload budgets include exact rows and rooted target restoration', () => {
  const start = driver.indexOf('const reloadTarget =');
  const end = driver.indexOf('\nconst openSavedEdit', start);
  const reloadHelper = driver.slice(start, end);
  assert.match(reloadHelper, /expectedRows, name, exactRowsName, verifySavedState/);
  assert.match(reloadHelper, /after: async \(\) => \{\s*await waitFor\(page, savedPreview\(expectedRows\.length\), 5000\);\s*exactRows\(report, exactRowsName, await readGrid\(page\), \['Observation ID', 'Report ID'\], expectedRows\);\s*if \(verifySavedState\) await verifySavedState\(\);\s*\}/);
  for (const [rows, timingName, exactRowsName, verifySavedState] of [
    ['innerRows', 'reload INNER nullable-key table', 'INNER duplicate-key pairs survive Builder reload', false],
    ['innerRows', 'reload saved INNER after LEFT Cancel', 'Cancel leaves saved INNER nullable Join rows unchanged after reload', true],
    ['leftRows', 'reload applied LEFT nullable-key table', 'LEFT duplicate-key multiplicity and null projections survive Builder reload', false],
    ['leftRows', 'reload saved LEFT after removal Cancel', 'Cancel removal preserves the exact LEFT nullable Join rows after reload', true],
  ]) {
    const callStart = driver.indexOf(`await reloadTarget(report, page, target.outputId, ${rows}, '${timingName}', '${exactRowsName}'`);
    assert.notEqual(callStart, -1, `reload timing must include exact ${rows} proof: ${timingName}`);
    const callEnd = verifySavedState ? driver.indexOf('\n  });', callStart) : driver.indexOf('\n', callStart);
    const call = driver.slice(callStart, callEnd);
    if (verifySavedState) {
      assert.match(call, /, async \(\) => \{/);
      assert.match(call, /readBuilder\(context, explorer\)/);
      assert.match(call, /builderCancelStateEvidence\(/);
      assert.match(call, /assertSavedStep\(/);
    } else {
      assert.match(call, /;$/);
    }
  }
  const removalReloadStart = driver.indexOf("name: 'reload nullable KEY_JOIN removal result'");
  const removalReloadEnd = driver.indexOf('\n  const finalBuilder', removalReloadStart);
  const removalReload = driver.slice(removalReloadStart, removalReloadEnd);
  assert.match(removalReload, /after: async \(\) => \{\s*await waitFor\(page, emptyTargetReady\(target\.outputId\), 5000\);\s*const afterRemoval = await readBuilder\(context, explorer\);[\s\S]*?check\(report, 'persistence', 'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document', restoredEvidence\.ok/);
});

test('nullable and duplicate-key coverage map to exact native lifecycle checks without claiming a browser pass', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  const duplicate = registry.find((entry) => entry.id === 'builder-combine')
    .coverage.find((entry) => entry.feature === 'duplicate-key multiplicity on nullable Join keys');
  const nullable = scenario.coverage.find((entry) => entry.feature === 'nullable scalar KEY_JOIN ordinary SQL NULL equality and LEFT preservation');
  assert.equal(hasLifecycleContract(nullable, scenario), true);
  assert.equal(hasLifecycleContract(duplicate, registry.find((entry) => entry.id === 'builder-combine')), true);
  assert.equal(duplicate.acceptance.scenario, scenario.id);
  assert.equal(nullable.acceptance.case, 'lifecycle');
  const checks = scenarioCaseFor(scenario, 'lifecycle').requiredChecks;
  for (const [coverage, expected] of [
    [nullable, {
      choice: /choose nullable KEY_JOIN/,
      proposal: /INNER nullable Join preserves all four pairs.*never matches NULL to NULL/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
    [duplicate, {
      choice: /choose nullable KEY_JOIN/,
      proposal: /INNER nullable Join preserves all four pairs.*never matches NULL to NULL/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
  ]) {
    for (const [phase, pattern] of Object.entries(expected)) assert.match(checks[coverage.acceptance.checks[phase]], pattern);
  }
});

test('nullable Playwright request listener classifies the owned proxy scope', () => {
  const scope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project: 'loom_dev_verify_123',
    explorer: 'verify-123-combine',
    protectedExplorer: 'verify-123-bootstrap',
  };
  const route = `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/commands`;
  assert.match(driver, /import \{ classifyNativeBrowserApiRequest \} from '\.\.\/helpers\/native-browser-api-scope\.mjs'/);
  const request = { url: () => route };
  assert.equal(classifyNativeBrowserApiRequest(request.url(), scope).kind, 'capture');
  assert.match(driver, /classifyNativeBrowserApiRequest\(request\.url\(\), scope\)/);
  assert.match(driver, /findRemoval\(stepID, afterIndex = 0\)/);
  assert.match(driver, /removeStepIds\.includes\(stepID\)/);
});

test('native request/response evidence rejects direct, foreign, protected, and mismatched routes', () => {
  const scope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project: 'loom_dev_verify_123',
    explorer: 'verify-123-combine',
    protectedExplorer: 'verify-123-bootstrap',
  };
  const path = `/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/construction-proposals`;
  const requestURL = scope.uiOrigin + path + '?outputId=target-1';
  assert.equal(nativeResponseScopeEvidence(requestURL, requestURL, scope).ok, true);
  assert.equal(nativeResponseScopeEvidence(scope.apiOrigin + path, scope.apiOrigin + path, scope).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/other-project/explorers/${scope.explorer}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/other-project/explorers/${scope.explorer}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.protectedExplorer}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.protectedExplorer}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/other-explorer/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/other-explorer/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(requestURL, `${scope.uiOrigin}${path}?outputId=other-target`, scope).ok, false);
});

test('Cancel evidence detects draft metadata and workspace changes', () => {
  const before = {
    draftVersion: 3,
    draftDigest: 'digest-3',
    workspace: { documents: [{ output: { id: 'target-1' }, construction: { steps: [{ id: 'step-1' }] } }] },
  };
  assert.equal(builderCancelStateEvidence(before, structuredClone(before)).ok, true);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), draftVersion: 4 }).ok, false);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), draftDigest: 'different' }).ok, false);
  const changedWorkspace = structuredClone(before);
  changedWorkspace.workspace.documents[0].construction.steps[0].id = 'step-2';
  assert.equal(builderCancelStateEvidence(before, changedWorkspace).ok, false);
  assert.equal(builderCancelStateEvidence(undefined, undefined).ok, false);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), workspace: undefined }).ok, false);
  assert.equal(builderCancelStateEvidence({ ...structuredClone(before), draftVersion: 0 }, before).ok, false);
  assert.equal(builderCancelStateEvidence({ ...structuredClone(before), draftDigest: '' }, before).ok, false);
});

test('removal proposal evidence binds the exact step, target, snapshot, route, and receipt', () => {
  const candidateConstruction = { version: 1, steps: [] };
  const requestBody = {
    outputId: 'target-1',
    removeStepIds: ['step-1'],
    snapshotToken: 'snapshot-3',
    expectedDraftVersion: 3,
    expectedDraftDigest: 'digest-3',
    candidateConstruction,
  };
  const response = {
    outputId: 'target-1',
    snapshotToken: 'snapshot-3',
    draftVersion: 3,
    draftDigest: 'digest-3',
    candidateConstruction: structuredClone(candidateConstruction),
    proposalId: 'proposal-4',
    previewStatus: 'READY',
    preview: { outputId: 'target-1', receiptId: 'proposal-4' },
  };
  const positive = {
    responseStatus: 200,
    response,
    requestBody,
    expectedOutputId: 'target-1',
    expectedStepID: 'step-1',
    expectedSnapshotToken: 'snapshot-3',
    expectedDraftVersion: 3,
    expectedDraftDigest: 'digest-3',
    domProposalId: 'proposal-4',
    domReceiptId: 'proposal-4',
    transportEvidence: { ok: true },
  };
  assert.equal(removalProposalEvidence(positive).ok, true);
  assert.equal(removalProposalEvidence({ ...positive, requestBody: { ...requestBody, removeStepIds: ['other-step'] } }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, response: { ...response, draftDigest: 'different' } }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, domReceiptId: 'other-receipt' }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, transportEvidence: { ok: false } }).ok, false);
});

test('removal restoration compares the complete target document', () => {
  const before = { rootResourceType: 'Observation', output: { id: 'target-1', title: 'Output' }, columns: [], construction: { steps: [] } };
  assert.equal(targetDocumentStateEvidence(before, structuredClone(before)).ok, true);
  assert.equal(targetDocumentStateEvidence(before, { ...structuredClone(before), rootResourceType: 'Patient' }).ok, false);
  assert.equal(targetDocumentStateEvidence(undefined, before).ok, false);
  assert.equal(targetDocumentStateEvidence(null, null).ok, false);
});
