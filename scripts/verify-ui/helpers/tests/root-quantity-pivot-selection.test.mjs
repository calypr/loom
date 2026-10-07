import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { includeBrowserDiagnostics } from '../cda-playwright.mjs';
import { finishCdaReport } from '../cda-fixtures.mjs';
import {
  classifyRootQuantityPivotValidationConsoleBatch,
  markRootQuantityPivotValidationBatchExpected,
  pivotSourceSelectionReady,
} from '../../workflows/root-quantity-pivot-workflow.mjs';

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

const validationBatchFixture = () => {
  const project = 'loom_dev_cda_fhir';
  const explorer = 'root-quantity-category-test';
  const origin = 'http://127.0.0.1:30008';
  const outputId = 'out_root_quantity';
  const route = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const url = origin + route;
  const snapshotToken = 'sha256:test-snapshot';
  const draftDigest = 'sha256:test-draft';
  const initialDraft = { snapshotToken, draftVersion: 4, draftDigest };
  const duplicateWitness = { status: 'final', present: true, value: 'd', rowCount: 100, numericCount: 100, valueSum: 250, valueMax: 5 };
  const rawDuplicateBucket = {
    status: duplicateWitness.status,
    category: JSON.stringify({ kind: 'STRING', string: duplicateWitness.value }),
    rowCount: duplicateWitness.rowCount,
    numericCount: duplicateWitness.numericCount,
    sum: duplicateWitness.valueSum,
    max: duplicateWitness.valueMax,
  };
  const consoleMessage = 'Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)';
  const ids = [
    'construction-proposal-first',
    'construction-proposal-second',
  ];
  const validated = ids.map((requestId, index) => {
    const browserRequestId = `playwright-${index + 10}`;
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
      changedStepId: `pivot-step-${index}`,
      candidateConstruction: {
        version: 1,
        steps: [{
          id: `pivot-step-${index}`,
          operation: {
            kind: 'PIVOT',
            pivot: {
              groupKeyIds: ['group-column'],
              categoryColumnId: 'category-column',
              valueColumnId: 'value-column',
              categories: [{ key: { kind: 'STRING', string: 'd' }, outputColumnId: 'output-d' }],
              duplicatePolicy: 'ERROR',
            },
          },
        }],
      },
      pivotSources: [
        { choiceId: `choice-group-${index}`, columnId: 'group-column' },
        { choiceId: `choice-category-${index}`, columnId: 'category-column' },
        { choiceId: `choice-value-${index}`, columnId: 'value-column' },
      ],
    };
    const request = {
      requestId,
      browserRequestId,
      endpoint: 'construction-proposals',
      path: route,
      origin,
      method: 'POST',
      startedAt: 100 + index * 100,
      responseReceivedAt: 110 + index * 100,
      completedAt: 120 + index * 100,
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
      visibleRepair: {
        alert: diagnostic.message,
        policy: 'ERROR',
        summary: 'Duplicate values: stop the pivot with an error.',
        sumOption: { label: 'Add them together', disabled: false },
      },
      classification: 'expected-domain-validation-repaired-by-user-selected-SUM',
    };
    const fixturePlaywrightRequestId = `cda-request-${index + 121}`;
    const fixtureNetworkRequest = {
      kind: 'network',
      status: 422,
      method: 'POST',
      url,
      requestDetails: { requestId, draftVersion: 4, draftDigest, outputId },
      requestId,
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
    authoringRequests: validated.map(item => item.request),
    workflowErrors: validated.flatMap(item => [item.workflowConsole, item.workflowHTTP]),
    fixtureNetwork: validated.flatMap(item => [item.fixtureNetworkRequest, item.consoleError]),
    fixtureErrors,
    fixtureDiagnostics,
    fixtureConsoleDiagnostics: validated.map(item => item.fixtureConsoleDiagnostic),
  };
};

const classifyFixture = fixture => classifyRootQuantityPivotValidationConsoleBatch(fixture);

test('expected root quantity validation console batch matches two exact ERROR proposals and raw witness', () => {
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
  assert.deepEqual(result.fixtureRequestPairs.map(({ browserRequestId, playwrightRequestId, networkIndex }) => ({ browserRequestId, playwrightRequestId, networkIndex })), [
    { browserRequestId: 'playwright-10', playwrightRequestId: 'cda-request-121', networkIndex: 0 },
    { browserRequestId: 'playwright-11', playwrightRequestId: 'cda-request-122', networkIndex: 2 },
  ]);
  assert.match(result.association, /console events have no request IDs/);
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
});

test('expected root quantity validation console batch rejects route, response, CAS, policy, and witness mismatches', () => {
  const mutations = [
    [fixture => { fixture.authoringRequests[1].path += '/other'; }, /exact owned proposal route/],
    [fixture => { fixture.authoringRequests[1].response.error.diagnostic.requestId = 'unrelated-request'; }, /exact proposal request/],
    [fixture => { fixture.authoringRequests[1].body.expectedDraftDigest = 'sha256:stale'; }, /draft digest/],
    [fixture => { fixture.authoringRequests[1].body.candidateConstruction.steps[0].operation.pivot.duplicatePolicy = 'SUM'; }, /duplicate policy/],
    [fixture => { fixture.validations[1].rawDuplicateBucket.sum += 1; }, /raw duplicate bucket/],
  ];
  for (const [mutate, message] of mutations) {
    const fixture = validationBatchFixture();
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
