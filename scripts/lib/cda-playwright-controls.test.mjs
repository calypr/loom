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
  return createCDAPlaywrightControls({ browser, browserApiOrigin: 'http://127.0.0.1:30102', ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report });
};

test('browser result inspection accepts callbacks and forwards their argument', async () => {
  const controls = fixture();
  const read = selector => document.querySelector(selector)?.textContent;
  assert.deepEqual(await controls.evaluate(read, '[data-testid="result"]'), [read, '[data-testid="result"]']);
  assert.throws(() => controls.evaluate('document.body.innerText'), /read-only function/);
});

test('browser waits accept callbacks and default to a five-second observable wait', async () => {
  const controls = fixture();
  const predicate = selector => Boolean(document.querySelector(selector));
  assert.deepEqual(await controls.wait(predicate, '#ready'), [predicate, '#ready', { timeout: 5000 }]);
  assert.deepEqual(await controls.wait(predicate, '#ready', 1200), [predicate, '#ready', { timeout: 1200 }]);
  assert.throws(() => controls.wait('document.body'), /predicate callback/);
});
