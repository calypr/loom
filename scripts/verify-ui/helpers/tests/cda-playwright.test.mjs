import assert from 'node:assert/strict';
import { test } from 'node:test';
import { browserEval, waitForBrowser } from '../cda-playwright.mjs';
import { readProposalPreviewDocument } from '../../workflows/verify-cda-related-source-after-pivot-browser.mjs';

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

test('browser inspection allows local checked/value declarations and read comparisons', async () => {
  const page = {
    async evaluate(callback, args) { return callback(args); },
    async waitForFunction(callback, args) { return callback(args); },
  };
  const readState = ({ node, expected }) => {
    const checked = node.checked;
    const value = node.value;
    return checked === true && value === expected;
  };

  assert.equal(await browserEval(page, readState, { node: { checked: true, value: 'selected' }, expected: 'selected' }), true);
  await waitForBrowser(page, readState, { node: { checked: true, value: 'selected' }, expected: 'selected' });
  for (const source of [
    'const  checked = args.node.checked; return checked;',
    'const\n  checked = args.node.checked; return checked;',
    'var first = 1, checked = args.node.checked; return first === 1 && checked;',
    'const [checked] = args.values; return checked;',
  ]) {
    const predicate = Function('args', source);
    await waitForBrowser(page, predicate, { node: { checked: true }, values: [true] });
  }
});

test('native Pivot preview inspection callback passes the read-only browser guard', async () => {
  let forwarded;
  const page = {
    async evaluate(callback, args) {
      forwarded = { callback, args };
      return { status: 'ready' };
    },
  };

  assert.deepEqual(await browserEval(page, readProposalPreviewDocument), { status: 'ready' });
  assert.equal(forwarded.callback, readProposalPreviewDocument);
  assert.deepEqual(forwarded.args, []);
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
  await assert.rejects(waitForBrowser(page, ({ node }) => { node.checked = true; return true; }, { node: {} }), /inspect results only/);
  await assert.rejects(waitForBrowser(page, ({ node }) => { node.value = 'false pass'; return true; }, { node: {} }), /inspect results only/);
  await assert.rejects(browserEval(page, ({ node, property }) => { node[property] = 'false pass'; return true; }, { node: {}, property: 'checked' }), /inspect results only/);
  await assert.rejects(browserEval(page, ({ node }) => { node.onclick = () => {}; return true; }, { node: {} }), /inspect results only/);
  await assert.rejects(browserEval(page, ({ node }) => node.addEventListener('click', () => {}), { node: {} }), /inspect results only/);
  await assert.rejects(browserEval(page, ({ node }) => node.onClick(), { node: {} }), /inspect results only/);
  await assert.rejects(browserEval(page, () => { window.location = '/changed'; return true; }), /inspect results only/);
  await assert.rejects(browserEval(page, () => { location = '/changed'; return true; }), /inspect results only/);
  await assert.rejects(browserEval(page, () => { location.href = '/changed'; return true; }), /inspect results only/);
  await assert.rejects(browserEval(page, () => { const doc = document; doc.location.href = '/changed'; return true; }), /inspect results only/);
  assert.equal(evaluated, false, 'Rejected inspections must never reach Playwright evaluate.');
});

test('browser waits reject source strings and use observable Playwright conditions', async () => {
  let waited = false;
  const page = { async waitForFunction() { waited = true; } };
  await assert.rejects(waitForBrowser(page, 'document.readyState === "complete"'), /function callback/);
  assert.equal(waited, false, 'Rejected waits must never reach Playwright waitForFunction.');
});
