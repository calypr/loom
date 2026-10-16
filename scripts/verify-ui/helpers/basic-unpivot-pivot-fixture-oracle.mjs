import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const BASIC_UNPIVOT_PIVOT_FIXTURE_DIR = fileURLToPath(
  new URL('../../../testdata/basic-unpivot-pivot/', import.meta.url),
);

const EXPECTED_PATIENTS = [
  {
    resourceType: 'Patient',
    id: 'reshape-patient-001',
    gender: 'female',
    birthDate: '1980-01-02',
  },
  {
    resourceType: 'Patient',
    id: 'reshape-patient-002',
    gender: 'male',
    birthDate: '1985-03-04',
  },
];

export const BASIC_UNPIVOT_PIVOT_EXPECTED = Object.freeze({
  sourceFields: Object.freeze(['id', 'gender', 'birthDate']),
  sourceColumnLabels: Object.freeze({
    patientID: 'Patient ID',
    gender: 'Gender',
    birthDate: 'Birth Date',
  }),
  unpivotKeys: Object.freeze({ gender: 'gender', birthDate: 'birthDate' }),
  patients: Object.freeze(EXPECTED_PATIENTS.map(patient => Object.freeze({ ...patient }))),
  initialRows: Object.freeze(EXPECTED_PATIENTS.map(patient => Object.freeze({
    patient_id: patient.id,
    gender: patient.gender,
    birth_date: patient.birthDate,
  }))),
  unpivotRows: Object.freeze(EXPECTED_PATIENTS.flatMap(patient => [
    Object.freeze({ patient_id: patient.id, field: 'gender', value: patient.gender }),
    Object.freeze({ patient_id: patient.id, field: 'birthDate', value: patient.birthDate }),
  ])),
  pivotRows: Object.freeze(EXPECTED_PATIENTS.map(patient => Object.freeze({
    patient_id: patient.id,
    gender: patient.gender,
    birth_date: patient.birthDate,
  }))),
});

const sha256 = contents => createHash('sha256').update(contents).digest('hex');

export function readBasicUnpivotPivotSeedManifest(fixtureDir = BASIC_UNPIVOT_PIVOT_FIXTURE_DIR) {
  const names = readdirSync(fixtureDir).filter(name => name.endsWith('.ndjson')).sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson'],
    'Basic Unpivot→Pivot fixture must contain only Patient and empty Observation NDJSON sources');
  return names.map(name => {
    const path = join(fixtureDir, name);
    const source = readFileSync(path, 'utf8');
    const sizeBytes = statSync(path).size;
    assert(sizeBytes <= 4096, `${name} must remain a small bounded fixture`);
    const records = source.split(/\r?\n/).filter(line => line.trim().length > 0);
    return { name, sizeBytes, recordCount: records.length, sha256: sha256(source) };
  });
}

export function readBasicUnpivotPivotFixture(fixtureDir = BASIC_UNPIVOT_PIVOT_FIXTURE_DIR) {
  const manifest = readBasicUnpivotPivotSeedManifest(fixtureDir);
  const patientEntry = manifest.find(file => file.name === 'Patient.ndjson');
  const observationEntry = manifest.find(file => file.name === 'Observation.ndjson');
  assert(patientEntry.sizeBytes > 0, 'Patient.ndjson must contain the independent source rows');
  assert.equal(observationEntry.sizeBytes, 0, 'Observation.ndjson must be empty for the Patient-only scenario');
  const source = readFileSync(join(fixtureDir, 'Patient.ndjson'), 'utf8');
  const patients = source.split(/\r?\n/).filter(line => line.trim().length > 0).map(line => JSON.parse(line));
  assert.deepEqual(patients, EXPECTED_PATIENTS,
    'Patient source must match the literal two-record oracle exactly');
  return { manifest, patients };
}

export function expectedBasicUnpivotPivotRows(patients) {
  const sorted = [...patients].sort((left, right) => left.id.localeCompare(right.id));
  return {
    initialRows: sorted.map(patient => ({
      patient_id: patient.id,
      gender: patient.gender,
      birth_date: patient.birthDate,
    })),
    unpivotRows: sorted.flatMap(patient => [
      { patient_id: patient.id, field: 'gender', value: patient.gender },
      { patient_id: patient.id, field: 'birthDate', value: patient.birthDate },
    ]),
    pivotRows: sorted.map(patient => ({
      patient_id: patient.id,
      gender: patient.gender,
      birth_date: patient.birthDate,
    })),
  };
}
