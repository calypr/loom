import assert from 'node:assert/strict';
import test from 'node:test';
import { api, readFixture, withFreshUIExplorer } from './app.mjs';

test('create an identifier column rule, preview its values, and reopen it from Recipe steps', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient');
  const expected = patients.flatMap(patient => patient.identifier?.map(item => item.value) ?? []);
  assert(expected.length > 2);

  await withFreshUIExplorer('identifier rule', async (scenario, page) => {
    const clone = await api(scenario.root, {
      method: 'POST',
      body: { name: `${scenario.explorerId}-imported`, sourceExplorerId: 'dataframe-default' },
    });
    const base = `${scenario.root}/${encodeURIComponent(clone.explorerId)}/authoring/v2`;
    const url = new URL(scenario.url);
    url.searchParams.set('explorer', clone.explorerId);
    await page.navigate(url.toString());
    return { base, clone };
  }, async ({ scenario, fixture, page }) => {
    const readDraft = () => api(`${fixture.base}/builder`);
    const before = await readDraft();
    assert.equal(before.workspace.documents[0].rootResourceType, 'Patient');
    assert(before.workspace.documents[0].importedRecipe);

    try {
      const addColumns = '[data-testid="construction-action-add-columns"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector('${addColumns}:not(:disabled)'))`), 'Add columns is ready');
      await page.click(addColumns);
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.clickText('Review column rule');
      await page.clickText('Preview column rule');
      await page.until(() => page.evaluate(`[...document.querySelectorAll('button')].some(button => button.innerText === 'Apply column rule' && !button.disabled)`), 'identifier rule preview is ready');
      await page.until(async () => {
        const rows = await page.tableRows();
        return expected.every(value => rows.some(row => row.includes(value)));
      }, 'candidate table shows the fixture identifier values');
      assert.equal((await readDraft()).draftDigest, before.draftDigest, 'Preview must leave the saved draft unchanged');
      await page.clickText('Apply column rule');

      const saved = await page.until(async () => {
        const draft = await readDraft();
        return draft.draftDigest !== before.draftDigest ? draft : false;
      }, 'identifier rule is saved');
      const savedDocument = saved.workspace.documents[0];
      assert.deepEqual(savedDocument.importedRecipe, before.workspace.documents[0].importedRecipe);
      const [addition] = savedDocument.importedSource.dynamicAdditions;
      assert.equal(addition.parameters.dynamic.name, 'identifiers');
      assert.equal(savedDocument.importedSource.steps.find(step => step.id === addition.stepId).editable, true);

      await page.reload();
      const rows = await page.until(async () => {
        const current = await page.tableRows();
        return expected.every(value => current.some(row => row.includes(value))) ? current : false;
      }, 'reloaded rows contain the fixture identifier values');
      for (const patient of patients) {
        assert(rows.some(row => row.includes(patient.id) && patient.identifier.every(item => row.includes(item.value))),
          `Identifiers stay paired with patient ${patient.id}`);
      }
      const editRule = 'button[aria-label^="Edit source operation"][aria-label$="identifiers"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(editRule)}))`),
        'created rule is available in Recipe steps');
      await page.click(editRule);
      await page.until(() => page.evaluate('Boolean(document.querySelector(\'input[aria-label="Rule name"][value="identifiers"]\'))'),
        'saved rule opens the shared identifier form');
      await page.selectOptionMatching('fieldset[aria-label="Source step parameters"] select', 'Last part of the identifier system');
      await page.clickText('Preview source step');
      await page.until(() => page.evaluate(`Boolean(document.querySelector('[data-testid="imported-source-step-apply"]:not(:disabled)'))`),
        'edited rule preview is ready');
      await page.click('[data-testid="imported-source-step-apply"]');
      await page.until(async () => {
        const draft = await readDraft();
        const rule = draft.workspace.documents[0].importedSource.dynamicAdditions[0];
        return rule.stepId === addition.stepId && rule.parameters.dynamic.key.call === 'last_segment';
      }, 'edited rule keeps its saved step identity');
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  });
});
