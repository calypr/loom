import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { flushRelatedUnpivotNativeRequests } from '../../workflows/verify-cda-related-unpivot-browser.mjs';

const apiOrigin = 'http://loom.test';
const ownedPathPrefix = '/api/v1/projects/loom_dev_cda_fhir/explorers/related-unpivot-test/authoring/v2';

function makeCapture() {
  const page = new EventEmitter();
  page.url = () => `${apiOrigin}/?project=loom_dev_cda_fhir&explorer=related-unpivot-test&mode=builder`;
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, { apiOrigin, ownedPathPrefix, report });
  const request = {
    url: () => `${apiOrigin}${ownedPathPrefix}/commands`,
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'related-unpivot-command-1' }),
    postData: () => JSON.stringify({ commandId: 'related-unpivot-command-1' }),
  };
  const response = {
    request: () => request,
    headers: () => ({ 'x-request-id': 'related-unpivot-command-1' }),
    status: () => 200,
    text: async () => JSON.stringify({ ok: true }),
  };
  return { page, report, capture, request, response };
}

test('Related Unpivot flush records exact native terminal chronology for completed requests', async () => {
  const { page, report, capture, request, response } = makeCapture();
  page.emit('request', request);
  page.emit('response', response);
  page.emit('requestfinished', request);

  const summary = await flushRelatedUnpivotNativeRequests(capture, report, 50);

  assert.deepEqual(summary, { timeoutMs: 50, total: 1, finished: 1, failed: 0, pending: 0 });
  assert.deepEqual(report.nativeRequests[0].nativeEventChronology.map(event => event.event), [
    'request', 'response', 'requestfinished',
  ]);
  assert.equal(report.nativeRequests[0].nativeEventChronology.at(-1).objectMatch, true);
  assert.equal(report.nativeRequestDrainEvidence, undefined);
});

test('Related Unpivot flush rejects an owned request without a native terminal event', async () => {
  const { page, report, capture, request } = makeCapture();
  page.emit('request', request);

  await assert.rejects(
    flushRelatedUnpivotNativeRequests(capture, report, 10),
    /Related Unpivot requests did not reach an exact native terminal event/,
  );

  assert.equal(report.nativeRequests[0].nativeEventChronology.some(event =>
    event.event === 'requestfinished' || event.event === 'requestfailed'), false);
  assert.equal(report.nativeRequestDrainEvidence.length, 1);
  assert.deepEqual(report.nativeRequestDrainEvidence[0].unresolvedRequests, [{
    index: 0,
    requestId: 'related-unpivot-command-1',
    browserRequestId: 'playwright-1',
    method: 'POST',
    path: `${ownedPathPrefix}/commands`,
  }]);
});

test('Related Unpivot flush rejects an empty native request ledger', async () => {
  const { report, capture } = makeCapture();

  await assert.rejects(
    flushRelatedUnpivotNativeRequests(capture, report, 50),
    /Related Unpivot lifecycle captured no owned native requests/,
  );

  assert.deepEqual(report.nativeRequests, []);
  assert.equal(report.nativeRequestDrainEvidence, undefined);
});

test('Related Unpivot flush rejects a terminal event from a different matching request object', async () => {
  const { page, report, capture, request } = makeCapture();
  const wrongRequest = {
    url: request.url,
    method: request.method,
    headers: request.headers,
    postData: request.postData,
  };
  assert.equal(wrongRequest.url(), request.url());
  assert.equal(wrongRequest.method(), request.method());
  assert.equal(wrongRequest.headers()['x-request-id'], request.headers()['x-request-id']);

  page.emit('request', request);
  page.emit('requestfinished', wrongRequest);

  await assert.rejects(
    flushRelatedUnpivotNativeRequests(capture, report, 10),
    /Related Unpivot requests did not reach an exact native terminal event/,
  );

  assert.deepEqual(report.nativeRequests[0].nativeEventChronology.map(event => event.event), ['request']);
  assert.equal(report.nativeRequests[0].requestId, 'related-unpivot-command-1');
  assert.equal(report.nativeRequestDrainEvidence.length, 1);
  const [correlationError] = report.errors;
  assert.equal(report.errors.length, 1);
  assert.deepEqual({ ...correlationError, observedAt: undefined }, {
    kind: 'request-capture-correlation',
    event: 'requestfinished',
    browserRequestId: null,
    method: 'POST',
    path: `${ownedPathPrefix}/commands`,
    observedAt: undefined,
    objectMatch: false,
    message: 'Playwright requestfinished request object did not match an exact captured request object',
  });
  assert(Number.isFinite(correlationError.observedAt));
});
