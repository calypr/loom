// @vitest-environment jsdom

import React from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { builderWorkspaceNullMocks } from '../../../testSupport/builderWorkspaceMocks';
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
import BuilderWorkspace from './BuilderWorkspace';
import { ConstructionProposalPanel, constructionProposalIsApplicable } from './constructionWorkspace/ConstructionProposalPanel';
import { ConstructionReshapeEditor } from './constructionOperations/ConstructionReshapeEditor';
import { useConstructionLifecycle, type ConstructionCandidateIntent } from './constructionWorkspace/useConstructionLifecycle';

const harness = vi.hoisted(() => {
  const client = {
    proposeSourceRelationship: vi.fn(),
    proposeColumnPolicy: vi.fn(),
    describeAuthoringNetwork: vi.fn().mockResolvedValue({network:{outputId:"table-1"},draftVersion:0}),
    getSelection: vi.fn(),
    listRowDefinitionChoices: vi.fn(),
    searchPopulationRoutes: vi.fn(),
    createSelection: vi.fn(),
    configuredColumnContextsQuery: vi.fn(),
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
    applyCommands: vi.fn(),
    configuredContext: undefined as unknown,
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
    useApplyExplorerBuilderCommandsV2Mutation: () => [harness.applyCommands, {isLoading:false}],
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
    useResolveConfiguredColumnContextsQuery: () => ({ data: harness.configuredContext, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('./useAutomaticPreview', () => ({ useAutomaticPreview: () => ({ error: undefined, isLoading: false }) }));
vi.mock('./hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('./hooks/usePortalHost', () => ({ usePortalHost: () => null }));

vi.mock('./components/BuilderToolbar', () => ({ BuilderToolbar: () => null, NewExplorerControl: () => null }));
vi.mock('./components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('./components/GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: () => null }));
vi.mock('./components/ConceptCatalog', () => ({ ConceptCatalog: () => null }));
vi.mock('./components/RowRootPicker', () => ({ RowRootPicker: () => null }));
vi.mock('./components/PreviewTable', async (importOriginal) => ({
  ...await importOriginal<typeof import('./components/PreviewTable')>(),
  PreviewTable: () => null,
}));
vi.mock('./components/DataframeContractPanel', () => builderWorkspaceNullMocks.DataframeContractPanel);
vi.mock('./components/PopulationPanel', () => builderWorkspaceNullMocks.PopulationPanel);
vi.mock('./components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('./components/RowDefinitionPanel', () => ({ RowDefinitionPanel: () => null }));
vi.mock('./components/RowDefinitionSettingsPanel', () => ({
  RowDefinitionSettingsPanel: ({ openActionRef }: { openActionRef?: { current: (() => void) | undefined } }) => {
    const [opened, setOpened] = React.useState(false);
    if (openActionRef) openActionRef.current = () => setOpened(true);
    return opened ? <section data-testid="row-definition-settings-opened" aria-label="Row definition settings" /> : null;
  },
  rowScopeLabel: () => '',
}));
vi.mock('./components/InterpretationPanel', () => builderWorkspaceNullMocks.InterpretationPanel);
vi.mock('./constructionWorkspace/FrameSourcePanel', () => ({ FrameSourcePanel: () => null }));
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
    initial: z.object({
      tableId: z.string().optional(),
      workspaceContext: z.object({
        snapshotToken: z.string(),
        outputId: z.string(),
      }).passthrough(),
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

const localOperationHistoryCases = [{
  id: 'operation-history-cancel-reverts-accepted-history-filter',
  initial: {
    tableId: 'table-1',
    workspaceContext: {
      snapshotToken: 'snapshot-operation-history',
      outputId: 'table-1',
    },
    sourceColumns: [
      { id: 'gender-id', path: 'gender', label: 'Gender', type: 'string', role: 'KEY_A' },
      { id: 'active-id', path: 'active', label: 'Active', type: 'boolean', role: 'KEY_B' },
      { id: 'multiple-birth-integer-id', path: 'multipleBirthInteger', label: 'Multiple birth integer', type: 'integer', role: 'NUMERIC' },
    ],
    aggregate: 'COUNT_ROWS', numericInputPresent: true, keyA: true, keyB: true,
    selectedKey: 'A', keyOutputId: groupKeyOutputId, aggregateOutputId,
    groupStepId, downstream: [{ id: filterStep.id, kind: 'FILTER', dependency: 'aggregate', columnId: aggregateOutputId }],
  },
  actions: [
    { kind: 'editAggregate', aggregate: 'MEAN' }, { kind: 'propose' },
    { kind: 'cancel' }, { kind: 'editAggregate', aggregate: 'COUNT_ROWS' }, { kind: 'propose' },
  ],
  expectations: [
    { actionIndex: 1, candidateActionIndex: 0, proposalStatus: 'READY',
      stepIds: [groupStepId, filterStep.id], groupOutputTypes: { [groupKeyOutputId]: 'string', [aggregateOutputId]: 'decimal' },
      downstreamInputTypes: {}, acceptedAggregate: 'COUNT_ROWS', candidateAggregate: 'MEAN', acceptedChangesOnApply: false,
      groupKeyOutputRelation: 'SAME_AS_ACCEPTED', acceptedGroupKeyOutputId: groupKeyOutputId },
    { actionIndex: 4, candidateActionIndex: 3, proposalStatus: 'READY',
      stepIds: [groupStepId, filterStep.id], groupOutputTypes: { [groupKeyOutputId]: 'string', [aggregateOutputId]: 'integer' },
      downstreamInputTypes: {}, acceptedAggregate: 'COUNT_ROWS', candidateAggregate: 'COUNT_ROWS', acceptedChangesOnApply: false,
      groupKeyOutputRelation: 'SAME_AS_ACCEPTED', acceptedGroupKeyOutputId: groupKeyOutputId },
  ],
}];
const frozenOperationHistoryCases = operationHistoryTraceFileSchema.parse(JSON.parse(
  readFileSync(resolve(process.cwd(), 'testSupport/operationHistoryJourneys.json'), 'utf8'),
)).cases;
const operationHistoryTraceFile = operationHistoryTraceFileSchema.parse({
  schemaVersion: 1,
  cases: [...frozenOperationHistoryCases, ...localOperationHistoryCases],
});
type OperationHistorySeedIdentity = {
  readonly id: string;
  readonly acceptedVersion: number;
  readonly acceptedDigest: string;
  readonly snapshotToken: string;
  readonly outputId: string;
};
const operationHistorySeedIdentityById = new Map<string, OperationHistorySeedIdentity>(
  operationHistoryTraceFile.cases.map((trace) => [trace.id, {
    id: trace.id,
    acceptedVersion: 1,
    acceptedDigest: acceptedState.draftDigest,
    snapshotToken: trace.initial.workspaceContext.snapshotToken,
    outputId: trace.initial.workspaceContext.outputId,
  }]),
);
type OperationHistoryTraceFile = z.infer<typeof operationHistoryTraceFileSchema>;
type OperationHistoryTrace = OperationHistoryTraceFile['cases'][number];
type OperationHistorySourceColumn = NonNullable<OperationHistoryTrace['initial']['sourceColumns']>[number];
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
    sourceInput: { supported: false, stageId: selectedStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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

const operationRow = (stepId: string): HTMLElement => {
  const item = globalThis.document.querySelector<HTMLElement>(
    `[data-testid="traversal-scope-navigator"] [data-operation-id="${stepId}"]`,
  );
  if (!item) throw new Error(`Operation ${stepId} is not in the traversal navigator.`);
  return item;
};
const clickHistoryEdit = async (stepId: string) => {
  const item = operationRow(stepId);
  const button = item.querySelector<HTMLButtonElement>('button[aria-label^="Edit "]');
  if (!button) throw new Error(`Operation ${stepId} has no edit action.`);
  fireEvent.click(button);
};
const clickHistoryRemove = (stepId: string) => {
  const item = operationRow(stepId);
  const button = item.querySelector<HTMLButtonElement>('button[aria-label^="Remove "]');
  if (!button) throw new Error(`Operation ${stepId} has no remove action.`);
  fireEvent.click(button);
};

describe('BuilderWorkspace operation history actual candidate capture', () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    harness.configuredContext=undefined;
    harness.builderState = acceptedState;
    harness.replayCapabilities = undefined;
    harness.nextProposalStatus = 'READY';
    harness.proposalCounter = 0;
    harness.client.getConstructionCapabilities.mockReset().mockImplementation(async (args) => {
      if (harness.replayCapabilities) return harness.replayCapabilities;
      const response = {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        sourceInput: { supported: false, stageId: args.stageId, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
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
    harness.getSuggestions.mockReset().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({ snapshotToken: catalog.snapshotToken, candidates: [] }),
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('regression: source-step Apply returns focus to the same fact after draft remount', async () => {
    const imported=structuredClone(acceptedState);
    const document=imported.workspace!.documents[0];
    document.importedRecipe={name:'default',translationVersion:'1',digest:'a'.repeat(64),output:'table-1'};
    document.importedSource={steps:[{id:'fields',kind:'FIELD_GROUP',title:'Patient fields',summary:'Patient source values',editable:true}]};
    harness.builderState=imported;
    harness.client.proposeSourceRelationship.mockResolvedValue({proposalId:'candidate-source',candidateReceiptId:'candidate-source',outputId:'table-1',snapshotToken:catalog.snapshotToken,draftVersion:1,draftDigest:acceptedState.draftDigest,baseDocumentDigest:'base',candidateWorkspaceDigest:'next-draft',previewStatus:'READY',sampledRows:1,sampled:false,affectedColumns:[],notices:[],candidateDocument:document,candidateNetwork:{owner:'IMPORTED_SOURCE',outputId:'table-1',sourceRows:document.rows,occurrences:[],bindings:[],operations:[]},candidateRowIdentity:{outputId:'table-1',sourceDefinition:document.rows,stages:[],relationships:[]},preview:previewFor('candidate-source','table-1')});
    harness.applyCommands.mockImplementation((args)=>({unwrap:async()=>({commandId:args.commandId,workspace:imported.workspace,draftVersion:2,draftDigest:'next-draft',results:[],diagnostics:[]})}));
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    const initiator=await screen.findByRole('button',{name:'Edit source operation 1: Fields from Patient'});initiator.focus();fireEvent.click(initiator);
    fireEvent.click(screen.getByRole('button',{name:'Preview source step'}));
    await waitFor(()=>expect(screen.getByRole('button',{name:'Apply source step'})).toBeEnabled());
    expect(harness.client.preview.mock.calls.filter(([args]) => args.receiptId === 'candidate-source')).toHaveLength(0);
    const apply=screen.getByRole('button',{name:'Apply source step'});apply.focus();fireEvent.click(apply);
    await waitFor(()=>expect(screen.queryByTestId('imported-source-step-editor')).toBeNull());
    const replacement=screen.getByRole('button',{name:'Edit source operation 1: Fields from Patient'});
    await waitFor(()=>expect(globalThis.document.activeElement).toBe(replacement));
  });

  it('regression: malformed column policy identity reports stale instead of endless loading', async () => {
    const state=structuredClone(acceptedState);
    (state.catalog as unknown as {candidates:unknown[]}).candidates=[{candidateId:'gender-field',nodeId:'patient-node',fieldPath:'gender',projectionModes:['VALUE','ALL']}];
    harness.builderState=state;
    harness.configuredContext={snapshotToken:catalog.snapshotToken,draftVersion:1,draftDigest:state.draftDigest,columns:[{outputId:'table-1',column:'gender',occurrenceId:'base',resolution:{state:'READY',capabilityCandidateIds:['gender-field']}}]};
    const document=state.workspace!.documents[0];
    harness.client.proposeColumnPolicy.mockResolvedValue({proposalId:'candidate-column',candidateReceiptId:'candidate-column',outputId:'table-1',snapshotToken:catalog.snapshotToken,draftVersion:99,draftDigest:'wrong-draft',baseDocumentDigest:'base',candidateWorkspaceDigest:'next-draft',previewStatus:'READY',sampledRows:1,sampled:false,affectedColumns:[],notices:[],candidateDocument:document,candidateNetwork:{owner:'DOCUMENT',outputId:'table-1',sourceRows:document.rows,occurrences:[],bindings:[],operations:[]},candidateRowIdentity:{outputId:'table-1',sourceDefinition:document.rows,stages:[],relationships:[]}});
    harness.client.preview.mockResolvedValue(previewFor('candidate-column','table-1'));
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    fireEvent.click(await screen.findByRole('button',{name:'Value policy for Gender'}));
    fireEvent.change(screen.getByRole('combobox',{name:'Value policy for Gender'}),{target:{value:'ALL'}});
    fireEvent.click(screen.getByRole('button',{name:'Preview column policy'}));
    await waitFor(()=>expect(harness.client.preview).toHaveBeenCalledOnce());
    await waitFor(()=>expect(screen.queryByRole('button',{name:'Working…'})).toBeNull());
    expect(screen.getByRole('button',{name:'Preview column policy'})).toBeDisabled();
    expect(screen.getByRole('button',{name:'Apply column policy'})).toBeDisabled();
    expect(screen.getByRole('button',{name:'Cancel column edit'})).toBeInTheDocument();
  });

  it('regression: Cancel aborts a pending policy preview and ignores its late response', async () => {
    const state=structuredClone(acceptedState);
    (state.catalog as unknown as {candidates:unknown[]}).candidates=[{candidateId:'gender-field',nodeId:'patient-node',fieldPath:'gender',projectionModes:['VALUE','ALL']}];
    harness.builderState=state;
    harness.configuredContext={snapshotToken:catalog.snapshotToken,draftVersion:1,draftDigest:state.draftDigest,columns:[{outputId:'table-1',column:'gender',occurrenceId:'base',resolution:{state:'READY',capabilityCandidateIds:['gender-field']}}]};
    let resolve!: (value:unknown)=>void;
    harness.client.proposeColumnPolicy.mockImplementation(()=>new Promise(done=>{resolve=done;}));
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    fireEvent.click(await screen.findByRole('button',{name:'Value policy for Gender'}));
    fireEvent.change(screen.getByRole('combobox',{name:'Value policy for Gender'}),{target:{value:'ALL'}});
    fireEvent.click(screen.getByRole('button',{name:'Preview column policy'}));
    const signal=harness.client.proposeColumnPolicy.mock.calls.at(-1)![1] as AbortSignal;
    fireEvent.click(screen.getByRole('button',{name:'Cancel column edit'}));
    expect(signal.aborted).toBe(true);
    await act(async()=>{resolve({candidateReceiptId:'late'});await Promise.resolve();});
    expect(harness.client.preview).not.toHaveBeenCalled();
    expect(screen.getByTestId('recipe-result-identity')).toHaveTextContent('Saved recipe result');
    expect(screen.queryByTestId('recipe-column-policy-inspector')).toBeNull();
  });

  it('regression: blocks replacing an open Group edit with removal before Apply or Cancel', async () => {
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    await clickHistoryEdit('group-step');
    await screen.findByTestId('construction-reshape-editor');
    fireEvent.change(screen.getByRole('combobox', { name: 'Summary 1' }), { target: { value: 'MEAN' } });
    await waitFor(() => expect(harness.client.proposeConstruction).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByTestId('recipe-result-identity').textContent).toBe('Proposed recipe result'));
    clickHistoryRemove('group-step');
    await new Promise(resolve=>setTimeout(resolve,700));
    expect(harness.client.proposeConstruction.mock.calls).toHaveLength(1);
  });

  it('regression: Open Define a row actually reveals the row editor from network-only presentation', async () => {
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    fireEvent.click(await screen.findByRole('button', {name:'Edit row definition'}));
    await screen.findByTestId('row-definition-settings-opened');
    expect(screen.getByTestId('row-definition-settings-opened').closest('[hidden]')).toBeNull();
  });

  it('regression: proposed Group grain stays coherent in the current row editor', async () => {
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    await clickHistoryEdit('group-step');
    await screen.findByTestId('construction-reshape-editor');
    fireEvent.click(screen.getByRole('checkbox', {name:'Group by Gender'}));
    await waitFor(()=>expect(harness.client.proposeConstruction).toHaveBeenCalledOnce());
    await waitFor(()=>expect(screen.getByTestId('recipe-result-identity').textContent).toBe('Proposed recipe result'));
    expect(screen.getByTestId('construction-group-grain-summary').textContent?.trim())
      .toBe('With no fields selected, one row summarizes all input rows.');
    expect(screen.getByTestId('recipe-result-identity')).toHaveTextContent('Proposed recipe result');
  });

  it('regression: source-step edit replaces the previous Group inspector', async () => {
    const imported=structuredClone(acceptedState);
    imported.workspace!.documents[0].importedRecipe={name:'default',translationVersion:'1',digest:'a'.repeat(64),output:'table-1'};
    imported.workspace!.documents[0].importedSource={steps:[{id:'fields',kind:'FIELD_GROUP',title:'Patient fields',summary:'Patient source values',editable:true}]};
    harness.builderState=imported;
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
    await clickHistoryEdit('group-step');
    await screen.findByTestId('construction-reshape-editor');
    fireEvent.click(screen.getByRole('button',{name:'Close operation editor'}));
    fireEvent.click(await screen.findByRole('button',{name:'Edit source operation 1: Fields from Patient'}));
    expect(screen.getByTestId('imported-source-step-editor')).toBeTruthy();
    expect(screen.queryByTestId('construction-reshape-editor')).toBeNull();
  });

  it('keeps the saved Group operation and identifiers across remounts', async () => {
    const localWorkspace = structuredClone(workspace);
    localWorkspace.documents[0].output.id = 'patients';
    for (const tab of localWorkspace.tabs) tab.outputId = 'patients';
    const localBefore = { ...acceptedState, workspace: structuredClone(localWorkspace) };
    const localConstruction = localWorkspace.documents[0].construction;
    if (!localConstruction || localConstruction.steps[0].operation.kind !== 'GROUP') throw new Error('Local saved Group fixture missing');
    localConstruction.steps[0].operation.group.aggregates = [{ operation: 'MEAN', inputColumnId: 'multiple-birth-integer-id', outputColumnId: aggregateOutputId }];
    for (const step of localConstruction.steps) for (const column of step.outputs) if (column.id === aggregateOutputId) column.type = 'decimal';
    const localSaved = { ...acceptedState, draftVersion: 2, draftDigest: `sha256:${'a'.repeat(64)}`, workspace: localWorkspace };
    const localCapabilities = { snapshotToken: catalog.snapshotToken, draftVersion: 2, draftDigest: localSaved.draftDigest, outputId: 'patients', stageId: sourceStage.id, sourceInput: { supported: false, stageId: sourceStage.id, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] }, baseConstruction: localConstruction, stages: [sourceStage], selectedStage: sourceStage, semanticGroupChoices: [], groupFieldChoices: [], groupRowValueChoices: [], workspaceInputs: [] };
    const record = { id: 'local-saved-group', actionIndex: 3, outputId: 'patients', stageId: sourceStage.id, before: localBefore, after: localSaved, storedWorkspace: localWorkspace, capabilities: localCapabilities };
    const saved = record.after, caps = record.capabilities;
    expect(saved.draftVersion).toBe(record.before.draftVersion + 1);
    expect(saved.draftDigest).not.toBe(record.before.draftDigest);
    expect(saved.workspace).toEqual(record.storedWorkspace);
    expect(caps.draftVersion).toBe(saved.draftVersion);
    expect(caps.draftDigest).toBe(saved.draftDigest);
    expect(caps.snapshotToken).toBe(saved.catalog.snapshotToken);
    expect(caps.outputId).toBe(record.outputId);
    expect(caps.stageId).toBe(record.stageId);
    const construction = saved.workspace?.documents.find(document => document.output.id === record.outputId)?.construction;
    const group = construction?.steps.find(step => step.id === groupStepId);
    expect(group?.operation.kind).toBe('GROUP');
    if (group?.operation.kind !== 'GROUP') throw new Error('Saved Group missing');
    expect(group.operation.group.aggregates?.[0]?.operation).toBe('MEAN');
    expect(construction?.steps.map(step => step.id)).toEqual([groupStepId, filterStep.id]);
    expect(caps.baseConstruction).toEqual(construction);
    for (let mount = 0; mount < 2; mount++) {
      harness.builderState = saved;
      harness.replayCapabilities = caps;
      harness.client.getConstructionCapabilities.mockClear();
      const view = render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);
      await clickHistoryEdit('group-step');
      await screen.findByTestId('construction-reshape-editor');
      expect((screen.getByRole('combobox', { name: 'Summary 1' }) as HTMLSelectElement).value).toBe('MEAN');
      expect(harness.client.getConstructionCapabilities).toHaveBeenCalled();
      for (const [request] of harness.client.getConstructionCapabilities.mock.calls) {
        expect(request, JSON.stringify(request)).toMatchObject({
          expectedDraftVersion: caps.draftVersion,
          expectedDraftDigest: caps.draftDigest,
          snapshotToken: caps.snapshotToken,
          outputId: record.outputId,
        });
      }
      expect(harness.client.getConstructionCapabilities.mock.calls.some(([request]) => request.stageId === record.stageId)).toBe(true);
      expect(harness.client.proposeConstruction).not.toHaveBeenCalled();
      view.unmount();
    }
  });

  it('wires BuilderWorkspace history and the actual Group editor to its proposal request', async () => {
    const trace = operationHistoryTraceFile.cases.find((candidate) => candidate.id === 'operation-history-count-to-mean-accepted-history-filter');
    expect(trace, 'accepted-history Filter schedule must be present').toBeDefined();
    if (!trace) throw new Error('Accepted-history Filter schedule is missing.');
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
    expect(proposalExpectation, 'proposal expectation must exist after the MEAN edit').toBeDefined();
    if (!proposalExpectation) throw new Error('Proposal expectation is missing after the MEAN edit.');
    const actionSequence = ['editAggregate(MEAN)', 'propose'];
    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="builder" />);

    await clickHistoryEdit(trace.initial.groupStepId);
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

  for (const trace of operationHistoryTraceFile.cases) it(`replays each operation schedule through ConstructionReshapeEditor: '${trace.id}'`, async () => {
    const construction = constructionForTrace(trace);
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

      if (candidateKeyOutputId) previousCandidateKeyOutputId = candidateKeyOutputId;
    }
  });

  it('reviews proposed removals before explicit Apply', () => {

    const preview = previewFor('reviewed-cascade', 'table-1');
    const response: ConstructionProposalResponse = {
      proposalId: preview.receiptId, outputId: 'table-1', snapshotToken: catalog.snapshotToken,
      draftVersion: 1, draftDigest: acceptedState.draftDigest,
      baseDocumentDigest: 'sha256:accepted-document', candidateWorkspaceDigest: 'sha256:cascade',
      changedStepId: groupStepId, candidateConstruction: { version: 1, steps: [groupStep] },
      dependencyImpact: { affectedStepIds: [groupStepId, filterStep.id], removedStepIds: [filterStep.id] },
      stages: [sourceStage], previewStatus: 'READY', previewDurationMs: 1, preview,
    };
    const onApply = vi.fn();
    const onCancel = vi.fn();
    const canApply = constructionProposalIsApplicable({ status: 'ready', response, preview }, {
      outputId: 'table-1', snapshotToken: catalog.snapshotToken,
      draftVersion: 1, draftDigest: acceptedState.draftDigest,
    });
    render(<ConstructionProposalPanel state={{ status: 'ready', response, preview }} canApply={canApply}
      baseConstruction={acceptedConstruction} onApply={onApply} onCancel={onCancel} onRetry={vi.fn()} />);
    const summary = screen.getByTestId('construction-removal-summary');
    expect(summary).toHaveTextContent('This edit also removes 1 dependent step.');
    expect(screen.getByTestId(`construction-removal-step-${filterStep.id}`)).toBeTruthy();
    expect(onApply).not.toHaveBeenCalled();
    const apply = screen.getByTestId('construction-apply-proposal');
    expect(apply).not.toBeDisabled();
    fireEvent.click(apply);
    expect(onApply).toHaveBeenCalledOnce();
    expect(onCancel).not.toHaveBeenCalled();
  });

});
