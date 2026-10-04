import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { createCDAPlaywrightControls } from './cda-playwright-controls.mjs';

const fixture = () => {
  const page = new EventEmitter();
  page.evaluate = async (...args) => args;
  page.waitForFunction = async (...args) => args;
  const browser = { page, diagnostics: { console: [], httpFailures: [], networkFailures: [] } };
  const report = { nativeRequests: [], errors: [] };
  const controls = createCDAPlaywrightControls({ browser, browserApiOrigin: 'http://127.0.0.1:30102', ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report });
  return { controls, browser, report };
};

test('browser result inspection accepts callbacks and forwards their argument', async () => {
  const { controls } = fixture();
  const read = selector => document.querySelector(selector)?.textContent;
  assert.deepEqual(await controls.evaluate(read, '[data-testid="result"]'), [read, '[data-testid="result"]']);
  assert.throws(() => controls.evaluate('document.body.innerText'), /read-only function/);
});

test('browser waits accept callbacks and default to a five-second observable wait', async () => {
  const { controls } = fixture();
  const predicate = selector => Boolean(document.querySelector(selector));
  assert.deepEqual(await controls.wait(predicate, '#ready'), [predicate, '#ready', { timeout: 5000 }]);
  assert.deepEqual(await controls.wait(predicate, '#ready', 1200), [predicate, '#ready', { timeout: 1200 }]);
  assert.throws(() => controls.wait('document.body'), /predicate callback/);
});

test('unexpected owned HTTP and network failures remain errors, including aborted requests', async () => {
  const { controls, browser, report } = fixture();
  browser.diagnostics.httpFailures.push({
    url: 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/commands',
    method: 'POST',
    status: 500,
    body: '{"error":"unexpected owned failure"}',
  });
  browser.diagnostics.networkFailures.push({
    url: 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/frame-source-options',
    method: 'GET',
    failure: 'net::ERR_ABORTED',
  });
  await controls.flush();
  assert.equal(report.errors.length, 2);
  assert.deepEqual(report.errors.map(error => error.kind), ['http', 'network']);
  assert.equal(report.errors[0].status, 500);
  assert.equal(report.errors[1].details, 'net::ERR_ABORTED');
});
