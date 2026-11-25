import { createContext, useContext, type ReactNode } from 'react';
import type {
  ExplorerBuilderCatalog, FieldChoiceSource, FieldPresentationRequest, SemanticInventorySelectionMode,
} from '../../../contracts/types';
import type { CatalogChoiceIntent } from '../model/catalogItems';
import type { ConceptCatalogView } from '../model/conceptCatalogView';
import type { CatalogInitialSelection, GroupedRowValuePolicyControl, RouteCoverage } from './useRouteCoverage';

interface CatalogRouteContext {
  readonly occurrenceId: string;
  readonly nodeId: string;
}

interface CatalogSourceProjectionAvailability {
  readonly available: boolean;
  readonly reason: string;
}

export interface CatalogRelatedSourceAvailability {
  readonly supported: boolean;
  readonly reason?: string;
}

export interface ConceptCatalogSession {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly outputId: string;
  readonly tableContextKey?: string;
  readonly rowRoot: string;
  readonly resourceType?: string;
  readonly sourceNodeId?: string;
  readonly routeContext?: CatalogRouteContext;
  readonly scopeToSource?: boolean;
  readonly structuredOnly?: boolean;
  readonly catalog: ExplorerBuilderCatalog;
  readonly disabled?: boolean;
  readonly disabledReason?: string;
  readonly sourceProjectionAvailability?: CatalogSourceProjectionAvailability;
  readonly relatedSourceAvailability?: CatalogRelatedSourceAvailability;
  readonly view?: ConceptCatalogView;
  readonly viewRevision?: number;
  readonly presentationContext?: Omit<FieldPresentationRequest, 'candidateIds'>;
  readonly onSwitchView?: (view: 'coded') => void;
  readonly semanticSelectionMode?: SemanticInventorySelectionMode;
  readonly existingSourceKeys?: ReadonlySet<string>;
  readonly nodeOccurrences?: Readonly<Record<string, string>>;
  readonly initialSelection?: CatalogInitialSelection;
  readonly initialFieldSource?: Pick<FieldChoiceSource, 'candidateId' | 'nodeId' | 'path'>;
  readonly groupedRowValuePolicy?: GroupedRowValuePolicyControl;
  readonly onAddSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => Promise<'preview-ready' | 'preview-pending' | void>;
  readonly onPreviewSelected?: (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => Promise<'preview-ready' | 'preview-pending' | void>;
  readonly onInspectRouteCoverage?: (selection: CatalogChoiceIntent, signal: AbortSignal) => Promise<RouteCoverage>;
}

const ConceptCatalogContext = createContext<ConceptCatalogSession | undefined>(undefined);

export function ConceptCatalogSessionProvider({ value, children }: {
  readonly value: ConceptCatalogSession;
  readonly children: ReactNode;
}) {
  return <ConceptCatalogContext.Provider value={value}>{children}</ConceptCatalogContext.Provider>;
}

export function useConceptCatalogSession<Selection>(select: (session: ConceptCatalogSession) => Selection): Selection {
  const session = useContext(ConceptCatalogContext);
  if (!session) throw new Error('Concept catalog requires its Builder session.');
  return select(session);
}
