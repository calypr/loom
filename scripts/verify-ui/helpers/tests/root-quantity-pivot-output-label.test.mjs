import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import { pivotOutputLabelLocator } from '../../workflows/root-quantity-pivot-workflow.mjs';

test('Pivot output label locator targets only the exact category among Missing and d', async t => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  t.after(() => browser.close());
  const page = await browser.newPage();

  await page.setContent('<label>New column label <input aria-label="Pivot output label Missing" value="Missing"></label><label>New column label <input aria-label="Pivot output label d" value="d"></label>');

  const target = pivotOutputLabelLocator(page, 'd');
  assert.equal(await target.count(), 1, 'the exact d label must identify one input');
  await target.fill('d maximum');

  assert.deepEqual(
    await page.locator('input[aria-label^="Pivot output label "]').evaluateAll(inputs => inputs.map(input => input.value)),
    ['Missing', 'd maximum'],
    'filling d must leave the distinct Missing category unchanged',
  );
});
