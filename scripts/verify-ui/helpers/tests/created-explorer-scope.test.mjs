import assert from 'node:assert/strict';
import test from 'node:test';
import { createdExplorerScope } from '../created-explorer-scope.mjs';
import { classifyNativeBrowserApiRequest } from '../native-browser-api-scope.mjs';

test('created Explorer scope follows the server ID when a long requested name is normalized', () => {
  const requestedName = 'qa-reshape-group-edit-before-related-column-7c32c06c-6530-43bc-97df-64bc58e3fa29';
  const response = {
    project: 'loom_dev_cda_fhir',
    explorerId: 'qa-reshape-group-edit-before-related-column-7c32c06c-6530-43bc-9',
  };

  const scope = createdExplorerScope(response.project, response);

  assert.notEqual(requestedName, response.explorerId);
  assert.equal(scope.explorerId, response.explorerId);
  assert.equal(scope.explorerRoot,
    '/api/v1/projects/loom_dev_cda_fhir/explorers/qa-reshape-group-edit-before-related-column-7c32c06c-6530-43bc-9');
  assert.equal(scope.authoringBase, `${scope.explorerRoot}/authoring/v2`);
  assert.equal(scope.explorerRoot.includes(requestedName), false,
    'Subsequent API paths must not reuse the unnormalized requested name');

  const browserScope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project: response.project,
    explorer: scope.explorerId,
    protectedExplorer: 'cda-builder-full-qa-1790440983382',
  };
  assert.equal(classifyNativeBrowserApiRequest(
    `${browserScope.uiOrigin}${scope.authoringBase}/builder`, browserScope,
  ).kind, 'capture');
  assert.equal(classifyNativeBrowserApiRequest(
    `${browserScope.uiOrigin}/api/v1/projects/${response.project}/explorers/${requestedName}/authoring/v2/builder`, browserScope,
  ).reason, 'outside-project-explorer-scope',
  'Native request classification must reject the original long requested name after creation');
});

test('created Explorer scope rejects missing IDs, wrong projects, and path-shaped IDs', () => {
  assert.throws(() => createdExplorerScope('loom_dev_cda_fhir', { project: 'loom_dev_cda_fhir' }),
    /valid server-assigned Explorer ID/);
  assert.throws(() => createdExplorerScope('loom_dev_cda_fhir', {
    project: 'another_project', explorerId: 'qa-group-edit',
  }), /match the requested project/);
  assert.throws(() => createdExplorerScope('loom_dev_cda_fhir', {
    project: 'loom_dev_cda_fhir', explorerId: 'qa/group-edit',
  }), /valid server-assigned Explorer ID/);
  assert.throws(() => createdExplorerScope('loom_dev_cda_fhir', {
    project: 'loom_dev_cda_fhir', explorerId: 'qa--group-edit',
  }), /valid server-assigned Explorer ID/);
  assert.throws(() => createdExplorerScope('loom_dev_cda_fhir', {
    project: 'loom_dev_cda_fhir', explorerId: 'qa-group-edit-',
  }), /valid server-assigned Explorer ID/);
});
