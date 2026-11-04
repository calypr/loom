import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { fixtureProjectExplorerCollectionPath } from '../fixture-context.mjs';
import { canonicalProjectID } from '../../../loom-dev.mjs';
import {
  assertBasicNamedCohortPresenceRows,
  buildBasicNamedCohortPresenceOracle,
} from '../basic-named-cohort-presence-oracle.mjs';

const fixtureDir = new URL('../../../../testdata/basic-named-cohort-presence-zero-match/', import.meta.url);
const read = filename => readFileSync(new URL(filename, fixtureDir), 'utf8').trim().split(/\r?\n/).map(line => JSON.parse(line));
const fixture = {
  patients: read('Patient.ndjson'),
  specimens: read('Specimen.ndjson'),
  observations: read('Observation.ndjson'),
};

test('literal Basic fixture proves nonempty zero-match Group false and positive comparator true', () => {
  const oracle = buildBasicNamedCohortPresenceOracle(fixture);
  assert.deepEqual(oracle.sourceCounts, { patients: 2, specimens: 2, observations: 1 });
  assert.deepEqual(oracle.groups.map(({ memberIDs, observationIDs, presence }) => ({ memberIDs, observationIDs, presence })), [
    { memberIDs: ['basic-presence-zero-specimen-001'], observationIDs: [], presence: false },
    { memberIDs: ['basic-presence-positive-specimen-001'], observationIDs: ['basic-presence-positive-observation-001'], presence: true },
  ]);
});

test('literal oracle rejects an absent explicit Group member or a mismatched related Observation', () => {
  assert.throws(() => buildBasicNamedCohortPresenceOracle({
    ...fixture,
    specimens: fixture.specimens.map(specimen => specimen.id === 'basic-presence-zero-specimen-001'
      ? { ...specimen, subject: { reference: 'Patient/basic-presence-positive-patient-001' } }
      : specimen),
  }), /exact explicit Group membership/);
  assert.throws(() => buildBasicNamedCohortPresenceOracle({
    ...fixture,
    observations: [{ ...fixture.observations[0], subject: { reference: 'Patient/basic-presence-zero-patient-001' } }],
  }), /exact related Observation IDs/);
});

test('row assertion requires exact group membership and Boolean results', () => {
  const source = buildBasicNamedCohortPresenceOracle(fixture);
  const oracle = { ...source, labelColumn: 'Group label' };
  const rows = [
    { 'Group label': 'Nonempty member with no related Observation', Members: ['basic-presence-zero-specimen-001'], Presence: false },
    { 'Group label': 'Nonempty member with one related Observation', Members: ['basic-presence-positive-specimen-001'], Presence: true },
  ];
  const accepted = assertBasicNamedCohortPresenceRows(rows, {
    sourceIDColumn: undefined, memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'literal test',
  });
  assert.deepEqual(accepted.map(row => row.presence), [false, true]);
  assert.throws(() => assertBasicNamedCohortPresenceRows([
    { ...rows[0], Members: ['basic-presence-positive-specimen-001'] }, rows[1],
  ], { memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'mutated test' }), /lost exact member|gained foreign member/);
  assert.throws(() => assertBasicNamedCohortPresenceRows([
    { ...rows[0], Presence: true }, rows[1],
  ], { memberColumn: 'Members', presenceColumn: 'Presence', oracle, phase: 'mutated presence' }), /exact Boolean false/);
});

// The former browser workflow's call-order/source-regex checks are retired.
// These assertions cover the retained oracle and route helpers, not browser
// creation, selection requests, or fresh UI reload after API seeding.
test('browser Explorer collection paths encode the canonical project identity', () => {
  const project = 'loom_dev_verify_example/cda-fhir-v1';
  assert.equal(fixtureProjectExplorerCollectionPath(project), '/api/v1/projects/loom_dev_verify_example%252Fcda-fhir-v1/explorers');
});

test('storage project aliases normalize to the canonical selection identity', () => {
  const storageProject = 'loom_dev_verify_mv48uh79-0a5e8d8';
  const selectionProject = canonicalProjectID(storageProject);
  assert.equal(selectionProject, 'loom_dev_verify_mv48uh79/0a5e8d8');
});
