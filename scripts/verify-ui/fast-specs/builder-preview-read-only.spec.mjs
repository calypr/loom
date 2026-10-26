import { expect, test } from '../helpers/fast-fixtures.mjs';

test('loads the seeded dataframe and previews it', async ({ fastPage, fastUrl }) => {
  const preview = fastPage.waitForResponse((response) =>
    response.request().method() === 'POST' &&
    /\/authoring\/v2\/preview(?:\?|$)/.test(response.url()),
    { timeout: 10_000 },
  );
  await fastPage.goto(fastUrl);
  const response = await preview;
  expect(response.ok()).toBe(true);
  const payload = await response.json();
  expect(Array.isArray(payload.rows) ? payload.rows.length : 0).toBeGreaterThan(0);
  await expect(fastPage.getByTestId('preview-table-content')).toBeVisible({ timeout: 10_000 });
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row').first()).toBeVisible();
});
