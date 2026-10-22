import { describe, expect, it } from 'vitest';
import { normalizedColumnLabel } from './columnLabel';

describe('normalizedColumnLabel', () => {
  it('trims a non-empty label and restores the saved label when empty', () => {
    expect(normalizedColumnLabel(' Zulu ', 'Alpha')).toBe('Zulu');
    expect(normalizedColumnLabel('   ', 'Alpha')).toBe('Alpha');
    expect(normalizedColumnLabel('', 'Alpha')).toBe('Alpha');
  });
});
