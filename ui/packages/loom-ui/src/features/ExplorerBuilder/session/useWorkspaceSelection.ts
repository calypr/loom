import { useMemo, useSyncExternalStore } from 'react';
import { createWorkspaceSelection } from './workspaceSelection';

type SelectionOwner = ReturnType<typeof createWorkspaceSelection>;
type WorkspaceSelection = ReturnType<SelectionOwner['getSnapshot']> & Omit<SelectionOwner, 'getSnapshot' | 'subscribe'>;

export function useWorkspaceSelection(ownerKey: string, explorerId: string, outputId: string | undefined): WorkspaceSelection {
  const identity = `${ownerKey}\u0000${explorerId}\u0000${outputId ?? ''}`;
  const { subscribe, getSnapshot, ...actions } = useMemo(createWorkspaceSelection, [identity]);
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return { ...state, ...actions };
}
