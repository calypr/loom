import { createStore } from '../../../data/createStore';
import { RequestOwner } from '../../../data/requests';
import type { ConceptCatalogView } from '../components/ConceptCatalog';

export type AddColumnsView = ConceptCatalogView | 'advanced';

export function createAddColumnsState() {
  const store = createStore<{ addColumnsView: AddColumnsView; addColumnsViewRevision: number }>({
    addColumnsView: 'all',
    addColumnsViewRevision: 0,
  });
  const requests = new RequestOwner();
  return {
    getSnapshot: store.getSnapshot,
    subscribeInScope: (identity: string) => (listener: () => void) => {
      const unsubscribe = store.subscribe(listener);
      requests.setIdentity(identity);
      return () => {
        unsubscribe();
        queueMicrotask(() => {
          if (!store.hasSubscribers()) requests.cancel('construction-choices');
        });
      };
    },
    startRequest: (identity: string) => requests.start('construction-choices', identity),
    setView(addColumnsView: AddColumnsView, incrementRevision: boolean) {
      const current = store.getSnapshot();
      if (current.addColumnsView === addColumnsView && !incrementRevision) return;
      store.setSnapshot({
        addColumnsView,
        addColumnsViewRevision: current.addColumnsViewRevision + (incrementRevision ? 1 : 0),
      });
    },
  };
}
