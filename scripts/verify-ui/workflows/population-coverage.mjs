import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readBasicPatientPopulationOracle } from '../helpers/population-coverage-oracle.mjs';
import { assertPreviewPatientIds } from '../helpers/playwright-authoring.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import { createdExplorerScope } from '../helpers/created-explorer-scope.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const ACTION_BUDGET_MS = 5_000;

const waitForOwnedResponse = (page, uiOrigin, path) => page.waitForResponse(response => {
  const url = new URL(response.url());
  return url.origin === uiOrigin && url.pathname === path && response.request().method() === 'POST';
}, { timeout: ACTION_BUDGET_MS });

const readSuccessfulJSON = async (response, label) => {
  let body;
  try { body = await response.json(); }
  catch (error) {
    throw new Error(`${label} returned HTTP ${response.status()} without JSON: ${error.message}`);
  }
  assert(response.ok(), `${label} returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 900)}`);
  return body;
};

const tableDocument = (builder, outputId) => {
  const document = builder.workspace?.documents?.find(candidate => candidate.output?.id === outputId);
  assert(document, `Builder state must contain output ${outputId}`);
  return document;
};

const bindCoverageResult = (response, request, expected) => {
  assert.equal(response.status, 'COMPLETE', 'population coverage must finish with exact counts');
  assert.deepEqual(response.binding, {
    receiptId: expected.receiptId,
    outputId: expected.outputId,
    project: expected.project,
    explorerId: expected.explorerId,
    generation: expected.generation,
    scopeDigest: expected.scopeDigest,
    selectionRevisionId: expected.selectionRevisionId,
    membershipDigest: expected.membershipDigest,
    resourceType: 'Patient',
  }, 'coverage response must bind the current receipt and exact saved Patient selection scope');
  assert.deepEqual(request, { receiptId: expected.receiptId, outputId: expected.outputId, limit: 100 },
    'native coverage action must submit the exact current receipt and selected table output');
  assert.deepEqual(response.counts, { selected: 2, mapped: 2, unmapped: 0, emittedRows: 2 },
    'both literal fixture Patients must map to exactly two emitted rows');
  assert.deepEqual(response.unmapped, [], 'no literal fixture Patient may be reported as unmapped');
  assert.equal(response.nextCursor, undefined, 'the exact two-member result must fit in one coverage page');
};

export const populationCoverageWorkflow = async (workflow, context) => {
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
  const selectionProject = canonicalProjectID(target.fixtureProject);
  assert.deepEqual(expectedIDs, ['dev-patient-001', 'dev-patient-002']);
  check('correctness', 'Basic Patient oracle contains the exact two literal fixture identities', true, {
    path: fixturePath, sourceProject: target.fixtureProject, project: selectionProject,
    generation: target.fixtureGeneration, resourceType: source.resourceType,
    ids: expectedIDs, sha256: source.sha256,
  });

  const title = `Verify ${context.runID.slice(-10)} population coverage`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS,
  });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const nameInput = page.locator('#new-explorer-name');
  await action('name the coverage Explorer', nameInput, () => nameInput.fill(title), { editable: true });
  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = fixtureProjectExplorerCollectionPath(selectionProject);
  const createStartedAt = Date.now();
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerCollectionPath &&
      request.postDataJSON()?.title === title;
  }, { timeout: ACTION_BUDGET_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  let createdExplorer;
  await action('create a blank coverage Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponsePromise;
      createdExplorer = await readSuccessfulJSON(response, 'native coverage Explorer creation');
      assert.equal(createdExplorer.project, selectionProject,
        'native Explorer creation response must stay in the exact run-owned project');
      assert.equal(createdExplorer.title, title, 'native Explorer creation response must match the requested title');
      assert(typeof createdExplorer.explorerId === 'string' && createdExplorer.explorerId,
        'native Explorer creation response must return its server-assigned ID');
      assert.notEqual(createdExplorer.explorerId, target.bootstrapExplorerId,
        'native Explorer creation response must not reuse the bootstrap ID');
      const remainingMs = Math.max(1, ACTION_BUDGET_MS - (Date.now() - createStartedAt));
      await expect(explorerControl).toHaveValue(createdExplorer.explorerId, { timeout: remainingMs });
    },
  });
  const explorerId = await explorerControl.inputValue();
  assert(explorerId && explorerId !== target.bootstrapExplorerId,
    'coverage case must use its newly created owned Explorer');
  assert.equal(explorerId, createdExplorer.explorerId,
    'coverage case must continue on the exact server-created Explorer selected by the native UI');
  check('correctness', 'native Explorer creation selects its exact server-assigned identity', true, {
    project: createdExplorer.project, explorerId, bootstrapExplorerId: target.bootstrapExplorerId, title,
  });
  report.target.explorer = explorerId;
  const browserAuthoringBase = createdExplorerScope(selectionProject, createdExplorer).browserAuthoringBase;

  const tableName = page.locator('#first-table-name');
  await action('name the Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
  const rootStarted = Date.now();
  await action('choose Patient rows', choosePatients, () => choosePatients.click(), {
    after: () => previewTable.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  await assertPreviewPatientIds(page, expectedIDs);
  const rootRenderMs = Date.now() - rootStarted;
  report.timings['Patient root action-to-exact-preview'] = rootRenderMs;
  check('performance', 'Patient root renders the exact independent IDs within five seconds', rootRenderMs <= ACTION_BUDGET_MS,
    { elapsedMs: rootRenderMs, budgetMs: ACTION_BUDGET_MS });

  const apiRoot = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}`;
  const requestJSON = async (path, body, label) => {
    const response = await fetch(`${target.apiUrl}${apiRoot}${path}`, {
      method: body ? 'POST' : 'GET',
      headers: { 'Content-Type': 'application/json' },
      ...(body ? { body: JSON.stringify(body) } : {}),
      signal: AbortSignal.timeout(30_000),
    });
    const value = await response.json();
    assert(response.ok, `${label} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 900)}`);
    return value;
  };
  const setupBuilder = await requestJSON('/authoring/v2/builder', undefined, 'coverage setup Builder read');
  assert.equal(setupBuilder.catalog?.generation, target.fixtureGeneration,
    'coverage setup Builder must retain the exact fixture generation');
  assert(setupBuilder.catalog?.snapshotToken && setupBuilder.catalog?.authorizationScopeDigest,
    'coverage setup Builder must expose the current snapshot and authorized scope');
  const initialPatientDocument = setupBuilder.workspace?.documents?.find(document => document.rootResourceType === 'Patient');
  assert(initialPatientDocument?.output?.id, 'native Patient root must have a saved output identity');
  const outputId = initialPatientDocument.output.id;
  assert.equal(initialPatientDocument.population, undefined,
    'starting collection must be unattached before the user chooses the saved selection');

  const refs = expectedIDs.map(id => ({
    project: selectionProject,
    generation: target.fixtureGeneration,
    resourceType: 'Patient',
    id,
  }));
  const selection = await requestJSON('/selections', {
    snapshotToken: setupBuilder.catalog.snapshotToken,
    idempotencyKey: `verify-population-coverage-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs } },
  }, 'two-Patient saved selection setup');
  assert.equal(selection.complete, true, 'saved setup selection must be complete');
  assert.equal(selection.project, selectionProject, 'saved selection must use the exact canonical fixture project');
  assert.equal(selection.generation, target.fixtureGeneration, 'saved selection must use the exact fixture generation');
  assert.equal(selection.resourceType, 'Patient', 'saved selection must use the Patient resource type');
  assert.equal(selection.scopeDigest, setupBuilder.catalog.authorizationScopeDigest,
    'saved selection must bind the current Builder authorization scope');
  assert.equal(selection.memberCount, source.count, 'saved selection must contain exactly both independent fixture Patients');
  assert(typeof selection.membershipDigest === 'string' && selection.membershipDigest,
    'saved selection must expose its immutable membership digest');

  const savedPage = await requestJSON(`/selections/${encodeURIComponent(selection.id)}?limit=100`, undefined,
    'saved selection exact-membership read');
  assert.equal(savedPage.revision?.id, selection.id, 'selection read must return the exact immutable revision');
  assert.equal(savedPage.revision?.complete, true, 'selection read must confirm complete membership');
  assert.equal(savedPage.revision?.scopeDigest, selection.scopeDigest, 'selection read must retain its exact scope');
  assert.equal(savedPage.revision?.membershipDigest, selection.membershipDigest,
    'selection read must retain its exact membership digest');
  assert.equal(savedPage.members?.length, source.count, 'selection read must return exactly two members');
  const selectedRefs = savedPage.members.map(member => member.ref)
    .map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  const expectedRefs = refs.map(ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`).sort();
  assert.deepEqual(selectedRefs, expectedRefs,
    'saved selection membership must equal the literal project/generation/Patient/ID fixture refs');
  report.target.populationCoverageFixture = {
    path: fixturePath, sha256: source.sha256, sourceProject: target.fixtureProject,
    project: selectionProject, generation: target.fixtureGeneration,
    resourceType: 'Patient', ids: expectedIDs, outputId,
    selectionRevisionId: selection.id, scopeDigest: selection.scopeDigest,
    membershipDigest: selection.membershipDigest,
  };
  check('correctness', 'saved Patient selection binds the exact project, generation, scope, and membership', true,
    report.target.populationCoverageFixture);

  const selectedURL = new URL(browserURL(target, target.fixtureProject, explorerId, 'builder'));
  selectedURL.searchParams.set('selection', selection.id);
  await page.goto(selectedURL.toString(), { waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await previewTable.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await assertPreviewPatientIds(page, expectedIDs);
  const settingsButton = page.getByTestId('construction-rows-settings-trigger');
  const settingsDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
  await action('open Patient row settings', settingsButton, () => settingsButton.click(), {
    after: () => settingsDialog.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const collectionSettings = settingsDialog.getByRole('region', { name: 'Starting collection settings', exact: true });
  const collection = collectionSettings.getByRole('region', { name: 'Starting collection', exact: true });
  await collectionSettings.getByText('2 Patient selected, not attached', { exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const useSelected = collection.getByRole('button', { name: 'Use selected resources', exact: true });
  await useSelected.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert(await useSelected.isEnabled(), 'exact saved Patient selection must be available for native attachment');
  assert.equal(await collection.getByRole('button', { name: 'Check selected-resource coverage', exact: true }).count(), 0,
    'coverage action must remain unavailable until the selection is attached');
  check('usability', 'coverage action appears only after the native Builder attaches a saved selection', true,
    { selectionRevisionId: selection.id, outputId });

  const commandPath = `${browserAuthoringBase}/commands`;
  const reconcilePath = `${browserAuthoringBase}/reconcile`;
  const commandResponsePromise = waitForOwnedResponse(page, uiOrigin, commandPath);
  const reconcileResponsePromise = waitForOwnedResponse(page, uiOrigin, reconcilePath);
  const attachedLabel = collectionSettings.getByText('2 Patient resources attached', { exact: true });
  const coverageButton = collection.getByRole('button', { name: 'Check selected-resource coverage', exact: true });
  const attachStarted = Date.now();
  let attachCommand;
  let attachCompile;
  let attachCompileRequest;
  await action('attach the saved Patient selection', useSelected, () => useSelected.click(), {
    after: async () => {
      const [commandResponse, reconcileResponse] = await Promise.all([commandResponsePromise, reconcileResponsePromise]);
      attachCommand = await readSuccessfulJSON(commandResponse, 'native selected-resource attachment');
      attachCompile = await readSuccessfulJSON(reconcileResponse, 'current receipt reconciliation after attachment');
      attachCompileRequest = reconcileResponse.request().postDataJSON();
      await Promise.all([
        attachedLabel.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        coverageButton.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        previewTable.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
      ]);
      assert(await coverageButton.isEnabled(), 'coverage control must be enabled for the completed current receipt');
    },
  });
  await assertPreviewPatientIds(page, expectedIDs);
  const attachRenderMs = Date.now() - attachStarted;
  report.timings['attach selected Patient collection action-to-preview'] = attachRenderMs;
  check('performance', 'native collection attachment renders exact Patient rows within five seconds', attachRenderMs <= ACTION_BUDGET_MS,
    { elapsedMs: attachRenderMs, budgetMs: ACTION_BUDGET_MS });
  assert(attachCommand?.draftVersion && attachCommand.draftDigest, 'native attachment command must return its saved draft identity');
  const savedAfterAttach = await requestJSON('/authoring/v2/builder', undefined, 'saved attached Builder read');
  const attachedDocument = tableDocument(savedAfterAttach, outputId);
  assert.equal(attachedDocument.population?.selectionRevisionId, selection.id,
    'server-saved Patient table must attach the exact selection chosen through Builder');
  assert.deepEqual(attachedDocument.population?.route, [],
    'same-resource Patient selection must persist the exact empty route');
  assert.equal(savedAfterAttach.catalog?.generation, target.fixtureGeneration,
    'attached Builder must retain the exact fixture generation');
  assert.equal(savedAfterAttach.draftVersion, attachCommand.draftVersion,
    'attached Builder must retain the successful native command draft version');
  assert.equal(savedAfterAttach.draftDigest, attachCommand.draftDigest,
    'attached Builder must retain the successful native command draft digest');
  assert.equal(attachCompileRequest.snapshotToken, savedAfterAttach.catalog.snapshotToken,
    'receipt compilation must use the current attached Builder snapshot');
  assert.equal(attachCompileRequest.draftVersion, savedAfterAttach.draftVersion,
    'receipt compilation must use the current saved attached draft version');
  assert.equal(attachCompileRequest.draftDigest, savedAfterAttach.draftDigest,
    'receipt compilation must use the current saved attached draft digest');
  assert.equal(attachCompile.generation, target.fixtureGeneration,
    'current attachment receipt must retain the exact fixture generation');
  assert.equal(attachCompile.authorizationScopeDigest, selection.scopeDigest,
    'current attachment receipt must retain the saved selection scope');
  assert(attachCompile.outputs?.some(output => output.outputId === outputId),
    'current attachment receipt must contain the exact Patient table output');
  check('correctness', 'native Builder saves the exact selection before compiling its coverage receipt', true, {
    outputId, selectionRevisionId: selection.id, generation: target.fixtureGeneration,
    draftVersion: savedAfterAttach.draftVersion, draftDigest: savedAfterAttach.draftDigest,
    receiptId: attachCompile.receiptId, scopeDigest: attachCompile.authorizationScopeDigest,
  });
  check('correctness', 'same-resource Patient selection persists the exact empty route', true, {
    outputId, selectionRevisionId: selection.id, route: attachedDocument.population.route,
  });

  const coverageSummary = collection.getByText('2 selected · 2 produce rows · 0 needs attention', { exact: true });
  const allMapped = collection.getByText('All selected resources produce rows.', { exact: true });
  const mappingPath = `${browserAuthoringBase}/population-mapping`;
  const runCoverageCheck = async (label, startedAt) => {
    const responsePromise = waitForOwnedResponse(page, uiOrigin, mappingPath);
    const button = collection.getByRole('button', { name: 'Check selected-resource coverage', exact: true });
    assert(await button.isEnabled(), `${label} coverage control must be actionable`);
    let mappingResponse;
    await action(label, button, () => button.click(), {
      after: async () => {
        mappingResponse = await responsePromise;
        await Promise.all([
          coverageSummary.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
          allMapped.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        ]);
      },
    });
    const elapsedMs = Date.now() - startedAt;
    report.timings[`${label} action-to-visible-result`] = elapsedMs;
    check('performance', `${label} action renders its exact coverage result within five seconds`, elapsedMs <= ACTION_BUDGET_MS,
      { elapsedMs, budgetMs: ACTION_BUDGET_MS });
    const response = await readSuccessfulJSON(mappingResponse, label);
    const request = mappingResponse.request().postDataJSON();
    return { response, request, elapsedMs };
  };

  const waitForCurrentPatientPreview = async () => {
    await page.waitForFunction(expectedOutputId => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const workspace = document.querySelector('[data-testid="construction-workspace"]');
      return Boolean(preview && workspace && preview.dataset.previewStatus === 'ready' &&
        preview.dataset.previewOutputId === expectedOutputId && preview.dataset.previewReceiptId &&
        preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
        preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
    }, outputId, { timeout: ACTION_BUDGET_MS });
    await assertPreviewPatientIds(page, expectedIDs);
  };

  const firstStarted = Date.now();
  const first = await runCoverageCheck('check selected-resource coverage', firstStarted);
  bindCoverageResult(first.response, first.request, {
    receiptId: attachCompile.receiptId,
    outputId,
    project: selectionProject,
    explorerId,
    generation: target.fixtureGeneration,
    scopeDigest: selection.scopeDigest,
    selectionRevisionId: selection.id,
    membershipDigest: selection.membershipDigest,
  });
  check('correctness', 'coverage report binds the exact current receipt and saved Patient scope', true,
    first.response.binding);
  check('correctness', 'coverage reports both literal Patients mapped to two rows', true, first.response.counts);
  check('correctness', 'coverage result visibly reports exact counts and no unmatched Patients',
    await coverageSummary.isVisible() && await allMapped.isVisible(),
    { summary: '2 selected · 2 produce rows · 0 needs attention', unmapped: first.response.unmapped });

  const useAllRows = collection.getByRole('button', { name: 'Use all authorized rows', exact: true });
  const detachedLabel = collectionSettings.getByText('2 Patient selected, not attached', { exact: true });
  const coverageReport = collection.getByTestId('population-coverage-report');
  const detachStarted = Date.now();
  await action('detach the saved Patient collection', useAllRows, () => useAllRows.click(), {
    after: async () => {
      await Promise.all([
        detachedLabel.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        page.waitForFunction(() => {
          const section = document.querySelector('section[aria-label="Starting collection"]');
          return section && !section.hasAttribute('data-attached-selection-revision-id');
        }, undefined, { timeout: ACTION_BUDGET_MS }),
        expect(coverageReport).toHaveCount(0, { timeout: ACTION_BUDGET_MS }),
      ]);
      await waitForCurrentPatientPreview();
    },
  });
  const detachRenderMs = Date.now() - detachStarted;
  report.timings['detach Patient collection action-to-exact-preview'] = detachRenderMs;
  check('performance', 'detaching the Patient collection restores exact rows within five seconds',
    detachRenderMs <= ACTION_BUDGET_MS, { elapsedMs: detachRenderMs, budgetMs: ACTION_BUDGET_MS });
  const savedAfterDetach = await requestJSON('/authoring/v2/builder', undefined, 'saved Builder read after detachment');
  const detachedDocument = tableDocument(savedAfterDetach, outputId);
  assert.equal(detachedDocument.population, undefined, 'Use all authorized rows must clear the saved selection and route');
  assert.equal(savedAfterDetach.catalog?.generation, target.fixtureGeneration,
    'detached Builder must retain the exact fixture generation');
  check('persistence', 'detaching the selected Patient collection clears its saved route and stale coverage report', true, {
    outputId, previousSelectionRevisionId: selection.id, savedPopulation: detachedDocument.population,
    patientIDs: expectedIDs, coverageReportVisible: false,
  });

  const useSelectedAgain = collection.getByRole('button', { name: 'Use selected resources', exact: true });
  const reattachStarted = Date.now();
  await action('reattach the same saved Patient selection', useSelectedAgain, () => useSelectedAgain.click(), {
    after: async () => {
      await Promise.all([
        attachedLabel.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        expect(collection).toHaveAttribute('data-attached-selection-revision-id', selection.id,
          { timeout: ACTION_BUDGET_MS }),
        coverageButton.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
      ]);
      await waitForCurrentPatientPreview();
    },
  });
  const reattachRenderMs = Date.now() - reattachStarted;
  report.timings['reattach Patient collection action-to-exact-preview'] = reattachRenderMs;
  check('performance', 'reattaching the exact Patient selection restores exact rows within five seconds',
    reattachRenderMs <= ACTION_BUDGET_MS, { elapsedMs: reattachRenderMs, budgetMs: ACTION_BUDGET_MS });
  assert(await coverageButton.isEnabled(), 'reattached selection must expose an actionable coverage control');
  const savedAfterReattach = await requestJSON('/authoring/v2/builder', undefined, 'saved Builder read after reattachment');
  const reattachedDocument = tableDocument(savedAfterReattach, outputId);
  assert.equal(reattachedDocument.population?.selectionRevisionId, selection.id,
    'reattachment must preserve the same immutable Patient selection');
  assert.deepEqual(reattachedDocument.population?.route, [],
    'reattachment must preserve the exact same-resource empty route');
  assert.equal(savedAfterReattach.catalog?.generation, target.fixtureGeneration,
    'reattached Builder must retain the exact fixture generation');
  check('usability', 'reattachment restores the actionable coverage control for the exact Patient selection', true, {
    selectionRevisionId: selection.id, outputId, coverageControlEnabled: true,
  });
  check('persistence', 'reattaching restores the exact Patient selection and empty route', true, {
    outputId, selectionRevisionId: selection.id, route: reattachedDocument.population.route, patientIDs: expectedIDs,
  });

  const reloadStarted = Date.now();
  const reloadCompilePromise = waitForOwnedResponse(page, uiOrigin, reconcilePath);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
  await previewTable.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  assert.equal(new URL(page.url()).searchParams.get('selection'), selection.id,
    'reload must retain the selected immutable revision handoff');
  await assertPreviewPatientIds(page, expectedIDs);
  const reloadedSettingsButton = page.getByTestId('construction-rows-settings-trigger');
  await action('reopen Patient row settings after reload', reloadedSettingsButton, () => reloadedSettingsButton.click(), {
    after: () => settingsDialog.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const reloadedCollectionSettings = settingsDialog.getByRole('region', { name: 'Starting collection settings', exact: true });
  const reloadedCollection = reloadedCollectionSettings.getByRole('region', { name: 'Starting collection', exact: true });
  const reloadedAttachedLabel = reloadedCollectionSettings.getByText('2 Patient resources attached', { exact: true });
  const reloadedCoverageButton = reloadedCollection.getByRole('button', { name: 'Check selected-resource coverage', exact: true });
  await Promise.all([
    reloadedAttachedLabel.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
    reloadedCoverageButton.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  ]);
  assert(await reloadedCoverageButton.isEnabled(), 'reloaded current receipt must expose an enabled coverage action');
  const reloadReadyMs = Date.now() - reloadStarted;
  report.timings['reload-to-attached-current-coverage-control'] = reloadReadyMs;
  check('performance', 'reload restores the exact attached collection and actionable coverage control within five seconds',
    reloadReadyMs <= ACTION_BUDGET_MS, { elapsedMs: reloadReadyMs, budgetMs: ACTION_BUDGET_MS });
  const reloadedCompileResponse = await reloadCompilePromise;
  const reloadCompile = await readSuccessfulJSON(reloadedCompileResponse, 'current receipt reconciliation after reload');
  const reloadCompileRequest = reloadedCompileResponse.request().postDataJSON();
  const savedAfterReload = await requestJSON('/authoring/v2/builder', undefined, 'saved Builder read after reload');
  const reloadedDocument = tableDocument(savedAfterReload, outputId);
  assert.equal(reloadedDocument.population?.selectionRevisionId, selection.id,
    'reload must restore the exact saved starting collection');
  assert.equal(savedAfterReload.catalog?.generation, target.fixtureGeneration,
    'reloaded Builder must retain the exact fixture generation');
  assert.equal(reloadCompileRequest.snapshotToken, savedAfterReload.catalog.snapshotToken,
    'reloaded receipt must compile against the exact current snapshot');
  assert.equal(reloadCompileRequest.draftVersion, savedAfterReload.draftVersion,
    'reloaded receipt must compile against the exact saved draft version');
  assert.equal(reloadCompileRequest.draftDigest, savedAfterReload.draftDigest,
    'reloaded receipt must compile against the exact saved draft digest');
  assert.equal(reloadCompile.generation, target.fixtureGeneration,
    'reloaded current receipt must retain the exact fixture generation');
  assert.equal(reloadCompile.authorizationScopeDigest, selection.scopeDigest,
    'reloaded current receipt must retain the exact selection scope');
  assert(reloadCompile.outputs?.some(output => output.outputId === outputId),
    'reloaded current receipt must contain the exact attached Patient table output');
  assert.equal(await reloadedCollection.getByText('2 selected · 2 produce rows · 0 needs attention', { exact: true }).count(), 0,
    'a prior coverage result must not survive reload as an unverified stale report');
  check('persistence', 'reload preserves the exact attached Patient collection and visible IDs', true,
    { outputId, selectionRevisionId: selection.id, ids: expectedIDs, generation: target.fixtureGeneration });

  const secondStarted = Date.now();
  const second = await (async () => {
    const responsePromise = waitForOwnedResponse(page, uiOrigin, mappingPath);
    assert(await reloadedCoverageButton.isEnabled(), 'reloaded coverage action must remain enabled');
    let mappingResponse;
    await action('repeat selected-resource coverage after reload', reloadedCoverageButton, () => reloadedCoverageButton.click(), {
      after: async () => {
        mappingResponse = await responsePromise;
        await Promise.all([
          reloadedCollection.getByText('2 selected · 2 produce rows · 0 needs attention', { exact: true })
            .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
          reloadedCollection.getByText('All selected resources produce rows.', { exact: true })
            .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
        ]);
      },
    });
    const elapsedMs = Date.now() - secondStarted;
    report.timings['repeat coverage after reload action-to-visible-result'] = elapsedMs;
    check('performance', 'repeat coverage after reload renders exact counts within five seconds', elapsedMs <= ACTION_BUDGET_MS,
      { elapsedMs, budgetMs: ACTION_BUDGET_MS });
    return {
      response: await readSuccessfulJSON(mappingResponse, 'repeat selected-resource coverage after reload'),
      request: mappingResponse.request().postDataJSON(),
    };
  })();
  bindCoverageResult(second.response, second.request, {
    receiptId: reloadCompile.receiptId,
    outputId,
    project: selectionProject,
    explorerId,
    generation: target.fixtureGeneration,
    scopeDigest: selection.scopeDigest,
    selectionRevisionId: selection.id,
    membershipDigest: selection.membershipDigest,
  });
  check('correctness', 'repeated coverage report binds the current reloaded receipt and exact Patient scope', true,
    second.response.binding);
  check('correctness', 'repeated coverage reports both literal Patients mapped to two rows', true,
    second.response.counts);
  check('correctness', 'repeated coverage visibly reports exact counts and no unmatched Patients',
    await reloadedCollection.getByText('2 selected · 2 produce rows · 0 needs attention', { exact: true }).isVisible() &&
    await reloadedCollection.getByText('All selected resources produce rows.', { exact: true }).isVisible(),
    { summary: '2 selected · 2 produce rows · 0 needs attention', unmapped: second.response.unmapped });
};
