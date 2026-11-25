import { createContext, useContext, type ReactNode } from 'react';
import type { ConstructionOperationFamily } from '../model/constructionOperationFamily';
import type { ConstructionWorkspaceTable } from '../model/constructionWorkspaceTable';

export interface ConstructionWorkspaceSession {
  readonly tables: ReadonlyArray<ConstructionWorkspaceTable>;
  readonly selectedOutputId?: string;
  readonly tableActionsDisabled?: boolean;
  readonly onSelectTable: (outputId: string) => void;
  readonly onNewTable: () => void;
  readonly onDuplicateTable: () => void;
  readonly onDeleteTable: () => void;
  readonly onRenameTable: (outputId: string) => void;
  readonly actionsDisabled?: boolean;
  readonly activeFamily?: ConstructionOperationFamily;
  readonly addColumnsDisabled?: boolean;
  readonly addColumnsReason?: string;
  readonly combineDisabled?: boolean;
  readonly calculateAvailable?: boolean;
  readonly calculateUnavailableReason?: string;
  readonly onSelectFamily: (family: ConstructionOperationFamily) => void;
  readonly previewStatus: 'empty' | 'stale' | 'previewing' | 'needs-repair' | 'error' | 'ready';
  readonly previewReceiptId?: string;
  readonly previewOutputId?: string;
  readonly proposalId?: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
}

const ConstructionWorkspaceContext = createContext<ConstructionWorkspaceSession | undefined>(undefined);

export function ConstructionWorkspaceSessionProvider({ value, children }: {
  readonly value: ConstructionWorkspaceSession;
  readonly children: ReactNode;
}) {
  return <ConstructionWorkspaceContext.Provider value={value}>{children}</ConstructionWorkspaceContext.Provider>;
}

export function useConstructionWorkspaceSession<Selection>(select: (state: ConstructionWorkspaceSession) => Selection): Selection {
  const session = useContext(ConstructionWorkspaceContext);
  if (!session) throw new Error('ConstructionWorkspace requires its Builder session.');
  return select(session);
}
