import assert from 'node:assert/strict';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientPopulationOracle } from '../helpers/population-coverage-oracle.mjs';
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

const explorerCollectionPath = project =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const capabilityPath = (project, explorerId) =>
  `${explorerCollectionPath(project)}/${encodeURIComponent(explorerId)}/authoring/v2/capability`;
const selectedExplorerFromURL = page => new URL(page.url()).searchParams.get('explorer');

const readExplorerSummaries = async (request, apiUrl, project, { timeout = ACTION_BUDGET_MS } = {}) => {
  const response = await request.get(`${apiUrl}${explorerCollectionPath(project)}`, { timeout });
  const summaries = await readSuccessfulJSON(response, 'fresh project Explorer list read');
  assert(Array.isArray(summaries), 'fresh project Explorer list must be an array');
  return summaries;
};

export const builderExplorerDeletionWorkflow = async (workflow, context) => {
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
  const project = canonicalProjectID(target.fixtureProject);
  check('correctness', 'Basic Patient fixture oracle confirms the exact fresh project generation', true, {
    path: fixturePath,
    project: target.fixtureProject,
    canonicalProject: project,
    generation: target.fixtureGeneration,
    ids: expectedPatientIDs,
    sha256: fixtureOracle.sha256,
  });

  const initialExplorers = await readExplorerSummaries(page.context().request, target.apiUrl, target.fixtureProject);
  assert.deepEqual(initialExplorers.map(item => item.explorerId), [target.bootstrapExplorerId],
    'fresh project must contain exactly its fixture-created empty bootstrap Explorer');
  const bootstrapPath = `${explorerCollectionPath(target.fixtureProject)}/${encodeURIComponent(target.bootstrapExplorerId)}`
    + '/authoring/v2/builder';
  const bootstrapResponse = await page.context().request.get(`${target.apiUrl}${bootstrapPath}`, {
    timeout: ACTION_BUDGET_MS,
  });
  const bootstrap = await readSuccessfulJSON(bootstrapResponse, 'fresh bootstrap Builder read');
  assert.equal(bootstrap.lifecycleState, 'NEW');
  assert.equal(bootstrap.workspace ?? null, null);
  check('correctness', 'fresh project starts with exactly one empty bootstrap Explorer', true, {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    explorerIds: initialExplorers.map(item => item.explorerId),
    bootstrapExplorerId: target.bootstrapExplorerId,
    lifecycleState: bootstrap.lifecycleState,
  });

  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const emptyBuilderHeading = page.getByRole('heading', { name: 'Build your first table', exact: true });
  const deleteButton = page.getByRole('button', { name: 'Delete explorer', exact: true });
  const uiOrigin = new URL(target.uiUrl).origin;
  const bootstrapURL = browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder');
  await page.goto(bootstrapURL, { waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, { timeout: ACTION_BUDGET_MS });
  await emptyBuilderHeading.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert.equal(page.url(), bootstrapURL, 'fresh bootstrap Explorer must be selected by the exact expected URL');

  const disposableTitle = `Verify ${context.runID.slice(-10)} Disposable Explorer`;
  const collectionPath = explorerCollectionPath(target.fixtureProject);
  const nameInput = page.locator('#new-explorer-name');
  const openCreate = page.getByText('New explorer', { exact: true });
  await action('open native Explorer creation', openCreate, () => openCreate.click(), {
    after: () => nameInput.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  await action('name the disposable Explorer', nameInput, () => nameInput.fill(disposableTitle), { editable: true });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === collectionPath &&
      request.postDataJSON()?.title === disposableTitle;
  }, { timeout: ACTION_BUDGET_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  let disposable;
  const createStartedAt = Date.now();
  await action('create the disposable Explorer through the native control', createBlank, () => createBlank.click(), {
    after: async () => {
      disposable = await readSuccessfulJSON(await createResponsePromise, 'native disposable Explorer creation');
      assert.equal(disposable.project, project, 'native Explorer must belong to the exact fresh project');
      assert.equal(disposable.title, disposableTitle);
      assert(typeof disposable.explorerId === 'string' && disposable.explorerId);
      assert.notEqual(disposable.explorerId, target.bootstrapExplorerId);
      await expect(explorerControl).toHaveValue(disposable.explorerId, {
        timeout: remainingActionMs(createStartedAt),
      });
      await expect(explorerControl.locator('option:checked')).toHaveText(disposableTitle, {
        timeout: remainingActionMs(createStartedAt),
      });
      assert.equal(selectedExplorerFromURL(page), disposable.explorerId);
      assert.deepEqual(
        (await readExplorerSummaries(page.context().request, target.apiUrl, target.fixtureProject))
          .map(item => item.explorerId).sort(),
        [target.bootstrapExplorerId, disposable.explorerId].sort(),
        'native creation must produce exactly two Explorers in the fresh project',
      );
    },
  });
  const disposableId = disposable.explorerId;
  report.target.explorer = disposableId;
  check('correctness', 'native creation gives the disposable Explorer a distinct server identity and exact project scope', true, {
    project: disposable.project,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    disposableExplorerId: disposableId,
    title: disposableTitle,
  });

  const deleteRequests = [];
  page.on('request', request => {
    const url = new URL(request.url());
    if (request.method() === 'DELETE' && url.origin === uiOrigin &&
      url.pathname.startsWith(`${collectionPath}/`)) {
      deleteRequests.push({ method: request.method(), path: url.pathname });
    }
  });

  const deleteRequestsBeforeUnsupportedReload = deleteRequests.length;
  const unsupportedPath = capabilityPath(target.fixtureProject, disposableId);
  const capabilityRouteMatcher = url => {
    try {
      const parsed = new URL(url);
      return parsed.origin === uiOrigin && parsed.pathname === unsupportedPath;
    } catch { return false; }
  };
  let capabilityOverrideCount = 0;
  let actualCapability;
  const unsupportedCapabilityRoute = async route => {
    const response = await route.fetch();
    actualCapability = await response.json();
    capabilityOverrideCount += 1;
    await route.fulfill({
      response,
      json: {
        ...actualCapability,
        features: { ...actualCapability.features, deleteExplorer: false },
      },
    });
  };
  await page.route(capabilityRouteMatcher, unsupportedCapabilityRoute);
  const unsupportedReloadStartedAt = Date.now();
  await action('reload the disposable Explorer with deletion unsupported', emptyBuilderHeading,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), {
      after: async () => {
        await expect(explorerControl).toHaveValue(disposableId, {
          timeout: remainingActionMs(unsupportedReloadStartedAt),
        });
        await expect(deleteButton).toHaveCount(0, { timeout: remainingActionMs(unsupportedReloadStartedAt) });
        assert.equal(capabilityOverrideCount, 1, 'reload must read the scoped capability through the unsupported override');
        assert.equal(actualCapability?.features?.deleteExplorer, true,
          'the real fresh-project capability must support deletion before the controlled unsupported override');
        const unsupportedExplorerList = await readExplorerSummaries(
          page.context().request, target.apiUrl, target.fixtureProject,
          { timeout: remainingActionMs(unsupportedReloadStartedAt) },
        );
        assert.deepEqual(unsupportedExplorerList.map(item => item.explorerId).sort(),
          [target.bootstrapExplorerId, disposableId].sort());
        assert.equal(deleteRequests.length, deleteRequestsBeforeUnsupportedReload,
          'unsupported capability reload must not issue a scoped DELETE request');
      },
    });
  check('correctness', 'unsupported capability reload leaves the scoped DELETE ledger empty',
    deleteRequests.length === deleteRequestsBeforeUnsupportedReload && deleteRequests.length === 0, {
      explorerId: disposableId,
      capabilityPath: unsupportedPath,
      deleteButtonCount: 0,
      deleteRequestCount: deleteRequests.length,
      capabilityReads: capabilityOverrideCount,
      actualDeleteCapability: actualCapability.features.deleteExplorer,
    });
  await page.unroute(capabilityRouteMatcher, unsupportedCapabilityRoute);

  const supportedReloadStartedAt = Date.now();
  await action('reload the disposable Explorer with deletion supported', emptyBuilderHeading,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), {
      after: async () => {
        await expect(explorerControl).toHaveValue(disposableId, {
          timeout: remainingActionMs(supportedReloadStartedAt),
        });
        await expect(deleteButton).toBeEnabled({ timeout: remainingActionMs(supportedReloadStartedAt) });
        await expect(deleteButton).toHaveAttribute('title', 'Delete explorer', {
          timeout: remainingActionMs(supportedReloadStartedAt),
        });
        assert.equal(selectedExplorerFromURL(page), disposableId);
      },
    });
  const confirmMessage = `Delete Explorer "${disposableTitle}"? This permanently removes its configuration and cannot be undone.`;
  const cancelStartedAt = Date.now();
  const cancelURL = page.url();
  const deleteRequestsBeforeCancel = deleteRequests.length;
  await action('cancel the native deletion confirmation', deleteButton, async () => {
    const dialogPromise = page.waitForEvent('dialog', { timeout: ACTION_BUDGET_MS });
    const clickPromise = deleteButton.click({ timeout: ACTION_BUDGET_MS });
    const dialog = await dialogPromise;
    assert.equal(dialog.type(), 'confirm');
    assert.equal(dialog.message(), confirmMessage);
    await dialog.dismiss();
    await clickPromise;
  }, {
    after: async () => {
      const explorersAfterCancel = await readExplorerSummaries(
        page.context().request, target.apiUrl, target.fixtureProject,
        { timeout: remainingActionMs(cancelStartedAt) },
      );
      assert.deepEqual(explorersAfterCancel.map(item => item.explorerId).sort(),
        [target.bootstrapExplorerId, disposableId].sort(),
        'canceling deletion must preserve both exact Explorer identities');
      await expect(explorerControl).toHaveValue(disposableId, {
        timeout: remainingActionMs(cancelStartedAt),
      });
      await expect(explorerControl.locator('option')).toHaveCount(2, {
        timeout: remainingActionMs(cancelStartedAt),
      });
      await expect(deleteButton).toBeEnabled({ timeout: remainingActionMs(cancelStartedAt) });
      assert.equal(page.url(), cancelURL, 'canceling deletion must preserve the selected Explorer URL');
      assert.equal(deleteRequests.length, deleteRequestsBeforeCancel,
        'canceling the confirmation must not issue a scoped DELETE request');
    },
  });
  check('correctness', 'Cancel preserves both exact Explorers, selection, URL, and DELETE ledger',
    deleteRequestsBeforeCancel === 0 && deleteRequests.length === deleteRequestsBeforeCancel, {
      project: target.fixtureProject,
      explorerIds: [target.bootstrapExplorerId, disposableId].sort(),
      selectedExplorerId: selectedExplorerFromURL(page),
      selectedURL: page.url(),
      deleteRequests,
    });

  const deleteResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'DELETE' && url.origin === uiOrigin && url.pathname ===
      `${collectionPath}/${encodeURIComponent(disposableId)}`;
  }, { timeout: ACTION_BUDGET_MS });
  const deleteButtonStartedAt = Date.now();
  await action('delete only the disposable Explorer through its native control', deleteButton,
    async () => {
      const dialogPromise = page.waitForEvent('dialog', { timeout: ACTION_BUDGET_MS });
      const clickPromise = deleteButton.click();
      const dialog = await dialogPromise;
      assert.equal(dialog.type(), 'confirm');
      assert.equal(dialog.message(), confirmMessage);
      await dialog.accept();
      await clickPromise;
    }, {
      after: async () => {
        const response = await deleteResponsePromise;
        assert(response.ok(), `native Explorer deletion returned HTTP ${response.status()}`);
        await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, {
          timeout: remainingActionMs(deleteButtonStartedAt),
        });
        await expect(explorerControl.locator('option')).toHaveCount(1, {
          timeout: remainingActionMs(deleteButtonStartedAt),
        });
        await expect(explorerControl.locator('option:checked')).toHaveText(
          initialExplorers[0].title, { timeout: remainingActionMs(deleteButtonStartedAt) },
        );
        assert.equal(selectedExplorerFromURL(page), target.bootstrapExplorerId,
          'deleting the disposable must select the exact bootstrap fallback in the URL');
        assert.equal(page.url(), bootstrapURL,
          'fallback selection must replace the URL with the exact project, survivor, and Builder mode');
      },
    });

  assert.deepEqual(deleteRequests, [{
    method: 'DELETE',
    path: `${collectionPath}/${encodeURIComponent(disposableId)}`,
  }], 'the only issued DELETE request must target the disposable Explorer');
  report.target.explorer = target.bootstrapExplorerId;
  const afterDelete = await readExplorerSummaries(page.context().request, target.apiUrl, target.fixtureProject);
  assert.deepEqual(afterDelete.map(item => item.explorerId), [target.bootstrapExplorerId],
    'the exact fallback must be the sole Explorer remaining after native deletion');
  check('correctness', 'native deletion removes only the disposable Explorer and selects the sole fallback', true, {
    project: target.fixtureProject,
    deletedExplorerId: disposableId,
    deletedExplorerTitle: disposableTitle,
    survivorExplorerId: target.bootstrapExplorerId,
    remainingExplorerIds: afterDelete.map(item => item.explorerId),
    fallbackURL: page.url(),
    deleteRequests,
  });

  const deleteRequestsBeforeSoleSurvivorReload = deleteRequests.length;
  const reloadStartedAt = Date.now();
  await action('reload the sole surviving fallback Explorer', emptyBuilderHeading,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS }), {
      after: async () => {
        await expect(explorerControl).toHaveValue(target.bootstrapExplorerId, {
          timeout: remainingActionMs(reloadStartedAt),
        });
        await expect(explorerControl.locator('option')).toHaveCount(1, {
          timeout: remainingActionMs(reloadStartedAt),
        });
        await expect(explorerControl.locator('option:checked')).toHaveText(initialExplorers[0].title, {
          timeout: remainingActionMs(reloadStartedAt),
        });
        await expect(deleteButton).toBeDisabled({ timeout: remainingActionMs(reloadStartedAt) });
        await expect(deleteButton).toHaveAttribute('title',
          'Create or select another Explorer before deleting this one',
          { timeout: remainingActionMs(reloadStartedAt) });
        assert.equal(page.url(), bootstrapURL,
          'reload must preserve the exact fallback URL while only one Explorer exists');
        assert.equal(deleteRequests.length, deleteRequestsBeforeSoleSurvivorReload,
          'sole-survivor reload must not issue another scoped DELETE request');
      },
    });

  assert.equal(deleteRequests.length, deleteRequestsBeforeSoleSurvivorReload,
    'sole-survivor state must retain the DELETE ledger captured before reload');
  const finalExplorerList = await readExplorerSummaries(page.context().request, target.apiUrl, target.fixtureProject);
  assert.deepEqual(finalExplorerList.map(item => item.explorerId), [target.bootstrapExplorerId]);
  check('correctness', 'sole surviving Explorer remains disabled after reload with no additional DELETE',
    deleteRequests.length === deleteRequestsBeforeSoleSurvivorReload && deleteRequests.length === 1 && finalExplorerList.length === 1 &&
      finalExplorerList[0].explorerId === target.bootstrapExplorerId, {
      survivorExplorerId: target.bootstrapExplorerId,
      deletedExplorerId: disposableId,
      selectedExplorerId: selectedExplorerFromURL(page),
      selectedURL: page.url(),
      remainingExplorerIds: finalExplorerList.map(item => item.explorerId),
      deleteRequests,
    });
};
