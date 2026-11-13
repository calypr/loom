import assert from 'node:assert/strict';
import { expect } from 'playwright/test';
const sorted = rows => structuredClone(rows).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
const documentFor = state => {
  const document = state.workspace?.documents.find(document => document.output.id === 'ResearchSubject');
  assert(document);
  return document;
};
const draftIdentity = state => ({ version: state.draftVersion, digest: state.draftDigest });

export async function runGraphRowsUpstreamDependency({ run, page }) {
  await run.open();
  const original = await run.state();
  const originalDocument = documentFor(original);
  const originalPreview = await run.preview();
  const editor = page.getByTestId('construction-reshape-group');
  const proposal = page.getByTestId('construction-proposal-panel');
    // A saved Group genuinely depends on the Patient traversal. Removing its
    // source must be rejected before Apply, preserving every workspace output.
    const patientID = originalDocument.columns.filter(column => column.source?.imported?.outputColumn === 'patient__patient_id');
    assert.equal(patientID.length, 1, 'Bind the exact pinned Patient ID, not a same-type catalog path');
    await page.getByRole('button', { name: 'One row per', exact: true }).click();
    await page.getByTestId('construction-action-group-rows').click();
    await editor.getByText('Add grouping field', { exact: true }).click();
    await editor.getByTestId('construction-group-by-field-list').locator(`input[type="checkbox"][value="${patientID[0].columnId}"]`).click();
    await expect(editor.getByTestId('construction-group-selected-keys').locator(`input[type="checkbox"][value="${patientID[0].columnId}"]`)).toBeChecked();
    await editor.getByText('Add grouping field', { exact: true }).click();
    const groupAdvanced = editor.getByTestId('construction-reshape-group-advanced');
    await groupAdvanced.locator('summary').first().click();
    await editor.getByRole('textbox', { name: 'Group output name 1', exact: true }).fill('patient_id');
    await groupAdvanced.locator('summary').first().click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    const patientGroups = await run.preview({ candidate: true });
    const patientKey = 'patient_id';
    assert.deepEqual(sorted(patientGroups.rows.map(row => [row[patientKey], row.row_count])), sorted([['p-zero', 1], ['p-one', 1], ['p-many', 2]]));
    await run.action('Apply Group depending on the Patient source', async () => {
      await page.getByTestId('construction-apply-proposal').click();
      await expect(proposal).toHaveCount(0);
    });
    const dependentSaved = await run.state();
    await run.action('Return to table after dependent Group', async () => {
      const rowDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      await rowDialog.getByRole('button', { name: 'Back to table', exact: true }).click();
      await expect(rowDialog).toHaveCount(0);
      assert.deepEqual(draftIdentity(await run.state()), draftIdentity(dependentSaved));
    });
    const dependentGroup = documentFor(dependentSaved).construction.steps.find(item => item.operation.kind === 'GROUP');
    assert.equal(dependentGroup.operation.group.keys[0].inputColumnId, patientID[0].columnId);
    assert.equal(dependentGroup.outputs.find(column => column.id === dependentGroup.operation.group.keys[0].outputColumnId).name, patientKey);
    const valuesBeforeReject = (await run.preview()).rows;
    const patientTraversal = documentFor(dependentSaved).importedSource.steps.filter(item => item.kind === 'TRAVERSAL' && item.id === 'traversals[0]');
    assert.equal(patientTraversal.length, 1);
    const upstream = page.getByTestId(`imported-source-step-${patientTraversal[0].id}`);
    const upstreamDetails = upstream.locator('xpath=ancestor::details[1]');
    if (await upstreamDetails.count() && await upstreamDetails.getAttribute('open') === null) await upstreamDetails.locator('summary').click();
    await upstream.click();
    await page.getByRole('checkbox', { name: 'Enable this source step', exact: true }).uncheck();
    const upstreamReview = page.getByRole('region', { name: 'Proposed source step', exact: true });
    const rejectedResponse = page.waitForResponse(response => response.url().endsWith('/source-relationship-proposals'));
    await run.action('Reject upstream source removal with a saved Group dependent', async () => {
      await upstreamReview.getByRole('button', { name: 'Preview source step', exact: true }).click();
      const response = await rejectedResponse;
      assert.equal(response.status(), 422);
      const failure = await response.json();
      assert.equal(failure.error?.code, 'INVALID_IMPORTED_RELATIONSHIP');
      assert.match(failure.error.message, /column|GROUP|group/i, 'The rejection must identify the missing downstream binding');
      await expect(upstreamReview.getByRole('alert')).toBeVisible();
      await expect(upstreamReview.getByTestId('imported-source-step-apply')).toBeDisabled();
      run.report.expectedRejectedResponses = [{ method: 'POST', pathname: new URL(response.url()).pathname, status: 422, errorCode: failure.error.code }];
      run.report.upstreamDependencyRejection = { stepId: patientTraversal[0].id, dependentGroupId: dependentGroup.id, errorCode: failure.error.code, message: failure.error.message };
      const unchanged = await run.state();
      assert.deepEqual(draftIdentity(unchanged), draftIdentity(dependentSaved));
      assert.deepEqual(unchanged.workspace, dependentSaved.workspace, 'A rejected source edit preserves every authored workspace output');
      assert.deepEqual(sorted((await run.preview()).rows), sorted(valuesBeforeReject));
    });
    await upstreamReview.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByTestId(`construction-history-step-${dependentGroup.id}`).click();
    await page.getByTestId(`construction-remove-step-${dependentGroup.id}`).click();
    await expect(proposal).toHaveAttribute('data-proposal-status', 'ready');
    await page.getByTestId('construction-apply-proposal').click();
    await expect(proposal).toHaveCount(0);
    await run.action('Reload dependency Group removal and restore exact source', async () => {
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.getByTestId('construction-table-ResearchSubject').click();
      assert.deepEqual(sorted((await run.preview()).rows), sorted(originalPreview.rows));
    });
    const final = await run.state();
    assert(!(documentFor(final).construction?.steps ?? []).some(step => step.id === dependentGroup.id));
    assert.deepEqual(documentFor(final).importedRecipe, originalDocument.importedRecipe);
    assert.deepEqual(documentFor(final).columns, originalDocument.columns);
    const commandTypes = run.report.requests.flatMap(request => request.commandTypes ?? []);
    assert(!commandTypes.includes('UPDATE_IMPORTED_SOURCE_STEP'));
    assert(!commandTypes.includes('APPLY_IMPORTED_RELATIONSHIP_PROPOSAL'), 'A rejected source proposal must never become a save command');
}
