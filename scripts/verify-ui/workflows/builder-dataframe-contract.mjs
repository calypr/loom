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

const shortDigest = value => value ? `${value.slice(0, 18)}…${value.slice(-8)}` : 'unavailable';

const selectedExplorerFromURL = page => new URL(page.url()).searchParams.get('explorer');

const structureLabel = value => ({
  scalar: 'Scalar columns',
  array: 'Includes arrays',
  'requires-review': 'Needs review',
}[value] ?? 'Not assessed');

export const builderDataframeContractWorkflow = async (workflow, context) => {
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
  assert.deepEqual(expectedIDs, ['dev-patient-001', 'dev-patient-002']);
  check('correctness', 'Basic Patient oracle contains the exact two independent row identities', true, {
    path: fixturePath,
    sourceProject: target.fixtureProject,
    generation: target.fixtureGeneration,
    resourceType: source.resourceType,
    ids: expectedIDs,
    sha256: source.sha256,
  });

  const project = canonicalProjectID(target.fixtureProject);
  const explorerTitle = `Verify ${context.runID.slice(-10)} Dataframe contract`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS,
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await action('open native Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await action('name the distinct Dataframe contract Explorer', explorerName,
    () => explorerName.fill(explorerTitle), { editable: true });

  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerCollectionPath &&
      request.postDataJSON()?.title === explorerTitle;
  }, { timeout: ACTION_BUDGET_MS });
  const createStartedAt = Date.now();
  let createdExplorer;
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create and select the distinct contract Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      createdExplorer = await readSuccessfulJSON(await createResponsePromise, 'native contract Explorer creation');
      assert.equal(createdExplorer.project, project, 'new Explorer must be owned by the exact Basic fixture project');
      assert.equal(createdExplorer.title, explorerTitle, 'new Explorer must retain its requested title');
      assert(typeof createdExplorer.explorerId === 'string' && createdExplorer.explorerId,
        'new Explorer must return its server-assigned identity');
      assert.notEqual(createdExplorer.explorerId, target.bootstrapExplorerId,
        'new contract Explorer must have a distinct identity from the bootstrap Explorer');
      await expect(explorerControl).toHaveValue(createdExplorer.explorerId, {
        timeout: remainingActionMs(createStartedAt),
      });
      await expect(explorerControl.locator('option:checked')).toHaveText(explorerTitle, {
        timeout: remainingActionMs(createStartedAt),
      });
      assert.equal(selectedExplorerFromURL(page), createdExplorer.explorerId,
        'native Explorer creation must select its server-assigned identity in the URL');
      assert(Date.now() - createStartedAt <= ACTION_BUDGET_MS,
        `native Explorer creation must settle within ${ACTION_BUDGET_MS} ms`);
    },
  });
  const explorerId = await explorerControl.inputValue();
  assert.equal(explorerId, createdExplorer.explorerId,
    'table authoring must continue in the exact Explorer created by the native action');
  report.target.explorer = explorerId;
  check('correctness', 'native Explorer creation selects the exact new Basic-project Explorer', true, {
    project, explorerId, bootstrapExplorerId: target.bootstrapExplorerId, title: explorerTitle,
  });

  const tableTitle = 'Basic Patient contract';
  const tableName = page.locator('#first-table-name');
  await action('name the simple Basic Patient table', tableName, () => tableName.fill(tableTitle), { editable: true });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
  const reconcilePath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/reconcile`;
  const reconcileResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.origin === uiOrigin && url.pathname === reconcilePath;
  }, { timeout: ACTION_BUDGET_MS });
  const rootStartedAt = Date.now();
  let receipt;
  let reconcileRequest;
  await action('create the exact Basic Patient table and compile its current receipt', choosePatients,
    () => choosePatients.click(), {
      after: async () => {
        const response = await reconcileResponsePromise;
        receipt = await readSuccessfulJSON(response, 'current Patient table compile receipt');
        reconcileRequest = response.request().postDataJSON();
        await previewTable.waitFor({ state: 'visible', timeout: remainingActionMs(rootStartedAt) });
        await expect(previewTable.getByRole('row')).toHaveCount(expectedIDs.length + 1, {
          timeout: remainingActionMs(rootStartedAt),
        });
        await Promise.all(expectedIDs.map(id =>
          expect(previewTable.getByText(id, { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(rootStartedAt) })));
        await assertPreviewPatientIds(page, expectedIDs);
      },
    });
  const rootElapsedMs = Date.now() - rootStartedAt;
  report.timings['create Basic Patient table to exact rows'] = rootElapsedMs;
  assert(rootElapsedMs <= ACTION_BUDGET_MS,
    `Basic Patient table rows must render within ${ACTION_BUDGET_MS} ms (elapsed ${rootElapsedMs} ms)`);
  check('performance', 'Basic Patient table renders its exact independent rows within five seconds', true, {
    elapsedMs: rootElapsedMs, budgetMs: ACTION_BUDGET_MS,
  });

  const builderPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`;
  const builderResponse = await page.context().request.get(`${target.apiUrl}${builderPath}`, {
    timeout: ACTION_BUDGET_MS,
  });
  const savedExplorer = await readSuccessfulJSON(builderResponse, 'current contract Builder read');
  assert.equal(savedExplorer.catalog?.generation, target.fixtureGeneration,
    'authored Patient table must stay on the exact Basic fixture generation');
  assert.equal(savedExplorer.workspace?.documents?.length, 1,
    'contract Explorer must contain exactly the one user-created table');
  const document = savedExplorer.workspace.documents[0];
  assert.equal(document.rootResourceType, 'Patient', 'user-created table must use Patient as its row root');
  assert.equal(document.output?.title, tableTitle, 'saved table must retain its exact visible title');
  assert.equal(document.rows?.kind, 'RECORDS', 'simple Patient table must retain root-record row semantics');
  assert.equal(document.route?.occurrenceId, 'base', 'simple Patient table must use the root occurrence');
  assert.equal(document.route?.resourceType, 'Patient', 'simple Patient route must retain the Patient root');
  assert.equal((document.route?.children ?? []).length, 0, 'simple Patient table must not add a related route');
  assert.equal(document.columns?.length, 1, 'saved Patient table must contain exactly one source column');
  const savedPatientID = document.columns[0];
  assert(typeof savedPatientID.column === 'string' && savedPatientID.column,
    'saved Patient.id source must retain its generated physical column identity');
  assert.equal(savedPatientID.occurrenceId, 'base', 'Patient.id must come from the root occurrence');
  assert.equal(savedPatientID.source?.kind, 'field', 'Patient.id must remain a direct field source');
  assert.equal(savedPatientID.source?.field?.path, 'id', 'Patient source field must be the literal id path');
  assert.equal(savedPatientID.source?.field?.projectionMode, 'VALUE', 'Patient.id must preserve its scalar value');
  const outputId = document.output?.id;
  assert(typeof outputId === 'string' && outputId, 'saved Patient table must expose its output identity');
  const savedTab = savedExplorer.workspace.tabs?.find(tab => tab.outputId === outputId);
  assert(savedTab, 'saved Patient output must retain its workspace tab');
  assert.equal(savedTab.title, tableTitle, 'saved Patient tab must retain the exact output title');
  const tableTab = page.getByTestId(`construction-table-${outputId}`);
  await expect(tableTab.getByText(tableTitle, { exact: true })).toBeVisible({ timeout: ACTION_BUDGET_MS });
  await expect(tableTab).toHaveAttribute('aria-current', 'page', { timeout: ACTION_BUDGET_MS });
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'visible contract must belong to the native-created Explorer selected in the URL');
  assert.equal(receipt.kind, 'ExplorerBuilderReceipt', 'current compile response must be the Builder receipt contract');
  assert.equal(receipt.generation, target.fixtureGeneration,
    'current compile receipt must be bound to the exact Basic fixture generation');
  assert.equal(receipt.snapshotToken, savedExplorer.catalog.snapshotToken,
    'current receipt must compile the exact saved Builder snapshot');
  assert.equal(reconcileRequest?.snapshotToken, savedExplorer.catalog.snapshotToken,
    'receipt request must use the exact saved Builder snapshot');
  assert.equal(reconcileRequest?.draftVersion, savedExplorer.draftVersion,
    'receipt request must use the exact saved Builder draft version');
  assert.equal(reconcileRequest?.draftDigest, savedExplorer.draftDigest,
    'receipt request must use the exact saved Builder draft digest');
  const receiptOutputs = receipt.outputs?.filter(output => output.outputId === outputId) ?? [];
  assert.equal(receiptOutputs.length, 1, 'current receipt must contain the selected Patient output exactly once');
  assert.equal(receipt.outputs.length, 1, 'current receipt must contain exactly the one authored table output');
  const output = receiptOutputs[0];

  // These are literal source/schema expectations: the Basic fixture has exactly two Patient IDs,
  // and graph-fhir.json defines Patient.id as a string field that is not required.
  assert.equal(output.rowGrain, 'patient', 'Dataframe contract must retain the literal Patient row grain');
  assert.equal(output.rowMultiplication, 'none', 'direct Patient rows must not multiply');
  assert.equal(output.columns.length, 1, 'simple Basic Patient table must contain its direct ID column only');
  assert.equal(output.lossless, true, 'direct Patient.id output must preserve its source value');
  assert.equal(output.structuralSuitability, 'scalar', 'direct Patient.id must remain a scalar output');
  assert.deepEqual(expectedIDs, ['dev-patient-001', 'dev-patient-002'],
    'preview row identities must remain the two literal Basic Patient records without multiplication');
  const patientIDColumns = output.columns.filter(column =>
    column.sourceResourceType === 'Patient' && column.sourcePath === 'id');
  assert.equal(patientIDColumns.length, 1, 'receipt must contain exactly one direct Patient.id column');
  const patientID = patientIDColumns[0];
  assert.equal(patientID.column, savedPatientID.column,
    'compile receipt column identity must match the exact saved Patient.id source column');
  assert.equal(patientID.logicalType, 'string', 'Patient.id schema type must remain string');
  assert.equal(patientID.nullable, true, 'optional Patient.id schema field must remain nullable');
  assert.equal(patientID.lossless, true, 'direct Patient.id lineage must preserve its source value');

  const digests = {
    generation: receipt.generation,
    receiptId: receipt.receiptId,
    shapeDigest: receipt.shapeDigest ?? null,
    capabilitySchemaDigest: receipt.capabilitySchemaDigest ?? null,
    recipeDigest: receipt.recipeDigest ?? null,
    resolvedSchemaDigest: receipt.resolvedSchemaDigest ?? null,
    outputContractDigest: receipt.outputContractDigest ?? null,
  };
  assert(typeof receipt.receiptId === 'string' && receipt.receiptId,
    'current compile receipt must expose its exact receipt identity');
  assert.equal(receipt.capabilitySchemaDigest, savedExplorer.catalog.resolvedSchemaDigest,
    'current receipt capability schema digest must match the selected Builder catalog');
  assert(typeof receipt.resolvedSchemaDigest === 'string' && receipt.resolvedSchemaDigest,
    'current receipt must expose its resolved output schema digest for the visible contract');
  check('correctness', 'current receipt binds the exact Patient output, generation, draft, and digest projection', true, {
    project, explorerId, outputId, tableTitle, generation: receipt.generation,
    receiptId: receipt.receiptId, draftVersion: savedExplorer.draftVersion,
    draftDigest: savedExplorer.draftDigest, digests,
  });
  check('correctness', 'Basic Patient row grain and direct Patient.id semantics match literal source expectations', true, {
    outputId, rowGrain: output.rowGrain, rowMultiplication: output.rowMultiplication,
    fixtureIDs: expectedIDs,
    patientID: {
      column: patientID.column, sourceResourceType: patientID.sourceResourceType,
      sourcePath: patientID.sourcePath, logicalType: patientID.logicalType, nullable: patientID.nullable,
    },
  });

  const contractHeading = page.getByRole('heading', { name: 'Dataframe contract', exact: true });
  const contractPanel = page.locator('section').filter({ has: contractHeading });
  const sourceSetup = page.getByTestId('construction-source-setup');
  const sourceSetupSummary = sourceSetup.locator(':scope > summary');
  assert.equal(await sourceSetup.getAttribute('open'), null,
    'rooted Patient table keeps Advanced source setup collapsed until the user opens it');
  const visibleContractFields = [
    `Generation: ${receipt.generation}`,
    `Receipt: ${shortDigest(receipt.receiptId)}`,
    `Shape: ${shortDigest(receipt.shapeDigest)}`,
    `Recipe: ${shortDigest(receipt.recipeDigest)}`,
    `Schema: ${shortDigest(receipt.resolvedSchemaDigest)}`,
    `Contract: ${shortDigest(receipt.outputContractDigest)}`,
    `Lossless: ${output.lossless === undefined ? 'Not assessed' : output.lossless ? 'Yes' : 'No'}`,
    `Structure: ${structureLabel(output.structuralSuitability)}`,
  ];
  const summary = 'One row per patient · Row multiplication: none · 1 physical columns';
  const contractOpenStartedAt = Date.now();
  await action('open Advanced source setup to inspect the current Dataframe contract', sourceSetupSummary,
    () => sourceSetupSummary.click(), {
      after: async () => {
        await expect(contractHeading).toBeVisible({ timeout: remainingActionMs(contractOpenStartedAt) });
        await expect(contractPanel).toHaveCount(1, { timeout: remainingActionMs(contractOpenStartedAt) });
        await expect(contractPanel).toBeVisible({ timeout: remainingActionMs(contractOpenStartedAt) });
        await Promise.all(visibleContractFields.map(value =>
          expect(contractPanel.getByText(value, { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(contractOpenStartedAt) })));
        await expect(contractPanel.getByText(summary, { exact: true }))
          .toBeVisible({ timeout: remainingActionMs(contractOpenStartedAt) });
      },
      requiredCheck: 'opening Advanced source setup reveals the current Dataframe contract within five seconds',
    });
  const contractOpenElapsedMs = Date.now() - contractOpenStartedAt;
  report.timings['open Advanced source setup to current Dataframe contract'] = contractOpenElapsedMs;
  assert(contractOpenElapsedMs <= ACTION_BUDGET_MS,
    `opening Advanced source setup must reveal the current Dataframe contract within ${ACTION_BUDGET_MS} ms (elapsed ${contractOpenElapsedMs} ms)`);
  check('correctness', 'visible Dataframe contract shows the current receipt projection and literal Patient grain', true, {
    outputId, receiptId: receipt.receiptId, displayed: visibleContractFields,
    rowSummary: summary,
  });

  const lineageSummary = contractPanel.getByText('Column lineage and nullability', { exact: true });
  const lineageStartedAt = Date.now();
  await action('expand the visible Patient column lineage and nullability', lineageSummary,
    () => lineageSummary.click(), {
      after: async () => {
        await expect(contractPanel.getByText(patientID.column, { exact: true }))
          .toBeVisible({ timeout: remainingActionMs(lineageStartedAt) });
        await expect(contractPanel.getByText('string · nullable', { exact: true }))
          .toBeVisible({ timeout: remainingActionMs(lineageStartedAt) });
        await expect(contractPanel.getByText('Patient · id', { exact: true }))
          .toBeVisible({ timeout: remainingActionMs(lineageStartedAt) });
      },
    });
  const lineageElapsedMs = Date.now() - lineageStartedAt;
  report.timings['expand Dataframe contract to exact Patient.id lineage'] = lineageElapsedMs;
  assert(lineageElapsedMs <= ACTION_BUDGET_MS,
    `Patient.id lineage must render within ${ACTION_BUDGET_MS} ms (elapsed ${lineageElapsedMs} ms)`);
  check('performance', 'Patient.id lineage and nullability render within five seconds', true, {
    elapsedMs: lineageElapsedMs, budgetMs: ACTION_BUDGET_MS,
  });
  check('correctness', 'expanded lineage shows the exact Patient.id identity, string type, and nullable status', true, {
    outputId, receiptId: receipt.receiptId,
    column: patientID.column, sourceResourceType: 'Patient', sourcePath: 'id',
    logicalType: 'string', nullable: true,
    visibleIdentity: 'Patient · id', visibleType: 'string · nullable',
  });

  const reloadReceiptResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.origin === uiOrigin && url.pathname === reconcilePath;
  }, { timeout: ACTION_BUDGET_MS });
  const reloadStartedAt = Date.now();
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'full reload must begin with the exact native-created Explorer selected in the URL');
  await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingActionMs(reloadStartedAt) });
  await expect(explorerControl).toHaveValue(explorerId, { timeout: remainingActionMs(reloadStartedAt) });
  assert.equal(selectedExplorerFromURL(page), explorerId,
    'full reload must restore the same Explorer from the URL');
  await expect(explorerControl.locator('option:checked')).toHaveText(explorerTitle, {
    timeout: remainingActionMs(reloadStartedAt),
  });
  await expect(tableTab).toHaveAttribute('aria-current', 'page', {
    timeout: remainingActionMs(reloadStartedAt),
  });

  const reloadReceiptResponse = await reloadReceiptResponsePromise;
  const reloadReceipt = await readSuccessfulJSON(reloadReceiptResponse, 'current Patient contract receipt after reload');
  const reloadRequest = reloadReceiptResponse.request().postDataJSON();
  await previewTable.waitFor({ state: 'visible', timeout: remainingActionMs(reloadStartedAt) });
  await expect(previewTable.getByRole('row')).toHaveCount(expectedIDs.length + 1, {
    timeout: remainingActionMs(reloadStartedAt),
  });
  await Promise.all(expectedIDs.map(id =>
    expect(previewTable.getByText(id, { exact: true }))
      .toBeVisible({ timeout: remainingActionMs(reloadStartedAt) })));
  await assertPreviewPatientIds(page, expectedIDs);
  const reloadRowsElapsedMs = Date.now() - reloadStartedAt;
  report.timings['reload selected Patient table to exact rows'] = reloadRowsElapsedMs;
  assert(reloadRowsElapsedMs <= ACTION_BUDGET_MS,
    `reloaded Patient table rows must render within ${ACTION_BUDGET_MS} ms (elapsed ${reloadRowsElapsedMs} ms)`);
  check('performance', 'reloaded Patient table renders its exact independent rows within five seconds', true, {
    elapsedMs: reloadRowsElapsedMs, budgetMs: ACTION_BUDGET_MS,
  });

  const reloadedBuilderResponse = await page.context().request.get(`${target.apiUrl}${builderPath}`, {
    timeout: ACTION_BUDGET_MS,
  });
  const reloadedExplorer = await readSuccessfulJSON(reloadedBuilderResponse, 'reloaded contract Builder read');
  assert.equal(reloadedExplorer.catalog?.generation, target.fixtureGeneration,
    'reloaded Patient table must remain bound to the exact Basic fixture generation');
  assert.equal(reloadedExplorer.workspace?.documents?.length, 1,
    'reloaded contract Explorer must retain its one authored Patient table');
  const reloadedDocument = reloadedExplorer.workspace.documents[0];
  assert.equal(reloadedDocument.output?.id, outputId, 'reload must preserve the exact Patient output identity');
  assert.equal(reloadedDocument.output?.title, tableTitle, 'reload must preserve the canonical Patient output title');
  assert.deepEqual(reloadedDocument.rows, document.rows, 'reload must preserve the exact Patient row definition');
  assert.deepEqual(reloadedDocument.route, document.route, 'reload must preserve the exact Patient source route');
  assert.deepEqual(reloadedDocument.columns, document.columns, 'reload must preserve the exact Patient.id source column');
  const reloadedTab = reloadedExplorer.workspace.tabs?.find(tab => tab.outputId === outputId);
  assert(reloadedTab, 'reload must preserve the selected Patient output tab');
  assert.deepEqual(reloadedTab, savedTab, 'reload must preserve the exact Patient output tab identity and title');

  assert.equal(reloadReceipt.kind, 'ExplorerBuilderReceipt', 'reload must compile a current Builder receipt');
  assert.equal(reloadReceipt.generation, target.fixtureGeneration,
    'reload receipt must retain the exact Basic fixture generation');
  assert.equal(reloadReceipt.snapshotToken, reloadedExplorer.catalog.snapshotToken,
    'reload receipt must compile the exact reloaded Builder snapshot');
  assert.equal(reloadRequest?.snapshotToken, reloadedExplorer.catalog.snapshotToken,
    'reload receipt request must use the exact reloaded Builder snapshot');
  assert.equal(reloadRequest?.draftVersion, reloadedExplorer.draftVersion,
    'reload receipt request must use the exact reloaded Builder draft version');
  assert.equal(reloadRequest?.draftDigest, reloadedExplorer.draftDigest,
    'reload receipt request must use the exact reloaded Builder draft digest');
  const reloadOutputs = reloadReceipt.outputs?.filter(candidate => candidate.outputId === outputId) ?? [];
  assert.equal(reloadOutputs.length, 1, 'reload receipt must contain the exact saved Patient output once');
  assert.equal(reloadReceipt.outputs.length, 1, 'reload receipt must contain only the exact saved Patient output');
  const reloadedOutput = reloadOutputs[0];
  assert.equal(reloadedOutput.rowGrain, 'patient', 'reloaded contract must retain literal Patient row grain');
  assert.equal(reloadedOutput.rowMultiplication, 'none', 'reloaded Patient rows must not multiply');
  assert.equal(reloadedOutput.lossless, true, 'reloaded direct Patient.id output must remain lossless');
  assert.equal(reloadedOutput.structuralSuitability, 'scalar', 'reloaded direct Patient.id output must remain scalar');
  assert.equal(reloadedOutput.columns.length, 1, 'reloaded Patient output must retain one direct ID column');
  const reloadedPatientIDColumns = reloadedOutput.columns.filter(column =>
    column.sourceResourceType === 'Patient' && column.sourcePath === 'id');
  assert.equal(reloadedPatientIDColumns.length, 1, 'reload receipt must contain the exact direct Patient.id lineage');
  const reloadedPatientID = reloadedPatientIDColumns[0];
  assert.equal(reloadedPatientID.column, reloadedDocument.columns[0].column,
    'reload receipt column must match the exact persisted source column');
  assert.equal(reloadedPatientID.column, savedPatientID.column,
    'reload receipt column must retain its exact original generated identity');
  assert.equal(reloadedPatientID.logicalType, 'string', 'reloaded Patient.id schema type must remain string');
  assert.equal(reloadedPatientID.nullable, true, 'reloaded optional Patient.id field must remain nullable');
  assert.equal(reloadedPatientID.lossless, true, 'reloaded direct Patient.id lineage must remain lossless');
  assert(typeof reloadReceipt.receiptId === 'string' && reloadReceipt.receiptId,
    'reload must expose its own current receipt identity');
  assert.equal(reloadReceipt.capabilitySchemaDigest, reloadedExplorer.catalog.resolvedSchemaDigest,
    'reload receipt capability schema digest must match the reloaded Builder catalog');
  assert(typeof reloadReceipt.resolvedSchemaDigest === 'string' && reloadReceipt.resolvedSchemaDigest,
    'reload receipt must expose its resolved output schema digest for the visible contract');

  const reloadedContractHeading = page.getByRole('heading', { name: 'Dataframe contract', exact: true });
  const reloadedContractPanel = page.locator('section').filter({ has: reloadedContractHeading });
  const reloadedSourceSetup = page.getByTestId('construction-source-setup');
  const reloadedSourceSetupSummary = reloadedSourceSetup.locator(':scope > summary');
  assert.equal(await reloadedSourceSetup.getAttribute('open'), null,
    'full reload resets Advanced source setup to its closed native disclosure state');
  const reloadedContractFields = [
    `Generation: ${reloadReceipt.generation}`,
    `Receipt: ${shortDigest(reloadReceipt.receiptId)}`,
    `Shape: ${shortDigest(reloadReceipt.shapeDigest)}`,
    `Recipe: ${shortDigest(reloadReceipt.recipeDigest)}`,
    `Schema: ${shortDigest(reloadReceipt.resolvedSchemaDigest)}`,
    `Contract: ${shortDigest(reloadReceipt.outputContractDigest)}`,
    `Lossless: ${reloadedOutput.lossless === undefined ? 'Not assessed' : reloadedOutput.lossless ? 'Yes' : 'No'}`,
    `Structure: ${structureLabel(reloadedOutput.structuralSuitability)}`,
  ];
  const reloadedSummary = 'One row per patient · Row multiplication: none · 1 physical columns';
  const reloadContractOpenStartedAt = Date.now();
  await action('open Advanced source setup after reload to inspect the current Dataframe contract',
    reloadedSourceSetupSummary, () => reloadedSourceSetupSummary.click(), {
      after: async () => {
        await expect(reloadedContractHeading).toBeVisible({ timeout: remainingActionMs(reloadContractOpenStartedAt) });
        await expect(reloadedContractPanel).toHaveCount(1, { timeout: remainingActionMs(reloadContractOpenStartedAt) });
        await expect(reloadedContractPanel).toBeVisible({ timeout: remainingActionMs(reloadContractOpenStartedAt) });
        await Promise.all(reloadedContractFields.map(value =>
          expect(reloadedContractPanel.getByText(value, { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(reloadContractOpenStartedAt) })));
        await expect(reloadedContractPanel.getByText(reloadedSummary, { exact: true }))
          .toBeVisible({ timeout: remainingActionMs(reloadContractOpenStartedAt) });
      },
      requiredCheck: 'opening Advanced source setup after reload reveals the current Dataframe contract within five seconds',
    });
  const reloadContractOpenElapsedMs = Date.now() - reloadContractOpenStartedAt;
  report.timings['open Advanced source setup after reload to current Dataframe contract'] = reloadContractOpenElapsedMs;
  assert(reloadContractOpenElapsedMs <= ACTION_BUDGET_MS,
    `opening Advanced source setup after reload must reveal the current Dataframe contract within ${ACTION_BUDGET_MS} ms (elapsed ${reloadContractOpenElapsedMs} ms)`);
  check('persistence', 'reload restores the same selected Explorer, Patient output, and exact two rows', true, {
    explorerId, selectedExplorerId: selectedExplorerFromURL(page), outputId,
    title: reloadedDocument.output.title, generation: reloadedExplorer.catalog.generation, ids: expectedIDs,
  });
  check('correctness', 'reload compiles a current receipt for the exact saved Patient output and generation', true, {
    explorerId, outputId, receiptId: reloadReceipt.receiptId,
    generation: reloadReceipt.generation, snapshotToken: reloadReceipt.snapshotToken,
    capabilitySchemaDigest: reloadReceipt.capabilitySchemaDigest,
    resolvedSchemaDigest: reloadReceipt.resolvedSchemaDigest,
    draftVersion: reloadedExplorer.draftVersion, draftDigest: reloadedExplorer.draftDigest,
  });
  check('correctness', 'reloaded Dataframe contract shows the current receipt and literal Patient grain', true, {
    outputId, receiptId: reloadReceipt.receiptId, displayed: reloadedContractFields, rowSummary: reloadedSummary,
  });
  const reloadLineageDetails = reloadedContractPanel.locator(':scope > details');
  const reloadLineageSummary = reloadLineageDetails.locator(':scope > summary');
  const reloadLineageStartedAt = Date.now();
  await expect(reloadLineageDetails).toHaveCount(1, { timeout: remainingActionMs(reloadLineageStartedAt) });
  if (await reloadLineageDetails.getAttribute('open') === null) {
    await action('reopen Patient column lineage after reload', reloadLineageSummary,
      () => reloadLineageSummary.click(), {
        after: async () => {
          await expect(reloadedContractPanel.getByText(reloadedPatientID.column, { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
          await expect(reloadedContractPanel.getByText('string · nullable', { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
          await expect(reloadedContractPanel.getByText('Patient · id', { exact: true }))
            .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
        },
      });
  }
  await expect(reloadedContractPanel.getByText(reloadedPatientID.column, { exact: true }))
    .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
  await expect(reloadedContractPanel.getByText('string · nullable', { exact: true }))
    .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
  await expect(reloadedContractPanel.getByText('Patient · id', { exact: true }))
    .toBeVisible({ timeout: remainingActionMs(reloadLineageStartedAt) });
  const reloadLineageElapsedMs = Date.now() - reloadLineageStartedAt;
  report.timings['reopen Patient lineage after reload to literal source and nullable type'] = reloadLineageElapsedMs;
  assert(reloadLineageElapsedMs <= ACTION_BUDGET_MS,
    `reloaded Patient.id lineage must render within ${ACTION_BUDGET_MS} ms (elapsed ${reloadLineageElapsedMs} ms)`);
  check('performance', 'reloaded Patient.id lineage and nullability render within five seconds', true, {
    elapsedMs: reloadLineageElapsedMs, budgetMs: ACTION_BUDGET_MS,
  });
  check('correctness', 'reloaded lineage shows exact Patient.id source, string type, and nullable status', true, {
    outputId, receiptId: reloadReceipt.receiptId,
    column: reloadedPatientID.column, sourceResourceType: 'Patient', sourcePath: 'id',
    logicalType: 'string', nullable: true,
    visibleIdentity: 'Patient · id', visibleType: 'string · nullable',
  });
};
