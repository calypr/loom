import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from './cda-playwright-requests.mjs';
import { sanitizePayload } from './playwright-browser.mjs';

test('owned requests correlate sanitized responses and reject sibling explorer prefixes', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8188',
    ownedPathPrefix: '/api/v1/projects/loom_dev_cda_fhir/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned/authoring/v2/commands',
    method: () => 'POST',
    headers: () => ({ 'x-request-id': 'owned-request-1' }),
    postData: () => '{"commands":[]}',
    failure: () => null,
  };
  const sibling = {
    ...request,
    url: () => 'http://127.0.0.1:8188/api/v1/projects/loom_dev_cda_fhir/explorers/owned-copy/authoring/v2/commands',
  };
  page.emit('request', request);
  page.emit('request', sibling);
  assert.equal(report.nativeRequests.length, 1);
  assert.equal(report.nativeRequests[0].requestId, 'owned-request-1');
  assert.equal(report.nativeRequests[0].body.commands.length, 0);

  const response = {
    request: () => request,
    status: () => 503,
    headers: () => ({ 'x-request-id': 'owned-response-1' }),
    text: async () => '{"error":"stale draft","token":"do-not-retain"}',
  };
  const waitingForResponse = capture.waitFor(entry => entry.path.endsWith('/commands') && entry.status === 503, { timeout: 1000 });
  page.emit('response', response);
  const matched = await waitingForResponse;
  await capture.flush();

  assert.equal(matched, report.nativeRequests[0]);
  assert.equal(report.nativeRequests.length, 1);
  assert.equal(report.nativeRequests[0].status, 503);
  assert.equal(report.nativeRequests[0].serverRequestId, 'owned-response-1');
  assert.deepEqual(report.nativeRequests[0].response, { error: 'stale draft', token: '[REDACTED]' });
  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].kind, 'http');
  assert.equal(report.errors[0].status, 503);
  assert.equal(report.errors[0].requestId, 'owned-request-1');
  assert.equal(report.errors[0].browserRequestId, report.nativeRequests[0].browserRequestId);
  assert.equal(report.errors[0].method, 'POST');
  assert.equal(report.errors[0].path, report.nativeRequests[0].path);
  assert.deepEqual(report.errors[0].response, report.nativeRequests[0].response);
});

test('related expand choice responses are retained as sanitized diagnostics', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/related-expand-choices',
    method: () => 'POST',
    headers: () => ({}),
    postData: () => '{"selectionToken":"secret"}',
  };
  page.emit('request', request);
  const completed = capture.waitFor(entry => entry.path.endsWith('/related-expand-choices') && entry.status === 422);
  page.emit('response', {
    request: () => request,
    status: () => 422,
    headers: () => ({}),
    text: async () => '{"error":"selection is stale","authorization":"secret"}',
  });
  await completed;
  assert.deepEqual(report.nativeRequests[0].body, { selectionToken: '[REDACTED]' });
  assert.deepEqual(report.nativeRequests[0].response, { error: 'selection is stale', authorization: '[REDACTED]' });
  assert.deepEqual(report.errors[0].response, report.nativeRequests[0].response);
});

test('successful owned responses without diagnostic bodies flush without a request-tracker crash', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:30102',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/builder',
    method: () => 'POST', headers: () => ({}), postData: () => '{}',
  };
  page.emit('request', request);
  page.emit('response', {
    request: () => request, status: () => 200, headers: () => ({}),
    text: () => { throw new Error('successful command body should not be read'); },
  });
  await capture.flush();
  assert.deepEqual(report.errors, []);
  assert.deepEqual(report.nativeRequests[0].response, { bodyNotRead: true });
});

test('UI proxy requests retain private exact bodies while reports stay redacted', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const capture = captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8282', browserRequestOrigin: 'http://127.0.0.1:30102',
    appOrigins: ['http://127.0.0.1:30102', 'http://127.0.0.1:8282'],
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report,
  });
  const request = {
    url: () => 'http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned/authoring/v2/preview',
    method: () => 'POST', headers: () => ({}), postData: () => '{"snapshotToken":"exact-private-token"}',
  };
  page.emit('request', request);
  page.emit('response', { request: () => request, status: () => 200, headers: () => ({}), text: async () => '{"draftToken":"exact-private-response"}' });
  await capture.flush();
  const entry = report.nativeRequests[0];
  assert.deepEqual(capture.rawRequestBody(entry), { snapshotToken: 'exact-private-token' });
  assert.deepEqual(capture.rawResponseBody(entry), { draftToken: 'exact-private-response' });
  assert(!JSON.stringify(report).includes('exact-private-'));
});

test('captured public snapshot hashes and no-auth metadata stay inspectable after report sanitization', () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const snapshotToken = `sha256:${'b'.repeat(64)}`;
  captureCDARequests(page, {
    apiOrigin: 'http://127.0.0.1:8282',
    ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned',
    report,
  });
  const request = {
    url: () => 'http://127.0.0.1:8282/api/v1/projects/isolated/explorers/owned/authoring/v2/construction-proposals',
    method: () => 'POST',
    headers: () => ({ 'content-type': 'application/json' }),
    postData: () => JSON.stringify({ snapshotToken, access_token: snapshotToken }),
  };

  page.emit('request', request);
  const safeReport = sanitizePayload(report);

  assert.equal(safeReport.nativeRequests[0].body.snapshotToken, snapshotToken);
  assert.equal(safeReport.nativeRequests[0].body.access_token, '[REDACTED]');
  assert.equal(safeReport.nativeRequests[0].authorizationHeaderPresent, false);
});

test('only the known missing favicon is recorded as an incidental asset failure', () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  captureCDARequests(page, { apiOrigin: 'http://127.0.0.1:30102', ownedPathPrefix: '/api/v1/projects/isolated/explorers/owned', report });
  const emit = (url, value) => page.emit('console', { type: () => 'error', location: () => ({ url }), text: () => value });
  emit('http://127.0.0.1:30102/favicon.ico', 'Failed to load resource: the server responded with a status of 404 (Not Found)');
  emit('http://127.0.0.1:30102/api/v1/projects/isolated/explorers/owned', 'Failed to load resource: the server responded with a status of 404 (Not Found)');
  assert.deepEqual(report.assetFailures, [{ kind: 'console', url: 'http://127.0.0.1:30102/favicon.ico', status: 404 }]);
  assert.equal(report.errors.length, 1);
  assert.equal(report.errors[0].kind, 'console');
});
