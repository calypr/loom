import { admitAuthoringRequest } from './authoring/admission';
import React, { useCallback, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useAutomaticPreview } from './useAutomaticPreview';
import { useAutomaticPreviewOwner } from './authoring/useAutomaticPreviewOwner';
import { useReconcile } from './authoring/useReconcile';
import { constructionAppendStageFor, constructionInputStageFor } from './constructionWorkspace/constructionStages';
import { effectiveOutputAvailability } from './constructionWorkspace/outputEligibility';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useAssessExplorerRowChangeMutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useResolveConfiguredColumnContextsQuery,
  useResolvePopulationSelectionQuery,
  useGetExplorerCandidateSuggestionsV2Mutation,
  useLoomClient,
} from '../../react';
import type { ResourceRef, SelectionRevision } from '../../selection';
import { canonicalProject, type ExplorerAuthoringApiError } from '../../api';
import type {
  ExplorerAuthoringDiagnostic,
  ExplorerBuilderCatalog,
  ExplorerBuilderCommand,
  ExplorerBuilderCompileResult,
  ExplorerBuilderCandidate,
  ExplorerBuilderColumn,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  FieldPresentationRequest,
  ConstructionRouteStep,
  Construction,
  ConstructionOperation,
  ConstructionStep,
  ConstructionChoiceSelection,
  SourceRelationshipChange,
  AuthoringNetworkResponse,
  SourceRelationshipProposal,
  SourceRelationshipProposalWithPreview,
  RowDefinitionProposal,
} from '../../types';
import { BuilderToolbar, NewExplorerControl } from './components/BuilderToolbar';
import {
  DatasetReviewPanel,
  type DatasetReviewTarget,
} from './components/DatasetReviewPanel';
import { useRecipeEditingOwner, projectRecipeCandidate, type RecipeEditToken } from './recipeEditing/recipeEditingOwner';
import { SourceRelationshipReview } from './components/SourceRelationshipReview';
import { AuthoredTraversalEditor } from './components/AuthoredTraversalEditor';
import { TraversalScopeNavigator } from './components/TraversalScopeNavigator';
import { RecipeColumnPolicyInspector } from './components/RecipeColumnPolicyInspector';
import { ConceptCatalog } from './components/ConceptCatalog';
import {
  PreviewTable,
} from './components/PreviewTable';
import { PopulationPanel } from './components/PopulationPanel';
import { RowChangePreviewPanel } from './components/RowChangePreviewPanel';
import { RowDefinitionPanel } from './components/RowDefinitionPanel';
import { RowDefinitionSettingsPanel, rowScopeLabel } from './components/RowDefinitionSettingsPanel';
import { useBrowserSubscription } from './hooks/useBrowserSubscription';
import { FrameSourcePanel } from './constructionWorkspace/FrameSourcePanel';
import { InterpretationPanel, type InterpretationContextState } from './components/InterpretationPanel';
import {
  derivedOccurrences,
  intentFingerprint,
  routeNode,
  routeSubtreeOccurrenceIds,
  selectedTable,
  workspaceFromState,
  type BuilderAuthoringState,
} from './authoring/model';
import {
  isCompilerNavigationSelection,
} from './authoring/workspaceGuards';
import {
  sameRootChangeRequestIdentity,
} from './authoring/rootChangeSelection';
import {
  planInitialTable,
  type InitialTableCandidateEvidence,
  type InitialTablePlan,
} from './authoring/initialTablePlan';
import type { PreviewLimit } from './authoring/previewRecovery';
import { useCommandQueue } from './authoring/useCommandQueue';
import { usePublish } from './authoring/usePublish';
import { shouldShowBuilderLoadError } from './authoring/builderLoadError';
import { useBuilderDraft } from './authoring/useBuilderDraft';
import { useRecipeFocus } from './authoring/useRecipeFocus';
import { useAddColumns } from './authoring/useAddColumns';
import { createRecipeNetworkLoader } from './authoring/recipeNetwork';
import { useRootChange } from './authoring/useRootChange';
import { useCombineCatalog } from './authoring/useCombineCatalog';
import { useDirtyBeforeUnload } from './hooks/useDirtyBeforeUnload';
import { usePortalHost } from './hooks/usePortalHost';
import { sameConstructionRoute } from './populationRoutes';
import { fieldCatalogItems, sanitizeReasonText, unavailableOperationReason } from './catalogItems';

import {
  ConstructionWorkspace,
  ConstructionActionBar,
  constructionOperationFamilies,
  type ConstructionHistorySelection,
  type ConstructionOperationFamily,
} from './constructionWorkspace/ConstructionWorkspace';
import {
  ConstructionColumnSelection,
  type ConstructionSelectableColumn,
} from './constructionWorkspace/ConstructionColumnSelection';
import { ConstructionProposalPanel } from './constructionWorkspace/ConstructionProposalPanel';
import { ConstructionProposalPreview } from './constructionWorkspace/ConstructionProposalPreview';
import { ImportedSourceSteps } from './ImportedSourceSteps';
import {
  constructionHistorySteps,
  constructionRowMeaning,
} from './constructionWorkspace/constructionHistory';
import {
  sourceProjectionAvailability,
} from './constructionWorkspace/sourceProjectionAvailability';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
} from './constructionWorkspace/useConstructionLifecycle';
import { ConstructionOperationEditor } from './constructionOperations/ConstructionOperationEditor';
import { FilterRowsEditor } from './constructionOperations/FilterRowsEditor';
import { ConstructionReshapeEditor, createReshapeEditorEntry, publicScalarListColumnIds, type ConstructionReshapeEditorEntry, type ReshapeEntryKind, type ReshapeForm } from './constructionOperations/ConstructionReshapeEditor';
import type { RelatedSourceStep } from './constructionOperations/RelatedSourceStepEditor';
import { RelatedFieldEditor, type RelatedFieldStep } from './constructionOperations/RelatedFieldEditor';
import { ConstructionCombineEditor } from './constructionOperations/ConstructionCombineEditor';
import type { ConstructionCombineStep } from './constructionOperations/combineEditorTypes';
import { TableCreatorPanel } from './workspacePanels/TableCreatorPanel';
import { OperationEditorHost } from './workspacePanels/OperationEditorHost';
import { RelatedStepEditors } from './workspacePanels/RelatedStepEditors';

const receiptContainsOutputIds = (
  receipt: ExplorerBuilderCompileResult | undefined,
  outputIds: ReadonlyArray<string>,
): boolean => receipt !== undefined && outputIds.every((outputId) =>
  receipt.outputs.some((output) => output.outputId === outputId),
);

const isRelatedFieldStep = (step: ConstructionStep): step is RelatedFieldStep =>
  step.operation.kind === 'RELATED_FIELD';

type ColumnSelectionState =
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

type SelectionOwnerStateStore = {
  readonly getSnapshot: () => SelectionOwnerState;
  readonly subscribe: (subscriber: () => void) => () => void;
  readonly update: (update: (current: SelectionOwnerState) => SelectionOwnerState) => void;
};

const createSelectionOwnerStateStore = (): SelectionOwnerStateStore => {
  let state = emptySelectionOwnerState;
  const subscribers = new Set<() => void>();
  return {
    getSnapshot: () => state,
    subscribe: (subscriber) => {
      subscribers.add(subscriber);
      return () => subscribers.delete(subscriber);
    },
    update: (update) => {
      const next = update(state);
      if (next === state) return;
      state = next;
      for (const subscriber of subscribers) subscriber();
    },
  };
};

const suppressedBuilderDiagnostic = (diagnostic: ExplorerAuthoringDiagnostic): boolean =>
  diagnostic.code === 'CONSTRUCTION_PREVIEW_FAILED';

const diagnosticsFromError = (
  error: unknown,
): ReadonlyArray<ExplorerAuthoringDiagnostic> => {
  const value = error as ExplorerAuthoringApiError | undefined;
  const diagnostics = value?.diagnostics?.length
    ? value.diagnostics
    : [{
        severity: 'error' as const,
        code: value?.code ?? 'EXPLORER_AUTHORING_FAILED',
        message: value?.message ?? 'Loom could not process the Builder request.',
        requestId: value?.requestId,
      }];
  return diagnostics.filter((diagnostic) => !suppressedBuilderDiagnostic(diagnostic));
};
const isUnsupportedUnknownSourceProjectionDiagnostic = (
  diagnostic: ExplorerAuthoringDiagnostic,
): boolean =>
  diagnostic.code === 'DOCUMENT_COMPILE_FAILED' &&
  diagnostic.message.includes('source projection[') &&
  diagnostic.message.includes('].type "unknown" is unsupported');

const unsupportedSavedSourceColumns = (tables: BuilderAuthoringState['tables']) =>
  tables.flatMap((table) =>
    table.document.columns
      .filter((column) => column.source.kind === 'field' && column.logicalType === 'unknown')
      .map((column) => ({
        outputId: table.outputId,
        tableTitle: table.document.output.title,
        column: column.column,
        label: column.label,
      })),
  );
const opaqueId = (prefix: 'output' | 'tab' | 'step') =>
  `${prefix}-${window.crypto.randomUUID()}`;
const constructionSourceStageId = 'source_projection';

const editableConstructionFamily = (
  operation: ConstructionOperation,
): Extract<ConstructionOperationFamily, 'KEEP_ROWS' | 'CALCULATE' | 'RESHAPE' | 'COMBINE'> | undefined => {
  switch (operation.kind) {
    case 'FILTER':
    case 'RELATED_ELIGIBILITY': return 'KEEP_ROWS';
    case 'DERIVE': return 'CALCULATE';
    case 'PIVOT':
    case 'CODED_PIVOT':
    case 'UNPIVOT':
    case 'GROUP':
    case 'CODED_GROUP':
    case 'EXPAND':
    case 'RELATED_EXPAND': return 'RESHAPE';
    case 'RELATED_SOURCE': return undefined;
    case 'RELATED_FIELD': return undefined;
    case 'COMBINE': return operation.combine.kind === 'KEY_JOIN' || operation.combine.kind === 'APPEND' || operation.combine.kind === 'MEMBERSHIP'
      ? 'COMBINE'
      : undefined;
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

type FirstTableProgress =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'running';
      readonly resourceType: string;
      readonly phase: 'checking' | 'creating' | 'adding-id' | 'previewing';
    };

type SourceProposalOwnerCandidate = Pick<SourceRelationshipProposal,
  'proposalId' | 'candidateDocument' | 'candidateNetwork' | 'candidateRowIdentity' |
  'candidateWorkspaceDigest' | 'candidateReceiptId' | 'outputId' | 'snapshotToken' |
  'draftVersion' | 'draftDigest'> &
  Partial<Pick<SourceRelationshipProposal, 'affectedColumns' | 'notices'>>;

const builderOwnerKeyFor = (
  project: string,
  authResourcePath: string | undefined,
  explorerId: string,
): string => JSON.stringify([
  canonicalProject(project),
  authResourcePath?.trim() ?? '',
  explorerId,
]);

const populationBindingKey = (
  table: BuilderAuthoringState['tables'][number] | undefined,
  selectionRevisionId = table?.document.population?.selectionRevisionId,
) => JSON.stringify([
  selectionRevisionId ?? '',
  (table?.document.population?.route ?? []).map((step) => [
    step.resourceType, step.relationship, step.catalogEdgeId ?? '', step.storageDirection ?? '',
  ]),
]);

const BuilderWorkspaceContent = ({
  organization,
  project,
  explorerId,
  populationSelection,
  populationSelectionLoading = false,
  populationSelectionError,
  onExplorerChange,
  featureFocus,
  workspaceComposition,
}: {
  readonly organization?: string;
  readonly project: string;
  readonly explorerId?: string;
  readonly populationSelection?: SelectionRevision;
  readonly populationSelectionLoading?: boolean;
  readonly populationSelectionError?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly workspaceComposition?: React.ComponentProps<typeof ConstructionWorkspace>['composition'];
  readonly featureFocus?: {
    readonly outputId: string;
    readonly column: string;
    readonly label?: string;
  };
}) => {
  const loomClient = useLoomClient();
  const projectId = organization ? `${organization}/${project}` : project;
  const authResourcePath = organization
    ? `/programs/${organization}/projects/${project}`
    : undefined;
  const [selectedExplorerId, setSelectedExplorerId] = useState(
    !explorerId || explorerId === 'default' ? 'dataframe-default' : explorerId,
  );
  const explorers = useGetExplorerAuthoringExplorersQuery({
    project: projectId,
    authResourcePath,
  });
  const effectiveExplorerId =
    selectedExplorerId && selectedExplorerId !== 'default'
      ? selectedExplorerId
      : 'dataframe-default';
  const explorerSummaries = useMemo(() => {
    const available = explorers.data ?? [];
    if (available.some((candidate) => candidate.explorerId === 'dataframe-default')) return available;
    return [{
      project: projectId,
      explorerId: 'dataframe-default',
      title: 'Shared default dataframe',
      management: 'SYSTEM',
      updatedAt: '',
    }, ...available];
  }, [explorers.data, projectId]);
  const ownerKey = builderOwnerKeyFor(projectId, authResourcePath, effectiveExplorerId);
  const workspaceAttached = useRef(false);
  const workspaceHostRef = useCallback((node: HTMLElement | null) => {
    workspaceAttached.current = node !== null;
  }, []);
  const {
    builderQueryArgs,
    builder,
    refetchBuilder,
    explorerQueryEnabled,
    builderDataKey,
    state,
    dispatch,
    rememberSelectedTable,
    previousDraftRevisionId,
    setLocalState,
    setRestorableDraft,
    serverDraft,
    serverDraftKey,
    latestState,
    syncBuilderData,
  } = useBuilderDraft({
    projectId,
    authResourcePath,
    effectiveExplorerId,
    ownerKey,
    builderOwnerKeyFor,
  });
  if (selectedExplorerId === 'dataframe-default' &&
      (builder.error as { code?: string } | undefined)?.code === 'DEFAULT_DATAFRAME_UNAVAILABLE') {
    const fallback = (explorers.data ?? []).find((candidate) => candidate.explorerId !== 'dataframe-default' && candidate.explorerId !== 'default');
    if (fallback) setSelectedExplorerId(fallback.explorerId);
  }
  const capabilities = useGetExplorerAuthoringCapabilityV2Query(builderQueryArgs);
  const [createExplorer, createStatus] = useCreateExplorerAuthoringMutation();
  const [deleteExplorer, deleteStatus] = useDeleteExplorerAuthoringMutation();
  const [applyBuilderCommands] = useApplyExplorerBuilderCommandsV2Mutation();
  const [assessRowChange, rowChangeStatus] = useAssessExplorerRowChangeMutation();
  const [getSuggestions, suggestionsStatus] =
    useGetExplorerCandidateSuggestionsV2Mutation();
  const [message, setMessage] = useState<string>();
  const [suggestionsFailure, setSuggestionsFailure] = useState<{
    readonly key: string;
    readonly message: string;
  }>();
  const [occurrenceInspectorHost, setOccurrenceInspectorHost] = useState<HTMLElement | null>(null);
  const [sourceChangeReviewHost, setSourceChangeReviewHost] = useState<HTMLDivElement | null>(null);
  const [cancelingConstructionProposal, setCancelingConstructionProposal] = useState(false);
  const cancelingConstructionProposalRef = useRef(false);
  const [populationSelectionOverride, setPopulationSelectionOverride] = useState<{
    readonly contextKey: string;
    readonly baseSelectionID: string;
    readonly selection: SelectionRevision;
  }>();
  const [clearedPopulationSelection, setClearedPopulationSelection] = useState<{
    readonly contextKey: string;
    readonly client: typeof loomClient;
    readonly selectionID: string;
  }>();
  const [populationVariantError, setPopulationVariantError] = useState<{
    readonly contextKey: string;
    readonly message: string;
  }>();
  const [populationVariantPending, setPopulationVariantPending] = useState(false);
  const [firstTableName, setFirstTableName] = useState('');
  const [tableCreatorOpen, setTableCreatorOpen] = useState(false);
  const [firstTableProgress, setFirstTableProgress] =
    useState<FirstTableProgress>({ kind: 'idle' });
  const [featureMode, setFeatureMode] = useState<'catalog' | 'graph'>('catalog');
  const [editingRecipeColumn, setEditingRecipeColumn] = useState<{ owner: string; columnId: string }>();
  const [sourceStepInspectorHost, setSourceStepInspectorHost] = useState<HTMLDivElement | null>(null);
  const [activeSourceStepId, setActiveSourceStepId] = useState<string>();
  const columnPolicyRequest = useRef<AbortController | undefined>(undefined);
  const sourceRelationshipRequest = useRef<AbortController | undefined>(undefined);
  const networkRequestOwner = useRef('');
  const sourceStepEditAction = useRef<((stepId: string) => void) | undefined>(undefined);
  const sourceFieldEditAction = useRef<((stepId: string, fieldId: string) => void) | undefined>(undefined);
  const sourceOutputEditAction = useRef<((stepId: string, outputKey: string) => void) | undefined>(undefined);
  const rowSettingsOpenAction = useRef<(() => void) | undefined>(undefined);
  const rowSettingsCloseAction = useRef<(() => void) | undefined>(undefined);
  const recipeEditToken = useRef<RecipeEditToken | undefined>(undefined);
  const [candidateInspection, setCandidateInspection] = useState<{ key: string; occurrenceId: string }>();
  const [sourceProposal, setSourceProposal] = useState<{ owner: string; proposal: SourceProposalOwnerCandidate; preview: ExplorerBuilderPreviewResult }>();
  const lastAcceptedOutputPreview = useRef<{ readonly owner: string; readonly outputId: string; readonly snapshotToken: string; readonly draftVersion: number; readonly draftDigest: string; readonly receiptId: string } | undefined>(undefined);
  const [connectingRelationship, setConnectingRelationship] = useState(false);
  const [savedNetwork, setSavedNetwork] = useState<{ owner: string; result: AuthoringNetworkResponse }>();
  const [recipeNotice, setRecipeNotice] = useState<string>();
  const [relationshipEdit, setRelationshipEdit] = useState<{ owner: string; change: SourceRelationshipChange }>();
  const [activeConstructionSelection, setActiveConstructionSelection] = useState<{
    readonly ownerKey: string;
    readonly explorerId: string;
    readonly outputId: string;
    readonly family: ConstructionOperationFamily;
  }>();
  const [rowValuePolicy, setRowValuePolicy] = useState<NonNullable<ConstructionChoiceSelection['rowValuePolicy']>>('ALL');
  const [reshapeEntry, setReshapeEntry] = useState(0);
  const [reshapeEntryKind, setReshapeEntryKind] = useState<ReshapeEntryKind>('choose');
  const [reshapeFormKind, setReshapeFormKind] = useState<ReshapeForm['kind']>('choose');
  const [reshapeInitialEntry, setReshapeInitialEntry] = useState<{
    readonly editorKey: string;
    readonly entry: ConstructionReshapeEditorEntry;
  }>();
  const [reviewOpen, setReviewOpen] = useState(false);
  const [reviewFocusTarget, setReviewFocusTarget] =
    useState<Exclude<DatasetReviewTarget, { readonly kind: 'new-table' }>>();
  const [reviewFocusColumn, setReviewFocusColumn] = useState<
    Extract<DatasetReviewTarget, { readonly kind: 'column' }>
  >();
  const toolbarHost = usePortalHost('explorer-builder-toolbar-host');
  const [tableToolbarHost, setTableToolbarHost] = useState<HTMLElement | null>(
    null,
  );
  const firstTableActionPending = useRef(false);
  const suggestionRequestKey = useRef('');
  const combineCatalog = useCombineCatalog({
    ownerKey,
    explorerId: state.explorerId,
    projectId,
    authResourcePath,
    state,
    latestState,
    loomClient,
  });
  const activeConstructionFamily = activeConstructionSelection?.ownerKey === ownerKey &&
    activeConstructionSelection.explorerId === state.explorerId &&
    activeConstructionSelection.outputId === state.selectedOutputId
    ? activeConstructionSelection.family
    : undefined;
  const setActiveConstructionFamily = (
    next: ConstructionOperationFamily | undefined |
      ((current: ConstructionOperationFamily | undefined) => ConstructionOperationFamily | undefined),
  ) => {
    const currentState = latestState.current;
    const outputId = currentState.selectedOutputId;
    if (!outputId) {
      setActiveConstructionSelection(undefined);
      return;
    }
    setActiveConstructionSelection((current) => {
      const currentFamily = current?.ownerKey === ownerKey &&
        current.explorerId === currentState.explorerId &&
        current.outputId === outputId
        ? current.family
        : undefined;
      const family = typeof next === 'function' ? next(currentFamily) : next;
      return family
        ? { ownerKey, explorerId: currentState.explorerId, outputId, family }
        : undefined;
    });
  };
  const selectTableFromUser = useCallback((outputId: string) => {
    setActiveConstructionSelection(undefined);
    rememberSelectedTable(outputId);
    dispatch({ type: 'selectTable', outputId });
  }, [dispatch, rememberSelectedTable]);
  const [interpretationContextRefreshVersion, setInterpretationContextRefreshVersion] = useState(0);

  const deferInterpretationContext = firstTableProgress.kind === 'running';
  const interpretationContextKey = !deferInterpretationContext &&
    state.catalog.snapshotToken &&
    state.draftVersion > 0 &&
    state.draftDigest
    ? JSON.stringify([
        ownerKey,
        state.catalog.snapshotToken,
        state.draftVersion,
        state.draftDigest,
        interpretationContextRefreshVersion,
      ])
    : '';
  const interpretationContextArgs = interpretationContextKey
    ? {
      project: projectId,
      explorerId: effectiveExplorerId,
      authResourcePath,
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest,
    }
    : undefined;
  const interpretationContextQuery = useResolveConfiguredColumnContextsQuery(
    interpretationContextArgs,
    interpretationContextKey,
  );

  const interpretationPanelContextState: InterpretationContextState = deferInterpretationContext
    ? { status: 'loading' }
    : !interpretationContextKey
      ? { status: 'error', message: 'The saved draft identity is not available.' }
      : interpretationContextQuery.isLoading
        ? { status: 'loading' }
        : interpretationContextQuery.data
          ? { status: 'ready', response: interpretationContextQuery.data }
          : {
            status: 'error',
            message: interpretationContextQuery.error instanceof Error
              ? interpretationContextQuery.error.message
              : 'Loom could not load interpretation context.',
          };
  const interpretationContext = interpretationPanelContextState.status === 'ready'
    ? interpretationPanelContextState.response
    : undefined;

  const selectExplorer = (nextExplorerId: string) => {
    setActiveConstructionSelection(undefined);
    combineCatalog.invalidate();
    cancelAutomaticPreview();
    compileGeneration.current += 1;
    previewGeneration.current += 1;
    activeCompile.current?.abort();
    activePreview.current?.abort();
    if (onExplorerChange) {
      onExplorerChange(nextExplorerId);
    } else {
      setSelectedExplorerId(nextExplorerId);
    }
  };

  const incomplete = state.tables.some(
    (table) => !table.document.rootResourceType,
  );

  const { compileGeneration, previewGeneration, activeCompile, activePreview, reconcileStatus, previewStatus, reconcileCurrent, executePreview } = useReconcile({
    projectId,
    authResourcePath,
    latestState,
    workspaceAttached,
    dispatch,
    refetchBuilder,
    syncBuilderData,
    setMessage,
    diagnosticsFromError,
  });

  const { previewLimit, setPreviewLimit, appliedChoicePreview, automaticPreviewGeneration, automaticPreviewRequest, cancelAutomaticPreview, queryOptionsFor } = useAutomaticPreviewOwner({
    ownerKey,
    projectId,
    state,
    latestState,
    compileGeneration,
    previewGeneration,
    dispatch,
    setMessage,
  });
  const { pendingCommands, commandRecipeAdmission, relatedExpandQueryOwnerRef, applyCommandsWithResult, applyCommands } = useCommandQueue({
    builderOwnerKeyFor,
    diagnosticsFromError,
    applyBuilderCommands,
    authResourcePath,
    builderDataKey,
    cancelAutomaticPreview,
    dispatch,
    projectId,
    refetchBuilder,
    rememberSelectedTable,
    syncBuilderData,
    latestState,
    serverDraft,
    serverDraftKey,
    automaticPreviewGeneration,
    compileGeneration,
    previewGeneration,
    activeCompile,
    activePreview,
    automaticPreviewRequest,
    appliedChoicePreview,
    setRestorableDraft,
    setActiveConstructionSelection,
    setLocalState,
    setMessage,
  });

  useDirtyBeforeUnload(state.dirty || pendingCommands > 0);

  const [sourceRowsInspectorHost, setSourceRowsInspectorHost] = useState<HTMLElement | null>(null);
  const table = selectedTable(state);
  const selectionOwnerIdentity = `${ownerKey}\u0000${state.explorerId}\u0000${table?.outputId ?? ''}`;
  const selectionOwnerStore = useMemo(createSelectionOwnerStateStore, [selectionOwnerIdentity]);
  const selectionOwnerState = React.useSyncExternalStore(
    selectionOwnerStore.subscribe,
    selectionOwnerStore.getSnapshot,
    selectionOwnerStore.getSnapshot,
  );
  const updateSelectionOwnerField = useCallback(
    (update: (current: SelectionOwnerState) => SelectionOwnerState) => selectionOwnerStore.update(update),
    [selectionOwnerStore],
  );
  const setRowIntentSession = useCallback<React.Dispatch<React.SetStateAction<boolean>>>(
    (update) => updateSelectionOwnerField((current) => ({
      ...current,
      rowIntentSession: typeof update === 'function' ? update(current.rowIntentSession) : update,
    })),
    [updateSelectionOwnerField],
  );
  const setSourceRowsOpen = useCallback<React.Dispatch<React.SetStateAction<boolean>>>(
    (update) => updateSelectionOwnerField((current) => ({
      ...current,
      sourceRowsOpen: typeof update === 'function' ? update(current.sourceRowsOpen) : update,
    })),
    [updateSelectionOwnerField],
  );
  const setConstructionHistorySelection = useCallback<
    React.Dispatch<React.SetStateAction<ConstructionHistorySelection>>
  >(
    (update) => updateSelectionOwnerField((current) => ({
      ...current,
      constructionHistorySelection: typeof update === 'function'
        ? update(current.constructionHistorySelection)
        : update,
    })),
    [updateSelectionOwnerField],
  );
  const setEditingConstructionStepId = useCallback<
    React.Dispatch<React.SetStateAction<string | undefined>>
  >(
    (update) => updateSelectionOwnerField((current) => ({
      ...current,
      editingConstructionStepId: typeof update === 'function'
        ? update(current.editingConstructionStepId)
        : update,
    })),
    [updateSelectionOwnerField],
  );
  const setColumnSelection = useCallback<React.Dispatch<React.SetStateAction<ColumnSelectionState>>>(
    (update) => updateSelectionOwnerField((current) => ({
      ...current,
      columnSelection: typeof update === 'function' ? update(current.columnSelection) : update,
    })),
    [updateSelectionOwnerField],
  );
  const {
    rowIntentSession,
    sourceRowsOpen,
    constructionHistorySelection,
    editingConstructionStepId,
    columnSelection,
  } = selectionOwnerState;
  const recipeOwner = useRecipeEditingOwner({ project: projectId, explorerId: state.explorerId,
    outputId: table?.outputId ?? '', snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion, draftDigest: state.draftDigest });
  const {
    recipeFocusGeneration,
    pendingRecipeFocus,
    recipeInitiator,
    recipeInitiatorFact,
    rememberRecipeFocus,
    rememberOperationFocus,
    restoreRecipeFocus,
    completePendingRecipeFocus,
  } = useRecipeFocus({
    recipeOwner,
    workspaceAttached,
    latestState,
  });
  const {
    rootChangeState,
    setRootChangeState,
    rootChangeStateRef,
    pendingRowChangePreview,
    setPendingRowChangePreview,
    rootRecipeAssessment,
    rootRecipeActions,
    rootReplacementInspectorHost,
    setRootReplacementInspectorHost,
    rootChangeIdentityForTable,
    currentRootChangeIdentity,
    getCurrentRootChangeIdentity,
    cancelRootChange,
    changeTableRoot,
    selectRootChoice,
    resolveRootChange,
    refreshRootChange,
    applyRowChangePreview,
  } = useRootChange({
    projectId,
    authResourcePath,
    state,
    table,
    latestState,
    serverDraft,
    workspaceAttached,
    assessRowChange,
    loomClient,
    previewLimit,
    refetchBuilder,
    syncBuilderData,
    applyCommands,
    rowSettingsOpenAction,
    rowSettingsCloseAction,
    setMessage,
  });
  commandRecipeAdmission.current = (commands) => {
    if (recipeOwner.state.status === 'saved') return true;
    const candidate = recipeOwner.state.candidate;
    const rootAssessment = rootRecipeAssessment.current;
    if (candidate && recipeOwner.state.target?.kind === 'root' && rootAssessment && commands.length === 1 &&
      commands[0].type === 'APPLY_TABLE_ROOT_REBASE' && commands[0].outputId === recipeOwner.state.identity.outputId &&
      rootAssessment.candidateReceiptId === candidate.receiptId &&
      JSON.stringify(commands[0].rowChange) === JSON.stringify(rootAssessment.proposal)) return true;
    if (candidate && commands.length === 1 && commands[0].outputId === recipeOwner.state.identity.outputId &&
      commands[0].proposalId === candidate.receiptId && ['APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', 'APPLY_COLUMN_POLICY_PROPOSAL',
        'APPLY_ROW_DEFINITION_PROPOSAL', 'APPLY_CONSTRUCTION_PROPOSAL'].includes(commands[0].type)) return true;
    setRecipeNotice('Apply or Cancel the active recipe change before starting another edit.');
    return false;
  };
  rootRecipeActions.current = {
    begin: (nodeId, mode) => {
      const existing = recipeOwner.getState();
      if (existing.status !== 'saved' && (existing.target?.kind !== 'root' || existing.target.mode !== mode || existing.target.nodeId !== nodeId)) {
        setRecipeNotice('Apply or Cancel the active recipe change before choosing another starting record.');
        return undefined;
      }
      if (existing.status === 'saved') {
        constructionLifecycle.cancel(); setActiveConstructionFamily(undefined); setEditingConstructionStepId(undefined); setRowIntentSession(false);
      }
      if (!recipeInitiator.current?.isConnected) rememberRecipeFocus(document.querySelector('[data-testid="construction-rows-settings-trigger"]'));
      const initial = existing.status === 'saved' ? recipeOwner.begin({kind:'root', mode, nodeId}) : recipeEditToken.current;
      if (!initial) return undefined;
      const attempt = recipeOwner.previewing(initial);
      if (!attempt) return undefined;
      recipeEditToken.current = attempt;
      rootRecipeAssessment.current = undefined;
      setSourceProposal(undefined);
      return {
        failed: (message, stale = false) => { recipeOwner.fail(attempt, message, stale); },
        noChange: () => { recipeOwner.cancel(attempt); recipeEditToken.current = undefined; restoreRecipeFocus(); },
        ready: (assessment, preview) => {
          if (assessment.snapshotToken !== attempt.identity.snapshotToken ||
            assessment.draftVersion !== attempt.identity.draftVersion || assessment.draftDigest !== attempt.identity.draftDigest ||
            assessment.proposal.outputId !== attempt.identity.outputId || assessment.proposal.rootNodeId !== nodeId ||
            (assessment.mode ?? 'PRESERVE') !== mode || (assessment.proposal.mode ?? 'PRESERVE') !== mode || !assessment.candidateReceiptId ||
            !assessment.candidateDocument || !assessment.candidateNetwork || !assessment.candidateRowIdentity) {
            recipeOwner.fail(attempt, 'The starting-record preview is incomplete or belongs to another draft. Cancel and review the current table.', true);
            return false;
          }
          const accepted = recipeOwner.ready(attempt, {receiptId:assessment.candidateReceiptId, preview,
            candidateDocument:assessment.candidateDocument, candidateNetwork:assessment.candidateNetwork,
            candidateRowIdentity:assessment.candidateRowIdentity});
          if (accepted) {
            rootRecipeAssessment.current = assessment;
            setSourceRowsOpen(false);
            lastAcceptedOutputPreview.current = { owner: relationshipOwner, outputId: attempt.identity.outputId,
              snapshotToken: attempt.identity.snapshotToken, draftVersion: attempt.identity.draftVersion,
              draftDigest: attempt.identity.draftDigest, receiptId: preview.receiptId };
          }
          else recipeOwner.fail(attempt, 'The starting-record preview does not belong to this recipe edit.');
          return accepted;
        },
      };
    },
    cancel: () => {
      if (recipeOwner.getState().target?.kind !== 'root') return true;
      const token = recipeEditToken.current;
      if (token && !recipeOwner.cancel(token)) return false;
      recipeEditToken.current = undefined;
      rootRecipeAssessment.current = undefined;
      restoreRecipeFocus();
      return true;
    },
    apply: async (assessment) => {
      const token = recipeEditToken.current;
      const current = rootRecipeAssessment.current;
      if (!token || !current || current.candidateReceiptId !== assessment.candidateReceiptId ||
        JSON.stringify(current.proposal) !== JSON.stringify(assessment.proposal) || !recipeOwner.applying(token)) return false;
      try {
        const applied = await applyCommands([{type:'APPLY_TABLE_ROOT_REBASE', outputId:token.identity.outputId, rowChange:assessment.proposal}]);
        if (applied) {
          recipeOwner.applied(token); recipeEditToken.current = undefined; rootRecipeAssessment.current = undefined;
          setPendingRowChangePreview(undefined); setRootChangeState({kind:'idle'}); restoreRecipeFocus();
        } else recipeOwner.fail(token, 'The starting-record change was not saved. Review the current draft.');
        return applied;
      } catch (cause) { recipeOwner.fail(token, cause instanceof Error ? cause.message : 'The starting-record change was not saved.'); return false; }
    },
  };
  const recipeMutationAllowed = () => {
    if (recipeOwner.state.status === 'saved' && rootChangeStateRef.current.kind === 'idle' && !pendingRowChangePreview) return true;
    setRecipeNotice('Apply or Cancel the active recipe change before starting another edit.');
    return false;
  };

  const candidateForPreviewColumn = (column: ExplorerBuilderColumn): ExplorerBuilderCandidate | undefined => {
    const context = interpretationContext;
    if (
      !table ||
      !context ||
      context.snapshotToken !== state.catalog.snapshotToken ||
      context.draftVersion !== state.draftVersion ||
      context.draftDigest !== state.draftDigest
    ) return undefined;
    const configuredColumn = context.columns.find((candidate) =>
      candidate.outputId === table.outputId &&
      candidate.column === column.column &&
      candidate.occurrenceId === column.occurrenceId,
    );
    if (configuredColumn?.resolution.state !== 'READY') return undefined;
    const candidateIds = new Set(configuredColumn.resolution.capabilityCandidateIds);
    if (candidateIds.size !== 1) return undefined;
    const [candidateId] = candidateIds;
    return state.catalog.candidates?.find((candidate) => candidate.candidateId === candidateId);
  };
  const unsupportedColumns = unsupportedSavedSourceColumns(state.tables);
  const hasUnsupportedSavedSourceColumns = unsupportedColumns.length > 0;
  const construction = table?.document.construction;
  const editingConstructionStep = construction?.steps.find(
    (step) => step.id === editingConstructionStepId,
  );
  const needsSemanticGroupChoices = editingConstructionStep?.operation.kind === 'GROUP' ||
    (activeConstructionFamily === 'RESHAPE' &&
      (reshapeFormKind === 'group' || reshapeFormKind === 'source-group' || reshapeFormKind === 'coded-group'));
  const capabilitiesRequest = firstTableProgress.kind !== 'running' &&
    !hasUnsupportedSavedSourceColumns && table?.document.rootResourceType &&
    state.catalog.snapshotToken &&
    state.draftVersion > 0 &&
    state.draftDigest
    ? {
        project: projectId,
        explorerId: state.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        snapshotToken: state.catalog.snapshotToken,
        expectedDraftVersion: state.draftVersion,
        expectedDraftDigest: state.draftDigest,
        outputId: table.outputId,
        ...(editingConstructionStep?.operation.kind === 'GROUP' ? { groupStepId: editingConstructionStep.id } : {}),
        stageId: editingConstructionStep
          ? constructionInputStageFor(construction, editingConstructionStep.id)
          : constructionAppendStageFor(construction, table?.document.rows),
        includeGroupChoices: needsSemanticGroupChoices,
      }
    : undefined;
  const constructionLifecycle = useConstructionLifecycle({
    client: loomClient,
    capabilitiesRequest,
    previewLimit,
    editingOwner: { onStart: (intent, request) => {
      const existing = recipeOwner.state;
      if (existing.status === 'saved' && (rootChangeStateRef.current.kind !== 'idle' || pendingRowChangePreview)) return undefined;
      let token = recipeEditToken.current;
      if (existing.status !== 'saved' && !(existing.target?.kind === 'operation'
        && existing.target.operationId === intent.changedStepId && existing.target.stageId === request.stageId)) return undefined;
      if (existing.status === 'saved' && !recipeInitiator.current?.isConnected) rememberRecipeFocus();
      if (!token || existing.status === 'saved') token = recipeOwner.begin(intent.changedStepId
        ? { kind: 'operation', operationId: intent.changedStepId, stageId: request.stageId }
        : { kind: 'appendOperation', operationKind: 'construction', stageId: request.stageId });
      if (!token) return undefined;
      const attempt = recipeOwner.previewing(token);
      if (!attempt) return undefined;
      recipeEditToken.current = attempt;
      return {
        ready: (response, preview) => recipeOwner.ready(attempt, { receiptId: response.proposalId ?? '',
          candidateWorkspaceDigest: response.candidateWorkspaceDigest, preview, candidateConstruction: response.candidateConstruction }),
        failed: (message) => { recipeOwner.fail(attempt, message); },
        cancel: () => { const canceled = recipeOwner.cancel(attempt); if (canceled) restoreRecipeFocus(); return canceled; },
        beginApply: () => recipeOwner.applying(attempt),
        finishApply: (applied) => { if (applied) { recipeOwner.applied(attempt); recipeEditToken.current = undefined; restoreRecipeFocus(); }
          else recipeOwner.fail(attempt, 'The construction change was not saved.'); },
      };
    } },
  });
  const blockingDiagnostics = state.diagnostics.some(
    (diagnostic) => diagnostic.severity === 'error',
  );
  const { publishing, publishDisabled, publish } = usePublish({
    ownerKey,
    projectId,
    authResourcePath,
    state,
    latestState,
    workspaceAttached,
    compileGeneration,
    dispatch,
    reconcileCurrent,
    refetchBuilder,
    syncBuilderData,
    setMessage,
    incomplete,
    blockingDiagnostics,
    recipeOwner,
    constructionLifecycle,
  });
  const combineRootNodeId = table?.document.rootResourceType
    ? derivedOccurrences(table, state.catalog)[0]?.nodeId
    : undefined;
  const canStartCombine = Boolean(
    combineRootNodeId &&
    state.catalog.snapshotToken &&
    state.draftVersion > 0 &&
    state.draftDigest
  );
  const constructionProposalBusy = constructionLifecycle.proposal.status === 'previewing' ||
    constructionLifecycle.proposal.status === 'applying';
  const tablePreview =
    state.preview?.outputId === table?.outputId ? state.preview : undefined;
  const previewIsCurrent = Boolean(
    table &&
      tablePreview &&
      tablePreview.rows !== null &&
      state.receipt &&
      state.reconciliation === 'resolved' &&
      tablePreview.receiptId === state.receipt.receiptId,
  );
  const currentPreviewStatus = !tablePreview
    ? 'empty'
    : previewIsCurrent
      ? 'ready'
      : 'stale';
  const selectedColumnIds =
    columnSelection.kind === 'selected' &&
    columnSelection.outputId === table?.outputId
      ? columnSelection.columnIds
      : [];
  const toggleSelectedColumn = (columnId: string) => {
    setColumnSelection((current) => {
      const currentIds = current.kind === 'selected' && current.outputId === table?.outputId
        ? current.columnIds
        : [];
      const nextIds = currentIds.includes(columnId)
        ? currentIds.filter((candidate) => candidate !== columnId)
        : [...currentIds, columnId];
      return nextIds.length > 0 && table
        ? { kind: 'selected', outputId: table.outputId, columnIds: nextIds }
        : { kind: 'empty' };
    });
  };
  const currentStage = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.stages.at(-1)
    : undefined;
  const selectableColumns: ReadonlyArray<ConstructionSelectableColumn> = currentStage
    ? currentStage.columns
        .map((column) => ({ id: column.id, label: column.label, type: column.type }))
    : (table?.document.columns ?? []).map((column) => ({
        id: column.column,
        label: column.label,
      type: column.logicalType,
    }));
  const sourceRowMeaning = !table?.document.rootResourceType
    ? 'Choose what one row represents to start this table.'
    : table.document.rows.kind === 'GROUPS'
      ? table.document.rows.groups.source.kind === 'FIELD'
        ? `One row per distinct ${rowScopeLabel(table.document.rows.groups.source.field.fieldPath)} value.`
        : 'One row per saved group.'
      : table.document.rows.kind === 'EXPANDED'
        ? `One row per value in ${rowScopeLabel(table.document.rows.expanded.scopePath)}.`
        : table.document.output.rowLabel?.trim().toLowerCase().startsWith('one row per')
          ? table.document.output.rowLabel!.trim()
          : `One row per ${table.document.rootResourceType} record.`;
  const focusedFeature = useMemo(() => {
    if (!featureFocus) return undefined;
    const targetTable = state.tables.find((candidate) => candidate.outputId === featureFocus.outputId);
    const column = targetTable?.document.columns.find((candidate) =>
      candidate.column === featureFocus.column
      || (featureFocus.label !== undefined && candidate.label === featureFocus.label));
    return targetTable && column ? { table: targetTable, column } : undefined;
  }, [featureFocus, state.tables]);
  const lastFocusAttempt = useRef<string | undefined>(undefined);
  if (focusedFeature && !reviewFocusTarget) {
    const attemptKey = [
      focusedFeature.table.outputId,
      focusedFeature.column.occurrenceId,
      state.selectedOutputId,
      state.selectedOccurrenceId,
    ].join('\u0000');
    if (lastFocusAttempt.current !== attemptKey) {
      lastFocusAttempt.current = attemptKey;
      if (state.selectedOutputId !== focusedFeature.table.outputId) {
        setActiveConstructionSelection(undefined);
        dispatch({ type: 'selectTable', outputId: focusedFeature.table.outputId });
      } else if (state.selectedOccurrenceId !== focusedFeature.column.occurrenceId) {
        dispatch({ type: 'selectOccurrence', occurrenceId: focusedFeature.column.occurrenceId });
      }
    }
  }
  const focusDatasetReviewTarget = useCallback((target: DatasetReviewTarget) => {
    if (target.kind === 'new-table') {
      setReviewOpen(false);
      setReviewFocusColumn(undefined);
      setReviewFocusTarget(undefined);
      window.setTimeout(() => document.getElementById('first-table-name')?.focus(), 0);
      return;
    }
    selectTableFromUser(target.outputId);
    setReviewOpen(false);
    setReviewFocusTarget(target);
    if (target.kind === 'column') {
      setFeatureMode('catalog');
      setReviewFocusColumn(target);
      dispatch({ type: 'selectOccurrence', occurrenceId: target.occurrenceId });
      return;
    }
    setReviewFocusColumn(undefined);
    if (target.kind === 'table' || (target.kind === 'row' && target.control === 'row-type')) {
      setFeatureMode('catalog');
    }
  }, [dispatch, selectTableFromUser]);
  useBrowserSubscription((_onStoreChange) => {
    if (!reviewFocusTarget || state.selectedOutputId !== reviewFocusTarget.outputId) return () => {};
    const focusTarget = reviewFocusTarget;
    const timer = window.setTimeout(() => {
      if (focusTarget.kind === 'column') {
        document
          .querySelector<HTMLElement>('[data-feature-focus="true"]')
          ?.scrollIntoView({ block: 'center' });
        return;
      }
      if (focusTarget.kind === 'row') {
        const selector = focusTarget.control === 'row-type'
          ? '[aria-label="Search row types"]'
          : '[aria-label="One row per"]';
        const control = document.querySelector<HTMLElement>(selector);
        control?.closest('section')?.scrollIntoView({ block: 'center' });
        control?.focus();
        return;
      }
      const targetTable = state.tables.find((candidate) => candidate.outputId === focusTarget.outputId);
      const selector = targetTable?.document.rootResourceType
        ? '[aria-label="Search columns"]'
        : '[aria-label="Search row types"]';
      const control = document.querySelector<HTMLElement>(selector);
      control?.closest('section')?.scrollIntoView({ block: 'center' });
      control?.focus();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [reviewFocusTarget, state.selectedOutputId, state.tables], Boolean(reviewFocusTarget && state.selectedOutputId === reviewFocusTarget.outputId));
  const handedOffPopulationSelectionID = populationSelection?.id;
  const cohortRevisionID = table && table.document.rows.kind === 'GROUPS' &&
    table.document.rows.groups.source.kind === 'EXPLICIT'
    ? table.document.rows.groups.source.explicit.revisionId
    : undefined;
  const attachedSelectionID = table?.document.population?.selectionRevisionId;
  const populationSelectionContextKey = JSON.stringify([
    projectId,
    state.explorerId,
    authResourcePath ?? '',
    state.catalog.snapshotToken,
    state.catalog.generation,
    table?.outputId ?? '',
    table?.document.rootResourceType ?? '',
    cohortRevisionID ?? '',
    handedOffPopulationSelectionID ?? '',
  ]);
  const detachedSelectionID = !attachedSelectionID && !cohortRevisionID && !handedOffPopulationSelectionID &&
    clearedPopulationSelection?.contextKey === populationSelectionContextKey &&
    clearedPopulationSelection.client === loomClient
    ? clearedPopulationSelection.selectionID
    : undefined;
  const resolvedSelectionID = attachedSelectionID ?? detachedSelectionID;
  const populationSelectionQueryArgs = !populationSelectionLoading && !handedOffPopulationSelectionID && table &&
    (resolvedSelectionID || cohortRevisionID)
    ? {
      project: projectId,
      explorerId: state.explorerId,
      authResourcePath,
      snapshotToken: state.catalog.snapshotToken,
      generation: state.catalog.generation,
      outputId: table.outputId,
      resourceType: table.document.rootResourceType ?? '',
      ...(resolvedSelectionID ? { attachedSelectionId: resolvedSelectionID } : {}),
      ...(cohortRevisionID ? { cohortRevisionId: cohortRevisionID } : {}),
    }
    : undefined;
  const populationSelectionQuery = useResolvePopulationSelectionQuery(
    populationSelectionQueryArgs,
    JSON.stringify([populationSelectionContextKey, resolvedSelectionID ?? '']),
  );
  const populationSelectionOverrideIsCurrent = populationSelectionOverride?.contextKey === populationSelectionContextKey && (
    populationSelection?.id === populationSelectionOverride.baseSelectionID ||
    attachedSelectionID === populationSelectionOverride.baseSelectionID ||
    attachedSelectionID === populationSelectionOverride.selection.id ||
    populationSelectionQuery.data?.id === populationSelectionOverride.baseSelectionID
  );
  const activePopulationSelection = populationSelectionOverrideIsCurrent
    ? populationSelectionOverride.selection
    : populationSelection ?? populationSelectionQuery.data;
  const populationExclusionContext = {
    contextKey: populationSelectionContextKey,
    client: loomClient,
    baseSelectionID: activePopulationSelection?.id,
    bindingKey: populationBindingKey(table),
  };
  const populationExclusionContextRef = useRef(populationExclusionContext);
  populationExclusionContextRef.current = populationExclusionContext;
  const activePopulationSelectionLoading = populationSelectionLoading || (
    !populationSelection && populationSelectionQuery.isLoading
  );
  const activePopulationSelectionError = populationVariantError?.contextKey === populationSelectionContextKey
    ? populationVariantError.message
    : populationSelectionQuery.error instanceof Error
      ? populationSelectionQuery.error.message
      : populationSelectionQuery.error
        ? 'Loom could not load the saved collection.'
        : populationSelectionError;
  const occurrences = useMemo(
    () => derivedOccurrences(table, state.catalog),
    [state.catalog, table],
  );
  const occurrence = occurrences.find(
    (candidate) => candidate.id === state.selectedOccurrenceId,
  );
  const startingOccurrence = occurrences.find((candidate) => candidate.depth === 0);
  const selectedRouteContext = useMemo(() => {
    if (!occurrence) return undefined;
    return {
      occurrenceId: occurrence.id,
      nodeId: occurrence.nodeId,
    };
  }, [occurrence]);
  const fieldPresentationContext: Omit<FieldPresentationRequest, 'candidateIds'> | undefined =
    table && startingOccurrence && state.draftVersion > 0 && state.draftDigest
      ? {
          expectedDraftVersion: state.draftVersion,
          expectedDraftDigest: state.draftDigest,
          sourceNodeId: startingOccurrence.nodeId,
          occurrenceId: startingOccurrence.id,
          ...(constructionLifecycle.capabilities.status === 'ready'
            ? { stageId: constructionLifecycle.capabilities.response.selectedStage.id }
            : {}),
        }
      : undefined;
  const sourceStageDescriptors = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.stages
    : undefined;
  const {
    selectedAddSource,
    concreteAddSources,
    setAddColumnsSource,
    addSourceContext,
    addSourceOptionLabel,
    addSourceRouteContext,
    addColumnsViewRevision,
    selectAddColumnsView,
    inspectRelatedRouteCoverage,
    applyConstructionProposal,
    previewRelatedSourceColumns,
    addSelectedFeatures,
    addColumnsReason,
  } = useAddColumns({
    state,
    table,
    selectedRouteContext,
    constructionLifecycle,
    hasUnsupportedSavedSourceColumns,
    editingConstructionStep,
    activeConstructionFamily,
    ownerKey,
    projectId,
    authResourcePath,
    previewLimit,
    latestState,
    loomClient,
    sourceStageDescriptors,
    rowValuePolicy,
    cancelingConstructionProposalRef,
    appliedChoicePreview,
    automaticPreviewRequest,
    applyCommandsWithResult,
    combineCatalog,
    setActiveConstructionFamily,
    setConstructionHistorySelection,
    setEditingConstructionStepId,
    setRowIntentSession,
    opaqueId,
  });
  const addColumnsSourceControls = concreteAddSources.length > 1 ? (
    <div role="group" aria-label="Add columns source" className="rounded border border-slate-200 bg-white px-3 py-2">
      <label className="flex flex-wrap items-center gap-2 text-sm text-slate-700">
        Fields from
        <select
          aria-label="Add columns source"
          value={selectedAddSource?.key ?? ''}
          disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
          onChange={(event) => {
            const source = concreteAddSources.find((candidate) => candidate.key === event.currentTarget.value);
            if (source) setAddColumnsSource({ context: addSourceContext, key: source.key });
          }}
          className="rounded border border-slate-300 bg-white px-2 py-1.5"
        >
          {concreteAddSources.map((source) => <option key={source.key} value={source.key}>{addSourceOptionLabel(source)}</option>)}
        </select>
      </label>
    </div>
  ) : undefined;
  const excludePopulationMember = useCallback(async (
    ref: ResourceRef,
    route: ReadonlyArray<ConstructionRouteStep>,
  ) => {
    const current = latestState.current;
    const currentTable = selectedTable(current);
    const baseSelection = activePopulationSelection;
    if (!currentTable || !baseSelection) return;
    const exclusionContext = populationExclusionContextRef.current;
    const requestIsCurrent = () => {
      const latestContext = populationExclusionContextRef.current;
      const latest = latestState.current;
      const latestTable = selectedTable(latest);
      return workspaceAttached.current &&
        latestContext.contextKey === exclusionContext.contextKey &&
        latestContext.client === exclusionContext.client &&
        latestContext.baseSelectionID === baseSelection.id &&
        latestContext.bindingKey === exclusionContext.bindingKey &&
        latestTable?.outputId === currentTable.outputId &&
        latestTable.document.rootResourceType === currentTable.document.rootResourceType &&
        populationBindingKey(latestTable) === exclusionContext.bindingKey &&
        latest.draftVersion === serverDraft.current.version &&
        latest.draftDigest === serverDraft.current.digest;
    };
    if (!requestIsCurrent()) return;
    setPopulationVariantPending(true);
    setPopulationVariantError(undefined);
    try {
      const idempotencyKey = `selection-variant-${window.crypto.randomUUID()}`;
      const variant = await loomClient.createSelection({
        project: projectId,
        explorerId: current.explorerId,
        authResourcePath,
        snapshotToken: current.catalog.snapshotToken,
        idempotencyKey,
        source: { kind: 'selectionRevision', selectionRevision: { selectionRevisionId: baseSelection.id } },
        exclusions: [ref],
        requestId: idempotencyKey,
      });
      if (!requestIsCurrent()) return;
      let cursor: string | undefined;
      let routeChoiceId: string | undefined;
      do {
        const routes = await loomClient.searchPopulationRoutes({
          project: projectId,
          explorerId: current.explorerId,
          authResourcePath,
          snapshotToken: current.catalog.snapshotToken,
          outputId: currentTable.outputId,
          selectionRevisionId: variant.id,
          limit: 50,
          ...(cursor ? { cursor } : {}),
          requestId: `population-routes-${window.crypto.randomUUID()}`,
        });
        if (!requestIsCurrent()) return;
        routeChoiceId = routes.choices.find((choice) =>
          sameConstructionRoute(choice.route, route),
        )?.routeChoiceId;
        cursor = routes.nextCursor;
      } while (!routeChoiceId && cursor);
      if (!routeChoiceId) {
        throw new Error('Loom could not preserve this table connection for the revised collection.');
      }
      if (!requestIsCurrent()) return;
      const attached = await applyCommands([{
        type: 'SET_TABLE_POPULATION',
        outputId: currentTable.outputId,
        selectionRevisionId: variant.id,
        routeChoiceId,
      }], undefined, requestIsCurrent);
      if (!attached) {
        if (!requestIsCurrent()) return;
        throw new Error('Loom created the revised collection but could not attach it to this table.');
      }
      const resultContext = populationExclusionContextRef.current;
      const resultTable = selectedTable(latestState.current);
      if (!workspaceAttached.current ||
          resultContext.contextKey !== exclusionContext.contextKey ||
          resultContext.client !== exclusionContext.client ||
          (resultContext.baseSelectionID !== undefined && resultContext.baseSelectionID !== baseSelection.id && resultContext.baseSelectionID !== variant.id) ||
          resultTable?.outputId !== currentTable.outputId ||
          resultTable.document.rootResourceType !== currentTable.document.rootResourceType ||
          populationBindingKey(resultTable) !== populationBindingKey(currentTable, variant.id)) return;
      setPopulationSelectionOverride({
        contextKey: populationSelectionContextKey,
        baseSelectionID: baseSelection.id,
        selection: variant,
      });
    } catch (error) {
      if (!requestIsCurrent()) return;
      setPopulationVariantError({
        contextKey: populationSelectionContextKey,
        message: error instanceof Error ? error.message : 'Loom could not create the revised collection.',
      });
    } finally {
      setPopulationVariantPending(false);
    }
  }, [activePopulationSelection, applyCommands, authResourcePath, loomClient, populationSelectionContextKey, projectId]);

  const ensureSuggestions = useCallback(() => {
    const current = latestState.current;
    const currentOccurrence = derivedOccurrences(
      selectedTable(current),
      current.catalog,
    ).find((candidate) => candidate.id === current.selectedOccurrenceId);
    if (!currentOccurrence || !current.catalog.snapshotToken) return;
    const key = `${current.explorerId}:${current.catalog.snapshotToken}:${currentOccurrence.id}`;
    if (suggestionRequestKey.current === key) return;
    if (
      (current.catalog.candidates ?? []).some(
        (candidate) => candidate.nodeId === currentOccurrence.nodeId,
      )
    )
      return;
    suggestionRequestKey.current = key;
    setSuggestionsFailure(undefined);
    const request = getSuggestions({
      project: projectId,
      explorerId: current.explorerId,
      authResourcePath,
      snapshotToken: current.catalog.snapshotToken,
      nodeId: currentOccurrence.nodeId,
      requestId: `suggestions-${currentOccurrence.id}`,
    });
    void request
      .unwrap()
      .then((value) => {
        const latest = latestState.current;
        if (
          latest.explorerId === current.explorerId &&
          value.snapshotToken === latest.catalog.snapshotToken &&
          latest.selectedOccurrenceId === currentOccurrence.id
        ) {
          dispatch({ type: 'candidatesLoaded', candidates: value.candidates });
          setSuggestionsFailure(undefined);
        } else if (suggestionRequestKey.current === key) {
          suggestionRequestKey.current = '';
        }
      })
      .catch((error: ExplorerAuthoringApiError) => {
        if (suggestionRequestKey.current === key) suggestionRequestKey.current = '';
        if (error.code !== 'CLIENT_CANCELLED') {
          const suffix = error.code ? ` (${error.code})` : '';
          const latest = latestState.current;
          if (latest.explorerId === current.explorerId &&
            latest.catalog.snapshotToken === current.catalog.snapshotToken &&
            latest.selectedOccurrenceId === currentOccurrence.id) {
            setSuggestionsFailure({
              key,
              message: `Available columns could not be loaded: ${error.message}${suffix}`,
            });
          }
        }
      });
  }, [authResourcePath, dispatch, getSuggestions, projectId]);
  const suggestionHostRef = useCallback(
    (host: HTMLSpanElement | null) => {
      if (host) ensureSuggestions();
    },
    [ensureSuggestions],
  );
  const suggestionIdentity = `${state.explorerId}:${state.catalog.snapshotToken}:${state.selectedOccurrenceId}`;
  const unsupportedSourceProjectionDiagnostic = state.diagnostics.find(
    isUnsupportedUnknownSourceProjectionDiagnostic,
  );
  const primaryDiagnostic = state.diagnostics[0];
  const outputAvailability = table
    ? effectiveOutputAvailability(table.document)
    : { hasAnyOutputColumn: false, hasVisibleOutputColumn: false };
  const previewDisabled =
    !table?.document.rootResourceType ||
    !outputAvailability.hasVisibleOutputColumn ||
    blockingDiagnostics ||
    constructionLifecycle.proposal.status !== 'idle';
  const addTable = () => {
    if (!recipeMutationAllowed()) return;
    if (
      firstTableActionPending.current ||
      latestState.current.tables.length === 0
    ) return;
    if (!tableCreatorOpen) setFirstTableName('');
    setTableCreatorOpen(true);
  };
  const duplicateTable = () => {
    if (!recipeMutationAllowed()) return;
    if (!table) return;
    if (!table.document.rootResourceType) {
      const outputId = opaqueId('output');
      const title = `${table.title} copy`;
      dispatch({
        type: 'addTable',
        table: {
          ...table,
          outputId,
          tabId: opaqueId('tab'),
          title,
          document: {
            ...table.document,
            output: { ...table.document.output, id: outputId, title },
          },
        },
      });
      rememberSelectedTable(outputId);
      return;
    }
    void applyCommands([
      {
        type: 'DUPLICATE_TABLE',
        sourceOutputId: table.outputId,
        title: `${table.title} copy`,
      },
    ]);
  };
  const renameTable = (outputId: string, value: string) => {
    const title = value.trim();
    if (!title) return;
    const target = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!target?.document.rootResourceType) {
      dispatch({ type: 'renameTable', outputId, title });
      return;
    }
    void applyCommands([{ type: 'RENAME_TABLE', outputId, title }]);
  };
  const renameTableById = (outputId: string) => {
    if (!recipeMutationAllowed()) return;
    const target = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!target) return;
    const title = window.prompt('Table name', target.title)?.trim();
    if (title && title !== target.title) renameTable(outputId, title);
  };
  const deleteSelectedTable = () => {
    if (!recipeMutationAllowed()) return;
    if (!table) return;
    if (!window.confirm(`Delete ${table.title}?`)) return;
    if (table.document.rootResourceType) {
      void applyCommands([
        { type: 'DELETE_TABLE', outputId: table.outputId },
      ]);
      return;
    }
    const fallbackOutputId = state.tables.find(
      (candidate) => candidate.outputId !== table.outputId,
    )?.outputId;
    dispatch({ type: 'removeTable', outputId: table.outputId });
    if (fallbackOutputId) rememberSelectedTable(fallbackOutputId);
  };
  const createCustomExplorer = async (title: string, fromCurrent: boolean) => {
    try {
      const created = await createExplorer({
        project: projectId,
        authResourcePath,
        name: title,
        title,
        ...(fromCurrent ? { sourceExplorerId: state.explorerId } : {}),
      }).unwrap();
      selectExplorer(created.explorerId);
      if (!onExplorerChange) void explorers.refetch();
      setMessage(undefined);
    } catch (error) {
      const apiError = error as ExplorerAuthoringApiError;
      const suffix = apiError.code ? ` (${apiError.code})` : '';
      setMessage(`Explorer creation failed: ${apiError.message}${suffix}`);
    }
  };
  const deleteCurrentExplorer = async () => {
    if (!capabilities.data?.features.deleteExplorer) return;
    const current = (explorers.data ?? []).find(
      (explorer) => explorer.explorerId === effectiveExplorerId,
    );
    const fallback =
      (explorers.data ?? []).find(
        (explorer) =>
          explorer.explorerId !== effectiveExplorerId &&
          explorer.explorerId === 'dataframe-default',
      ) ??
      (explorers.data ?? []).find(
        (explorer) => explorer.explorerId !== effectiveExplorerId,
      );
    if (!current || !fallback) return;
    if (
      !window.confirm(
        `Delete Explorer "${current.title}"? This permanently removes its configuration and cannot be undone.`,
      )
    )
      return;
    try {
      await deleteExplorer({
        project: projectId,
        explorerId: effectiveExplorerId,
        authResourcePath,
        requestId: `delete-explorer-${effectiveExplorerId}`,
      }).unwrap();
      compileGeneration.current += 1;
      activeCompile.current?.abort();
      selectExplorer(fallback.explorerId);
      if (!onExplorerChange) await explorers.refetch();
      setMessage(undefined);
    } catch (error) {
      const apiError = error as ExplorerAuthoringApiError;
      const suffix = apiError.code ? ` (${apiError.code})` : '';
      setMessage(`Explorer deletion failed: ${apiError.message}${suffix}`);
    }
  };
  const createFirstTableFromRoot = async (nodeId: string) => {
    if (firstTableActionPending.current) return;
    const current = latestState.current;
    const root = current.catalog.nodes.find((node) => node.nodeId === nodeId);
    if (!root || !current.catalog.snapshotToken) return;

    let tableCreated = false;
    firstTableActionPending.current = true;
    setFirstTableProgress({
      kind: 'running',
      resourceType: root.resourceType,
      phase: 'checking',
    });
    try {
      let evidence: InitialTableCandidateEvidence = { kind: 'unavailable' };
      try {
        const value = await getSuggestions({
          project: projectId,
          explorerId: current.explorerId,
          authResourcePath,
          snapshotToken: current.catalog.snapshotToken,
          nodeId: root.nodeId,
          requestId: `first-table-suggestions-${window.crypto.randomUUID()}`,
        }).unwrap();
        if (
          value.snapshotToken === current.catalog.snapshotToken &&
          value.nodeId === root.nodeId
        ) {
          evidence = { kind: 'available', candidates: value.candidates };
        }
      } catch {
        // A root table is still useful when Loom cannot prove an identity field.
      }

      const plan: InitialTablePlan = planInitialTable(
        root,
        firstTableName,
        evidence,
      );
      setFirstTableProgress({
        kind: 'running',
        resourceType: root.resourceType,
        phase: 'creating',
      });
      const created = await applyCommandsWithResult([{
        type: 'CREATE_TABLE',
        title: plan.title,
        rootNodeId: plan.root.nodeId,
      }]);
      const createdTable = created?.results.find(
        (result) =>
          result.type === 'TABLE_CREATED' &&
          Boolean(result.outputId) &&
          Boolean(result.occurrenceId),
      );
      if (!createdTable?.outputId || !createdTable.occurrenceId) {
        setMessage(`Loom could not create a ${root.resourceType} table.`);
        return;
      }
      tableCreated = true;
      setFirstTableName('');

      if (plan.kind === 'root-only') {
        const reason = plan.reason === 'CATALOG_UNAVAILABLE'
          ? 'Loom could not verify a safe identity column from the current catalog.'
          : plan.reason === 'AMBIGUOUS_DIRECT_ID'
            ? 'Loom found more than one executable direct ID field.'
            : 'Loom did not find a direct scalar string ID field.';
        setMessage(
          `${plan.title} was created with ${root.resourceType} rows. ${reason} Add a column to preview the table.`,
        );
        return;
      }

      setFirstTableProgress({
        kind: 'running',
        resourceType: root.resourceType,
        phase: 'adding-id',
      });
      const added = await applyCommandsWithResult([{
        type: 'ADD_COLUMN',
        outputId: createdTable.outputId,
        occurrenceId: createdTable.occurrenceId,
        candidateId: plan.candidate.candidateId,
        projectionMode: 'VALUE',
        initialPresentation: 'TABLE',
        title: `${root.resourceType} ID`,
      }]);
      if (!added) {
        setMessage(
          `${plan.title} was created, but Loom could not add the catalog-verified ID column. Review the Builder diagnostics and try again.`,
        );
        return;
      }

    } finally {
      if (tableCreated) setTableCreatorOpen(false);
      firstTableActionPending.current = false;
      setFirstTableProgress({ kind: 'idle' });
    }
  };

  const automaticPreviewQuery = useAutomaticPreview(queryOptionsFor({
    previewDisabled,
    pendingCommands,
    firstTableProgress,
    reconcileCurrent,
    executePreview,
  }));
  const automaticPreviewFailure = automaticPreviewQuery.error as Partial<ExplorerAuthoringApiError> | undefined;
  const automaticPreviewFailureMessage = automaticPreviewQuery.error === undefined
    ? undefined
    : automaticPreviewFailure?.message
      ? `${automaticPreviewFailure.message}${automaticPreviewFailure.code ? ` (${automaticPreviewFailure.code})` : ''}`
      : 'The automatic preview request failed.';
  const busy =
    pendingCommands > 0 ||
    reconcileStatus.isLoading ||
    previewStatus.isLoading ||
    firstTableProgress.kind === 'running' ||
    constructionProposalBusy ||
    publishing ||
    createStatus.isLoading ||
    deleteStatus.isLoading;
  const emptyExplorerProject = !effectiveExplorerId &&
    !explorers.isLoading &&
    !explorers.error &&
    false;

  if (
    explorers.isLoading ||
    (explorerQueryEnabled && builder.isLoading)
  ) {
    return (
      <main ref={workspaceHostRef} className="p-6" role="status">
        Loading the selected Explorer configuration…
      </main>
    );
  }
  if (emptyExplorerProject) {
    return (
      <main ref={workspaceHostRef} className="min-h-full p-6">
        <section
          aria-labelledby="empty-explorer-project-title"
          className="mx-auto max-w-xl rounded-lg border border-slate-200 bg-white p-6 shadow-sm"
        >
          <h1
            id="empty-explorer-project-title"
            className="text-lg font-semibold text-slate-900"
          >
            Create your first Explorer
          </h1>
          <p className="mt-2 text-sm text-slate-600">
            This project does not have an Explorer yet. Create one to start
            building its first table.
          </p>
          {message ? (
            <p role="alert" className="mt-3 text-sm text-red-700">
              {message}
            </p>
          ) : null}
          <div className="mt-4">
            <NewExplorerControl
              mode="blank-only"
              busy={createStatus.isLoading}
              onCreateExplorer={(title, fromCurrent) =>
                void createCustomExplorer(title, fromCurrent)
              }
            />
          </div>
        </section>
      </main>
    );
  }
  if (shouldShowBuilderLoadError(Boolean(explorers.error), Boolean(builder.data))) {
    return (
      <main ref={workspaceHostRef} className="p-6" role="alert">
        <p>Couldn’t load this dataset.</p>
        <button
          type="button"
          onClick={() => {
            void explorers.refetch();
            if (explorerQueryEnabled) void refetchBuilder();
          }}
        >
          Try again
        </button>
      </main>
    );
  }

  const toolbar = (
    <BuilderToolbar
      explorers={explorerSummaries}
      selectedExplorerId={effectiveExplorerId}
      onExplorerChange={selectExplorer}
      onCreateExplorer={(title, fromCurrent) =>
        void createCustomExplorer(title, fromCurrent)
      }
      deleteSupported={capabilities.data?.features.deleteExplorer ?? false}
      deleteDisabled={
        !(explorers.data ?? []).some(
          (explorer) => explorer.explorerId !== effectiveExplorerId,
        )
      }
      onDeleteExplorer={() => void deleteCurrentExplorer()}
      tables={state.tables}
      selectedOutputId={state.selectedOutputId}
      onSelectTable={selectTableFromUser}
      onRenameTable={renameTable}
      onNewTable={addTable}
      onDuplicateTable={duplicateTable}
      onDeleteTable={deleteSelectedTable}
      onReview={() => {
        const open = !reviewOpen;
        setReviewOpen(open);
        const current = latestState.current;
        if (open && (!receiptContainsOutputIds(current.receipt, current.tables.map((table) => table.outputId)) ||
          current.reconciliation !== 'resolved')) {
          void reconcileCurrent();
        }
      }}
      reviewExpanded={reviewOpen}
      onPublish={() => void publish()}
      publishDisabled={publishDisabled}
      publishing={publishing}
      busy={busy}
      columnCreationSupported
      tableToolbarHost={featureMode === 'graph' ? tableToolbarHost : null}
    />
  );

  const reshapeEditorKeyFor = (entry: number, kind: ReshapeEntryKind, stageId: string) =>
    `${ownerKey}:${state.catalog.snapshotToken}:${state.draftVersion}:${state.draftDigest}:${table?.outputId ?? ''}:${stageId}:${entry}:${kind}`;
  const startCombine = async () => {
    const current = latestState.current;
    const currentTable = selectedTable(current);
    const requestOwner = { ownerKey, explorerId: current.explorerId };
    const rootNodeId = currentTable?.document.rootResourceType
      ? derivedOccurrences(currentTable, current.catalog)[0]?.nodeId
      : undefined;
    if (!currentTable || !rootNodeId || !current.catalog.snapshotToken) {
      setMessage('Choose a rooted table before starting a new table from published versions.');
      return;
    }

    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('choose');
    setReshapeFormKind('choose');

    const existingTitles = new Set(current.tables.map((candidate) => candidate.title.trim().toLocaleLowerCase()));
    const baseTitle = currentTable.title.trim() ? `${currentTable.title.trim()} combined` : 'Combined table';
    let title = baseTitle;
    let suffix = 2;
    while (existingTitles.has(title.toLocaleLowerCase())) {
      title = `${baseTitle} ${suffix++}`;
    }

    const created = await applyCommandsWithResult([{
      type: 'CREATE_TABLE',
      title,
      rootNodeId,
    }]);
    if (!combineCatalog.isOwnerCurrent(requestOwner)) return;
    const createdTable = created?.results.find(
      (result) => result.type === 'TABLE_CREATED' && Boolean(result.outputId),
    );
    if (!createdTable?.outputId) {
      return;
    }
    setFeatureMode('catalog');
    setActiveConstructionFamily('COMBINE');
    void combineCatalog.loadPage();
  };

  const selectConstructionFamily = (family: ConstructionOperationFamily) => {
    if (!recipeMutationAllowed()) return;
    rememberOperationFocus();
    const willOpen = activeConstructionFamily !== family;
    setRowIntentSession(false);
    if (family === 'ADD_COLUMNS' && willOpen) {
      const admission = admitAuthoringRequest(state.tables, { kind: 'add-columns-proposal', outputId: table?.outputId ?? '' });
      if (admission.kind === 'refused') {
        setMessage(admission.refusal.message);
        return;
      }
      selectAddColumnsView('fields');
    }
    if (family === 'COMBINE' && willOpen) {
      if (!canStartCombine) return;
      void startCombine();
      return;
    }
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('choose');
    setReshapeFormKind('choose');
    setActiveConstructionFamily((current) => current === family ? undefined : family);
  };
  const chooseRelatedRows = () => {
    if (!recipeMutationAllowed()) return;
    rememberOperationFocus();
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setRowIntentSession(true);
    setReshapeEntryKind('related-expand');
    setReshapeFormKind('related-expand');
    setReshapeEntry((current) => current + 1);
    setActiveConstructionFamily('RESHAPE');
  };
  const chooseReshapeRows = (
    kind: 'group' | 'source-group' | 'coded-group' | 'expand' | 'expand-signed' | 'categories' | 'pivot' | 'coded-pivot' | 'unpivot',
    preferredColumnIds?: ReadonlyArray<string>,
  ) => {
    if (!recipeMutationAllowed()) return;
    if (kind === 'expand' && preferredColumnIds?.length !== 1) return;
    rememberOperationFocus();
    if (kind !== 'expand') setRowIntentSession(true);

    let entry: ConstructionReshapeEditorEntry | undefined;
    let entryStageId: string | undefined;
    if ((kind === 'expand' || kind === 'expand-signed' || kind === 'group') && table && constructionLifecycle.capabilities.status === 'ready') {
      const response = constructionLifecycle.capabilities.response;
      const stage = response.selectedStage;
      const appendStageId = constructionAppendStageFor(response.baseConstruction, table.document.rows);
      const supported = stage.id === appendStageId && stage.capabilities.some((candidate) =>
        candidate.kind === (kind === 'expand' ? 'EXPAND' : 'GROUP') && candidate.supported,
      );
      if (kind === 'expand') {
        const requestedColumnId = preferredColumnIds?.[0];
        if (!supported || !publicScalarListColumnIds(stage).includes(requestedColumnId ?? '')) return;
        entry = createReshapeEditorEntry({
          construction: construction ?? response.baseConstruction,
          capabilities: response,
          kind,
          selectedColumns: preferredColumnIds,
        });
        if (!entry) return;
        entryStageId = stage.id;
      } else if (kind === 'expand-signed') {
        // No column-keyed entry: the signed picker is capability-driven and
        // opens like the unpivot lane (empty form + capabilities choices),
        // staying apply-disabled until a field resolves.
        const signedSupported = stage.id === appendStageId
          && (response.expandSourceInput?.supported ?? false)
          && (response.expandSourceInput?.choices ?? []).some((choice) => choice.isPopulated);
        if (!signedSupported) return;
      } else if (supported) {
        entry = createReshapeEditorEntry({
          construction: construction ?? response.baseConstruction,
          capabilities: response,
          kind,
          selectedColumns: selectedColumnIds,
        });
        entryStageId = stage.id;
      }
    } else if (kind === 'expand' || kind === 'expand-signed') {
      return;
    }

    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    const editorKind = kind === 'expand-signed' ? 'expand' : kind;
    setReshapeEntryKind(editorKind);
    setReshapeFormKind(kind === 'categories' ? 'choose' : kind === 'expand-signed' ? 'expand' : kind);
    const nextEntry = reshapeEntry + 1;
    if (entry && entryStageId) {
      if (entry.candidateIntent) constructionLifecycle.onCandidateChange(entry.candidateIntent);
      else constructionLifecycle.cancel();
      setReshapeInitialEntry({
        editorKey: reshapeEditorKeyFor(nextEntry, editorKind, entryStageId),
        entry,
      });
    } else {
      setReshapeInitialEntry(undefined);
    }
    setReshapeEntry(nextEntry);
    setActiveConstructionFamily('RESHAPE');
  };
  const selectConstructionHistory = (selection: ConstructionHistorySelection) => {
    if (recipeOwner.state.status !== 'saved') {
      setRecipeNotice('Apply or Cancel the active recipe change before editing another step.'); return;
    }
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setRowIntentSession(false);
    setConstructionHistorySelection(selection);
    setEditingConstructionStepId(undefined);
    setActiveConstructionFamily(undefined);
  };
  const editConstructionStep = (stepId: string) => {
    if (!recipeMutationAllowed()) return;
    const step = construction?.steps.find((candidate) => candidate.id === stepId);
    if (!step) return;
    rememberOperationFocus();
    if (step.operation.kind === 'RELATED_SOURCE' || step.operation.kind === 'RELATED_FIELD') {
      constructionLifecycle.cancel();
      setConstructionHistorySelection({ kind: 'step', stepId });
      setEditingConstructionStepId(stepId);
      setActiveConstructionFamily(undefined);
      return;
    }
    const family = editableConstructionFamily(step.operation);
    if (!family) return;
    setRowIntentSession(false);
    constructionLifecycle.cancel();
    if (family === 'COMBINE') {
      void combineCatalog.loadPage();
    } else {
      combineCatalog.cancel();
    }
    setConstructionHistorySelection({ kind: 'step', stepId });
    setEditingConstructionStepId(stepId);
    setActiveConstructionFamily(family);
    if (family === 'RESHAPE') setReshapeFormKind('choose');
    setReshapeInitialEntry(undefined);
  };
  const closeConstructionWorkspace = () => {
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setEditingConstructionStepId(undefined);
    setActiveConstructionFamily(undefined);
    setRowIntentSession(false);
    setReshapeEntryKind('choose');
    setReshapeFormKind('choose');
    setReshapeInitialEntry(undefined);
  };
  const cancelConstructionEditor = closeConstructionWorkspace;
  const switchRowIntent = (_intent: 'records' | 'reshape') => {
    if (!rowIntentSession) return;
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('choose');
    setReshapeFormKind('choose');
    setReshapeInitialEntry(undefined);
    setActiveConstructionFamily(undefined);
  };
  const closeRowIntentSession = () => {
    setSourceRowsOpen(false);
    if (!rowIntentSession) return;
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('choose');
    setReshapeFormKind('choose');
    setReshapeInitialEntry(undefined);
    setActiveConstructionFamily(undefined);
    setRowIntentSession(false);
  };
  const removeConstructionStep = (stepId: string) => {
    if (!recipeMutationAllowed()) return;
    if (!construction?.steps.some((step) => step.id === stepId)) return;
    combineCatalog.cancel();
    constructionLifecycle.onCandidateChange({
      candidateConstruction: {
        ...construction,
        steps: construction.steps.filter((step) => step.id !== stepId),
      },
      removeStepIds: [stepId],
    });
    setConstructionHistorySelection({ kind: 'step', stepId });
    // Keep the saved-step input stage stable while its removal proposal is
    // active. Closing the editor here changes the capabilities request key
    // before the proposal can render; cancel/apply clears this state later.
    if (editingConstructionStepId !== stepId) setEditingConstructionStepId(undefined);
    setActiveConstructionFamily(undefined);
  };
  const restorePreviousDraft = async () => {
    if (!recipeMutationAllowed()) return;
    if (!previousDraftRevisionId) return;
    constructionLifecycle.cancel();
    combineCatalog.cancel();
    setActiveConstructionFamily(undefined);
    setEditingConstructionStepId(undefined);
    setConstructionHistorySelection({ kind: 'source' });
    setColumnSelection({ kind: 'empty' });
    await applyCommands([{
      type: 'RESTORE_DRAFT_REVISION',
      draftRevisionId: previousDraftRevisionId,
    }]);
  };

  const activeOperation = constructionOperationFamilies.find(
    (candidate) => candidate.family === activeConstructionFamily,
  );
  const activeReshapeEditorKey = table && constructionLifecycle.capabilities.status === 'ready'
    ? `${reshapeEditorKeyFor(
        reshapeEntry,
        reshapeEntryKind,
        constructionLifecycle.capabilities.response.selectedStage.id,
      )}:${!editingConstructionStep && (reshapeFormKind === 'choose' || reshapeFormKind === 'pivot' || reshapeFormKind === 'unpivot') ? selectedColumnIds.join(',') : ''}`
    : undefined;
  const activeReshapeInitialEntry = reshapeInitialEntry && reshapeInitialEntry.editorKey === activeReshapeEditorKey
    ? reshapeInitialEntry.entry
    : undefined;
  // The signed EXPAND flow opens the expand editor with no column-keyed entry
  // (capability-driven, like the unpivot lane), so only a chooser-less
  // non-signed expand demotes; demoting the signed kind made the signed card
  // unopenable.
  const activeReshapeInitialKind = reshapeEntryKind === 'expand' && !activeReshapeInitialEntry && reshapeFormKind !== 'expand'
    ? 'choose'
    : reshapeEntryKind;
  const showReshapeColumnSelection = activeOperation?.family === 'RESHAPE' &&
    (reshapeFormKind === 'choose' || reshapeFormKind === 'pivot' || reshapeFormKind === 'unpivot' || reshapeFormKind === 'coded-pivot');
  const hideGenericReshapeSelection = activeOperation?.family === 'RESHAPE' && !showReshapeColumnSelection;
  const editingRelatedSourceStep: RelatedSourceStep | undefined = editingConstructionStep?.operation.kind === 'RELATED_SOURCE'
    ? editingConstructionStep as RelatedSourceStep
    : undefined;
  const editingRelatedFieldStep = editingConstructionStep && isRelatedFieldStep(editingConstructionStep)
    ? editingConstructionStep
    : undefined;
  const sourceProjectionSummary = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.sourceProjectionAvailability
    : undefined;
  const capabilityNodeOccurrences = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.nodeOccurrences
    : undefined;
  const existingSourceKeys = constructionLifecycle.capabilities.status === 'ready'
    ? new Set(
        constructionLifecycle.capabilities.response.selectedStage.columns
          .map((column) => column.sourceKey)
          .filter((key): key is string => Boolean(key)),
      )
    : undefined;
  const selectedCapabilityStageId = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.selectedStage.id
    : undefined;
  const rowValuesSupported = sourceStageDescriptors?.at(-1)?.capabilities.some(
    (capability) => capability.kind === 'ROW_VALUES' && capability.supported,
  ) ?? false;
  const groupedRowValuePolicy = rowValuesSupported
    ? { value: rowValuePolicy, onChange: setRowValuePolicy }
    : undefined;
  const sourceAvailability = hasUnsupportedSavedSourceColumns
    ? { available: false, reason: 'Remove unsupported saved source fields before adding more fields.' }
    : constructionLifecycle.capabilities.status === 'error'
    ? { available: false, reason: sanitizeReasonText(constructionLifecycle.capabilities.message) }
    : sourceProjectionAvailability(sourceStageDescriptors, sourceProjectionSummary, selectedCapabilityStageId);
  const capabilityStage = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.selectedStage
    : undefined;
  const constructionCapabilitiesResponse = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response
    : undefined;
  const relatedSourceCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'RELATED_SOURCE',
  );
  const relatedSourceReason = [
    relatedSourceCapability?.reasonCode,
    relatedSourceCapability?.reason,
  ]
    .map((part) => (part === undefined ? '' : sanitizeReasonText(part).trim()))
    .filter((part) => part !== '')
    .join(': ');
  const capabilityIsForAppendStage = constructionLifecycle.capabilities.status === 'ready' &&
    capabilityStage?.id === constructionAppendStageFor(
      constructionLifecycle.capabilities.response.baseConstruction,
      table?.document.rows,
    );
  const deriveCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'DERIVE',
  );
  const calculateUnavailableReason = !table?.document.rootResourceType
    ? 'Choose a row source before calculating.'
    : constructionLifecycle.capabilities.status === 'loading'
      ? 'Checking calculation support for this table stage…'
      : constructionLifecycle.capabilities.status === 'error'
        ? sanitizeReasonText(constructionLifecycle.capabilities.message)
        : constructionLifecycle.capabilities.status === 'idle'
          ? 'Calculation support is not available until the table capabilities load.'
          : !capabilityIsForAppendStage
            ? 'The table stage changed; reload calculation support.'
            : deriveCapability?.reason
              ? sanitizeReasonText(deriveCapability.reason)
              : 'The current table stage does not support calculated columns.';
  const capabilitiesErrorMessage = constructionLifecycle.capabilities.status === 'error'
    ? constructionLifecycle.capabilities.message
    : '';
  const reshapeAvailabilityFor = (kind: 'GROUP' | 'PIVOT' | 'CODED_PIVOT') => {
    const capability = capabilityStage?.capabilities.find((candidate) => candidate.kind === kind);
    return capabilityIsForAppendStage && capability?.supported
      ? { supported: true }
      : {
          supported: false,
          reason: unavailableOperationReason({
            stage: constructionLifecycle.capabilities.status === 'error'
              ? 'error'
              : constructionLifecycle.capabilities.status !== 'ready' || !capabilityIsForAppendStage
                ? 'checking'
                : 'ready',
            errorMessage: capabilitiesErrorMessage,
            checkingMessage: 'Checking this table’s available row operations…',
            label: 'This row operation is unavailable for the current table.',
            capability,
          }),
        };
  };
  const codedGroupingAvailable = capabilityIsForAppendStage &&
    capabilityStage?.capabilities.some((candidate) => candidate.kind === 'CODED_GROUP' && candidate.supported) &&
    Boolean(capabilityStage.codedGroupChoices?.length);
  const sourceGroupingAvailable = capabilityIsForAppendStage &&
    constructionLifecycle.capabilities.status === 'ready' &&
    constructionLifecycle.capabilities.response.sourceInput?.supported &&
    constructionLifecycle.capabilities.response.sourceInput.choices.some((choice) => choice.isPopulated);
  const stageGroupingAvailability = reshapeAvailabilityFor('GROUP');
  const codedPivotAvailability = reshapeAvailabilityFor('CODED_PIVOT');
  const ordinaryGroupAvailable = stageGroupingAvailability.supported || codedPivotAvailability.supported;
  const tablePivotAvailability = constructionLifecycle.capabilities.status === 'ready' && constructionLifecycle.capabilities.response.pivotSourceInput?.supported
    ? { supported: true } : reshapeAvailabilityFor('PIVOT');
  const expandCapability = capabilityStage?.capabilities.find((candidate) => candidate.kind === 'EXPAND');
  const signedExpandAvailable = constructionLifecycle.capabilities.status === 'ready'
    && previewIsCurrent && capabilityIsForAppendStage
    && (constructionLifecycle.capabilities.response.expandSourceInput?.supported ?? false)
    && (constructionLifecycle.capabilities.response.expandSourceInput?.choices ?? []).some((choice) => choice.isPopulated);
  const groupAlternatives = [
    ...(sourceGroupingAvailable ? [{ kind: 'source-group' as const, label: 'Source fields' }] : []),
    ...(codedGroupingAvailable ? [{ kind: 'coded-group' as const, label: 'Recorded code' }] : []),
  ];
  const reshapeRowsAvailability = {
    group: ordinaryGroupAvailable ? { supported: true } : stageGroupingAvailability,
    groupEntry: 'group' as const,
    groupAlternatives,
    expandSigned: signedExpandAvailable ? { supported: true as const } : undefined,
    pivot: codedPivotAvailability.supported ? codedPivotAvailability : tablePivotAvailability,
    pivotPending: Boolean(capabilitiesRequest) && constructionLifecycle.capabilities.status === 'loading',
    pivotEntry: codedPivotAvailability.supported ? 'coded-pivot' as const : 'pivot' as const,
    pivotAlternative: codedPivotAvailability.supported && tablePivotAvailability.supported ? 'pivot' as const : undefined,
  };
  const relatedExpandCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'RELATED_EXPAND',
  );
  const relatedRowsAvailability = capabilityIsForAppendStage &&
    relatedExpandCapability?.supported &&
    Boolean(capabilityStage?.relatedExpandAnchors?.length)
    ? { supported: true }
    : {
        supported: false,
        reason: unavailableOperationReason({
          stage: constructionLifecycle.capabilities.status === 'error'
            ? 'error'
            : constructionLifecycle.capabilities.status !== 'ready'
              ? 'checking'
              : 'ready',
          errorMessage: capabilitiesErrorMessage,
          checkingMessage: 'Checking available related row paths…',
          label: 'This table has no executable related-record row path at the current stage.',
          capability: relatedExpandCapability,
        }),
      };
  const relatedSourceUnavailableReason = hasUnsupportedSavedSourceColumns
    ? 'Remove unsupported saved source fields before adding related fields.'
    : editingConstructionStep
      ? 'Close the saved-step editor before adding related fields; proposals follow the current final step.'
    : constructionLifecycle.capabilities.status === 'ready' && !capabilityIsForAppendStage
      ? 'Related fields can only be added after the current final step.'
      : undefined;
  const relatedSourceAvailability = !relatedSourceUnavailableReason &&
    capabilityIsForAppendStage &&
    relatedSourceCapability?.supported &&
    capabilityStage?.rowIdentityColumn
    ? { supported: true }
    : {
        supported: false,
        reason: relatedSourceUnavailableReason || relatedSourceReason || (
          constructionLifecycle.capabilities.status === 'error'
            ? sanitizeReasonText(constructionLifecycle.capabilities.message)
            : constructionLifecycle.capabilities.status === 'ready'
              ? 'Loom has not proved that this stage retains a source row anchor.'
              : 'Loom is checking whether this stage supports related-source fields.'
        ),
      };
  const sourceSelectionDisabledReason = pendingCommands > 0
    ? 'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.'
    : state.reconciliation === 'pending'
      ? 'Loom is refreshing the current table draft. Field selection will return when the refresh completes.'
      : undefined;
  const sourceColumns = sourceStageDescriptors?.find(
    (stage) => stage.id === constructionSourceStageId,
  )?.columns ?? (table?.document.columns ?? []).map((column) => ({
    id: column.column,
    name: column.column,
    label: column.label,
    type: column.logicalType,
  }));
  const rowMeaning = constructionRowMeaning(sourceRowMeaning, table?.document.construction, sourceColumns);
  const relationshipOwner = JSON.stringify([builderDataKey, state.explorerId, table?.outputId,
    state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);
  const loadRecipeNetwork = createRecipeNetworkLoader({
    firstTableProgress,
    table,
    networkRequestOwner,
    relationshipOwner,
    loomClient,
    projectId,
    state,
    authResourcePath,
    setSavedNetwork,
    completePendingRecipeFocus,
  });
  const pendingRelationship = relationshipEdit?.owner === relationshipOwner ? relationshipEdit.change : undefined;
  const proposeRelationship = (change: SourceRelationshipChange, prepareImmediately = false) => {
    if (!recipeMutationAllowed()) return;
    rememberRecipeFocus();
    const token = recipeOwner.begin(change.action === 'ADD'
      ? { kind: 'connection', parentOccurrenceId: change.parentOccurrenceId, catalogEdgeId: change.edgeId }
      : { kind: 'relationship', occurrenceId: change.occurrenceId });
    if (!token) return;
    recipeEditToken.current = token;
    setRecipeNotice(undefined);
    setSourceProposal(undefined);
    setRelationshipEdit({ owner: relationshipOwner, change });
    if (prepareImmediately) void prepareSourceRelationship(change);
  };
  const cancelRecipeRelationship = (restoreFocus = true) => {
    if (recipeOwner.getState().status === 'applying') return false;
    const token = recipeEditToken.current;
    if (token && !recipeOwner.cancel(token)) return false;
    sourceRelationshipRequest.current?.abort();
    sourceRelationshipRequest.current = undefined;
    setConnectingRelationship(false);
    columnPolicyRequest.current?.abort();
    recipeEditToken.current = undefined;
    setSourceProposal(undefined);
    setRelationshipEdit(undefined);
    setEditingRecipeColumn(undefined);
    setRecipeNotice(undefined);
    constructionLifecycle.cancel();
    if (restoreFocus) restoreRecipeFocus();
    else {
      recipeFocusGeneration.current += 1;
      recipeInitiator.current = undefined;
      recipeInitiatorFact.current = undefined;
      pendingRecipeFocus.current = undefined;
    }
    return true;
  };
  const sourceReviewStatus = (status: 'previewing' | 'applying' | 'failed', message?: string) => {
    const token = recipeEditToken.current;
    if (!token) return;
    if (status === 'previewing') recipeEditToken.current = recipeOwner.previewing(token);
    else if (status === 'failed') recipeOwner.fail(token, message ?? 'The proposal failed.');
  };
  const beginSourceReview = () => {
    const currentToken = recipeEditToken.current;
    if (!currentToken) return undefined;
    const attempt = recipeOwner.previewing(currentToken);
    if (!attempt) return undefined;
    recipeEditToken.current = attempt;
    setSourceProposal(undefined);
    return {
      ready: (proposal: SourceProposalOwnerCandidate, preview: ExplorerBuilderPreviewResult) => {
        if (proposal.outputId !== attempt.identity.outputId || proposal.snapshotToken !== attempt.identity.snapshotToken ||
          proposal.draftVersion !== attempt.identity.draftVersion || proposal.draftDigest !== attempt.identity.draftDigest) {
          recipeOwner.fail(attempt, 'The draft changed while the proposal was loading. Cancel and review the current draft.', true);
          return false;
        }
        const accepted = recipeOwner.ready(attempt, { receiptId: proposal.candidateReceiptId,
          candidateWorkspaceDigest: proposal.candidateWorkspaceDigest, preview,
          candidateDocument: proposal.candidateDocument, candidateNetwork: proposal.candidateNetwork,
          candidateRowIdentity: proposal.candidateRowIdentity });
        if (accepted) {
          lastAcceptedOutputPreview.current = { owner: relationshipOwner, outputId: attempt.identity.outputId,
            snapshotToken: attempt.identity.snapshotToken, draftVersion: attempt.identity.draftVersion,
            draftDigest: attempt.identity.draftDigest, receiptId: preview.receiptId };
          setSourceProposal({ owner: relationshipOwner, proposal, preview });
        }
        else recipeOwner.fail(attempt, 'The candidate preview does not belong to this recipe edit. Preview again or Cancel.');
        return accepted;
      },
      failed: (message: string) => { recipeOwner.fail(attempt, message); },
    };
  };
  const prepareSourceRelationship = (change: SourceRelationshipChange) => {
    const ownership = beginSourceReview();
    if (!ownership) {
      setConnectingRelationship(false);
      return;
    }
    sourceRelationshipRequest.current?.abort();
    const controller = new AbortController();
    sourceRelationshipRequest.current = controller;
    setConnectingRelationship(true);
    void (async () => {
      try {
        const proposal = await loomClient.proposeSourceRelationship({
          project: projectId,
          explorerId: state.explorerId,
          authResourcePath,
          snapshotToken: state.catalog.snapshotToken,
          expectedDraftVersion: state.draftVersion,
          expectedDraftDigest: state.draftDigest,
          outputId: table?.outputId ?? '',
          change,
        }, controller.signal);
        if (controller.signal.aborted) return;
        const preview = proposal.preview;
        if (preview.rows === null || preview.receiptId !== proposal.candidateReceiptId || preview.outputId !== proposal.outputId) {
          throw new Error('The proposal did not include a preview for its candidate receipt.');
        }
        setConnectingRelationship(false);
        ownership.ready(proposal, preview);
      } catch (cause) {
        if (!controller.signal.aborted) {
          setConnectingRelationship(false);
          ownership.failed(cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.');
        }
      } finally {
        if (sourceRelationshipRequest.current === controller) sourceRelationshipRequest.current = undefined;
      }
    })();
  };
  const beginSourceRowsReview = () => {
    const editing = recipeOwner.getState();
    if (editing.status === 'saved') {
      if (!recipeMutationAllowed()) return undefined;
      recipeEditToken.current = recipeOwner.begin({ kind: 'sourceRows' });
    } else if (editing.target?.kind !== 'sourceRows') {
      setRecipeNotice('Apply or Cancel the active recipe change before opening another row editor.');
      return undefined;
    }
    const attempt = beginSourceReview();
    return attempt ? { failed: attempt.failed, ready: (proposal: RowDefinitionProposal, preview: ExplorerBuilderPreviewResult) => proposal.proposalId
      ? attempt.ready({ ...proposal, proposalId: proposal.proposalId, candidateReceiptId: proposal.proposalId }, preview) : false } : undefined;
  };
  const applySourceRecipe = async (commands: ExplorerBuilderCommand[]) => {
    const token = recipeEditToken.current;
    if (!token || commands.some((command) => command.proposalId !== recipeOwner.state.candidate?.receiptId) || !recipeOwner.applying(token)) return false;
    let applied = false;
    try { applied = await applyCommands(commands); } catch (error) {
      recipeOwner.fail(token, error instanceof Error ? error.message : 'The proposal was not saved.'); return false;
    }
    if (applied) { recipeOwner.applied(token); recipeEditToken.current = undefined; setSourceProposal(undefined); setRelationshipEdit(undefined); setEditingRecipeColumn(undefined); restoreRecipeFocus(); }
    else recipeOwner.fail(token, 'The change was not saved. Review the current draft.');
    return applied;
  };

  const attachedPopulationSelection = table?.document.population?.selectionRevisionId === activePopulationSelection?.id
    ? activePopulationSelection
    : undefined;
  const startingCollectionSummary = table?.document.population
    ? attachedPopulationSelection
      ? `Starting collection: ${attachedPopulationSelection.memberCount.toLocaleString()} ${attachedPopulationSelection.resourceType} resources attached`
      : 'Starting collection: Attached selection details are loading'
    : activePopulationSelection
      ? `Starting collection: ${activePopulationSelection.memberCount.toLocaleString()} ${activePopulationSelection.resourceType} selected, not attached`
      : table?.document.rootResourceType
        ? `Starting collection: All authorized ${table.document.rootResourceType} records`
        : 'Starting collection: Choose a starting record type first';
  const persistedConstructionHistory = constructionHistorySteps(
    table?.document.construction,
    sourceColumns,
  );
  const canApplyConstructionProposal = constructionLifecycle.canApply &&
    !cancelingConstructionProposal &&
    pendingCommands === 0 &&
    !publishing &&
    state.reconciliation !== 'pending';
  const populatedFieldItems = table
    ? fieldCatalogItems(state.catalog, table.document.rootResourceType ?? '', undefined, '', undefined, true)
      .filter((item) => item.kind === 'FIELD')
    : [];
  const populatedFieldCandidates = populatedFieldItems.flatMap((item) => item.kind === 'FIELD' ? [item.candidate] : []);
  const operationEditor = table && activeOperation ? (
    <OperationEditorHost
      table={table}
      activeOperation={activeOperation}
      state={state}
      ownerKey={ownerKey}
      rowIntentSession={rowIntentSession}
      editingConstructionStep={editingConstructionStep}
      construction={construction}
      lifecycle={constructionLifecycle}
      pendingCommands={pendingCommands}
      publishing={publishing}
      showReshapeColumnSelection={showReshapeColumnSelection}
      selectedColumnIds={selectedColumnIds}
      selectableColumns={selectableColumns}
      currentStage={currentStage}
      activeConstructionFamily={activeConstructionFamily}
      onToggleSelectedColumn={toggleSelectedColumn}
      setColumnSelection={setColumnSelection}
      rowValuesSupported={rowValuesSupported}
      rowValuePolicy={rowValuePolicy}
      setRowValuePolicy={setRowValuePolicy}
      selectedAddSource={selectedAddSource}
      project={projectId}
      authResourcePath={authResourcePath}
      addColumnsSourceControls={addColumnsSourceControls}
      addSourceRouteContext={addSourceRouteContext}
      addColumnsViewRevision={addColumnsViewRevision}
      fieldPresentationContext={fieldPresentationContext}
      onSwitchAddColumnsView={selectAddColumnsView}
      existingSourceKeys={existingSourceKeys}
      capabilityNodeOccurrences={capabilityNodeOccurrences}
      sourceAvailability={sourceAvailability}
      relatedSourceAvailability={relatedSourceAvailability}
      hasUnsupportedSavedSourceColumns={hasUnsupportedSavedSourceColumns}
      sourceSelectionDisabledReason={sourceSelectionDisabledReason}
      groupedRowValuePolicy={groupedRowValuePolicy}
      onAddSelected={addSelectedFeatures}
      onPreviewSelected={previewRelatedSourceColumns}
      onInspectRouteCoverage={inspectRelatedRouteCoverage}
      onApplyCommands={applyCommands}
      activeReshapeEditorKey={activeReshapeEditorKey}
      activeReshapeInitialKind={activeReshapeInitialKind}
      activeReshapeInitialEntry={activeReshapeInitialEntry}
      onReshapeFormKindChange={setReshapeFormKind}
      relatedExpandQueryOwnerRef={relatedExpandQueryOwnerRef}
      loomClient={loomClient}
      combineCatalog={combineCatalog}
      onEditConstructionStep={editConstructionStep}
      onCancelConstructionEditor={cancelConstructionEditor}
      onClose={closeConstructionWorkspace}
    />
  ) : undefined;
  const relatedStepEditors = table && (editingRelatedSourceStep || editingRelatedFieldStep) ? (
    <RelatedStepEditors
      table={table}
      state={state}
      ownerKey={ownerKey}
      construction={construction}
      lifecycle={constructionLifecycle}
      sourceStep={editingRelatedSourceStep}
      fieldStep={editingRelatedFieldStep}
      project={projectId}
      authResourcePath={authResourcePath}
      pendingCommands={pendingCommands}
      publishing={publishing}
      onCancelSource={() => {
        constructionLifecycle.cancel();
        setEditingConstructionStepId(undefined);
        setActiveConstructionFamily(undefined);
      }}
      onCancelField={() => {
        constructionLifecycle.cancel();
        setEditingConstructionStepId(undefined);
      }}
    />
  ) : undefined;
  const proposalPanel = (
    <ConstructionProposalPanel
      state={constructionLifecycle.proposal}
      baseConstruction={construction ?? (constructionLifecycle.capabilities.status === 'ready' ? constructionLifecycle.capabilities.response.baseConstruction : undefined)}
      currentInput={previewIsCurrent && tablePreview && capabilityIsForAppendStage && capabilityStage
        ? { preview: tablePreview, stageId: capabilityStage.id }
        : undefined}
      canApply={canApplyConstructionProposal}
      onApply={() => void applyConstructionProposal()}
      onCancel={() => {
        const queryOwner = relatedExpandQueryOwnerRef.current;
        const closeProposal = closeConstructionWorkspace;
        if (!queryOwner) {
          closeProposal();
          return;
        }
        if (cancelingConstructionProposalRef.current) return;
        cancelingConstructionProposalRef.current = true;
        setCancelingConstructionProposal(true);
        void (async () => {
          let ownsEditor = false;
          try {
            await queryOwner.pauseAndDrain();
            ownsEditor = relatedExpandQueryOwnerRef.current === queryOwner;
          } finally {
            cancelingConstructionProposalRef.current = false;
            setCancelingConstructionProposal(false);
          }
          if (ownsEditor) closeProposal();
        })();
      }}
      onRetry={constructionLifecycle.retry}
    />
  );
  const workspaceEditor = operationEditor || relatedStepEditors || constructionLifecycle.proposal.status !== 'idle'
    ? <div className="construction-mutation-inspector">
        <div className="construction-mutation-fields">{operationEditor}{relatedStepEditors}</div>
        {proposalPanel}
      </div>
    : undefined;
  const sourceCandidate = sourceProposal?.owner === relationshipOwner ? sourceProposal : undefined;
  const sourceReviewColumnLabels = table ? new Map(table.document.columns.map((column) => [column.columnId ?? column.column, column.label || column.column])) : undefined;
  const rootCandidate = recipeOwner.state.target?.kind === 'root' ? recipeOwner.state.candidate : undefined;
  const candidatePreview = rootCandidate?.preview ?? sourceCandidate?.preview ?? (constructionLifecycle.proposal.status === 'ready' ||
    constructionLifecycle.proposal.status === 'applying'
    ? constructionLifecycle.proposal.preview
    : undefined);
  const lastOutputPreview = lastAcceptedOutputPreview.current;
  const retainedOutputPreviewReceipt = lastOutputPreview?.owner === relationshipOwner && lastOutputPreview.outputId === table?.outputId &&
    lastOutputPreview.snapshotToken === state.catalog.snapshotToken && lastOutputPreview.draftVersion === state.draftVersion &&
    lastOutputPreview.draftDigest === state.draftDigest ? lastOutputPreview.receiptId : undefined;
  const generatedOutputRevision = candidatePreview?.receiptId ?? retainedOutputPreviewReceipt ??
    `saved:${state.catalog.snapshotToken}:${state.draftVersion}:${state.draftDigest}`;
  const proposalResponse = 'response' in constructionLifecycle.proposal
    ? constructionLifecycle.proposal.response
    : undefined;
  const proposalPresentationTable = table && rootCandidate
    ? { ...table, document: rootCandidate.candidateDocument! }
    : table && sourceCandidate
    ? { ...table, document: sourceCandidate.proposal.candidateDocument }
    : table && proposalResponse
    ? {
        ...table,
        document: {
          ...table.document,
          construction: proposalResponse.candidateConstruction,
        },
      }
    : undefined;
  // Inspection is ephemeral and receipt-bound; it never changes the saved route or a stage anchor.
  const candidateInspectionKey = candidatePreview && proposalPresentationTable
    ? JSON.stringify([relationshipOwner, candidatePreview.receiptId, candidatePreview.outputId]) : undefined;
  const inspectedOccurrenceId = candidateInspectionKey && proposalPresentationTable
    ? candidateInspection?.key === candidateInspectionKey && routeNode(proposalPresentationTable.document.route, candidateInspection.occurrenceId)
      ? candidateInspection.occurrenceId
      : routeNode(proposalPresentationTable.document.route, state.selectedOccurrenceId) ? state.selectedOccurrenceId : proposalPresentationTable.document.route.occurrenceId
    : state.selectedOccurrenceId;
  const pendingChangeReview = table && pendingRelationship ? <SourceRelationshipReview key={`${relationshipOwner}:${JSON.stringify(pendingRelationship)}`}
    columnLabels={sourceReviewColumnLabels}
    client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
    snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
    outputId={table.outputId} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
    change={pendingRelationship} sharedPreview onStatusChange={sourceReviewStatus} onPreviewStart={beginSourceReview}
    relationshipDescription={(() => {
      if (pendingRelationship.action === 'ADD') {
        const parent = routeNode(table.document.route, pendingRelationship.parentOccurrenceId);
        const edge = state.catalog.edges.find((item) => item.edgeId === pendingRelationship.edgeId);
        const target = state.catalog.nodes.find((item) => item.nodeId === edge?.toNodeId);
        return parent && edge && target ? `${parent.resourceType} → ${target.resourceType} via ${edge.label}` : undefined;
      }
      const occurrence = routeNode(table.document.route, pendingRelationship.occurrenceId);
      return occurrence ? `${occurrence.resourceType} via ${occurrence.relationship}` : undefined;
    })()}
    preparing={connectingRelationship}
    preparationError={recipeOwner.state.error}
    requestControllerRef={sourceRelationshipRequest}
    {...(sourceCandidate ? { prepared: { proposal: sourceCandidate.proposal, preview: sourceCandidate.preview } } : {})}
    onApply={applySourceRecipe} onCancel={cancelRecipeRelationship} /> : undefined;
  const sourceRecipeInspector = recipeOwner.state.status === 'stale' && recipeOwner.state.target?.kind !== 'root'
    ? <button type="button" className="rounded border border-slate-300 bg-white px-3 py-1 text-sm" onClick={() => cancelRecipeRelationship()}>Cancel</button>
    : recipeOwner.state.target?.kind === 'root' ? (recipeOwner.state.target.mode === 'REPLACE'
      ? <div ref={setRootReplacementInspectorHost} aria-label="Starting-record replacement inspector" />
      : rootCandidate || rootChangeState.kind === 'idle' ? <RowChangePreviewPanel candidateRoot={rootRecipeAssessment.current?.candidateRootResourceType ?? table?.document.rootResourceType ?? 'records'}
          status={rootCandidate ? 'ready' : recipeOwner.state.status === 'failed' || recipeOwner.state.status === 'stale' ? 'error' : 'loading'}
          preview={rootCandidate?.preview} error={recipeOwner.state.error} sharedPreview
          disabled={pendingCommands > 0 || recipeOwner.state.status === 'applying'}
          onApply={() => void applyRowChangePreview()} onCancel={cancelRootChange} /> : undefined)
    : table && (editingRecipeColumn?.owner === relationshipOwner || recipeOwner.state.target?.kind === 'sourceStep') ? <>
      {recipeOwner.state.target?.kind === 'sourceStep' ? <div className="construction-inspector-portal-host" ref={setSourceStepInspectorHost} aria-label="Source step inspector" /> : null}
      {editingRecipeColumn?.owner === relationshipOwner ? (() => {
        const column = table.document.columns.find((item) => (item.columnId ?? item.column) === editingRecipeColumn.columnId);
        const occurrence = column ? derivedOccurrences(table, state.catalog).find((item) => item.id === column.occurrenceId) : undefined;
        return column ? <RecipeColumnPolicyInspector key={`${relationshipOwner}:${editingRecipeColumn.columnId}`}
          column={column} sourceNodeId={occurrence?.nodeId ?? ''} candidate={candidateForPreviewColumn(column)}
          disabled={pendingCommands > 0 || publishing || state.reconciliation === 'pending'}
          onCancel={cancelRecipeRelationship}
          busy={recipeOwner.state.status === 'previewing' || recipeOwner.state.status === 'applying'}
          error={recipeOwner.state.error}
          cancelDisabled={recipeOwner.state.status === 'applying'}
          canApply={recipeOwner.state.status === 'preview-ready' && Boolean(sourceCandidate)}
          onDraftChange={() => {
            if (recipeEditToken.current) recipeEditToken.current = recipeOwner.changed(recipeEditToken.current);
            setSourceProposal(undefined);
          }}
          onPreview={(columnValue) => {
            if (columnValue.source.kind !== 'field' || !columnValue.source.field.projectionMode) return;
            const attempt = beginSourceReview();
            if (!attempt) return;
            columnPolicyRequest.current?.abort();
            const controller = new AbortController();
            columnPolicyRequest.current = controller;
            void (async () => {
              try {
                const proposal = await loomClient.proposeColumnPolicy({ project: projectId, explorerId: state.explorerId,
                  authResourcePath, snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
                  expectedDraftDigest: state.draftDigest, outputId: table.outputId, columnId: column.columnId ?? column.column,
                  projectionMode: columnValue.source.kind === 'field' ? columnValue.source.field.projectionMode! : 'VALUE' }, controller.signal);
                if (controller.signal.aborted) return;
                const preview = await loomClient.preview({ project: projectId, explorerId: state.explorerId, authResourcePath,
                  receiptId: proposal.candidateReceiptId, outputId: table.outputId }, controller.signal);
                if (controller.signal.aborted) return;
                attempt.ready(proposal, preview);
              } catch (error) { if (!controller.signal.aborted) attempt.failed(error instanceof Error ? error.message : 'The column policy could not be previewed.'); }
            })();
          }}
          onChange={() => { if (sourceCandidate) void applySourceRecipe([{ type: 'APPLY_COLUMN_POLICY_PROPOSAL', outputId: table.outputId,
            proposalId: sourceCandidate.preview.receiptId }]); }} /> : null;
      })() : null}
  </> : undefined;
  const selectTraversalScope = (occurrenceId: string) => {
    const editing = recipeOwner.getState();
    const relationshipReviewActive = Boolean(
      pendingRelationship &&
      (editing.target?.kind === 'connection' || editing.target?.kind === 'relationship'),
    );
    if (editing.status === 'applying' && relationshipReviewActive) return;
    const candidateOccurrence = Boolean(
      candidateInspectionKey && proposalPresentationTable &&
      routeNode(proposalPresentationTable.document.route, occurrenceId),
    );
    const savedOccurrence = Boolean(table && routeNode(table.document.route, occurrenceId));
    if (candidateInspectionKey && proposalPresentationTable) {
      if (!candidateOccurrence) return;
      setCandidateInspection({ key: candidateInspectionKey, occurrenceId });
      if (!savedOccurrence) return;
    }
    if (relationshipReviewActive && savedOccurrence) {
      const sameSavedInspection = occurrenceId === state.selectedOccurrenceId &&
        (!candidateInspectionKey || occurrenceId === inspectedOccurrenceId);
      if (!sameSavedInspection) {
        if (!cancelRecipeRelationship(false)) return;
        setCandidateInspection(undefined);
      }
    } else if (candidateInspectionKey && proposalPresentationTable) {
      return;
    }
    dispatch({ type: 'selectOccurrence', occurrenceId });
  };
  const editTraversalColumn = (columnId: string) => {
    const binding = savedNetwork?.owner === relationshipOwner ? savedNetwork.result.network.bindings.find((item) => item.columnId === columnId) : undefined;
    if (binding?.sourceStepId) sourceStepEditAction.current?.(binding.sourceStepId);
    else {
      if (!recipeMutationAllowed()) return;
      rememberRecipeFocus();
      const token = recipeOwner.begin({ kind: 'column', columnId });
      if (!token) return;
      recipeEditToken.current = token;
      setEditingRecipeColumn({ owner: relationshipOwner, columnId });
    }
  };
  const traversalTable = proposalPresentationTable ?? table;
  const traversalNetwork = rootCandidate?.candidateNetwork ?? sourceCandidate?.proposal.candidateNetwork ?? (savedNetwork?.owner === relationshipOwner ? savedNetwork.result.network : undefined);
  const traversalRowIdentity = rootCandidate?.candidateRowIdentity ?? sourceCandidate?.proposal.candidateRowIdentity ?? (proposalPresentationTable ? undefined : savedNetwork?.owner === relationshipOwner ? savedNetwork.result.rowIdentity : undefined);
  const traversalSourceSteps = traversalTable?.document.importedSource?.steps ?? [];
  const resourceTypeChoices = Array.from(new Set((state.catalog.nodes ?? []).filter((node) => node.populated).map((node) => node.resourceType))).sort();
  const sourcePathChoices = Array.from(new Set(populatedFieldCandidates.flatMap((candidate) => [
    ...(candidate.repeated ? [`root.${candidate.fieldPath}[]`] : []),
    ...(candidate.repeatedBoundaries ?? []).map((boundary) => `root.${boundary.path}[]`),
  ]))).sort();
  const fieldPathChoices = Array.from(new Set(populatedFieldCandidates.map((candidate) => candidate.fieldPath))).sort();
  const fieldKindChoices = Array.from(new Set(populatedFieldCandidates.map((candidate) => candidate.logicalType))).sort();
  const catalogOutputsForStep = (stepId: string) => {
    const step = traversalSourceSteps.find((candidate) => candidate.id === stepId && candidate.kind === 'CATALOG_PROJECTION');
    if (!step?.parameters?.catalog) return [];
    const overrides = step.parameters.catalog.outputs ?? [];
    const bindings = traversalNetwork?.bindings.filter((binding) => binding.status === 'EXACT' && binding.sourceStepId === stepId && binding.outputKey) ?? [];
    const emitted = bindings.flatMap((binding) => {
      const matches = traversalTable?.document.columns.filter((column) => (column.columnId ?? column.column) === binding.columnId) ?? [];
      if (matches.length !== 1 || !binding.outputKey) return [];
      const override = overrides.find((item) => item.fieldPath === binding.outputKey);
      return [{ outputKey: binding.outputKey, columnId: binding.columnId, defaultName: matches[0].label || matches[0].column, enabled: override?.enabled ?? true }];
    });
    const emittedKeys = new Set(emitted.map((output) => output.outputKey));
    const persistedDisabled = overrides.filter((output) => !output.enabled && !emittedKeys.has(output.fieldPath))
      .map((output) => ({ outputKey: output.fieldPath, defaultName: output.name ?? output.fieldPath, enabled: false }));
    return [...emitted, ...persistedDisabled];
  };
  const pivotOutputsForStep = (stepId: string) => {
    const step = traversalSourceSteps.find((candidate) => candidate.id === stepId && candidate.kind === 'PIVOT');
    if (!step?.parameters?.pivot) return [];
    const overrides = step.parameters.pivot.outputs ?? [];
    const bindings = traversalNetwork?.bindings.filter((binding) => binding.status === 'EXACT' && binding.sourceStepId === stepId && binding.outputKey) ?? [];
    const emitted = bindings.flatMap((binding) => {
      const matches = traversalTable?.document.columns.filter((column) => (column.columnId ?? column.column) === binding.columnId) ?? [];
      if (matches.length !== 1 || !binding.outputKey) return [];
      const override = overrides.find((item) => item.key === binding.outputKey);
      return [{ outputKey: binding.outputKey, columnId: binding.columnId, defaultName: matches[0].label || matches[0].column, enabled: override?.enabled ?? true }];
    });
    const emittedKeys = new Set(emitted.map((output) => output.outputKey));
    const persistedDisabled = overrides.filter((output) => !output.enabled && !emittedKeys.has(output.key))
      .map((output) => ({ outputKey: output.key, defaultName: output.name || output.key, enabled: false }));
    return [...emitted, ...persistedDisabled];
  };
  const traversalOperationLabels = new Map(traversalSourceSteps.flatMap((step) => {
    const parameters = step.parameters;
    const name = parameters?.dynamic?.name ?? parameters?.extension?.name ?? parameters?.pivot?.name ?? parameters?.catalog?.name ?? parameters?.identity?.name;
    return name ? [[step.id, `${step.kind === 'DYNAMIC_COLUMN' ? 'Keyed columns' : step.kind === 'PIVOT' ? 'Pivot' : step.kind === 'CATALOG_PROJECTION' ? 'Populated fields' : step.kind === 'IDENTITY' ? 'Row identity' : 'Extension columns'}: ${name}`] as const] : [];
  }));
  const editTraversalRows = () => {
    if (!recipeMutationAllowed() || !table) return;
    rememberRecipeFocus();
    if (table.document.importedRecipe) {
      const sourceStep = table.document.importedSource?.steps.find((step) => step.kind === 'EXPANSION')
        ?? table.document.importedSource?.steps.find((step) => step.kind === 'OUTPUT');
      if (sourceStep) { sourceStepEditAction.current?.(sourceStep.id); return; }
    }
    rowSettingsOpenAction.current?.();
  };
  const canInspectOccurrence = Boolean(table && recipeOwner.state.status === 'saved' && !sourceRowsOpen && !sourceRecipeInspector && !workspaceEditor &&
    routeNode(table.document.route, inspectedOccurrenceId));
  const occurrenceInspection = canInspectOccurrence ? <div className="min-w-0" ref={setOccurrenceInspectorHost} /> : undefined;
  const recordNetwork = table ? (
    <div className="flex min-h-0 min-w-0 flex-col gap-2" ref={(node) => { if (node) loadRecipeNetwork(); }}>
      {recipeNotice ? <p role="alert" className="text-xs text-amber-900">{recipeNotice}</p> : null}
      <AuthoredTraversalEditor
        projectionKind={proposalPresentationTable ? 'proposed' : 'saved'}
        network={rootCandidate?.candidateNetwork ?? sourceCandidate?.proposal.candidateNetwork ?? (savedNetwork?.owner === relationshipOwner ? savedNetwork.result.network : undefined)}
        rowIdentity={rootCandidate?.candidateRowIdentity ?? sourceCandidate?.proposal.candidateRowIdentity ?? (proposalPresentationTable ? undefined : savedNetwork?.owner === relationshipOwner ? savedNetwork.result.rowIdentity : undefined)}
        showInspector={canInspectOccurrence}
        inspectorHost={occurrenceInspectorHost}
        onInspectValues={() => { selectConstructionFamily('ADD_COLUMNS'); selectAddColumnsView('fields'); }}
        onInspectRelatedRows={() => chooseRelatedRows()}
        onEditSourceStep={(stepId) => sourceStepEditAction.current?.(stepId)}
        onInspectOperation={editConstructionStep}
        showOccurrenceNavigation={false}
        onEditStartingRecords={() => { if (recipeMutationAllowed()) rowSettingsOpenAction.current?.(); }}
        mutationBlockedReason={recipeOwner.state.status !== 'saved' ? 'Apply or Cancel the active recipe change before starting another edit.' : undefined}
        presentation="compact"
        onTableToolbarHostChange={setTableToolbarHost}
        catalog={state.catalog} table={proposalPresentationTable ?? table} selectedOccurrenceId={inspectedOccurrenceId}
        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved'}
        importedRelationshipActions={table.document.importedRecipe ? {
          onAdd: (parentOccurrenceId, edgeId) => proposeRelationship({ action: 'ADD', parentOccurrenceId, edgeId, matchMode: 'OPTIONAL' }, true),
          onChangeMatchMode: (occurrenceId, matchMode) => proposeRelationship({ action: 'MATCH', occurrenceId, matchMode }),
          onRemove: (occurrenceId) => proposeRelationship({ action: 'REMOVE', occurrenceId }),
        } : undefined}
        onDefineRow={editTraversalRows}
        onSelectOccurrence={selectTraversalScope}
        onSetBase={(nodeId) => void applyCommands([{ type: 'CREATE_TABLE', title: table.title, rootNodeId: nodeId }])}
        onChangeBase={(nodeId) => selectRootChoice({ kind: 'catalog-node', nodeId })}
        onAppendEdge={(parentOccurrenceId, edgeId) => {
          if (!table.document.importedRecipe) void applyCommands([{ type: 'ADD_ROUTE', outputId: table.outputId, parentOccurrenceId, edgeId }]);
        }}
        onChangeEdge={(occurrenceId, edgeId) => {
          if (!table.document.importedRecipe) void applyCommands([{ type: 'UPDATE_ROUTE_EDGE', outputId: table.outputId, occurrenceId, edgeId }]);
        }}
        onChangeMatchMode={(occurrenceId, matchMode) => {
          if (!table.document.importedRecipe) void applyCommands([{ type: 'SET_ROUTE_MATCH_MODE', outputId: table.outputId, occurrenceId, matchMode }]);
        }}
        onTruncate={(occurrenceId) => {
          if (table.document.importedRecipe) return;
          const subtree = routeNode(table.document.route, occurrenceId);
          if (!subtree) return;
          const ids = routeSubtreeOccurrenceIds(subtree);
          const columnCount = table.document.columns.filter((column) => ids.has(column.occurrenceId)).length;
          if (!window.confirm(`Remove this local branch (${ids.size} occurrence${ids.size === 1 ? '' : 's'}, ${columnCount} column${columnCount === 1 ? '' : 's'})?`)) return;
          void applyCommands([{ type: 'REMOVE_ROUTE', outputId: table.outputId, occurrenceId }]);
        }}
      />
    </div>
  ) : undefined;

  const candidatePreviewElement = candidatePreview ? (
    <ConstructionProposalPreview
      preview={candidatePreview}
      presentationTable={proposalPresentationTable}
    />
  ) : undefined;
  const externalWorkspaceEditor = sourceRecipeInspector ?? (sourceRowsOpen ? <div className="construction-inspector-portal-host" ref={setSourceRowsInspectorHost} aria-label="Source row settings inspector" /> : workspaceEditor);
  const workspaceCandidatePreview = candidatePreview;
  const workspacePreview = workspaceCandidatePreview ?? tablePreview;
  const workspacePreviewIsCurrent = Boolean(workspaceCandidatePreview) || previewIsCurrent;
  const workspacePreviewStatus = workspaceCandidatePreview
    ? 'ready'
    : automaticPreviewQuery.error
      ? 'error'
      : constructionLifecycle.proposal.status === 'previewing'
      ? 'previewing'
      : constructionLifecycle.proposal.status === 'needs-repair'
        ? 'needs-repair'
        : constructionLifecycle.proposal.status === 'error'
          ? 'error'
          : currentPreviewStatus;
  const workspacePreviewProposalId = workspaceCandidatePreview?.receiptId ??
    (!rowIntentSession ? proposalResponse?.proposalId : undefined);
  const tableCreatorPanel = (
    <TableCreatorPanel
      isFirstTable={state.tables.length === 0}
      previousDraftRevisionId={previousDraftRevisionId}
      pendingCommands={pendingCommands}
      publishing={publishing}
      reconciliationPending={state.reconciliation === 'pending'}
      firstTableName={firstTableName}
      onFirstTableNameChange={setFirstTableName}
      firstTableProgress={firstTableProgress}
      catalog={state.catalog}
      onChooseRoot={(nodeId) => void createFirstTableFromRoot(nodeId)}
      onUndo={() => void restorePreviousDraft()}
      onCancel={() => {
        setTableCreatorOpen(false);
        setFirstTableName('');
      }}
    />
  );

  const rootChangePreviewReady = pendingRowChangePreview?.status === 'ready' &&
    currentRootChangeIdentity !== undefined &&
    sameRootChangeRequestIdentity(pendingRowChangePreview.identity, currentRootChangeIdentity);

  return (
    <main ref={workspaceHostRef} className="min-h-full bg-slate-50 p-2 text-slate-900 sm:p-3">
      {toolbarHost ? createPortal(toolbar, toolbarHost) : toolbar}
      {reviewOpen ? (
        <DatasetReviewPanel
          tables={state.tables}
          catalog={state.catalog}
          receipt={receiptContainsOutputIds(state.receipt, state.tables.map((candidate) => candidate.outputId))
            ? state.receipt
            : undefined}
          preview={receiptContainsOutputIds(state.receipt, state.tables.map((candidate) => candidate.outputId))
            ? state.preview
            : undefined}
          diagnostics={state.diagnostics}
          reconciliation={state.reconciliation}
          onFocus={focusDatasetReviewTarget}
          onClose={() => setReviewOpen(false)}
        />
      ) : null}
      <div className="mx-auto max-w-[1920px] space-y-3">
        {(message ||
          blockingDiagnostics ||
          hasUnsupportedSavedSourceColumns ||
          state.reconciliation === 'stale') && (
          <section
            className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-900"
            role="alert"
          >
            <div className="font-semibold">
              {blockingDiagnostics
                ? 'Builder needs attention'
                : state.reconciliation === 'stale'
                  ? 'Catalog or receipt changed'
                  : 'Builder needs attention'}
            </div>
            <p>
              {hasUnsupportedSavedSourceColumns &&
              (!primaryDiagnostic || isUnsupportedUnknownSourceProjectionDiagnostic(primaryDiagnostic))
                ? 'Some saved source fields have an unsupported type. Remove them to restore Preview and field selection.'
                : primaryDiagnostic?.message ?? message}
            </p>
            {hasUnsupportedSavedSourceColumns ? (
              <ul className="mt-2 space-y-2">
                {unsupportedColumns.map((column) => (
                  <li
                    key={`${column.outputId}:${column.column}`}
                    className="flex flex-wrap items-center gap-2 rounded-md border border-red-200 bg-white/70 px-2 py-2"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="font-semibold">{column.tableTitle}</span>
                      <span className="mx-1" aria-hidden="true">·</span>
                      <span className="font-mono">{column.label}</span>
                    </span>
                    <button
                      type="button"
                      className="rounded border border-slate-300 bg-white px-2.5 py-1 font-semibold text-slate-800 hover:bg-slate-50 disabled:opacity-50"
                      aria-label={`Open ${column.tableTitle} table to review ${column.label}`}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onClick={() => selectTableFromUser(column.outputId)}
                    >
                      Open {column.tableTitle}
                    </button>
                    <button
                      type="button"
                      className="rounded border border-red-300 bg-white px-2.5 py-1 font-semibold text-red-800 hover:bg-red-50 disabled:opacity-50"
                      aria-label={`Remove ${column.label} from ${column.tableTitle}`}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onClick={() => void applyCommands([{
                        type: 'REMOVE_COLUMN',
                        outputId: column.outputId,
                        column: column.column,
                      }])}
                    >
                      Remove
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
            {unsupportedSourceProjectionDiagnostic ? (
              <details className="mt-2 rounded border border-red-200 bg-white/70 px-2 py-1.5">
                <summary className="cursor-pointer font-medium">
                  Technical details · Code: {unsupportedSourceProjectionDiagnostic.code}
                </summary>
                <p className="mt-1 break-words font-mono text-[11px]">
                  {unsupportedSourceProjectionDiagnostic.message}
                </p>
                {unsupportedSourceProjectionDiagnostic.requestId ? (
                  <p className="mt-1 font-mono text-[11px]">
                    Request ID: {unsupportedSourceProjectionDiagnostic.requestId}
                  </p>
                ) : null}
              </details>
            ) : primaryDiagnostic?.code ? (
              <p className="mt-1">
                Technical details · Code: {primaryDiagnostic.code}
              </p>
            ) : null}
            {(state.reconciliation === 'stale' ||
              state.reconciliation === 'repair') &&
            state.tables.length > 0 &&
            !incomplete ? (
              <button
                type="button"
                className="mt-2 rounded border border-blue-300 bg-white px-2.5 py-1 font-semibold text-blue-800 hover:bg-blue-50"
                onClick={() => dispatch({ type: 'requestRecompile' })}
              >
                Recompile
              </button>
            ) : null}
          </section>
        )}
        {suggestionsFailure?.key === suggestionIdentity ? (
          <div className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950"
            role="alert" data-testid="builder-suggestions-error">
            <p>{suggestionsFailure.message}</p>
            <button type="button" className="mt-2 font-semibold text-blue-800 underline"
              data-testid="builder-suggestions-retry"
              onClick={() => {
                suggestionRequestKey.current = '';
                setSuggestionsFailure(undefined);
                ensureSuggestions();
              }}>
              Retry finding columns
            </button>
          </div>
        ) : null}
        {state.tables.length === 0 ? tableCreatorPanel : (
          <>
            <span key={suggestionIdentity} ref={suggestionHostRef} hidden />
            {tableCreatorOpen ? tableCreatorPanel : null}
            {table ? (
              <ConstructionWorkspace
                composition={workspaceComposition}
                showNavigationDuringEditor={false}
                network={recordNetwork}
                navigation={traversalTable ? <div className="space-y-3">
                  <TraversalScopeNavigator table={traversalTable} network={traversalNetwork} rowIdentity={traversalRowIdentity}
                    showScopeNavigation={false}
                    constructionStages={constructionLifecycle.proposal.status === 'ready' ? constructionLifecycle.proposal.response.stages
                      : constructionCapabilitiesResponse?.outputId === table.outputId && constructionCapabilitiesResponse.snapshotToken === state.catalog.snapshotToken && constructionCapabilitiesResponse.draftVersion === state.draftVersion && constructionCapabilitiesResponse.draftDigest === state.draftDigest ? constructionCapabilitiesResponse.stages : undefined}
                    selectedOccurrenceId={inspectedOccurrenceId} sourceOperationLabels={traversalOperationLabels}
                    selectedOperation={recipeOwner.state.target?.kind === 'sourceStep' ? { kind: 'source', stepId: activeSourceStepId ?? recipeOwner.state.target.sourceStepId }
                      : editingConstructionStepId ? { kind: 'construction', stepId: editingConstructionStepId } : undefined}
                    projectionKind={proposalPresentationTable ? 'proposed' : 'saved'}
                    disabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved'}
                    sourceStepEditOnly={recipeOwner.state.target?.kind === 'sourceStep' && recipeOwner.state.status !== 'applying' && recipeOwner.state.status !== 'stale'}
                    sourceOperationNavigationDisabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status === 'applying' || recipeOwner.state.status === 'stale'}
                    disabledReason={recipeOwner.state.status !== 'saved' ? 'Select an existing source operation to include it in this change.' : undefined}
                    onSelectScope={selectTraversalScope} onEditSourceStep={(stepId) => sourceStepEditAction.current?.(stepId)}
                    onEditSourceOutput={(stepId, outputKey) => { setActiveSourceStepId(stepId); sourceOutputEditAction.current?.(stepId, outputKey); }}
                    onEditConstructionStep={editConstructionStep} onEditColumn={editTraversalColumn} onEditSourceRows={editTraversalRows}
                    onRemoveSourceStep={(stepId) => {
                      if (!recipeMutationAllowed()) return;
                      void applyCommands([{ type: 'UPDATE_IMPORTED_SOURCE_STEP', outputId: table.outputId, sourceStep: { stepId, enabled: false } }]);
                    }}
                    onRemoveConstructionStep={removeConstructionStep}
                    onExpandRelatedRows={() => chooseRelatedRows()} onGroupRows={() => chooseReshapeRows('group')}
                    resultActions={<ConstructionActionBar variant="compact" activeFamily={activeConstructionFamily}
                      families={['ADD_COLUMNS', 'KEEP_ROWS', 'CALCULATE', 'RESHAPE']}
                      disabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved' || state.reconciliation === 'pending'}
                      addColumnsDisabled={Boolean(addColumnsReason)}
                      addColumnsReason={addColumnsReason}
                      calculateAvailable={Boolean(capabilityIsForAppendStage && capabilityStage?.capabilities.some((candidate) => candidate.kind === 'DERIVE' && candidate.supported))}
                      calculateUnavailableReason={calculateUnavailableReason} onSelect={selectConstructionFamily} />}
                    combineActions={<ConstructionActionBar variant="compact" activeFamily={activeConstructionFamily}
                      families={['COMBINE']}
                      disabled={pendingCommands > 0 || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved' || state.reconciliation === 'pending'}
                      combineDisabled={!canStartCombine}
                      onSelect={selectConstructionFamily} />} />
                </div> : undefined}
                tables={state.tables.map((candidate) => ({
                  outputId: candidate.outputId,
                  title: candidate.title,
                }))}
                selectedOutputId={table.outputId}
                tableActionsDisabled={
                  (sourceRowsOpen && recipeOwner.state.status === 'saved') || pendingCommands > 0 ||
                  publishing ||
                  firstTableProgress.kind === 'running' ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                onSelectTable={(outputId) => {
                  if (sourceRowsOpen && recipeOwner.state.status === 'saved') { setRecipeNotice('Close source row settings before switching tables.'); return; }
                  setSourceRowsOpen(false);
                  if (recipeOwner.state.status === 'saved') {
                    selectConstructionHistory({ kind: 'source' });
                    combineCatalog.cancel();
                  }
                  selectTableFromUser(outputId);
                }}
                onNewTable={addTable}
                onDuplicateTable={duplicateTable}
                onDeleteTable={deleteSelectedTable}
                onRenameTable={renameTableById}
                rowSetup={(
                  <RowDefinitionSettingsPanel
                    key={`${table.outputId}:${state.catalog.snapshotToken}:${builder.data?.draftVersion ?? ''}:${builder.data?.draftDigest ?? ''}`}
                    inspectorHost={sourceRowsInspectorHost}
                    onOpenChange={setSourceRowsOpen}
                    operationInspectorPresentation="workspace"
                    openActionRef={rowSettingsOpenAction}
                    closeActionRef={rowSettingsCloseAction}
                    replacementPresentation="shared" replacementInspectorHost={rootReplacementInspectorHost}
                    onBeginRootProposal={(nodeId) => rootRecipeActions.current?.begin(nodeId, 'REPLACE')}
                    onCancelRootProposal={cancelRootChange}
                    onBeginProposal={beginSourceRowsReview}
                    onCancelProposal={() => { if (recipeOwner.state.target?.kind === 'sourceRows') cancelRecipeRelationship(); else if (recipeOwner.state.status === 'saved') restoreRecipeFocus(); }}
                    presentation="compact"
                    triggerPresentation="hidden"
                    triggerLabel={table.document.rootResourceType || 'Choose records'}
                    client={loomClient}
                    project={projectId}
                    explorerId={state.explorerId}
                    authResourcePath={authResourcePath}
                    snapshotToken={state.catalog.snapshotToken}
                    draftVersion={state.draftVersion}
                    draftDigest={state.draftDigest}
                    table={table}
                    workspaceTables={state.tables}
                    currentRowMeaning={rowMeaning}
                    startingCollectionSummary={startingCollectionSummary}
                    renderRootSettings={(onRootChange, onResolveRootChange, onReplaceRoot) => (
                      <RowDefinitionPanel
                        catalog={state.catalog}
                        table={table}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                        rootChangeState={rootChangeState}
                        currentIdentity={rootChangeIdentityForTable(table.outputId)}
                        onChange={onRootChange}
                        onResolve={onResolveRootChange}
                        onRefresh={refreshRootChange}
                        onKeepCurrent={cancelRootChange}
                        onReplace={onReplaceRoot}
                      />
                    )}
                    startingCollectionSettings={(
                      activePopulationSelection ||
                      table.document.population ||
                      activePopulationSelectionLoading ||
                      activePopulationSelectionError
                    ) ? (
                      <PopulationPanel
                        table={table}
                        selection={activePopulationSelection}
                        loading={activePopulationSelectionLoading}
                        error={activePopulationSelectionError}
                        project={projectId}
                        explorerId={state.explorerId}
                        authResourcePath={authResourcePath}
                        snapshotToken={state.catalog.snapshotToken}
                        draftVersion={state.draftVersion}
                        draftDigest={state.draftDigest}
                        receiptId={state.receipt?.receiptId}
                        disabled={activePopulationSelectionLoading || populationVariantPending || pendingCommands > 0 || state.reconciliation === 'pending'}
                        onAttach={(routeChoiceId) => void applyCommands([{
                          type: 'SET_TABLE_POPULATION',
                          outputId: table.outputId,
                          selectionRevisionId: activePopulationSelection?.id,
                          routeChoiceId,
                        }])}
                        onClear={() => {
                          if (activePopulationSelection) setClearedPopulationSelection({
                            contextKey: populationSelectionContextKey,
                            client: loomClient,
                            selectionID: activePopulationSelection.id,
                          });
                          void applyCommands([{
                            type: 'CLEAR_TABLE_POPULATION',
                            outputId: table.outputId,
                          }]);
                        }}
                        onExclude={(ref, route) => void excludePopulationMember(ref, route)}
                        onApplyMemberRemoval={(proposalId) => applyCommands([{
                          type: 'APPLY_POPULATION_MEMBER_PROPOSAL',
                          outputId: table.outputId,
                          proposalId,
                        }])}
                      />
                    ) : null}
                    selection={activePopulationSelection}
                    constructionHistory={persistedConstructionHistory}
                    disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                    relatedRows={relatedRowsAvailability}
                    reshapeRows={reshapeRowsAvailability}
                    onChooseRelatedRows={chooseRelatedRows}
                    onChooseReshape={chooseReshapeRows}
                    rowOperationEditor={undefined}
                    onSwitchRowIntent={switchRowIntent}
                    onCloseRowIntentSession={closeRowIntentSession}
                    onChangeRootSelection={selectRootChoice}
                    onResolveRootSelection={resolveRootChange}
                    rootChangeState={rootChangeState}
                    rootChangePreviewOpen={rootChangePreviewReady}
                    onCancelRootChange={cancelRootChange}
                    onEditConstructionStep={editConstructionStep}
                    onRemoveConstructionStep={removeConstructionStep}
                    onApply={(proposalId) => applySourceRecipe([{
                      type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId: table.outputId, proposalId,
                    }])}
                    onApplyRootReplacement={(assessment) => rootRecipeActions.current?.apply(assessment) ?? Promise.resolve(false)}
                  />
                )}
                history={{
                  resourceType: table.document.rootResourceType,
                  steps: persistedConstructionHistory,
                  selected: constructionHistorySelection,
                  disabled: pendingCommands > 0 || publishing || recipeOwner.state.status !== 'saved',
                  onSelect: selectConstructionHistory,
                  onEditStep: editConstructionStep,
                  onRemoveStep: removeConstructionStep,
                  ...(table.document.importedRecipe ? {
                    sourceSteps: (
                      <ImportedSourceSteps
                        key={JSON.stringify([
                          builderDataKey,
                          state.explorerId,
                          table.outputId,
                          state.draftVersion,
                          state.draftDigest,
                        ])}
                        outputId={table.outputId}
                        importedSource={table.document.importedSource}
                        editorHost={sourceStepInspectorHost}
                        reviewHost={sourceChangeReviewHost}
                        editActionRef={sourceStepEditAction}
                        fieldEditActionRef={sourceFieldEditAction} navigatorOnly reviewOwnsCancel
                        outputEditActionRef={sourceOutputEditAction}
                        resourceTypeChoices={resourceTypeChoices}
                        sourcePathChoices={sourcePathChoices}
                        fieldPathChoices={fieldPathChoices}
                        fieldKindChoices={fieldKindChoices}
                        catalogOutputsForStep={catalogOutputsForStep}
                        pivotOutputsForStep={pivotOutputsForStep}
                        outputRevision={generatedOutputRevision}
                        onActiveOperationChange={setActiveSourceStepId}
                        onBeginEdit={(stepId) => {
                          if (!recipeMutationAllowed()) return false;
                          constructionLifecycle.cancel();
                          setActiveConstructionFamily(undefined);
                          setEditingConstructionStepId(undefined);
                          setRowIntentSession(false);
                          rememberRecipeFocus();
                          const token = recipeOwner.begin({ kind: 'sourceStep', sourceStepId: stepId });
                          recipeEditToken.current = token;
                          if (!token) return false;
                          setActiveSourceStepId(stepId);
                          return true;
                        }}
                        onCancelEdit={() => { setActiveSourceStepId(undefined); cancelRecipeRelationship(); }}
                        onDraftChange={() => {
                          if (recipeEditToken.current) recipeEditToken.current = recipeOwner.changed(recipeEditToken.current);
                          setSourceProposal(undefined);
                        }}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                        renderReview={(sourceStep, onCancel, sourceSteps) => <SourceRelationshipReview
                          columnLabels={sourceReviewColumnLabels}
                          key={`${relationshipOwner}:source:${JSON.stringify(sourceSteps ?? sourceStep)}`}
                          client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                          snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
                          outputId={table.outputId} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                          {...(sourceSteps ? { sourceSteps } : { sourceStep })} sharedPreview onStatusChange={sourceReviewStatus} onPreviewStart={beginSourceReview}
                          onApply={applySourceRecipe} onCancel={onCancel} />}
                        onRefresh={() => recipeMutationAllowed() ? applyCommands([{
                          type: 'REFRESH_IMPORTED_SOURCE',
                          outputId: table.outputId,
                        }]) : Promise.resolve(false)}
                        onApply={(command) => applyCommands([command])}
                      />
                    ),
                  } : {}),
                }}
                actionsDisabled={
                  !table.document.rootResourceType ||
                  pendingCommands > 0 ||
                  state.reconciliation === 'pending' ||
                  rootChangeState.kind !== 'idle' ||
                  Boolean(pendingRowChangePreview) ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                addColumnsDisabled={Boolean(addColumnsReason)}
                addColumnsReason={addColumnsReason}
                combineDisabled={!canStartCombine}
                calculateAvailable={Boolean(
                  capabilityIsForAppendStage &&
                  capabilityStage?.capabilities.some(
                    (candidate) => candidate.kind === 'DERIVE' && candidate.supported,
                  ),
                )}
                calculateUnavailableReason={calculateUnavailableReason}
                activeFamily={activeConstructionFamily}
                onSelectFamily={selectConstructionFamily}
                preview={
                  <>
                    {!workspaceCandidatePreview && currentPreviewStatus === 'stale' ? (
                      <p
                        role="status"
                        data-testid="construction-preview-stale-notice"
                        className="border-b border-amber-200 bg-amber-50 px-4 py-2 text-xs text-amber-950"
                      >
                        Showing the last successful preview for this table.
                        The table refreshes automatically when your changes are saved.
                      </p>
                    ) : null}
                    {workspaceCandidatePreview ? (
                      candidatePreviewElement
                    ) : (
                      <PreviewTable
                        preview={tablePreview}
                        previewErrorMessage={automaticPreviewFailureMessage}
                        table={table}
                        limit={previewLimit}
                        onLimitChange={(limit) => {
                          setPreviewLimit(limit);
                        }}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                        onRowLineage={tablePreview ? (rowId, offset, signal) => loomClient.rowLineage({
                          project: projectId,
                          explorerId: state.explorerId,
                          authResourcePath,
                          receiptId: tablePreview.receiptId,
                          outputId: tablePreview.outputId,
                          rowId,
                          offset,
                          limit: 25,
                        }, signal) : undefined}
                      />
                    )}
                  </>
                }
                editor={externalWorkspaceEditor}
                inspection={occurrenceInspection}
                changeReview={pendingChangeReview}
                onChangeReviewHostChange={setSourceChangeReviewHost}
                previewStatus={workspacePreviewStatus}
                previewReceiptId={workspacePreview?.receiptId}
                previewOutputId={workspacePreview?.outputId ?? table?.outputId}
                proposalId={workspacePreviewProposalId}
                draftVersion={state.draftVersion}
                draftDigest={state.draftDigest}
              />
            ) : null}
          </>
        )}
      </div>
    </main>
  );
};

const BuilderWorkspace = (
  props: React.ComponentProps<typeof BuilderWorkspaceContent>,
) => {
  const projectId = props.organization
    ? `${props.organization}/${props.project}`
    : props.project;
  const authResourcePath = props.organization
    ? `/programs/${props.organization}/projects/${props.project}`
    : undefined;
  const ownerKey = builderOwnerKeyFor(
    projectId,
    authResourcePath,
    !props.explorerId || props.explorerId === 'default' ? 'dataframe-default' : props.explorerId,
  );
  return <BuilderWorkspaceContent key={ownerKey} {...props} />;
};

export default BuilderWorkspace;
