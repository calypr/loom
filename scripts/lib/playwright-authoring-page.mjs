import assert from 'node:assert/strict';
import { recordCheck } from '../verify-ui/report.mjs';

const ACTION_TIMEOUT_MS = 5_000;

export const configureNativePage = page => {
  page.setDefaultTimeout(ACTION_TIMEOUT_MS);
  page.setDefaultNavigationTimeout(ACTION_TIMEOUT_MS);
};

const pageFor = workflow => {
  assert(workflow?.page, 'Native Playwright workflow must include a page');
  return workflow.page;
};

const escapeRegExp = value => String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const exactTextPattern = value => {
  const words = String(value).trim().split(/\s+/).filter(Boolean).map(escapeRegExp);
  return new RegExp(`^\\s*${words.join('\\s+')}\\s*$`);
};

const controlLocator = (page, selector, identity = {}) => {
  if (identity.name !== undefined || identity.includes !== undefined) {
    const accessibleName = identity.name ?? new RegExp(String(identity.includes).replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
    if (selector.includes('summary')) {
      const hasText = identity.name !== undefined ? exactTextPattern(identity.name) : accessibleName;
      return page.locator(selector).filter({ hasText });
    }
    const role = /input\[type=["']checkbox/.test(selector) ? 'checkbox'
      : /input\[type=["']radio/.test(selector) ? 'radio'
        : /select|input|textarea/.test(selector) ? 'combobox'
          : /button/.test(selector) ? 'button' : null;
    if (role) {
      const scope = selector.match(/^(.*?)(?:\s+)(?:button|input|select|textarea)(?:\[|$)/)?.[1];
      const root = scope ? page.locator(scope) : page;
      return root.getByRole(role, { name: accessibleName, exact: identity.name !== undefined });
    }
  }
  return page.locator(selector);
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

export const click = async (workflow, selector, identity = {}) => {
  const page = pageFor(workflow);
  const locator = controlLocator(page, selector, identity);
  const label = `click ${identity.name ?? identity.includes ?? selector}`;
  await workflow.action(label, locator, () => locator.click());
  return locator;
};

export const fill = async (workflow, selector, value) => {
  const page = pageFor(workflow);
  const locator = page.locator(selector);
  const label = `fill ${selector}`;
  await workflow.action(label, locator, async () => {
    if (await locator.evaluate(element => element instanceof HTMLSelectElement)) {
      await locator.selectOption(String(value));
    } else {
      await locator.fill(String(value));
    }
  }, { editable: true });
  return locator;
};

export const evaluate = (page, expression) => page.evaluate(source => {
  return Function(`"use strict"; return (${source});`)();
}, expression);

export const waitFor = (page, expression, timeout = ACTION_TIMEOUT_MS) => page.waitForFunction(source => {
  return Boolean(Function(`"use strict"; return (${source});`)());
}, expression, { timeout: Math.min(ACTION_TIMEOUT_MS, timeout) });

export const reload = async (page, expression) => {
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await waitFor(page, expression);
};

export const goto = async (page, url, waitForExpression) => {
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  if (waitForExpression) await waitFor(page, waitForExpression);
};

export const recordPlaywrightTiming = async (report, page, workflow, {
  name, action, after, timeout = ACTION_TIMEOUT_MS, budget = ACTION_TIMEOUT_MS, dimension = 'usability',
}) => {
  const waitTimeoutMs = Math.min(ACTION_TIMEOUT_MS, timeout);
  const budgetMs = Math.min(ACTION_TIMEOUT_MS, budget);
  const started = Date.now();
  try {
    await action();
    if (after) await waitFor(page, after, waitTimeoutMs);
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'passed', elapsedMs, renderTimeoutMs: waitTimeoutMs, performanceBudgetMs: budgetMs });
    report.timings[name] = elapsedMs;
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', elapsedMs <= budgetMs,
      { elapsedMs, budgetMs, waitTimeoutMs });
    recordCheck(report, dimension, name + ' completed', true, { elapsedMs });
    return elapsedMs;
  } catch (error) {
    const elapsedMs = Date.now() - started;
    report.actions.push({ name, status: 'failed', elapsedMs, error: String(error?.message ?? error) });
    report.failureAction ??= { name, elapsedMs };
    recordCheck(report, dimension, name + ' completed', false,
      { elapsedMs, error: String(error?.message ?? error) });
    if (after) recordCheck(report, 'performance', name + ' action-to-render within budget', false,
      { elapsedMs, budgetMs, waitTimeoutMs });
    throw error;
  }
};
