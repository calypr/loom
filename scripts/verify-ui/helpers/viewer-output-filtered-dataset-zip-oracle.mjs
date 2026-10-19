import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { readNDJSONResourceIdentityOracle } from './ndjson-resource-oracle.mjs';

const EXPECTED_PATIENTS = [
  {
    id: 'dev-patient-001',
    gender: 'female',
    genderPresent: true,
    nameFamily: ['Example', 'Example-Smith'],
  },
  {
    id: 'dev-patient-002',
    gender: null,
    genderPresent: false,
    nameFamily: ['Builder'],
  },
];

export async function readFilteredPatientDatasetZipOracle(fixtureDir, { project, generation } = {}) {
  for (const [name, value] of Object.entries({ fixtureDir, project, generation })) {
    assert.equal(typeof value, 'string', `${name} is required for the filtered ZIP oracle`);
    assert.notEqual(value.trim(), '', `${name} must be non-empty`);
  }

  const patientPath = resolve(fixtureDir, 'Patient.ndjson');
  const source = await readNDJSONResourceIdentityOracle({
    path: patientPath,
    project,
    generation,
    resourceType: 'Patient',
  });
  const bytes = await readFile(patientPath);
  const records = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  assert.equal(source.sha256, createHash('sha256').update(bytes).digest('hex'),
    'filtered ZIP oracle identity reader and raw rows must bind the same source bytes');
  assert.deepEqual([...source.ids].sort(), EXPECTED_PATIENTS.map(patient => patient.id).sort(),
    'filtered ZIP oracle must retain exactly the two literal Basic Patient identities');
  assert.deepEqual(records.map(record => record.resourceType), ['Patient', 'Patient'],
    'filtered ZIP oracle source must contain only Patient resources');

  const rows = records.map(record => {
    assert(Array.isArray(record.name), `Patient ${record.id} must have its literal name array`);
    const nameFamily = record.name.map(name => name.family);
    assert(nameFamily.every(value => typeof value === 'string' && value.length > 0),
      `Patient ${record.id} name.family values must be literal strings`);
    return {
      patient_id: record.id,
      gender: Object.hasOwn(record, 'gender') ? record.gender : null,
      name_family: nameFamily,
    };
  }).sort((left, right) => left.patient_id.localeCompare(right.patient_id));

  const literals = records.map(record => ({
    id: record.id,
    gender: Object.hasOwn(record, 'gender') ? record.gender : null,
    genderPresent: Object.hasOwn(record, 'gender'),
    nameFamily: record.name.map(name => name.family),
  })).sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(literals, EXPECTED_PATIENTS,
    'filtered ZIP oracle must preserve the exact female/missing-gender and repeated family literals');

  return {
    patientPath,
    patientSHA256: source.sha256,
    project,
    generation,
    patientIDs: rows.map(row => row.patient_id),
    femalePatientIDs: rows.filter(row => row.gender === 'female').map(row => row.patient_id),
    missingGenderPatientIDs: literals.filter(patient => !patient.genderPresent).map(patient => patient.id),
    rows,
  };
}
