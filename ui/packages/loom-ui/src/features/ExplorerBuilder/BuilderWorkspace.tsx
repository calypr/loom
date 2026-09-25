import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useAssessExplorerRowChangeMutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useGetExplorerBuilderStateV2Query,
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
  useLoomClient,
} from '../../react';
import type { ResourceRef, SelectionRevision } from '../../selection';
import { canonicalProject, type ConfiguredColumnContextResponse, type ExplorerAuthoringApiError } from '../../api';
import type {
  ExplorerAuthoringDiagnostic,
  ExplorerBuilderCatalog,
  ExplorerBuilderCommand,
  ExplorerBuilderCompileResult,
  ExplorerBuilderState,
  ConstructionRouteStep,
  Construction,
  ConstructionOperation,
  ConstructionStep,
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
import { PreviewTable } from './components/PreviewTable';
import { DataframeContractPanel } from './components/DataframeContractPanel';
import { PopulationPanel } from './components/PopulationPanel';
import { RowChangeRepairPanel } from './components/RowChangeRepairPanel';
import { RowDefinitionPanel } from './components/RowDefinitionPanel';
import { RowDefinitionSettingsPanel } from './components/RowDefinitionSettingsPanel';
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
  constructionOperationFamilies,
  type ConstructionHistorySelection,
  type ConstructionOperationFamily,
} from './constructionWorkspace/ConstructionWorkspace';
import {
  ConstructionColumnSelection,
  type ConstructionSelectableColumn,
} from './constructionWorkspace/ConstructionColumnSelection';
import {
  ConstructionProposalPanel,
} from './constructionWorkspace/ConstructionProposalPanel';
import { ConstructionProposalPreview } from './constructionWorkspace/ConstructionProposalPreview';
import {
  constructionHistorySteps,
} from './constructionWorkspace/constructionHistory';
import {
  sourceProjectionAvailability,
} from './constructionWorkspace/sourceProjectionAvailability';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
} from './constructionWorkspace/useConstructionLifecycle';
import { ConstructionOperationEditor } from './constructionOperations/ConstructionOperationEditor';
import { ConstructionReshapeEditor } from './constructionOperations/ConstructionReshapeEditor';
import {
  RelatedSourceStepEditor,
  type RelatedSourceStep,
} from './constructionOperations/RelatedSourceStepEditor';

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

const constructionInputStageFor = (
  construction: Construction | undefined,
  stepId: string,
): string => {
  const index = construction?.steps.findIndex((step) => step.id === stepId) ?? -1;
  return index <= 0
    ? constructionSourceStageId
    : construction?.steps[index - 1]?.id ?? constructionSourceStageId;
};

const constructionAppendStageFor = (construction: Construction | undefined): string =>
  construction?.steps.at(-1)?.id ?? constructionSourceStageId;

const editableConstructionFamily = (
  operation: ConstructionOperation,
): Extract<ConstructionOperationFamily, 'KEEP_ROWS' | 'CALCULATE' | 'RESHAPE'> | undefined => {
  switch (operation.kind) {
    case 'FILTER': return 'KEEP_ROWS';
    case 'DERIVE': return 'CALCULATE';
    case 'PIVOT':
    case 'UNPIVOT':
    case 'GROUP':
    case 'EXPAND':
    case 'RELATED_EXPAND': return 'RESHAPE';
    case 'RELATED_SOURCE': return undefined;
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

type InterpretationContextLoad =
  | { readonly key: string; readonly status: 'loading' }
  | { readonly key: string; readonly status: 'ready'; readonly response: ConfiguredColumnContextResponse }
  | { readonly key: string; readonly status: 'error'; readonly message: string };

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
                })
              : emptyBuilderState(projectId);
        return {
          key: builderDataKey,
          value: builderAuthoringReducer(base, action),
        };
      });
    },
    [authResourcePath, builderDataKey, projectId, selectedExplorerId],
  );
  const [message, setMessage] = useState<string>();
  const [pendingRowChange, setPendingRowChange] =
    useState<PendingRowChange>();
  const [lastPublished, setLastPublished] = useState<{
    readonly ownerKey: string;
    readonly draftDigest: string;
  }>();
  const [pendingCommands, setPendingCommands] = useState(0);
  const [activePopulationSelection, setActivePopulationSelection] = useState(populationSelection);
  const [activePopulationSelectionLoading, setActivePopulationSelectionLoading] = useState(false);
  const [populationVariantError, setPopulationVariantError] = useState<string>();
  const [populationVariantPending, setPopulationVariantPending] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [firstTableName, setFirstTableName] = useState('');
  const [previewLimit, setPreviewLimit] = useState<PreviewLimit>(25);
  const [featureMode, setFeatureMode] = useState<'catalog' | 'graph'>('catalog');
  const [activeConstructionFamily, setActiveConstructionFamily] =
    useState<ConstructionOperationFamily>();
  const [addColumnsSource, setAddColumnsSource] = useState<{
    readonly context: string;
    readonly key: string;
  }>();
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
  const commandQueue = useRef<Promise<void>>(Promise.resolve());
  const serverDraft = useRef({ version: 0, digest: '' });
  const suggestionRequestKey = useRef('');
  const latestState = useRef(state);
  latestState.current = state;
  const [interpretationContextRefreshVersion, setInterpretationContextRefreshVersion] = useState(0);
  const [interpretationContextLoad, setInterpretationContextLoad] = useState<InterpretationContextLoad>();

  const serverDraftKey = useRef('');
  if (builder.data && serverDraftKey.current !== builderDataKey) {
    serverDraft.current = {
      version: builder.data.draftVersion,
      digest: builder.data.draftDigest,
    };
    serverDraftKey.current = builderDataKey;
  }

  const interpretationContextKey = state.catalog.snapshotToken && state.draftVersion > 0 && state.draftDigest
    ? JSON.stringify([
        ownerKey,
        state.catalog.snapshotToken,
        state.draftVersion,
        state.draftDigest,
        interpretationContextRefreshVersion,
      ])
    : '';
  useEffect(() => {
    if (!interpretationContextKey) {
      setInterpretationContextLoad(undefined);
      return;
    }
    const controller = new AbortController();
    let active = true;
    setInterpretationContextLoad({ key: interpretationContextKey, status: 'loading' });
    void loomClient.resolveConfiguredColumnContexts({
      project: projectId,
      explorerId: selectedExplorerId,
      authResourcePath,
      snapshotToken: state.catalog.snapshotToken,
      expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest,
    }, controller.signal).then(
      (response) => {
        if (!active) return;
        setInterpretationContextLoad({ key: interpretationContextKey, status: 'ready', response });
      },
    ).catch((error: unknown) => {
      if (!active || controller.signal.aborted) return;
      setInterpretationContextLoad({
        key: interpretationContextKey,
        status: 'error',
        message: error instanceof Error ? error.message : 'Loom could not load interpretation context.',
      });
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [authResourcePath, interpretationContextKey, loomClient, projectId, selectedExplorerId, state.catalog.snapshotToken, state.draftDigest, state.draftVersion]);

  const interpretationPanelContextState: InterpretationContextState = !interpretationContextKey
    ? { status: 'error', message: 'The saved draft identity is not available.' }
    : interpretationContextLoad?.key !== interpretationContextKey || interpretationContextLoad.status === 'loading'
      ? { status: 'loading' }
      : interpretationContextLoad.status === 'ready'
        ? { status: 'ready', response: interpretationContextLoad.response }
        : { status: 'error', message: interpretationContextLoad.message };
  const interpretationContext = interpretationPanelContextState.status === 'ready'
    ? interpretationPanelContextState.response
    : undefined;

  const selectExplorer = (nextExplorerId: string) => {
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
    [builderDataKey, projectId, selectedExplorerId],
  );

  const incomplete = state.tables.some(
    (table) => !table.document.rootResourceType,
  );

  const applyCommands = useCallback(
    (commands: ReadonlyArray<ExplorerBuilderCommand>) => {
      compileGeneration.current += 1;
      previewGeneration.current += 1;
      activeCompile.current?.abort();
      activePreview.current?.abort();
      setPendingCommands((value) => value + 1);
      const run = commandQueue.current.then(async () => {
        const current = latestState.current;
        const commandId = window.crypto.randomUUID();
        try {
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
          serverDraft.current = {
            version: value.draftVersion,
            digest: value.draftDigest,
          };
          setRestorableDraft({
            ownerKey: builderOwnerKeyFor(projectId, authResourcePath, current.explorerId),
            revisionId: value.previousDraftRevisionId,
          });
          serverDraftKey.current = builderDataKey;
          dispatch({ type: 'commandsApplied', value });
          setMessage(undefined);
          return true;
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
            return false;
          }
          if (apiError.code !== 'CLIENT_CANCELLED') {
            dispatch({
              type: 'repair',
              diagnostics: diagnosticsFromError(apiError),
            });
          }
          return false;
        } finally {
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
      dispatch,
      projectId,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  useDirtyBeforeUnload(state.dirty);

  const table = selectedTable(state);
  const construction = table?.document.construction;
  const editingConstructionStep = construction?.steps.find(
    (step) => step.id === editingConstructionStepId,
  );
  const capabilitiesRequest = table?.document.rootResourceType &&
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
          : constructionAppendStageFor(construction),
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
  const rowMeaning = !table?.document.rootResourceType
    ? 'Choose what one row represents to start this table.'
    : table.document.output.rowLabel?.trim() ||
      state.catalog.nodes.find(
        (node) => node.resourceType === table.document.rootResourceType,
      )?.rowGrain ||
      (table.document.rows.kind === 'RECORDS'
        ? 'One row per source record.'
        : table.document.rows.kind === 'GROUPS'
          ? 'One row per group.'
          : 'One row per expanded value.');
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
    dispatch({ type: 'selectTable', outputId: target.outputId });
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
  }, [dispatch]);
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
  useEffect(() => {
    setActivePopulationSelection(populationSelection);
    setPopulationVariantError(undefined);
  }, [handedOffPopulationSelectionID]);
  useEffect(() => {
    const attachedSelectionID = table?.document.population?.selectionRevisionId;
    if (
      handedOffPopulationSelectionID ||
      !attachedSelectionID ||
      activePopulationSelection?.id === attachedSelectionID
    ) {
      setActivePopulationSelectionLoading(false);
      return;
    }
    const controller = new AbortController();
    setActivePopulationSelectionLoading(true);
    setPopulationVariantError(undefined);
    void loomClient.getSelection({
      project: projectId,
      explorerId: state.explorerId,
      authResourcePath,
      selectionRevision: attachedSelectionID,
      limit: 1,
    }, controller.signal).then(
      (page) => setActivePopulationSelection(page.revision),
      (error: unknown) => {
        if (!controller.signal.aborted) setPopulationVariantError(error instanceof Error ? error.message : 'Loom could not load the attached collection.');
      },
    ).finally(() => {
      if (!controller.signal.aborted) setActivePopulationSelectionLoading(false);
    });
    return () => controller.abort();
  }, [activePopulationSelection?.id, authResourcePath, handedOffPopulationSelectionID, loomClient, projectId, state.explorerId, table?.document.population?.selectionRevisionId]);
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
  const addSourceOptions = useMemo(
    () => catalogSourceOptions(
      state.catalog,
      table?.document.rootResourceType ?? '',
      selectedRouteContext?.nodeId,
    ),
    [state.catalog, table?.document.rootResourceType, selectedRouteContext?.nodeId],
  );
  const addSourceContext = JSON.stringify([
    state.explorerId,
    state.catalog.snapshotToken,
    table?.outputId ?? '',
    selectedRouteContext?.occurrenceId ?? '',
  ]);
  const selectedAddSource = addSourceOptions.find(
    (source) => source.key === (addColumnsSource?.context === addSourceContext
      ? addColumnsSource.key
      : addSourceOptions.find((option) => option.sourceNodeId === selectedRouteContext?.nodeId)?.key),
  ) ?? addSourceOptions[0];
  const addSourceRouteContext = selectedAddSource?.sourceNodeId === selectedRouteContext?.nodeId
    ? selectedRouteContext
    : undefined;
  const addSelectedFeatures = async (
    selections: ReadonlyArray<CatalogChoiceIntent>,
  ) => {
    if (!table) throw new Error('Choose a table before adding features.');
    const relatedSelections = selections.filter((selection) => selection.relatedSource);
    if (relatedSelections.length > 0) {
      if (relatedSelections.length !== 1 || selections.length !== 1) {
        throw new Error('Add one related field at a time so Loom can preview its exact route.');
      }
      const relatedSource = relatedSelections[0]?.relatedSource;
      if (!relatedSource) return;
      const capabilities = constructionLifecycle.capabilities;
      if (capabilities.status !== 'ready') {
        throw new Error('Loom is still checking whether this stage can add a related field.');
      }
      const { selectedStage, baseConstruction } = capabilities.response;
      if (
        editingConstructionStep ||
        selectedStage.id !== constructionAppendStageFor(baseConstruction)
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
      const form = relatedSelections[0]!.constructionChoice.form;
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
              ...(relatedSelections[0]?.contributorPredicate
                ? { predicate: relatedSelections[0].contributorPredicate }
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
            label: form === 'COUNT'
              ? `Count of related ${source.resourceType} records`
              : form === 'PRESENCE'
                ? `Has related ${source.resourceType} record`
                : candidate.label.trim() || candidate.fieldPath,
            type: form === 'COUNT' ? 'integer' : form === 'PRESENCE' ? 'boolean' : candidate.logicalType,
          },
        ],
      } satisfies ConstructionStep;
      constructionLifecycle.onCandidateChange({
        candidateConstruction: {
          ...baseConstruction,
          steps: [...baseConstruction.steps, step],
        },
        changedStepId: step.id,
      });
      return;
    }
    const applied = await applyCommands(selections.map((selection) => ({
      type: 'APPLY_CONSTRUCTION_CHOICE',
      outputId: table.outputId,
      constructionChoice: selection.constructionChoice,
      ...(selection.title ? { title: selection.title } : {}),
    } satisfies ExplorerBuilderCommand)));
    if (!applied) {
      throw new Error('Loom did not add the selected features. Review the Builder message and try again.');
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
      setActivePopulationSelection(variant);
    } catch (error) {
      setPopulationVariantError(error instanceof Error ? error.message : 'Loom could not create the revised collection.');
    } finally {
      setPopulationVariantPending(false);
    }
  }, [activePopulationSelection, applyCommands, authResourcePath, loomClient, projectId]);

  const changeTableRoot = useCallback(
    async (nodeId: string, resolution: RowChangeResolution = {}) => {
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
        const target = current.catalog.nodes.find(
          (node) => node.nodeId === nodeId,
        )?.resourceType ?? 'the selected resource';
        const featureCount = assessment.preservedFeatureKeys.length;
        if (!window.confirm(
          `Make each ${target} one row? Loom can preserve ${featureCount} configured ${featureCount === 1 ? 'feature' : 'features'}, the selected population, filters, and actions.`,
        )) return;
        await applyCommands([{
          type: 'APPLY_TABLE_ROOT_REBASE',
          rowChange: assessment.proposal,
        }]);
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
      applyCommands,
      assessRowChange,
      authResourcePath,
      projectId,
      refetchBuilder,
      syncBuilderData,
    ],
  );

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
  const busy =
    pendingCommands > 0 ||
    reconcileStatus.isLoading ||
    previewStatus.isLoading ||
    constructionProposalBusy ||
    publishing ||
    createStatus.isLoading ||
    deleteStatus.isLoading;
  const blockingDiagnostics = state.diagnostics.some(
    (diagnostic) => diagnostic.severity === 'error',
  );
  const hasVisibleSelectedColumn = Boolean(
    table?.document.columns.some(
      (column) => column.table?.visible ?? Boolean(column.table),
    ),
  );
  const previewDisabled =
    !table?.document.rootResourceType ||
    !hasVisibleSelectedColumn ||
    blockingDiagnostics ||
    constructionLifecycle.proposal.status !== 'idle';
  const publishDisabled =
    (lastPublished?.ownerKey === ownerKey &&
      lastPublished.draftDigest === state.draftDigest) ||
    incomplete ||
    blockingDiagnostics ||
    constructionLifecycle.proposal.status !== 'idle' ||
    state.tables.some((candidate) => candidate.document.columns.length === 0);

  const addTableNamed = (value: string) => {
    const title = value.trim();
    if (!title) return;
    const outputId = opaqueId('output');
    dispatch({
      type: 'addTable',
      table: {
        outputId,
        tabId: opaqueId('tab'),
        title,
        document: {
          kind: 'ExplorerBuilderDocument',
          output: { id: outputId, title },
          rootResourceType: '',
          route: { occurrenceId: 'base', resourceType: '' },
          rows: { kind: 'RECORDS', records: {} },
          columns: [],
        },
      },
    });
  };
  const addTable = () => {
    const title = window.prompt('Table name')?.trim();
    if (title) addTableNamed(title);
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
    if (!table || state.tables.length <= 1) return;
    if (!window.confirm(`Delete ${table.title}?`)) return;
    if (table.document.rootResourceType) {
      void applyCommands([
        { type: 'DELETE_TABLE', outputId: table.outputId },
      ]);
      return;
    }
    dispatch({ type: 'removeTable', outputId: table.outputId });
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
  const reconcileCurrent = useCallback(async (): Promise<
    ExplorerBuilderCompileResult | undefined
  > => {
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
    dispatch({ type: 'compiling' });
    for (;;) {
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
      try {
        const value = await request.unwrap();
        if (generation !== compileGeneration.current) return undefined;
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
          generation !== compileGeneration.current ||
          apiError.code === 'CLIENT_CANCELLED'
        )
          return undefined;
        if (isDraftDesynchronized(apiError.code)) {
          const refreshed = await refetchBuilder({ reload: true });
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
          if (!refreshed.data) return undefined;
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
        if (generation === compileGeneration.current)
          activeCompile.current = undefined;
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
    async (request: PreviewRequest, receiptId: string) => {
      const generation = ++previewGeneration.current;
      activePreview.current?.abort();
      let activeRequest = request;
      let activeReceiptId = receiptId;
      const limit = request.limit;
      let transientRetries = 0;
      for (;;) {
        if (generation !== previewGeneration.current) return;
        const previewRequest = previewBuilder({
          project: projectId,
          explorerId: latestState.current.explorerId,
          authResourcePath,
          receiptId: activeReceiptId,
          outputId: activeRequest.outputId,
          limit,
        });
        activePreview.current = previewRequest;
        try {
          const value = await previewRequest.unwrap();
          if (
            generation !== previewGeneration.current ||
            value.receiptId !== activeReceiptId
          )
            return;
          dispatch({ type: 'preview', value });
          setMessage(undefined);
          return;
        } catch (error) {
          const apiError = error as ExplorerAuthoringApiError;
          if (
            generation !== previewGeneration.current ||
            apiError.code === 'CLIENT_CANCELLED'
          )
            return;
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
            const receipt = await reconcileCurrent();
            if (generation !== previewGeneration.current) return;
            if (!receipt) return;
            activeReceiptId = receipt.receiptId;
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
            if (generation !== previewGeneration.current) return;
            if (!refreshed.data) return;
            latestState.current = builderAuthoringReducer(latestState.current, {
              type: 'catalogRefreshed',
              catalog: refreshed.data.catalog,
            });
            syncBuilderData(refreshed.data, 'catalog');
            const receipt = await reconcileCurrent();
            if (generation !== previewGeneration.current) return;
            if (!receipt) return;
            activeReceiptId = receipt.receiptId;
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
          return;
        } finally {
          if (activePreview.current === previewRequest)
            activePreview.current = undefined;
        }
      }
    },
    [
      authResourcePath,
      dispatch,
      previewBuilder,
      projectId,
      reconcileCurrent,
      refetchBuilder,
      syncBuilderData,
    ],
  );

  const preview = async (limit: PreviewLimit = previewLimit) => {
    if (!table || previewDisabled) return;
    const request = {
      outputId: table.outputId,
      limit,
      receiptRefreshes: 0,
    };
    const receipt =
      state.receipt && state.reconciliation === 'resolved'
        ? state.receipt
        : await reconcileCurrent();
    if (receipt) await executePreview(request, receipt.receiptId);
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
        Loom’s V2 Builder state could not be loaded. This Builder has no V1
        fallback.
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
      onSelectTable={(outputId) => dispatch({ type: 'selectTable', outputId })}
      onRenameTable={renameTable}
      onNewTable={addTable}
      onDuplicateTable={duplicateTable}
      onDeleteTable={deleteSelectedTable}
      onReorderTable={reorderTable}
      onPreview={() => void preview()}
      onReview={() => setReviewOpen((open) => !open)}
      reviewExpanded={reviewOpen}
      onPublish={() => void publish()}
      previewDisabled={previewDisabled}
      publishDisabled={publishDisabled}
      publishing={publishing}
      busy={busy}
      columnCreationSupported
      tableToolbarHost={featureMode === 'graph' ? tableToolbarHost : null}
    />
  );

  const selectConstructionFamily = (family: ConstructionOperationFamily) => {
    constructionLifecycle.cancel();
    setConstructionHistorySelection({ kind: 'source' });
    setEditingConstructionStepId(undefined);
    setActiveConstructionFamily((current) => current === family ? undefined : family);
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
    if (step.operation.kind === 'RELATED_SOURCE') {
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
  const editingRelatedSourceStep: RelatedSourceStep | undefined = editingConstructionStep?.operation.kind === 'RELATED_SOURCE'
    ? editingConstructionStep as RelatedSourceStep
    : undefined;
  const sourceStageDescriptors = constructionLifecycle.capabilities.status === 'ready'
    ? constructionLifecycle.capabilities.response.stages
    : undefined;
  const sourceAvailability = constructionLifecycle.capabilities.status === 'error'
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
    );
  const relatedSourceUnavailableReason = editingConstructionStep
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
  )?.columns ?? [];
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
      <header className="border-b border-slate-200 px-4 py-4">
        <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500">
          {editingConstructionStep ? 'Edit saved step' : 'Proposed change'}
        </p>
        <div className="mt-1 flex items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-slate-950">
              {activeOperation.label}
            </h2>
            <p className="mt-1 text-sm text-slate-600">
              {activeOperation.description}
            </p>
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
            Close
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

      <div className="p-4">
        {activeOperation.family === 'ADD_COLUMNS' ? (
          <div className="grid gap-4">
            <label className="grid gap-1 text-sm font-medium text-slate-800">
              Source
              <select
                aria-label="Add columns source"
                data-testid="construction-add-columns-source"
                value={selectedAddSource?.key ?? ''}
                onChange={(event) => setAddColumnsSource({
                  context: addSourceContext,
                  key: event.currentTarget.value,
                })}
                className="rounded-md border border-slate-300 bg-white px-3 py-2 text-sm font-normal text-slate-900"
              >
                {addSourceOptions.map((source) => (
                  <option key={source.key} value={source.key}>{source.label}</option>
                ))}
              </select>
              <span className="font-normal text-slate-600">
                Choose a source, then Loom checks its route and output forms before adding a column.
              </span>
            </label>
            <ConceptCatalog
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
              disabledReason={sourceSelectionDisabledReason}
              disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
              onAddSelected={addSelectedFeatures}
            />
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
            <ConstructionOperationEditor
              family={activeOperation.family}
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
          !construction ? (
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
              onApply={(proposalId) => applyCommands([{
                type: 'APPLY_TABLE_SHAPE_PROPOSAL',
                outputId: table.outputId,
                proposalId,
              }])}
            />
          ) : constructionLifecycle.capabilities.status === 'loading' ? (
            <p role="status" className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
              Loading the current table columns and reshape support…
            </p>
          ) : constructionLifecycle.capabilities.status === 'error' ? (
            <p role="alert" className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-950">
              {constructionLifecycle.capabilities.message}
            </p>
          ) : constructionLifecycle.capabilities.status === 'ready' ? (
            <ConstructionReshapeEditor
              construction={construction ?? constructionLifecycle.capabilities.response.baseConstruction}
              capabilities={constructionLifecycle.capabilities.response}
              editingStep={editingConstructionStep}
              selectedColumns={selectedColumnIds}
              relatedExpandContext={{
                project: projectId,
                explorerId: state.explorerId,
                authResourcePath,
                snapshotToken: state.catalog.snapshotToken,
                outputId: table.outputId,
                catalog: state.catalog,
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
  const proposalPanel = (
    <ConstructionProposalPanel
      state={constructionLifecycle.proposal}
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
  const workspaceEditor = operationEditor || relatedSourceStepEditor || constructionLifecycle.proposal.status !== 'idle'
    ? <>{operationEditor}{relatedSourceStepEditor}{proposalPanel}</>
    : undefined;
  const candidatePreview = constructionLifecycle.proposal.status === 'ready' ||
    constructionLifecycle.proposal.status === 'applying'
    ? constructionLifecycle.proposal.preview
    : undefined;
  const proposalResponse = 'response' in constructionLifecycle.proposal
    ? constructionLifecycle.proposal.response
    : undefined;
  const workspacePreview = candidatePreview ?? tablePreview;
  const workspacePreviewIsCurrent = Boolean(candidatePreview) || previewIsCurrent;
  const workspacePreviewStatus = candidatePreview
    ? 'ready'
    : constructionLifecycle.proposal.status === 'previewing'
      ? 'previewing'
      : constructionLifecycle.proposal.status === 'needs-repair'
        ? 'needs-repair'
        : constructionLifecycle.proposal.status === 'error'
          ? 'error'
          : currentPreviewStatus;
  const workspacePreviewProposalId = candidatePreview?.receiptId ?? proposalResponse?.proposalId;

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
            <p>{state.diagnostics[0]?.message ?? message}</p>
            {state.diagnostics[0]?.code ? (
              <p className="mt-1">
                Technical details · Code: {state.diagnostics[0].code}
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
        {state.tables.length === 0 ? (
          <section className="rounded-xl border border-blue-200 bg-white px-6 py-12 text-center shadow-sm">
            <h2 className="text-xl font-semibold text-slate-900">
              Create your first table
            </h2>
            <p className="mx-auto mt-2 max-w-xl text-sm text-slate-600">
              Name the table, choose its starting resource in the dataset graph,
              then select the columns you want to publish.
            </p>
            <form
              className="mx-auto mt-6 flex max-w-lg flex-col gap-2 sm:flex-row"
              onSubmit={(event) => {
                event.preventDefault();
                if (!firstTableName.trim()) return;
                addTableNamed(firstTableName);
                setFirstTableName('');
              }}
            >
              <label className="sr-only" htmlFor="first-table-name">
                Table name
              </label>
              <input
                id="first-table-name"
                value={firstTableName}
                onChange={(event) =>
                  setFirstTableName(event.currentTarget.value)
                }
                placeholder="Table name, e.g. Patients"
                autoFocus
                className="min-w-0 flex-1 rounded-md border border-slate-300 bg-white px-3 py-2 text-sm outline-blue-500 focus:border-blue-500"
              />
              <button
                type="submit"
                disabled={!firstTableName.trim()}
                className="rounded-md bg-blue-700 px-4 py-2 text-sm font-semibold text-white hover:bg-blue-800 disabled:cursor-not-allowed disabled:opacity-40"
              >
                Create table
              </button>
            </form>
          </section>
        ) : (
          <>
            <span key={suggestionIdentity} ref={suggestionHostRef} hidden />
            {table ? (
              <ConstructionWorkspace
                tables={state.tables.map((candidate) => ({
                  outputId: candidate.outputId,
                  title: candidate.title,
                }))}
                selectedOutputId={table.outputId}
                tableActionsDisabled={pendingCommands > 0 || publishing || constructionLifecycle.proposal.status === 'applying'}
                onSelectTable={(outputId) => {
                  selectConstructionHistory({ kind: 'source' });
                  dispatch({ type: 'selectTable', outputId });
                }}
                onNewTable={addTable}
                onDuplicateTable={duplicateTable}
                onDeleteTable={deleteSelectedTable}
                onRenameTable={renameTableById}
                onMoveTable={reorderTable}
                title={table.title}
                rowMeaning={rowMeaning}
                onUndo={previousDraftRevisionId ? () => void restorePreviousDraft() : undefined}
                undoDisabled={
                  pendingCommands > 0 ||
                  publishing ||
                  state.reconciliation === 'pending' ||
                  constructionLifecycle.proposal.status === 'applying'
                }
                previewRowCount={workspacePreviewIsCurrent ? workspacePreview?.rowCount : undefined}
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
                  constructionLifecycle.proposal.status === 'applying'
                }
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
                        Showing the last successful preview for this table. Run
                        Preview to refresh it for the current draft.
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
                      <ConstructionProposalPreview preview={candidatePreview} />
                    ) : (
                      <PreviewTable
                        preview={tablePreview}
                        table={table}
                        limit={previewLimit}
                        onLimitChange={(limit) => {
                          setPreviewLimit(limit);
                          preview(limit);
                        }}
                        onColumnChange={(column) =>
                          void applyCommands([
                            {
                              type: 'UPDATE_COLUMN',
                              outputId: table.outputId,
                              column: column.column,
                              columnValue: column,
                            },
                          ])
                        }
                        onColumnsChange={(columns) =>
                          void applyCommands(
                            columns.map((column) => ({
                              type: 'UPDATE_COLUMN' as const,
                              outputId: table.outputId,
                              column: column.column,
                              columnValue: column,
                            })),
                          )
                        }
                      />
                    )}
                  </>
                }
                editor={workspaceEditor}
                previewStatus={workspacePreviewStatus}
                previewReceiptId={workspacePreview?.receiptId}
                previewOutputId={workspacePreview?.outputId}
                proposalId={workspacePreviewProposalId}
                draftVersion={state.draftVersion}
                draftDigest={state.draftDigest}
              />
            ) : null}
            <details
              className="rounded-xl border border-slate-200 bg-white shadow-sm"
              open={!table?.document.rootResourceType}
              data-testid="construction-source-setup"
            >
              <summary className="cursor-pointer list-none px-4 py-3 text-sm font-semibold text-slate-800 marker:hidden">
                <span>Source and column setup</span>
                <span className="ml-2 text-xs font-normal text-slate-500">
                  Set the row meaning, browse available data, or edit source columns.
                </span>
              </summary>
              <div className="space-y-3 border-t border-slate-200 p-3">
            {table ? (
              <div className="grid gap-3 lg:grid-cols-2">
                <div className="space-y-3">
                  <RowDefinitionPanel
                    catalog={state.catalog}
                    table={table}
                    disabled={rowChangeStatus.isLoading || pendingCommands > 0 || state.reconciliation === 'pending'}
                    onChange={(nodeId, occurrenceId) => void changeTableRoot(nodeId, { rootOccurrenceId: occurrenceId })}
                  />
                  <RowDefinitionSettingsPanel
                    client={loomClient}
                    project={projectId}
                    explorerId={state.explorerId}
                    authResourcePath={authResourcePath}
                    snapshotToken={state.catalog.snapshotToken}
                    draftVersion={state.draftVersion}
                    draftDigest={state.draftDigest}
                    table={table}
                    selection={activePopulationSelection}
                    disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                    onApply={(proposalId) => applyCommands([{
                      type: 'APPLY_ROW_DEFINITION_PROPOSAL', outputId: table.outputId, proposalId,
                    }])}
                  />
                </div>
                <PopulationPanel
                  table={table}
                  selection={activePopulationSelection}
                  loading={populationSelectionLoading || activePopulationSelectionLoading}
                  error={populationVariantError ?? populationSelectionError}
                  project={projectId}
                  explorerId={state.explorerId}
                  authResourcePath={authResourcePath}
                  snapshotToken={state.catalog.snapshotToken}
                  receiptId={state.receipt?.receiptId}
                  disabled={populationSelectionLoading || activePopulationSelectionLoading || populationVariantPending || pendingCommands > 0 || state.reconciliation === 'pending'}
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
                />
              </div>
            ) : null}
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
                  catalog={state.catalog}
                  sourceProjectionAvailability={sourceAvailability}
                  relatedSourceAvailability={relatedSourceAvailability}
                  disabledReason={sourceSelectionDisabledReason}
                  disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                  onAddSelected={addSelectedFeatures}
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
                onChange={(column) =>
                  table &&
                  void applyCommands([
                    {
                      type: 'UPDATE_COLUMN',
                      outputId: table.outputId,
                      column: column.column,
                      columnValue: column,
                    },
                  ])
                }
                onColumnsChange={(columns) =>
                  table &&
                  void applyCommands(
                    columns.map((column) => ({
                      type: 'UPDATE_COLUMN' as const,
                      outputId: table.outputId,
                      column: column.column,
                      columnValue: column,
                    })),
                  )
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
                      disabledReason={sourceSelectionDisabledReason}
                      disabled={pendingCommands > 0 || state.reconciliation === 'pending'}
                      onAddSelected={addSelectedFeatures}
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
            </details>
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
