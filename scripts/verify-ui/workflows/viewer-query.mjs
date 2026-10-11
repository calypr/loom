import assert from 'node:assert/strict';
import { expect } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { sanitizePayload, sanitizeText } from '../helpers/playwright-browser.mjs';
import { recordCheck, recordUntested } from '../helpers/report.mjs';
import { waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import { canonicalProjectID } from '../../loom-dev.mjs';

const graphPath = '/graphql/graph';
export const explorerRuntimePath = (project, explorer) =>
  `${fixtureProjectExplorerCollectionPath(project)}/${encodeURIComponent(explorer)}`;

const fixturePatientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const ids = patients.map(patient => patient.id).sort();
  assert.deepEqual(ids, ['dev-patient-001', 'dev-patient-002'], 'Viewer fixture oracle must contain exactly the two independent Patient identities');
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    ids,
    femaleIDs: patients.filter(patient => patient.gender === 'female').map(patient => patient.id).sort(),
  };
};

export const assertViewerPatientRows = (rows, expectedIDs) => {
  const actualIDs = rows.map(text => expectedIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  assert.deepEqual(actualIDs, [...expectedIDs].sort(), 'Viewer rows must contain the exact independent fixture Patient identities');
  return actualIDs;
};

export const assertViewerPatientRowsInOrder = (rows, expectedIDs) => {
  const actualIDs = rows.map(text => expectedIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`);
  assert.deepEqual(actualIDs, [...expectedIDs], 'Viewer rows must appear in the exact independent fixture Patient order');
  return actualIDs;
};

export const matchesViewerRequestBody = (body, { project, selector }) => {
  const input = body?.variables?.input;
  return input?.projectId === canonicalProjectID(project) && input.selector?.recipe === selector?.recipe &&
    input.selector?.translationVersion === selector?.translationVersion &&
    input.selector?.output === selector?.output;
};

export const matchesViewerProjectRequest = (request, { origin, project }) => {
  try {
    const url = new URL(request.url());
    return request.method() === 'POST' && url.origin === origin && url.pathname === graphPath &&
      requestBody(request)?.variables?.input?.projectId === canonicalProjectID(project);
  } catch {
    return false;
  }
};

const captureViewerFailure = async (page, report, testInfo, error, details = {}) => {
  const { action, ...safeDetails } = details;
  const location = new URL(page.url());
  const dom = await page.locator('body').innerText().catch(() => '');
  const evidence = sanitizePayload({
    message: sanitizeText(error?.message ?? error),
    stack: sanitizeText(error?.stack ?? ''),
    url: `${location.origin}${location.pathname}`,
    ...safeDetails,
    ...(action ? { action: { label: action.label, locator: action.locator } } : {}),
    dom: sanitizeText(dom).slice(0, 8000),
  });
  report.failureTrace ??= evidence;
  await testInfo?.attach('viewer-query-first-failure.json', {
    body: JSON.stringify(evidence, null, 2),
    contentType: 'application/json',
  }).catch(() => undefined);
};

const requestBody = request => {
  try { return request.postDataJSON(); }
  catch { return null; }
};

export const matchesViewerOutputRequest = (request, { origin, project, selector, sort }) => {
  try {
    const url = new URL(request.url());
    const body = requestBody(request);
    const input = body?.variables?.input;
    const sortMatches = sort
      ? input?.sort?.column === sort.column && input.sort.desc === sort.desc && input.columns?.includes(sort.column)
      : Boolean(input) && !Object.hasOwn(input, 'sort');
    return request.method() === 'POST' && url.origin === origin && url.pathname === graphPath &&
      matchesViewerRequestBody(body, { project, selector }) && sortMatches;
  } catch {
    return false;
  }
};

const waitForViewerPatientOrder = (page, expectedIDs) => page.waitForFunction(ids => {
  const table = document.querySelector('table[aria-label$=" results"]');
  if (!table) return false;
  const rows = [...table.querySelectorAll('tbody tr')].map(row => row.innerText);
  const actualIDs = rows.map(text => ids.find(id => text.includes(id)) ?? null);
  return actualIDs.length === ids.length && actualIDs.every((id, index) => id === ids[index]);
}, expectedIDs, { timeout: 5000 });

const matchesViewerGenderFilterUpdate = body => {
  if (!Array.isArray(body?.commands) || body.commands.length !== 1) return false;
  const [command] = body.commands;
  return command?.type === 'UPDATE_COLUMN' &&
    typeof command.outputId === 'string' && command.outputId.length > 0 &&
    typeof command.column === 'string' && command.column.length > 0 &&
    command.columnValue?.column === command.column &&
    command.columnValue?.label === 'Gender' &&
    command.columnValue?.filter?.label === 'Gender';
};

export const matchesViewerGenderFilterRequest = (request, { origin, path }) => {
  try {
    const url = new URL(request.url());
    return request.method() === 'POST' && url.origin === origin && url.pathname === path &&
      matchesViewerGenderFilterUpdate(requestBody(request));
  } catch {
    return false;
  }
};

const sameSelector = (left, right) => Boolean(left && right && left.recipe === right.recipe &&
  left.translationVersion === right.translationVersion && left.output === right.output);

const checkViewerScope = (report, { target, explorer, runtime, output, request }) => {
  const body = requestBody(request);
  const project = target.fixtureProject;
  const selectorOwned = runtime.outputs.some(candidate => sameSelector(candidate.selector, body?.variables?.input?.selector));
  const scope = {
    requestProject: body?.variables?.input?.projectId ?? null,
    expectedProject: canonicalProjectID(project),
    outputId: output.outputId,
    explorer,
    generation: runtime.generation ?? runtime.publication?.generation ?? null,
    expectedGeneration: target.fixtureGeneration ?? null,
    selectorOwned,
  };
  const projectMatches = body?.variables?.input?.projectId === canonicalProjectID(project);
  const generationMatches = target.fixtureGeneration === null || target.fixtureGeneration === undefined ||
    scope.generation === target.fixtureGeneration;
  const passed = projectMatches && selectorOwned && generationMatches;
  recordCheck(report, 'correctness', 'Viewer query retains the exact project, generation, Explorer output, and published selector scope', passed, scope);
  assert(passed, 'Viewer GraphQL request did not match the selected project, generation, and published Explorer selector');
  report.target.viewerQueryOwnership = {
    project,
    explorer,
    generation: scope.generation,
    outputId: output.outputId,
    selector: output.selector,
  };
  return body.variables.input.selector;
};

export const viewerQueryWorkflow = async ({ page, report, action, fault, check }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  const uiOrigin = new URL(target.uiUrl).origin;
  let explorer = target.bootstrapExplorerId;
  let expectedIDs;
  let sourceOracle;
  let sourceBytesBefore;
  try {
  if (!context.custom) {
    sourceOracle = fixturePatientOracle(target);
    expectedIDs = sourceOracle.ids;
    const expectedPreviewRows = expectedIDs.map(id => ({ 'Patient ID': id }));
    sourceBytesBefore = sourceOracle.sha256;
    assert.equal(sourceOracle.femaleIDs.length, 1, 'Viewer fixture oracle must identify exactly one female Patient for the optional facet contract');
    report.target.fixtureOracle = {
      path: sourceOracle.path,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
      sha256: sourceOracle.sha256,
      patientIDs: expectedIDs,
      femaleIDs: sourceOracle.femaleIDs,
    };

    await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
    const newExplorer = page.getByText('New explorer', { exact: true });
    await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
      after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    });
    const title = `Verify ${context.runID.slice(-10)} viewer`;
    const explorerName = page.locator('#new-explorer-name');
    await action('name Viewer Explorer', explorerName, () => explorerName.fill(title), { editable: true });
    const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
    await action('create blank Viewer Explorer', createBlank, () => createBlank.click(), {
      after: async () => {
        const picker = page.getByRole('combobox', { name: 'Explorer' });
        await picker.waitFor({ state: 'visible' });
        await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected, title);
      },
    });
    explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
    assert(explorer && explorer !== target.bootstrapExplorerId, 'Viewer setup must create a fresh Explorer');
    report.target.explorer = explorer;

    const tableName = page.locator('#first-table-name');
    await action('name Viewer Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create Viewer Patient table and render Preview', choosePatients, () => choosePatients.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
        await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 5000 });
      },
    });
    await waitForBuilderRenderedGrid(page, {
      tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
      expectedRows: expectedPreviewRows,
    });

    const addColumns = page.getByRole('button', { name: /Add columns:/ });
    await action('open Add columns for Viewer table', addColumns, () => addColumns.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' }),
    });
    const fields = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await action('open Fields and related data', fields, () => fields.click());
    const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
    await action('open raw Patient fields', rawFields, () => rawFields.click());
    const gender = page.getByRole('checkbox', { name: 'Select Patient.gender', exact: true });
    await action('select Patient Gender', gender, () => gender.check());
    const addFeature = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await action('add Gender feature', addFeature, () => addFeature.click(), {
      after: async () => {
        await page.getByRole('button', { name: 'Apply columns', exact: true }).waitFor({ state: 'visible' });
      },
    });
    const applyColumns = page.getByRole('button', { name: 'Apply columns', exact: true });
    await action('apply Gender and render Viewer source Preview', applyColumns, () => applyColumns.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByRole('button', { name: /^Select Gender/ }).waitFor({ state: 'visible', timeout: 5000 });
        await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 5000 });
      },
    });
    await waitForBuilderRenderedGrid(page, {
      tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
      expectedRows: expectedPreviewRows,
    });
    const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
    await action('close Viewer operation editor', closeEditor, () => closeEditor.click(), {
      after: () => page.getByTestId('construction-workspace').waitFor({ state: 'visible' }),
    });

    const genderFilter = page.getByRole('checkbox', { name: 'Use Gender as filter', exact: true });
    const sourceSetup = page.locator('details[data-testid="construction-source-setup"] > summary');
    await action('open Advanced source setup', sourceSetup, () => sourceSetup.click(), {
      after: () => genderFilter.waitFor({ state: 'visible' }),
    });
    const commandsPath = `${explorerRuntimePath(project, explorer)}/authoring/v2/commands`;
    const filterUpdateResponse = page.waitForResponse(response =>
      matchesViewerGenderFilterRequest(response.request(), { origin: uiOrigin, path: commandsPath }),
    { timeout: 4000 }).then(
      response => ({ response }),
      error => ({ error }),
    );
    await action('declare Gender as a Viewer runtime filter', genderFilter, () => genderFilter.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        const captured = await filterUpdateResponse;
        assert(captured.response, `Gender filter update did not return: ${captured.error?.message ?? 'no matching response'}`);
        assert(captured.response.status() >= 200 && captured.response.status() < 300,
          `Gender filter update returned HTTP ${captured.response.status()}`);
        await expect(genderFilter).toBeChecked({ timeout: 750 });
      },
    });

    const publishPath = `${explorerRuntimePath(project, explorer)}/authoring/v2/publish`;
    const publishResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
      new URL(response.url()).origin === new URL(target.uiUrl).origin && new URL(response.url()).pathname === publishPath, { timeout: 5000 });
    const publish = page.getByRole('button', { name: 'Publish', exact: true });
    await action('publish Viewer Explorer', publish, () => publish.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await Promise.all([
          publishResponse,
          expect(publish).toBeDisabled({ timeout: 5000 }),
        ]);
      },
    });
    const published = await publishResponse;
    check('correctness', 'Viewer setup Publish returned success', published.status() >= 200 && published.status() < 300,
      { path: new URL(published.url()).pathname, status: published.status(), explorer });
  }

  report.target.project = project;
  report.target.explorer = explorer;
  const runtimePath = explorerRuntimePath(project, explorer);
  const runtimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === runtimePath, { timeout: 5000 });
  const initialQueryPromise = page.waitForRequest(request =>
    matchesViewerProjectRequest(request, { origin: uiOrigin, project }), { timeout: 5000 });
  const viewerURL = browserURL(target, project, explorer, 'viewer');
  await page.goto(viewerURL, { waitUntil: 'domcontentloaded' });
  const runtimeResponse = await runtimeResponsePromise;
  assert(runtimeResponse.ok(), `Viewer runtime GET failed with HTTP ${runtimeResponse.status()}`);
  const runtimeEnvelope = await runtimeResponse.json();
  const runtime = runtimeEnvelope.runtime;
  assert(runtime && Array.isArray(runtime.outputs) && runtime.outputs.length > 0, 'Viewer runtime must expose its published outputs');
  if (!context.custom) {
    const generation = runtime.generation ?? runtime.publication?.generation;
    check('correctness', 'published Viewer runtime retains the fixture generation', generation === target.fixtureGeneration,
      { generation, expectedGeneration: target.fixtureGeneration, project, explorer });
  }
  const initialQuery = await initialQueryPromise;
  const initialBody = requestBody(initialQuery);
  const output = runtime.outputs.find(candidate => matchesViewerRequestBody({ variables: { input: initialBody?.variables?.input } }, {
    project,
    selector: candidate.selector,
  }));
  assert(output, 'Initial Viewer query must use a selector from this exact published Explorer runtime');
  report.target.viewerOutputId = output.outputId;
  const outputSelector = output.selector;
  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: 5000 });
  const initialRows = await outputTable.locator('tbody tr').allInnerTexts();
  if (context.custom) {
    check('correctness', 'custom Viewer runtime and output table load before fault injection', true,
      { outputId: output.outputId, rowCount: initialRows.length, explorer });
  } else {
    assertViewerPatientRows(initialRows, expectedIDs);
  }
  checkViewerScope(report, { target, explorer, runtime, output, request: initialQuery });

  const injection = await fault({
    method: 'POST',
    path: graphPath,
    matchesRequest: body => matchesViewerRequestBody(body, { project, selector: outputSelector }),
  });
  const retryURL = new URL(viewerURL);
  retryURL.searchParams.set('verify_run', context.runID);
  await page.goto(retryURL.toString(), { waitUntil: 'domcontentloaded' });
  const queryError = page.getByRole('alert').filter({ hasText: 'Results could not be loaded.' });
  try {
    await queryError.waitFor({ state: 'visible', timeout: 5000 });
  } catch (error) {
    await captureViewerFailure(page, report, testInfo, error, {
      action: { label: 'wait for Viewer query error', locator: queryError.toString(), targetLocator: queryError },
      phase: 'injected-query-failure',
      project,
      explorer,
      outputId: output.outputId,
      selector: outputSelector,
      injectedRequestCount: injection.count(),
    });
    throw error;
  }
  recordCheck(report, 'correctness', 'injected result-query error is visibly reported', true,
    { alert: await queryError.innerText(), project, explorer, outputId: output.outputId });
  check('correctness', 'specific owned GraphQL result read fault was injected exactly once', injection.count() === 1,
    { origin: uiOrigin, path: graphPath, method: 'POST', project, explorer, outputId: output.outputId, count: injection.count() });

  const retry = page.getByRole('button', { name: /^(Try again|Retry)$/ });
  try {
    await expect(retry).toHaveCount(1);
    await expect(retry).toBeVisible();
    await expect(retry).toBeEnabled();
  } catch (error) {
    await captureViewerFailure(page, report, testInfo, error, {
      action: { label: 'inspect Viewer Retry', locator: retry.toString(), targetLocator: retry },
      phase: 'retry-unavailable', project, explorer, outputId: output.outputId, selector: outputSelector,
    });
    throw error;
  }
  check('usability', 'result-query error exposes an actionable Retry control', true,
    { count: 1, visible: true, enabled: true });

  const retryResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.origin === uiOrigin && url.pathname === graphPath &&
      matchesViewerRequestBody(requestBody(response.request()), { project, selector: outputSelector });
  }, { timeout: 5000 });
  await action('retry result query', retry, () => retry.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await Promise.all([
        retryResponse,
        outputTable.waitFor({ state: 'visible', timeout: 5000 }),
        queryError.waitFor({ state: 'hidden', timeout: 5000 }),
      ]);
    },
  });
  const recoveredResponse = await retryResponse;
  check('correctness', 'retry result query completed', recoveredResponse.status() >= 200 && recoveredResponse.status() < 300,
    { status: recoveredResponse.status(), origin: new URL(recoveredResponse.url()).origin, path: new URL(recoveredResponse.url()).pathname, project, explorer, outputId: output.outputId });
  const restoredRows = await outputTable.locator('tbody tr').allInnerTexts();
  if (context.custom) {
    check('correctness', 'Retry restored the custom output table', await outputTable.isVisible(), { rowCount: restoredRows.length });
    recordUntested(report, 'correctness', 'fixture-specific Patient row identities', 'Custom target rows are unknown and remain unasserted.');
    recordUntested(report, 'usability', 'custom target filter semantics', 'The custom Explorer schema is unknown; only its output query recovery is checked.');
    return;
  }
  const actualIDs = assertViewerPatientRows(restoredRows, expectedIDs);
  check('correctness', 'retried Viewer results contain both independent fixture Patients', actualIDs.length === expectedIDs.length,
    { rows: restoredRows, patientIDs: actualIDs, expectedPatientIDs: expectedIDs, sourceSHA256: sourceOracle.sha256 });

  const patientIDColumns = output.columns.filter(column => column.label === 'Patient ID');
  assert.equal(patientIDColumns.length, 1, 'Published Viewer must expose one Patient ID column for sorting');
  const patientIDColumn = patientIDColumns[0];
  assert.equal(patientIDColumn.sortable, true, 'Published scalar Patient ID column must be sortable');
  const patientIDHeader = outputTable.getByRole('columnheader', { name: /^Patient ID(?:\s|$)/ });
  await expect(patientIDHeader).toHaveCount(1);
  const patientIDSortButton = patientIDHeader.getByRole('button');
  await expect(patientIDSortButton).toBeEnabled();

  const sortPatientIDs = async (desc, orderedIDs, afterReload = false) => {
    const direction = desc ? 'descending' : 'ascending';
    let captured;
    const responsePromise = page.waitForResponse(response => matchesViewerOutputRequest(response.request(), {
      origin: uiOrigin,
      project,
      selector: outputSelector,
      sort: { column: patientIDColumn.column, desc },
    }), { timeout: 5000 }).then(
      response => ({ response }),
      error => ({ error }),
    );
    await action(`sort Viewer by Patient ID ${direction}`, patientIDSortButton, () => patientIDSortButton.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        captured = await responsePromise;
        assert(captured.response, `Patient ID ${direction} sort did not return: ${captured.error?.message ?? 'no matching response'}`);
        const responseFailure = await captured.response.finished();
        assert.equal(responseFailure, null, `Patient ID ${direction} GraphQL response did not finish cleanly`);
        await Promise.all([
          expect(patientIDHeader).toHaveAttribute('aria-sort', direction, { timeout: 5000 }),
          waitForViewerPatientOrder(page, orderedIDs),
        ]);
      },
    });
    const response = captured.response;
    const statusPassed = response.status() >= 200 && response.status() < 300;
    check('correctness', afterReload
      ? 'Viewer Patient ID sorting after reload completed for the exact output'
      : `Viewer Patient ID ${direction} sort completed for the exact output`, statusPassed,
      { status: response.status(), project, explorer, outputId: output.outputId, column: patientIDColumn.column, desc });
    const payload = await response.json();
    assert(!Array.isArray(payload.errors) || payload.errors.length === 0,
      `Patient ID ${direction} GraphQL sort response must not contain errors`);
    const connection = payload.data?.dataframeRows;
    assert(connection && Array.isArray(connection.columns) && Array.isArray(connection.rows),
      `Patient ID ${direction} GraphQL sort response must contain dataframe rows`);
    const columnIndex = connection.columns.indexOf(patientIDColumn.column);
    assert(columnIndex >= 0, 'Sorted GraphQL response must include the exact Patient ID column');
    const responseIDs = connection.rows.map(row => Array.isArray(row) ? row[columnIndex] : row?.[patientIDColumn.column]);
    assert.deepEqual(responseIDs, orderedIDs, `Patient ID ${direction} GraphQL response must match the independent fixture order`);
    const rows = await outputTable.locator('tbody tr').allInnerTexts();
    const actualIDs = assertViewerPatientRowsInOrder(rows, orderedIDs);
    check('correctness', afterReload
      ? 'Viewer reapplies Patient ID sorting after reload'
      : `Viewer Patient ID ${direction} sort renders the exact fixture order`,
      actualIDs.length === orderedIDs.length,
      { rows, patientIDs: actualIDs, expectedPatientIDs: orderedIDs, sourceSHA256: sourceOracle.sha256 });
  };

  await sortPatientIDs(false, expectedIDs);
  await sortPatientIDs(true, [...expectedIDs].reverse());

  let reloadResponseCapture;
  const unsortedReloadResponsePromise = page.waitForResponse(response => matchesViewerOutputRequest(response.request(), {
    origin: uiOrigin,
    project,
    selector: outputSelector,
  }), { timeout: 5000 }).then(
    response => ({ response }),
    error => ({ error }),
  );
  await action('reload Viewer after Patient ID sorting', outputTable, () => page.reload({ waitUntil: 'domcontentloaded' }), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      reloadResponseCapture = await unsortedReloadResponsePromise;
      assert(reloadResponseCapture.response,
        `Viewer reload did not return an unsorted output query: ${reloadResponseCapture.error?.message ?? 'no matching response'}`);
      const responseFailure = await reloadResponseCapture.response.finished();
      assert.equal(responseFailure, null, 'Unsorted Viewer reload response did not finish cleanly');
      await Promise.all([
        outputTable.waitFor({ state: 'visible', timeout: 5000 }),
        expect(patientIDHeader).toHaveAttribute('aria-sort', 'none', { timeout: 5000 }),
        page.waitForFunction(ids => {
          const table = document.querySelector('table[aria-label$=" results"]');
          const rows = [...(table?.querySelectorAll('tbody tr') ?? [])].map(row => row.innerText);
          const actualIDs = rows.map(text => ids.find(id => text.includes(id)) ?? null).sort();
          return actualIDs.length === ids.length && actualIDs.every((id, index) => id === [...ids].sort()[index]);
        }, expectedIDs, { timeout: 5000 }),
      ]);
    },
  });
  const unsortedReloadResponse = reloadResponseCapture.response;
  const unsortedReloadInput = requestBody(unsortedReloadResponse.request())?.variables?.input;
  const sortFieldOmitted = Boolean(unsortedReloadInput) && !Object.hasOwn(unsortedReloadInput, 'sort');
  const reloadSortAttribute = await patientIDHeader.getAttribute('aria-sort');
  const reloadRows = await outputTable.locator('tbody tr').allInnerTexts();
  const reloadedIDs = assertViewerPatientRows(reloadRows, expectedIDs);
  check('correctness', 'Viewer reload clears the in-memory sort and retains both exact fixture Patients',
    unsortedReloadResponse.status() >= 200 && unsortedReloadResponse.status() < 300 &&
      sortFieldOmitted && reloadSortAttribute === 'none' && reloadedIDs.length === expectedIDs.length,
    { status: unsortedReloadResponse.status(), rows: reloadRows, patientIDs: reloadedIDs,
      expectedPatientIDs: expectedIDs, sortFieldOmitted, ariaSort: reloadSortAttribute, sourceSHA256: sourceOracle.sha256 });
  await sortPatientIDs(false, expectedIDs, true);

  const loadValues = page.getByRole('button', { name: 'Load values', exact: true });
  await expect(loadValues).toHaveCount(1);
  await expect(loadValues).toBeVisible();
  await expect(loadValues).toBeEnabled();
  recordCheck(report, 'usability', 'Gender facet can be opened to load values', true, { count: 1 });
  const genderBinding = output.filters.find(binding => binding.label === 'Gender');
  assert(genderBinding?.column, 'Published Viewer runtime must expose the declared Gender filter column');
  const hasGenderSelection = body => Array.isArray(body?.variables?.input?.filters) &&
    body.variables.input.filters.some(filter => filter.column === genderBinding.column && filter.value === 'female');
  const matchesOutputQuery = request => {
    try {
      const url = new URL(request.url());
      return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === graphPath &&
        matchesViewerRequestBody(requestBody(request), { project, selector: outputSelector });
    } catch {
      return false;
    }
  };
  const patientIDsFromResponse = async (response, label) => {
    assert(response.status() >= 200 && response.status() < 300, `${label} returned HTTP ${response.status()}`);
    const payload = await response.json();
    assert(!Array.isArray(payload.errors) || payload.errors.length === 0, `${label} GraphQL response must not contain errors`);
    const connection = payload.data?.dataframeRows;
    assert(connection && Array.isArray(connection.columns) && Array.isArray(connection.rows), `${label} must return dataframe rows`);
    const columnIndex = connection.columns.indexOf(patientIDColumn.column);
    assert(columnIndex >= 0, `${label} must return the exact Patient ID column`);
    return connection.rows.map(row => Array.isArray(row) ? row[columnIndex] : row?.[patientIDColumn.column]);
  };
  await action('load Gender facet values', loadValues, () => loadValues.click(), {
    after: () => page.getByRole('checkbox', { name: /female/i }).waitFor({ state: 'visible' }),
  });
  const female = page.getByRole('checkbox', { name: /female/i });
  await expect(female).toHaveCount(1);
  await expect(female).toBeVisible();
  await expect(female).toBeEnabled();
  recordCheck(report, 'usability', 'female facet value is visible and actionable', true, { count: 1 });
  const applyFemaleFilter = async label => {
    let captured;
    const filteredResponsePromise = page.waitForResponse(response => {
      const request = response.request();
      return matchesOutputQuery(request) && hasGenderSelection(requestBody(request));
    }, { timeout: 5000 }).then(
      response => ({ response }),
      error => ({ error }),
    );
    await action(label, female, () => female.check(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        captured = await filteredResponsePromise;
        assert(captured.response, `${label} did not return: ${captured.error?.message ?? 'no matching response'}`);
        const responseFailure = await captured.response.finished();
        assert.equal(responseFailure, null, `${label} GraphQL response did not finish cleanly`);
        const responseIDs = (await patientIDsFromResponse(captured.response, label)).sort();
        assert.deepEqual(responseIDs, sourceOracle.femaleIDs,
          `${label} GraphQL response must contain only the independently identified female Patient`);
        await page.waitForFunction(expectedID => {
          const rows = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')];
          return rows.length === 1 && rows[0].innerText.includes(expectedID);
        }, sourceOracle.femaleIDs[0], { timeout: 5000 });
      },
    });
    return captured.response;
  };
  await applyFemaleFilter('apply female filter');
  const filteredRows = await outputTable.locator('tbody tr').allInnerTexts();
  const filteredIDs = assertViewerPatientRows(filteredRows, sourceOracle.femaleIDs);
  recordCheck(report, 'correctness', 'Gender facet returns only the independently identified female fixture Patient',
    filteredIDs.length === 1, { rows: filteredRows, patientIDs: filteredIDs, expectedPatientIDs: sourceOracle.femaleIDs });

  const genderResetResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    return matchesViewerOutputRequest(request, { origin: uiOrigin, project, selector: outputSelector }) &&
      !hasGenderSelection(requestBody(request));
  }, { timeout: 5000 }).then(
    response => ({ response }),
    error => ({ error }),
  );
  let genderResetResponse;
  await action('reload Viewer and reset transient Gender selection', outputTable,
    () => page.reload({ waitUntil: 'domcontentloaded' }), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        const [captured] = await Promise.all([
          genderResetResponsePromise,
          outputTable.waitFor({ state: 'visible', timeout: 5000 }),
          page.waitForFunction(ids => {
            const table = document.querySelector('table[aria-label$=" results"]');
            const rows = [...(table?.querySelectorAll('tbody tr') ?? [])].map(row => row.innerText);
            const actualIDs = rows.map(text => ids.find(id => text.includes(id)) ?? null).sort();
            const sortedExpectedIDs = [...ids].sort();
            return actualIDs.length === ids.length && actualIDs.every((id, index) => id === sortedExpectedIDs[index]);
          }, expectedIDs, { timeout: 5000 }),
        ]);
        assert(captured.response, `Gender reset reload did not return: ${captured.error?.message ?? 'no matching response'}`);
        genderResetResponse = captured.response;
        const responseFailure = await genderResetResponse.finished();
        assert.equal(responseFailure, null, 'Gender reset reload GraphQL response did not finish cleanly');
        const [reloadValues] = await Promise.all([
          patientIDsFromResponse(genderResetResponse, 'Gender reset reload'),
          expect(loadValues).toBeVisible({ timeout: 5000 }),
        ]);
        assert.deepEqual(reloadValues.sort(), expectedIDs,
          'Gender reset reload GraphQL response must contain both exact fixture Patients');
      },
    });
  const resetRows = await outputTable.locator('tbody tr').allInnerTexts();
  const resetIDs = assertViewerPatientRows(resetRows, expectedIDs);
  recordCheck(report, 'correctness', 'full Viewer reload clears the transient Gender filter and returns both exact fixture Patients',
    resetIDs.length === expectedIDs.length, {
      rows: resetRows,
      patientIDs: resetIDs,
      expectedPatientIDs: expectedIDs,
      filterOmittedFromRequest: !hasGenderSelection(requestBody(genderResetResponse.request())),
      responseStatus: genderResetResponse.status(),
      sourceSHA256: sourceOracle.sha256,
    });

  await action('reload Gender facet values after reset', loadValues, () => loadValues.click(), {
    after: () => female.waitFor({ state: 'visible' }),
  });
  await expect(female).toHaveCount(1);
  await expect(female).toBeVisible();
  await expect(female).not.toBeChecked();
  recordCheck(report, 'usability', 'declared Gender facet remains available and unchecked after Viewer reload', true,
    { available: true, selected: false, filterColumn: genderBinding.column });

  await applyFemaleFilter('reapply female filter after Viewer reload');
  const reappliedRows = await outputTable.locator('tbody tr').allInnerTexts();
  const reappliedIDs = assertViewerPatientRows(reappliedRows, sourceOracle.femaleIDs);
  recordCheck(report, 'correctness', 'reapplying the Gender facet after reload restores only the exact female fixture Patient',
    reappliedIDs.length === 1, { rows: reappliedRows, patientIDs: reappliedIDs, expectedPatientIDs: sourceOracle.femaleIDs });

  const currentMode = new URL(page.url()).searchParams.get('mode');
  recordCheck(report, 'correctness', 'Viewer route remains represented in the URL', currentMode === 'viewer', { mode: currentMode });
  recordUntested(report, 'persistence', 'saved user-selected Gender filter preferences persist across sessions',
    'This workflow verifies that in-memory selection resets on full reload; saved user filter preferences are unsupported and remain unclaimed.');

  } finally {
    if (sourceBytesBefore) {
      try {
        const sourceAfter = createHash('sha256').update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
        recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during Viewer verification', sourceAfter === sourceBytesBefore,
          { before: sourceBytesBefore, after: sourceAfter });
      } catch (error) {
        recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during Viewer verification', false,
          { before: sourceBytesBefore, error: error.message });
      }
    }
  }
};
