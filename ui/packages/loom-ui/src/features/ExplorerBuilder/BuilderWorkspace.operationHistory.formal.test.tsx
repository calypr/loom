// @vitest-environment jsdom
/// <reference types="node" />
import React from 'react';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createHash } from 'node:crypto';
import { z } from 'zod';
import type {
  DiscoverConstructionCategoriesArgs,
  GetConstructionCapabilitiesArgs,
  PreviewExplorerBuilderArgs,
  ProposeConstructionArgs,
} from '../../api';
import type {
  Construction,
  ConstructionCategoryDiscoveryResponse,
  ConstructionCapabilitiesResponse,
  ConstructionProposalResponse,
  ConstructionStep,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../types';
import { constructionProposalResponseSchema, constructionSchema, explorerBuilderPreviewResultSchema } from '../../types';
import BuilderWorkspace from './BuilderWorkspace';
import { ConstructionProposalPanel, constructionProposalIsApplicable } from './constructionWorkspace/ConstructionProposalPanel';
import { ConstructionReshapeEditor } from './constructionOperations/ConstructionReshapeEditor';
import { useConstructionLifecycle, type ConstructionCandidateIntent } from './constructionWorkspace/useConstructionLifecycle';

const harness = vi.hoisted(() => {
  const client = {
    getSelection: vi.fn(),
    listRowDefinitionChoices: vi.fn(),
    searchPopulationRoutes: vi.fn(),
    createSelection: vi.fn(),
    configuredColumnContextsQuery: vi.fn(),
    getTableShapeCapabilities: vi.fn(),
    discoverTableShapeCategories: vi.fn(),
    resolveTableShape: vi.fn(),
    proposeTableShape: vi.fn(),
    getConstructionCapabilities: vi.fn<(args: GetConstructionCapabilitiesArgs, signal?: AbortSignal) => Promise<ConstructionCapabilitiesResponse>>(),
    getConstructionInputs: vi.fn(),
    discoverConstructionCategories: vi.fn<(args: DiscoverConstructionCategoriesArgs, signal?: AbortSignal) => Promise<ConstructionCategoryDiscoveryResponse>>(),
    proposeConstruction: vi.fn<(args: ProposeConstructionArgs, signal?: AbortSignal) => Promise<ConstructionProposalResponse>>(),
    proposeConstructionChoices: vi.fn(),
    preview: vi.fn<(args: PreviewExplorerBuilderArgs, signal?: AbortSignal) => Promise<ExplorerBuilderPreviewResult>>(),
    browseSemanticInventory: vi.fn(),
  };
  const getSuggestions = vi.fn();
  return {
    client,
    getSuggestions,
    builderState: null as ExplorerBuilderState | null,
    replayCapabilities: undefined as ConstructionCapabilitiesResponse | undefined,
    nextProposalStatus: 'READY' as 'READY' | 'NEEDS_REPAIR',
    proposalCounter: 0,
  };
});

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  const mutation = () => [vi.fn(), { isLoading: false }];
  return {
    ...actual,
    useLoomClient: () => harness.client,
    useApplyExplorerBuilderCommandsV2Mutation: mutation,
    useAssessExplorerRowChangeMutation: mutation,
    useCreateExplorerAuthoringMutation: mutation,
    useDeleteExplorerAuthoringMutation: mutation,
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: {} } }),
    useGetExplorerAuthoringExplorersQuery: () => ({
      data: [{ explorerId: 'builder', title: 'Operation history' }],
      isLoading: false,
      refetch: vi.fn(),
    }),
    useGetExplorerBuilderStateV2Query: () => ({
      data: harness.builderState,
      isLoading: false,
      refetch: vi.fn(),
    }),
    useGetExplorerCandidateSuggestionsV2Mutation: () => [harness.getSuggestions, { isLoading: false }],
    usePreviewExplorerAuthoringV2Mutation: mutation,
    usePublishExplorerAuthoringV2Mutation: mutation,
    useReconcileExplorerBuilderV2Mutation: mutation,
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('./useAutomaticPreview', () => ({ useAutomaticPreview: () => ({ error: undefined, isLoading: false }) }));
vi.mock('./hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('./hooks/usePortalHost', () => ({ usePortalHost: () => null }));

vi.mock('./components/BuilderToolbar', () => ({ BuilderToolbar: () => null, NewExplorerControl: () => null }));
vi.mock('./components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('./components/GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: () => null }));
vi.mock('./components/ColumnSelector', () => ({ ColumnSelector: () => null }));
vi.mock('./components/ConceptCatalog', () => ({ ConceptCatalog: () => null }));
vi.mock('./components/RowRootPicker', () => ({ RowRootPicker: () => null }));
vi.mock('./components/PreviewTable', async (importOriginal) => ({
  ...await importOriginal<typeof import('./components/PreviewTable')>(),
  PreviewTable: () => null,
}));
vi.mock('./components/DataframeContractPanel', () => ({ DataframeContractPanel: () => null }));
vi.mock('./components/PopulationPanel', () => ({ PopulationPanel: () => null }));
vi.mock('./components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('./components/RowDefinitionPanel', () => ({ RowDefinitionPanel: () => null }));
vi.mock('./components/RowDefinitionSettingsPanel', () => ({ RowDefinitionSettingsPanel: () => null, rowScopeLabel: () => '' }));
vi.mock('./components/TableShapeSettingsPanel', () => ({ TableShapeSettingsPanel: () => null }));
vi.mock('./components/InterpretationPanel', () => ({ InterpretationPanel: () => null }));
vi.mock('./constructionWorkspace/FrameSourcePanel', () => ({ FrameSourcePanel: () => null }));
vi.mock('./constructionOperations/SourceCollectionExpandEditor', () => ({ SourceCollectionExpandEditor: () => null }));
vi.mock('./constructionWorkspace/ConstructionColumnSelection', () => ({ ConstructionColumnSelection: () => null }));
vi.mock('./constructionOperations/ConstructionOperationEditor', () => ({ ConstructionOperationEditor: () => null }));
vi.mock('./constructionOperations/FilterRowsEditor', () => ({ FilterRowsEditor: () => null }));
vi.mock('./constructionOperations/RelatedSourceStepEditor', () => ({ RelatedSourceStepEditor: () => null }));
vi.mock('./constructionOperations/RelatedFieldEditor', () => ({ RelatedFieldEditor: () => null }));
vi.mock('./constructionOperations/ConstructionCombineEditor', () => ({ ConstructionCombineEditor: () => null }));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const groupStepId = 'group-step';
const aggregateOutputId = 'aggregate-value';
const groupKeyOutputId = 'group-key-A-1';

const groupStep = {
  id: groupStepId,
  inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
  operation: {
    kind: 'GROUP' as const,
    group: {
      constructionId: groupStepId,
      missingKeyPolicy: 'GROUP' as const,
      keys: [{ inputColumnId: 'gender-id', outputColumnId: groupKeyOutputId }],
      aggregates: [{ operation: 'COUNT_ROWS' as const, outputColumnId: aggregateOutputId }],
    },
  },
  outputs: [
    { id: groupKeyOutputId, name: 'gender', label: 'Gender', type: 'string' },
    { id: aggregateOutputId, name: 'row_count', label: 'Rows', type: 'integer' },
  ],
};

const filterStep = {
  id: 'filter-by-aggregate',
  inputs: [{ kind: 'STEP_OUTPUT' as const, stepId: groupStepId }],
  operation: { kind: 'FILTER' as const, filter: { columnId: aggregateOutputId, operator: 'EXISTS' as const } },
  outputs: [
    { id: groupKeyOutputId, name: 'gender', label: 'Gender', type: 'string' },
    { id: aggregateOutputId, name: 'row_count', label: 'Rows', type: 'integer' },
  ],
};

const acceptedConstruction: Construction = {
  version: 1,
  steps: [groupStep, filterStep],
};

const acceptedDocument = {
  kind: 'ExplorerBuilderDocument' as const,
  output: { id: 'table-1', title: 'Patients' },
  rootResourceType: 'Patient',
  route: { occurrenceId: 'base', resourceType: 'Patient' },
  rows: { kind: 'RECORDS' as const, records: {} },
  columns: [
    {
      columnId: 'gender-id', column: 'gender', label: 'Gender', logicalType: 'string', occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'gender', projectionMode: 'VALUE' as const } },
      table: { visible: true, order: 0 },
    },
    {
      columnId: 'active-id', column: 'active', label: 'Active', logicalType: 'boolean', occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'active', projectionMode: 'VALUE' as const } },
      table: { visible: true, order: 1 },
    },
    {
      columnId: 'multiple-birth-integer-id', column: 'multiple_birth_integer', label: 'Multiple birth integer', logicalType: 'integer', occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'multipleBirthInteger', projectionMode: 'VALUE' as const } },
      table: { visible: true, order: 2 },
    },
  ],
  construction: acceptedConstruction,
};

const workspace: ExplorerBuilderWorkspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace',
  explorer: { title: 'Operation history' },
  documents: [acceptedDocument],
  tabs: [{ id: 'patients_tab', title: 'Patients', outputId: 'table-1', order: 0, visible: true }],
};

const catalog = {
  snapshotToken: 'snapshot-operation-history',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 }],
  edges: [],
  candidates: [],
};

const sourceStage: ConstructionCapabilitiesResponse['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  operation: 'SOURCE_PROJECTION',
  rowIdentityColumn: 'source-row-id',
  columns: [
    { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string', cardinality: 'optional_one' },
    { id: 'active-id', name: 'active', label: 'Active', type: 'boolean', cardinality: 'optional_one' },
    { id: 'multiple-birth-integer-id', name: 'multipleBirthInteger', label: 'Multiple birth integer', type: 'integer', cardinality: 'optional_one' },
  ],
  capabilities: [{ kind: 'GROUP', supported: true }],
};

const acceptedState: ExplorerBuilderState = {
  apiVersion,
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: 1,
  draftDigest: 'sha256:accepted-group-count',
  workspace,
  catalog,
};

const operationHistoryTraceFileSchema = z.object({
  schemaVersion: z.literal(1),
  cases: z.array(z.object({
    id: z.string(),
    traceKind: z.enum(['proposal-input', 'lifecycle-witness']).optional(),
    requestFingerprint: z.string().optional(),
    initial: z.object({
      tableId: z.string().optional(),
      sourceColumns: z.array(z.object({
        id: z.string(),
        path: z.string().optional(),
        label: z.string().optional(),
        type: z.string(),
        role: z.enum(['KEY_A', 'KEY_B', 'NUMERIC']).optional(),
      }).passthrough()).optional(),
      aggregate: z.string(),
      numericInputPresent: z.boolean(),
      keyA: z.boolean(),
      keyB: z.boolean(),
      selectedKey: z.string().optional(),
      keyOutputId: z.string().optional(),
      aggregateOutputId: z.string(),
      groupStepId: z.string(),
      downstream: z.array(z.object({
        id: z.string(),
        kind: z.string(),
        dependency: z.string(),
        columnId: z.string().optional(),
        inputStepId: z.string().optional(),
        outputColumnId: z.string().optional(),
      }).passthrough()),
    }).passthrough(),
    actions: z.array(z.object({ kind: z.string(), aggregate: z.string().optional() }).passthrough()),
    expectations: z.array(z.object({
      actionIndex: z.number().int(),
      candidateActionIndex: z.number().int().optional(),
      requestFingerprint: z.string().optional(),
      proposalStatus: z.string(),
      stepIds: z.array(z.string()),
      candidateStepIds: z.array(z.string()).optional(),
      candidateInputStepIds: z.record(z.string(), z.string()).optional(),
      groupOutputTypes: z.record(z.string(), z.string()),
      downstreamInputTypes: z.record(z.string(), z.object({
        columnId: z.string(),
        type: z.string(),
        inputStepId: z.string().optional(),
      }).passthrough()),
      downstreamOutputTypes: z.record(z.string(), z.object({ columnId: z.string(), type: z.string() })).optional(),
      candidateGroupKeyOutputId: z.string().nullable().optional(),
      acceptedGroupKeyOutputId: z.string().nullable().optional(),
      groupKeyOutputRelation: z.enum(['ABSENT', 'SAME_AS_ACCEPTED', 'FRESH_FROM_ACCEPTED']).optional(),
      acceptedAggregate: z.string(),
      candidateAggregate: z.string(),
      acceptedChangesOnApply: z.boolean(),
      applyAck: z.string().optional(),
    }).passthrough()),
  }).passthrough()),
}).passthrough();

const operationHistorySeedIdentityFileSchema = z.object({
  schemaVersion: z.literal(1),
  cases: z.array(z.object({
    id: z.string(),
    acceptedVersion: z.number().int(),
    acceptedDigest: z.string(),
    snapshotToken: z.string(),
    outputId: z.string(),
  }).strict()),
}).strict();

const operationHistoryServiceProposalResponseSchema = constructionProposalResponseSchema.extend({
  previewStatus: z.enum(['READY', 'NEEDS_REPAIR', 'PREVIEW_PENDING']),
});
type OperationHistoryServiceProposalResponse = z.infer<typeof operationHistoryServiceProposalResponseSchema>;
const operationHistoryServicePreviewResultSchema = z.object({
  receiptId: z.string().min(1),
  outputId: z.string().min(1),
  columns: z.array(z.object({
    publicColumn: z.string().min(1),
    label: z.string(),
    logicalType: z.string().min(1),
    filterable: z.boolean(),
    chartable: z.boolean(),
    nullable: z.boolean().optional(),
    shape: z.string().optional(),
    sourceResourceType: z.string().optional(),
    sourcePath: z.string().optional(),
    authoredColumns: z.array(z.string()).optional(),
    lossless: z.boolean().optional(),
    structuralSuitability: z.string().optional(),
  }).passthrough()),
  summary: z.object({
    Output: z.string(),
    Columns: z.array(z.string()),
    RowCount: z.number().int().nonnegative(),
    Complete: z.boolean(),
    Truncated: z.boolean(),
    PartialValidation: z.boolean(),
    RowLineageCapability: z.object({
      Available: z.boolean(),
      ReasonCode: z.string(),
      Operation: z.string(),
    }).passthrough(),
  }).passthrough(),
}).passthrough();
type OperationHistoryServicePreviewResult = z.infer<typeof operationHistoryServicePreviewResultSchema>;

const operationHistoryProposalResponseFileSchema = z.object({
  schemaVersion: z.literal(1),
  cases: z.array(z.object({
    id: z.string(),
    checkpoints: z.array(z.object({
      actionIndex: z.number().int(),
      requestId: z.string().optional(),
      requestFingerprint: z.string().optional(),
      proposalResponse: operationHistoryServiceProposalResponseSchema,
      previewResult: operationHistoryServicePreviewResultSchema,
    }).strict()),
  }).strict()),
}).strict();

const operationHistoryCandidateFileSchema = z.object({
  schemaVersion: z.literal(1),
  cases: z.array(z.object({
    id: z.string(),
    checkpoints: z.array(z.object({
      actionIndex: z.number().int(),
      proposalRequest: z.object({ candidateConstruction: constructionSchema }).passthrough(),
    }).passthrough()),
  }).passthrough()),
}).passthrough();

const operationHistoryTracePath = process.env.BUILDER_OPERATION_TRACES;
const operationHistoryTraceFile = operationHistoryTracePath
  ? operationHistoryTraceFileSchema.parse(JSON.parse(readFileSync(operationHistoryTracePath, 'utf8')))
  : operationHistoryTraceFileSchema.parse({ schemaVersion: 1, cases: [] });
const isSeedPreparation = process.env.BUILDER_OPERATION_PREPARE === '1';
const isPanelCheck = process.env.BUILDER_OPERATION_PANEL_CHECK === '1';
const unparsedSeedIdentities: unknown = process.env.BUILDER_OPERATION_SEED_IDENTITIES
  ? JSON.parse(readFileSync(process.env.BUILDER_OPERATION_SEED_IDENTITIES, 'utf8'))
  : undefined;
const operationHistorySeedIdentities = unparsedSeedIdentities === undefined
  ? undefined
  : operationHistorySeedIdentityFileSchema.parse(unparsedSeedIdentities);
if (operationHistoryTracePath && !isSeedPreparation && !isPanelCheck) {
  if (!operationHistorySeedIdentities) {
    throw new Error('Replay mode requires BUILDER_OPERATION_SEED_IDENTITIES from the Go seed-prepare pass.');
  }
  const traceIds = operationHistoryTraceFile.cases.map((trace) => trace.id).sort();
  const identityIds = operationHistorySeedIdentities.cases.map((identity) => identity.id).sort();
  if (JSON.stringify(identityIds) !== JSON.stringify(traceIds)) {
    throw new Error('Go seed identity map must contain exactly one entry for each generated trace ID.');
  }
}
const operationHistorySeedIdentityById = new Map(
  (operationHistorySeedIdentities?.cases ?? []).map((identity) => [identity.id, identity]),
);
const operationHistoryProposalResponsePath = process.env.BUILDER_OPERATION_PROPOSAL_RESPONSES;
const unparsedOperationHistoryProposalResponses: unknown = isPanelCheck
  ? operationHistoryProposalResponsePath
    ? JSON.parse(readFileSync(operationHistoryProposalResponsePath, 'utf8'))
    : (() => { throw new Error('Panel-check mode requires BUILDER_OPERATION_PROPOSAL_RESPONSES.'); })()
  : undefined;
const operationHistoryProposalResponses = unparsedOperationHistoryProposalResponses === undefined
  ? undefined
  : operationHistoryProposalResponseFileSchema.parse(unparsedOperationHistoryProposalResponses);
const operationHistoryCandidatePath = process.env.BUILDER_OPERATION_CANDIDATES;
const capturedCandidates = new Map<string, Array<Record<string, unknown>>>();
const capturedSeeds = new Map<string, { readonly acceptedDocument: unknown; readonly acceptedWorkspace: ExplorerBuilderWorkspace }>();
const actualRequestFingerprints = new Set<string>();

const panelPreviewFromServicePreview = (
  servicePreview: OperationHistoryServicePreviewResult,
): ExplorerBuilderPreviewResult => explorerBuilderPreviewResultSchema.parse({
  apiVersion,
  kind: 'ExplorerBuilderPreview',
  receiptId: servicePreview.receiptId,
  outputId: servicePreview.outputId,
  columns: servicePreview.columns.map((column) => ({
    column: column.publicColumn,
    authoredColumns: column.authoredColumns,
    label: column.label,
    logicalType: column.logicalType,
    filterable: column.filterable,
    chartable: column.chartable,
    nullable: column.nullable,
    shape: column.shape,
    sourceResourceType: column.sourceResourceType,
    sourcePath: column.sourcePath,
    lossless: column.lossless,
    structuralSuitability: column.structuralSuitability,
  })),
  // This is a panel-only projection, not a serialized wire preview: Go exports
  // the service summary and emitted columns, while this UI callback has no rows.
  rows: [],
  rowLineageCapability: servicePreview.summary.RowLineageCapability.Available
    ? { status: 'AVAILABLE' }
    : {
      status: 'UNAVAILABLE',
      reasonCode: servicePreview.summary.RowLineageCapability.ReasonCode || 'SERVICE_PREVIEW_ROWS_NOT_EXPORTED',
    },
  rowCount: servicePreview.summary.RowCount,
  sampled: !servicePreview.summary.Complete || servicePreview.summary.Truncated,
  partialValidation: servicePreview.summary.PartialValidation,
  // Diagnostics and row-lineage presentation fields are required by the UI type
  // but are not included in the service-preview artifact.
  diagnostics: [],
});

const actualRequestFingerprint = (request: ProposeConstructionArgs): string => {
  const stableRequest = Object.fromEntries(Object.entries(request).filter(([key]) => key !== 'requestId'));
  return createHash('sha256').update(JSON.stringify(stableRequest)).digest('hex');
};

type OperationHistoryTraceFile = z.infer<typeof operationHistoryTraceFileSchema>;
type OperationHistoryTrace = OperationHistoryTraceFile['cases'][number];
type OperationHistorySourceColumn = NonNullable<OperationHistoryTrace['initial']['sourceColumns']>[number];
type OperationHistorySeedIdentity = z.infer<typeof operationHistorySeedIdentityFileSchema>['cases'][number];
type GroupKeyRole = 'KEY_A' | 'KEY_B';

const roleForSourceColumn = (column: OperationHistorySourceColumn): 'KEY_A' | 'KEY_B' | 'NUMERIC' =>
  column.role ?? (column.path === 'gender' ? 'KEY_A' : column.path === 'active' ? 'KEY_B' : 'NUMERIC');

const sourceColumnForRole = (
  trace: OperationHistoryTrace,
  role: 'KEY_A' | 'KEY_B' | 'NUMERIC',
): OperationHistorySourceColumn | undefined => trace.initial.sourceColumns?.find((column) => roleForSourceColumn(column) === role);

const sourceOutputName = (column: OperationHistorySourceColumn): string => {
  if (!column.path) throw new Error(`Trace source column ${column.id} has no field path.`);
  return column.path.replace(/([a-z0-9])([A-Z])/g, '$1_$2').replaceAll('.', '_').toLocaleLowerCase();
};

const groupOutputColumnsForTrace = (trace: OperationHistoryTrace): ConstructionStep['outputs'] => {
  const outputs: ConstructionStep['outputs'][number][] = [];
  const selectedRole = trace.initial.selectedKey === 'A' ? 'KEY_A' : trace.initial.selectedKey === 'B' ? 'KEY_B' : undefined;
  if (selectedRole) {
    const selected = sourceColumnForRole(trace, selectedRole);
    if (!selected || !trace.initial.keyOutputId) throw new Error(`${trace.id}: selected ${selectedRole} key is missing source/output identity.`);
    outputs.push({
      id: trace.initial.keyOutputId,
      name: sourceOutputName(selected),
      label: selected.label ?? selected.path ?? selected.id,
      type: selected.type,
    });
  }
  outputs.push(trace.initial.aggregate === 'MEAN'
    ? { id: trace.initial.aggregateOutputId, name: 'mean_value', label: 'Mean value', type: 'decimal' }
    : { id: trace.initial.aggregateOutputId, name: 'row_count', label: 'Row count', type: 'integer' });
  return outputs;
};

const constructionForTrace = (trace: OperationHistoryTrace): Construction => {
  const selectedRole: GroupKeyRole | undefined = trace.initial.selectedKey === 'A'
    ? 'KEY_A'
    : trace.initial.selectedKey === 'B'
      ? 'KEY_B'
      : undefined;
  const selectedSource = selectedRole ? sourceColumnForRole(trace, selectedRole) : undefined;
  if (selectedRole && (!selectedSource || !trace.initial.keyOutputId)) {
    throw new Error(`${trace.id}: selected key is missing its source or output id.`);
  }
  const numeric = sourceColumnForRole(trace, 'NUMERIC');
  const aggregate = trace.initial.aggregate === 'MEAN'
    ? numeric
      ? { operation: 'MEAN' as const, inputColumnId: numeric.id, outputColumnId: trace.initial.aggregateOutputId }
      : undefined
    : trace.initial.aggregate === 'COUNT_ROWS'
      ? { operation: 'COUNT_ROWS' as const, outputColumnId: trace.initial.aggregateOutputId }
      : undefined;
  if (!aggregate) throw new Error(`${trace.id}: unsupported seed aggregate or missing numeric source.`);
  const group: ConstructionStep = {
    id: trace.initial.groupStepId,
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: trace.initial.groupStepId,
        missingKeyPolicy: 'GROUP',
        keys: selectedSource && trace.initial.keyOutputId
          ? [{ inputColumnId: selectedSource.id, outputColumnId: trace.initial.keyOutputId }]
          : [],
        aggregates: [aggregate],
      },
    },
    outputs: groupOutputColumnsForTrace(trace),
  };
  const downstreamSteps: ConstructionStep[] = [];
  let previousStepId = group.id;
  let previousOutputs = group.outputs;
  for (const dependent of trace.initial.downstream) {
    const columnId = dependent.columnId ?? (dependent.dependency === 'aggregate'
      ? trace.initial.aggregateOutputId
      : trace.initial.keyOutputId ?? 'missing-group-key');
    if (dependent.inputStepId && dependent.inputStepId !== previousStepId) {
      throw new Error(`${trace.id}: ${dependent.id} input ${dependent.inputStepId} does not consume previous step ${previousStepId}.`);
    }
    const outputs = [...previousOutputs];
    let step: ConstructionStep;
    if (dependent.kind === 'FILTER') {
      step = {
        id: dependent.id,
        inputs: [{ kind: 'STEP_OUTPUT', stepId: previousStepId }],
        operation: { kind: 'FILTER', filter: { columnId, operator: 'EXISTS' } },
        outputs,
      };
    } else if (dependent.kind === 'CALCULATE') {
      const outputId = dependent.outputColumnId ?? `calculated-${dependent.id}`;
      step = {
        id: dependent.id,
        inputs: [{ kind: 'STEP_OUTPUT', stepId: previousStepId }],
        operation: {
          kind: 'DERIVE',
          derive: {
            constructionId: dependent.id,
            outputColumnId: outputId,
            operation: 'ADD',
            left: { kind: 'COLUMN', columnId },
            right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } },
            missingInputPolicy: 'PROPAGATE_NULL',
          },
        },
        outputs: [...outputs, { id: outputId, name: 'calculated_value', label: 'Calculated value' }],
      };
    } else {
      throw new Error(`${trace.id}: unsupported dependent kind ${dependent.kind}.`);
    }
    downstreamSteps.push(step);
    previousStepId = step.id;
    previousOutputs = step.outputs;
  }
  return { version: 1, steps: [group, ...downstreamSteps] };
};

const documentForTrace = (trace: OperationHistoryTrace, construction: Construction) => ({
  kind: 'ExplorerBuilderDocument' as const,
  output: { id: trace.initial.tableId ?? 'table-1', title: 'Patients' },
  rootResourceType: 'Patient',
  route: { occurrenceId: 'base', resourceType: 'Patient' },
  rows: { kind: 'RECORDS' as const, records: {} },
  columns: (trace.initial.sourceColumns ?? []).map((column, order) => ({
    columnId: column.id,
    column: sourceOutputName(column),
    label: column.label ?? column.id,
    logicalType: column.type,
    occurrenceId: 'base',
    source: { kind: 'field' as const, field: { path: column.path ?? '', projectionMode: 'VALUE' as const } },
    table: { visible: true, order },
  })),
  construction,
});

const capabilitiesForTrace = (
  trace: OperationHistoryTrace,
  construction: Construction,
  seedIdentity: OperationHistorySeedIdentity,
): ConstructionCapabilitiesResponse => {
  const selectedStage: ConstructionCapabilitiesResponse['selectedStage'] = {
    id: 'source_projection',
    inputStageId: '',
    operation: 'SOURCE_PROJECTION',
    rowIdentityColumn: 'patient-row-id',
    columns: (trace.initial.sourceColumns ?? []).map((column) => ({
      id: column.id,
      name: sourceOutputName(column),
      label: column.label ?? column.id,
      type: column.type,
      cardinality: 'optional_one',
    })),
    capabilities: [{ kind: 'GROUP', supported: true }],
  };
  return {
    snapshotToken: seedIdentity.snapshotToken,
    draftVersion: seedIdentity.acceptedVersion,
    draftDigest: seedIdentity.acceptedDigest,
    outputId: seedIdentity.outputId,
    stageId: selectedStage.id,
    baseConstruction: construction,
    stages: [selectedStage],
    selectedStage,
    groupFieldChoices: [],
    groupRowValueChoices: [],
    semanticGroupChoices: [],
    workspaceInputs: [],
  };
};

const workspaceForTrace = (
  trace: OperationHistoryTrace,
  document: ReturnType<typeof documentForTrace>,
): ExplorerBuilderWorkspace => {
  const outputId = trace.initial.tableId ?? 'table-1';
  return {
    apiVersion,
    kind: 'ExplorerBuilderWorkspace',
    explorer: { title: 'Operation history' },
    documents: [document],
  tabs: [{ id: `${outputId}_tab`, title: 'Patients', outputId, order: 0, visible: true }],
  };
};

const previewFor = (receiptId: string, outputId: string): ExplorerBuilderPreviewResult => ({
  apiVersion,
  kind: 'ExplorerBuilderPreview' as const,
  rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
  receiptId,
  outputId,
  columns: [
    { column: 'site', label: 'Site', logicalType: 'string', filterable: true, chartable: false },
    { column: 'row_count', label: 'Rows', logicalType: 'decimal', filterable: true, chartable: false },
  ],
  rows: [{ site: 'north', row_count: 2.5 }],
  rowCount: 1,
  diagnostics: [],
});

const proposeResponse = (args: ProposeConstructionArgs): ConstructionProposalResponse => {
  const proposalId = `proposal-group-operation-${harness.proposalCounter}`;
  const previewStatus = harness.nextProposalStatus;
  return {
  ...(previewStatus === 'READY' ? { proposalId } : {}),
  outputId: args.outputId,
  snapshotToken: args.snapshotToken,
  draftVersion: args.expectedDraftVersion,
  draftDigest: args.expectedDraftDigest,
  baseDocumentDigest: 'sha256:accepted-document',
  candidateWorkspaceDigest: `sha256:group-candidate-${harness.proposalCounter}`,
  changedStepId: args.changedStepId ?? '',
  candidateConstruction: args.candidateConstruction,
  dependencyImpact: { affectedStepIds: [] },
  stages: harness.replayCapabilities?.stages ?? [sourceStage],
  previewStatus,
  previewDurationMs: 1,
  ...(previewStatus === 'READY' ? { preview: previewFor(proposalId, args.outputId) } : {}),
  };
};

type ConstructionLifecycle = ReturnType<typeof useConstructionLifecycle>;
type EditorReplayHandle = {
  readonly candidateIntent: () => ConstructionCandidateIntent | undefined;
  readonly proposeCandidateAndWait: ConstructionLifecycle['proposeCandidateAndWait'];
  readonly cancel: ConstructionLifecycle['cancel'];
};

const LifecycleEditorReplay = React.forwardRef<EditorReplayHandle, {
  readonly construction: Construction;
  readonly capabilitiesRequest: GetConstructionCapabilitiesArgs;
}>(({ construction, capabilitiesRequest }, ref) => {
  const lifecycle = useConstructionLifecycle({ client: harness.client, capabilitiesRequest });
  const candidateIntent = React.useRef<ConstructionCandidateIntent | undefined>(undefined);
  React.useImperativeHandle(ref, () => ({
    candidateIntent: () => candidateIntent.current,
    proposeCandidateAndWait: lifecycle.proposeCandidateAndWait,
    cancel: lifecycle.cancel,
  }), [lifecycle.cancel, lifecycle.proposeCandidateAndWait]);

  if (lifecycle.capabilities.status !== 'ready') {
    return <p role="status" data-testid="operation-history-capabilities-loading">Loading operation capabilities</p>;
  }
  const editingStep = construction.steps.find((step) => step.id === capabilitiesRequest.groupStepId);
  if (editingStep?.operation.kind !== 'GROUP') {
    return <p role="alert">Trace seed has no editable Group step.</p>;
  }
  return (
    <ConstructionReshapeEditor
      construction={construction}
      capabilities={lifecycle.capabilities.response}
      editingStep={editingStep}
      initialKind="group"
      selectedColumns={[]}
      disabled={false}
      onCandidateChange={(intent) => { candidateIntent.current = intent; }}
      onEditStep={() => undefined}
    />
  );
});

describe.skipIf(!operationHistoryTracePath)('BuilderWorkspace operation history actual candidate capture', () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    harness.builderState = acceptedState;
    harness.replayCapabilities = undefined;
    harness.nextProposalStatus = 'READY';
    harness.proposalCounter = 0;
    harness.client.getConstructionCapabilities.mockReset().mockImplementation(async (args) => {
      const response = harness.replayCapabilities ?? {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: acceptedConstruction,
        stages: [sourceStage],
        selectedStage: sourceStage,
        groupFieldChoices: [],
        groupRowValueChoices: [],
        semanticGroupChoices: [],
        workspaceInputs: [],
      };
      return {
        ...response,
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
      };
    });
    harness.client.proposeConstruction.mockReset().mockImplementation(async (args: ProposeConstructionArgs) => {
      harness.proposalCounter += 1;
      return proposeResponse(args);
    });
    harness.client.discoverConstructionCategories.mockReset().mockRejectedValue(new Error('Category discovery is not used by this trace.'));
    harness.client.preview.mockReset();
    harness.client.browseSemanticInventory.mockReset().mockResolvedValue({ state: 'complete', entries: [] });
    harness.client.getSelection.mockReset();
    harness.client.listRowDefinitionChoices.mockReset().mockResolvedValue({ snapshotToken: catalog.snapshotToken, outputId: 'table-1', choices: [], explicitGroups: [] });
    harness.client.configuredColumnContextsQuery.mockReset().mockReturnValue({ data: undefined, error: undefined, isLoading: false, isFetching: false });
    harness.client.getTableShapeCapabilities.mockReset().mockResolvedValue({});
    harness.client.discoverTableShapeCategories.mockReset().mockResolvedValue({});
    harness.client.resolveTableShape.mockReset().mockResolvedValue({});
    harness.client.proposeTableShape.mockReset().mockResolvedValue({});
    harness.getSuggestions.mockReset().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({ snapshotToken: catalog.snapshotToken, candidates: [] }),
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('wires BuilderWorkspace history and the actual Group editor to its proposal request', async () => {
    if (isSeedPreparation || isPanelCheck) return;
    const trace = operationHistoryTraceFile.cases.find((candidate) => candidate.id === 'operation-history-count-to-mean-accepted-history-filter');
    expect(trace, 'generated accepted-history Filter trace must be present').toBeDefined();
    if (!trace) throw new Error('Generated accepted-history Filter trace is missing.');
    const seedIdentity = operationHistorySeedIdentityById.get(trace.id);
    if (!seedIdentity) throw new Error(`${trace.id}: Go seed identity is missing.`);
    const traceConstruction = constructionForTrace(trace);
    const traceDocument = documentForTrace(trace, traceConstruction);
    harness.builderState = {
      ...acceptedState,
      workspace: workspaceForTrace(trace, traceDocument),
      catalog: { ...acceptedState.catalog, snapshotToken: seedIdentity.snapshotToken },
      draftVersion: seedIdentity.acceptedVersion,
      draftDigest: seedIdentity.acceptedDigest,
    };
    const proposalActionIndex = trace.actions.findIndex((action, index) => action.kind === 'propose' &&
      trace.actions[0]?.kind === 'editAggregate' && trace.actions[0].aggregate === 'MEAN' &&
      index === 1 && trace.actions[index - 1]?.kind === 'editAggregate' && trace.actions[index - 1].aggregate === 'MEAN');
    expect(proposalActionIndex, `actual replay expects editAggregate(MEAN) → propose, got ${JSON.stringify(trace.actions)}`).toBe(1);
    const proposalExpectation = trace.expectations.find((candidate) => candidate.actionIndex === proposalActionIndex);
    expect(proposalExpectation, 'generated proposal expectation must exist after the MEAN edit').toBeDefined();
    if (!proposalExpectation) throw new Error('Generated proposal expectation is missing after the MEAN edit.');
    const actionSequence = ['editAggregate(MEAN)', 'propose'];
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);

    fireEvent.click(await screen.findByTestId(`construction-history-step-${trace.initial.groupStepId}`));
    await screen.findByTestId('construction-reshape-editor');
    fireEvent.change(screen.getByRole('combobox', { name: 'Summary 1' }), { target: { value: 'MEAN' } });

    await waitFor(() => expect(harness.client.proposeConstruction).toHaveBeenCalledOnce(), { timeout: 5000 });
    const request = harness.client.proposeConstruction.mock.calls[0]?.[0];
    expect(request, `action sequence: ${actionSequence.join(' → ')}`).toBeDefined();
    if (!request) throw new Error(`No proposal request captured after: ${actionSequence.join(' → ')}`);

    const candidate = request.candidateConstruction;
    const candidateGroup = candidate.steps.find((step) => step.id === trace.initial.groupStepId);
    expect(candidate.steps.map((step) => step.id), `action sequence: ${actionSequence.join(' → ')}`).toEqual(proposalExpectation.candidateStepIds ?? proposalExpectation.stepIds);
    expect(candidateGroup?.operation.kind).toBe('GROUP');
    if (candidateGroup?.operation.kind !== 'GROUP') throw new Error('Captured candidate lost its Group step.');
    const candidateAggregate = candidateGroup.operation.group.aggregates?.[0];
    expect(candidateAggregate?.operation).toBe('MEAN');
    expect(candidateAggregate && 'inputColumnId' in candidateAggregate ? candidateAggregate.inputColumnId : undefined)
      .toBe(sourceColumnForRole(trace, 'NUMERIC')?.id);
    expect(candidateAggregate?.outputColumnId).toBe(trace.initial.aggregateOutputId);
    expect(Object.fromEntries(candidateGroup.outputs.map((output) => [output.id, output.type]))).toEqual(proposalExpectation.groupOutputTypes);
    expect(candidate.steps.slice(1)).toEqual(traceConstruction.steps.slice(1));
    expect(request.changedStepId).toBe(trace.initial.groupStepId);
    expect(request.snapshotToken).toBe(seedIdentity.snapshotToken);
    expect(request.expectedDraftVersion).toBe(seedIdentity.acceptedVersion);
    expect(request.expectedDraftDigest).toBe(seedIdentity.acceptedDigest);
    expect(request.outputId).toBe(seedIdentity.outputId);
    expect(request.removeStepIds ?? []).toEqual([]);
    expect(await screen.findByTestId('construction-proposal-ready')).toBeTruthy();
    expect(screen.getByTestId('construction-apply-proposal')).not.toBeDisabled();
    expect(harness.client.preview).not.toHaveBeenCalled();
  });

  it.each(operationHistoryTraceFile.cases)('replays every generated operation trace through ConstructionReshapeEditor: $id', async (trace) => {
    const construction = constructionForTrace(trace);
    const acceptedDocument = documentForTrace(trace, construction);
    const acceptedWorkspace = workspaceForTrace(trace, acceptedDocument);
    if (isSeedPreparation) {
      capturedSeeds.set(trace.id, { acceptedDocument, acceptedWorkspace });
      return;
    }
    if (isPanelCheck) return;
    vi.useFakeTimers();
    const contextFor = (actionIndex: number) =>
      `${trace.id} at action ${actionIndex}: ${JSON.stringify(trace.actions.slice(0, actionIndex + 1))}`;
    const seedIdentity = operationHistorySeedIdentityById.get(trace.id);
    if (!seedIdentity) throw new Error(`${trace.id}: Go seed identity is missing.`);
    const capabilities = capabilitiesForTrace(trace, construction, seedIdentity);
    const outputId = seedIdentity.outputId;
    harness.replayCapabilities = capabilities;
    const capabilitiesRequest: GetConstructionCapabilitiesArgs = {
      project: 'HTAN_INT/BForePC',
      explorerId: 'builder',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: seedIdentity.snapshotToken,
      expectedDraftVersion: seedIdentity.acceptedVersion,
      expectedDraftDigest: seedIdentity.acceptedDigest,
      outputId,
      stageId: 'source_projection',
      groupStepId: trace.initial.groupStepId,
    };
    const ref = React.createRef<EditorReplayHandle>();
    render(<LifecycleEditorReplay ref={ref} construction={construction} capabilitiesRequest={capabilitiesRequest} />);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByTestId('construction-reshape-editor'), `seed failed to open: ${trace.id}`).toBeTruthy();

    const keySources = (trace.initial.sourceColumns ?? []).filter((column) => roleForSourceColumn(column) !== 'NUMERIC');
    const inputKey = (selectedRole: GroupKeyRole | undefined, actionIndex: number) => {
      for (const source of keySources) {
        const role = roleForSourceColumn(source);
        if (role === 'NUMERIC') continue;
        const checkbox = screen.getByRole('checkbox', { name: `Group by ${source.label ?? source.path ?? source.id}` });
        if (!(checkbox instanceof HTMLInputElement)) throw new Error(`${contextFor(actionIndex)}: expected a checkbox for ${source.id}.`);
        const shouldBeChecked = selectedRole === role;
        if (checkbox.checked !== shouldBeChecked) fireEvent.click(checkbox);
      }
    };

    let candidateActionIndex: number | undefined;
    let previousCandidateKeyOutputId = trace.initial.keyOutputId;
    const checkpoints: Array<Record<string, unknown>> = [];
    for (const [actionIndex, action] of trace.actions.entries()) {
      if (action.kind === 'editAggregate') {
        if (action.aggregate !== 'COUNT_ROWS' && action.aggregate !== 'MEAN') {
          throw new Error(`${contextFor(actionIndex)}: unsupported aggregate ${action.aggregate}.`);
        }
        fireEvent.change(screen.getByRole('combobox', { name: 'Summary 1' }), { target: { value: action.aggregate } });
        candidateActionIndex = actionIndex;
        continue;
      }
      if (action.kind === 'editKey' || action.kind === 'restoreKey') {
        const selectedRole = action.key === 'A' ? 'KEY_A' : action.key === 'B' ? 'KEY_B' : undefined;
        if (action.key !== 'A' && action.key !== 'B' && action.key !== 'none') {
          throw new Error(`${contextFor(actionIndex)}: unsupported key selector ${action.key}.`);
        }
        inputKey(selectedRole, actionIndex);
        candidateActionIndex = actionIndex;
        continue;
      }
      if (action.kind === 'cancel') {
        ref.current?.cancel();
        continue;
      }
      if (action.kind === 'apply' || action.kind === 'applyAck') {
        // Apply acknowledgement is checked by the shared Go model replay; this suite owns request construction.
        continue;
      }
      if (action.kind !== 'propose') throw new Error(`${contextFor(actionIndex)}: unsupported action ${action.kind}.`);

      const expectation = trace.expectations.find((entry) => entry.actionIndex === actionIndex);
      expect(expectation, `${contextFor(actionIndex)}: expected model checkpoint`).toBeDefined();
      if (!expectation) throw new Error(`${contextFor(actionIndex)}: missing model checkpoint.`);
      expect(candidateActionIndex, `${contextFor(actionIndex)}: latest Group edit action`).toBe(expectation.candidateActionIndex);
      const intent = ref.current?.candidateIntent();
      expect(intent, `${contextFor(actionIndex)}: editor did not emit a candidate`).toBeDefined();
      if (!intent) throw new Error(`${contextFor(actionIndex)}: editor emitted no candidate intent.`);

      harness.nextProposalStatus = expectation.proposalStatus === 'NEEDS_REPAIR' ? 'NEEDS_REPAIR' : 'READY';
      const requestCount = harness.client.proposeConstruction.mock.calls.length;
      let proposalOutcome: { readonly kind: 'ready' } | { readonly kind: 'rejected'; readonly message: string } | undefined;
      await act(async () => {
        const proposalSettled = ref.current!.proposeCandidateAndWait(intent).then(
          () => ({ kind: 'ready' as const }),
          (error: unknown) => ({ kind: 'rejected' as const, message: error instanceof Error ? error.message : String(error) }),
        );
        await vi.advanceTimersByTimeAsync(300);
        proposalOutcome = await proposalSettled;
      });
      const request = harness.client.proposeConstruction.mock.calls[requestCount]?.[0];
      expect(request, `${contextFor(actionIndex)}: no actual LoomClient proposal request`).toBeDefined();
      if (!request) throw new Error(`${contextFor(actionIndex)}: no actual proposal request was captured.`);
      expect(request.outputId, contextFor(actionIndex)).toBe(outputId);
      expect(request.snapshotToken, contextFor(actionIndex)).toBe(seedIdentity.snapshotToken);
      expect(request.expectedDraftVersion, contextFor(actionIndex)).toBe(seedIdentity.acceptedVersion);
      expect(request.expectedDraftDigest, contextFor(actionIndex)).toBe(seedIdentity.acceptedDigest);
      expect(request.project, contextFor(actionIndex)).toBe('HTAN_INT/BForePC');
      expect(request.explorerId, contextFor(actionIndex)).toBe('builder');
      expect(request.authResourcePath, contextFor(actionIndex)).toBe('/programs/HTAN_INT/projects/BForePC');
      expect(request.changedStepId, contextFor(actionIndex)).toBe(trace.initial.groupStepId);
      expect(request.removeStepIds ?? [], contextFor(actionIndex)).toEqual([]);
      expect(request.candidateConstruction, contextFor(actionIndex)).toEqual(intent.candidateConstruction);
      expect(proposalOutcome?.kind, contextFor(actionIndex)).toBe(expectation.proposalStatus === 'READY' ? 'ready' : 'rejected');

      const candidate = request.candidateConstruction;
      const candidateGroup = candidate.steps.find((step) => step.id === trace.initial.groupStepId);
      expect(candidate, contextFor(actionIndex)).toBeDefined();
      expect(candidate.steps.map((step) => step.id), contextFor(actionIndex)).toEqual(expectation.candidateStepIds ?? expectation.stepIds);
      expect(candidateGroup?.operation.kind, contextFor(actionIndex)).toBe('GROUP');
      if (candidateGroup?.operation.kind !== 'GROUP') throw new Error(`${contextFor(actionIndex)}: captured candidate has no Group.`);
      const aggregate = candidateGroup.operation.group.aggregates?.[0];
      expect(aggregate?.operation, contextFor(actionIndex)).toBe(expectation.candidateAggregate);
      expect(aggregate?.outputColumnId, contextFor(actionIndex)).toBe(trace.initial.aggregateOutputId);
      if (expectation.candidateAggregate === 'MEAN') {
        const numeric = sourceColumnForRole(trace, 'NUMERIC');
        expect(numeric, contextFor(actionIndex)).toBeDefined();
        expect(aggregate && 'inputColumnId' in aggregate ? aggregate.inputColumnId : undefined, contextFor(actionIndex)).toBe(numeric?.id);
      }
      const candidateKey = candidateGroup.operation.group.keys?.find((key) => 'inputColumnId' in key);
      const candidateKeyOutputId = candidateKey?.outputColumnId;
      const relation = expectation.groupKeyOutputRelation;
      if (relation === 'ABSENT') expect(candidateKeyOutputId, contextFor(actionIndex)).toBeUndefined();
      if (relation === 'SAME_AS_ACCEPTED') expect(candidateKeyOutputId, contextFor(actionIndex)).toBe(expectation.acceptedGroupKeyOutputId);
      if (relation === 'FRESH_FROM_ACCEPTED') {
        expect(candidateKeyOutputId, contextFor(actionIndex)).toBeTruthy();
        expect(candidateKeyOutputId, contextFor(actionIndex)).not.toBe(expectation.acceptedGroupKeyOutputId);
        expect(candidateKeyOutputId, contextFor(actionIndex)).not.toBe(previousCandidateKeyOutputId);
      }
      const aggregateOutput = candidateGroup.outputs.find((output) => output.id === trace.initial.aggregateOutputId);
      expect(aggregateOutput?.type, contextFor(actionIndex)).toBe(expectation.groupOutputTypes[trace.initial.aggregateOutputId]);
      const candidateKeySource = candidateKey && 'inputColumnId' in candidateKey
        ? trace.initial.sourceColumns?.find((column) => column.id === candidateKey.inputColumnId)
        : undefined;
      if (candidateKeyOutputId) {
        expect(candidateGroup.outputs.find((output) => output.id === candidateKeyOutputId)?.type, contextFor(actionIndex)).toBe(candidateKeySource?.type);
      }
      expect(candidateGroup.outputs.length, contextFor(actionIndex)).toBe(Object.keys(expectation.groupOutputTypes).length);
      expect(candidate.steps.slice(1), `${contextFor(actionIndex)}: downstream request steps must remain raw accepted steps`).toEqual(construction.steps.slice(1));
      for (const [dependentStepId, inputStepId] of Object.entries(expectation.candidateInputStepIds ?? {})) {
        const dependent = candidate.steps.find((step) => step.id === dependentStepId);
        expect(dependent?.inputs[0], `${contextFor(actionIndex)}: raw ${dependentStepId} input`).toEqual({ kind: 'STEP_OUTPUT', stepId: inputStepId });
      }

      const rawDownstreamInputTypes = Object.fromEntries(trace.initial.downstream.map((dependent) => {
        const columnId = dependent.columnId ?? (dependent.dependency === 'aggregate'
          ? trace.initial.aggregateOutputId
          : expectation.acceptedGroupKeyOutputId ?? 'missing-group-key');
        const type = candidateGroup.outputs.find((output) => output.id === columnId)?.type ?? 'MISSING';
        const rawStep = candidate.steps.find((step) => step.id === dependent.id);
        const input = rawStep?.inputs[0];
        return [dependent.id, {
          columnId,
          type,
          inputStepId: input?.kind === 'STEP_OUTPUT' ? input.stepId : undefined,
        }];
      }));
      const fingerprint = actualRequestFingerprint(request);
      actualRequestFingerprints.add(fingerprint);
      checkpoints.push({
        actionIndex,
        acceptedDocument,
        acceptedWorkspace: workspaceForTrace(trace, acceptedDocument),
        proposalRequest: request,
        candidateConstruction: candidate,
        changedStepId: request.changedStepId,
        removeStepIds: request.removeStepIds ?? [],
        expected: {
          ...expectation,
          actualCandidateGroupKeyOutputId: candidateKeyOutputId ?? null,
          actualRequestFingerprint: fingerprint,
          // The raw request keeps accepted downstream steps; the Go model's downstreamInputTypes are resolved values.
          rawDownstreamInputTypes,
          rawDownstreamOutputTypes: Object.fromEntries(candidate.steps.slice(1).map((step) => [step.id, step.outputs.map((output) => ({ id: output.id, type: output.type }))])),
        },
      });
      if (candidateKeyOutputId) previousCandidateKeyOutputId = candidateKeyOutputId;
    }
    capturedCandidates.set(trace.id, checkpoints);
  });

  it('accounts for every generated trace and proposal checkpoint', () => {
    if (isPanelCheck) {
      expect(operationHistoryProposalResponses?.cases).toHaveLength(operationHistoryTraceFile.cases.length);
      return;
    }
    if (isSeedPreparation) {
      expect(capturedSeeds.size).toBe(operationHistoryTraceFile.cases.length);
      expect(capturedCandidates.size).toBe(0);
      return;
    }
    const proposalCount = operationHistoryTraceFile.cases.reduce((count, trace) =>
      count + trace.actions.filter((action) => action.kind === 'propose').length, 0);
    const capturedCount = [...capturedCandidates.values()].reduce((count, checkpoints) => count + checkpoints.length, 0);
    const expectedFingerprintCount = new Set(operationHistoryTraceFile.cases.flatMap((trace) =>
      trace.expectations.flatMap((expectation) => expectation.requestFingerprint ? [expectation.requestFingerprint] : []),
    )).size;
    expect(capturedCandidates.size).toBe(operationHistoryTraceFile.cases.length);
    expect([...capturedCandidates.keys()].sort()).toEqual(operationHistoryTraceFile.cases.map((trace) => trace.id).sort());
    expect(capturedCount).toBe(proposalCount);
    console.info(`Operation-history actual replay: ${capturedCandidates.size} traces, ${capturedCount} proposal checkpoints, ${actualRequestFingerprints.size} exact request fingerprints; model has ${expectedFingerprintCount} fingerprints.`);
  });

  it('renders the Go-resolved key cascade before Apply and preserves the raw editor request', () => {
    if (isSeedPreparation) {
      expect(capturedSeeds.size).toBe(operationHistoryTraceFile.cases.length);
      return;
    }
    if (!isPanelCheck) return;
    const responseFile = operationHistoryProposalResponses;
    if (!responseFile) throw new Error('Go proposal response file was not loaded.');
    if (!operationHistoryCandidatePath) throw new Error('Panel-check mode requires BUILDER_OPERATION_CANDIDATES.');
    const candidateFile = operationHistoryCandidateFileSchema.parse(JSON.parse(readFileSync(operationHistoryCandidatePath, 'utf8')));
    const traceById = new Map(operationHistoryTraceFile.cases.map((trace) => [trace.id, trace]));
    const responseRows = responseFile.cases.flatMap((entry) => entry.checkpoints.map((checkpoint) => ({
      id: entry.id,
      actionIndex: checkpoint.actionIndex,
      proposalResponse: checkpoint.proposalResponse,
      previewResult: checkpoint.previewResult,
    })));
    const candidateRows = candidateFile.cases.flatMap((entry) => entry.checkpoints.map((checkpoint) => ({
      id: entry.id,
      checkpoint,
    })));
    const responseByKey = new Map<string, OperationHistoryServiceProposalResponse>(responseRows.map((row) => [
      `${row.id}\u0000${row.actionIndex}`,
      row.proposalResponse,
    ]));
    expect(responseByKey.size, 'Go response keys must be unique').toBe(responseRows.length);
    expect(candidateRows).toHaveLength(responseRows.length);
    const candidateKeys = new Set(candidateRows.map((row) => `${row.id}\u0000${row.checkpoint.actionIndex}`));
    expect([...candidateKeys].sort()).toEqual([...responseByKey.keys()].sort());
    for (const row of responseRows) {
      if (row.proposalResponse.previewStatus === 'PREVIEW_PENDING') {
        expect(row.previewResult.receiptId, `${row.id} action ${row.actionIndex}: Service.Preview receipt`).toBe(row.proposalResponse.proposalId);
        expect(row.previewResult.outputId, `${row.id} action ${row.actionIndex}: Service.Preview output`).toBe(row.proposalResponse.outputId);
        expect(row.previewResult.summary.Complete, `${row.id} action ${row.actionIndex}: Service.Preview completes`).toBe(true);
      }
    }
    const cascadeTraceId = 'operation-history-group-key-cascade-count-to-mean-ready-preserves-calculate';
    const cascadeTrace = traceById.get(cascadeTraceId);
    expect(cascadeTrace, 'combined COUNT_ROWS→MEAN key-cascade trace must be present').toBeDefined();
    if (!cascadeTrace) throw new Error('Combined COUNT_ROWS→MEAN key-cascade trace is missing.');
    expect(cascadeTrace.actions.slice(0, 3)).toEqual([
      { kind: 'editAggregate', aggregate: 'MEAN' },
      { kind: 'editKey', key: 'none' },
      { kind: 'propose' },
    ]);
    const cascade = responseRows.find((row) => row.id === cascadeTraceId && row.actionIndex === 2 &&
      row.proposalResponse.previewStatus === 'PREVIEW_PENDING' && (row.proposalResponse.dependencyImpact.removedStepIds?.length ?? 0) > 0);
    expect(cascade, 'Go responses must include a proposal that cascades a group-key Filter while retaining an aggregate Calculate').toBeDefined();
    if (!cascade) throw new Error('No actual Go response demonstrates the key-dependent Filter cascade.');
    const trace = cascadeTrace;
    if (!trace) throw new Error(`Go cascade response refers to unknown trace ${cascade.id}.`);
    const rawCheckpoint = candidateRows.find((row) => row.id === cascade.id && row.checkpoint.actionIndex === cascade.actionIndex)?.checkpoint;
    expect(rawCheckpoint, `${cascade.id} action ${cascade.actionIndex}: raw request checkpoint exists`).toBeDefined();
    if (!rawCheckpoint) throw new Error('Raw request checkpoint for Go cascade is missing.');
    const modelExpectation = trace.expectations.find((entry) => entry.actionIndex === cascade.actionIndex);
    expect(rawCheckpoint.proposalRequest.candidateConstruction.steps.map((step) => step.id))
      .toEqual(modelExpectation?.candidateStepIds ?? modelExpectation?.stepIds);
    const rawRequest = rawCheckpoint.proposalRequest;
    const rawFilterId = trace.initial.downstream.find((step) => step.kind === 'FILTER' && step.dependency === 'group-key')?.id;
    const calculateStep = trace.initial.downstream.find((step) => step.kind === 'CALCULATE' && step.dependency === 'aggregate');
    const calculateId = calculateStep?.id;
    const calculateOutputId = calculateStep?.outputColumnId;
    expect(rawFilterId).toBeDefined();
    expect(calculateId).toBeDefined();
    expect(calculateOutputId).toBeDefined();
    expect(rawRequest.candidateConstruction.steps.map((step) => step.id)).toContain(rawFilterId);
    expect(rawRequest.candidateConstruction.steps.map((step) => step.id)).toContain(calculateId);
    expect(rawRequest.candidateConstruction.steps.slice(1)).toEqual(constructionForTrace(trace).steps.slice(1));
    const rawCalculate = rawRequest.candidateConstruction.steps.find((step) => step.id === calculateId);
    expect(rawCalculate?.operation.kind).toBe('DERIVE');
    if (rawCalculate?.operation.kind !== 'DERIVE') throw new Error('Raw candidate lost its Calculate step.');
    expect(rawCalculate.operation.derive.left).toEqual({ kind: 'COLUMN', columnId: trace.initial.aggregateOutputId });
    expect(rawCalculate.operation.derive.right).toEqual({ kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 1 } });
    expect(rawCalculate.outputs.find((output) => output.id === calculateOutputId)?.type).toBeUndefined();

    const serviceResponse = cascade.proposalResponse;
    expect(serviceResponse.previewStatus).toBe('PREVIEW_PENDING');
    const preview = cascade.previewResult;
    const removedStepIds = serviceResponse.dependencyImpact.removedStepIds ?? [];
    expect(removedStepIds).toEqual([rawFilterId]);
    const resolvedSteps = serviceResponse.candidateConstruction.steps;
    expect(resolvedSteps.map((step) => step.id)).toEqual([trace.initial.groupStepId, calculateId]);
    const resolvedGroup = resolvedSteps.find((step) => step.id === trace.initial.groupStepId);
    expect(resolvedGroup?.operation.kind).toBe('GROUP');
    if (resolvedGroup?.operation.kind !== 'GROUP') throw new Error('Go-resolved candidate lost its Group step.');
    const resolvedCalculate = resolvedSteps.find((step) => step.id === calculateId);
    expect(resolvedCalculate?.operation.kind).toBe('DERIVE');
    if (resolvedCalculate?.operation.kind !== 'DERIVE') throw new Error('Go-resolved candidate lost its Calculate step.');
    expect(resolvedCalculate.inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: trace.initial.groupStepId }]);
    expect(modelExpectation, `${cascade.id} action ${cascade.actionIndex}: model proposal checkpoint exists`).toBeDefined();
    if (!modelExpectation) throw new Error('Go cascade has no corresponding model checkpoint.');
    const resolvedGroupTypes = Object.fromEntries(resolvedGroup.outputs.map((output) => [output.id, output.type]));
    expect(resolvedGroupTypes).toEqual(modelExpectation.groupOutputTypes);
    expect(resolvedCalculate.outputs.find((output) => output.id === calculateOutputId)?.type).toBeUndefined();
    const calculateStage = serviceResponse.stages.find((stage) => stage.id === calculateId);
    const calculateOutput = calculateStage?.columns.find((output) => output.id === calculateOutputId);
    expect(calculateOutput?.type).toBe('decimal');
    expect(modelExpectation.downstreamOutputTypes?.[calculateId ?? '']).toEqual({ columnId: calculateOutputId, type: 'decimal' });
    expect(modelExpectation.downstreamInputTypes[calculateId ?? '']?.inputStepId).toBe(trace.initial.groupStepId);
    const calculateIndex = resolvedSteps.findIndex((step) => step.id === calculateId);
    expect(calculateIndex).toBe(1);

    const baseConstruction = constructionForTrace(trace);
    const proposalId = serviceResponse.proposalId;
    expect(proposalId, `${cascade.id} action ${cascade.actionIndex}: actual proposal receipt exists`).toBeDefined();
    if (!proposalId) throw new Error('Go cascade response has no proposal receipt.');
    expect(preview.receiptId).toBe(proposalId);
    expect(preview.receiptId).toBe(proposalId);
    expect(preview.outputId).toBe(serviceResponse.outputId);
    expect(preview.summary.Output).toBe(serviceResponse.outputId);
    expect(preview.summary.RowCount).toBe(0);
    expect(preview.summary.Complete).toBe(true);
    expect(preview.summary.Truncated).toBe(false);
    expect(preview.columns.map((column) => column.publicColumn)).toEqual(preview.summary.Columns);
    const panelPreview = panelPreviewFromServicePreview(preview);
    expect(panelPreview.rows).toEqual([]);
    expect(panelPreview.rowCount).toBe(preview.summary.RowCount);
    expect(panelPreview.columns.map((column) => column.column)).toEqual(preview.summary.Columns);
    // Panel-only presentation state: the Go lifecycle.Service DTO stays PREVIEW_PENDING;
    // its separately exported Service.Preview result supplies the completed preview.
    // This adapts that internal result to the panel type and is not HTTP wire JSON.
    const response = constructionProposalResponseSchema.parse({
      ...serviceResponse,
      previewStatus: 'READY',
      preview: panelPreview,
    });
    const state = { status: 'ready' as const, response, preview: panelPreview };
    const canApply = constructionProposalIsApplicable(state, {
      outputId: response.outputId,
      snapshotToken: response.snapshotToken,
      draftVersion: response.draftVersion,
      draftDigest: response.draftDigest,
    });
    const onApply = vi.fn();
    render(
      <ConstructionProposalPanel
        state={state}
        canApply={canApply}
        onApply={onApply}
        onCancel={vi.fn()}
        onRetry={vi.fn()}
        baseConstruction={baseConstruction}
      />,
    );
    const removalSummary = screen.getByTestId('construction-removal-summary');
    expect(removalSummary).toHaveTextContent('This edit also removes 1 dependent step.');
    const listedRemovalIds = Array.from(removalSummary.querySelectorAll('[data-testid^="construction-removal-step-"]'))
      .map((element) => element.getAttribute('data-testid')?.replace('construction-removal-step-', ''));
    expect(listedRemovalIds).toEqual(removedStepIds);
    expect(screen.getByTestId(`construction-removal-step-${rawFilterId}`)).toBeTruthy();
    const apply = screen.getByTestId('construction-apply-proposal');
    expect(apply).not.toBeDisabled();
    expect(onApply).not.toHaveBeenCalled();

    for (const row of responseRows) {
      const checkpoint = candidateRows.find((candidate) => candidate.id === row.id && candidate.checkpoint.actionIndex === row.actionIndex)?.checkpoint;
      expect(checkpoint, `${row.id} action ${row.actionIndex}: request checkpoint exists for Go response`).toBeDefined();
      expect(responseByKey.get(`${row.id}\u0000${row.actionIndex}`)).toEqual(row.proposalResponse);
    }
  });

  afterAll(() => {
    if (isSeedPreparation) {
      const seedPath = process.env.BUILDER_OPERATION_SEEDS;
      if (!seedPath) throw new Error('Seed preparation requires BUILDER_OPERATION_SEEDS.');
      mkdirSync(dirname(seedPath), { recursive: true });
      writeFileSync(seedPath, `${JSON.stringify({
        schemaVersion: 1,
        cases: operationHistoryTraceFile.cases.map((trace) => ({
          id: trace.id,
          acceptedWorkspace: capturedSeeds.get(trace.id)?.acceptedWorkspace,
        })),
      }, null, 2)}\n`);
      return;
    }
    if (isPanelCheck) {
      return;
    }
    const artifactPath = operationHistoryCandidatePath;
    if (!artifactPath) throw new Error('Replay mode requires BUILDER_OPERATION_CANDIDATES.');
    mkdirSync(dirname(artifactPath), { recursive: true });
    writeFileSync(artifactPath, `${JSON.stringify({
      schemaVersion: 1,
      cases: operationHistoryTraceFile.cases.map((trace) => ({
        id: trace.id,
        checkpoints: capturedCandidates.get(trace.id) ?? [],
      })),
    }, null, 2)}\n`);
  });
});
