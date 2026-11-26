import { createStore } from '../../../data/createStore';
import type { BuilderAuthoringState } from '../model/authoring';
import type { LocalDraft } from './draftHydration';

export function createBuilderDraftAccess(initialState: BuilderAuthoringState) {
  let state = initialState;
  return {
    getState: () => state,
    setCurrentDraft(next: BuilderAuthoringState) { state = next; },
  };
}

export function createRememberedTable(ownerKey: string, getStorage: () => Pick<Storage, 'getItem' | 'setItem'>) {
  const key = `loom.builder.selected-table:${ownerKey}`;
  return {
    read() {
      try {
        return getStorage().getItem(key) ?? undefined;
      } catch {
        return undefined;
      }
    },
    write(outputId: string) {
      try {
        getStorage().setItem(key, outputId);
      } catch {
        // Essential: Persistence is optional when browser storage is disabled.
      }
    },
  };
}

export function createLocalDraftStore() {
  const store = createStore<LocalDraft | undefined>(undefined);
  return {
    getSnapshot: store.getSnapshot,
    subscribe: store.subscribe,
    update(update: LocalDraft | ((draft: LocalDraft | undefined) => LocalDraft)) {
      store.setSnapshot(typeof update === 'function' ? update(store.getSnapshot()) : update);
    },
  };
}
