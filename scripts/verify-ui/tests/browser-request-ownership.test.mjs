import assert from 'node:assert/strict';
import test from 'node:test';
import { isApplicationBrowserRequest } from '../../lib/browser-request-ownership.mjs';

const origins = ['http://127.0.0.1:30008', 'http://127.0.0.1:8188'];
test('owned UI assets and API requests remain monitored', () => {
  assert(isApplicationBrowserRequest(origins[0] + '/src/main.tsx', {}, origins));
  assert(isApplicationBrowserRequest(origins[1] + '/api/v1/projects/qa/preview', {}, origins));
});
test('Chrome background documents are outside application ownership', () => {
  assert.equal(isApplicationBrowserRequest('https://www.google.com/one-google-bar', { url: 'chrome://newtab' }, origins), false);
});
test('external resources initiated by the app remain monitored', () => {
  assert(isApplicationBrowserRequest('https://cdn.example.test/module.js', { stack: { callFrames: [], parent: { callFrames: [{ url: origins[0] + '/src/main.tsx' }] } } }, origins));
  assert(isApplicationBrowserRequest('https://cdn.example.test/style.css', { url: origins[0] + '/' }, origins));
});
