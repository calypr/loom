import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../lib/cda-playwright-requests.mjs';
import { classifyExpectedCdaCancellation } from './cda-fixtures.mjs';

test('late request-failure copies inherit exact cancellation evidence and leave other request IDs unexpected', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [], network: [] };
  const requestFailures = new WeakMap();
  const trackers = new Set();
  const reason = 'A same-action replacement superseded this contributor lookup.';
  const proof = { action: 'Only records meeting a condition', query: 'same action and owned route' };
  const url = 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/related-expand-contributors';
  let capture;
  let expectedRequest;

  page.on('requestfailed', failedRequest => {
    const capturedEntry = capture.byRequest.get(failedRequest);
    const failure = {
      method: failedRequest.method(),
      url: failedRequest.url(),
      requestId: 'contributor-request-shared',
      playwrightRequestId: `cda-request-${capturedEntry.browserRequestId}`,
      errorText: 'net::ERR_ABORTED',
    };
    requestFailures.set(failedRequest, failure);
    report.network.push({ kind: 'network', browserRequestId: capturedEntry.browserRequestId });
    if (failedRequest !== expectedRequest) return;
    classifyExpectedCdaCancellation({ request: failedRequest, reason, proof, report, requestFailures, trackers });
  });

  capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  trackers.add(capture);
  const makeRequest = () => ({
    url: () => url,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'contributor-request-shared' }),
    postData: () => JSON.stringify({ outputId: 'out-owned', snapshotToken: 'sha256:owned' }),
    failure: () => ({ errorText: 'net::ERR_ABORTED' }),
  });
  expectedRequest = makeRequest();
  page.emit('request', expectedRequest);
  const capturedEntry = capture.byRequest.get(expectedRequest);
  assert(capturedEntry?.browserRequestId);
  page.emit('requestfailed', expectedRequest);

  const lateCapturedError = report.errors.find(error => error.browserRequestId === capturedEntry.browserRequestId);
  assert(lateCapturedError, 'the request-capture listener must append its error after the fixture listener');
  assert.equal(lateCapturedError.expected, true, 'the late row must copy the exact request’s earlier expected classification');
  assert.equal(lateCapturedError.expectedCancellation.browserRequestId, capturedEntry.browserRequestId);
  assert.equal(lateCapturedError.expectedCancellation.reason, reason);

  // Exercise same-reason idempotency against an additional late duplicate from this exact request.
  const duplicateLateError = { kind: 'network', origin: capturedEntry.origin, path: capturedEntry.path,
    url: `${capturedEntry.origin}${capturedEntry.path}`, requestId: capturedEntry.requestId,
    browserRequestId: capturedEntry.browserRequestId, method: capturedEntry.method,
    startedAt: capturedEntry.startedAt, error: capturedEntry.failure };
  report.errors.push(duplicateLateError);
  const reconciled = classifyExpectedCdaCancellation({ request: expectedRequest, reason, proof, report, requestFailures, trackers });
  assert.equal(reconciled.reason, reason);
  assert.equal(duplicateLateError.expected, true);
  assert.equal(duplicateLateError.expectedCancellation.browserRequestId, capturedEntry.browserRequestId);
  assert.equal(report.network[0].expected, true);
  assert.equal(report.expectedCancellations.length, 1, 'same-reason reconciliation must not duplicate the cancellation ledger');

  // A matching path and request header do not make a second browser Request expected.
  const unexpectedRequest = makeRequest();
  page.emit('request', unexpectedRequest);
  const unexpectedEntry = capture.byRequest.get(unexpectedRequest);
  assert.notEqual(unexpectedEntry.browserRequestId, capturedEntry.browserRequestId);
  page.emit('requestfailed', unexpectedRequest);
  const unexpectedError = report.errors.find(error => error.browserRequestId === unexpectedEntry.browserRequestId);
  assert(unexpectedError, 'the unrelated native request failure must remain in the report');
  assert.equal(unexpectedError.expected, undefined, 'a different browser request ID must remain unexpected');
  assert.equal(unexpectedError.expectedCancellation, undefined);
  await capture.flush();
});
