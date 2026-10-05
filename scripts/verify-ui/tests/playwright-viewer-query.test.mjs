import test from 'node:test';
import assert from 'node:assert/strict';
import { assertViewerPatientRows, matchesViewerRequestBody } from '../viewer-query.mjs';

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

test('Viewer row oracle rejects missing, duplicate, and unexpected identities', () => {
  assert.deepEqual(assertViewerPatientRows(['dev-patient-001', 'dev-patient-002'], [
    'dev-patient-001', 'dev-patient-002',
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
