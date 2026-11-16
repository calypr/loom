import { useBrowserSubscription } from './useBrowserSubscription';

const handlers: Array<() => void> = [];
let listening = false;
const onKeyDown = (event: KeyboardEvent) => {
  if (event.key !== 'Escape' || event.defaultPrevented) return;
  const handler = handlers.at(-1);
  if (!handler) return;
  event.preventDefault();
  handler();
};

export const useEscapeToClose = (
  open: boolean,
  setOpen: (open: boolean) => void,
): void => {
  useBrowserSubscription((_onStoreChange) => {
    if (!open) return () => {};
    const handler = () => setOpen(false);
    handlers.push(handler);
    if (!listening) {
      window.addEventListener('keydown', onKeyDown);
      listening = true;
    }
    return () => {
      const index = handlers.lastIndexOf(handler);
      if (index >= 0) handlers.splice(index, 1);
      if (handlers.length === 0 && listening) {
        window.removeEventListener('keydown', onKeyDown);
        listening = false;
      }
    };
  }, [open, setOpen], open);
};
