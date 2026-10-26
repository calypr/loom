import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT,
  chooseCdaGroupPivotJoinWitness,
  prepareCdaGroupPivotJoinOracle,
  selectCdaGroupPivotJoinSubjects,
  verifyCdaGroupPivotJoinReread,
} from '../cda-group-pivot-join-oracle.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const raw = [
  { _id: 'Observation/001', id: 'obs-001', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['A', 'not-the-category'] },
  { _id: 'Observation/002', id: 'obs-002', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['A', 'later-code'] },
  { _id: 'Observation/003', id: 'obs-003', project, generation, resourceType: 'Observation', subjectReference: 'Patient/shared', status: 'final', codeCodingCodes: ['B', 'A'] },
  { _id: 'Observation/004', id: 'obs-004', project, generation, resourceType: 'Observation', subjectReference: 'Patient/left-only', status: 'final', codeCodingCodes: ['C'] },
];

const rawRow = (id, subjectReference, codeCodingCodes = ['A']) => ({
  _id: `Observation/${id}`,
  id: `obs-${id}`,
  project,
  generation,
  resourceType: 'Observation',
  subjectReference,
  status: 'final',
  codeCodingCodes,
});

test('bounded witness uses the first raw code and yields exact overlapping Observation populations', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  assert.equal(witness.sharedSubject, 'Patient/shared');
  assert.equal(witness.leftOnlySubject, 'Patient/left-only');
  assert.deepEqual(witness.categories, ['A', 'B']);
  assert.deepEqual(witness.left.map(row => row.id).sort(), ['obs-001', 'obs-002', 'obs-003', 'obs-004']);
  assert.deepEqual(witness.right.map(row => row.id).sort(), ['obs-001', 'obs-002', 'obs-003']);
  assert.notDeepEqual(witness.left.map(row => row._id), witness.left.map(row => row.id), 'Arango document keys are not FHIR ids.');
  assert(witness.left.every(row => row.status === 'final'), 'Status is deliberately constant; coding.code supplies the Pivot categories.');
  assert.deepEqual(witness.right.map(row => row.codeCodingCodes), [['A', 'not-the-category'], ['A', 'later-code'], ['B', 'A']]);
});

test('oracle computes Group counts, coding-code Pivot multiplicity, and LEFT/INNER null semantics', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  const expected = prepareCdaGroupPivotJoinOracle(witness, { project, generation });
  assert.deepEqual(expected.leftGroups, [['Patient/left-only', 1], ['Patient/shared', 3]]);
  assert.deepEqual(expected.rightGroups, [['Patient/shared', 'A', 2], ['Patient/shared', 'B', 1]]);
  assert.deepEqual(expected.rightPivot, [['Patient/shared', 2, 1]]);
  assert.deepEqual(expected.leftJoin, [
    ['Patient/left-only', 1, null, null, null],
    ['Patient/shared', 3, 'Patient/shared', 2, 1],
  ]);
  assert.deepEqual(expected.innerJoin, [['Patient/shared', 3, 'Patient/shared', 2, 1]]);
});

test('exact scoped reread preserves every selected raw document and coding array', () => {
  const witness = chooseCdaGroupPivotJoinWitness(raw, { project, generation });
  assert.equal(verifyCdaGroupPivotJoinReread(witness, [...raw].reverse(), { project, generation }).exact, true);
  assert.throws(() => verifyCdaGroupPivotJoinReread(witness, raw.slice(1), { project, generation }), /return all and only/);
  assert.throws(() => verifyCdaGroupPivotJoinReread(witness, [...raw, raw[0]], { project, generation }), /return all and only/);
  assert.throws(() => verifyCdaGroupPivotJoinReread(witness, raw.map(row => row.id === 'obs-002' ? { ...row, project: 'another' } : row), { project, generation }), /project/);
  assert.throws(() => verifyCdaGroupPivotJoinReread(witness, raw.map(row => row.id === 'obs-002' ? { ...row, codeCodingCodes: ['A'] } : row), { project, generation }), /changed raw Observation/);
});

test('subject selector ranks counts deterministically and returns at most ten scoped subjects', () => {
  const rows = [];
  let id = 0;
  for (const [subject, count] of [['Patient/z', 3], ['Patient/a', 3], ['Patient/m', 2], ...'bcdefghijkl'.split('').map(letter => [`Patient/${letter}`, 1])]) {
    for (let index = 0; index < count; index += 1) rows.push(rawRow(String(++id).padStart(3, '0'), subject));
  }
  const expected = ['Patient/a', 'Patient/z', 'Patient/m', 'Patient/b', 'Patient/c', 'Patient/d', 'Patient/e', 'Patient/f', 'Patient/g', 'Patient/h'];
  assert.deepEqual(selectCdaGroupPivotJoinSubjects(rows, { project, generation }), expected);
  assert.deepEqual(selectCdaGroupPivotJoinSubjects(rows, { project, generation, limit: 2 }), ['Patient/a', 'Patient/z']);
  assert.equal(selectCdaGroupPivotJoinSubjects(rows, { project, generation }).length, 10);
});

test('subjects with an empty or blank first code are not eligible witness members', () => {
  const noCode = [
    rawRow('005', 'Patient/no-code', []),
    rawRow('006', 'Patient/no-code', ['', 'later-code']),
  ];
  const rows = [...raw, ...noCode];
  const selected = selectCdaGroupPivotJoinSubjects(rows, { project, generation });
  assert(selected.includes('Patient/no-code'), 'Subject counts include valid rows even when they cannot supply a Pivot category.');
  const witness = chooseCdaGroupPivotJoinWitness(rows, { project, generation });
  assert.equal(witness.leftOnlySubject, 'Patient/left-only');
  assert.deepEqual(witness.categories, ['A', 'B']);
  assert.deepEqual(witness.left.map(row => row.id).sort(), ['obs-001', 'obs-002', 'obs-003', 'obs-004']);
});

test('bounded selector and witness reject malformed code arrays, duplicate IDs, and scope mismatches', () => {
  assert.throws(() => selectCdaGroupPivotJoinSubjects([rawRow('005', 'Patient/p', 'A')], { project, generation }), /must be an array/);
  assert.throws(() => chooseCdaGroupPivotJoinWitness([rawRow('005', 'Patient/p', ['A', 1])], { project, generation }), /must be strings/);
  assert.throws(() => selectCdaGroupPivotJoinSubjects([raw[0], { ...raw[1], _id: raw[0]._id }], { project, generation }), /repeated Arango document key/);
  assert.throws(() => selectCdaGroupPivotJoinSubjects([raw[0], { ...raw[1], id: raw[0].id }], { project, generation }), /repeated FHIR Observation ID/);
  assert.throws(() => selectCdaGroupPivotJoinSubjects([{ ...raw[0], project: 'another' }], { project, generation }), /project/);
  assert.throws(() => selectCdaGroupPivotJoinSubjects([{ ...raw[0], generation: 'other' }], { project, generation }), /generation/);
  assert.throws(() => selectCdaGroupPivotJoinSubjects(raw, { project, generation, limit: 11 }), /between 1 and 10/);
  const oversized = Array.from({ length: CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT + 1 }, (_, index) => rawRow(String(index).padStart(5, '0'), 'Patient/bounded'));
  assert.throws(() => selectCdaGroupPivotJoinSubjects(oversized, { project, generation }), /within 2000 Observations/);
});

test('bounded witness absence remains unverified instead of widening or inventing a category', () => {
  assert.equal(CDA_GROUP_PIVOT_JOIN_SCAN_LIMIT, 2_000);
  assert.equal(chooseCdaGroupPivotJoinWitness(raw.slice(0, 2), { project, generation }), undefined);
  const oneCategory = raw.map(row => ({ ...row, codeCodingCodes: ['A', 'B'] }));
  assert.equal(chooseCdaGroupPivotJoinWitness(oneCategory, { project, generation }), undefined);
  assert.throws(() => chooseCdaGroupPivotJoinWitness(raw, { project, generation, limit: 3 }), /within 3/);
  assert.throws(() => chooseCdaGroupPivotJoinWitness(raw.map(row => ({ ...row, generation: 'other' })), { project, generation }), /generation/);
  assert.throws(() => chooseCdaGroupPivotJoinWitness([raw[0], { ...raw[1], id: raw[0].id }, ...raw.slice(2)], { project, generation }), /repeated FHIR Observation ID/);
});
