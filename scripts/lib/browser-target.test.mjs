import test from 'node:test';
import assert from 'node:assert/strict';
import { findPageTarget } from './browser.mjs';

test('an explicitly owned about:blank page excludes Chrome new-tab targets', () => {
  const newTab = { id: 'newtab', type: 'page', url: 'chrome://newtab/' };
  const owned = { id: 'owned-blank', type: 'page', url: 'about:blank' };
  assert.equal(findPageTarget([newTab, owned], 'about:blank'), owned);
  assert.equal(findPageTarget([newTab], 'about:blank'), undefined);
});

test('legacy browser callers still attach to the first page target', () => {
  const worker = { id: 'worker', type: 'worker', url: 'about:blank' };
  const page = { id: 'page', type: 'page', url: 'chrome://newtab/' };
  assert.equal(findPageTarget([worker, page]), page);
});
