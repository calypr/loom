import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  assertScope001LoadedAuthorizationWitness,
  assertScope001RestrictedAuthVisibleRows,
  assertScope001SeededAuthorizationWitness,
  readScope001RestrictedAuthOracle,
  SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
} from '../scope-001-restricted-auth-oracle.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/scope-001-restricted-auth', import.meta.url));
const expectedRows = [{
  patientId: 'scope-001-auth-patient-allowed',
  observationId: 'scope-001-auth-observation-allowed',
  values: [111],
}];

test('SCOPE-001 restricted-auth oracle proves both linked witnesses and excludes the blocked ID', () => {
  const oracle = readScope001RestrictedAuthOracle(fixtureDir);
  assert.equal(oracle.patientId, 'scope-001-auth-patient-allowed');
  assert.deepEqual(oracle.witnesses, [
    {
      patientId: 'scope-001-auth-patient-allowed',
      observationId: 'scope-001-auth-observation-allowed',
      value: 111,
    },
    {
      patientId: 'scope-001-auth-patient-allowed',
      observationId: 'scope-001-auth-observation-blocked',
      value: 999,
    },
  ]);
  assert.deepEqual(oracle.blockedWitness, {
    patientId: 'scope-001-auth-patient-allowed',
    observationId: 'scope-001-auth-observation-blocked',
    value: 999,
  });
  assert.deepEqual(oracle.expectedVisibleRows, expectedRows);
  assert.deepEqual(oracle.expectedVisibleRows.flatMap(row => row.values), [111]);
  assert.equal(oracle.expectedVisibleRows.some(row => row.observationId === 'scope-001-auth-observation-blocked'), false);

  const observations = readFileSync(join(fixtureDir, 'Observation.ndjson'), 'utf8')
    .trim()
    .split(/\r?\n/)
    .map(line => JSON.parse(line));
  const occurrencesOf999 = observations
    .filter(observation => observation.valueQuantity?.value === 999)
    .map(observation => observation.id);
  assert.deepEqual(occurrencesOf999, ['scope-001-auth-observation-blocked']);
  assert.equal(readFileSync(join(fixtureDir, 'Patient.ndjson'), 'utf8').includes('auth_resource_path'), false);
  assert.equal(readFileSync(join(fixtureDir, 'Observation.ndjson'), 'utf8').includes('auth_resource_path'), false);
});

test('visible-row assertion rejects duplicate and extra rows', () => {
  assert.deepEqual(assertScope001RestrictedAuthVisibleRows(expectedRows, fixtureDir).expectedVisibleRows, expectedRows);
  assert.throws(
    () => assertScope001RestrictedAuthVisibleRows([...expectedRows, ...expectedRows], fixtureDir),
    /visible rows must match the literal restricted-auth fixture oracle exactly/,
  );
  assert.throws(
    () => assertScope001RestrictedAuthVisibleRows([...expectedRows, {
      patientId: 'scope-001-auth-patient-allowed',
      observationId: 'scope-001-auth-observation-blocked',
      values: [999],
    }], fixtureDir),
    /visible rows must match the literal restricted-auth fixture oracle exactly/,
  );
});

test('loaded auth witness keeps both paths in one project and generation with only the allowed read scope', () => {
  const project = 'loom_dev_scope001-auth';
  const generation = 'scope001-auth-v1';
  const patient = {
    _id: 'Patient/test-root', id: 'scope-001-auth-patient-allowed', resourceType: 'Patient',
    project, dataset_generation: generation, auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  };
  const observations = [
    {
      _id: 'Observation/allowed', id: 'scope-001-auth-observation-allowed', resourceType: 'Observation',
      project, dataset_generation: generation, auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
      payload: { subject: { reference: 'Patient/scope-001-auth-patient-allowed' }, valueQuantity: { value: 111 } },
    },
    {
      _id: 'Observation/blocked', id: 'scope-001-auth-observation-blocked', resourceType: 'Observation',
      project, dataset_generation: generation, auth_resource_path: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
      payload: { subject: { reference: 'Patient/scope-001-auth-patient-allowed' }, valueQuantity: { value: 999 } },
    },
  ];
  const subjectEdges = [
    {
      _from: 'Observation/allowed', _to: patient._id, from_type: 'Observation', to_type: 'Patient',
      label: 'subject_Patient', project, dataset_generation: generation,
      auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    },
    {
      _from: 'Observation/blocked', _to: patient._id, from_type: 'Observation', to_type: 'Patient',
      label: 'subject_Patient', project, dataset_generation: generation,
      auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
    },
  ];
  const evidence = assertScope001LoadedAuthorizationWitness({ project, generation, patientCount: 1, patient, observations, subjectEdges });
  assert.deepEqual(evidence, {
    project,
    generation,
    patientId: 'scope-001-auth-patient-allowed',
    loadedObservationIds: ['scope-001-auth-observation-allowed', 'scope-001-auth-observation-blocked'],
    readScopeAuthResourcePaths: ['loom_dev_scope001-auth'],
    blockedAuthResourcePath: 'loom_dev_scope001-blocked',
    subjectEdgeAuthResourcePaths: ['loom_dev_scope001-auth'],
  });
  const seeded = structuredClone(observations).map(observation => ({
    ...observation,
    auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
  }));
  assert.deepEqual(assertScope001SeededAuthorizationWitness({
    project, generation, patientCount: 1, patient, observations: seeded, subjectEdges,
  }), { seededObservationCount: 2, seededSubjectEdgeCount: 2 });
  assert.throws(() => assertScope001LoadedAuthorizationWitness({
    project, generation, patientCount: 1, patient, observations: observations.map((record, index) => index === 1
      ? { ...record, auth_resource_path: SCOPE001_ALLOWED_AUTH_RESOURCE_PATH } : record), subjectEdges,
  }), /exact expected authorization path/);
  assert.throws(() => assertScope001LoadedAuthorizationWitness({
    project, generation, patientCount: 1, patient, observations,
    subjectEdges: subjectEdges.map((edge, index) => index === 1
      ? { ...edge, auth_resource_path: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH } : edge),
  }), /target-document guard is independently exercised/);
  assert.throws(() => assertScope001LoadedAuthorizationWitness({
    project, generation, patientCount: 1, patient,
    observations: observations.slice(0, 1),
    subjectEdges: subjectEdges.slice(0, 1),
  }), /both loaded allowed and blocked Observation witnesses must remain present/);
});
