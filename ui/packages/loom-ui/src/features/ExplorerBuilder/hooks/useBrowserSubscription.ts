import { useCallback, useRef, useSyncExternalStore } from 'react';

export const useCommitLifecycle = (
  effect: (onStoreChange: () => void) => void | (() => void),
  dependencies: ReadonlyArray<unknown>,
  enabled = true,
): void => {
  const effectRef = useRef(effect);
  effectRef.current = effect;
  const subscribe = useCallback(
    (onStoreChange: () => void) => {
      if (!enabled) return () => {};
      const cleanup = effectRef.current(onStoreChange);
      return typeof cleanup === 'function' ? cleanup : () => {};
    },
    [...dependencies, enabled],
  );
  useSyncExternalStore(subscribe, () => 0, () => 0);
};

export const useBrowserSubscription = useCommitLifecycle;
