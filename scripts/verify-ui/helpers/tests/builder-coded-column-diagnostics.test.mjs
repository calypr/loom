import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { generatedJ01ConceptNDJSON } from '../../../loom-dev.mjs';
import { previewHeaderMatches } from '../../workflows/builder-coded-source-column.mjs';

test('rendered uppercase coded-column headers match labels after semantic normalization', () => {
  assert.equal(previewHeaderMatches('FIXTURE HEIGHT ABC (KG)', 'Fixture Height ABC'), true);
  assert.equal(previewHeaderMatches('OBSERVATION ID', 'Observation ID'), true);
  assert.equal(previewHeaderMatches('OTHER HEIGHT ABC (KG)', 'Fixture Height ABC'), false);
});

test('coded-source-column fixture keeps the six-row independent oracle without global J01 expansion', () => {
  const repositoryRoot = fileURLToPath(new URL('../../../../', import.meta.url));
  const fixtureDir = join(repositoryRoot, 'testdata', 'builder-coded-source-column');
  const originalDir = join(repositoryRoot, 'testdata', 'devloop-fixture');
  const patients = readFileSync(join(fixtureDir, 'Patient.ndjson'));
  const observations = readFileSync(join(fixtureDir, 'Observation.ndjson'));
  assert.deepEqual(patients, readFileSync(join(originalDir, 'Patient.ndjson')));
  assert.deepEqual(observations, readFileSync(join(originalDir, 'Observation.ndjson')));
  assert.deepEqual(patients.toString('utf8').trim().split(/\r?\n/).map(line => JSON.parse(line).id), [
    'dev-patient-001', 'dev-patient-002',
  ]);
  assert.deepEqual(observations.toString('utf8').trim().split(/\r?\n/).map(line => JSON.parse(line).id), [
    'dev-observation-001', 'dev-observation-002', 'dev-observation-003',
    'dev-pair-001', 'dev-pair-002', 'dev-pair-003',
  ]);
  assert.equal(generatedJ01ConceptNDJSON(fixtureDir), undefined);
});
