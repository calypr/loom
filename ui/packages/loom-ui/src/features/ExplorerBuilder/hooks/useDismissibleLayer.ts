import { useRef } from 'react';
import { useBrowserSubscription } from './useBrowserSubscription';

export const useDismissibleLayer = <T extends HTMLElement>(
  open: boolean,
  setOpen: (open: boolean) => void,
) => {
  const ref = useRef<T>(null);

  useBrowserSubscription((_onStoreChange) => {
    if (!open) return () => {};
    const dismissOnPointerDown = (event: PointerEvent) => {
      if (!ref.current?.contains(event.target as Node)) setOpen(false);
    };
    const dismissOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', dismissOnPointerDown);
    document.addEventListener('keydown', dismissOnEscape);
    return () => {
      document.removeEventListener('pointerdown', dismissOnPointerDown);
      document.removeEventListener('keydown', dismissOnEscape);
    };
  }, [open, setOpen], open);

  return ref;
};
