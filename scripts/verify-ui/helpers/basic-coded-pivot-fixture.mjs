import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

export const BASIC_CODED_PIVOT_SYSTEM = 'urn:loom:basic-coded-pivot';
export const BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID = 'basic-pivot-patient';
export const BASIC_CODED_PIVOT_EXPECTED_ROWS = Object.freeze([
  Object.freeze(['basic-pivot-001', '2', '10']),
  Object.freeze(['basic-pivot-002', '3', '—']),
  Object.freeze(['basic-pivot-003', '—', '12']),
  Object.freeze(['basic-pivot-004', '2', '10']),
]);
export const BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS = Object.freeze(
  BASIC_CODED_PIVOT_EXPECTED_ROWS.map(([, ...values]) => Object.freeze(values))
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))),
);
export const BASIC_CODED_PIVOT_EXPECTED_IDS = Object.freeze([
  'basic-pivot-001', 'basic-pivot-002', 'basic-pivot-003', 'basic-pivot-004',
]);

export const readBasicCodedPivotPatientFixture = path => {
  const bytes = readFileSync(path);
  const lines = bytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean);
  assert.equal(lines.length, 1, 'Basic coded Pivot fixture must contain one loader-required Patient row.');
  let patient;
  try { patient = JSON.parse(lines[0]); }
  catch (error) { throw new Error(`Invalid Basic coded Pivot Patient JSON: ${error.message}`); }
  assert.equal(patient.resourceType, 'Patient');
  assert.equal(patient.id, BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID);
  return {
    patient,
    id: patient.id,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    path,
  };
};

export const readBasicCodedPivotFixture = path => {
  const bytes = readFileSync(path);
  const lines = bytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean);
  const observations = lines.map((line, index) => {
    try { return JSON.parse(line); }
    catch (error) { throw new Error(`Invalid Basic coded Pivot fixture JSON on line ${index + 1}: ${error.message}`); }
  });
  assert.equal(observations.length, BASIC_CODED_PIVOT_EXPECTED_IDS.length,
    'Basic coded Pivot fixture must contain exactly four Observation rows.');
  assert.deepEqual(observations.map(observation => observation.id).sort(), [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
  assert(observations.every(observation => observation.resourceType === 'Observation'));

  const actual = observations.map(observation => {
    const values = new Map();
    for (const component of observation.component ?? []) {
      for (const coding of component.code?.coding ?? []) {
        if (coding.system !== BASIC_CODED_PIVOT_SYSTEM) continue;
        assert(['alpha', 'beta'].includes(coding.code), `Unexpected coded category ${coding.code}`);
        assert(!values.has(coding.code), `${observation.id} has duplicate ${coding.code} values.`);
        assert(Number.isSafeInteger(component.valueInteger), `${observation.id} ${coding.code} must have an integer value.`);
        values.set(coding.code, String(component.valueInteger));
      }
    }
    return [observation.id, values.get('alpha') ?? '—', values.get('beta') ?? '—'];
  }).sort((left, right) => left[0].localeCompare(right[0]));
  assert.deepEqual(actual, BASIC_CODED_PIVOT_EXPECTED_ROWS,
    'Independent literal FHIR rows differ from the Basic coded Pivot oracle.');

  return {
    observations,
    rows: actual,
    ids: actual.map(([id]) => id),
    sha256: createHash('sha256').update(bytes).digest('hex'),
    path,
  };
};

export const assertBasicCodedPivotRendered = (snapshot, expectedRows = BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS) =>
  assertBasicCodedPivotPreviewRendered(snapshot, expectedRows);

export const assertBasicCodedPivotPreviewRendered = (snapshot, expectedRows = BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS) => {
  assert(Array.isArray(snapshot?.headers) && Array.isArray(snapshot?.rows), 'Coded Pivot proposal preview needs headers and rows.');
  const header = value => String(value ?? '').split(/\r?\n/, 1)[0].replace(/\s+/g, ' ').trim().toLowerCase();
  assert.deepEqual(snapshot.headers.map(header), ['alpha', 'beta'],
    'Coded Pivot proposal preview must expose exactly its two generated category columns.');
  const canonicalRows = rows => rows.map((row, index) => {
    assert(Array.isArray(row) && row.length === 2,
      `Coded Pivot proposal preview row ${index + 1} must contain exactly Alpha and Beta.`);
    return row.map(value => String(value ?? '').trim());
  }).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const actual = canonicalRows(snapshot.rows);
  assert.deepEqual(actual, canonicalRows(expectedRows),
    'Coded Pivot proposal preview must preserve the exact generated value pairs and multiplicity.');
  return actual;
};
