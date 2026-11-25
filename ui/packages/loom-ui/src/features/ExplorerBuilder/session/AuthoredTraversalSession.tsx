import { createContext, useContext, type ReactNode } from 'react';
import type { ExplorerBuilderCatalog } from '../../../contracts/types';
import type { DraftTable } from '../model/authoring';

export interface AuthoredTraversalSession {
  readonly importedRelationshipActions?: {
    readonly onAdd: (parentOccurrenceId: string, edgeId: string, nodeId: string) => void;
    readonly onChangeMatchMode: (occurrenceId: string, matchMode: 'OPTIONAL' | 'REQUIRED') => void;
    readonly onRemove: (occurrenceId: string) => void;
  };
  readonly catalog: ExplorerBuilderCatalog;
  readonly table?: DraftTable;
  readonly selectedOccurrenceId: string;
  readonly currentRelatedRecord?: { readonly id: string; readonly nodeId: string; readonly resourceType: string };
  readonly disabled: boolean;
  readonly onSelectOccurrence: (id: string) => void;
  readonly onSetBase: (nodeId: string) => void;
  readonly onChangeBase: (nodeId: string) => void;
  readonly onAppendEdge: (
    parentOccurrenceId: string,
    edgeId: string,
    nodeId: string,
  ) => void;
  readonly onChangeEdge: (occurrenceId: string, edgeId: string) => void;
  readonly onChangeMatchMode: (
    occurrenceId: string,
    matchMode: 'OPTIONAL' | 'REQUIRED',
  ) => void;
  readonly onTruncate: (occurrenceId: string) => void;
  readonly projectionKind: 'saved' | 'proposed';
  readonly network?: import('../../../contracts/types').AuthoringNetworkResponse['network'];
  readonly rowIdentity?: import('../../../contracts/types').AuthoringNetworkResponse['rowIdentity'];
  readonly onEditSourceStep?: (sourceStepId: string) => void;
  readonly onInspectValues?: (occurrenceId: string) => void;
  readonly onInspectRelatedRows?: (occurrenceId: string) => void;
  readonly onInspectOperation?: (operationId: string) => void;
  readonly onDefineRow?: () => void;
  readonly onEditStartingRecords?: () => void;
  readonly mutationBlockedReason?: string;
}

const AuthoredTraversalContext = createContext<AuthoredTraversalSession | undefined>(undefined);

export function AuthoredTraversalSessionProvider({ value, children }: {
  readonly value: AuthoredTraversalSession;
  readonly children: ReactNode;
}) {
  return <AuthoredTraversalContext.Provider value={value}>{children}</AuthoredTraversalContext.Provider>;
}

export function useAuthoredTraversalSession<Selection>(select: (session: AuthoredTraversalSession) => Selection): Selection {
  const session = useContext(AuthoredTraversalContext);
  if (!session) throw new Error('Authored traversal requires its Builder session.');
  return select(session);
}
