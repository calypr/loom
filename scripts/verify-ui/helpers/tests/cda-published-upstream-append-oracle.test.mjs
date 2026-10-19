import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_PUBLISHED_APPEND_RESOURCES,
  CDA_PUBLISHED_APPEND_SOURCES,
  CdaPublishedAppendWitnessUnavailable,
  MAX_CDA_PUBLISHED_APPEND_SCAN,
  assertCdaPublishedAppendReread,
  cdaPublishedAppendRereadQuery,
  cdaPublishedAppendScanQuery,
  prepareCdaPublishedAppendOracle,
} from '../cda-published-upstream-append-oracle.mjs';

const project = 'loom-published-cda-oracle';
const generation = 'cda-fhir-v1';
const row = (resourceType, key, id, fieldPresent, fieldValue) => ({
  _id: `${resourceType}/${key}`, id, project, generation, resourceType, fieldPresent, fieldValue,
});
const scans = () => ({
  Observation: [
    row('Observation', 'obs-04', 'obs-four', true, 'final'),
    row('Observation', 'obs-02', 'obs-two', true, 'final'),
    row('Observation', 'obs-01', 'obs-one', true, 'final'),
    row('Observation', 'obs-03', 'obs-three', true, 'final'),
    row('Observation', 'obs-preliminary', 'obs-preliminary', true, 'preliminary'),
  ],
  Patient: [
    row('Patient', 'patient-02', 'patient-b', true, 'patient-b'),
    row('Patient', 'patient-03', 'patient-c', true, 'different-payload-id'),
    row('Patient', 'patient-01', 'patient-a', true, 'patient-a'),
  ],
});

test('bounded scans scope raw CDA records and reread by exact Arango keys', () => {
  assert.deepEqual(CDA_PUBLISHED_APPEND_RESOURCES.map(({ resourceType, valueField }) => [resourceType, valueField]), [
    ['Observation', 'status'], ['Patient', 'id'],
  ]);
  assert.deepEqual(CDA_PUBLISHED_APPEND_SOURCES.map(({ sourceKey, resourceType, valueField }) => [sourceKey, resourceType, valueField]), [
    ['observation-left', 'Observation', 'status'],
    ['observation-right', 'Observation', 'status'],
    ['patient', 'Patient', 'id'],
  ]);
  for (const { resourceType } of CDA_PUBLISHED_APPEND_RESOURCES) {
    const query = cdaPublishedAppendScanQuery({ project, generation, resourceType });
    assert.ok(query.includes(`r.project == ${JSON.stringify(project)}`));
    assert.ok(query.includes(`r.dataset_generation == ${JSON.stringify(generation)}`));
    assert.ok(query.includes(`r.payload.resourceType == ${JSON.stringify(resourceType)}`));
    assert.match(query, /SORT r\._id LIMIT 2000/);
    assert.match(query, /fieldPresent:HAS\(r\.payload/);
  }
  const reread = cdaPublishedAppendRereadQuery({
    project, generation, resourceType: 'Patient', documentIDs: ['Patient/patient-01', 'Patient/patient-02'],
  });
  assert.match(reread, /r\._id IN \["Patient\/patient-01","Patient\/patient-02"\]/);
  assert.match(reread, /r\.id,/);
  assert.notEqual('Patient/patient-01', 'patient-a');
  assert.equal(MAX_CDA_PUBLISHED_APPEND_SCAN, 2_000);
});

test('oracle selects disjoint Observation pairs and matching Patient payload IDs, then preserves APPEND rows and null padding', () => {
  const oracle = prepareCdaPublishedAppendOracle(scans(), { project, generation });
  assert.deepEqual(oracle.sources['observation-left'].map(member => member._id), [
    'Observation/obs-01', 'Observation/obs-02',
  ]);
  assert.deepEqual(oracle.sources['observation-right'].map(member => member._id), [
    'Observation/obs-03', 'Observation/obs-04',
  ]);
  assert.deepEqual(oracle.sources.patient.map(member => member.id), ['patient-a', 'patient-b']);
  assert(oracle.sources.patient.every(member => member.id === member.fieldValue));
  assert.deepEqual(oracle.sourceReferences.patient, [
    { project, generation, resourceType: 'Patient', id: 'patient-a' },
    { project, generation, resourceType: 'Patient', id: 'patient-b' },
  ]);
  assert(oracle.sourceReferences.patient.every(reference => !Object.hasOwn(reference, '_id')));
  assert.deepEqual(oracle.append.rows, [
    ['obs-one', 'final'], ['obs-two', 'final'], ['obs-three', 'final'], ['obs-four', 'final'],
    ['patient-a', null], ['patient-b', null],
  ]);
  assert.equal(oracle.append.rows.length, 6);
  assert.equal(oracle.append.duplicateFinalStatusCount, 4);
  assert.deepEqual(oracle.resources.map(resource => [resource.resourceType, resource.returnedRows]), [
    ['Observation', 5], ['Patient', 3],
  ]);
  assertCdaPublishedAppendReread(
    [scans().Patient[2], scans().Patient[0]], oracle.exactExpected.patient,
    { project, generation, resourceType: 'Patient' },
  );
});

test('oracle reports bounded unavailability when either source population cannot be formed', () => {
  const input = scans();
  input.Observation = input.Observation.filter(member => member.fieldValue !== 'final')
    .concat(row('Observation', 'obs-only', 'obs-only', true, 'final'));
  input.Patient = [row('Patient', 'patient-mismatch', 'patient-real-id', true, 'other-payload-id')];
  assert.throws(() => prepareCdaPublishedAppendOracle(input, { project, generation }), error => {
    assert(error instanceof CdaPublishedAppendWitnessUnavailable);
    assert.match(error.message, /disjoint Observation and Patient populations/);
    assert.deepEqual(error.evidence.reason, [
      'the bounded Observation.status scan has fewer than four final rows for two disjoint pairs',
      'the bounded Patient.id scan has fewer than two payload IDs matching their top-level FHIR IDs',
    ]);
    return true;
  });
});

test('oracle rejects out-of-scope records, invalid IDs, and duplicate raw identities', () => {
  const wrongScope = scans();
  wrongScope.Patient[0] = { ...wrongScope.Patient[0], generation: 'other-generation' };
  assert.throws(() => prepareCdaPublishedAppendOracle(wrongScope, { project, generation }), /out-of-scope/);

  const duplicateID = scans();
  duplicateID.Observation[0] = { ...duplicateID.Observation[0], id: duplicateID.Observation[1].id };
  assert.throws(() => prepareCdaPublishedAppendOracle(duplicateID, { project, generation }), /repeated a FHIR id/);

  const changed = scans();
  assert.throws(() => assertCdaPublishedAppendReread(
    [{ ...changed.Observation[0], fieldValue: 'preliminary' }], [changed.Observation[0]],
    { project, generation, resourceType: 'Observation' },
  ), /differs from the bounded witness/);
});
