import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import { click } from '../cda-playwright.mjs';
import {
  groupEditRawFieldsDisclosureSelector,
  groupEditRawFieldsSummarySelector,
} from '../../workflows/verify-cda-group-edit-before-related-column-browser.mjs';

test('Group edit opens only the raw-fields summary among nested field summaries', async (t) => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();
  const nestedSummaries = Array.from({ length: 332 }, (_, index) =>
    `<details><summary>Field choices ${index}</summary></details>`).join('');
  await page.setContent(`
    <details data-testid="feature-catalog-raw-fields">
      <summary>Raw FHIR fields (advanced)</summary>
      <section aria-labelledby="feature-catalog-fields-title">
        <div>${nestedSummaries}</div>
      </section>
    </details>
  `);

  assert.ok(await page.locator(`${groupEditRawFieldsDisclosureSelector} summary`).count() > 1,
    'the catalog contains nested field summaries below the raw-fields disclosure');
  assert.equal(await page.locator(groupEditRawFieldsSummarySelector).count(), 1,
    'the Group-edit locator must select only the disclosure summary');
  await click(page, groupEditRawFieldsSummarySelector, {}, 5000, {
    action: async (_label, _locator, perform) => perform(),
  });
  assert.equal(await page.locator(groupEditRawFieldsDisclosureSelector).evaluate((section) => section.open), true);
});
