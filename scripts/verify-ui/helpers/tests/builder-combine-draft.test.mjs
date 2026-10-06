import assert from 'node:assert/strict';
import test from 'node:test';
import {
  appendGroupedCounts,
  builderDraftStateEvidence,
  canceledDraftEvidence,
  currentDraftSourceEvidence,
  groupCounts,
  groupedPivotRows,
  joinGroupPivotRows,
  joinGroupedCounts,
  joinPivotRows,
  patientDerivedAppendOracle,
  sourceRecompileEvidence,
  uniqueValueFieldProjection,
  workspaceOutputOption,
} from '../builder-combine-draft-helpers.mjs';

const source = (outputId, kinds = ['GROUP']) => ({
  output: { id: outputId },
  construction: { steps: kinds.map((kind, index) => ({ id: outputId + '-' + index, operation: { kind } })) },
});

test('fresh Builder scope accepts only the exact empty pre-table state, then requires a versioned draft', () => {
  const empty = { workspace: null, draftVersion: 0, draftDigest: '' };
  assert.deepEqual(builderDraftStateEvidence(empty, 'empty'), {
    ok: true,
    expectedState: 'empty',
    actualState: 'empty',
    empty: true,
    draft: false,
    workspaceDocumentCount: null,
    draftVersion: 0,
    draftDigest: '',
  });
  assert.equal(builderDraftStateEvidence(empty, 'draft').ok, false);

  const draft = { workspace: { documents: [{ output: { id: 'table-1' } }] }, draftVersion: 1, draftDigest: 'draft-digest-1' };
  assert.equal(builderDraftStateEvidence(draft, 'draft').ok, true);
  assert.equal(builderDraftStateEvidence(draft, 'empty').ok, false);

  for (const malformed of [
    { workspace: null, draftVersion: 0, draftDigest: 'unexpected-digest' },
    { workspace: null, draftVersion: 1, draftDigest: 'draft-digest-1' },
    { workspace: { documents: [] }, draftVersion: 1, draftDigest: 'draft-digest-1' },
    { workspace: { documents: [{ output: { id: 'table-1' } }] }, draftVersion: 1, draftDigest: '' },
  ]) {
    assert.equal(builderDraftStateEvidence(malformed, 'empty').ok, false, JSON.stringify(malformed));
    assert.equal(builderDraftStateEvidence(malformed, 'draft').ok, false, JSON.stringify(malformed));
  }
});

test('stable Group keys are resolved by exact VALUE field projection identity and reject duplicate projections', () => {
  const document = { columns: [
    { columnId: 'column_exact_id', column: 'fhir_id', label: 'Observation ID', occurrenceId: 'base', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
    { columnId: 'column_other_id_label', column: 'business_id', label: 'Observation ID', occurrenceId: 'base', source: { kind: 'field', field: { path: 'identifier', projectionMode: 'VALUE' } } },
  ] };
  assert.deepEqual(uniqueValueFieldProjection(document, 'id'), {
    ok: true,
    fieldPath: 'id',
    occurrenceId: 'base',
    projectionMode: 'VALUE',
    matches: [{
      columnId: 'column_exact_id', column: 'fhir_id', label: 'Observation ID', occurrenceId: 'base',
      fieldPath: 'id', projectionMode: 'VALUE',
    }],
    binding: {
      columnId: 'column_exact_id', column: 'fhir_id', label: 'Observation ID', occurrenceId: 'base',
      fieldPath: 'id', projectionMode: 'VALUE',
    },
  });
  const duplicate = uniqueValueFieldProjection({ columns: [
    document.columns[0],
    { ...document.columns[0], columnId: 'column_duplicate_id', column: 'second_fhir_id' },
  ] }, 'id');
  assert.equal(duplicate.ok, false);
  assert.equal(duplicate.binding, null);
  assert.deepEqual(duplicate.matches.map((column) => column.columnId), ['column_exact_id', 'column_duplicate_id']);
  assert.equal(uniqueValueFieldProjection(document, 'id', 'related-observation').ok, false);
});

test('current-draft evidence requires exact distinct saved workspace outputs and excludes revisions', () => {
  const inputs = [
    { kind: 'WORKSPACE_OUTPUT', outputId: 'group-a' },
    { kind: 'WORKSPACE_OUTPUT', outputId: 'group-b' },
  ];
  const evidence = currentDraftSourceEvidence({
    inputs,
    expectedOutputIDs: ['group-a', 'group-b'],
    sourceDocuments: [source('group-a'), source('group-b')],
  });
  assert.equal(evidence.ok, true);
  assert.equal(workspaceOutputOption('group-a'), '["WORKSPACE_OUTPUT","group-a"]');

  assert.equal(currentDraftSourceEvidence({
    inputs: [{ kind: 'TABLE_REVISION', tableId: 't', revisionId: 'r', outputId: 'group-a' }, inputs[1]],
    expectedOutputIDs: ['group-a', 'group-b'],
    sourceDocuments: [source('group-a'), source('group-b')],
  }).ok, false);
  assert.equal(currentDraftSourceEvidence({
    inputs: [inputs[0], inputs[0]], expectedOutputIDs: ['group-a', 'group-b'],
    sourceDocuments: [source('group-a'), source('group-b')],
  }).ok, false);
  assert.equal(currentDraftSourceEvidence({
    inputs, expectedOutputIDs: ['group-a', 'group-b'], sourceDocuments: [source('group-a'), source('group-b')],
    publishedOutputIDs: ['group-a'],
  }).ok, false);
  assert.equal(currentDraftSourceEvidence({
    inputs, expectedOutputIDs: ['group-a', 'group-b'], sourceDocuments: [source('group-a'), source('group-b', [])],
  }).ok, false);
});

test('Cancel evidence compares full workspace, CAS, and authorization generation scope', () => {
  const before = { workspace: { documents: [{ id: 'a' }, { id: 'b' }] }, draftVersion: 4, draftDigest: 'digest-a', catalog: { generation: 'gen', authorizationScopeDigest: 'scope' } };
  assert.equal(canceledDraftEvidence(before, structuredClone(before)).ok, true);
  assert.equal(canceledDraftEvidence(before, { ...structuredClone(before), draftVersion: 5 }).ok, false);
  assert.equal(canceledDraftEvidence(before, { ...structuredClone(before), workspace: { documents: [{ id: 'a' }] } }).ok, false);
  assert.equal(canceledDraftEvidence(before, { ...structuredClone(before), catalog: { generation: 'gen', authorizationScopeDigest: 'other' } }).ok, false);
});

test('raw independent rows produce exact grouped Join and three-source APPEND rows', () => {
  const observations = [{ status: 'final' }, { status: 'final' }, { status: 'preliminary' }, { status: 'unknown' }];
  const reports = [{ status: 'final' }, { status: 'final' }, { status: 'preliminary' }];
  const patients = [{ gender: 'female' }];
  const observationGroups = groupCounts(observations, 'status');
  const reportGroups = groupCounts(reports, 'status');
  const patientDerivedCounts = groupCounts(patients, 'gender').map(([gender, count]) => [gender, count + 1]);
  assert.deepEqual(observationGroups, [['final', 2], ['preliminary', 1], ['unknown', 1]]);
  assert.deepEqual(joinGroupedCounts(observationGroups, reportGroups), [['final', 2, 'final', 2], ['preliminary', 1, 'preliminary', 1]]);
  assert.deepEqual(joinGroupedCounts(observationGroups, reportGroups, 'LEFT'), [
    ['final', 2, 'final', 2], ['preliminary', 1, 'preliminary', 1], ['unknown', 1, '—', '—'],
  ]);
  assert.deepEqual(appendGroupedCounts([
    { keyRows: observationGroups.map(([value]) => [value]), countRows: observationGroups },
    { keyRows: reportGroups.map(([value]) => [value]), countRows: reportGroups },
    { keyRows: [['female']], countRows: patientDerivedCounts },
  ]), [
    ['final', 2], ['preliminary', 1], ['unknown', 1],
    ['final', 2], ['preliminary', 1], ['female', 2],
  ]);
});

test('independent raw records predict Group→Pivot→Join including an unmatched identity', () => {
  const observations = [
    { id: 'a', status: 'final' }, { id: 'b', status: 'final' },
    { id: 'c', status: 'preliminary' }, { id: 'd', status: 'unknown' },
  ];
  const reports = [{ id: 'a', status: 'final' }, { id: 'b', status: 'final' }, { id: 'c', status: 'preliminary' }];
  const categories = ['final', 'preliminary', 'unknown'];
  const left = groupedPivotRows(observations, 'id', 'status', categories);
  const right = groupedPivotRows(reports, 'id', 'status', categories);
  assert.deepEqual(left, [['a', 1, null, null], ['b', 1, null, null], ['c', null, 1, null], ['d', null, null, 1]]);
  assert.deepEqual(joinPivotRows(left, right), [['a', 1, null, null, 'a', 1, null, null], ['b', 1, null, null, 'b', 1, null, null], ['c', null, 1, null, 'c', null, 1, null]]);
});

test('mixed Group and Group→Pivot siblings preserve the shared key and left-only identity', () => {
  const observations = [
    { id: 'a', valueInteger: 10 }, { id: 'b', valueInteger: 20 },
    { id: 'c', valueInteger: 30 }, { id: 'd', valueInteger: 40 },
  ];
  const reports = [
    { id: 'a', status: 'final' }, { id: 'b', status: 'final' }, { id: 'c', status: 'preliminary' },
  ];
  const observationGroups = groupCounts(observations, 'id');
  const reportPivot = groupedPivotRows(reports, 'id', 'status', ['final', 'preliminary']);
  assert.deepEqual(joinGroupPivotRows(observationGroups, reportPivot), [
    ['a', 1, 'a', 1, null], ['b', 1, 'b', 1, null], ['c', 1, 'c', null, 1],
  ]);
  assert.deepEqual(joinGroupPivotRows(observationGroups, reportPivot, 'LEFT'), [
    ['a', 1, 'a', 1, null], ['b', 1, 'b', 1, null], ['c', 1, 'c', null, 1], ['d', 1, null, null, null],
  ]);
});

test('source edits must advance draft CAS, retain exact workspace binding, and replace preview receipt', () => {
  const before = { draftVersion: 3, draftDigest: 'one' };
  const after = {
    draftVersion: 4,
    draftDigest: 'two',
    workspace: { documents: [{ output: { id: 'target' }, construction: { steps: [{ inputs: [{ kind: 'WORKSPACE_OUTPUT', outputId: 'source' }] }] } }] },
  };
  assert.deepEqual(sourceRecompileEvidence({ before, after, targetOutputId: 'target', sourceOutputId: 'source', oldReceipt: 'receipt-1', newReceipt: 'receipt-2' }), {
    ok: true,
    sourceStillBound: true,
    draftAdvanced: true,
    receiptChanged: true,
    beforeDraftVersion: 3,
    afterDraftVersion: 4,
    oldReceipt: 'receipt-1',
    newReceipt: 'receipt-2',
  });
  assert.equal(sourceRecompileEvidence({ before, after: { ...after, draftVersion: 3 }, targetOutputId: 'target', sourceOutputId: 'source', oldReceipt: 'receipt-1', newReceipt: 'receipt-2' }).ok, false);
  assert.equal(sourceRecompileEvidence({ before, after, targetOutputId: 'target', sourceOutputId: 'source', oldReceipt: 'same', newReceipt: 'same' }).ok, false);
});

test('Group→DERIVE edit changes only the Patient-derived value in the independent APPEND oracle', () => {
  const fixture = {
    observations: [{ status: 'final' }, { status: 'final' }, { status: 'preliminary' }, { status: 'unknown' }],
    reports: [{ status: 'final' }, { status: 'final' }, { status: 'preliminary' }],
    patients: [{ gender: 'female' }],
  };
  const before = patientDerivedAppendOracle({ ...fixture, offset: 1 });
  const after = patientDerivedAppendOracle({ ...fixture, offset: 2 });
  assert.deepEqual(before.patientRows, [['female', '1', '2']]);
  assert.deepEqual(after.patientRows, [['female', '1', '3']]);
  assert.deepEqual(before.appendRows, [
    ['final', '2'], ['preliminary', '1'], ['unknown', '1'],
    ['final', '2'], ['preliminary', '1'], ['female', '2'],
  ]);
  assert.deepEqual(after.appendRows, [
    ['final', '2'], ['preliminary', '1'], ['unknown', '1'],
    ['final', '2'], ['preliminary', '1'], ['female', '3'],
  ]);
  assert.deepEqual(before.appendRows.map(([key]) => key), after.appendRows.map(([key]) => key));
  assert.equal(before.appendRows.filter((row, index) => row[1] !== after.appendRows[index]?.[1]).length, 1);
  assert.throws(() => patientDerivedAppendOracle({ ...fixture, offset: 1.5 }), /integer offset/);
});
