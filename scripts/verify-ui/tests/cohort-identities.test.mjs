import assert from 'node:assert/strict';
import test from 'node:test';
import { assertCohortPatientIDColumns } from '../cohort-identities.mjs';

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
