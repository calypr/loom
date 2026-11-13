import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const fixtureURL = new URL('../../../testdata/graph-rows/', import.meta.url);
export const graphRowsFixtureDirectory = fileURLToPath(fixtureURL);
const types = ['ResearchSubject', 'Patient', 'Observation', 'Condition', 'ResearchStudy', 'DocumentReference', 'Specimen', 'MedicationAdministration', 'Group'];
const clone = value => structuredClone(value);

export function loadGraphRowsFixture() {
  const resources = Object.fromEntries(types.map(type => [type,
    readFileSync(new URL(`${type}.ndjson`, fixtureURL), 'utf8').trim().split(/\r?\n/).map(JSON.parse),
  ]));
  const oracle = JSON.parse(readFileSync(new URL('oracle.json', fixtureURL), 'utf8'));
  return { resources, oracle };
}

// This reference evaluator only follows explicit fixture references and copies
// selected scalars. It does not import a compiler, generated query or API result.
// It validates the hand-authored oracle, not Loom's implementation.
export function validateGraphRowsFixture({ resources, oracle } = loadGraphRowsFixture()) {
  assert.equal(oracle.schemaVersion, 1);
  for (const type of types) {
    assert.equal(resources[type].length, oracle.recordCounts[type], `${type} seed count changed`);
    assert.equal(new Set(resources[type].map(record => record.id)).size, resources[type].length, `${type} duplicate identity`);
    assert(resources[type].every(record => record.resourceType === type), `${type} resource type changed`);
  }
  assert.deepEqual(resources.Patient.map(patient => patient.id), oracle.replacementPatientIDs, 'Root replacement must include every Patient, including the performer-only record');
  const patients = new Map(resources.Patient.map(patient => [`Patient/${patient.id}`, patient]));
  const related = (type, reference) => resources[type].filter(record => record.subject?.reference === reference);
  const preserving = resources.ResearchSubject.map(subject => {
    const patient = patients.get(subject.subject.reference);
    assert(patient, `Missing fixture patient ${subject.subject.reference}`);
    const observations = related('Observation', subject.subject.reference);
    return {
      identity: [subject.id], patientId: patient.id,
      observationIds: observations.map(record => record.id),
      observationValues: observations.map(record => record.valueInteger),
      observationCount: observations.length, hasObservation: observations.length > 0,
      conditionIds: related('Condition', subject.subject.reference).map(record => record.id),
    };
  });
  assert.deepEqual(preserving, oracle.preserving, 'Row-preserving literal oracle differs from seed references');
  assert.deepEqual(preserving.filter(row => row.hasObservation).map(row => row.identity), oracle.requiredObservationIdentities);
  const expanded = resources.ResearchSubject.flatMap(subject =>
    related('Observation', subject.subject.reference).map(observation => ({
      identity: [subject.id, observation.id], patientId: patients.get(subject.subject.reference).id,
      observationValue: observation.valueInteger, subjectStatus: subject.status,
    })));
  assert.deepEqual(expanded, oracle.expandedDropMissing, 'Related expansion must retain input/related identity pairs');
  // A list authored before expansion is copied in full to each pair. The
  // explicitly selected scalar belongs to that pair's related record only.
  assert.deepEqual(resources.ResearchSubject.flatMap(subject => {
    const observations = related('Observation', subject.subject.reference);
    return observations.map(observation => ({
      identity: [subject.id, observation.id], patientId: patients.get(subject.subject.reference).id,
      subjectStatus: subject.status, observationValues: observations.map(record => record.valueInteger),
      perMatchObservationValue: observation.valueInteger,
    }));
  }), oracle.expandedListsAndExplicitScalars, 'Expansion must preserve the existing list and distinguish an explicit per-match scalar');
  const unmatched = resources.ResearchSubject.filter(subject => related('Observation', subject.subject.reference).length === 0);
  assert.deepEqual(unmatched.map(subject => ({ identity: [subject.id, null], patientId: patients.get(subject.subject.reference).id,
    observationValue: null, subjectStatus: subject.status })), [oracle.expandedKeepMissingExtra]);
  assert.deepEqual(unmatched.map(subject => ({
    identity: [subject.id, null], patientId: patients.get(subject.subject.reference).id,
    subjectStatus: subject.status, observationValues: [], perMatchObservationValue: null,
  })), [oracle.expandedListsKeepMissingExtra]);
  const groups = new Map();
  for (const subject of resources.ResearchSubject) {
    const identity = [patients.get(subject.subject.reference).gender ?? null, subject.status];
    const key = JSON.stringify(identity);
    const group = groups.get(key) ?? { identity, contributorSubjectIds: [], count: 0, observationValues: [] };
    group.contributorSubjectIds.push(subject.id);
    group.count++;
    group.observationValues.push(...related('Observation', subject.subject.reference).map(record => record.valueInteger));
    groups.set(key, group);
  }
  assert.deepEqual([...groups.values()], oracle.groupKeepMissing, 'Group oracle must retain the complete key tuple and contributors');
  assert.deepEqual([...groups.values()].filter(group => group.identity.every(value => value !== null && value !== undefined)),
    oracle.groupExcludeMissing, 'Group EXCLUDE oracle must drop only missing-key inputs and retain exact tuples/contributors');
  assert.deepEqual(resources.ResearchSubject.flatMap(subject => subject.identifier.map((item, index) => ({
    identity: [subject.id, index], value: item.value,
  }))), oracle.sourceExpandedDropEmpty);
  assert.deepEqual(resources.ResearchSubject.filter(subject => subject.identifier.length === 0).map(subject => ({
    identity: [subject.id, null], value: null,
  })), [oracle.sourceExpandedKeepEmptyExtra]);
  assert.deepEqual(oracle.explicitGroups, [
    { identity: ['named-active'], contributorSubjectIds: resources.ResearchSubject.filter(subject => subject.status === 'active').map(subject => subject.id), count: 3 },
    { identity: ['named-empty'], contributorSubjectIds: [], count: 0 },
  ]);
  const many = resources.ResearchSubject.find(subject => subject.id === 'rs-many');
  const manyObservations = related('Observation', many.subject.reference);
  assert.equal(manyObservations.length, 2, 'The Cartesian-product trap requires exactly two observations');
  assert.equal(related('Condition', many.subject.reference).length, 3, 'The Cartesian-product trap requires exactly three conditions');
  assert.deepEqual({
    rootSubjectPatient: patients.get(many.subject.reference).id,
    observationPerformerPatients: [...new Set(manyObservations.flatMap(observation => observation.performer.map(ref => patients.get(ref.reference).id)))],
  }, oracle.distinctPatientOccurrences);
  assert.notEqual(oracle.distinctPatientOccurrences.rootSubjectPatient, oracle.distinctPatientOccurrences.observationPerformerPatients[0]);
  const studies = new Map(resources.ResearchStudy.map(study => [`ResearchStudy/${study.id}`, study]));
  assert.deepEqual(resources.ResearchSubject.map(subject => {
    const study = subject.study ? studies.get(subject.study.reference) : undefined;
    if (!study) {
      assert.equal(subject.id, oracle.missingStudyReference.subjectId);
      assert.equal(subject.study?.reference, oracle.missingStudyReference.reference);
      assert.equal(oracle.missingStudyReference.targetPresent, false);
    }
    return { identity: [subject.id], studyIds: study ? [study.id] : [], studyTitles: study ? [study.title] : [] };
  }), oracle.nativeStudy);
  for (const type of ['DocumentReference', 'Specimen', 'MedicationAdministration']) {
    assert.deepEqual(resources[type].map(record => record.id), oracle.defaultOutputIdentities[type]);
  }
  assert.deepEqual(resources.Group.flatMap(group => group.member.map(member => [group.id, member.entity.reference.split('/')[1]])), oracle.defaultOutputIdentities.GroupMember);
  assert.deepEqual(resources.Observation.map(observation => ({
    identity: [observation.id],
    subjectPatientIds: [patients.get(observation.subject.reference).id],
    performerPatientIds: observation.performer.map(reference => patients.get(reference.reference).id),
  })), oracle.observationPatientOccurrences, 'Sibling Patient occurrences must keep their distinct references');
  return clone(oracle);
}

// The caller must adapt actual public result columns to this explicit fixture
// shape, without inventing identities or dropping duplicates. Sort row order
// only; field/value order remains part of the assertion.
export function assertGraphRowsResult(caseName, actual, oracle = validateGraphRowsFixture()) {
  const expected = oracle[caseName];
  assert(Array.isArray(expected), `Unknown exact row case ${caseName}`);
  assert(Array.isArray(actual), 'Actual rows must be an array');
  const sorted = rows => clone(rows).sort((a, b) => JSON.stringify(a.identity ?? a).localeCompare(JSON.stringify(b.identity ?? b)));
  assert.deepEqual(sorted(actual), sorted(expected), `Exact graph-rows case ${caseName}`);
}
