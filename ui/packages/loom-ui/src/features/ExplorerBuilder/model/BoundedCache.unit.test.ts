import { describe, expect, it } from 'vitest';
import { BoundedCache } from './BoundedCache';

describe('BoundedCache', () => {
  it('moves recently used values to the bounded cache tail', () => {
    const cache = new BoundedCache<number>(2);
    expect(cache.getOrSet('first', () => 1)).toBe(1);
    expect(cache.getOrSet('second', () => 2)).toBe(2);
    expect(cache.getOrSet('first', () => 3)).toBe(1);
    expect(cache.getOrSet('third', () => 4)).toBe(4);
    expect(cache.getOrSet('first', () => 5)).toBe(1);
    expect(cache.getOrSet('second', () => 6)).toBe(6);
  });
});
