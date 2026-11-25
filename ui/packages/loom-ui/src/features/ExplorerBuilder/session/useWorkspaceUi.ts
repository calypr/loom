import { useState, useSyncExternalStore } from 'react';
import { createWorkspaceUi } from './workspaceUi';

type WorkspaceUi<Panel, Client> = ReturnType<typeof createWorkspaceUi<Panel, Client>>;
type WorkspaceUiBinding<Panel, Client> = ReturnType<WorkspaceUi<Panel, Client>['getSnapshot']> &
  Omit<WorkspaceUi<Panel, Client>, 'getSnapshot' | 'subscribe'>;

export function useWorkspaceUi<Panel, Client>(): WorkspaceUiBinding<Panel, Client> {
  const { subscribe, getSnapshot, ...actions } = useState(createWorkspaceUi<Panel, Client>)[0];
  return { ...useSyncExternalStore(subscribe, getSnapshot, getSnapshot), ...actions };
}
