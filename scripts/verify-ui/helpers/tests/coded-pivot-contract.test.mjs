import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { chromium } from 'playwright';
import { hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { DEFAULT_ACTION_TO_RENDER_BUDGET_MS, recordPivotActionToRender } from '../quantity-pivot-budget.mjs';
import {
  CODED_PIVOT_OBSERVATION_ID,
  codedPivotExpectedHeaderValuesFor,
  codedPivotFailureDomSnapshot,
  codedPivotFirstTableReady,
  codedPivotFixtureFor,
  codedPivotRemovalProposalReady,
  codedPivotRenderedValuesFor,
  codedPivotRestoredSourceRowVisible,
  codedPivotValuesFor,
} from '../coded-pivot-fixture.mjs';
import {
  codedPivotFirstFailureEvidenceFor,
  codedPivotSourceOptionsDiagnosticFor,
  summarizeCodedPivotNativeRequests,
} from '../coded-pivot-native-evidence.mjs';

const scenarioID = 'standalone-reshape-coded-pivot';
const source = (component, overrides = {}) => ({
  id: CODED_PIVOT_OBSERVATION_ID,
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  resourceType: 'Observation',
  component,
  ...overrides,
});
const codedComponent = (code, value) => ({
  code: { coding: [{ system: 'https://cda.readthedocs.io', code }] },
  ...value,
});

test('integer and string coded Pivot fixtures require the exact scoped Observation values', () => {
  const integer = codedPivotFixtureFor('integer');
  assert.deepEqual(integer.map(({ system, code, type, value }) => ({ system, code, type, value })), [{ system: 'https://cda.readthedocs.io', code: 'days_to_collection', type: 'integer', value: '162' }]);
  const rawArtifact = JSON.parse(readFileSync(new URL('./fixtures/coded-pivot-integer-raw-observation.json', import.meta.url), 'utf8'));
  assert.equal(rawArtifact.rowCount, 1);
  const observedInteger = { ...rawArtifact.rows[0], project: rawArtifact.scope.project, generation: rawArtifact.scope.generation };
  assert.deepEqual(codedPivotValuesFor(observedInteger, { mode: 'integer', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' }), [
    { code: 'days_to_collection', type: 'integer', value: '162' },
  ]);

  const strings = codedPivotFixtureFor('string');
  assert.deepEqual(strings.map(({ code, value }) => [code, value]), [
    ['specimen_type', 'analyte'],
    ['primary_disease_type', 'Ductal and lobular neoplasms'],
  ]);
  assert.deepEqual(codedPivotValuesFor(source([
    codedComponent('specimen_type', { valueString: 'analyte' }),
    codedComponent('primary_disease_type', { valueString: 'Ductal and lobular neoplasms' }),
  ]), { mode: 'string', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' }), [
    { code: 'specimen_type', type: 'string', value: 'analyte' },
    { code: 'primary_disease_type', type: 'string', value: 'Ductal and lobular neoplasms' },
  ]);
});

test('coded Pivot raw oracle rejects scope drift, duplicate codes, and wrong scalar types', () => {
  const options = { mode: 'integer', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
  const exact = codedComponent('days_to_collection', { valueInteger: 162 });
  assert.throws(() => codedPivotValuesFor(source([exact], { id: 'different-observation' }), options), /exact Observation fixture/);
  assert.throws(() => codedPivotValuesFor(source([exact], { resourceType: 'Patient' }), options), /exact Observation fixture/);
  assert.throws(() => codedPivotValuesFor(source([exact], { project: 'different-project' }), options), /project and generation/);
  assert.throws(() => codedPivotValuesFor(source([exact], { generation: 'other-generation' }), options), /project and generation/);
  assert.throws(() => codedPivotValuesFor(source([exact, exact]), options), /exactly one raw component/);
  assert.throws(() => codedPivotValuesFor(source([]), options), /exactly one raw component/);
  assert.throws(() => codedPivotValuesFor(source([codedComponent('days_to_collection', { valueInteger: '162' })]), options), /integer/);
  const rawArtifact = JSON.parse(readFileSync(new URL('./fixtures/coded-pivot-integer-raw-observation.json', import.meta.url), 'utf8'));
  const rawSource = () => ({ ...rawArtifact.rows[0], project: rawArtifact.scope.project, generation: rawArtifact.scope.generation });
  const wrongValue = rawSource();
  wrongValue.component[0].valueInteger = 163;
  assert.throws(() => codedPivotValuesFor(wrongValue, options), /must equal 162/);
  const missingValue = rawSource();
  delete missingValue.component[0].valueInteger;
  assert.throws(() => codedPivotValuesFor(missingValue, options), /must equal 162/);
  const quantityInsteadOfInteger = rawSource();
  delete quantityInsteadOfInteger.component[0].valueInteger;
  quantityInsteadOfInteger.component[0].valueQuantity = { value: 162 };
  assert.throws(() => codedPivotValuesFor(quantityInsteadOfInteger, options), /must equal 162/);
  assert.throws(() => codedPivotValuesFor(source([
    codedComponent('specimen_type', { valueString: 42 }),
    codedComponent('primary_disease_type', { valueString: 'Ductal and lobular neoplasms' }),
  ]), { ...options, mode: 'string' }), /string/);
  assert.throws(() => codedPivotFixtureFor('decimal'), /Unsupported coded Pivot fixture mode/);
});

test('rendered coded values bind each exact Coding to its persisted output header and cell', () => {
  const expected = codedPivotFixtureFor('string');
  const codedStep = {
    operation: { kind: 'CODED_PIVOT', codedPivot: { categories: [
      { system: expected[0].system, code: expected[0].code, outputColumnId: 'coded-specimen' },
      { system: expected[1].system, code: expected[1].code, outputColumnId: 'coded-disease' },
    ] } },
    outputs: [
      { id: 'coded-specimen', label: 'Specimen type' },
      { id: 'coded-disease', label: 'Primary disease type' },
    ],
  };
  const rendered = {
    headers: ['OBSERVATION ID', 'SPECIMEN TYPE', 'PRIMARY DISEASE TYPE'],
    rows: [[CODED_PIVOT_OBSERVATION_ID, 'analyte', 'Ductal and lobular neoplasms']],
  };
  assert.deepEqual(codedPivotExpectedHeaderValuesFor(codedStep, expected), [
    { system: expected[0].system, code: expected[0].code, outputColumnId: 'coded-specimen', label: 'Specimen type', value: 'analyte' },
    { system: expected[1].system, code: expected[1].code, outputColumnId: 'coded-disease', label: 'Primary disease type', value: 'Ductal and lobular neoplasms' },
  ]);
  assert.deepEqual(codedPivotRenderedValuesFor(rendered, codedStep, expected).map(({ system, code, label, value }) => ({ system, code, label, value })), [
    { system: expected[0].system, code: expected[0].code, label: 'Specimen type', value: 'analyte' },
    { system: expected[1].system, code: expected[1].code, label: 'Primary disease type', value: 'Ductal and lobular neoplasms' },
  ]);
  assert.throws(() => codedPivotRenderedValuesFor({ ...rendered, rows: [[CODED_PIVOT_OBSERVATION_ID, 'Ductal and lobular neoplasms', 'analyte']] }, codedStep, expected), /specimen_type value must be "analyte" under "Specimen type"/);
});

test('serialized browser predicates use their explicit ID arguments', () => {
  const calls = [];
  const browserRestorationPredicate = runInNewContext(`(${codedPivotRestoredSourceRowVisible.toString()})`, {
    document: {
      querySelector(selector) {
        calls.push(selector);
        return { innerText: `Observation ${CODED_PIVOT_OBSERVATION_ID}` };
      },
    },
  });
  assert.equal(browserRestorationPredicate({ id: CODED_PIVOT_OBSERVATION_ID }), true);
  assert.equal(browserRestorationPredicate({ id: 'different-observation' }), false);

  const browserRemovalPredicate = runInNewContext(`(${codedPivotRemovalProposalReady.toString()})`, {
    document: {
      querySelector(selector) {
        calls.push(selector);
        return { getAttribute: () => 'ready', innerText: `Restores ${CODED_PIVOT_OBSERVATION_ID}` };
      },
    },
  });
  assert.equal(browserRemovalPredicate({ id: CODED_PIVOT_OBSERVATION_ID }), true);
  assert.equal(browserRemovalPredicate({ id: 'different-observation' }), false);
  assert.deepEqual(calls, [
    '[data-testid="preview-table-scroll"]',
    '[data-testid="preview-table-scroll"]',
    '[data-testid="construction-proposal-panel"]',
    '[data-testid="construction-proposal-panel"]',
  ]);
});

test('first-table readiness predicate accepts the retained heading and enabled Observation choice in native Playwright', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const capturedPage = (heading = 'Build your first table', disabled = false) => `
    <main>
      <section>
        <h2>${heading}</h2>
        <p>Choose a populated record type. Loom will add its direct ID column and load a preview. The table name is optional.</p>
        <section aria-label="Choose row type">
          <button type="button" aria-label="Choose Observation rows" ${disabled ? 'disabled' : ''}>
            <span>Observation</span><span>815,261 authorized records</span>
          </button>
        </section>
      </section>
    </main>`;
  await page.route('http://coded-pivot.test/**', route => route.fulfill({
    contentType: 'text/html',
    body: '<!doctype html><html><body></body></html>',
  }));
  const baseURL = 'http://coded-pivot.test/?project=loom_dev_cda_fhir&explorer=owned-explorer&mode=builder';
  const selectedURL = `${baseURL}&selection=owned-selection`;

  for (const url of [baseURL, selectedURL]) {
    await page.goto(url);
    await page.setContent(capturedPage());
    assert.equal(page.url(), url);
    assert.equal(await page.evaluate(codedPivotFirstTableReady), true);
    assert.equal(await page.getByRole('button', { name: 'Choose Observation rows' }).isEnabled(), true);
  }

  await page.setContent(capturedPage('Build another table'));
  assert.equal(await page.evaluate(codedPivotFirstTableReady), false);

  await page.setContent(capturedPage('Build your first table', true));
  assert.equal(await page.evaluate(codedPivotFirstTableReady), false);
  assert.equal(await page.getByRole('button', { name: 'Choose Observation rows' }).isEnabled(), false);
});

test('coded Pivot failure evidence retains the first chooser DOM and exact matching source across cleanup', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const chooser = `
    <section aria-label="Coded values as columns">
      <label><input type="radio" name="coded-pivot-source" value="choice-integer" checked>
        <span><strong>Observation component values (integer)</strong>
        <span>Codes and their paired values on Observation records.</span></span>
      </label>
    </section>`;
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const explorerId = 'qa-coded-pivot-integer';
  const ownedPath = `/api/v1/projects/${project}/explorers/${explorerId}`;
  const request = {
    snapshotToken: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7', outputId: 'out_coded_pivot_source',
    resourceType: 'Observation', limit: 50,
  };
  const endpointPath = `${ownedPath}/authoring/v2/frame-source-options`;
  const sourceOption = {
    choiceId: 'choice-integer', title: 'Observation component values (integer)',
    description: 'Codes and their paired values on Observation records.', resourceType: 'Observation',
    sourcePath: 'Observation.component', bindingId: 'component-value', owningScope: 'resource',
    keyPath: 'Observation.component.code.coding.code', valuePath: 'Observation.component.valueInteger',
    logicalType: 'integer', exampleConcept: 'Days to collection', observedOccurrences: 1, route: [],
    forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'DEFAULT' }], defaultForm: 'VALUE',
  };
  const response = {
    snapshotToken: request.snapshotToken, outputId: request.outputId, complete: true, truncated: false,
    sources: [sourceOption],
  };
  await page.route(`${origin}/**`, async route => {
    if (new URL(route.request().url()).pathname === endpointPath) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(response) });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'text/html', body: `<!doctype html><html><body><main>${chooser}</main></body></html>` });
  });
  await page.goto(origin);
  const captureReport = { nativeRequests: [], errors: [] };
  const requestCapture = captureCDARequests(page, {
    apiOrigin: origin, ownedPathPrefix: ownedPath, report: captureReport, responsePaths: /frame-source-options/,
  });
  const fetchResponse = page.evaluate(async ({ path, body }) => {
    const result = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    return result.json();
  }, { path: endpointPath, body: request });
  const entry = await requestCapture.waitFor(candidate => candidate.path === endpointPath && candidate.method === 'POST' &&
    Array.isArray(requestCapture.rawResponseBody(candidate)?.sources), { timeoutMs: 5000 });
  assert.deepEqual(await fetchResponse, response);
  const capturedRequest = requestCapture.rawRequestBody(entry);
  const capturedResponse = requestCapture.rawResponseBody(entry);
  assert.deepEqual(capturedRequest, request);
  assert.deepEqual(capturedResponse, response);
  const captureOrder = [];
  const evidence = await codedPivotFirstFailureEvidenceFor({
    mode: 'integer', action: { label: 'Select integer coded source', startedAt: 10 },
    captureDom: async () => {
      captureOrder.push('dom');
      return page.evaluate(codedPivotFailureDomSnapshot, { mode: 'integer' });
    },
    captureSourceOptions: domSnapshot => {
      captureOrder.push('source-options');
      return codedPivotSourceOptionsDiagnosticFor(entry, capturedRequest, capturedResponse, {
        origin, project, generation: 'cda-fhir-v1', explorerId, outputId: request.outputId,
        snapshotToken: request.snapshotToken, mode: 'integer', domSnapshot,
      });
    },
  });

  assert.deepEqual(captureOrder, ['dom', 'source-options']);
  assert.ok(evidence.sourceOptions, evidence.sourceOptionsCaptureError);
  assert.equal(evidence.dom.codedPivotSectionPresent, true);
  assert.equal(evidence.dom.sourceControls[0].checked, true);
  assert.equal(evidence.dom.sourceControlCount, 1);
  assert.equal(evidence.dom.sourceControlsTruncated, false);
  assert.deepEqual(evidence.sourceOptions.candidateChoices, [sourceOption]);
  assert.deepEqual(evidence.sourceOptions.matchedChoiceIndexes, [0]);
  assert.equal(evidence.sourceOptions.target.generation, 'cda-fhir-v1');
  assert.match(evidence.sourceOptions.target.identitySource, /BuilderV2 catalog generation\/snapshot/);
  assert.match(evidence.sourceOptions.frameOwnership, /empty route, Observation resourceType/);
  assert.equal(evidence.sourceOptions.envelope.sourceCount, 1);

  await page.setContent('<!doctype html><html><body><h1>Build your first table</h1></body></html>');
  assert.equal(await page.evaluate(() => document.querySelector('section[aria-label="Coded values as columns"]') === null), true);
  assert.equal(evidence.dom.codedPivotSectionPresent, true, 'The captured action-boundary DOM must survive later cleanup navigation.');
  assert.deepEqual(evidence.sourceOptions.candidateChoices, [sourceOption]);
});

test('coded Pivot source-options diagnostics bind the owned frame and expose bounded-page truncation', () => {
  const origin = 'http://127.0.0.1:30008';
  const project = 'loom_dev_cda_fhir';
  const explorerId = 'qa-coded-pivot-integer';
  const outputId = 'out_coded_pivot_source';
  const snapshotToken = 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7';
  const path = `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/frame-source-options`;
  const request = { snapshotToken, outputId, resourceType: 'Observation', limit: 50 };
  const entry = { origin, path, method: 'POST', status: 200, completedAt: 10, body: request };
  const domSnapshot = { mode: 'integer', sourceControls: [{
    name: 'coded-pivot-source', labelText: 'Observation component values (integer) Codes and their paired values on Observation records.',
  }] };
  const option = (index, overrides = {}) => ({
    choiceId: `choice-${index}`, title: `Other numeric frame ${index}`, description: 'bounded diagnostic fixture',
    resourceType: 'Observation', sourcePath: 'Observation.component', bindingId: `binding-${index}`,
    owningScope: 'resource', keyPath: 'Observation.component.code.coding.code',
    valuePath: 'Observation.component.valueDecimal', logicalType: 'decimal', exampleConcept: 'other',
    observedOccurrences: 1, route: [],
    forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'DEFAULT' }], defaultForm: 'VALUE',
    ...overrides,
  });
  const matchingOption = option(50, {
    choiceId: 'choice-integer', title: 'Observation component values (integer)',
    description: 'Codes and their paired values on Observation records.',
    valuePath: 'Observation.component.valueInteger', logicalType: 'integer',
  });
  const response = {
    snapshotToken, outputId, complete: false, truncated: false, nextCursor: 'next-page-token',
    sources: [...Array.from({ length: 50 }, (_, index) => option(index, { description: 'x'.repeat(12_000) })), matchingOption],
  };
  const diagnostic = codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  });
  assert.equal(diagnostic.envelope.sourceCount, 51);
  assert.equal(diagnostic.envelope.sourceCountExceedsLimit, true);
  assert.equal(diagnostic.envelope.sourceCountExceedsDiagnosticEntryCap, true);
  assert.equal(diagnostic.envelope.complete, false);
  assert.equal(diagnostic.envelope.nextCursor, 'next-page-token');
  assert.equal(diagnostic.diagnosticTruncated, true);
  assert.deepEqual(diagnostic.candidateChoices, [matchingOption], 'A matching native choice remains available outside the preview cap.');
  assert.deepEqual(diagnostic.matchedChoiceIndexes, [0]);
  assert.ok(diagnostic.sourcePreview.length < 50);
  assert.ok(diagnostic.diagnosticBytes <= diagnostic.diagnosticByteCap);
  assert.ok(diagnostic.envelope.responseBytes > diagnostic.diagnosticByteCap);

  assert.throws(() => codedPivotSourceOptionsDiagnosticFor({ ...entry, path: `${path}/other` }, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin: 'http://127.0.0.1:8188', project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project: 'wrong-project', generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned endpoint/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: '', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot,
  }), /exact project, generation/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId: 'out_other', snapshotToken, mode: 'integer', domSnapshot,
  }), /exact owned output/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken: 'sha256:other', mode: 'integer', domSnapshot,
  }), /exact owned output/);
  assert.throws(() => codedPivotSourceOptionsDiagnosticFor(entry, request, response, {
    origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer',
    domSnapshot: { ...domSnapshot, mode: 'string' },
  }), /failure DOM must retain its mode/);

  for (const wrongFrame of [
    { ...matchingOption, route: [{ fromResourceType: 'Observation', toResourceType: 'Patient' }] },
    { ...matchingOption, resourceType: 'Patient' },
    { ...matchingOption, valuePath: 'Observation.component.valueString' },
  ]) {
    const wrongFrameDiagnostic = codedPivotSourceOptionsDiagnosticFor(entry, request, {
      ...response, sources: [wrongFrame], complete: true, nextCursor: undefined,
    }, { origin, project, generation: 'cda-fhir-v1', explorerId, outputId, snapshotToken, mode: 'integer', domSnapshot });
    assert.equal(wrongFrameDiagnostic.candidateChoiceCount, 0, 'Only the exact direct Observation mode frame belongs to this chooser contract.');
  }
});

test('native request evidence rejects pending, failed, and statusless requests', () => {
  assert.equal(summarizeCodedPivotNativeRequests([{ browserRequestId: '1', status: 200, completedAt: 10 }]).passed, true);

  const pending = summarizeCodedPivotNativeRequests([{ browserRequestId: '2', status: 200, responseReceivedAt: 5 }]);
  assert.equal(pending.pending.length, 1);
  assert.equal(pending.passed, false);

  const failed = summarizeCodedPivotNativeRequests([{ browserRequestId: '3', failure: 'net::ERR_ABORTED', completedAt: 12 }]);
  assert.equal(failed.pending.length, 0, 'An explicit request failure is terminal evidence.');
  assert.equal(failed.terminalFailures.length, 1, 'A terminal failure remains visible and blocks a clean pass.');
  assert.equal(failed.passed, false);

  assert.equal(summarizeCodedPivotNativeRequests([{ browserRequestId: '4', completedAt: 14 }]).passed, false);
});

test('integer and string native cases have separate registered four-dimension lifecycle contracts', () => {
  const scenario = registry.find(entry => entry.id === scenarioID);
  assert(scenario, 'The native coded Pivot scenario is registered.');
  assert.equal(scenario.script, 'verify-cda-coded-pivot.mjs');
  assert.ok(scenario.endpoints.includes('POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/construction-proposals'));
  assert.ok(scenario.endpoints.includes('POST /api/v1/projects/{project}/explorers/{explorer}/authoring/v2/commands'));

  for (const mode of ['integer', 'string']) {
    const caseName = `coded-pivot-${mode}`;
    const contract = scenarioCaseFor(scenario, caseName);
    assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-reshape.spec.mjs');
    assert.match(contract.playwrightGrep, new RegExp(`${caseName}\\$`));
    assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });
    assert.equal(contract.lifecycleEvidence.performance.check, contract.performanceCheckName);
    assert.equal(contract.lifecycleEvidence.performance.checkpointBudgetMs, DEFAULT_ACTION_TO_RENDER_BUDGET_MS);
    assert.equal(contract.requiredChecks.length, 9);
    assert.equal(contract.requiredChecks[0], 'Independent raw Observation oracle proves the exact project, generation, ID, Coding.system/code, and mode-specific scalar values');
    assert.match(contract.requiredChecks[3], /Cancel preserves the exact saved CODED_PIVOT and values/);
    assert.match(contract.requiredChecks[7], /five seconds/);
    assert.equal(contract.requiredChecks[8], 'No unexpected native requests or browser errors occurred');
  }

  const sourceText = readFileSync(new URL('../../workflows/verify-cda-coded-pivot.mjs', import.meta.url), 'utf8');
  const nativeEvidenceText = readFileSync(new URL('../coded-pivot-native-evidence.mjs', import.meta.url), 'utf8');
  for (const [index, dimension] of [
    [0, 'correctness'], [1, 'usability'], [2, 'correctness'], [3, 'persistence'],
    [4, 'persistence'], [5, 'persistence'], [6, 'persistence'], [7, 'performance'], [8, 'correctness'],
  ]) assert.match(sourceText, new RegExp(`recordCheck\\(${index}, '${dimension}'`));
  assert.match(sourceText, /scenarioCaseFor\('standalone-reshape-coded-pivot', `coded-pivot-\$\{mode\}`\)/);
  assert.match(sourceText, /construction-cancel-proposal/);
  assert.match(sourceText, /frame-source-options/);
  assert.match(sourceText, /semantic-inventory/);
  assert.match(sourceText, /sourceChoiceId, selectedSourceOption\.choiceId/);
  assert.match(sourceText, /recordPivotActionToRender\(/);
  assert.match(sourceText, /startedAt:\s*started/);
  assert.match(sourceText, /name:\s*`\$\{name\}-to-render`/);
  assert.match(sourceText, /codedPivotRenderedValuesFor\(/);
  assert.match(sourceText, /waitNative\(codedPivotRemovalProposalReady, \{ id: observationId \}/);
  assert.match(sourceText, /summarizeCodedPivotNativeRequests\(/);
  assert.match(sourceText, /report\.nativeRequestEvidence\.passed/);
  const firstFailureCapture = sourceText.indexOf('const firstFailureEvidence = await codedPivotFirstFailureEvidenceFor');
  assert(firstFailureCapture >= 0 && firstFailureCapture < sourceText.indexOf('} finally {', firstFailureCapture),
    'Failure-time DOM and source options must be captured before the workflow cleanup finally block.');
  assert.match(nativeEvidenceText, /Number\.isFinite\(request\.completedAt\)/);
  assert.match(nativeEvidenceText, /terminalFailures/);
  assert.match(nativeEvidenceText, /invalidStatuses/);
  assert.match(sourceText, /CODED_PIVOT/);
  assert.ok(sourceText.indexOf('const editStarted = Date.now();') < sourceText.indexOf("await select('Set missing value policy'"));
  assert.ok(sourceText.indexOf('const editRequestStart = report.nativeRequests.length;') < sourceText.indexOf("await select('Set missing value policy'"));
  assert.ok(sourceText.indexOf('const reapplyStarted = Date.now();') < sourceText.indexOf("await select('Set missing value policy after Cancel'"));
  assert.ok(sourceText.indexOf('const reapplyRequestStart = report.nativeRequests.length;') < sourceText.indexOf("await select('Set missing value policy after Cancel'"));
  assert.match(sourceText, /const editProposal = await requestCapture\.waitFor\([\s\S]*proposalUsesCodedPivot\(entry, \{ policy: 'ERROR'/);
  assert.match(sourceText, /const reapplyProposal = await requestCapture\.waitFor\([\s\S]*proposalUsesCodedPivot\(entry, \{ policy: 'ERROR'/);
  assert.match(sourceText, /codedPivotBindingsFor\(editedCodedStep\), report\.initialStepBindings/);
  assert.match(sourceText, /report\.cancelReloadAssociation, report\.outputAssociation/);

  const checkpoints = [];
  assert.deepEqual(recordPivotActionToRender({ cases: checkpoints, name: 'apply-to-render', startedAt: 100, finishedAt: 137,
    budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS }), { name: 'apply-to-render', durationMs: 37, budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS });
  assert.deepEqual(checkpoints, [{ name: 'apply-to-render', durationMs: 37, budgetMs: DEFAULT_ACTION_TO_RENDER_BUDGET_MS }]);
});

test('coverage maps only the exact integer and string coded forms and leaves other coded forms open', () => {
  const owner = registry.find(entry => entry.id === 'builder-authoring');
  const broadGap = owner.coverage.find(entry => entry.feature === 'coded Pivot');
  assert.equal(broadGap.acceptance.kind, 'unmapped');
  assert.match(broadGap.acceptance.unmappedReason, /Other source resource types/);

  for (const [feature, caseName] of [
    ['integer coded Pivot columns', 'coded-pivot-integer'],
    ['string coded Pivot columns', 'coded-pivot-string'],
  ]) {
    const coverage = registry.find(entry => entry.id === scenarioID).coverage.find(entry => entry.feature === `native ${feature === 'integer coded Pivot columns' ? 'integer' : 'string'} coded Pivot values on one exact CDA Observation`);
    assert.equal(coverage.status, 'untested');
    assert.equal(coverage.acceptance.kind, 'lifecycle');
    assert.equal(coverage.acceptance.case, caseName);
    assert.equal(hasLifecycleContract(coverage, registry.find(entry => entry.id === scenarioID)), true);
  }
});

test('both native coded forms join the exact scenario while retaining distinct Playwright case names', () => {
  const spec = readFileSync(new URL('../../specs/standalone-reshape.spec.mjs', import.meta.url), 'utf8');
  const codedCaseNames = [...spec.matchAll(/register\('coded-pivot-([^']+)'/g)].map(([, mode]) => `coded-pivot-${mode}`);
  assert.deepEqual(codedCaseNames, ['coded-pivot-integer', 'coded-pivot-string']);

  const codedRegistrations = [...spec.matchAll(/register\('coded-pivot-(integer|string)',\s*runCodedPivotWorkflow,\s*\{\s*mode:\s*'(integer|string)'\s*\},\s*\{\s*cdaScenarioID:\s*'standalone-reshape-coded-pivot'\s*,?\s*\}\s*\);/g)]
    .map(([, caseMode, workflowMode]) => [caseMode, workflowMode]);
  assert.deepEqual(codedRegistrations, [['integer', 'integer'], ['string', 'string']],
    'Each explicit native case must bind its matching fixture mode to the shared coded Pivot scenario.');
});
