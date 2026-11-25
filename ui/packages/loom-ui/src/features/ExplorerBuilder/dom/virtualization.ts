import { useCallback, useRef, useState, useSyncExternalStore, type RefCallback } from 'react';
import { createStore } from '../../../data/createStore';

interface VirtualViewport {
  readonly width: number;
  readonly height: number;
  readonly scrollLeft: number;
  readonly scrollTop: number;
}

interface VirtualViewportBinding<T extends HTMLElement> {
  readonly viewport: VirtualViewport;
  readonly ref: RefCallback<T>;
}

const defaultViewport = (width: number, height: number): VirtualViewport => ({
  width,
  height,
  scrollLeft: 0,
  scrollTop: 0,
});

export const useVirtualViewport = <T extends HTMLElement>(
  fallbackWidth = 1024,
  fallbackHeight = 640,
): VirtualViewportBinding<T> => {
  const [element, setElement] = useState<T | null>(null);
  const [store] = useState(() => createStore(defaultViewport(fallbackWidth, fallbackHeight)));
  const fallbackRef = useRef({ width: fallbackWidth, height: fallbackHeight });
  const ref = useCallback((mounted: T | null) => setElement(mounted), []);

  const subscribe = useCallback((onChange: () => void) => {
    if (!element) return () => {};
    const unsubscribe = store.subscribe(onChange);
    const update = () => {
      const fallback = fallbackRef.current;
      const next = {
        width: element.clientWidth || fallback.width,
        height: element.clientHeight || fallback.height,
        scrollLeft: element.scrollLeft,
        scrollTop: element.scrollTop,
      };
      const current = store.getSnapshot();
      if (next.width !== current.width || next.height !== current.height ||
          next.scrollLeft !== current.scrollLeft || next.scrollTop !== current.scrollTop) {
        store.setSnapshot(next);
      }
    };
    update();
    element.addEventListener('scroll', update, { passive: true });
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(update);
    observer?.observe(element);
    return () => {
      observer?.disconnect();
      element.removeEventListener('scroll', update);
      unsubscribe();
    };
  }, [element, store]);

  const viewport = useSyncExternalStore(subscribe, store.getSnapshot, store.getSnapshot);
  return { viewport, ref };
};
