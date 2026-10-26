import assert from 'node:assert/strict';
import test from 'node:test';
import {
  BASIC_UNPIVOT_PIVOT_EXPECTED,
  readBasicUnpivotPivotFixture,
  expectedBasicUnpivotPivotRows,
} from '../basic-unpivot-pivot-fixture-oracle.mjs';
import { createReport } from '../report.mjs';
import { previewHeaderIndex } from '../playwright-preview-rows.mjs';
import { assertBasicUnpivotPivotContext } from '../../workflows/basic-unpivot-pivot.mjs';

test('Basic Unpivot→Pivot startup validates the native fixture context shape', () => {
  const scenarioID = 'basic-unpivot-pivot-roundtrip';
  const caseName = 'unpivot-to-pivot';
  const target = {
    fixtureProject: 'loom_dev_verify_context-test',
    fixtureGeneration: 'cda-fhir-v1',
    fixtureDir: '/tmp/basic-unpivot-pivot-context-test',
  };
  const report = createReport({
    scenario: scenarioID,
    caseName,
    target: { project: target.fixtureProject, generation: target.fixtureGeneration },
  });
  const nativeFixtureContext = { scenarioID, caseName, report, target, seed: { fresh: true } };

  assert.equal(report.case, caseName);
  assert.equal(report.caseName, undefined);
  assert.doesNotThrow(() => assertBasicUnpivotPivotContext(nativeFixtureContext));
  assert.throws(() => assertBasicUnpivotPivotContext({ ...nativeFixtureContext, caseName: 'wrong-case' }), /native fixture context must select the U→P case/);
});

test('Basic Unpivot→Pivot oracle pins two Patient records and the exact 2×2 shape roundtrip', () => {
  const { manifest, patients } = readBasicUnpivotPivotFixture();
  assert.deepEqual(manifest.map(file => [file.name, file.recordCount]), [
    ['Observation.ndjson', 0],
    ['Patient.ndjson', 2],
  ]);
  assert.deepEqual(patients, BASIC_UNPIVOT_PIVOT_EXPECTED.patients);
  assert.deepEqual(BASIC_UNPIVOT_PIVOT_EXPECTED.sourceColumnLabels, {
    patientID: 'Patient ID',
    gender: 'Gender',
    birthDate: 'Birth Date',
  });
  assert.deepEqual(expectedBasicUnpivotPivotRows(patients), {
    initialRows: [
      { patient_id: 'reshape-patient-001', gender: 'female', birth_date: '1980-01-02' },
      { patient_id: 'reshape-patient-002', gender: 'male', birth_date: '1985-03-04' },
    ],
    unpivotRows: [
      { patient_id: 'reshape-patient-001', field: 'gender', value: 'female' },
      { patient_id: 'reshape-patient-001', field: 'birthDate', value: '1980-01-02' },
      { patient_id: 'reshape-patient-002', field: 'gender', value: 'male' },
      { patient_id: 'reshape-patient-002', field: 'birthDate', value: '1985-03-04' },
    ],
    pivotRows: [
      { patient_id: 'reshape-patient-001', gender: 'female', birth_date: '1980-01-02' },
      { patient_id: 'reshape-patient-002', gender: 'male', birth_date: '1985-03-04' },
    ],
  });
});

test('U→P visible headers map CSS-uppercase labels to exact independent fields', () => {
  const renderedHeaders = ['BIRTH DATE', 'GENDER', 'PATIENT ID'];
  const exactOracleLabels = ['Birth Date', 'Gender', 'Patient ID'];
  assert.deepEqual(exactOracleLabels.map(label => previewHeaderIndex(renderedHeaders, label)), [0, 1, 2]);
  assert.throws(() => previewHeaderIndex(renderedHeaders, 'Birth'), /must occur exactly once/);
  assert.throws(() => previewHeaderIndex([...renderedHeaders, 'birth date'], 'Birth Date'), /must occur exactly once/);
});
