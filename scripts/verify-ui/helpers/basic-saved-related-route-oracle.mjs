import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const MAX_FIXTURE_BYTES = 64 * 1024;

export const BASIC_SAVED_RELATED_ROUTE_A = Object.freeze([
  Object.freeze({
    fromResourceType: 'Patient',
    toResourceType: 'Observation',
    relationship: 'subject_Patient',
    storageDirection: 'INBOUND',
  }),
]);

export const BASIC_SAVED_RELATED_ROUTE_B = Object.freeze([
  Object.freeze({
    fromResourceType: 'Patient',
    toResourceType: 'Group',
    relationship: 'member_entity_Patient',
    storageDirection: 'INBOUND',
  }),
  Object.freeze({
    fromResourceType: 'Group',
    toResourceType: 'Specimen',
    relationship: 'subject_Group',
    storageDirection: 'INBOUND',
  }),
  Object.freeze({
    fromResourceType: 'Specimen',
    toResourceType: 'Observation',
    relationship: 'specimen_Specimen',
    storageDirection: 'INBOUND',
  }),
]);

const readResources = (fixtureDir, resourceType) => {
  const path = join(fixtureDir, resourceType + '.ndjson');
  const stat = statSync(path);
  assert(stat.size > 0 && stat.size <= MAX_FIXTURE_BYTES,
    resourceType + '.ndjson must remain a small, bounded development fixture.');
  const bytes = readFileSync(path);
  const resources = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map((line, index) => {
    try {
      return JSON.parse(line);
    } catch (error) {
      throw new Error(resourceType + '.ndjson line ' + (index + 1) + ' is invalid JSON: ' + error.message);
    }
  });
  assert(resources.length > 0, resourceType + '.ndjson must contain source records.');
  assert(resources.every(resource => resource?.resourceType === resourceType),
    resourceType + '.ndjson rows must retain their declared resource type.');
  const ids = resources.map(resource => resource.id);
  assert(ids.every(id => typeof id === 'string' && id.length > 0),
    resourceType + '.ndjson rows must have non-empty FHIR IDs.');
  assert.equal(new Set(ids).size, ids.length, resourceType + '.ndjson IDs must be unique.');
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    resources,
  };
};

const referenceID = (value, resourceType) => {
  const reference = value?.reference;
  const prefix = resourceType + '/';
  if (typeof reference !== 'string' || !reference.startsWith(prefix)) return null;
  const id = reference.slice(prefix.length);
  return id && !id.includes('/') ? id : null;
};

const compareRows = (left, right) =>
  left.patientID.localeCompare(right.patientID)
  || String(left.observationID ?? '').localeCompare(String(right.observationID ?? ''));

const matchCountsByPatient = (rows, patientIDs) => Object.fromEntries(patientIDs.map(patientID => [
  patientID,
  rows.reduce((count, row) => count + Number(row.patientID === patientID && row.observationID != null), 0),
]));

const routeLabel = route => {
  const first = route[0];
  if (!first) return 'Same record';
  return first.fromResourceType + route.map(edge => {
    let field = edge.relationship;
    for (const resourceType of [edge.fromResourceType, edge.toResourceType]) {
      const suffix = '_' + resourceType;
      if (field.endsWith(suffix)) {
        field = field.slice(0, -suffix.length);
        break;
      }
    }
    return edge.storageDirection === 'INBOUND'
      ? ' <-[' + field + ']- ' + edge.toResourceType
      : ' -[' + field + ']-> ' + edge.toResourceType;
  }).join('');
};

export const routeSignature = route => (route ?? []).map(step => ({
  fromResourceType: step.fromResourceType,
  toResourceType: step.toResourceType,
  relationship: step.relationship,
  storageDirection: step.storageDirection,
}));

export const assertRouteSignature = (actual, expected, phase) => {
  assert.deepEqual(routeSignature(actual), routeSignature(expected),
    phase + ': saved RELATED_EXPAND route differs from the expected fixture relationship path.');
  return routeSignature(actual);
};

export const buildBasicSavedRelatedRouteOracle = ({ patients, observations, groups, specimens }) => {
  for (const [resourceType, resources] of Object.entries({ Patient: patients, Observation: observations, Group: groups, Specimen: specimens })) {
    assert(Array.isArray(resources) && resources.every(resource => resource?.resourceType === resourceType),
      resourceType + ' source records are required to derive the saved route oracle.');
  }

  const patientIDs = patients.map(patient => patient.id).sort();
  assert.deepEqual(patientIDs, ['dev-patient-001', 'dev-patient-002'],
    'The bounded route fixture requires its two literal Patient row identities.');

  const knownPatients = new Set(patientIDs);
  const knownGroups = new Set(groups.map(group => group.id));
  const knownSpecimens = new Set(specimens.map(specimen => specimen.id));

  const routeARows = observations.flatMap(observation => {
    const patientID = referenceID(observation.subject, 'Patient');
    return patientID && knownPatients.has(patientID)
      ? [{ patientID, observationID: observation.id }]
      : [];
  }).sort(compareRows);

  const groupIDsByPatient = new Map(patientIDs.map(id => [id, []]));
  for (const group of groups) {
    for (const member of group.member ?? []) {
      const patientID = referenceID(member?.entity, 'Patient');
      if (patientID && knownPatients.has(patientID)) groupIDsByPatient.get(patientID).push(group.id);
    }
  }

  const specimenIDsByGroup = new Map([...knownGroups].map(id => [id, []]));
  for (const specimen of specimens) {
    const groupID = referenceID(specimen.subject, 'Group');
    if (groupID && knownGroups.has(groupID)) specimenIDsByGroup.get(groupID).push(specimen.id);
  }

  const observationIDsBySpecimen = new Map([...knownSpecimens].map(id => [id, []]));
  for (const observation of observations) {
    const specimenID = referenceID(observation.specimen, 'Specimen');
    if (specimenID && knownSpecimens.has(specimenID)) observationIDsBySpecimen.get(specimenID).push(observation.id);
  }

  const routeBRows = patientIDs.flatMap(patientID => {
    const observationIDs = groupIDsByPatient.get(patientID)
      .flatMap(groupID => specimenIDsByGroup.get(groupID))
      .flatMap(specimenID => observationIDsBySpecimen.get(specimenID))
      .sort();
    return observationIDs.length
      ? observationIDs.map(observationID => ({ patientID, observationID }))
      : [{ patientID, observationID: null }];
  }).sort(compareRows);

  return {
    patientIDs,
    routeA: {
      label: routeLabel(BASIC_SAVED_RELATED_ROUTE_A),
      signature: routeSignature(BASIC_SAVED_RELATED_ROUTE_A),
      rows: routeARows,
      matchCountsByPatient: matchCountsByPatient(routeARows, patientIDs),
    },
    routeB: {
      label: routeLabel(BASIC_SAVED_RELATED_ROUTE_B),
      signature: routeSignature(BASIC_SAVED_RELATED_ROUTE_B),
      rows: routeBRows,
      matchCountsByPatient: matchCountsByPatient(routeBRows, patientIDs),
    },
  };
};

export const readBasicSavedRelatedRouteOracle = fixtureDir => {
  const files = ['Patient', 'Observation', 'Group', 'Specimen']
    .map(resourceType => [resourceType, readResources(fixtureDir, resourceType)]);
  const byType = new Map(files);
  return {
    fixtureDir,
    files: Object.fromEntries(files.map(([resourceType, source]) => [resourceType, {
      path: source.path,
      sha256: source.sha256,
      count: source.resources.length,
    }])),
    ...buildBasicSavedRelatedRouteOracle({
      patients: byType.get('Patient').resources,
      observations: byType.get('Observation').resources,
      groups: byType.get('Group').resources,
      specimens: byType.get('Specimen').resources,
    }),
  };
};
