import { createContext, useContext, type ReactNode } from 'react';
import type { useBuilderWorkspaceActions } from './useBuilderWorkspaceActions';

type OperationEditorSession = NonNullable<ReturnType<typeof useBuilderWorkspaceActions>['operationEditorSession']>;

const OperationEditorContext = createContext<OperationEditorSession | undefined>(undefined);

export function OperationEditorSessionProvider({ value, children }: {
  readonly value: OperationEditorSession;
  readonly children: ReactNode;
}) {
  return <OperationEditorContext.Provider value={value}>{children}</OperationEditorContext.Provider>;
}

export function useOperationEditorSession<Selection>(select: (session: OperationEditorSession) => Selection): Selection {
  const session = useContext(OperationEditorContext);
  if (!session) throw new Error('Operation editor requires its Builder session.');
  return select(session);
}
