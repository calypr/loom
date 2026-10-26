import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { readFilteredPatientDatasetZipOracle } from '../viewer-output-filtered-dataset-zip-oracle.mjs';

const fixtureDir = fileURLToPath(new URL('../../../../testdata/devloop-fixture/', import.meta.url));
const identity = { project: 'basic-fixture-project', generation: 'basic-fixture-generation' };

test('filtered ZIP oracle retains exact typed rows, omitted gender, and repeated family values', async () => {
  const oracle = await readFilteredPatientDatasetZipOracle(fixtureDir, identity);
  assert.deepEqual(oracle.patientIDs, ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(oracle.femalePatientIDs, ['dev-patient-001']);
  assert.deepEqual(oracle.rows, [
    { patient_id: 'dev-patient-001', gender: 'female', name_family: ['Example', 'Example-Smith'] },
    { patient_id: 'dev-patient-002', gender: null, name_family: ['Builder'] },
  ]);
  assert.deepEqual(oracle.missingGenderPatientIDs, ['dev-patient-002'],
    'the raw oracle must distinguish an absent source gender from its nullable table projection');
  const raw = (await readFile(oracle.patientPath, 'utf8')).split(/\r?\n/).filter(Boolean).map(JSON.parse);
  assert.equal(Object.hasOwn(raw.find(record => record.id === 'dev-patient-002'), 'gender'), false,
    'the second archive null must come from an absent source member');
});

test('filtered ZIP oracle rejects fixture rows that change the independent repeated values', async () => {
  const source = (await readFile(join(fixtureDir, 'Patient.ndjson'), 'utf8'))
    .replace('Example-Smith', 'Changed-Family');
  const root = await mkdtemp(join(tmpdir(), 'filtered-zip-oracle-'));
  try {
    await writeFile(join(root, 'Patient.ndjson'), source);
    await assert.rejects(readFilteredPatientDatasetZipOracle(root, identity),
      /exact female\/missing-gender and repeated family literals/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
