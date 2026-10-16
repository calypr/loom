import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const BASIC_ADD_COLUMN_COVERAGE_FIXTURE_DIR = fileURLToPath(
  new URL('../../../testdata/basic-add-column-coverage/', import.meta.url),
);

const expectedRecords = Object.freeze([
  Object.freeze({
    resourceType: 'Observation',
    id: 'basic-add-column-coverage-present',
    status: 'final',
    code: Object.freeze({ text: 'Add Column coverage witness' }),
    valueQuantity: Object.freeze({
      value: 1,
      unit: 'day',
      system: 'http://unitsofmeasure.org',
      code: 'd',
    }),
  }),
  Object.freeze({
    resourceType: 'Observation',
    id: 'basic-add-column-coverage-null',
    status: 'final',
    code: Object.freeze({ text: 'Add Column coverage witness' }),
    valueQuantity: Object.freeze({
      value: 1,
      unit: 'day',
      system: 'http://unitsofmeasure.org',
      code: null,
    }),
  }),
  Object.freeze({
    resourceType: 'Observation',
    id: 'basic-add-column-coverage-missing',
    status: 'final',
    code: Object.freeze({ text: 'Add Column coverage witness' }),
    valueQuantity: Object.freeze({
      value: 1,
      unit: 'day',
      system: 'http://unitsofmeasure.org',
    }),
  }),
]);

const expectedPatient = Object.freeze({
  resourceType: 'Patient',
  id: 'basic-add-column-coverage-inert-patient',
});

const MAX_FIXTURE_BYTES = 4096;

/**
 * Reads the exact three-row Add Columns fixture and returns a raw-source oracle.
 * The row states retain the missing/null distinction for the oracle; populated
 * counts and examples describe only values the native UI can display.
 */
export function readBasicAddColumnCoverageOracle({
  fixtureDir = BASIC_ADD_COLUMN_COVERAGE_FIXTURE_DIR,
} = {}) {
  const names = readdirSync(fixtureDir)
    .filter(name => name.endsWith('.ndjson'))
    .sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson'],
    'The Add Columns coverage fixture must contain exactly Patient and Observation sources');

  const patientPath = join(fixtureDir, 'Patient.ndjson');
  const patientSize = statSync(patientPath).size;
  assert(patientSize > 0 && patientSize <= MAX_FIXTURE_BYTES,
    `Patient.ndjson must be a nonempty fixture of at most ${MAX_FIXTURE_BYTES} bytes`);
  const patients = readFileSync(patientPath, 'utf8')
    .split(/\r?\n/)
    .filter(line => line.trim().length > 0)
    .map(line => JSON.parse(line));
  assert.deepEqual(patients, [expectedPatient],
    'The fixture must contain one inert Patient with no relationship to its Observations');

  const path = join(fixtureDir, 'Observation.ndjson');
  const size = statSync(path).size;
  assert(size > 0 && size <= MAX_FIXTURE_BYTES,
    `Observation.ndjson must be a nonempty fixture of at most ${MAX_FIXTURE_BYTES} bytes`);
  const records = readFileSync(path, 'utf8')
    .split(/\r?\n/)
    .filter(line => line.trim().length > 0)
    .map(line => JSON.parse(line));
  assert.equal(records.length, 3,
    'The Add Columns coverage fixture must contain exactly three Observation rows');
  assert.deepEqual(records, expectedRecords,
    'The Add Columns coverage records must preserve the exact present/null/missing witness');

  const rows = records.map(record => {
    assert.equal(record.resourceType, 'Observation');
    assert.equal(typeof record.id, 'string');
    assert(record.valueQuantity && typeof record.valueQuantity === 'object');
    if (!Object.hasOwn(record.valueQuantity, 'code')) {
      return { id: record.id, state: 'missing' };
    }
    if (record.valueQuantity.code === null) {
      return { id: record.id, state: 'null', value: null };
    }
    assert.equal(typeof record.valueQuantity.code, 'string',
      `Observation ${record.id} must have a string or null valueQuantity.code`);
    return { id: record.id, state: 'present', value: record.valueQuantity.code };
  });
  const populatedValues = rows
    .filter(row => row.state === 'present')
    .map(row => row.value);

  return {
    resourceType: 'Observation',
    rows,
    populatedCount: populatedValues.length,
    displayedCount: rows.length,
    populatedExamples: [...new Set(populatedValues)],
  };
}
