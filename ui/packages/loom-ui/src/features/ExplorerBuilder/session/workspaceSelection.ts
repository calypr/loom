import { createStore } from '../../../data/createStore';
import type { ConstructionHistorySelection } from '../model/constructionHistory';

export type ColumnSelectionState =
  | { readonly kind: 'empty' }
  | {
      readonly kind: 'selected';
      readonly outputId: string;
      readonly columnIds: ReadonlyArray<string>;
    };

type SelectionOwnerState = {
  readonly rowIntentSession: boolean;
  readonly sourceRowsOpen: boolean;
  readonly constructionHistorySelection: ConstructionHistorySelection;
  readonly editingConstructionStepId: string | undefined;
  readonly columnSelection: ColumnSelectionState;
};

const emptySelectionOwnerState: SelectionOwnerState = {
  rowIntentSession: false,
  sourceRowsOpen: false,
  constructionHistorySelection: { kind: 'source' },
  editingConstructionStepId: undefined,
  columnSelection: { kind: 'empty' },
};

type Update<Value> = Value | ((current: Value) => Value);

export function createWorkspaceSelection() {
  const store = createStore(emptySelectionOwnerState);
  const set = <Field extends keyof SelectionOwnerState>(field: Field, update: Update<SelectionOwnerState[Field]>) => {
    const current = store.getSnapshot();
    const value = typeof update === 'function' ? update(current[field]) : update;
    store.setSnapshot({ ...current, [field]: value });
  };
  return {
    getSnapshot: store.getSnapshot,
    subscribe: store.subscribe,
    setRowIntentSession: (update: Update<boolean>) => set('rowIntentSession', update),
    setSourceRowsOpen: (update: Update<boolean>) => set('sourceRowsOpen', update),
    setConstructionHistorySelection: (update: Update<ConstructionHistorySelection>) => set('constructionHistorySelection', update),
    setEditingConstructionStepId: (update: Update<string | undefined>) => set('editingConstructionStepId', update),
    setColumnSelection: (update: Update<ColumnSelectionState>) => set('columnSelection', update),
  };
}
