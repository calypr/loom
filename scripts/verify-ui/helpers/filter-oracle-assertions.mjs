import assert from 'node:assert/strict';

export function assertVisibleIdentityMembership(actualRows, expectedIds, context) {
  const actualIds = actualRows.map(row => row[0]);
  assert.deepEqual(actualIds, expectedIds, `${context}: visible Specimen identities differ from raw CDA source`);
}

export function assertPersistedFilterState({ actualHistoryCount, expectedHistoryCount, actualIds, expectedIds, context }) {
  assert.equal(actualHistoryCount, expectedHistoryCount, `${context}: saved filter history did not persist`);
  assert.deepEqual(actualIds, expectedIds, `${context}: persisted preview identities differ from raw CDA source`);
}
