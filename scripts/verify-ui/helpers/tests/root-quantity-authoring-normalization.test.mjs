import assert from 'node:assert/strict';
import test from 'node:test';
import { authoringRequestsFromNative } from '../../workflows/root-quantity-pivot-workflow.mjs';

test('category discovery native capture becomes a fully identified authoring request', () => {
  const capturedRequest = {
    requestId: 'root-quantity-request-17',
    browserRequestId: 'playwright-17',
    serverRequestId: 'server-request-17',
    path: '/api/v1/projects/loom_dev_cda_fhir/explorers/fresh-explorer/authoring/v2/construction-category-discoveries',
    origin: 'http://127.0.0.1:8188',
    method: 'POST',
    startedAt: 12_000,
    completedAt: 12_087,
    status: 200,
    body: {
      expectedDraftVersion: 4,
      expectedDraftDigest: 'draft-sha256',
      outputId: 'out_root',
      receiptId: 'receipt_pivot',
    },
    response: {
      draftVersion: 4,
      draftDigest: 'draft-sha256',
      outputId: 'out_root',
      receiptId: 'receipt_pivot',
      rowCount: 2,
    },
  };
  const rawRequestBody = { ...capturedRequest.body, candidateConstruction: { version: 1, steps: [] } };
  const rawResponseBody = { ...capturedRequest.response, categories: [{ key: { kind: 'MISSING' } }, { key: { kind: 'STRING', string: 'd' } }] };
  const accessorCalls = [];
  const capture = {
    rawRequestBody(request) {
      accessorCalls.push(['request', request]);
      return rawRequestBody;
    },
    rawResponseBody(request) {
      accessorCalls.push(['response', request]);
      return rawResponseBody;
    },
  };

  const entries = authoringRequestsFromNative([
    capturedRequest,
    { path: '/api/v1/projects/loom_dev_cda_fhir/explorers/fresh-explorer/authoring/v2/builder', status: 200 },
  ], capture);

  assert.equal(entries.length, 1, 'the category-discovery capture is selected and unrelated builder GET is excluded');
  const [entry] = entries;
  assert.equal(entry.endpoint, 'construction-category-discoveries');
  assert.equal(entry.pathname, capturedRequest.path);
  assert.equal(entry.url, `${capturedRequest.origin}${capturedRequest.path}`);
  assert.equal(entry.method, 'POST');
  assert.equal(entry.status, 200);
  assert.equal(entry.requestId, 'root-quantity-request-17');
  assert.equal(entry.browserRequestId, 'playwright-17');
  assert.equal(entry.serverRequestId, 'server-request-17');
  assert.equal(entry.requestStartedAtMs, 12_000);
  assert.equal(entry.responseFinishedAtMs, 12_087);
  assert.equal(entry.durationMs, 87);
  assert.equal(entry.requestDraftVersion, 4);
  assert.equal(entry.requestDraftDigest, 'draft-sha256');
  assert.equal(entry.requestOutputId, 'out_root');
  assert.equal(entry.requestReceiptId, 'receipt_pivot');
  assert.equal(entry.responseDraftVersion, 4);
  assert.equal(entry.responseDraftDigest, 'draft-sha256');
  assert.equal(entry.responseReceiptId, 'receipt_pivot');
  assert.equal(entry.responseOutputId, 'out_root');
  assert.equal(entry.responseRowCount, 2);
  assert.deepEqual(entry.body, rawRequestBody);
  assert.deepEqual(entry.response, rawResponseBody);
  assert.deepEqual(accessorCalls, [['request', capturedRequest], ['response', capturedRequest]]);
});
