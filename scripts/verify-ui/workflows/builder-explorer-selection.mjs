import assert from 'node:assert/strict';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientPopulationOracle } from '../helpers/population-coverage-oracle.mjs';
import { assertPreviewPatientIds } from '../helpers/playwright-authoring.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const ACTION_BUDGET_MS = 5_000;

const remainingActionMs = startedAt => Math.max(1, ACTION_BUDGET_MS - (Date.now() - startedAt));

const readSuccessfulJSON = async (response, label) => {
  let body;
  try { body = await response.json(); }
  catch (error) {
    throw new Error(`${label} returned HTTP ${response.status()} without JSON: ${error.message}`);
  }
  assert(response.ok(), `${label} returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 900)}`);
  return body;
};

const selectedExplorerFromURL = page => new URL(page.url()).searchParams.get('explorer');

export const builderExplorerSelectionWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  const target = context.target;
  configureNativePage(page);

  const fixturePath = join(target.fixtureDir, 'Patient.ndjson');
  const source = await readBasicPatientPopulationOracle({
    path: fixturePath,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
  });
  const expectedIDs = [...source.ids].sort();
  const project = canonicalProjectID(target.fixtureProject);
  assert.deepEqual(expectedIDs, ['dev-patient-001', 'dev-patient-002']);
  check('correctness', 'Basic Patient oracle contains the exact independent Explorer-selection rows', true, {
    path: fixturePath, sourceProject: target.fixtureProject, project,
    generation: target.fixtureGeneration, resourceType: source.resourceType,
    ids: expectedIDs, sha256: source.sha256,
  });

  const bootstrapPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(target.bootstrapExplorerId)}/authoring/v2/builder`;
  const bootstrapStatePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'GET' && url.origin === new URL(target.uiUrl).origin &&
      url.pathname === bootstrapPath;
  }, { timeout: ACTION_BUDGET_MS });
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS,
  });
  const bootstrapState = await readSuccessfulJSON(await bootstrapStatePromise, 'bootstrap Explorer Builder read');
  assert.equal(bootstrapState.lifecycleState, 'NEW', 'fresh Basic bootstrap Explorer must start without a saved Builder workspace');
  assert.equal(bootstrapState.catalog?.generation, target.fixtureGeneration,
    'bootstrap Explorer must use the exact fresh Basic fixture generation');
  assert.equal(bootstrapState.workspace ?? null, null,
    'bootstrap Explorer must remain empty so its visible state distinguishes the authored Explorer');
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const firstTableHeading = page.getByRole('heading', { name: 'Build your first table', exact: true });
  await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, { timeout: ACTION_BUDGET_MS });
  await firstTableHeading.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert.equal(selectedExplorerFromURL(page), target.bootstrapExplorerId,
    'initial Explorer selection must be represented by the actual URL query');
  check('correctness', 'fresh bootstrap Explorer has a distinct empty Builder workspace', true, {
    project: target.fixtureProject, generation: target.fixtureGeneration,
    explorerId: target.bootstrapExplorerId, lifecycleState: bootstrapState.lifecycleState,
  });

  const explorerTitle = `Verify ${context.runID.slice(-10)} Explorer selection`;
  const explorerPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const uiOrigin = new URL(target.uiUrl).origin;
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await action('open native Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const explorerNameInput = page.locator('#new-explorer-name');
  await action('name the distinct Explorer', explorerNameInput, () => explorerNameInput.fill(explorerTitle), { editable: true });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerPath &&
      request.postDataJSON()?.title === explorerTitle;
  }, { timeout: ACTION_BUDGET_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  let createdExplorer;
  const createStartedAt = Date.now();
  await action('create and select the distinct Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      createdExplorer = await readSuccessfulJSON(await createResponsePromise, 'native Explorer creation');
      assert.equal(createdExplorer.project, project, 'native Explorer must be created in the fresh exact Basic project');
      assert.equal(createdExplorer.title, explorerTitle, 'native Explorer must retain the requested unique title');
      assert(typeof createdExplorer.explorerId === 'string' && createdExplorer.explorerId,
        'native Explorer creation must return its server-assigned ID');
      assert.notEqual(createdExplorer.explorerId, target.bootstrapExplorerId,
        'new native Explorer must have a different identity from the bootstrap Explorer');
      await expect(explorerControl).toHaveValue(createdExplorer.explorerId, {
        timeout: remainingActionMs(createStartedAt),
      });
      assert.equal(selectedExplorerFromURL(page), createdExplorer.explorerId,
        'native Explorer creation must update the URL to its server-assigned selection');
      await expect(explorerControl.locator('option:checked')).toHaveText(explorerTitle, {
        timeout: remainingActionMs(createStartedAt),
      });
    },
  });
  const explorerId = await explorerControl.inputValue();
  assert.equal(explorerId, createdExplorer.explorerId,
    'workflow must continue on the exact Explorer selected by the native creation action');
  report.target.explorer = explorerId;
  check('correctness', 'native Explorer creation selects the exact server-assigned Explorer in the owned project', true, {
    project: createdExplorer.project, generation: target.fixtureGeneration,
    explorerId, bootstrapExplorerId: target.bootstrapExplorerId, title: explorerTitle,
  });

  const tableTitle = 'Explorer selection Patients';
  const tableName = page.locator('#first-table-name');
  await action('name the distinct Explorer Patient table', tableName, () => tableName.fill(tableTitle), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
  const rootStartedAt = Date.now();
  await action('create the exact Patient table in the selected Explorer', choosePatients, () => choosePatients.click(), {
    after: async () => {
      await previewTable.waitFor({ state: 'visible', timeout: remainingActionMs(rootStartedAt) });
      await assertPreviewPatientIds(page, expectedIDs);
    },
  });
  const rootElapsedMs = Date.now() - rootStartedAt;
  report.timings['create Patient table in selected Explorer to exact rows'] = rootElapsedMs;
  check('performance', 'selected Explorer Patient table renders the exact fixture rows within five seconds',
    rootElapsedMs <= ACTION_BUDGET_MS, { elapsedMs: rootElapsedMs, budgetMs: ACTION_BUDGET_MS });

  const builderPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`;
  const builderResponse = await page.context().request.get(`${target.apiUrl}${builderPath}`, { timeout: 30_000 });
  const savedExplorer = await readSuccessfulJSON(builderResponse, 'saved selected Explorer Builder read');
  assert.equal(savedExplorer.catalog?.generation, target.fixtureGeneration,
    'authored Explorer table must remain bound to the exact Basic fixture generation');
  assert.equal(savedExplorer.workspace?.documents?.length, 1,
    'authored Explorer must contain exactly its own one Patient table');
  const patientDocument = savedExplorer.workspace.documents[0];
  assert.equal(patientDocument.rootResourceType, 'Patient', 'authored Explorer table must be rooted at Patient');
  assert.equal(patientDocument.output?.title, tableTitle,
    'authored Explorer must persist its distinct title on the canonical output');
  const outputId = patientDocument.output?.id;
  assert(typeof outputId === 'string' && outputId, 'authored Explorer table must expose its saved output identity');
  const savedTab = savedExplorer.workspace.tabs?.find(tab => tab.outputId === outputId);
  assert(savedTab, 'authored Explorer workspace must retain a visible tab for its saved output');
  assert.equal(savedTab.title, tableTitle, 'saved Explorer tab must retain the canonical output title');
  const tableTab = page.getByTestId(`construction-table-${outputId}`);
  await expect(tableTab.getByText(tableTitle, { exact: true })).toBeVisible({ timeout: ACTION_BUDGET_MS });
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'authored Explorer table must be visible under its own URL selection');
  check('correctness', 'authored Explorer persists its unique Patient table and exact fixture generation', true, {
    project, generation: target.fixtureGeneration, explorerId, outputId,
    tabId: savedTab.id, tableTitle: savedTab.title,
    sourceIDs: expectedIDs, draftVersion: savedExplorer.draftVersion, draftDigest: savedExplorer.draftDigest,
  });

  const switchToBootstrapStartedAt = Date.now();
  await action('switch to the empty bootstrap Explorer', explorerControl,
    () => explorerControl.selectOption(target.bootstrapExplorerId), {
      after: async () => {
        await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, {
          timeout: remainingActionMs(switchToBootstrapStartedAt),
        });
        assert.equal(selectedExplorerFromURL(page), target.bootstrapExplorerId,
          'native Explorer switch must update the actual URL query');
        await firstTableHeading.waitFor({
          state: 'visible', timeout: remainingActionMs(switchToBootstrapStartedAt),
        });
        await expect(tableTab).toHaveCount(0, { timeout: remainingActionMs(switchToBootstrapStartedAt) });
        await expect(previewTable).toHaveCount(0, { timeout: remainingActionMs(switchToBootstrapStartedAt) });
      },
    });
  const switchToBootstrapMs = Date.now() - switchToBootstrapStartedAt;
  report.timings['switch to bootstrap Explorer and its empty state'] = switchToBootstrapMs;
  check('usability', 'native Explorer switch shows the bootstrap Explorer distinct empty table state', true, {
    explorerId: target.bootstrapExplorerId, selectedExplorerId: selectedExplorerFromURL(page),
    heading: 'Build your first table', authoredOutputHidden: true,
  });
  check('performance', 'switching to the empty Explorer settles its distinct visible state within five seconds',
    switchToBootstrapMs <= ACTION_BUDGET_MS, { elapsedMs: switchToBootstrapMs, budgetMs: ACTION_BUDGET_MS });

  const switchBackStartedAt = Date.now();
  await action('switch back to the authored Patient Explorer', explorerControl,
    () => explorerControl.selectOption(explorerId), {
      after: async () => {
        await expect(explorerControl).toHaveValue(explorerId, { timeout: remainingActionMs(switchBackStartedAt) });
        assert.equal(selectedExplorerFromURL(page), explorerId,
          'switching back must restore the authored Explorer URL identity');
        await expect(explorerControl.locator('option:checked')).toHaveText(explorerTitle, {
          timeout: remainingActionMs(switchBackStartedAt),
        });
        await expect(tableTab).toHaveAttribute('aria-current', 'page', {
          timeout: remainingActionMs(switchBackStartedAt),
        });
        await previewTable.waitFor({ state: 'visible', timeout: remainingActionMs(switchBackStartedAt) });
        await assertPreviewPatientIds(page, expectedIDs);
      },
    });
  const switchBackMs = Date.now() - switchBackStartedAt;
  report.timings['switch back to authored Explorer and exact rows'] = switchBackMs;
  check('correctness', 'switching back restores the authored Explorer table and both exact Patient rows', true, {
    explorerId, title: explorerTitle, outputId, tableTitle, ids: expectedIDs,
  });
  check('performance', 'switching back restores the exact authored Explorer table within five seconds',
    switchBackMs <= ACTION_BUDGET_MS, { elapsedMs: switchBackMs, budgetMs: ACTION_BUDGET_MS });

  const reloadStartedAt = Date.now();
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'reload begins from the actual URL containing the selected authored Explorer');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await expect(explorerControl).toHaveValue(explorerId, { timeout: remainingActionMs(reloadStartedAt) });
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'demo route must initialize the selected Explorer from its URL query after reload');
  await expect(explorerControl.locator('option:checked')).toHaveText(explorerTitle, {
    timeout: remainingActionMs(reloadStartedAt),
  });
  await expect(tableTab).toHaveAttribute('aria-current', 'page', {
    timeout: remainingActionMs(reloadStartedAt),
  });
  await previewTable.waitFor({ state: 'visible', timeout: remainingActionMs(reloadStartedAt) });
  await assertPreviewPatientIds(page, expectedIDs);
  const reloadElapsedMs = Date.now() - reloadStartedAt;
  report.timings['reload selected Explorer to exact saved rows'] = reloadElapsedMs;
  check('persistence', 'reload restores the selected Explorer from the URL and its saved Patient table', true, {
    urlExplorerId: selectedExplorerFromURL(page), explorerId, outputId, tableTitle, ids: expectedIDs,
    generation: target.fixtureGeneration,
  });
  check('performance', 'reload restores the selected Explorer and exact rows within five seconds',
    reloadElapsedMs <= ACTION_BUDGET_MS, { elapsedMs: reloadElapsedMs, budgetMs: ACTION_BUDGET_MS });
};
