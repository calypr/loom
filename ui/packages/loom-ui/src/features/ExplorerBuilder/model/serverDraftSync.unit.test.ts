import { describe, expect, it } from 'vitest';
import { shouldSyncServerDraft } from './serverDraftSync';

describe('shouldSyncServerDraft', () => {
  it('syncs only when builder data exists and the key changes', () => {
    const present = { draftVersion: 1, draftDigest: 'digest' };
    expect(shouldSyncServerDraft(present, 'key-a', 'key-b')).toBe(true);
    expect(shouldSyncServerDraft(present, 'key-a', 'key-a')).toBe(false);
    expect(shouldSyncServerDraft(undefined, 'key-a', 'key-b')).toBe(false);
    expect(shouldSyncServerDraft(null, 'key-a', 'key-b')).toBe(false);
  });
});
