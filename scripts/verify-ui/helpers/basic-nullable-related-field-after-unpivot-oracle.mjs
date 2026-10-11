import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const sourceRoot = resolve(fileURLToPath(new URL('../../..', import.meta.url)));
const fixtureDirectory = join(sourceRoot, 'testdata/basic-related-field-after-unpivot-nullable');
const MAX_SOURCE_FILE_BYTES = 4096;
const linkedPatientID = 'basic-nullable-gender-patient-001';
const schemaPatientID = 'basic-nullable-gender-schema-patient-002';
const specimenID = 'basic-nullable-gender-specimen-001';

function readResourceRows(directory, resourceType, { allowEmpty = false } = {}) {
  const path = join(directory, `${resourceType}.ndjson`);
  const source = readFileSync(path);
  assert(source.length <= MAX_SOURCE_FILE_BYTES,
    `${resourceType}.ndjson must be at most ${MAX_SOURCE_FILE_BYTES} bytes`);
  const text = source.toString('utf8');
  const records = text.split(/\r?\n/).filter(line => line.trim().length > 0).map(line => JSON.parse(line));
  if (!allowEmpty) assert(records.length > 0, `${resourceType}.ndjson must contain source rows`);
  return {
    records,
    path,
    sizeBytes: statSync(path).size,
    sha256: createHash('sha256').update(source).digest('hex'),
  };
}

export function readBasicNullableRelatedFieldAfterUnpivotSeedManifest(directory = fixtureDirectory) {
  const names = readdirSync(directory).filter(name => name.endsWith('.ndjson')).sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson', 'Specimen.ndjson'],
    'The nullable Basic fixture must contain exactly Patient, empty Observation, and Specimen NDJSON files');
  const manifest = names.map(name => {
    const source = readFileSync(join(directory, name));
    return {
      name,
      sizeBytes: statSync(join(directory, name)).size,
      sha256: createHash('sha256').update(source).digest('hex'),
      recordCount: source.toString('utf8').split(/\r?\n/).filter(line => line.trim().length > 0).length,
    };
  });
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 0 },
    { name: 'Patient.ndjson', recordCount: 2 },
    { name: 'Specimen.ndjson', recordCount: 1 },
  ], 'The fixture must have two Patients, one Specimen, and no Observation rows');
  assert.equal(manifest[0].sizeBytes, 0, 'Observation.ndjson must be empty');
  return manifest;
}

export function validateBasicNullableRelatedFieldAfterUnpivotRecords({ patients, specimens }) {
  assert(Array.isArray(patients), 'Patient source rows must be an array');
  assert(Array.isArray(specimens), 'Specimen source rows must be an array');
  assert.equal(patients.length, 2, 'The fixture must have exactly two Patient source rows');
  assert.equal(specimens.length, 1, 'The fixture must have exactly one Specimen root row');

  const linkedPatients = patients.filter(patient => patient.id === linkedPatientID);
  const schemaPatients = patients.filter(patient => patient.id === schemaPatientID);
  const [specimen] = specimens;
  assert.equal(linkedPatients.length, 1, 'The Specimen must resolve to exactly one linked Patient');
  assert.equal(schemaPatients.length, 1, 'The unrelated schema witness Patient must be unique');
  const [patient] = linkedPatients;
  const [schemaPatient] = schemaPatients;

  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.id, linkedPatientID);
  assert.equal(Object.hasOwn(patient, 'gender'), false,
    'The linked Patient must omit gender; explicit null is a different source shape');
  assert.equal(schemaPatient.resourceType, 'Patient');
  assert.equal(schemaPatient.gender, 'female', 'The unlinked Patient keeps Patient.gender discoverable in the catalog');
  assert.equal(specimen.resourceType, 'Specimen');
  assert.equal(specimen.id, specimenID);
  assert.equal(specimen.status, 'available');
  assert.deepEqual(specimen.subject, { reference: `Patient/${linkedPatientID}` },
    'The only Specimen must link directly to the Patient whose gender is missing');

  return {
    patient: { id: patient.id, gender: null, genderPropertyPresent: false },
    schemaWitnessPatient: { id: schemaPatient.id, gender: schemaPatient.gender, linkedToRoot: false },
    specimen: { id: specimen.id, status: specimen.status, subjectReference: specimen.subject.reference },
    rows: [{
      retainedSpecimenStatus: specimen.status,
      retainedPatientIDs: [patient.id],
      unpivotKey: 'Specimen ID',
      unpivotValue: specimen.id,
      patientGenderAll: [null],
    }],
  };
}

export function loadBasicNullableRelatedFieldAfterUnpivotOracle(directory = fixtureDirectory) {
  const patients = readResourceRows(directory, 'Patient').records;
  const specimens = readResourceRows(directory, 'Specimen').records;
  const observations = readResourceRows(directory, 'Observation', { allowEmpty: true }).records;
  assert.equal(observations.length, 0, 'The independent Basic fixture must contain no Observation rows');
  return validateBasicNullableRelatedFieldAfterUnpivotRecords({ patients, specimens });
}
