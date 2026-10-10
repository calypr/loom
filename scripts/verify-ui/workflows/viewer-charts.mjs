import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicViewerChartsOracle } from '../helpers/basic-viewer-charts-oracle.mjs';
import { waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';
import { browserURL } from './builder-url.mjs';

const GRAPH_PATH = '/graphql/graph';
const runtimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;
const authoringPath = (project, explorer, suffix) =>
  `${runtimePath(project, explorer)}/authoring/v2/${suffix}`;
const requestBody = request => {
  try { return request.postDataJSON(); } catch { return null; }
};
export const requestBodyFromResponse = response => requestBody(response.request());
const sameSelector = (left, right) => Boolean(left && right &&
  left.recipe === right.recipe && left.translationVersion === right.translationVersion && left.output === right.output);
const isOwnedResponse = (response, { origin, method, path }) => {
  try {
    const url = new URL(response.url());
    return response.request().method() === method && url.origin === origin && url.pathname === path;
  } catch {
    return false;
  }
};
const fieldPathFor = column => column?.source?.field?.path ?? column?.source?.fieldPath ?? null;
const expectedPreviewRows = oracle => oracle.rows.map(row => ({
  'Patient ID': row.id,
  Gender: row.gender ?? '—',
}));
const expectedIDsOnly = oracle => oracle.rows.map(row => ({ 'Patient ID': row.id }));
export const sortViewerPreviewRowsByPatientID = rows => [...rows].sort((left, right) =>
  left['Patient ID'].localeCompare(right['Patient ID']));

export const visibleGridSnapshotFromElement = element => {
  const normalizeHeader = header => (header.textContent ?? '').replace(/\s+/g, ' ').trim();
  const snapshotRows = (headers, cellRows) => ({
    headers,
    rows: cellRows.map(cells => Object.fromEntries(headers.map((header, index) =>
      [header, (cells[index]?.innerText ?? '').trim()]))),
  });
  if (String(element.tagName ?? '').toLowerCase() === 'table') {
    const headers = [...element.querySelectorAll('thead th')].map(normalizeHeader);
    const rows = [...element.querySelectorAll('tbody tr')].map(row => [...row.querySelectorAll('td')]);
    return snapshotRows(headers, rows);
  }
  const rows = [...element.querySelectorAll('[role="row"]')];
  const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
    .map(normalizeHeader);
  return snapshotRows(headers, rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')]));
};

const visibleGridSnapshot = table => table.evaluate(visibleGridSnapshotFromElement);

const readSuccessfulJSON = async (response, label) => {
  assert(response, `${label} response was not captured`);
  assert(response.status() >= 200 && response.status() < 300,
    `${label} returned HTTP ${response.status()}`);
  return response.json();
};

const readBuilder = async ({ page, target, project, explorer }) => {
  const response = await page.context().request.get(
    `${target.apiUrl}${authoringPath(project, explorer, 'builder')}`,
    { timeout: 5_000 },
  );
  assert(response.ok(), `Builder state read returned HTTP ${response.status()}`);
  return response.json();
};

export const assertChartFacet = payload => {
  assert.deepEqual(payload?.errors ?? [], [], 'chart GraphQL query must not return errors');
  const raw = payload?.data?.dataframeAggregations?.aggregations;
  const aggregations = typeof raw === 'string' ? JSON.parse(raw) : raw;
  assert(Array.isArray(aggregations), 'chart response must expose the raw dataframe aggregations array');
  const [facet] = aggregations;
  assert.equal(aggregations.length, 1, 'Show charts must request exactly one chart facet for this output');
  assert.equal(facet.kind, 'TERMS');
  assert.deepEqual(facet.columns, ['key', 'doc_count']);
  assert.deepEqual(facet.rows, [{ key: 'female', doc_count: '1' }],
    'TERMS rows must contain the one populated value and omit the missing-value bin');
  assert.equal(facet.missingCount, 1,
    'the raw TERMS facet must separately report the Patient with missing Gender');
  assert.equal(facet.rows.some(row => row.key === null || row.key === undefined), false,
    'missing Gender must not render as a null chart term');
  assert.notEqual(facet.truncated, true, 'the exact one-term result must not be truncated');
  return facet;
};

export const graphInputUsesFixtureProject = (body, project) => body?.variables?.input?.projectId === project;

export const chartSpecFor = ({ request, project, output, column }) => {
  try {
    const url = new URL(request.url());
    const body = requestBody(request);
    const input = body?.variables?.input;
    const facetInput = body?.variables?.facetInput;
    const specs = facetInput?.specs;
    return request.method() === 'POST' && url.pathname === GRAPH_PATH &&
      graphInputUsesFixtureProject(body, project) && facetInput?.projectId === project &&
      sameSelector(input?.selector, output.selector) && sameSelector(facetInput?.selector, output.selector) &&
      Array.isArray(specs) && specs.length === 1 &&
      specs[0]?.name === `loom:${output.outputId}:chart:${column}` &&
      specs[0]?.kind === 'TERMS' && specs[0]?.column === column && specs[0]?.size === 12 &&
      !Object.hasOwn(specs[0], 'excludeSelfFilter');
  } catch {
    return false;
  }
};

const assertVisibleChartSummary = async panel => {
  const table = panel.getByRole('table', { name: 'Gender chart values', exact: true });
  await expect(table).toBeVisible({ timeout: 5_000 });
  const rows = table.getByRole('row');
  await expect(rows).toHaveCount(2, { timeout: 5_000 });
  const headers = table.getByRole('columnheader');
  await expect(headers).toHaveText(['Category', 'Count'], { timeout: 5_000 });
  const categoryRow = table.getByRole('row', { name: 'female 1', exact: true });
  await expect(categoryRow).toHaveCount(1, { timeout: 5_000 });
  const cells = categoryRow.getByRole('cell');
  await expect(cells).toHaveText(['female', '1'], { timeout: 5_000 });
  const missing = panel.getByText('Missing values: 1', { exact: true });
  await expect(missing).toBeVisible({ timeout: 5_000 });
  const summary = {
    table: await table.getAttribute('aria-label'),
    headers: await headers.allInnerTexts(),
    row: await cells.allInnerTexts(),
    missing: await missing.innerText(),
  };
  assert.deepEqual(summary, {
    table: 'Gender chart values',
    headers: ['Category', 'Count'],
    row: ['female', '1'],
    missing: 'Missing values: 1',
  });
  return summary;
};

const waitForNativeViewerGrid = async (table, expectedRows) => {
  await expect(table).toBeVisible({ timeout: 5_000 });
  await expect.poll(async () => {
    const snapshot = await visibleGridSnapshot(table);
    return { headers: snapshot.headers, rows: sortViewerPreviewRowsByPatientID(snapshot.rows) };
  }, { timeout: 5_000 }).toEqual({
    headers: ['Patient ID', 'Gender'],
    rows: sortViewerPreviewRowsByPatientID(expectedRows),
  });
};

const captureChartFacet = async ({ page, target, project, output, column, action, chartButton, phase }) => {
  const origin = new URL(target.uiUrl).origin;
  let visibleSummary;
  const responsePromise = page.waitForResponse(response =>
    isOwnedResponse(response, { origin, method: 'POST', path: GRAPH_PATH }) &&
      chartSpecFor({ request: response.request(), project, output, column }),
  { timeout: 5_000 });
  await action(`open ${phase} charts`, chartButton, () => chartButton.click(), {
    timeout: 5_000,
    budget: 5_000,
    after: async () => {
      const response = await responsePromise;
      assert(response.status() >= 200 && response.status() < 300,
        `${phase} chart facet query returned HTTP ${response.status()}`);
      await expect(page.getByRole('button', { name: 'Hide charts', exact: true }))
        .toHaveAttribute('aria-pressed', 'true', { timeout: 5_000 });
      const charts = page.getByRole('region', { name: 'Charts', exact: true });
      await expect(charts).toBeVisible({ timeout: 5_000 });
      const panel = charts.getByRole('article');
      await expect(panel).toHaveCount(1, { timeout: 5_000 });
      await expect(panel.getByText('Gender', { exact: true })).toBeVisible({ timeout: 5_000 });
      await expect(panel.locator('canvas')).toHaveCount(1, { timeout: 5_000 });
      await expect(panel.locator('canvas')).toBeVisible({ timeout: 5_000 });
      visibleSummary = await assertVisibleChartSummary(panel);
    },
    requiredCheck: `${phase} Show charts renders the exact published Gender summary and plot within five seconds`,
  });
  const response = await responsePromise;
  const payload = await response.json();
  const facet = assertChartFacet(payload);
  const body = requestBody(response.request());
  const spec = body?.variables?.facetInput?.specs?.[0];
  assert.equal(spec.name, `loom:${output.outputId}:chart:${column}`);
  assert.equal(facet.name, spec.name, 'response facet name must be bound to the requested output column');
  return { response, facet, spec, payload, summary: visibleSummary };
};

export const viewerChartsWorkflow = async ({ page, report, action, check }, context) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const uiOrigin = new URL(target.uiUrl).origin;
  const oracle = readBasicViewerChartsOracle({ fixtureDir: target.fixtureDir });
  const sourceHashBefore = oracle.sha256;
  const expectedIDs = oracle.rows.map(row => row.id);
  let explorer;

  report.target.project = project;
  report.target.canonicalProject = canonicalProject;
  report.target.fixtureOracle = {
    path: oracle.path,
    sha256: oracle.sha256,
    resourceType: oracle.resourceType,
    rows: oracle.rows,
    displayedCount: oracle.displayedCount,
    populatedCount: oracle.populatedCount,
    missingCount: oracle.missingCount,
    expectedTerms: oracle.expectedTerms,
  };
  check('correctness', 'independent Patient oracle proves one female value and one omitted Gender value',
    oracle.displayedCount === 2 && oracle.populatedCount === 1 && oracle.missingCount === 1 &&
      oracle.expectedTerms.length === 1 && oracle.expectedTerms[0].key === 'female' &&
      oracle.expectedTerms[0].doc_count === 1 && oracle.rows[1].state === 'missing' &&
      !Object.hasOwn(oracle.rows[1], 'gender'),
    { path: oracle.path, sha256: oracle.sha256, rows: oracle.rows,
      expectedTerms: oracle.expectedTerms, displayedCount: oracle.displayedCount });

  try {
    await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
      waitUntil: 'domcontentloaded', timeout: 5_000,
    });
    const newExplorer = page.getByText('New explorer', { exact: true });
    await action('open Viewer chart Explorer creation', newExplorer, () => newExplorer.click(), {
      after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: 5_000 }),
    });
    const title = `Verify ${context.runID.slice(-10)} chart`;
    const explorerName = page.locator('#new-explorer-name');
    await action('name Viewer chart Explorer', explorerName, () => explorerName.fill(title), { editable: true });
    const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
    await action('create fresh Viewer chart Explorer', createBlank, () => createBlank.click(), {
      after: async () => {
        const picker = page.getByRole('combobox', { name: 'Explorer', exact: true });
        await picker.waitFor({ state: 'visible', timeout: 5_000 });
        await page.waitForFunction(expected =>
          document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected,
        title, { timeout: 5_000 });
      },
    });
    explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
    assert(explorer && explorer !== target.bootstrapExplorerId,
      'Viewer chart case must use a freshly created Explorer');
    report.target.explorer = explorer;

    const tableName = page.locator('#first-table-name');
    await action('name Viewer chart Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create Viewer chart Patient table and settle its Preview', choosePatients,
      () => choosePatients.click(), {
        timeout: 5_000,
        budget: 5_000,
        after: async () => {
          await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5_000 });
          await waitForBuilderRenderedGrid(page, {
            tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
            expectedRows: expectedIDs.map(id => ({ 'Patient ID': id })),
          });
        },
        requiredCheck: 'native Builder Preview shows exactly the two Patient source rows within five seconds',
      });

    const graphMode = page.getByRole('button', { name: 'Advanced graph', exact: true });
    const sourceSetup = page.getByTestId('construction-source-setup');
    if (await sourceSetup.getAttribute('open') === null) {
      const sourceSetupSummary = sourceSetup.locator(':scope > summary');
      await action('open Advanced source setup for chart controls', sourceSetupSummary,
        () => sourceSetupSummary.click(), {
          after: () => graphMode.waitFor({ state: 'visible', timeout: 5_000 }),
        });
    }
    const genderDisplayName = page.getByRole('textbox', { name: 'Display name for available gender', exact: true });
    const addGenderAsChart = page.getByRole('checkbox', { name: 'Add Gender as chart', exact: true });
    await action('open Advanced graph for available chart fields', graphMode, () => graphMode.click(), {
      after: () => genderDisplayName.waitFor({ state: 'visible', timeout: 5_000 }),
    });
    await action('set the chart field display name to Gender', genderDisplayName, () => genderDisplayName.fill('Gender'), {
      after: () => addGenderAsChart.waitFor({ state: 'visible', timeout: 5_000 }),
    });
    await expect(addGenderAsChart).toBeEnabled({ timeout: 5_000 });
    await action('add Gender as chart', addGenderAsChart, () => addGenderAsChart.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        await expect(page.getByRole('checkbox', { name: 'Use Gender as chart', exact: true }))
          .toBeChecked({ timeout: 5_000 });
        await expect(page.getByRole('checkbox', { name: 'Display Gender in table', exact: true }))
          .toHaveCount(1, { timeout: 5_000 });
      },
      requiredCheck: 'native Add Gender as chart creates the authored bar-chart binding within five seconds',
    });

    const displayGender = page.getByRole('checkbox', { name: 'Display Gender in table', exact: true });
    await expect(displayGender).not.toBeChecked({ timeout: 5_000 });
    await action('include Gender in Builder Preview', displayGender, () => displayGender.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        await waitForBuilderRenderedGrid(page, {
          tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
          expectedRows: expectedPreviewRows(oracle),
        });
        await expect(displayGender).toBeChecked({ timeout: 5_000 });
      },
      requiredCheck: 'settled Builder Preview shows female and an empty cell for omitted Gender within five seconds',
    });
    const previewTable = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
    const previewSnapshot = await visibleGridSnapshot(previewTable);
    assert.deepEqual(previewSnapshot.headers, ['Patient ID', 'Gender']);
    assert.deepEqual(sortViewerPreviewRowsByPatientID(previewSnapshot.rows),
      sortViewerPreviewRowsByPatientID(expectedPreviewRows(oracle)));
    check('correctness', 'Builder Preview shows the exact Patient IDs, female value, and missing-value marker', true,
      previewSnapshot);

    const beforeRemoval = await readBuilder({ page, target, project, explorer });
    const document = beforeRemoval.workspace?.documents?.find(candidate => candidate.output?.title === 'Patients');
    assert(document, 'saved chart Explorer must contain the authored Patients table');
    assert.equal(document.rootResourceType, 'Patient');
    assert.equal(document.rows?.kind, 'RECORDS');
    const gender = document.columns?.find(column => column.label === 'Gender' && fieldPathFor(column) === 'gender');
    assert(gender, 'saved chart column must map to the literal Patient.gender source');
    const genderColumn = gender.column;
    const outputId = document.output?.id;
    assert(typeof outputId === 'string' && outputId, 'saved Patient output must expose its exact output identity');
    assert.deepEqual(gender.chart, { type: 'bar', title: 'Gender', order: 0 },
      'Add Gender as chart must save the exact bar chart title, source column, and normalized chart order');
    check('correctness', 'saved Builder chart binding uses the exact Patient.gender field and generated column identity',
      gender.occurrenceId === 'base' && genderColumn.length > 0 &&
        gender.chart?.type === 'bar' && gender.chart?.title === 'Gender' && gender.table?.visible === true,
      { outputId, column: genderColumn, source: gender.source, occurrenceId: gender.occurrenceId,
        chart: gender.chart, table: gender.table });
    report.target.viewerCharts = { outputId, column: genderColumn, receiptId: null, revisionId: null };

    const removeGender = page.getByRole('button', { name: 'Remove Gender', exact: true });
    await action('remove the Gender chart field', removeGender, () => removeGender.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        await expect(genderDisplayName).toHaveValue('Gender', { timeout: 5_000 });
        await addGenderAsChart.waitFor({ state: 'visible', timeout: 5_000 });
        await waitForBuilderRenderedGrid(page, {
          tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
          expectedRows: expectedIDsOnly(oracle),
        });
      },
      requiredCheck: 'native Remove Gender clears its chart column while preserving both source rows within five seconds',
    });
    const removed = await readBuilder({ page, target, project, explorer });
    const removedDocument = removed.workspace?.documents?.find(candidate => candidate.output?.id === outputId);
    check('persistence', 'removing the chart deletes only its configured Gender column and retains the Patient output',
      removedDocument?.columns?.every(column => fieldPathFor(column) !== 'gender') &&
        removedDocument.output?.id === outputId && removedDocument.rootResourceType === 'Patient',
      { outputId, columns: removedDocument?.columns?.map(column => ({ column: column.column, label: column.label,
        fieldPath: fieldPathFor(column) })) ?? null });

    await action('restore Gender as chart', addGenderAsChart, () => addGenderAsChart.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: () => expect(page.getByRole('checkbox', { name: 'Use Gender as chart', exact: true }))
        .toBeChecked({ timeout: 5_000 }),
      requiredCheck: 'native Add Gender as chart restores the removed chart field within five seconds',
    });
    const restoredDisplayGender = page.getByRole('checkbox', { name: 'Display Gender in table', exact: true });
    await expect(restoredDisplayGender).not.toBeChecked({ timeout: 5_000 });
    await action('restore Gender in Builder Preview', restoredDisplayGender, () => restoredDisplayGender.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        await waitForBuilderRenderedGrid(page, {
          tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
          expectedRows: expectedPreviewRows(oracle),
        });
        await expect(restoredDisplayGender).toBeChecked({ timeout: 5_000 });
      },
      requiredCheck: 'restored Builder Preview returns to exact female and missing-value rows within five seconds',
    });
    const restoredBuilder = await readBuilder({ page, target, project, explorer });
    const restoredDocument = restoredBuilder.workspace?.documents?.find(candidate => candidate.output?.id === outputId);
    const restoredGender = restoredDocument?.columns?.find(column => column.label === 'Gender' && fieldPathFor(column) === 'gender');
    assert(restoredGender, 'restored chart must retain the Patient.gender source');
    check('persistence', 'removing and restoring the chart preserves the same output and internal column identity',
      restoredDocument?.output?.id === outputId && restoredGender.column === genderColumn &&
        restoredGender.occurrenceId === 'base' && restoredGender.chart?.type === 'bar' &&
        restoredGender.chart?.title === 'Gender' && restoredGender.chart?.order === 0 &&
        restoredGender.table?.visible === true,
      { outputId, originalColumn: genderColumn, restoredColumn: restoredGender.column,
        source: restoredGender.source, chart: restoredGender.chart, table: restoredGender.table });

    const publishPath = authoringPath(project, explorer, 'publish');
    const publishResponsePromise = page.waitForResponse(response =>
      isOwnedResponse(response, { origin: uiOrigin, method: 'POST', path: publishPath }),
    { timeout: 5_000 });
    const publish = page.getByRole('button', { name: 'Publish', exact: true });
    await action('publish Viewer Gender chart', publish, () => publish.click(), {
      timeout: 5_000,
      budget: 5_000,
      after: async () => {
        const response = await publishResponsePromise;
        assert(response.status() >= 200 && response.status() < 300,
          `Publish returned HTTP ${response.status()}`);
      },
      requiredCheck: 'native Publish completes for the exact Patient Gender chart within five seconds',
    });
    const publishResponse = await publishResponsePromise;
    const receipt = await readSuccessfulJSON(publishResponse, 'Viewer chart Publish');
    const publishedOutput = receipt.outputs?.find(candidate => candidate.outputId === outputId);
    assert(receipt.kind === 'ExplorerBuilderPublication' && typeof receipt.receiptId === 'string' &&
      typeof receipt.revisionId === 'string' && publishedOutput?.state === 'READY',
    'Publish response must bind the exact receipt, revision, and ready Patient output');
    report.target.viewerCharts.receiptId = receipt.receiptId;
    report.target.viewerCharts.revisionId = receipt.revisionId;
    check('correctness', 'Publish returns the exact receipt and READY Patient output identity', true,
      { project, explorer, receiptId: receipt.receiptId, revisionId: receipt.revisionId,
        outputId, output: publishedOutput });

    const runtimeResponsePromise = page.waitForResponse(response =>
      isOwnedResponse(response, { origin: uiOrigin, method: 'GET', path: runtimePath(project, explorer) }),
    { timeout: 5_000 });
    const initialQueryPromise = page.waitForResponse(response => {
      if (!isOwnedResponse(response, { origin: uiOrigin, method: 'POST', path: GRAPH_PATH })) return false;
      const body = requestBody(response.request());
      return graphInputUsesFixtureProject(body, project) &&
        body.variables.input.selector?.output === outputId;
    }, { timeout: 5_000 });
    await page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded', timeout: 5_000 });
    const [runtimeResponse, initialQuery] = await Promise.all([runtimeResponsePromise, initialQueryPromise]);
    const runtimeEnvelope = await readSuccessfulJSON(runtimeResponse, 'Viewer runtime');
    const runtime = runtimeEnvelope.runtime;
    assert(runtime && Array.isArray(runtime.outputs), 'published Viewer runtime must expose its outputs');
    assert.equal(runtimeEnvelope.project, canonicalProject);
    assert.equal(runtimeEnvelope.explorerId, explorer);
    assert.equal(runtime.generation ?? runtime.publication?.generation, target.fixtureGeneration);
    assert.equal(runtime.publication?.revisionId, receipt.revisionId,
      'runtime publication revision must match the accepted Publish receipt');
    assert(typeof runtime.publication?.executionId === 'string' && runtime.publication.executionId,
      'Viewer runtime must expose the exact publication execution identity');
    const output = runtime.outputs.find(candidate => candidate.outputId === outputId);
    assert(output, 'Viewer runtime must expose the exact published Patient output');
    const initialBody = requestBodyFromResponse(initialQuery);
    assert(sameSelector(initialBody?.variables?.input?.selector, output.selector),
      'initial Viewer rows must use the exact selector returned by the published runtime');
    const runtimeGender = output.columns?.find(column => column.column === genderColumn);
    const runtimeChart = output.charts?.find(chart => chart.column === genderColumn);
    assert(runtimeGender && runtimeChart, 'runtime must retain the exact Gender column and chart binding');
    check('correctness', 'Viewer runtime binds the publish revision to the exact project, generation, Explorer output, selector, and Gender column',
      runtime.publication.revisionId === receipt.revisionId &&
        runtime.publication.generation === target.fixtureGeneration &&
        output.outputId === outputId && runtimeGender.column === genderColumn &&
        runtimeChart.type === 'bar' && (runtimeChart.title ?? runtimeChart.label) === 'Gender',
      { project, canonicalProject, explorer, generation: runtime.generation ?? runtime.publication.generation,
        receiptId: receipt.receiptId, revisionId: runtime.publication.revisionId,
        executionId: runtime.publication.executionId, outputId, selector: output.selector,
        column: runtimeGender, chart: runtimeChart });
    report.target.viewerQueryOwnership = {
      project, canonicalProject, explorer, generation: runtime.generation ?? runtime.publication.generation,
      outputId, column: genderColumn, selector: output.selector,
      receiptId: receipt.receiptId, revisionId: receipt.revisionId,
      executionId: runtime.publication.executionId,
    };

    const outputTable = page.locator('table[aria-label$=" results"]');
    await outputTable.waitFor({ state: 'visible', timeout: 5_000 });
    await waitForNativeViewerGrid(outputTable, expectedPreviewRows(oracle));
    const initialViewerSnapshot = await visibleGridSnapshot(outputTable);
    assert.deepEqual(initialViewerSnapshot.headers, ['Patient ID', 'Gender']);
    assert.deepEqual(
      sortViewerPreviewRowsByPatientID(initialViewerSnapshot.rows),
      sortViewerPreviewRowsByPatientID(expectedPreviewRows(oracle)));
    check('correctness', 'Viewer table retains the exact Patient IDs and visible Gender values before chart activation', true,
      initialViewerSnapshot);

    const showCharts = page.getByRole('button', { name: 'Show charts', exact: true });
    const firstChart = await captureChartFacet({ page, target, project, output,
      column: genderColumn, action, chartButton: showCharts, phase: 'initial' });
    const chartRegion = page.getByRole('region', { name: 'Charts', exact: true });
    const chartPanel = chartRegion.getByRole('article');
    const chartTitle = await chartPanel.getByText('Gender', { exact: true }).innerText();
    const accessibility = await chartPanel.locator('canvas').evaluateAll(elements => elements.map(element => ({
      role: element.getAttribute('role'),
      ariaLabel: element.getAttribute('aria-label'),
      title: element.getAttribute('title'),
    })));
    check('usability', 'Viewer displays the exact Gender title and a visible chart plot',
      chartTitle === 'Gender' && await chartPanel.locator('canvas').isVisible(),
      { title: chartTitle, plotCount: accessibility.length, plotAccessibility: accessibility,
        accessibleChartSection: await chartRegion.getAttribute('aria-label') });
    check('correctness', 'Viewer requests the exact scoped TERMS facet with female:1 and one separate missing value',
      firstChart.spec?.name === `loom:${outputId}:chart:${genderColumn}` &&
        firstChart.spec.kind === 'TERMS' && firstChart.spec.column === genderColumn &&
        firstChart.spec.size === 12 && firstChart.facet.rows.length === 1 &&
        firstChart.facet.rows[0].key === 'female' && firstChart.facet.rows[0].doc_count === '1' &&
        firstChart.facet.missingCount === 1,
      { project, canonicalProject, outputId, selector: output.selector, requestedFacet: firstChart.spec,
        responseFacet: firstChart.facet, visibleChartTitle: chartTitle,
        plotAccessibility: accessibility });
    check('usability', 'Viewer exposes female:1 and a separate missing Gender count in the chart summary',
      true, firstChart.summary);
    report.target.viewerCharts.chartFacet = firstChart.facet;
    report.target.viewerCharts.visibleSummary = firstChart.summary;

    const initialPublicationIdentity = {
      receiptId: receipt.receiptId,
      revisionId: runtime.publication.revisionId,
      executionId: runtime.publication.executionId,
      outputId: output.outputId,
      selector: output.selector,
      column: genderColumn,
    };
    const reloadRuntimePromise = page.waitForResponse(response =>
      isOwnedResponse(response, { origin: uiOrigin, method: 'GET', path: runtimePath(project, explorer) }),
    { timeout: 5_000 });
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 });
    const reloadRuntimeResponse = await reloadRuntimePromise;
    const reloadEnvelope = await readSuccessfulJSON(reloadRuntimeResponse, 'reloaded Viewer runtime');
    const reloadedRuntime = reloadEnvelope.runtime;
    const reloadedOutput = reloadedRuntime?.outputs?.find(candidate => candidate.outputId === outputId);
    assert(reloadedOutput, 'reload must retain the exact Patient output');
    const reloadedIdentity = {
      receiptId: receipt.receiptId,
      revisionId: reloadedRuntime.publication?.revisionId,
      executionId: reloadedRuntime.publication?.executionId,
      outputId: reloadedOutput.outputId,
      selector: reloadedOutput.selector,
      column: reloadedOutput.charts?.find(chart => chart.column === genderColumn)?.column,
    };
    check('persistence', 'reload preserves the same publish receipt, revision, execution, output, selector, and chart column identity',
      reloadEnvelope.project === canonicalProject && reloadEnvelope.explorerId === explorer &&
        (reloadedRuntime.generation ?? reloadedRuntime.publication?.generation) === target.fixtureGeneration &&
        reloadedIdentity.revisionId === initialPublicationIdentity.revisionId &&
        reloadedIdentity.executionId === initialPublicationIdentity.executionId &&
        reloadedIdentity.outputId === initialPublicationIdentity.outputId &&
        sameSelector(reloadedIdentity.selector, initialPublicationIdentity.selector) &&
        reloadedIdentity.column === initialPublicationIdentity.column,
      { before: initialPublicationIdentity, after: reloadedIdentity,
        generation: reloadedRuntime.generation ?? reloadedRuntime.publication?.generation });

    const reloadedTable = page.locator('table[aria-label$=" results"]');
    await reloadedTable.waitFor({ state: 'visible', timeout: 5_000 });
    await waitForNativeViewerGrid(reloadedTable, expectedPreviewRows(oracle));
    const reloadedViewerSnapshot = await visibleGridSnapshot(reloadedTable);
    assert.deepEqual(reloadedViewerSnapshot.headers, ['Patient ID', 'Gender']);
    assert.deepEqual(
      sortViewerPreviewRowsByPatientID(reloadedViewerSnapshot.rows),
      sortViewerPreviewRowsByPatientID(expectedPreviewRows(oracle)));
    const reopenedButton = page.getByRole('button', { name: 'Show charts', exact: true });
    const reopenedChart = await captureChartFacet({ page, target, project, output: reloadedOutput,
      column: genderColumn, action, chartButton: reopenedButton, phase: 'reopened' });
    const reopenedRegion = page.getByRole('region', { name: 'Charts', exact: true });
    const reopenedPanel = reopenedRegion.getByRole('article');
    const reopenedTitle = await reopenedPanel.getByText('Gender', { exact: true }).innerText();
    assert.deepEqual(reopenedChart.facet, firstChart.facet,
      'reopening charts after reload must retain the exact raw facet values and count types');
    assert.deepEqual(reopenedChart.summary, firstChart.summary,
      'reopening charts after one reload must restore the same exact accessible category/count and missing count');
    check('persistence', 'reopening charts after the single Viewer reload restores the exact Gender summary and plot',
      reopenedTitle === 'Gender' && await reopenedPanel.locator('canvas').isVisible() &&
        JSON.stringify(reopenedChart.facet) === JSON.stringify(firstChart.facet) &&
        JSON.stringify(reopenedChart.summary) === JSON.stringify(firstChart.summary) &&
        reloadedOutput.outputId === outputId &&
        reloadedRuntime.publication?.revisionId === receipt.revisionId &&
        JSON.stringify(sortViewerPreviewRowsByPatientID(reloadedViewerSnapshot.rows)) ===
          JSON.stringify(sortViewerPreviewRowsByPatientID(initialViewerSnapshot.rows)),
      { title: reopenedTitle, outputId: reloadedOutput.outputId,
        revisionId: reloadedRuntime.publication?.revisionId,
        facet: reopenedChart.facet, originalFacet: firstChart.facet,
        summary: reopenedChart.summary, originalSummary: firstChart.summary,
        rows: reloadedViewerSnapshot.rows });
    report.target.viewerCharts.reopenedFacet = reopenedChart.facet;
    report.target.viewerCharts.reopenedSummary = reopenedChart.summary;
  } finally {
    try {
      const afterHash = createHash('sha256').update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
      check('correctness', 'independent Patient fixture stayed unchanged during Viewer chart verification',
        afterHash === sourceHashBefore,
        { before: sourceHashBefore, after: afterHash, fixture: join(target.fixtureDir, 'Patient.ndjson') });
    } catch (error) {
      check('correctness', 'independent Patient fixture stayed unchanged during Viewer chart verification', false,
        { before: sourceHashBefore, error: error.message });
    }
  }
};
