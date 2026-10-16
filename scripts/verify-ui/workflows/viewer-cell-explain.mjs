import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
import { isDeepStrictEqual } from 'node:util';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientCellExplainOracle } from '../helpers/viewer-cell-explain-oracle.mjs';
import { browserURL } from './builder-url.mjs';

const outputId = 'patients';
const explorerId = 'default';
const outputColumns = ['patient_id', 'gender'];
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;
const builderStatePath = (project, explorer) =>
  `${runtimePath(project, explorer)}/authoring/v2/builder`;
const cellTracePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/cell-trace`;

const workspaceForCellExplain = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Basic Patient cell explanation' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients', rowLabel: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [
      {
        columnId: 'patient-id',
        column: 'patient_id',
        label: 'Patient ID',
        logicalType: 'string',
        occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
        table: { visible: true, order: 0 },
      },
      {
        columnId: 'patient-gender',
        column: 'gender',
        label: 'Gender',
        logicalType: 'string',
        occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } },
        table: { visible: true, order: 1 },
      },
    ],
  }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const matchesSelector = (actual, expected) => actual?.recipe === expected?.recipe &&
  actual?.translationVersion === expected?.translationVersion && actual?.output === expected?.output;

const matchesGraphRequest = (request, { origin, project, selector }) => {
  try {
    const url = new URL(request.url());
    const input = request.postDataJSON()?.variables?.input;
    return request.method() === 'POST' && url.origin === origin && url.pathname === '/graphql/graph' &&
      input?.projectId === project && input.selector?.output === outputId &&
      Array.isArray(input.columns) && input.columns.join('\u0000') === outputColumns.join('\u0000') &&
      (!selector || matchesSelector(input.selector, selector));
  } catch { return false; }
};

const capture = promise => promise.then(value => ({ value }), error => ({ error }));
const requireCapture = async (captured, label) => {
  const result = await captured;
  assert(result.value, `${label} did not complete successfully: ${result.error?.message ?? 'no response'}`);
  return result.value;
};

const rowsFromConnection = connection => {
  const columns = Array.isArray(connection?.columns) ? connection.columns : [];
  const rows = Array.isArray(connection?.rows) ? connection.rows : [];
  assert.deepEqual(columns, outputColumns, 'Viewer query must request the configured patient_id and gender output columns');
  const rowIDs = Array.isArray(connection?.rowIds) ? connection.rowIds : [];
  assert.equal(rowIDs.length, rows.length, 'Viewer query must return one row identity for each output row');
  assert.equal(new Set(rowIDs).size, rowIDs.length, 'Viewer output row identities must be distinct');
  assert(rowIDs.every(rowID => typeof rowID === 'string' && rowID.length > 0), 'Viewer output row identities must be non-empty strings');
  return rows.map((row, index) => {
    const valueAt = column => Array.isArray(row) ? row[columns.indexOf(column)] : row?.[column];
    return { rowId: rowIDs[index], patientID: valueAt('patient_id'), gender: valueAt('gender') };
  });
};

const expectedOutputRows = oracle => oracle.patients.map(patient => ({
  patientID: patient.id,
  gender: patient.genderPresent ? patient.gender : null,
}));

const remainingBudgetMs = started => Math.max(1, Math.floor(5_000 - (performance.now() - started)));

const waitForRenderedRows = (page, tableRows, started) => page.waitForFunction(expected => {
  const actual = [...document.querySelectorAll('table[aria-label="Patients results"] tbody tr')].map(row =>
    [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
  return actual.length === expected.length && JSON.stringify(actual) === JSON.stringify(expected);
}, tableRows, { timeout: remainingBudgetMs(started) });

const qualityReportFor = (runtime, output) => {
  const matches = (runtime?.qualityReports ?? []).filter(report =>
    report.output === output.outputId || report.output === output.name);
  assert.equal(matches.length, 1, 'published Viewer output must have one matching receipt-bound quality report');
  const [report] = matches;
  assert.equal(typeof report.receiptId, 'string');
  assert(report.receiptId.length > 0, 'quality report must identify the immutable compilation receipt');
  assert.equal(typeof report.scopeDigest, 'string');
  assert(report.scopeDigest.length > 0, 'quality report must retain its publication scope digest');
  return report;
};

const inspectRuntime = (entry, { canonicalProject, generation, outputSelector }) => {
  assert(entry.status >= 200 && entry.status < 300, `Viewer runtime returned HTTP ${entry.status}`);
  const envelope = entry.response;
  const runtime = envelope?.runtime;
  const output = runtime?.outputs?.find(candidate => candidate.outputId === outputId);
  assert.equal(envelope?.project, canonicalProject, 'Viewer runtime must belong to the exact fixture project');
  assert.equal(envelope?.explorerId, explorerId, 'Viewer runtime must belong to the selected default Explorer');
  assert.equal(runtime?.generation ?? runtime?.publication?.generation, generation, 'Viewer runtime must retain the exact fixture generation');
  assert(output, 'published Viewer runtime must contain the configured Patient output');
  if (outputSelector) assert(matchesSelector(output.selector, outputSelector), 'Viewer reload must retain the exact Patient output selector');
  const qualityReport = qualityReportFor(runtime, output);
  return { envelope, runtime, output, qualityReport };
};

const assertOutputRows = (entries, oracle, check, phase) => {
  const expected = expectedOutputRows(oracle);
  const actual = entries.map(({ patientID, gender }) => ({ patientID, gender: gender ?? null }));
  const byID = values => [...values].sort((left, right) => String(left.patientID).localeCompare(String(right.patientID)));
  assert(isDeepStrictEqual(byID(actual), byID(expected)), `${phase} Viewer response must match the literal Patient gender values and missing field`);
  check('correctness', `${phase} Viewer rows match the exact independent Patient IDs and Gender values`, true, {
    patientIDs: entries.map(entry => entry.patientID),
    expectedPatientIDs: expected.map(entry => entry.patientID),
    genderByPatient: byID(actual),
    expectedGenderByPatient: byID(expected),
    patientPath: oracle.patientPath,
    patientSHA256: oracle.patientSHA256,
  });
};

export const authorizationScopeDigestFromBuilder = (builder, generation) => {
  const catalog = builder?.catalog;
  assert.equal(catalog?.generation, generation, 'Builder catalog must use the exact fixture generation');
  assert.equal(typeof catalog?.snapshotToken, 'string', 'Builder catalog must identify the authorized capability snapshot');
  assert(catalog.snapshotToken.length > 0, 'Builder catalog must identify the authorized capability snapshot');
  assert.equal(typeof catalog?.authorizationScopeDigest, 'string', 'Builder catalog must expose its authorization scope digest');
  assert(catalog.authorizationScopeDigest.length > 0, 'Builder catalog must expose its authorization scope digest');
  return catalog.authorizationScopeDigest;
};

export const assertCellTraceBinding = ({ entry, canonicalProject, explorer, generation, qualityReport,
  authorizationScopeDigest, outputIdForTrace, rowId }) => {
  assert(entry, 'cell explanation must issue a captured receipt-bound trace request');
  assert.equal(entry.status >= 200 && entry.status < 300, true, `cell trace returned HTTP ${entry.status}`);
  assert.equal(typeof authorizationScopeDigest, 'string');
  assert(authorizationScopeDigest.length > 0, 'cell trace must bind the receipt authorization scope');
  const request = entry.request;
  assert.deepEqual(Object.keys(request ?? {}).sort(), ['column', 'limit', 'outputId', 'receiptId', 'rowId'].sort(),
    'cell trace request must contain only the selected receipt, output, row, column, and page limit');
  assert.deepEqual(request, {
    receiptId: qualityReport.receiptId,
    outputId: outputIdForTrace,
    rowId,
    column: 'gender',
    limit: 25,
  }, 'cell trace request must bind the exact current receipt and selected output cell');
  const response = entry.response;
  assert.deepEqual(response?.binding, {
    receiptId: qualityReport.receiptId,
    outputId: outputIdForTrace,
    project: canonicalProject,
    explorerId: explorer,
    generation,
    scopeDigest: authorizationScopeDigest,
  }, 'cell trace response must bind evidence to the current receipt authorization scope, project, Explorer, and generation');
  const feature = response?.feature;
  assert.equal(feature?.outputId, outputIdForTrace);
  assert.equal(feature?.column, 'gender');
  assert.equal(feature?.authoredColumn, 'gender');
  assert.equal(feature?.occurrenceId, 'base');
  assert.equal(feature?.label, 'Gender');
  assert.equal(feature?.logicalType, 'string');
  assert.equal(feature?.sourceResourceType, 'Patient');
  assert.equal(feature?.sourcePath, 'gender');
  assert.equal(feature?.projectionMode, 'VALUE');
  assert.equal(feature?.lossless, true);
  assert.deepEqual(feature?.lossReasons, []);
  const trace = response?.trace;
  assert.equal(trace?.status, 'VALUE');
  assert.equal(trace?.rowId, rowId);
  assert.equal(trace?.column, 'gender');
  assert.equal(trace?.value, 'female');
  assert.equal(trace?.complete, true);
  assert.equal(trace?.hasMore, false);
  assert.deepEqual(trace?.contributions, [{ resourceType: 'Patient', resourceId: 'dev-patient-001', value: 'female' }],
    'cell trace must identify the exact independent Patient source record and literal value');
  return response;
};

export const viewerCellExplainWorkflow = async ({ page, report, action, check, networkObserver }, context) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const oracle = readBasicPatientCellExplainOracle(target.fixtureDir);
  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: target.sourceRoot, encoding: 'utf8' }).trim();
  report.target.project = project;
  report.target.fixtureProjectAlias = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorerId;
  report.target.viewerOutputId = outputId;
  report.target.fixtureOracle = {
    patientPath: oracle.patientPath,
    patientSHA256: oracle.patientSHA256,
    patients: oracle.patients,
    explainedPatient: oracle.explainedPatient,
  };

  const configPath = repositoryConfigPath(project, generation);
  const configured = await fetch(`${target.apiUrl}${configPath}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Loom-Source-Commit': sourceCommit },
    body: JSON.stringify(workspaceForCellExplain()),
    signal: AbortSignal.timeout(30_000),
  });
  const configuredText = await configured.text();
  let configuredBody;
  try { configuredBody = JSON.parse(configuredText); }
  catch { configuredBody = { body: configuredText.slice(0, 1_000) }; }
  const configuredCorrectly = configured.ok && configuredBody.project === canonicalProject &&
    configuredBody.generation === generation && configuredBody.explorerId === explorerId && configuredBody.activated === true;
  check('correctness', 'Basic Patient output is configured on the exact fixture generation', configuredCorrectly,
    { status: configured.status, path: configPath, project: configuredBody.project, expectedProject: canonicalProject,
      generation: configuredBody.generation, expectedGeneration: generation, explorer: configuredBody.explorerId, sourceCommit });
  assert(configuredCorrectly, 'repository config must publish the exact receipt-bound Patient output');

  const origin = new URL(target.uiUrl).origin;
  const outputRuntimePath = runtimePath(project, explorerId);
  report.target.viewerOutputId = outputId;
  const runtimeStart = networkObserver.requests.length;
  const initialRenderStarted = performance.now();
  const initialRuntimeCapture = capture(networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === outputRuntimePath,
  { fromIndex: runtimeStart, timeoutMs: remainingBudgetMs(initialRenderStarted) }));
  const initialGraphCapture = capture(page.waitForResponse(response =>
    matchesGraphRequest(response.request(), { origin, project }), { timeout: remainingBudgetMs(initialRenderStarted) }));
  await page.goto(browserURL(target, project, explorerId, 'viewer'), {
    waitUntil: 'domcontentloaded', timeout: remainingBudgetMs(initialRenderStarted),
  });
  const [initialRuntimeEntry, initialGraphResponse] = await Promise.all([
    requireCapture(initialRuntimeCapture, 'initial Viewer runtime'),
    requireCapture(initialGraphCapture, 'initial Patient output query'),
  ]);
  const { runtime, output, qualityReport } = inspectRuntime(initialRuntimeEntry, {
    canonicalProject, generation,
  });
  const authoringStateResponse = await fetch(`${target.apiUrl}${builderStatePath(project, explorerId)}`, {
    headers: { 'X-Loom-Source-Commit': sourceCommit },
    signal: AbortSignal.timeout(30_000),
  });
  const authoringStateText = await authoringStateResponse.text();
  let authoringState;
  try { authoringState = JSON.parse(authoringStateText); }
  catch { authoringState = { body: authoringStateText.slice(0, 1_000) }; }
  assert(authoringStateResponse.ok,
    `read-only Builder catalog returned HTTP ${authoringStateResponse.status}: ${JSON.stringify(authoringState).slice(0, 800)}`);
  const authorizationScopeDigest = authorizationScopeDigestFromBuilder(authoringState, generation);
  report.target.cellTraceScope = {
    source: 'read-only Builder catalog for the exact project and Explorer',
    generation,
    authorizationScopeDigest,
    publicationScopeDigest: qualityReport.scopeDigest,
  };
  check('correctness', 'Viewer runtime exposes the exact Patients receipt for cell explanation', true,
    { outputId: output.outputId, receiptId: qualityReport.receiptId, generation,
      publicationScopeDigest: qualityReport.scopeDigest, authorizationScopeDigest });
  assert.equal(typeof output.selector?.recipe, 'string');
  assert(output.selector.recipe.length > 0, 'Viewer output selector must identify its recipe');
  assert.equal(typeof output.selector?.translationVersion, 'string');
  assert(output.selector.translationVersion.length > 0, 'Viewer output selector must identify its translation version');
  assert.equal(output.selector?.output, outputId, 'Viewer output selector must target the configured Patient output');
  const graphStatus = initialGraphResponse.status();
  const graphBody = await initialGraphResponse.json();
  const graphErrors = Array.isArray(graphBody.errors) ? graphBody.errors : [];
  const initialConnection = graphBody.data?.dataframeRows;
  assert(graphStatus >= 200 && graphStatus < 300, `initial Patient output query returned HTTP ${graphStatus}`);
  assert.deepEqual(graphErrors, [], 'initial Patient output query must not return GraphQL errors');
  assert(matchesGraphRequest(initialGraphResponse.request(), { origin, project, selector: output.selector }),
    'initial Patient output query must use the current project and published output selector');
  check('correctness', 'initial Viewer query uses the exact project and receipt-selected output', true,
    { project, selector: output.selector, columns: outputColumns, graphErrors: graphErrors.length });
  const initialRows = rowsFromConnection(initialConnection);
  assertOutputRows(initialRows, oracle, check, 'initial');
  const femaleIndex = initialRows.findIndex(row => row.patientID === oracle.explainedPatient.id);
  assert(femaleIndex >= 0, 'the independent female Patient must exist in the rendered output');
  const initialRowId = initialRows[femaleIndex].rowId;
  const table = page.getByRole('table', { name: 'Patients results', exact: true });
  await expect(table).toBeVisible({ timeout: remainingBudgetMs(initialRenderStarted) });
  const expectedRenderedRows = expectedOutputRows(oracle).map(row => [row.patientID, row.gender ?? '—']);
  const graphOrderedRows = initialRows.map(row => [row.patientID, row.gender ?? '—']);
  await waitForRenderedRows(page, graphOrderedRows, initialRenderStarted);
  const initialRenderMs = Math.round(performance.now() - initialRenderStarted);
  const initialDOMRows = await table.locator('tbody tr').evaluateAll(rows => rows.map(row =>
    [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())));
  assert.deepEqual(initialDOMRows.map(row => row[0]), initialRows.map(row => row.patientID),
    'rendered Viewer rows must keep the GraphQL response order that binds each rowId to its cell');
  assert.deepEqual([...initialDOMRows].sort((left, right) => left[0].localeCompare(right[0])),
    [...expectedRenderedRows].sort((left, right) => left[0].localeCompare(right[0])),
  'Viewer must render the raw female value and display the absent Gender field as empty');
  assert(initialRenderMs <= 5_000, `initial Viewer output took ${initialRenderMs}ms to render the exact fixture rows`);
  check('performance', 'Viewer initial page renders exact Patient values within five seconds', true,
    { elapsedMs: initialRenderMs, budgetMs: 5_000, rows: initialDOMRows });
  const dialog = page.getByRole('dialog', { name: 'Why is Gender female?', exact: true });

  const explainCell = async (phase, entries, quality, expectedRowID) => {
    const rowIndex = entries.findIndex(entry => entry.rowId === expectedRowID);
    assert(rowIndex >= 0, `${phase} Explain row identity must exist in the corresponding query response`);
    const patient = table.locator('tbody tr').filter({ hasText: oracle.explainedPatient.id });
    await expect(patient).toHaveCount(1, { timeout: 5_000 });
    const trigger = patient.getByRole('button', { name: `Explain Gender for row ${rowIndex + 1}`, exact: true });
    const fromIndex = networkObserver.requests.length;
    const traceCapture = capture(networkObserver.waitForResponse(entry =>
      entry.method === 'POST' && entry.path === cellTracePath(project, explorerId),
    { fromIndex, timeoutMs: 5_000 }));
    let traceEntry;
    await action(`Explain the female Patient Gender cell ${phase}`, trigger, () => trigger.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        traceEntry = await requireCapture(traceCapture, `${phase} receipt-bound cell trace`);
        assertCellTraceBinding({ entry: traceEntry, canonicalProject, explorer: explorerId,
          generation, qualityReport: quality, authorizationScopeDigest, outputIdForTrace: outputId, rowId: expectedRowID });
        await expect(dialog).toBeVisible({ timeout: 5_000 });
        await expect(dialog.locator('p').filter({ hasText: /^female$/ }).first()).toBeVisible({ timeout: 5_000 });
        await expect(dialog.getByText('One source record supplied this value.', { exact: true }))
          .toBeVisible({ timeout: 5_000 });
      },
      requiredCheck: `${phase} Explain action shows receipt-bound source evidence within five seconds`,
    });
    check('correctness', `${phase} Explain request binds the exact rendered Patient row and Gender column`, true,
      { request: traceEntry.request, rowID: expectedRowID, outputId: outputId, column: 'gender', receiptId: quality.receiptId });
    check('correctness', `${phase} Explain returns the exact independent female Patient source`, true,
      { trace: traceEntry.response?.trace, feature: traceEntry.response?.feature, binding: traceEntry.response?.binding,
        expectedPatient: oracle.explainedPatient });
    return traceEntry;
  };

  const expandSourceDetails = async phase => {
    const details = dialog.locator('details');
    await expect(details).toHaveCount(1, { timeout: 5_000 });
    const summary = details.locator('summary');
    await action(`Expand ${phase} Gender source details`, summary, () => summary.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        await expect(details.getByText('Patient / dev-patient-001', { exact: true })).toBeVisible({ timeout: 5_000 });
        await expect(details.getByText('female', { exact: true })).toBeVisible({ timeout: 5_000 });
      },
      requiredCheck: `${phase} source details show the exact Patient identity and literal Gender value within five seconds`,
    });
    check('correctness', `${phase} Source details identify Patient dev-patient-001 with female Gender`, true,
      { resourceType: 'Patient', resourceId: oracle.explainedPatient.id, value: oracle.explainedPatient.value,
        patientPath: oracle.patientPath, patientSHA256: oracle.patientSHA256 });
  };

  const closeDialog = async phase => {
    const close = dialog.getByRole('button', { name: 'Close cell explanation', exact: true });
    await action(`Close ${phase} cell explanation`, close, () => close.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: () => expect(dialog).toBeHidden({ timeout: 5_000 }),
      requiredCheck: `${phase} cell explanation closes within five seconds`,
    });
  };

  await explainCell('initial', initialRows, qualityReport, initialRowId);
  await expandSourceDetails('initial');
  await closeDialog('initial');
  const reopenedTrace = await explainCell('reopened', initialRows, qualityReport, initialRowId);
  check('correctness', 'reopened cell explanation retains the same receipt-bound Patient source',
    reopenedTrace.request?.receiptId === qualityReport.receiptId && reopenedTrace.response?.trace?.rowId === initialRowId,
    { receiptId: reopenedTrace.request?.receiptId, expectedReceiptId: qualityReport.receiptId,
      rowID: reopenedTrace.response?.trace?.rowId, expectedRowID: initialRowId });

  const reloadStart = networkObserver.requests.length;
  const reloadRenderStarted = performance.now();
  const reloadRuntimeCapture = capture(networkObserver.waitForResponse(entry =>
    entry.method === 'GET' && entry.path === outputRuntimePath,
  { fromIndex: reloadStart, timeoutMs: remainingBudgetMs(reloadRenderStarted) }));
  const reloadGraphCapture = capture(page.waitForResponse(response =>
    matchesGraphRequest(response.request(), { origin, project, selector: output.selector }),
  { timeout: remainingBudgetMs(reloadRenderStarted) }));
  await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingBudgetMs(reloadRenderStarted) });
  const [reloadRuntimeEntry, reloadGraphResponse] = await Promise.all([
    requireCapture(reloadRuntimeCapture, 'reloaded Viewer runtime'),
    requireCapture(reloadGraphCapture, 'reloaded Patient output query'),
  ]);
  const reloaded = inspectRuntime(reloadRuntimeEntry, {
    canonicalProject, generation, outputSelector: output.selector,
  });
  assert.equal(reloaded.qualityReport.receiptId, qualityReport.receiptId,
    'unchanged Viewer reload must retain the same immutable compilation receipt');
  assert.equal(reloaded.qualityReport.scopeDigest, qualityReport.scopeDigest,
    'unchanged Viewer reload must retain the publication semantic scope digest');
  check('correctness', 'Viewer reload retains the same immutable output receipt and scope binding', true,
    { receiptId: reloaded.qualityReport.receiptId, publicationScopeDigest: reloaded.qualityReport.scopeDigest,
      expectedReceiptId: qualityReport.receiptId, expectedPublicationScopeDigest: qualityReport.scopeDigest,
      authorizationScopeDigest });
  await expect(dialog).toBeHidden({ timeout: remainingBudgetMs(reloadRenderStarted) });
  const reloadGraphStatus = reloadGraphResponse.status();
  const reloadGraphBody = await reloadGraphResponse.json();
  const reloadGraphErrors = Array.isArray(reloadGraphBody.errors) ? reloadGraphBody.errors : [];
  assert(reloadGraphStatus >= 200 && reloadGraphStatus < 300, `reloaded Patient output query returned HTTP ${reloadGraphStatus}`);
  assert.deepEqual(reloadGraphErrors, [], 'reloaded Patient output query must not return GraphQL errors');
  assert(matchesGraphRequest(reloadGraphResponse.request(), { origin, project, selector: output.selector }),
    'reloaded Patient output query must use the same project and published output selector');
  check('correctness', 'reloaded Viewer query uses the exact project and receipt-selected output', true,
    { project, selector: output.selector, columns: outputColumns, graphErrors: reloadGraphErrors.length });
  const reloadedRows = rowsFromConnection(reloadGraphBody.data?.dataframeRows);
  assertOutputRows(reloadedRows, oracle, check, 'reloaded');
  const reloadFemale = reloadedRows.find(row => row.patientID === oracle.explainedPatient.id);
  assert(reloadFemale, 'reloaded output must retain the independent female Patient row');
  await waitForRenderedRows(page, reloadedRows.map(row => [row.patientID, row.gender ?? '—']), reloadRenderStarted);
  const reloadedDOMRows = await table.locator('tbody tr').evaluateAll(rows => rows.map(row =>
    [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())));
  assert.deepEqual(reloadedDOMRows.map(row => row[0]), reloadedRows.map(row => row.patientID),
    'reloaded Viewer rows must keep the GraphQL response order that binds each rowId to its cell');
  assert.deepEqual([...reloadedDOMRows].sort((left, right) => left[0].localeCompare(right[0])),
    [...expectedRenderedRows].sort((left, right) => left[0].localeCompare(right[0])),
  'Viewer reload must restore both literal Patient values, including the missing Gender display');
  const reloadRenderMs = Math.round(performance.now() - reloadRenderStarted);
  assert(reloadRenderMs <= 5_000, `Viewer reload took ${reloadRenderMs}ms to restore the exact fixture rows`);
  check('performance', 'Viewer reload restores exact Patient values within five seconds', true,
    { elapsedMs: reloadRenderMs, budgetMs: 5_000, rows: reloadedDOMRows });
  check('correctness', 'Viewer reload restores the same receipt, Patient identities, and missing-Gender display', true,
    { receiptId: reloaded.qualityReport.receiptId, patientIDs: reloadedRows.map(row => row.patientID),
      expectedPatientIDs: oracle.patients.map(patient => patient.id), rows: reloadedDOMRows });

  await explainCell('after reload', reloadedRows, reloaded.qualityReport, reloadFemale.rowId);
  await expandSourceDetails('after reload');
  await closeDialog('after reload');

  const patientBytesAfter = readFileSync(oracle.patientPath);
  const patientSHA256After = createHash('sha256').update(patientBytesAfter).digest('hex');
  assert.equal(patientSHA256After, oracle.patientSHA256, 'Basic Patient source fixture must stay unchanged during cell explanation');
  check('correctness', 'Basic Patient source fixture stays unchanged during the receipt-bound explanation lifecycle', true,
    { patientPath: oracle.patientPath, patientSHA256Before: oracle.patientSHA256, patientSHA256After });
  report.target.cellExplanation = {
    outputId,
    column: 'gender',
    patientID: oracle.explainedPatient.id,
    value: oracle.explainedPatient.value,
    receiptId: qualityReport.receiptId,
    sourceResourceType: 'Patient',
    sourcePath: 'gender',
    sourceSHA256: oracle.patientSHA256,
    publicationScopeDigest: qualityReport.scopeDigest,
    authorizationScopeDigest,
  };
};
