// @vitest-environment jsdom
import React from 'react';
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
  usePopulationMappingMutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
} from '../../react';
import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionChoice,
  ConstructionProposalResponse,
  ConstructionStageDescriptor,
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
  ExplorerBuilderCommand,
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
  RelatedFieldChoiceSearchResponse,
  ExplorerRowDefinition,
} from '../../types';
import BuilderWorkspace from './BuilderWorkspace';

const mockLoomClient = vi.hoisted(() => ({
  browseFrameSourceOptions: vi.fn(),
  browseSemanticInventory: vi.fn(),
  getConstructionCapabilities: vi.fn(),
  getSelection: vi.fn(),
  listRowDefinitionChoices: vi.fn(),
  preview: vi.fn(),
  proposeConstruction: vi.fn(),
  proposeConstructionChoices: vi.fn(),
  searchRelatedFieldChoices: vi.fn(),
  configuredColumnContextsQuery: vi.fn(),
  searchConstructionChoices: vi.fn(),
}));

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  return {
    ...actual,
    useLoomClient: () => mockLoomClient,
    useApplyExplorerBuilderCommandsV2Mutation: vi.fn(),
    useAssessExplorerRowChangeMutation: vi.fn(),
    useCreateExplorerAuthoringMutation: vi.fn(),
    useDeleteExplorerAuthoringMutation: vi.fn(),
    useGetExplorerAuthoringCapabilityV2Query: vi.fn(),
    useGetExplorerAuthoringExplorersQuery: vi.fn(),
    useGetExplorerBuilderStateV2Query: vi.fn(),
    useGetExplorerCandidateSuggestionsV2Mutation: vi.fn(),
    usePreviewExplorerAuthoringV2Mutation: vi.fn(),
    usePopulationMappingMutation: vi.fn(),
    usePublishExplorerAuthoringV2Mutation: vi.fn(),
    useReconcileExplorerBuilderV2Mutation: vi.fn(),
    useResolveConfiguredColumnContextsQuery: mockLoomClient.configuredColumnContextsQuery,
    useResolvePopulationSelectionQuery: vi.fn(() => ({
      data: undefined,
      error: undefined,
      isLoading: false,
      isFetching: false,
      refetch: vi.fn(),
    })),
  };
});

vi.mock('./components/BuilderToolbar', () => ({
  BuilderToolbar: ({ onPreview }: { readonly onPreview: () => void }) => (
    <button type="button" onClick={onPreview}>Preview</button>
  ),
}));
vi.mock('./components/GuidedGraphWorkspace', () => ({
  GuidedGraphWorkspace: ({
    onSelectOccurrence,
  }: {
    readonly onSelectOccurrence: (occurrenceId: string) => void;
  }) => (
    <button
      type="button"
      onClick={() => onSelectOccurrence('saved-report-occurrence')}
    >
      Select saved DiagnosticReport occurrence
    </button>
  ),
}));
vi.mock('./components/ColumnSelector', () => ({ ColumnSelector: () => null }));
vi.mock('./components/DataframeContractPanel', () => ({ DataframeContractPanel: () => null }));
vi.mock('./components/PopulationPanel', () => ({ PopulationPanel: () => null }));
vi.mock('./components/RowDefinitionPanel', () => ({ RowDefinitionPanel: () => null }));
vi.mock('./components/RowDefinitionSettingsPanel', () => ({ RowDefinitionSettingsPanel: () => null }));
vi.mock('./components/TableShapeSettingsPanel', () => ({ TableShapeSettingsPanel: () => null }));
vi.mock('./components/InterpretationPanel', () => ({ InterpretationPanel: () => null }));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const choiceOption: ConstructionChoice['options'][number] = {
  form: 'VALUE',
  shape: 'SCALAR',
  decision: 'DEFAULT',
  preservation: 'PRESERVING',
  rowEffect: 'PRESERVES_ROW_GRAIN',
  support: 'SUPPORTED',
  reason: 'Loom confirms this field preserves the row grain.',
};

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 10 },
    { nodeId: 'report-node', resourceType: 'DiagnosticReport', rowRootEligible: false, populated: true, documentCount: 5 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 20 },
  ],
  edges: [
    { edgeId: 'patient-reports', fromNodeId: 'patient-node', toNodeId: 'report-node', label: 'reports' },
    { edgeId: 'patient-observations', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'observations' },
  ],
  candidates: [],
};

const fieldCandidate = (
  candidateId: string,
  nodeId: string,
  resourceType: string,
  fieldPath: string,
): ExplorerBuilderCandidate => ({
  candidateId,
  nodeId,
  populated: true,
  fieldPath,
  label: `${resourceType} ${fieldPath}`,
  logicalType: 'string',
  cardinality: 'optional_one',
  repeated: false,
  filterable: true,
  chartable: false,
  projectionModes: ['VALUE'],
  defaultProjectionMode: 'VALUE',
  constructionChoice: {
    choiceId: `${candidateId}-local-choice`,
    route: [],
    presentation: {
      summary: `${resourceType}.${fieldPath}`,
      facts: [{ label: 'Field', value: fieldPath }],
    },
    source: {
      kind: 'FIELD',
      candidateId,
      nodeId,
      resourceType,
      path: fieldPath,
      cardinality: 'optional_one',
    },
    options: [choiceOption],
  },
  aggregateOperations: [],
  transformations: {
    temporalReduction: {
      available: false,
      reason: 'No temporal reduction is available for this field.',
      timestampFields: [],
      anchorFields: [],
    },
    unitNormalization: {
      available: false,
      reason: 'No unit normalization is available for this field.',
      presets: [],
    },
  },
  valueTransformations: {
    exactCategoryRecode: { available: false },
    codedValueRecoding: {
      available: false,
      reasonCode: 'NO_CODED_VALUE',
      reason: 'This field has no coded value to recode.',
    },
  },
});

catalog.candidates = [
  fieldCandidate('report-status', 'report-node', 'DiagnosticReport', 'status'),
  fieldCandidate('observation-status', 'observation-node', 'Observation', 'status'),
];

const workspace: ExplorerBuilderWorkspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace',
  explorer: { title: 'Test Explorer' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: 'patients', title: 'Patients' },
    rootResourceType: 'Patient',
    route: {
      occurrenceId: 'base',
      resourceType: 'Patient',
      children: [{
        occurrenceId: 'saved-report-occurrence',
        resourceType: 'DiagnosticReport',
        relationship: 'reports',
      }],
    },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId: 'patients', order: 0, visible: true }],
};

const builderState: ExplorerBuilderState = {
  apiVersion,
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  workspace,
  catalog,
};

const namedGroupMemberColumnId = 'named-cohort-status-column-id';
const namedGroupMemberColumn: ExplorerBuilderWorkspace['documents'][number]['columns'][number] = {
  columnId: namedGroupMemberColumnId,
  column: 'member_status',
  label: 'Member status',
  occurrenceId: 'base',
  source: { kind: 'field', field: { path: 'status', projectionMode: 'VALUE' } },
  table: { visible: true, order: 0 },
};

const namedGroupWorkspace = (policy: 'ALL' | 'ONE'): ExplorerBuilderWorkspace => {
  const baseDocument = workspace.documents[0];
  if (!baseDocument) throw new Error('Test workspace is missing its base document.');
  const rows: Extract<ExplorerRowDefinition, { kind: 'GROUPS' }> = {
    kind: 'GROUPS',
    groups: {
      source: {
        kind: 'EXPLICIT',
        explicit: { revisionId: 'named-cohort-revision', unassignedMemberPolicy: 'ERROR' },
      },
      rowValues: [{ columnId: namedGroupMemberColumnId, policy }],
    },
  };
  return { ...workspace, documents: [{ ...baseDocument, rows, columns: [namedGroupMemberColumn] }] };
};

const observationRouteChoice: ConstructionChoice = {
  choiceId: 'server-observation-route-choice',
  route: [{
    edgeId: 'patient-observations',
    fromNodeId: 'patient-node',
    toNodeId: 'observation-node',
    fromResourceType: 'Patient',
    toResourceType: 'Observation',
    relationship: 'observations',
    storageDirection: 'OUTBOUND',
    matchMode: 'OPTIONAL',
  }],
  presentation: {
    summary: 'Observation.status through observations',
    facts: [{ label: 'Relationship', value: 'observations' }],
  },
  source: {
    kind: 'FIELD',
    candidateId: 'observation-status',
    nodeId: 'observation-node',
    resourceType: 'Observation',
    path: 'status',
    cardinality: 'optional_one',
  },
  options: [choiceOption],
};

const relatedSourceChoice: ConstructionChoice = {
  ...observationRouteChoice,
  choiceId: 'server-observation-related-choice',
  presentation: {
    summary: 'Observation.status through observations',
    facts: [{ label: 'Relationship', value: 'observations' }],
  },
  options: [{
    form: 'ALL',
    shape: 'LIST',
    decision: 'REQUIRES_DECISION',
    preservation: 'PRESERVING',
    rowEffect: 'PRESERVES_ROW_GRAIN',
    support: 'SUPPORTED',
    reason: 'Loom proves this list form preserves the selected stage row grain.',
    contributorPredicateOperators: ['EXISTS', 'EQUALS'],
  }, {
    form: 'COUNT',
    shape: 'SCALAR',
    decision: 'REQUIRES_DECISION',
    preservation: 'REDUCING',
    rowEffect: 'PRESERVES_ROW_GRAIN',
    support: 'SUPPORTED',
    reason: 'Count distinct matching source records.',
    contributorPredicateOperators: ['EXISTS', 'EQUALS'],
  }, {
    form: 'PRESENCE',
    shape: 'SCALAR',
    decision: 'REQUIRES_DECISION',
    preservation: 'REDUCING',
    rowEffect: 'PRESERVES_ROW_GRAIN',
    support: 'SUPPORTED',
    reason: 'Show whether a matching source record exists.',
    contributorPredicateOperators: ['EXISTS', 'EQUALS'],
  }],
};

const mutationResult = () => [
  vi.fn().mockReturnValue({ unwrap: vi.fn().mockResolvedValue({}) }),
  { isLoading: false },
];

const configureRelatedSourceCountFixture = () => {
  const construction: Construction = {
    version: 1,
    steps: [{
      id: 'keep-vitals',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'FILTER',
        filter: { columnId: 'patient-row-key', operator: 'EXISTS' },
      },
      outputs: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
    }],
  };
  const sourceStage: ConstructionStageDescriptor = {
    id: 'source_projection',
    inputStageId: '',
    rowIdentityColumn: 'source-row-key',
    columns: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
    capabilities: [],
  };
  const selectedStage: ConstructionStageDescriptor = {
    id: 'keep-vitals',
    inputStageId: 'source_projection',
    operation: 'FILTER',
    rowIdentityColumn: '_key',
    columns: [{
      id: 'patient-row-key',
      name: 'patient_row_key',
      label: 'Patient row key',
      cardinality: 'required_one',
    }],
    capabilities: [{ kind: 'RELATED_SOURCE', supported: true }, { kind: 'ROW_VALUES', supported: true }],
  };
  const relatedWorkspace: ExplorerBuilderWorkspace = {
    ...workspace,
    documents: [{ ...workspace.documents[0]!, construction }],
  };
  (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
    data: { ...builderState, workspace: relatedWorkspace },
    isLoading: false,
    refetch: vi.fn(),
  });
  mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
    readonly snapshotToken: string;
    readonly expectedDraftVersion: number;
    readonly expectedDraftDigest: string;
    readonly outputId: string;
    readonly stageId: string;
  }) => ({
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    outputId: args.outputId,
    stageId: args.stageId,
    baseConstruction: construction,
    stages: [sourceStage, selectedStage],
    selectedStage,
  }));
  const alternateRelatedSourceChoice: ConstructionChoice = {
    ...relatedSourceChoice,
    choiceId: 'server-observation-related-choice-via-report',
    route: [
      {
        edgeId: 'patient-reports',
        fromNodeId: 'patient-node',
        toNodeId: 'report-node',
        fromResourceType: 'Patient',
        toResourceType: 'DiagnosticReport',
        relationship: 'reports',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      },
      {
        edgeId: 'report-observations',
        fromNodeId: 'report-node',
        toNodeId: 'observation-node',
        fromResourceType: 'DiagnosticReport',
        toResourceType: 'Observation',
        relationship: 'observations',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      },
    ],
  };
  mockLoomClient.searchConstructionChoices.mockResolvedValue({
    snapshotToken: 'snapshot-1',
    outputId: 'patients',
    complete: true,
    truncated: false,
    choices: [relatedSourceChoice, alternateRelatedSourceChoice],
  });
  mockLoomClient.proposeConstruction.mockImplementation(async (args: {
    readonly candidateConstruction: Construction;
    readonly requestId: string;
  }) => {
    const lastStep = args.candidateConstruction.steps.at(-1);
    const output = lastStep?.outputs.at(-1);
    const isCount = lastStep?.operation.kind === 'RELATED_SOURCE' &&
      lastStep.operation.relatedSource.form === 'COUNT';
    const outputColumn = output?.name ?? 'related_Observation_status';
    const proposalId = args.requestId.startsWith('construction-route-coverage-')
      ? 'related-source-coverage-proposal'
      : 'related-source-proposal';
    return {
      proposalId,
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-workspace-1',
      changedStepId: lastStep?.id ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage, selectedStage],
      previewStatus: 'READY' as const,
      previewDurationMs: 7,
      preview: {
        apiVersion,
        kind: 'ExplorerBuilderPreview' as const,
        rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
        receiptId: proposalId,
        outputId: 'patients',
        columns: [{
          column: outputColumn,
          label: output?.label ?? 'Observation status',
          logicalType: isCount ? 'integer' : 'string',
          filterable: true,
          chartable: false,
        }],
        rows: [{ [outputColumn]: isCount ? 1 : ['registered'] }],
        rowCount: 1,
        diagnostics: [],
      },
    };
  });
  mockLoomClient.preview.mockResolvedValue({
    apiVersion,
    kind: 'ExplorerBuilderPreview',
    rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
    receiptId: 'related-source-proposal',
    outputId: 'patients',
    columns: [{ column: 'patient_row_key', label: 'Patient row key', logicalType: 'string', filterable: true, chartable: false }],
    rows: [{ patient_row_key: 'patient-1' }],
    rowCount: 1,
    diagnostics: [],
  });
  const savedPreview: ExplorerBuilderPreviewResult = {
    apiVersion,
    kind: 'ExplorerBuilderPreview',
    rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
    receiptId: 'saved-patient-preview',
    outputId: 'patients',
    columns: [{ column: 'patient_row_key', label: 'Patient row key', logicalType: 'string', filterable: true, chartable: false }],
    rows: [{ patient_row_key: 'patient-1' }],
    rowCount: 1,
    diagnostics: [],
  };
  const savedReceipt: ExplorerBuilderCompileResult = {
    apiVersion,
    kind: 'ExplorerBuilderReceipt',
    receiptId: savedPreview.receiptId,
    snapshotToken: 'snapshot-1',
    builder: relatedWorkspace,
    outputs: [{ outputId: 'patients', columns: savedPreview.columns }],
    diagnostics: [],
  };
  (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
    vi.fn().mockReturnValue({ unwrap: vi.fn().mockResolvedValue(savedReceipt) }),
    { isLoading: false },
  ]);
  (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
    vi.fn().mockReturnValue({ unwrap: vi.fn().mockResolvedValue(savedPreview) }),
    { isLoading: false },
  ]);
  return { construction, relatedWorkspace, selectedStage };
};

const initialPatientWorkspace = (
  title: string,
  columns: ExplorerBuilderWorkspace['documents'][number]['columns'] = [],
): ExplorerBuilderWorkspace => ({
  ...workspace,
  documents: [{
    ...workspace.documents[0]!,
    output: { id: 'patients', title },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    columns,
  }],
  tabs: [{ id: 'patients-tab', title, outputId: 'patients', order: 0, visible: true }],
});

const configureInitialTableFlow = (
  applyExplorerCommands: Mock,
  candidates: ReadonlyArray<ExplorerBuilderCandidate>,
) => {
  (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
    data: {
      ...builderState,
      lifecycleState: 'NEW',
      draftVersion: 0,
      draftDigest: '',
      workspace: null,
      catalog: { ...catalog, candidates: [] },
    },
    isLoading: false,
    refetch: vi.fn(),
  });

  applyExplorerCommands.mockImplementation((request: {
    readonly commands: ReadonlyArray<ExplorerBuilderCommand>;
  }) => ({
    unwrap: vi.fn().mockImplementation(async () => {
      const command = request.commands[0];
      if (command?.type === 'CREATE_TABLE') {
        return {
          commandId: 'create-patient-table',
          workspace: initialPatientWorkspace(command.title ?? 'Patient'),
          draftVersion: 1,
          draftDigest: 'sha256:draft-1',
          results: [{ type: 'TABLE_CREATED', outputId: 'patients', occurrenceId: 'base' }],
          diagnostics: [],
        };
      }
      return {
        commandId: 'add-patient-id',
        workspace: initialPatientWorkspace('CDA Patients', [{
          column: 'id',
          label: 'Patient id',
          logicalType: 'string',
          occurrenceId: 'base',
          source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
          table: { visible: true },
        }]),
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [{ type: 'COLUMN_ADDED', outputId: 'patients', occurrenceId: 'base', column: 'id' }],
        diagnostics: [],
      };
    }),
  }));

  const getSuggestions = vi.fn().mockReturnValue({
    unwrap: vi.fn().mockResolvedValue({
      apiVersion,
      kind: 'ExplorerBuilderCandidateSuggestions',
      snapshotToken: 'snapshot-1',
      nodeId: 'patient-node',
      candidates,
      diagnostics: [],
    }),
  });
  (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue([
    getSuggestions,
    { isLoading: false },
  ]);

  const preview: ExplorerBuilderPreviewResult = {
    apiVersion,
    kind: 'ExplorerBuilderPreview',
    rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
    receiptId: 'receipt-1',
    outputId: 'patients',
    columns: [{
      column: 'id',
      label: 'Patient id',
      logicalType: 'string',
      filterable: true,
      chartable: false,
    }],
    rows: [{ id: 'patient-1' }],
    rowCount: 1,
    diagnostics: [],
  };
  const compile: ExplorerBuilderCompileResult = {
    apiVersion,
    kind: 'ExplorerBuilderReceipt',
    receiptId: 'receipt-1',
    snapshotToken: 'snapshot-1',
    builder: initialPatientWorkspace('CDA Patients', [{
      column: 'id',
      label: 'Patient id',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true },
    }]),
    outputs: [{
      outputId: 'patients',
      columns: preview.columns,
    }],
    diagnostics: [],
  };
  const reconcile = vi.fn().mockReturnValue({
    unwrap: vi.fn().mockResolvedValue(compile),
  });
  (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
    reconcile,
    { isLoading: false },
  ]);
  const previewBuilder = vi.fn().mockReturnValue({
    unwrap: vi.fn().mockResolvedValue(preview),
  });
  (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
    previewBuilder,
    { isLoading: false },
  ]);

  return { getSuggestions, reconcile, previewBuilder };
};

describe('BuilderWorkspace Add columns source selection', () => {
  let applyExplorerCommands: Mock;

  beforeEach(() => {
    mockLoomClient.browseFrameSourceOptions.mockReset().mockImplementation(async (args: { outputId: string; snapshotToken: string }) => ({
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      sources: [],
    }));
    mockLoomClient.browseSemanticInventory.mockReset().mockResolvedValue({
      contextToken: 'semantic-context',
      buildId: 'semantic-build',
      state: 'complete',
      sourceAvailability: 'verified',
      entries: [],
    });
    mockLoomClient.getConstructionCapabilities.mockReset().mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => {
      const selectedStage = {
        id: args.stageId,
        inputStageId: '',
        rowIdentityColumn: 'source-row-id',
        columns: [],
        capabilities: [],
      };
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: { version: 1, steps: [] },
        stages: [{ ...selectedStage, id: 'source_projection' }],
        selectedStage,
      };
    });
    mockLoomClient.searchRelatedFieldChoices.mockReset().mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      complete: true,
      truncated: false,
      choices: [],
    }));
    mockLoomClient.getSelection.mockReset();
    mockLoomClient.listRowDefinitionChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'patients',
      choices: [],
      explicitGroups: [{
        revisionId: 'named-cohort-revision',
        sourceSelectionRevisionId: 'named-cohort-source-selection',
        groupCount: 1,
        memberCount: 2,
        createdAt: '2026-09-21T00:00:00Z',
        unassignedMemberPolicies: ['ERROR'],
      }],
    });
    mockLoomClient.preview.mockReset();
    mockLoomClient.proposeConstruction.mockReset();
    mockLoomClient.proposeConstructionChoices.mockReset();
    mockLoomClient.configuredColumnContextsQuery.mockReset().mockReturnValue({
      data: undefined,
      error: undefined,
      isLoading: true,
      isFetching: true,
      refetch: vi.fn(),
    });
    mockLoomClient.searchConstructionChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'patients',
      complete: true,
      truncated: false,
      choices: [observationRouteChoice],
    });

    applyExplorerCommands = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({
        commandId: 'command-1',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [{ type: 'TABLE_CHANGED', outputId: 'patients' }],
        diagnostics: [],
      }),
    });
    (useGetExplorerAuthoringExplorersQuery as Mock).mockReturnValue({
      data: [{ explorerId: 'test', title: 'Test Explorer' }],
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: builderState,
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerAuthoringCapabilityV2Query as Mock).mockReturnValue({
      data: { features: { deleteExplorer: false } },
    });
    (useApplyExplorerBuilderCommandsV2Mutation as Mock).mockReturnValue([
      applyExplorerCommands,
      { isLoading: false },
    ]);
    (useAssessExplorerRowChangeMutation as Mock).mockReturnValue(mutationResult());
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue(mutationResult());
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue(mutationResult());
    (usePopulationMappingMutation as Mock).mockReturnValue(mutationResult());
    (usePublishExplorerAuthoringV2Mutation as Mock).mockReturnValue(mutationResult());
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue(mutationResult());
    (useDeleteExplorerAuthoringMutation as Mock).mockReturnValue(mutationResult());
    (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue(mutationResult());
  });

  it('renders the blank first-table home without requesting construction capabilities', async () => {
    const emptyWorkspace: ExplorerBuilderWorkspace = { ...workspace, documents: [], tabs: [] };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: emptyWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getConstructionCapabilities.mockClear();

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    expect(await screen.findByRole('heading', { name: 'Build your first table' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Choose Patient rows' })).toBeInTheDocument();
    expect(mockLoomClient.getConstructionCapabilities).not.toHaveBeenCalled();
  });

  it('keeps Add columns catalog and adds scalar contributor choices for a terminal Group', async () => {
    const savedFieldSource: NonNullable<Construction['steps'][number]['rowValues']>[number] = {
      fieldSource: {
        kind: 'BINDING',
        valueKind: 'SCALAR',
        sourceStageId: 'source_projection',
        sourceShapeId: 'group-input-shape',
        sourceRole: 'OWNER',
        occurrenceId: 'patient-owner',
        resourceType: 'Patient',
        ownerOccurrenceId: 'patient-owner',
        ownerResourceType: 'Patient',
        candidateId: 'saved-patient-status-candidate',
        canonicalPath: 'active',
      },
      outputColumnId: 'saved-active-value',
      policy: 'ONE',
    };
    const groupConstruction: Construction = {
      version: 1,
      steps: [{
        id: 'group-rows',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'GROUP', group: {
          constructionId: 'group-rows',
          missingKeyPolicy: 'GROUP',
          keys: [{ inputColumnId: 'patient-id', outputColumnId: 'patient-id-key' }],
          aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'row-count' }],
        } },
        rowValues: [savedFieldSource],
        outputs: [
          { id: 'patient-id-key', name: 'patient_id', label: 'Patient ID', type: 'string' },
          { id: 'row-count', name: 'row_count', label: 'Row count', type: 'integer' },
          { id: 'saved-active-value', name: 'active', label: 'Active', type: 'boolean' },
        ],
      }],
    };
    const groupedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]!, construction: groupConstruction }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: groupedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    const sourceStage: ConstructionStageDescriptor = {
      id: 'source_projection',
      inputStageId: '',
      operation: 'SOURCE_PROJECTION',
      rowIdentityColumn: '__loom_expansion_identity',
      columns: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' }],
      capabilities: [{ kind: 'GROUP', supported: true }, { kind: 'ROW_VALUES', supported: true }],
    };
    const groupStage: ConstructionStageDescriptor = {
      id: 'group-rows',
      inputStageId: 'source_projection',
      operation: 'GROUP',
      rowIdentityColumn: 'group-row-id',
      columns: [
        { id: 'patient-id-key', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' },
        { id: 'row-count', name: 'row_count', label: 'Row count', type: 'integer', cardinality: 'required_one' },
        { id: 'saved-active-value', name: 'active', label: 'Active', type: 'boolean', cardinality: 'optional_one' },
      ],
      capabilities: [{ kind: 'ROW_VALUES', supported: true }],
    };
    const groupRowValueChoice = {
      kind: 'FIELD_SOURCE',
      valueKind: 'SCALAR',
      fieldSource: { kind: 'CHOICE', choiceId: 'group-component-value-choice' },
      label: 'Component value',
      sourceStageId: 'source_projection',
      sourceShapeId: 'group-input-shape',
      candidateId: 'component-value-candidate',
      sourceRole: 'ANCESTOR',
      occurrenceId: 'patient-observation-owner',
      resourceType: 'ObservationComponent',
      ownerOccurrenceId: 'patient-observation-owner',
      ownerResourceType: 'Observation',
      canonicalPath: 'valueQuantity.value',
      scopeLabel: 'Observation component · component[].valueQuantity.value',
      valueType: 'decimal',
      boundarySelector: 'component[]',
    } satisfies ConstructionCapabilitiesResponse['groupRowValueChoices'][number];
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
      readonly groupStepId?: string;
    }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      baseConstruction: groupConstruction,
      stages: [sourceStage, groupStage],
      selectedStage: args.stageId === sourceStage.id ? sourceStage : groupStage,
      groupFieldChoices: [],
      groupRowValueChoices: args.groupStepId === 'group-rows' ? [groupRowValueChoice] : [],
      workspaceInputs: [],
    } satisfies ConstructionCapabilitiesResponse));
    mockLoomClient.proposeConstruction.mockImplementation(async (args: {
      readonly candidateConstruction: Construction;
      readonly changedStepId?: string;
      readonly requestId: string;
    }): Promise<ConstructionProposalResponse> => {
      const preview: ExplorerBuilderPreviewResult = {
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
        receiptId: 'group-rowvalue-proposal',
        outputId: 'patients',
        columns: [],
        rows: [{}],
        rowCount: 1,
        diagnostics: [],
      };
      return {
        proposalId: 'group-rowvalue-proposal',
        outputId: 'patients',
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'sha256:draft-1',
        baseDocumentDigest: 'document-1',
        candidateWorkspaceDigest: 'sha256:group-rowvalue-candidate',
        changedStepId: args.changedStepId ?? '',
        candidateConstruction: args.candidateConstruction,
        dependencyImpact: { affectedStepIds: [] },
        stages: [sourceStage, groupStage],
        previewStatus: 'READY',
        previewDurationMs: 1,
        preview,
      };
    });
    const receipt: ExplorerBuilderCompileResult = {
      apiVersion,
      kind: 'ExplorerBuilderReceipt',
      receiptId: 'grouped-table-receipt',
      snapshotToken: 'snapshot-1',
      builder: groupedWorkspace,
      outputs: [{ outputId: 'patients', columns: [] }],
      diagnostics: [],
    };
    const reconcile = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue(receipt),
    });
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);

    const { unmount } = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    const contributorChoices = await screen.findByRole('region', { name: 'Add columns from Group contributors' });
    expect(screen.getByRole('tab', { name: 'Coded values' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Other fields' })).toBeInTheDocument();
    expect(screen.queryByTestId('construction-reshape-add-summary')).not.toBeInTheDocument();
    expect(await screen.findByText('No concepts match this search.')).toBeInTheDocument();
    expect(mockLoomClient.getConstructionCapabilities).toHaveBeenLastCalledWith(
      expect.objectContaining({ stageId: 'source_projection', groupStepId: 'group-rows' }),
      expect.any(AbortSignal),
    );

    fireEvent.click(within(contributorChoices).getByTestId('construction-group-add-column-choice-group-component-value-choice'));
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalled());
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls.at(-1)?.[0];
    const proposedGroup = proposalArgs?.candidateConstruction.steps.find((step) => step.id === 'group-rows');
    if (!proposedGroup || proposedGroup.operation.kind !== 'GROUP') {
      throw new Error('The Add columns choice did not propose an update to the active Group step.');
    }
    expect(proposedGroup.operation.group.keys).toEqual([
      { inputColumnId: 'patient-id', outputColumnId: 'patient-id-key' },
    ]);
    expect(proposedGroup.rowValues).toEqual([
      savedFieldSource,
      expect.objectContaining({
        fieldSource: { kind: 'CHOICE', choiceId: 'group-component-value-choice' },
        policy: 'ALL',
      }),
    ]);
    unmount();
    await act(async () => {
      await new Promise<void>((resolve) => window.setTimeout(resolve, 0));
    });
  });

  it('shows separate Coded values and Other fields tabs with shared source scopes', async () => {
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: {
        ...builderState,
        catalog: {
          ...catalog,
          candidates: [fieldCandidate('patient-birthdate', 'patient-node', 'Patient', 'birthDate')],
        },
      },
      isLoading: false,
      refetch: vi.fn(),
    });
    const { unmount } = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    expect(screen.queryByRole('heading', { name: 'Add coded values to this table' })).not.toBeInTheDocument();
    const tabs = screen.getByRole('tablist', { name: 'Column types' });
    const codedTab = within(tabs).getByRole('tab', { name: 'Coded values' });
    const fieldsTab = within(tabs).getByRole('tab', { name: 'Other fields' });
    expect(codedTab).toHaveAttribute('aria-selected', 'true');
    expect(fieldsTab).toHaveAttribute('aria-selected', 'false');
    expect(screen.queryByText('Selected fields and coded values stay in the add list as you switch tabs.')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Coded values for this table' })).not.toBeInTheDocument();
    expect(screen.queryByText('How coded columns work')).not.toBeInTheDocument();
    expect(screen.queryByText('Availability and coverage notes')).not.toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Search coded values by code or label' })).toBeInTheDocument();
    expect(screen.queryByTestId('feature-catalog-raw-fields')).not.toBeInTheDocument();
    const source = screen.getByRole('group', { name: 'Add columns source' });
    const sourceScopes = within(source).getByRole('group', { name: 'Search scope' });
    const patientScope = within(sourceScopes).getByRole('button', { name: 'Patient, Current table rows' });
    expect(patientScope).toHaveAttribute('aria-pressed', 'true');
    expect(within(sourceScopes).queryByRole('button', { name: /All accessible resources/ })).not.toBeInTheDocument();
    expect(screen.getByRole('searchbox', { name: 'Search coded values by code or label' })
      .compareDocumentPosition(source) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(source).queryByRole('group', { name: 'Related resources' })).not.toBeInTheDocument();
    expect(within(source).queryByRole('searchbox', { name: 'Search related resources' })).not.toBeInTheDocument();
    await waitFor(() => expect(mockLoomClient.browseSemanticInventory.mock.calls.some(([args]) => args.outputId === 'patients')).toBe(true));
    const request = mockLoomClient.browseSemanticInventory.mock.calls.find(([args]) => args.outputId === 'patients')?.[0];
    if (!request) throw new Error('The coded browser did not request the current table inventory.');
    expect(request).toMatchObject({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      snapshotToken: 'snapshot-1',
      outputId: 'patients',
      rowRoot: 'Patient',
      selectionMode: 'SYSTEM_VERSION_CATEGORY',
      limit: 50,
    });
    expect(request.resourceType).toBeUndefined();
    expect(request.sourceNodeId).toBeUndefined();
    expect(request.frameId).toBeUndefined();
    expect(mockLoomClient.browseFrameSourceOptions).not.toHaveBeenCalled();
    expect(screen.queryByRole('heading', { name: 'Coded sources' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Browse sources' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Ready-to-add paired concepts' })).not.toBeInTheDocument();

    const codedSearch = screen.getByRole('searchbox', { name: 'Search coded values by code or label' });
    fireEvent.change(codedSearch, { target: { value: 'diagnostic' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() => expect(mockLoomClient.browseSemanticInventory.mock.calls.some(
      ([args]) => args.outputId === 'patients' && args.query === 'diagnostic',
    )).toBe(true));
    const globalSearchRequest = mockLoomClient.browseSemanticInventory.mock.calls.find(
      ([args]) => args.outputId === 'patients' && args.query === 'diagnostic',
    )?.[0];
    expect(globalSearchRequest?.query).toBe('diagnostic');
    expect(globalSearchRequest?.resourceType).toBeUndefined();

    const semanticRequestCount = mockLoomClient.browseSemanticInventory.mock.calls.length;
    fireEvent.click(fieldsTab);
    expect(fieldsTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('searchbox', { name: 'Search dataset fields by name or type' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Add other fields to this table' })).not.toBeInTheDocument();
    expect(screen.queryByText('Browse available dataset fields such as dates, text, numbers, booleans, or identifiers.')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Available dataset fields' })).not.toBeInTheDocument();
    expect(screen.getByText(/^\d+ available$/)).toBeInTheDocument();
    const fieldsSource = screen.getByRole('group', { name: 'Add columns source' });
    expect(screen.getByRole('searchbox', { name: 'Search dataset fields by name or type' })
      .compareDocumentPosition(fieldsSource) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByTestId('feature-catalog-raw-fields').tagName).not.toBe('DETAILS');
    expect(await screen.findByRole('button', { name: 'Select Patient.birthDate' })).toBeEnabled();
    expect(screen.queryByRole('region', { name: 'Coded values' })).not.toBeInTheDocument();
    expect(mockLoomClient.browseSemanticInventory).toHaveBeenCalledTimes(semanticRequestCount);
    expect(within(screen.getByRole('group', { name: 'Search scope' })).getByRole('button', { name: 'Patient, Current table rows' }))
      .toHaveAttribute('aria-pressed', 'true');
    unmount();
    await act(async () => {
      await new Promise<void>((resolve) => globalThis.setTimeout(resolve, 0));
    });
  });

  it('shows populated root and exact active related fields without exposing unsupported related scopes', async () => {
    const patientBirthDate = fieldCandidate('patient-birth-date', 'patient-node', 'Patient', 'birthDate');
    const medicationAdministrationNode = {
      nodeId: 'medicationadministration-node',
      resourceType: 'MedicationAdministration',
      rowRootEligible: false,
      populated: true,
      documentCount: 2,
    };
    const relatedExpandConstruction: Construction = {
      version: 1,
      steps: [{
        id: 'related-expand-step',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        outputs: [],
        operation: {
          kind: 'RELATED_EXPAND',
          relatedExpand: {
            anchorColumnId: 'patient-id',
            choiceId: 'patient-medication-administration-route',
            targetNodeId: 'medicationadministration-node',
            targetResourceType: 'MedicationAdministration',
            route: [],
            contributorRule: { policy: 'ALL_MATCHES' },
            emptyPolicy: 'PRESERVE_PARENT',
            relatedRecordColumnId: 'medicationadministration-id',
          },
        },
      }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: {
        ...builderState,
        workspace: {
          ...workspace,
          documents: workspace.documents.map((document) => ({
            ...document,
            construction: relatedExpandConstruction,
          })),
        },
        catalog: {
          ...catalog,
          nodes: [...catalog.nodes, medicationAdministrationNode],
          edges: [...catalog.edges, {
            edgeId: 'patient-medicationadministration',
            fromNodeId: 'patient-node',
            toNodeId: medicationAdministrationNode.nodeId,
            label: 'medication administrations',
          }],
          candidates: [patientBirthDate],
        },
      },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => {
      const selectedStage: ConstructionStageDescriptor = {
        id: args.stageId,
        inputStageId: '',
        rowIdentityColumn: 'medicationadministration-row-key',
        columns: [],
        capabilities: [
          { kind: 'RELATED_SOURCE', supported: false, reasonCode: 'NO_SOURCE_ROW_ANCHOR', reason: 'No retained root source anchor.' },
          { kind: 'RELATED_EXPAND', supported: false, reasonCode: 'NO_RELATED_ROUTE', reason: 'No additional related expansion route is available.' },
          { kind: 'RELATED_FIELD', supported: true },
        ],
        activeRelatedRecord: {
          targetNodeId: 'medicationadministration-node',
          targetResourceType: 'MedicationAdministration',
          terminalIdentityColumn: 'medicationadministration-id',
        },
      };
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: relatedExpandConstruction,
        stages: [
          { ...selectedStage, id: 'source_projection', rowIdentityColumn: 'source-row-id', activeRelatedRecord: undefined },
          selectedStage,
        ],
        selectedStage,
        sourceProjectionAvailability: { stageId: args.stageId, supported: true },
      };
    });
    const relatedFieldChoices: RelatedFieldChoiceSearchResponse['choices'] = [
      {
        choiceId: 'medication-administration-medication',
        label: 'Medication',
        source: {
          kind: 'FIELD', candidateId: 'medication-administration-medication',
          nodeId: 'medicationadministration-node', resourceType: 'MedicationAdministration',
          path: 'medication', cardinality: 'optional_one', logicalType: 'CodeableReference',
        },
      },
      {
        choiceId: 'medication-administration-effective',
        label: 'Effective date',
        source: {
          kind: 'FIELD', candidateId: 'medication-administration-effective',
          nodeId: 'medicationadministration-node', resourceType: 'MedicationAdministration',
          path: 'effective', cardinality: 'optional_one', logicalType: 'dateTime',
        },
      },
    ];
    mockLoomClient.searchRelatedFieldChoices.mockReset().mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      complete: true,
      truncated: false,
      choices: relatedFieldChoices,
    }));

    const { unmount } = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    const source = await screen.findByRole('group', { name: 'Add columns source' });
    const searchScope = within(source).getByRole('group', { name: 'Search scope' });
    const codedTab = screen.getByRole('tab', { name: 'Coded values' });
    expect(codedTab).toHaveAttribute('aria-selected', 'true');
    const codedRelatedScope = within(searchScope).getByRole('button', {
      name: 'MedicationAdministration, Related resource',
    });
    expect(codedRelatedScope).toHaveAttribute('data-source-kind', 'RELATED');
    expect(within(searchScope).queryByRole('button', { name: /this row’s related record/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    expect(within(searchScope).getByRole('button', { name: 'Patient, Current table rows' })).toHaveAttribute('aria-pressed', 'true');
    const exactRelated = within(searchScope).getByRole('button', {
      name: 'MedicationAdministration, This row’s related record',
    });
    expect(exactRelated.textContent).toBe('MedicationAdministration');
    expect(exactRelated).toHaveAttribute('data-source-kind', 'EXACT_RELATED');
    expect(within(searchScope).queryByRole('button', { name: 'MedicationAdministration, Related resource' })).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Select Patient.birthDate' })).toBeEnabled();

    fireEvent.click(exactRelated);
    expect(screen.getByRole('tab', { name: 'Other fields' })).toHaveAttribute('aria-selected', 'true');
    expect(await screen.findByRole('heading', { name: 'From this row’s MedicationAdministration' })).toBeInTheDocument();
    await waitFor(() => expect(mockLoomClient.searchRelatedFieldChoices).toHaveBeenCalledTimes(1));
    const relatedFieldsList = screen.getByRole('group', { name: 'Fields from this related record' });
    expect(within(relatedFieldsList).getByRole('button', { name: /Medication/ })).toBeInTheDocument();
    expect(within(relatedFieldsList).getByRole('button', { name: /Effective date/ })).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('construction-close-operation-editor'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    const reopenedCodedTab = screen.getByRole('tab', { name: 'Coded values' });
    expect(reopenedCodedTab).toHaveAttribute('aria-selected', 'true');
    const reopenedSource = screen.getByRole('group', { name: 'Search scope' });
    expect(within(reopenedSource).getByRole('button', { name: 'Patient, Current table rows' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(reopenedSource).queryByRole('button', { name: /this row’s related record/ })).not.toBeInTheDocument();
    expect(within(reopenedSource).getByRole('button', { name: 'MedicationAdministration, Related resource' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    const reopenedExactRelated = within(screen.getByRole('group', { name: 'Search scope' })).getByRole('button', {
      name: 'MedicationAdministration, This row’s related record',
    });
    fireEvent.click(reopenedExactRelated);
    expect(screen.getByRole('tab', { name: 'Other fields' })).toHaveAttribute('aria-selected', 'true');
    await waitFor(() => expect(mockLoomClient.searchRelatedFieldChoices).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole('tab', { name: 'Coded values' }));
    expect(screen.getByRole('tab', { name: 'Coded values' })).toHaveAttribute('aria-selected', 'true');
    expect(within(screen.getByRole('group', { name: 'Search scope' })).queryByRole('button', { name: /this row’s related record/ })).not.toBeInTheDocument();
    expect(within(screen.getByRole('group', { name: 'Search scope' })).getByRole('button', {
      name: 'Patient, Current table rows',
    })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.queryByRole('heading', { name: 'Coded values for this table' })).not.toBeInTheDocument();
    await act(async () => {
      await new Promise<void>((resolve) => globalThis.setTimeout(resolve, 0));
    });
    unmount();
    await act(async () => {
      await new Promise<void>((resolve) => globalThis.setTimeout(resolve, 0));
    });
  });

  it('saves the primary Columns-menu policy edit as one versioned command using the physical field name', async () => {
    const initialWorkspace = namedGroupWorkspace('ALL');
    const savedWorkspace = namedGroupWorkspace('ONE');
    const savedDocument = savedWorkspace.documents[0];
    expect(savedDocument?.columns.map(column => column.columnId)).toEqual([namedGroupMemberColumnId]);
    if (!savedDocument || savedDocument.rows.kind !== 'GROUPS' || savedDocument.rows.groups.source.kind !== 'EXPLICIT') {
      throw new Error('Saved test workspace must retain explicit named groups.');
    }
    expect(savedDocument.rows.groups.source.explicit?.revisionId).toBe('named-cohort-revision');
    expect(savedDocument.rows.groups.rowValues).toEqual([{ columnId: namedGroupMemberColumnId, policy: 'ONE' }]);
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, draftVersion: 7, draftDigest: 'sha256:named-group-draft', workspace: initialWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    const initialReceipt: ExplorerBuilderCompileResult = {
      apiVersion,
      kind: 'ExplorerBuilderReceipt',
      receiptId: 'named-group-receipt',
      snapshotToken: 'snapshot-1',
      builder: initialWorkspace,
      outputs: [{
        outputId: 'patients',
        columns: [{
          column: 'member_status',
          label: 'Member status',
          logicalType: 'string',
          filterable: true,
          chartable: false,
        }],
      }],
      diagnostics: [],
    };
    const savedReceipt: ExplorerBuilderCompileResult = {
      ...initialReceipt,
      receiptId: 'named-group-receipt-one',
      builder: savedWorkspace,
    };
    const previewForReceipt = (receiptId: string): ExplorerBuilderPreviewResult => ({
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId,
      outputId: 'patients',
      columns: initialReceipt.outputs[0]!.columns,
      rows: [{ member_status: 'active' }],
      rowCount: 1,
      diagnostics: [],
    });
    const reconcile = vi.fn()
      .mockReturnValueOnce({ unwrap: vi.fn().mockResolvedValue(initialReceipt) })
      .mockReturnValue({ unwrap: vi.fn().mockResolvedValue(savedReceipt) });
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);
    const previewBuilder = vi.fn()
      .mockReturnValueOnce({ unwrap: vi.fn().mockResolvedValue(previewForReceipt(initialReceipt.receiptId)) })
      .mockReturnValue({ unwrap: vi.fn().mockResolvedValue(previewForReceipt(savedReceipt.receiptId)) });
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      previewBuilder,
      { isLoading: false },
    ]);
    applyExplorerCommands.mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({
        commandId: 'update-member-row-policy',
        workspace: savedWorkspace,
        draftVersion: 8,
        draftDigest: 'sha256:named-group-draft-one',
        results: [{ type: 'TABLE_CHANGED', outputId: 'patients' }],
        diagnostics: [],
      }),
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Columns' }));
    const policy = await screen.findByRole('combobox', {
      name: 'Values per cohort member for Member status (member_status)',
    });
    expect(policy).toHaveProperty('value', 'ALL');
    fireEvent.change(policy, { target: { value: 'ONE' } });

    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledOnce());
    expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 7,
      expectedDraftDigest: 'sha256:named-group-draft',
      commands: [{
        type: 'UPDATE_COLUMN_ROW_VALUE_POLICY',
        outputId: 'patients',
        column: namedGroupMemberColumn.column,
        rowValuePolicy: 'ONE',
      }],
    }));
    await waitFor(() => expect(screen.getByRole('combobox', {
      name: 'Values per cohort member for Member status (member_status)',
    })).toHaveProperty('value', 'ONE'));
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    expect(reconcile.mock.calls[0]?.[0]).toMatchObject({
      draftVersion: 7,
      draftDigest: 'sha256:named-group-draft',
    });
    expect(reconcile.mock.calls[1]?.[0]).toMatchObject({
      draftVersion: 8,
      draftDigest: 'sha256:named-group-draft-one',
    });
    await waitFor(() => expect(previewBuilder).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      const previewPanel = screen.getByTestId('construction-preview');
      expect(previewPanel).toHaveAttribute('data-preview-status', 'ready');
      expect(previewPanel).toHaveAttribute('data-preview-receipt-id', savedReceipt.receiptId);
      expect(previewPanel).toHaveAttribute('data-preview-output-id', 'patients');
    });
  });

  it('omits fields for an unsupported related source without offering a dead-end add action', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select saved DiagnosticReport occurrence' }));
    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));

    const source = await screen.findByRole('group', { name: 'Add columns source' });
    const sourceOptions = within(source).getByRole('group', { name: 'Search scope' });
    expect(within(sourceOptions).getByRole('button', { name: 'Patient, Current table rows' })).toBeInTheDocument();
    const reportSource = within(sourceOptions).getByRole('button', {
      name: 'DiagnosticReport, Related resource, selected occurrence',
    });
    expect(reportSource).toHaveAttribute('aria-pressed', 'false');
    expect(within(sourceOptions).getByRole('button', { name: 'Patient, Current table rows' })).toHaveAttribute('aria-pressed', 'true');
    const observationSource = within(sourceOptions).getByRole('button', {
      name: 'Observation, Related resource',
    });
    fireEvent.click(observationSource);
    expect(observationSource).toHaveAttribute('aria-pressed', 'true');
    expect(reportSource).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));

    expect(screen.queryByRole('button', { name: 'Select Observation.status' })).not.toBeInTheDocument();
    expect(within(screen.getByTestId('construction-operation-editor')).queryByRole(
      'button',
      { name: 'Select DiagnosticReport.status' },
    )).not.toBeInTheDocument();
    expect(mockLoomClient.searchConstructionChoices).not.toHaveBeenCalled();
    expect(mockLoomClient.proposeConstruction).not.toHaveBeenCalled();
    expect(applyExplorerCommands).not.toHaveBeenCalled();
  });

  it('shows all metadata-driven related sources in the visible source row', async () => {
    const syntheticNodes = Array.from({ length: 8 }, (_, index) => ({
      nodeId: `synthetic-node-${index}`,
      resourceType: `SyntheticResource${index}`,
      rowRootEligible: false,
      populated: true,
      documentCount: index + 1,
    }));
    const syntheticEdges = syntheticNodes.map((node, index) => ({
      edgeId: `patient-synthetic-${index}`,
      fromNodeId: 'patient-node',
      toNodeId: node.nodeId,
      label: `synthetic${index}`,
    }));
    const duplicateResourceNode = {
      nodeId: 'synthetic-node-7-secondary',
      resourceType: 'SyntheticResource7',
      rowRootEligible: false,
      populated: true,
      documentCount: 9,
    };
    const expandedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [...catalog.nodes, ...syntheticNodes, duplicateResourceNode],
      edges: [
        ...catalog.edges,
        ...syntheticEdges,
        {
          edgeId: 'patient-synthetic-7-secondary',
          fromNodeId: 'patient-node',
          toNodeId: duplicateResourceNode.nodeId,
          label: 'synthetic7-secondary',
        },
      ],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, catalog: expandedCatalog },
      isLoading: false,
      refetch: vi.fn(),
    });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));

    const source = await screen.findByRole('group', { name: 'Add columns source' });
    const sourceOptions = within(source).getByRole('group', { name: 'Search scope' });
    expect(within(sourceOptions).getByRole('button', { name: 'Patient, Current table rows' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(sourceOptions).getByRole('button', { name: 'Patient, Current table rows' })).toBeInTheDocument();
    expect(source.querySelectorAll('[data-source-kind="RELATED"]')).toHaveLength(11);
    expect(within(sourceOptions).queryByRole('button', { name: /All accessible resources/ })).not.toBeInTheDocument();
    expect(within(source).queryByRole('group', { name: 'Related resources' })).not.toBeInTheDocument();
    expect(within(source).queryByRole('searchbox', { name: 'Search related resources' })).not.toBeInTheDocument();

    const matchingSource = within(sourceOptions).getByRole('button', {
      name: 'SyntheticResource7, Related resource · source synthetic-node-7',
    });
    expect(within(sourceOptions).getByRole('button', {
      name: 'SyntheticResource7, Related resource · source synthetic-node-7-secondary',
    })).toBeInTheDocument();
    fireEvent.click(matchingSource);
    expect(matchingSource).toHaveAttribute('aria-pressed', 'true');
    expect(matchingSource).toHaveAttribute('data-source-key', 'node:synthetic-node-7');
  });

  it('offers per-column repair for unsupported saved fields across all tables and suppresses repeated 422s', async () => {
    const rawCompileError = 'INVALID_RECIPE at $.recipe: invalid_construction at $.outputs[0].construction: source projection[2].type "unknown" is unsupported';
    const validPatientId = {
      column: 'patient_id',
      label: 'Patient ID',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'id', projectionMode: 'VALUE' as const } },
      table: { visible: true },
    };
    const unsupportedCollection = {
      column: 'collection',
      label: 'collection',
      logicalType: 'unknown',
      occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'collection', projectionMode: 'VALUE' as const } },
      table: { visible: true },
    };
    const unsupportedValueReference = {
      column: 'extension_value_reference',
      label: 'extension[].valueReference',
      logicalType: 'unknown',
      occurrenceId: 'base',
      source: { kind: 'field' as const, field: { path: 'extension[].valueReference', projectionMode: 'VALUE' as const } },
      table: { visible: true },
    };
    const invalidWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [
        {
          ...workspace.documents[0]!,
          output: { id: 'patients', title: 'Patients' },
          rootResourceType: 'Patient',
          route: { occurrenceId: 'base', resourceType: 'Patient' },
          columns: [validPatientId],
        },
        {
          ...workspace.documents[0]!,
          output: { id: 'specimens', title: 'Specimens' },
          rootResourceType: 'Specimen',
          route: { occurrenceId: 'base', resourceType: 'Specimen' },
          columns: [unsupportedCollection, unsupportedValueReference],
        },
      ],
      tabs: [
        { id: 'patients-tab', title: 'Patients', outputId: 'patients', order: 0, visible: true },
        { id: 'specimens-tab', title: 'Specimens', outputId: 'specimens', order: 1, visible: true },
      ],
    };
    const partiallyRepairedWorkspace: ExplorerBuilderWorkspace = {
      ...invalidWorkspace,
      documents: invalidWorkspace.documents.map((document) =>
        document.output.id === 'specimens'
          ? { ...document, columns: [unsupportedValueReference] }
          : document,
      ),
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: invalidWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    applyExplorerCommands.mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({
        commandId: 'remove-collection',
        workspace: partiallyRepairedWorkspace,
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [{ type: 'TABLE_CHANGED', outputId: 'specimens' }],
        diagnostics: [],
      }),
    });
    const reconcile = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockRejectedValue({
        code: 'DOCUMENT_COMPILE_FAILED',
        message: rawCompileError,
        requestId: 'compile-request-1',
      }),
    });
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    expect(await screen.findByRole('button', { name: 'Remove collection from Specimens' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open Specimens table to review collection' })).toBeInTheDocument();
    expect(screen.getByText('extension[].valueReference')).toBeInTheDocument();
    expect(mockLoomClient.getConstructionCapabilities).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    await waitFor(() => expect(reconcile).toHaveBeenCalledOnce());
    expect(await screen.findByText(
      'Some saved source fields have an unsupported type. Remove them to restore Preview and field selection.',
    )).toBeInTheDocument();
    const technicalDetails = screen.getByText(rawCompileError).closest('details');
    expect(technicalDetails).not.toBeNull();
    expect(technicalDetails?.open).toBe(false);
    expect(screen.queryByText(/Add from source is unavailable here:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Adding fields from related resources is unavailable here:/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Remove collection from Specimens' }));
    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'REMOVE_COLUMN',
        outputId: 'specimens',
        column: 'collection',
      }],
    })));
    const repairAlert = screen.getByRole('alert');
    expect(within(repairAlert).getByText('extension[].valueReference')).toBeInTheDocument();
    expect(within(repairAlert).queryByText('collection')).not.toBeInTheDocument();
    expect(mockLoomClient.getConstructionCapabilities).not.toHaveBeenCalled();
  });

  it('proposes a distinct related-record count when row values are also supported and applies only the proposal', async () => {
    configureRelatedSourceCountFixture();

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    fireEvent.click(await screen.findByRole('button', { name: 'Observation, Related resource' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const selectionOptions = await screen.findByTestId('catalog-selection-options');
    const routeChoice = within(selectionOptions).getByRole('radio', {
      name: 'Observation status: Patient -[observations]-> Observation',
    });
    fireEvent.click(routeChoice);
    fireEvent.click(within(selectionOptions).getByRole('radio', {
      name: 'Observation status: Count matching records',
    }));
    fireEvent.click(within(within(selectionOptions)
      .getByTestId('catalog-route-coverage-server-observation-related-choice'))
      .getByRole('button', { name: 'Check related values' }));
    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.some(
      ([request]) => request.requestId.startsWith('construction-route-coverage-'),
    )).toBe(true));
    const routeCoverageRequest = mockLoomClient.proposeConstruction.mock.calls.find(
      ([request]) => request.requestId.startsWith('construction-route-coverage-'),
    )?.[0];
    expect(routeCoverageRequest?.candidateConstruction).toEqual(expect.objectContaining({
      steps: [
        expect.objectContaining({ id: 'keep-vitals' }),
        expect.objectContaining({
          operation: expect.objectContaining({
            kind: 'RELATED_SOURCE',
            relatedSource: expect.objectContaining({ form: 'ALL', route: relatedSourceChoice.route }),
          }),
        }),
      ],
    }));
    expect(mockLoomClient.proposeConstructionChoices).not.toHaveBeenCalled();
    fireEvent.click(within(selectionOptions).getByRole('radio', {
      name: 'Only records where Observation status equals',
    }));
    fireEvent.change(within(selectionOptions).getByRole('textbox', {
      name: 'Observation status exact value',
    }), { target: { value: 'registered' } });
    fireEvent.click(within(selectionOptions).getByRole('button', { name: 'Preview related columns' }));

    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.some(
      ([request]) => !request.requestId.startsWith('construction-route-coverage-'),
    )).toBe(true));
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls.find(
      ([request]) => !request.requestId.startsWith('construction-route-coverage-'),
    )?.[0];
    expect(proposalArgs).toEqual(expect.objectContaining({
      outputId: 'patients',
      candidateConstruction: {
        version: 1,
        steps: [
          expect.objectContaining({ id: 'keep-vitals' }),
          expect.objectContaining({
            inputs: [{ kind: 'STEP_OUTPUT', stepId: 'keep-vitals' }],
            operation: expect.objectContaining({
              kind: 'RELATED_SOURCE',
              relatedSource: expect.objectContaining({
                anchorColumnId: '_key',
                choiceId: relatedSourceChoice.choiceId,
                sourceOccurrenceId: 'observation-node',
                contributorRule: {
                  policy: 'ALL_MATCHES',
                  predicate: {
                    candidateId: 'observation-status',
                    operator: 'EQUALS',
                    value: { kind: 'STRING', string: 'registered' },
                  },
                },
                form: 'COUNT',
                source: expect.objectContaining({
                  candidateId: 'observation-status',
                  nodeId: 'observation-node',
                  resourceType: 'Observation',
                  path: 'status',
                  cardinality: 'optional_one',
                  logicalType: 'string',
                }),
                route: relatedSourceChoice.route,
              }),
            }),
          }),
        ],
      },
    }));
    expect(proposalArgs.candidateConstruction.steps[1].outputs.at(-1)).toEqual(expect.objectContaining({
      name: 'related_Observation_count',
      label: 'Count of related Observation records where status equals registered',
      type: 'integer',
    }));
    expect(mockLoomClient.proposeConstructionChoices).not.toHaveBeenCalled();
    expect(await screen.findByText('Proposal preview')).toBeInTheDocument();
    expect(applyExplorerCommands).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Apply change' }));
    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledOnce());
    expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'APPLY_CONSTRUCTION_PROPOSAL',
        outputId: 'patients',
        proposalId: 'related-source-proposal',
      }],
    }));
  });

  it('keeps the existing Add action atomic for related COUNT columns', async () => {
    configureRelatedSourceCountFixture();

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    fireEvent.click(await screen.findByRole('button', { name: 'Observation, Related resource' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const selectionOptions = await screen.findByTestId('catalog-selection-options');
    fireEvent.click(within(selectionOptions).getByRole('radio', {
      name: 'Observation status: Patient -[observations]-> Observation',
    }));
    fireEvent.click(within(selectionOptions).getByRole('radio', {
      name: 'Observation status: Count matching records',
    }));
    fireEvent.click(within(selectionOptions).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.some(
      ([request]) => !request.requestId.startsWith('construction-route-coverage-'),
    )).toBe(true));
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls.find(
      ([request]) => !request.requestId.startsWith('construction-route-coverage-'),
    )?.[0];
    expect(proposalArgs?.candidateConstruction.steps[1]?.operation).toEqual(expect.objectContaining({
      kind: 'RELATED_SOURCE',
      relatedSource: expect.objectContaining({
        form: 'COUNT',
        route: relatedSourceChoice.route,
      }),
    }));
    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledOnce());
    expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'APPLY_CONSTRUCTION_PROPOSAL',
        outputId: 'patients',
        proposalId: 'related-source-proposal',
      }],
    }));
  });

  it('proposes two same-route related fields together while preserving the Patient row identity and auth scope', async () => {
    const construction: Construction = {
      version: 1,
      steps: [{
        id: 'keep-patients',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: {
          kind: 'FILTER',
          filter: { columnId: 'patient-row-key', operator: 'EXISTS' },
        },
        outputs: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
      }],
    };
    const patientRowKey = { id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' };
    const sourceStage: ConstructionStageDescriptor = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-key',
      columns: [patientRowKey],
      capabilities: [],
    };
    const selectedStage: ConstructionStageDescriptor = {
      id: 'keep-patients',
      inputStageId: 'source_projection',
      operation: 'FILTER',
      rowIdentityColumn: '_key',
      columns: [{ ...patientRowKey, cardinality: 'required_one' }],
      capabilities: [{ kind: 'RELATED_SOURCE', supported: true }],
    };
    const savedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]!, construction }],
    };
    const relatedCandidates = [
      { ...fieldCandidate('observation-id', 'observation-node', 'Observation', 'id'), label: 'Observation id' },
      {
        ...fieldCandidate('observation-value', 'observation-node', 'Observation', 'valueQuantity.value'),
        label: 'Observation value',
        logicalType: 'decimal',
      },
    ];
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: [...(catalog.candidates ?? []), ...relatedCandidates],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, catalog: relatedCatalog, workspace: savedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      baseConstruction: construction,
      stages: [sourceStage, selectedStage],
      selectedStage,
    }));
    const choiceForField = (candidate: ExplorerBuilderCandidate): ConstructionChoice => ({
      ...relatedSourceChoice,
      choiceId: `server-${candidate.candidateId}-related-choice`,
      source: {
        kind: 'FIELD',
        candidateId: candidate.candidateId,
        nodeId: candidate.nodeId,
        resourceType: 'Observation',
        path: candidate.fieldPath,
        cardinality: candidate.cardinality,
      },
      options: [{
        form: 'ALL',
        shape: 'LIST',
        decision: 'REQUIRES_DECISION',
        preservation: 'PRESERVING',
        rowEffect: 'PRESERVES_ROW_GRAIN',
        support: 'SUPPORTED',
        reason: 'Loom proves this list form preserves the selected Patient row grain.',
      }],
    });
    const choices = relatedCandidates.map(choiceForField);
    mockLoomClient.searchConstructionChoices.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly outputId: string;
      readonly source: { readonly candidateId: string };
    }) => ({
      snapshotToken: args.snapshotToken,
      outputId: args.outputId,
      complete: true,
      truncated: false,
      choices: choices.filter((choice) => choice.source.kind === 'FIELD' &&
        choice.source.candidateId === args.source.candidateId),
    }));
    const candidateProposalIds: string[] = [];
    mockLoomClient.proposeConstruction.mockImplementation(async (args: {
      readonly candidateConstruction: Construction;
      readonly changedStepId?: string;
      readonly requestId: string;
    }): Promise<ConstructionProposalResponse> => {
      const isRouteCoverageRequest = args.requestId.startsWith('construction-route-coverage-');
      const proposalId = isRouteCoverageRequest
        ? `route-coverage-${args.candidateConstruction.steps.length}`
        : `related-source-batch-proposal-${candidateProposalIds.length + 1}`;
      if (!isRouteCoverageRequest) candidateProposalIds.push(proposalId);
      const preview: ExplorerBuilderPreviewResult = {
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
        receiptId: proposalId,
        outputId: 'patients',
        columns: [
          { column: 'patient_row_key', label: 'Patient row key', logicalType: 'string', filterable: true, chartable: false },
          { column: 'related_Observation_id', label: 'Observation id', logicalType: 'string', filterable: true, chartable: false },
          { column: 'related_Observation_valueQuantity_value', label: 'Observation value', logicalType: 'decimal', filterable: true, chartable: false },
        ],
        rows: [{
          patient_row_key: 'patient-1',
          related_Observation_id: ['observation-1'],
          related_Observation_valueQuantity_value: [12.5],
        }],
        rowCount: 1,
        diagnostics: [],
      };
      return {
        proposalId,
        outputId: 'patients',
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'sha256:draft-1',
        baseDocumentDigest: 'document-1',
        candidateWorkspaceDigest: 'candidate-workspace-related-batch',
        changedStepId: args.changedStepId ?? '',
        candidateConstruction: args.candidateConstruction,
        dependencyImpact: { affectedStepIds: [] },
        stages: [sourceStage, selectedStage],
        previewStatus: 'READY',
        previewDurationMs: 7,
        preview,
      };
    });
    const savedPreview: ExplorerBuilderPreviewResult = {
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: 'saved-patient-preview',
      outputId: 'patients',
      columns: [{ column: 'patient_row_key', label: 'Patient row key', logicalType: 'string', filterable: true, chartable: false }],
      rows: [{ patient_row_key: 'patient-1' }],
      rowCount: 1,
      diagnostics: [],
    };
    const savedReceipt: ExplorerBuilderCompileResult = {
      apiVersion,
      kind: 'ExplorerBuilderReceipt',
      receiptId: savedPreview.receiptId,
      snapshotToken: 'snapshot-1',
      builder: savedWorkspace,
      outputs: [{ outputId: 'patients', columns: savedPreview.columns }],
      diagnostics: [],
    };
    const reconcileBuilder = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue(savedReceipt),
    });
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcileBuilder,
      { isLoading: false },
    ]);
    const previewBuilder = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue(savedPreview),
    });
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      previewBuilder,
      { isLoading: false },
    ]);

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(screen.getByText('Advanced source setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    fireEvent.click(await screen.findByRole('button', { name: 'Observation, Related resource' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.id' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.valueQuantity.value' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

    const selectionOptions = await screen.findByTestId('catalog-selection-options');
    expect(within(selectionOptions).getByRole('button', { name: 'Preview related columns' })).toBeInTheDocument();
    fireEvent.click(within(selectionOptions).getByRole('radio', { name: 'Observation id: Keep all matching values' }));
    fireEvent.click(within(selectionOptions).getByRole('radio', { name: 'Observation value: Keep all matching values' }));
    fireEvent.click(within(selectionOptions).getByRole('button', { name: 'Preview related columns' }));

    await screen.findByTestId('construction-proposal-ready');
    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.filter(
      ([request]) => request.requestId.startsWith('construction-proposal-'),
    )).toHaveLength(1));
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls.find(
      ([request]) => request.requestId.startsWith('construction-proposal-'),
    )?.[0];
    expect(proposalArgs).toEqual(expect.objectContaining({
      project: 'HTAN_INT/BForePC',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      explorerId: 'test',
      outputId: 'patients',
      candidateConstruction: expect.objectContaining({
        version: 1,
        steps: [
          expect.objectContaining({ id: 'keep-patients' }),
          expect.objectContaining({
            inputs: [{ kind: 'STEP_OUTPUT', stepId: 'keep-patients' }],
            operation: expect.objectContaining({
              kind: 'RELATED_SOURCE',
              relatedSource: expect.objectContaining({
                anchorColumnId: '_key',
                choiceId: 'server-observation-id-related-choice',
                sourceOccurrenceId: 'observation-node',
                source: expect.objectContaining({
                  candidateId: 'observation-id',
                  nodeId: 'observation-node',
                  resourceType: 'Observation',
                  path: 'id',
                  cardinality: 'optional_one',
                }),
                route: observationRouteChoice.route,
                form: 'ALL',
              }),
            }),
            outputs: [
              patientRowKey,
              expect.objectContaining({ name: 'related_Observation_id', label: 'Observation IDs' }),
            ],
          }),
          expect.objectContaining({
            inputs: [{ kind: 'STEP_OUTPUT', stepId: expect.any(String) }],
            operation: expect.objectContaining({
              kind: 'RELATED_SOURCE',
              relatedSource: expect.objectContaining({
                anchorColumnId: '_key',
                choiceId: 'server-observation-value-related-choice',
                sourceOccurrenceId: 'observation-node',
                source: expect.objectContaining({
                  candidateId: 'observation-value',
                  nodeId: 'observation-node',
                  resourceType: 'Observation',
                  path: 'valueQuantity.value',
                  cardinality: 'optional_one',
                  logicalType: 'decimal',
                }),
                route: observationRouteChoice.route,
                form: 'ALL',
              }),
            }),
            outputs: [
              patientRowKey,
              expect.objectContaining({ name: 'related_Observation_id', label: 'Observation IDs' }),
              expect.objectContaining({ name: 'related_Observation_valueQuantity_value', label: 'Observation value', type: 'decimal' }),
            ],
          }),
        ],
      }),
    }));
    const proposedSteps: Construction['steps'] = proposalArgs?.candidateConstruction.steps ?? [];
    expect(proposedSteps).toHaveLength(3);
    expect(proposedSteps?.[2]?.inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: proposedSteps[1]?.id }]);
    expect(proposedSteps?.[1]?.outputs.map((column) => column.id)).toEqual([
      'patient-row-key', proposedSteps[1]?.outputs[1]?.id,
    ]);
    expect(proposedSteps?.[2]?.outputs.map((column) => column.id)).toEqual([
      'patient-row-key', proposedSteps[1]?.outputs[1]?.id, proposedSteps[2]?.outputs[2]?.id,
    ]);
    expect(screen.getByTestId('construction-proposal-ready')).toHaveTextContent(
      '1 row and 3 columns · checked in 7 ms.',
    );

    fireEvent.click(screen.getByTestId('construction-cancel-proposal'));
    expect(applyExplorerCommands).not.toHaveBeenCalled();
    expect(savedWorkspace.documents[0]?.construction).toEqual(construction);
    await waitFor(() => expect(screen.queryByTestId('construction-proposal-ready')).not.toBeInTheDocument());
    const savedTable = screen.getByRole('table');
    expect(within(savedTable).getByRole('columnheader', { name: 'Patient row key' })).toBeInTheDocument();
    expect(within(savedTable).getByRole('cell')).toHaveTextContent('patient-1');
    expect(within(savedTable).queryByRole('columnheader', { name: 'Observation id' })).not.toBeInTheDocument();
    expect(within(savedTable).queryByRole('columnheader', { name: 'Observation value' })).not.toBeInTheDocument();

    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    fireEvent.click(await screen.findByRole('button', { name: 'Observation, Related resource' }));
    fireEvent.click(screen.getByRole('tab', { name: 'Other fields' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.id' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select Observation.valueQuantity.value' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 2 selected features' }));

    const restagedOptions = await screen.findByTestId('catalog-selection-options');
    fireEvent.click(within(restagedOptions).getByRole('radio', { name: 'Observation id: Keep all matching values' }));
    fireEvent.click(within(restagedOptions).getByRole('radio', { name: 'Observation value: Keep all matching values' }));
    fireEvent.click(within(restagedOptions).getByRole('button', { name: 'Preview related columns' }));
    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.filter(
      ([request]) => request.requestId.startsWith('construction-proposal-'),
    )).toHaveLength(2));
    expect(candidateProposalIds).toEqual([
      'related-source-batch-proposal-1',
      'related-source-batch-proposal-2',
    ]);
    const restagedProposalArgs = mockLoomClient.proposeConstruction.mock.calls.filter(
      ([request]) => request.requestId.startsWith('construction-proposal-'),
    )[1]?.[0];
    if (!restagedProposalArgs) throw new Error('The restaged related-source proposal was not captured.');
    const restagedConstruction: Construction = restagedProposalArgs.candidateConstruction;
    expect(restagedConstruction.steps.slice(1).map((step) => step.operation.kind))
      .toEqual(['RELATED_SOURCE', 'RELATED_SOURCE']);
    expect(restagedConstruction.steps[1]?.inputs)
      .toEqual([{ kind: 'STEP_OUTPUT', stepId: 'keep-patients' }]);
    expect(restagedConstruction.steps[1]?.operation).toEqual(expect.objectContaining({
      kind: 'RELATED_SOURCE',
      relatedSource: expect.objectContaining({
        source: expect.objectContaining({ candidateId: 'observation-id' }),
        route: observationRouteChoice.route,
        form: 'ALL',
      }),
    }));
    expect(applyExplorerCommands).not.toHaveBeenCalled();
    expect(await screen.findByText('Proposal preview')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Apply change' }));
    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledOnce());
    expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'APPLY_CONSTRUCTION_PROPOSAL',
        outputId: 'patients',
        proposalId: 'related-source-batch-proposal-2',
      }],
    }));
  });

  it('does not offer related sources while editing an earlier construction step', async () => {
    const construction = {
      version: 1,
      steps: [
        {
          id: 'keep-vitals',
          inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
          operation: {
            kind: 'FILTER' as const,
            filter: { columnId: 'patient-row-key', operator: 'EXISTS' as const },
          },
          outputs: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
        },
        {
          id: 'keep-reports',
          inputs: [{ kind: 'STEP_OUTPUT' as const, stepId: 'keep-vitals' }],
          operation: {
            kind: 'FILTER' as const,
            filter: { columnId: 'patient-row-key', operator: 'EXISTS' as const },
          },
          outputs: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
        },
      ],
    };
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: '_key',
      columns: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
      capabilities: [{ kind: 'RELATED_SOURCE', supported: true }],
    };
    const laterStage = {
      id: 'keep-reports',
      inputStageId: 'keep-vitals',
      operation: 'FILTER',
      rowIdentityColumn: '_key',
      columns: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
      capabilities: [{ kind: 'RELATED_SOURCE', supported: true }],
    };
    const relatedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]!, construction }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: relatedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => {
      const selectedStage = args.stageId === 'source_projection' ? sourceStage : laterStage;
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: construction,
        stages: [sourceStage, laterStage],
        selectedStage,
      };
    });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByTestId('construction-history-step-keep-vitals'));
    fireEvent.click(screen.getByTestId('construction-edit-step-keep-vitals'));
    await waitFor(() => {
      expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalledWith(
        expect.objectContaining({ stageId: 'source_projection' }),
        expect.any(AbortSignal),
      );
    });
    expect(screen.getByText('Editing a saved step')).toBeInTheDocument();
    expect(screen.queryByTestId('construction-source-setup')).toBeNull();
    expect(mockLoomClient.proposeConstruction).not.toHaveBeenCalled();
    expect(applyExplorerCommands).not.toHaveBeenCalled();
  });

  it('edits a saved related source in place and keeps its output selectable before a fresh preview', async () => {
    const construction: Construction = {
      version: 1,
      steps: [
        {
          id: 'related-observation',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'RELATED_SOURCE',
            relatedSource: {
              anchorColumnId: '_key',
              choiceId: 'saved-observation-choice',
              sourceOccurrenceId: 'observation-node',
              source: {
                kind: 'FIELD',
                candidateId: 'observation-status',
                nodeId: 'observation-node',
                resourceType: 'Observation',
                path: 'status',
                cardinality: 'optional_one',
                logicalType: 'string',
              },
              route: relatedSourceChoice.route,
              contributorRule: { policy: 'ALL_MATCHES' },
              form: 'ALL',
              outputColumnId: 'related-observation-status',
            },
          },
          outputs: [
            { id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' },
            { id: 'related-observation-status', name: 'related_observation_status', label: 'Observation status', type: 'string' },
          ],
        },
        {
          id: 'keep-related-status',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'related-observation' }],
          operation: {
            kind: 'FILTER',
            filter: { columnId: 'related-observation-status', operator: 'EXISTS' },
          },
          outputs: [
            { id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' },
            { id: 'related-observation-status', name: 'related_observation_status', label: 'Observation status', type: 'string' },
          ],
        },
      ],
    };
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: '_key',
      columns: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
      capabilities: [{ kind: 'RELATED_SOURCE', supported: true }],
    };
    const relatedStage = {
      id: 'related-observation',
      inputStageId: 'source_projection',
      operation: 'RELATED_SOURCE',
      rowIdentityColumn: '_key',
      columns: [
        { id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' },
        { id: 'related-observation-status', name: 'related_observation_status', label: 'Observation status', type: 'string' },
      ],
      capabilities: [],
    };
    const finalStage = {
      id: 'keep-related-status',
      inputStageId: 'related-observation',
      operation: 'FILTER',
      rowIdentityColumn: '_key',
      columns: relatedStage.columns,
      capabilities: [{ kind: 'RELATED_SOURCE', supported: true }],
    };
    const relatedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]!, construction }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: relatedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
    }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      baseConstruction: construction,
      stages: [sourceStage, relatedStage, finalStage],
      selectedStage: args.stageId === 'source_projection' ? sourceStage : finalStage,
    }));
    const reportRelatedChoice: ConstructionChoice = {
      ...relatedSourceChoice,
      choiceId: 'server-report-related-choice',
      route: [{
        edgeId: 'patient-reports',
        fromNodeId: 'patient-node',
        toNodeId: 'report-node',
        fromResourceType: 'Patient',
        toResourceType: 'DiagnosticReport',
        relationship: 'reports',
        storageDirection: 'OUTBOUND',
        matchMode: 'OPTIONAL',
      }],
      source: {
        kind: 'FIELD',
        candidateId: 'report-status',
        nodeId: 'report-node',
        resourceType: 'DiagnosticReport',
        path: 'status',
        cardinality: 'optional_one',
      },
      options: [{ ...relatedSourceChoice.options[0]!, decision: 'DEFAULT' }],
    };
    mockLoomClient.searchConstructionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'patients',
      complete: true,
      truncated: false,
      choices: [reportRelatedChoice],
    });
    mockLoomClient.proposeConstruction.mockImplementation(async (args: {
      readonly candidateConstruction: Construction;
      readonly changedStepId?: string;
    }): Promise<ConstructionProposalResponse> => ({
      proposalId: 'edit-related-proposal',
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-workspace-1',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: ['keep-related-status'] },
      stages: [],
      previewStatus: 'READY',
      previewDurationMs: 7,
    }));
    mockLoomClient.preview.mockResolvedValue({
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: 'edit-related-proposal',
      outputId: 'patients',
      columns: [
        { column: 'patient_row_key', label: 'Patient row key', logicalType: 'string', filterable: true, chartable: false },
        { column: 'related_DiagnosticReport_status', label: 'DiagnosticReport status', logicalType: 'string', filterable: true, chartable: false },
      ],
      rows: [{ patient_row_key: 'patient-1', related_DiagnosticReport_status: 'final' }],
      rowCount: 1,
      diagnostics: [],
    });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    await screen.findByTestId('construction-history');
    fireEvent.click(screen.getByTestId('construction-history-step-related-observation'));
    expect(screen.getByTestId('construction-edit-step-related-observation')).toBeInTheDocument();
    expect(screen.getByTestId('construction-remove-step-related-observation')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-edit-step-related-observation'));
    const relatedSourceEditor = await screen.findByTestId('related-source-step-editor');
    expect(within(relatedSourceEditor).getByRole('heading', { name: 'Edit connection to Observation' })).toBeInTheDocument();
    const outputName = screen.getByRole('textbox', { name: 'Output column name' });
    const outputLabel = screen.getByRole('textbox', { name: 'Output column label' });
    expect(outputName).toBeInstanceOf(HTMLInputElement);
    expect(outputLabel).toBeInstanceOf(HTMLInputElement);
    expect((outputName as HTMLInputElement).value).toBe('related_observation_status');
    expect((outputLabel as HTMLInputElement).value).toBe('Observation status');
    expect(screen.getByText('Current source: Observation.status')).toBeInTheDocument();
    expect(screen.getByText('Current route: Patient -[observations]-> Observation')).toBeInTheDocument();

    const editor = within(screen.getByTestId('related-source-step-editor'));
    const relatedSourceSelector = editor.getByRole('combobox', { name: 'Related source to inspect' });
    const reportOption = within(relatedSourceSelector).getByRole('option', { name: /DiagnosticReport/ });
    fireEvent.change(relatedSourceSelector, { target: { value: (reportOption as HTMLOptionElement).value } });
    fireEvent.change(editor.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'DiagnosticReport' },
    });
    fireEvent.click(editor.getByRole('button', { name: 'Search' }));
    fireEvent.click(await editor.findByRole('button', { name: 'Select DiagnosticReport.status' }));
    fireEvent.click(editor.getByRole('button', { name: 'Add 1 selected feature' }));
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls[0]?.[0];
    expect(proposalArgs).toEqual(expect.objectContaining({
      changedStepId: 'related-observation',
      candidateConstruction: {
        version: 1,
        steps: [
          expect.objectContaining({
            id: 'related-observation',
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: {
              kind: 'RELATED_SOURCE',
              relatedSource: expect.objectContaining({
                anchorColumnId: '_key',
                choiceId: reportRelatedChoice.choiceId,
                sourceOccurrenceId: 'report-node',
                source: expect.objectContaining({
                  candidateId: 'report-status',
                  nodeId: 'report-node',
                  resourceType: 'DiagnosticReport',
                  path: 'status',
                  cardinality: 'optional_one',
                  logicalType: 'string',
                }),
                route: reportRelatedChoice.route,
                contributorRule: { policy: 'ALL_MATCHES' },
                form: 'ALL',
                outputColumnId: 'related-observation-status',
              }),
            },
            outputs: expect.arrayContaining([
              expect.objectContaining({
                id: 'related-observation-status',
                name: 'related_DiagnosticReport_status',
                label: 'DiagnosticReport status',
              }),
            ]),
          }),
          expect.objectContaining({
            id: 'keep-related-status',
            inputs: [{ kind: 'STEP_OUTPUT', stepId: 'related-observation' }],
            outputs: expect.arrayContaining([
              expect.objectContaining({
                id: 'related-observation-status',
                name: 'related_DiagnosticReport_status',
                label: 'DiagnosticReport status',
              }),
            ]),
          }),
        ],
      },
    }));

    await screen.findByTestId('construction-proposal-ready');
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));
    await waitFor(() => expect(applyExplorerCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'APPLY_CONSTRUCTION_PROPOSAL',
        outputId: 'patients',
        proposalId: 'edit-related-proposal',
      }],
    })));
  });

  it('creates a populated row type with its direct ID and renders the first preview', async () => {
    window.sessionStorage.clear();
    const selectedTableStorageKey = `loom.builder.selected-table:${JSON.stringify([
      'HTAN_INT/BForePC',
      '/programs/HTAN_INT/projects/BForePC',
      'test',
    ])}`;
    const { getSuggestions, reconcile, previewBuilder } = configureInitialTableFlow(
      applyExplorerCommands,
      [fieldCandidate('patient-id', 'patient-node', 'Patient', 'id')],
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.change(screen.getByRole('textbox', { name: 'Table name (optional)' }), {
      target: { value: 'CDA Patients' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Choose Patient rows' }));

    await waitFor(() => expect(previewBuilder).toHaveBeenCalledOnce());
    expect(window.sessionStorage.getItem(selectedTableStorageKey)).toBe('patients');
    expect(getSuggestions).toHaveBeenCalledWith(expect.objectContaining({
      snapshotToken: 'snapshot-1',
      nodeId: 'patient-node',
    }));
    expect(applyExplorerCommands.mock.calls.map(([request]) =>
      (request as { readonly commands: ReadonlyArray<ExplorerBuilderCommand> }).commands[0]?.type,
    )).toEqual(['CREATE_TABLE', 'ADD_COLUMN']);
    expect(applyExplorerCommands.mock.calls[0]?.[0]).toEqual(expect.objectContaining({
      commands: [{ type: 'CREATE_TABLE', title: 'CDA Patients', rootNodeId: 'patient-node' }],
    }));
    expect(applyExplorerCommands.mock.calls[1]?.[0]).toEqual(expect.objectContaining({
      commands: [{
        type: 'ADD_COLUMN',
        outputId: 'patients',
        occurrenceId: 'base',
        candidateId: 'patient-id',
        projectionMode: 'VALUE',
        initialPresentation: 'TABLE',
        title: 'Patient ID',
      }],
    }));
    expect(reconcile).toHaveBeenCalledOnce();
    expect(reconcile.mock.calls[0]?.[0]).toMatchObject({ draftVersion: 2, draftDigest: 'sha256:draft-2' });
    expect(previewBuilder).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'receipt-1',
      outputId: 'patients',
      limit: 25,
    }));
    expect(await screen.findByText('patient-1')).toBeInTheDocument();
  });

  it('waits for first-table ID provisioning before resolving configured column context', async () => {
    const { previewBuilder } = configureInitialTableFlow(
      applyExplorerCommands,
      [fieldCandidate('patient-id', 'patient-node', 'Patient', 'id')],
    );
    let releaseAddColumn: (() => void) | undefined;
    applyExplorerCommands.mockImplementation((request: {
      readonly commands: ReadonlyArray<ExplorerBuilderCommand>;
    }) => ({
      unwrap: vi.fn().mockImplementation(async () => {
        const command = request.commands[0];
        if (command?.type === 'CREATE_TABLE') {
          return {
            commandId: 'create-patient-table',
            workspace: initialPatientWorkspace(command.title ?? 'Patient'),
            draftVersion: 1,
            draftDigest: 'sha256:draft-1',
            results: [{ type: 'TABLE_CREATED', outputId: 'patients', occurrenceId: 'base' }],
            diagnostics: [],
          };
        }
        await new Promise<void>((resolve) => { releaseAddColumn = resolve; });
        return {
          commandId: 'add-patient-id',
          workspace: initialPatientWorkspace('CDA Patients', [{
            column: 'id',
            label: 'Patient id',
            logicalType: 'string',
            occurrenceId: 'base',
            source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
            table: { visible: true },
          }]),
          draftVersion: 2,
          draftDigest: 'sha256:draft-2',
          results: [{ type: 'COLUMN_ADDED', outputId: 'patients', occurrenceId: 'base', column: 'id' }],
          diagnostics: [],
        };
      }),
    }));
    mockLoomClient.configuredColumnContextsQuery.mockClear();

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Choose Patient rows' }));

    await waitFor(() => {
      expect(applyExplorerCommands).toHaveBeenCalledTimes(2);
      expect(releaseAddColumn).toBeDefined();
    });
    // The second command is deliberately unresolved, so this is the intermediate
    // draftVersion 1 window between CREATE_TABLE and default-ID provisioning.
    expect(mockLoomClient.configuredColumnContextsQuery.mock.calls.every(([args]) => args === undefined)).toBe(true);

    const release = releaseAddColumn;
    await act(async () => release?.());
    await waitFor(() => expect(mockLoomClient.configuredColumnContextsQuery.mock.calls.some(([args]) =>
      args?.expectedDraftVersion === 2 && args.expectedDraftDigest === 'sha256:draft-2')).toBe(true));
    const configuredContextCall = mockLoomClient.configuredColumnContextsQuery.mock.calls.find((call) => call[0] !== undefined);
    expect(configuredContextCall?.[0]).toEqual(expect.objectContaining({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 2,
      expectedDraftDigest: 'sha256:draft-2',
    }));
    expect(configuredContextCall?.[1]).toBe(JSON.stringify([
      JSON.stringify(['HTAN_INT/BForePC', '/programs/HTAN_INT/projects/BForePC', 'test']),
      'snapshot-1',
      2,
      'sha256:draft-2',
      0,
    ]));
    expect(configuredContextCall).toBeDefined();
    await waitFor(() => expect(previewBuilder).toHaveBeenCalledOnce());
  });

  it('creates a root table and explains when the catalog has no executable direct ID', async () => {
    const { reconcile, previewBuilder } = configureInitialTableFlow(
      applyExplorerCommands,
      [fieldCandidate('patient-name', 'patient-node', 'Patient', 'name')],
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Choose Patient rows' }));

    expect(await screen.findByText(/Patient was created with Patient rows/)).toBeInTheDocument();
    expect(applyExplorerCommands).toHaveBeenCalledOnce();
    expect(applyExplorerCommands.mock.calls[0]?.[0]).toEqual(expect.objectContaining({
      commands: [{ type: 'CREATE_TABLE', title: 'Patient', rootNodeId: 'patient-node' }],
    }));
    expect(reconcile).not.toHaveBeenCalled();
    expect(previewBuilder).not.toHaveBeenCalled();
  });

  it('creates a new table from the inline row picker and previews its verified ID', async () => {
    const patientIdColumn: ExplorerBuilderWorkspace['documents'][number]['columns'][number] = {
      column: 'id',
      label: 'Patient id',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true },
    };
    const specimenIdColumn: ExplorerBuilderWorkspace['documents'][number]['columns'][number] = {
      column: 'specimen_id',
      label: 'Specimen id',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true },
    };
    const existingWorkspace = initialPatientWorkspace('Patients', [patientIdColumn]);
    const specimenCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        {
          nodeId: 'specimen-node',
          resourceType: 'Specimen',
          rowRootEligible: true,
          populated: true,
          documentCount: 12,
        },
      ],
      candidates: [fieldCandidate('patient-id', 'patient-node', 'Patient', 'id')],
    };
    const workspaceWithNewTable: ExplorerBuilderWorkspace = {
      ...existingWorkspace,
      documents: [
        ...existingWorkspace.documents,
        {
          ...existingWorkspace.documents[0]!,
          output: { id: 'specimens', title: 'Specimen review' },
          rootResourceType: 'Specimen',
          route: { occurrenceId: 'base', resourceType: 'Specimen' },
          columns: [],
        },
      ],
      tabs: [
        ...existingWorkspace.tabs,
        {
          id: 'specimens-tab',
          title: 'Specimen review',
          outputId: 'specimens',
          order: 1,
          visible: true,
        },
      ],
    };
    const workspaceWithSpecimenId: ExplorerBuilderWorkspace = {
      ...workspaceWithNewTable,
      documents: workspaceWithNewTable.documents.map((document) =>
        document.output.id === 'specimens'
          ? { ...document, columns: [specimenIdColumn] }
          : document,
      ),
    };
    const suggestion = fieldCandidate('specimen-id', 'specimen-node', 'Specimen', 'id');
    const getSuggestions = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({
        apiVersion,
        kind: 'ExplorerBuilderCandidateSuggestions',
        snapshotToken: 'snapshot-1',
        nodeId: 'specimen-node',
        candidates: [suggestion],
        diagnostics: [],
      }),
    });
    (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue([
      getSuggestions,
      { isLoading: false },
    ]);
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: {
        ...builderState,
        workspace: existingWorkspace,
        catalog: specimenCatalog,
      },
      isLoading: false,
      refetch: vi.fn(),
    });

    applyExplorerCommands.mockImplementation((request: {
      readonly commands: ReadonlyArray<ExplorerBuilderCommand>;
    }) => ({
      unwrap: vi.fn().mockImplementation(async () => {
        const command = request.commands[0];
        if (command?.type === 'CREATE_TABLE') {
          return {
            commandId: 'create-specimen-table',
            workspace: workspaceWithNewTable,
            draftVersion: 2,
            draftDigest: 'sha256:draft-2',
            results: [{ type: 'TABLE_CREATED', outputId: 'specimens', occurrenceId: 'base' }],
            diagnostics: [],
          };
        }
        return {
          commandId: 'add-specimen-id',
          workspace: workspaceWithSpecimenId,
          draftVersion: 3,
          draftDigest: 'sha256:draft-3',
          results: [{ type: 'COLUMN_ADDED', outputId: 'specimens', occurrenceId: 'base', column: 'specimen_id' }],
          diagnostics: [],
        };
      }),
    }));

    const preview: ExplorerBuilderPreviewResult = {
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: 'specimen-receipt',
      outputId: 'specimens',
      columns: [{
        column: 'specimen_id',
        label: 'Specimen id',
        logicalType: 'string',
        filterable: true,
        chartable: false,
      }],
      rows: [{ specimen_id: 'specimen-1' }],
      rowCount: 1,
      diagnostics: [],
    };
    let rejectInitialReconcile: ((reason: unknown) => void) | undefined;
    const initialReconcilePromise = new Promise<ExplorerBuilderCompileResult>((_resolve, reject) => {
      rejectInitialReconcile = reject;
    });
    const initialReconcileAbort = vi.fn(() => {
      rejectInitialReconcile?.(Object.assign(
        new Error('Superseded initial automatic compile.'),
        { code: 'CLIENT_CANCELLED' },
      ));
    });
    const initialReconcileRequest = {
      unwrap: vi.fn(() => initialReconcilePromise),
      abort: initialReconcileAbort,
    };
    const reconcile = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue({
        apiVersion,
        kind: 'ExplorerBuilderReceipt',
        receiptId: 'specimen-receipt',
        snapshotToken: 'snapshot-1',
        builder: workspaceWithSpecimenId,
        outputs: [{ outputId: 'specimens', columns: preview.columns }],
        diagnostics: [],
      } satisfies ExplorerBuilderCompileResult),
    }).mockReturnValueOnce(initialReconcileRequest);
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);
    const previewBuilder = vi.fn().mockReturnValue({
      unwrap: vi.fn().mockResolvedValue(preview),
    });
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      previewBuilder,
      { isLoading: false },
    ]);
    const browserPrompt = vi.spyOn(window, 'prompt').mockReturnValue('Unexpected prompt');

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    await waitFor(() => expect(reconcile).toHaveBeenCalledOnce());
    expect(reconcile.mock.calls[0]?.[0]).toMatchObject({
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
    });
    fireEvent.click(screen.getByTestId('construction-new-table'));
    fireEvent.click(screen.getByTestId('construction-new-table'));
    expect(screen.getAllByRole('region', { name: 'Choose row type' })).toHaveLength(1);
    expect(applyExplorerCommands).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Table name (optional)' }), {
      target: { value: 'Specimen review' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Choose Specimen rows' }));

    await waitFor(() => expect(previewBuilder).toHaveBeenCalledOnce());
    await waitFor(() => expect(initialReconcileAbort).toHaveBeenCalled());
    expect(browserPrompt).not.toHaveBeenCalled();
    browserPrompt.mockRestore();
    expect(getSuggestions).toHaveBeenCalledWith(expect.objectContaining({
      snapshotToken: 'snapshot-1',
      nodeId: 'specimen-node',
    }));
    expect(applyExplorerCommands.mock.calls.map(([request]) =>
      (request as { readonly commands: ReadonlyArray<ExplorerBuilderCommand> }).commands[0]?.type,
    )).toEqual(['CREATE_TABLE', 'ADD_COLUMN']);
    expect(applyExplorerCommands.mock.calls[0]?.[0]).toEqual(expect.objectContaining({
      commands: [{ type: 'CREATE_TABLE', title: 'Specimen review', rootNodeId: 'specimen-node' }],
    }));
    expect(applyExplorerCommands.mock.calls[1]?.[0]).toEqual(expect.objectContaining({
      commands: [{
        type: 'ADD_COLUMN',
        outputId: 'specimens',
        occurrenceId: 'base',
        candidateId: 'specimen-id',
        projectionMode: 'VALUE',
        initialPresentation: 'TABLE',
        title: 'Specimen ID',
      }],
    }));
    expect(reconcile).toHaveBeenCalledTimes(2);
    expect(reconcile.mock.calls[0]?.[0]).toMatchObject({ draftVersion: 1, draftDigest: 'sha256:draft-1' });
    expect(reconcile.mock.calls[1]?.[0]).toMatchObject({ draftVersion: 3, draftDigest: 'sha256:draft-3' });
    expect(previewBuilder).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'specimen-receipt',
      outputId: 'specimens',
      limit: 25,
    }));
    expect(screen.getByTestId('construction-table-patients')).toBeInTheDocument();
    expect(screen.getByTestId('construction-table-specimens')).toBeInTheDocument();
    expect(await screen.findByText('specimen-1')).toBeInTheDocument();
    const previewPanel = screen.getByTestId('construction-preview');
    expect(previewPanel).toHaveAttribute('data-preview-status', 'ready');
    expect(previewPanel).toHaveAttribute('data-preview-receipt-id', 'specimen-receipt');
    expect(previewPanel).toHaveAttribute('data-preview-output-id', 'specimens');
  });
});
