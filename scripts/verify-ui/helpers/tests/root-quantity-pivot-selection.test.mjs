import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { includeBrowserDiagnostics } from '../cda-playwright.mjs';
import { finishCdaReport } from '../cda-fixtures.mjs';
import { createFixtureBrowserDiagnostics } from '../fixtures.mjs';
import {
  createFixtureNativeRequestLedger,
  finalizeFixtureNativeRequestReport,
  projectFixtureNetworkDiagnostics,
} from '../native-request-ledger.mjs';
import { classifyNetworkRecord, createReport, finishReport, recordCheck } from '../report.mjs';
import {
  classifyRootQuantityPivotValidationConsoleBatch,
  markRootQuantityPivotValidationBatchExpected,
  pivotSourceSelectionReady,
  unexpectedRootQuantityPivotConsoleErrors,
} from '../../workflows/root-quantity-pivot-workflow.mjs';

const fixtureDiagnosticsReport = () => ({
  errors: [],
  assertions: [],
  assetFailures: [],
  dimensions: { correctness: { status: 'untested', evidence: [] } },
});

const installDocument = ({ controls, checkboxes = [] }) => {
  const previous = globalThis.document;
  globalThis.document = {
    querySelector: selector => controls.get(selector) ?? null,
    querySelectorAll: selector => selector === 'input[type="checkbox"]' ? checkboxes : [],
  };
  return () => {
    if (previous === undefined) delete globalThis.document;
    else globalThis.document = previous;
  };
};

test('Pivot source postcondition accepts the exact reset group and generated category/value fields', () => {
  const groupSelector = 'select[aria-label="Add pivot group field"]';
  const categorySelector = 'select[aria-label="Pivot category field"]';
  const valueSelector = 'select[aria-label="Pivot values field"]';
  const groupChoice = 'source:choice-status';
  const categoryChoice = 'source:choice-code';
  const valueChoice = 'source:choice-value';
  const groupControl = { options: [{ value: '' }, { value: 'source:choice-other' }], value: '' };
  const categoryControl = {
    options: [{ value: 'pivot-input_code', textContent: 'Code' }],
    value: 'pivot-input_code',
    selectedOptions: [{ value: 'pivot-input_code', textContent: 'Code' }],
  };
  const valueControl = {
    options: [{ value: 'pivot-input_value', textContent: 'Quantity (decimal)' }],
    value: 'pivot-input_value',
    selectedOptions: [{ value: 'pivot-input_value', textContent: 'Quantity (decimal)' }],
  };
  const checkedGroup = { getAttribute: name => name === 'aria-label' ? 'Pivot group Status' : null, checked: true };
  const restore = installDocument({
    controls: new Map([[groupSelector, groupControl], [categorySelector, categoryControl], [valueSelector, valueControl]]),
    checkboxes: [checkedGroup],
  });
  try {
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: groupChoice, role: 'group', outputLabel: 'Status' }), true);
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: categoryChoice, role: 'category', outputLabel: 'Code' }), true);
    assert.equal(pivotSourceSelectionReady({ selector: valueSelector, sourceValue: valueChoice, role: 'value', outputLabel: 'Quantity', outputType: 'decimal' }), true);
  } finally {
    restore();
  }
});

test('Pivot source postcondition rejects a retained source choice, wrong group, and wrong remapped field', () => {
  const groupSelector = 'select[aria-label="Add pivot group field"]';
  const categorySelector = 'select[aria-label="Pivot category field"]';
  const sourceChoice = 'source:choice-status';
  const groupControl = { options: [{ value: sourceChoice }], value: '' };
  const categoryControl = {
    options: [{ value: 'pivot-input_other', textContent: 'Other' }],
    value: 'pivot-input_other',
    selectedOptions: [{ value: 'pivot-input_other', textContent: 'Other' }],
  };
  const uncheckedGroup = { getAttribute: name => name === 'aria-label' ? 'Pivot group Other' : null, checked: true };
  const restore = installDocument({ controls: new Map([[groupSelector, groupControl], [categorySelector, categoryControl]]), checkboxes: [uncheckedGroup] });
  try {
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: sourceChoice, role: 'group', outputLabel: 'Status' }), false);
    groupControl.options = [{ value: '' }];
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: sourceChoice, role: 'group', outputLabel: 'Status' }), false);
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: 'source:choice-code', role: 'category', outputLabel: 'Code' }), false);
    categoryControl.options = [{ value: 'pivot-input_code', textContent: 'Code' }];
    categoryControl.value = 'source:choice-code';
    categoryControl.selectedOptions = [{ value: 'source:choice-code', textContent: 'Code' }];
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: 'source:choice-code', role: 'category', outputLabel: 'Code' }), false);
  } finally {
    restore();
  }
});

test('workflow times the role-aware selection postcondition instead of requiring the ephemeral source token to persist', async () => {
  const workflow = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /after:\s*\(\)\s*=>\s*waitForObservable\(page,\s*pivotSourceSelectionReady/);
  assert.match(workflow, /const selectPivotSource = async \(label, path\) =>[\s\S]*?await action\(/);
  assert.doesNotMatch(workflow, /const selectPivotSource = async \(label, path\) =>[\s\S]*?await selectNative\(page, selector, matches\[0\]\.value\)/);
});

test('fixture workflow diagnostics drain owned HTTP bodies and keep raw browser projection explicit', async () => {
  let resolveBody;
  const body = new Promise(resolve => { resolveBody = resolve; });
  const report = fixtureDiagnosticsReport();
  const adapter = createFixtureBrowserDiagnostics(report);
  let drainFinished = false;
  adapter.record('console', {
    text: 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)',
    location: 'http://127.0.0.1:30008/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
  });
  adapter.trackHttpDiagnosticRead(body.then(responseBody => {
    adapter.record('httpFailures', {
      url: 'http://127.0.0.1:30008/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
      status: 422,
      body: responseBody,
      browserRequestId: 'fixture-playwright-422',
      playwrightRequestId: 'fixture-playwright-422',
    });
  }), {
    browserRequestId: 'fixture-playwright-422',
    requestId: 'fixture-request-422',
    method: 'POST',
    path: '/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
    status: 422,
  });

  const draining = adapter.finalize({ timeoutMs: 500 }).then(result => {
    drainFinished = true;
    return result;
  });
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(drainFinished, false, 'the fixture lifecycle drain must wait for its captured response body');
  resolveBody('{"error":{"code":"TABLE_PIVOT_CELL_CARDINALITY"}}');
  const outcome = await draining;
  assert.equal(outcome.drainError, undefined);

  assert.equal(report.errors.length, 0,
    'fixture finalization retains raw diagnostics without duplicating them into the generic report error gate');
  adapter.includeBrowserDiagnostics();
  assert.equal(report.errors.filter(error => error.kind === 'console').length, 1);
  assert.equal(report.errors.filter(error => error.kind === 'http').length, 1);
  assert.equal(report.errors.find(error => error.kind === 'http').playwrightRequestId, 'fixture-playwright-422');
  assert.equal(report.browserDiagnostics.retainedCounts.httpFailures, 1);
  assert.equal(report.assertions.find(assertion => assertion.name === 'fixture HTTP response diagnostics drained before report finalization').status, 'passed');

  const fixtures = await readFile(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  const spec = await readFile(new URL('../../specs/root-quantity-pivot.spec.mjs', import.meta.url), 'utf8');
  assert.match(fixtures, /\.\.\.browserDiagnostics, page, check, action, fault, nativeRequestLedger/);
  assert.match(fixtures, /browserDiagnostics\.record\('console'/);
  assert.match(fixtures, /browserDiagnostics\.record\('httpFailures'/);
  assert.match(fixtures, /browserDiagnostics\.record\('networkFailures'/);
  assert.match(fixtures, /includeBrowserDiagnostics\(\)\s*\{\s*project\(\);/);
  const teardownDrain = fixtures.indexOf('await browserDiagnostics.finalize({ timeoutMs: 5_000 });');
  const nativeReportFinalization = fixtures.indexOf('finalizeFixtureNativeRequestReport(', teardownDrain);
  assert(teardownDrain >= 0 && nativeReportFinalization > teardownDrain,
    'fixture teardown must drain and project response bodies before native report finalization');
  assert.match(spec, /\.\.\.workflow,\s*caseName: 'fixture-lifecycle'/);
});

test('fixture finalization keeps injected faults request-scoped and same-path status peers fatal', async () => {
  const project = 'fixture-owned';
  const explorer = 'editor';
  const origin = 'http://127.0.0.1:30008';
  const url = `${origin}/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const message = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const ledger = createFixtureNativeRequestLedger();
  const scope = ledger.openScope({ project, origin });
  const report = createReport({
    scenario: 'fixture-ledger-test',
    caseName: 'fixture-request-scoped-injected-fault',
    target: { fixtureProject: project },
  });
  const requests = ['browser-expected-422', 'browser-unexpected-422'].map((browserRequestId, index) => {
    const requestId = `server-request-${index + 1}`;
    const request = {
      url: () => url,
      method: () => 'POST',
      headers: () => ({ 'x-request-id': requestId }),
      resourceType: () => 'fetch',
    };
    ledger.recordRequest(request, {
      requestId,
      browserRequestId,
      method: 'POST',
      resourceType: 'fetch',
      url,
      startedAt: index + 1,
    });
    ledger.recordResponse(request, { status: 422, serverRequestId: requestId, observedAt: index + 2 });
    ledger.recordFinished(request, { observedAt: index + 3 });
    const diagnostic = {
      kind: 'network',
      status: 422,
      method: 'POST',
      url,
      rawURL: url,
      browserRequestId,
      playwrightRequestId: browserRequestId,
      responseBody: {
        captureState: 'completed',
        body: index === 0 ? '{"error":"injected"}' : '{"error":"unexpected"}',
      },
    };
    ledger.associateDiagnostic(request, diagnostic);
    return diagnostic;
  });
  report.network.push(...requests, {
    kind: 'console-error', text: message, location: url, rawLocation: url,
  });
  const adapter = createFixtureBrowserDiagnostics(report);
  adapter.record('httpFailures', {
    url, status: 422, body: { captureState: 'completed', body: '{"error":"injected"}' },
    browserRequestId: 'browser-expected-422', playwrightRequestId: 'browser-expected-422',
  });
  adapter.record('httpFailures', {
    url, status: 422, body: { captureState: 'completed', body: '{"error":"unexpected"}' },
    browserRequestId: 'browser-unexpected-422', playwrightRequestId: 'browser-unexpected-422',
  });
  adapter.record('console', { text: message, location: url });

  await adapter.finalize({ timeoutMs: 500 });
  assert.equal(report.errors.length, 0, 'teardown keeps browser diagnostics in their raw channels');
  projectFixtureNetworkDiagnostics({
    report,
    ledger,
    faults: [{
      id: 'injected-422',
      matched: true,
      action: 'fulfill',
      responseStatus: 422,
      playwrightRequestId: 'browser-expected-422',
      method: 'POST',
      rawURL: url,
    }],
  });
  await ledger.flush(scope, { explorer, timeoutMs: 20 });
  await finalizeFixtureNativeRequestReport({ report, ledger, project });
  finishReport(report);

  const expected = report.network.find(entry => entry.kind === 'network' && entry.playwrightRequestId === 'browser-expected-422');
  const unexpected = report.network.find(entry => entry.kind === 'network' && entry.playwrightRequestId === 'browser-unexpected-422');
  const pairedConsole = report.network.find(entry => entry.observedAs === 'console-error');
  assert.equal(classifyNetworkRecord(expected), 'expected-injected');
  assert.equal(expected.injectedRequestId, 'injected-422');
  assert.equal(classifyNetworkRecord(unexpected), 'unexpected-error',
    'same method, path, and status cannot transfer the policy to another browser request');
  assert.equal(expected.responseBody.captureState, 'completed');
  assert.equal(expected.responseBody.body, '{"error":"injected"}',
    'the exact request’s completed response body remains in the native network evidence');
  assert.equal(pairedConsole.injectedFault, true);
  assert.equal(pairedConsole.playwrightRequestId, 'browser-expected-422',
    'the unique exact console pairing retains the injected Request identity');
  assert.equal(adapter.diagnostics.httpFailures.length, 2,
    'both raw HTTP responses remain available with their distinct browser IDs');
  assert.deepEqual(adapter.diagnostics.httpFailures.map(entry => entry.playwrightRequestId), [
    'browser-expected-422', 'browser-unexpected-422',
  ]);
  assert.equal(adapter.diagnostics.console.length, 1, 'the raw console evidence remains retained');
  assert.equal(report.nativeRequests.find(entry => entry.browserRequestId === 'browser-expected-422').injectedFault, true);
  assert.equal(report.status, 'failed', 'the same-path, same-status request with another identity remains fatal');

  const explicitlyProjectedReport = { errors: [] };
  const explicitAdapter = createFixtureBrowserDiagnostics(explicitlyProjectedReport);
  explicitAdapter.record('console', { text: 'Pivot workflow diagnostic', location: url });
  explicitAdapter.includeBrowserDiagnostics();
  assert.equal(explicitlyProjectedReport.errors[0].message, 'Pivot workflow diagnostic',
    'Pivot workflows can still explicitly project raw browser diagnostics when needed');
});

test('an extra console error remains fatal when exact request policy consumes only one record', async () => {
  const url = 'http://127.0.0.1:30008/api/v1/projects/fixture-owned/explorers/editor/authoring/v2/construction-proposals';
  const message = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const report = createReport({ scenario: 'fixture-ledger-test', caseName: 'extra-console-remains-fatal' });
  report.network.push(
    { kind: 'network', status: 422, method: 'POST', url, rawURL: url, playwrightRequestId: 'browser-expected-422' },
    { kind: 'console-error', text: message, location: url, rawLocation: url },
    { kind: 'console-error', text: message, location: url, rawLocation: url },
  );
  const adapter = createFixtureBrowserDiagnostics(report);
  adapter.record('console', { text: message, location: url });
  adapter.record('console', { text: message, location: url });
  await adapter.finalize({ timeoutMs: 500 });
  projectFixtureNetworkDiagnostics({
    report,
    ledger: { linkProjectedDiagnostics() {} },
    faults: [{
      id: 'injected-422', matched: true, action: 'fulfill', responseStatus: 422,
      playwrightRequestId: 'browser-expected-422', method: 'POST', rawURL: url,
    }],
  });
  finishReport(report);

  assert.equal(adapter.diagnostics.console.length, 2, 'both ambiguous raw console observations remain captured');
  assert.equal(report.network.filter(entry => entry.observedAs === 'console-error' && entry.injectedFault).length, 1,
    'the exact request policy consumes at most one console record');
  assert.equal(report.network.filter(entry => entry.kind === 'console-error').length, 1,
    'the additional console error remains unclassified and fatal');
  assert.equal(report.status, 'failed');
});

test('fixture teardown projects a failed response-body read and records it without throwing', async () => {
  const report = fixtureDiagnosticsReport();
  const adapter = createFixtureBrowserDiagnostics(report);
  const responseBody = { captureState: 'pending' };
  const read = Promise.resolve().then(() => { throw new Error('retained response body read failed'); }).catch(error => {
    responseBody.captureState = 'readfailed';
    responseBody.error = error.message;
    adapter.record('httpFailures', {
      url: 'http://127.0.0.1:30008/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
      status: 422,
      body: responseBody,
      browserRequestId: 'fixture-playwright-failed-body',
      playwrightRequestId: 'fixture-playwright-failed-body',
    });
    throw error;
  });
  adapter.trackHttpDiagnosticRead(read, {
    browserRequestId: 'fixture-playwright-failed-body',
    requestId: 'fixture-request-failed-body',
    method: 'POST',
    path: '/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
    status: 422,
  });

  const outcome = await adapter.finalize({ timeoutMs: 500 });

  assert.match(outcome.drainError, /Failed fixture HTTP diagnostic bodies/);
  assert.equal(responseBody.captureState, 'readfailed');
  assert.equal(adapter.diagnostics.httpFailures[0].body.captureState, 'readfailed',
    'the failed response body remains in the retained raw HTTP channel');
  assert.match(report.errors.find(error => error.kind === 'browser-diagnostic-drain').message, /retained response body read failed/);
  assert.equal(report.assertions.find(assertion => assertion.name === 'fixture HTTP response diagnostics drained before report finalization').status, 'failed');
});

test('fixture teardown times out pending response reads without masking an earlier workflow failure', async () => {
  const report = fixtureDiagnosticsReport();
  const adapter = createFixtureBrowserDiagnostics(report);
  adapter.trackHttpDiagnosticRead(new Promise(() => {}), {
    browserRequestId: 'fixture-playwright-pending-body',
    requestId: 'fixture-request-pending-body',
    method: 'POST',
    path: '/api/v1/projects/fixture/explorers/root/authoring/v2/construction-proposals',
    status: 422,
  });
  const workflowError = new Error('earlier workflow assertion failed');

  const observedError = await (async () => {
    try {
      throw workflowError;
    } finally {
      const outcome = await adapter.finalize({ timeoutMs: 15 });
      assert.match(outcome.drainError, /Timed out flushing fixture HTTP diagnostic bodies/);
    }
  })().then(() => undefined, error => error);

  assert.equal(observedError, workflowError);
  assert.match(report.errors.find(error => error.kind === 'browser-diagnostic-drain').message, /fixture-playwright-pending-body/);
  assert.equal(report.assertions.find(assertion => assertion.name === 'fixture HTTP response diagnostics drained before report finalization').status, 'failed');
});

test('fixture diagnostic channels cap every event class and report exact drop counts', async () => {
  const report = fixtureDiagnosticsReport();
  const adapter = createFixtureBrowserDiagnostics(report);
  const channels = ['pageErrors', 'console', 'networkFailures', 'httpFailures', 'assetFailures'];
  for (const channel of channels) {
    for (let index = 0; index < 100; index += 1) {
      assert.equal(adapter.record(channel, {
        message: `event-${channel}-${index}`,
        text: `event-${channel}-${index}`,
        url: `http://127.0.0.1/${channel}/${index}`,
        browserRequestId: `${channel}-${index}`,
        playwrightRequestId: `${channel}-${index}`,
        status: 422,
      }), true);
    }
    assert.equal(adapter.record(channel, { message: `overflow-${channel}` }), false);
  }

  const outcome = await adapter.finalize({ timeoutMs: 500 });

  for (const channel of channels) {
    assert.equal(adapter.diagnostics[channel].length, 100);
    assert.equal(outcome.droppedCounts[channel], 1);
    assert.equal(report.browserDiagnostics.retainedCounts[channel], 100);
    assert.equal(report.browserDiagnostics.droppedCounts[channel], 1);
  }
  assert.equal(report.errors.find(error => error.kind === 'browser-diagnostic-overflow').droppedCounts.httpFailures, 1);
  assert.equal(report.assertions.find(assertion => assertion.name === 'fixture browser diagnostic channels stayed within capture limits').status, 'failed');
});

test('live fixture diagnostics are projected before batch classification and repeated teardown projection is idempotent', async () => {
  const workflow = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  const fullPopulationLifecycle = workflow.slice(workflow.indexOf('await runFullPopulationLifecycle('));
  assert.match(fullPopulationLifecycle,
    /await drainResponseReads\(\);\s*await context\.flushHttpDiagnostics\(\{ timeoutMs: 5_000 \}\);\s*context\.includeBrowserDiagnostics\(\);\s*const validationBatch = classifyRootQuantityPivotValidationConsoleBatch\(/);

  const message = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const url = 'http://127.0.0.1:30008/api/v1/projects/loom_dev_cda_fhir/explorers/root-quantity-category-test/authoring/v2/construction-proposals';
  const diagnostics = {
    pageErrors: [],
    console: [
      { text: message, location: url },
      { text: message, location: url },
    ],
    networkFailures: [],
    httpFailures: [
      { url, status: 422, body: '{"error":{"code":"TABLE_PIVOT_CELL_CARDINALITY"}}', playwrightRequestId: 'cda-request-121' },
      { url, status: 422, body: '{"error":{"code":"TABLE_PIVOT_CELL_CARDINALITY"}}', playwrightRequestId: 'cda-request-126' },
    ],
    assetFailures: [],
  };
  const report = {
    errors: [],
    network: [
      { kind: 'network', status: 422, method: 'POST', url, playwrightRequestId: 'cda-request-121' },
      { kind: 'network', status: 422, method: 'POST', url, playwrightRequestId: 'cda-request-126' },
      { kind: 'console-error', text: message, location: url },
      { kind: 'console-error', text: message, location: url },
    ],
  };
  includeBrowserDiagnostics(diagnostics, report);
  assert.equal(report.errors.filter(error => error.kind === 'console' && error.message === message).length, 1);
  assert.equal(report.errors.filter(error => error.kind === 'http' && error.url === url && error.status === 422).length, 1);
  assert.equal(report.errors.filter(error => error.kind === 'console-error').length, 0,
    'The live error ledger projects generic console events while raw console-error entries stay in the network ledger');
  assert.equal(report.network.filter(error => error.kind === 'console-error').length, 2);
  assert.equal(diagnostics.console.length, 2, 'Raw console diagnostics remain available for exact two-event validation');
  const once = structuredClone(report.errors);
  includeBrowserDiagnostics(diagnostics, report);
  assert.deepEqual(report.errors, once, 'The fixture teardown projection must not duplicate live diagnostics');
});

const validationBatchFixture = ({ retainedCase016 = false } = {}) => {
  const project = retainedCase016 ? 'loom_dev_verify_mv0shkfb-b03838f' : 'loom_dev_cda_fhir';
  const explorer = retainedCase016 ? 'root-quantity-category-1791539714311' : 'root-quantity-category-test';
  const origin = 'http://127.0.0.1:30008';
  const outputId = retainedCase016 ? 'out_22c2a6b07c411065a0389d20' : 'out_root_quantity';
  const route = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const url = origin + route;
  const snapshotToken = retainedCase016
    ? 'sha256:d334a20d129ce3945acb0f1e5f8494c5d666e7770919e92abedd89ddc4451a0c'
    : 'sha256:test-snapshot';
  const draftDigest = retainedCase016
    ? 'sha256:6531c650dfebe410e10ea7101daa192de3a6545e05a84a447ef528d56314415f'
    : 'sha256:test-draft';
  const initialDraft = { snapshotToken, draftVersion: 4, draftDigest };
  const duplicateWitness = retainedCase016 ? {
    status: 'final',
    present: true,
    value: 'd',
    rawWitnessIDs: ['quantity-pivot-string-a', 'quantity-pivot-string-b'],
    rowCount: 2,
    numericCount: 2,
    valueSum: 6,
    valueMax: 4,
  } : { status: 'final', present: true, value: 'd', rowCount: 100, numericCount: 100, valueSum: 250, valueMax: 5 };
  const rawDuplicateBucket = Array.isArray(duplicateWitness.rawWitnessIDs) ? {
    rawWitnessIDs: duplicateWitness.rawWitnessIDs,
    status: duplicateWitness.status,
    category: JSON.stringify({ kind: 'STRING', string: duplicateWitness.value }),
  } : {
    status: duplicateWitness.status,
    category: JSON.stringify({ kind: 'STRING', string: duplicateWitness.value }),
    rowCount: duplicateWitness.rowCount,
    numericCount: duplicateWitness.numericCount,
    sum: duplicateWitness.valueSum,
    max: duplicateWitness.valueMax,
  };
  const consoleMessage = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const ids = [
    ...(retainedCase016
      ? ['construction-proposal-fb900a9e-e9af-453d-a738-2892f86a4903', 'construction-proposal-e85f11fc-a0ce-45f9-bc8a-b344001c7f84']
      : ['construction-proposal-first', 'construction-proposal-second']),
  ];
  const retainedRequests = [
    {
      browserRequestId: 'playwright-10',
      sumRepairRequestId: 'construction-proposal-f901ead8-28ad-4eba-b8d9-d8ecc70421f6',
      sumRepairBrowserRequestId: 'playwright-11',
      startedAt: 1791539720045,
      responseReceivedAt: 1791539720167,
      completedAt: 1791539720174,
      sumStartedAt: 1791539720523,
      sumResponseReceivedAt: 1791539720715,
      sumCompletedAt: 1791539720724,
      stepId: 'pivot_919622f5-ce3f-461d-bf17-db4cd2d9b40c',
      groupColumnId: 'pivot-input_9ddd2203-33e2-442a-a49f-afc2290af9a7',
      categoryColumnId: 'pivot-input_4d186de2-b4b4-4421-b0f3-4088c91987b8',
      valueColumnId: 'pivot-input_6ab2db72-9e61-42cc-8e73-21fdcad2af73',
      sourceProjectionOrder: ['category', 'value', 'group'],
      outputColumnIds: [
        'pivot-column_bd6e752c-ff20-4bfe-a85a-fda3b8a02125',
        'pivot-column_0b5eda0a-846d-475c-a896-a5976e89f25b',
        'pivot-column_c9a0da9a-1b1c-4281-9917-525161925b3c',
      ],
      repairReceiptId: 'receipt_be91ba4e4304854ed3eeb2b96091d1460eab53a5610fbabdfab52646ff08f175',
      baseReceiptId: 'receipt_94f7a493206257080cc6d7a330024ae8df0c7f1d476e18283ce43bf6f8dca67c',
      baseDocumentDigest: 'sha256:660fe947fac69f9f5091adeb3289cf6b6e1baaa17b803f10662576ed607c0607',
    },
    {
      browserRequestId: 'playwright-15',
      sumRepairRequestId: 'construction-proposal-fa2f8a0f-a574-4ca8-b3a1-4a206c9be524',
      sumRepairBrowserRequestId: 'playwright-16',
      startedAt: 1791539721984,
      responseReceivedAt: 1791539722122,
      completedAt: 1791539722129,
      sumStartedAt: 1791539722473,
      sumResponseReceivedAt: 1791539722620,
      sumCompletedAt: 1791539722628,
      stepId: 'pivot_2067f5cd-0561-4859-a56e-ef434917a958',
      groupColumnId: 'pivot-input_54383f9d-4197-4ef4-9519-2d6ac11e9e6a',
      categoryColumnId: 'pivot-input_0f59ce48-34e7-4854-98de-40d6a12a5001',
      valueColumnId: 'pivot-input_65b4136d-c7f4-45d9-8693-da1a39bd3851',
      sourceProjectionOrder: ['category', 'group', 'value'],
      outputColumnIds: [
        'pivot-column_69676191-7b38-443b-83ad-c7ef5b1515a2',
        'pivot-column_623dcd7d-9710-4cf9-896a-bd73261dc972',
        'pivot-column_bfc2ce1e-b836-494e-beca-ebfabfe7516b',
      ],
      repairReceiptId: 'receipt_0310f871b4c08d282611b39c88ab5cd1142a63a75f465e789d1724fc8b174a83',
      baseReceiptId: 'receipt_94f7a493206257080cc6d7a330024ae8df0c7f1d476e18283ce43bf6f8dca67c',
      baseDocumentDigest: 'sha256:660fe947fac69f9f5091adeb3289cf6b6e1baaa17b803f10662576ed607c0607',
    },
  ];
  const chronology = (browserRequestId, startedAt, responseReceivedAt, completedAt) => [
    { event: 'request', browserRequestId, observedAt: startedAt, objectMatch: true },
    { event: 'response', browserRequestId, observedAt: responseReceivedAt, objectMatch: true },
    { event: 'requestfinished', browserRequestId, observedAt: responseReceivedAt + 1, objectMatch: true },
  ];
  const validated = ids.map((requestId, index) => {
    const retained = retainedCase016 ? retainedRequests[index] : null;
    const browserRequestId = retained?.browserRequestId ?? `playwright-${index + 10}`;
    const diagnostic = {
      code: 'TABLE_PIVOT_CELL_CARDINALITY',
      message: 'More than one record matched a Pivot cell; choose how to handle duplicates or filter the input rows.',
      requestId,
      severity: 'error',
      stage: 'preview',
    };
    const response = {
      diagnostics: [diagnostic],
      error: { code: diagnostic.code, message: diagnostic.message, requestId, diagnostic },
    };
    const body = {
      outputId,
      snapshotToken,
      expectedDraftVersion: initialDraft.draftVersion,
      expectedDraftDigest: draftDigest,
      changedStepId: retained?.stepId ?? `pivot-step-${index}`,
      candidateConstruction: {
        version: 1,
        steps: [{
          id: retained?.stepId ?? `pivot-step-${index}`,
          ...(retained ? { inputs: [{ kind: 'SOURCE_PROJECTION' }] } : {}),
          operation: {
            kind: 'PIVOT',
            pivot: {
              ...(retained ? { constructionId: retained.stepId } : {}),
              groupKeyIds: [retained?.groupColumnId ?? 'group-column'],
              categoryColumnId: retained?.categoryColumnId ?? 'category-column',
              valueColumnId: retained?.valueColumnId ?? 'value-column',
              categories: retainedCase016 ? [
                { key: { kind: 'MISSING' }, outputColumnId: 'pivot-column_bd6e752c-ff20-4bfe-a85a-fda3b8a02125' },
                { key: { kind: 'NULL' }, outputColumnId: 'pivot-column_0b5eda0a-846d-475c-a896-a5976e89f25b' },
                { key: { kind: 'STRING', string: 'd' }, outputColumnId: 'pivot-column_c9a0da9a-1b1c-4281-9917-525161925b3c' },
              ] : [{ key: { kind: 'STRING', string: 'd' }, outputColumnId: 'output-d' }],
              duplicatePolicy: 'ERROR',
              ...(retained ? { missingCellPolicy: 'NULL', unlistedCategoryPolicy: 'ERROR' } : {}),
            },
          },
          ...(retained ? { outputs: [
            { id: retained.groupColumnId, name: 'status', label: 'Observation.status', type: 'string' },
            { id: retained.outputColumnIds[0], name: 'missing_value', label: 'Missing', type: 'decimal' },
            { id: retained.outputColumnIds[1], name: 'null_value', label: 'Null', type: 'decimal' },
            { id: retained.outputColumnIds[2], name: 'd', label: 'd', type: 'decimal' },
          ] } : {}),
        }],
      },
      pivotSources: [
        { choiceId: `choice-group-${index}`, columnId: retained?.groupColumnId ?? 'group-column' },
        { choiceId: `choice-category-${index}`, columnId: retained?.categoryColumnId ?? 'category-column' },
        { choiceId: `choice-value-${index}`, columnId: retained?.valueColumnId ?? 'value-column' },
      ],
    };
    const startedAt = retained?.startedAt ?? 100 + index * 100;
    const responseReceivedAt = retained?.responseReceivedAt ?? startedAt + 10;
    const completedAt = retained?.completedAt ?? startedAt + 20;
    const request = {
      requestId,
      browserRequestId,
      endpoint: 'construction-proposals',
      path: route,
      origin,
      method: 'POST',
      startedAt,
      responseReceivedAt,
      completedAt,
      nativeEventChronology: chronology(browserRequestId, startedAt, responseReceivedAt, completedAt),
      serverRequestId: requestId,
      status: 422,
      body,
      response,
    };
    const validation = {
      requestId,
      backendRequestId: requestId,
      code: diagnostic.code,
      duplicatePolicy: 'ERROR',
      project,
      explorer,
      outputId,
      snapshotToken,
      draftVersion: initialDraft.draftVersion,
      rawDuplicateBucket,
      ...(retained ? { rawDuplicateWitnessIDs: duplicateWitness.rawWitnessIDs } : {}),
      visibleRepair: {
        alert: diagnostic.message,
        policy: 'ERROR',
        summary: 'Duplicate values: stop the pivot with an error.',
        sumOption: { label: 'Add them together', disabled: false },
      },
      classification: 'expected-domain-validation-repaired-by-user-selected-SUM',
    };
    const fixturePlaywrightRequestId = retained?.browserRequestId === 'playwright-10' ? 'request-121'
      : retained?.browserRequestId === 'playwright-15' ? 'request-126'
        : `cda-request-${index + 121}`;
    const fixtureNetworkRequest = {
      kind: 'network',
      status: 422,
      method: 'POST',
      url,
      requestDetails: { requestId, draftVersion: 4, draftDigest, outputId },
      playwrightRequestId: fixturePlaywrightRequestId,
      responseBody: { captureState: 'completed', body: JSON.stringify(response) },
    };
    return {
      request,
      validation,
      fixtureNetworkRequest,
      fixturePlaywrightRequestId,
      consoleError: { kind: 'console-error', text: consoleMessage, location: url },
      fixtureConsoleDiagnostic: { kind: 'console-error', text: consoleMessage, location: url },
      workflowConsole: { kind: 'console', message: consoleMessage, location: url },
      workflowHTTP: { kind: 'http', requestId, browserRequestId, url, status: 422 },
      sumRepair: retained ? {
        requestId: retained.sumRepairRequestId,
        browserRequestId: retained.sumRepairBrowserRequestId,
        endpoint: 'construction-proposals',
        path: route,
        origin,
        method: 'POST',
        startedAt: retained.sumStartedAt,
        responseReceivedAt: retained.sumResponseReceivedAt,
        completedAt: retained.sumCompletedAt,
        nativeEventChronology: chronology(retained.sumRepairBrowserRequestId, retained.sumStartedAt, retained.sumResponseReceivedAt, retained.sumCompletedAt),
        serverRequestId: retained.sumRepairRequestId,
        status: 200,
        body: (() => {
          const repairBody = structuredClone(body);
          repairBody.candidateConstruction.steps[0].operation.pivot.duplicatePolicy = 'SUM';
          return repairBody;
        })(),
        response: {
          candidateConstruction: {
            sourceProjections: retained.sourceProjectionOrder.map(role => ({
              columnId: role === 'category' ? retained.categoryColumnId : role === 'value' ? retained.valueColumnId : retained.groupColumnId,
              fhirType: role === 'value' ? 'decimal' : 'string',
              fieldPath: role === 'category' ? 'valueQuantity.code' : role === 'value' ? 'valueQuantity.value' : 'status',
              label: role === 'category' ? 'Observation.valueQuantity.code' : role === 'value' ? 'Observation.valueQuantity.value' : 'Observation.status',
              logicalType: role === 'value' ? 'decimal' : 'string',
              occurrenceId: 'base',
              ownerStepId: retained.stepId,
            })),
            steps: (() => {
              const steps = structuredClone(body.candidateConstruction.steps);
              steps[0].operation.pivot.duplicatePolicy = 'SUM';
              return steps;
            })(),
          },
          baseReceiptId: retained.baseReceiptId,
          baseDocumentDigest: retained.baseDocumentDigest,
          draftVersion: initialDraft.draftVersion,
          draftDigest,
          outputId,
          snapshotToken,
          previewStatus: 'READY',
          proposalId: retained.repairReceiptId,
          preview: {
            kind: 'ExplorerBuilderPreview',
            outputId,
            receiptId: retained.repairReceiptId,
            partialValidation: true,
            columns: [
              { chartable: true, column: 'status', filterable: true, label: 'Observation.status', logicalType: 'string', nullable: true, shape: 'scalar' },
              { chartable: true, column: 'missing_value', filterable: true, label: 'Missing', logicalType: 'decimal', nullable: true, shape: 'scalar' },
              { chartable: true, column: 'null_value', filterable: true, label: 'Null', logicalType: 'decimal', nullable: true, shape: 'scalar' },
              { chartable: true, column: 'd', filterable: true, label: 'd', logicalType: 'decimal', nullable: true, shape: 'scalar' },
            ],
            rowCount: 1,
            rows: [{
              __loom_row_id: `["GROUPED_PIVOT","${retained.stepId}",["STRING","final"]]`,
              d: 6,
              missing_value: 3,
              null_value: 5,
              status: 'final',
            }],
            diagnostics: [],
          },
        },
      } : (() => {
        const sumStartedAt = completedAt + 10;
        const sumResponseReceivedAt = sumStartedAt + 10;
        const sumCompletedAt = sumStartedAt + 20;
        const sumRequestId = `construction-proposal-sum-${index + 1}`;
        const sumBrowserRequestId = `playwright-sum-${index + 1}`;
        const sumBody = structuredClone(body);
        sumBody.candidateConstruction.steps[0].operation.pivot.duplicatePolicy = 'SUM';
        return {
          requestId: sumRequestId,
          browserRequestId: sumBrowserRequestId,
          endpoint: 'construction-proposals',
          path: route,
          origin,
          method: 'POST',
          startedAt: sumStartedAt,
          responseReceivedAt: sumResponseReceivedAt,
          completedAt: sumCompletedAt,
          nativeEventChronology: chronology(sumBrowserRequestId, sumStartedAt, sumResponseReceivedAt, sumCompletedAt),
          serverRequestId: sumRequestId,
          status: 200,
          body: sumBody,
          response: {
            candidateConstruction: { steps: structuredClone(sumBody.candidateConstruction.steps) },
            draftVersion: initialDraft.draftVersion,
            draftDigest,
            outputId,
            snapshotToken,
            previewStatus: 'READY',
            proposalId: `receipt-sum-${index + 1}`,
            preview: {
              kind: 'ExplorerBuilderPreview',
              outputId,
              receiptId: `receipt-sum-${index + 1}`,
              rowCount: 1,
              rows: [{ status: 'final', d: 6 }],
              diagnostics: [],
            },
          },
        };
      })(),
    };
  });
  const fixtureErrors = [];
  const fixtureDiagnostics = {
    pageErrors: [],
    console: validated.map(item => ({ text: item.consoleError.text, location: item.consoleError.location })),
    networkFailures: [],
    httpFailures: validated.map(item => ({
      url,
      status: 422,
      body: JSON.stringify(item.request.response),
      playwrightRequestId: item.fixturePlaywrightRequestId,
    })),
    assetFailures: [],
  };
  includeBrowserDiagnostics(fixtureDiagnostics, { errors: fixtureErrors });
  return {
    project,
    explorer,
    outputId,
    origin,
    initialDraft,
    duplicateWitness,
    validations: validated.map(item => item.validation),
    authoringRequests: validated.flatMap(item => [item.request, item.sumRepair]),
    workflowErrors: validated.flatMap(item => [item.workflowConsole, item.workflowHTTP]),
    fixtureNetwork: validated.flatMap(item => [item.fixtureNetworkRequest, item.consoleError]),
    fixtureErrors,
    fixtureDiagnostics,
    fixtureConsoleDiagnostics: validated.map(item => item.fixtureConsoleDiagnostic),
  };
};

const classifyFixture = fixture => classifyRootQuantityPivotValidationConsoleBatch(fixture);

const finishExpectedValidationBatch = ({ fixture = validationBatchFixture({ retainedCase016: true }), mutate } = {}) => {
  const validationBatch = classifyFixture(fixture);
  const batchEvidence = {
    kind: 'expected-root-quantity-pivot-validation-console-batch',
    project: fixture.project,
    explorer: fixture.explorer,
    route: validationBatch.route,
    status: validationBatch.status,
    code: validationBatch.code,
    duplicatePolicy: 'ERROR',
    outputId: fixture.outputId,
    snapshotToken: fixture.initialDraft.snapshotToken,
    draftVersion: fixture.initialDraft.draftVersion,
    draftDigest: fixture.initialDraft.draftDigest,
    rawDuplicateBucket: {
      rawWitnessIDs: fixture.duplicateWitness.rawWitnessIDs,
      status: fixture.duplicateWitness.status,
      category: JSON.stringify({ kind: 'STRING', string: fixture.duplicateWitness.value }),
      rowCount: fixture.duplicateWitness.rowCount,
      numericCount: fixture.duplicateWitness.numericCount,
      sum: fixture.duplicateWitness.valueSum,
      max: fixture.duplicateWitness.valueMax,
    },
    requestIDs: validationBatch.requestIDs,
    sumRepairPairs: validationBatch.sumRepairPairs,
    fixtureRequestPairs: validationBatch.fixtureRequestPairs,
    consoleEventCount: validationBatch.fixtureConsoleNetworkIndexes.length,
    consoleEventsHaveRequestIDs: false,
    association: validationBatch.association,
  };
  const report = createReport({
    scenario: 'root-quantity-pivot',
    caseName: 'fixture-lifecycle',
    target: {
      kind: 'isolated',
      project: fixture.project,
      fixtureProject: fixture.project,
      generation: 'devloop-v1',
      uiUrl: fixture.origin,
      explorer: fixture.explorer,
    },
    requiredChecks: ['root quantity fixture finalizer retains the exact validation proof'],
  });
  report.explorer = fixture.explorer;
  report.network = fixture.fixtureNetwork;
  report.errors = fixture.fixtureErrors;
  recordCheck(report, 'correctness', report.requiredChecks[0], true, { requestIDs: validationBatch.requestIDs });
  markRootQuantityPivotValidationBatchExpected({
    validationBatch,
    batchEvidence,
    workflowErrors: fixture.workflowErrors,
    nativeReport: report,
    browserConsoleDiagnostics: fixture.fixtureDiagnostics.console,
  });
  report.expectedHttpFailureBatches = [batchEvidence];
  projectFixtureNetworkDiagnostics({ report, ledger: { linkProjectedDiagnostics() {} }, faults: [] });
  mutate?.({ report, fixture, validationBatch, batchEvidence });
  finishReport(report);
  return { report, fixture, validationBatch, batchEvidence };
};

test('Basic finishReport accepts the exact request-bound CASE-016 validation batch after SUM repair', () => {
  const fixture = validationBatchFixture({ retainedCase016: true });
  const { report, validationBatch } = finishExpectedValidationBatch({
    fixture,
    mutate: ({ report }) => {
      for (const event of report.network.filter(record => record.kind === 'console-error')) {
        event.expectedHttpFailure = JSON.parse(JSON.stringify(event.expectedHttpFailure));
      }
    },
  });
  assert.equal(report.project, undefined, 'The retained browser report stores project identity under target.project');
  assert.equal(report.target.project, fixture.project);
  assert.equal(validationBatch.requestIDs.length, 2);
  assert.equal(report.network.length, 4, 'The two completed HTTP bodies and two console observations remain in the raw network evidence');
  assert.equal(report.network.filter(entry => entry.kind === 'network' && entry.status === 422).length, 2);
  assert.equal(report.network.filter(entry => entry.kind === 'console-error').length, 2);
  assert.equal(classifyNetworkRecord(report.network.find(entry => entry.kind === 'network')), 'unexpected-error',
    'The plain classifier does not trust expected flags; the report boundary must revalidate the whole batch proof');
  assert.equal(report.status, 'passed', 'The generic fixture finalizer must honor only the exact validated batch proof');
  assert.deepEqual(report.missingRequiredChecks, []);
  assert.equal(report.assertions.some(assertion => assertion.name === 'no unexpected network, module, or browser errors'), false);
  assert.equal(report.errors.some(error => error.kind === 'unexpected-network'), false);
});

test('Basic finishReport rejects a validation batch bound to a different target project', () => {
  const wrongTarget = finishExpectedValidationBatch({
    mutate: ({ report }) => { report.target.project = 'another-project'; },
  });
  assert.equal(wrongTarget.report.status, 'failed');
  assert(wrongTarget.report.assertions.some(assertion => assertion.name === 'no unexpected network, module, or browser errors' && assertion.status === 'failed'));
});

test('Basic finishReport keeps copied request proofs, wrong response bodies, and extra console errors fatal', () => {
  const sameRoute422 = finishExpectedValidationBatch({
    mutate: ({ report }) => {
      const source = report.network.find(entry => entry.kind === 'network');
      report.network.push({
        ...source,
        playwrightRequestId: 'unrelated-playwright-request',
        requestDetails: { ...source.requestDetails, requestId: 'unrelated-server-request' },
      });
    },
  });
  assert.equal(sameRoute422.report.status, 'failed', 'Copying the proof onto another request ID cannot consume a same-route 422');
  assert(sameRoute422.report.assertions.some(assertion => assertion.name === 'no unexpected network, module, or browser errors' && assertion.status === 'failed'));

  const extraConsole = finishExpectedValidationBatch({
    mutate: ({ report }) => {
      const source = report.network.find(entry => entry.kind === 'console-error');
      report.network.push({ ...source });
    },
  });
  assert.equal(extraConsole.report.status, 'failed', 'An additional ambiguous console error invalidates the two-event proof and remains fatal');

  const wrongBody = finishExpectedValidationBatch({
    mutate: ({ report }) => {
      const request = report.network.find(entry => entry.kind === 'network');
      request.responseBody.body = JSON.stringify({ error: { code: 'OTHER', requestId: request.requestDetails.requestId } });
    },
  });
  assert.equal(wrongBody.report.status, 'failed', 'The finalizer rechecks the completed response body instead of trusting an expected flag');

  const flagOnly = finishExpectedValidationBatch({
    mutate: ({ report }) => { report.expectedHttpFailureBatches = []; },
  });
  assert.equal(flagOnly.report.status, 'failed', 'Expected markers without the validated batch proof remain fatal');
});

test('full-population root quantity validation batch matches two exact ERROR proposals and raw witness', () => {
  const result = classifyFixture(validationBatchFixture());
  assert.deepEqual(result.requestIDs, ['construction-proposal-first', 'construction-proposal-second']);
  assert.equal(result.status, 422);
  assert.equal(result.code, 'TABLE_PIVOT_CELL_CARDINALITY');
  assert.equal(result.localConsoleIndexes.length, 2);
  assert.equal(result.fixtureConsoleNetworkIndexes.length, 2);
  assert.equal(result.fixtureDiagnosticConsoleIndexes.length, 2);
  assert.equal(result.fixtureConsoleErrorIndexes.length, 1);
  assert.deepEqual(result.fixtureConsoleErrorDiagnosticIndexes, []);
  assert.equal(result.fixtureHTTPIndexes.length, 1);
  assert.equal(result.fixtureRequestPairs.length, 2);
  assert.equal(result.sumRepairPairs.length, 2, 'Both full-population ERROR proposals must be repaired by captured SUM previews');
  assert.deepEqual(result.fixtureRequestPairs.map(({ browserRequestId, playwrightRequestId, networkIndex }) => ({ browserRequestId, playwrightRequestId, networkIndex })), [
    { browserRequestId: 'playwright-10', playwrightRequestId: 'cda-request-121', networkIndex: 0 },
    { browserRequestId: 'playwright-11', playwrightRequestId: 'cda-request-122', networkIndex: 2 },
  ]);
  assert.match(result.association, /console events have no request IDs/);
});

test('retained CASE-016 422 requests are classified only after their exact same-draft SUM proposals reach READY', () => {
  // Reduced projection of the retained CASE-016 report (SHA-256 97577a90b6155ab1c7aed0b713f21a09a58337455ad980b3bea37c03b156fe58); request IDs, target/draft bindings, source columns, timings, receipt, and preview fields come from that artifact.
  const fixture = validationBatchFixture({ retainedCase016: true });
  const result = classifyFixture(fixture);
  assert.equal(fixture.project, 'loom_dev_verify_mv0shkfb-b03838f');
  assert.equal(fixture.explorer, 'root-quantity-category-1791539714311');
  assert.equal(fixture.outputId, 'out_22c2a6b07c411065a0389d20');
  assert.deepEqual(result.requestIDs, [
    'construction-proposal-fb900a9e-e9af-453d-a738-2892f86a4903',
    'construction-proposal-e85f11fc-a0ce-45f9-bc8a-b344001c7f84',
  ]);
  assert.deepEqual(result.sumRepairPairs.map(({ validationRequestId, requestId, status, previewStatus, outputId, snapshotToken, draftVersion, draftDigest }) => ({
    validationRequestId, requestId, status, previewStatus, outputId, snapshotToken, draftVersion, draftDigest,
  })), [
    {
      validationRequestId: 'construction-proposal-fb900a9e-e9af-453d-a738-2892f86a4903',
      requestId: 'construction-proposal-f901ead8-28ad-4eba-b8d9-d8ecc70421f6',
      status: 200,
      previewStatus: 'READY',
      outputId: 'out_22c2a6b07c411065a0389d20',
      snapshotToken: 'sha256:d334a20d129ce3945acb0f1e5f8494c5d666e7770919e92abedd89ddc4451a0c',
      draftVersion: 4,
      draftDigest: 'sha256:6531c650dfebe410e10ea7101daa192de3a6545e05a84a447ef528d56314415f',
    },
    {
      validationRequestId: 'construction-proposal-e85f11fc-a0ce-45f9-bc8a-b344001c7f84',
      requestId: 'construction-proposal-fa2f8a0f-a574-4ca8-b3a1-4a206c9be524',
      status: 200,
      previewStatus: 'READY',
      outputId: 'out_22c2a6b07c411065a0389d20',
      snapshotToken: 'sha256:d334a20d129ce3945acb0f1e5f8494c5d666e7770919e92abedd89ddc4451a0c',
      draftVersion: 4,
      draftDigest: 'sha256:6531c650dfebe410e10ea7101daa192de3a6545e05a84a447ef528d56314415f',
    },
  ]);
  assert.deepEqual(result.sumRepairPairs.map(pair => [pair.startedAt > pair.validationCompletedAt, pair.previewReceiptId === pair.proposalId]), [
    [true, true],
    [true, true],
  ]);
  assert.deepEqual(result.sumRepairPairs.map(pair => pair.rowCount), [1, 1]);
  const repairs = fixture.authoringRequests.filter(request => request.status === 200);
  assert.deepEqual(repairs.map(request => ({
    projectionColumns: request.response.candidateConstruction.sourceProjections.map(projection => projection.columnId),
    previewColumns: request.response.preview.columns.map(column => column.column),
    previewRows: request.response.preview.rows,
  })), [
    {
      projectionColumns: [
        'pivot-input_4d186de2-b4b4-4421-b0f3-4088c91987b8',
        'pivot-input_6ab2db72-9e61-42cc-8e73-21fdcad2af73',
        'pivot-input_9ddd2203-33e2-442a-a49f-afc2290af9a7',
      ],
      previewColumns: ['status', 'missing_value', 'null_value', 'd'],
      previewRows: [{
        __loom_row_id: '["GROUPED_PIVOT","pivot_919622f5-ce3f-461d-bf17-db4cd2d9b40c",["STRING","final"]]',
        d: 6,
        missing_value: 3,
        null_value: 5,
        status: 'final',
      }],
    },
    {
      projectionColumns: [
        'pivot-input_0f59ce48-34e7-4854-98de-40d6a12a5001',
        'pivot-input_54383f9d-4197-4ef4-9519-2d6ac11e9e6a',
        'pivot-input_65b4136d-c7f4-45d9-8693-da1a39bd3851',
      ],
      previewColumns: ['status', 'missing_value', 'null_value', 'd'],
      previewRows: [{
        __loom_row_id: '["GROUPED_PIVOT","pivot_2067f5cd-0561-4859-a56e-ef434917a958",["STRING","final"]]',
        d: 6,
        missing_value: 3,
        null_value: 5,
        status: 'final',
      }],
    },
  ]);
});

test('CASE-016 binds native fixture responses through requestDetails.requestId and rejects wrong IDs or bodies', () => {
  const fixture = validationBatchFixture({ retainedCase016: true });
  const nativeRequests = fixture.fixtureNetwork.filter(entry => entry.kind === 'network');
  assert.equal(nativeRequests.length, 2);
  assert(nativeRequests.every(entry => !Object.hasOwn(entry, 'requestId')),
    'the fixture-native shape keeps the server request identity under requestDetails and the browser identity under playwrightRequestId');
  const result = classifyFixture(fixture);
  assert.deepEqual(result.fixtureRequestPairs.map(pair => pair.requestId), result.requestIDs);
  assert.deepEqual(result.fixtureRequestPairs.map(pair => pair.playwrightRequestId), ['request-121', 'request-126']);

  const wrongIdentity = validationBatchFixture({ retainedCase016: true });
  const wrongIdentityEntry = wrongIdentity.fixtureNetwork.find(entry => entry.kind === 'network');
  wrongIdentityEntry.requestId = wrongIdentity.validations[0].requestId;
  wrongIdentityEntry.requestDetails.requestId = 'construction-proposal-wrong-native-id';
  assert.throws(() => classifyFixture(wrongIdentity), /Fixture network request must bind to exact response/,
    'a correct-looking top-level decoy cannot substitute for the nested native request identity');

  const wrongBody = validationBatchFixture({ retainedCase016: true });
  wrongBody.fixtureNetwork.find(entry => entry.kind === 'network').responseBody.body = '{"error":"different response"}';
  assert.throws(() => classifyFixture(wrongBody), /Fixture response body must match the locally captured and validated proposal response/,
    'the exact native request ID still requires its exact completed response body');
});

test('captured request event order remains sufficient when native timestamps share a millisecond', () => {
  const fixture = validationBatchFixture();
  for (const request of fixture.authoringRequests) {
    request.startedAt = 500;
    request.responseReceivedAt = 500;
    request.completedAt = 500;
    for (const event of request.nativeEventChronology) event.observedAt = 500;
  }
  const result = classifyFixture(fixture);
  assert.equal(result.requestIDs.length, 2);
  assert.equal(result.sumRepairPairs.length, 2);

  const reorderedFixture = validationBatchFixture();
  for (const request of reorderedFixture.authoringRequests) {
    request.startedAt = 500;
    request.responseReceivedAt = 500;
    request.completedAt = 500;
    for (const event of request.nativeEventChronology) event.observedAt = 500;
  }
  reorderedFixture.authoringRequests[1].nativeEventChronology.reverse();
  assert.throws(() => classifyFixture(reorderedFixture), /preserve native request, response, and requestfinished array order/,
    'Equal timestamps must not make a reordered native event sequence look complete');
});

test('projected expected validation events retain raw evidence and finish without re-emitting failures', () => {
  const fixture = validationBatchFixture();
  const result = classifyFixture(fixture);
  const batchEvidence = { reason: 'exact scoped Pivot ERROR validation pair', requestIDs: result.requestIDs };
  const originalFixtureNetwork = structuredClone(fixture.fixtureNetwork);
  const originalFixtureErrors = structuredClone(fixture.fixtureErrors);
  const originalConsoleDiagnostics = structuredClone(fixture.fixtureDiagnostics.console);
  const nativeReport = {
    status: 'running',
    requiredChecks: ['exact Pivot validation batch is accepted'],
    assertions: [{ dimension: 'correctness', name: 'exact Pivot validation batch is accepted', status: 'passed', evidence: {} }],
    dimensions: { correctness: { status: 'passed', evidence: [] } },
    network: fixture.fixtureNetwork,
    errors: fixture.fixtureErrors,
  };
  const rawNetworkArray = nativeReport.network;
  const rawErrorArray = nativeReport.errors;
  const requestEvidence = markRootQuantityPivotValidationBatchExpected({
    validationBatch: result,
    batchEvidence,
    workflowErrors: fixture.workflowErrors,
    nativeReport,
    browserConsoleDiagnostics: fixture.fixtureDiagnostics.console,
  });

  assert.equal(requestEvidence.size, 2);
  assert.equal(nativeReport.network, rawNetworkArray, 'Classification must retain the original fixture network array');
  assert.equal(nativeReport.errors, rawErrorArray, 'Classification must retain the original fixture error array');
  assert.equal(nativeReport.network.length, 4, 'Both HTTP responses and both raw console diagnostics remain in the network ledger');
  assert.equal(nativeReport.network.filter(entry => entry.kind === 'network' && entry.status === 422 && entry.expectedHttpFailure).length, 2);
  assert.equal(nativeReport.network.filter(entry => entry.kind === 'console-error' && entry.expectedHttpFailure).length, 2);
  assert.equal(fixture.fixtureDiagnostics.console.filter(entry => entry.expectedHttpFailure).length, 2);
  assert.equal(nativeReport.errors.filter(entry => entry.expectedHttpFailure).length, 2,
    'The projected generic console and deduplicated HTTP records carry the exact batch proof');
  const stripClassification = entry => {
    const { expected, expectedHttpFailure, expectedRootQuantityPivotValidation, expectedHttpFailureBatch, ...raw } = entry;
    return raw;
  };
  assert.deepEqual(nativeReport.network.map(stripClassification), originalFixtureNetwork);
  assert.deepEqual(nativeReport.errors.map(stripClassification), originalFixtureErrors);
  assert.deepEqual(fixture.fixtureDiagnostics.console.map(stripClassification), originalConsoleDiagnostics);
  assert.equal(nativeReport.errors.filter(entry => entry.kind === 'console').length, 1);
  assert.equal(nativeReport.errors.filter(entry => entry.kind === 'http' && entry.status === 422).length, 1);
  assert.equal(nativeReport.errors.filter(entry => entry.kind === 'console-error').length, 0);

  finishCdaReport(nativeReport);
  assert.equal(nativeReport.status, 'passed', 'The actual CDA finishReport path must not re-emit the proven expected network events');
  assert.equal(nativeReport.network.length, 4, 'finishCdaReport must restore retained raw network evidence after status calculation');
  assert.deepEqual(nativeReport.network.map(stripClassification), originalFixtureNetwork);
  assert.deepEqual(nativeReport.errors.map(stripClassification), originalFixtureErrors);
});

test('finishReport still fails on an unrelated unexpected network error after the exact expected batch is classified', () => {
  const fixture = validationBatchFixture();
  const result = classifyFixture(fixture);
  const nativeReport = {
    status: 'running',
    requiredChecks: ['exact Pivot validation batch is accepted'],
    assertions: [{ dimension: 'correctness', name: 'exact Pivot validation batch is accepted', status: 'passed', evidence: {} }],
    dimensions: { correctness: { status: 'passed', evidence: [] } },
    network: fixture.fixtureNetwork,
    errors: fixture.fixtureErrors,
  };
  markRootQuantityPivotValidationBatchExpected({
    validationBatch: result,
    batchEvidence: { reason: 'exact scoped Pivot ERROR validation pair', requestIDs: result.requestIDs },
    workflowErrors: fixture.workflowErrors,
    nativeReport,
    browserConsoleDiagnostics: fixture.fixtureDiagnostics.console,
  });
  nativeReport.network.push({ kind: 'network', method: 'GET', status: 500, url: 'http://127.0.0.1:30008/api/v1/unrelated' });
  finishCdaReport(nativeReport);
  assert.equal(nativeReport.status, 'failed', 'An unrelated network failure must remain fatal after batch classification');
  assert(nativeReport.assertions.some(entry => entry.name === 'no unexpected network, module, or browser errors' && entry.status === 'failed'));

  const consoleFixture = validationBatchFixture();
  const consoleBatch = classifyFixture(consoleFixture);
  markRootQuantityPivotValidationBatchExpected({
    validationBatch: consoleBatch,
    batchEvidence: { reason: 'exact scoped Pivot ERROR validation pair', requestIDs: consoleBatch.requestIDs },
    workflowErrors: consoleFixture.workflowErrors,
    nativeReport: {
      status: 'running',
      requiredChecks: [],
      assertions: [],
      network: consoleFixture.fixtureNetwork,
      errors: consoleFixture.fixtureErrors,
    },
    browserConsoleDiagnostics: consoleFixture.fixtureDiagnostics.console,
  });
  const unrelatedConsole = { kind: 'console', message: 'Unexpected browser console error' };
  consoleFixture.workflowErrors.push(unrelatedConsole);
  assert.deepEqual(unexpectedRootQuantityPivotConsoleErrors(consoleFixture.workflowErrors), [unrelatedConsole],
    'The production error projection must suppress only the tagged expected 422 console events');
});

test('expected root quantity validation console batch rejects route, response, CAS, policy, and witness mismatches', () => {
  const mutations = [
    [fixture => { fixture.authoringRequests[2].path += '/other'; }, /exact owned proposal route/],
    [fixture => { fixture.authoringRequests[2].response.error.diagnostic.requestId = 'unrelated-request'; }, /exact proposal request/],
    [fixture => { fixture.authoringRequests[2].body.expectedDraftDigest = 'sha256:stale'; }, /draft digest/],
    [fixture => { fixture.authoringRequests[2].body.candidateConstruction.steps[0].operation.pivot.duplicatePolicy = 'SUM'; }, /duplicate policy/],
    [fixture => { fixture.validations[1].rawDuplicateBucket.sum += 1; }, /raw duplicate bucket/],
  ];
  for (const [mutate, message] of mutations) {
    const fixture = validationBatchFixture();
    mutate(fixture);
    assert.throws(() => classifyFixture(fixture), message);
  }
});

test('root quantity validation batch rejects generic 422s, mismatched SUM bindings, and incomplete repair captures', () => {
  const mutations = [
    [fixture => {
      const extra = structuredClone(fixture.authoringRequests[0]);
      extra.requestId = 'unrelated-generic-422';
      extra.serverRequestId = extra.requestId;
      fixture.authoringRequests.push(extra);
    }, /only failed native proposal requests/],
    [fixture => { fixture.authoringRequests[0].response.error.code = 'UNRELATED_422'; }, /only TABLE_PIVOT_CELL_CARDINALITY/],
    [fixture => { fixture.authoringRequests[1].path = fixture.authoringRequests[1].path.replace('root-quantity-category-', 'other-explorer-'); }, /exact owned project and Explorer route/],
    [fixture => { fixture.authoringRequests[1].body.outputId = 'out_other'; }, /SUM repair must preserve the rejected proposal bindings/],
    [fixture => { fixture.authoringRequests[1].body.snapshotToken = 'sha256:other-snapshot'; }, /SUM repair must preserve the rejected proposal bindings/],
    [fixture => { fixture.authoringRequests[1].body.pivotSources[0].columnId = 'other-column'; }, /SUM repair must preserve the rejected proposal bindings/],
    [fixture => { fixture.authoringRequests[1].response.previewStatus = 'RUNNING'; }, /valid READY preview terminal/],
    [fixture => { fixture.authoringRequests[1].response.draftVersion += 1; }, /SUM repair response must use the saved draft version/],
    [fixture => { fixture.authoringRequests[1].response = undefined; }, /SUM repair must retain its completed response body/],
    [fixture => { fixture.authoringRequests[1].nativeEventChronology.pop(); }, /exactly one requestfinished event/],
    [fixture => { fixture.authoringRequests[0].nativeEventChronology.pop(); }, /exactly one requestfinished event/],
  ];
  for (const [mutate, message] of mutations) {
    const fixture = validationBatchFixture({ retainedCase016: true });
    mutate(fixture);
    assert.throws(() => classifyFixture(fixture), message);
  }
});

test('expected root quantity validation console batch rejects an extra matching console event in every ledger', () => {
  const mutations = [
    [fixture => fixture.workflowErrors.push({ ...fixture.workflowErrors.find(error => error.kind === 'console') }), /local browser capture must contain exactly two console events/],
    [fixture => fixture.fixtureNetwork.push({ ...fixture.fixtureNetwork.find(error => error.kind === 'console-error') }), /Fixture network diagnostics must contain exactly two matching console events/],
    [fixture => fixture.fixtureConsoleDiagnostics.push({ ...fixture.fixtureConsoleDiagnostics[0] }), /Fixture console diagnostics must retain exactly two matching console events/],
    [fixture => fixture.fixtureErrors.push({ ...fixture.fixtureErrors.find(error => error.kind === 'console') }), /Fixture error ledger must retain its single matching generic console event/],
    [fixture => fixture.fixtureErrors.push({ kind: 'console-error', text: 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)', location: fixture.origin + fixture.authoringRequests[0].path }), /Live fixture error ledger must not duplicate raw console diagnostics/],
    [fixture => { fixture.fixtureErrors.find(error => error.kind === 'console').location += '/other'; }, /Fixture error ledger must retain its single matching generic console event/],
    [fixture => { fixture.fixtureErrors.find(error => error.kind === 'http').status = 500; }, /Fixture error ledger must retain its single deduplicated matching HTTP response event/],
    [fixture => { fixture.fixtureErrors.find(error => error.kind === 'http').playwrightRequestId = 'cda-request-unrelated'; }, /deduplicated fixture HTTP projection must identify one of the two validated proposal requests/],
    [fixture => fixture.fixtureErrors.push({ ...fixture.fixtureErrors.find(error => error.kind === 'http') }), /Fixture error ledger must retain its single deduplicated matching HTTP response event/],
  ];
  for (const [mutate, message] of mutations) {
    const fixture = validationBatchFixture();
    mutate(fixture);
    assert.throws(() => classifyFixture(fixture), message);
  }
});
