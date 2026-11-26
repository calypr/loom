import { createContext, useContext, type ReactNode } from 'react';
import type { ExplorerBuilderPreviewResult, RowChangeAssessment, RowDefinitionProposal } from '../../../contracts/types';
import type { SelectionRevision } from '../../../contracts/selection';
import type { DraftTable } from '../model/authoring';
import type { ConstructionHistoryStep } from '../model/constructionHistory';
import type { RootChangePickerState, RootChangeSelection } from '../model/rootChangeSelection';
import type { RowDefinitionTransport } from './useRowDefinitionRequests';

export type ReadyReplacementAssessment = Extract<RowChangeAssessment, { readonly status: 'READY' }> & {
  readonly mode: 'REPLACE';
  readonly proposal: Extract<RowChangeAssessment, { readonly status: 'READY' }>['proposal'] & { readonly mode: 'REPLACE' };
};

export interface RowDefinitionEditorSession {
  readonly onOpenChange?: (open: boolean) => void;
  readonly onBeginRootProposal?: (nodeId: string) => {ready: (assessment: ReadyReplacementAssessment, preview: ExplorerBuilderPreviewResult) => boolean; failed: (message: string, stale?: boolean) => void} | undefined;
  readonly onCancelRootProposal?: () => void;
  readonly onBeginProposal?: () => { ready: (proposal: RowDefinitionProposal, preview: import('../../../contracts/types').ExplorerBuilderPreviewResult) => boolean; failed: (message: string) => void } | undefined;
  readonly onCancelProposal?: () => void;
  readonly client: RowDefinitionTransport;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly table: DraftTable;
  readonly workspaceTables: ReadonlyArray<DraftTable>;
  readonly currentRowMeaning: string;
  readonly startingCollectionSummary: string;
  readonly selection?: SelectionRevision;
  readonly constructionHistory?: ReadonlyArray<ConstructionHistoryStep>;
  readonly disabled: boolean;
  readonly onApply: (proposalId: string) => Promise<boolean>;
  readonly relatedRows: { readonly supported: boolean; readonly reason?: string };
  readonly reshapeRows: Record<'group' | 'pivot', { readonly supported: boolean; readonly reason?: string }> & {
    readonly pivotPending?: boolean;
    readonly groupEntry: 'group';
    readonly groupAlternatives: ReadonlyArray<{
      readonly kind: 'source-group' | 'coded-group';
      readonly label: string;
    }>;
    readonly pivotEntry?: 'pivot' | 'coded-pivot';
    readonly pivotAlternative?: 'pivot';
    readonly expandSigned?: { readonly supported: boolean };
  };
  readonly onChooseRelatedRows: () => void;
  readonly onChooseReshape: (kind: 'group' | 'source-group' | 'coded-group' | 'pivot' | 'coded-pivot' | 'categories' | 'unpivot' | 'expand-signed') => void;
  readonly onSwitchRowIntent?: (intent: 'records' | 'reshape') => void;
  readonly onCloseRowIntentSession?: () => void;
  readonly onChangeRootSelection: (selection: RootChangeSelection) => void;
  readonly onResolveRootSelection: (referenceId: string, alternative: string) => void;
  readonly rootChangeState: RootChangePickerState;
  readonly rootChangePreviewOpen: boolean;
  readonly onCancelRootChange: () => void;
  readonly onApplyRootReplacement: (assessment: ReadyReplacementAssessment) => Promise<boolean>;
  readonly onEditConstructionStep?: (stepId: string) => void;
  readonly onRemoveConstructionStep?: (stepId: string) => void;
}

const RowDefinitionEditorContext = createContext<RowDefinitionEditorSession | undefined>(undefined);

export function RowDefinitionEditorSessionProvider({ value, children }: {
  readonly value: RowDefinitionEditorSession;
  readonly children: ReactNode;
}) {
  return <RowDefinitionEditorContext.Provider value={value}>{children}</RowDefinitionEditorContext.Provider>;
}

export function useRowDefinitionEditorSession<Selection>(select: (session: RowDefinitionEditorSession) => Selection): Selection {
  const session = useContext(RowDefinitionEditorContext);
  if (!session) throw new Error('Row definition editor requires its Builder session.');
  return select(session);
}
