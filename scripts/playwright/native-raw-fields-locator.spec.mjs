import { expect, test } from '@playwright/test';
import { click } from '../lib/playwright-authoring-page.mjs';

test('raw FHIR locator targets the exact direct disclosure summary in the Add columns editor', async ({ page }) => {
  const nested = Array.from({ length: 306 }, (_, index) => `<details><summary>Resource ${index}</summary></details>`).join('');
  await page.setContent(`<section aria-label="Add columns editor">
    <details data-testid="feature-catalog-raw-fields">
      <summary>Raw FHIR fields (advanced)</summary>${nested}
      <details><summary>Raw FHIR fields (advanced)</summary></details>
      <input type="checkbox" aria-label="Select Patient.id">
    </details>
  </section>`);

  const editor = page.getByRole('region', { name: 'Add columns editor', exact: true });
  await expect(editor).toHaveCount(1);
  const disclosure = editor.getByTestId('feature-catalog-raw-fields');
  await expect(disclosure).toHaveCount(1);
  await expect(disclosure.locator('summary')).toHaveCount(308);

  const exactLabel = /^\s*Raw FHIR fields \(advanced\)\s*$/;
  const broadExactLabel = disclosure.locator('summary').filter({ hasText: exactLabel });
  await expect(broadExactLabel).toHaveCount(2);
  const directExactLabel = editor.locator('[data-testid="feature-catalog-raw-fields"] > summary').filter({ hasText: exactLabel });
  await expect(directExactLabel).toHaveCount(1);

  const patientID = editor.locator('input[type="checkbox"][aria-label="Select Patient.id"]');
  await expect(patientID).toHaveCount(1);
  await expect(patientID).toBeHidden();
  const workflow = { page, action: async (_label, locator, action) => {
    await expect(locator).toHaveCount(1);
    await action();
  } };
  await click(workflow, '[aria-label="Add columns editor"] [data-testid="feature-catalog-raw-fields"] > summary', {
    name: 'Raw FHIR fields (advanced)',
  });
  await expect(disclosure).toHaveAttribute('open', '');
  await expect(patientID).toBeVisible();
});
