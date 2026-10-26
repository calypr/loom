import { describe, expect, it } from 'vitest';
import { commandDraftIdentity } from './commandDraftIdentity';

describe('commandDraftIdentity', () => {
  it('uses the server draft when the UI state lags behind it', () => {
    expect(commandDraftIdentity(
      { draftVersion: 1 },
      { version: 2, digest: 'sha256:draft-2' },
    )).toEqual({
      expectedDraftVersion: 2,
      expectedDraftDigest: 'sha256:draft-2',
    });
  });

  it('falls back to the UI state before the server draft is initialized', () => {
    expect(commandDraftIdentity(
      { draftVersion: 3 },
      { version: 0, digest: '' },
    )).toEqual({
      expectedDraftVersion: 3,
      expectedDraftDigest: undefined,
    });
  });
});
