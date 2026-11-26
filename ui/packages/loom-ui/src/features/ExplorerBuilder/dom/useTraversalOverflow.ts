import { useCallback, useState, useSyncExternalStore } from 'react';
import { createStore } from '../../../data/createStore';

const serverOverflow = () => false;

export const useTraversalOverflow = (
  graphHostRef: React.RefObject<HTMLDivElement | null>,
  traversalRef: React.RefObject<HTMLElement | null>,
  identity: string,
): boolean => {
  const [overflow] = useState(() => createStore(false));

  const subscribe = useCallback((onChange: () => void) => {
    const graphHost = graphHostRef.current;
    const traversal = traversalRef.current;
    if (!graphHost || !traversal) return () => {};
    const unsubscribe = overflow.subscribe(onChange);
    const measure = () => {
      const truncatedWidth = Array.from(
        traversal.querySelectorAll<HTMLElement>('[data-traversal-label]'),
      ).reduce(
        (width, label) =>
          width + Math.max(0, label.scrollWidth - label.clientWidth),
        0,
      );
      const naturalWidth = traversal.scrollWidth + truncatedWidth;
      const allottedWidth = Math.max(0, graphHost.clientWidth - 9 * 16);
      overflow.setSnapshot(naturalWidth > allottedWidth + 1);
    };
    const observer =
      typeof ResizeObserver === 'undefined'
        ? undefined
        : new ResizeObserver(measure);
    observer?.observe(graphHost);
    measure();
    return () => {
      observer?.disconnect();
      unsubscribe();
    };
  }, [graphHostRef, identity, overflow, traversalRef]);

  return useSyncExternalStore(subscribe, overflow.getSnapshot, serverOverflow);
};
