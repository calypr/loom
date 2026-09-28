// @vitest-environment jsdom
import React from 'react';
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
  ConstructionChoice,
  ConstructionProposalResponse,
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
  ExplorerBuilderCommand,
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../types';
import BuilderWorkspace from './BuilderWorkspace';

const mockLoomClient = vi.hoisted(() => ({
  browseFrameSourceOptions: vi.fn(),
  browseSemanticInventory: vi.fn(),
  getConstructionCapabilities: vi.fn(),
  getSelection: vi.fn(),
  preview: vi.fn(),
  proposeConstruction: vi.fn(),
  resolveConfiguredColumnContexts: vi.fn(),
  searchConstructionChoices: vi.fn(),
}));

vi.mock('../../react', () => ({
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
}));

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
vi.mock('./components/RowChangeRepairPanel', () => ({ RowChangeRepairPanel: () => null }));
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
    mockLoomClient.getSelection.mockReset();
    mockLoomClient.preview.mockReset();
    mockLoomClient.proposeConstruction.mockReset();
    mockLoomClient.resolveConfiguredColumnContexts.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      libraries: [],
      pinnedRevisions: [],
      columns: [],
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

  it('inspects an Observation route without a saved occurrence id and does not apply an unsupported source choice', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByText('Source and column setup'));
    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(screen.getByRole('button', { name: 'Select saved DiagnosticReport occurrence' }));
    fireEvent.click(screen.getByText('Source and column setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));

    const source = await screen.findByRole('group', { name: 'Add columns source' });
    const currentRows = within(source).getByRole('group', { name: 'Search scope' });
    const relatedSources = within(source).getByRole('group', { name: 'Related resources' });
    expect(within(currentRows).getByRole('button', { name: 'Patient, Current table rows' })).toBeInTheDocument();
    const reportSource = within(relatedSources).getByRole('button', {
      name: 'DiagnosticReport, Related resource, selected occurrence',
    });
    expect(reportSource).toHaveAttribute('aria-pressed', 'true');
    const observationSource = within(relatedSources).getByRole('button', {
      name: 'Observation, Related resource',
    });
    fireEvent.click(observationSource);
    expect(observationSource).toHaveAttribute('aria-pressed', 'true');
    expect(reportSource).toHaveAttribute('aria-pressed', 'false');

    const relatedField = await screen.findByRole('checkbox', { name: 'Select Observation.status' });
    expect(relatedField).toBeDisabled();
    expect(within(screen.getByTestId('construction-operation-editor')).queryByRole(
      'checkbox',
      { name: 'Select DiagnosticReport.status' },
    )).not.toBeInTheDocument();
    const constructionEditor = within(screen.getByTestId('construction-operation-editor'));
    fireEvent.click(constructionEditor.getByText('Inspect meaning, evidence, and construction choices'));
    fireEvent.click(constructionEditor.getByRole('button', { name: 'Load choices for these table rows' }));

    await waitFor(() => expect(mockLoomClient.searchConstructionChoices).toHaveBeenCalledTimes(1));
    const searchArgs = mockLoomClient.searchConstructionChoices.mock.calls[0]?.[0];
    expect(searchArgs).toEqual(expect.objectContaining({
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      source: { kind: 'FIELD', candidateId: 'observation-status' },
    }));
    expect(searchArgs).not.toHaveProperty('occurrenceId');

    expect(applyExplorerCommands).not.toHaveBeenCalled();
  });

  it('shows metadata-driven related sources and keeps the current row source available while searching', async () => {
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
    fireEvent.click(screen.getByText('Source and column setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));

    const source = await screen.findByRole('group', { name: 'Add columns source' });
    const currentRows = within(source).getByRole('group', { name: 'Search scope' });
    const relatedSources = within(source).getByRole('group', { name: 'Related resources' });
    expect(within(currentRows).getByRole('button', { name: 'All accessible resources, Search coded concepts and fields across the dataset' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(currentRows).getByRole('button', { name: 'Patient, Current table rows' })).toBeInTheDocument();
    fireEvent.click(within(relatedSources).getByText('Related resources (11)'));
    expect(within(relatedSources).getAllByTestId('construction-add-columns-source-option')).toHaveLength(11);

    const search = within(relatedSources).getByRole('searchbox', { name: 'Search related resources' });
    fireEvent.change(search, { target: { value: 'SyntheticResource7' } });
    expect(within(relatedSources).getAllByTestId('construction-add-columns-source-option')).toHaveLength(2);
    expect(within(currentRows).getByRole('button', { name: 'Patient, Current table rows' })).toBeInTheDocument();

    const matchingSource = within(relatedSources).getByRole('button', {
      name: 'SyntheticResource7, Related resource · source synthetic-node-7',
    });
    expect(within(relatedSources).getByRole('button', {
      name: 'SyntheticResource7, Related resource · source synthetic-node-7-secondary',
    })).toBeInTheDocument();
    fireEvent.click(matchingSource);
    expect(matchingSource).toHaveAttribute('aria-pressed', 'true');
    expect(matchingSource).toHaveAttribute('data-source-key', 'node:synthetic-node-7');
    expect((search as HTMLInputElement).value).toBe('');
    expect(within(relatedSources).getAllByTestId('construction-add-columns-source-option')).toHaveLength(11);
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

  it('proposes a distinct related-record count at the selected stage and applies only the proposal', async () => {
    const construction = {
      version: 1,
      steps: [{
        id: 'keep-vitals',
        inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
        operation: {
          kind: 'FILTER' as const,
          filter: { columnId: 'patient-row-key', operator: 'EXISTS' as const },
        },
        outputs: [{
          id: 'patient-row-key',
          name: 'patient_row_key',
          label: 'Patient row key',
        }],
      }],
    };
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-key',
      columns: [{ id: 'patient-row-key', name: 'patient_row_key', label: 'Patient row key' }],
      capabilities: [],
    };
    const selectedStage = {
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
      stages: [sourceStage, selectedStage],
      selectedStage,
    }));
    mockLoomClient.searchConstructionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'patients',
      complete: true,
      truncated: false,
      choices: [relatedSourceChoice],
    });
    mockLoomClient.proposeConstruction.mockImplementation(async (args: {
      readonly candidateConstruction: unknown;
    }) => ({
      proposalId: 'related-source-proposal',
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-workspace-1',
      changedStepId: '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage, selectedStage],
      previewStatus: 'READY',
      previewDurationMs: 7,
    }));
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

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(screen.getByText('Source and column setup'));
    fireEvent.click(await screen.findByTestId('construction-action-add-columns'));
    fireEvent.click(await screen.findByRole('button', { name: 'Observation, Related resource' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation.status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const selectionDialog = await screen.findByRole('dialog', { name: 'Choose how to add these fields' });
    fireEvent.click(within(selectionDialog).getByRole('radio', {
      name: 'Observation status: Count matching records',
    }));
    fireEvent.click(within(selectionDialog).getByRole('radio', {
      name: 'Only records where Observation status equals',
    }));
    fireEvent.change(within(selectionDialog).getByRole('textbox', {
      name: 'Observation status exact value',
    }), { target: { value: 'registered' } });
    fireEvent.click(within(selectionDialog).getByRole('button', { name: 'Add 1 column' }));

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
    expect(screen.getByText(/Adding fields from related resources is unavailable here:/))
      .toHaveTextContent('Close the saved-step editor before adding related fields');
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

    await screen.findByTestId('construction-column-selection');
    expect(screen.getByRole('button', { name: 'Select Observation status (string)' }))
      .toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-history-step-related-observation'));
    expect(screen.getByTestId('construction-edit-step-related-observation')).toBeInTheDocument();
    expect(screen.getByTestId('construction-remove-step-related-observation')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-edit-step-related-observation'));

    expect(await screen.findByTestId('related-source-step-editor')).toBeInTheDocument();
    const outputName = screen.getByRole('textbox', { name: 'Output column name' });
    const outputLabel = screen.getByRole('textbox', { name: 'Output column label' });
    expect(outputName).toBeInstanceOf(HTMLInputElement);
    expect(outputLabel).toBeInstanceOf(HTMLInputElement);
    expect((outputName as HTMLInputElement).value).toBe('related_observation_status');
    expect((outputLabel as HTMLInputElement).value).toBe('Observation status');
    expect(screen.getByText('Current source: Observation.status')).toBeInTheDocument();
    expect(screen.getByText('Current route: Patient → Observation via Observations')).toBeInTheDocument();

    const editor = within(screen.getByTestId('related-source-step-editor'));
    const relatedSourceSelector = editor.getByRole('combobox', { name: 'Related source to inspect' });
    const reportOption = within(relatedSourceSelector).getByRole('option', { name: /DiagnosticReport/ });
    fireEvent.change(relatedSourceSelector, { target: { value: (reportOption as HTMLOptionElement).value } });
    fireEvent.change(editor.getByRole('searchbox', { name: 'Search features by field name, concept, or code' }), {
      target: { value: 'DiagnosticReport' },
    });
    fireEvent.click(editor.getByRole('button', { name: 'Search' }));
    fireEvent.click(await editor.findByRole('checkbox', { name: 'Select DiagnosticReport.status' }));
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
    expect(previewBuilder).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'receipt-1',
      outputId: 'patients',
      limit: 25,
    }));
    expect(await screen.findByText('patient-1')).toBeInTheDocument();
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
    const browserPrompt = vi.spyOn(window, 'prompt').mockReturnValue('Unexpected prompt');

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByTestId('construction-new-table'));
    fireEvent.click(screen.getByTestId('construction-new-table'));
    expect(screen.getAllByRole('region', { name: 'Choose row type' })).toHaveLength(1);
    expect(applyExplorerCommands).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Table name (optional)' }), {
      target: { value: 'Specimen review' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Choose Specimen rows' }));

    await waitFor(() => expect(previewBuilder).toHaveBeenCalledOnce());
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
    expect(reconcile).toHaveBeenCalledOnce();
    expect(previewBuilder).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'specimen-receipt',
      outputId: 'specimens',
      limit: 25,
    }));
    expect(screen.getByTestId('construction-table-patients')).toBeInTheDocument();
    expect(screen.getByTestId('construction-table-specimens')).toBeInTheDocument();
    expect(await screen.findByText('specimen-1')).toBeInTheDocument();
  });
});
