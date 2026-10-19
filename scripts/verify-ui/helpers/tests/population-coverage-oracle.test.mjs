import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { readBasicPatientPopulationOracle } from '../population-coverage-oracle.mjs';

const fixturePath = fileURLToPath(new URL('../../../../testdata/devloop-fixture/Patient.ndjson', import.meta.url));

test('Basic Patient population oracle binds the exact fixture identities to the configured project and generation', async () => {
  const oracle = await readBasicPatientPopulationOracle({
    path: fixturePath,
    project: 'loom_dev_fixture',
    generation: 'fixture-v1',
  });

  assert.deepEqual(oracle.scope, { project: 'loom_dev_fixture', generation: 'fixture-v1' });
  assert.equal(oracle.resourceType, 'Patient');
  assert.equal(oracle.count, 2);
  assert.deepEqual(oracle.ids, ['dev-patient-002', 'dev-patient-001']);
  assert.match(oracle.sha256, /^[a-f0-9]{64}$/);
});
