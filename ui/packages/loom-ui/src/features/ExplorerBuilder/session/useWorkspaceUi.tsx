import { createContext, useContext, useState, useSyncExternalStore, type ReactNode } from 'react';
import type { ConstructionOperationFamily } from '../model/constructionOperationFamily';
import type { ReshapeEntryKind, ReshapeForm } from '../model/reshapeForms';
import type { ColumnSelectionState } from './workspaceSelection';
import type { AddColumnsView } from './addColumnsState';
import { createWorkspaceUi } from './workspaceUi';

type OccurrencePanel = {
  readonly family: ConstructionOperationFamily | undefined;
  readonly addColumns: { readonly view: AddColumnsView };
  readonly reshapeEntryKind: ReshapeEntryKind;
  readonly reshapeFormKind: ReshapeForm['kind'];
  readonly columnSelection: ColumnSelectionState;
};

type WorkspaceUi = ReturnType<typeof createWorkspaceUi<OccurrencePanel, object>>;
type WorkspaceUiState = ReturnType<WorkspaceUi['getSnapshot']>;
type WorkspaceUiActions = Omit<WorkspaceUi, 'getSnapshot' | 'subscribe'>;

const WorkspaceUiContext = createContext<WorkspaceUi | undefined>(undefined);

export function WorkspaceUiProvider({ children }: { readonly children: ReactNode }) {
  const [workspaceUi] = useState(createWorkspaceUi<OccurrencePanel, object>);
  return <WorkspaceUiContext.Provider value={workspaceUi}>{children}</WorkspaceUiContext.Provider>;
}

function useWorkspaceUiOwner() {
  const owner = useContext(WorkspaceUiContext);
  if (!owner) throw new Error('Workspace UI must be read inside WorkspaceUiProvider.');
  return owner;
}

export function useWorkspaceUi<Selection>(select: (state: WorkspaceUiState) => Selection): Selection {
  const { subscribe, getSnapshot } = useWorkspaceUiOwner();
  return select(useSyncExternalStore(subscribe, getSnapshot, getSnapshot));
}

export function useWorkspaceUiActions(): WorkspaceUiActions {
  const { subscribe: _subscribe, getSnapshot: _getSnapshot, ...actions } = useWorkspaceUiOwner();
  return actions;
}
