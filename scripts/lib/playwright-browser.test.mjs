import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { launchBrowser, matchesPendingCancellation } from './playwright-browser.mjs';

test('catalog cancellation requires exact method, Explorer path, and component request identity', () => {
  const path = '/api/v1/projects/owned/explorers/current/authoring/v2/semantic-inventory';
  const expected = { origin: 'http://127.0.0.1:30102', method: 'POST', paths: [path], requestIdPrefixes: ['feature-catalog-'] };
  const request = (url, method, requestId) => ({ url: () => url, method: () => method, headers: () => ({ 'x-request-id': requestId }) });
  assert.equal(matchesPendingCancellation(request(`http://127.0.0.1:30102${path}`, 'POST', 'feature-catalog-123'), expected), true);
  assert.equal(matchesPendingCancellation(request(`http://127.0.0.1:30102${path}`, 'GET', 'feature-catalog-123'), expected), false);
  assert.equal(matchesPendingCancellation(request(`http://127.0.0.1:8282${path}`, 'POST', 'feature-catalog-123'), expected), false);
  assert.equal(matchesPendingCancellation(request(`http://127.0.0.1:30102${path.replace('current', 'other')}`, 'POST', 'feature-catalog-123'), expected), false);
  assert.equal(matchesPendingCancellation(request(`http://127.0.0.1:30102${path}`, 'POST', 'unrelated-123'), expected), false);
});

test('unexpected browser request abort remains recorded and does not crash diagnostics', async () => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-abort-test-'));
  const browser = await launchBrowser({ evidence, appOrigins: ['https://loom.local'] });
  try {
    await browser.page.route('https://loom.local/**', route => route.abort());
    await assert.rejects(browser.page.goto('https://loom.local/check', { waitUntil: 'domcontentloaded' }));
    assert.equal(browser.diagnostics.networkFailures.length, 1);
    assert.equal(browser.diagnostics.networkFailures[0].canceled, undefined);
  } finally {
    await browser.close();
    await rm(evidence, { recursive: true, force: true });
  }
});

test('Playwright helper clicks a native control and records first-failure evidence', async t => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-helper-test-'));
  const origin = 'https://loom.local';
  const apiPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/pivot-category-edit-browser-123/authoring/v2/builder';
  let browser;

  try {
    try {
      browser = await launchBrowser({ evidence, appOrigins: [origin] });
    } catch (error) {
      if (/Executable doesn't exist|browserType\.launch:.*(?:not found|failed to launch)/i.test(String(error))) {
        t.skip(`Chromium is unavailable in this checkout: ${error.message}`);
        return;
      }
      throw error;
    }
    await browser.page.route(`${origin}/**`, route => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === apiPath) {
        return route.fulfill({
          status: 422,
          contentType: 'application/json',
          body: JSON.stringify({
            diagnostic: 'owned builder validation detail',
            password: 'must-not-be-retained',
            access_token: 'also-must-not-be-retained',
          }),
        });
      }
      return route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: `<!doctype html>
          <button type="button">Duplicate</button><button type="button">Duplicate</button>
          <button type="button" disabled>Disabled</button>
          <div style="position:relative"><button type="button">Intercepted</button><span style="position:absolute;inset:0;z-index:2"></span></div>
          <input type="text" aria-label="Read only" readonly value="unchanged">
          <input type="password" name="password" value="must-not-be-retained">
          <button type="button" onclick="document.querySelector('#result').textContent='Clicked'; console.error('authorization: Bearer hidden-console-token'); fetch('${apiPath}')">Replay</button>
          <output id="result">Waiting</output>`,
      });
    });
    await browser.page.goto(origin, { waitUntil: 'domcontentloaded' });
    const ambiguous = browser.page.getByRole('button', { name: 'Duplicate' });
    await assert.rejects(ambiguous.click({ timeout: 250 }), /strict mode violation/i);
    assert.equal(await ambiguous.count(), 2);

    const disabled = browser.page.getByRole('button', { name: 'Disabled' });
    assert.equal(await disabled.isVisible(), true);
    assert.equal(await disabled.isEnabled(), false);
    await assert.rejects(disabled.click({ timeout: 200 }), /Timeout/i);

    const intercepted = browser.page.getByRole('button', { name: 'Intercepted' });
    assert.equal(await intercepted.isVisible(), true);
    assert.equal(await intercepted.isEnabled(), true);
    await assert.rejects(intercepted.click({ timeout: 250 }), /intercepts pointer events|Timeout/i);

    const readOnly = browser.page.getByRole('textbox', { name: 'Read only' });
    assert.equal(await readOnly.isEditable(), false);
    await assert.rejects(readOnly.fill('changed', { timeout: 250 }), /readonly|not editable|cannot be filled/i);

    const response = browser.page.waitForResponse(item => item.url().includes(apiPath) && item.status() === 422);
    await browser.page.getByRole('button', { name: 'Replay' }).click();
    await browser.page.getByText('Clicked').waitFor({ state: 'visible' });
    await response;
    const replay = browser.page.getByRole('button', { name: 'Replay' });
    const trace = await browser.captureFailure(new Error('focused helper evidence check'), {
      phase: 'static-html-test',
      action: { label: 'Click the replay control', locator: replay.toString(), targetLocator: replay },
    });
    assert.equal(trace, 'failure-trace.json');
    assert.equal(browser.diagnostics.httpFailures[0].status, 422);
    assert.equal(browser.diagnostics.httpFailures[0].body.includes('owned builder validation detail'), true);
    assert.equal(browser.diagnostics.httpFailures[0].body.includes('must-not-be-retained'), false);
    assert.equal(browser.diagnostics.console.some(item => item.text.includes('hidden-console-token')), false);
    assert((await readFile(join(evidence, 'first-failure.dom.txt'), 'utf8')).includes('Clicked'));
    const failure = JSON.parse(await readFile(join(evidence, 'first-failure.json'), 'utf8'));
    assert.equal(failure.controls.some(control => control.label === 'Replay'), true);
    assert.equal(failure.action.target.count, 1);
    assert.equal(failure.action.target.visible, true);
    assert.equal(failure.action.target.enabled, true);
    assert.equal(failure.controls.find(control => control.type === 'password').value, '[REDACTED]');
    assert.equal(JSON.stringify(failure).includes('must-not-be-retained'), false);
    assert((await stat(join(evidence, 'first-failure.png'))).size > 0);
    assert((await stat(join(evidence, 'failure-trace.json'))).size > 0);
    assert.equal((await readFile(join(evidence, 'failure-trace.json'), 'utf8')).includes('must-not-be-retained'), false);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true });
  }
});

test('fresh loopback no-auth failure retains a Playwright trace zip', async t => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-trace-test-'));
  let browser;
  try {
    try {
      browser = await launchBrowser({ evidence, appOrigins: ['http://127.0.0.1:38888'], noAuth: true });
    } catch (error) {
      if (/Executable doesn't exist|browserType\.launch:.*(?:not found|failed to launch)/i.test(String(error))) {
        t.skip(`Chromium is unavailable in this checkout: ${error.message}`);
        return;
      }
      throw error;
    }
    await browser.page.setContent('<button>Review</button>');
    const action = browser.page.getByRole('button', { name: 'Review' });
    const trace = await browser.captureFailure(new Error('controlled trace evidence check'), {
      action: { label: 'Review', locator: action.toString(), targetLocator: action },
    });
    assert.equal(trace, 'failure-trace.zip');
    assert((await stat(join(evidence, 'failure-trace.zip'))).size > 0);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
