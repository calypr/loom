import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';

const evidence = resolve(process.argv[2] ?? '/tmp/loom-browser-disclosure');
const browser = await launchBrowser({ evidence, appOrigins: ['http://127.0.0.1'], noAuth: true });
const report = { status: 'running', evidence, checks: [], actions: [] };
try {
  const { page } = browser;
  const actionTracker = {};
  await page.setContent('<!doctype html><details id="advanced" open><summary>Advanced</summary><fieldset><input aria-label="Column name" value="Original"></fieldset></details>');
  const input = page.locator('input[aria-label="Column name"]');
  const summary = page.getByText('Advanced', { exact: true });

  await performAction(actionTracker, 'collapse Advanced disclosure', summary, target => target.click());
  assert.equal(await input.count(), 1, 'The collapsed control target must be unique');
  assert.equal(await input.isVisible(), false, 'A control inside collapsed details must be hidden');
  await assert.rejects(input.fill('Ignored', { timeout: 1000 }), /not visible|Timeout/i,
    'Playwright must refuse to type into the collapsed control');
  report.checks.push('collapsed control hidden', 'hidden fill rejected');

  await performAction(actionTracker, 'open Advanced disclosure', summary, target => target.click());
  await assert.doesNotReject(input.waitFor({ state: 'visible', timeout: 1000 }));
  await performAction(actionTracker, 'rename column', input, target => target.fill('Renamed'), { editable: true });
  assert.equal(await input.inputValue(), 'Renamed', 'Native typing must update the open control');
  report.status = 'passed';
  report.checks.push('summary actionable', 'opened control native typing');
  report.actions = actionTracker.actions;
} catch (error) {
  await browser.captureFailure(error, {
    phase: 'disclosure-actionability',
    action: { label: 'verify disclosure control actionability', locator: 'input[aria-label="Column name"]', targetLocator: browser.page.locator('input[aria-label="Column name"]') },
  });
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  process.exitCode = 1;
  throw error;
} finally {
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser.close();
}
process.stdout.write(`${JSON.stringify(report)}\n`);
