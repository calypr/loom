import assert from 'node:assert/strict';
import { readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { hasSameQuantityValueMultiset, readBasicRelatedQuantityOneAllOracle } from '../basic-related-quantity-one-all-oracle.mjs';

const repositoryRoot = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const fixtureDir = join(repositoryRoot, 'testdata/devloop-fixture');

test('Basic Quantity oracle for a same-code family predicts ONE disagreement and exact decimal ALL values', async () => {
  const fixtureFiles = readdirSync(fixtureDir);
  assert(fixtureFiles.includes('Observation.ndjson'));
  assert(fixtureFiles.includes('Patient.ndjson'));
  const oracle = await readBasicRelatedQuantityOneAllOracle(fixtureDir);
  assert.equal(oracle.patientID, 'dev-patient-001');
  assert.deepEqual(oracle.relatedObservationIDs, [
    'dev-observation-001',
    'dev-observation-003',
  ]);
  assert.deepEqual(oracle.codeFamily, {
    system: 'https://example.test/codes',
    code: 'height',
    display: 'Height',
  });
  assert.equal(oracle.valuePath, 'Observation.valueQuantity.value');
  assert.equal(oracle.logicalType, 'decimal');
  assert.equal(oracle.cardinality, 'optional_one');
  assert.deepEqual(oracle.one, { disagreement: true, distinctValues: [172.5, 180] });
  assert.deepEqual(oracle.all, [
    { observationID: 'dev-observation-001', value: 172.5, quantity: { value: 172.5, unit: 'cm' } },
    { observationID: 'dev-observation-003', value: 180, quantity: { value: 180, unit: 'cm' } },
  ]);
  assert(Object.values(oracle.files).every(file => /^[a-f0-9]{64}$/.test(file.sha256)));
});

test('Quantity ALL comparison ignores order while preserving numeric types, missingness, and duplicates', () => {
  assert.equal(hasSameQuantityValueMultiset([180, 172.5], [172.5, 180]), true,
    'the ALL renderer may return the exact numeric multiset in a different order');
  assert.equal(hasSameQuantityValueMultiset([172.5, 172.5, 180], [180, 172.5, 172.5]), true,
    'duplicate terminal values remain represented with their original multiplicity');
  assert.equal(hasSameQuantityValueMultiset([172.5, '180'], [172.5, 180]), false,
    'numeric-looking strings must not be coerced into decimal values');
  assert.equal(hasSameQuantityValueMultiset([172.5, undefined], [172.5, 180]), false,
    'a missing terminal value must not disappear during comparison');
  assert.equal(hasSameQuantityValueMultiset([172.5, 172.5], [172.5, 180]), false,
    'equal length alone must not hide a changed duplicate count');
  assert.equal(hasSameQuantityValueMultiset(Array(2), [172.5, 180]), false,
    'sparse missing array entries must not pass numeric type validation');
  assert.equal(hasSameQuantityValueMultiset(Array(2), Array(2)), false,
    'two sparse arrays must not compare equal as if they contained numeric values');
});
