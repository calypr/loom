import { captureCDARequests } from './cda-playwright-requests.mjs';
import { performAction } from './playwright-actions.mjs';

const escapeRegExp = value => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

export function createCDAPlaywrightControls({ browser, browserApiOrigin, ownedPathPrefix, report, shouldReportHttpError }) {
  if (!browser?.page || !report?.errors || !report?.nativeRequests) {
    throw new TypeError('CDA Playwright controls require a browser page and error/request report arrays');
  }
  let lastAction;
  const cursor = { console: 0, httpFailures: 0, networkFailures: 0 };
  const requests = captureCDARequests(browser.page, {
    apiOrigin: browserApiOrigin,
    appOrigins: [browserApiOrigin],
    ownedPathPrefix,
    report,
    ...(shouldReportHttpError ? { shouldReportHttpError } : {}),
  });
  const remember = (label, locator, startedAt, elapsedMs) => {
    lastAction = { label, locator: locator.toString(), targetLocator: locator, startedAt, elapsedMs };
    browser.lastElapsedMs = elapsedMs;
    return elapsedMs;
  };
  const locatorFor = (selector, identity = {}) => {
    let locator = browser.page.locator(selector);
    if (identity.name !== undefined) locator = locator.filter({ hasText: new RegExp(`^\\s*${escapeRegExp(identity.name)}\\s*$`) });
    if (identity.includes !== undefined) locator = locator.filter({ hasText: identity.includes });
    return locator;
  };
  const click = async (selector, identity = {}) => {
    const locator = locatorFor(selector, identity);
    const label = `Click ${identity.name ?? identity.includes ?? selector}`;
    const startedAt = Date.now();
    return remember(label, locator, startedAt, await performAction(browser, label, locator, target => target.click()));
  };
  const selectOption = async (selector, value) => {
    const locator = browser.page.locator(selector);
    const label = `Select ${value} in ${selector}`;
    const startedAt = Date.now();
    return remember(label, locator, startedAt, await performAction(browser, label, locator, target => target.selectOption(value)));
  };
  const fill = async (selector, value) => {
    const locator = browser.page.locator(selector);
    const label = `Fill ${selector}`;
    const startedAt = Date.now();
    return remember(label, locator, startedAt, await performAction(browser, label, locator, target => target.fill(value), { editable: true }));
  };
  const evaluate = (read, argument) => {
    if (typeof read !== 'function') throw new TypeError('Browser evaluation must be a read-only function');
    return browser.page.evaluate(read, argument);
  };
  const wait = (predicate, argument, timeout = 5000) => {
    if (typeof predicate !== 'function') throw new TypeError('Browser waits must use a Playwright predicate callback');
    return browser.page.waitForFunction(predicate, argument, { timeout });
  };
  const navigate = async url => {
    const startedAt = Date.now();
    const label = 'Navigate to Builder';
    const locator = browser.page.locator('body');
    browser.activeAction = { label, locator: `page.goto(${url})`, targetLocator: locator, startedAt };
    await browser.page.goto(url, { waitUntil: 'domcontentloaded', timeout: 30000 });
    browser.actions ??= [];
    const elapsedMs = Date.now() - startedAt;
    browser.actions.push({ label, elapsedMs });
    lastAction = { label, locator: `page.goto(${url})`, targetLocator: locator, startedAt, elapsedMs };
    browser.activeAction = undefined;
    browser.lastElapsedMs = elapsedMs;
    return elapsedMs;
  };
  const collectDiagnostics = () => {
    const diagnostics = browser.diagnostics;
    for (const item of diagnostics.console.slice(cursor.console)) {
      if (item.location?.endsWith('/favicon.ico')) continue;
      report.errors.push({ kind: 'console', details: item.text, url: item.location });
    }
    cursor.console = diagnostics.console.length;
    for (const item of diagnostics.httpFailures.slice(cursor.httpFailures)) {
      const pathname = new URL(item.url).pathname;
      if (pathname.startsWith(ownedPathPrefix)) continue;
      report.errors.push({ kind: 'http', url: pathname, status: item.status, body: item.body });
    }
    cursor.httpFailures = diagnostics.httpFailures.length;
    for (const item of diagnostics.networkFailures.slice(cursor.networkFailures)) {
      const pathname = new URL(item.url).pathname;
      if (pathname.startsWith(ownedPathPrefix)) continue;
      if (item.method === 'GET' && item.failure === 'net::ERR_ABORTED' && ['/frame-source-options', '/semantic-inventory'].includes(pathname)) {
        report.expectedCancellations ??= [];
        report.expectedCancellations.push({ method: item.method, url: pathname, reason: 'Superseded catalog read was aborted by the UI' });
        continue;
      }
      report.errors.push({ kind: 'network', url: pathname, method: item.method, details: item.failure });
    }
    cursor.networkFailures = diagnostics.networkFailures.length;
  };
  return {
    click,
    selectOption,
    fill,
    evaluate,
    wait,
    navigate,
    get lastAction() { return lastAction; },
    async flush() {
      await requests.flush();
      collectDiagnostics();
    },
  };
}
