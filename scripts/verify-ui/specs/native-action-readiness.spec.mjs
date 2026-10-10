import { expect, test } from '@playwright/test';
import { click as cdaClickFromSource } from '../helpers/cda-playwright.mjs';
import { pathToFileURL } from 'node:url';
import { performAction, prepareNativeAction, requireUnique } from '../helpers/playwright-actions.mjs';


const cdaClick = process.env.CDA_PLAYWRIGHT_MODULE
  ? (await import(pathToFileURL(process.env.CDA_PLAYWRIGHT_MODULE).href)).click
  : cdaClickFromSource;

test('shared action readiness uses Playwright auto-waiting for count and editability', async ({ page }) => {
  await page.setContent(`
    <button>Duplicate</button><button>Duplicate</button>
    <button disabled>Disabled</button>
    <div style="position:relative"><button>Intercepted</button><span style="position:absolute;inset:0;z-index:2"></span></div>
    <input aria-label="Read only" readonly value="unchanged">
    <button onclick="document.querySelector('output').textContent='Saved'">Save</button>
    <output>Waiting</output>`);

  const duplicates = page.getByRole('button', { name: 'Duplicate' });
  await expect(requireUnique(duplicates, 'Duplicate', { timeout: 250 })).rejects.toThrow(/expected exactly one control/i);
  await expect(prepareNativeAction(page.getByRole('button', { name: 'Disabled' }), 'Disabled', { timeout: 250 }))
    .rejects.toThrow();
  await expect(prepareNativeAction(page.getByRole('button', { name: 'Intercepted' }), 'Intercepted', { timeout: 250 }))
    .rejects.toThrow();

  const readOnly = page.getByRole('textbox', { name: 'Read only' });
  await expect(prepareNativeAction(readOnly, 'Read only', { timeout: 250, editable: true }))
    .rejects.toThrow(/control must be editable/i);
  await expect(readOnly).toHaveValue('unchanged');

  await page.setContent('<select aria-label="Transient" disabled><option value="ready">Ready</option></select>');
  const transient = page.getByRole('combobox', { name: 'Transient' });
  await transient.evaluate(select => window.setTimeout(() => { select.disabled = false; }, 150));
  await prepareNativeAction(transient, 'Transient select', { timeout: 5_000 });
  await transient.selectOption('ready');
  await expect(transient).toHaveValue('ready');

  await page.setContent('<button onclick="document.querySelector(\'output\').textContent=\'Saved\'">Save</button><output>Waiting</output>');
  const elapsedMs = await performAction(undefined, 'Save', page.getByRole('button', { name: 'Save' }), target => target.click());
  await expect(page.getByText('Saved')).toBeVisible();
  expect(elapsedMs).toBeGreaterThanOrEqual(0);
});

test('CDA button locator clicks a uniquely selected no-identity tab with decorative icon text', async ({ page }) => {
  await page.setContent(`<button data-testid="table-tab" aria-pressed="false" onclick="this.setAttribute('aria-pressed', 'true')"><span aria-hidden="true">▤</span>Direct source field rows</button>`);
  const actionContext = {
    async action(_label, locator, perform) {
      await expect(locator).toHaveCount(1);
      await perform(locator);
    },
  };

  await cdaClick(page, '[data-testid="table-tab"]', {}, 5000, actionContext);

  const tab = page.getByRole('button', { name: 'Direct source field rows', exact: true });
  await expect(tab).toHaveAttribute('aria-pressed', 'true');
});

test('CDA button locator follows its accessible name after a DOM reorder', async ({ page }) => {
  await page.setContent(`<section aria-label="Add columns editor">
    <script>window.clicked = [];</script>
    <button data-testid="load-choices" onclick="window.clicked.push('load-choices')">Load choices for these table rows</button>
    <button data-testid="add-selected" onclick="window.clicked.push('add-selected')">Add 2 selected features</button>
  </section>`);

  const actionContext = {
    async action(_label, locator, perform, { timeout = 5000 } = {}) {
      await expect(locator).toHaveCount(1, { timeout });
      await page.evaluate(() => {
        const editor = document.querySelector('[aria-label="Add columns editor"]');
        const inserted = document.createElement('button');
        inserted.textContent = 'Async inserted option';
        editor.insertBefore(inserted, editor.firstElementChild);
      });
      await locator.click({ trial: true, timeout });
      await perform(locator);
    },
  };

  await cdaClick(page, '[aria-label="Add columns editor"] button', {
    includes: 'Add 2 selected features',
  }, 5000, actionContext);

  expect(await page.evaluate(() => window.clicked)).toEqual(['add-selected']);
  await expect(page.getByTestId('load-choices')).toBeVisible();
});

test('CDA button locator rejects duplicate accessible names before action dispatch', async ({ page }) => {
  await page.setContent(`<section aria-label="Add columns editor">
    <script>window.clicked = [];</script>
    <button data-testid="add-first" onclick="window.clicked.push('first')">Add 2 selected features</button>
    <button data-testid="add-second" onclick="window.clicked.push('second')">Add 2 selected features</button>
  </section>`);
  let actionCalls = 0;
  const actionContext = { async action() { actionCalls += 1; } };

  await expect(cdaClick(page, '[aria-label="Add columns editor"] button', {
    includes: 'Add 2 selected features',
  }, 5000, actionContext)).rejects.toThrow(/Expected exactly one target/);
  expect(actionCalls).toBe(0);
  expect(await page.evaluate(() => window.clicked)).toEqual([]);
});
