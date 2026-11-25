import { useDomSubscription } from './useDomSubscription';

export const useDirtyBeforeUnload = (dirty: boolean): void => {
  useDomSubscription(() => {
    if (!dirty) return () => {};
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty], dirty);
};
