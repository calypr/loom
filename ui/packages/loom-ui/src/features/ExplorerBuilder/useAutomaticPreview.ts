import { useEffect, useRef } from 'react';

export const useAutomaticPreview = ({ requestKey, enabled, refresh, cancel }: {
  readonly requestKey: string | undefined;
  readonly enabled: boolean;
  readonly refresh: () => Promise<void>;
  readonly cancel?: () => void;
}) => {
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const cancelRef = useRef(cancel);
  cancelRef.current = cancel;
  const requestedKey = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!requestKey || !enabled || requestedKey.current === requestKey) return;
    let started = false;
    let finished = false;
    const timer = window.setTimeout(() => {
      requestedKey.current = requestKey;
      started = true;
      void refreshRef.current().finally(() => { finished = true; });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      if (started && !finished) {
        requestedKey.current = undefined;
        cancelRef.current?.();
      }
    };
  }, [requestKey, enabled]);
};
