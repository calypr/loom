import assert from 'node:assert/strict';
import test from 'node:test';
import { createdExplorerScope } from '../created-explorer-scope.mjs';
import { classifyNativeBrowserApiRequest } from '../native-browser-api-scope.mjs';

test('created Explorer scope uses the observed truncated related ONE/ALL ID', () => {
  const requestedName = 'related-one-all-1791518358629-4d3b31ef-6980-450f-b4fe-703140335258';
  const response = {
    project: 'loom_dev_cda_fhir',
    explorerId: 'related-one-all-1791518358629-4d3b31ef-6980-450f-b4fe-7031403352',
  };

  assert.equal(requestedName.length, 66);
  assert.equal(response.explorerId.length, 64);
  assert.equal(response.explorerId, requestedName.slice(0, 64), 'The server response is the observed truncated ID');
  const scope = createdExplorerScope(response.project, response);

  assert.notEqual(requestedName, response.explorerId);
  assert.equal(scope.explorerId, response.explorerId);
  assert.equal(scope.explorerRoot,
    '/api/v1/projects/loom_dev_cda_fhir/explorers/related-one-all-1791518358629-4d3b31ef-6980-450f-b4fe-7031403352');
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

test('coded Pivot setup follows the server-assigned ExplorerSummary ID for later routes', () => {
  const project = 'loom_dev_cda_fhir';
  const cases = [
    {
      mode: 'integer',
      requestedName: 'qa-reshape-coded-pivot-integer-15abebd3-2038-4f39-964e-b46773b13918',
      explorerId: 'qa-reshape-coded-pivot-integer-15abebd3-2038-4f39-964e-b46773b13',
      updatedAt: '2026-10-09T04:32:24.798046221Z',
    },
    {
      mode: 'string',
      requestedName: 'qa-reshape-coded-pivot-string-453c9397-7af9-4370-834c-89ade12c27ce',
      explorerId: 'qa-reshape-coded-pivot-string-453c9397-7af9-4370-834c-89ade12c27',
      updatedAt: '2026-10-09T04:33:12.983779632Z',
    },
  ];

  for (const { mode, requestedName, explorerId, updatedAt } of cases) {
    const response = {
      project,
      explorerId,
      title: `CDA coded pivot ${mode} verification`,
      management: 'INTERACTIVE',
      updatedAt,
    };
    const scope = createdExplorerScope(project, response);

    assert.equal(requestedName.length > 64, true);
    assert.notEqual(requestedName, scope.explorerId);
    assert.equal(scope.explorerId, explorerId);
    assert.equal(scope.explorerRoot, `/api/v1/projects/${project}/explorers/${explorerId}`);
    assert.equal(scope.authoringBase, `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2`);
    assert.deepEqual([
      `${scope.authoringBase}/builder`,
      `${scope.explorerRoot}/selections`,
      `${scope.explorerRoot}/authoring/v2/frame-source-options`,
      `${scope.explorerRoot}/authoring/v2/semantic-inventory`,
      `${scope.explorerRoot}/authoring/v2/construction-proposals`,
      `${scope.explorerRoot}/authoring/v2/commands`,
      `${scope.explorerRoot}/authoring/v2/preview`,
    ], [
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/builder`,
      `/api/v1/projects/${project}/explorers/${explorerId}/selections`,
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/frame-source-options`,
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/semantic-inventory`,
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/construction-proposals`,
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/commands`,
      `/api/v1/projects/${project}/explorers/${explorerId}/authoring/v2/preview`,
    ]);
  }
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
