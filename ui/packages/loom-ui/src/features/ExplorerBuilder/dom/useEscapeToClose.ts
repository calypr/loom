import { useDomSubscription } from './useDomSubscription';

import { registerEscape } from './escapeStack';

export const useEscapeToClose = (
  open: boolean,
  setOpen: (open: boolean) => void,
): void => {
  useDomSubscription(() => {
    if (!open) return () => {};
    return registerEscape('window', () => setOpen(false));
  }, [open, setOpen], open);
};
