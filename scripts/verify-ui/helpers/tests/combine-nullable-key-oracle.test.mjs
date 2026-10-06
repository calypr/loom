import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const readRows = (name) => readFileSync(
  new URL('../../../../testdata/verify-combine/' + name, import.meta.url),
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

test('nullable subject-reference fixture proves SQL NULL key equality and LEFT preservation', () => {
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
  assert.deepEqual(observations.map(({ id, key }) => [id, key]), [
    ['combine-observation-final-1', 'Patient/combine-null-key-match'],
    ['combine-observation-final-2', null],
    ['combine-observation-preliminary', 'Patient/combine-null-key-preliminary'],
    ['combine-observation-unmatched', 'Patient/combine-null-key-unmatched'],
  ]);
  assert.deepEqual(reports.map(({ id, key }) => [id, key]), [
    ['combine-observation-final-1', null],
    ['combine-observation-final-2', 'Patient/combine-null-key-match'],
    ['combine-observation-preliminary', 'Patient/combine-null-key-preliminary'],
  ]);

  assert.deepEqual(joinOnNullableKey(observations, reports, 'INNER'), [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-preliminary', 'combine-observation-preliminary'],
  ], 'equal non-NULL keys match, while the NULL key on each side never matches');
  assert.deepEqual(joinOnNullableKey(observations, reports, 'LEFT'), [
    ['combine-observation-final-1', 'combine-observation-final-2'],
    ['combine-observation-final-2', null],
    ['combine-observation-preliminary', 'combine-observation-preliminary'],
    ['combine-observation-unmatched', null],
  ], 'LEFT retains both unmatched left rows and null-extends the absent right identity');

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
