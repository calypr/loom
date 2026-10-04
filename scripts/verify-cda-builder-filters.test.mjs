import test from 'node:test';
import assert from 'node:assert/strict';
import { assertPersistedFilterState, assertVisibleIdentityMembership } from './lib/filter-oracle-assertions.mjs';

test('raw-source membership assertion rejects an incorrect visible result', () => {
  assert.throws(() => assertVisibleIdentityMembership(
    [['wrong-specimen', 'BodyStructure/x']], ['77d5efff-e239-57d9-88ac-bbb6394872fe'], 'known equality filter',
  ), /visible Specimen identities differ from raw CDA source/);
});

test('persisted filter assertion rejects a lost saved step after reload', () => {
  assert.throws(() => assertPersistedFilterState({
    actualHistoryCount: 0,
    expectedHistoryCount: 1,
    actualIds: ['77d5efff-e239-57d9-88ac-bbb6394872fe'],
    expectedIds: ['77d5efff-e239-57d9-88ac-bbb6394872fe'],
    context: 'reload',
  }), /saved filter history did not persist/);
});

test('persisted filter assertion rejects a wrong row even when the step survived', () => {
  assert.throws(() => assertPersistedFilterState({
    actualHistoryCount: 1,
    expectedHistoryCount: 1,
    actualIds: ['b7cad184-db67-5542-a975-10fffa3e89e7'],
    expectedIds: ['77d5efff-e239-57d9-88ac-bbb6394872fe'],
    context: 'edited equality reload',
  }), /persisted preview identities differ from raw CDA source/);
});
