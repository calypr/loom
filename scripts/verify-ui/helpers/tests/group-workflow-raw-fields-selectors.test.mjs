import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { click } from '../cda-playwright.mjs';
import { nestedRepeatedRawFieldsSummarySelector } from '../../workflows/verify-cda-nested-repeated-browser.mjs';
import { groupAddFieldsRawFieldsSummarySelector } from '../../workflows/verify-cda-group-add-fields-browser.mjs';
import { groupRelatedSummaryRawFieldsSummarySelector } from '../../workflows/verify-cda-group-related-summary-browser.mjs';
import { groupRelatedValuesRawFieldsSummarySelector } from '../../workflows/verify-cda-group-related-values-browser.mjs';
import { groupOneConflictRawFieldsSummarySelector } from '../../workflows/verify-cda-group-one-conflict-browser.mjs';

const workflowSelectors = [
  ['nested repeated', nestedRepeatedRawFieldsSummarySelector],
  ['group add fields', groupAddFieldsRawFieldsSummarySelector],
  ['group related summary', groupRelatedSummaryRawFieldsSummarySelector],
  ['group related values', groupRelatedValuesRawFieldsSummarySelector],
  ['group ONE conflict', groupOneConflictRawFieldsSummarySelector],
];

test('grouped CDA workflows open the outer raw FHIR disclosure with nested row details', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const rowDetails = Array.from({ length: 332 }, (_, index) =>
      `<article><details><summary>Inspect meaning, evidence, and construction choices ${index}</summary></details></article>`).join('');
    await page.setContent(`<section aria-label="Add columns editor">
      <details data-testid="feature-catalog-raw-fields">
        <summary>Raw FHIR fields (advanced)</summary>
        <section>${rowDetails}<input type="checkbox" aria-label="Select Patient.id"></section>
      </details>
    </section>`);

    const disclosure = page.getByTestId('feature-catalog-raw-fields');
    const patientID = page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    assert.equal(await disclosure.locator('summary').count(), 333,
      'the descendant selector sees the outer summary and 332 nested row summaries');
    assert.equal(await page.locator('[data-testid="feature-catalog-raw-fields"] > summary').count(), 1);
    assert.equal(await patientID.isVisible(), false);

    const actionContext = {
      action: async (_label, locator, perform) => {
        assert.equal(await locator.count(), 1, 'workflow actions must have one target');
        await perform();
      },
    };
    for (const [workflow, selector] of workflowSelectors) {
      const summary = page.locator(selector);
      assert.equal(await summary.count(), 1, `${workflow} must target one outer disclosure summary`);
      assert.equal(await summary.evaluate(element => element.parentElement?.dataset.testid === 'feature-catalog-raw-fields'), true);

      await click(page, selector, {}, 5000, actionContext);
      assert.equal(await disclosure.getAttribute('open'), '', `${workflow} must open the outer disclosure`);
      assert.equal(await patientID.isVisible(), true);

      await click(page, selector, {}, 5000, actionContext);
      assert.equal(await disclosure.getAttribute('open'), null, `${workflow} must close the outer disclosure`);
      assert.equal(await patientID.isVisible(), false);
    }
  } finally {
    await browser.close();
  }
});
