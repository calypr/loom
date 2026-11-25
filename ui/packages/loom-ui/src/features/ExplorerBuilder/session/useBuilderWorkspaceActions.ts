import { useResolveConfiguredColumnContextsQuery, useResolvePopulationSelectionQuery } from '../../../data/builderQueries';
import { useWorkspaceUi, useWorkspaceUiActions } from './useWorkspaceUi';
import type { DatasetReviewTarget } from '../model/datasetReviewSelection';
import { useWorkspaceSelection } from './useWorkspaceSelection';
import { useCallback, useMemo, useRef, useState } from 'react';
import { useAutomaticPreview } from './useAutomaticPreviewResource';
import { useAutomaticPreviewActions } from './useAutomaticPreviewActions';
import { useReconcileActions } from './useReconcileActions';
import { constructionAppendStageFor, constructionInputStageFor } from '../model/constructionStages';
import { effectiveOutputAvailability } from '../model/outputEligibility';
import { useBuilderMutation, useGetExplorerAuthoringCapabilityV2Query } from '../../../react';
import type { ConfiguredColumnContextResponse } from '../../../contracts/interpretation';
import type { SelectionRevision } from '../../../contracts/selection';
import { type ExplorerAuthoringApiError } from '../../../client/api';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCandidate, ExplorerBuilderColumn, FieldPresentationRequest } from '../../../contracts/types';
import { useRecipeEditingOwner } from './useRecipeEditingOwner';
import { authoredTraversal } from '../model/authoredTraversal';
import { rowScopeLabel } from '../model/rowScopeLabel';
import { derivedOccurrences, selectedTable, type BuilderAuthoringState } from '../model/authoring';
import { useCommandQueueActions } from './useCommandQueueActions';
import { usePublish } from './usePublish';
import { useBuilderActions, useBuilderSession } from './BuilderSessionProvider';
import { useRecipeFocus } from './useRecipeFocus';
import { useConstructionNavigationState } from './constructionNavigation';
import { useFirstTableActions } from './useFirstTableActions';
import { useTableExplorerCrud } from './useTableExplorerCrud';
import { useSuggestions } from './useSuggestions';
import { usePopulationExclusion } from './usePopulationExclusion';
import { useAddColumns } from './useAddColumns';
import { useTraversalState } from './traversal';
import { sourceRecipeOwnerKey, useSourceRecipeState } from './sourceRecipe';
import { useRootChangeActions } from './useRootChangeActions';
import { useCombineCatalog } from './useCombineCatalog';
import { type ConstructionSelectableColumn } from '../model/constructionColumnSelection';
import { useConstructionLifecycle } from './useConstructionLifecycle';
import { builderOwnerKeyFor, type useBuilderWorkspaceScope } from './builderWorkspaceScope';
type InterpretationContextState =
  | { readonly status: 'loading' }
  | { readonly status: 'error'; readonly message: string }
  | { readonly status: 'ready'; readonly response: ConfiguredColumnContextResponse };

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

export const isUnsupportedUnknownSourceProjectionDiagnostic = (
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

export function useBuilderWorkspaceActions({
  populationSelection, populationSelectionLoading, populationSelectionError, onExplorerChange,
  featureFocus, scope, focusFirstTableName,
}: {
  readonly populationSelection?: SelectionRevision;
  readonly populationSelectionLoading: boolean;
  readonly populationSelectionError?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly featureFocus?: { readonly outputId: string; readonly column: string; readonly label?: string };
  readonly scope: ReturnType<typeof useBuilderWorkspaceScope>;
  readonly focusFirstTableName: () => void;
}) {
  const {
    loomClient, projectId, authResourcePath, setSelectedExplorerId, explorers,
    effectiveExplorerId, explorerSummaries, ownerKey,
  } = scope;
  const workspaceAttached = useRef(false);
  const workspaceHostRef = useCallback((node: HTMLElement | null) => {
    workspaceAttached.current = node !== null;
  }, []);
  const {
    builderQueryArgs,
    builder,
    explorerQueryEnabled,
    builderDataKey,
    state,
    previousDraftRevisionId,
    serverDraft,
    draftAccess,
  } = useBuilderSession(draft => draft);
  const { refetchBuilder, dispatch, rememberSelectedTable, syncBuilderData } = useBuilderActions();
  const capabilities = useGetExplorerAuthoringCapabilityV2Query(builderQueryArgs);
  const [assessRowChange, rowChangeStatus] = useBuilderMutation('assessRowChange');
  const [getSuggestions, suggestionsStatus] =
    useBuilderMutation('suggestions');
  const {
    message, pendingSavedRuleRemoval, cancelingConstructionProposal, populationSelectionOverride,
    clearedPopulationSelection, featureMode, rowValuePolicy, reviewOpen, reviewFocusTarget,
    reviewFocusColumn, interpretationContextRefreshVersion,
  } = useWorkspaceUi(view => view);
  const {
    setMessage, setPendingSavedRuleRemoval, beginConstructionCancellation, finishConstructionCancellation,
    isCancelingConstructionProposal, setPopulationSelectionOverride, setClearedPopulationSelection, setFeatureMode,
    occurrencePanelsFor, setRowValuePolicy, setReviewOpen, setReviewFocusTarget, setReviewFocusColumn,
    setInterpretationContextRefreshVersion,
  } = useWorkspaceUiActions();
  const traversalState = useTraversalState();
  const { occurrenceInspectorHost, setOccurrenceInspectorHost, editingRecipeColumn, setEditingRecipeColumn,
    sourceStepInspectorHost, setSourceStepInspectorHost, activeSourceStepId, setActiveSourceStepId,
    sourceStepEditAction, sourceStepSwitchOccurrenceAction, sourceFieldEditAction, sourceOutputEditAction,
    rowSettingsOpenAction, rowSettingsCloseAction } = traversalState;
  const [sourceChangeReviewHost, setSourceChangeReviewHost] = useState<HTMLDivElement | null>(null);
  const sourceStepEditDirty = useRef(false);
  const advancedRuleEditorReturn = useRef<string | undefined>(undefined);
  const sourceStepEditCancelAction = useRef<(() => void) | undefined>(undefined);
  const {
    firstTableName, setFirstTableName, tableCreatorOpen, setTableCreatorOpen,
    firstTableProgress, isFirstTableActionPending, createFirstTableFromRoot,
  } = useFirstTableActions({ getSuggestions, setMessage });
  const sourceRecipeState = useSourceRecipeState();
  const { sourceRelationshipRequest, recipeEditToken, sourceProposal, setSourceProposal,
    lastAcceptedOutputPreview, connectingRelationship, setConnectingRelationship, savedNetwork,
    recipeNotice, setRecipeNotice, setRelationshipEdit } = sourceRecipeState;
  const navigationState = useConstructionNavigationState({
    ownerKey, state, draftAccess,
  });
  const { activeConstructionFamily, setActiveConstructionFamily, setActiveConstructionSelection,
    reshapeEntryKind, setReshapeEntryKind, reshapeFormKind, setReshapeFormKind } = navigationState;
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
    invalidateCompileAndPreview();
    if (onExplorerChange) {
      onExplorerChange(nextExplorerId);
    } else {
      setSelectedExplorerId(nextExplorerId);
    }
  };

  const incomplete = state.tables.some(
    (table) => !table.document.rootResourceType,
  );

  const reconcile = useReconcileActions({
    workspaceAttached,
    setMessage,
    diagnosticsFromError,
  });

  const { compileRequests, previewRequests, invalidateCompileAndPreview, reconcileStatus, previewStatus, reconcileCurrent, executePreview } = reconcile;
  const automaticPreview = useAutomaticPreviewActions(ownerKey, {
    compileRequests,
    previewRequests,
    setMessage,
  });
  const { previewLimit, setPreviewLimit, appliedChoicePreview, automaticPreviewGeneration, automaticPreviewRequest, cancelAutomaticPreview, queryOptionsFor } = automaticPreview;
  const { pendingCommands, commandRecipeAdmission, relatedExpandQueryOwnerRef, applyCommandsWithResult, applyCommands } = useCommandQueueActions({
    reconcile,
    automaticPreview,
    builderOwnerKeyFor,
    diagnosticsFromError,
    setActiveConstructionSelection,
    setMessage,
  });


  const [sourceRowsInspectorHost, setSourceRowsInspectorHost] = useState<HTMLElement | null>(null);
  const table = selectedTable(state);
  const {
    rowIntentSession, sourceRowsOpen, constructionHistorySelection, editingConstructionStepId, columnSelection,
    setRowIntentSession, setSourceRowsOpen, setConstructionHistorySelection, setEditingConstructionStepId, setColumnSelection,
  } = useWorkspaceSelection(ownerKey, state.explorerId, table?.outputId);
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
  } = useRootChangeActions({
    workspaceAttached,
    assessRowChange,
    loomClient,
    previewLimit,
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
            lastAcceptedOutputPreview.current = { owner: sourceRecipeOwnerKey(builderDataKey, state, table?.outputId), outputId: attempt.identity.outputId,
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
    workspaceAttached,
    compileGeneration: compileRequests.generation,
    reconcileCurrent,
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
  const currentPreviewStatus: 'empty' | 'ready' | 'stale' = !tablePreview
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
      focusFirstTableName();
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
  }, [dispatch, selectTableFromUser, focusFirstTableName]);
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
    activePopulationSelection, populationSelectionContextKey, loomClient,
    workspaceAttached, applyCommands, setPopulationSelectionOverride,
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
    isCancelingConstructionProposal,
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
  } = useSuggestions({ getSuggestions,  });
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
    recipeMutationAllowed, isFirstTableActionPending,
    tableCreatorOpen, setFirstTableName, setTableCreatorOpen, applyCommands,
    selectExplorer, onExplorerChange, explorers, capabilities,
    effectiveExplorerId, invalidateCompile: compileRequests.invalidate, setMessage, opaqueId,
  });

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

  return {
    explorers, explorerQueryEnabled, builder, workspaceHostRef, emptyExplorerProject, message,
    createStatus, createCustomExplorer, refetchBuilder, explorerSummaries, effectiveExplorerId,
    selectExplorer, capabilities, deleteCurrentExplorer, state, selectTableFromUser, renameTable,
    addTable, duplicateTable, deleteSelectedTable, reviewOpen, setReviewOpen, draftAccess,
    reconcileCurrent, publish, publishDisabled, publishing, busy, featureMode, tableToolbarHost,
    navigationState, table, construction, constructionLifecycle, combineCatalog,
    setConstructionHistorySelection, setEditingConstructionStepId, setRowIntentSession,
    setFeatureMode, setMessage, applyCommandsWithResult, recipeMutationAllowed,
    rememberOperationFocus, selectAddColumnsView, canStartCombine, selectedColumnIds, recipeOwner,
    setRecipeNotice, rowIntentSession, setSourceRowsOpen, editingConstructionStepId,
    previousDraftRevisionId, setColumnSelection, applyCommands, editingConstructionStep,
    setRelatedGraphSelection, sourceStageDescriptors, rowValuePolicy, setRowValuePolicy,
    hasUnsupportedSavedSourceColumns, previewIsCurrent, capabilitiesRequest, pendingCommands,
    sourceRowMeaning, sourceRecipeState, builderDataKey, firstTableProgress, loomClient, projectId,
    authResourcePath, completePendingRecipeFocus, rememberRecipeFocus, setEditingRecipeColumn,
    restoreRecipeFocus, recipeFocusGeneration, recipeInitiator, recipeInitiatorFact,
    pendingRecipeFocus, activePopulationSelection, cancelingConstructionProposal, graphSource,
    graphOccurrence, savedNetwork, selectedRelatedGraphSource, graphOccurrences,
    edgesToGraphOccurrence, selectableColumns, currentStage, activeConstructionFamily,
    toggleSelectedColumn, addSourceRouteContext, addColumnsView, sourceStepEditAction,
    advancedRuleEditorReturn, pendingSavedRuleRemoval, setPendingSavedRuleRemoval,
    addSelectedFeatures, sourceRelationshipRequest, recipeEditToken, addColumnsViewRevision,
    fieldPresentationContext, previewRelatedSourceColumns, inspectRelatedRouteCoverage,
    setReshapeFormKind, relatedExpandQueryOwnerRef, ownerKey, setActiveConstructionFamily,
    tablePreview, applyConstructionProposal, beginConstructionCancellation,
    finishConstructionCancellation, traversalState, sourceProposal, lastAcceptedOutputPreview,
    dispatch, sourceStepEditDirty, sourceStepEditCancelAction, candidateForPreviewColumn,
    occurrencePanelsFor, addColumnsPanel, reshapeEntryKind, reshapeFormKind, columnSelection,
    restoreAddColumnsPanel, setReshapeEntryKind, relatedGraphSource, connectingRelationship,
    setRootReplacementInspectorHost, rootChangeState, rootRecipeAssessment, applyRowChangePreview,
    cancelRootChange, editingRecipeColumn, setSourceStepInspectorHost, setSourceProposal,
    sourceRowsOpen, setOccurrenceInspectorHost, recipeNotice, occurrenceInspectorHost,
    rowSettingsOpenAction, setTableToolbarHost, selectRootChoice, setSourceRowsInspectorHost,
    automaticPreviewQuery, currentPreviewStatus, firstTableName, setFirstTableName,
    createFirstTableFromRoot, setTableCreatorOpen, pendingRowChangePreview, currentRootChangeIdentity,
    focusDatasetReviewTarget, blockingDiagnostics, primaryDiagnostic, unsupportedColumns,
    unsupportedSourceProjectionDiagnostic, incomplete, suggestionsFailure, suggestionIdentity,
    suggestionRequestKey, setSuggestionsFailure, ensureSuggestions, suggestionHostRef,
    setActiveSourceStepId, tableCreatorOpen, activeSourceStepId, addColumnsReason, renameTableById,
    sourceRowsInspectorHost, rowSettingsCloseAction, rootReplacementInspectorHost, rootRecipeActions,
    rootChangeIdentityForTable, refreshRootChange, activePopulationSelectionLoading,
    activePopulationSelectionError, populationVariantPending, setClearedPopulationSelection,
    populationSelectionContextKey, excludePopulationMember, resolveRootChange,
    constructionHistorySelection, sourceStepInspectorHost, sourceChangeReviewHost,
    sourceFieldEditAction, sourceOutputEditAction, sourceStepSwitchOccurrenceAction,
    automaticPreviewFailureMessage, previewLimit, setPreviewLimit, setSourceChangeReviewHost,
    reviewFocusTarget,
  };
}
