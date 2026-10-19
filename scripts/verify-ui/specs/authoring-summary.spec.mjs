import { expect, test } from '@playwright/test';
import { click } from '../helpers/playwright-authoring-page.mjs';

const workflowFor = page => ({
  page,
  action: async (_label, _locator, perform) => perform(),
});

test('opens an exact-text native summary when Chromium exposes no button role', async ({ page }) => {
  await page.setContent(`
    <details id="new-explorer">
      <summary>New explorer</summary>
      <label for="new-explorer-name">Explorer name</label>
      <input id="new-explorer-name">
    </details>
    <details id="prefixed">
      <summary>New explorer options</summary>
    </details>
  `);

  await expect(page.getByRole('button', { name: 'New explorer', exact: true })).toHaveCount(0);
  await click(workflowFor(page), 'summary', { name: 'New explorer' });

  await expect(page.locator('#new-explorer')).toHaveJSProperty('open', true);
  await expect(page.locator('#new-explorer-name')).toBeVisible();
  await expect(page.locator('#prefixed')).toHaveJSProperty('open', false);
});

test('duplicate exact native summaries retain Playwright strictness', async ({ page }) => {
  await page.setContent(`
    <details id="first"><summary>New explorer</summary></details>
    <details id="second"><summary>New explorer</summary></details>
  `);

  await expect(click(workflowFor(page), 'summary', { name: 'New explorer' })).rejects.toThrow(/strict mode violation/);
  await expect(page.locator('details[open]')).toHaveCount(0);
});
