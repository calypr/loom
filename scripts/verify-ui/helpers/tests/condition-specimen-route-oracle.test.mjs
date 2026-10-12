import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { readConditionSpecimenRouteOracle } from '../condition-specimen-route-oracle.mjs';

const repositoryRoot = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const fixtureDir = join(repositoryRoot, 'testdata/basic-root-picker-ambiguity');

test('Condition-to-Specimen oracle proves three distinct routes, authored Patient anchor, and unmatched row', () => {
  const oracle = readConditionSpecimenRouteOracle(fixtureDir);
  const expectedSpecimens = oracle.sourceIDs.Specimen;
  assert.deepEqual(oracle.patientAnchorRows.map(row => row.specimenId), expectedSpecimens);
  assert.deepEqual(oracle.focusRows.map(row => row.specimenId), expectedSpecimens);
  assert.deepEqual(oracle.stageRows.map(row => row.specimenId), expectedSpecimens);
  assert.equal(oracle.patientAnchorRows.find(row => row.specimenId === 'specimen-subject-route').conditionId,
    'condition-subject-route');
  assert.equal(oracle.focusRows.find(row => row.specimenId === 'specimen-focus-route').conditionId,
    'condition-focus-route');
  assert.equal(oracle.stageRows.find(row => row.specimenId === 'specimen-stage-route').conditionId,
    'condition-stage-route');
  assert.deepEqual(oracle.patientAnchorRows.find(row => row.specimenId === 'specimen-unmatched'), {
    specimenId: 'specimen-unmatched', conditionId: null, patientId: null,
  });
  assert.equal(new Set([
    oracle.patientAnchorRows.find(row => row.conditionId)?.specimenId,
    oracle.focusRows.find(row => row.conditionId)?.specimenId,
    oracle.stageRows.find(row => row.conditionId)?.specimenId,
  ]).size, 3, 'Each compiler route must produce a different exact Specimen ID.');
});
