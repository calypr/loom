import { describe, expect, it } from 'vitest';
import { isDraftRestoreError } from './draftRestore';

describe('isDraftRestoreError', () => {
  it('treats draft conflicts and stale snapshots as restore errors', () => {
    expect(isDraftRestoreError('DRAFT_CONFLICT')).toBe(true);
    expect(isDraftRestoreError('STALE_SNAPSHOT')).toBe(true);
  });

  it('leaves other errors alone', () => {
    expect(isDraftRestoreError('SERVICE_UNAVAILABLE')).toBe(false);
    expect(isDraftRestoreError(undefined)).toBe(false);
  });
});
