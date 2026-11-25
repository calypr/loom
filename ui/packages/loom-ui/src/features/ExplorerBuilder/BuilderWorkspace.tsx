import { admitAuthoringRequest } from './model/admission';
import React, { useCallback, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useAutomaticPreview } from './useAutomaticPreview';
import { useAutomaticPreviewOwner } from './authoring/useAutomaticPreviewOwner';
import { useReconcile } from './authoring/useReconcile';
import { constructionAppendStageFor, constructionInputStageFor } from './model/constructionStages';
import { effectiveOutputAvailability } from './model/outputEligibility';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useAssessExplorerRowChangeMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useResolveConfiguredColumnContextsQuery,
  useResolvePopulationSelectionQuery,
  useGetExplorerCandidateSuggestionsV2Mutation,
  useLoomClient,
} from '../../react';
import type { SelectionRevision } from '../../contracts/selection';
import { canonicalProject, type ExplorerAuthoringApiError } from '../../client/api';
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
  Construction,
  ConstructionStep,
  ConstructionChoiceSelection,
  SourceRelationshipChange,
  AuthoringNetworkResponse,
  SourceRelationshipProposal,
  SourceRelationshipProposalWithPreview,
  RowDefinitionProposal,
} from '../../contracts/types';
import { BuilderToolbar, NewExplorerControl } from './components/BuilderToolbar';
import {
  DatasetReviewPanel,
  type DatasetReviewTarget,
} from './components/DatasetReviewPanel';
import { sameRecipeEditingIdentity, useRecipeEditingOwner, projectRecipeCandidate, type RecipeEditToken } from './session/recipeEditingOwner';
import { CreateColumnRule } from './CreateColumnRule';
import { columnRuleDefaults, type ColumnRuleOccurrence } from './model/sourceColumnRule';
import { SourceRelationshipReview } from './components/SourceRelationshipReview';
import { AuthoredTraversalEditor } from './components/AuthoredTraversalEditor';
import { authoredTraversal } from './model/authoredTraversal';
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
} from './model/authoring';
import {
  isCompilerNavigationSelection,
} from './model/workspaceGuards';
import {
  sameRootChangeRequestIdentity,
} from './model/rootChangeSelection';
import type { PreviewLimit } from './model/previewRecovery';
import { useCommandQueue } from './authoring/useCommandQueue';
import { usePublish } from './authoring/usePublish';
import { shouldShowBuilderLoadError } from './authoring/builderLoadError';
import { useBuilderDraft } from './authoring/useBuilderDraft';
import { useRecipeFocus } from './authoring/useRecipeFocus';
import { useReshapeAvailability } from './authoring/useReshapeAvailability';
import { useConstructionNavigation } from './authoring/useConstructionNavigation';
import { useFirstTable } from './authoring/useFirstTable';
import { useTableExplorerCrud } from './authoring/useTableExplorerCrud';
import { useSuggestions } from './authoring/useSuggestions';
import { usePopulationExclusion } from './authoring/usePopulationExclusion';
import { useAddColumns } from './authoring/useAddColumns';
import { useTraversalProjectedFields } from './authoring/useTraversalProjectedFields';
import { useSourceRecipe } from './authoring/useSourceRecipe';
import { useRootChange } from './authoring/useRootChange';
import { useCombineCatalog } from './authoring/useCombineCatalog';
import { useDirtyBeforeUnload } from './hooks/useDirtyBeforeUnload';
import { usePortalHost } from './hooks/usePortalHost';
import { fieldCatalogItems, sanitizeReasonText, unavailableOperationReason } from './model/catalogItems';

import {
  ConstructionWorkspace,
  ConstructionActionBar,
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
} from './model/constructionHistory';
import {
  sourceProjectionAvailability,
} from './model/sourceProjectionAvailability';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
} from './constructionWorkspace/useConstructionLifecycle';
import { ConstructionOperationEditor } from './constructionOperations/ConstructionOperationEditor';
import { FilterRowsEditor } from './constructionOperations/FilterRowsEditor';
import { ConstructionReshapeEditor, type ReshapeEntryKind, type ReshapeForm } from './constructionOperations/ConstructionReshapeEditor';
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

type OccurrencePanel = {
  readonly family: ConstructionOperationFamily | undefined;
  readonly addColumns: ReturnType<typeof useAddColumns>['panel'];
  readonly reshapeEntryKind: ReshapeEntryKind;
  readonly reshapeFormKind: ReshapeForm['kind'];
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

const builderOwnerKeyFor = (
  project: string,
  authResourcePath: string | undefined,
  explorerId: string,
): string => JSON.stringify([
  canonicalProject(project),
  authResourcePath?.trim() ?? '',
  explorerId,
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
    const available = (explorers.data ?? []).filter(
      (candidate) =>
        candidate.explorerId === 'dataframe-default' ||
        (candidate.explorerId !== 'default' && candidate.management?.toUpperCase() !== 'REPOSITORY'),
    );
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
    draftAccess,
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
    const fallback = explorerSummaries.find((candidate) => candidate.explorerId !== 'dataframe-default');
    if (fallback) setSelectedExplorerId(fallback.explorerId);
  }
  const capabilities = useGetExplorerAuthoringCapabilityV2Query(builderQueryArgs);
  const [applyBuilderCommands] = useApplyExplorerBuilderCommandsV2Mutation();
  const [assessRowChange, rowChangeStatus] = useAssessExplorerRowChangeMutation();
  const [getSuggestions, suggestionsStatus] =
    useGetExplorerCandidateSuggestionsV2Mutation();
  const [message, setMessage] = useState<string>();
  const { occurrenceInspectorHost, setOccurrenceInspectorHost, editingRecipeColumn, setEditingRecipeColumn,
    sourceStepInspectorHost, setSourceStepInspectorHost, activeSourceStepId, setActiveSourceStepId,
    columnPolicyRequest, sourceStepEditAction, sourceStepSwitchOccurrenceAction, sourceFieldEditAction, sourceOutputEditAction,
    rowSettingsOpenAction, rowSettingsCloseAction, actionsFor: traversalActionsFor } = useTraversalProjectedFields();
  const [sourceChangeReviewHost, setSourceChangeReviewHost] = useState<HTMLDivElement | null>(null);
  const sourceStepEditDirty = useRef(false);
  const [pendingSavedRuleRemoval, setPendingSavedRuleRemoval] = useState<string>();
  const [advancedView, setAdvancedView] = useState<'add' | 'saved'>('add');
  const advancedRuleEditorReturn = useRef<string | undefined>(undefined);
  const sourceStepEditCancelAction = useRef<(() => void) | undefined>(undefined);
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
  const { firstTableName, setFirstTableName, tableCreatorOpen, setTableCreatorOpen,
    firstTableProgress, firstTableActionPending, actionsFor: firstTableActionsFor } = useFirstTable({
    draftAccess, getSuggestions, projectId, authResourcePath, setMessage,
  });
  const [featureMode, setFeatureMode] = useState<'catalog' | 'graph'>('catalog');
  const { sourceRelationshipRequest, recipeEditToken, sourceProposal, setSourceProposal,
    lastAcceptedOutputPreview, connectingRelationship, setConnectingRelationship, savedNetwork,
    recipeNotice, setRecipeNotice, setRelationshipEdit, actionsFor: sourceRecipeActionsFor } = useSourceRecipe();
  const { activeConstructionFamily, setActiveConstructionFamily, setActiveConstructionSelection,
    reshapeEntryKind, setReshapeEntryKind, reshapeFormKind, setReshapeFormKind, actionsFor: constructionNavigationActionsFor } = useConstructionNavigation({
    ownerKey, state, draftAccess,
  });
  const occurrencePanels = useRef<{ key: string; panels: Map<string, OccurrencePanel> } | undefined>(undefined);
  const [rowValuePolicy, setRowValuePolicy] = useState<NonNullable<ConstructionChoiceSelection['rowValuePolicy']>>('ALL');
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
  const combineCatalog = useCombineCatalog({
    ownerKey,
    explorerId: state.explorerId,
    projectId,
    authResourcePath,
    state,
    draftAccess,
    loomClient,
  });
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
    draftAccess,
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
    draftAccess,
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
    draftAccess,
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
    draftAccess,
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
    draftAccess,
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
    const recipeState = recipeOwner.getState();
    if (recipeState.status === 'saved') return true;
    const candidate = recipeState.candidate;
    const rootAssessment = rootRecipeAssessment.current;
    if (candidate && recipeState.target?.kind === 'root' && rootAssessment && commands.length === 1 &&
      commands[0].type === 'APPLY_TABLE_ROOT_REBASE' && commands[0].outputId === recipeState.identity.outputId &&
      rootAssessment.candidateReceiptId === candidate.receiptId &&
      JSON.stringify(commands[0].rowChange) === JSON.stringify(rootAssessment.proposal)) return true;
    if (candidate && commands.length === 1 && commands[0].outputId === recipeState.identity.outputId &&
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
    if (recipeOwner.getState().status === 'saved' && rootChangeStateRef.current.kind === 'idle' && !pendingRowChangePreview) return true;
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
    draftAccess,
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
  const { excludePopulationMember, populationVariantError, populationVariantPending } = usePopulationExclusion({
    table, draftAccess, activePopulationSelection, populationSelectionContextKey, loomClient,
    workspaceAttached, serverDraft, applyCommands, projectId, authResourcePath,
    setPopulationSelectionOverride,
  });
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
  const graphOccurrences = useMemo(
    () => table ? authoredTraversal(table, state.catalog) : [],
    [state.catalog, table],
  );
  const graphOccurrence = graphOccurrences.find((candidate) => candidate.id === state.selectedOccurrenceId) ?? graphOccurrences.at(0);
  const edgesToGraphOccurrence = (occurrence: typeof graphOccurrence) => {
    const edgeIds: string[] = [];
    let current = occurrence;
    while (current?.parentId && current.edgeId) {
      edgeIds.unshift(current.edgeId);
      const parentId = current.parentId;
      current = graphOccurrences.find((candidate) => candidate.id === parentId);
    }
    return edgeIds;
  };
  const graphEdgeIds = edgesToGraphOccurrence(graphOccurrence);
  const [relatedGraphSelection, setRelatedGraphSelection] = useState<string>();
  const addColumnsCapabilities = constructionLifecycle.capabilities;
  const addColumnsStage = addColumnsCapabilities.status === 'ready' ? addColumnsCapabilities.response.selectedStage : undefined;
  const relatedRecord = activeConstructionFamily === 'ADD_COLUMNS' && addColumnsStage &&
    addColumnsCapabilities.status === 'ready' && !hasUnsupportedSavedSourceColumns && !editingConstructionStep &&
    addColumnsStage.id === constructionAppendStageFor(addColumnsCapabilities.response.baseConstruction, table?.document.rows) &&
    addColumnsStage.capabilities.some((capability) => capability.kind === 'RELATED_FIELD' && capability.supported)
    ? addColumnsStage.activeRelatedRecord : undefined;
  const relatedGraphSource = relatedRecord && addColumnsStage ? {
    occurrenceId: `related-record:${ownerKey}:${state.draftDigest}:${addColumnsStage.id}`,
    nodeId: relatedRecord.targetNodeId,
    resourceType: relatedRecord.targetResourceType,
    isRoot: false,
    exactRelated: true,
  } : undefined;
  const selectedRelatedGraphSource = relatedGraphSource?.occurrenceId === relatedGraphSelection ? relatedGraphSource : undefined;
  const graphSource = selectedRelatedGraphSource ?? (graphOccurrence ? {
    occurrenceId: graphOccurrence.id,
    nodeId: graphOccurrence.catalogNodeId,
    resourceType: graphOccurrence.resourceType,
    isRoot: graphOccurrence.depth === 0,
    edgeIds: graphEdgeIds,
  } : undefined);
  const fieldPresentationContext: Omit<FieldPresentationRequest, 'candidateIds'> | undefined =
    table && graphOccurrence?.catalogNodeId && state.draftVersion > 0 && state.draftDigest
      ? {
          expectedDraftVersion: state.draftVersion,
          expectedDraftDigest: state.draftDigest,
          sourceNodeId: graphOccurrence.catalogNodeId,
          occurrenceId: graphOccurrence.id,
          ...(constructionLifecycle.capabilities.status === 'ready'
            ? { stageId: constructionLifecycle.capabilities.response.selectedStage.id }
            : {}),
        }
      : undefined;
  const sourceStageDescriptors = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.stages
    : undefined;
  const {
    panel: addColumnsPanel,
    restorePanel: restoreAddColumnsPanel,
    addSourceRouteContext,
    addColumnsView,
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
    graphSource,
    constructionLifecycle,
    editingConstructionStep,
    activeConstructionFamily,
    ownerKey,
    projectId,
    authResourcePath,
    previewLimit,
    draftAccess,
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
  const {
    suggestionsFailure, setSuggestionsFailure, suggestionRequestKey,
    ensureSuggestions, suggestionHostRef, suggestionIdentity,
  } = useSuggestions({ state, draftAccess, dispatch, getSuggestions, projectId, authResourcePath });
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
  const { addTable, duplicateTable, renameTable, renameTableById, deleteSelectedTable,
    createCustomExplorer, deleteCurrentExplorer, createStatus, deleteStatus } = useTableExplorerCrud({
    state, table, draftAccess, dispatch, recipeMutationAllowed, firstTableActionPending,
    tableCreatorOpen, setFirstTableName, setTableCreatorOpen, rememberSelectedTable, applyCommands,
    projectId, authResourcePath, selectExplorer, onExplorerChange, explorers, capabilities,
    effectiveExplorerId, compileGeneration, activeCompile, setMessage, opaqueId,
  });
  const { createFirstTableFromRoot } = firstTableActionsFor({ applyCommandsWithResult });

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
        !explorerSummaries.some(
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
        const current = draftAccess.getState();
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

  const { startCombine, selectConstructionFamily, chooseRelatedRows, chooseReshapeRows,
    selectConstructionHistory, editConstructionStep, closeConstructionWorkspace: closeConstructionNavigation, cancelConstructionEditor,
    switchRowIntent, closeRowIntentSession, removeConstructionStep, restorePreviousDraft,
    activeOperation, activeReshapeEditorKey, activeReshapeInitialEntry, activeReshapeInitialKind,
    showReshapeColumnSelection, hideGenericReshapeSelection } = constructionNavigationActionsFor({
    table, construction, constructionLifecycle, combineCatalog, setConstructionHistorySelection,
    setEditingConstructionStepId, setRowIntentSession, setFeatureMode, setMessage, applyCommandsWithResult,
    recipeMutationAllowed, rememberOperationFocus, selectAddColumnsView, canStartCombine,
    selectedColumnIds, recipeOwner, setRecipeNotice, rowIntentSession, setSourceRowsOpen,
    editingConstructionStepId, previousDraftRevisionId, setColumnSelection, applyCommands, editingConstructionStep
  });
  const closeConstructionWorkspace = () => {
    setRelatedGraphSelection(undefined);
    closeConstructionNavigation();
  };
  const editingRelatedSourceStep: RelatedSourceStep | undefined = editingConstructionStep?.operation.kind === 'RELATED_SOURCE'
    ? editingConstructionStep as RelatedSourceStep
    : undefined;
  const editingRelatedFieldStep = editingConstructionStep && isRelatedFieldStep(editingConstructionStep)
    ? editingConstructionStep
    : undefined;
  const { capabilityNodeOccurrences, existingSourceKeys, rowValuesSupported, groupedRowValuePolicy, sourceAvailability, capabilityStage, constructionCapabilitiesResponse, capabilityIsForAppendStage, calculateUnavailableReason, reshapeRowsAvailability, relatedRowsAvailability, relatedSourceAvailability, sourceSelectionDisabledReason } = useReshapeAvailability({
    constructionLifecycle, sourceStageDescriptors, rowValuePolicy, setRowValuePolicy,
    hasUnsupportedSavedSourceColumns, table, previewIsCurrent, capabilitiesRequest,
    editingConstructionStep, pendingCommands, state
  });
  const sourceColumns = sourceStageDescriptors?.find(
    (stage) => stage.id === constructionSourceStageId,
  )?.columns ?? (table?.document.columns ?? []).map((column) => ({
    id: column.column,
    name: column.column,
    label: column.label,
    type: column.logicalType,
  }));
  const rowMeaning = constructionRowMeaning(sourceRowMeaning, table?.document.construction, sourceColumns);
  const { relationshipOwner, loadRecipeNetwork, pendingRelationship, proposeRelationship, cancelRecipeRelationship, sourceReviewStatus, beginSourceReview, beginSourceRowsReview, applySourceRecipe } = sourceRecipeActionsFor({
    builderDataKey, state, table, firstTableProgress, loomClient, projectId, authResourcePath,
    completePendingRecipeFocus, recipeMutationAllowed, rememberRecipeFocus, recipeOwner,
    columnPolicyRequest, setEditingRecipeColumn, constructionLifecycle, restoreRecipeFocus,
    recipeFocusGeneration, recipeInitiator, recipeInitiatorFact, pendingRecipeFocus, applyCommands
  });
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
  const activeRuleReview = recipeOwner.state.target?.kind === 'createColumnRule' ? recipeOwner.state.target : undefined;
  const reviewingAdvancedColumns = Boolean(activeRuleReview);
  const ruleOccurrence: ColumnRuleOccurrence | undefined = table ? {
    id: graphSource && !graphSource.isRoot ? graphSource.occurrenceId : '',
    resourceType: graphSource?.resourceType ?? table.document.rootResourceType,
    label: graphOccurrence?.path ?? table.document.rootResourceType,
  } : undefined;
  const removedRuleOccurrence = Boolean(state.selectedOccurrenceId &&
    table?.document.importedSource?.relationships?.removals?.includes(state.selectedOccurrenceId));
  const ruleUnavailableReason = !table?.document.importedRecipe
    ? 'Column rules are available on imported recipe tables.'
    : removedRuleOccurrence
      ? 'The selected graph occurrence was removed.'
      : graphSource && !graphSource.isRoot && !graphSource.nodeId
        ? 'This graph occurrence is unavailable.'
        : !capabilities.data
          ? capabilities.isLoading ? 'Checking whether this Loom server can create column rules…' : 'Could not check whether this Loom server can create column rules.'
          : !capabilities.data.operations?.includes('sourceRuleCreate')
            ? 'This Loom server version can’t add new column rules. Imported rules are available under Saved rules.'
            : undefined;
  const ruleInventoryScope = {
    project: projectId, explorerId: state.explorerId, authResourcePath,
    snapshotToken: state.catalog.snapshotToken,
    rowRoot: table?.document.rootResourceType ?? '',
    resourceType: ruleOccurrence?.resourceType ?? '',
  };
  const ruleFieldPaths = Array.from(new Set((state.catalog.candidates ?? [])
    .filter(candidate => candidate.nodeId === graphSource?.nodeId && candidate.populated !== false && candidate.fieldPath)
    .map(candidate => ruleOccurrence && candidate.fieldPath.startsWith(`${ruleOccurrence.resourceType}.`)
      ? candidate.fieldPath.slice(ruleOccurrence.resourceType.length + 1) : candidate.fieldPath))).sort();
  const advancedOccurrenceId = ruleOccurrence?.id || table?.document.route.occurrenceId;
  const advancedNetwork = table && savedNetwork?.owner === relationshipOwner &&
    savedNetwork.result.network.outputId === table.outputId ? savedNetwork.result.network : undefined;
  const importedSourceSteps = table?.document.importedSource?.steps ?? [];
  const advancedRules = importedSourceSteps.flatMap((step) => {
    if (!['DYNAMIC_COLUMN', 'EXTENSION_COLUMN', 'CATALOG_PROJECTION', 'PIVOT'].includes(step.kind)) return [];
    const fields = (advancedNetwork?.projectedFields ?? []).filter((field) =>
      field.owner === 'PINNED_SOURCE' && field.sourceStepId === step.id && field.occurrenceId === advancedOccurrenceId,
    );
    const parentOccurrence = step.parentId
      ? advancedNetwork?.occurrences.find((item) => item.sourceStepId === step.parentId)?.occurrenceId
        ?? importedSourceSteps.find((item) => item.id === step.parentId)?.occurrenceId
      : table?.document.route.occurrenceId;
    return parentOccurrence === advancedOccurrenceId || fields.length ? [{ step, fields }] : [];
  });
  const unassignedFrameIds = new Set<string>();
  const savedFramesForNode = (table?.document.frames ?? []).filter((frame) => {
    if (!graphSource || selectedRelatedGraphSource) return false;
    const owners = graphOccurrences.filter((occurrence) => {
      if (occurrence.resourceType !== frame.source.resourceType) return false;
      const edgeIds = edgesToGraphOccurrence(occurrence);
      return edgeIds.length === frame.route.length && frame.route.every((step, index) => step.edgeId === edgeIds[index]);
    });
    if (owners.length === 1) return owners[0].id === graphSource.occurrenceId;
    if (!graphSource.isRoot) return false;
    unassignedFrameIds.add(frame.id);
    return true;
  });
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
      graphSource={graphSource}
      project={projectId}
      authResourcePath={authResourcePath}
      addSourceRouteContext={addSourceRouteContext}
      addColumnsView={addColumnsView}
      reviewingAdvancedColumns={reviewingAdvancedColumns}
      onBackFromColumnRuleReview={() => cancelRecipeRelationship(false)}
      columnRulesUnavailableReason={ruleUnavailableReason}
      columnRuleInventoryScope={ruleInventoryScope}
      columnRuleFieldPaths={ruleFieldPaths}
      advancedView={advancedView}
      onAdvancedViewChange={(view) => { setPendingSavedRuleRemoval(undefined); setAdvancedView(view); }}
      configuredAdvancedColumns={<section aria-label="Configured column rules" className="grid min-w-0 content-start gap-2"
        ref={(node) => { if (node && table.document.importedRecipe) loadRecipeNetwork(); }}>
        <h3 className="sr-only">Saved rules</h3>
        {advancedRules.length ? <div className="grid gap-3">
          {advancedRules.map(({ step, fields }) => (
            <section key={step.id} aria-label={step.title} className="border-b border-slate-100 pb-2">
              <div className="flex items-center justify-between gap-2 pb-1 text-xs">
                <h4 className="font-semibold">{step.title}</h4>
                {step.editable ? <button type="button" className="shrink-0 text-blue-800 underline"
                  aria-label={`Configure ${step.title}`}
                  disabled={pendingCommands > 0 || publishing || reviewingAdvancedColumns}
                  onClick={() => {
                    if (!sourceStepEditAction.current || !recipeMutationAllowed()) return;
                    advancedRuleEditorReturn.current = step.id;
                    closeConstructionWorkspace();
                    sourceStepEditAction.current(step.id);
                  }}>Configure rule</button> : null}
              </div>
              {fields.length ? <ul aria-label={`Columns from ${step.title}`} className="max-h-56 divide-y divide-slate-100 overflow-auto border-y border-slate-200 text-xs">
                {fields.map((field) => {
                  const removalKey = JSON.stringify([relationshipOwner, state.draftVersion, state.draftDigest, advancedOccurrenceId, step.id, field.columnId]);
                  const removalDisabled = pendingCommands > 0 || publishing || reviewingAdvancedColumns || state.reconciliation === 'pending';
                  return <li key={field.columnId ?? field.column} className="grid min-w-0 gap-1 px-2 py-1.5">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="min-w-0 flex-1 truncate" title={field.label}>{field.label}</span>
                      {field.columnId && field.removal === 'DISABLE_PINNED_OUTPUT' && pendingSavedRuleRemoval !== removalKey
                        ? <button type="button" className="shrink-0 text-slate-600 hover:text-slate-900"
                          aria-label={`Remove ${field.label}`} disabled={removalDisabled}
                          onClick={() => setPendingSavedRuleRemoval(removalKey)}>Remove</button> : null}
                    </div>
                    {field.columnId && field.removal === 'DISABLE_PINNED_OUTPUT' && pendingSavedRuleRemoval === removalKey ? (
                      <div className="flex flex-wrap justify-end gap-x-3 gap-y-1">
                        <button type="button" className="font-medium text-red-700 hover:underline" aria-label={`Confirm removal of ${field.label}`}
                          disabled={removalDisabled} onClick={() => {
                            setPendingSavedRuleRemoval(undefined);
                            removeProjectedField(field);
                          }}>Remove column</button>
                        <button type="button" className="text-slate-600 hover:underline"
                          onClick={() => setPendingSavedRuleRemoval(undefined)}>Cancel</button>
                      </div>
                    ) : null}
                  </li>;
                })}
              </ul> : null}
            </section>
          ))}
        </div> : savedFramesForNode.length ? null : <p className="text-xs text-slate-600">No column rules configured on this node.</p>}
        {savedFramesForNode.length ? (
          <FrameSourcePanel
            key={`${table.outputId}:${state.catalog.snapshotToken}:${graphSource?.occurrenceId}`}
            project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
            snapshotToken={state.catalog.snapshotToken} outputId={table.outputId}
            rowRoot={table.document.rootResourceType} frames={savedFramesForNode} unassignedFrameIds={unassignedFrameIds}
            columns={table.document.columns} disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
            allowNewSource={false}
            onSet={(choice, form) => applyCommands([{ type: 'SET_FRAME_SOURCE', outputId: table.outputId, frameChoiceId: choice.choiceId, form }])}
            onReplace={(frameId, choice, form) => applyCommands([{ type: 'REPLACE_FRAME_SOURCE', outputId: table.outputId, frameId, frameChoiceId: choice.choiceId, form }])}
            onRemove={(frameId) => applyCommands([{ type: 'REMOVE_FRAME_SOURCE', outputId: table.outputId, frameId }])}
            onAddSelected={addSelectedFeatures}
            onRemoveColumn={(column) => applyCommands([{ type: 'REMOVE_COLUMN', outputId: table.outputId, column }])}
          />
        ) : null}
      </section>}
      renderColumnRule={(preset, inventory, path, onClear, inline) => {
        const defaults = columnRuleDefaults[preset.id];
        const initialParameters = preset.id === 'extension' && defaults?.extension && path
          ? { extension: { ...defaults.extension, source: { select: path } } }
          : preset.id === 'catalog' && defaults?.catalog && path
            ? { catalog: { ...defaults.catalog, includePaths: [path] } }
            : defaults;
        if (!ruleOccurrence || !initialParameters) return null;
        return (
          <CreateColumnRule
            key={`${relationshipOwner}:${ruleOccurrence.id}:${preset.id}:${path ?? ''}`}
            kind={preset.kind} occurrence={ruleOccurrence} preset={preset} initialParameters={initialParameters}
            identifierSystems={inventory.systems} categorySystems={inventory.categorySystems} identifierInventoryLoading={inventory.loading}
            extensionSourcePaths={preset.id === 'extension' ? inventory.extensionPaths : undefined}
            identifierInventoryUnavailable={inventory.unavailable}
            fieldPathChoices={preset.id === 'catalog' ? ruleFieldPaths : undefined}
            reviewing={(!inline || inline.active) && activeRuleReview?.ruleKind === preset.kind && activeRuleReview.occurrenceId === ruleOccurrence.id}
            inline={inline}
            disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing || Boolean(inline?.disabled)}
            onCancel={() => { if (cancelRecipeRelationship(false) && !inline) onClear?.(); }}
            renderReview={(sourceRule, onCancel, disabled, label, automatic) => <SourceRelationshipReview
              key={JSON.stringify(sourceRule)}
              client={loomClient} project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
              snapshotToken={state.catalog.snapshotToken} draftVersion={state.draftVersion} draftDigest={state.draftDigest}
              outputId={table.outputId} disabled={disabled}
              requestControllerRef={sourceRelationshipRequest}
              sourceRule={sourceRule} sharedPreview autoPreview={automatic || (preset.id === 'catalog' && Boolean(path))} createRuleReview={{ label, active: Boolean(activeRuleReview) }}
              onPreviewStart={() => {
                if (!activeRuleReview) {
                  if (!recipeMutationAllowed()) return undefined;
                  recipeEditToken.current = recipeOwner.begin({ kind: 'createColumnRule', ruleKind: preset.kind, occurrenceId: ruleOccurrence.id });
                }
                return beginSourceReview();
              }}
              onApply={async (commands) => {
                const saved = await applySourceRecipe(commands);
                if (saved) closeConstructionWorkspace();
                return saved;
              }}
              onCancel={onCancel} />}
          />
        );
      }}
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
      activeReshapeEditorKey={activeReshapeEditorKey}
      activeReshapeInitialKind={activeReshapeInitialKind}
      activeReshapeInitialEntry={activeReshapeInitialEntry}
      onReshapeFormKindChange={setReshapeFormKind}
      relatedExpandQueryOwnerRef={relatedExpandQueryOwnerRef}
      loomClient={loomClient}
      combineCatalog={combineCatalog}
      onEditConstructionStep={editConstructionStep}
      onCancelConstructionEditor={cancelConstructionEditor}
      onClose={() => {
        if (reviewingAdvancedColumns && !cancelRecipeRelationship(false)) return;
        closeConstructionWorkspace();
      }}
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
  const { sourceCandidate, sourceReviewColumnLabels, rootCandidate, candidatePreview, generatedOutputRevision, proposalResponse, proposalPresentationTable, inspectedOccurrenceId, selectTraversalScope, editTraversalColumn, traversalTable, traversalNetwork, traversalRowIdentity, resourceTypeChoices, sourcePathChoices, fieldPathChoices, fieldKindChoices, catalogOutputsForStep, pivotOutputsForStep, traversalOperationLabels, editTraversalRows, projectedFieldsForStep, occurrenceForStep, removeProjectedField, canRemoveProjectedField, editProjectedField, renameProjectedField, editableProjectedFieldIds, projectedFieldRemovalDisabledReason } = traversalActionsFor({
    state, table, sourceProposal, relationshipOwner, recipeOwner, constructionLifecycle,
    lastAcceptedOutputPreview, pendingRelationship, cancelRecipeRelationship, dispatch,
    savedNetwork, recipeMutationAllowed, rememberRecipeFocus, recipeEditToken,
    applyCommands, pendingCommands, publishing, setRecipeNotice, sourceStepEditDirty, sourceStepEditCancelAction,
    canEditColumnPolicy: (column) => {
      if (!table || column.source.kind !== 'field') return false;
      const candidate = candidateForPreviewColumn(column);
      const occurrence = derivedOccurrences(table, state.catalog).find((item) => item.id === column.occurrenceId);
      return Boolean(candidate && occurrence && candidate.nodeId === occurrence.nodeId &&
        candidate.fieldPath === column.source.field.path && candidate.projectionModes.length > 1);
    },
    onSwitchSavedOccurrence: (occurrenceId, restoredEditor) => {
      const key = JSON.stringify([relationshipOwner, state.draftVersion, state.draftDigest]);
      if (occurrencePanels.current?.key !== key) occurrencePanels.current = { key, panels: new Map() };
      const { panels } = occurrencePanels.current;
      panels.set(state.selectedOccurrenceId, {
        family: activeConstructionFamily, addColumns: addColumnsPanel, reshapeEntryKind, reshapeFormKind, columnSelection,
      });
      const panel = panels.get(occurrenceId);
      setActiveConstructionFamily(restoredEditor ? undefined : panel?.family);
      restoreAddColumnsPanel(panel?.addColumns);
      setReshapeEntryKind(panel?.reshapeEntryKind ?? 'choose');
      setReshapeFormKind(panel?.reshapeFormKind ?? 'choose');
      setColumnSelection(panel?.columnSelection ?? { kind: 'empty' });
    },
  });
  const selectGraphOccurrence = (occurrenceId: string) => {
    if (activeConstructionFamily === 'ADD_COLUMNS' && constructionLifecycle.proposal.status === 'applying') return;
    if (occurrenceId !== graphSource?.occurrenceId && recipeOwner.getState().target?.kind === 'createColumnRule') {
      if (!cancelRecipeRelationship(false)) return;
    }
    if (relatedGraphSource?.occurrenceId === occurrenceId) {
      if (occurrenceId !== graphSource?.occurrenceId) constructionLifecycle.cancel();
      setRelatedGraphSelection(occurrenceId);
      selectAddColumnsView('fields');
      return;
    }
    setRelatedGraphSelection(undefined);
    if (activeConstructionFamily === 'ADD_COLUMNS' && occurrenceId !== graphSource?.occurrenceId &&
      table && routeNode(table.document.route, occurrenceId)) {
      constructionLifecycle.cancel();
    }
    selectTraversalScope(occurrenceId);
  };
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
  const staleEditIdentity = {
    project: state.project,
    explorerId: state.explorerId,
    outputId: table?.outputId ?? '',
    snapshotToken: state.catalog.snapshotToken,
    draftVersion: state.draftVersion,
    draftDigest: state.draftDigest,
  };
  const staleSourceEdit = recipeOwner.state.status === 'stale' &&
    recipeOwner.state.target?.kind !== 'root' &&
    (Boolean(pendingRelationship) ||
      Boolean(recipeOwner.state.token && !sameRecipeEditingIdentity(recipeOwner.state.token.identity, staleEditIdentity)));
  const sourceRecipeInspector = staleSourceEdit ? undefined
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
          disabled={pendingCommands > 0 || publishing || state.reconciliation === 'pending' || recipeOwner.state.status === 'stale'}
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
  const canInspectOccurrence = Boolean(table && recipeOwner.state.status === 'saved' && !sourceRowsOpen && !sourceRecipeInspector && !workspaceEditor &&
    routeNode(table.document.route, inspectedOccurrenceId));
  const occurrenceInspection = canInspectOccurrence && table ? (
    <>
      <div className="min-w-0" ref={setOccurrenceInspectorHost} />
    </>
  ) : undefined;
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
        catalog={state.catalog} table={proposalPresentationTable ?? table} selectedOccurrenceId={selectedRelatedGraphSource?.occurrenceId ?? inspectedOccurrenceId}
        currentRelatedRecord={relatedGraphSource ? { id: relatedGraphSource.occurrenceId, nodeId: relatedGraphSource.nodeId, resourceType: relatedGraphSource.resourceType } : undefined}
        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing || sourceRowsOpen || recipeOwner.state.status !== 'saved'}
        importedRelationshipActions={table.document.importedRecipe ? {
          onAdd: (parentOccurrenceId, edgeId) => proposeRelationship({ action: 'ADD', parentOccurrenceId, edgeId, matchMode: 'OPTIONAL' }, true),
          onChangeMatchMode: (occurrenceId, matchMode) => proposeRelationship({ action: 'MATCH', occurrenceId, matchMode }),
          onRemove: (occurrenceId) => proposeRelationship({ action: 'REMOVE', occurrenceId }),
        } : undefined}
        onDefineRow={editTraversalRows}
        onSelectOccurrence={selectGraphOccurrence}
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
            {staleSourceEdit ? <span hidden ref={(element) => {
              const staleEdit = recipeOwner.getState();
              if (!element || staleEdit.status !== 'stale' || !cancelRecipeRelationship(false)) return;
              setRecipeNotice(staleEdit.error);
              advancedRuleEditorReturn.current = undefined;
              sourceStepEditDirty.current = false;
              setActiveSourceStepId(undefined);
              setSourceRowsOpen(false);
              setActiveConstructionFamily(undefined);
              setEditingConstructionStepId(undefined);
              setRowIntentSession(false);
            }} /> : null}
            {tableCreatorOpen ? tableCreatorPanel : null}
            {table ? (
              <ConstructionWorkspace
                composition={workspaceComposition}
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
                    onSelectScope={selectGraphOccurrence} onEditSourceStep={(stepId) => sourceStepEditAction.current?.(stepId)}
                    onEditConstructionStep={editConstructionStep} onEditColumn={editTraversalColumn} onEditSourceRows={editTraversalRows}
                    onRemoveSourceStep={(stepId) => {
                      if (!recipeMutationAllowed()) return;
                      void applyCommands([{ type: 'UPDATE_IMPORTED_SOURCE_STEP', outputId: table.outputId, sourceStep: { stepId, remove: true } }]);
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
                  reviewingAdvancedColumns || (sourceRowsOpen && recipeOwner.state.status === 'saved') || pendingCommands > 0 ||
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
                        reviewInEditor={advancedRuleEditorReturn.current !== undefined}
                        editActionRef={sourceStepEditAction}
                        fieldEditActionRef={sourceFieldEditAction} navigatorOnly reviewOwnsCancel
                        outputEditActionRef={sourceOutputEditAction}
                        cancelEditActionRef={sourceStepEditCancelAction}
                        switchOccurrenceActionRef={sourceStepSwitchOccurrenceAction}
                        occurrenceForStep={occurrenceForStep}
                        projectedFieldsForStep={projectedFieldsForStep}
                        onRemoveProjectedField={removeProjectedField}
                        canRemoveProjectedField={canRemoveProjectedField}
                        onEditProjectedField={editProjectedField}
                        onRenameProjectedField={renameProjectedField}
                        editableProjectedFieldIds={editableProjectedFieldIds}
                        projectedFieldRemovalDisabledReason={projectedFieldRemovalDisabledReason}
                        resourceTypeChoices={resourceTypeChoices}
                        sourcePathChoices={sourcePathChoices}
                        fieldPathChoices={fieldPathChoices}
                        fieldKindChoices={fieldKindChoices}
                        catalogOutputsForStep={catalogOutputsForStep}
                        pivotOutputsForStep={pivotOutputsForStep}
                        outputRevision={generatedOutputRevision}
                        onActiveOperationChange={setActiveSourceStepId}
                        onSuspendEdit={() => {
                          if (!cancelRecipeRelationship(false)) return false;
                          advancedRuleEditorReturn.current = undefined;
                          sourceStepEditDirty.current = false;
                          setActiveSourceStepId(undefined);
                          return true;
                        }}
                        onBeginEdit={(stepId) => {
                          if (!recipeMutationAllowed()) return false;
                          if (advancedRuleEditorReturn.current !== stepId) advancedRuleEditorReturn.current = undefined;
                          constructionLifecycle.cancel();
                          setActiveConstructionFamily(undefined);
                          setEditingConstructionStepId(undefined);
                          setRowIntentSession(false);
                          rememberRecipeFocus();
                          const token = recipeOwner.begin({ kind: 'sourceStep', sourceStepId: stepId });
                          sourceStepEditDirty.current = false;
                          recipeEditToken.current = token;
                          if (!token) return false;
                          setActiveSourceStepId(stepId);
                          return true;
                        }}
                        onCancelEdit={() => {
                          sourceStepEditDirty.current = false;
                          setActiveSourceStepId(undefined);
                          if (recipeEditToken.current) recipeOwner.cancel(recipeEditToken.current);
                          recipeEditToken.current = undefined;
                          const returnToAdvanced = advancedRuleEditorReturn.current !== undefined;
                          advancedRuleEditorReturn.current = undefined;
                          cancelRecipeRelationship();
                          if (returnToAdvanced) {
                            setActiveConstructionFamily('ADD_COLUMNS');
                            selectAddColumnsView('advanced');
                          }
                        }}
                        onDraftChange={() => {
                          sourceStepEditDirty.current = true;
                          if (recipeEditToken.current) recipeEditToken.current = recipeOwner.changed(recipeEditToken.current);
                          setSourceProposal(undefined);
                        }}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                        renderReview={(sourceStep, onCancel, sourceSteps) => <SourceRelationshipReview
                          columnLabels={sourceReviewColumnLabels}
                          ruleEdit={advancedRuleEditorReturn.current !== undefined}
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
                  workspaceCandidatePreview ? (
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
                  )
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
