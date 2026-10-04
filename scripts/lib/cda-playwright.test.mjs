import assert from 'node:assert/strict';
import { test } from 'node:test';
import { browserEval, waitForBrowser } from './cda-playwright.mjs';

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
