import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { launchBrowser } from './lib/playwright-browser.mjs';

const evidence = resolve(process.argv[2] ?? '/tmp/loom-browser-disclosure');
const browser = await launchBrowser({ evidence });
try {
  const { page } = browser;
  await page.setContent('<!doctype html><details id="advanced" open><summary>Advanced</summary><fieldset><input aria-label="Column name" value="Original"></fieldset></details>');
  const input = page.locator('input[aria-label="Column name"]');
  const summary = page.getByText('Advanced', { exact: true });

  await page.locator('#advanced').evaluate(details => { details.open = false; });
  assert.equal(await input.count(), 1, 'The collapsed control target must be unique');
  assert.equal(await input.isVisible(), false, 'A control inside collapsed details must be hidden');
  await assert.rejects(input.fill('Ignored', { timeout: 1000 }), /not visible|Timeout/i,
    'Playwright must refuse to type into the collapsed control');

  await summary.click();
  await assert.doesNotReject(input.waitFor({ state: 'visible', timeout: 1000 }));
  await input.fill('Renamed');
  assert.equal(await input.inputValue(), 'Renamed', 'Native typing must update the open control');
  process.stdout.write(`${JSON.stringify({ status: 'passed', evidence, checks: ['collapsed control hidden', 'hidden fill rejected', 'summary actionable', 'opened control native typing'] })}\n`);
} catch (error) {
  await browser.captureFailure(error, {
    phase: 'disclosure-actionability',
    action: { label: 'verify disclosure control actionability', locator: 'input[aria-label="Column name"]', targetLocator: browser.page.locator('input[aria-label="Column name"]') },
  });
  throw error;
} finally {
  await browser.close();
}
