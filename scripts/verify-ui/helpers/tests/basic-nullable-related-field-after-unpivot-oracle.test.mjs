import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  loadBasicNullableRelatedFieldAfterUnpivotOracle,
  readBasicNullableRelatedFieldAfterUnpivotSeedManifest,
  validateBasicNullableRelatedFieldAfterUnpivotRecords,
} from '../basic-nullable-related-field-after-unpivot-oracle.mjs';

const linkedPatient = { resourceType: 'Patient', id: 'basic-nullable-gender-patient-001' };
const schemaPatient = { resourceType: 'Patient', id: 'basic-nullable-gender-schema-patient-002', gender: 'female' };
const specimen = {
  resourceType: 'Specimen',
  id: 'basic-nullable-gender-specimen-001',
  status: 'available',
  subject: { reference: 'Patient/basic-nullable-gender-patient-001' },
};

test('Basic nullable fixture supplies exact [null] related ALL rows from a missing source scalar', () => {
  assert.deepEqual(loadBasicNullableRelatedFieldAfterUnpivotOracle(), {
    patient: { id: linkedPatient.id, gender: null, genderPropertyPresent: false },
    schemaWitnessPatient: { id: schemaPatient.id, gender: 'female', linkedToRoot: false },
    specimen: {
      id: specimen.id,
      status: specimen.status,
      subjectReference: 'Patient/basic-nullable-gender-patient-001',
    },
    rows: [{
      retainedSpecimenStatus: 'available',
      retainedPatientIDs: ['basic-nullable-gender-patient-001'],
      unpivotKey: 'Specimen ID',
      unpivotValue: 'basic-nullable-gender-specimen-001',
      patientGenderAll: [null],
    }],
  });
});

test('Basic nullable fixture keeps the field discoverable without linking the schema witness Patient', () => {
  const manifest = readBasicNullableRelatedFieldAfterUnpivotSeedManifest();
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 0 },
    { name: 'Patient.ndjson', recordCount: 2 },
    { name: 'Specimen.ndjson', recordCount: 1 },
  ]);
  assert(manifest.every(file => /^[a-f0-9]{64}$/.test(file.sha256)));
});

test('Basic nullable oracle rejects explicit null, a changed direct route, and unexpected Patient identities', () => {
  assert.throws(() => validateBasicNullableRelatedFieldAfterUnpivotRecords({
    patients: [{ ...linkedPatient, gender: null }, schemaPatient], specimens: [specimen],
  }), /must omit gender/);
  assert.throws(() => validateBasicNullableRelatedFieldAfterUnpivotRecords({
    patients: [linkedPatient, schemaPatient],
    specimens: [{ ...specimen, subject: { reference: 'Patient/unexpected' } }],
  }), /link directly/);
  assert.throws(() => validateBasicNullableRelatedFieldAfterUnpivotRecords({
    patients: [linkedPatient, schemaPatient, { ...schemaPatient, id: 'unexpected' }], specimens: [specimen],
  }), /exactly two Patient/);
});
