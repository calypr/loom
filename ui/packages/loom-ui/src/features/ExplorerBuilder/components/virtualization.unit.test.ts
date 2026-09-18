// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { BoundedCache, useVirtualViewport, virtualRange } from './virtualization';

describe('virtualization helpers', () => {
  it('keeps a large grid window bounded', () => {
    const rows = virtualRange({
      count: 1000,
      offset: 0,
      viewport: 640,
      itemSize: 44,
      overscan: 3,
    });
    const columns = virtualRange({
      count: 200,
      offset: 0,
      viewport: 1024,
      itemSize: 180,
      overscan: 2,
    });

    expect(rows.end - rows.start).toBeLessThanOrEqual(22);
    expect(columns.end - columns.start).toBeLessThanOrEqual(12);
    expect((rows.end - rows.start) * (columns.end - columns.start)).toBeLessThan(1500);
  });

  it('moves recently used values to the bounded cache tail', () => {
    const cache = new BoundedCache<number>(2);
    expect(cache.getOrSet('first', () => 1)).toBe(1);
    expect(cache.getOrSet('second', () => 2)).toBe(2);
    expect(cache.getOrSet('first', () => 3)).toBe(1);
    expect(cache.getOrSet('third', () => 4)).toBe(4);
    expect(cache.getOrSet('first', () => 5)).toBe(1);
    expect(cache.getOrSet('second', () => 6)).toBe(6);
  });

  it('subscribes when a conditionally rendered viewport mounts', () => {
    const { result } = renderHook(() =>
      useVirtualViewport<HTMLDivElement>(320, 240),
    );
    const element = document.createElement('div');
    Object.defineProperties(element, {
      clientWidth: { configurable: true, value: 480 },
      clientHeight: { configurable: true, value: 300 },
      scrollTop: { configurable: true, writable: true, value: 0 },
      scrollLeft: { configurable: true, writable: true, value: 0 },
    });

    expect(result.current.viewport).toMatchObject({
      width: 320,
      height: 240,
      scrollTop: 0,
    });

    act(() => result.current.ref(element));
    expect(result.current.viewport).toMatchObject({
      width: 480,
      height: 300,
      scrollTop: 0,
    });

    act(() => {
      element.scrollTop = 1100;
      element.dispatchEvent(new Event('scroll'));
    });
    expect(result.current.viewport.scrollTop).toBe(1100);
  });
});
