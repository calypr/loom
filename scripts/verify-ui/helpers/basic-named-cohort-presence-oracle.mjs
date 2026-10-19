import assert from 'node:assert/strict';

export const BASIC_NAMED_COHORT_PRESENCE_EXPECTED = Object.freeze([
  Object.freeze({
    groupId: 'group-zero-match',
    label: 'Nonempty member with no related Observation',
    ordinal: 0,
    memberIDs: Object.freeze(['basic-presence-zero-specimen-001']),
    patientIDs: Object.freeze(['basic-presence-zero-patient-001']),
    observationIDs: Object.freeze([]),
    presence: false,
  }),
  Object.freeze({
    groupId: 'group-positive-match',
    label: 'Nonempty member with one related Observation',
    ordinal: 1,
    memberIDs: Object.freeze(['basic-presence-positive-specimen-001']),
    patientIDs: Object.freeze(['basic-presence-positive-patient-001']),
    observationIDs: Object.freeze(['basic-presence-positive-observation-001']),
    presence: true,
  }),
]);

const indexResources = (resources, type, name) => {
  assert(Array.isArray(resources), `${name} must be an array of literal FHIR resources.`);
  const byID = new Map();
  for (const resource of resources) {
    assert.equal(resource?.resourceType, type, `${name} contains a non-${type} resource.`);
    assert.equal(typeof resource.id, 'string', `${name} contains a resource without an ID.`);
    assert(!byID.has(resource.id), `${name} contains duplicate ${type}/${resource.id}.`);
    byID.set(resource.id, resource);
  }
  return byID;
};

const referenceID = (resource, type, context) => {
  const reference = resource?.subject?.reference;
  assert.equal(typeof reference, 'string', `${context} must have an explicit subject reference.`);
  const prefix = `${type}/`;
  assert(reference.startsWith(prefix), `${context} subject must be a literal ${type} reference.`);
  return reference.slice(prefix.length);
};

export const buildBasicNamedCohortPresenceOracle = ({ patients, specimens, observations }) => {
  const patientsByID = indexResources(patients, 'Patient', 'Patient.ndjson');
  const specimensByID = indexResources(specimens, 'Specimen', 'Specimen.ndjson');
  const observationsByID = indexResources(observations, 'Observation', 'Observation.ndjson');
  assert.equal(patientsByID.size, 2, 'The literal fixture must contain exactly two Patients.');
  assert.equal(specimensByID.size, 2, 'The literal fixture must contain exactly two Specimens.');
  assert.equal(observationsByID.size, 1, 'The literal fixture must contain exactly one Observation.');

  const observationIDsByPatient = new Map([...patientsByID.keys()].map(id => [id, []]));
  for (const observation of observationsByID.values()) {
    const patientID = referenceID(observation, 'Patient', `Observation/${observation.id}`);
    assert(patientsByID.has(patientID), `Observation/${observation.id} points outside the literal Patient fixture.`);
    observationIDsByPatient.get(patientID).push(observation.id);
  }
  const memberIDsByPatient = new Map([...patientsByID.keys()].map(id => [id, []]));
  for (const specimen of specimensByID.values()) {
    const patientID = referenceID(specimen, 'Patient', `Specimen/${specimen.id}`);
    assert(patientsByID.has(patientID), `Specimen/${specimen.id} points outside the literal Patient fixture.`);
    memberIDsByPatient.get(patientID).push(specimen.id);
  }

  const expected = BASIC_NAMED_COHORT_PRESENCE_EXPECTED.map(group => {
    const patientIDs = group.patientIDs;
    assert.equal(patientIDs.length, 1, `${group.groupId} must bind exactly one Patient.`);
    const patientID = patientIDs[0];
    const memberIDs = memberIDsByPatient.get(patientID).slice().sort();
    const observationIDs = observationIDsByPatient.get(patientID).slice().sort();
    assert.deepEqual(memberIDs, [...group.memberIDs].sort(), `${group.groupId} exact explicit Group membership differs from the literal Specimen references.`);
    assert.deepEqual(observationIDs, [...group.observationIDs].sort(), `${group.groupId} exact related Observation IDs differ from the literal subject references.`);
    assert.equal(observationIDs.length > 0, group.presence, `${group.groupId} literal PRESENCE must match its related-ID set.`);
    return { ...group, memberIDs, patientIDs, observationIDs };
  });
  assert.equal(expected[0].memberIDs.length, 1, 'The zero-match group must be explicitly nonempty.');
  assert.equal(expected[0].observationIDs.length, 0, 'The explicit nonempty Group member must have zero related Observations.');
  assert.equal(expected[1].observationIDs.length, 1, 'The positive comparator must have exactly one related Observation.');
  assert.equal([...memberIDsByPatient.values()].flat().length, 2, 'Every literal Specimen must belong to one expected group.');
  assert.equal([...observationIDsByPatient.values()].flat().length, 1, 'Every literal Observation must belong to one expected route result.');
  return { sourceCounts: { patients: 2, specimens: 2, observations: 1 }, groups: expected };
};

export const assertBasicNamedCohortPresenceRows = (rows, { sourceIDColumn, memberColumn, presenceColumn, oracle, phase }) => {
  assert(Array.isArray(rows), `${phase}: native preview rows must be an array.`);
  assert.equal(rows.length, oracle.groups.length, `${phase}: each literal named Group must render exactly once.`);
  const actualByLabel = new Map();
  for (const row of rows) {
    const label = row[oracle.labelColumn];
    assert.equal(typeof label, 'string', `${phase}: native row omitted the Group label.`);
    assert(!actualByLabel.has(label), `${phase}: duplicate Group row ${label}.`);
    actualByLabel.set(label, row);
  }
  return oracle.groups.map(group => {
    const row = actualByLabel.get(group.label);
    assert(row, `${phase}: missing Group row ${group.label}.`);
    const members = row[memberColumn];
    const memberText = Array.isArray(members) ? members.join('; ') : String(members ?? '');
    for (const memberID of group.memberIDs) assert(memberText.includes(memberID), `${phase}: Group ${group.label} lost exact member ${memberID}.`);
    for (const other of oracle.groups.filter(candidate => candidate.groupId !== group.groupId)) {
      for (const foreignID of other.memberIDs) assert(!memberText.includes(foreignID), `${phase}: Group ${group.label} gained foreign member ${foreignID}.`);
    }
    const presence = row[presenceColumn];
    assert.equal(presence, group.presence, `${phase}: ${group.label} must render exact Boolean ${group.presence}.`);
    if (sourceIDColumn) assert(row[sourceIDColumn], `${phase}: row must retain source identity ${sourceIDColumn}.`);
    return { groupId: group.groupId, label: group.label, memberIDs: [...group.memberIDs], observationIDs: [...group.observationIDs], presence };
  });
};
