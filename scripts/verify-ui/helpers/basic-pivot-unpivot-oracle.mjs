import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repositoryRoot = resolve(fileURLToPath(new URL('../../..', import.meta.url)));
const defaultFixtureDir = join(repositoryRoot, 'testdata/basic-pivot-unpivot');
const MAX_FIXTURE_BYTES = 16 * 1024;

const patients = Object.freeze([
  Object.freeze({ resourceType: 'Patient', id: 'basic-pivot-unpivot-patient-a', gender: 'female' }),
  Object.freeze({ resourceType: 'Patient', id: 'basic-pivot-unpivot-patient-b', gender: 'male' }),
]);

const observationCode = Object.freeze({ text: 'Basic fixture Observation' });

const observations = Object.freeze([
  Object.freeze({ resourceType: 'Observation', id: 'basic-pivot-unpivot-a-missing', status: 'final',
    code: observationCode,
    subject: Object.freeze({ reference: 'Patient/basic-pivot-unpivot-patient-a' }),
    valueQuantity: Object.freeze({ value: 3, unit: 'unit' }) }),
  Object.freeze({ resourceType: 'Observation', id: 'basic-pivot-unpivot-a-null', status: 'final',
    code: observationCode,
    subject: Object.freeze({ reference: 'Patient/basic-pivot-unpivot-patient-a' }),
    valueQuantity: Object.freeze({ code: null, value: 5, unit: 'unit' }) }),
  Object.freeze({ resourceType: 'Observation', id: 'basic-pivot-unpivot-a-d', status: 'final',
    code: observationCode,
    subject: Object.freeze({ reference: 'Patient/basic-pivot-unpivot-patient-a' }),
    valueQuantity: Object.freeze({ code: 'd', value: 2, unit: 'unit' }) }),
  Object.freeze({ resourceType: 'Observation', id: 'basic-pivot-unpivot-b-missing', status: 'final',
    code: observationCode,
    subject: Object.freeze({ reference: 'Patient/basic-pivot-unpivot-patient-b' }),
    valueQuantity: Object.freeze({ value: 7, unit: 'unit' }) }),
  Object.freeze({ resourceType: 'Observation', id: 'basic-pivot-unpivot-b-d', status: 'final',
    code: observationCode,
    subject: Object.freeze({ reference: 'Patient/basic-pivot-unpivot-patient-b' }),
    valueQuantity: Object.freeze({ code: 'd', value: 8, unit: 'unit' }) }),
]);

const expectedPivotRows = Object.freeze([
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', MISSING: 3, NULL: 5, d: 2 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', MISSING: 7, NULL: null, d: 8 }),
]);

const expectedPreservingUnpivotRows = Object.freeze([
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'MISSING', value: 3 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'NULL', value: 5 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'd', value: 2 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'MISSING', value: 7 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'NULL', value: null }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'd', value: 8 }),
]);

const expectedDroppingUnpivotRows = Object.freeze([
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'MISSING', value: 3 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'NULL', value: 5 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-a', category: 'd', value: 2 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'MISSING', value: 7 }),
  Object.freeze({ subjectReference: 'Patient/basic-pivot-unpivot-patient-b', category: 'd', value: 8 }),
]);

const readNDJSON = (directory, name) => {
  const path = join(directory, name);
  const source = readFileSync(path);
  assert(source.length > 0 && source.length <= MAX_FIXTURE_BYTES,
    `${name} must be a bounded nonempty Basic fixture file`);
  return {
    path,
    source,
    records: source.toString('utf8').split(/\r?\n/).filter(line => line.trim()).map(line => JSON.parse(line)),
  };
};

export function readBasicPivotUnpivotSeedManifest(directory = defaultFixtureDir) {
  const names = readdirSync(directory).filter(name => name.endsWith('.ndjson')).sort();
  assert.deepEqual(names, ['Observation.ndjson', 'Patient.ndjson'],
    'The exact Pivot→Unpivot Basic fixture must contain only Observation and Patient NDJSON files');
  return names.map(name => {
    const path = join(directory, name);
    const source = readFileSync(path);
    return {
      name,
      path,
      sizeBytes: statSync(path).size,
      sha256: createHash('sha256').update(source).digest('hex'),
      recordCount: source.toString('utf8').split(/\r?\n/).filter(line => line.trim()).length,
    };
  });
}

export function validateBasicPivotUnpivotRecords({ observations: actualObservations, patients: actualPatients }) {
  assert(Array.isArray(actualObservations), 'Observation source rows must be an array');
  assert(Array.isArray(actualPatients), 'Patient source rows must be an array');
  assert.deepEqual(actualPatients, patients, 'The Basic fixture must contain the exact two independently named Patient identities');
  assert.deepEqual(actualObservations, observations,
    'The Basic fixture must retain the exact five Observation rows, including the absent-versus-null category distinction');

  const patientIDs = new Set(actualPatients.map(patient => patient.id));
  assert.equal(patientIDs.size, 2, 'The Pivot fixture must have two distinct group identities');
  for (const row of actualObservations) {
    assert(patientIDs.has(row.subject.reference.slice('Patient/'.length)),
      `Observation ${row.id} must reference exactly one fixture Patient`);
    assert.equal(row.status, 'final');
    assert.equal(typeof row.valueQuantity.value, 'number');
    assert.equal(row.valueQuantity.unit, 'unit');
  }

  const missingCodeRows = actualObservations.filter(row => !Object.hasOwn(row.valueQuantity, 'code'));
  const nullCodeRows = actualObservations.filter(row => Object.hasOwn(row.valueQuantity, 'code') && row.valueQuantity.code === null);
  assert.deepEqual(missingCodeRows.map(row => row.id), [
    'basic-pivot-unpivot-a-missing', 'basic-pivot-unpivot-b-missing',
  ], 'Both literal MISSING category rows must omit valueQuantity.code');
  assert.deepEqual(nullCodeRows.map(row => row.id), ['basic-pivot-unpivot-a-null'],
    'The literal NULL category row must own valueQuantity.code with a null value');

  return {
    patients: actualPatients.map(({ id, gender }) => ({ id, gender })),
    sourceRows: actualObservations.map(row => ({
      id: row.id,
      subjectReference: row.subject.reference,
      category: Object.hasOwn(row.valueQuantity, 'code')
        ? row.valueQuantity.code === null ? 'NULL' : row.valueQuantity.code
        : 'MISSING',
      value: row.valueQuantity.value,
    })),
    categories: [
      Object.freeze({ identity: 'MISSING', key: Object.freeze({ kind: 'MISSING' }) }),
      Object.freeze({ identity: 'NULL', key: Object.freeze({ kind: 'NULL' }) }),
      Object.freeze({ identity: 'd', key: Object.freeze({ kind: 'STRING', string: 'd' }) }),
    ],
    pivotRows: expectedPivotRows.map(row => ({ ...row })),
    preservingUnpivotRows: expectedPreservingUnpivotRows.map(row => ({ ...row })),
    droppingUnpivotRows: expectedDroppingUnpivotRows.map(row => ({ ...row })),
    absentCategoryRows: missingCodeRows.map(row => row.id),
    explicitNullCategoryRows: nullCodeRows.map(row => row.id),
  };
}

export function loadBasicPivotUnpivotFixtureOracle(directory = defaultFixtureDir) {
  const manifest = readBasicPivotUnpivotSeedManifest(directory);
  assert.deepEqual(manifest.map(({ name, recordCount }) => ({ name, recordCount })), [
    { name: 'Observation.ndjson', recordCount: 5 },
    { name: 'Patient.ndjson', recordCount: 2 },
  ]);
  const observationFile = readNDJSON(directory, 'Observation.ndjson');
  const patientFile = readNDJSON(directory, 'Patient.ndjson');
  return {
    ...validateBasicPivotUnpivotRecords({
      observations: observationFile.records,
      patients: patientFile.records,
    }),
    seedManifest: manifest,
  };
}
