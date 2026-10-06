import assert from 'node:assert/strict';
import { test } from 'node:test';
import { browserEval, includeBrowserDiagnostics, waitForBrowser, waitForCapturedResponse } from '../cda-playwright.mjs';

test('browser network diagnostics deduplicate only the same captured request', () => {
  const url = 'http://127.0.0.1:30008/api/v1/projects/owned/explorers/editor/authoring/v2/construction-capabilities';
  const report = { errors: [] };
  const failure = (browserRequestId, playwrightRequestId = browserRequestId) => ({
    url,
    errorText: 'net::ERR_ABORTED',
    browserRequestId,
    playwrightRequestId,
  });

  includeBrowserDiagnostics({ networkFailures: [failure('playwright-1'), failure('playwright-2')] }, report);
  includeBrowserDiagnostics({ networkFailures: [failure('playwright-1')] }, report);
  assert.equal(report.errors.length, 2, 'same-URL failures from distinct requests must both remain, while the same request is deduplicated');
  assert.deepEqual(report.errors.map(error => error.browserRequestId), ['playwright-1', 'playwright-2']);

  const legacyReport = { errors: [] };
  includeBrowserDiagnostics({ networkFailures: [failure(undefined, 'request-1'), failure(undefined, 'request-2')] }, legacyReport);
  includeBrowserDiagnostics({ networkFailures: [failure(undefined, 'request-1')] }, legacyReport);
  assert.equal(legacyReport.errors.length, 2, 'legacy Playwright request IDs provide exact fallback identity');
  assert.deepEqual(legacyReport.errors.map(error => error.playwrightRequestId), ['request-1', 'request-2']);

  const requestIdReport = { errors: [] };
  const requestIDFailure = requestId => ({ url, errorText: 'net::ERR_ABORTED', requestId });
  includeBrowserDiagnostics({ networkFailures: [requestIDFailure('legacy-1'), requestIDFailure('legacy-2')] }, requestIdReport);
  includeBrowserDiagnostics({ networkFailures: [requestIDFailure('legacy-1')] }, requestIdReport);
  assert.equal(requestIdReport.errors.length, 2, 'legacy request IDs remain available as an exact fallback identity');
  assert.deepEqual(requestIdReport.errors.map(error => error.requestId), ['legacy-1', 'legacy-2']);

  const anonymousReport = { errors: [] };
  includeBrowserDiagnostics({ networkFailures: [failure(undefined, undefined), failure(undefined, undefined)] }, anonymousReport);
  assert.equal(anonymousReport.errors.length, 2, 'unidentified same-URL failures must not be collapsed');
});

test('captured response wait accepts a response completed before the visible UI result', async () => {
  const completed = { path: '/owned/preview', status: 200, response: { rows: [['patient-1']] }, completedAt: 1 };
  const tracker = {
    waitFor(predicate, { timeoutMs }) {
      assert.equal(timeoutMs, 5000);
      assert(predicate(completed));
      return Promise.resolve(completed);
    },
  };
  assert.equal(await waitForCapturedResponse(null, tracker, entry => entry.path.endsWith('/preview')), completed);
});

test('browser inspection and waits require callbacks with explicit serializable arguments', async () => {
  const calls = [];
  const page = {
    async evaluate(callback, args) {
      calls.push({ kind: 'evaluate', args });
      return callback(args);
    },
    async waitForFunction(callback, args, options) {
      calls.push({ kind: 'wait', args, timeout: options.timeout });
      assert.equal(callback(args), true);
    },
  };

  assert.equal(await browserEval(page, ([left, right]) => left + right, [19, 23]), 42);
  await waitForBrowser(page, ([expected, actual]) => expected === actual, [42, 42]);
  assert.deepEqual(calls, [
    { kind: 'evaluate', args: [19, 23] },
    { kind: 'wait', args: [42, 42], timeout: 5000 },
  ]);
});

test('browser inspection rejects source strings and obvious control mutations', async () => {
  let evaluated = false;
  const page = { async evaluate() { evaluated = true; } };
  await assert.rejects(browserEval(page, 'return document.body.innerText;'), /function callback/);
  await assert.rejects(browserEval(page, () => document.querySelector('button').click()), /inspect results only/);
  await assert.rejects(browserEval(page, () => document.querySelector('button')['click']()), /inspect results only/);
  await assert.rejects(waitForBrowser(page, 'document.querySelector("button")'), /function callback/);
  await assert.rejects(waitForBrowser(page, () => { document.querySelector('input').value = 'false pass'; return true; }), /inspect results only/);
  await assert.rejects(waitForBrowser(page, () => { document.querySelector('input')['value'] = 'false pass'; return true; }), /inspect results only/);
  assert.equal(evaluated, false, 'Rejected inspections must never reach Playwright evaluate.');
});

test('browser waits reject source strings and use observable Playwright conditions', async () => {
  let waited = false;
  const page = { async waitForFunction() { waited = true; } };
  await assert.rejects(waitForBrowser(page, 'document.readyState === "complete"'), /function callback/);
  assert.equal(waited, false, 'Rejected waits must never reach Playwright waitForFunction.');
});
