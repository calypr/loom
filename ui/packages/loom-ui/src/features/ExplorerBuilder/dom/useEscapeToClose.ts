import { useDomSubscription } from './useDomSubscription';

import { registerEscape } from './escapeStack';

export const useEscapeToClose = (
  open: boolean,
  setOpen: (open: boolean) => void,
  phase: Parameters<typeof registerEscape>[0] = 'window',
): void => {
  useDomSubscription(() => {
    if (!open) return () => {};
    return registerEscape(phase, () => setOpen(false));
  }, [open, setOpen, phase], open);
};
