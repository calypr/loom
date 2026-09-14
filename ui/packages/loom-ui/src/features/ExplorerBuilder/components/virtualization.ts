import { useEffect, useRef, useState, type RefObject } from 'react';

export interface VirtualRange {
  readonly start: number;
  readonly end: number;
}

export const virtualRange = ({
  count,
  offset,
  viewport,
  itemSize,
  overscan = 2,
}: {
  readonly count: number;
  readonly offset: number;
  readonly viewport: number;
  readonly itemSize: number;
  readonly overscan?: number;
}): VirtualRange => {
  if (count <= 0 || itemSize <= 0) return { start: 0, end: 0 };
  const first = Math.max(0, Math.floor(Math.max(0, offset) / itemSize) - overscan);
  const visible = Math.max(1, Math.ceil(Math.max(0, viewport) / itemSize));
  return {
    start: Math.min(first, count),
    end: Math.min(count, first + visible + overscan * 2),
  };
};

export interface VirtualViewport {
  readonly width: number;
  readonly height: number;
  readonly scrollLeft: number;
  readonly scrollTop: number;
}

const defaultViewport = (width: number, height: number): VirtualViewport => ({
  width,
  height,
  scrollLeft: 0,
  scrollTop: 0,
});

export const useVirtualViewport = <T extends HTMLElement>(
  elementRef: RefObject<T | null>,
  fallbackWidth = 1024,
  fallbackHeight = 640,
): VirtualViewport => {
  const [viewport, setViewport] = useState(() =>
    defaultViewport(fallbackWidth, fallbackHeight),
  );
  const fallbackRef = useRef({ width: fallbackWidth, height: fallbackHeight });

  useEffect(() => {
    const element = elementRef.current;
    if (!element) return undefined;
    const update = () => {
      const fallback = fallbackRef.current;
      setViewport({
        width: element.clientWidth || fallback.width,
        height: element.clientHeight || fallback.height,
        scrollLeft: element.scrollLeft,
        scrollTop: element.scrollTop,
      });
    };
    update();
    element.addEventListener('scroll', update, { passive: true });
    if (typeof ResizeObserver === 'undefined') {
      return () => element.removeEventListener('scroll', update);
    }
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => {
      observer.disconnect();
      element.removeEventListener('scroll', update);
    };
  }, [elementRef]);

  return viewport;
};

export class BoundedCache<V> {
  private readonly entries = new Map<string, V>();

  public constructor(private readonly capacity: number) {}

  public clear(): void {
    this.entries.clear();
  }

  public getOrSet(key: string, create: () => V): V {
    const existing = this.entries.get(key);
    if (existing !== undefined) {
      this.entries.delete(key);
      this.entries.set(key, existing);
      return existing;
    }
    const value = create();
    this.entries.set(key, value);
    while (this.entries.size > this.capacity) {
      const oldest = this.entries.keys().next();
      if (oldest.done) break;
      this.entries.delete(oldest.value);
    }
    return value;
  }
}
