import test from 'node:test';
import assert from 'node:assert/strict';
import { correlateRequestFailure } from '../network-timing.mjs';

test('request failure timing links its start action and main-frame navigations in its interval', () => {
  const correlation = correlateRequestFailure({
    workflowStartedAt: 100,
    requestStartedAt: 140,
    failedAt: 190,
    action: { id: 'action-7', label: 'Append rows' },
    navigationSequenceAtStart: 3,
    navigations: [
      { id: 'navigation-3', sequence: 3, startedAt: 130, atMs: 30, url: 'http://loom.local/builder' },
      { id: 'navigation-4', sequence: 4, startedAt: 160, atMs: 60, url: 'http://loom.local/preview' },
      { id: 'navigation-5', sequence: 5, startedAt: 200, atMs: 100, url: 'http://loom.local/next' },
    ],
  });

  assert.deepEqual(correlation, {
    requestStartedMs: 40,
    failedAtMs: 90,
    durationMs: 50,
    action: { id: 'action-7', label: 'Append rows' },
    mainFrameNavigations: [
      { id: 'navigation-4', atMs: 60, url: 'http://loom.local/preview' },
    ],
  });
});

import { readFileSync } from 'node:fs';

test('the actual fixture request handler retains timing for request 450', () => {
  const source = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  const start = source.indexOf('    const onRequest = request => {');
  const end = source.indexOf('    const onFrameNavigated', start);
  assert.ok(start >= 0 && end > start);
  const run = new Function('requestMetadata', `
    const target = {}; const report = { target: {} };
    const capabilityRequests = new Map(); let requestSequence = 0;
    let sequence = 0; const playwrightRequestId = () => 'request-' + (++sequence);
    const belongsToTarget = () => true;
    const safeURL = () => 'http://loom.local/reconcile';
    const requestDiagnostic = () => ({ draftVersion: 14 });
    const workflowStartedAt = performance.now();
    const actionSnapshot = () => ({ id: 'action-1', label: 'Reload' });
    const navigationSequence = 0; const capabilityBinding = () => null;
    ${source.slice(start, end)}
    return onRequest;
  `);
  const metadata = new WeakMap(); const onRequest = run(metadata);
  let last;
  for (let i = 0; i < 450; i++) {
    last = { method: () => 'POST', resourceType: () => 'fetch', url: () => 'http://loom.local/reconcile' };
    onRequest(last);
  }
  assert.equal(metadata.get(last).requestId, 'request-450');
  assert.equal(metadata.get(last).requestDetails.draftVersion, 14);
  assert.equal(metadata.get(last).action.id, 'action-1');
});
