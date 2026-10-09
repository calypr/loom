import assert from 'node:assert/strict';

export const assertCohortMemberFieldBinding = (document, { cohortRevisionId, selectionRevisionId, fieldPath }) => {
  assert(document && typeof document === 'object', 'saved cohort document must be present');
  assert(typeof cohortRevisionId === 'string' && cohortRevisionId.length > 0, 'expected cohort revision must be present');
  assert(typeof selectionRevisionId === 'string' && selectionRevisionId.length > 0,
    'expected immutable selection revision must be present');
  assert.equal(document.rows?.kind, 'GROUPS', 'named cohort member field must belong to grouped rows');
  const groups = document.rows.groups;
  assert(groups && typeof groups === 'object', 'named cohort must retain its group row definition');
  assert.equal(groups.source?.kind, 'EXPLICIT', 'named cohort member field must use the saved explicit cohort');
  assert.equal(groups.source.explicit?.revisionId, cohortRevisionId, 'named cohort binding must retain the exact cohort revision');
  assert.equal(document.population?.selectionRevisionId, selectionRevisionId,
    'named cohort binding must retain the exact immutable selection revision');
  assert(typeof fieldPath === 'string' && fieldPath.length > 0, 'named cohort member field needs an expected source path');

  const bindings = groups.rowValues;
  assert(Array.isArray(bindings), 'named cohort member field must expose its saved row bindings');
  assert.equal(bindings.length, 1, 'named cohort must have exactly one member field binding');
  const binding = bindings[0];
  assert.equal(binding.policy, 'ALL', 'named cohort member field binding must use ALL');
  assert(typeof binding.columnId === 'string' && binding.columnId.length > 0,
    'named cohort member field binding must retain a stable column identity');

  const columns = document.columns ?? [];
  const boundColumns = columns.filter(column => column.columnId === binding.columnId);
  assert.equal(boundColumns.length, 1, 'named cohort member field binding must resolve by columnId exactly once');
  const memberColumns = columns.filter(column => column.source?.kind === 'field' && column.source.field?.path === fieldPath);
  assert.equal(memberColumns.length, 1, `named cohort must have exactly one ${fieldPath} member field column`);
  const column = memberColumns[0];
  assert.equal(column, boundColumns[0], `ALL member binding must resolve to the exact ${fieldPath} source column`);
  assert.equal(column.source.kind, 'field', `ALL member binding must use a ${fieldPath} field source`);
  assert.equal(column.source.field?.path, fieldPath, `ALL member binding must use the exact ${fieldPath} source path`);
  assert.equal(column.source.field?.projectionMode, 'VALUE', `ALL member binding must project ${fieldPath} as a value`);
  assert(typeof column.column === 'string' && column.column.length > 0,
    'ALL member field must retain its exact physical column');
  assert.equal(column.occurrenceId, 'base', 'ALL member field must resolve from the saved base occurrence');
  return { cohortRevisionId, selectionRevisionId, binding, column };
};

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
