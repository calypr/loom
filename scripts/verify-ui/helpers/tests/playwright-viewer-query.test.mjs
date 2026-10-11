import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertViewerPatientRows,
  assertViewerPatientRowsInOrder,
  explorerRuntimePath,
  matchesViewerGenderFilterRequest,
  matchesViewerProjectRequest,
  matchesViewerOutputRequest,
  matchesViewerRequestBody,
} from '../../workflows/viewer-query.mjs';

const project = 'fixture-project';
const selector = { recipe: 'recipe-a', translationVersion: 'v3', output: 'patients' };
const body = { variables: { input: { projectId: project, selector } } };

test('Viewer fault ownership requires the exact project and published output selector', () => {
  assert.equal(matchesViewerRequestBody(body, { project, selector }), true);
  assert.equal(matchesViewerRequestBody(body, { project: 'other-project', selector }), false);
  assert.equal(matchesViewerRequestBody(body, { project, selector: { ...selector, recipe: 'other-recipe' } }), false);
  assert.equal(matchesViewerRequestBody(body, { project, selector: { ...selector, translationVersion: 'v2' } }), false);
  assert.equal(matchesViewerRequestBody(body, { project, selector: { ...selector, output: 'other-output' } }), false);
  assert.equal(matchesViewerRequestBody({ variables: { input: { projectId: project } } }, { project, selector }), false);
});

test('Viewer request matching canonicalizes the owned fixture project alias', () => {
  const origin = 'http://127.0.0.1:30008';
  const projectAlias = 'loom_dev_verify_mv39vec2-03e5e56';
  const canonicalProject = 'loom_dev_verify_mv39vec2/03e5e56';
  const canonicalBody = {
    variables: { input: { projectId: canonicalProject, selector } },
  };
  const request = (url, method, body) => ({
    url: () => url,
    method: () => method,
    postDataJSON: () => body,
  });
  const initialQuery = request(`${origin}/graphql/graph`, 'POST', canonicalBody);

  assert.equal(matchesViewerProjectRequest(initialQuery, { origin, project: projectAlias }), true,
    'the UI canonical project ID must match the fixture project alias');
  assert.equal(matchesViewerRequestBody(canonicalBody, { project: projectAlias, selector }), true,
    'output ownership must accept the UI canonical project ID');
  assert.equal(matchesViewerProjectRequest(
    request(`${origin}/graphql/graph`, 'POST', { variables: { input: { projectId: 'loom_dev_verify_mv39vec2/other' } } }),
    { origin, project: projectAlias },
  ), false, 'a different canonical project must remain outside the waiter scope');
  assert.equal(matchesViewerProjectRequest(
    request('http://127.0.0.1:30009/graphql/graph', 'POST', canonicalBody),
    { origin, project: projectAlias },
  ), false, 'the request must remain scoped to the UI origin');
});

test('Viewer row oracle rejects missing, duplicate, and unexpected identities', () => {
  assert.deepEqual(assertViewerPatientRows(['dev-patient-001', 'dev-patient-002'], [
    'dev-patient-001', 'dev-patient-002',
  ]), ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(assertViewerPatientRows(['dev-patient-001', 'dev-patient-002'], [
    'dev-patient-002', 'dev-patient-001',
  ]), ['dev-patient-001', 'dev-patient-002']);
  assert.throws(() => assertViewerPatientRows(['dev-patient-001'], [
    'dev-patient-001', 'dev-patient-002',
  ]), /exact independent fixture Patient identities/);
  assert.throws(() => assertViewerPatientRows(['dev-patient-001', 'dev-patient-001'], [
    'dev-patient-001', 'dev-patient-002',
  ]), /exact independent fixture Patient identities/);
  assert.throws(() => assertViewerPatientRows(['dev-patient-001', 'wrong-patient'], [
    'dev-patient-001', 'dev-patient-002',
  ]), /exact independent fixture Patient identities/);
});

test('Viewer sort oracle checks exact fixture order instead of only membership', () => {
  const ascending = ['dev-patient-001', 'dev-patient-002'];
  assert.deepEqual(assertViewerPatientRowsInOrder(ascending, ascending), ascending);
  assert.throws(() => assertViewerPatientRowsInOrder([...ascending].reverse(), ascending), /exact independent fixture Patient order/);
  assert.throws(() => assertViewerPatientRowsInOrder(['dev-patient-001'], ascending), /exact independent fixture Patient order/);
  assert.throws(() => assertViewerPatientRowsInOrder(['dev-patient-001', 'unexpected'], ascending), /exact independent fixture Patient order/);
});

test('Viewer output request matching requires the exact project, selector, visible sort column, and direction', () => {
  const origin = 'http://127.0.0.1:30008';
  const column = 'col-patient-id';
  const input = { projectId: project, selector, columns: [column], sort: { column, desc: false } };
  const request = (url, method, value) => ({
    url: () => url,
    method: () => method,
    postDataJSON: () => ({ variables: { input: value } }),
  });
  const matching = request(`${origin}/graphql/graph`, 'POST', input);
  const unsorted = request(`${origin}/graphql/graph`, 'POST', { projectId: project, selector, columns: [column] });
  const wrongDirection = request(`${origin}/graphql/graph`, 'POST', { ...input, sort: { column, desc: true } });
  const wrongColumn = request(`${origin}/graphql/graph`, 'POST', { ...input, sort: { column: 'col-other', desc: false } });
  const wrongProject = request(`${origin}/graphql/graph`, 'POST', { ...input, projectId: 'other-project' });
  const wrongSelector = request(`${origin}/graphql/graph`, 'POST', { ...input, selector: { ...selector, output: 'other-output' } });
  const wrongOrigin = request('http://127.0.0.1:30009/graphql/graph', 'POST', input);
  const wrongPath = request(`${origin}/graphql/other`, 'POST', input);
  const wrongMethod = request(`${origin}/graphql/graph`, 'GET', input);

  const expected = { origin, project, selector, sort: { column, desc: false } };
  assert.equal(matchesViewerOutputRequest(matching, expected), true);
  assert.equal(matchesViewerOutputRequest(unsorted, { origin, project, selector }), true);
  assert.equal(matchesViewerOutputRequest(matching, { origin, project, selector }), false);
  assert.equal(matchesViewerOutputRequest(wrongDirection, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongColumn, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongProject, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongSelector, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongOrigin, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongPath, expected), false);
  assert.equal(matchesViewerOutputRequest(wrongMethod, expected), false);
});

test('Viewer filter response matching scopes the exact Explorer Gender update', () => {
  const origin = 'http://127.0.0.1:30008';
  const path = '/api/v1/projects/fixture-project/explorers/fixture-explorer/authoring/v2/commands';
  const capturedRequest = {
    commandId: 'command-1',
    snapshotToken: 'snapshot-1',
    expectedDraftVersion: 3,
    commands: [{
      type: 'UPDATE_COLUMN',
      outputId: 'out-patients',
      column: 'col-gender',
      columnValue: {
        column: 'col-gender',
        label: 'Gender',
        logicalType: 'string',
        source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } },
        table: { visible: true, order: 1 },
        filter: { label: 'Gender' },
      },
    }],
  };
  const request = (url, method, body) => ({
    url: () => url,
    method: () => method,
    postDataJSON: () => body,
  });

  const matchingRequest = request(`${origin}${path}`, 'POST', capturedRequest);
  const otherProjectRequest = request(
    `${origin}/api/v1/projects/other/explorers/fixture-explorer/authoring/v2/commands`,
    'POST', capturedRequest,
  );
  const otherExplorerRequest = request(
    `${origin}/api/v1/projects/fixture-project/explorers/other-explorer/authoring/v2/commands`,
    'POST', capturedRequest,
  );
  const wrongOriginRequest = request(`http://127.0.0.1:30009${path}`, 'POST', capturedRequest);
  const getRequest = request(`${origin}${path}`, 'GET', capturedRequest);
  const clearedFilterRequest = request(`${origin}${path}`, 'POST', {
    ...capturedRequest,
    commands: [{
      ...capturedRequest.commands[0],
      columnValue: { ...capturedRequest.commands[0].columnValue, filter: undefined },
    }],
  });
  const combinedCommandRequest = request(`${origin}${path}`, 'POST', {
    ...capturedRequest,
    commands: [...capturedRequest.commands, { type: 'RENAME_TABLE' }],
  });

  assert.equal(matchesViewerGenderFilterRequest(matchingRequest, { origin, path }), true);
  assert.equal(matchesViewerGenderFilterRequest(otherProjectRequest, { origin, path }), false);
  assert.equal(matchesViewerGenderFilterRequest(otherExplorerRequest, { origin, path }), false);
  assert.equal(matchesViewerGenderFilterRequest(wrongOriginRequest, { origin, path }), false);
  assert.equal(matchesViewerGenderFilterRequest(getRequest, { origin, path }), false);
  assert.equal(matchesViewerGenderFilterRequest(clearedFilterRequest, { origin, path }), false);
  assert.equal(matchesViewerGenderFilterRequest(combinedCommandRequest, { origin, path }), false);
});

test('Viewer browser response paths use the canonical double-encoded project identity', () => {
  assert.equal(
    explorerRuntimePath('loom_dev_verify_run-1234', 'viewer-one'),
    '/api/v1/projects/loom_dev_verify_run%252F1234/explorers/viewer-one',
  );
});
