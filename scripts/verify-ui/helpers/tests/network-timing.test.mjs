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
