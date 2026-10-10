import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import {
  builderCapabilitiesIdentityFromState,
  readBuilderCapabilitiesIdentity,
  watchConstructionCapabilitiesReadiness,
} from '../construction-capabilities-readiness.mjs';
import { recordPlaywrightTiming } from '../playwright-authoring-page.mjs';
import { createReport } from '../report.mjs';

const scope = {
  apiOrigin: 'http://127.0.0.1:30008',
  project: 'loom_dev_verify_caps',
  explorer: 'fresh-caps-explorer',
};
const expected = {
  outputId: 'output-final',
  snapshotToken: 'snapshot-final',
  draftVersion: 7,
  draftDigest: 'digest-final',
};
const capabilitiesPath = `/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/construction-capabilities`;
const request = ({ path = capabilitiesPath, origin = scope.apiOrigin, outputId = expected.outputId,
  snapshotToken = expected.snapshotToken, draftVersion = expected.draftVersion,
  draftDigest = expected.draftDigest, requestId = 'construction-capabilities-11111111-2222-4333-8444-555555555555' } = {}) => ({
  url: () => `${origin}${path}`,
  method: () => 'POST',
  headers: () => ({ 'x-request-id': requestId }),
  postData: () => JSON.stringify({ outputId, snapshotToken, expectedDraftVersion: draftVersion, expectedDraftDigest: draftDigest }),
  failure: () => ({ errorText: 'net::ERR_ABORTED' }),
});

const successfulTerminal = (page, item, status = 200) => {
  page.emit('response', { request: () => item, status: () => status });
  page.emit('requestfinished', item);
};

test('capabilities readiness waits for exact post-Apply identity, successful response, and requestfinished', async () => {
  const page = new EventEmitter();
  const waiter = watchConstructionCapabilitiesReadiness({ page, ...scope });
  waiter.markActionStarted();

  const priorVersion = request({ draftVersion: expected.draftVersion - 1 });
  page.emit('request', priorVersion);
  successfulTerminal(page, priorVersion);
  const wrongOutput = request({ outputId: 'output-other' });
  page.emit('request', wrongOutput);
  successfulTerminal(page, wrongOutput);
  const wrongSnapshot = request({ snapshotToken: 'snapshot-other' });
  page.emit('request', wrongSnapshot);
  successfulTerminal(page, wrongSnapshot);
  const wrongDigest = request({ draftDigest: 'digest-other' });
  page.emit('request', wrongDigest);
  successfulTerminal(page, wrongDigest);

  const exact = request();
  page.emit('request', exact);
  page.emit('response', { request: () => exact, status: () => 200 });
  const ready = waiter.waitFor(expected, { timeoutMs: 100 });
  page.emit('requestfinished', exact);
  const result = await ready;

  assert.deepEqual(result.requests.map(item => ({ requestId: item.requestId, responseStatus: item.responseStatus, terminalEvent: item.terminalEvent })), [{
    requestId: 'construction-capabilities-11111111-2222-4333-8444-555555555555',
    responseStatus: 200,
    terminalEvent: 'requestfinished',
  }]);
  waiter.dispose();
});

test('capabilities readiness rejects a failed exact request and does not count an earlier request', async () => {
  const page = new EventEmitter();
  const waiter = watchConstructionCapabilitiesReadiness({ page, ...scope });
  const earlier = request();
  page.emit('request', earlier);
  page.emit('response', { request: () => earlier, status: () => 200 });
  page.emit('requestfinished', earlier);
  waiter.markActionStarted();
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 5 }), /Timed out waiting for exact post-Apply capabilities/);

  const failed = request({ requestId: 'construction-capabilities-22222222-2222-4333-8444-555555555555' });
  page.emit('request', failed);
  page.emit('requestfailed', failed);
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 20 }), /Exact post-Apply capabilities request .* failed: net::ERR_ABORTED/);
  waiter.dispose();
});

test('capabilities readiness keeps a successful response pending until native requestfinished', async () => {
  const page = new EventEmitter();
  const waiter = watchConstructionCapabilitiesReadiness({ page, ...scope });
  waiter.markActionStarted();
  const exact = request();
  page.emit('request', exact);
  page.emit('response', { request: () => exact, status: () => 200 });
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 5 }), /Timed out waiting for exact post-Apply capabilities/);
  waiter.dispose();
});

test('capabilities readiness does not match terminal events from a different request object with identical identity', async () => {
  const page = new EventEmitter();
  const waiter = watchConstructionCapabilitiesReadiness({ page, ...scope });
  waiter.markActionStarted();
  const exact = request();
  const sameIdentityDifferentObject = request();
  assert.equal(exact.url(), sameIdentityDifferentObject.url());
  assert.deepEqual(exact.headers(), sameIdentityDifferentObject.headers());
  assert.equal(exact.postData(), sameIdentityDifferentObject.postData());
  page.emit('request', exact);
  successfulTerminal(page, sameIdentityDifferentObject);
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 5 }), /Timed out waiting for exact post-Apply capabilities/);
  waiter.dispose();
});

test('capabilities readiness ignores wrong route/origin and rejects non-success responses', async () => {
  const page = new EventEmitter();
  const waiter = watchConstructionCapabilitiesReadiness({ page, ...scope });
  waiter.markActionStarted();
  const wrongRoute = request({ path: capabilitiesPath.replace('/construction-capabilities', '/schema-fields') });
  page.emit('request', wrongRoute);
  successfulTerminal(page, wrongRoute);
  const wrongOrigin = request({ origin: 'http://127.0.0.1:8188' });
  page.emit('request', wrongOrigin);
  successfulTerminal(page, wrongOrigin);
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 5 }), /Timed out waiting for exact post-Apply capabilities/);

  const nonSuccess = request({ requestId: 'construction-capabilities-33333333-2222-4333-8444-555555555555' });
  page.emit('request', nonSuccess);
  page.emit('response', { request: () => nonSuccess, status: () => 503 });
  await assert.rejects(waiter.waitFor(expected, { timeoutMs: 20 }), /failed: HTTP 503/);
  waiter.dispose();
});

test('builder identity read is bound to the exact project, Explorer, output, and caller deadline', async () => {
  let capturedURL;
  let capturedSignal;
  const identity = await readBuilderCapabilitiesIdentity({
    apiOrigin: scope.apiOrigin,
    project: scope.project,
    explorer: scope.explorer,
    outputId: expected.outputId,
    timeoutMs: 500,
    fetchImpl: async (url, options) => {
      capturedURL = String(url);
      capturedSignal = options.signal;
      return {
        ok: true,
        status: 200,
        json: async () => ({
          catalog: { snapshotToken: expected.snapshotToken },
          draftVersion: expected.draftVersion,
          draftDigest: expected.draftDigest,
          workspace: { documents: [{ output: { id: expected.outputId } }] },
        }),
      };
    },
  });
  assert.equal(capturedURL, `${scope.apiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/builder`);
  assert.equal(capturedSignal.aborted, false);
  assert.deepEqual(identity, expected);
  assert.deepEqual(builderCapabilitiesIdentityFromState({
    catalog: { snapshotToken: expected.snapshotToken },
    draftVersion: expected.draftVersion,
    draftDigest: expected.draftDigest,
    workspace: { documents: [{ output: { id: expected.outputId } }] },
  }, { outputId: expected.outputId }), expected);
  await assert.rejects(readBuilderCapabilitiesIdentity({
    apiOrigin: scope.apiOrigin,
    project: scope.project,
    explorer: scope.explorer,
    outputId: 'output-not-in-builder',
    timeoutMs: 500,
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        catalog: { snapshotToken: expected.snapshotToken },
        draftVersion: expected.draftVersion,
        draftDigest: expected.draftDigest,
        workspace: { documents: [{ output: { id: expected.outputId } }] },
      }),
    }),
  }), /exactly one requested output/);
});

test('recordPlaywrightTiming passes only the remaining shared budget to readiness settlement', async () => {
  const report = createReport({ scenario: 'readiness-test', target: {} });
  const page = { waitForFunction: async () => new Promise(resolve => setTimeout(resolve, 15)) };
  const workflow = { action: async (_label, _locator, perform) => perform() };
  let settlementTimeout;

  await recordPlaywrightTiming(report, page, workflow, {
    name: 'Apply and settle capabilities',
    action: async () => new Promise(resolve => setTimeout(resolve, 15)),
    after: 'true',
    timeout: 100,
    budget: 100,
    settle: async ({ timeoutMs }) => { settlementTimeout = timeoutMs; },
  });

  assert.equal(report.actions[0].status, 'passed');
  assert.ok(settlementTimeout > 0 && settlementTimeout < 90,
    `settlement must receive the remaining deadline, not a fresh budget; got ${settlementTimeout}`);
  assert.ok(report.timings['Apply and settle capabilities'] <= 100);
});
