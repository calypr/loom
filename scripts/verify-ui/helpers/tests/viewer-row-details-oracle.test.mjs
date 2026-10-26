import test from 'node:test';
import assert from 'node:assert/strict';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readBasicPatientRowDetailsOracle } from '../viewer-row-details-oracle.mjs';

const fixtureDir = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/devloop-fixture');

test('Basic Viewer row details pin patient_id to the independent Patient.id values', () => {
  const oracle = readBasicPatientRowDetailsOracle(fixtureDir);

  assert.deepEqual(oracle.patientIDs, ['dev-patient-001', 'dev-patient-002']);
  assert.equal(oracle.patientIDColumn, 'patient_id');
  assert.equal(oracle.patientIDSourcePath, 'id');
  assert.match(oracle.patientSHA256, /^[a-f0-9]{64}$/);
  assert.match(oracle.recipeSHA256, /^[a-f0-9]{64}$/);
});
