import assert from 'node:assert/strict';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { readStandaloneCodedFieldFixture } from '../standalone-coded-field-fixture.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../..');

test('standalone coded-field fixture preserves per-Coding correlation and absent controls', () => {
  const oracle = readStandaloneCodedFieldFixture(resolve(repositoryRoot, 'testdata/basic-standalone-coded-fields'));
  assert.deepEqual(oracle.patientIDs, ['coded-catalog-patient-01', 'coded-catalog-patient-02']);
  assert.deepEqual(oracle.conditionCodesByPatient['coded-catalog-patient-01'].map(({ version }) => version), [
    'v1', 'v2', undefined,
  ]);
  assert.equal(oracle.medicationCodesByPatient['coded-catalog-patient-01'].length, 2);
  assert.equal(oracle.specimenCodesByPatient['coded-catalog-patient-01'][0]?.display, undefined);
  assert.equal(oracle.specimenCodesByPatient['coded-catalog-patient-02'].length, 0);
  assert.equal(oracle.observationPairsByPatient['coded-catalog-patient-01'].quantity, 42);
  assert.equal(oracle.observationPairsByPatient['coded-catalog-patient-02'].quantity, null);
  assert.deepEqual(Object.keys(oracle.routes).sort(), ['Condition', 'Medication', 'Observation', 'Specimen']);
  assert.deepEqual(Object.keys(oracle.sourceFiles).sort(), [
    'Condition', 'Medication', 'MedicationAdministration', 'Observation', 'Patient', 'Specimen',
  ]);
});
