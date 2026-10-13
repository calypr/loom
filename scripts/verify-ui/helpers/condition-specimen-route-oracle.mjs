import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const sortedRows = rows => [...rows].sort((left, right) =>
  `${left.specimenId ?? ''}\0${left.conditionId ?? ''}\0${left.patientId ?? ''}`
    .localeCompare(`${right.specimenId ?? ''}\0${right.conditionId ?? ''}\0${right.patientId ?? ''}`));

const readResources = (fixtureDir, resourceType) => {
  const path = join(fixtureDir, `${resourceType}.ndjson`);
  const values = readFileSync(path, 'utf8').split(/\r?\n/).filter(line => line.trim()).map((line, index) => {
    let value;
    try { value = JSON.parse(line); }
    catch (error) { throw new Error(`Invalid ${resourceType}.ndjson JSON at line ${index + 1}: ${error.message}`); }
    assert.equal(value.resourceType, resourceType, `${resourceType}.ndjson line ${index + 1} must keep its resource type`);
    assert(typeof value.id === 'string' && value.id.trim(), `${resourceType}.ndjson line ${index + 1} must have an ID`);
    return value;
  });
  assert(values.length > 0, `${resourceType}.ndjson must contain source records`);
  assert.equal(new Set(values.map(value => value.id)).size, values.length,
    `${resourceType}.ndjson must contain unique IDs`);
  return values;
};

export const readConditionSpecimenRouteOracle = fixtureDir => {
  const oracle = JSON.parse(readFileSync(join(fixtureDir, 'route-oracle.json'), 'utf8'));
  const resources = Object.fromEntries(['Condition', 'Observation', 'Patient', 'Specimen']
    .map(type => [type, readResources(fixtureDir, type)]));
  const byID = Object.fromEntries(Object.entries(resources).map(([type, values]) =>
    [type, new Map(values.map(value => [value.id, value]))]));
  const get = (type, id) => {
    const value = byID[type].get(id);
    assert(value, `Independent route oracle references missing ${type}/${id}`);
    return value;
  };
  const ids = type => resources[type].map(value => value.id).sort();
  assert.equal(oracle.startRoot, 'Condition');
  assert.deepEqual(ids('Condition'), [
    'condition-focus-route', 'condition-stage-route', 'condition-subject-route',
  ]);
  for (const condition of resources.Condition) {
    assert.deepEqual(condition.clinicalStatus, { text: 'active' },
      `Condition/${condition.id} must retain the loader-required clinicalStatus value.`);
  }
  assert.deepEqual(ids('Observation'), ['observation-focus-route', 'observation-stage-route']);
  assert.deepEqual(ids('Patient'), [
    'patient-focus-anchor', 'patient-observation-anchor', 'patient-subject-anchor', 'patient-unmatched',
  ]);
  assert.deepEqual(ids('Specimen'), [
    'specimen-focus-route', 'specimen-stage-route', 'specimen-subject-route', 'specimen-unmatched',
  ]);

  const stage = oracle.alternatives.find(choice => choice.key === 'condition-stage-assessment');
  const focus = oracle.alternatives.find(choice => choice.key === 'observation-focus-inbound');
  const patient = oracle.alternatives.find(choice => choice.key === 'condition-patient-subject');
  assert(stage && focus && patient, 'Independent oracle must retain the stage, focus-inbound, and Patient-subject alternatives.');
  assert.equal(get('Condition', stage.conditionId).stage?.[0]?.assessment?.[0]?.reference,
    `Observation/${stage.observationId}`);
  assert.equal(get('Observation', stage.observationId).specimen?.reference, `Specimen/${stage.specimenId}`);
  assert(get('Observation', 'observation-focus-route').focus?.some(reference =>
    reference.reference === `Condition/${focus.conditionId}`),
  'Focus route oracle must be grounded in Observation.focus → Condition source data.');
  assert.equal(get('Observation', 'observation-focus-route').specimen?.reference, `Specimen/${focus.specimenId}`);
  const authoredCondition = get('Condition', patient.conditionId);
  const authoredPatientReference = authoredCondition.subject?.reference;
  assert.equal(authoredPatientReference, `Patient/${patient.patientId}`,
    'The independent Patient-subject path must use the exact Condition.subject → Patient reference.');
  assert.equal(get('Specimen', patient.specimenId).subject?.reference, authoredPatientReference,
    'The Patient-subject path must join the exact Specimen.subject reference.');
  assert.equal(get('Specimen', oracle.unmatched.specimenId).subject?.reference, 'Patient/patient-unmatched');
  assert(resources.Observation.every(value => value.subject === undefined),
    'Observation.subject must remain absent so the Patient-subject path has no competing route.');

  const routeRows = alternative => sortedRows(resources.Specimen.map(specimen => ({
    specimenId: specimen.id,
    conditionId: specimen.id === alternative.specimenId ? alternative.conditionId : null,
    patientId: specimen.id === alternative.specimenId ? alternative.patientId : null,
  })));
  return {
    oracle,
    sourceIDs: Object.fromEntries(Object.keys(resources).map(type => [type, ids(type)])),
    sourceHashes: Object.fromEntries(['Patient', 'Condition', 'Observation', 'Specimen'].map(type => [
      type,
      createHash('sha256').update(readFileSync(join(fixtureDir, `${type}.ndjson`))).digest('hex'),
    ])),
    conditions: resources.Condition.map(condition => ({
      id: condition.id,
      patientId: condition.subject?.reference?.replace(/^Patient\//, ''),
    })),
    patientSubjectRows: routeRows(patient),
    focusRows: routeRows(focus),
    stageRows: routeRows(stage),
  };
};
