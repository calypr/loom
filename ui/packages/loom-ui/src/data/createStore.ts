export function createStore<Snapshot>(initialSnapshot: Snapshot) {
  let snapshot = initialSnapshot;
  const subscribers = new Set<() => void>();

  return {
    getSnapshot: () => snapshot,
    hasSubscribers: () => subscribers.size > 0,
    setSnapshot(nextSnapshot: Snapshot, beforeNotify?: () => void) {
      if (Object.is(snapshot, nextSnapshot)) return;
      snapshot = nextSnapshot;
      beforeNotify?.();
      subscribers.forEach((subscriber) => subscriber());
    },
    subscribe(subscriber: () => void) {
      subscribers.add(subscriber);
      return () => { subscribers.delete(subscriber); };
    },
  };
}
