import assert from 'node:assert/strict';
import { expect } from '@playwright/test';

const ACTION_TIMEOUT_MS = 5_000;

const boundedTimeout = (timeout) => Math.max(1, Math.min(ACTION_TIMEOUT_MS, Number(timeout) || ACTION_TIMEOUT_MS));

export function validatedArangoContainer(target, explicitOverride) {
  const validatedTarget = target?.arangoContainer;
  assert(typeof validatedTarget === 'string' && validatedTarget.length > 0,
    'The CDA fixture must provide its validated Arango container.');
  if (explicitOverride !== undefined) {
    assert.equal(explicitOverride, validatedTarget,
      'An explicit Arango container override must equal the validated CDA target.');
  }
  return validatedTarget;
}

export function createNativeCdaWorkflowTools({ page, cda }) {
  const click = (_page, selector, identity = {}, timeout = ACTION_TIMEOUT_MS) =>
    cda.click(selector, identity, boundedTimeout(timeout));
  const fill = (_page, selector, value, identityOrTimeout = {}, timeout = ACTION_TIMEOUT_MS) => {
    const identity = identityOrTimeout && typeof identityOrTimeout === 'object' ? identityOrTimeout : {};
    const requestedTimeout = typeof identityOrTimeout === 'number' ? identityOrTimeout : timeout;
    return cda.fill(selector, value, identity, boundedTimeout(requestedTimeout));
  };
  const selectOption = (_page, selector, value, options = {}) =>
    cda.selectOption(selector, value, { ...options, timeout: boundedTimeout(options.timeout) });
  const navigate = (_page, url) => cda.navigate(url);
  const inspect = (_page, callback, args = []) => cda.inspect(callback, args);
  const wait = (_page, predicate, args = [], timeout = ACTION_TIMEOUT_MS) => {
    const deadline = boundedTimeout(timeout);
    if (typeof predicate === 'function') return cda.wait(predicate, args, deadline);

    const { kind, selector, value, statuses, text } = predicate ?? {};
    const locator = page.locator(selector);
    if (kind === 'present') return expect(locator).toHaveCount(1, { timeout: deadline });
    if (kind === 'hidden') return expect(locator).toBeHidden({ timeout: deadline });
    if (kind === 'enabled') return expect(locator).toBeEnabled({ timeout: deadline });
    if (kind === 'value') return expect(locator).toHaveValue(value, { timeout: deadline });
    if (kind === 'some-text') return expect(locator).toContainText(text, { timeout: deadline });
    if (kind === 'some-text-exact') {
      return expect.poll(() => locator.allTextContents(), { timeout: deadline }).toContain(text);
    }
    if (kind === 'status-in') {
      return expect.poll(async () => statuses.includes(await locator.getAttribute('data-proposal-status')),
        { timeout: deadline }).toBe(true);
    }
    if (kind === 'open') {
      return expect.poll(() => locator.evaluate(element => Boolean(element.open)), { timeout: deadline }).toBe(value);
    }
    if (kind === 'rows') {
      const expectedCount = Number(predicate.count);
      return expect.poll(async () => {
        const rowCount = Number(await locator.getAttribute('aria-rowcount')) - 1;
        const bodyText = await page.locator('body').innerText();
        return rowCount === expectedCount
          && !bodyText.includes(predicate.loadingText ?? 'Loading your table…')
          && !bodyText.includes('Preview failed:');
      }, { timeout: deadline }).toBe(true);
    }
    if (kind === 'label-input-starts') {
      return expect.poll(() => locator.evaluateAll((labels, wanted) =>
        labels.some(label => label.innerText.trim().startsWith(wanted) && label.querySelector('input')), text),
      { timeout: deadline }).toBe(true);
    }
    if (kind === 'label-input-value') {
      return expect.poll(() => locator.evaluateAll((labels, wanted) =>
        labels.some(label => label.innerText.trim().startsWith(wanted.text)
          && label.querySelector('input')?.value === wanted.value), { text, value }),
      { timeout: deadline }).toBe(true);
    }
    throw new Error(`Unsupported native CDA wait condition: ${kind}`);
  };

  const clickControl = click;
  const fillControl = fill;
  const selectControl = selectOption;
  const nativeClick = click;
  const nativeFill = fill;
  const nativeSelect = selectOption;
  const captureCDARequests = (_page, options = {}) => cda.captureRequests(options.ownedPathPrefix, options);
  const captureRequests = (_page, _report, ownedPathPrefix, options = {}) => cda.captureRequests(ownedPathPrefix, options);
  const waitForCapturedResponse = (_page, tracker, predicate, timeout = ACTION_TIMEOUT_MS) =>
    cda.waitForCapturedResponse(tracker, predicate, boundedTimeout(timeout));
  const performAction = async (_owner, label, locator, perform, options = {}) => {
    const started = Date.now();
    await expect(locator, `${label}: expected exactly one native control`).toHaveCount(1,
      { timeout: boundedTimeout(options.timeout) });
    await cda.step(
      label,
      () => perform(locator, { timeout: boundedTimeout(options.timeout) }),
      boundedTimeout(options.timeout),
    );
    return Date.now() - started;
  };
  const requireUnique = async (locator, label = 'native control') => {
    await expect(locator, `${label}: expected exactly one native control`).toHaveCount(1, { timeout: ACTION_TIMEOUT_MS });
    return locator;
  };

  return {
    click, fill, selectOption, navigate, inspect,
    clickControl, fillControl, selectControl, nativeClick, nativeFill, nativeSelect,
    navigatePage: navigate,
    inspectDOM: inspect,
    browserEval: inspect,
    inspectPage: inspect,
    waitForDOM: wait,
    waitForBrowser: wait,
    captureCDARequests,
    captureRequests,
    waitForCapturedResponse,
    performAction,
    requireUnique,
  };
}
