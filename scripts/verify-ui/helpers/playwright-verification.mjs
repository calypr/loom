import { performAction, requireUnique } from './playwright-actions.mjs';

export async function inspectDOM(page, inspect, args = {}) {
  if (typeof inspect !== 'function') throw new TypeError('inspectDOM requires a read-only inspection function');
  return page.evaluate(inspect, args);
}

export async function waitForDOM(page, predicate, args = {}, timeout = 30000) {
  if (typeof predicate !== 'function') throw new TypeError('waitForDOM requires an observable predicate');
  return page.waitForFunction(predicate, args, { timeout });
}

export async function navigatePage(page, url) {
  return page.goto(url, { waitUntil: 'domcontentloaded' });
}

function targetLocator(page, selector, identity = {}) {
  let locator = page.locator(selector);
  if (identity.name !== undefined) {
    locator = locator.filter({ has: page.getByText(identity.name, { exact: true }) });
  }
  if (identity.includes !== undefined) locator = locator.filter({ hasText: identity.includes });
  return locator;
}

export async function clickControl(tracker, page, selector, identity = {}, timeout = 5000) {
  const locator = targetLocator(page, selector, identity);
  const label = `Click ${identity.name ?? identity.includes ?? selector}`;
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.click(options), { timeout });
}

export async function fillControl(tracker, page, selector, value, timeout = 5000) {
  const locator = page.locator(selector);
  const label = `Fill ${selector}`;
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.fill(value, options), { timeout, editable: true });
}

export async function selectControl(tracker, page, selector, value, timeout = 5000) {
  const locator = page.locator(selector);
  const label = `Select ${value} in ${selector}`;
  await requireUnique(locator, label);
  return performAction(tracker, label, locator, (target, options) => target.selectOption(value, options), { timeout });
}
