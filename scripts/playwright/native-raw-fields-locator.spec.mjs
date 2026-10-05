import { expect, test } from '@playwright/test';

test('raw FHIR locator selects the direct summary among nested resource summaries', async ({ page }) => {
  const nested = Array.from({ length: 9 }, (_, index) => `<details><summary>Resource ${index}</summary></details>`).join('');
  await page.setContent(`<details data-testid="feature-catalog-raw-fields">
    <summary>Raw FHIR fields (advanced)</summary>${nested}
    <input type="checkbox" aria-label="Select Patient.id">
  </details>`);

  const disclosure = page.getByTestId('feature-catalog-raw-fields');
  await expect(disclosure).toHaveCount(1);
  await expect(disclosure.locator('summary')).toHaveCount(10);
  const patientID = page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
  const patientDisclosure = page.locator(
    '[data-testid="feature-catalog-raw-fields"]:has(input[type="checkbox"][aria-label="Select Patient.id"])');
  await expect(patientDisclosure).toHaveCount(1);
  await expect(patientDisclosure.locator(':scope > summary')).toHaveCount(1);
  await patientDisclosure.locator(':scope > summary').click();
  await expect(await patientDisclosure.evaluate(element => element.open)).toBe(true);
  await expect(patientID).toHaveCount(1);
  await expect(patientDisclosure.getByRole('checkbox', { name: 'Select Patient.id', exact: true })).toHaveCount(1);
});
