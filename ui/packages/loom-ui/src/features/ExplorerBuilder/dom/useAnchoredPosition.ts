import { useMemo, useSyncExternalStore, type RefObject } from 'react';
import { createStore } from '../../../data/createStore';

interface Position {
  readonly top: number;
  readonly right: number;
}

function createPositionStore(anchor: RefObject<HTMLElement | null>, open: boolean) {
  const store = createStore<Position | undefined>(undefined);
  function subscribe(onChange: () => void) {
    if (!open) return () => {};
    const unsubscribe = store.subscribe(onChange);
    function measure() {
      const element = anchor.current;
      if (!element) return;
      const bounds = element.getBoundingClientRect();
      const below = bounds.bottom + 4;
      const top = below + 120 <= window.innerHeight - 8
        ? below
        : Math.max(8, bounds.top - 124);
      const right = Math.max(8, window.innerWidth - bounds.right);
      const position = store.getSnapshot();
      if (position?.top === top && position.right === right) return;
      store.setSnapshot({ top, right });
    }
    measure();
    window.addEventListener('resize', measure);
    window.addEventListener('scroll', measure, true);
    return () => {
      window.removeEventListener('resize', measure);
      window.removeEventListener('scroll', measure, true);
      unsubscribe();
      store.setSnapshot(undefined);
    };
  }
  return { getSnapshot: store.getSnapshot, subscribe };
}

const serverSnapshot = () => undefined;

export function useAnchoredPosition(anchor: RefObject<HTMLElement | null>, open: boolean) {
  const store = useMemo(() => createPositionStore(anchor, open), [anchor, open]);
  return useSyncExternalStore(store.subscribe, store.getSnapshot, serverSnapshot);
}
