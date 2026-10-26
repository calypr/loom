import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const readRows = (name) => readFileSync(
  new URL('../../../../testdata/verify-combine-nullable-duplicates/' + name, import.meta.url),
  'utf8',
).split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));

const nullableReferenceRows = (rows) => rows.map((row) => ({
  id: row.id,
  key: row.subject?.reference ?? null,
}));

const joinOnNullableKey = (leftRows, rightRows, joinType) => leftRows.flatMap((left) => {
  const matches = left.key === null
    ? []
    : rightRows.filter((right) => right.key !== null && right.key === left.key);
  const rows = matches.length ? matches : joinType === 'LEFT' ? [null] : [];
  return rows.map((right) => [left.id, right?.id ?? null]);
});

test('nullable duplicate-key fixture proves 2x2 row multiplication, SQL NULL non-equality, and LEFT preservation', () => {
  const rawObservations = readRows('Observation.ndjson');
  const rawReports = readRows('DiagnosticReport.ndjson');
  const observations = nullableReferenceRows(rawObservations);
  const reports = nullableReferenceRows(rawReports);

  assert.equal(observations.length, 4);
  assert.equal(reports.length, 3);
  assert.ok(observations.every((row) => typeof row.id === 'string' && row.id.length > 0),
    'fixture keeps every required Observation resource id');
  assert.ok(reports.every((row) => typeof row.id === 'string' && row.id.length > 0),
    'fixture keeps every required DiagnosticReport resource id');
  assert.equal(Object.hasOwn(rawObservations[2], 'subject'), false,
    'the selected left NULL key is represented by the absent optional FHIR subject field');
  assert.equal(Object.hasOwn(rawReports[0], 'subject'), false,
    'the selected right NULL key is represented by the absent optional FHIR subject field');
  assert.deepEqual(observations.map(({ id, key }) => [id, key]), [
    ['combine-observation-final-1', 'Patient/combine-null-key-match'],
    ['combine-observation-final-2', 'Patient/combine-null-key-match'],
    ['combine-observation-preliminary', null],
    ['combine-observation-unmatched', 'Patient/combine-null-key-unmatched'],
  ]);
  assert.deepEqual(reports.map(({ id, key }) => [id, key]), [
    ['combine-observation-final-1', null],
    ['combine-observation-final-2', 'Patient/combine-null-key-match'],
    ['combine-observation-preliminary', 'Patient/combine-null-key-match'],
  ]);
  assert.equal(observations.filter(({ key }) => key === 'Patient/combine-null-key-match').length, 2);
  assert.equal(reports.filter(({ key }) => key === 'Patient/combine-null-key-match').length, 2);

  assert.deepEqual(joinOnNullableKey(observations, reports, 'INNER'), [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-final-1', 'combine-observation-preliminary'],
    ['combine-observation-final-2', 'combine-observation-final-2'],
    ['combine-observation-final-2', 'combine-observation-preliminary'],
  ], 'the shared key emits all 2x2 row pairs, while the NULL key on each side never matches');
  assert.deepEqual(joinOnNullableKey(observations, reports, 'LEFT'), [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-final-1', 'combine-observation-preliminary'],
    ['combine-observation-final-2', 'combine-observation-final-2'],
    ['combine-observation-final-2', 'combine-observation-preliminary'],
    ['combine-observation-preliminary', null],
    ['combine-observation-unmatched', null],
  ], 'LEFT keeps all four duplicate-key pairs and retains/null-extends both unmatched left rows');

  const appendRows = [
    ...rawObservations.map(({ id, status }) => [id, status, null]),
    ...rawReports.map(({ id, status }) => [id, status, null]),
    ...readRows('Patient.ndjson').map(({ id, gender }) => [id, null, gender]),
  ];
  assert.deepEqual(appendRows, [
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-observation-unmatched', 'unknown', null],
    ['combine-observation-final-1', 'final', null],
    ['combine-observation-final-2', 'final', null],
    ['combine-observation-preliminary', 'preliminary', null],
    ['combine-fixture-patient', null, 'female'],
  ], 'adding nullable key data leaves the APPEND 8-row ID/status/gender oracle unchanged');
});
