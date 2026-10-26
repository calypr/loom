import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

export const COHORT_GROUP_PIVOT_EXPECTED = Object.freeze({
  cohortLabel: 'Two fixture Patients',
  sourceFields: Object.freeze({ patientID: 'Patient ID', gender: 'Gender' }),
  groupRows: Object.freeze([
    Object.freeze({ 'Group label': 'Two fixture Patients', Gender: 'female', 'Row count': 1 }),
    Object.freeze({ 'Group label': 'Two fixture Patients', Gender: 'male', 'Row count': 1 }),
  ]),
  viewerColumns: Object.freeze(['Group label', 'female', 'male']),
  viewerRows: Object.freeze([
    Object.freeze({ 'Group label': 'Two fixture Patients', female: 1, male: 1 }),
  ]),
});

const EXPECTED_PATIENTS = Object.freeze([
  Object.freeze({ resourceType: 'Patient', id: 'reshape-patient-001', gender: 'female', birthDate: '1980-01-02' }),
  Object.freeze({ resourceType: 'Patient', id: 'reshape-patient-002', gender: 'male', birthDate: '1985-03-04' }),
]);

const sha256 = text => createHash('sha256').update(text).digest('hex');

export function readCohortGroupPivotFixture(fixtureDir) {
  const names = readdirSync(fixtureDir).filter(name => name.endsWith('.ndjson')).sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson'],
    'The Basic cohort Group→Pivot fixture must contain only Patient and Observation NDJSON sources.');

  const patientPath = join(fixtureDir, 'Patient.ndjson');
  const observationPath = join(fixtureDir, 'Observation.ndjson');
  const patientText = readFileSync(patientPath, 'utf8');
  const observationText = readFileSync(observationPath, 'utf8');
  assert.equal(statSync(observationPath).size, 0, 'The Patient cohort fixture must have no Observation rows.');
  const patients = patientText.split(/\r?\n/).filter(line => line.trim()).map(line => JSON.parse(line));
  assert.deepEqual(patients, EXPECTED_PATIENTS,
    'Patient.ndjson must match the independent literal ID, gender, and birth-date oracle exactly.');

  return {
    patients,
    patientIDs: EXPECTED_PATIENTS.map(patient => patient.id),
    groupRows: COHORT_GROUP_PIVOT_EXPECTED.groupRows.map(row => ({ ...row })),
    viewerColumns: [...COHORT_GROUP_PIVOT_EXPECTED.viewerColumns],
    viewerRows: COHORT_GROUP_PIVOT_EXPECTED.viewerRows.map(row => ({ ...row })),
    sourceHashes: {
      patient: sha256(patientText),
      observation: sha256(observationText),
    },
  };
}
