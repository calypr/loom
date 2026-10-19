import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { join } from 'node:path';

const MAX_FIXTURE_BYTES = 16 * 1024;
const EXPECTED_PATIENT_ID = 'dev-patient-001';
const EXPECTED_CODE = { system: 'https://example.test/codes', code: 'height', display: 'Height' };
const EXPECTED_VALUES = [172.5, 180];

export function hasSameQuantityValueMultiset(actual, expected) {
  if (!Array.isArray(actual) || !Array.isArray(expected) || actual.length !== expected.length) return false;
  const finiteNumbers = values => Array.from(values).every(value =>
    typeof value === 'number' && Number.isFinite(value));
  if (!finiteNumbers(actual) || !finiteNumbers(expected)) return false;
  const sortedActual = [...actual].sort((left, right) => left - right);
  const sortedExpected = [...expected].sort((left, right) => left - right);
  return sortedActual.every((value, index) => value === sortedExpected[index]);
}

async function readRows(fixtureDir, resourceType) {
  const path = join(fixtureDir, `${resourceType}.ndjson`);
  const metadata = await stat(path);
  assert(metadata.size > 0 && metadata.size <= MAX_FIXTURE_BYTES,
    `${resourceType}.ndjson must remain a small, bounded fixture`);
  const bytes = await readFile(path);
  const rows = bytes.toString('utf8').split(/\r?\n/).filter(line => line.trim()).map((line, index) => {
    try {
      return JSON.parse(line);
    } catch (error) {
      throw new Error(`${resourceType}.ndjson line ${index + 1} is invalid JSON: ${error.message}`);
    }
  });
  assert(rows.every(row => row?.resourceType === resourceType),
    `${resourceType}.ndjson must contain only ${resourceType} resources`);
  assert(rows.every(row => typeof row.id === 'string' && row.id.length > 0),
    `${resourceType}.ndjson resources must have literal IDs`);
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length,
    `${resourceType}.ndjson IDs must be unique`);
  return { path, bytes, rows };
}

export async function readBasicRelatedQuantityOneAllOracle(fixtureDir) {
  const patientFile = await readRows(fixtureDir, 'Patient');
  const observationFile = await readRows(fixtureDir, 'Observation');
  const patient = patientFile.rows.find(row => row.id === EXPECTED_PATIENT_ID);
  assert(patient, 'The fixture must contain the exact related Patient');
  const observations = observationFile.rows
    .filter(observation => observation.subject?.reference === `Patient/${EXPECTED_PATIENT_ID}`)
    .map(observation => {
      const codings = observation.code?.coding;
      assert(Array.isArray(codings) && codings.length === 1,
        `Observation/${observation.id} must have one root CodeableConcept coding`);
      assert.deepEqual(codings[0], EXPECTED_CODE,
        `Observation/${observation.id} must retain the shared coded family key and display`);
      assert.equal(observation.valueQuantity?.unit, 'cm');
      assert.equal(typeof observation.valueQuantity?.value, 'number',
        `Observation/${observation.id} must expose a scalar decimal Quantity value`);
      return {
        observationID: observation.id,
        code: { ...codings[0] },
        value: observation.valueQuantity.value,
        quantity: { ...observation.valueQuantity },
      };
    }).sort((left, right) => left.observationID.localeCompare(right.observationID));

  assert.deepEqual(observations.map(({ observationID }) => observationID),
    ['dev-observation-001', 'dev-observation-003'],
    'The complete literal Patient→Observation route must contain exactly the two expected Observation identities');
  assert(observations.every(({ code }) => code.system === EXPECTED_CODE.system &&
    code.code === EXPECTED_CODE.code && code.display === EXPECTED_CODE.display),
  'Every Observation returned by the selected Patient relationship must belong to the exact Height family');
  const values = observations.map(observation => observation.value);
  assert.deepEqual(values, EXPECTED_VALUES, 'The exact coded-family scalar values changed');
  assert.equal(new Set(values).size, 2, 'The fixture must predict a ONE disagreement from distinct values');

  return {
    fixtureDir,
    files: {
      Patient: { path: patientFile.path, sha256: createHash('sha256').update(patientFile.bytes).digest('hex') },
      Observation: { path: observationFile.path, sha256: createHash('sha256').update(observationFile.bytes).digest('hex') },
    },
    patientID: patient.id,
    relatedObservationIDs: observations.map(observation => observation.observationID),
    codeFamily: EXPECTED_CODE,
    valuePath: 'Observation.valueQuantity.value',
    logicalType: 'decimal',
    cardinality: 'optional_one',
    one: { disagreement: true, distinctValues: values },
    all: observations.map(({ observationID, value, quantity }) => ({ observationID, value, quantity })),
  };
}
