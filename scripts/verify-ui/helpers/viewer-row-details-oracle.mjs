import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];

export const readBasicPatientRowDetailsOracle = fixtureDir => {
  const patientPath = join(fixtureDir, 'Patient.ndjson');
  const patientBytes = readFileSync(patientPath);
  const patients = patientBytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const patientIDs = patients.map(patient => patient.id);
  assert.deepEqual(patientIDs, expectedPatientIDs, 'Basic Patient fixture must contain the exact two independent Patient IDs');
  assert(patients.every(patient => patient.resourceType === 'Patient'), 'Basic fixture oracle must contain only Patient resources');

  const recipePath = join(fixtureDir, 'recipe.json');
  const recipeBytes = readFileSync(recipePath);
  const recipe = JSON.parse(recipeBytes.toString('utf8'));
  const [output] = recipe.outputs ?? [];
  assert.equal(output?.rootResourceType, 'Patient', 'Basic recipe must output Patient rows');
  assert.equal(output?.rowGrain, 'patient', 'Basic recipe must use Patient row grain');
  assert.deepEqual(output?.fields, [{ name: 'patient_id', expr: { select: 'root.id' } }],
    'Basic recipe must project patient_id from the raw Patient.id field');

  return {
    patientPath,
    patientSHA256: createHash('sha256').update(patientBytes).digest('hex'),
    recipePath,
    recipeSHA256: createHash('sha256').update(recipeBytes).digest('hex'),
    patientIDs,
    patientIDColumn: 'patient_id',
    patientIDSourcePath: 'id',
  };
};
