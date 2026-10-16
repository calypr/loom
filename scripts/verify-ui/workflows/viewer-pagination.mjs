import assert from 'node:assert/strict';
import { expect } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { matchesViewerRequestBody } from './viewer-query.mjs';
import { recordUntested } from '../helpers/report.mjs';
import { waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';

const graphPath = '/graphql/graph';
const expectedFixtureIDs = [
  'page-patient-001',
  'page-patient-002',
  'page-patient-003',
  'page-patient-004',
  'page-patient-005',
  'page-patient-006',
];

const explorerRuntimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;

const requestBody = request => {
  try { return request.postDataJSON(); }
  catch { return null; }
};

const validPageIDs = (ids, expectedIDs, expectedCount) => Array.isArray(ids) &&
  ids.length === expectedCount && new Set(ids).size === ids.length &&
  ids.every(id => typeof id === 'string' && expectedIDs.includes(id));

export const assertViewerPaginationIDs = (ids, expectedIDs, expectedCount) => {
  assert.equal(Array.isArray(ids), true, 'Viewer page identities must be an array');
  assert.equal(ids.length, expectedCount, `Viewer page must contain exactly ${expectedCount} Patient identities`);
  assert.equal(new Set(ids).size, ids.length, 'Viewer page identities must not repeat');
  assert(ids.every(id => expectedIDs.includes(id)), 'Viewer page must contain only independent fixture Patient identities');
  return [...ids];
};

export const matchesViewerPaginationRequest = (request, { origin, project, selector, first, after }) => {
  try {
    const url = new URL(request.url());
    const body = requestBody(request);
    const input = body?.variables?.input;
    const scopeMatches = selector
      ? matchesViewerRequestBody(body, { project, selector })
      : input?.projectId === project && Boolean(input?.selector?.recipe) &&
        Boolean(input?.selector?.translationVersion) && Boolean(input?.selector?.output);
    const cursorMatches = after === undefined
      ? Boolean(input) && !Object.hasOwn(input, 'after')
      : input?.after === after;
    const unsorted = input?.sort === undefined || input.sort === null;
    return request.method() === 'POST' && url.origin === origin && url.pathname === graphPath &&
      scopeMatches && input?.first === first && cursorMatches && unsorted;
  } catch {
    return false;
  }
};

const readPatientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const records = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const ids = records.map(record => record.id);
  assert.deepEqual(ids, expectedFixtureIDs, 'Viewer pagination source oracle must contain exactly six literal Patient IDs');
  assert(records.every(record => record.resourceType === 'Patient'), 'Viewer pagination fixture records must be Patients');
  return { path, sha256: createHash('sha256').update(bytes).digest('hex'), ids };
};

const rowIDs = (rows, expectedIDs, expectedCount) => {
  const ids = rows.map(text => expectedIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`);
  return assertViewerPaginationIDs(ids, expectedIDs, expectedCount);
};

const sameIDs = (left, right) => left.length === right.length &&
  [...left].sort().every((id, index) => id === [...right].sort()[index]);

export const viewerPaginationPartitionMatches = (firstPageIDs, secondPageIDs, expectedIDs) => {
  const allIDs = [...firstPageIDs, ...secondPageIDs];
  return firstPageIDs.length + secondPageIDs.length === expectedIDs.length &&
    new Set(firstPageIDs).size === firstPageIDs.length &&
    new Set(secondPageIDs).size === secondPageIDs.length &&
    firstPageIDs.every(id => expectedIDs.includes(id)) &&
    secondPageIDs.every(id => expectedIDs.includes(id)) &&
    new Set(allIDs).size === allIDs.length && sameIDs(allIDs, expectedIDs);
};

const captureResponse = (page, predicate) => page.waitForResponse(predicate, { timeout: 5000 }).then(
  response => ({ response }),
  error => ({ error }),
);

const requireResponse = async (capture, label) => {
  const observed = await capture;
  assert(observed.response, `${label} did not return a matching response: ${observed.error?.message ?? 'no response'}`);
  const failure = await observed.response.finished();
  assert.equal(failure, null, `${label} response did not finish cleanly`);
  return observed.response;
};

const decodePage = async ({ response, project, selector, origin, first, after, idColumn, expectedIDs, expectedCount, expectedHasNext, check, checkName }) => {
  const request = response.request();
  const requestMatches = matchesViewerPaginationRequest(request, { origin, project, selector, first, after });
  const status = response.status();
  const payload = await response.json();
  const graphErrors = Array.isArray(payload.errors) ? payload.errors : [];
  const connection = payload.data?.dataframeRows;
  const columns = Array.isArray(connection?.columns) ? connection.columns : [];
  const rows = Array.isArray(connection?.rows) ? connection.rows : [];
  const columnIndex = columns.indexOf(idColumn);
  const ids = columnIndex < 0 ? [] : rows.map(row => Array.isArray(row) ? row[columnIndex] : row?.[idColumn]);
  const pageInfo = connection?.pageInfo;
  const idsMatch = validPageIDs(ids, expectedIDs, expectedCount);
  const pageInfoMatches = typeof pageInfo?.hasNextPage === 'boolean' && pageInfo.hasNextPage === expectedHasNext &&
    (!expectedHasNext || (typeof pageInfo.endCursor === 'string' && pageInfo.endCursor.length > 0));
  const passed = status >= 200 && status < 300 && requestMatches && graphErrors.length === 0 &&
    columns.length > 0 && columnIndex >= 0 && idsMatch && pageInfoMatches;
  check('correctness', checkName, passed, {
    status,
    requestMatches,
    first,
    afterPresent: after !== undefined,
    afterMatches: after === undefined ? !Object.hasOwn(requestBody(request)?.variables?.input ?? {}, 'after') : requestBody(request)?.variables?.input?.after === after,
    idColumn,
    patientIDs: ids,
    expectedCount,
    expectedHasNext,
    endCursorPresent: typeof pageInfo?.endCursor === 'string' && pageInfo.endCursor.length > 0,
    graphErrorCount: graphErrors.length,
  });
  return { ids, pageInfo };
};

const verifyRenderedPage = async ({ page, outputTable, expectedIDs, expectedCount, responseIDs, check, checkName }) => {
  await page.waitForFunction(count => document.querySelector('table[aria-label$=" results"] tbody')?.querySelectorAll('tr').length === count,
    expectedCount, { timeout: 5000 });
  const rows = await outputTable.locator('tbody tr').allInnerTexts();
  const ids = rowIDs(rows, expectedIDs, expectedCount);
  check('correctness', checkName, sameIDs(ids, responseIDs), {
    patientIDs: ids,
    responsePatientIDs: responseIDs,
    expectedCount,
  });
  return ids;
};

const verifyPager = async ({ page, previous, next, pageNumber, previousDisabled, nextDisabled, check, checkName }) => {
  await expect(page.getByText(`Page ${pageNumber}`, { exact: true })).toBeVisible({ timeout: 5000 });
  await expect(previous).toHaveCount(1);
  await expect(next).toHaveCount(1);
  if (previousDisabled) await expect(previous).toBeDisabled();
  else await expect(previous).toBeEnabled();
  if (nextDisabled) await expect(next).toBeDisabled();
  else await expect(next).toBeEnabled();
  check('usability', checkName, true, { page: pageNumber, previousDisabled, nextDisabled });
};

export const viewerPaginationWorkflow = async ({ page, report, action, check }, context) => {
  const target = context.target;
  const project = target.fixtureProject;
  const uiOrigin = new URL(target.uiUrl).origin;
  const oracle = readPatientOracle(target);
  const expectedIDs = oracle.ids;
  const expectedPreviewRows = expectedIDs.map(id => ({ 'Patient ID': id }));
  const sourceSHA256 = oracle.sha256;
  check('correctness', 'Viewer pagination fixture contains the six exact independent Patient IDs', true,
    { path: oracle.path, project, generation: target.fixtureGeneration, patientIDs: expectedIDs, sha256: sourceSHA256 });
  report.target.fixtureOracle = { path: oracle.path, project, generation: target.fixtureGeneration, patientIDs: expectedIDs, sha256: sourceSHA256 };

  let explorer = target.bootstrapExplorerId;
  try {
    await page.goto(browserURL(target, project, explorer, 'builder'), { waitUntil: 'domcontentloaded' });
    const newExplorer = page.getByText('New explorer', { exact: true });
    await action('open Viewer pagination Explorer creation', newExplorer, () => newExplorer.click(), {
      after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    });
    const title = `Verify ${context.runID.slice(-10)} pagination`;
    const explorerName = page.locator('#new-explorer-name');
    await action('name Viewer pagination Explorer', explorerName, () => explorerName.fill(title), { editable: true });
    const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
    await action('create blank Viewer pagination Explorer', createBlank, () => createBlank.click(), {
      after: async () => {
        const picker = page.getByRole('combobox', { name: 'Explorer' });
        await picker.waitFor({ state: 'visible' });
        await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected, title);
      },
    });
    explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
    assert(explorer && explorer !== target.bootstrapExplorerId, 'Pagination setup must create a fresh Explorer');
    report.target.explorer = explorer;

    const tableName = page.locator('#first-table-name');
    await action('name Viewer pagination Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create six-Patient table and render Preview', choosePatients, () => choosePatients.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
        await page.waitForFunction(count => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(count), expectedIDs.length + 1, { timeout: 5000 });
        await waitForBuilderRenderedGrid(page, {
          tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
          expectedRows: expectedPreviewRows,
        });
      },
    });
    const publishPath = `${explorerRuntimePath(project, explorer)}/authoring/v2/publish`;
    const publishCapture = captureResponse(page, response => {
      const url = new URL(response.url());
      return response.request().method() === 'POST' && url.origin === uiOrigin && url.pathname === publishPath;
    });
    const publish = page.getByRole('button', { name: 'Publish', exact: true });
    await action('publish Viewer pagination Explorer', publish, () => publish.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await Promise.all([
          publishCapture.then(async observed => {
            assert(observed.response, `Pagination Publish did not return: ${observed.error?.message ?? 'no response'}`);
            await expect(publish).toBeDisabled({ timeout: 5000 });
          }),
        ]);
      },
    });
    const published = await requireResponse(publishCapture, 'Viewer pagination Publish');
    check('correctness', 'Viewer pagination Publish returned success', published.status() >= 200 && published.status() < 300,
      { status: published.status(), path: new URL(published.url()).pathname, explorer });
  } catch (error) {
    report.failureTrace ??= { message: error?.message ?? String(error), explorer };
    throw error;
  }

  report.target.project = project;
  report.target.explorer = explorer;
  const runtimePath = explorerRuntimePath(project, explorer);
  const runtimeCapture = captureResponse(page, response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === runtimePath);
  const initialCapture = captureResponse(page, response => matchesViewerPaginationRequest(response.request(), {
    origin: uiOrigin, project, first: 20,
  }));
  let selector;
  let patientIDColumn;
  let outputTable;
  let pageSizeControl;
  let previous;
  let next;
  await action('open published Viewer and render its initial page', page.getByRole('combobox', { name: 'Explorer' }),
    () => page.goto(browserURL(target, project, explorer, 'viewer'), { waitUntil: 'domcontentloaded' }), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        const runtimeResponse = await requireResponse(runtimeCapture, 'Published Viewer runtime');
        assert(runtimeResponse.ok(), `Viewer pagination runtime GET failed with HTTP ${runtimeResponse.status()}`);
        const runtimeEnvelope = await runtimeResponse.json();
        const runtime = runtimeEnvelope.runtime;
        assert(runtime && Array.isArray(runtime.outputs) && runtime.outputs.length > 0, 'Pagination Viewer runtime must expose its published outputs');
        const generation = runtime.generation ?? runtime.publication?.generation;
        check('correctness', 'published pagination Viewer retains the fixture generation', generation === target.fixtureGeneration,
          { generation, expectedGeneration: target.fixtureGeneration, project, explorer });

        const initialResponse = await requireResponse(initialCapture, 'Initial Viewer page');
        const initialInput = requestBody(initialResponse.request())?.variables?.input;
        const output = runtime.outputs.find(candidate => matchesViewerRequestBody({ variables: { input: initialInput } }, {
          project,
          selector: candidate.selector,
        }));
        assert(output, 'Initial pagination query must use a selector from this exact published Explorer runtime');
        const patientIDColumns = output.columns.filter(column => column.label === 'Patient ID');
        assert.equal(patientIDColumns.length, 1, 'Published pagination output must expose one Patient ID column');
        patientIDColumn = patientIDColumns[0].column;
        report.target.viewerOutputId = output.outputId;
        selector = output.selector;
        outputTable = page.locator('table[aria-label$=" results"]');
        await outputTable.waitFor({ state: 'visible', timeout: 5000 });
        pageSizeControl = page.getByRole('textbox', { name: 'Rows per page' });
        previous = page.getByRole('button', { name: 'Previous', exact: true });
        next = page.getByRole('button', { name: 'Next', exact: true });
        await expect(pageSizeControl).toHaveValue('20');
        const initialConnection = await decodePage({
          response: initialResponse, project, selector, origin: uiOrigin, first: 20, after: undefined,
          idColumn: patientIDColumn, expectedIDs, expectedCount: 6, expectedHasNext: false,
          check, checkName: 'initial Viewer page response uses the exact output and default first 20',
        });
        await verifyRenderedPage({
          page, outputTable, expectedIDs, expectedCount: 6, responseIDs: initialConnection.ids,
          check, checkName: 'initial Viewer page renders all six independent Patient IDs',
        });
        await verifyPager({ page, previous, next, pageNumber: 1, previousDisabled: true, nextDisabled: true,
          check, checkName: 'Viewer starts on page one with both navigation buttons disabled' });
      },
    });

  const selectPageSize = async ({ size, label, responseCheck, renderCheck, pagerCheck, expectedCount, expectedHasNext }) => {
    const responseCapture = captureResponse(page, response => matchesViewerPaginationRequest(response.request(), {
      origin: uiOrigin, project, selector, first: size,
    }));
    let result;
    await action(label, pageSizeControl, async control => {
      await control.click();
      const option = page.getByRole('option', { name: String(size), exact: true });
      await expect(option).toBeVisible({ timeout: 5000 });
      await option.click();
    }, {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        const response = await requireResponse(responseCapture, label);
        result = await decodePage({ response, project, selector, origin: uiOrigin, first: size, after: undefined,
          idColumn: patientIDColumn, expectedIDs, expectedCount, expectedHasNext, check, checkName: responseCheck });
        await expect(pageSizeControl).toHaveValue(String(size), { timeout: 5000 });
        const ids = await verifyRenderedPage({ page, outputTable, expectedIDs, expectedCount,
          responseIDs: result.ids, check, checkName: renderCheck });
        await verifyPager({ page, previous, next, pageNumber: 1, previousDisabled: true,
          nextDisabled: !expectedHasNext, check, checkName: pagerCheck });
        result = { ...result, renderedIDs: ids, response };
      },
    });
    return result;
  };

  const firstPage = await selectPageSize({
    size: 5,
    label: 'select five rows per page',
    responseCheck: 'Viewer first page-size change requests five rows from the first cursor',
    renderCheck: 'Viewer first page renders five unique fixture Patient IDs',
    pagerCheck: 'Viewer page one exposes only the next action at five rows',
    expectedCount: 5,
    expectedHasNext: true,
  });
  const firstPageIDs = firstPage.renderedIDs;
  const nextCursor = firstPage.pageInfo.endCursor;
  check('correctness', 'Viewer first page has a usable cursor for the remaining Patient',
    typeof nextCursor === 'string' && nextCursor.length > 0,
    { hasNextPage: firstPage.pageInfo.hasNextPage, endCursorPresent: Boolean(nextCursor) });

  const goToPageTwo = async ({ label, requestCheck, renderCheck, pagerCheck, cursor = nextCursor }) => {
    const responseCapture = captureResponse(page, response => matchesViewerPaginationRequest(response.request(), {
      origin: uiOrigin, project, selector, first: 5, after: cursor,
    }));
    let result;
    await action(label, next, () => next.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        const response = await requireResponse(responseCapture, label);
        result = await decodePage({ response, project, selector, origin: uiOrigin, first: 5, after: cursor,
          idColumn: patientIDColumn, expectedIDs, expectedCount: 1, expectedHasNext: false, check, checkName: requestCheck });
        result.renderedIDs = await verifyRenderedPage({ page, outputTable, expectedIDs, expectedCount: 1,
          responseIDs: result.ids, check, checkName: renderCheck });
        await verifyPager({ page, previous, next, pageNumber: 2, previousDisabled: false,
          nextDisabled: true, check, checkName: pagerCheck });
        result.response = response;
      },
    });
    return result;
  };

  const secondPage = await goToPageTwo({
    label: 'navigate to Viewer page two',
    requestCheck: 'Viewer Next request uses the exact first-page cursor and published output',
    renderCheck: 'Viewer second page contains only the remaining fixture Patient ID',
    pagerCheck: 'Viewer page two exposes Previous and disables Next',
  });
  const remainingIDs = expectedIDs.filter(id => !firstPageIDs.includes(id));
  check('correctness', 'Viewer cursor pages partition all six independent Patient IDs without overlap',
    viewerPaginationPartitionMatches(firstPageIDs, secondPage.renderedIDs, expectedIDs) &&
      sameIDs(secondPage.renderedIDs, remainingIDs),
    { firstPageIDs, secondPageIDs: secondPage.renderedIDs, expectedIDs, remainingIDs });

  const previousCapture = captureResponse(page, response => matchesViewerPaginationRequest(response.request(), {
    origin: uiOrigin, project, selector, first: 5, after: undefined,
  }));
  let previousPage;
  await action('return to Viewer page one', previous, () => previous.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      const response = await requireResponse(previousCapture, 'Previous page');
      previousPage = await decodePage({ response, project, selector, origin: uiOrigin, first: 5, after: undefined,
        idColumn: patientIDColumn, expectedIDs, expectedCount: 5, expectedHasNext: true, check,
        checkName: 'Viewer Previous request returns the exact first five identities' });
      previousPage.renderedIDs = await verifyRenderedPage({ page, outputTable, expectedIDs, expectedCount: 5,
        responseIDs: previousPage.ids, check, checkName: 'Viewer Previous renders the same first-page identities' });
      check('correctness', 'Viewer Previous restores the captured first-page Patient IDs', sameIDs(previousPage.renderedIDs, firstPageIDs),
        { previousPageIDs: previousPage.renderedIDs, initialPageIDs: firstPageIDs });
      await verifyPager({ page, previous, next, pageNumber: 1, previousDisabled: true, nextDisabled: false,
        check, checkName: 'Viewer Previous restores page-one navigation controls' });
    },
  });

  const repeatedPageTwo = await goToPageTwo({
    label: 'repeat Next to Viewer page two',
    requestCheck: 'Viewer repeated Next request uses the current first-page cursor',
    renderCheck: 'Viewer repeated Next returns the same remaining Patient ID',
    pagerCheck: 'Viewer repeated Next restores page-two navigation controls',
    cursor: previousPage.pageInfo.endCursor,
  });
  check('correctness', 'Viewer repeated Next preserves the captured second-page identity',
    sameIDs(repeatedPageTwo.renderedIDs, secondPage.renderedIDs),
    { repeatedPageTwoIDs: repeatedPageTwo.renderedIDs, initialPageTwoIDs: secondPage.renderedIDs });

  await selectPageSize({
    size: 10,
    label: 'change Viewer page size to ten on page two',
    responseCheck: 'Viewer page-size change on page two resets to page one with first ten',
    renderCheck: 'Viewer page-size change renders all six independent Patient IDs',
    pagerCheck: 'Viewer ten-row page reset disables both navigation buttons',
    expectedCount: 6,
    expectedHasNext: false,
  });

  const restoredFirstPage = await selectPageSize({
    size: 5,
    label: 'restore five rows per page before reload',
    responseCheck: 'Viewer five-row page is restored before the reload check',
    renderCheck: 'Viewer restored first page retains five independent Patient IDs',
    pagerCheck: 'Viewer restored five-row page exposes only Next',
    expectedCount: 5,
    expectedHasNext: true,
  });
  await goToPageTwo({
    label: 'navigate to Viewer page two before reload',
    requestCheck: 'Viewer reaches the exact second cursor page before reload',
    renderCheck: 'Viewer second page retains the remaining Patient before reload',
    pagerCheck: 'Viewer is on page two before reload',
    cursor: restoredFirstPage.pageInfo.endCursor,
  });

  const reloadCapture = captureResponse(page, response => matchesViewerPaginationRequest(response.request(), {
    origin: uiOrigin, project, selector, first: 20,
  }));
  await action('reload Viewer from page two', outputTable, () => page.reload({ waitUntil: 'domcontentloaded' }), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      const response = await requireResponse(reloadCapture, 'Viewer reload');
      const reloaded = await decodePage({ response, project, selector, origin: uiOrigin, first: 20, after: undefined,
        idColumn: patientIDColumn, expectedIDs, expectedCount: 6, expectedHasNext: false, check,
        checkName: 'Viewer reload request resets to default first twenty without a cursor' });
      const reloadedIDs = await verifyRenderedPage({ page, outputTable, expectedIDs, expectedCount: 6,
        responseIDs: reloaded.ids, check, checkName: 'Viewer reload renders all six independent Patient IDs on page one' });
      const pageSizeAfterReload = await page.getByRole('textbox', { name: 'Rows per page' }).inputValue();
      check('correctness', 'Viewer reload resets current page and page size to the source-defined defaults',
        pageSizeAfterReload === '20' && reloadedIDs.length === 6,
        { pageSize: pageSizeAfterReload, pageNumber: 1, pageSizeDefault: 20, patientIDs: reloadedIDs });
      await verifyPager({ page, previous, next, pageNumber: 1, previousDisabled: true, nextDisabled: true,
        check, checkName: 'Viewer reload restores initial page navigation controls' });
    },
  });

  const sourceSHA256After = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'Viewer pagination Patient fixture stayed unchanged', sourceSHA256After === sourceSHA256,
    { expectedSHA256: sourceSHA256, actualSHA256: sourceSHA256After, patientIDs: expectedIDs });
  recordUntested(report, 'persistence', 'Viewer page and page-size choices survive a full reload',
    'This case verifies the source-defined reset to page one and default page size twenty; it does not claim pagination state persistence.');
};
