import { launchBrowser as launchPlaywrightBrowser, sanitizeText } from './playwright-browser.mjs';
import { performAction } from './playwright-actions.mjs';
import { captureCDARequests } from './cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';
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
  const ownedArango = arangoContainer ?? process.env.LOOM_ARANGO_CONTAINER;
  const ownedClickhouse = clickhouseContainer ?? process.env.LOOM_CLICKHOUSE_CONTAINER;
  if (!ownedArango) throw new Error('Set LOOM_ARANGO_CONTAINER to the isolated CDA source database container.');
  if (requireClickhouse && !ownedClickhouse) throw new Error('Set LOOM_CLICKHOUSE_CONTAINER to the isolated source database container.');
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

export async function launchBrowser(evidence, onDialog, {
  noAuth = true,
  apiOrigin: configuredApiOrigin = process.env.LOOM_CDA_API_ORIGIN,
  uiOrigin: configuredUiOrigin = process.env.LOOM_CDA_UI_ORIGIN,
} = {}) {
  const uiOrigin = configuredUiOrigin.replace(/\/$/, '');
  const apiOrigin = configuredApiOrigin.replace(/\/$/, '');
  const session = await launchPlaywrightBrowser({ evidence, appOrigins: [uiOrigin, apiOrigin], noAuth });
  const dialogErrors = [];
  if (onDialog) {
    session.page.on('dialog', async dialog => {
      try {
        const response = await onDialog({ type: dialog.type(), message: dialog.message(), defaultValue: dialog.defaultValue() });
        if (response?.accept === false) await dialog.dismiss();
        else await dialog.accept(response?.promptText);
      } catch (error) {
        dialogErrors.push(sanitizeText(error?.message ?? error));
        await dialog.dismiss().catch(() => undefined);
      }
    });
  } else {
    session.page.on('dialog', async dialog => {
      dialogErrors.push(`Unexpected ${dialog.type()} dialog: ${sanitizeText(dialog.message())}`);
      await dialog.dismiss().catch(() => undefined);
    });
  }
  const result = { ...session, dialogErrors, actions: [] };
  session.page.__loomSession = result;
  return result;
}

export async function browserEval(page, inspect, args = []) {
  requireInspectionCallback(inspect, 'Browser inspection');
  if (typeof args === 'string') throw new TypeError('Browser inspection arguments must be structured data, not source code.');
  return page.evaluate(inspect, args);
}

export async function waitForBrowser(page, predicate, args = [], timeout = 5000) {
  requireInspectionCallback(predicate, 'Browser waits');
  if (typeof args === 'string') throw new TypeError('Browser wait arguments must be structured data, not source code.');
  await page.waitForFunction(predicate, args, { timeout });
}

function normalizedLabel(value) {
  return String(value ?? '').replace(/\s+/g, ' ').trim();
}

async function exactTarget(page, selector, identity = {}) {
  const candidates = page.locator(selector);
  const matches = await candidates.evaluateAll((elements, match) => {
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
  if (matches.length !== 1) {
    throw new Error(`Expected exactly one target for ${selector} ${identity.name ?? identity.includes ?? ''}; found ${matches.length}`);
  }
  return { locator: candidates.nth(matches[0].index), label: normalizedLabel(matches[0].label) };
}

export async function click(page, selector, identity = {}, timeout = 5000) {
  const { locator, label } = await exactTarget(page, selector, identity);
  const actionLabel = identity.name ?? identity.includes ?? label;
  const state = { selector, label, visible: await locator.isVisible(), enabled: await locator.isEnabled(),
    tag: await locator.evaluate(element => element.tagName.toLowerCase()) };
  await performAction(page.__loomSession, actionLabel, locator, (target, options) => target.click(options), { timeout });
  return state;
}

export async function fill(page, selector, value, identity = {}, timeout = 5000) {
  const { locator, label } = await exactTarget(page, selector, identity);
  const actionLabel = identity.name ?? identity.includes ?? label;
  const state = { selector, label, visible: await locator.isVisible(), enabled: await locator.isEnabled(), editable: await locator.isEditable() };
  await performAction(page.__loomSession, actionLabel, locator, async (target, options) => {
    await target.click({ ...options, trial: true });
    await target.fill(String(value), options);
  }, { timeout, editable: true });
  return state;
}

export async function press(page, selector, key, timeout = 5000) {
  const { locator, label } = await exactTarget(page, selector);
  await performAction(page.__loomSession, `Press ${key} in ${label}`, locator, async (target, actionOptions) => {
    await target.click({ ...actionOptions, trial: true });
    await target.press(key, actionOptions);
  }, { timeout, editable: true });
}

export async function selectOption(page, selector, value, options = {}) {
  const { locator, label } = await exactTarget(page, selector);
  if (!await locator.isVisible() || !await locator.isEnabled()) {
    throw new Error(`Select is not visible and enabled: ${selector}`);
  }
  const offered = await locator.locator('option').evaluateAll((items, wanted) =>
    items.some(item => item.value === wanted && !item.disabled), value);
  if (!offered) throw new Error(`Select does not offer enabled option ${value}: ${selector}`);
  await performAction(page.__loomSession, `Select ${value}`, locator, async (target, actionOptions) => {
    await target.click({ ...actionOptions, trial: true });
    await target.selectOption(value, actionOptions);
  }, { timeout: options.timeout ?? 5000 });
  const settledWhen = options.settledWhen ?? ((target) => document.querySelector(target.selector)?.value === target.value);
  await waitForBrowser(page, settledWhen, [{ selector, value }], options.timeout ?? 5000);
  if (options.dismissSelector) await click(page, options.dismissSelector);
  return { selector, label, value };
}

export async function scrollIntoView(page, selector, identity = {}, timeout = 5000) {
  const { locator, label } = await exactTarget(page, selector, identity);
  await locator.scrollIntoViewIfNeeded({ timeout });
  const bounds = await locator.evaluate(element => {
    const rect = element.getBoundingClientRect();
    return { top: rect.top, bottom: rect.bottom, viewportHeight: innerHeight,
      text: (element.getAttribute('aria-label') || element.innerText || element.textContent || '').trim() };
  });
  return { selector, label, ...bounds };
}

export async function navigate(page, url) {
  await page.goto(url, { waitUntil: 'load', timeout: 5000 });
}

export function captureRequests(browser, report, ownedPathPrefix, options = {}) {
  const apiOrigin = options.apiOrigin ?? process.env.LOOM_CDA_API_ORIGIN;
  const uiOrigin = options.uiOrigin ?? process.env.LOOM_CDA_UI_ORIGIN;
  const tracker = captureCDARequests(browser.page, {
    ...options,
    // The Builder browser calls the UI proxy; direct fixture/setup fetches use apiOrigin.
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix,
    report,
  });
  browser.failureContext = () => ({
    project: report.project ?? report.target?.project,
    explorer: report.explorer ?? report.explorerId,
    recentOwnedRequests: report.nativeRequests.slice(-30),
    setup: report.setup?.slice(-10),
    latestCase: report.cases?.at(-1),
    latestAction: report.actions?.at(-1),
  });
  return tracker;
}

export async function waitForCapturedResponse(_page, tracker, predicate, timeout = 5000) {
  return tracker.waitFor(predicate, { timeoutMs: timeout });
}

export function includeBrowserDiagnostics(browser, report) {
  report.errors ??= [];
  const add = (entry, matches) => {
    if (!report.errors.some(matches)) report.errors.push(entry);
  };
  for (const failure of browser.diagnostics.pageErrors) {
    add({ kind: 'runtime', message: failure.message }, existing => existing.kind === 'runtime' && existing.message === failure.message);
  }
  for (const failure of browser.diagnostics.console) {
    add({ kind: 'console', message: failure.text, location: failure.location }, existing => existing.kind === 'console' && existing.message === failure.text);
  }
  for (const failure of browser.diagnostics.networkFailures) {
    add({ kind: 'network', path: failure.url, failure: failure.failure }, existing => existing.kind === 'network' && existing.path === failure.url);
  }
  for (const failure of browser.diagnostics.httpFailures) {
    add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, existing => existing.kind === 'http' && existing.url === failure.url && existing.status === failure.status);
  }
  report.incidentalErrors ??= [];
  for (const failure of browser.diagnostics.assetFailures) {
    if (!report.incidentalErrors.some(existing => existing.url === failure.url && existing.status === failure.status)) {
      report.incidentalErrors.push(failure);
    }
  }
}
