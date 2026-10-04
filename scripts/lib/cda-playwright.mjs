import { launchBrowser as launchPlaywrightBrowser, sanitizeText } from './playwright-browser.mjs';
import { performAction } from './playwright-actions.mjs';
import { captureCDARequests } from './cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './owned-cda-target.mjs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';

const sourceRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));

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

const forbiddenInspection = /\.(?:click|focus|blur|select|dispatchEvent|setAttribute|removeAttribute|scrollIntoView|submit|requestSubmit|on[A-Z]\w*|handle[A-Z]\w*)\s*\(|(?:^|[^\w$])(?:value|checked|selected|open|scrollTop|scrollLeft|innerHTML|outerHTML|textContent|innerText|dataset\.\w+|location(?:\.href)?)\s*=(?!=)|\+\+|--/i;

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

function readOnlyBody(body) {
  if (forbiddenInspection.test(body)) {
    throw new Error('Browser evaluation is read-only. Use a Playwright locator or keyboard action for control interaction.');
  }
  return body;
}

export async function browserEval(page, body) {
  const inspection = readOnlyBody(body);
  return page.evaluate(new Function(inspection));
}

export async function waitForBrowser(page, expression, timeout = 30000) {
  const predicateBody = readOnlyBody(`return Boolean(${expression});`);
  const predicate = new Function(predicateBody);
  await page.waitForFunction(predicate, undefined, { timeout });
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
  await waitForBrowser(page, options.settledWhen ?? `document.querySelector(${JSON.stringify(selector)})?.value === ${JSON.stringify(value)}`,
    options.timeout ?? 5000);
  if (options.dismissSelector) await click(page, options.dismissSelector);
  return { selector, label, value };
}

export async function navigate(page, url) {
  await page.goto(url, { waitUntil: 'load', timeout: 30000 });
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

export async function waitForCapturedResponse(page, tracker, predicate, timeout = 10000) {
  const response = await page.waitForResponse(candidate => {
    const entry = tracker.byRequest.get(candidate.request());
    return Boolean(entry && predicate(entry));
  }, { timeout });
  await tracker.flush();
  const entry = tracker.byRequest.get(response.request());
  if (!entry) throw new Error(`Playwright response was outside the owned request prefix: ${response.url()}`);
  return entry;
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
