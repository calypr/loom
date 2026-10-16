import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { COHORT_GROUP_PIVOT_EXPECTED, readCohortGroupPivotFixture } from '../cohort-group-pivot-publication-oracle.mjs';
import { decodeCohortGroupPivotIntegerValue } from '../../workflows/cohort-group-pivot-publication.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/basic-unpivot-pivot/', import.meta.url));

test('Basic named cohort Group→Pivot oracle pins exact source members and published rows', () => {
  const fixture = readCohortGroupPivotFixture(fixtureDir);

  assert.deepEqual(fixture.patientIDs, ['reshape-patient-001', 'reshape-patient-002']);
  assert.deepEqual(fixture.patients.map(({ id, gender, birthDate }) => ({ id, gender, birthDate })), [
    { id: 'reshape-patient-001', gender: 'female', birthDate: '1980-01-02' },
    { id: 'reshape-patient-002', gender: 'male', birthDate: '1985-03-04' },
  ]);
  assert.deepEqual(fixture.groupRows, [
    { 'Group label': 'Two fixture Patients', Gender: 'female', 'Row count': 1 },
    { 'Group label': 'Two fixture Patients', Gender: 'male', 'Row count': 1 },
  ]);
  assert.deepEqual(fixture.viewerColumns, ['Group label', 'female', 'male']);
  assert.deepEqual(fixture.viewerRows, [
    { 'Group label': 'Two fixture Patients', female: 1, male: 1 },
  ]);
  assert.deepEqual(fixture.groupRows, COHORT_GROUP_PIVOT_EXPECTED.groupRows);
  assert.deepEqual(fixture.viewerRows, COHORT_GROUP_PIVOT_EXPECTED.viewerRows);
  assert.match(fixture.sourceHashes.patient, /^[a-f0-9]{64}$/);
  assert.match(fixture.sourceHashes.observation, /^[a-f0-9]{64}$/);
});

test('cohort Group→Pivot decoder accepts canonical safe integer JSON strings without weakening the numeric oracle', () => {
  assert.equal(decodeCohortGroupPivotIntegerValue('1', 'integer'), 1);
  assert.equal(decodeCohortGroupPivotIntegerValue('0', 'integer'), 0);
  assert.equal(decodeCohortGroupPivotIntegerValue('-1', 'integer'), -1);
  assert.equal(decodeCohortGroupPivotIntegerValue(1, 'integer'), 1);
  assert.equal(decodeCohortGroupPivotIntegerValue(Number.MAX_SAFE_INTEGER, 'integer'), Number.MAX_SAFE_INTEGER);

  for (const value of [null, true, 1.5, Number.MAX_SAFE_INTEGER + 1, '', '01', '-0', '1.0', '1e0', '9007199254740992']) {
    assert.throws(() => decodeCohortGroupPivotIntegerValue(value, 'integer'),
      `unsafe or non-canonical integer value ${JSON.stringify(value)} must be rejected`);
  }
  assert.throws(() => decodeCohortGroupPivotIntegerValue('1', 'string'), /Only columns declared as integer/);
});
