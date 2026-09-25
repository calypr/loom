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
  ConstructionChoice,
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
  ExplorerBuilderState,
  ExplorerBuilderWorkspace,
} from '../../types';
import BuilderWorkspace from './BuilderWorkspace';

const mockLoomClient = vi.hoisted(() => ({
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

vi.mock('./components/BuilderToolbar', () => ({ BuilderToolbar: () => null }));
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
vi.mock('./components/PreviewTable', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./components/PreviewTable')>();
  return { ...actual, PreviewTable: () => null };
});
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
  }],
};

const mutationResult = () => [
  vi.fn().mockReturnValue({ unwrap: vi.fn().mockResolvedValue({}) }),
  { isLoading: false },
];

describe('BuilderWorkspace Add columns source selection', () => {
  let applyExplorerCommands: Mock;

  beforeEach(() => {
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

    const source = await screen.findByRole('combobox', { name: 'Add columns source' });
    if (!(source instanceof HTMLSelectElement)) {
      throw new Error('The Add columns source control should be a select element.');
    }
    expect(source.value).toBe('node:report-node');
    expect(within(source).getByRole('option', { name: 'Observation — related source' })).toBeInTheDocument();

    fireEvent.change(source, { target: { value: 'node:observation-node' } });
    expect(source.value).toBe('node:observation-node');

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

  it('proposes a supported related source at the selected stage and applies only the proposal', async () => {
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
    const source = await screen.findByRole('combobox', { name: 'Add columns source' });
    fireEvent.change(source, { target: { value: 'node:observation-node' } });
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Observation.status' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add 1 selected feature' }));

    const selectionDialog = await screen.findByRole('dialog', { name: 'Choose output forms' });
    fireEvent.click(within(selectionDialog).getByRole('radio', {
      name: 'Observation status: LIST · PRESERVING · ALL',
    }));
    fireEvent.click(within(selectionDialog).getByRole('button', { name: 'Add 1 selected feature' }));

    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls[0]?.[0];
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
                contributorRule: { policy: 'ALL_MATCHES' },
                form: 'ALL',
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
});
