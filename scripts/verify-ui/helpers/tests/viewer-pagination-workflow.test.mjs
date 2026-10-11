import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertViewerPaginationIDs,
  matchesViewerPaginationRequest,
  viewerPaginationPartitionMatches,
} from '../../workflows/viewer-pagination.mjs';

const expectedIDs = [
  'page-patient-001',
  'page-patient-002',
  'page-patient-003',
  'page-patient-004',
  'page-patient-005',
  'page-patient-006',
];

const request = ({ method = 'POST', pathname = '/graphql/graph', body } = {}) => ({
  method: () => method,
  url: () => `http://127.0.0.1:30008${pathname}`,
  postDataJSON: () => body,
});

const pageRequest = ({ project = 'owned-project', first = 5, after, selector = {
  recipe: 'owned-recipe', translationVersion: 'owned-version', output: 'owned-output',
}, sort } = {}) => request({ body: { variables: { input: {
  projectId: project,
  selector,
  first,
  ...(sort === undefined ? {} : { sort }),
  ...(after === undefined ? {} : { after }),
} } } });

test('Viewer pagination page identity checks accept only unique independent fixture Patients', () => {
  assert.deepEqual(assertViewerPaginationIDs(expectedIDs.slice(0, 5), expectedIDs, 5), expectedIDs.slice(0, 5));
  assert.deepEqual(assertViewerPaginationIDs(expectedIDs.slice(5), expectedIDs, 1), expectedIDs.slice(5));
  assert.throws(() => assertViewerPaginationIDs(['page-patient-001', 'page-patient-001'], expectedIDs, 2), /must not repeat/);
  assert.throws(() => assertViewerPaginationIDs(['patient-outside-fixture'], expectedIDs, 1), /only independent fixture/);
});

test('Viewer pagination pages form a disjoint union of the six literal Patient identities', () => {
  assert.equal(viewerPaginationPartitionMatches(expectedIDs.slice(0, 5), expectedIDs.slice(5), expectedIDs), true);
  assert.equal(viewerPaginationPartitionMatches(expectedIDs.slice(0, 5), [expectedIDs[4]], expectedIDs), false);
  assert.equal(viewerPaginationPartitionMatches(expectedIDs.slice(0, 4), expectedIDs.slice(5), expectedIDs), false);
});

test('Viewer page request matching binds project, exact output selector, page size, and cursor', () => {
  const selector = { recipe: 'owned-recipe', translationVersion: 'owned-version', output: 'owned-output' };
  const firstPage = pageRequest({ selector });
  const secondPage = pageRequest({ selector, after: 'cursor-for-page-two' });
  const options = { origin: 'http://127.0.0.1:30008', project: 'owned-project', selector, first: 5 };
  assert.equal(matchesViewerPaginationRequest(firstPage, options), true);
  assert.equal(matchesViewerPaginationRequest(secondPage, { ...options, after: 'cursor-for-page-two' }), true);
  assert.equal(matchesViewerPaginationRequest(secondPage, options), false);
  assert.equal(matchesViewerPaginationRequest(pageRequest({ project: 'another-project', selector }), options), false);
  assert.equal(matchesViewerPaginationRequest(pageRequest({ selector: { ...selector, output: 'other-output' } }), options), false);
  assert.equal(matchesViewerPaginationRequest(pageRequest({ first: 20, selector }), options), false);
  assert.equal(matchesViewerPaginationRequest(pageRequest({ selector, sort: { column: 'Patient ID', desc: false } }), options), false);
  assert.equal(matchesViewerPaginationRequest(request({ method: 'GET', body: firstPage.postDataJSON() }), options), false);
  assert.equal(matchesViewerPaginationRequest(request({ pathname: '/graphql/other', body: firstPage.postDataJSON() }), options), false);
});
