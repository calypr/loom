import { expect, test } from '@playwright/test';
import { performAction, prepareNativeAction, requireUnique } from '../lib/playwright-actions.mjs';

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
