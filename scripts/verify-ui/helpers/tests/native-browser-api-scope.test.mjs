import assert from 'node:assert/strict';
import test from 'node:test';
import { classifyNativeBrowserApiRequest, isSameUiProxyResponse } from '../native-browser-api-scope.mjs';

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
