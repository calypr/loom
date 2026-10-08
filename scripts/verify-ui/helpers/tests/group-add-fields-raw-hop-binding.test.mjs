import assert from 'node:assert/strict';
import test from 'node:test';
import { rawCdaRelatedHopBinding } from '../../workflows/verify-cda-group-add-fields-browser.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const patientKey = 'Patient/patient-1';
const specimenKey = 'Specimen/specimen-1';
const documents = new Map([
  [patientKey, { id: 'patient-1', project, dataset_generation: generation, resourceType: 'Patient', payload: { resourceType: 'Patient' } }],
  [specimenKey, { id: 'specimen-1', project, dataset_generation: generation, resourceType: 'Specimen', payload: { resourceType: 'Specimen' } }],
  ['Observation/observation-1', { id: 'observation-1', project, dataset_generation: generation, resourceType: 'Observation', payload: { resourceType: 'Observation' } }],
  ['Observation/observation-2', { id: 'observation-2', project, dataset_generation: generation, resourceType: 'Observation', payload: { resourceType: 'Observation' } }],
  ['Condition/not-an-observation', { id: 'not-an-observation', project, dataset_generation: generation, resourceType: 'Condition', payload: { resourceType: 'Condition' } }],
]);

const edge = (from, to, fromType, toType, overrides = {}) => ({
  _from: from,
  _to: to,
  label: 'subject_Patient',
  from_type: fromType,
  to_type: toType,
  project,
  dataset_generation: generation,
  ...overrides,
});

const edges = [
  edge(specimenKey, patientKey, 'Specimen', 'Patient'),
  edge('Observation/observation-1', patientKey, 'Observation', 'Patient'),
  edge('Observation/observation-2', patientKey, 'Observation', 'Patient'),
  edge('Observation/wrong-patient', 'Patient/other-patient', 'Observation', 'Patient'),
  edge('Observation/wrong-project', patientKey, 'Observation', 'Patient', { project: 'another-project' }),
  edge('Observation/wrong-generation', patientKey, 'Observation', 'Patient', { dataset_generation: 'another-generation' }),
  edge('Observation/wrong-label', patientKey, 'Observation', 'Patient', { label: 'another_reference' }),
  edge('Condition/not-an-observation', patientKey, 'Observation', 'Patient'),
  edge(patientKey, 'Observation/reversed-edge', 'Patient', 'Observation'),
];

const matchingRawTargetIDs = (hop, anchor) => {
  const binding = rawCdaRelatedHopBinding(hop);
  return edges
    .filter(candidate => candidate[binding.anchorEndpoint] === anchor &&
      candidate.label === hop.label && candidate.from_type === binding.fromType &&
      candidate.to_type === binding.toType && candidate.project === project &&
      candidate.dataset_generation === generation &&
      candidate[binding.targetEndpoint]?.startsWith(`${hop.to}/`))
    .map(candidate => documents.get(candidate[binding.targetEndpoint]))
    .filter(document => document?.project === project && document.dataset_generation === generation &&
      document.resourceType === hop.to && document.payload.resourceType === hop.to)
    .map(document => document.id)
    .sort();
};

test('raw CDA oracle binds outbound Specimen→Patient traversal to the stored edge direction', () => {
  const hop = { from: 'Specimen', to: 'Patient', label: 'subject_Patient', direction: 'OUTBOUND' };

  assert.deepEqual(rawCdaRelatedHopBinding(hop), {
    anchorEndpoint: '_from', targetEndpoint: '_to', fromType: 'Specimen', toType: 'Patient',
  });
  assert.deepEqual(matchingRawTargetIDs(hop, specimenKey), ['patient-1']);
  assert.deepEqual(matchingRawTargetIDs(hop, 'Specimen/other-specimen'), []);
});

test('raw CDA oracle reverses stored Observation→Patient types for inbound Patient→Observation traversal', () => {
  const hop = { from: 'Patient', to: 'Observation', label: 'subject_Patient', direction: 'INBOUND' };

  assert.deepEqual(rawCdaRelatedHopBinding(hop), {
    anchorEndpoint: '_to', targetEndpoint: '_from', fromType: 'Observation', toType: 'Patient',
  });
  assert.deepEqual(matchingRawTargetIDs(hop, patientKey), ['observation-1', 'observation-2']);
  assert.deepEqual(matchingRawTargetIDs(hop, 'Patient/other-patient'), []);
});

test('raw CDA oracle rejects unsupported relationship directions', () => {
  assert.throws(() => rawCdaRelatedHopBinding({ from: 'Patient', to: 'Observation', direction: 'SIDEWAYS' }),
    /Unsupported raw CDA relationship direction/);
});
