import { useBrowserSubscription } from './useBrowserSubscription';

export const useDirtyBeforeUnload = (dirty: boolean): void => {
  useBrowserSubscription((_onStoreChange) => {
    if (!dirty) return () => {};
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty], dirty);
};
