import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
import { validateGraphRowsFixture } from '../helpers/graph-rows-fixture-oracle.mjs';

const documentFor = state => state.workspace.documents.find(document => document.output.id === 'ResearchSubject');
const sorted = rows => structuredClone(rows).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));

export async function runGraphRowsRootProposal({ run, page }) {
  const assessments = [];
  page.on('response', async response => {
    if (response.url().endsWith('/authoring/v2/row-change') && response.request().method() === 'POST' && response.status() === 200) {
      try { assessments.push(await response.json()); } catch { /* Cancelled reads are not usable assessments. */ }
    }
  });
  await run.open();
  const original = await run.state(); const document = documentFor(original);
  const originalRows = (await run.preview()).rows;
  const otherOutputs = original.workspace.documents.filter(item => item.output.id !== 'ResearchSubject');
  const oracle = validateGraphRowsFixture();
  const choosePatient = async () => {
    await page.getByRole('navigation', { name: 'Recipe presentation', exact: true }).getByRole('button', { name: 'Both editors', exact: true }).click();
    await page.getByTestId('row-recipe-expression').getByRole('button', { name: /^Edit source rows:/ }).click();
    const settings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    await settings.getByTestId('construction-row-intent-records').click();
    const root = settings.getByRole('combobox', { name: 'Record type', exact: true });
    const choices = await root.locator('option').evaluateAll(options => options.filter(option => !option.disabled && /^Patient(?: via |$)/.test(option.textContent)).map(option => option.value));
    assert.equal(choices.length, 1, 'Select the exact default Patient occurrence, not an arbitrary repeated resource');
    const before = assessments.length;
    await root.selectOption(choices[0]);
    await expect.poll(() => assessments.length).toBeGreaterThan(before);
    const blocked = assessments.at(-1);
    assert.equal(blocked.status, 'BLOCKED');
    assert(blocked.unresolved.some(item => item.code === 'IMPORTED_SOURCE_ROOT_OVERLAY_REQUIRED'));
    await expect(settings.locator('[aria-label="Record type choices"]')).toContainText('typed source-root overlay');
    assert.deepEqual((await run.state()).workspace, original.workspace, 'Imported preserve-root rejection cannot mutate any output');
    return settings;
  };
  const startReplacement = async () => {
    const settings = await choosePatient();
    const before = assessments.length;
    await settings.getByRole('button', { name: 'Start fresh with Patient', exact: true }).click();
    const dialog = page.getByRole('region', { name: 'Replace this table’s current setup?', exact: true });
    await expect(dialog.getByRole('button', { name: 'Change starting record type', exact: true })).toBeEnabled();
    await expect.poll(() => assessments.length).toBeGreaterThan(before);
    const assessment = assessments.at(-1);
    assert.equal(assessment.status, 'READY'); assert.equal(assessment.mode, 'REPLACE');
    assert.equal(assessment.candidateRootResourceType, 'Patient');
    assert.equal(assessment.candidateDocument.output.id, document.output.id);
    assert.equal(assessment.candidateDocument.rootResourceType, 'Patient');
    assert.equal(assessment.candidateDocument.importedRecipe, undefined);
    assert.equal(assessment.candidateNetwork.owner, 'DOCUMENT');
    assert.equal(assessment.candidateNetwork.occurrences[0].resourceType, 'Patient');
    assert(assessment.candidateRowIdentity);
    await expect(dialog).toContainText('detaches this table from its pinned recipe');
    await expect(dialog).toContainText('resets its source changes and table setup');
    await expect(dialog.getByRole('region', { name: 'Replacement consequences', exact: true })).toContainText('Patient');
    const preview = await run.preview({ candidate: true });
    assert.equal(preview.receiptId, assessment.candidateReceiptId);
    const idColumns = assessment.candidateDocument.columns.filter(column => column.source.kind === 'field' && /(?:^|\.)id$/.test(column.source.field.path));
    assert.equal(idColumns.length, 1);
    assert.deepEqual(sorted(preview.rows.map(row => row[idColumns[0].column])), sorted(oracle.replacementPatientIDs));
    await expect(page.getByTestId('row-recipe-expression')).toHaveAttribute('data-recipe-state', 'proposed');
    await expect(page.getByTestId('row-recipe-expression').locator('[data-term-kind="source-rows"]')).toContainText('Patient');
    const graph = page.getByTestId('authored-traversal-editor');
    await expect(graph).toHaveAttribute('data-projection-kind', 'proposed');
    const starting = graph.getByRole('navigation', { name: 'Authored occurrences', exact: true }).locator('button[data-occurrence-id="base"]');
    await expect(starting).toContainText('Patient');
    assert.deepEqual((await run.state()).workspace, original.workspace, 'Replacement Preview cannot detach the saved pin');
    return { dialog, assessment, idColumn: idColumns[0] };
  };
  await run.action('Root proposal: explicitly keep current rows after the imported preserve-root rejection', async () => {
    const settings = await choosePatient();
    await settings.getByRole('button', { name: 'Keep current rows', exact: true }).click();
    assert.deepEqual((await run.state()).workspace, original.workspace);
    await settings.getByRole('button', { name: 'Back to table', exact: true }).click();
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalRows));
  });
  const first = await run.action('Root proposal: imported preserve rejection and explicit replacement Preview', startReplacement);
  await run.action('Root proposal: Cancel preserves pin, draft and every original value', async () => {
    await first.dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(first.dialog).toHaveCount(0);
    const settings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    if (await settings.isVisible()) await settings.getByRole('button', { name: 'Back to table', exact: true }).click();
    const state = await run.state();
    assert.deepEqual([state.draftVersion, state.draftDigest], [original.draftVersion, original.draftDigest]);
    assert.deepEqual(state.workspace, original.workspace);
    assert.deepEqual(sorted((await run.preview()).rows), sorted(originalRows));
  });
  const second = await startReplacement();
  await run.action('Root proposal: Apply previewed explicit detachment and reset', async () => {
    await second.dialog.getByRole('button', { name: 'Change starting record type', exact: true }).click();
    await expect(second.dialog).toHaveCount(0);
    await expect(page.getByTestId('construction-preview')).toHaveAttribute('data-preview-proposal-id', '');
  });
  await run.action('Root proposal: reload Patient rows and unchanged unrelated pins', async () => {
    await page.reload({ waitUntil: 'domcontentloaded' }); await page.getByTestId('construction-table-ResearchSubject').click();
    const state = await run.state(); const after = documentFor(state);
    assert.equal(after.output.id, document.output.id); assert.equal(after.rootResourceType, 'Patient');
    assert.equal(after.importedRecipe, undefined); assert.equal(after.importedSource, undefined);
    assert.equal(after.construction?.steps?.length ?? 0, 0); assert.equal(after.columns.length, 1);
    assert.deepEqual(state.workspace.documents.filter(item => item.output.id !== 'ResearchSubject'), otherOutputs);
    const preview = await run.preview();
    assert.deepEqual(sorted(preview.rows.map(row => row[after.columns[0].column])), sorted(oracle.replacementPatientIDs));
    await expect(page.getByTestId('row-recipe-expression').locator('[data-term-kind="source-rows"]')).toContainText('Patient');
    await expect(page.getByTestId('authored-traversal-editor').getByRole('navigation', { name: 'Authored occurrences', exact: true }).locator('button[data-occurrence-id="base"]')).toContainText('Patient');
  });
  assert.equal(run.report.requests.flatMap(request => request.commandTypes ?? []).filter(type => type === 'APPLY_TABLE_ROOT_REBASE').length, 1);
}
