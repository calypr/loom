import test from 'node:test';
import assert from 'node:assert/strict';
import { cdaMembershipObservationQuery, MAX_CDA_MEMBERSHIP_SCAN, prepareCdaMembershipOracle } from './cda-current-draft-membership-oracle.mjs';

const scope = { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
const rows = [
  { _id: 'Observation/z', id: 'obs-z', ...scope, resourceType: 'Observation' },
  { _id: 'Observation/a', id: 'obs-a', ...scope, resourceType: 'Observation' },
  { _id: 'Observation/m', id: 'obs-m', ...scope, resourceType: 'Observation' },
];

test('bounded raw oracle creates exact overlapping CDA ID selections and independent INCLUDE/EXCLUDE results', () => {
  const oracle = prepareCdaMembershipOracle(rows, scope);
  assert.deepEqual(oracle.leftIDs, ['obs-a', 'obs-m']);
  assert.deepEqual(oracle.rightIDs, ['obs-a', 'obs-z']);
  assert.deepEqual(oracle.includeIDs, ['obs-a']);
  assert.deepEqual(oracle.excludeIDs, ['obs-m']);
  assert.equal(oracle.scannedCount, 3);
  assert.equal(oracle.selectionLimit, 3);
});

test('raw query pins project, generation, Observation type, deterministic identity order, and the scan limit', () => {
  const query = cdaMembershipObservationQuery(scope);
  assert.match(query, /r\.project == "loom_dev_cda_fhir"/);
  assert.match(query, /r\.dataset_generation == "cda-fhir-v1"/);
  assert.match(query, /r\.payload\.resourceType == "Observation"/);
  assert.match(query, /SORT r\._id LIMIT 2000/);
  assert.equal(MAX_CDA_MEMBERSHIP_SCAN, 2_000);
});

test('oracle rejects missing witnesses, repeated IDs, and cross-project or cross-generation rows', () => {
  assert.throws(() => prepareCdaMembershipOracle(rows.slice(0, 2), scope), /at least three distinct/);
  assert.throws(() => prepareCdaMembershipOracle([rows[0], rows[1], { ...rows[2], id: rows[1].id }], scope), /repeated a FHIR/);
  assert.throws(() => prepareCdaMembershipOracle([rows[0], rows[1], { ...rows[2], project: 'other' }], scope), /out-of-scope/);
  assert.throws(() => prepareCdaMembershipOracle([rows[0], rows[1], { ...rows[2], generation: 'other' }], scope), /out-of-scope/);
});
