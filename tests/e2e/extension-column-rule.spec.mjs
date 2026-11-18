import assert from 'node:assert/strict';
import test from 'node:test';
import { api, readFixture, withFreshUIExplorer } from './app.mjs';

const fixtureDirectory = new URL('../fixtures/extension-column-rule/', import.meta.url).pathname;

test('extension rules preserve nested URL ancestry through preview and reload', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient', fixtureDirectory);
  const [leftURL, rightURL] = ['left', 'right'].map(name => `https://example.test/parent/${name}`);
  const leafURL = 'https://example.test/child/leaf';
  const expected = patients.map(patient => ({
    id: patient.id,
    top: patient.extension?.find(extension => extension.url === 'https://example.test/flagged')?.valueBoolean,
    left: patient.extension?.find(extension => extension.url === leftURL)
      ?.extension?.find(extension => extension.url === leafURL)?.valueString,
    right: patient.extension?.find(extension => extension.url === rightURL)
      ?.extension?.find(extension => extension.url === leafURL)?.valueString,
  }));
  assert.deepEqual(expected.map(row => row.left), ['left-only-first', 'left-only-second']);
  assert.deepEqual(expected.map(row => row.right), ['right-only-first', undefined]);

  await withFreshUIExplorer('extension rule', async (scenario, page) => {
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
      assert.equal(before.workspace.documents[0].rootResourceType, 'Patient');
      assert(before.workspace.documents[0].importedRecipe);

      const addColumns = '[data-testid="construction-action-add-columns"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector('${addColumns}:not(:disabled)'))`), 'Add columns is ready');
      await page.click(addColumns);
      await page.click('[role="tab"][data-column-tab="advanced"]');
      await page.clickText('Extensions');

      const extensionSource = 'fieldset[aria-label="Source step parameters"] select[aria-label="Extension source path"]';
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(extensionSource)}))`), 'Extension path choices');
      const choices = await page.evaluate(`Array.from(document.querySelector(${JSON.stringify(extensionSource)}).options, option => option.value)`);
      assert(choices.includes('extension[]'));
      assert(choices.includes('extension[].extension[]'));
      await page.selectOptionText(extensionSource, 'extension[]');
      await page.clickText('Review column rule');
      await page.clickText('Preview column rule');
      await page.until(() => page.evaluate(`[...document.querySelectorAll('button')].some(button => button.innerText === 'Apply column rule' && !button.disabled)`), 'extension rule preview is ready');

      const readRowsWithHeaders = async () => ({
        headers: await page.evaluate(`[...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="columnheader"], thead th')].map(header => header.innerText.trim().toLowerCase().replace(/[^a-z0-9]+/g, '_'))`),
        rows: await page.tableRows(),
      });
      const preview = await page.until(async () => {
        const table = await readRowsWithHeaders();
        const leftIndex = table.headers.findIndex(header => header.includes('left_leaf'));
        const rightIndex = table.headers.findIndex(header => header.includes('right_leaf'));
        const topIndex = table.headers.findIndex(header => header.includes('flagged'));
        return leftIndex >= 0 && rightIndex >= 0 && topIndex >= 0 && table.rows.length >= expected.length ? table : false;
      }, 'one column per observed extension URL is visible').catch(async error => {
        console.error('Extension preview table', await readRowsWithHeaders());
        throw error;
      });
      for (const patient of expected) {
        const row = preview.rows.find(cells => cells.includes(patient.id));
        assert(row, `Preview contains ${patient.id}`);
        const leftIndex = preview.headers.findIndex(header => header.includes('left_leaf'));
        const rightIndex = preview.headers.findIndex(header => header.includes('right_leaf'));
        const topIndex = preview.headers.findIndex(header => header.includes('flagged'));
        assert.equal(row[leftIndex], patient.left, `${patient.id} keeps the left parent URL paired with its leaf value`);
        if (patient.right) assert.equal(row[rightIndex], patient.right, `${patient.id} keeps the right parent URL paired with its leaf value`);
        else assert(!row[rightIndex].includes('left-only'), `${patient.id} does not copy the left leaf value into the right column`);
        assert.equal(row[topIndex].toLowerCase(), String(patient.top), `${patient.id} retains the top-level extension value`);
      }
      assert.equal((await readDraft()).draftDigest, before.draftDigest, 'Preview leaves the saved draft unchanged');
      await page.clickText('Apply column rule');

      const saved = await page.until(async () => {
        const draft = await readDraft();
        return draft.draftDigest !== before.draftDigest ? draft : false;
      }, 'extension rule is saved');
      const document = saved.workspace.documents[0];
      assert.deepEqual(document.importedRecipe, before.workspace.documents[0].importedRecipe);
      const [addition] = document.importedSource.ruleAdditions;
      assert.equal(addition.kind, 'EXTENSION_COLUMN');
      assert.equal(addition.parameters.extension.source.select, 'extension[]');
      assert.equal(document.columns.find(column => column.column.includes('flagged'))?.logicalType, 'boolean');

      await page.reload();
      const reloadedRows = await page.until(async () => {
        const table = await readRowsWithHeaders();
        return table.rows.length >= expected.length && table.headers.some(header => header.includes('right_leaf')) ? table : false;
      }, 'reloaded table contains extension values');
      for (const patient of expected) {
        const row = reloadedRows.rows.find(cells => cells.includes(patient.id));
        assert(row, `Reloaded table contains ${patient.id}`);
        const leftIndex = reloadedRows.headers.findIndex(header => header.includes('left_leaf'));
        const rightIndex = reloadedRows.headers.findIndex(header => header.includes('right_leaf'));
        assert.equal(row[leftIndex], patient.left);
        if (patient.right) assert.equal(row[rightIndex], patient.right);
        else assert(!row[rightIndex].includes('left-only'));
      }
    } finally {
      const capability = await api(`${fixture.base}/capability`);
      if (capability.features?.deleteExplorer) {
        await api(`${scenario.root}/${encodeURIComponent(fixture.clone.explorerId)}`, { method: 'DELETE' });
      }
    }
  }, { fixtureDirectory });
});
