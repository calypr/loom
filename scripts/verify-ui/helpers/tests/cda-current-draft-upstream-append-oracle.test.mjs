import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CDA_UPSTREAM_APPEND_RESOURCES,
  CDA_UPSTREAM_APPEND_SOURCES,
  CdaUpstreamAppendWitnessUnavailable,
  MAX_CDA_UPSTREAM_APPEND_SCAN,
  assertCdaUpstreamAppendReread,
  cdaUpstreamAppendRereadQuery,
  cdaUpstreamAppendScanQuery,
  prepareCdaUpstreamAppendOracle,
} from '../cda-current-draft-upstream-append-oracle.mjs';

const project = 'loom-cda-oracle';
const generation = 'cda-fhir-v1';
const row = (resourceType, _id, id, fieldPresent, fieldValue) => ({
  _id: `${resourceType}/${_id}`,
  id,
  project,
  generation,
  resourceType,
  fieldPresent,
  fieldValue,
});
const scans = () => ({
  Patient: [
    row('Patient', 'patient-doc-02', 'patient-b', true, 'patient-b'),
    row('Patient', 'patient-doc-01', 'patient-a', true, 'patient-a'),
  ],
  Observation: [
    row('Observation', 'obs-doc-04', 'obs-final-4', true, 'final'),
    row('Observation', 'obs-doc-02', 'obs-final-2', true, 'final'),
    row('Observation', 'obs-doc-01', 'obs-final-1', true, 'final'),
    row('Observation', 'obs-doc-03', 'obs-final-3', true, 'final'),
    row('Observation', 'obs-doc-05', 'obs-preliminary', true, 'preliminary'),
  ],
});

test('bounded scans cover exact Patient.id and Observation.status fields with FHIR and Arango identities separate', () => {
  assert.deepEqual(CDA_UPSTREAM_APPEND_RESOURCES.map(({ resourceType, fieldPath }) => [resourceType, fieldPath]), [
    ['Patient', 'id'], ['Observation', 'status'],
  ]);
  assert.deepEqual(CDA_UPSTREAM_APPEND_SOURCES.map(({ sourceKey, resourceType }) => [sourceKey, resourceType]), [
    ['observation-left', 'Observation'], ['observation-right', 'Observation'], ['patient-id', 'Patient'],
  ]);
  for (const resource of CDA_UPSTREAM_APPEND_RESOURCES) {
    const query = cdaUpstreamAppendScanQuery({ ...resource, project, generation });
    assert.ok(query.includes(`r.project == ${JSON.stringify(project)}`));
    assert.match(query, /r\.dataset_generation ==/);
    assert.match(query, /r\.payload\.resourceType ==/);
    assert.match(query, /SORT r\._id LIMIT 2000/);
    assert.match(query, /fieldPresent:HAS\(r\.payload/);
  }
  const exact = cdaUpstreamAppendRereadQuery({
    resourceType: 'Patient', fieldPath: 'id', project, generation,
    documentIDs: ['Patient/patient-doc-01', 'Patient/patient-doc-02'],
  });
  assert.match(exact, /r\._id IN \["Patient\/patient-doc-01","Patient\/patient-doc-02"\]/);
  assert.match(exact, /r\.id,/);
  assert.notEqual('Patient/patient-doc-01', 'patient-a');
  assert.equal(MAX_CDA_UPSTREAM_APPEND_SCAN, 2_000);
});

test('oracle derives two disjoint Observation pairs, Patient ID counts, and duplicate-preserving APPEND rows', () => {
  const oracle = prepareCdaUpstreamAppendOracle(scans(), { project, generation });
  const left = oracle.sources['observation-left'];
  const right = oracle.sources['observation-right'];
  const patient = oracle.sources['patient-id'];
  assert.deepEqual(left.map(item => item._id), ['Observation/obs-doc-01', 'Observation/obs-doc-02']);
  assert.deepEqual(right.map(item => item._id), ['Observation/obs-doc-03', 'Observation/obs-doc-04']);
  assert.equal(new Set([...left, ...right].map(item => item._id)).size, 4);
  assert(left.every(item => item.resourceType === 'Observation' && item.fieldValue === 'final'));
  assert.deepEqual(patient.map(item => item.id), ['patient-a', 'patient-b']);
  assert(patient.every(item => item.id !== item._id && item.key === item.id));
  assert.deepEqual(oracle.grouped['observation-left'], [['final', 2]]);
  assert.deepEqual(oracle.grouped['observation-right'], [['final', 2]]);
  assert.deepEqual(oracle.grouped['patient-id'], [['patient-a', 1], ['patient-b', 1]]);
  assert.deepEqual(oracle.patientDerived.plusOne, [['patient-a', 1, 2], ['patient-b', 1, 2]]);
  assert.deepEqual(oracle.patientDerived.plusTwo, [['patient-a', 1, 3], ['patient-b', 1, 3]]);
  assert.deepEqual(oracle.append.plusOne, [
    ['final', '2'], ['final', '2'], ['patient-a', '2'], ['patient-b', '2'],
  ]);
  assert.deepEqual(oracle.append.plusTwo, [
    ['final', '2'], ['final', '2'], ['patient-a', '3'], ['patient-b', '3'],
  ]);
  assert.equal(oracle.append.duplicateStatusCount, 2);
  assert.equal(oracle.resources.find(item => item.resourceType === 'Patient').eligibleRows, 2);
  assert.equal(oracle.resources.find(item => item.resourceType === 'Observation').selectedFinalStatusRows, 4);
  assert.equal(oracle.selected['observation-left'].length, 2);
  assert.equal(oracle.selected['observation-right'].length, 2);
  assert.equal(oracle.selected['patient-id'].length, 2);
  assertCdaUpstreamAppendReread(
    [scans().Patient[1], scans().Patient[0]], oracle.exactExpected['patient-id'],
    { project, generation, resourceType: 'Patient' },
  );
});

test('oracle reports honest bounded unavailability without four final Observations or two Patient IDs', () => {
  const input = scans();
  input.Patient = [row('Patient', 'patient-doc-01', 'patient-a', true, 'patient-a')];
  input.Observation = input.Observation.filter(item => item.fieldValue !== 'final').concat(
    row('Observation', 'one', 'obs-only', true, 'final'),
  );
  assert.throws(() => prepareCdaUpstreamAppendOracle(input, { project, generation }), error => {
    assert(error instanceof CdaUpstreamAppendWitnessUnavailable);
    assert.match(error.message, /Patient\.id and disjoint Observation final-status witnesses/);
    assert.equal(error.evidence.scanLimitPerResource, 2_000);
    assert.equal(error.evidence.resources.length, 2);
    assert.deepEqual(error.evidence.reason, [
      'the bounded Patient.id scan has fewer than two eligible unique FHIR IDs',
      'the bounded Observation.status scan has fewer than four final rows for two disjoint pairs',
    ]);
    return true;
  });
});

test('oracle rejects a row outside the exact project or generation', () => {
  const input = scans();
  input.Patient[0] = { ...input.Patient[0], generation: 'other-generation' };
  assert.throws(() => prepareCdaUpstreamAppendOracle(input, { project, generation }), /out-of-scope/);
});
