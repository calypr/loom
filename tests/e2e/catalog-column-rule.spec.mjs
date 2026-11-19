import assert from 'node:assert/strict';
import test from 'node:test';
import { api, readFixture, withFreshUIExplorer } from './app.mjs';

test('create a catalog projection rule, review generated fields, and reload it', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient');
  assert(patients.length > 0 && patients.every(patient => patient.birthDate));

  await withFreshUIExplorer('catalog column rule', async (scenario, page) => {
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
    try {
      const before = await readDraft();
      const document = before.workspace.documents.find(item => item.rootResourceType === 'Patient');
      assert(document?.importedRecipe, 'catalog rules are available on imported Patient tables');

      await page.click('[data-testid="construction-action-add-columns"]');
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.until(() => page.evaluate(`[...document.querySelectorAll('button')].some(button => button.innerText === 'birthDate' && !button.disabled)`), 'the advanced column source is available');
      await page.clickText('birthDate');
      assert.equal(await page.evaluate(`[...document.querySelectorAll('button')].find(button => button.innerText === 'birthDate')?.getAttribute('aria-pressed')`), 'true');
      assert.ok(await page.evaluate(`Boolean(document.querySelector('[aria-label="Available advanced columns"]'))`));
      assert.equal(await page.evaluate(`Boolean(document.querySelector('input[aria-label="Include gender"]'))`), false);
      assert.equal(await page.evaluate(`[...document.querySelectorAll('button')].some(button => button.innerText === 'Preview field')`), false);
      await page.until(() => page.evaluate(`/birth[_ ]?date/i.test(document.querySelector('[aria-label="Generated columns"]')?.innerText ?? '')`),
        'the selected field can be previewed directly');
      await page.clickText('gender');
      await page.until(() => page.evaluate(`/gender/i.test(document.querySelector('[aria-label="Generated columns"]')?.innerText ?? '')`),
        'choosing another field replaces the active preview');
      assert.equal(await page.evaluate(`[...document.querySelectorAll('button')].find(button => button.innerText === 'birthDate')?.getAttribute('aria-pressed')`), 'false');
      assert.equal((await readDraft()).draftDigest, before.draftDigest, 'Switching fields does not change the saved draft');
      await page.pressEscape();
      await page.clickText('Build a multi-field rule');
      await page.clickLabel('Include birthDate', 'input');
      await page.clickLabel('Include gender', 'input');
      await page.clickLabel('Exclude gender', 'input');
      await page.until(() => page.evaluate(`Boolean(document.querySelector('[aria-label="Generated columns"]'))`),
        'generated column receipt appears');

      const generatedReceipt = await page.evaluate(`document.querySelector('[aria-label="Generated columns"]')?.innerText ?? ''`);
      assert.match(generatedReceipt, /birth[_ ]?date/i, 'receipt names the included field');
      assert.doesNotMatch(generatedReceipt, /gender/i, 'receipt omits the excluded field');
      const candidateRows = await page.until(async () => {
        const rows = await page.tableRows();
        return patients.every(patient => rows.some(row => row.includes(patient.id) && row.includes(patient.birthDate))) ? rows : false;
      }, 'catalog-projected values are shown');
      for (const patient of patients) {
        assert(candidateRows.some(row => row.includes(patient.id) && row.includes(patient.birthDate)),
          `preview pairs ${patient.birthDate} with ${patient.id}`);
      }
      assert.equal((await readDraft()).draftDigest, before.draftDigest, 'Preview leaves the saved draft unchanged');
      await page.clickText('Apply column rule');

      const saved = await page.until(async () => {
        const draft = await readDraft();
        return draft.draftDigest !== before.draftDigest ? draft : false;
      }, 'catalog rule is saved');
      const savedDocument = saved.workspace.documents.find(item => item.output.id === document.output.id);
      const [addition] = savedDocument.importedSource.ruleAdditions;
      assert.equal(addition.kind, 'CATALOG_PROJECTION');
      assert.deepEqual(addition.parameters.catalog.includePaths, ['birthDate', 'gender']);
      assert.deepEqual(addition.parameters.catalog.excludePaths, ['gender']);
      assert.equal(savedDocument.importedSource.steps.find(step => step.id === addition.stepId).kind, 'CATALOG_PROJECTION');
      assert(savedDocument.importedSource.ruleAdditions.length >= 1, 'the catalog rule remains one recipe step');

      await page.reload();
      const reloaded = await page.until(async () => {
        const current = await page.tableRows();
        return patients.every(patient => current.some(row => row.includes(patient.id) && row.includes(patient.birthDate)))
          ? current : false;
      }, 'reloaded rows contain catalog-projected birth dates');
      for (const patient of patients) {
        assert(reloaded.some(row => row.includes(patient.id) && row.includes(patient.birthDate)),
          `reloaded projection pairs ${patient.birthDate} with ${patient.id}`);
      }
      await page.click('[data-testid="construction-action-add-columns"]');
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.click('[role="tab"][data-column-tab="saved"]');
      const rules = '[aria-label="Configured column rules"]';
      await page.until(() => page.evaluate(`/birth[_ ]?date/i.test(document.querySelector(${JSON.stringify(rules)})?.innerText ?? '')`),
        'saved rule shows the birth date column');
      assert.equal(await page.evaluate(`Boolean(document.querySelector('[aria-label="Available advanced columns"]'))`), false);
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.until(() => page.evaluate(`Boolean(document.querySelector('[aria-label="Available advanced columns"]'))`),
        'available actions return when switching to Add columns');
      await page.clickText('gender');
      await page.until(() => page.evaluate(`/gender/i.test(document.querySelector('[aria-label="Generated columns"]')?.innerText ?? '')`),
        'a second catalog rule previews its own column without ambiguity');
      await page.clickText('Apply column rule');
      const secondRules = await page.until(async () => {
        const draft = await readDraft();
        const additions = draft.workspace.documents[0].importedSource.ruleAdditions;
        return additions?.length === 2 ? additions : false;
      }, 'both catalog rules are saved');
      assert.equal(secondRules[0].stepId, addition.stepId);
      assert.deepEqual(secondRules.map(rule => rule.parameters.catalog.name), ['catalog_fields', 'catalog_fields_2']);
      const removedRule = secondRules[1];
      const recipeStep = '[data-operation-id="' + removedRule.stepId + '"]';
      const removeStep = recipeStep + ' button[aria-label^="Remove source operation"]';
      await page.until(() => page.evaluate('Boolean(document.querySelector(' + JSON.stringify(removeStep) + '))'), 'saved source rule appears in Recipe steps');
      await page.click(removeStep);
      const afterRemoval = await page.until(async () => {
        const draft = await readDraft();
        const source = draft.workspace.documents[0].importedSource;
        return source.ruleAdditions.length === 1 && !source.ruleAdditions.some(rule => rule.stepId === removedRule.stepId) &&
          !source.steps.some(step => step.id === removedRule.stepId) ? { draft, source } : false;
      }, 'removed rule is deleted from the saved recipe plan');
      assert.equal(afterRemoval.source.ruleAdditions[0].stepId, secondRules[0].stepId);
      assert.equal(afterRemoval.source.nextRuleIndex, 2);

      await page.reload();
      await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=traversal-recipe-steps]"))'), 'Recipe steps restored after reload');
      assert.equal(await page.evaluate('Boolean(document.querySelector(' + JSON.stringify(recipeStep) + '))'), false);
      const removalReloaded = await readDraft();
      assert.deepEqual(removalReloaded.workspace.documents[0].importedSource.ruleAdditions.map(rule => rule.stepId), [secondRules[0].stepId]);
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  });
});
