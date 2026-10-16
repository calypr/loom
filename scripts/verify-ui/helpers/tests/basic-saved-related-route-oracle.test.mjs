import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  BASIC_SAVED_RELATED_ROUTE_A,
  BASIC_SAVED_RELATED_ROUTE_B,
  buildBasicSavedRelatedRouteOracle,
  readBasicSavedRelatedRouteOracle,
} from '../basic-saved-related-route-oracle.mjs';

const repositoryRoot = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const fixtureDir = join(repositoryRoot, 'testdata/devloop-fixture');
const read = resourceType => readFileSync(join(fixtureDir, resourceType + '.ndjson'), 'utf8')
  .split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));

test('devloop fixture derives exact direct and J02 Patient-to-Observation route rows', () => {
  const oracle = readBasicSavedRelatedRouteOracle(fixtureDir);
  assert.deepEqual(oracle.patientIDs, ['dev-patient-001', 'dev-patient-002']);
  assert.deepEqual(oracle.routeA.rows.map(row => [row.patientID, row.observationID]), [
    ['dev-patient-001', 'dev-observation-001'],
    ['dev-patient-001', 'dev-observation-003'],
    ['dev-patient-002', 'dev-observation-002'],
  ]);
  assert.deepEqual(oracle.routeA.matchCountsByPatient, {
    'dev-patient-001': 2,
    'dev-patient-002': 1,
  });
  assert.deepEqual(oracle.routeB.rows.map(row => [row.patientID, row.observationID]), [
    ['dev-patient-001', 'dev-observation-001'],
    ['dev-patient-002', null],
  ]);
  assert.deepEqual(oracle.routeB.matchCountsByPatient, {
    'dev-patient-001': 1,
    'dev-patient-002': 0,
  });
  assert.equal(oracle.routeA.label, 'Patient <-[subject]- Observation');
  assert.equal(oracle.routeB.label,
    'Patient <-[member_entity]- Group <-[subject]- Specimen <-[specimen]- Observation');
  assert.deepEqual(oracle.routeA.signature, [
    { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
  ]);
  assert.deepEqual(oracle.routeB.signature, [
    { fromResourceType: 'Patient', toResourceType: 'Group', relationship: 'member_entity_Patient', storageDirection: 'INBOUND' },
    { fromResourceType: 'Group', toResourceType: 'Specimen', relationship: 'subject_Group', storageDirection: 'INBOUND' },
    { fromResourceType: 'Specimen', toResourceType: 'Observation', relationship: 'specimen_Specimen', storageDirection: 'INBOUND' },
  ]);
  assert(Object.values(oracle.files).every(file => /^[a-f0-9]{64}$/.test(file.sha256)),
    'Each independently read fixture file must retain a SHA-256 identity.');
});

test('saved route contract keeps all three direct pairs and the unrelated J02 chain separate', () => {
  const patients = read('Patient');
  const observations = read('Observation');
  const groups = read('Group');
  const specimens = read('Specimen');
  const oracle = buildBasicSavedRelatedRouteOracle({
    patients,
    observations,
    groups,
    specimens,
  });
  assert.deepEqual(groups.find(group => group.id === 'dev-j02-group')?.member
    ?.map(member => member.entity?.reference), ['Patient/dev-patient-001']);
  assert.equal(specimens.find(specimen => specimen.id === 'dev-j02-specimen')?.subject?.reference,
    'Group/dev-j02-group');
  assert.equal(observations.find(observation => observation.id === 'dev-observation-001')?.specimen?.reference,
    'Specimen/dev-j02-specimen');
  assert.deepEqual(oracle.routeA.signature, [
    { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
  ]);
  assert.deepEqual(oracle.routeB.signature, [
    { fromResourceType: 'Patient', toResourceType: 'Group', relationship: 'member_entity_Patient', storageDirection: 'INBOUND' },
    { fromResourceType: 'Group', toResourceType: 'Specimen', relationship: 'subject_Group', storageDirection: 'INBOUND' },
    { fromResourceType: 'Specimen', toResourceType: 'Observation', relationship: 'specimen_Specimen', storageDirection: 'INBOUND' },
  ]);
  assert.deepEqual(oracle.routeA.rows, [
    { patientID: 'dev-patient-001', observationID: 'dev-observation-001' },
    { patientID: 'dev-patient-001', observationID: 'dev-observation-003' },
    { patientID: 'dev-patient-002', observationID: 'dev-observation-002' },
  ]);
  assert.deepEqual(oracle.routeB.rows, [
    { patientID: 'dev-patient-001', observationID: 'dev-observation-001' },
    { patientID: 'dev-patient-002', observationID: null },
  ]);
  assert.notDeepEqual(oracle.routeA.rows, oracle.routeB.rows);
  assert.equal(BASIC_SAVED_RELATED_ROUTE_A.length, 1);
  assert.equal(BASIC_SAVED_RELATED_ROUTE_B.length, 3);
});

test('breaking the J02 Group bridge changes only the long-route oracle', () => {
  const groups = read('Group').map(group => structuredClone(group));
  groups[0].member = [];
  const oracle = buildBasicSavedRelatedRouteOracle({
    patients: read('Patient'),
    observations: read('Observation'),
    groups,
    specimens: read('Specimen'),
  });
  assert.deepEqual(oracle.routeA.rows.map(row => [row.patientID, row.observationID]), [
    ['dev-patient-001', 'dev-observation-001'],
    ['dev-patient-001', 'dev-observation-003'],
    ['dev-patient-002', 'dev-observation-002'],
  ]);
  assert.deepEqual(oracle.routeB.rows.map(row => [row.patientID, row.observationID]), [
    ['dev-patient-001', null],
    ['dev-patient-002', null],
  ]);
});
