import {useCallback, useRef, useState, type RefCallback} from 'react';
import { useCommitLifecycle } from '../hooks/useBrowserSubscription';

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
  const [viewport, setViewport] = useState(() =>
    defaultViewport(fallbackWidth, fallbackHeight),
  );
  const fallbackRef = useRef({ width: fallbackWidth, height: fallbackHeight });
  const ref = useCallback((mounted: T | null) => setElement(mounted), []);

  useCommitLifecycle(() => {
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
  }, [element]);

  return { viewport, ref };
};
