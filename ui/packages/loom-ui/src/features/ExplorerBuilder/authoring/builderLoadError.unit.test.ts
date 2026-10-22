import { describe, expect, it } from 'vitest';
import { shouldShowBuilderLoadError } from './builderLoadError';

describe('shouldShowBuilderLoadError', () => {
  it('requires either an explorer list error or missing Builder data', () => {
    expect(shouldShowBuilderLoadError(false, false)).toBe(true);
    expect(shouldShowBuilderLoadError(true, true)).toBe(true);
    expect(shouldShowBuilderLoadError(false, true)).toBe(false);
  });
});
