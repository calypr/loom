import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from './cda-playwright-requests.mjs';

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
  page.emit('response', response);
  await capture.flush();

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
  page.emit('response', {
    request: () => request,
    status: () => 422,
    headers: () => ({}),
    text: async () => '{"error":"selection is stale","authorization":"secret"}',
  });
  await capture.flush();
  assert.deepEqual(report.nativeRequests[0].body, { selectionToken: '[REDACTED]' });
  assert.deepEqual(report.nativeRequests[0].response, { error: 'selection is stale', authorization: '[REDACTED]' });
  assert.deepEqual(report.errors[0].response, report.nativeRequests[0].response);
});
