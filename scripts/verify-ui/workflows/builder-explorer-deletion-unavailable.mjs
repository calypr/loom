import assert from 'node:assert/strict';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientPopulationOracle } from '../helpers/population-coverage-oracle.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const remainingActionMs = startedAt => Math.max(1, ACTION_TIMEOUT_MS - (Date.now() - startedAt));

const readSuccessfulJSON = async (response, label) => {
  let body;
  try { body = await response.json(); }
  catch (error) {
    throw new Error(`${label} returned HTTP ${response.status()} without JSON: ${error.message}`);
  }
  assert(response.ok(), `${label} returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 900)}`);
  return body;
};

const explorerCollectionPath = project =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const capabilityPath = (project, explorerId) =>
  `${explorerCollectionPath(project)}/${encodeURIComponent(explorerId)}/authoring/v2/capability`;
const selectedExplorerFromURL = page => new URL(page.url()).searchParams.get('explorer');

const readExplorerSummaries = async (request, apiUrl, project, { timeout = ACTION_TIMEOUT_MS } = {}) => {
  const response = await request.get(`${apiUrl}${explorerCollectionPath(project)}`, { timeout });
  const summaries = await readSuccessfulJSON(response, 'fresh project Explorer list read');
  assert(Array.isArray(summaries), 'fresh project Explorer list must be an array');
  return summaries;
};

export const builderExplorerDeletionUnavailableWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  const target = context.target;
  configureNativePage(page);

  const fixturePath = join(target.fixtureDir, 'Patient.ndjson');
  const fixtureOracle = await readBasicPatientPopulationOracle({
    path: fixturePath,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
  });
  const expectedPatientIDs = [...fixtureOracle.ids].sort();
  assert.deepEqual(expectedPatientIDs, ['dev-patient-001', 'dev-patient-002']);
  check('correctness', 'Basic Patient oracle confirms the fresh project generation', true, {
    path: fixturePath,
    project: target.fixtureProject,
    canonicalProject: canonicalProjectID(target.fixtureProject),
    generation: target.fixtureGeneration,
    patientIDs: expectedPatientIDs,
    sha256: fixtureOracle.sha256,
  });

  const initialExplorers = await readExplorerSummaries(
    page.context().request,
    target.apiUrl,
    target.fixtureProject,
  );
  assert.deepEqual(initialExplorers.map(item => item.explorerId), [target.bootstrapExplorerId],
    'fresh Basic project must start with exactly its bootstrap Explorer');

  const uiOrigin = new URL(target.uiUrl).origin;
  const collectionPath = explorerCollectionPath(target.fixtureProject);
  const deleteRequests = [];
  page.on('request', request => {
    const url = new URL(request.url());
    if (request.method() === 'DELETE' && url.origin === uiOrigin &&
      url.pathname.startsWith(`${collectionPath}/`)) {
      deleteRequests.push({ method: request.method(), path: url.pathname });
    }
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const emptyBuilderHeading = page.getByRole('heading', { name: 'Build your first table', exact: true });
  const deleteButton = page.getByRole('button', { name: 'Delete explorer', exact: true });
  const bootstrapURL = browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder');
  const bootstrapCapabilityResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'GET' && url.origin === uiOrigin &&
      url.pathname === capabilityPath(target.fixtureProject, target.bootstrapExplorerId);
  }, { timeout: ACTION_TIMEOUT_MS });
  await page.goto(bootstrapURL, { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, { timeout: ACTION_TIMEOUT_MS });
  await expect(emptyBuilderHeading).toBeVisible({ timeout: ACTION_TIMEOUT_MS });
  const bootstrapCapability = await readSuccessfulJSON(
    await bootstrapCapabilityResponse,
    'bootstrap authoring capability read',
  );
  assert.equal(bootstrapCapability.features?.deleteExplorer, false,
    'the real Basic backend capability must report Explorer deletion unsupported');
  await expect(deleteButton).toHaveCount(0, { timeout: ACTION_TIMEOUT_MS });
  assert.equal(page.url(), bootstrapURL, 'unsupported capability must preserve the exact bootstrap Builder URL');
  check('correctness', 'unsupported capability omits the Explorer delete control', true, {
    explorerId: target.bootstrapExplorerId,
    capabilityPath: capabilityPath(target.fixtureProject, target.bootstrapExplorerId),
    deleteExplorer: bootstrapCapability.features.deleteExplorer,
    url: page.url(),
  });

  const disposableTitle = `Verify ${context.runID.slice(-10)} Unsupported Delete`;
  const nameInput = page.locator('#new-explorer-name');
  const openCreate = page.getByText('New explorer', { exact: true });
  await action('open native Explorer creation', openCreate, () => openCreate.click(), {
    after: () => expect(nameInput).toBeVisible({ timeout: ACTION_TIMEOUT_MS }),
  });
  await action('name the disposable Explorer', nameInput, () => nameInput.fill(disposableTitle), { editable: true });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === collectionPath &&
      request.postDataJSON()?.title === disposableTitle;
  }, { timeout: ACTION_TIMEOUT_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  let disposable;
  const createStartedAt = Date.now();
  await action('create the disposable Explorer through the native control', createBlank, () => createBlank.click(), {
    after: async () => {
      disposable = await readSuccessfulJSON(await createResponsePromise, 'native disposable Explorer creation');
      assert.equal(disposable.project, canonicalProjectID(target.fixtureProject));
      assert.equal(disposable.title, disposableTitle);
      assert(typeof disposable.explorerId === 'string' && disposable.explorerId);
      assert.notEqual(disposable.explorerId, target.bootstrapExplorerId);
      await expect(explorerControl).toHaveValue(disposable.explorerId, {
        timeout: remainingActionMs(createStartedAt),
      });
      assert.equal(selectedExplorerFromURL(page), disposable.explorerId);
    },
  });

  const disposableId = disposable.explorerId;
  report.target.explorer = disposableId;
  const explorersBeforeReload = await readExplorerSummaries(
    page.context().request,
    target.apiUrl,
    target.fixtureProject,
  );
  assert.deepEqual(explorersBeforeReload.map(item => item.explorerId).sort(),
    [target.bootstrapExplorerId, disposableId].sort(),
    'native creation must leave exactly the bootstrap and disposable Explorers');
  check('correctness', 'native creation preserves the bootstrap and adds one disposable Explorer', true, {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    explorerIds: explorersBeforeReload.map(item => item.explorerId).sort(),
    bootstrapExplorerId: target.bootstrapExplorerId,
    disposableExplorerId: disposableId,
  });
  const disposableURL = page.url();
  assert.equal(disposableURL, browserURL(target, target.fixtureProject, disposableId, 'builder'));

  const capabilityResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'GET' && url.origin === uiOrigin &&
      url.pathname === capabilityPath(target.fixtureProject, disposableId);
  }, { timeout: ACTION_TIMEOUT_MS });
  const beforeReloadDeleteCount = deleteRequests.length;
  const reloadStartedAt = Date.now();
  await action('reload the disposable Explorer with unsupported deletion', emptyBuilderHeading,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await expect(explorerControl).toHaveValue(disposableId, {
          timeout: remainingActionMs(reloadStartedAt),
        });
        await expect(emptyBuilderHeading).toBeVisible({ timeout: remainingActionMs(reloadStartedAt) });
        const capability = await readSuccessfulJSON(
          await capabilityResponsePromise,
          'disposable authoring capability read after reload',
        );
        assert.equal(capability.features?.deleteExplorer, false,
          'the real Basic backend capability must remain unsupported after reload');
        await expect(deleteButton).toHaveCount(0, { timeout: remainingActionMs(reloadStartedAt) });
        assert.equal(page.url(), disposableURL,
          'unsupported deletion reload must preserve the exact selected Explorer URL');
        const explorersAfterReload = await readExplorerSummaries(
          page.context().request,
          target.apiUrl,
          target.fixtureProject,
          { timeout: remainingActionMs(reloadStartedAt) },
        );
        assert.deepEqual(explorersAfterReload, explorersBeforeReload,
          'the exact Explorer summaries must remain unchanged across reload');
        assert.deepEqual(explorersAfterReload.map(item => item.explorerId).sort(),
          [target.bootstrapExplorerId, disposableId].sort());
        assert.equal(deleteRequests.length, beforeReloadDeleteCount,
          'unsupported capability reload must issue no scoped DELETE request');
      },
    });

  check('correctness', 'unsupported deletion stays hidden and preserves both Explorer identities across reload',
    deleteRequests.length === 0 && page.url() === disposableURL, {
      project: target.fixtureProject,
      capabilityPath: capabilityPath(target.fixtureProject, disposableId),
      deleteExplorer: false,
      explorerIds: explorersBeforeReload.map(item => item.explorerId).sort(),
      selectedExplorerId: selectedExplorerFromURL(page),
      selectedURL: page.url(),
      deleteRequests,
    });
};
