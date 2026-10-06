import assert from 'node:assert/strict';
import test from 'node:test';
import { prepareCdaGroupJoinOracle } from './cda-current-draft-group-join-workflow.mjs';

test('subject.reference witness keeps overlapping Group counts 2 to 1 through Count distinct', () => {
  const rows = [
    { _id: 'Observation/1', id: '1', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/2', id: '2', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/3', id: '3', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/shared' },
    { _id: 'Observation/4', id: '4', project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1', resourceType: 'Observation', groupKey: 'Patient/left-only' },
  ];

  const oracle = prepareCdaGroupJoinOracle(rows);

  assert.equal(oracle.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.groupKeyFieldPath, 'subject.reference');
  assert.equal(oracle.memberships.sharedGroupKey, 'Patient/shared');
  assert.equal(oracle.memberships.leftOnlyGroupKey, 'Patient/left-only');
  assert.deepEqual(oracle.expectedLeftInitial, [['Patient/left-only', 1], ['Patient/shared', 2]]);
  assert.deepEqual(oracle.expectedRight, [['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeftDistinct, [['Patient/left-only', 1], ['Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedLeft, [
    ['Patient/left-only', 1, '—', '—'],
    ['Patient/shared', 2, 'Patient/shared', 1],
  ]);
  assert.deepEqual(oracle.expectedInner, [['Patient/shared', 2, 'Patient/shared', 1]]);
  assert.deepEqual(oracle.expectedDistinctInner, [['Patient/shared', 1, 'Patient/shared', 1]]);
});
