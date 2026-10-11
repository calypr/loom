import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const sourceRoot = resolve(fileURLToPath(new URL('../../..', import.meta.url)));
const fixtureDirectory = join(sourceRoot, 'testdata/basic-related-field-after-unpivot-gender');
const MAX_SOURCE_FILE_BYTES = 4096;

const expectedPatient = Object.freeze({
  resourceType: 'Patient',
  id: 'basic-gender-patient-001',
  gender: 'female',
});

const expectedSpecimen = Object.freeze({
  resourceType: 'Specimen',
  id: 'basic-gender-specimen-001',
  status: 'available',
  subject: Object.freeze({ reference: `Patient/${expectedPatient.id}` }),
});

const expectedRows = Object.freeze([
  Object.freeze({
    retainedSpecimenStatus: 'available',
    retainedPatientIDs: Object.freeze(['basic-gender-patient-001']),
    unpivotKey: 'Specimen ID',
    unpivotValue: 'basic-gender-specimen-001',
    patientGenderAll: Object.freeze(['female']),
  }),
]);

export function readBasicRelatedFieldAfterUnpivotSeedManifest(directory = fixtureDirectory) {
  const names = readdirSync(directory).filter(name => name.endsWith('.ndjson')).sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson', 'Specimen.ndjson'],
    'The exact Basic fixture seed manifest must contain only Patient, empty Observation, and Specimen NDJSON files');
  const manifest = names.map(name => {
    const path = join(directory, name);
    const source = readFileSync(path);
    const text = source.toString('utf8');
    return {
      name,
      path,
      sizeBytes: statSync(path).size,
      sha256: createHash('sha256').update(source).digest('hex'),
      recordCount: text.split(/\r?\n/).filter(line => line.trim().length > 0).length,
    };
  });
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 0 },
    { name: 'Patient.ndjson', recordCount: 1 },
    { name: 'Specimen.ndjson', recordCount: 1 },
  ], 'The Basic fixture seed manifest must have one Patient, no Observation rows, and one Specimen');
  assert.equal(manifest[0].sizeBytes, 0, 'Observation.ndjson must be empty for this fixture');
  return manifest;
}

function readFixture(resourceType) {
  const path = join(fixtureDirectory, `${resourceType}.ndjson`);
  const size = statSync(path).size;
  assert(size > 0 && size <= MAX_SOURCE_FILE_BYTES,
    `${resourceType}.ndjson must be a nonempty fixture of at most ${MAX_SOURCE_FILE_BYTES} bytes`);

  const lines = readFileSync(path, 'utf8').split(/\r?\n/).filter(line => line.trim().length > 0);
  assert.equal(lines.length, 1, `${resourceType}.ndjson must contain exactly one bounded source row`);
  return JSON.parse(lines[0]);
}

export function validateBasicRelatedFieldAfterUnpivotRecords({ patients, specimens }) {
  assert(Array.isArray(patients), 'Patient source rows must be an array');
  assert(Array.isArray(specimens), 'Specimen source rows must be an array');
  assert.equal(patients.length, 1, 'The Basic fixture must contain exactly one Patient');
  assert.equal(specimens.length, 1, 'The Basic fixture must contain exactly one Specimen');

  const [patient] = patients;
  const [specimen] = specimens;
  assert.equal(patient.resourceType, 'Patient', 'The source identity must be a Patient');
  assert.equal(patient.id, expectedPatient.id, 'The fixture Patient identity changed');
  assert(typeof patient.gender === 'string' && patient.gender.length > 0,
    'The exact Patient must have a nonempty string gender');
  assert.deepEqual(patient, expectedPatient, 'The Patient source differs from the literal Basic fixture');

  assert.equal(specimen.resourceType, 'Specimen', 'The source identity must be a Specimen');
  assert.equal(specimen.id, expectedSpecimen.id, 'The fixture Specimen identity changed');
  assert.equal(specimen.status, expectedSpecimen.status,
    'The Specimen status scalar must remain available after Unpivot');
  assert.deepEqual(specimen.subject, { reference: `Patient/${patient.id}` },
    'The Specimen must have exactly one direct subject reference to the selected Patient');
  assert.deepEqual(specimen, expectedSpecimen,
    'The Specimen source differs from the literal Basic fixture');

  return {
    patient: { id: patient.id, gender: patient.gender },
    specimen: { id: specimen.id, status: specimen.status, subjectReference: specimen.subject.reference },
    rows: expectedRows.map(row => ({
      retainedSpecimenStatus: row.retainedSpecimenStatus,
      retainedPatientIDs: [...row.retainedPatientIDs],
      unpivotKey: row.unpivotKey,
      unpivotValue: row.unpivotValue,
      patientGenderAll: [...row.patientGenderAll],
    })),
  };
}

export function loadBasicRelatedFieldAfterUnpivotFixtureOracle() {
  return validateBasicRelatedFieldAfterUnpivotRecords({
    patients: [readFixture('Patient')],
    specimens: [readFixture('Specimen')],
  });
}
