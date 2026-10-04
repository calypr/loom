import assert from 'node:assert/strict';
import { launchBrowser } from './playwright-browser.mjs';
import { performAction, requireUnique } from './playwright-actions.mjs';

const browserForPage = new WeakMap();
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();

export async function launchCdaBrowser(evidence, apiOrigin, uiOrigin) {
  for (const [name, value] of [['API', apiOrigin], ['UI', uiOrigin]]) {
    const origin = new URL(value);
    assert(['http:', 'https:'].includes(origin.protocol), `${name} must use HTTP or HTTPS`);
    assert(['127.0.0.1', 'localhost', '::1'].includes(origin.hostname), `${name} must target a loopback stack`);
    assert(!origin.username && !origin.password, `${name} origin cannot include credentials`);
    assert.equal(origin.pathname, '/', `${name} value must be an origin without a path`);
    assert.equal(origin.search + origin.hash, '', `${name} value must not include a query or fragment`);
  }
  const browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  browserForPage.set(browser.page, browser);
  return browser;
}

async function targetFor(page, selector, identity = {}) {
  const candidates = page.locator(selector);
  const count = await candidates.count();
  const matches = [];
  for (let index = 0; index < count; index++) {
    const candidate = candidates.nth(index);
    const label = normalize(await candidate.evaluate(element =>
      element.getAttribute('aria-label') || element.innerText || element.textContent));
    const matchesIdentity = identity.name !== undefined
      ? label === identity.name
      : identity.includes !== undefined
        ? label.toLocaleLowerCase().includes(identity.includes.toLocaleLowerCase())
        : true;
    if (matchesIdentity) matches.push(candidate);
  }
  assert.equal(matches.length, 1,
    `Expected one Playwright target for ${selector} ${JSON.stringify(identity)}, found ${matches.length}`);
  return matches[0];
}

export async function click(page, selector, identity = {}) {
  const browser = browserForPage.get(page);
  const label = `Click ${identity.name ?? identity.includes ?? selector}`;
  browser.activeAction = { label, locator: selector, startedAt: Date.now() };
  const target = await targetFor(page, selector, identity);
  assert(await target.isVisible(), `Playwright target is not visible: ${selector}`);
  assert(await target.isEnabled(), `Playwright target is disabled: ${selector}`);
  browser.lastAction = { label, locator: target.toString(), targetLocator: target, startedAt: Date.now() };
  await performAction(browser, label, target, (locator, { timeout }) => locator.click({ timeout }));
  return target;
}

export async function navigate(page, url) {
  const browser = browserForPage.get(page);
  const body = page.locator('body');
  browser.lastAction = { label: 'Navigate to Builder page', locator: body.toString(), targetLocator: body, startedAt: Date.now() };
  await performAction(browser, 'Navigate to Builder page', body,
    async () => { await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 5000 }); });
}

export async function browserEval(page, inspect, args) {
  assert.equal(typeof inspect, 'function', 'Browser inspection must be a callback');
  return page.evaluate(inspect, args);
}

export async function waitForBrowser(page, predicate, timeoutOrArgs = 5000, args) {
  assert.equal(typeof predicate, 'function', 'Browser waits must use a callback');
  const timeout = Array.isArray(timeoutOrArgs) ? 5000 : timeoutOrArgs;
  const waitArgs = Array.isArray(timeoutOrArgs) ? timeoutOrArgs : args;
  await page.waitForFunction(predicate, waitArgs, { timeout });
}

export async function waitForControl(page, selector, { timeout = 5000, enabled = false, hidden = false } = {}) {
  const control = page.locator(selector);
  const count = await control.count();
  if (hidden && count === 0) return control;
  await requireUnique(control, `Wait for ${selector}`);
  await control.waitFor({ state: hidden ? 'hidden' : 'visible', timeout });
  if (!hidden && enabled) assert(await control.isEnabled(), `Playwright control is disabled: ${selector}`);
  return control;
}

export async function selectOption(page, selector, value, { dismissSelector } = {}) {
  const browser = browserForPage.get(page);
  browser.activeAction = { label: `Select ${value}`, locator: selector, startedAt: Date.now() };
  const select = await targetFor(page, selector);
  assert(await select.isVisible(), `Playwright select is not visible: ${selector}`);
  assert(await select.isEnabled(), `Playwright select is disabled: ${selector}`);
  assert(await select.isEditable(), `Playwright select is not editable: ${selector}`);
  browser.lastAction = { label: `Select ${value}`, locator: select.toString(), targetLocator: select, startedAt: Date.now() };
  await performAction(browser, `Select ${value}`, select,
    (locator, { timeout }) => locator.selectOption(value, { timeout }));
  await page.waitForFunction(({ selector: targetSelector, expectedValue }) =>
    document.querySelector(targetSelector)?.value === expectedValue,
  { selector, expectedValue: value }, { timeout: 5000 });
  if (dismissSelector) await click(page, dismissSelector);
}

export async function fill(page, selector, value) {
  const browser = browserForPage.get(page);
  browser.activeAction = { label: `Fill ${selector}`, locator: selector, startedAt: Date.now() };
  const target = await targetFor(page, selector);
  await requireUnique(target, `Fill ${selector}`);
  assert(await target.isEditable(), `Playwright fill target is not editable: ${selector}`);
  browser.lastAction = { label: `Fill ${selector}`, locator: target.toString(), targetLocator: target, startedAt: Date.now() };
  await performAction(browser, `Fill ${selector}`, target,
    (locator, { timeout }) => locator.fill(value, { timeout }), { editable: true });
}
