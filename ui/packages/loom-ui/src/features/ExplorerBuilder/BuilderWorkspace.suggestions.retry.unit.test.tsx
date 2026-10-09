// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCandidate, ExplorerBuilderState } from '../../types';
import BuilderWorkspace from './BuilderWorkspace';

const hooks = vi.hoisted(() => {
  const browseSemanticInventory = vi.fn();
  return {
    getSuggestions: vi.fn(),
    browseSemanticInventory,
    client: { browseSemanticInventory },
  };
});

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  const mutation = () => [vi.fn(), { isLoading: false }];
  return {
    ...actual,
    useLoomClient: () => hooks.client,
    useApplyExplorerBuilderCommandsV2Mutation: () => mutation(),
    useAssessExplorerRowChangeMutation: () => mutation(),
    useCreateExplorerAuthoringMutation: () => mutation(),
    useDeleteExplorerAuthoringMutation: () => mutation(),
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: {} } }),
    useGetExplorerAuthoringExplorersQuery: () => ({
      data: [{ explorerId: 'test', title: 'Test Explorer' }],
      isLoading: false,
      refetch: vi.fn(),
    }),
    useGetExplorerBuilderStateV2Query: () => ({ data: builderState, isLoading: false, refetch: vi.fn() }),
    useGetExplorerCandidateSuggestionsV2Mutation: () => [hooks.getSuggestions, { isLoading: false }],
    usePreviewExplorerAuthoringV2Mutation: () => mutation(),
    usePublishExplorerAuthoringV2Mutation: () => mutation(),
    useReconcileExplorerBuilderV2Mutation: () => mutation(),
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('./useAutomaticPreview', () => ({ useAutomaticPreview: () => ({ error: undefined, isLoading: false }) }));
vi.mock('./hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('./hooks/usePortalHost', () => ({ usePortalHost: () => null }));
vi.mock('./constructionWorkspace/useConstructionLifecycle', () => ({
  useConstructionLifecycle: () => {
    const sourceProjectionStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'patient-id-column',
      columns: [],
      capabilities: [],
    };
    return {
      capabilities: {
        status: 'ready',
        response: {
          snapshotToken: 'snapshot-1',
          draftVersion: 1,
          draftDigest: 'sha256:draft-1',
          outputId: 'patients',
          stageId: 'source_projection',
          baseConstruction: { version: 1, steps: [] },
          stages: [sourceProjectionStage],
          selectedStage: sourceProjectionStage,
          workspaceInputs: [],
        },
      },
      proposal: { status: 'idle' },
      canApply: false,
      pivotDiscovery: undefined,
      cancel: vi.fn(),
      retry: vi.fn(),
      beginApply: vi.fn(),
      finishApply: vi.fn(),
      onCandidateChange: vi.fn(),
      onDiscoverCategories: vi.fn(),
    };
  },
}));
vi.mock('./components/BuilderToolbar', () => ({ BuilderToolbar: () => null }));
vi.mock('./components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('./components/GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: () => null }));
vi.mock('./components/ColumnSelector', () => ({ ColumnSelector: () => null }));
vi.mock('./components/RowRootPicker', () => ({ RowRootPicker: () => null }));
vi.mock('./components/PreviewTable', () => ({ PreviewTable: () => null }));
vi.mock('./components/DataframeContractPanel', () => ({ DataframeContractPanel: () => null }));
vi.mock('./components/PopulationPanel', () => ({ PopulationPanel: () => null }));
vi.mock('./components/RowChangeRepairPanel', () => ({ RowChangeRepairPanel: () => null }));
vi.mock('./components/RowChangePreviewPanel', () => ({ RowChangePreviewPanel: () => null }));
vi.mock('./components/RowDefinitionPanel', () => ({ RowDefinitionPanel: () => null }));
vi.mock('./components/RowDefinitionSettingsPanel', () => ({ RowDefinitionSettingsPanel: () => null }));
vi.mock('./components/TableShapeSettingsPanel', () => ({ TableShapeSettingsPanel: () => null }));
vi.mock('./components/InterpretationPanel', () => ({ InterpretationPanel: () => null }));
vi.mock('./constructionWorkspace/ConstructionWorkspace', () => ({
  ConstructionWorkspace: () => null,
  ConstructionUndoButton: () => null,
  constructionOperationFamilies: [],
}));
vi.mock('./constructionWorkspace/ConstructionColumnSelection', () => ({ ConstructionColumnSelection: () => null }));
vi.mock('./constructionWorkspace/PairedColumnSuggestions', () => ({ PairedColumnSuggestions: () => null }));
vi.mock('./constructionWorkspace/FrameSourcePanel', () => ({ FrameSourcePanel: () => null }));
vi.mock('./constructionWorkspace/ConstructionProposalPanel', () => ({ ConstructionProposalPanel: () => null }));
vi.mock('./constructionWorkspace/ConstructionProposalPreview', () => ({ ConstructionProposalPreview: () => null }));
vi.mock('./constructionWorkspace/PreviewValueCoverage', () => ({ PreviewValueCoverage: () => null }));
vi.mock('./constructionOperations/ConstructionOperationEditor', () => ({ ConstructionOperationEditor: () => null }));
vi.mock('./constructionOperations/FilterRowsEditor', () => ({ FilterRowsEditor: () => null }));
vi.mock('./constructionOperations/ConstructionReshapeEditor', () => ({
  ConstructionReshapeEditor: () => null,
  createReshapeEditorEntry: vi.fn(),
  hasPublicScalarListColumn: vi.fn(() => false),
}));
vi.mock('./constructionOperations/RelatedSourceStepEditor', () => ({ RelatedSourceStepEditor: () => null }));
vi.mock('./constructionOperations/RelatedFieldEditor', () => ({ RelatedFieldEditor: () => null }));
vi.mock('./constructionOperations/ConstructionCombineEditor', () => ({ ConstructionCombineEditor: () => null }));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const builderState: ExplorerBuilderState = {
  apiVersion,
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  catalog: {
    snapshotToken: 'snapshot-1',
    generation: 'generation-1',
    routePolicy: {},
    nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 }],
    edges: [],
    candidates: [],
  },
  workspace: {
    apiVersion,
    kind: 'ExplorerBuilderWorkspace',
    explorer: { title: 'Test Explorer' },
    documents: [{
      kind: 'ExplorerBuilderDocument',
      output: { id: 'patients', title: 'Patients' },
      rootResourceType: 'Patient',
      route: { occurrenceId: 'base', resourceType: 'Patient' },
      rows: { kind: 'RECORDS', records: {} },
      columns: [{
        columnId: 'patient-id-column',
        column: 'id',
        label: 'Patient ID',
        occurrenceId: 'base',
        source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
        table: { visible: true, order: 0 },
      }],
    }],
    tabs: [{ id: 'patients-tab', title: 'Patients', outputId: 'patients', order: 0, visible: true }],
  },
};

const recoveredPatientCandidate: ExplorerBuilderCandidate = {
  candidateId: 'patient-name-text',
  nodeId: 'patient-node',
  fieldPath: 'name[0].text',
  label: 'Patient name',
  logicalType: 'string',
  cardinality: 'optional_one',
  repeated: false,
  filterable: true,
  chartable: false,
  projectionModes: ['VALUE'],
  defaultProjectionMode: 'VALUE',
  aggregateOperations: [],
  transformations: {
    temporalReduction: {
      available: false,
      reason: 'This field has no temporal values to reduce.',
      timestampFields: [],
      anchorFields: [],
    },
    unitNormalization: {
      available: false,
      reason: 'This field has no units to normalize.',
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
  constructionChoice: {
    choiceId: 'patient-name-text-choice',
    route: [],
    presentation: {
      summary: 'Patient.name[0].text',
      facts: [{ label: 'Field', value: 'name[0].text' }],
    },
    source: {
      kind: 'FIELD',
      candidateId: 'patient-name-text',
      nodeId: 'patient-node',
      resourceType: 'Patient',
      path: 'name[0].text',
      cardinality: 'optional_one',
    },
    options: [{
      form: 'VALUE',
      shape: 'SCALAR',
      decision: 'DEFAULT',
      preservation: 'PRESERVING',
      rowEffect: 'PRESERVES_ROW_GRAIN',
      support: 'SUPPORTED',
      reason: 'This scalar field preserves the Patient row grain.',
    }],
  },
};

describe('BuilderWorkspace suggestion transport recovery', () => {
  beforeEach(() => {
    hooks.getSuggestions.mockReset();
    hooks.browseSemanticInventory.mockReset().mockResolvedValue({
      contextToken: 'semantic-context-1',
      buildId: 'semantic-build-1',
      state: 'complete',
      sourceAvailability: 'verified',
      entries: [],
    });
  });

  it('offers an actionable retry after a transport failure and deduplicates the recovered request', async () => {
    hooks.getSuggestions
      .mockReturnValueOnce({ unwrap: () => Promise.reject({ code: 'SERVICE_UNAVAILABLE', message: 'temporarily unavailable' }) })
      .mockReturnValueOnce({ unwrap: () => Promise.resolve({
        apiVersion,
        kind: 'ExplorerBuilderCandidateSuggestions',
        snapshotToken: 'snapshot-1',
        nodeId: 'patient-node',
        candidates: [recoveredPatientCandidate],
        diagnostics: [],
      }) });

    const { container, rerender } = render(<BuilderWorkspace project="test-project" explorerId="test" />);
    const advancedSourceSetup = container.querySelector<HTMLDetailsElement>(
      '[data-testid="construction-source-setup"]',
    );
    expect(advancedSourceSetup?.open).toBe(false);

    const failure = await screen.findByTestId('builder-suggestions-error');
    expect(screen.getByRole('alert')).toBe(failure);
    expect(failure.closest('details[data-testid="construction-source-setup"]')).toBeNull();
    expect(failure.closest('[hidden], [aria-hidden="true"]')).toBeNull();
    expect(advancedSourceSetup?.open).toBe(false);
    const retry = screen.getByRole('button', { name: 'Retry finding columns' });
    expect(retry.closest('details[data-testid="construction-source-setup"]')).toBeNull();
    expect(retry.closest('[hidden], [aria-hidden="true"]')).toBeNull();
    expect(failure.textContent).toContain('Available columns could not be loaded: temporarily unavailable (SERVICE_UNAVAILABLE)');
    expect((retry as HTMLButtonElement).disabled).toBe(false);
    expect(container.querySelector('input[aria-label="Select Patient.name[0].text"]')).toBeNull();
    fireEvent.click(retry);

    await waitFor(() => expect(hooks.getSuggestions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(container.querySelector('[data-testid="builder-suggestions-error"]')).toBeNull());
    expect(advancedSourceSetup?.open).toBe(false);

    fireEvent.click(screen.getByText('Advanced source setup'));
    await waitFor(() => expect(advancedSourceSetup?.open).toBe(true));
    const recoveredOption = await screen.findByRole('checkbox', {
      name: 'Select Patient.name[0].text',
    });
    expect((recoveredOption as HTMLInputElement).disabled).toBe(false);
    fireEvent.click(recoveredOption);
    expect((recoveredOption as HTMLInputElement).checked).toBe(true);
    expect(advancedSourceSetup?.open).toBe(true);

    expect(hooks.getSuggestions.mock.calls.map(([input]) => input)).toEqual([
      expect.objectContaining({ project: 'test-project', explorerId: 'test', snapshotToken: 'snapshot-1', nodeId: 'patient-node' }),
      expect.objectContaining({ project: 'test-project', explorerId: 'test', snapshotToken: 'snapshot-1', nodeId: 'patient-node' }),
    ]);

    rerender(<BuilderWorkspace project="test-project" explorerId="test" />);
    await new Promise(resolve => setTimeout(resolve, 0));
    await waitFor(() => expect(hooks.getSuggestions).toHaveBeenCalledTimes(2));
  });
});
