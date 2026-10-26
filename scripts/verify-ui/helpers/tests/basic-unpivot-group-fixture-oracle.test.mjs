import assert from 'node:assert/strict';
import test from 'node:test';
import {
  BASIC_UNPIVOT_PIVOT_EXPECTED,
  expectedBasicUnpivotPivotRows,
  readBasicUnpivotPivotFixture,
} from '../basic-unpivot-pivot-fixture-oracle.mjs';
import { createReport } from '../report.mjs';
import {
  assertBasicUnpivotGroupContext,
  BASIC_UNPIVOT_GROUP_EDITED_EXPECTED_ROWS,
  BASIC_UNPIVOT_GROUP_EXPECTED_ROWS,
  readVisibleTableInBrowser,
} from '../../workflows/basic-unpivot-group.mjs';

const literalPatients = [
  { resourceType: 'Patient', id: 'reshape-patient-001', gender: 'female', birthDate: '1980-01-02' },
  { resourceType: 'Patient', id: 'reshape-patient-002', gender: 'male', birthDate: '1985-03-04' },
];
const literalGroupedRows = [
  { Field: 'birthDate', 'Row count': 2 },
  { Field: 'gender', 'Row count': 2 },
];
const literalEditedGroupedRows = [
  { 'Source field': 'birthDate', 'Row count': 2 },
  { 'Source field': 'gender', 'Row count': 2 },
];

test('Basic Unpivot→Group context matches native fixture report contract', () => {
  const scenarioID = 'basic-unpivot-group';
  const caseName = 'unpivot-to-group';
  const target = {
    fixtureProject: 'loom_dev_verify_context-test',
    fixtureGeneration: 'cda-fhir-v1',
    fixtureDir: '/tmp/basic-unpivot-group-context-test',
  };
  const report = createReport({
    scenario: scenarioID,
    caseName,
    target: { project: target.fixtureProject, generation: target.fixtureGeneration },
  });
  const nativeFixtureContext = { scenarioID, caseName, report, target, seed: { fresh: true } };

  assert.equal(report.case, caseName);
  assert.equal(report.caseName, undefined);
  assert.doesNotThrow(() => assertBasicUnpivotGroupContext(nativeFixtureContext));
  assert.throws(() => assertBasicUnpivotGroupContext({ ...nativeFixtureContext, caseName: 'wrong-case' }), /Unpivot→Group case/);
});

test('literal Basic source independently pins every Unpivot row and both grouped counts', () => {
  const { manifest, patients } = readBasicUnpivotPivotFixture();
  assert.deepEqual(manifest.map(file => [file.name, file.recordCount]), [
    ['Observation.ndjson', 0],
    ['Patient.ndjson', 2],
  ]);
  assert.deepEqual(patients, literalPatients);
  assert.deepEqual(BASIC_UNPIVOT_PIVOT_EXPECTED.patients, literalPatients);

  const expected = expectedBasicUnpivotPivotRows(patients);
  assert.deepEqual(expected.unpivotRows, [
    { patient_id: 'reshape-patient-001', field: 'gender', value: 'female' },
    { patient_id: 'reshape-patient-001', field: 'birthDate', value: '1980-01-02' },
    { patient_id: 'reshape-patient-002', field: 'gender', value: 'male' },
    { patient_id: 'reshape-patient-002', field: 'birthDate', value: '1985-03-04' },
  ]);
  const grouped = [...new Set(expected.unpivotRows.map(row => row.field))].map(field => ({
    Field: field,
    'Row count': expected.unpivotRows.filter(row => row.field === field).length,
  })).sort((left, right) => left.Field.localeCompare(right.Field));
  assert.deepEqual(grouped, literalGroupedRows);
  assert.deepEqual(grouped.map(({ Field, ...row }) => ({ 'Source field': Field, ...row })), literalEditedGroupedRows);
  assert.deepEqual(BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, literalGroupedRows);
  assert.deepEqual(BASIC_UNPIVOT_GROUP_EDITED_EXPECTED_ROWS, literalEditedGroupedRows);
});

test('visible row reader works as a serialized browser callback with exact headers and values', () => {
  const cell = innerText => ({ innerText });
  const makeRow = (values, hasCells = true) => ({
    querySelector: selector => selector === '[role="cell"]' && hasCells ? {} : null,
    querySelectorAll: selector => selector === '[role="cell"]' ? values.map(cell) : [],
  });
  const table = {
    querySelectorAll: selector => selector === '[role="columnheader"]'
      ? [cell(' Patient ID\n'), cell(' Row count ')]
      : selector === '[role="row"]'
        ? [makeRow([], false), makeRow(['reshape-patient-001', '2']), makeRow([], false), makeRow(['reshape-patient-002', '2'])]
        : [],
  };
  const evaluateInBrowserRealm = Function('table', `return (${readVisibleTableInBrowser.toString()})(table);`);

  assert.deepEqual(evaluateInBrowserRealm(table), {
    headers: ['Patient ID', 'Row count'],
    rows: [['reshape-patient-001', '2'], ['reshape-patient-002', '2']],
  });
});
