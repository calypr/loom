import assert from 'node:assert/strict';
import test from 'node:test';
import {
  MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN,
  MAX_CDA_STARTING_COLLECTION_WITNESSES,
  CdaStartingCollectionReplacementWitnessUnavailable,
  assertCdaStartingCollectionReplacementReread,
  bindCdaStartingCollectionReplacementSelectionRevisions,
  cdaStartingCollectionReplacementRereadQuery,
  cdaStartingCollectionReplacementScanQuery,
  prepareCdaStartingCollectionReplacementOracle,
} from '../cda-starting-collection-replacement-oracle.mjs';

const project = 'loom-cda-starting-collection-replacement';
const generation = 'cda-fhir-v1';
const patient = (key, id, overrides = {}) => ({
  _id: `Patient/${key}`,
  id,
  project,
  generation,
  resourceType: 'Patient',
  payloadResourceType: 'Patient',
  fieldPresent: true,
  fieldValue: id,
  ...overrides,
});
const observation = (key, id, overrides = {}) => ({
  _id: `Observation/${key}`,
  id,
  project,
  generation,
  resourceType: 'Observation',
  payloadResourceType: 'Observation',
  fieldPresent: true,
  fieldValue: id,
  ...overrides,
});
const edge = (key, from, to, overrides = {}) => ({
  _id: `fhir_edge/${key}`,
  _from: from,
  _to: to,
  project,
  generation,
  fromType: 'Observation',
  toType: 'Patient',
  label: 'subject_Patient',
  ...overrides,
});
const raw = (obs, patientDoc, relationship) => ({ observation: obs, patient: patientDoc, edge: relationship });
const scanRows = () => {
  const p1 = patient('patient-a', 'patient-a');
  const p2 = patient('patient-b', 'patient-b');
  const o1 = observation('observation-a', 'observation-a');
  const o2 = observation('observation-b', 'observation-b');
  const o3 = observation('observation-z', 'observation-z');
  return [
    raw(o3, p1, edge('edge-z', o3._id, p1._id)),
    raw(o2, p2, edge('edge-b', o2._id, p2._id)),
    raw(o1, p1, edge('edge-a', o1._id, p1._id)),
  ];
};

test('bounded query scopes and orders the raw Observation.subject_Patient to Patient witness', () => {
  const query = cdaStartingCollectionReplacementScanQuery({ project, generation });
  assert.match(query, /observation\.project == "loom-cda-starting-collection-replacement"/);
  assert.match(query, /observation\.dataset_generation == "cda-fhir-v1"/);
  assert.match(query, /observation\.payload\.resourceType == "Observation"/);
  assert.match(query, /edge\._from == observation\._id/);
  assert.match(query, /edge\.from_type == "Observation" AND edge\.to_type == "Patient"/);
  assert.match(query, /edge\.label == "subject_Patient"/);
  assert.match(query, /patient\.payload\.resourceType == "Patient"/);
  assert.match(query, /SORT observation\._id\s+LIMIT 2000/);
  assert.match(query, /SORT observation\._id, edge\._id\s+LIMIT 4000/);
  assert.match(query, /fieldPresent:HAS\(observation\.payload,"id"\)/);
  assert.match(query, /fieldPresent:HAS\(patient\.payload,"id"\)/);
  assert.equal(MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN, 2_000);
  assert.equal(MAX_CDA_STARTING_COLLECTION_WITNESSES, 4_000);
});

test('oracle chooses two sorted Patient rows and a deterministic Observation whose subject row is the first Patient', () => {
  const oracle = prepareCdaStartingCollectionReplacementOracle(scanRows(), { project, generation });
  assert.deepEqual(oracle.scope, { project, generation });
  assert.deepEqual(oracle.originalPatientIDs, ['patient-a', 'patient-b']);
  assert.deepEqual(oracle.originalPatientReferences, [
    { project, generation, resourceType: 'Patient', id: 'patient-a' },
    { project, generation, resourceType: 'Patient', id: 'patient-b' },
  ]);
  assert.equal(oracle.candidateObservationID, 'observation-a');
  assert.deepEqual(oracle.candidateObservationReference,
    { project, generation, resourceType: 'Observation', id: 'observation-a' });
  assert.equal(oracle.candidatePatientID, 'patient-a');
  assert.equal(oracle.witnesses.edge._from, 'Observation/observation-a');
  assert.equal(oracle.witnesses.edge._to, 'Patient/patient-a');
  assert.deepEqual(oracle.expectedGroupRows, {
    originalPatients: [{ id: 'patient-a', count: 1 }, { id: 'patient-b', count: 1 }],
    candidateObservationSubjectPatients: [{ id: 'patient-a', count: 1 }],
  });
  assert.equal(oracle.scanLimitBoundsReturnedRowsNotDatabaseScanCost, true);
});

test('exact reread binds document identities and validates the selected raw resources and edge', () => {
  const oracle = prepareCdaStartingCollectionReplacementOracle(scanRows(), { project, generation });
  const query = cdaStartingCollectionReplacementRereadQuery({
    project,
    generation,
    patientDocumentIDs: ['Patient/patient-a', 'Patient/patient-b'],
    candidateObservationDocumentID: 'Observation/observation-a',
    edgeDocumentID: 'fhir_edge/edge-a',
    candidatePatientDocumentID: 'Patient/patient-a',
  });
  assert.match(query, /patient\._id IN \["Patient\/patient-a","Patient\/patient-b"\]/);
  assert.match(query, /observation\._id == "Observation\/observation-a"/);
  assert.match(query, /edge\._id == "fhir_edge\/edge-a"/);
  assert.match(query, /edge\._to == "Patient\/patient-a"/);
  assert.match(query, /edge\.label == "subject_Patient"/);
  const expected = oracle.witnesses;
  assert.deepEqual(assertCdaStartingCollectionReplacementReread([{
    patients: [expected.originalPatients[1], expected.originalPatients[0]],
    candidateObservation: expected.candidateObservation,
    edge: expected.edge,
  }], oracle), {
    exact: true,
    patients: 2,
    candidateObservationID: 'observation-a',
    project,
    generation,
  });
  assert.throws(() => assertCdaStartingCollectionReplacementReread([{
    patients: expected.originalPatients,
    candidateObservation: expected.candidateObservation,
    edge: { ...expected.edge, _to: 'Patient/patient-b' },
  }], oracle), /terminate at the exact Patient document/);
});

test('revision binding carries exact resource selections, references, revision IDs and literal group rows', () => {
  const oracle = prepareCdaStartingCollectionReplacementOracle(scanRows(), { project, generation });
  const binding = bindCdaStartingCollectionReplacementSelectionRevisions(oracle, {
    patientRevisionId: 'selection-patient-r7',
    observationRevisionId: 'selection-observation-r3',
  });
  assert.deepEqual(binding, {
    scope: { project, generation },
    sources: {
      patient: {
        resourceType: 'Patient',
        revisionId: 'selection-patient-r7',
        references: oracle.originalPatientReferences,
        expectedGroupRows: [{ id: 'patient-a', count: 1 }, { id: 'patient-b', count: 1 }],
      },
      observation: {
        resourceType: 'Observation',
        revisionId: 'selection-observation-r3',
        references: [oracle.candidateObservationReference],
        expectedGroupRows: [{ id: 'patient-a', count: 1 }],
      },
    },
    relation: {
      label: 'subject_Patient',
      from: oracle.candidateObservationReference,
      to: oracle.candidatePatientReference,
      edgeDocumentID: 'fhir_edge/edge-a',
    },
  });
  assert.throws(() => bindCdaStartingCollectionReplacementSelectionRevisions(oracle, {
    patientRevisionId: '', observationRevisionId: 'selection-observation-r3',
  }), /patientRevisionId must be non-empty/);
});

test('oracle rejects out-of-scope, mismatched, duplicate and conflicting raw evidence', () => {
  const wrongScope = scanRows();
  wrongScope[0] = raw(wrongScope[0].observation,
    { ...wrongScope[0].patient, generation: 'other-generation' }, wrongScope[0].edge);
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(wrongScope, { project, generation }),
    /escaped the exact generation scope/);

  const wrongPayloadID = scanRows();
  wrongPayloadID[0] = raw(wrongPayloadID[0].observation,
    { ...wrongPayloadID[0].patient, fieldValue: 'different-patient' }, wrongPayloadID[0].edge);
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(wrongPayloadID, { project, generation }),
    /payload id must match/);

  const repeatedEdge = scanRows();
  repeatedEdge.push(repeatedEdge[0]);
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(repeatedEdge, { project, generation }),
    /repeated edge/);

  const conflictingSubject = scanRows();
  conflictingSubject.push(raw(conflictingSubject[0].observation, patient('patient-b', 'patient-b'),
    edge('edge-conflicting-subject', conflictingSubject[0].observation._id, 'Patient/patient-b')));
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(conflictingSubject, { project, generation }),
    /conflicting Patient subjects/);

  const duplicatePatientFHIRID = scanRows();
  duplicatePatientFHIRID[1] = raw(duplicatePatientFHIRID[1].observation,
    patient('patient-other-doc', 'patient-a'), edge('edge-other-doc', 'Observation/observation-b', 'Patient/patient-other-doc'));
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(duplicatePatientFHIRID, { project, generation }),
    /FHIR id maps to more than one raw document/);
});

test('oracle reports bounded witness unavailability when two distinct Patient subjects are absent', () => {
  const onePatient = scanRows().filter(row => row.patient.id === 'patient-a');
  assert.throws(() => prepareCdaStartingCollectionReplacementOracle(onePatient, { project, generation }), error => {
    assert(error instanceof CdaStartingCollectionReplacementWitnessUnavailable);
    assert.match(error.message, /cannot provide two distinct Patient subjects/);
    assert.deepEqual(error.evidence, {
      project,
      generation,
      scanLimitObservations: MAX_CDA_STARTING_COLLECTION_OBSERVATION_SCAN,
      witnessLimit: MAX_CDA_STARTING_COLLECTION_WITNESSES,
      returnedEdgeWitnesses: 2,
      distinctPatients: 1,
      requiredPatients: 2,
      reason: 'fewer than two distinct, scoped Patient documents are reachable through Observation.subject_Patient edges',
    });
    return true;
  });
});
