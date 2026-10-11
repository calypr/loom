import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  loadBasicRelatedFieldAfterUnpivotFixtureOracle,
  readBasicRelatedFieldAfterUnpivotSeedManifest,
  validateBasicRelatedFieldAfterUnpivotRecords,
} from '../basic-related-field-after-unpivot-fixture-oracle.mjs';

const patient = {
  resourceType: 'Patient',
  id: 'basic-gender-patient-001',
  gender: 'female',
};

const specimen = {
  resourceType: 'Specimen',
  id: 'basic-gender-specimen-001',
  status: 'available',
  subject: { reference: 'Patient/basic-gender-patient-001' },
};

test('Basic related-field fixture produces the exact bounded post-Unpivot rows', () => {
  const oracle = loadBasicRelatedFieldAfterUnpivotFixtureOracle();
  assert.deepEqual(oracle, {
    patient: { id: 'basic-gender-patient-001', gender: 'female' },
    specimen: {
      id: 'basic-gender-specimen-001',
      status: 'available',
      subjectReference: 'Patient/basic-gender-patient-001',
    },
    rows: [{
      retainedSpecimenStatus: 'available',
      retainedPatientIDs: ['basic-gender-patient-001'],
      unpivotKey: 'Specimen ID',
      unpivotValue: 'basic-gender-specimen-001',
      patientGenderAll: ['female'],
    }],
  });
});

test('Basic fixture seed manifest includes the required empty Observation file and no extra resources', () => {
  const manifest = readBasicRelatedFieldAfterUnpivotSeedManifest();
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 0 },
    { name: 'Patient.ndjson', recordCount: 1 },
    { name: 'Specimen.ndjson', recordCount: 1 },
  ]);
  assert.equal(manifest[0].sizeBytes, 0);
  assert(manifest.every(file => /^[a-f0-9]{64}$/.test(file.sha256)), 'Every seed resource file must have a recorded SHA-256.');
});

test('Basic related-field oracle rejects extra identities, missing gender, and wrong references', () => {
  assert.throws(() => validateBasicRelatedFieldAfterUnpivotRecords({
    patients: [patient, { ...patient, id: 'unexpected-patient' }],
    specimens: [specimen],
  }), /exactly one Patient/);

  assert.throws(() => validateBasicRelatedFieldAfterUnpivotRecords({
    patients: [{ ...patient, gender: '' }],
    specimens: [specimen],
  }), /nonempty string gender/);

  assert.throws(() => validateBasicRelatedFieldAfterUnpivotRecords({
    patients: [patient],
    specimens: [{ ...specimen, subject: { reference: 'Patient/other-patient' } }],
  }), /exactly one direct subject reference/);
});
