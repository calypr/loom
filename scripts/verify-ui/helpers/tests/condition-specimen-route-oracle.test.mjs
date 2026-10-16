import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { conditionPatientRowsMatch, readConditionSpecimenRouteOracle } from '../condition-specimen-route-oracle.mjs';

const repositoryRoot = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const fixtureDir = join(repositoryRoot, 'testdata/basic-root-picker-ambiguity');

test('Condition-to-Specimen oracle proves three distinct routes, Patient-subject path, and unmatched row', () => {
  const oracle = readConditionSpecimenRouteOracle(fixtureDir);
  const expectedSpecimens = oracle.sourceIDs.Specimen;
  assert.deepEqual(oracle.patientSubjectRows.map(row => row.specimenId), expectedSpecimens);
  assert.deepEqual(oracle.focusRows.map(row => row.specimenId), expectedSpecimens);
  assert.deepEqual(oracle.stageRows.map(row => row.specimenId), expectedSpecimens);
  assert.equal(oracle.patientSubjectRows.find(row => row.specimenId === 'specimen-subject-route').conditionId,
    'condition-subject-route');
  assert.equal(oracle.focusRows.find(row => row.specimenId === 'specimen-focus-route').conditionId,
    'condition-focus-route');
  assert.equal(oracle.stageRows.find(row => row.specimenId === 'specimen-stage-route').conditionId,
    'condition-stage-route');
  assert.deepEqual(oracle.patientSubjectRows.find(row => row.specimenId === 'specimen-unmatched'), {
    specimenId: 'specimen-unmatched', conditionId: null, patientId: null,
  });
  assert.equal(new Set([
    oracle.patientSubjectRows.find(row => row.conditionId)?.specimenId,
    oracle.focusRows.find(row => row.conditionId)?.specimenId,
    oracle.stageRows.find(row => row.conditionId)?.specimenId,
  ]).size, 3, 'Each compiler route must produce a different exact Specimen ID.');
});

test('visible Condition and Patient rows compare as an exact unordered multiset when Specimen ID is hidden', () => {
  const oracle = readConditionSpecimenRouteOracle(fixtureDir);
  const expected = oracle.patientSubjectRows;
  const rendered = [
    { conditionId: 'condition-subject-route', patientId: 'patient-subject-anchor' },
    { conditionId: null, patientId: null },
    { conditionId: null, patientId: null },
    { conditionId: null, patientId: null },
  ];

  assert.equal(expected.length, 4, 'The independent route oracle must retain all four Specimen rows.');
  assert.equal(conditionPatientRowsMatch(rendered, expected), true,
    'Hidden Specimen IDs must not impose an order on the visible Condition/Patient tuples.');
  assert.equal(conditionPatientRowsMatch(rendered.slice(1), expected), false,
    'The projected comparison must retain all three null rows, including the unmatched Specimen.');
  assert.equal(conditionPatientRowsMatch([
    ...rendered.slice(0, 3), { conditionId: null, patientId: 'patient-unmatched' },
  ], expected), false,
  'An unmatched Specimen must not expose its unrelated Patient subject.');
});
