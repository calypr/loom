import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyNativeBrowserApiRequest,
  isSameUiProxyResponse,
  nativeRequestsHaveOwnedTransportOutcomes,
} from '../native-browser-api-scope.mjs';

const scope = {
  uiOrigin: 'http://127.0.0.1:30008',
  apiOrigin: 'http://127.0.0.1:8188',
  project: 'loom_dev_cda_fhir',
  explorer: 'qa-explorer-1',
  protectedExplorer: 'protected-explorer',
};
const previewPath = `/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/preview`;

test('captures same-origin UI-proxied requests only for the owned project and Explorer', () => {
  assert.equal(classifyNativeBrowserApiRequest(scope.uiOrigin + previewPath, scope).kind, 'capture');
  assert.equal(classifyNativeBrowserApiRequest(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers`, scope,
  ).kind, 'capture');
  assert.equal(classifyNativeBrowserApiRequest(`${scope.uiOrigin}/assets/builder.js`, scope).kind, 'ignore');
});

test('rejects direct API access, foreign origins, foreign projects, and foreign Explorers', () => {
  assert.equal(classifyNativeBrowserApiRequest(scope.apiOrigin + previewPath, scope).reason, 'direct-api-origin');
  assert.equal(classifyNativeBrowserApiRequest(`http://example.test${previewPath}`, scope).reason, 'unexpected-origin');
  assert.equal(classifyNativeBrowserApiRequest(
    `${scope.uiOrigin}/api/v1/projects/other-project/explorers/${scope.explorer}/authoring/v2/preview`, scope,
  ).reason, 'outside-project-explorer-scope');
  assert.equal(classifyNativeBrowserApiRequest(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/other-explorer/authoring/v2/preview`, scope,
  ).reason, 'outside-project-explorer-scope');
});

test('rejects protected Explorer traffic even when it arrives through the UI proxy', () => {
  const path = `/api/v1/projects/${scope.project}/explorers/${scope.protectedExplorer}/authoring/v2/preview`;
  assert.equal(classifyNativeBrowserApiRequest(scope.uiOrigin + path, scope).reason, 'protected-explorer');
});

test('binds decoded API evidence to the exact UI-proxy request URL', () => {
  const request = `${scope.uiOrigin}${previewPath}?outputId=output-1`;
  assert(isSameUiProxyResponse(request, request, scope));
  assert.equal(isSameUiProxyResponse(request, `${scope.apiOrigin}${previewPath}?outputId=output-1`, scope), false);
  assert.equal(isSameUiProxyResponse(request, `${scope.uiOrigin}${previewPath}?outputId=output-2`, scope), false);
});

test('final native transport gate accepts bound responses and only exact owned pre-response cancellations', () => {
  const response = {
    origin: scope.uiOrigin,
    path: previewPath,
    transportScope: 'owned-project-explorer',
    authorizationHeaderPresent: false,
    networkTerminal: true,
    terminalState: 'finished',
    bodyReadStatus: 'decoded',
    status: 200,
    responseBinding: {
      origin: scope.uiOrigin,
      path: previewPath,
      matchesCapturedUiProxyRequest: true,
    },
  };
  const cancellation = {
    origin: scope.uiOrigin,
    path: previewPath,
    transportScope: 'owned-project-explorer',
    authorizationHeaderPresent: false,
    expectedOwnerCancellation: { expected: true, networkTerminal: true, bodyReadStatus: 'failed' },
    networkTerminal: true,
    terminalState: 'failed',
    bodyReadStatus: 'failed',
    cancelled: true,
    loadingFailure: { errorText: 'net::ERR_ABORTED', canceled: true },
    loadingFailed: { errorText: 'net::ERR_ABORTED', canceled: true },
    requestCorrelationId: 'construction-choices-opaque',
    cdpRequestId: 'request-17',
    cdpRequestMatchCount: 1,
  };

  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([response], scope.uiOrigin), true);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([cancellation], scope.uiOrigin), true);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([response, cancellation], scope.uiOrigin), true);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([], scope.uiOrigin), false);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, expectedOwnerCancellation: undefined }], scope.uiOrigin), false,
    'a missing response is not accepted without classified owner retirement');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, cdpRequestId: undefined }], scope.uiOrigin), false,
    'a classified abort still needs unique CDP request identity');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, cdpRequestMatchCount: 2 }], scope.uiOrigin), false,
    'ambiguous CDP request identity remains fatal');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, responseHeadersAt: 10 }], scope.uiOrigin), false,
    'a response that began before failure cannot use the pre-response exception');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, status: 200 }], scope.uiOrigin), false,
    'an HTTP status prevents the pre-response exception');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, responseReceivedAt: 10 }], scope.uiOrigin), false,
    'a response event prevents the pre-response exception');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, responseBinding: {} }], scope.uiOrigin), false,
    'a partial response binding cannot be treated as pre-response cancellation');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, loadingFailed: undefined }], scope.uiOrigin), false,
    'the independent CDP loading-failed event is required');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, origin: scope.apiOrigin }], scope.uiOrigin), false);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, authorizationHeaderPresent: true }], scope.uiOrigin), false);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, transportScope: 'owned-project' }], scope.uiOrigin), false,
    'only an owned Explorer request can use the owner-retirement exception');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...response, responseBinding: { ...response.responseBinding, matchesCapturedUiProxyRequest: false } }], scope.uiOrigin), false);
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...response, bodyReadStatus: 'failed' }], scope.uiOrigin), false,
    'a response URL alone does not establish a completed response body');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...response, status: undefined }], scope.uiOrigin), false,
    'a response must have an HTTP status');
  assert.equal(nativeRequestsHaveOwnedTransportOutcomes([{ ...cancellation, requestCorrelationId: undefined }], scope.uiOrigin), false,
    'a pre-response cancellation needs its captured request correlation ID');
});
