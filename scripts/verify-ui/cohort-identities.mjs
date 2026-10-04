import assert from 'node:assert/strict';

export const assertCohortPatientIDColumns = (document, legacyIdentity) => {
  const idColumns = document.columns.filter(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
  const matchingLegacyColumns = idColumns.filter(column =>
    column.column === legacyIdentity.column && column.columnId === legacyIdentity.columnId);
  assert.equal(matchingLegacyColumns.length, 1, 'first-table Patient ID must retain exactly its original column identity');
  const legacy = matchingLegacyColumns[0];
  assert.deepEqual(legacy.source, legacyIdentity.source, 'first-table Patient ID must retain its exact source definition');
  const bindings = document.rows.groups.rowValues ?? [];
  assert(bindings.every(binding => typeof binding.columnId === 'string' && binding.columnId),
    'cohort member bindings must retain explicit column identities');
  assert(!bindings.some(binding => binding.columnId === legacyIdentity.columnId),
    'first-table Patient ID identity column must not be bound as a cohort member value');
  const memberColumns = idColumns.filter(column => column.columnId && column.columnId !== legacyIdentity.columnId
    && bindings.some(binding => binding.columnId === column.columnId));
  assert.equal(memberColumns.length, 1, 'exactly one distinct Patient.id member column must be bound in the cohort');
  const member = memberColumns[0];
  assert(member.columnId && member.column && member.column !== legacyIdentity.column
    && member.logicalType?.toLowerCase() === 'string',
  'bound Patient.id member column must be a distinct stable scalar column');
  const binding = bindings.find(item => item.columnId === member.columnId);
  assert(binding, 'Patient.id member column must have its exact persisted cohort binding');
  return { legacy, member, binding };
};
