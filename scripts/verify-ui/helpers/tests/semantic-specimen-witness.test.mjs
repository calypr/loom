import test from 'node:test';
import assert from 'node:assert/strict';
import { parseSpecimenSemanticWitnessReport } from '../semantic-specimen-witness.mjs';

const report = {
  status: 'passed',
  evidence: {
    queryReport: '/tmp/source-read.json',
    explainReport: '/tmp/explain.json',
    querySha256: 'a'.repeat(64),
  },
  witness: {
    project: 'project-a',
    generation: 'generation-a',
    resourceType: 'Specimen',
    id: 'specimen-1',
    sourceKey: 'source-key-1',
    raw: {
      type: { coding: [{ system: 'urn:specimen', code: 'Tumor' }] },
      noncoded: { path: 'subject.reference', value: 'Patient/patient-1' },
    },
  },
};

test('uses the independent retained Specimen witness for exact category and noncoded field expectations', () => {
  const witness = parseSpecimenSemanticWitnessReport(report, {
    project: 'project-a',
    generation: 'generation-a',
  });

  assert.deepEqual(witness.source, {
    project: 'project-a',
    generation: 'generation-a',
    resourceType: 'Specimen',
    id: 'specimen-1',
    sourceKey: 'source-key-1',
  });
  assert.equal(witness.coding.system, 'urn:specimen');
  assert.deepEqual(witness.expectedSemanticValues, ['Tumor']);
  assert.deepEqual(witness.noncoded, { path: 'subject.reference', value: 'Patient/patient-1' });
});

test('rejects a source witness from a different project or generation', () => {
  assert.throws(() => parseSpecimenSemanticWitnessReport(report, {
    project: 'another-project',
    generation: 'generation-a',
  }), /another project/);
  assert.throws(() => parseSpecimenSemanticWitnessReport(report, {
    project: 'project-a',
    generation: 'another-generation',
  }), /another generation/);
});

test('rejects a Specimen witness without an exact Coding or populated noncoded field', () => {
  const withoutCoding = structuredClone(report);
  withoutCoding.witness.raw.type.coding = [{ system: ' ', code: 'Tumor' }];
  assert.throws(() => parseSpecimenSemanticWitnessReport(withoutCoding, {
    project: 'project-a',
    generation: 'generation-a',
  }), /no exact system\/code Coding/);

  const withoutNoncoded = structuredClone(report);
  withoutNoncoded.witness.raw.noncoded.value = undefined;
  assert.throws(() => parseSpecimenSemanticWitnessReport(withoutNoncoded, {
    project: 'project-a',
    generation: 'generation-a',
  }), /must have an independently read value/);
});
