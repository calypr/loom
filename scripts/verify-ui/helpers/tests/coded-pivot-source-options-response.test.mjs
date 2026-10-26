import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { captureCDARequests } from '../cda-playwright-requests.mjs';
import { assertCodedPivotOwnedSuccessResponse } from '../coded-pivot-native-evidence.mjs';

const origin = 'http://127.0.0.1:8188';
const explorerRoot = '/api/v1/projects/loom_dev_cda_fhir/explorers/owned';
const snapshotToken = `sha256:${'a'.repeat(64)}`;
const responseBody = {
  error: {
    code: 'SEMANTIC_INVENTORY_UNAVAILABLE',
    message: 'current generation lacks complete semantic inventory',
  },
};

const cases = [
  {
    label: 'frame-source-options',
    path: `${explorerRoot}/authoring/v2/frame-source-options`,
    request: { outputId: 'out-basic', snapshotToken, resourceType: 'Observation', limit: 50 },
  },
  {
    label: 'semantic-inventory',
    path: `${explorerRoot}/authoring/v2/semantic-inventory`,
    request: {
      outputId: 'out-basic', snapshotToken, sourceChoiceId: 'source-basic',
      rowRoot: 'Observation', limit: 50,
    },
  },
];

test('owned source-options and inventory 409 responses resolve with the current request-view API and retain literal bodies', async () => {
  for (const { label, path, request: body } of cases) {
    const page = new EventEmitter();
    page.url = () => `${origin}/builder`;
    const report = { nativeRequests: [], network: [], errors: [] };
    const capture = captureCDARequests(page, {
      apiOrigin: origin,
      ownedPathPrefix: explorerRoot,
      report,
      responsePaths: /frame-source-options|semantic-inventory/,
    });
    const requestId = `owned-${label}-409`;
    const request = {
      url: () => `${origin}${path}`,
      method: () => 'POST',
      resourceType: () => 'fetch',
      headers: () => ({ 'x-request-id': requestId }),
      postData: () => JSON.stringify(body),
      failure: () => null,
    };
    const waitForOwnedResponse = capture.waitForResponse(entry => {
      const observedRequest = capture.requestBody(entry);
      return entry.path === path && entry.method === 'POST' &&
        observedRequest?.outputId === body.outputId &&
        observedRequest?.snapshotToken === body.snapshotToken &&
        (label !== 'frame-source-options' || observedRequest?.resourceType === 'Observation') &&
        (label !== 'semantic-inventory' || observedRequest?.sourceChoiceId === 'source-basic') &&
        entry.networkComplete && entry.responseCaptureComplete && entry.responseBodyCaptured &&
        entry.completedAt !== undefined;
    }, { fromIndex: 0, timeoutMs: 500 });

    page.emit('request', request);
    page.emit('response', {
      request: () => request,
      status: () => 409,
      body: async () => Buffer.from(JSON.stringify(responseBody)),
    });
    page.emit('requestfinished', request);

    try {
      const entry = await waitForOwnedResponse;
      assert.equal(entry.origin, origin);
      assert.equal(entry.path, path);
      assert.equal(entry.method, 'POST');
      assert.equal(entry.status, 409);
      assert.equal(entry.networkComplete, true);
      assert.equal(entry.responseCaptureComplete, true);
      assert.equal(entry.responseBodyCaptured, true);
      assert.equal(typeof entry.completedAt, 'number');
      assert.deepEqual(capture.requestBody(entry), body);
      const observedBody = capture.responseBody(entry);
      assert.deepEqual(observedBody, responseBody);
      assert.throws(() => assertCodedPivotOwnedSuccessResponse(entry, label, observedBody), error =>
        error.message === `Owned ${label} response returned HTTP 409: ${JSON.stringify(responseBody)}`);
      assert.deepEqual(assertCodedPivotOwnedSuccessResponse({ status: 200 }, label, { entries: [], sources: [] }),
        { entries: [], sources: [] });
      assert.equal(report.errors.find(error => error.kind === 'http')?.status, 409);
    } finally {
      capture.stop();
    }
  }
});
