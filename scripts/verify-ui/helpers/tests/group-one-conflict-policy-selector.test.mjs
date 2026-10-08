import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { browserEval, selectOption } from '../cda-playwright.mjs';
import {
  groupOneConflictChoicePolicySelector,
  groupOneConflictChoiceDialogSelector,
  groupOneConflictOperationPolicySelector,
  groupOneConflictSelectedFieldSelector,
  inspectGroupOneConflictChooserState,
} from '../../workflows/verify-cda-group-one-conflict-browser.mjs';

test('Group ONE retry selects ALL in the chooser when both policy controls are mounted', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<section aria-label="Add columns editor">
      <input type="checkbox" aria-label="Select Specimen.id" checked>
      <select aria-label="Values per grouped row"><option value="ONE" selected>Require one distinct value</option><option value="ALL">Keep all distinct values</option></select>
    </section>
    <section role="dialog" aria-labelledby="catalog-selection-dialog-title">
      <h2 id="catalog-selection-dialog-title">Choose how to add these fields</h2>
      <select aria-label="Values per grouped row"><option value="ONE" selected>Require one distinct value</option><option value="ALL">Keep all distinct values</option></select>
    </section>`);

    const broadSelector = 'select[aria-label="Values per grouped row"]';
    assert.equal(await page.locator(broadSelector).count(), 2,
      'the operation editor and failed-choice dialog both expose this label');
    await assert.rejects(
      selectOption(page, broadSelector, 'ALL', {}, { action: async (_label, _locator, perform) => perform() }),
      /Expected exactly one target/,
    );
    assert.equal(await page.locator(groupOneConflictOperationPolicySelector).count(), 1);
    assert.equal(await page.locator(groupOneConflictChoicePolicySelector).count(), 1);
    assert.equal(await page.locator(`${groupOneConflictChoiceDialogSelector} input[aria-label="Select Specimen.id"]`).count(), 0,
      'the field checkbox remains in the operation editor outside the chooser portal');
    assert.equal(await page.locator(groupOneConflictSelectedFieldSelector).isChecked(), true);

    const stateArgs = {
      dialogSelector: groupOneConflictChoiceDialogSelector,
      selectedFieldSelector: groupOneConflictSelectedFieldSelector,
    };
    assert.deepEqual(await browserEval(page, inspectGroupOneConflictChooserState, stateArgs), {
      dialogOpen: true,
      fieldSelected: true,
      policy: 'ONE',
      allAvailable: true,
    }, 'ONE failure state must read the exact selected field from the operation editor');

    await selectOption(page, groupOneConflictChoicePolicySelector, 'ALL', {}, {
      action: async (_label, _locator, perform) => perform(),
    });
    const values = await page.locator(broadSelector).evaluateAll(selects => selects.map(select => select.value));
    assert.deepEqual(values, ['ONE', 'ALL'], 'the scoped native action changes only the chooser policy');
    assert.deepEqual(await browserEval(page, inspectGroupOneConflictChooserState, stateArgs), {
      dialogOpen: true,
      fieldSelected: true,
      policy: 'ALL',
      allAvailable: true,
    }, 'ALL recovery keeps the exact Specimen.id field selected');
  } finally {
    await browser.close();
  }
});
