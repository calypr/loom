import assert from 'node:assert/strict';
import test from 'node:test';
import { membershipCompiledGroupKeyEvidence, prepareCdaMembershipOracle } from '../cda-current-draft-membership-oracle.mjs';
import { membershipOutputNullabilityEvidence } from '../builder-combine-helpers.mjs';

test('overlapping exact Observation populations map INCLUDE to the intersection and EXCLUDE to the left remainder', () => {
  const project = 'cda_membership_fixture';
  const generation = 'cda-generation-001';
  const rows = [
    { _id: 'observations/003', id: 'observation-c', project, generation, resourceType: 'Observation' },
    { _id: 'observations/002', id: 'observation-b', project, generation, resourceType: 'Observation' },
    { _id: 'observations/001', id: 'observation-a', project, generation, resourceType: 'Observation' },
  ];

  const oracle = prepareCdaMembershipOracle(rows, { project, generation });

  assert.deepEqual(oracle.leftIDs, ['observation-a', 'observation-b']);
  assert.deepEqual(oracle.rightIDs, ['observation-a', 'observation-c']);
  assert.deepEqual(oracle.includeIDs, ['observation-a']);
  assert.deepEqual(oracle.excludeIDs, ['observation-b']);
  assert.deepEqual(oracle.selected, {
    a: { _id: 'observations/001', id: 'observation-a' },
    b: { _id: 'observations/002', id: 'observation-b' },
    c: { _id: 'observations/003', id: 'observation-c' },
  });
});


test('published GROUP ID nullability follows compiled schema when the authored key omits nullable', () => {
  const authoredKey = { id: 'group-key-id-001', name: 'col_observation_id_001', label: 'Observation ID' };
  const step = {
    operation: { kind: 'GROUP', group: { keys: [{ outputColumnId: authoredKey.id }] } },
    outputs: [authoredKey],
  };
  const compiledPreviewColumn = {
    column: authoredKey.name, label: authoredKey.label, logicalType: 'string', nullable: true, shape: 'scalar',
  };
  const publishedColumn = {
    name: authoredKey.name, label: authoredKey.label, type: 'string', semanticPath: 'id', nullable: true, repeated: false,
  };

  assert.equal(Object.hasOwn(authoredKey, 'nullable'), false);
  assert.notEqual(authoredKey.id, authoredKey.name);
  const compiledEvidence = membershipCompiledGroupKeyEvidence({
    step, previewColumns: [compiledPreviewColumn], expectedLabel: 'Observation ID',
  });
  assert.equal(compiledEvidence.ok, true);
  assert.equal(compiledEvidence.groupKeyOutput.id, 'group-key-id-001');
  assert.equal(compiledEvidence.compiledColumn.column, 'col_observation_id_001');
  assert.equal(membershipCompiledGroupKeyEvidence({
    step, previewColumns: [{ ...compiledPreviewColumn, column: authoredKey.id }], expectedLabel: 'Observation ID',
  }).ok, false, 'the compiled preview resolves the authored output name, not its stable output ID');
  assert.equal(membershipCompiledGroupKeyEvidence({
    step: { ...step, outputs: [{ ...authoredKey }, { ...authoredKey }] },
    previewColumns: [compiledPreviewColumn], expectedLabel: 'Observation ID',
  }).ok, false, 'a duplicated stable output ID must not pick an ambiguous compiled column');
  assert.equal(membershipOutputNullabilityEvidence(publishedColumn, compiledPreviewColumn.nullable).ok, true);
  assert.equal(membershipOutputNullabilityEvidence(
    { ...publishedColumn, nullable: Boolean(authoredKey.nullable) }, compiledPreviewColumn.nullable,
  ).ok, false, 'an omitted authored bool must not replace the compiled GROUP preview contract');
});
