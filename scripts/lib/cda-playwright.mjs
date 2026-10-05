import { captureCDARequests } from './cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';
import { expect } from '@playwright/test';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const sourceRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const forbiddenInspection = /\.(?:click|focus|blur|select|dispatchEvent|setAttribute|removeAttribute|scrollIntoView|submit|requestSubmit|remove|append|prepend|replaceWith|insertAdjacentHTML|on[A-Z]\w*|handle[A-Z]\w*)\s*\(|\[['"](?:click|focus|blur|select|dispatchEvent|setAttribute|removeAttribute|submit|requestSubmit)['"]\]\s*\(|\b(?:eval|Function)\s*\(|(?:^|[^\w$])(?:value|checked|selected|open|scrollTop|scrollLeft|innerHTML|outerHTML|textContent|innerText|dataset\.\w+|location(?:\.href)?)\s*=(?!=)|\[['"][^\]]+['"]\]\s*=(?!=)|\+\+|--/i;

function requireInspectionCallback(callback, label) {
  if (typeof callback !== 'function') throw new TypeError(`${label} requires a function callback.`);
  const source = Function.prototype.toString.call(callback);
  if (forbiddenInspection.test(source)) {
    throw new TypeError(`${label} callbacks may inspect results only; use Playwright locators for control interaction.`);
  }
}

export async function assertOwnedTarget({ project, apiOrigin, uiOrigin, arangoContainer, clickhouseContainer, requireClickhouse = false } = {}) {
  const ownedArango = arangoContainer ?? process.env.LOOM_CDA_ARANGO_CONTAINER;
  const ownedClickhouse = clickhouseContainer ?? process.env.LOOM_CDA_CLICKHOUSE_CONTAINER;
  if (!ownedArango) throw new Error('Set LOOM_CDA_ARANGO_CONTAINER to the isolated CDA source database container.');
  if (requireClickhouse && !ownedClickhouse) throw new Error('Set LOOM_CDA_CLICKHOUSE_CONTAINER to the isolated source database container.');
  return assertOwnedCdaTarget({
    project: project ?? process.env.LOOM_CDA_PROJECT,
    apiOrigin: apiOrigin ?? process.env.LOOM_CDA_API_ORIGIN,
    uiOrigin: uiOrigin ?? process.env.LOOM_CDA_UI_ORIGIN,
    apiContainer: process.env.LOOM_CDA_API_CONTAINER,
    composeProject: process.env.LOOM_CDA_COMPOSE_PROJECT,
    sourceRoot,
    arangoContainer: ownedArango,
    clickhouseContainer: ownedClickhouse,
  });
}

function normalizeLabel(value) {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

async function exactTarget(page, selector, identity = {}) {
  const candidates = page.locator(selector);
  const discoverMatches = () => candidates.evaluateAll((elements, match) => {
    const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    return elements.map((element, index) => ({
      index,
      label: normalize(element.getAttribute('aria-label') || element.innerText || element.textContent),
    })).filter(({ label }) => {
      if (match.name !== undefined) return label === match.name;
      if (match.includes !== undefined) return label.toLowerCase().includes(match.includes.toLowerCase());
      return true;
    });
  }, identity);
  let matches = [];
  await expect.poll(async () => {
    matches = await discoverMatches();
    return matches.length;
  }, {
    message: `Expected exactly one target for ${selector} ${identity.name ?? identity.includes ?? ''}`,
    timeout: 5000,
  }).toBe(1);
  return { locator: candidates.nth(matches[0].index), label: normalizeLabel(matches[0].label) };
}

async function action(actionContext, label, locator, perform, options = {}) {
  if (!actionContext || typeof actionContext.action !== 'function') {
    throw new TypeError('CDA actions require the native cda fixture action context.');
  }
  return actionContext.action(label, locator, perform, options);
}

export async function click(page, selector, identity = {}, timeout = 5000, actionContext) {
  const { locator, label } = await exactTarget(page, selector, identity);
  const actionLabel = identity.name ?? identity.includes ?? label;
  const state = {
    selector,
    label,
    visible: await locator.isVisible(),
    enabled: await locator.isEnabled(),
    tag: await locator.evaluate(element => element.tagName.toLowerCase()),
  };
  await action(actionContext, actionLabel, locator, () => locator.click({ timeout }), { timeout });
  return state;
}

export async function fill(page, selector, value, identity = {}, timeout = 5000, actionContext) {
  const { locator, label } = await exactTarget(page, selector, identity);
  const actionLabel = identity.name ?? identity.includes ?? label;
  const state = {
    selector,
    label,
    visible: await locator.isVisible(),
    enabled: await locator.isEnabled(),
    editable: await locator.isEditable(),
  };
  await action(actionContext, actionLabel, locator, () => locator.fill(String(value), { timeout }), { timeout, editable: true });
  return state;
}

export async function press(page, selector, key, timeout = 5000, actionContext) {
  const { locator, label } = await exactTarget(page, selector);
  await action(actionContext, `Press ${key} in ${label}`, locator,
    () => locator.press(key, { timeout }), { timeout, editable: true });
}

export async function selectOption(page, selector, value, options = {}, actionContext) {
  const { locator, label } = await exactTarget(page, selector);
  const timeout = Math.min(5000, options.timeout ?? 5000);
  if (!await locator.isVisible() || !await locator.isEnabled()) {
    throw new Error(`Select is not visible and enabled: ${selector}`);
  }
  const offered = await locator.locator('option').evaluateAll((items, wanted) =>
    items.some(item => item.value === wanted && !item.disabled), value);
  if (!offered) throw new Error(`Select does not offer enabled option ${value}: ${selector}`);
  await action(actionContext, `Select ${value}`, locator,
    () => locator.selectOption(value, { timeout }), { timeout });
  const settledWhen = options.settledWhen ?? ((target) => document.querySelector(target.selector)?.value === target.value);
  await waitForBrowser(page, settledWhen, [{ selector, value }], timeout);
  if (options.dismissSelector) await click(page, options.dismissSelector, {}, 5000, actionContext);
  return { selector, label, value };
}

export async function scrollIntoView(page, selector, identity = {}, timeout = 5000, actionContext) {
  const { locator, label } = await exactTarget(page, selector, identity);
  const inspectBounds = async () => {
    const bounds = await locator.evaluate(element => {
      const rect = element.getBoundingClientRect();
      return { top: rect.top, bottom: rect.bottom, viewportHeight: innerHeight,
        text: (element.getAttribute('aria-label') || element.innerText || element.textContent || '').trim() };
    });
    return { selector, label, ...bounds };
  };
  if (actionContext?.step) {
    return actionContext.step(`Scroll ${identity.name ?? identity.includes ?? label} into view`, async () => {
      await locator.scrollIntoViewIfNeeded({ timeout });
      return inspectBounds();
    });
  }
  await locator.scrollIntoViewIfNeeded({ timeout });
  return inspectBounds();
}

export async function navigate(page, url, actionContext) {
  const go = () => page.goto(url, { waitUntil: 'load', timeout: 5000 });
  if (actionContext?.navigate) return actionContext.navigate(url, go);
  if (actionContext?.step) return actionContext.step('Navigate to Builder page', go);
  return go();
}

export async function browserEval(page, inspect, args = []) {
  requireInspectionCallback(inspect, 'Browser inspection');
  if (typeof args === 'string') throw new TypeError('Browser inspection arguments must be structured data, not source code.');
  return page.evaluate(inspect, args);
}

export async function waitForBrowser(page, predicate, args = [], timeout = 5000) {
  requireInspectionCallback(predicate, 'Browser waits');
  if (typeof args === 'string') throw new TypeError('Browser wait arguments must be structured data, not source code.');
  await page.waitForFunction(predicate, args, { timeout: Math.min(5000, timeout) });
}

export function captureRequests(page, report, ownedPathPrefix, options = {}) {
  const apiOrigin = (options.apiOrigin ?? process.env.LOOM_CDA_API_ORIGIN)?.replace(/\/$/, '');
  const uiOrigin = (options.uiOrigin ?? process.env.LOOM_CDA_UI_ORIGIN)?.replace(/\/$/, '');
  return captureCDARequests(page, {
    ...options,
    apiOrigin,
    // Builder traffic calls the UI proxy; direct fixture fetches use apiOrigin.
    browserRequestOrigin: options.browserRequestOrigin ?? uiOrigin,
    appOrigins: options.appOrigins ?? [apiOrigin, uiOrigin],
    ownedPathPrefix,
    report,
  });
}

export async function waitForCapturedResponse(_page, tracker, predicate, timeout = 5000) {
  return tracker.waitFor(predicate, { timeoutMs: Math.min(5000, timeout) });
}

export function includeBrowserDiagnostics(diagnostics, report) {
  report.errors ??= [];
  const add = (entry, matches) => {
    if (!report.errors.some(matches)) report.errors.push(entry);
  };
  for (const failure of diagnostics.pageErrors ?? []) {
    add({ kind: 'runtime', message: failure.message }, existing => existing.kind === 'runtime' && existing.message === failure.message);
  }
  for (const failure of diagnostics.console ?? []) {
    add({ kind: 'console', message: failure.text, location: failure.location }, existing => existing.kind === 'console' && existing.message === failure.text);
  }
  for (const failure of diagnostics.networkFailures ?? []) {
    const requestIdentity = typeof failure.browserRequestId === 'string' && failure.browserRequestId
      ? ['browserRequestId', failure.browserRequestId]
      : typeof failure.playwrightRequestId === 'string' && failure.playwrightRequestId
        ? ['playwrightRequestId', failure.playwrightRequestId]
        : typeof failure.requestId === 'string' && failure.requestId
          ? ['requestId', failure.requestId]
          : undefined;
    add({ kind: 'network', path: failure.url, failure: failure.errorText, requestId: failure.requestId,
      playwrightRequestId: failure.playwrightRequestId,
      browserRequestId: failure.browserRequestId, ...(failure.expected ? { expected: true } : {}),
      ...(failure.canceled ? { canceled: true } : {}),
      ...(failure.expectedCancellation ? { expectedCancellation: failure.expectedCancellation } : {}) },
    existing => Boolean(requestIdentity) && existing.kind === 'network' &&
      existing[requestIdentity[0]] === requestIdentity[1]);
  }
  for (const failure of diagnostics.httpFailures ?? []) {
    add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body,
      playwrightRequestId: failure.playwrightRequestId, browserRequestId: failure.browserRequestId },
    existing => existing.kind === 'http' && existing.url === failure.url && existing.status === failure.status);
  }
  report.incidentalErrors ??= [];
  for (const failure of diagnostics.assetFailures ?? []) {
    if (!report.incidentalErrors.some(existing => existing.url === failure.url && existing.status === failure.status)) {
      report.incidentalErrors.push(failure);
    }
  }
}
