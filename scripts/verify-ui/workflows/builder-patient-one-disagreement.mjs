import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { readFileSync } from 'node:fs';
import { openFreshPatientTable } from './builder-group-entry.mjs';
import { findRenderedBuilderHeaderIndex, waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';

const proposalPanel = '[data-testid="construction-proposal-panel"]';
const choiceProposalPanel = '[data-testid="construction-choice-proposal-panel"]';
const renderedTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';

export const readNativeGroupSummary = page =>
  page.getByRole('combobox', { name: 'Summary 1', exact: true }).inputValue();

const requireCheck = (workflow, dimension, name, passed, evidence = {}) =>
  workflow.check(dimension, name, passed, evidence);

const sorted = values => [...values].sort((left, right) => left.localeCompare(right));

export const patientOneDisagreementWorkflow = async (workflow, context) => {
  const { page, report } = workflow;
  const { fixtureIDs, fixtureOracle, explorer, explorerName, apiRoot, outputId, sourceIdColumn, sourceIdColumnId } =
    await openFreshPatientTable(workflow, context, 'Patient ONE disagreement');
  const fixturePath = join(context.target.fixtureDir, 'Patient.ndjson');
  report.target.qaIsolation.fixtureOracle = {
    ...fixtureOracle,
    sha256: createHash('sha256').update(readFileSync(fixturePath)).digest('hex'),
  };
  report.target.qaIsolation.title = explorerName;
  report.target.qaIsolation.outputId = outputId;
  report.target.qaIsolation.setupMutations = [...report.target.qaIsolation.workflowMutations];
  report.target.qaIsolation.workflowMutations.push('native UI created a two-Patient root table from the raw fixture records');
  report.target.qaIsolation.workflowCleanup = 'none; the harness retains the fresh loom_dev_verify project and case-owned Explorer with the final applied ALL repair';

  const readBuilder = async () => {
    const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Builder read returned HTTP ${response.status}.`);
    return value;
  };
  const readTable = async () => page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    if (!table) return { headers: [], rows: [] };
    const rows = [...table.querySelectorAll('[role="row"]')];
    return {
      headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    };
  }, { tableSelector: '[data-testid="preview-table-scroll"] [role="table"]' });
  const readChoicePreview = async () => page.evaluate(selector => {
    const panel = document.querySelector(selector);
    const preview = document.querySelector('[data-testid="construction-preview"] [data-testid="construction-proposal-preview"]');
    const table = preview?.querySelector('table');
    const headers = [...(table?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => ({
        text: cell.innerText.trim(),
        raw: cell.getAttribute('title'),
      })));
    return { headers, rows };
  }, choiceProposalPanel);

  const openRows = page.getByTestId('construction-rows-settings-trigger');
  await workflow.action('open Patient row settings for empty-key Group', openRows, () => openRows.click(), {
    after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
  });
  const groupAction = page.getByTestId('construction-action-group-rows');
  await workflow.action('open native empty-key Group proposal', groupAction, () => groupAction.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready'),
  });
  const groupPreview = await page.evaluate(selector => {
    const panel = document.querySelector(selector);
    const table = panel?.querySelector('[data-testid="construction-proposal-preview"] table');
    const headers = [...(table?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
    return { headers, rows, groupKeys: [...(panel?.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]') ?? [])]
      .filter(input => input.checked).map(input => input.getAttribute('aria-label')) };
  }, proposalPanel);
  const countIndex = groupPreview.headers.findIndex(header => header.toLowerCase() === 'row count');
  assert.deepEqual(groupPreview.groupKeys, [], 'ONE disagreement requires one empty-key group across both Patients');
  assert.equal(await readNativeGroupSummary(page), 'COUNT_ROWS');
  assert.equal(groupPreview.rows.length, 1);
  assert.equal(groupPreview.rows[0]?.[countIndex], String(fixtureIDs.length));
  requireCheck(workflow, 'correctness', 'empty-key Group prepares one row for the two raw Patient records', true,
    { groupPreview, rawPatientIDs: fixtureIDs, expectedCount: fixtureIDs.length });
  await workflow.action('Apply empty-key COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: [{ 'Row count': String(fixtureIDs.length) }] }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI applied empty-key COUNT_ROWS Group to combine the two Patient records');
  const groupedBuilder = await readBuilder();
  const groupedDocument = groupedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  const groupedStep = groupedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(groupedStep?.id, 'native Group Apply must save the exact Group step before the ONE choice');
  assert.deepEqual(groupedStep.operation.group.keys ?? [], []);
  assert.equal(groupedStep.operation.group.aggregates?.[0]?.operation, 'COUNT_ROWS');
  const groupedGrid = await readTable();
  const groupedCountIndex = findRenderedBuilderHeaderIndex(groupedGrid.headers, 'row count');
  assert.equal(groupedGrid.rows.length, 1);
  assert.equal(groupedGrid.rows[0]?.[groupedCountIndex], String(fixtureIDs.length));

  await workflow.action('open Add Columns for raw Patient ID', page.getByTestId('construction-action-add-columns'),
    () => page.getByTestId('construction-action-add-columns').click(), {
      after: () => page.getByRole('region', { name: 'Add columns editor', exact: true }).waitFor({ state: 'visible' }),
    });
  await workflow.action('choose raw fields and related data', page.getByRole('button', { name: 'Fields and related data', exact: true }),
    () => page.getByRole('button', { name: 'Fields and related data', exact: true }).click(), {
      after: () => page.getByRole('combobox', { name: 'Values per grouped row', exact: true }).waitFor({ state: 'visible' }),
    });
  const addColumnsPolicy = page.getByRole('combobox', { name: 'Values per grouped row', exact: true });
  await workflow.action('set Patient ID grouped-row policy to ONE', addColumnsPolicy,
    () => addColumnsPolicy.selectOption('ONE'), {
      after: async () => assert.equal(await addColumnsPolicy.inputValue(), 'ONE'),
    });
  const rawFieldsSummary = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  await workflow.action('open raw FHIR Patient fields', rawFieldsSummary, () => rawFieldsSummary.click(), {
    after: () => page.locator('[data-testid="feature-catalog-raw-fields"] input[aria-label="Select Patient.id"]').waitFor({ state: 'visible' }),
  });
  const patientIDChoice = page.locator('[data-testid="feature-catalog-raw-fields"] input[aria-label="Select Patient.id"]');
  assert.equal(await patientIDChoice.count(), 1, 'raw Patient.id must be the unique native field choice');
  await workflow.action('select raw Patient.id with ONE', patientIDChoice, () => patientIDChoice.check(), {
    after: async () => assert.equal(await patientIDChoice.isChecked(), true),
  });
  const beforeONE = await readBuilder();
  assert.equal(beforeONE.draftVersion, groupedBuilder.draftVersion);
  assert.equal(beforeONE.draftDigest, groupedBuilder.draftDigest);
  assert.equal(beforeONE.catalog?.generation, context.target.fixtureGeneration);
  assert.equal(beforeONE.catalog?.snapshotToken, groupedBuilder.catalog?.snapshotToken);
  const choiceProposalPath = `${apiRoot}/authoring/v2/construction-choice-proposals`;
  const uiOrigin = new URL(context.target.uiUrl).origin;
  const expectedONEResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === uiOrigin && url.pathname === choiceProposalPath
      && response.request().method() === 'POST' && response.status() === 422;
  }, { timeout: 5000 });
  const addSelected = page.getByRole('button', { name: /Add 1 selected feature/ });
  await workflow.action('submit raw Patient.id with ONE for preview', addSelected, () => addSelected.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'error'
      && Boolean(document.querySelector('[data-testid="construction-choice-proposal-panel"] [role="alert"]'))),
  });
  const oneResponse = await expectedONEResponse;
  const oneResponseBody = await oneResponse.json();
  const oneRequestBody = oneResponse.request().postDataJSON();
  const oneChoice = oneRequestBody?.constructionChoices?.[0];
  assert.equal(oneResponse.status(), 422, 'raw Patient.id ONE disagreement must return exact HTTP 422');
  assert.equal(oneResponseBody?.error?.code, 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES');
  assert.equal(oneRequestBody?.outputId, outputId);
  assert.equal(oneRequestBody?.expectedDraftVersion, beforeONE.draftVersion);
  assert.equal(oneRequestBody?.expectedDraftDigest, beforeONE.draftDigest);
  assert.equal(oneRequestBody?.snapshotToken, beforeONE.catalog.snapshotToken);
  assert.equal(oneRequestBody?.constructionChoices?.length, 1);
  assert.equal(oneChoice?.rowValuePolicy, 'ONE');
  assert.equal(oneChoice?.title, sourceIdColumn.label);
  assert.equal(typeof oneChoice?.choiceId, 'string', 'captured ONE request must identify its exact native Patient.id field choice');
  assert(oneChoice.choiceId, 'captured ONE request must have a non-empty Patient.id choice identity');
  const oneFailureState = await page.evaluate(({ policySelector, fieldSelector }) => {
    const editor = document.querySelector('[aria-label="Add columns editor"]');
    const proposal = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    const field = document.querySelector(fieldSelector);
    const policy = editor?.querySelector(policySelector);
    return {
      editorOpen: Boolean(editor),
      proposalStatus: proposal?.dataset.proposalStatus,
      errorVisible: Boolean(proposal?.querySelector('[role="alert"]')),
      fieldSelected: Boolean(field?.checked),
      policy: policy?.value,
      allAvailable: [...(policy?.options ?? [])].some(option => option.value === 'ALL'),
    };
  }, { policySelector: 'select[aria-label="Values per grouped row"]',
    fieldSelector: '[data-testid="feature-catalog-raw-fields"] input[aria-label="Select Patient.id"]' });
  assert.deepEqual(oneFailureState, {
    editorOpen: true, proposalStatus: 'error', errorVisible: true,
    fieldSelected: true, policy: 'ONE', allAvailable: true,
  }, 'the ONE refusal must keep Patient.id selected and expose ALL in the same editor');
  const afterONE = await readBuilder();
  assert.deepEqual(afterONE.workspace, beforeONE.workspace, 'rejected Patient.id ONE must not change the saved Group workspace');
  assert.equal(afterONE.draftVersion, beforeONE.draftVersion);
  assert.equal(afterONE.draftDigest, beforeONE.draftDigest);

  const expectedDiagnostic = report.network.filter(record => record.kind === 'network'
    && record.status === 422 && record.method === 'POST' && record.rawURL === oneResponse.url());
  assert.equal(expectedDiagnostic.length, 1, 'the exact expected native 422 must be present once in fixture network evidence');
  const consoleDiagnostics = report.network.filter(record => record.kind === 'console-error'
    && record.location === `${uiOrigin}${choiceProposalPath}` && /422/.test(record.text ?? ''));
  report.expectedFailures ??= [];
  report.expectedFailures.push({ method: 'POST', path: choiceProposalPath, status: 422,
    code: oneResponseBody.error.code, outputId, draftVersion: beforeONE.draftVersion,
    draftDigest: beforeONE.draftDigest, snapshotToken: beforeONE.catalog.snapshotToken,
    field: { choiceId: oneChoice.choiceId, title: oneChoice.title, rowValuePolicy: oneChoice.rowValuePolicy },
    rawPatientIDs: fixtureIDs, networkDiagnostics: expectedDiagnostic, consoleDiagnostics });
  for (const diagnostic of [...expectedDiagnostic, ...consoleDiagnostics]) {
    const index = report.network.indexOf(diagnostic);
    if (index >= 0) report.network.splice(index, 1);
  }
  requireCheck(workflow, 'correctness', 'Patient.id ONE returns the exact multiple-values 422 for both raw IDs', true,
    { status: oneResponse.status(), code: oneResponseBody.error.code, path: choiceProposalPath,
      request: { outputId: oneRequestBody.outputId, draftVersion: oneRequestBody.expectedDraftVersion,
        draftDigest: oneRequestBody.expectedDraftDigest, snapshotToken: oneRequestBody.snapshotToken,
        choiceId: oneChoice.choiceId, title: oneChoice.title, rowValuePolicy: oneChoice.rowValuePolicy },
      rawPatientIDs: fixtureIDs, savedWorkspaceUnchanged: true });
  requireCheck(workflow, 'persistence', 'rejected Patient.id ONE leaves the saved Group and exact draft unchanged', true,
    { draftVersion: afterONE.draftVersion, draftDigest: afterONE.draftDigest,
      groupStepID: groupedStep.id, workspaceUnchanged: true });

  await workflow.action('change the retained Patient.id selection to ALL', addColumnsPolicy,
    () => addColumnsPolicy.selectOption('ALL'), {
      after: async () => assert.equal(await addColumnsPolicy.inputValue(), 'ALL'),
    });
  const recoveryState = await page.evaluate(({ policySelector, fieldSelector }) => {
    const editor = document.querySelector('[aria-label="Add columns editor"]');
    const proposal = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    const field = document.querySelector(fieldSelector);
    const policy = editor?.querySelector(policySelector);
    return { editorOpen: Boolean(editor), proposalStatus: proposal?.dataset.proposalStatus,
      fieldSelected: Boolean(field?.checked), policy: policy?.value,
      allAvailable: [...(policy?.options ?? [])].some(option => option.value === 'ALL') };
  }, { policySelector: 'select[aria-label="Values per grouped row"]',
    fieldSelector: '[data-testid="feature-catalog-raw-fields"] input[aria-label="Select Patient.id"]' });
  assert.deepEqual(recoveryState, { editorOpen: true, proposalStatus: 'error', fieldSelected: true,
    policy: 'ALL', allAvailable: true }, 'ALL repair must retain the same selection in the open editor');
  requireCheck(workflow, 'usability', 'failed Patient.id ONE retains its selected field for same-editor ALL repair', true,
    { afterONE: oneFailureState, afterSwitchToALL: recoveryState });
  const expectedALLResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === uiOrigin && url.pathname === choiceProposalPath
      && response.request().method() === 'POST' && response.status() === 200;
  }, { timeout: 5000 });
  await workflow.action('resubmit the retained Patient.id field with ALL', addSelected, () => addSelected.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && document.querySelector('[data-testid="construction-preview"] [data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready'),
  });
  const allResponse = await expectedALLResponse;
  const allResponseBody = await allResponse.json();
  const allRequestBody = allResponse.request().postDataJSON();
  const allChoice = allRequestBody?.constructionChoices?.[0];
  assert.equal(allRequestBody?.outputId, outputId);
  assert.equal(allRequestBody?.expectedDraftVersion, beforeONE.draftVersion);
  assert.equal(allRequestBody?.expectedDraftDigest, beforeONE.draftDigest);
  assert.equal(allRequestBody?.snapshotToken, beforeONE.catalog.snapshotToken);
  assert.equal(allRequestBody?.constructionChoices?.length, 1);
  assert.equal(allChoice?.choiceId, oneChoice.choiceId,
    'ALL repair must use the same raw Patient.id catalog selection as the rejected ONE request');
  assert.equal(allChoice?.rowValuePolicy, 'ALL');
  assert.equal(allChoice?.title, sourceIdColumn.label);
  const allPreview = await readChoicePreview();
  const idHeaderIndex = findRenderedBuilderHeaderIndex(allPreview.headers, sourceIdColumn.label);
  assert.equal(allPreview.rows.length, 1, 'ALL repair preview must retain one empty-key Group row');
  assert(idHeaderIndex >= 0, `ALL repair preview must expose ${sourceIdColumn.label}: ${JSON.stringify(allPreview.headers)}`);
  const rawPreviewIDs = JSON.parse(allPreview.rows[0][idHeaderIndex].raw);
  assert.deepEqual(sorted(rawPreviewIDs), fixtureIDs,
    'ALL repair preview must contain exactly the independent literal Patient IDs');
  requireCheck(workflow, 'correctness', 'same-editor ALL repair preview matches both literal raw Patient IDs', true,
    { status: allResponse.status(), code: allResponseBody?.error?.code ?? null,
      choiceId: allChoice.choiceId, rowValuePolicy: allChoice.rowValuePolicy,
      headers: allPreview.headers, rows: allPreview.rows, rawPatientIDs: sorted(rawPreviewIDs), expectedPatientIDs: fixtureIDs });
  await workflow.action('Apply ALL repair for raw Patient.id', page.getByRole('button', { name: 'Apply columns', exact: true }),
    () => page.getByRole('button', { name: 'Apply columns', exact: true }).click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: [{ [sourceIdColumn.label]: { titleJsonArray: fixtureIDs } }] }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI repaired the Patient.id row-value policy with ALL and applied it');
  let appliedBuilder = await readBuilder();
  let appliedDocument = appliedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  let appliedGroup = appliedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const addedPatientIDColumns = appliedDocument?.columns?.filter(column =>
    column.source?.field?.path === 'id' && (column.columnId ?? column.id) !== sourceIdColumnId) ?? [];
  assert.equal(addedPatientIDColumns.length, 1, 'ALL repair must save one new native Patient.id field column');
  const addedPatientIDColumnId = addedPatientIDColumns[0].columnId ?? addedPatientIDColumns[0].id;
  const appliedPatientIDValue = appliedGroup?.rowValues?.find(value => value.inputColumnId === addedPatientIDColumnId);
  assert.equal(appliedPatientIDValue?.policy, 'ALL', 'saved Group must retain ALL for the exact Patient.id input');
  await page.reload({ waitUntil: 'domcontentloaded' });
  const tableSelector = `[data-testid="construction-table-${outputId}"]`;
  await page.locator(tableSelector).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.locator(tableSelector).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: [{ [sourceIdColumn.label]: { titleJsonArray: fixtureIDs } }] });
  appliedBuilder = await readBuilder();
  appliedDocument = appliedBuilder.workspace?.documents?.find(document => document.output?.id === outputId);
  appliedGroup = appliedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  const reloadedPatientIDColumns = appliedDocument?.columns?.filter(column =>
    column.source?.field?.path === 'id' && (column.columnId ?? column.id) !== sourceIdColumnId) ?? [];
  assert.equal(reloadedPatientIDColumns.length, 1, 'reload must retain exactly one added Patient.id field column');
  const reloadedPatientIDColumnId = reloadedPatientIDColumns[0].columnId ?? reloadedPatientIDColumns[0].id;
  const reloadedPatientIDValue = appliedGroup?.rowValues?.find(value => value.inputColumnId === reloadedPatientIDColumnId);
  assert.equal(reloadedPatientIDValue?.policy, 'ALL', 'reloaded saved Group must retain ALL for Patient.id');
  const rendered = await page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    const rows = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    const headers = [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim());
    const cells = rows.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => ({
      text: cell.innerText.trim(), raw: cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title'),
    })));
    return { headers, cells };
  }, { tableSelector: renderedTableSelector });
  const renderedIDIndex = findRenderedBuilderHeaderIndex(rendered.headers, sourceIdColumn.label);
  const renderedRawIDs = renderedIDIndex < 0 ? [] : rendered.cells.map(row => JSON.parse(row[renderedIDIndex].raw));
  assert.equal(rendered.cells.length, 1, 'reloaded ALL repair must still show exactly one empty-key group row');
  assert.deepEqual(sorted(renderedRawIDs[0] ?? []), fixtureIDs,
    'reloaded Builder row must display the exact raw Patient IDs after ALL repair');
  requireCheck(workflow, 'persistence', 'ALL repair applies and persists exact Patient IDs after reload', true,
    { draftVersion: appliedBuilder.draftVersion, draftDigest: appliedBuilder.draftDigest,
      groupStepID: appliedGroup?.id, rowValuePolicy: reloadedPatientIDValue.policy,
      headers: rendered.headers, cells: rendered.cells, rawPatientIDs: sorted(renderedRawIDs[0]), expectedPatientIDs: fixtureIDs,
      fixtureOracleSha256: report.target.qaIsolation.fixtureOracle.sha256 });
  report.target.qaIsolation.requiredLifecycleChecks = [
    'empty-key Group prepares one row for the two raw Patient records',
    'Patient.id ONE returns the exact multiple-values 422 for both raw IDs',
    'rejected Patient.id ONE leaves the saved Group and exact draft unchanged',
    'failed Patient.id ONE retains its selected field for same-editor ALL repair',
    'same-editor ALL repair preview matches both literal raw Patient IDs',
    'ALL repair applies and persists exact Patient IDs after reload',
  ];
};
