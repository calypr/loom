import { describe, expect, it } from 'vitest';
import { hasSingleScopedRowTable } from './datasetReviewSelection';

describe('dataset review selection', () => {
  it('resolves a row setting only when exactly one scoped table remains', () => {
    expect(hasSingleScopedRowTable(true, 1)).toBe(true);
    expect(hasSingleScopedRowTable(true, 0)).toBe(false);
    expect(hasSingleScopedRowTable(true, 2)).toBe(false);
    expect(hasSingleScopedRowTable(false, 1)).toBe(false);
  });
});
