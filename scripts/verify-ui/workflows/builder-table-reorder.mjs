import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { assertVisibleRowsMatchOracle, readNDJSONResourceIdentityOracle } from '../helpers/ndjson-resource-oracle.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const ACTION_BUDGET_MS = 5_000;

const remainingBudgetMs = (startedAt, label) => {
  const remaining = ACTION_BUDGET_MS - (Date.now() - startedAt);
  assert(remaining > 0, `${label} exhausted its ${ACTION_BUDGET_MS} ms budget`);
  return remaining;
};

const selectedExplorerFromURL = page => new URL(page.url()).searchParams.get('explorer');

const visibleTableTabs = page => page.locator('button[data-testid^="construction-table-"]').evaluateAll(buttons =>
  buttons.map(button => ({
    outputId: button.dataset.testid.slice('construction-table-'.length),
    title: button.querySelector('span:last-child')?.textContent?.trim() ?? button.innerText.trim(),
    selected: button.getAttribute('aria-current') === 'page',
  })));

const activeOutputId = async (page, timeoutMs = ACTION_BUDGET_MS) => {
  const selected = page.locator('button[data-testid^="construction-table-"][aria-current="page"]');
  assert.equal(await selected.count(), 1, 'Builder must expose exactly one selected output tab');
  const testId = await selected.getAttribute('data-testid', { timeout: timeoutMs });
  assert(testId?.startsWith('construction-table-'), 'Selected Builder tab must expose its output identity');
  return testId.slice('construction-table-'.length);
};

const waitForPreview = async (page, outputId, sourceCount, timeoutMs = ACTION_BUDGET_MS) => {
  await page.waitForFunction(({ expectedOutputId, expectedRows }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return preview?.dataset.previewStatus === 'ready'
      && preview.dataset.previewOutputId === expectedOutputId
      && table?.getAttribute('aria-rowcount') === String(expectedRows + 1)
      && !document.body.innerText.includes('Loading your table…')
      && !document.body.innerText.includes('Preview failed:');
  }, { expectedOutputId: outputId, expectedRows: sourceCount }, { timeout: timeoutMs });
};

const assertPreviewRows = async (page, outputId, oracle, label, timeoutMs = ACTION_BUDGET_MS) => {
  assert.equal(await activeOutputId(page, timeoutMs), outputId, `${label} must keep its exact output tab selected`);
  const grid = page.locator('[data-testid="preview-table-scroll"] [role="table"]');
  assert.equal(await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-output-id', { timeout: timeoutMs }), outputId,
    `${label} preview must belong to its exact output`);
  assert.equal(await grid.getAttribute('aria-colcount', { timeout: timeoutMs }), '1', `${label} preview must render only its direct ID column`);
  const ariaRowCount = await grid.getAttribute('aria-rowcount', { timeout: timeoutMs });
  const rows = await grid.locator('[role="row"]').evaluateAll(elements => elements.slice(1).map(row => ({
    ordinal: Number(row.firstElementChild?.innerText.trim()),
    cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
  })).filter(row => row.cells.length));
  assert.equal(rows.length, oracle.count, `${label} must render every independent source row`);
  return assertVisibleRowsMatchOracle({ rows, sourceIds: oracle.ids, ariaRowCount });
};

const readBuilder = async (page, target, explorerId, timeoutMs = ACTION_BUDGET_MS) => {
  const path = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`;
  const response = await page.context().request.get(`${target.apiUrl}${path}`, { timeout: timeoutMs });
  let value;
  try { value = await response.json(); }
  catch (error) { throw new Error(`Saved Builder read returned HTTP ${response.status()} without JSON: ${error.message}`); }
  assert(response.ok(), `Saved Builder read returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 900)}`);
  return value;
};

const orderedSavedTables = builder => {
  const documents = new Map((builder.workspace?.documents ?? []).map(document => [document.output?.id, document]));
  return [...(builder.workspace?.tabs ?? [])]
    .sort((left, right) => left.order - right.order)
    .map(tab => {
      const document = documents.get(tab.outputId);
      assert(document, `Saved table tab ${tab.outputId} must have an output document`);
      return {
        outputId: tab.outputId,
        tabId: tab.id,
        title: document.output?.title,
        tabTitle: tab.title,
        rootResourceType: document.rootResourceType,
        order: tab.order,
      };
    });
};

const assertSavedBindings = (builder, target, expected) => {
  assert.equal(builder.catalog?.generation, target.fixtureGeneration,
    'Saved Builder must use the exact Basic fixture generation');
  assert.equal(builder.workspace?.documents?.length, expected.length,
    'Saved Builder must contain exactly the authored Patient and Observation outputs');
  assert.equal(builder.workspace?.tabs?.length, expected.length,
    'Saved Builder must retain exactly one tab for each authored output');
  const tables = orderedSavedTables(builder);
  assert.deepEqual(tables.map(table => table.outputId), expected.map(table => table.outputId),
    'Saved table tabs must have the exact expected output order');
  assert.deepEqual(tables.map(table => table.order), expected.map((_, index) => index),
    'Saved table order values must be contiguous from zero');
  assert.equal(new Set(tables.map(table => table.tabId)).size, expected.length,
    'Saved table tab identities must be unique');
  assert.equal(new Set(tables.map(table => table.outputId)).size, expected.length,
    'Saved output identities must be unique');

  for (const [index, expectedTable] of expected.entries()) {
    const actual = tables[index];
    assert(typeof expectedTable.tabId === 'string' && expectedTable.tabId,
      `${expectedTable.title} must have a saved table tab identity`);
    assert.equal(actual.outputId, expectedTable.outputId, `${expectedTable.title} must retain its output identity`);
    assert.equal(actual.tabId, expectedTable.tabId, `${expectedTable.title} must retain its tab identity`);
    assert.equal(actual.title, expectedTable.title, `${expectedTable.title} must retain its saved output title`);
    assert.equal(actual.tabTitle, expectedTable.title, `${expectedTable.title} must retain its saved tab title`);
    assert.equal(actual.rootResourceType, expectedTable.rootResourceType,
      `${expectedTable.title} must retain its exact root resource type`);

    const document = builder.workspace.documents.find(candidate => candidate.output?.id === expectedTable.outputId);
    const directRootIDs = (document.columns ?? []).filter(column => column.occurrenceId === 'base'
      && column.source?.kind === 'field'
      && column.source.field?.path === 'id'
      && column.source.field?.projectionMode === 'VALUE');
    assert.equal(directRootIDs.length, 1,
      `${expectedTable.title} must retain exactly one direct root-resource ID binding`);
  }
  return tables;
};

export const builderTableReorderWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  const target = context.target;
  configureNativePage(page);

  const patientPath = join(target.fixtureDir, 'Patient.ndjson');
  const observationPath = join(target.fixtureDir, 'Observation.ndjson');
  assert.equal(existsSync(join(target.fixtureDir, 'j01-concepts.fixture.json')), false,
    'dedicated table-reorder fixture must not request J01 Observation augmentation');
  const [patientOracle, observationOracle] = await Promise.all([
    readNDJSONResourceIdentityOracle({
      path: patientPath, project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Patient',
    }),
    readNDJSONResourceIdentityOracle({
      path: observationPath, project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Observation',
    }),
  ]);
  assert.equal(patientOracle.count, 2, 'Basic Patient source must contain exactly two records');
  assert.deepEqual([...patientOracle.ids].sort(), ['dev-patient-001', 'dev-patient-002'],
    'Basic Patient source must contain the exact two fixture IDs');
  assert.equal(observationOracle.count, 6, 'Basic Observation source must contain exactly six records');
  assert.deepEqual([...observationOracle.ids].sort(), [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
    'dev-pair-001', 'dev-pair-002', 'dev-pair-003',
  ], 'Basic Observation source must contain the exact six fixture IDs');
  assert.equal(context.custom, false, 'Table reorder must run against the registered Basic fixture target');
  assert.equal(context.seed?.fresh, true, 'Table reorder must use a fresh disposable Basic fixture project');
  assert(target.fixtureProject.startsWith('loom_dev_verify_'), 'Table reorder must use the exact disposable Basic QA project');
  report.target.fixtureOracle = {
    project: target.fixtureProject,
    canonicalProject: canonicalProjectID(target.fixtureProject),
    generation: target.fixtureGeneration,
    patient: { path: patientOracle.path, sha256: patientOracle.sha256, count: patientOracle.count, ids: patientOracle.ids },
    observation: {
      path: observationOracle.path, sha256: observationOracle.sha256,
      count: observationOracle.count, ids: observationOracle.ids,
    },
  };
  check('correctness', 'fresh Basic fixture exposes exact independent Patient and Observation source identities', true, {
    project: target.fixtureProject, generation: target.fixtureGeneration,
    patientIDs: patientOracle.ids, observationIDs: observationOracle.ids,
    patientSHA256: patientOracle.sha256, observationSHA256: observationOracle.sha256,
  });

  const explorerTitle = `Verify ${context.runID.slice(-10)} table reorder`;
  const patientTitle = 'Reorder QA Patients';
  const observationTitle = 'Reorder QA Observations';
  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS,
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.value === expected,
    target.bootstrapExplorerId, { timeout: ACTION_BUDGET_MS });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await action('open native Explorer creation for table reorder', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await action('name the disposable table-reorder Explorer', explorerName,
    () => explorerName.fill(explorerTitle), { editable: true });
  const createExplorerResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerCollectionPath
      && body?.title === explorerTitle;
  }, { timeout: ACTION_BUDGET_MS });
  let createdExplorer;
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create and select the disposable table-reorder Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createExplorerResponse;
      let body;
      try { body = await response.json(); }
      catch (error) { throw new Error(`Native Explorer creation returned HTTP ${response.status()} without JSON: ${error.message}`); }
      assert(response.ok(), `Native Explorer creation returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 900)}`);
      createdExplorer = body;
      assert.equal(createdExplorer.project, canonicalProjectID(target.fixtureProject),
        'Native Explorer creation must remain in the exact Basic fixture project');
      assert.equal(createdExplorer.title, explorerTitle, 'Native Explorer must retain its distinct requested title');
      assert(typeof createdExplorer.explorerId === 'string' && createdExplorer.explorerId,
        'Native Explorer creation must return its server-assigned identity');
      assert.notEqual(createdExplorer.explorerId, target.bootstrapExplorerId,
        'Native Explorer must be distinct from the fixture bootstrap Explorer');
      await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.value === expected,
        createdExplorer.explorerId, { timeout: ACTION_BUDGET_MS });
      assert.equal(selectedExplorerFromURL(page), createdExplorer.explorerId,
        'Native Explorer creation must select its exact server-assigned URL identity');
    },
    requiredCheck: 'native Explorer creation selects a distinct Explorer in the exact Basic fixture project',
  });
  const explorerId = await explorerControl.inputValue();
  assert.equal(explorerId, createdExplorer.explorerId, 'Workflow must continue in the exact newly created Explorer');
  report.target.explorer = explorerId;
  report.target.nativeMutationInventory = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorerId,
    setup: ['fresh dedicated Basic two-resource fixture project and bootstrap Explorer from the fixture harness', 'native New explorer → Create blank'],
    authoring: [],
    cleanup: [],
  };

  const tableName = page.locator('#first-table-name');
  await action('name the native Patient root table', tableName, () => tableName.fill(patientTitle), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  let patientOutputId;
  await action('create Patient rows through the populated native root picker', choosePatients,
    () => choosePatients.click(), {
      after: async () => {
        await page.waitForFunction(() => document.querySelectorAll('button[data-testid^="construction-table-"]').length === 1,
          null, { timeout: ACTION_BUDGET_MS });
        const tabs = await visibleTableTabs(page);
        assert.equal(tabs[0].title, patientTitle, 'Native Patient root picker must create the named Patient tab');
        patientOutputId = tabs[0].outputId;
        await waitForPreview(page, patientOutputId, patientOracle.count);
        await assertPreviewRows(page, patientOutputId, patientOracle, 'Patient root table');
      },
      requiredCheck: 'native Patient root table renders its exact independent IDs within five seconds',
    });
  let builder = await readBuilder(page, target, explorerId);
  let savedTables = assertSavedBindings(builder, target, [
    { outputId: patientOutputId, tabId: orderedSavedTables(builder)[0]?.tabId, title: patientTitle, rootResourceType: 'Patient' },
  ]);
  const patientTabId = savedTables[0].tabId;
  check('correctness', 'native Patient root table saves its unique output, title, direct root binding, and exact visible IDs', true, {
    outputId: patientOutputId, tabId: patientTabId, title: patientTitle,
    rootResourceType: savedTables[0].rootResourceType, ids: patientOracle.ids,
    project: target.fixtureProject, generation: target.fixtureGeneration,
  });
  report.target.nativeMutationInventory.authoring.push({
    rootResourceType: 'Patient', title: patientTitle, outputId: patientOutputId,
    controls: ['#first-table-name', 'Choose Patient rows'],
  });

  const newTable = page.getByRole('button', { name: 'New table', exact: true });
  await action('open the native New table root picker', newTable, () => newTable.click(), {
    after: () => page.locator('#first-table-name').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  await action('name the native Observation root table', tableName, () => tableName.fill(observationTitle), { editable: true });
  const chooseObservations = page.getByRole('button', { name: 'Choose Observation rows', exact: true });
  let observationOutputId;
  await action('create Observation rows through the populated native root picker', chooseObservations,
    () => chooseObservations.click(), {
      after: async () => {
        await page.waitForFunction(() => document.querySelectorAll('button[data-testid^="construction-table-"]').length === 2,
          null, { timeout: ACTION_BUDGET_MS });
        const tabs = await visibleTableTabs(page);
        assert.deepEqual(tabs.map(tab => tab.title), [patientTitle, observationTitle],
          'Native Observation root picker must add its named tab after Patient');
        observationOutputId = tabs.find(tab => tab.title === observationTitle)?.outputId;
        assert(observationOutputId && observationOutputId !== patientOutputId,
          'Native Observation table must receive a unique output identity');
        await waitForPreview(page, observationOutputId, observationOracle.count);
        await assertPreviewRows(page, observationOutputId, observationOracle, 'Observation root table');
      },
      requiredCheck: 'native Observation root table renders its exact independent IDs within five seconds',
    });
  builder = await readBuilder(page, target, explorerId);
  savedTables = assertSavedBindings(builder, target, [
    { outputId: patientOutputId, tabId: patientTabId, title: patientTitle, rootResourceType: 'Patient' },
    { outputId: observationOutputId, tabId: orderedSavedTables(builder).find(table => table.outputId === observationOutputId)?.tabId,
      title: observationTitle, rootResourceType: 'Observation' },
  ]);
  const observationTabId = savedTables[1].tabId;
  assert.deepEqual((await visibleTableTabs(page)).map(tab => tab.outputId), [patientOutputId, observationOutputId],
    'Visible native Builder tabs must initially order Patient before Observation');
  check('correctness', 'native Observation root table saves a distinct output, title, direct root binding, and exact visible IDs', true, {
    outputId: observationOutputId, tabId: observationTabId, title: observationTitle,
    rootResourceType: savedTables[1].rootResourceType, ids: observationOracle.ids,
    project: target.fixtureProject, generation: target.fixtureGeneration,
  });
  check('correctness', 'initial saved workspace orders the exact Patient and Observation tab identities', true, {
    tables: savedTables,
  });
  report.target.nativeMutationInventory.authoring.push({
    rootResourceType: 'Observation', title: observationTitle, outputId: observationOutputId,
    controls: ['New table', '#first-table-name', 'Choose Observation rows'],
  });

  const moveUp = page.getByRole('button', { name: `Move ${observationTitle} up`, exact: true });
  await action('move Observation above Patient and render its exact rows', moveUp, () => moveUp.click(), {
    after: async () => {
      await page.waitForFunction(({ expectedIds, selectedId, expectedRows }) => {
        const tabs = [...document.querySelectorAll('button[data-testid^="construction-table-"]')];
        const outputIds = tabs.map(button => button.dataset.testid.slice('construction-table-'.length));
        const selected = tabs.filter(button => button.getAttribute('aria-current') === 'page');
        const preview = document.querySelector('[data-testid="construction-preview"]');
        const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
        return JSON.stringify(outputIds) === JSON.stringify(expectedIds)
          && selected.length === 1
          && selected[0].dataset.testid === `construction-table-${selectedId}`
          && preview?.dataset.previewStatus === 'ready'
          && preview.dataset.previewOutputId === selectedId
          && table?.getAttribute('aria-rowcount') === String(expectedRows + 1);
      }, {
        expectedIds: [observationOutputId, patientOutputId],
        selectedId: observationOutputId,
        expectedRows: observationOracle.count,
      }, { timeout: ACTION_BUDGET_MS });
      await assertPreviewRows(page, observationOutputId, observationOracle, 'reordered Observation table');
    },
    requiredCheck: 'native Move-up reorders Observation before Patient while retaining the Observation output and exact preview within five seconds',
  });
  const reorderedVisibleIds = (await visibleTableTabs(page)).map(tab => tab.outputId);
  assert.deepEqual(reorderedVisibleIds, [observationOutputId, patientOutputId],
    'Native Move-up must put Observation before Patient in the visible table order');
  assert.equal(await activeOutputId(page), observationOutputId,
    'Native Move-up must retain the selected Observation output');
  builder = await readBuilder(page, target, explorerId);
  const reorderedTables = assertSavedBindings(builder, target, [
    { outputId: observationOutputId, tabId: observationTabId, title: observationTitle, rootResourceType: 'Observation' },
    { outputId: patientOutputId, tabId: patientTabId, title: patientTitle, rootResourceType: 'Patient' },
  ]);
  check('correctness', 'native Move-up saves Observation before Patient with unchanged exact output and tab identities', true, {
    visibleOutputIds: reorderedVisibleIds, savedTables: reorderedTables, activeOutputId: observationOutputId,
  });

  const reloadStartedAt = Date.now();
  const remainingReloadMs = () => remainingBudgetMs(reloadStartedAt, 'Reloading reordered tables');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingReloadMs() });
  await page.waitForFunction(expectedIds => {
    const tabs = [...document.querySelectorAll('button[data-testid^="construction-table-"]')];
    const outputIds = tabs.map(button => button.dataset.testid.slice('construction-table-'.length));
    const selected = tabs.filter(button => button.getAttribute('aria-current') === 'page');
    return JSON.stringify(outputIds) === JSON.stringify(expectedIds)
      && selected.length === 1
      && selected[0].dataset.testid === `construction-table-${expectedIds[0]}`;
  }, [observationOutputId, patientOutputId], { timeout: remainingReloadMs() });
  assert.deepEqual((await visibleTableTabs(page)).map(tab => tab.outputId), [observationOutputId, patientOutputId],
    'Reload must restore the persisted Observation-before-Patient tab order');
  const reloadActiveOutputId = await activeOutputId(page, remainingReloadMs());
  assert.equal(reloadActiveOutputId, observationOutputId,
    'Reload must select the first saved Observation output before any explicit table click');
  await waitForPreview(page, observationOutputId, observationOracle.count, remainingReloadMs());
  await assertPreviewRows(page, observationOutputId, observationOracle, 'reloaded Observation table', remainingReloadMs());
  builder = await readBuilder(page, target, explorerId, remainingReloadMs());
  const reloadedTables = assertSavedBindings(builder, target, [
    { outputId: observationOutputId, tabId: observationTabId, title: observationTitle, rootResourceType: 'Observation' },
    { outputId: patientOutputId, tabId: patientTabId, title: patientTitle, rootResourceType: 'Patient' },
  ]);
  const reloadElapsedMs = Date.now() - reloadStartedAt;
  assert(reloadElapsedMs <= ACTION_BUDGET_MS,
    `Reloading reordered tables, exact Observation rows, and saved order took ${reloadElapsedMs} ms`);
  report.timings['reload reordered table order and exact selected Observation preview'] = reloadElapsedMs;
  check('persistence', 'reordered table order and saved root/title/output bindings survive reload', true, {
    selectedOutputAfterReload: reloadActiveOutputId,
    savedTables: reloadedTables,
    reloadElapsedMs,
    budgetMs: ACTION_BUDGET_MS,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
  });

  for (const table of [
    { outputId: patientOutputId, tabId: patientTabId, title: patientTitle, resourceType: 'Patient', oracle: patientOracle },
    { outputId: observationOutputId, tabId: observationTabId, title: observationTitle, resourceType: 'Observation', oracle: observationOracle },
  ]) {
    const tab = page.getByTestId(`construction-table-${table.outputId}`);
    await action(`select reloaded ${table.resourceType} output and render its exact rows`, tab,
      () => tab.click(), {
        after: async () => {
          await waitForPreview(page, table.outputId, table.oracle.count);
          await assertPreviewRows(page, table.outputId, table.oracle, `reloaded ${table.resourceType} table`);
        },
        requiredCheck: `selecting reloaded ${table.resourceType} activates its saved output and renders exact source IDs within five seconds`,
      });
  }
  check('persistence', 'selecting each reloaded table renders exact IDs and activates its saved output', true, {
    outputs: [
      { outputId: patientOutputId, tabId: patientTabId, title: patientTitle, ids: patientOracle.ids },
      { outputId: observationOutputId, tabId: observationTabId, title: observationTitle, ids: observationOracle.ids },
    ],
  });

  const [patientOracleAfter, observationOracleAfter] = await Promise.all([
    readNDJSONResourceIdentityOracle({
      path: patientPath, project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Patient',
    }),
    readNDJSONResourceIdentityOracle({
      path: observationPath, project: target.fixtureProject, generation: target.fixtureGeneration, resourceType: 'Observation',
    }),
  ]);
  assert.equal(patientOracleAfter.sha256, patientOracle.sha256, 'Patient fixture source must remain unchanged');
  assert.equal(observationOracleAfter.sha256, observationOracle.sha256, 'Observation fixture source must remain unchanged');
  check('correctness', 'independent Patient and Observation source files stay unchanged during the browser lifecycle', true, {
    patientSHA256: patientOracleAfter.sha256, observationSHA256: observationOracleAfter.sha256,
  });
};
