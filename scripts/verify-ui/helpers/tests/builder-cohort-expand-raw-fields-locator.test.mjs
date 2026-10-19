import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { click, recordPlaywrightTiming } from '../playwright-authoring-page.mjs';
import { createReport } from '../report.mjs';
import {
  addColumnsRawFieldsDisclosureSelector,
  addColumnsRawFieldsSummarySelector,
} from '../add-columns-raw-fields-selectors.mjs';

test('cohort expansion opens only the Add columns outer Raw FHIR disclosure among 561 nested summaries', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const nestedSummaries = Array.from({ length: 560 }, (_, index) =>
      `<details><summary>Inspect row feature ${index}</summary></details>`).join('');
    await page.setContent(`<section aria-label="Change editor">
      <section aria-label="Add columns editor">
        <details data-testid="feature-catalog-raw-fields">
          <summary>Raw FHIR fields (advanced)</summary>
          ${nestedSummaries}
          <input type="checkbox" aria-label="Select Patient.id">
        </details>
      </section>
      <section aria-label="Other editor">
        <details data-testid="feature-catalog-raw-fields"><summary>Other raw fields</summary></details>
      </section>
    </section>`);

    const oldDescendantSelector = '[aria-label="Add columns editor"] [data-testid="feature-catalog-raw-fields"] summary';
    assert.equal(await page.locator(oldDescendantSelector).count(), 561,
      'the old scoped descendant selector includes the outer summary and all 560 nested summaries');
    assert.equal(await page.locator('[data-testid="feature-catalog-raw-fields"] > summary').count(), 2,
      'the direct-child selector still needs the Add columns editor scope');

    const summary = page.locator(addColumnsRawFieldsSummarySelector);
    const disclosure = page.locator(addColumnsRawFieldsDisclosureSelector);
    assert.equal(await summary.count(), 1);
    assert.equal(await summary.isVisible(), true);
    assert.equal(await summary.innerText(), 'Raw FHIR fields (advanced)');
    assert.equal(await disclosure.evaluate(element => element.open), false);

    const workflow = {
      page,
      action: async (_label, locator, perform) => {
        assert.equal(await locator.count(), 1, 'the production click driver requires one target');
        await locator.click({ trial: true });
        await perform();
      },
    };
    const report = createReport({
      scenario: 'builder-authoring',
      caseName: 'cohort-expand',
      target: {},
      evidenceDirectory: '/tmp/case009-selector-test',
    });
    await recordPlaywrightTiming(report, page, workflow, {
      name: 'open scoped cohort Raw FHIR disclosure',
      action: () => click(workflow, addColumnsRawFieldsSummarySelector),
      after: `document.querySelector(${JSON.stringify(addColumnsRawFieldsDisclosureSelector)})?.open === true`,
      timeout: 5000,
      budget: 5000,
    });

    assert.equal(await disclosure.evaluate(element => element.open), true);
    assert.equal(await page.getByRole('checkbox', { name: 'Select Patient.id', exact: true }).isVisible(), true);
    assert.equal(report.actions[0].status, 'passed');
    assert.ok(report.actions[0].elapsedMs <= 5000);
  } finally {
    await browser.close();
  }
});
