import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useAutomaticPreview, type AutomaticPreviewIdentity, type AutomaticPreviewRun } from './useAutomaticPreview';
import { constructionAppendStageFor, constructionInputStageFor } from './constructionWorkspace/constructionStages';
import { effectiveOutputAvailability } from './constructionWorkspace/outputEligibility';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useAssessExplorerRowChangeMutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useGetExplorerBuilderStateV2Query,
  useResolveConfiguredColumnContextsQuery,
  useResolvePopulationSelectionQuery,
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
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
  ConstructionRouteStep,
  Construction,
  ConstructionOperation,
  ConstructionStep,
  ConstructionChoiceSelection,
  ConstructionChoiceProposalResponse,
  RowChangeAssessment,
  RowChangeUnresolvedReference,
} from '../../types';
import { BuilderToolbar } from './components/BuilderToolbar';
import {
  DatasetReviewPanel,
  type DatasetReviewTarget,
} from './components/DatasetReviewPanel';
import { GuidedGraphWorkspace } from './components/GuidedGraphWorkspace';
import { ColumnSelector } from './components/ColumnSelector';
import { ConceptCatalog } from './components/ConceptCatalog';
import { RowRootPicker } from './components/RowRootPicker';
import {
  PreviewTable,
  type PreviewTablePresentationChange,
} from './components/PreviewTable';
import { DataframeContractPanel } from './components/DataframeContractPanel';
import { PopulationPanel } from './components/PopulationPanel';
import { RowChangeRepairPanel } from './components/RowChangeRepairPanel';
import { RowChangePreviewPanel } from './components/RowChangePreviewPanel';
import { RowDefinitionPanel } from './components/RowDefinitionPanel';
import { RowDefinitionSettingsPanel, rowScopeLabel } from './components/RowDefinitionSettingsPanel';
import { FrameSourcePanel } from './constructionWorkspace/FrameSourcePanel';
import { SourceCollectionExpandEditor } from './constructionOperations/SourceCollectionExpandEditor';
import { TableShapeSettingsPanel } from './components/TableShapeSettingsPanel';
import { InterpretationPanel, type InterpretationContextState } from './components/InterpretationPanel';
import {
  derivedOccurrences,
  intentFingerprint,
  routeNode,
  routeSubtreeOccurrenceIds,
  selectedTable,
  stateFromBuilder,
  workspaceFromState,
  type BuilderAuthoringState,
} from './authoring/model';
import { builderAuthoringReducer } from './authoring/reducer';
import {
  planInitialTable,
  type InitialTableCandidateEvidence,
  type InitialTablePlan,
} from './authoring/initialTablePlan';
import {
  isPreviewResponseSizeError,
  previewRecoveryAction,
  type PreviewLimit,
} from './authoring/previewRecovery';
import { useDirtyBeforeUnload } from './hooks/useDirtyBeforeUnload';
import { usePortalHost } from './hooks/usePortalHost';
import { sameConstructionRoute } from './populationRoutes';
import { catalogSourceOptions, type CatalogChoiceIntent } from './catalogItems';

import {
  ConstructionWorkspace,
  ConstructionUndoButton,
  constructionOperationFamilies,
  type ConstructionHistorySelection,
  type ConstructionOperationFamily,
} from './constructionWorkspace/ConstructionWorkspace';
import {
  ConstructionColumnSelection,
  type ConstructionSelectableColumn,
} from './constructionWorkspace/ConstructionColumnSelection';
import {
  PairedColumnSuggestions,
  type PairedColumnSuggestion,
} from './constructionWorkspace/PairedColumnSuggestions';
import {
  ConstructionProposalPanel,
} from './constructionWorkspace/ConstructionProposalPanel';
import { ConstructionProposalPreview } from './constructionWorkspace/ConstructionProposalPreview';
import { PreviewValueCoverage } from './constructionWorkspace/PreviewValueCoverage';
import {
  constructionHistorySteps,
  constructionRowMeaning,
} from './constructionWorkspace/constructionHistory';
import {
  sourceProjectionAvailability,
} from './constructionWorkspace/sourceProjectionAvailability';
import { relatedSourceOutputLabel } from './constructionWorkspace/relatedSourceOutputLabel';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
} from './constructionWorkspace/useConstructionLifecycle';
import {
  matchesAcceptedChoicePreview,
  type AppliedChoicePreview,
} from './constructionWorkspace/appliedChoicePreview';
import { ConstructionOperationEditor } from './constructionOperations/ConstructionOperationEditor';
import { FilterRowsEditor } from './constructionOperations/FilterRowsEditor';
import { ConstructionReshapeEditor, createReshapeEditorEntry, hasPublicScalarListColumn, type ConstructionReshapeEditorEntry, type ReshapeEntryKind } from './constructionOperations/ConstructionReshapeEditor';
import {
  RelatedSourceStepEditor,
  type RelatedSourceStep,
} from './constructionOperations/RelatedSourceStepEditor';
import { RelatedFieldEditor, type RelatedFieldStep } from './constructionOperations/RelatedFieldEditor';
import type { RelatedExpandQueryOwner } from './constructionOperations/RelatedExpandEditor';

const previewPresentationCommand = (
  outputId: string,
  change: PreviewTablePresentationChange,
): ExplorerBuilderCommand => {
  if (change.kind === 'AUTHORED_COLUMN') {
    return {
      type: 'UPDATE_COLUMN',
      outputId,
      column: change.column.column,
      columnValue: change.column,
    };
  }
  return {
    type: 'UPDATE_CONSTRUCTION_OUTPUT',
    outputId,
    constructionOutput: {
      stepId: change.stepId,
      columnId: change.column.id,
      label: change.column.label,
      table: change.column.table,
    },
  };
};

const isRelatedFieldStep = (step: ConstructionStep): step is RelatedFieldStep =>
  step.operation.kind === 'RELATED_FIELD';

type ChoiceProposalState =
  | { readonly status: 'idle' }
  | { readonly status: 'previewing' }
  | {
      readonly status: 'ready';
      readonly selections: ReadonlyArray<CatalogChoiceIntent>;
      readonly ownerKey: string;
      readonly limit: PreviewLimit;
      readonly response: ConstructionChoiceProposalResponse;
    }
  | { readonly status: 'error'; readonly message: string };

const emptyCatalog = (): ExplorerBuilderCatalog => ({
  snapshotToken: '',
  generation: '',
  routePolicy: {},
  nodes: [],
  edges: [],
  candidates: [],
});
const emptyBuilderState = (project: string): BuilderAuthoringState => ({
  project,
  explorerId: 'default',
  catalog: emptyCatalog(),
  workspace: null,
  draftVersion: 0,
  draftDigest: '',
  previewRequestVersion: 0,
  tables: [],
  selectedOccurrenceId: 'base',
  diagnostics: [],
  dirty: false,
  reconciliation: 'idle',
});

type ColumnSelectionState =
  | { readonly kind: 'empty' }
  | {
      readonly kind: 'selected';
      readonly outputId: string;
      readonly columnIds: ReadonlyArray<string>;
    };

const diagnosticsFromError = (
  error: unknown,
): ReadonlyArray<ExplorerAuthoringDiagnostic> => {
  const value = error as ExplorerAuthoringApiError | undefined;
  if (value?.diagnostics?.length) return value.diagnostics;
  return [
    {
      severity: 'error',
      code: value?.code ?? 'EXPLORER_AUTHORING_FAILED',
      message: value?.message ?? 'Loom could not process the Builder request.',
      requestId: value?.requestId,
    },
  ];
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
const isStaleSnapshot = (code: string | undefined) =>
  [
    'STALE_CATALOG_SNAPSHOT',
    'STALE_SNAPSHOT',
    'SNAPSHOT_STALE',
    'STALE_RECEIPT',
    'RECEIPT_STALE',
    'COMPILE_RECEIPT_NOT_FOUND',
    'RECEIPT_RECOMPILE_REQUIRED',
  ].includes(code ?? '');
const isDraftDesynchronized = (code: string | undefined) =>
  ['DRAFT_CONFLICT', 'INVALID_EXPLORER_COMMAND_RESULT'].includes(code ?? '');
const opaqueId = (prefix: 'output' | 'tab' | 'step') =>
  `${prefix}-${window.crypto.randomUUID()}`;
const constructionSourceStageId = 'source_projection';

const editableConstructionFamily = (
  operation: ConstructionOperation,
): Extract<ConstructionOperationFamily, 'KEEP_ROWS' | 'CALCULATE' | 'RESHAPE'> | undefined => {
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
    case 'COMBINE': return undefined;
    default: {
      const exhaustive: never = operation;
      return exhaustive;
    }
  }
};

type PreviewRequest = {
  readonly outputId: string;
  readonly limit: PreviewLimit;
  readonly receiptRefreshes: number;
};

type AutomaticPreviewExecution = {
  cancelled: boolean;
  compileGeneration?: number;
  compileRequest?: { readonly abort: () => void };
  previewGeneration?: number;
  previewRequest?: { readonly abort: () => void };
};

type RowChangeResolution = {
  readonly rootOccurrenceId?: string;
  readonly routeRebase?: ReadonlyArray<{
    readonly occurrenceId: string;
    readonly edgeId: string;
  }>;
};

type PendingRowChange = {
  readonly nodeId: string;
  readonly resolution: RowChangeResolution;
  readonly assessment: Extract<RowChangeAssessment, { readonly status: 'BLOCKED' }>;
};

type RowChangePreviewContext = {
  readonly assessment: Extract<RowChangeAssessment, { readonly status: 'READY' }>;
  readonly outputId: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
};

type PendingRowChangePreview =
  | (RowChangePreviewContext & { readonly status: 'loading' })
  | (RowChangePreviewContext & { readonly status: 'ready'; readonly preview: ExplorerBuilderPreviewResult })
  | (RowChangePreviewContext & { readonly status: 'error'; readonly error: string });

type FirstTableProgress =
  | { readonly kind: 'idle' }
  | {
      readonly kind: 'running';
      readonly resourceType: string;
      readonly phase: 'checking' | 'creating' | 'adding-id' | 'previewing';
    };

const builderDataKeyFor = (
  ownerKey: string,
  value: {
    readonly catalog: ExplorerBuilderCatalog;
    readonly workspace: unknown;
    readonly draftVersion: number;
    readonly draftDigest: string;
  },
) =>
  `${ownerKey}:${value.catalog.snapshotToken}:${value.draftVersion}:${value.draftDigest}:${JSON.stringify(value.workspace)}`;

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
}: {
  readonly organization?: string;
  readonly project: string;
  readonly explorerId?: string;
  readonly populationSelection?: SelectionRevision;
  readonly populationSelectionLoading?: boolean;
  readonly populationSelectionError?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
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
  const requestedExplorerId = explorerId || 'default';
  const [selectedExplorerId, setSelectedExplorerId] =
    useState(requestedExplorerId);
  const ownerKey = builderOwnerKeyFor(projectId, authResourcePath, selectedExplorerId);
  const selectedTableStorageKey = `loom.builder.selected-table:${ownerKey}`;
  const rememberedOutputId = useMemo(() => {
    try {
      return window.sessionStorage.getItem(selectedTableStorageKey) ?? undefined;
    } catch {
      return undefined;
    }
  }, [selectedTableStorageKey]);
  const explorers = useGetExplorerAuthoringExplorersQuery({
    project: projectId,
    authResourcePath,
  });
  const builder = useGetExplorerBuilderStateV2Query({
    project: projectId,
    explorerId: selectedExplorerId,
    authResourcePath,
  });
  const refetchBuilder = builder.refetch;
  const capabilities = useGetExplorerAuthoringCapabilityV2Query({
    project: projectId,
    explorerId: selectedExplorerId,
    authResourcePath,
  });
  const [createExplorer, createStatus] = useCreateExplorerAuthoringMutation();
  const [deleteExplorer, deleteStatus] = useDeleteExplorerAuthoringMutation();
  const [applyBuilderCommands] = useApplyExplorerBuilderCommandsV2Mutation();
  const [assessRowChange, rowChangeStatus] = useAssessExplorerRowChangeMutation();
  const [reconcileBuilder, reconcileStatus] =
    useReconcileExplorerBuilderV2Mutation();
  const [getSuggestions, suggestionsStatus] =
    useGetExplorerCandidateSuggestionsV2Mutation();
  const [previewBuilder, previewStatus] =
    usePreviewExplorerAuthoringV2Mutation();
  const [publishBuilder] = usePublishExplorerAuthoringV2Mutation();
  const builderDataKey = builder.data
    ? builderDataKeyFor(builderOwnerKeyFor(projectId, authResourcePath, selectedExplorerId), builder.data)
    : '';
  const builderDataRef = useRef(builder.data);
  builderDataRef.current = builder.data;
  const [localState, setLocalState] = useState<{
    readonly key: string;
    readonly value: BuilderAuthoringState;
  }>();
  const [restorableDraft, setRestorableDraft] = useState<{
    readonly ownerKey: string;
    readonly revisionId?: string;
  }>();
  const previousDraftRevisionId = restorableDraft?.ownerKey === ownerKey
    ? restorableDraft.revisionId
    : builder.data?.previousDraftRevisionId;
  const state =
    localState?.key === builderDataKey
      ? localState.value
      : builder.data
        ? stateFromBuilder(builder.data, {
            project: projectId,
            explorerId: selectedExplorerId,
            selectedOutputId: localState?.key.startsWith(`${ownerKey}:`) ? localState.value.selectedOutputId : rememberedOutputId,
          })
        : emptyBuilderState(projectId);
  const dispatch = useCallback(
    (action: Parameters<typeof builderAuthoringReducer>[1]) => {
      setLocalState((current) => {
        const base =
          current?.key === builderDataKey
            ? current.value
            : builderDataRef.current
              ? stateFromBuilder(builderDataRef.current, {
                  project: projectId,
                  explorerId: selectedExplorerId,
                  selectedOutputId: current?.key.startsWith(`${ownerKey}:`) ? current.value.selectedOutputId : rememberedOutputId,
                })
              : emptyBuilderState(projectId);
        return {
          key: builderDataKey,
          value: builderAuthoringReducer(base, action),
        };
      });
    },
    [authResourcePath, builderDataKey, ownerKey, projectId, rememberedOutputId, selectedExplorerId],
  );
  const rememberSelectedTable = useCallback((outputId: string) => {
    try {
      window.sessionStorage.setItem(selectedTableStorageKey, outputId);
    } catch {
      // Browser storage can be unavailable; table editing remains usable.
    }
  }, [selectedTableStorageKey]);
  const selectTableFromUser = useCallback((outputId: string) => {
    rememberSelectedTable(outputId);
    dispatch({ type: 'selectTable', outputId });
  }, [dispatch, rememberSelectedTable]);
  const [message, setMessage] = useState<string>();
  const [pendingRowChange, setPendingRowChange] =
    useState<PendingRowChange>();
  const [pendingRowChangePreview, setPendingRowChangePreview] =
    useState<PendingRowChangePreview>();
  const rowChangePreviewRequest = useRef<AbortController | undefined>(undefined);
  const [lastPublished, setLastPublished] = useState<{
    readonly ownerKey: string;
    readonly draftDigest: string;
  }>();
  const [pendingCommands, setPendingCommands] = useState(0);
  const [populationSelectionOverride, setPopulationSelectionOverride] = useState<{
    readonly contextKey: string;
    readonly baseSelectionID: string;
    readonly selection: SelectionRevision;
  }>();
  const [populationVariantError, setPopulationVariantError] = useState<{
    readonly contextKey: string;
    readonly message: string;
  }>();
  const [populationVariantPending, setPopulationVariantPending] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [firstTableName, setFirstTableName] = useState('');
  const [tableCreatorOpen, setTableCreatorOpen] = useState(false);
  const [firstTableProgress, setFirstTableProgress] =
    useState<FirstTableProgress>({ kind: 'idle' });
  const [previewLimit, setPreviewLimit] = useState<PreviewLimit>(25);
  const [choiceProposal, setChoiceProposal] = useState<ChoiceProposalState>({ status: 'idle' });
  const choiceProposalRequest = useRef<AbortController | undefined>(undefined);
  const appliedChoicePreview = useRef<AppliedChoicePreview | undefined>(undefined);
  const [featureMode, setFeatureMode] = useState<'catalog' | 'graph'>('catalog');
  const [sourceSetupOpen, setSourceSetupOpen] = useState(false);
  const [pairedColumnSuggestion, setPairedColumnSuggestion] =
    useState<PairedColumnSuggestion>();
  const [activeConstructionFamily, setActiveConstructionFamily] =
    useState<ConstructionOperationFamily>();
  const [addColumnsView, setAddColumnsView] = useState<'coded' | 'fields'>('coded');
  const [rowValuePolicy, setRowValuePolicy] = useState<NonNullable<ConstructionChoiceSelection['rowValuePolicy']>>('ALL');
  const [reshapeEntry, setReshapeEntry] = useState(0);
  const [reshapeEntryKind, setReshapeEntryKind] = useState<ReshapeEntryKind>('choose');
  const [reshapeInitialEntry, setReshapeInitialEntry] = useState<{
    readonly editorKey: string;
    readonly entry: ConstructionReshapeEditorEntry;
  }>();
  const [addColumnsSource, setAddColumnsSource] = useState<{
    readonly context: string;
    readonly key: string;
  }>();
  const [addSourceSearch, setAddSourceSearch] = useState<{
    readonly context: string;
    readonly query: string;
  }>({ context: '', query: '' });
  const [relatedSourceMenuOpen, setRelatedSourceMenuOpen] = useState(false);
  const [constructionHistorySelection, setConstructionHistorySelection] =
    useState<ConstructionHistorySelection>({ kind: 'source' });
  const [editingConstructionStepId, setEditingConstructionStepId] =
    useState<string>();
  const [columnSelection, setColumnSelection] =
    useState<ColumnSelectionState>({ kind: 'empty' });
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
  const compileGeneration = useRef(0);
  const previewGeneration = useRef(0);
  const activeCompile = useRef<{ abort: () => void } | undefined>(undefined);
  const activePreview = useRef<{ abort: () => void } | undefined>(undefined);
  const automaticPreviewGeneration = useRef(0);
  const activeAutomaticPreview = useRef<{ readonly generation: number; readonly execution: AutomaticPreviewExecution; readonly cancel: () => void } | undefined>(undefined);
  const automaticPreviewRequest = useRef<AutomaticPreviewIdentity | undefined>(undefined);
  const cancelAutomaticPreview = useCallback(() => {
    activeAutomaticPreview.current?.cancel();
  }, []);
  const commandQueue = useRef<Promise<void>>(Promise.resolve());
  const relatedExpandQueryOwnerRef = useRef<RelatedExpandQueryOwner | null>(null);
  const firstTableActionPending = useRef(false);
  const serverDraft = useRef({ version: 0, digest: '' });
  const suggestionRequestKey = useRef('');
  const latestState = useRef(state);
  latestState.current = state;
  const [interpretationContextRefreshVersion, setInterpretationContextRefreshVersion] = useState(0);

  const serverDraftKey = useRef('');
  if (builder.data && serverDraftKey.current !== builderDataKey) {
    serverDraft.current = {
      version: builder.data.draftVersion,
      digest: builder.data.draftDigest,
    };
    serverDraftKey.current = builderDataKey;
  }

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
      explorerId: selectedExplorerId,
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

  const syncBuilderData = useCallback(
    (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => {
      const nextKey = builderDataKeyFor(builderOwnerKeyFor(projectId, authResourcePath, selectedExplorerId), value);
      setRestorableDraft({
        ownerKey: builderOwnerKeyFor(projectId, authResourcePath, selectedExplorerId),
        revisionId: value.previousDraftRevisionId,
      });
      serverDraft.current = {
        version: value.draftVersion,
        digest: value.draftDigest,
      };
      serverDraftKey.current = nextKey;
      setLocalState((current) => {
        const nextValue =
          mode === 'hydrate'
            ? stateFromBuilder(value, {
                project: projectId,
                explorerId: selectedExplorerId,
                selectedOutputId: current?.key.startsWith(`${ownerKey}:`) ? current.value.selectedOutputId : rememberedOutputId,
              })
            : builderAuthoringReducer(
                current?.key === builderDataKey
                  ? current.value
                  : latestState.current,
                { type: 'catalogRefreshed', catalog: value.catalog },
              );
        return { key: nextKey, value: nextValue };
      });
    },
    [builderDataKey, ownerKey, projectId, rememberedOutputId, selectedExplorerId],
  );

  const incomplete = state.tables.some(
    (table) => !table.document.rootResourceType,
  );

  const applyCommandsWithResult = useCallback(
    (commands: ReadonlyArray<ExplorerBuilderCommand>, proposedCommandId?: string) => {
      cancelAutomaticPreview();
      compileGeneration.current += 1;
      previewGeneration.current += 1;
      activeCompile.current?.abort();
      activePreview.current?.abort();
      setPendingCommands((value) => value + 1);
      const run = commandQueue.current.then(async () => {
        const queryOwner = relatedExpandQueryOwnerRef.current;
        let commandSucceeded = false;
        try {
          await queryOwner?.pauseAndDrain();
          const current = latestState.current;
          const commandId = proposedCommandId ?? window.crypto.randomUUID();
          const value = await applyBuilderCommands({
            project: projectId,
            explorerId: current.explorerId,
            authResourcePath,
            commandId,
            snapshotToken: current.catalog.snapshotToken,
            expectedDraftVersion: serverDraft.current.version,
            expectedDraftDigest: serverDraft.current.digest || undefined,
            commands,
            requestId: `builder-command-${commandId}`,
          }).unwrap();
          commandSucceeded = true;
          serverDraft.current = {
            version: value.draftVersion,
            digest: value.draftDigest,
          };
          setRestorableDraft({
            ownerKey: builderOwnerKeyFor(projectId, authResourcePath, current.explorerId),
            revisionId: value.previousDraftRevisionId,
          });
          serverDraftKey.current = builderDataKey;
          const next = builderAuthoringReducer(latestState.current, {
            type: 'commandsApplied',
            value,
          });
          const selectsTableThroughCommand = commands.some((command) =>
            command.type === 'CREATE_TABLE' ||
            command.type === 'DUPLICATE_TABLE' ||
            (command.type === 'DELETE_TABLE' && command.outputId === current.selectedOutputId),
          );
          if (selectsTableThroughCommand && next.selectedOutputId) {
            rememberSelectedTable(next.selectedOutputId);
          }
          latestState.current = next;
          setLocalState({ key: builderDataKey, value: next });
          setMessage(undefined);
          return value;
        } catch (error) {
          const apiError = error as ExplorerAuthoringApiError;
          if (
            isDraftDesynchronized(apiError.code) ||
            isStaleSnapshot(apiError.code)
          ) {
            const refreshed = await refetchBuilder({ reload: true });
            if (refreshed.data) {
              syncBuilderData(refreshed.data, 'hydrate');
              setMessage(undefined);
            } else if (apiError.code !== 'CLIENT_CANCELLED') {
              dispatch({
                type: 'repair',
                diagnostics: diagnosticsFromError(apiError),
              });
            }
            return undefined;
          }
          if (apiError.code !== 'CLIENT_CANCELLED') {
            dispatch({
              type: 'repair',
              diagnostics: diagnosticsFromError(apiError),
            });
          }
          return undefined;
        } finally {
          if (!commandSucceeded &&
              relatedExpandQueryOwnerRef.current === queryOwner &&
              queryOwner &&
              serverDraft.current.version === queryOwner.draftVersion &&
              serverDraft.current.digest === queryOwner.draftDigest) {
            queryOwner.resume();
          }
          setPendingCommands((value) => Math.max(0, value - 1));
        }
      });
      commandQueue.current = run.then(() => undefined, () => undefined);
      return run;
    },
    [
      applyBuilderCommands,
      authResourcePath,
      builderDataKey,
      cancelAutomaticPreview,
      dispatch,
      projectId,
      refetchBuilder,
      rememberSelectedTable,
      syncBuilderData,
    ],
  );
  const applyCommands = useCallback(
    (commands: ReadonlyArray<ExplorerBuilderCommand>, proposedCommandId?: string) =>
      applyCommandsWithResult(commands, proposedCommandId).then(Boolean),
    [applyCommandsWithResult],
  );

  useDirtyBeforeUnload(state.dirty || pendingCommands > 0);

  const table = selectedTable(state);
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
  useEffect(() => {
    rowChangePreviewRequest.current?.abort();
    setPendingRowChangePreview(undefined);
  }, [state.explorerId, table?.outputId, state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);
  useEffect(() => {
    choiceProposalRequest.current?.abort();
    choiceProposalRequest.current = undefined;
    setChoiceProposal({ status: 'idle' });
  }, [state.explorerId, table?.outputId, state.catalog.snapshotToken, state.draftVersion, state.draftDigest]);
  const unsupportedColumns = unsupportedSavedSourceColumns(state.tables);
  const hasUnsupportedSavedSourceColumns = unsupportedColumns.length > 0;
  const construction = table?.document.construction;
  const editingConstructionStep = construction?.steps.find(
    (step) => step.id === editingConstructionStepId,
  );
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
        stageId: editingConstructionStep
          ? constructionInputStageFor(construction, editingConstructionStep.id)
          : constructionAppendStageFor(construction, table?.document.rows),
      }
    : undefined;
  const constructionLifecycle = useConstructionLifecycle({
    client: loomClient,
    capabilitiesRequest,
    previewLimit,
  });
  const constructionProposalBusy = constructionLifecycle.proposal.status === 'previewing' ||
    constructionLifecycle.proposal.status === 'applying';
  useEffect(() => {
    setActiveConstructionFamily(undefined);
    setColumnSelection({ kind: 'empty' });
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
  }, [state.explorerId, table?.outputId]);
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
  useEffect(() => {
    setPairedColumnSuggestion(undefined);
  }, [state.explorerId, state.catalog.snapshotToken, table?.outputId, table?.document.rootResourceType]);
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
  useEffect(() => {
    if (!focusedFeature || reviewFocusTarget) return;
    if (state.selectedOutputId !== focusedFeature.table.outputId) {
      dispatch({ type: 'selectTable', outputId: focusedFeature.table.outputId });
      return;
    }
    if (state.selectedOccurrenceId !== focusedFeature.column.occurrenceId) {
      dispatch({ type: 'selectOccurrence', occurrenceId: focusedFeature.column.occurrenceId });
    }
  }, [dispatch, focusedFeature, reviewFocusTarget, state.selectedOccurrenceId, state.selectedOutputId]);
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
  useEffect(() => {
    if (!reviewFocusTarget || state.selectedOutputId !== reviewFocusTarget.outputId) return;
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
  }, [reviewFocusTarget, state.selectedOutputId, state.tables]);
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
  const populationSelectionQueryArgs = !populationSelectionLoading && !handedOffPopulationSelectionID && table &&
    (attachedSelectionID || cohortRevisionID)
    ? {
      project: projectId,
      explorerId: state.explorerId,
      authResourcePath,
      snapshotToken: state.catalog.snapshotToken,
      generation: state.catalog.generation,
      outputId: table.outputId,
      resourceType: table.document.rootResourceType ?? '',
      ...(attachedSelectionID ? { attachedSelectionId: attachedSelectionID } : {}),
      ...(cohortRevisionID ? { cohortRevisionId: cohortRevisionID } : {}),
    }
    : undefined;
  const populationSelectionQuery = useResolvePopulationSelectionQuery(
    populationSelectionQueryArgs,
    JSON.stringify([populationSelectionContextKey, attachedSelectionID ?? '']),
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
  const selectedRouteContext = useMemo(() => {
    if (!occurrence) return undefined;
    return {
      occurrenceId: occurrence.id,
      nodeId: occurrence.nodeId,
    };
  }, [occurrence]);
  const addSourceOptions = useMemo(() => {
    const sources = catalogSourceOptions(
      state.catalog,
      table?.document.rootResourceType ?? '',
      selectedRouteContext?.nodeId,
    );
    const searchableSources = [{ kind: 'ALL' as const, key: 'all', label: 'All accessible resources' }, ...sources];
    const current = constructionLifecycle.capabilities;
    if (current.status !== 'ready' ||
        current.response.selectedStage.id !== constructionAppendStageFor(current.response.baseConstruction, table?.document.rows) ||
        !current.response.selectedStage.activeRelatedRecord ||
        !current.response.selectedStage.capabilities.some((capability) => capability.kind === 'RELATED_FIELD' && capability.supported)) return searchableSources;
    const anchor = current.response.selectedStage.activeRelatedRecord;
    return [{
      kind: 'EXACT_RELATED' as const,
      key: `exact:${current.response.selectedStage.id}:${anchor.targetNodeId}`,
      label: `${anchor.targetResourceType} — this row’s related record`,
      resourceType: anchor.targetResourceType,
      sourceNodeId: undefined,
    }, ...searchableSources];
  }, [state.catalog, table?.document.rootResourceType, selectedRouteContext?.nodeId, constructionLifecycle.capabilities]);
  const addSourceContext = JSON.stringify([
    state.explorerId,
    state.catalog.snapshotToken,
    table?.outputId ?? '',
    selectedRouteContext?.occurrenceId ?? '',
  ]);
  const selectedAddSource = addSourceOptions.find(
    (source) => source.key === (addColumnsSource?.context === addSourceContext
      ? addColumnsSource.key
      : addSourceOptions.find((option) => option.kind === 'EXACT_RELATED')?.key ??
        addSourceOptions.find((option) => (option.kind === 'RELATED' || option.kind === 'SAVED_OCCURRENCE') && option.sourceNodeId === selectedRouteContext?.nodeId)?.key),
  ) ?? addSourceOptions.find((option) => option.kind === 'ALL') ?? addSourceOptions[0];
  useEffect(() => {
    choiceProposalRequest.current?.abort();
    choiceProposalRequest.current = undefined;
    setChoiceProposal({ status: 'idle' });
  }, [selectedAddSource?.key, activeConstructionFamily]);
  const relatedSourceTypeCounts = addSourceOptions.reduce((counts, source) => {
    if (source.kind === 'RELATED') {
      counts.set(source.resourceType, (counts.get(source.resourceType) ?? 0) + 1);
    }
    return counts;
  }, new Map<string, number>());
  const addSourceOptionItems = addSourceOptions.map((source) => {
    const isCurrentOccurrence = source.sourceNodeId !== undefined && source.sourceNodeId === selectedRouteContext?.nodeId;
    const label = source.resourceType ?? source.label;
    const baseDescription = source.kind === 'ALL'
      ? 'Search coded concepts and fields across the dataset'
      : source.kind === 'ROOT'
      ? 'Current table rows'
      : source.kind === 'EXACT_RELATED'
        ? 'Related record for this row'
        : source.kind === 'SAVED_OCCURRENCE'
          ? 'Selected occurrence'
          : isCurrentOccurrence
            ? 'Related resource, selected occurrence'
            : 'Related resource';
    const description = source.kind === 'RELATED' && (relatedSourceTypeCounts.get(source.resourceType) ?? 0) > 1
      ? `${baseDescription} · source ${source.sourceNodeId}`
      : baseDescription;
    return {
      source,
      label,
      description,
      selected: source.key === selectedAddSource?.key,
      isSearchScope: source.kind === 'ROOT' || source.kind === 'ALL',
    };
  });
  const scopeSourceOptions = addSourceOptionItems.filter((option) => option.isSearchScope);
  const relatedSourceOptions = addSourceOptionItems.filter((option) => !option.isSearchScope);
  const addSourceSearchQuery = addSourceSearch.context === addSourceContext
    ? addSourceSearch.query.trim().toLowerCase()
    : '';
  const visibleRelatedSourceOptions = relatedSourceOptions.filter(({ source, label }) =>
    `${label} ${source.label}`.toLowerCase().includes(addSourceSearchQuery),
  );
  const addSourceRouteContext = selectedAddSource?.sourceNodeId !== undefined && selectedAddSource.sourceNodeId === selectedRouteContext?.nodeId
    ? selectedRouteContext
    : undefined;
  const buildRelatedSourceCandidate = (selection: CatalogChoiceIntent) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const relatedSource = selection.relatedSource;
    if (!relatedSource) throw new Error('Choose a related field first.');
    const capabilities = constructionLifecycle.capabilities;
    if (capabilities.status !== 'ready') {
      throw new Error('Loom is still checking whether this stage can add a related field.');
    }
    const { selectedStage, baseConstruction } = capabilities.response;
    if (
      editingConstructionStep ||
      selectedStage.id !== constructionAppendStageFor(baseConstruction, table?.document.rows)
    ) {
      throw new Error(
        'Close the saved-step editor before adding a related field; proposals must follow the current final step.',
      );
    }
    const support = selectedStage.capabilities.find(
      (capability) => capability.kind === 'RELATED_SOURCE',
    );
    if (!support?.supported || !selectedStage.rowIdentityColumn) {
      throw new Error(
        support?.reason || 'Loom has not proved that this stage retains a source row anchor.',
      );
    }
    const { choice, candidate } = relatedSource;
    const source = choice.source;
    const form = selection.constructionChoice.form;
    const contributorPredicate = selection.contributorPredicate;
    const supportedForm = choice.options.find((option) =>
      option.form === form &&
      option.support === 'SUPPORTED',
    );
    if (
      source.kind !== 'FIELD' ||
      source.candidateId !== candidate.candidateId ||
      source.nodeId !== candidate.nodeId ||
      source.path !== candidate.fieldPath ||
      source.cardinality !== candidate.cardinality ||
      (source.resourceType === table.document.rootResourceType && choice.route.length === 0) ||
      !supportedForm ||
      (form !== 'ALL' && form !== 'COUNT' && form !== 'PRESENCE') ||
      (source.cardinality !== 'optional_one' && source.cardinality !== 'required_one')
    ) {
      throw new Error('Loom did not provide a supported scalar related field choice for this stage.');
    }

    const stepId = opaqueId('step');
    const outputColumnId = `related-${window.crypto.randomUUID()}`;
    const baseName = (form === 'COUNT'
      ? `related_${source.resourceType}_count`
      : form === 'PRESENCE'
        ? `has_related_${source.resourceType}`
        : `related_${source.resourceType}_${source.path}`)
      .replace(/[^A-Za-z0-9_]/g, '_')
      .replace(/_+/g, '_')
      .replace(/^([0-9])/, '_$1');
    const usedNames = new Set(selectedStage.columns.map((column) => column.name.toLowerCase()));
    let outputName = baseName;
    for (let suffix = 2; usedNames.has(outputName.toLowerCase()); suffix += 1) {
      outputName = `${baseName}_${suffix}`;
    }
    const step = {
      id: stepId,
      inputs: [selectedStage.operation
        ? { kind: 'STEP_OUTPUT' as const, stepId: selectedStage.id }
        : { kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'RELATED_SOURCE',
        relatedSource: {
          anchorColumnId: selectedStage.rowIdentityColumn,
          choiceId: choice.choiceId,
          sourceOccurrenceId: source.nodeId,
          source: {
            kind: 'FIELD',
            candidateId: source.candidateId,
            nodeId: source.nodeId,
            resourceType: source.resourceType,
            path: source.path,
            cardinality: source.cardinality,
            logicalType: candidate.logicalType,
          },
          route: choice.route,
          contributorRule: {
            policy: 'ALL_MATCHES',
            ...(contributorPredicate
              ? { predicate: contributorPredicate }
              : {}),
          },
          form,
          outputColumnId,
        },
      },
      outputs: [
        ...selectedStage.columns.map((column) => ({
          id: column.id,
          name: column.name,
          label: column.label,
          ...(column.type === undefined ? {} : { type: column.type }),
        })),
        {
          id: outputColumnId,
          name: outputName,
          label: relatedSourceOutputLabel(
            source.resourceType, candidate.fieldPath, candidate.label, form, contributorPredicate,
          ),
          type: form === 'COUNT' ? 'integer' : form === 'PRESENCE' ? 'boolean' : candidate.logicalType,
        },
      ],
    } satisfies ConstructionStep;
    return {
      intent: {
        candidateConstruction: {
          ...baseConstruction,
          steps: [...baseConstruction.steps, step],
        },
        changedStepId: step.id,
      },
      outputColumnName: outputName,
    };
  };
  const inspectRelatedRouteCoverage = async (selection: CatalogChoiceIntent, signal: AbortSignal) => {
    const relatedCandidate = selection.relatedSource ? buildRelatedSourceCandidate(selection) : undefined;
    if (selection.constructionChoice.form === 'ALL' && !relatedCandidate) {
      const current = latestState.current;
      if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {
        throw new Error('The current table is still loading.');
      }
      const response = await loomClient.proposeConstructionChoices({
        project: projectId,
        explorerId: current.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        commandId: window.crypto.randomUUID(),
        snapshotToken: current.catalog.snapshotToken,
        expectedDraftVersion: current.draftVersion,
        expectedDraftDigest: current.draftDigest,
        outputId: table.outputId,
        constructionChoices: [{ ...selection.constructionChoice,
          ...(sourceStageDescriptors?.at(-1)?.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported) ? { rowValuePolicy: 'ALL' as const } : {}), ...(selection.title ? { title: selection.title } : {}) }],
        limit: 25,
      }, signal);
      const latest = latestState.current;
      if (
        signal.aborted ||
        response.snapshotToken !== current.catalog.snapshotToken ||
        response.draftVersion !== current.draftVersion ||
        response.draftDigest !== current.draftDigest ||
        response.outputId !== table.outputId ||
        latest.draftVersion !== current.draftVersion ||
        latest.draftDigest !== current.draftDigest ||
        selectedTable(latest)?.outputId !== table.outputId
      ) throw new Error('The table changed while Loom checked paired values.');
      const columnId = response.candidateColumnIds[0];
      const rows = response.preview.rows;
      if (response.previewStatus !== 'READY' || !columnId || !rows) {
        throw new Error('Loom could not measure paired values in this preview.');
      }
      let empty = 0;
      let one = 0;
      let many = 0;
      for (const row of rows) {
        const values = row[columnId];
        if (values === null || values === undefined) empty += 1;
        else if (Array.isArray(values)) {
          const present = values.filter((value) => value !== null && value !== undefined).length;
          if (present === 0) empty += 1;
          else if (present === 1) one += 1;
          else many += 1;
        } else throw new Error('Loom did not return a paired value list for this route.');
      }
      return {
        kind: 'VALUES' as const, empty, one, many, displayedRows: rows.length,
        sampled: response.preview.sampled !== false || response.preview.partialValidation === true,
      };
    }
    if (!relatedCandidate || (selection.constructionChoice.form !== 'COUNT' && selection.constructionChoice.form !== 'ALL')) {
      throw new Error('Route coverage is unavailable for this result form.');
    }
    const candidate = relatedCandidate;
    const current = latestState.current;
    if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest || !table) {
      throw new Error('The current table is still loading.');
    }
    const response = await loomClient.proposeConstruction({
      project: projectId,
      explorerId: current.explorerId,
      ...(authResourcePath ? { authResourcePath } : {}),
      snapshotToken: current.catalog.snapshotToken,
      expectedDraftVersion: current.draftVersion,
      expectedDraftDigest: current.draftDigest,
      outputId: table.outputId,
      ...candidate.intent,
      limit: 25,
      requestId: `construction-route-coverage-${window.crypto.randomUUID()}`,
    }, signal);
    const latest = latestState.current;
    if (
      signal.aborted ||
      response.snapshotToken !== current.catalog.snapshotToken ||
      response.draftVersion !== current.draftVersion ||
      response.draftDigest !== current.draftDigest ||
      response.outputId !== table.outputId ||
      latest.draftVersion !== current.draftVersion ||
      latest.draftDigest !== current.draftDigest ||
      selectedTable(latest)?.outputId !== table.outputId
    ) throw new Error('The table changed while Loom checked matching records.');
    const preview = response.preview;
    const rows = preview?.rows;
    if (response.previewStatus !== 'READY' || !preview || !rows ||
      !preview.columns.some((column) => column.column === candidate.outputColumnName)) {
      throw new Error('Loom could not measure matching records in this preview.');
    }
    let zero = 0;
    let one = 0;
    let many = 0;
    if (selection.constructionChoice.form === 'ALL') {
      let empty = 0;
      for (const row of rows) {
        const values = row[candidate.outputColumnName];
        if (values === null || values === undefined) empty += 1;
        else if (Array.isArray(values)) {
          const present = values.filter((value) => value !== null && value !== undefined).length;
          if (present === 0) empty += 1;
          else if (present === 1) one += 1;
          else many += 1;
        } else throw new Error('Loom did not return related values for this route.');
      }
      return {
        kind: 'VALUES' as const, empty, one, many, displayedRows: rows.length,
        sampled: preview.sampled !== false || preview.partialValidation === true,
      };
    }
    for (const row of rows) {
      const count = row[candidate.outputColumnName];
      if (typeof count !== 'number' || !Number.isInteger(count) || count < 0) {
        throw new Error('Loom did not return valid match counts for this route.');
      }
      if (count === 0) zero += 1;
      else if (count === 1) one += 1;
      else many += 1;
    }
    return {
      zero, one, many, displayedRows: rows.length,
      sampled: preview.sampled !== false || preview.partialValidation === true,
    };
  };
  const addSelectedFeatures = async (
    selections: ReadonlyArray<CatalogChoiceIntent>,
    saveImmediately = false,
  ) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const relatedSelections = selections.filter((selection) => selection.relatedSource);
    if (relatedSelections.length > 0) {
      if (relatedSelections.length !== 1 || selections.length !== 1) {
        throw new Error('Add one related field at a time so Loom can preview its exact route.');
      }
      const candidate = buildRelatedSourceCandidate(relatedSelections[0]!);
      constructionLifecycle.onCandidateChange(candidate.intent);
      return 'preview-pending' as const;
    }
    const current = latestState.current;
    const proposalOwnerKey = ownerKey;
    const proposalLimit = previewLimit;
    if (!current.catalog.snapshotToken || !current.draftVersion || !current.draftDigest) {
      throw new Error('The current table is still loading. Try adding the columns again.');
    }
    choiceProposalRequest.current?.abort();
    const controller = new AbortController();
    choiceProposalRequest.current = controller;
    if (!saveImmediately) setChoiceProposal({ status: 'previewing' });
    try {
      const response = await loomClient.proposeConstructionChoices({
        project: projectId,
        explorerId: current.explorerId,
        ...(authResourcePath ? { authResourcePath } : {}),
        commandId: window.crypto.randomUUID(),
        snapshotToken: current.catalog.snapshotToken,
        expectedDraftVersion: current.draftVersion,
        expectedDraftDigest: current.draftDigest,
        outputId: table.outputId,
        constructionChoices: selections.map((selection) => ({
          ...selection.constructionChoice,
          ...(sourceStageDescriptors?.at(-1)?.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported) ? { rowValuePolicy } : {}),
          ...(selection.title ? { title: selection.title } : {}),
        })),
        limit: proposalLimit,
      }, controller.signal);
      const latest = latestState.current;
      if (
        controller.signal.aborted ||
        ownerKey !== proposalOwnerKey ||
        previewLimit !== proposalLimit ||
        latest.explorerId !== current.explorerId ||
        latest.catalog.snapshotToken !== response.snapshotToken ||
        latest.draftVersion !== response.draftVersion ||
        latest.draftDigest !== response.draftDigest ||
        selectedTable(latest)?.outputId !== response.outputId
      ) throw new Error('The table changed while its column preview was loading. Choose the columns again.');
      if (response.preview.rows === null || response.preview.outputId !== table.outputId) {
        throw new Error('Loom did not render rows for these columns. The table was not changed.');
      }
      if (saveImmediately) {
        const applied = await applyCommands(response.constructionChoices.map((choice) => ({
          type: 'APPLY_CONSTRUCTION_CHOICE',
          outputId: response.outputId,
          constructionChoice: { choiceId: choice.choiceId, form: choice.form, ...(choice.frameId ? { frameId: choice.frameId } : {}), ...(choice.rowValuePolicy ? { rowValuePolicy: choice.rowValuePolicy } : {}) },
          ...(choice.title ? { title: choice.title } : {}),
        } satisfies ExplorerBuilderCommand)), response.commandId);
        if (!applied) throw new Error('Loom could not save these coded-value columns. The table was not changed.');
        return;
      }
      setChoiceProposal({ status: 'ready', selections, ownerKey: proposalOwnerKey, limit: proposalLimit, response });
      return 'preview-ready' as const;
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Loom could not preview the selected columns.';
      if (!controller.signal.aborted && !saveImmediately) setChoiceProposal({ status: 'error', message });
      throw error;
    } finally {
      if (choiceProposalRequest.current === controller) choiceProposalRequest.current = undefined;
    }
  };

  const excludePopulationMember = useCallback(async (
    ref: ResourceRef,
    route: ReadonlyArray<ConstructionRouteStep>,
  ) => {
    const current = latestState.current;
    const currentTable = selectedTable(current);
    const baseSelection = activePopulationSelection;
    if (!currentTable || !baseSelection) return;
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
        routeChoiceId = routes.choices.find((choice) =>
          sameConstructionRoute(choice.route, route),
        )?.routeChoiceId;
        cursor = routes.nextCursor;
      } while (!routeChoiceId && cursor);
      if (!routeChoiceId) {
        throw new Error('Loom could not preserve this table connection for the revised collection.');
      }
      const attached = await applyCommands([{
        type: 'SET_TABLE_POPULATION',
        outputId: currentTable.outputId,
        selectionRevisionId: variant.id,
        routeChoiceId,
      }]);
      if (!attached) throw new Error('Loom created the revised collection but could not attach it to this table.');
      setPopulationSelectionOverride({
        contextKey: populationSelectionContextKey,
        baseSelectionID: baseSelection.id,
        selection: variant,
      });
    } catch (error) {
      setPopulationVariantError({
        contextKey: populationSelectionContextKey,
        message: error instanceof Error ? error.message : 'Loom could not create the revised collection.',
      });
    } finally {
      setPopulationVariantPending(false);
    }
  }, [activePopulationSelection, applyCommands, authResourcePath, loomClient, populationSelectionContextKey, projectId]);

  const changeTableRoot = useCallback(
    async (nodeId: string, resolution: RowChangeResolution = {}) => {
      rowChangePreviewRequest.current?.abort();
      setPendingRowChangePreview(undefined);
      const current = latestState.current;
      const currentTable = selectedTable(current);
      if (!currentTable) return;
      try {
        const assessment = await assessRowChange({
          project: projectId,
          explorerId: current.explorerId,
          authResourcePath,
          snapshotToken: current.catalog.snapshotToken,
          draftVersion: serverDraft.current.version,
          draftDigest: serverDraft.current.digest,
          outputId: currentTable.outputId,
          rootNodeId: nodeId,
          ...resolution,
          requestId: `row-change-${window.crypto.randomUUID()}`,
        }).unwrap();
        if (assessment.status === 'NO_CHANGE') {
          setPendingRowChange(undefined);
          setMessage(undefined);
          return;
        }
        if (assessment.status === 'BLOCKED') {
          const actionable = assessment.unresolved.some(
            (reference) => (reference.alternatives?.length ?? 0) > 0,
          );
          setPendingRowChange(
            actionable ? { nodeId, resolution, assessment } : undefined,
          );
          setMessage(actionable
            ? undefined
            : `Loom did not change the rows: ${assessment.unresolved
                .map((reference) => reference.message)
                .join(' ')}`);
          return;
        }
        setPendingRowChange(undefined);
        if (!assessment.candidateReceiptId) {
          setMessage('Loom cannot preview the proposed rows. The current table has not changed.');
          return;
        }
        const context: RowChangePreviewContext = {
          assessment,
          outputId: currentTable.outputId,
          snapshotToken: current.catalog.snapshotToken,
          draftVersion: serverDraft.current.version,
          draftDigest: serverDraft.current.digest,
        };
        setPendingRowChangePreview({ ...context, status: 'loading' });
        const controller = new AbortController();
        rowChangePreviewRequest.current = controller;
        try {
          const candidatePreview = await loomClient.preview({
            project: projectId,
            explorerId: current.explorerId,
            authResourcePath,
            receiptId: assessment.candidateReceiptId,
            outputId: currentTable.outputId,
            limit: previewLimit,
            requestId: `row-change-preview-${window.crypto.randomUUID()}`,
          }, controller.signal);
          const latest = latestState.current;
          if (controller.signal.aborted) return;
          if (candidatePreview.receiptId !== assessment.candidateReceiptId ||
            candidatePreview.outputId !== currentTable.outputId ||
            candidatePreview.rows === null ||
            latest.catalog.snapshotToken !== context.snapshotToken ||
            latest.selectedOutputId !== context.outputId ||
            serverDraft.current.version !== context.draftVersion ||
            serverDraft.current.digest !== context.draftDigest) {
            throw new Error('The proposed rows could not be matched to the current table. Review the change again.');
          }
          setPendingRowChangePreview({ ...context, status: 'ready', preview: candidatePreview });
        } catch (error) {
          if (!controller.signal.aborted) {
            setPendingRowChangePreview({
              ...context,
              status: 'error',
              error: error instanceof Error ? error.message : 'Loom could not preview the proposed rows.',
            });
          }
        } finally {
          if (rowChangePreviewRequest.current === controller) rowChangePreviewRequest.current = undefined;
        }
      } catch (error) {
        const apiError = error as ExplorerAuthoringApiError;
        if (isDraftDesynchronized(apiError.code) || isStaleSnapshot(apiError.code)) {
          const refreshed = await refetchBuilder({ reload: true });
          if (refreshed.data) syncBuilderData(refreshed.data, 'hydrate');
        }
        if (apiError.code !== 'CLIENT_CANCELLED') {
          setMessage(apiError.message ?? 'Loom could not assess the row change.');
        }
      }
    },
    [
      assessRowChange,
      authResourcePath,
      loomClient,
      previewLimit,
      projectId,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  const applyRowChangePreview = useCallback(async () => {
    const pending = pendingRowChangePreview;
    if (pending?.status !== 'ready') return;
    const current = latestState.current;
    if (current.catalog.snapshotToken !== pending.snapshotToken ||
      current.selectedOutputId !== pending.outputId ||
      serverDraft.current.version !== pending.draftVersion ||
      serverDraft.current.digest !== pending.draftDigest) {
      setPendingRowChangePreview(undefined);
      setMessage('The table changed. Review the proposed rows again.');
      return;
    }
    const applied = await applyCommands([{
      type: 'APPLY_TABLE_ROOT_REBASE',
      rowChange: pending.assessment.proposal,
    }]);
    if (applied) setPendingRowChangePreview(undefined);
  }, [applyCommands, pendingRowChangePreview]);

  const resolveRowChange = useCallback(
    (
      reference: RowChangeUnresolvedReference,
      alternative: string,
    ) => {
      if (!pendingRowChange) return;
      const resolution = reference.code === 'AMBIGUOUS_ROW_ROOT_OCCURRENCE'
        ? {
            ...pendingRowChange.resolution,
            rootOccurrenceId: alternative,
          }
        : {
            ...pendingRowChange.resolution,
            routeRebase: [
              ...(pendingRowChange.resolution.routeRebase ?? []).filter(
                (choice) => choice.occurrenceId !== reference.id,
              ),
              { occurrenceId: reference.id, edgeId: alternative },
            ],
          };
      void changeTableRoot(pendingRowChange.nodeId, resolution);
    },
    [changeTableRoot, pendingRowChange],
  );

  const ensureSuggestions = useCallback(() => {
    const current = latestState.current;
    const currentOccurrence = derivedOccurrences(
      selectedTable(current),
      current.catalog,
    ).find((candidate) => candidate.id === current.selectedOccurrenceId);
    if (!currentOccurrence || !current.catalog.snapshotToken) return;
    const key = `${current.explorerId}:${current.catalog.snapshotToken}:${currentOccurrence.id}`;
    if (suggestionRequestKey.current === key) return;
    suggestionRequestKey.current = key;
    if (
      (current.catalog.candidates ?? []).some(
        (candidate) => candidate.nodeId === currentOccurrence.nodeId,
      )
    )
      return;
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
          value.snapshotToken === latest.catalog.snapshotToken &&
          latest.selectedOccurrenceId === currentOccurrence.id
        ) {
          dispatch({ type: 'candidatesLoaded', candidates: value.candidates });
        }
      })
      .catch((error: ExplorerAuthoringApiError) => {
        if (error.code !== 'CLIENT_CANCELLED') {
          const suffix = error.code ? ` (${error.code})` : '';
          setMessage(
            `Available columns could not be loaded: ${error.message}${suffix}`,
          );
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
  const blockingDiagnostics = state.diagnostics.some(
    (diagnostic) => diagnostic.severity === 'error',
  );
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
  const publishDisabled =
    (lastPublished?.ownerKey === ownerKey &&
      lastPublished.draftDigest === state.draftDigest) ||
    incomplete ||
    blockingDiagnostics ||
    constructionLifecycle.proposal.status !== 'idle' ||
    state.tables.some((candidate) =>
      !effectiveOutputAvailability(candidate.document).hasAnyOutputColumn,
    );

  const addTable = () => {
    if (
      firstTableActionPending.current ||
      latestState.current.tables.length === 0
    ) return;
    if (!tableCreatorOpen) setFirstTableName('');
    setTableCreatorOpen(true);
  };
  const duplicateTable = () => {
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
    const target = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!target) return;
    const title = window.prompt('Table name', target.title)?.trim();
    if (title && title !== target.title) renameTable(outputId, title);
  };
  const deleteSelectedTable = () => {
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
  const reorderTable = (outputId: string, before?: string) => {
    const moving = state.tables.find((candidate) => candidate.outputId === outputId);
    if (!moving?.document.rootResourceType || incomplete) {
      dispatch({ type: 'reorderTable', outputId, before });
      return;
    }
    const outputIds = state.tables
      .filter((candidate) => candidate.outputId !== outputId)
      .map((candidate) => candidate.outputId);
    const index = before ? outputIds.indexOf(before) : outputIds.length;
    outputIds.splice(index < 0 ? outputIds.length : index, 0, outputId);
    void applyCommands([{ type: 'REORDER_TABLES', outputIds }]);
  };
  const createCustomExplorer = async (title: string, fromCurrent: boolean) => {
    try {
      const created = await createExplorer({
        project: projectId,
        authResourcePath,
        name: title,
        title,
        sourceExplorerId: fromCurrent ? state.explorerId : undefined,
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
      (explorer) => explorer.explorerId === selectedExplorerId,
    );
    const fallback =
      (explorers.data ?? []).find(
        (explorer) =>
          explorer.explorerId !== selectedExplorerId &&
          explorer.explorerId === 'default',
      ) ??
      (explorers.data ?? []).find(
        (explorer) => explorer.explorerId !== selectedExplorerId,
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
        explorerId: selectedExplorerId,
        authResourcePath,
        requestId: `delete-explorer-${selectedExplorerId}`,
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
  const reconcileCurrent = useCallback(async (
    owner?: AutomaticPreviewExecution,
  ): Promise<ExplorerBuilderCompileResult | undefined> => {
    const submitted = latestState.current;
    if (
      !submitted.catalog.snapshotToken ||
      submitted.tables.length === 0 ||
      submitted.tables.some((candidate) => !candidate.document.rootResourceType)
    )
      return undefined;
    const generation = ++compileGeneration.current;
    const submittedFingerprint = intentFingerprint(
      workspaceFromState(submitted),
    );
    let snapshotToken = submitted.catalog.snapshotToken;
    let draftVersion = submitted.draftVersion;
    let draftDigest = submitted.draftDigest;
    let attempt = 1;
    if (!owner) dispatch({ type: 'compiling' });
    for (;;) {
      if (owner?.cancelled) return undefined;
      const request = reconcileBuilder({
        project: projectId,
        explorerId: submitted.explorerId,
        authResourcePath,
        snapshotToken,
        draftVersion,
        draftDigest,
        requestId: `builder-${generation}-${attempt}`,
      });
      activeCompile.current = request;
      if (owner) {
        owner.compileGeneration = generation;
        owner.compileRequest = request;
      }
      try {
        const value = await request.unwrap();
        if (owner?.cancelled || generation !== compileGeneration.current) return undefined;
        const current = latestState.current;
        if (
          current.explorerId !== submitted.explorerId ||
          intentFingerprint(workspaceFromState(current)) !==
            submittedFingerprint
        )
          return undefined;
        dispatch({ type: 'compiled', value });
        return value.diagnostics.some(
          (diagnostic) => diagnostic.severity === 'error',
        )
          ? undefined
          : value;
      } catch (error) {
        const apiError = error as ExplorerAuthoringApiError;
        if (
          owner?.cancelled ||
          generation !== compileGeneration.current ||
          apiError.code === 'CLIENT_CANCELLED'
        )
          return undefined;
        if (isDraftDesynchronized(apiError.code)) {
          const refreshed = await refetchBuilder({ reload: true });
          if (owner?.cancelled) return undefined;
          if (refreshed.data) {
            syncBuilderData(refreshed.data, 'hydrate');
            setMessage(undefined);
          } else {
            dispatch({
              type: 'repair',
              diagnostics: diagnosticsFromError(apiError),
            });
          }
          return undefined;
        }
        if (isStaleSnapshot(apiError.code)) {
          const refreshed = await refetchBuilder({ reload: true });
          if (owner?.cancelled || !refreshed.data) return undefined;
          syncBuilderData(refreshed.data, 'catalog');
          snapshotToken = refreshed.data.catalog.snapshotToken;
          draftVersion = refreshed.data.draftVersion;
          draftDigest = refreshed.data.draftDigest;
          attempt += 1;
          continue;
        }
        if (apiError.retryable && attempt < 3) {
          attempt += 1;
          await new Promise((resolve) => window.setTimeout(resolve, 250));
          continue;
        }
        dispatch({
          type: 'repair',
          diagnostics: diagnosticsFromError(apiError),
        });
        return undefined;
      } finally {
        if (activeCompile.current === request) activeCompile.current = undefined;
        if (owner?.compileRequest === request) owner.compileRequest = undefined;
      }
    }
  }, [
    authResourcePath,
    dispatch,
    projectId,
    reconcileBuilder,
    refetchBuilder,
    syncBuilderData,
  ]);
  const executePreview = useCallback(
    async (
      request: PreviewRequest,
      receipt: ExplorerBuilderCompileResult,
      owner?: AutomaticPreviewExecution,
    ): Promise<{ readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined> => {
      const generation = ++previewGeneration.current;
      let activeRequest = request;
      let activeReceipt = receipt;
      const limit = request.limit;
      let transientRetries = 0;
      for (;;) {
        if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
        const previewRequest = previewBuilder({
          project: projectId,
          explorerId: latestState.current.explorerId,
          authResourcePath,
          receiptId: activeReceipt.receiptId,
          outputId: activeRequest.outputId,
          limit,
        });
        activePreview.current = previewRequest;
        if (owner) {
          owner.previewGeneration = generation;
          owner.previewRequest = previewRequest;
        }
        try {
          const value = await previewRequest.unwrap();
          if (
            owner?.cancelled ||
            generation !== previewGeneration.current ||
            value.receiptId !== activeReceipt.receiptId ||
            value.outputId !== activeRequest.outputId
          )
            return undefined;
          setMessage(undefined);
          return { receipt: activeReceipt, preview: value };
        } catch (error) {
          const apiError = error as ExplorerAuthoringApiError;
          if (
            owner?.cancelled ||
            generation !== previewGeneration.current ||
            apiError.code === 'CLIENT_CANCELLED'
          )
            return undefined;
          const recovery = previewRecoveryAction(apiError, {
            receiptRefreshes: activeRequest.receiptRefreshes,
            transientRetries,
            limit,
          });
          if (recovery === 'retry') {
            transientRetries += 1;
            continue;
          }
          if (recovery === 'recompile') {
            activeRequest = {
              ...activeRequest,
              limit,
              receiptRefreshes: activeRequest.receiptRefreshes + 1,
            };
            const refreshedReceipt = await reconcileCurrent(owner);
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
            if (!refreshedReceipt) return undefined;
            activeReceipt = refreshedReceipt;
            setMessage(undefined);
            continue;
          }
          if (recovery === 'refresh-catalog') {
            activeRequest = {
              ...activeRequest,
              limit,
              receiptRefreshes: activeRequest.receiptRefreshes + 1,
            };
            const refreshed = await refetchBuilder({ reload: true });
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
            if (!refreshed.data) return undefined;
            latestState.current = builderAuthoringReducer(latestState.current, {
              type: 'catalogRefreshed',
              catalog: refreshed.data.catalog,
            });
            syncBuilderData(refreshed.data, 'catalog');
            const refreshedReceipt = await reconcileCurrent(owner);
            if (owner?.cancelled || generation !== previewGeneration.current) return undefined;
            if (!refreshedReceipt) return undefined;
            activeReceipt = refreshedReceipt;
            setMessage(undefined);
            continue;
          }
          if (isPreviewResponseSizeError(apiError.code)) {
            setMessage(
              `Loom could not return ${limit} preview rows at the current table width. Choose fewer rows or hide some table columns.`,
            );
          } else if (
            ['PLAN_TOO_EXPENSIVE', 'EXPENSIVE_PLAN'].includes(
              apiError.code ?? '',
            )
          ) {
            setMessage(
              'This plan is too expensive to preview. Remove columns or shorten the route.',
            );
          } else {
            const suffix = apiError.code ? ` (${apiError.code})` : '';
            setMessage(`Preview failed: ${apiError.message}${suffix}`);
          }
          throw error;
        } finally {
          if (activePreview.current === previewRequest) activePreview.current = undefined;
          if (owner?.previewRequest === previewRequest) owner.previewRequest = undefined;
        }
      }
    },
    [
      authResourcePath,
      previewBuilder,
      projectId,
      reconcileCurrent,
      refetchBuilder,
      syncBuilderData,
    ],
  );

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

  const automaticPreviewIdentity: AutomaticPreviewIdentity | undefined =
    table && state.catalog.snapshotToken && state.draftVersion > 0 && state.draftDigest
      ? {
          key: JSON.stringify([
            ownerKey,
            state.catalog.snapshotToken,
            state.draftVersion,
            state.draftDigest,
            state.previewRequestVersion,
            table.outputId,
            previewLimit,
          ]),
          ownerKey,
          snapshotToken: state.catalog.snapshotToken,
          draftVersion: state.draftVersion,
          draftDigest: state.draftDigest,
          previewRequestVersion: state.previewRequestVersion,
          outputId: table.outputId,
          limit: previewLimit,
        }
      : undefined;
  automaticPreviewRequest.current = automaticPreviewIdentity;
  const automaticPreviewQuery = useAutomaticPreview({
    request: automaticPreviewIdentity,
    enabled: !previewDisabled && pendingCommands === 0 && firstTableProgress.kind === 'idle',
    refresh: async (identity, signal, options): Promise<AutomaticPreviewRun | undefined> => {
      activeAutomaticPreview.current?.cancel();
      const generation = ++automaticPreviewGeneration.current;
      const execution: AutomaticPreviewExecution = { cancelled: false };
      const cancel = () => {
        if (automaticPreviewGeneration.current !== generation) return;
        automaticPreviewGeneration.current += 1;
        execution.cancelled = true;
        if (activeAutomaticPreview.current?.generation === generation) {
          activeAutomaticPreview.current = undefined;
        }
        if (execution.compileRequest && execution.compileGeneration === compileGeneration.current) {
          compileGeneration.current += 1;
        }
        if (execution.previewRequest && execution.previewGeneration === previewGeneration.current) {
          previewGeneration.current += 1;
        }
        execution.compileRequest?.abort();
        execution.previewRequest?.abort();
      };
      activeAutomaticPreview.current = { generation, execution, cancel };
      signal.addEventListener('abort', cancel, { once: true });
      const identityIsCurrent = () => {
        const current = latestState.current;
        return !signal.aborted &&
          automaticPreviewRequest.current?.key === identity.key &&
          current.catalog.snapshotToken === identity.snapshotToken &&
          current.draftVersion === identity.draftVersion &&
          current.draftDigest === identity.draftDigest &&
          current.previewRequestVersion === identity.previewRequestVersion &&
          selectedTable(current)?.outputId === identity.outputId;
      };
      const accept = (receipt: ExplorerBuilderCompileResult, preview: ExplorerBuilderPreviewResult) => {
        if (!identityIsCurrent() ||
          receipt.snapshotToken !== identity.snapshotToken ||
          !receipt.outputs.some((output) => output.outputId === identity.outputId) ||
          preview.receiptId !== receipt.receiptId ||
          preview.outputId !== identity.outputId) return undefined;
        dispatch({
          type: 'previewAccepted',
          identity: {
            project: projectId,
            explorerId: latestState.current.explorerId,
            snapshotToken: identity.snapshotToken,
            draftVersion: identity.draftVersion,
            draftDigest: identity.draftDigest,
            previewRequestVersion: identity.previewRequestVersion,
            outputId: identity.outputId,
          },
          receipt,
          preview,
        });
        setMessage(undefined);
        return { identity, receipt, preview };
      };
      try {
        if (!identityIsCurrent()) return undefined;
        const current = latestState.current;
        const receipt = !options?.reload && current.receipt && current.reconciliation === 'resolved' &&
          current.receipt.snapshotToken === identity.snapshotToken
          ? current.receipt
          : await reconcileCurrent(execution);
        if (execution.cancelled || !receipt || !identityIsCurrent()) return undefined;
        const candidate = appliedChoicePreview.current;
        appliedChoicePreview.current = undefined;
        if (matchesAcceptedChoicePreview(candidate, {
          ownerKey: identity.ownerKey,
          outputId: identity.outputId,
          limit: identity.limit,
          snapshotToken: identity.snapshotToken,
          receipt,
        })) return accept(receipt, candidate.preview);
        const completed = await executePreview({
          outputId: identity.outputId,
          limit: identity.limit,
          receiptRefreshes: 0,
        }, receipt, execution);
        if (execution.cancelled || !completed || !identityIsCurrent()) return undefined;
        return accept(completed.receipt, completed.preview);
      } catch (error) {
        if (!identityIsCurrent()) return undefined;
        const apiError = error as ExplorerAuthoringApiError;
        const suffix = apiError.code ? ` (${apiError.code})` : '';
        setMessage(`Preview failed: ${apiError.message}${suffix}`);
        throw error;
      } finally {
        signal.removeEventListener('abort', cancel);
        if (activeAutomaticPreview.current?.generation === generation) {
          activeAutomaticPreview.current = undefined;
        }
      }
    },
  });
  const automaticPreviewFailure = automaticPreviewQuery.error as Partial<ExplorerAuthoringApiError> | undefined;
  const automaticPreviewFailureMessage = automaticPreviewQuery.error === undefined
    ? undefined
    : automaticPreviewFailure?.message
      ? `${automaticPreviewFailure.message}${automaticPreviewFailure.code ? ` (${automaticPreviewFailure.code})` : ''}`
      : 'The automatic preview request failed.';
  const addColumnsPreviewPending = automaticPreviewQuery.isLoading &&
    outputAvailability.hasAnyOutputColumn;
  const busy =
    pendingCommands > 0 ||
    reconcileStatus.isLoading ||
    previewStatus.isLoading ||
    firstTableProgress.kind === 'running' ||
    constructionProposalBusy ||
    publishing ||
    createStatus.isLoading ||
    deleteStatus.isLoading;
  const applyPresentationChanges = (changes: ReadonlyArray<PreviewTablePresentationChange>) => {
    if (!table || changes.length === 0) return;
    void applyCommandsWithResult(changes.map((change) =>
      previewPresentationCommand(table.outputId, change),
    ));
  };
  const executePublish = useCallback(
    async (receiptId: string) => {
      let activeReceiptId = receiptId;
      let refreshes = 0;
      for (;;) {
        try {
          await publishBuilder({
            project: projectId,
            explorerId: latestState.current.explorerId,
            authResourcePath,
            receiptId: activeReceiptId,
          }).unwrap();
          setLastPublished({
            ownerKey,
            draftDigest: latestState.current.draftDigest,
          });
          dispatch({ type: 'published' });
          setMessage(undefined);
          return;
        } catch (error) {
          const apiError = error as ExplorerAuthoringApiError;
          if (isStaleSnapshot(apiError.code) && refreshes === 0) {
            refreshes += 1;
            const refreshed = await refetchBuilder({ reload: true });
            if (!refreshed.data) return;
            latestState.current = builderAuthoringReducer(latestState.current, {
              type: 'catalogRefreshed',
              catalog: refreshed.data.catalog,
            });
            syncBuilderData(refreshed.data, 'catalog');
            const receipt = await reconcileCurrent();
            if (!receipt) return;
            activeReceiptId = receipt.receiptId;
            continue;
          }
          const suffix = apiError.code ? ` (${apiError.code})` : '';
          setMessage(
            `Publication failed; the previously active Viewer revision remains available: ${apiError.message}${suffix}`,
          );
          return;
        }
      }
    },
    [
      authResourcePath,
      dispatch,
      ownerKey,
      projectId,
      publishBuilder,
      reconcileCurrent,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  const publish = async () => {
    if (publishDisabled || publishing) return;
    setPublishing(true);
    try {
      const receipt =
        state.receipt && state.reconciliation === 'resolved'
          ? state.receipt
          : await reconcileCurrent();
      if (!receipt) return;
      const publishable = state.tables.every((candidate) =>
        receipt.outputs.some(
          (output) =>
            output.outputId === candidate.outputId && output.columns.length > 0,
        ),
      );
      if (!publishable) {
        setMessage(
          'Every table needs at least one output column before publishing.',
        );
        return;
      }
      await executePublish(receipt.receiptId);
    } finally {
      setPublishing(false);
    }
  };

  if (explorers.isLoading || builder.isLoading) {
    return (
      <main className="p-6" role="status">
        Loading the selected Explorer configuration…
      </main>
    );
  }
  if (explorers.error || !builder.data) {
    return (
      <main className="p-6" role="alert">
        <p>Couldn’t load this dataset.</p>
        <button
          type="button"
          onClick={() => {
            void explorers.refetch();
            void refetchBuilder();
          }}
        >
          Try again
        </button>
      </main>
    );
  }

  const toolbar = (
    <BuilderToolbar
      explorers={explorers.data ?? []}
      selectedExplorerId={selectedExplorerId}
      onExplorerChange={selectExplorer}
      onCreateExplorer={(title, fromCurrent) =>
        void createCustomExplorer(title, fromCurrent)
      }
      deleteSupported={capabilities.data?.features.deleteExplorer ?? false}
      deleteDisabled={
        !(explorers.data ?? []).some(
          (explorer) => explorer.explorerId !== selectedExplorerId,
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
      onReorderTable={reorderTable}
      onReview={() => {
        const open = !reviewOpen;
        setReviewOpen(open);
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
  const selectConstructionFamily = (family: ConstructionOperationFamily) => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('choose');
    setActiveConstructionFamily((current) => current === family ? undefined : family);
  };
  const chooseRelatedRows = () => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind('related-expand');
    setReshapeEntry((current) => current + 1);
    setActiveConstructionFamily('RESHAPE');
  };
  const chooseReshapeRows = (kind: 'group' | 'source-group' | 'coded-group' | 'expand' | 'categories' | 'pivot' | 'coded-pivot' | 'unpivot') => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setReshapeEntryKind(kind);
    const nextEntry = reshapeEntry + 1;
    if (kind === 'expand' && table && constructionLifecycle.capabilities.status === 'ready') {
      const response = constructionLifecycle.capabilities.response;
      const appendStageId = constructionAppendStageFor(response.baseConstruction, table.document.rows);
      const supported = response.selectedStage.id === appendStageId
        && response.selectedStage.capabilities.some((candidate) => candidate.kind === 'EXPAND' && candidate.supported)
        && hasPublicScalarListColumn(response.selectedStage);
      const entry = supported
        ? createReshapeEditorEntry({
            construction: construction ?? response.baseConstruction,
            capabilities: response,
          })
        : undefined;
      if (entry) {
        constructionLifecycle.onCandidateChange(entry.candidateIntent);
        setReshapeInitialEntry({
          editorKey: reshapeEditorKeyFor(nextEntry, kind, response.selectedStage.id),
          entry,
        });
      } else {
        setReshapeInitialEntry(undefined);
      }
    } else {
      setReshapeInitialEntry(undefined);
    }
    setReshapeEntry(nextEntry);
    setActiveConstructionFamily('RESHAPE');
  };
  const openCodedValueCatalog = () => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setPairedColumnSuggestion(undefined);
    setFeatureMode('catalog');
    setAddColumnsView('fields');
    setAddColumnsSource({ context: addSourceContext, key: 'all' });
    setAddSourceSearch({ context: addSourceContext, query: '' });
    setActiveConstructionFamily('ADD_COLUMNS');
    requestAnimationFrame(() => {
      document.querySelector<HTMLInputElement>('[aria-label="Add columns editor"] #feature-catalog-search')?.focus();
    });
  };
  const selectConstructionHistory = (selection: ConstructionHistorySelection) => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection(selection);
    setEditingConstructionStepId(undefined);
    setActiveConstructionFamily(undefined);
  };
  const editConstructionStep = (stepId: string) => {
    const step = construction?.steps.find((candidate) => candidate.id === stepId);
    if (!step) return;
    if (step.operation.kind === 'RELATED_SOURCE' || step.operation.kind === 'RELATED_FIELD') {
      constructionLifecycle.cancel();
      setConstructionHistorySelection({ kind: 'step', stepId });
      setEditingConstructionStepId(stepId);
      setActiveConstructionFamily(undefined);
      return;
    }
    const family = editableConstructionFamily(step.operation);
    if (!family) return;
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'step', stepId });
    setEditingConstructionStepId(stepId);
    setActiveConstructionFamily(family);
  };
  const removeConstructionStep = (stepId: string) => {
    if (!construction?.steps.some((step) => step.id === stepId)) return;
    constructionLifecycle.onCandidateChange({
      candidateConstruction: {
        ...construction,
        steps: construction.steps.filter((step) => step.id !== stepId),
      },
      removeStepIds: [stepId],
    });
    setConstructionHistorySelection({ kind: 'step', stepId });
    setEditingConstructionStepId(undefined);
    setActiveConstructionFamily(undefined);
  };
  const applyConstructionProposal = async () => {
    const proposalId = constructionLifecycle.beginApply();
    if (!proposalId || !table) return;
    const applied = await applyCommands([{
      type: 'APPLY_CONSTRUCTION_PROPOSAL',
      outputId: table.outputId,
      proposalId,
    }]);
    constructionLifecycle.finishApply(applied);
    if (applied) {
      setActiveConstructionFamily(undefined);
      setConstructionHistorySelection({ kind: 'source' });
      setEditingConstructionStepId(undefined);
    }
  };

  const restorePreviousDraft = async () => {
    if (!previousDraftRevisionId) return;
    constructionLifecycle.cancel();
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
    ? reshapeEditorKeyFor(
        reshapeEntry,
        reshapeEntryKind,
        constructionLifecycle.capabilities.response.selectedStage.id,
      )
    : undefined;
  const activeReshapeInitialEntry = reshapeInitialEntry && reshapeInitialEntry.editorKey === activeReshapeEditorKey
    ? reshapeInitialEntry.entry
    : undefined;
  const activeReshapeInitialKind = reshapeEntryKind === 'expand' && !activeReshapeInitialEntry
    ? 'choose'
    : reshapeEntryKind;
  const editingRelatedSourceStep: RelatedSourceStep | undefined = editingConstructionStep?.operation.kind === 'RELATED_SOURCE'
    ? editingConstructionStep as RelatedSourceStep
    : undefined;
  const editingRelatedFieldStep = editingConstructionStep && isRelatedFieldStep(editingConstructionStep)
    ? editingConstructionStep
    : undefined;
  const sourceStageDescriptors = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.stages
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
    ? { available: false, reason: constructionLifecycle.capabilities.message }
    : sourceProjectionAvailability(sourceStageDescriptors);
  const capabilityStage = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.selectedStage
    : undefined;
  const relatedSourceCapability = capabilityStage?.capabilities.find(
    (candidate) => candidate.kind === 'RELATED_SOURCE',
  );
  const relatedSourceReason = [
    relatedSourceCapability?.reasonCode,
    relatedSourceCapability?.reason,
  ].filter((reason): reason is string => Boolean(reason?.trim())).join(': ');
  const capabilityIsForAppendStage = constructionLifecycle.capabilities.status === 'ready' &&
    capabilityStage?.id === constructionAppendStageFor(
      constructionLifecycle.capabilities.response.baseConstruction,
      table?.document.rows,
    );
  const reshapeAvailabilityFor = (kind: 'GROUP' | 'PIVOT' | 'CODED_PIVOT') => {
    const capability = capabilityStage?.capabilities.find((candidate) => candidate.kind === kind);
    return capabilityIsForAppendStage && capability?.supported
      ? { supported: true }
      : {
          supported: false,
          reason: constructionLifecycle.capabilities.status === 'error'
            ? constructionLifecycle.capabilities.message
            : constructionLifecycle.capabilities.status !== 'ready' || !capabilityIsForAppendStage
              ? 'Checking this table’s available row operations…'
              : capability?.reason ?? 'This row operation is unavailable for the current table.',
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
  const tablePivotAvailability = constructionLifecycle.capabilities.status === 'ready' && constructionLifecycle.capabilities.response.pivotSourceInput?.supported
    ? { supported: true } : reshapeAvailabilityFor('PIVOT');
  const expandCapability = capabilityStage?.capabilities.find((candidate) => candidate.kind === 'EXPAND');
  const expandRowsAvailability = capabilityIsForAppendStage && expandCapability?.supported && hasPublicScalarListColumn(capabilityStage)
    ? { supported: true }
    : {
        supported: false,
        reason: constructionLifecycle.capabilities.status === 'error'
          ? constructionLifecycle.capabilities.message
          : constructionLifecycle.capabilities.status !== 'ready' || !capabilityIsForAppendStage
            ? 'Checking this table’s available row operations…'
            : expandCapability?.reason ?? 'No list column is available in this table.',
      };
  const groupEntries = [
    ...(stageGroupingAvailability.supported || codedPivotAvailability.supported ? [{ kind: 'group' as const, label: 'By table column' }] : []),
    ...(sourceGroupingAvailable ? [{ kind: 'source-group' as const, label: 'By source fields' }] : []),
    ...(codedGroupingAvailable ? [{ kind: 'coded-group' as const, label: 'By recorded code' }] : []),
  ];
  const reshapeRowsAvailability = {
    group: groupEntries.length > 0 ? { supported: true } : stageGroupingAvailability,
    groupEntry: groupEntries[0]?.kind ?? 'group' as const,
    groupAlternatives: groupEntries.slice(1),
    pivot: codedPivotAvailability.supported ? codedPivotAvailability : tablePivotAvailability,
    expand: expandRowsAvailability,
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
        reason: constructionLifecycle.capabilities.status === 'error'
          ? constructionLifecycle.capabilities.message
          : constructionLifecycle.capabilities.status !== 'ready'
            ? 'Checking available related row paths…'
            : relatedExpandCapability?.reason ?? 'This table has no executable related-record row path at the current stage.',
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
            ? constructionLifecycle.capabilities.message
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
    pendingCommands === 0 &&
    !publishing &&
    state.reconciliation !== 'pending';
  const operationEditor = table && activeOperation ? (
    <section
      aria-label={`${activeOperation.label} editor`}
      data-testid="construction-operation-editor"
      data-operation-family={activeOperation.family}
      className="overflow-hidden rounded-xl border border-emerald-200 bg-white shadow-sm"
    >
      <header className="border-b border-slate-200 px-3 py-2.5">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-slate-950">{activeOperation.label}</h2>
            {editingConstructionStep ? <p className="text-xs text-slate-500">Editing a saved step</p> : null}
          </div>
          <button
            type="button"
            aria-label="Close operation editor"
            data-testid="construction-close-operation-editor"
            onClick={() => {
              constructionLifecycle.cancel();
              setActiveConstructionFamily(undefined);
              setEditingConstructionStepId(undefined);
            }}
            className="rounded px-2 py-1 text-sm text-slate-500 hover:bg-slate-100"
          >
            Back to table
          </button>
        </div>
        {selectedColumnIds.length > 0 ? (
          <p className="mt-3 rounded bg-slate-50 px-2.5 py-2 text-xs text-slate-600">
            Inputs from your selection:{' '}
            <span className="font-medium text-slate-800">
              {selectableColumns
                .filter((column) => selectedColumnIds.includes(column.id))
                .map((column) => column.label)
                .join(', ')}
            </span>
          </p>
        ) : null}
      </header>

      <div className="p-3">
        {activeOperation.family === 'ADD_COLUMNS' ? (
          <div className="grid gap-4">
            {rowValuesSupported ? (
              <label className="flex flex-wrap items-center gap-2 text-sm text-slate-700">
                Values from each row's contributing records
                <select aria-label="Values per grouped row" value={rowValuePolicy} onChange={(event) => {
                  const value = event.currentTarget.value;
                  if (value === 'ALL' || value === 'ONE') setRowValuePolicy(value);
                }} className="rounded border border-slate-300 bg-white px-2 py-1.5">
                  <option value="ALL">Keep all distinct values</option>
                  <option value="ONE">Require one distinct value</option>
                </select>
                <span className="text-xs text-slate-500">Missing values stay empty. Require one stops if records disagree.</span>
              </label>
            ) : null}
            <div role="group" aria-label="Column types" className="flex gap-1 rounded-md bg-slate-100 p-1">
              <button type="button" aria-pressed={addColumnsView === 'coded'} onClick={() => setAddColumnsView('coded')}
                className={`rounded px-3 py-1.5 text-sm font-medium ${addColumnsView === 'coded' ? 'bg-white text-blue-900 shadow-sm' : 'text-slate-600'}`}>
                Coded values
              </button>
              <button type="button" aria-pressed={addColumnsView === 'fields'} onClick={() => setAddColumnsView('fields')}
                className={`rounded px-3 py-1.5 text-sm font-medium ${addColumnsView === 'fields' ? 'bg-white text-blue-900 shadow-sm' : 'text-slate-600'}`}>
                Fields and related data
              </button>
            </div>
            {addColumnsView === 'coded' && table.document.rootResourceType ? (
              <>
                {sourceAvailability.available ? <PairedColumnSuggestions
                  project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                  snapshotToken={state.catalog.snapshotToken} outputId={table.outputId}
                  rowRoot={table.document.rootResourceType}
                  columns={[...selectableColumns, ...table.document.columns.map((column) => ({ id: column.column, label: column.label }))]}
                  disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                  onSelectSuggestion={(suggestion) => {
                    setPairedColumnSuggestion(suggestion);
                    setFeatureMode('catalog');
                    setAddColumnsView('fields');
                  }}
                  onBrowseAll={openCodedValueCatalog}
                /> : null}
                <FrameSourcePanel
                  key={`${table.outputId}:${state.catalog.snapshotToken}`}
                  project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                  snapshotToken={state.catalog.snapshotToken} outputId={table.outputId}
                  rowRoot={table.document.rootResourceType} frames={table.document.frames ?? []}
                  columns={table.document.columns} disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                  onSet={(choice, form) => applyCommands([{
                    type: 'SET_FRAME_SOURCE', outputId: table.outputId, frameChoiceId: choice.choiceId, form,
                  }])}
                  onReplace={(frameId, choice, form) => applyCommands([{
                    type: 'REPLACE_FRAME_SOURCE', outputId: table.outputId, frameId, frameChoiceId: choice.choiceId, form,
                  }])}
                  onRemove={(frameId) => applyCommands([{
                    type: 'REMOVE_FRAME_SOURCE', outputId: table.outputId, frameId,
                  }])}
                  onAddSelected={(selections) => addSelectedFeatures(selections, true)}
                  onRemoveColumn={(column) => applyCommands([{
                    type: 'REMOVE_COLUMN', outputId: table.outputId, column,
                  }])}
                />
              </>
            ) : null}
            {addColumnsView === 'fields' ? <>
            <fieldset
              aria-label="Add columns source"
              data-testid="construction-add-columns-source"
              className="rounded-lg border border-slate-200 bg-white p-2"
            >
              <legend className="px-1 text-xs font-semibold text-slate-700">
                Search in
              </legend>
              <div role="group" aria-label="Search scope">
                <div className="flex flex-wrap gap-1.5">
                  {scopeSourceOptions.map(({ source, label, description, selected }) => (
                    <button
                      key={source.key}
                      type="button"
                      aria-label={`${label}, ${description}`}
                      aria-pressed={selected}
                      data-testid="construction-add-columns-source-option"
                      data-source-key={source.key}
                      data-source-kind={source.kind}
                      onClick={() => {
                        setAddColumnsSource({ context: addSourceContext, key: source.key });
                        setAddSourceSearch({ context: addSourceContext, query: '' });
                      }}
                      title={description}
                      className={`rounded-md border px-2 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-700 ${selected ? 'border-blue-700 bg-blue-50 text-blue-950' : 'border-slate-300 bg-white text-slate-800 hover:border-slate-500'}`}
                    >
                      {label}
                    </button>
                  ))}
                </div>
              </div>
              {relatedSourceOptions.length > 0 ? (
                <details role="group" aria-label="Related resources" open={relatedSourceMenuOpen || selectedAddSource?.kind === 'EXACT_RELATED'} className="mt-2 border-t border-slate-100 pt-2">
                  <summary className="cursor-pointer text-xs font-medium text-blue-800" onClick={(event) => { event.preventDefault(); setRelatedSourceMenuOpen((open) => !open); }}>
                    Related resources ({relatedSourceOptions.length})
                  </summary>
                  {relatedSourceOptions.length >= 6 ? (
                    <input
                      type="search"
                      aria-label="Search related resources"
                      data-testid="construction-add-columns-source-search"
                      value={addSourceSearch.context === addSourceContext ? addSourceSearch.query : ''}
                      onChange={(event) => setAddSourceSearch({
                        context: addSourceContext,
                        query: event.currentTarget.value,
                      })}
                      placeholder="Search resource types"
                      className="mb-2 w-full rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-900 placeholder:text-slate-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-700"
                    />
                  ) : null}
                  <div className="flex flex-wrap gap-1.5">
                    {visibleRelatedSourceOptions.map(({ source, label, description, selected }) => (
                      <button
                        key={source.key}
                        type="button"
                        aria-label={`${label}, ${description}`}
                        aria-pressed={selected}
                        data-testid="construction-add-columns-source-option"
                        data-source-key={source.key}
                        data-source-kind={source.kind}
                        onClick={() => {
                          setAddColumnsSource({ context: addSourceContext, key: source.key });
                          setAddSourceSearch({ context: addSourceContext, query: '' });
                        }}
                        title={description}
                        className={`rounded-md border px-2 py-1 text-xs font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-700 ${selected ? 'border-blue-700 bg-blue-50 text-blue-950' : 'border-slate-300 bg-white text-slate-800 hover:border-slate-500'}`}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                  {visibleRelatedSourceOptions.length === 0 ? (
                    <p role="status" className="text-sm text-slate-600">
                      No related resources match this search.
                    </p>
                  ) : null}
                </details>
              ) : null}
            </fieldset>
            {selectedAddSource?.kind === 'EXACT_RELATED' && constructionLifecycle.capabilities.status === 'ready' ? (
              <RelatedFieldEditor
                key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${constructionLifecycle.capabilities.response.selectedStage.id}`}
                project={projectId}
                explorerId={state.explorerId}
                authResourcePath={authResourcePath}
                snapshotToken={state.catalog.snapshotToken}
                outputId={table.outputId}
                construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                capabilities={constructionLifecycle.capabilities.response}
                disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                onCandidateChange={constructionLifecycle.onCandidateChange}
              />
            ) : <ConceptCatalog
              key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${selectedAddSource?.key ?? 'root'}:${addSourceRouteContext?.occurrenceId ?? 'unscoped'}`}
              project={projectId}
              explorerId={state.explorerId}
              authResourcePath={authResourcePath}
              snapshotToken={state.catalog.snapshotToken}
              outputId={table.outputId}
              rowRoot={table.document.rootResourceType}
              resourceType={selectedAddSource?.resourceType}
              sourceNodeId={selectedAddSource?.sourceNodeId}
              routeContext={addSourceRouteContext}
              layout="panel"
              catalog={state.catalog}
              sourceProjectionAvailability={sourceAvailability}
              relatedSourceAvailability={relatedSourceAvailability}
              suppressUnavailableNotices={hasUnsupportedSavedSourceColumns}
              disabledReason={sourceSelectionDisabledReason}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
              pairedColumnSuggestion={pairedColumnSuggestion}
              groupedRowValuePolicy={groupedRowValuePolicy}
              onPairedColumnSuggestionHandled={(requestId) =>
                setPairedColumnSuggestion((current) => current?.requestId === requestId ? undefined : current)
              }
              onAddSelected={addSelectedFeatures}
              onInspectRouteCoverage={inspectRelatedRouteCoverage}
            />}
            </> : null}
          </div>
        ) : null}
        {activeOperation.family === 'KEEP_ROWS' || activeOperation.family === 'CALCULATE' ? (
          constructionLifecycle.capabilities.status === 'loading' ? (
            <p role="status" className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              Loading the current table columns and operation support…
            </p>
          ) : constructionLifecycle.capabilities.status === 'error' ? (
            <p role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
              {constructionLifecycle.capabilities.message}
            </p>
          ) : constructionLifecycle.capabilities.status === 'ready' ? (
            activeOperation.family === 'KEEP_ROWS' ? (
              <FilterRowsEditor
                key={`${ownerKey}:${table.outputId}:${constructionLifecycle.capabilities.response.selectedStage.id}:${editingConstructionStep?.id ?? 'new'}`}
                project={projectId} explorerId={state.explorerId} authResourcePath={authResourcePath}
                snapshotToken={state.catalog.snapshotToken} outputId={table.outputId} catalog={state.catalog}
                construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
                capabilities={constructionLifecycle.capabilities.response}
                editingStep={editingConstructionStep}
                selectedColumns={selectedColumnIds}
                disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                onCandidateChange={constructionLifecycle.onCandidateChange}
                onEditStep={editConstructionStep}
              />
            ) : <ConstructionOperationEditor
              family="CALCULATE"
              construction={table.document.construction ?? constructionLifecycle.capabilities.response.baseConstruction}
              capabilities={constructionLifecycle.capabilities.response}
              editingStep={editingConstructionStep}
              selectedColumns={selectedColumnIds}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
              onCandidateChange={constructionLifecycle.onCandidateChange}
              onEditStep={editConstructionStep}
            />
          ) : null
        ) : null}
        {activeOperation.family === 'RESHAPE' ? (
          constructionLifecycle.capabilities.status === 'loading' ? (
            <p role="status" className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              Loading the current table columns and reshape support…
            </p>
          ) : constructionLifecycle.capabilities.status === 'error' ? (
            <p role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
              {constructionLifecycle.capabilities.message}
            </p>
          ) : constructionLifecycle.capabilities.status === 'ready' ? (
            <ConstructionReshapeEditor
              key={activeReshapeEditorKey}
              construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
              capabilities={constructionLifecycle.capabilities.response}
              editingStep={editingConstructionStep}
              initialKind={activeReshapeInitialKind}
              initialEntry={activeReshapeInitialEntry}
              selectedColumns={selectedColumnIds}
              pivotDiscovery={constructionLifecycle.pivotDiscovery}
              onDiscoverCategories={constructionLifecycle.onDiscoverCategories}
              relatedExpandContext={{
                project: projectId,
                explorerId: state.explorerId,
                authResourcePath,
                snapshotToken: state.catalog.snapshotToken,
                outputId: table.outputId,
                catalog: state.catalog,
              }}
              relatedExpandQueryOwnerRef={relatedExpandQueryOwnerRef}
              codedPivotContext={{
                client: loomClient,
                project: projectId,
                explorerId: state.explorerId,
                authResourcePath,
                snapshotToken: state.catalog.snapshotToken,
                outputId: table.outputId,
                rowRoot: table.document.rootResourceType,
              }}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
              onCandidateChange={constructionLifecycle.onCandidateChange}
              onEditStep={editConstructionStep}
            />
          ) : (
            null
          )
        ) : null}
        {activeOperation.family === 'COMBINE' ? (
          <div role="status" data-testid="construction-operation-unavailable" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
            <h3 className="font-semibold">Combining tables is not available yet</h3>
            <p className="mt-1">The authoring API does not yet execute immutable table joins or appends. No change has been proposed.</p>
          </div>
        ) : null}
      </div>
    </section>
  ) : undefined;
  const relatedSourceStepEditor = table && editingRelatedSourceStep ? (
    <section
      aria-label="Related source editor"
      data-testid="construction-related-source-editor"
      className="overflow-hidden rounded-xl border border-emerald-200 bg-white p-4 shadow-sm"
    >
      {constructionLifecycle.capabilities.status === 'loading' ? (
        <p role="status" className="text-sm text-slate-700">Loading the saved step’s input stage…</p>
      ) : constructionLifecycle.capabilities.status === 'error' ? (
        <p role="alert" className="text-sm text-red-800">{constructionLifecycle.capabilities.message}</p>
      ) : constructionLifecycle.capabilities.status === 'ready' ? (
        <RelatedSourceStepEditor
          project={projectId}
          explorerId={state.explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={state.catalog.snapshotToken}
          outputId={table.outputId}
          rowRoot={table.document.rootResourceType ?? ''}
          catalog={state.catalog}
          construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
          capabilities={constructionLifecycle.capabilities.response}
          step={editingRelatedSourceStep}
          disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
          onCandidateChange={constructionLifecycle.onCandidateChange}
          onCancel={() => {
            constructionLifecycle.cancel();
            setEditingConstructionStepId(undefined);
            setActiveConstructionFamily(undefined);
          }}
        />
      ) : null}
    </section>
  ) : undefined;
  const relatedFieldStepEditor = table && editingRelatedFieldStep ? (
    <section aria-label="Related field editor" className="overflow-hidden rounded-xl border border-emerald-200 bg-white p-4 shadow-sm">
      {constructionLifecycle.capabilities.status === 'loading' ? (
        <p role="status" className="text-sm text-slate-700">Loading the saved step’s input stage…</p>
      ) : constructionLifecycle.capabilities.status === 'error' ? (
        <p role="alert" className="text-sm text-red-800">{constructionLifecycle.capabilities.message}</p>
      ) : constructionLifecycle.capabilities.status === 'ready' ? (
        <RelatedFieldEditor
          key={`${ownerKey}:${editingRelatedFieldStep.id}`}
          project={projectId}
          explorerId={state.explorerId}
          authResourcePath={authResourcePath}
          snapshotToken={state.catalog.snapshotToken}
          outputId={table.outputId}
          construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
          capabilities={constructionLifecycle.capabilities.response}
          step={editingRelatedFieldStep}
          disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
          onCandidateChange={constructionLifecycle.onCandidateChange}
          onCancel={() => {
            constructionLifecycle.cancel();
            setEditingConstructionStepId(undefined);
          }}
        />
      ) : null}
    </section>
  ) : undefined;
  const proposalPanel = (
    <ConstructionProposalPanel
      state={constructionLifecycle.proposal}
      baseConstruction={construction ?? (constructionLifecycle.capabilities.status === 'ready' ? constructionLifecycle.capabilities.response.baseConstruction : undefined)}
      canApply={canApplyConstructionProposal}
      onApply={() => void applyConstructionProposal()}
      onCancel={() => {
        constructionLifecycle.cancel();
        setActiveConstructionFamily(undefined);
        setEditingConstructionStepId(undefined);
      }}
      onRetry={constructionLifecycle.retry}
    />
  );
  const choicePreview = choiceProposal.status === 'ready' ? choiceProposal.response.preview : undefined;
  const applyChoiceProposal = async () => {
    if (choiceProposal.status !== 'ready' || pendingCommands > 0) return;
    const { response } = choiceProposal;
    const latest = latestState.current;
    if (
      ownerKey !== choiceProposal.ownerKey ||
      previewLimit !== choiceProposal.limit ||
      latest.catalog.snapshotToken !== response.snapshotToken ||
      latest.draftVersion !== response.draftVersion ||
      latest.draftDigest !== response.draftDigest ||
      selectedTable(latest)?.outputId !== response.outputId
    ) {
      setChoiceProposal({ status: 'error', message: 'The table changed since this preview. Choose the columns again.' });
      return;
    }
    const applied = await applyCommands(response.constructionChoices.map((choice) => ({
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId: response.outputId,
      constructionChoice: { choiceId: choice.choiceId, form: choice.form, ...(choice.frameId ? { frameId: choice.frameId } : {}), ...(choice.rowValuePolicy ? { rowValuePolicy: choice.rowValuePolicy } : {}) },
      ...(choice.title ? { title: choice.title } : {}),
    } satisfies ExplorerBuilderCommand)), response.commandId);
    if (applied) {
      appliedChoicePreview.current = {
        ownerKey: choiceProposal.ownerKey,
        outputId: response.outputId,
        limit: choiceProposal.limit,
        snapshotToken: response.snapshotToken,
        candidateWorkspaceDigest: response.candidateWorkspaceDigest,
        preview: response.preview,
      };
      setChoiceProposal({ status: 'idle' });
    } else {
      appliedChoicePreview.current = undefined;
    }
  };
  const choiceProposalPanel = choiceProposal.status === 'idle' ? null : (
    <section data-testid="construction-choice-proposal-panel" data-proposal-status={choiceProposal.status} className="rounded-xl border border-blue-200 bg-white p-4 shadow-sm">
      <h3 className="text-base font-semibold text-slate-900">Preview new columns</h3>
      {choiceProposal.status === 'previewing' ? <p role="status" className="mt-2 text-sm text-slate-600">Rendering rows with the selected columns…</p> : null}
      {choiceProposal.status === 'error' ? <p role="alert" className="mt-2 text-sm text-red-800">{choiceProposal.message}</p> : null}
      {choiceProposal.status === 'ready' ? (
        <>
          <p className="mt-2 text-sm text-slate-700">
            {choiceProposal.response.candidateColumnIds.length} new {choiceProposal.response.candidateColumnIds.length === 1 ? 'column' : 'columns'} in the rendered row preview. Apply saves them to this table.
          </p>
          <PreviewValueCoverage preview={choiceProposal.response.preview} columnIds={choiceProposal.response.candidateColumnIds} />
        </>
      ) : null}
      <div className="mt-4 flex gap-2">
        {choiceProposal.status === 'ready' ? (
          <button type="button" onClick={() => void applyChoiceProposal()} disabled={pendingCommands > 0} className="rounded bg-blue-700 px-3 py-2 text-sm font-semibold text-white disabled:opacity-50">Apply columns</button>
        ) : null}
        <button type="button" onClick={() => {
          choiceProposalRequest.current?.abort();
          setChoiceProposal({ status: 'idle' });
        }} className="rounded border border-slate-300 px-3 py-2 text-sm font-medium text-slate-700">Cancel</button>
      </div>
    </section>
  );
  const workspaceEditor = operationEditor || relatedSourceStepEditor || relatedFieldStepEditor || constructionLifecycle.proposal.status !== 'idle' || choiceProposal.status !== 'idle'
    ? <>{choiceProposalPanel}{operationEditor}{relatedSourceStepEditor}{relatedFieldStepEditor}{proposalPanel}</>
    : undefined;
  const candidatePreview = choicePreview ?? ((constructionLifecycle.proposal.status === 'ready' ||
    constructionLifecycle.proposal.status === 'applying'
    ? constructionLifecycle.proposal.preview
    : undefined));
  const proposalResponse = 'response' in constructionLifecycle.proposal
    ? constructionLifecycle.proposal.response
    : undefined;
  const proposalPresentationTable = !choicePreview && table && proposalResponse
    ? {
        ...table,
        document: {
          ...table.document,
          construction: proposalResponse.candidateConstruction,
        },
      }
    : undefined;
  const workspacePreview = candidatePreview ?? tablePreview;
  const workspacePreviewIsCurrent = Boolean(candidatePreview) || previewIsCurrent;
  const workspacePreviewStatus = candidatePreview
    ? 'ready'
    : automaticPreviewQuery.error
      ? 'error'
      : choiceProposal.status === 'previewing' || constructionLifecycle.proposal.status === 'previewing'
      ? 'previewing'
      : constructionLifecycle.proposal.status === 'needs-repair'
        ? 'needs-repair'
        : choiceProposal.status === 'error' || constructionLifecycle.proposal.status === 'error'
          ? 'error'
          : currentPreviewStatus;
  const workspacePreviewProposalId = candidatePreview?.receiptId ?? proposalResponse?.proposalId;
  const tableCreatorPanel = (
    <section className="rounded-xl border border-blue-200 bg-white px-6 py-8 shadow-sm">
      <h2 className="text-xl font-semibold text-slate-900">
        {state.tables.length === 0 ? 'Build your first table' : 'Build another table'}
      </h2>
      {state.tables.length === 0 && previousDraftRevisionId ? (
        <div className="mt-3">
          <ConstructionUndoButton
            onUndo={() => void restorePreviousDraft()}
            disabled={pendingCommands > 0 || publishing || state.reconciliation === 'pending'}
          />
        </div>
      ) : null}
      <p className="mt-2 max-w-xl text-sm text-slate-600">
        Choose a populated record type. Loom will add its direct ID column and
        load a preview. The table name is optional.
      </p>
      <div className="mt-5 max-w-sm">
        <label className="block text-sm font-medium text-slate-800" htmlFor="first-table-name">
          Table name (optional)
        </label>
        <input
          id="first-table-name"
          value={firstTableName}
          onChange={(event) => setFirstTableName(event.currentTarget.value)}
          placeholder="Defaults to the record type"
          disabled={firstTableProgress.kind === 'running'}
          className="mt-1 w-full rounded-md border border-slate-300 bg-white px-3 py-2 text-sm outline-blue-500 focus:border-blue-500 disabled:bg-slate-100"
        />
      </div>
      <div className="mt-5 max-w-3xl">
        <RowRootPicker
          catalog={state.catalog}
          disabled={
            pendingCommands > 0 ||
            state.reconciliation === 'pending' ||
            firstTableProgress.kind === 'running'
          }
          onChoose={(nodeId) => void createFirstTableFromRoot(nodeId)}
        />
      </div>
      {firstTableProgress.kind === 'running' ? (
        <p role="status" className="mt-4 text-sm text-blue-800">
          {firstTableProgress.phase === 'checking'
            ? `Checking ${firstTableProgress.resourceType} fields…`
            : firstTableProgress.phase === 'creating'
              ? `Creating ${firstTableProgress.resourceType} table…`
              : firstTableProgress.phase === 'adding-id'
                ? 'Adding the ID column…'
                : 'Loading the preview…'}
        </p>
      ) : null}
      {state.tables.length > 0 ? (
        <div className="mt-4 flex justify-end">
          <button
            type="button"
            className="rounded-md border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50"
            disabled={firstTableProgress.kind === 'running'}
            onClick={() => {
              setTableCreatorOpen(false);
              setFirstTableName('');
            }}
          >
            Cancel
          </button>
        </div>
      ) : null}
    </section>
  );

  return (
    <main className="min-h-screen bg-slate-50 p-2 pb-10 text-slate-900 sm:p-3">
      {toolbarHost ? createPortal(toolbar, toolbarHost) : toolbar}
      {reviewOpen ? (
        <DatasetReviewPanel
          tables={state.tables}
          catalog={state.catalog}
          receipt={state.receipt}
          preview={state.preview}
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
        {pendingRowChange && table ? (
          <RowChangeRepairPanel
            unresolved={pendingRowChange.assessment.unresolved}
            catalog={state.catalog}
            table={table}
            disabled={rowChangeStatus.isLoading}
            onChoose={resolveRowChange}
            onCancel={() => setPendingRowChange(undefined)}
          />
        ) : null}
        {pendingRowChangePreview && table?.outputId === pendingRowChangePreview.outputId ? (
          <RowChangePreviewPanel
            candidateRoot={pendingRowChangePreview.assessment.candidateRootResourceType}
            status={pendingRowChangePreview.status}
            preview={pendingRowChangePreview.status === 'ready' ? pendingRowChangePreview.preview : undefined}
            error={pendingRowChangePreview.status === 'error' ? pendingRowChangePreview.error : undefined}
            disabled={pendingCommands > 0}
            onApply={() => void applyRowChangePreview()}
            onCancel={() => {
              rowChangePreviewRequest.current?.abort();
              setPendingRowChangePreview(undefined);
            }}
          />
        ) : null}
        {state.tables.length === 0 ? tableCreatorPanel : (
          <>
            <span key={suggestionIdentity} ref={suggestionHostRef} hidden />
            {tableCreatorOpen ? tableCreatorPanel : null}
            {table ? (
              <ConstructionWorkspace
                tables={state.tables.map((candidate) => ({
                  outputId: candidate.outputId,
                  title: candidate.title,
                }))}
                selectedOutputId={table.outputId}
                tableActionsDisabled={
                  pendingCommands > 0 ||
                  publishing ||
                  firstTableProgress.kind === 'running' ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                onSelectTable={(outputId) => {
                  selectConstructionHistory({ kind: 'source' });
                  selectTableFromUser(outputId);
                }}
                onNewTable={addTable}
                onDuplicateTable={duplicateTable}
                onDeleteTable={deleteSelectedTable}
                onRenameTable={renameTableById}
                onMoveTable={reorderTable}
                title={table.title}
                rowMeaning={rowMeaning}
                rowSetup={(
                  <RowDefinitionSettingsPanel
                    client={loomClient}
                    project={projectId}
                    explorerId={state.explorerId}
                    authResourcePath={authResourcePath}
                    snapshotToken={state.catalog.snapshotToken}
                    draftVersion={state.draftVersion}
                    draftDigest={state.draftDigest}
                    table={table}
                    currentRowMeaning={rowMeaning}
                    startingCollectionSummary={startingCollectionSummary}
                    renderRootSettings={(onRootChange) => (
                      <RowDefinitionPanel
                        catalog={state.catalog}
                        table={table}
                        disabled={rowChangeStatus.isLoading || pendingCommands > 0 || state.reconciliation === 'pending'}
                        onChange={onRootChange}
                      />
                    )}
                    startingCollectionSettings={(
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
                        onClear={() => void applyCommands([{
                          type: 'CLEAR_TABLE_POPULATION',
                          outputId: table.outputId,
                        }])}
                        onExclude={(ref, route) => void excludePopulationMember(ref, route)}
                        onApplyMemberRemoval={(proposalId) => applyCommands([{
                          type: 'APPLY_POPULATION_MEMBER_PROPOSAL',
                          outputId: table.outputId,
                          proposalId,
                        }])}
                      />
                    )}
                    selection={activePopulationSelection}
                    constructionHistory={persistedConstructionHistory}
                    disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                    relatedRows={relatedRowsAvailability}
                    reshapeRows={reshapeRowsAvailability}
                    onChooseRelatedRows={chooseRelatedRows}
                    onChooseReshape={chooseReshapeRows}
                    sourceCollectionAction={table.document.rootResourceType ? (
                      <SourceCollectionExpandEditor
                        key={JSON.stringify([
                          projectId,
                          state.explorerId,
                          authResourcePath ?? '',
                          state.catalog.snapshotToken,
                          state.draftVersion,
                          state.draftDigest,
                          table.outputId,
                        ])}
                        context={{
                          client: loomClient,
                          project: projectId,
                          explorerId: state.explorerId,
                          authResourcePath,
                          snapshotToken: state.catalog.snapshotToken,
                          draftVersion: state.draftVersion,
                          draftDigest: state.draftDigest,
                          outputId: table.outputId,
                          rowsKind: table.document.rows.kind,
                          onApply: (proposalId) => applyCommands([{
                            type: 'APPLY_ROW_DEFINITION_PROPOSAL',
                            outputId: table.outputId,
                            proposalId,
                          }]),
                        }}
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                      />
                    ) : undefined}
                    onChangeRootOccurrence={(nodeId, occurrenceId) => void changeTableRoot(nodeId, { rootOccurrenceId: occurrenceId })}
                    onEditConstructionStep={editConstructionStep}
                    onRemoveConstructionStep={removeConstructionStep}
                    onApply={(proposalId) => applyCommands([{
                      type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId: table.outputId, proposalId,
                    }])}
                  />
                )}
                onUndo={previousDraftRevisionId ? () => void restorePreviousDraft() : undefined}
                undoDisabled={
                  pendingCommands > 0 ||
                  publishing ||
                  state.reconciliation === 'pending' ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                previewRowCount={workspacePreviewIsCurrent ? workspacePreview?.rowCount : undefined}
                previewSampled={workspacePreviewIsCurrent ? workspacePreview?.sampled : undefined}
                previewColumnCount={workspacePreviewIsCurrent ? workspacePreview?.columns.length : undefined}
                history={persistedConstructionHistory.length > 0 ? {
                  steps: persistedConstructionHistory,
                  selected: constructionHistorySelection,
                  disabled: pendingCommands > 0 || publishing || constructionLifecycle.proposal.status === 'applying',
                  onSelect: selectConstructionHistory,
                  onEditStep: editConstructionStep,
                  onRemoveStep: removeConstructionStep,
                } : undefined}
                actionsDisabled={
                  !table.document.rootResourceType ||
                  pendingCommands > 0 ||
                  state.reconciliation === 'pending' ||
                  Boolean(pendingRowChange) ||
                  Boolean(pendingRowChangePreview) ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                addColumnsDisabled={addColumnsPreviewPending}
                activeFamily={activeConstructionFamily}
                onSelectFamily={selectConstructionFamily}
                preview={
                  <>
                    {!candidatePreview && currentPreviewStatus === 'stale' ? (
                      <p
                        role="status"
                        data-testid="construction-preview-stale-notice"
                        className="border-b border-amber-200 bg-amber-50 px-4 py-2 text-xs text-amber-950"
                      >
                        Showing the last successful preview for this table.
                        The table refreshes automatically when your changes are saved.
                      </p>
                    ) : null}
                    <ConstructionColumnSelection
                      columns={selectableColumns}
                      selectedColumnIds={selectedColumnIds}
                      disabled={!table.document.rootResourceType || pendingCommands > 0 || !currentStage}
                      onToggleColumn={(columnId) =>
                        setColumnSelection((current) => {
                          const currentIds =
                            current.kind === 'selected' &&
                            current.outputId === table.outputId
                              ? current.columnIds
                              : [];
                          const nextIds = currentIds.includes(columnId)
                            ? currentIds.filter((candidate) => candidate !== columnId)
                            : [...currentIds, columnId];
                          return nextIds.length > 0
                            ? {
                                kind: 'selected',
                                outputId: table.outputId,
                                columnIds: nextIds,
                              }
                            : { kind: 'empty' };
                        })
                      }
                      onClear={() => setColumnSelection({ kind: 'empty' })}
                      onOpenFamily={selectConstructionFamily}
                    />
                    {candidatePreview ? (
                      <ConstructionProposalPreview
                        preview={candidatePreview}
                        presentationTable={proposalPresentationTable}
                      />
                    ) : (
                      <PreviewTable
                        preview={tablePreview}
                        previewErrorMessage={automaticPreviewFailureMessage}
                        table={table}
                        limit={previewLimit}
                        onLimitChange={(limit) => {
                          setPreviewLimit(limit);
                        }}
                        onColumnChange={(change) => applyPresentationChanges([change])}
                        onColumnsChange={applyPresentationChanges}
                        candidateForColumn={candidateForPreviewColumn}
                        onTransformationChange={(column, transformationChange) =>
                          table &&
                          void applyCommands([{
                            type: 'UPDATE_COLUMN_TRANSFORMATION',
                            outputId: table.outputId,
                            column,
                            transformationChange,
                          }])
                        }
                        onRowValuePolicyChange={(physicalColumn, rowValuePolicy) =>
                          table &&
                          void applyCommands([{
                            type: 'UPDATE_COLUMN_ROW_VALUE_POLICY',
                            outputId: table.outputId,
                            column: physicalColumn,
                            rowValuePolicy,
                          }])
                        }
                        disabled={pendingCommands > 0 || state.reconciliation === 'pending' || publishing}
                        onRemoveColumn={(column) => void applyCommands([{ type: 'REMOVE_COLUMN', outputId: table.outputId, column }])}
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
                editor={workspaceEditor}
                previewStatus={workspacePreviewStatus}
                previewReceiptId={workspacePreview?.receiptId}
                previewOutputId={workspacePreview?.outputId ?? table?.outputId}
                proposalId={workspacePreviewProposalId}
                draftVersion={state.draftVersion}
                draftDigest={state.draftDigest}
              />
            ) : null}
            {!activeOperation && !workspaceEditor ? <details
              className="w-fit max-w-full rounded-md border border-slate-200 bg-white shadow-sm open:w-full"
              open={sourceSetupOpen || !table?.document.rootResourceType}
              onToggle={(event) => setSourceSetupOpen(event.currentTarget.open)}
              data-testid="construction-source-setup"
            >
              <summary className="cursor-pointer list-none px-3 py-2 text-xs font-semibold text-blue-800 marker:hidden">
                <span>Advanced source setup</span>
              </summary>
              <div className="space-y-3 border-t border-slate-200 p-3">
            <section className="rounded-xl border border-slate-200 bg-white px-4 py-3 shadow-sm">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h2 className="text-base font-semibold text-slate-900">Build your features</h2>
                  <p className="text-xs text-slate-600">
                    Choose rows, then find fields or coded concepts. Open the graph when you need route control.
                  </p>
                </div>
                <div
                  className="inline-flex rounded-md border border-slate-300 bg-slate-50 p-1"
                  aria-label="Feature authoring view"
                >
                  <button
                    type="button"
                    aria-pressed={featureMode === 'catalog'}
                    onClick={() => setFeatureMode('catalog')}
                    className={`rounded px-3 py-1.5 text-sm font-semibold ${featureMode === 'catalog' ? 'bg-white text-blue-800 shadow-sm' : 'text-slate-600 hover:text-slate-900'}`}
                  >
                    Concept catalog
                  </button>
                  <button
                    type="button"
                    aria-pressed={featureMode === 'graph'}
                    onClick={() => setFeatureMode('graph')}
                    className={`rounded px-3 py-1.5 text-sm font-semibold ${featureMode === 'graph' ? 'bg-white text-blue-800 shadow-sm' : 'text-slate-600 hover:text-slate-900'}`}
                  >
                    Advanced graph
                  </button>
                </div>
              </div>
            </section>
            <div className="grid items-stretch gap-3 xl:grid-cols-[minmax(0,1.25fr)_minmax(28rem,0.95fr)]">
              {featureMode === 'catalog' ? (
                table?.document.rootResourceType ? (
                  <ConceptCatalog
                  key={`${ownerKey}:${state.catalog.snapshotToken}:${table.document.rootResourceType}`}
                  project={projectId}
                  explorerId={state.explorerId}
                  authResourcePath={authResourcePath}
                  snapshotToken={state.catalog.snapshotToken}
                  outputId={table.outputId}
                  rowRoot={table.document.rootResourceType}
                  schemaDiscoveryEnabled={sourceSetupOpen || !table?.document.rootResourceType}
                  catalog={state.catalog}
                  sourceProjectionAvailability={sourceAvailability}
                  relatedSourceAvailability={relatedSourceAvailability}
                  suppressUnavailableNotices={hasUnsupportedSavedSourceColumns}
                  disabledReason={sourceSelectionDisabledReason}
                  disabled={pendingCommands > 0 || state.reconciliation === 'pending' || addColumnsPreviewPending}
                  pairedColumnSuggestion={pairedColumnSuggestion}
                  onPairedColumnSuggestionHandled={(requestId) =>
                    setPairedColumnSuggestion((current) =>
                      current?.requestId === requestId ? undefined : current,
                    )
                  }
                  onAddSelected={addSelectedFeatures}
                  onInspectRouteCoverage={inspectRelatedRouteCoverage}
                  />
                ) : (
                  <RowRootPicker
                    catalog={state.catalog}
                    disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                    onChoose={(nodeId) => {
                      if (!table) return;
                      void applyCommands([{
                        type: 'CREATE_TABLE',
                        title: table.title,
                        rootNodeId: nodeId,
                      }]);
                    }}
                  />
                )
              ) : (
              <GuidedGraphWorkspace
                catalog={state.catalog}
                table={table}
                selectedOccurrenceId={state.selectedOccurrenceId}
                disabled={
                  rowChangeStatus.isLoading ||
                  addColumnsPreviewPending ||
                  (state.reconciliation === 'pending' &&
                    Boolean(table?.document.rootResourceType))
                }
                onSelectOccurrence={(occurrenceId) =>
                  dispatch({ type: 'selectOccurrence', occurrenceId })
                }
                onSetBase={(nodeId) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'CREATE_TABLE',
                      title: table.title,
                      rootNodeId: nodeId,
                    },
                  ])
                }
                onChangeBase={(nodeId) => void changeTableRoot(nodeId)}
                onAppendEdge={(parentOccurrenceId, edgeId) => {
                  if (!table) return;
                  const edge = state.catalog.edges.find(
                    (candidate) => candidate.edgeId === edgeId,
                  );
                  if (!edge) return;
                  void applyCommands([
                    {
                      type: 'ADD_ROUTE',
                      outputId: table.outputId,
                      parentOccurrenceId,
                      edgeId,
                    },
                  ]);
                }}
                onChangeEdge={(occurrenceId, edgeId) => {
                  if (!table) return;
                  void applyCommands([
                    {
                      type: 'UPDATE_ROUTE_EDGE',
                      outputId: table.outputId,
                      occurrenceId,
                      edgeId,
                    },
                  ]);
                }}
                onChangeMatchMode={(occurrenceId, matchMode) => {
                  if (!table) return;
                  void applyCommands([
                    {
                      type: 'SET_ROUTE_MATCH_MODE',
                      outputId: table.outputId,
                      occurrenceId,
                      matchMode,
                    },
                  ]);
                }}
                onTruncate={(occurrenceId) => {
                  if (!table) return;
                  const subtree = routeNode(table.document.route, occurrenceId);
                  if (!subtree) return;
                  const ids = routeSubtreeOccurrenceIds(subtree);
                  const columnCount = table.document.columns.filter((column) =>
                    ids.has(column.occurrenceId),
                  ).length;
                  if (
                    !window.confirm(
                      `Remove this local branch (${ids.size} occurrence${ids.size === 1 ? '' : 's'}, ${columnCount} column${columnCount === 1 ? '' : 's'})?`,
                    )
                  )
                    return;
                  void applyCommands([
                    {
                      type: 'REMOVE_ROUTE',
                      outputId: table.outputId,
                      occurrenceId,
                    },
                  ]);
                }}
                onTableToolbarHostChange={setTableToolbarHost}
              />
              )}
              <div className="min-w-0 space-y-3">
              <ColumnSelector
                catalog={state.catalog}
                interpretationContext={interpretationContext}
                table={table}
                occurrenceId={state.selectedOccurrenceId}
                focusColumn={
                  reviewFocusColumn && reviewFocusColumn.outputId === table?.outputId
                    ? reviewFocusColumn.column
                    : focusedFeature && focusedFeature.table.outputId === table?.outputId
                      ? focusedFeature.column.column
                      : undefined
                }
                disabled={!occurrence}
                showAvailable={featureMode === 'graph'}
                loadingCandidates={suggestionsStatus.isLoading}
                onAdd={(candidate, displayName, initialPresentation) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'ADD_COLUMN',
                      outputId: table.outputId,
                      occurrenceId: state.selectedOccurrenceId,
                      candidateId: candidate.candidateId,
                      projectionMode: candidate.defaultProjectionMode,
                      initialPresentation,
                      title: displayName,
                    },
                  ])
                }
                onAddAll={(candidates) => {
                  if (!table) return;
                  void applyCommands(
                    candidates.map((candidate) => ({
                      type: 'ADD_COLUMN' as const,
                      outputId: table.outputId,
                      occurrenceId: state.selectedOccurrenceId,
                      candidateId: candidate.candidateId,
                      projectionMode: candidate.defaultProjectionMode,
                      initialPresentation: 'TABLE',
                      title: candidate.label,
                    })),
                  );
                }}
                onAddSource={(source, title) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'ADD_COLUMN_SOURCE',
                      outputId: table.outputId,
                      occurrenceId: state.selectedOccurrenceId,
                      source,
                      title,
                    },
                  ])
                }
                onChange={(column) => applyPresentationChanges([{ kind: 'AUTHORED_COLUMN', column }])}
                onColumnsChange={(columns) => applyPresentationChanges(columns.map((column) => ({ kind: 'AUTHORED_COLUMN', column })))}
                onPresentationChanges={applyPresentationChanges}
                onConstructionOutputChange={(stepId, column) =>
                  applyPresentationChanges([{ kind: 'CONSTRUCTION_OUTPUT', stepId, column }])
                }
                onRemove={(column) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'REMOVE_COLUMN',
                      outputId: table.outputId,
                      column,
                    },
                  ])
                }
                onInspectSource={(column) =>
                  loomClient.inspectColumnSource({
                    project: projectId,
                    explorerId: state.explorerId,
                    authResourcePath,
                    snapshotToken: state.catalog.snapshotToken,
                    outputId: table?.outputId ?? '',
                    column: column.column,
                    requestId: `column-source-${window.crypto.randomUUID()}`,
                  })
                }
                onEditInGraph={(column) => {
                  setFeatureMode('graph');
                  dispatch({
                    type: 'selectOccurrence',
                    occurrenceId: column.occurrenceId,
                  });
                }}
                onSourceChange={(column, source) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'UPDATE_COLUMN_SOURCE',
                      outputId: table.outputId,
                      column,
                      source,
                    },
                  ])
                }
                onContributorChange={(column, contributor) =>
                  table &&
                  void applyCommands([
                    contributor
                      ? {
                          type: 'SET_COLUMN_CONTRIBUTOR',
                          outputId: table.outputId,
                          column,
                          contributor,
                        }
                      : {
                          type: 'CLEAR_COLUMN_CONTRIBUTOR',
                          outputId: table.outputId,
                          column,
                        },
                  ])
                }
                onTransformationChange={(column, transformationChange) =>
                  table &&
                  void applyCommands([{
                    type: 'UPDATE_COLUMN_TRANSFORMATION',
                    outputId: table.outputId,
                    column,
                    transformationChange,
                  }])
                }
                />
                {featureMode === 'graph' && table && occurrence ? (
                  selectedRouteContext ? (
                    <ConceptCatalog
                      key={`${ownerKey}:${state.catalog.snapshotToken}:${table.outputId}:${occurrence.id}`}
                      project={projectId}
                      explorerId={state.explorerId}
                      authResourcePath={authResourcePath}
                      snapshotToken={state.catalog.snapshotToken}
                      outputId={table.outputId}
                      rowRoot={table.document.rootResourceType}
                      resourceType={occurrence.resourceType}
                      routeContext={selectedRouteContext}
                      layout="panel"
                      catalog={state.catalog}
                      sourceProjectionAvailability={sourceAvailability}
                      relatedSourceAvailability={relatedSourceAvailability}
                      suppressUnavailableNotices={hasUnsupportedSavedSourceColumns}
                      disabledReason={sourceSelectionDisabledReason}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onAddSelected={addSelectedFeatures}
                      onInspectRouteCoverage={inspectRelatedRouteCoverage}
                    />
                  ) : (
                    <p role="alert" className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900">
                      Loom could not identify the saved route for this graph occurrence.
                    </p>
                  )
                ) : null}
              </div>
            </div>
            {table ? (
              <section className="mt-3 rounded-lg border border-indigo-100 bg-indigo-50/20 p-3">
                <h2 className="text-base font-semibold text-indigo-950">Feature meanings</h2>
                <p className="mt-1 text-sm text-slate-600">Keep the current inline meaning, or review an exact reusable interpretation before applying it.</p>
                <div className="mt-2 space-y-2">
                  {table.document.columns.filter((column) => column.occurrenceId === state.selectedOccurrenceId).map((column) => (
                    <InterpretationPanel
                      key={column.column}
                      project={projectId}
                      explorerId={state.explorerId}
                      authResourcePath={authResourcePath}
                      outputId={table.outputId}
                      column={column}
                      contextState={interpretationPanelContextState}
                      snapshotToken={state.catalog.snapshotToken}
                      expectedDraftVersion={state.draftVersion}
                      expectedDraftDigest={state.draftDigest}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onApply={(command) => applyCommands([command])}
                      onApplied={() => setMessage(undefined)}
                      onContextRefresh={() => setInterpretationContextRefreshVersion((version) => version + 1)}
                    />
                  ))}
                </div>
              </section>
            ) : null}
            {table &&
            !table.document.construction &&
            activeConstructionFamily !== 'CALCULATE' &&
            activeConstructionFamily !== 'RESHAPE' ? (
              <details className="rounded-lg border border-slate-200 bg-white p-3">
                <summary className="cursor-pointer text-sm font-semibold text-slate-800">
                  Table shape settings
                </summary>
                <TableShapeSettingsPanel
                  client={loomClient}
                  project={projectId}
                  explorerId={state.explorerId}
                  authResourcePath={authResourcePath}
                  snapshotToken={state.catalog.snapshotToken}
                  draftVersion={state.draftVersion}
                  draftDigest={state.draftDigest}
                  table={table}
                  disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                  onApply={(proposalId) =>
                    applyCommands([
                      {
                        type: 'APPLY_TABLE_SHAPE_PROPOSAL',
                        outputId: table.outputId,
                        proposalId,
                      },
                    ])
                  }
                />
              </details>
            ) : null}
            {state.receipt && state.reconciliation === 'resolved' && table ? (
              <DataframeContractPanel
                receipt={state.receipt}
                outputId={table.outputId}
              />
            ) : null}
              </div>
            </details> : null}
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
    props.explorerId || 'default',
  );
  return <BuilderWorkspaceContent key={ownerKey} {...props} />;
};

export default BuilderWorkspace;
