import { useRef } from 'react';
import { useDomSubscription } from './useDomSubscription';
import { registerEscape } from './escapeStack';

export const useDismissibleLayer = <T extends HTMLElement>(
  open: boolean,
  setOpen: (open: boolean) => void,
) => {
  const ref = useRef<T>(null);

  useDomSubscription(() => {
    if (!open) return () => {};
    const dismissOnPointerDown = (event: PointerEvent) => {
      if (!ref.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', dismissOnPointerDown);
    const unregisterEscape = registerEscape('document', () => setOpen(false));
    return () => {
      document.removeEventListener('pointerdown', dismissOnPointerDown);
      unregisterEscape();
    };
  }, [open, setOpen], open);

  return ref;
};
