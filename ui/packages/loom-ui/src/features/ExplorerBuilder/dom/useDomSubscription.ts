import { useCallback, useRef, useSyncExternalStore } from 'react';

export const useDomSubscription = (
  attach: () => void | (() => void),
  dependencies: ReadonlyArray<unknown>,
  enabled = true,
): void => {
  const attachRef = useRef(attach);
  attachRef.current = attach;
  const subscribe = useCallback(
    () => {
      if (!enabled) return () => {};
      const cleanup = attachRef.current();
      return typeof cleanup === 'function' ? cleanup : () => {};
    },
    [...dependencies, enabled],
  );
  useSyncExternalStore(subscribe, () => 0, () => 0);
};
