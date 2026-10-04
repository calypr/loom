import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';

const evidence = resolve(process.argv[2] ?? '.audit/ml-plan-detail-20260918');
const prototype = resolve('docs/product/history/20260919-superseded/ml-dataframer/catalog-prototype.html');
await mkdir(evidence, { recursive: true });
const report = { status: 'running', scope: 'isolated synthetic interaction prototype; no backend verification', prototype, evidence, assertions: [], screenshots: [] };
let browser;
const action = (name, locator, run) => performAction(report, name, locator, run);
const countColumns = () => browser.page.locator('[data-column]').count();
const clickId = (id, label = id) => action(label, browser.page.locator(`#${id}`), target => target.click());
const search = value => action(`search ${value || 'all concepts'}`, browser.page.locator('#search'), target => target.fill(value));
const screenshot = async name => {
  await browser.page.screenshot({ path: resolve(evidence, name), fullPage: true });
  report.screenshots.push(name);
};

try {
  browser = await launchBrowser({ evidence, appOrigins: [], noAuth: false });
  const { page } = browser;
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto(pathToFileURL(prototype).href, { waitUntil: 'load' });
  assert.equal(await countColumns(), 0);
  assert.equal(await page.locator('#catalog .concept').count(), 50);
  await clickId('concept-1');
  await clickId('next');
  await clickId('concept-51');
  await search('SYN-0900');
  await page.locator('#concept-900').waitFor({ state: 'visible' });
  await clickId('concept-900');
  assert.equal(await countColumns(), 0);
  await clickId('add');
  const decision = page.locator('#decision');
  await page.waitForFunction(() => document.querySelector('#decision')?.open === true);
  await screenshot('basket-decision.png');
  await clickId('cancel');
  await page.waitForFunction(() => document.querySelector('#decision')?.open === false);
  assert.equal(await countColumns(), 0);
  assert.match(await page.locator('#basket').innerText(), /3 selected/);
  report.assertions.push('50 visible of 1000 concepts', 'cross-page and filtered selection survives', 'basket cancel leaves zero columns and three selections');

  await clickId('add');
  await page.locator('#policy').selectOption('list');
  await clickId('confirm');
  assert.equal(await countColumns(), 3);
  await search('');
  await screenshot('basket-selected.png');
  await action('inspect source path for selected column', page.locator('[data-column="900"] button'), target => target.click());
  assert.match(await page.locator('#inspect-body').innerText(), /Observation\.specimen/);
  await clickId('close-inspector');
  await page.setViewportSize({ width: 390, height: 844 });
  await screenshot('basket-mobile.png');
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, '390px layout must not overflow horizontally');
  report.assertions.push('explicit choice adds exactly three', 'inspector exposes source path', '390px view has no horizontal overflow');

  await page.locator('#mode').selectOption('immediate');
  await clickId('concept-1');
  await clickId('next');
  await clickId('concept-51');
  await search('SYN-0900');
  await page.locator('#concept-900').waitFor({ state: 'visible' });
  await clickId('concept-900');
  await clickId('cancel');
  assert.equal(await countColumns(), 2);
  assert.deepEqual(browser.diagnostics.pageErrors, []);
  assert.deepEqual(browser.diagnostics.console, []);
  report.assertions.push('immediate-add cancel leaves the two prior columns', 'no browser exceptions');
  report.status = 'passed';
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = { label: report.activeAction.label, locator: report.activeAction.locator, elapsedMs: Date.now() - report.activeAction.startedAt };
  await browser?.captureFailure(error, { phase: 'synthetic-ml-dataframer-prototype', action: report.activeAction });
  process.exitCode = 1;
} finally {
  report.diagnostics = browser?.diagnostics;
  report.finishedAt = new Date().toISOString();
  await writeFile(resolve(evidence, 'prototype-evidence.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
process.stdout.write(`${JSON.stringify({ status: report.status, evidence, assertions: report.assertions.length, error: report.error })}\n`);
