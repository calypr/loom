import { useMemo, useSyncExternalStore } from 'react';
import { createStore } from '../../../data/createStore';

const serverHost = () => null;

function createPortalHostStore(id: string) {
  const store = createStore<HTMLElement | null>(null);

  function subscribe(onChange: () => void) {
    if (typeof document === 'undefined') return () => {};
    const unsubscribe = store.subscribe(onChange);
    const read = () => store.setSnapshot(document.getElementById(id));
    read();
    const observer = new MutationObserver(read);
    observer.observe(document.body, { childList: true, subtree: true });
    return () => {
      observer.disconnect();
      unsubscribe();
    };
  }

  return { subscribe, getSnapshot: store.getSnapshot };
}

export function usePortalHost(id: string): HTMLElement | null {
  const store = useMemo(() => createPortalHostStore(id), [id]);
  return useSyncExternalStore(store.subscribe, store.getSnapshot, serverHost);
}
