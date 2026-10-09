import { expect, test } from '@playwright/test';
import { click } from '../helpers/playwright-authoring-page.mjs';
import {
  namedCohortAddColumnsEditorSelector,
  namedCohortAddColumnsRawFieldsSummarySelector,
} from '../workflows/verify-cda-named-cohort-related-count-browser.mjs';

test('named-cohort selector targets the Add columns raw-fields disclosure among nested and unrelated summaries', async ({ page }) => {
  const nested = Array.from({ length: 306 }, (_, index) => `<details><summary>Resource ${index}</summary></details>`).join('');
  await page.setContent(`<section data-testid="construction-operation-editor" data-operation-family="COMBINE">
    <details data-testid="feature-catalog-raw-fields">
      <summary>Unrelated editor raw fields</summary>
    </details>
  </section>
  <section data-testid="construction-operation-editor" data-operation-family="ADD_COLUMNS" aria-label="Add columns editor">
    <details data-testid="feature-catalog-raw-fields">
      <summary>Raw FHIR fields (advanced)</summary>${nested}
      <details><summary>Raw FHIR fields (advanced)</summary></details>
      <input type="checkbox" aria-label="Select Patient.id">
    </details>
  </section>`);

  const editor = page.locator(namedCohortAddColumnsEditorSelector);
  await expect(editor).toHaveCount(1);
  await expect(page.getByRole('region', { name: 'Add columns editor', exact: true })).toHaveCount(1);
  const unrelatedDisclosure = page.locator('[data-operation-family="COMBINE"] [data-testid="feature-catalog-raw-fields"]');
  await expect(unrelatedDisclosure).toHaveJSProperty('open', false);
  const disclosure = editor.getByTestId('feature-catalog-raw-fields');
  await expect(disclosure).toHaveCount(1);
  await expect(disclosure.locator('summary')).toHaveCount(308);

  const exactLabel = /^\s*Raw FHIR fields \(advanced\)\s*$/;
  const broadExactLabel = disclosure.locator('summary').filter({ hasText: exactLabel });
  await expect(broadExactLabel).toHaveCount(2);
  const namedCohortSummary = page.locator(namedCohortAddColumnsRawFieldsSummarySelector).filter({ hasText: exactLabel });
  await expect(namedCohortSummary).toHaveCount(1);

  const patientID = editor.locator('input[type="checkbox"][aria-label="Select Patient.id"]');
  await expect(patientID).toHaveCount(1);
  await expect(patientID).toBeHidden();
  const workflow = { page, action: async (_label, locator, action) => {
    await expect(locator).toHaveCount(1);
    await action();
  } };
  await click(workflow, namedCohortAddColumnsRawFieldsSummarySelector, {
    name: 'Raw FHIR fields (advanced)',
  });
  await expect(disclosure).toHaveAttribute('open', '');
  await expect(unrelatedDisclosure).toHaveJSProperty('open', false);
  await expect(patientID).toBeVisible();
});
