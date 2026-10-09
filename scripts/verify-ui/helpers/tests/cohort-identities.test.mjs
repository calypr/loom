import assert from 'node:assert/strict';
import test from 'node:test';
import { assertCohortMemberFieldBinding, assertCohortPatientIDColumns } from '../cohort-identities.mjs';

const idSource = { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } };

const cdaDocument = () => ({
  columns: [
    {
      columnId: 'source_5d01133eea8dbceb2d782092',
      column: 'col_1757c07475e6b63d648a40f8',
      label: 'Patient ID',
      logicalType: 'string',
      occurrenceId: 'base',
      source: idSource,
    },
    {
      columnId: 'member_9a8d0d0e',
      column: 'col_patient_member_id',
      label: 'Patient ID (cohort member)',
      logicalType: 'string',
      occurrenceId: 'base',
      source: idSource,
    },
  ],
  rows: { groups: { rowValues: [{ columnId: 'member_9a8d0d0e', policy: 'ALL' }] } },
});

test('keeps CDA legacy Patient ID identity outside cohort member bindings', () => {
  const document = cdaDocument();
  const result = assertCohortPatientIDColumns(document, document.columns[0]);
  assert.equal(result.legacy.columnId, 'source_5d01133eea8dbceb2d782092');
  assert.equal(result.member.columnId, 'member_9a8d0d0e');
  assert.equal(result.binding.policy, 'ALL');
});

test('supports the synthetic legacy Patient ID shape without a columnId', () => {
  const legacy = {
    column: 'patient_id',
    label: 'Patient ID',
    logicalType: 'string',
    source: idSource,
  };
  const member = {
    columnId: 'member_patient_id',
    column: 'patient_id_member',
    label: 'Patient ID member',
    logicalType: 'string',
    source: idSource,
  };
  const document = { columns: [legacy, member], rows: { groups: { rowValues: [{ columnId: member.columnId, policy: 'ALL' }] } } };
  const result = assertCohortPatientIDColumns(document, legacy);
  assert.equal(result.legacy.column, 'patient_id');
  assert.equal(result.member.column, 'patient_id_member');
});

test('rejects cohort membership accidentally bound to the legacy Patient ID', () => {
  const document = cdaDocument();
  document.rows.groups.rowValues.push({ columnId: document.columns[0].columnId, policy: 'ALL' });
  assert.throws(() => assertCohortPatientIDColumns(document, document.columns[0]), /must not be bound/);
});

test('rejects a missing member binding after the UI operation', () => {
  const document = cdaDocument();
  document.rows.groups.rowValues = [];
  assert.throws(() => assertCohortPatientIDColumns(document, document.columns[0]), /exactly one distinct/);
});

const cohortRevisionId = 'grouprev_cf467f0bfc6f7dda2f5568fd963333f9a495101424b660b19e817d02b2027c3b';
const selectionRevisionId = 'selection_30228fde63d4b7c29ae74361a9ab502366e02a62aba50c7ffee8c0f09e2fc706';
const resourceTypeColumnId = 'source_d44aab344dd675e370dd19b7';
// Observed fields copied from domain.savedFieldDocument in the retained
// standalone-cohort-fields-domain-json-f9677b4ebb0c60e8277a6a2a7ad4d5a3a41abbe4.json attachment.
// This excerpt intentionally omits saved-document fields not needed by this helper contract.
const capturedSavedFieldDocumentExcerpt = () => ({
  rows: {
    kind: 'GROUPS',
    groups: {
      source: { kind: 'EXPLICIT', explicit: { revisionId: cohortRevisionId, unassignedMemberPolicy: 'GROUP_AS_UNASSIGNED' } },
      rowValues: [{ columnId: resourceTypeColumnId, policy: 'ALL' }],
    },
  },
  population: { selectionRevisionId, route: [] },
  columns: [
    {
      columnId: 'source_87d164314658e200dcaf057c',
      column: 'col_030b296fc06b98e3b096b4a5',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
    },
    {
      columnId: resourceTypeColumnId,
      column: 'col_6d636c73273b8490887afcf0',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'resourceType', projectionMode: 'VALUE' } },
    },
  ],
});

const validateResourceTypeBinding = document => {
  return assertCohortMemberFieldBinding(document, {
    cohortRevisionId,
    selectionRevisionId,
    fieldPath: 'resourceType',
  });
};

test('binds the exact Resource Type field with ALL to the named cohort and immutable selection', () => {
  const document = capturedSavedFieldDocumentExcerpt();
  const result = validateResourceTypeBinding(document);
  assert.equal(result.binding.columnId, resourceTypeColumnId);
  assert.equal(result.binding.policy, 'ALL');
  assert.equal(result.column.source.field.path, 'resourceType');
  assert.equal(result.cohortRevisionId, cohortRevisionId);
  assert.equal(result.selectionRevisionId, selectionRevisionId);
});

test('rejects a missing or non-ALL named cohort member binding', () => {
  const missing = capturedSavedFieldDocumentExcerpt();
  missing.rows.groups.rowValues = [];
  assert.throws(() => validateResourceTypeBinding(missing), /exactly one/);

  const wrongPolicy = capturedSavedFieldDocumentExcerpt();
  wrongPolicy.rows.groups.rowValues[0].policy = 'ONE';
  assert.throws(() => validateResourceTypeBinding(wrongPolicy), /ALL/);
});

test('rejects a binding to another column or a field with the wrong source path', () => {
  const wrongColumn = capturedSavedFieldDocumentExcerpt();
  wrongColumn.rows.groups.rowValues[0].columnId = wrongColumn.columns[0].columnId;
  assert.throws(() => validateResourceTypeBinding(wrongColumn), /resourceType/);

  const wrongPath = capturedSavedFieldDocumentExcerpt();
  wrongPath.columns[1].source.field.path = 'id';
  assert.throws(() => validateResourceTypeBinding(wrongPath), /resourceType/);
});

test('rejects a stale cohort or immutable selection revision', () => {
  const staleCohort = capturedSavedFieldDocumentExcerpt();
  staleCohort.rows.groups.source.explicit.revisionId = 'grouprev_stale';
  assert.throws(() => validateResourceTypeBinding(staleCohort), /cohort revision/);

  const staleSelection = capturedSavedFieldDocumentExcerpt();
  staleSelection.population.selectionRevisionId = 'selection_stale';
  assert.throws(() => validateResourceTypeBinding(staleSelection), /selection revision/);
});
