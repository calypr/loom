import { describe, expect, it } from 'vitest';
import { hasRowValuePolicy } from './shapeCombinePolicy';

describe('hasRowValuePolicy', () => {
  it('treats only a non-empty string dimension value as a row policy', () => {
    expect(hasRowValuePolicy(null)).toBe(false);
    expect(hasRowValuePolicy(undefined)).toBe(false);
    expect(hasRowValuePolicy('')).toBe(false);
    expect(hasRowValuePolicy(42)).toBe(false);
    expect(hasRowValuePolicy('COUNT')).toBe(true);
  });
});
