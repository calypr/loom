import assert from 'node:assert/strict';
import { recordCheck } from './report.mjs';

let lastLocator;
let evidenceContext;
let timingDepth = 0;
let activeTimingName;
let actionRoutedForTiming = false;

export const setActionEvidence = (browser, report, action) => {
  evidenceContext = browser && report ? { browser, report, action } : undefined;
};

const captureActionFailure = async (label, locator, error, started) => {
  if (!evidenceContext) return;
  const { browser, report } = evidenceContext;
  const elapsedMs = Date.now() - started;
  const state = await locatorState(locator);
  report.failureAction ??= { name: label, locator: locator.toString(), state, elapsedMs };
  report.failureTrace ??= await browser.captureFailure(error, {
    action: { label, locator: locator.toString(), targetLocator: locator }, elapsedMs, phase: 'action',
  }).catch(() => undefined);
};

const locatorState = async locator => {
  try {
    const count = await locator.count();
    if (count !== 1) return { count, visible: false, enabled: false, editable: false };
    return {
      count,
      visible: await locator.isVisible(),
      enabled: await locator.isEnabled(),
      editable: await locator.isEditable().catch(() => false),
    };
  } catch (error) {
    return { error: String(error?.message ?? error) };
  }
};

export const controlLocator = (page, selector, identity = {}) => {
  if (identity.name !== undefined || identity.includes !== undefined) {
    const accessibleName = identity.name ?? new RegExp(String(identity.includes).replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    const role = /input\[type=["']checkbox/.test(selector) ? 'checkbox'
      : /input\[type=["']radio/.test(selector) ? 'radio'
        : /select|input|textarea/.test(selector) ? 'combobox'
        : /button|summary/.test(selector) ? 'button' : null;
    if (role) {
      const scope = selector.match(/^(.*?)(?:\s+)(?:button|input|select|textarea)(?:\[|$)/)?.[1];
      const root = scope ? page.locator(scope) : page;
      return root.getByRole(role, { name: accessibleName, exact: identity.name !== undefined });
    }
  }
  return page.locator(selector);
};

export const inspectAction = async (page, selector) => {
  const locator = page.locator(selector);
  const state = await locatorState(locator);
  let receivesEvents = false;
  if (state.count === 1 && state.visible && state.enabled) {
    try { await locator.click({ trial: true }); receivesEvents = true; }
    catch { receivesEvents = false; }
  }
  return { ...state, found: state.count === 1, receivesEvents,
    actionable: state.count === 1 && state.visible && state.enabled && receivesEvents };
};

export const isActionable = state => Boolean(state?.actionable);

const uniqueActionable = async (locator, label) => {
  const state = await locatorState(locator);
  assert.equal(state.count, 1, `${label}: expected one control, found ${state.count}`);
  assert(state.visible, `${label}: control is not visible`);
  assert(state.enabled, `${label}: control is disabled`);
  return state;
};

export const click = async (page, selector, identity = {}) => {
  const locator = controlLocator(page, selector, identity);
  lastLocator = locator;
  const label = `click ${identity.name ?? identity.includes ?? selector}`;
  const started = Date.now();
  try {
    const perform = async () => {
      await uniqueActionable(locator, label);
      await locator.click({ trial: true });
      await locator.click();
    };
    if (evidenceContext?.action && (!timingDepth || !actionRoutedForTiming)) {
      await evidenceContext.action(activeTimingName ?? label, locator, perform);
      if (timingDepth) actionRoutedForTiming = true;
    } else await perform();
  } catch (error) {
    await captureActionFailure(label, locator, error, started);
    throw error;
  }
  return locator;
};

export const fill = async (page, selector, value) => {
  const locator = page.locator(selector);
  lastLocator = locator;
  const label = `fill ${selector}`;
  const started = Date.now();
  try {
    const perform = async () => {
      await uniqueActionable(locator, label);
      assert(await locator.isEditable(), `${label}: control is read-only`);
      if (await locator.evaluate(element => element instanceof HTMLSelectElement)) {
        await locator.selectOption(String(value));
      } else {
        await locator.fill(String(value));
      }
    };
    if (evidenceContext?.action && (!timingDepth || !actionRoutedForTiming)) {
      await evidenceContext.action(activeTimingName ?? label, locator, perform, { editable: true });
      if (timingDepth) actionRoutedForTiming = true;
    } else await perform();
  } catch (error) {
    await captureActionFailure(label, locator, error, started);
    throw error;
  }
  return locator;
};

export const evaluate = (page, expression) => page.evaluate(source => {
  // The migrated callers pass read-only DOM expressions. Actions use Locator APIs above.
  return Function(`"use strict"; return (${source});`)();
}, expression);

export const waitFor = (page, expression, timeout = 30000) => page.waitForFunction(source => {
  return Boolean(Function(`"use strict"; return (${source});`)());
}, expression, { timeout });

export const reload = async (page, expression) => {
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitFor(page, expression, 30000);
};

export const goto = async (page, url, waitForExpression) => {
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  if (waitForExpression) await waitFor(page, waitForExpression, 30000);
};

export const recordPlaywrightTiming = async (report, page, browser, { name, action, after, timeout = 30000, budget = 5000, dimension = 'usability' }) => {
  const started = Date.now();
  timingDepth += 1;
  activeTimingName = `${name} control action`;
  actionRoutedForTiming = false;
  try {
    await action();
    if (after) await waitFor(page, after, timeout);
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'passed', elapsedMs, renderTimeoutMs: timeout, performanceBudgetMs: budget });
    report.timings[name] = elapsedMs;
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', elapsedMs <= budget,
      { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    recordCheck(report, dimension, name + ' completed', true, { elapsedMs });
    return elapsedMs;
  } catch (error) {
    const elapsedMs = Date.now() - started;
    const state = lastLocator ? await locatorState(lastLocator) : null;
    report.actions.push({ name, status: 'failed', elapsedMs, error: String(error?.message ?? error), control: state });
    report.failureAction = { name, locator: lastLocator?.toString() ?? null, state, elapsedMs };
    recordCheck(report, dimension, name + ' completed', false, { elapsedMs, error: String(error?.message ?? error), control: state });
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', false,
      { elapsedMs, budgetMs: budget, waitTimeoutMs: timeout });
    await browser.captureFailure(error, {
      action: { label: name, locator: lastLocator?.toString(), targetLocator: lastLocator }, elapsedMs, phase: 'action',
    }).catch(() => undefined);
    throw error;
  } finally {
    timingDepth -= 1;
    if (!timingDepth) {
      activeTimingName = undefined;
      actionRoutedForTiming = false;
    }
    lastLocator = undefined;
  }
};
