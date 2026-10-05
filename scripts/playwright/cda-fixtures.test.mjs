import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../lib/cda-playwright-requests.mjs';
import { classifyExpectedCdaCancellation } from './cda-fixtures.mjs';

test('same-reason cancellation reclassifies exact error copies appended after the fixture listener', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [{ kind: 'network', browserRequestId: 'playwright-other' }], network: [] };
  const requestFailures = new WeakMap();
  const trackers = new Set();
  const reason = 'A same-action replacement superseded this contributor lookup.';
  const proof = { action: 'Only records meeting a condition', query: 'same action and owned route' };
  let capture;
  let request;
  let capturedEntry;

  page.on('requestfailed', failedRequest => {
    capturedEntry = capture.byRequest.get(failedRequest);
    const failure = {
      method: failedRequest.method(),
      url: failedRequest.url(),
      requestId: 'contributor-request-1',
      playwrightRequestId: 'cda-request-1',
      errorText: 'net::ERR_ABORTED',
    };
    requestFailures.set(failedRequest, failure);
    report.network.push({ kind: 'network', browserRequestId: capturedEntry.browserRequestId });
    classifyExpectedCdaCancellation({ request: failedRequest, reason, proof, report, requestFailures, trackers });
  });

  capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  trackers.add(capture);
  request = {
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/related-expand-contributors',
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'contributor-request-1' }),
    postData: () => JSON.stringify({ outputId: 'out-owned', snapshotToken: 'sha256:owned' }),
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  };

  page.emit('request', request);
  capturedEntry = capture.byRequest.get(request);
  assert(capturedEntry?.browserRequestId);
  page.emit('requestfailed', request);
  assert.equal(report.errors.length, 2);
  const lateCapturedError = report.errors.find(error => error.browserRequestId === capturedEntry.browserRequestId);
  assert(lateCapturedError, 'the request-capture listener must append its error after the fixture listener');
  assert.equal(lateCapturedError.expected, undefined, 'the first classification runs before this late copy exists');

  const reconciled = classifyExpectedCdaCancellation({ request, reason, proof, report, requestFailures, trackers });
  assert.equal(reconciled.reason, reason);
  assert.equal(lateCapturedError.expected, true);
  assert.equal(lateCapturedError.expectedCancellation.browserRequestId, capturedEntry.browserRequestId);
  assert.equal(report.network[0].expected, true);
  assert.equal(report.errors.find(error => error.browserRequestId === 'playwright-other').expected, undefined);
  assert.equal(report.expectedCancellations.length, 1, 'same-reason reconciliation must not duplicate the cancellation ledger');
  await capture.flush();
});
