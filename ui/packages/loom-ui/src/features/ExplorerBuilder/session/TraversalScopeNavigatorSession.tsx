import { createContext, useContext, type ReactNode } from 'react';
import type { AuthoringNetworkResponse, ConstructionStageDescriptor } from '../../../contracts/types';
import type { DraftTable } from '../model/authoring';

export interface TraversalScopeNavigatorSession {
  readonly table: DraftTable;
  readonly network?: AuthoringNetworkResponse['network'];
  readonly rowIdentity?: AuthoringNetworkResponse['rowIdentity'];
  readonly constructionStages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly sourceOperationLabels?: ReadonlyMap<string, string>;
  readonly selectedOccurrenceId?: string;
  readonly selectedOperation?: { readonly kind: 'source' | 'construction'; readonly stepId: string };
  readonly projectionKind: 'saved' | 'proposed';
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly sourceStepEditOnly?: boolean;
  readonly sourceOperationNavigationDisabled?: boolean;
  readonly onSelectScope: (occurrenceId: string) => void;
  readonly onEditSourceStep: (stepId: string) => void;
  readonly onEditSourceField?: (stepId: string, fieldId: string) => void;
  readonly onEditConstructionStep: (stepId: string) => void;
  readonly onRemoveSourceStep?: (stepId: string) => void;
  readonly onRemoveConstructionStep?: (stepId: string) => void;
  readonly onEditColumn: (columnId: string) => void;
  readonly onEditSourceRows: () => void;
  readonly onExpandRelatedRows: (occurrenceId: string) => void;
  readonly onGroupRows: (occurrenceId: string) => void;
}

const TraversalScopeNavigatorContext = createContext<TraversalScopeNavigatorSession | undefined>(undefined);

export function TraversalScopeNavigatorSessionProvider({ value, children }: {
  readonly value: TraversalScopeNavigatorSession;
  readonly children: ReactNode;
}) {
  return <TraversalScopeNavigatorContext.Provider value={value}>{children}</TraversalScopeNavigatorContext.Provider>;
}

export function useTraversalScopeNavigatorSession<Selection>(select: (state: TraversalScopeNavigatorSession) => Selection): Selection {
  const session = useContext(TraversalScopeNavigatorContext);
  if (!session) throw new Error('TraversalScopeNavigator requires its Builder session.');
  return select(session);
}
