import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const expectedPatients = [
  { id: 'dev-patient-001', genderPresent: true, gender: 'female' },
  { id: 'dev-patient-002', genderPresent: false },
];

export const readBasicPatientCellExplainOracle = fixtureDir => {
  const patientPath = join(fixtureDir, 'Patient.ndjson');
  const patientBytes = readFileSync(patientPath);
  const patients = patientBytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  assert(patients.every(patient => patient.resourceType === 'Patient'), 'cell explanation oracle must contain only Patient resources');
  assert.deepEqual(patients.map(patient => ({
    id: patient.id,
    genderPresent: Object.hasOwn(patient, 'gender'),
    ...(Object.hasOwn(patient, 'gender') ? { gender: patient.gender } : {}),
  })), expectedPatients, 'Basic Patient fixture must preserve the exact female and missing-gender source records');

  return {
    patientPath,
    patientSHA256: createHash('sha256').update(patientBytes).digest('hex'),
    patients: expectedPatients.map(patient => ({ ...patient })),
    patientIDColumn: 'patient_id',
    patientIDSourcePath: 'id',
    genderColumn: 'gender',
    genderSourcePath: 'gender',
    explainedPatient: { id: expectedPatients[0].id, value: expectedPatients[0].gender },
  };
};
