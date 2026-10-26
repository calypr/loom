import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

export const SCOPE001_RESTRICTED_AUTH_PATIENT_ID = 'scope-001-auth-patient-allowed';
export const SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID = 'scope-001-auth-observation-allowed';
export const SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID = 'scope-001-auth-observation-blocked';
export const SCOPE001_ALLOWED_AUTH_RESOURCE_PATH = 'loom_dev_scope001-auth';
export const SCOPE001_BLOCKED_AUTH_RESOURCE_PATH = 'loom_dev_scope001-blocked';

const parseNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(line => line.trim())
  .map((line, index) => {
    try {
      return JSON.parse(line);
    } catch (error) {
      throw new Error(`Invalid JSON in ${path} at line ${index + 1}: ${error.message}`);
    }
  });

const assertNoAuthResourcePath = resources => {
  const pending = [...resources];
  while (pending.length > 0) {
    const value = pending.pop();
    if (Array.isArray(value)) {
      pending.push(...value);
      continue;
    }
    if (value && typeof value === 'object') {
      assert.equal(
        Object.hasOwn(value, 'auth_resource_path'),
        false,
        'FHIR fixture input must not encode the post-load auth_resource_path mutation',
      );
      pending.push(...Object.values(value));
    }
  }
};

/**
 * Read the literal SCOPE-001 restricted-auth FHIR fixture and derive its visible rows.
 * Both Observations are loaded from NDJSON; the blocked witness is denied only by
 * its post-load target-document authorization path, while its subject edge remains allowed.
 */
export const readScope001RestrictedAuthOracle = fixtureDir => {
  const patients = parseNDJSON(join(fixtureDir, 'Patient.ndjson'));
  const observations = parseNDJSON(join(fixtureDir, 'Observation.ndjson'));
  assertNoAuthResourcePath([...patients, ...observations]);

  assert.equal(patients.length, 1, 'fixture must contain exactly one Patient root');
  const patient = patients[0];
  assert.equal(patient.resourceType, 'Patient', 'fixture root must be a Patient');
  assert.equal(patient.id, SCOPE001_RESTRICTED_AUTH_PATIENT_ID, 'fixture must contain the named allowed Patient');

  assert.equal(observations.length, 2, 'fixture must contain exactly the allowed and blocked Observation witnesses');
  const observationsById = new Map();
  for (const observation of observations) {
    assert.equal(observation.resourceType, 'Observation', 'each related witness must be an Observation');
    assert.equal(typeof observation.id, 'string', 'each related Observation must have an ID');
    assert.equal(observationsById.has(observation.id), false, `duplicate Observation ID: ${observation.id}`);
    assert.equal(
      observation.subject?.reference,
      `Patient/${patient.id}`,
      `${observation.id} must link to the allowed Patient by subject.reference`,
    );
    assert.equal(Number.isFinite(observation.valueQuantity?.value), true, `${observation.id} must have a numeric valueQuantity.value`);
    observationsById.set(observation.id, observation);
  }

  assert.deepEqual(
    [...observationsById.keys()].sort(),
    [
      SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
      SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
    ].sort(),
    'fixture must contain exactly the two named Observation witnesses',
  );

  const allowedWitness = observationsById.get(SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID);
  const blockedWitness = observationsById.get(SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID);
  assert.equal(allowedWitness.valueQuantity.value, 111, 'allowed Observation witness must carry value 111');
  assert.equal(blockedWitness.valueQuantity.value, 999, 'blocked Observation witness must carry value 999');

  const witnesses = [allowedWitness, blockedWitness].map(observation => ({
    patientId: patient.id,
    observationId: observation.id,
    value: observation.valueQuantity.value,
  }));
  const blockedIDs = new Set([SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID]);
  const visibleWitnesses = witnesses.filter(witness => !blockedIDs.has(witness.observationId));
  const expectedVisibleRows = visibleWitnesses.map(witness => ({
    patientId: witness.patientId,
    observationId: witness.observationId,
    values: [witness.value],
  }));

  assert.deepEqual(expectedVisibleRows, [{
    patientId: SCOPE001_RESTRICTED_AUTH_PATIENT_ID,
    observationId: SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID,
    values: [111],
  }], 'blocked Observation must not contribute a visible row');

  return {
    patientId: patient.id,
    witnesses,
    blockedObservationId: SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID,
    blockedWitness: {
      patientId: patient.id,
      observationId: blockedWitness.id,
      value: blockedWitness.valueQuantity.value,
    },
    expectedVisibleRows,
  };
};

export const assertScope001RestrictedAuthVisibleRows = (actualRows, fixtureDir) => {
  const oracle = readScope001RestrictedAuthOracle(fixtureDir);
  assert.deepEqual(actualRows, oracle.expectedVisibleRows, 'visible rows must match the literal restricted-auth fixture oracle exactly');
  return oracle;
};

/** Verify the fixture-only post-load path split before the Builder assertion. */
const assertScope001LoadedAuthorizationShape = ({
  project,
  generation,
  patientCount,
  patient,
  observations,
  subjectEdges,
}) => {
  assert.equal(project, 'loom_dev_scope001-auth', 'authorization witness must use the exact isolated storage project');
  assert.equal(generation, 'scope001-auth-v1', 'authorization witness must use the exact isolated generation');
  assert.equal(patientCount, 1, 'authorization witness must contain exactly one scoped Patient');
  assert(patient && typeof patient._id === 'string', 'loaded Patient must include its exact Arango document identity');
  assert.equal(patient.id, SCOPE001_RESTRICTED_AUTH_PATIENT_ID);
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.project, project);
  assert.equal(patient.dataset_generation, generation);
  assert.equal(patient.auth_resource_path, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH);
  assert.equal(observations?.length, 2, 'both loaded allowed and blocked Observation witnesses must remain present');
  assert.equal(subjectEdges?.length, 2, 'both loaded Observation-to-Patient subject edges must remain present');

  const expectedPaths = new Map([
    [SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH],
    [SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID, SCOPE001_BLOCKED_AUTH_RESOURCE_PATH],
  ]);
  const observationById = new Map();
  for (const observation of observations) {
    assert.equal(expectedPaths.has(observation?.id), true, 'loaded Observation identity must be one of the literal two witnesses');
    assert.equal(observationById.has(observation.id), false, 'loaded Observation witness IDs must be unique');
    assert.equal(observation.resourceType, 'Observation');
    assert.equal(observation.project, project);
    assert.equal(observation.dataset_generation, generation);
    assert.equal(observation.payload?.subject?.reference, `Patient/${patient.id}`);
    assert.equal(Number.isFinite(observation.payload?.valueQuantity?.value), true,
      `${observation.id} must preserve its literal numeric witness`);
    assert.equal(observation.payload.valueQuantity.value,
      observation.id === SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID ? 111 : 999,
      `${observation.id} must preserve its exact allowed/blocked value`);
    assert.equal(observation.auth_resource_path, expectedPaths.get(observation.id),
      `${observation.id} must retain its exact expected authorization path`);
    observationById.set(observation.id, observation);
  }

  for (const observationId of expectedPaths.keys()) {
    const observation = observationById.get(observationId);
    assert(observation?._id, `loaded ${observationId} must include its Arango document identity`);
    const edges = subjectEdges.filter(edge => edge?._from === observation._id);
    assert.equal(edges.length, 1, `${observationId} must have exactly one scoped subject_Patient edge`);
    const [edge] = edges;
    assert.equal(edge._to, patient._id, `${observationId} must link to the exact Patient document`);
    assert.equal(edge.from_type, 'Observation');
    assert.equal(edge.to_type, 'Patient');
    assert.equal(edge.label, 'subject_Patient');
    assert.equal(edge.project, project);
    assert.equal(edge.dataset_generation, generation);
    assert.equal(edge.auth_resource_path, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
      `${observationId} subject edge must remain readable so the target-document guard is independently exercised`);
  }

  return {
    project,
    generation,
    patientId: patient.id,
    loadedObservationIds: [...observationById.keys()].sort(),
    readScopeAuthResourcePaths: [SCOPE001_ALLOWED_AUTH_RESOURCE_PATH],
    blockedAuthResourcePath: SCOPE001_BLOCKED_AUTH_RESOURCE_PATH,
    subjectEdgeAuthResourcePaths: [...new Set(subjectEdges.map(edge => edge.auth_resource_path))].sort(),
  };
};

export const assertScope001SeededAuthorizationWitness = (witness) => {
  const { observations, subjectEdges, patient, project, generation, patientCount } = witness ?? {};
  assert.equal(project, 'loom_dev_scope001-auth', 'authorization witness must use the exact isolated storage project');
  assert.equal(generation, 'scope001-auth-v1', 'authorization witness must use the exact isolated generation');
  assert.equal(patientCount, 1, 'authorization witness must contain exactly one scoped Patient');
  assert(patient && typeof patient._id === 'string', 'loaded Patient must include its exact Arango document identity');
  assert.equal(patient.id, SCOPE001_RESTRICTED_AUTH_PATIENT_ID);
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.project, project);
  assert.equal(patient.dataset_generation, generation);
  assert.equal(patient.auth_resource_path, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH);
  assert.equal(observations?.length, 2, 'seeded authorization witness must contain both Observation records');
  assert.equal(subjectEdges?.length, 2, 'seeded authorization witness must contain both subject edges');
  const observationById = new Map(observations.map(observation => [observation?.id, observation]));
  assert.equal(observationById.size, 2, 'seeded Observation witness IDs must be unique');
  for (const [id, value] of [
    [SCOPE001_RESTRICTED_AUTH_ALLOWED_OBSERVATION_ID, 111],
    [SCOPE001_RESTRICTED_AUTH_BLOCKED_OBSERVATION_ID, 999],
  ]) {
    const observation = observationById.get(id);
    assert(observation?._id, `seeded ${id} must include its Arango document identity`);
    assert.equal(observation.resourceType, 'Observation');
    assert.equal(observation.project, project);
    assert.equal(observation.dataset_generation, generation);
    assert.equal(observation.auth_resource_path, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH,
      `seeded ${id} must begin on the authorized source path`);
    assert.equal(observation.payload?.subject?.reference, `Patient/${patient.id}`);
    assert.equal(observation.payload?.valueQuantity?.value, value);
    const edges = subjectEdges.filter(edge => edge?._from === observation._id);
    assert.equal(edges.length, 1, `seeded ${id} must have exactly one subject edge`);
    const [edge] = edges;
    assert.equal(edge._to, patient._id);
    assert.equal(edge.from_type, 'Observation');
    assert.equal(edge.to_type, 'Patient');
    assert.equal(edge.label, 'subject_Patient');
    assert.equal(edge.project, project);
    assert.equal(edge.dataset_generation, generation);
    assert.equal(edge.auth_resource_path, SCOPE001_ALLOWED_AUTH_RESOURCE_PATH);
  }
  return { seededObservationCount: observations.length, seededSubjectEdgeCount: subjectEdges.length };
};

export const assertScope001LoadedAuthorizationWitness = witness => {
  const evidence = assertScope001LoadedAuthorizationShape(witness);
  assert.deepEqual(evidence.subjectEdgeAuthResourcePaths, [SCOPE001_ALLOWED_AUTH_RESOURCE_PATH],
    'the blocked test must be caused by the target document path, not by a denied subject edge');
  return evidence;
};
