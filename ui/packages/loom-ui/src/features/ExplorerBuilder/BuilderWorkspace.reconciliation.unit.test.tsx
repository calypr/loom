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
import BuilderWorkspace from './BuilderWorkspace';
import type { SelectionRevision } from '../../selection';

const mockLoomClient = vi.hoisted(() => ({
  getSelection: vi.fn(),
  searchPopulationRoutes: vi.fn(),
  createSelection: vi.fn(),
  resolveConfiguredColumnContexts: vi.fn(),
  getTableShapeCapabilities: vi.fn(),
  discoverTableShapeCategories: vi.fn(),
  resolveTableShape: vi.fn(),
  proposeTableShape: vi.fn(),
  getConstructionCapabilities: vi.fn(),
  proposeConstruction: vi.fn(),
  preview: vi.fn(),
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
  BuilderToolbar: ({
    onPreview,
    onPublish,
    onSelectTable,
    tables,
    previewDisabled,
    publishDisabled,
    publishing,
    busy,
  }: {
    readonly onPreview: () => void;
    readonly onPublish: () => void;
    readonly onSelectTable: (outputId: string) => void;
    readonly tables: ReadonlyArray<{ readonly outputId: string }>;
    readonly previewDisabled: boolean;
    readonly publishDisabled: boolean;
    readonly publishing: boolean;
    readonly busy: boolean;
  }) => (
    <div>
      <button
        type="button"
        disabled={previewDisabled || busy}
        onClick={onPreview}
      >
        Preview
      </button>
      <button
        type="button"
        disabled={publishDisabled || publishing}
        aria-busy={publishing}
        onClick={onPublish}
      >
        {publishing ? 'Publishing…' : 'Publish'}
      </button>
      {tables[1] ? (
        <button type="button" onClick={() => onSelectTable(tables[1].outputId)}>
          Select second table
        </button>
      ) : null}
    </div>
  ),
}));

vi.mock('./components/GuidedGraphWorkspace', () => ({
  GuidedGraphWorkspace: ({
    onChangeEdge,
    onChangeBase,
  }: {
    readonly onChangeEdge: (occurrenceId: string, edgeId: string) => void;
    readonly onChangeBase: (nodeId: string) => void;
  }) => (
    <>
      <button
        type="button"
        onClick={() =>
          onChangeEdge('patient-subject', 'specimen-patient-participant')
        }
      >
        Change relationship
      </button>
      <button type="button" onClick={() => onChangeBase('patient-node')}>
        Change rows
      </button>
    </>
  ),
}));

vi.mock('./components/ConceptCatalog', () => ({
  ConceptCatalog: ({
    onAddSelected,
    disabled,
    disabledReason,
  }: {
    readonly onAddSelected?: (
      selections: ReadonlyArray<{
        constructionChoice: { choiceId: string; form: 'VALUE' | 'ALL' };
        title?: string;
      }>,
    ) => Promise<void>;
    readonly disabled?: boolean;
    readonly disabledReason?: string;
  }) => (
    <>
      <button
        type="button"
        disabled={disabled}
        onClick={() => void onAddSelected?.([
          {
            constructionChoice: { choiceId: 'field-choice-a', form: 'VALUE' },
            title: 'Field A',
          },
          {
            constructionChoice: { choiceId: 'semantic-choice-a', form: 'ALL' },
            title: 'Feature A',
          },
        ])}
      >
        Add catalog fixture
      </button>
      {disabled && disabledReason ? <p role="status">{disabledReason}</p> : null}
    </>
  ),
}));

vi.mock('./components/ColumnSelector', () => ({
  ColumnSelector: ({
    onChange,
  }: {
    readonly onChange: (value: unknown) => void;
  }) => (
    <button
      type="button"
      onClick={() =>
        onChange({
          column: 'specimen_identifier',
          label: 'Updated specimen identifier',
          occurrenceId: 'base',
          source: {
            kind: 'field',
            field: {
              path: 'identifier[].value',
              projectionMode: 'FIRST',
            },
          },
          table: { visible: true, order: 0 },
        })
      }
    >
      Save column change
    </button>
  ),
}));

vi.mock('./components/PreviewTable', () => ({
  PreviewTable: () => <div>Preview table</div>,
}));

vi.mock('./constructionOperations/ConstructionReshapeEditor', () => ({
  ConstructionReshapeEditor: ({
    editingStep,
  }: {
    readonly editingStep?: { readonly id: string; readonly operation: { readonly kind: string } };
  }) => (
    <div
      data-testid="construction-reshape-editor"
      data-editing-step-id={editingStep?.id ?? ''}
      data-editing-operation={editingStep?.operation.kind ?? ''}
    />
  ),
}));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const column = {
  column: 'specimen_identifier',
  label: 'Specimen identifier',
  occurrenceId: 'base',
  source: {
    kind: 'field' as const,
    field: {
      path: 'identifier[].value',
      projectionMode: 'FIRST',
    },
  },
  table: { visible: true, order: 0 },
};
const workspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace' as const,
  explorer: { title: 'Test Explorer' },
  documents: [
    {
      kind: 'ExplorerBuilderDocument' as const,
      output: { id: 'specimens', title: 'Specimens' },
      rootResourceType: 'Specimen',
      route: { occurrenceId: 'base', resourceType: 'Specimen' },
      rows: { kind: 'RECORDS' as const, records: {} },
      columns: [column],
    },
  ],
  tabs: [
    {
      id: 'specimens-tab',
      title: 'Specimens',
      outputId: 'specimens',
      order: 0,
      visible: true,
    },
  ],
};
const catalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
  nodes: [
    {
      nodeId: 'specimen-node',
      resourceType: 'Specimen',
      rowRootEligible: true,
      populated: true,
      documentCount: 1,
    },
  ],
  edges: [],
  candidates: [
    {
      candidateId: 'specimen-id',
      nodeId: 'specimen-node',
      fieldPath: 'identifier[].value',
      label: 'Specimen identifier',
      logicalType: 'string',
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      filterable: true,
      chartable: false,
    },
  ],
};
const builderState = {
  apiVersion,
  kind: 'ExplorerBuilderState' as const,
  lifecycleState: 'READY' as const,
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  workspace,
  catalog,
};
const receipt = {
  apiVersion,
  kind: 'ExplorerBuilderReceipt' as const,
  receiptId: 'receipt-1',
  snapshotToken: 'snapshot-1',
  builder: workspace,
  outputs: [
    {
      outputId: 'specimens',
      columns: [
        {
          column: 'specimen_identifier',
          label: 'Specimen identifier',
          logicalType: 'string',
          filterable: true,
          chartable: false,
        },
      ],
    },
  ],
  diagnostics: [],
};

const tableShapeCapabilities = {
  catalogId: 'shape-catalog', outputId: 'specimens',
  reshapeModes: [{ choiceId: 'mode-none', choiceKind: 'reshapeMode', label: 'Keep columns', mode: 'NONE', availability: { kind: 'supported' } }],
  groupColumns: [], categoryColumns: [], valueColumns: [], pivotCategoryDiscovery: { kind: 'not-requested' },
  duplicatePolicies: [], missingCellPolicies: [], unlistedCategoryPolicies: [], unpivotColumns: [],
  unpivotKeyOutput: { kind: 'unsupported', reason: 'No key is available.' },
  unpivotValueOutput: { kind: 'unsupported', reason: 'No value is available.' },
  unpivotNullRowPolicies: [], derivedAvailability: { kind: 'supported' },
  unpivotWithDerivedAvailability: { kind: 'unsupported', reason: 'Not supported after unpivot.' },
  derivedOutputSuggestions: [{
    choiceId: 'suggestion-calculated', choiceKind: 'derivedOutput', label: 'Calculated value',
    availability: { kind: 'supported' }, resultTypeLabel: 'number',
    suggestedOutput: { column: 'calculated', label: 'Calculated value' },
  }],
  binaryOperators: [{
    choiceId: 'operator-add', choiceKind: 'binaryOperator', label: 'Add', availability: { kind: 'supported' },
    requiresDivisionByZeroPolicy: false,
  }],
  operands: [
    { choiceId: 'weight', choiceKind: 'operand', label: 'Weight', availability: { kind: 'supported' } },
    { choiceId: 'height', choiceKind: 'operand', label: 'Height', availability: { kind: 'supported' } },
  ],
  missingInputPolicies: [{ choiceId: 'missing-propagate', choiceKind: 'missingInputPolicy', label: 'Propagate missing', availability: { kind: 'supported' } }],
  divisionByZeroPolicies: [],
  savedProposalIntent: { kind: 'NONE', reshapeMode: { kind: 'reshapeMode', choiceId: 'mode-none' }, derivedColumns: [] },
  savedProposalAvailability: { kind: 'supported' },
};
const tableShapeComparison = {
  status: 'AVAILABLE', base: { rowCount: 1, sampled: false }, candidate: { rowCount: 1, sampled: false },
  changedColumns: [], changedRowCount: 0, changedRowsSampled: false, changedRows: [],
  contributors: [], contributorsSampled: false, evidenceLimitations: [], notices: [],
  exclusions: { status: 'COMPLETE', records: [], complete: true, sampled: false },
  declaredInformationLoss: { status: 'COMPLETE', items: [] },
};

const resolvedRequest = <T,>(value: T) => ({
  unwrap: vi.fn().mockResolvedValue(value),
  abort: vi.fn(),
});

const rejectedRequest = (error: unknown) => ({
  unwrap: vi.fn().mockRejectedValue(error),
  abort: vi.fn(),
});

const deferredRequest = <T,>() => {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return {
    request: { unwrap: vi.fn(() => promise), abort: vi.fn() },
    resolve,
  };
};

const abortableRequest = <T,>() => {
  let reject: (reason: Error) => void = () => undefined;
  const promise = new Promise<T>((_resolve, rejectPromise) => {
    reject = rejectPromise;
  });
  const abort = vi.fn(() => reject(new Error('CLIENT_CANCELLED')));
  return { unwrap: vi.fn(() => promise), abort };
};

describe('BuilderWorkspace on-demand reconciliation', () => {
  let applyCommands: Mock;
  let resolveContext: Mock;
  let assessRowChange: Mock;
  let reconcile: Mock;
  let preview: Mock;
  let publish: Mock;

  beforeEach(() => {
    mockLoomClient.getSelection.mockReset();
    resolveContext = vi.fn(async (args: { snapshotToken: string; expectedDraftVersion: number; expectedDraftDigest: string }) => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      libraries: [],
      pinnedRevisions: [],
      columns: [{
        outputId: 'specimens',
        column: 'specimen_identifier',
        occurrenceId: 'base',
        resolution: { state: 'READY' as const, capabilityCandidateIds: ['specimen-id'], applicableRevisionIds: [] },
      }],
    }));
    mockLoomClient.resolveConfiguredColumnContexts = resolveContext;
    mockLoomClient.getTableShapeCapabilities.mockResolvedValue(tableShapeCapabilities);
    mockLoomClient.discoverTableShapeCategories.mockResolvedValue({});
    mockLoomClient.resolveTableShape.mockResolvedValue({
      catalogId: 'shape-catalog', resolutionId: 'derived-resolution', kind: 'DERIVED', outputDescriptors: [], postPivotOperands: [],
    });
    mockLoomClient.proposeTableShape.mockResolvedValue({
      proposalId: 'shape-proposal', baseReceiptId: 'receipt-1', baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-1', draftDigest: 'sha256:draft-1', draftVersion: 1,
      mode: 'ADD', outputId: 'specimens', snapshotToken: 'snapshot-1', comparison: tableShapeComparison,
    });
    mockLoomClient.getConstructionCapabilities.mockImplementation(async (args: {
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
        columns: [{ id: 'specimen_identifier_id', name: 'specimen_identifier', label: 'Specimen identifier', type: 'string' }],
        capabilities: [
          { kind: 'FILTER' as const, supported: true },
          { kind: 'DERIVE' as const, supported: true },
          { kind: 'PIVOT' as const, supported: false, reason: 'Not supported in this fixture.' },
          { kind: 'UNPIVOT' as const, supported: false, reason: 'Not supported in this fixture.' },
        ],
      };
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: { version: 1, steps: [] },
        stages: [selectedStage],
        selectedStage,
      };
    });
    applyCommands = vi.fn().mockReturnValue(
      resolvedRequest({
        commandId: 'command-1',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [
          {
            type: 'TABLE_CHANGED',
            outputId: 'specimens',
            column: 'specimen_identifier',
          },
        ],
        diagnostics: [],
      }),
    );
    assessRowChange = vi.fn().mockReturnValue(resolvedRequest({
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      status: 'READY',
      currentRootResourceType: 'Specimen',
      candidateRootResourceType: 'Patient',
      preservedFeatureKeys: ['specimen_identifier'],
      proposal: {
        outputId: 'specimens',
        rootNodeId: 'patient-node',
        rootOccurrenceId: 'patient-subject',
        sourceDocumentDigest: 'sha256:document-1',
        routeRebase: [{ occurrenceId: 'base', edgeId: 'patient-specimen' }],
        preservedFeatureKeys: ['specimen_identifier'],
      },
      unresolved: [],
      diagnostics: [],
    }));
    reconcile = vi.fn().mockReturnValue(resolvedRequest(receipt));
    preview = vi.fn().mockReturnValue(
      resolvedRequest({
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        receiptId: 'receipt-1',
        outputId: 'specimens',
        columns: receipt.outputs[0].columns,
        rows: [],
        rowCount: 0,
        diagnostics: [],
      }),
    );
    publish = vi.fn().mockReturnValue(resolvedRequest({}));

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
      applyCommands,
      { isLoading: false },
    ]);
    (useAssessExplorerRowChangeMutation as Mock).mockReturnValue([
      assessRowChange,
      { isLoading: false },
    ]);
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      preview,
      { isLoading: false },
    ]);
    (usePopulationMappingMutation as Mock).mockReturnValue([
      vi.fn(),
      { isLoading: false },
    ]);
    (usePublishExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      publish,
      { isLoading: false },
    ]);
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue([
      vi.fn(),
      { isLoading: false },
    ]);
    (useDeleteExplorerAuthoringMutation as Mock).mockReturnValue([
      vi.fn(),
      { isLoading: false },
    ]);
    (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue(
      [vi.fn(), { isLoading: false }],
    );
  });

  it('starts a blank table from the row picker without opening the graph', async () => {
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: {
        ...builderState,
        lifecycleState: 'NEW',
        draftVersion: 0,
        draftDigest: '',
        workspace: null,
        catalog: {
          ...catalog,
          nodes: [
            ...catalog.nodes,
            {
              nodeId: 'patient-node',
              resourceType: 'Patient',
              rowRootEligible: true,
              populated: true,
              documentCount: 42,
            },
          ],
        },
      },
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

    fireEvent.change(screen.getByLabelText('Table name'), {
      target: { value: 'Patient features' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create table' }));

    expect(await screen.findByText('What should one row represent?')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Change rows' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Choose Patient rows' }));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'CREATE_TABLE',
        title: 'Patient features',
        rootNodeId: 'patient-node',
      }],
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    expect(await screen.findByRole('button', { name: 'Change rows' })).toBeInTheDocument();
  });

  it('sends selected fields and concepts as one construction-choice command batch', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Add catalog fixture' }));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [
        {
          type: 'APPLY_CONSTRUCTION_CHOICE',
        outputId: 'specimens',
          constructionChoice: { choiceId: 'field-choice-a', form: 'VALUE' },
          title: 'Field A',
        },
        {
          type: 'APPLY_CONSTRUCTION_CHOICE',
          outputId: 'specimens',
          constructionChoice: { choiceId: 'semantic-choice-a', form: 'ALL' },
          title: 'Feature A',
        },
      ],
    }));
  });

  it('explains that source choices are unavailable while the previous draft update is pending', async () => {
    const pending = deferredRequest<{
      readonly commandId: string;
      readonly workspace: typeof workspace;
      readonly draftVersion: number;
      readonly draftDigest: string;
      readonly results: ReadonlyArray<never>;
      readonly diagnostics: ReadonlyArray<never>;
    }>();
    applyCommands.mockReturnValue(pending.request);

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Add catalog fixture' }));

    expect(await screen.findByText(
      'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.',
    )).toHaveAttribute('role', 'status');

    pending.resolve({
      commandId: 'pending-command',
      workspace,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [],
      diagnostics: [],
    });
    await waitFor(() => expect(screen.queryByText(
      'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.',
    )).not.toBeInTheDocument());
  });

  it('routes saved Group history edits to the typed Reshape editor', async () => {
    const groupStep = {
      id: 'group_specimens',
      inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'GROUP' as const,
        group: {
          constructionId: 'group_specimens',
          keys: [{ inputColumnId: 'specimen_identifier_id', outputColumnId: 'specimen_identifier_group' }],
          aggregates: [{ operation: 'COUNT_ROWS' as const, outputColumnId: 'specimen_count' }],
        },
      },
      outputs: [
        { id: 'specimen_identifier_group', name: 'specimen_identifier_group', label: 'Specimen identifier' },
        { id: 'specimen_count', name: 'specimen_count', label: 'Specimen count', type: 'integer' },
      ],
    };
    const groupWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        construction: { version: 1, steps: [groupStep] },
      }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: groupWorkspace },
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

    fireEvent.click(await screen.findByTestId('construction-action-reshape'));
    expect(await screen.findByTestId('construction-reshape-editor')).toHaveAttribute('data-editing-step-id', '');
    fireEvent.click(screen.getByTestId('construction-history-step-group_specimens'));
    fireEvent.click(screen.getByTestId('construction-edit-step-group_specimens'));

    await waitFor(() => {
      expect(screen.getByTestId('construction-reshape-editor')).toHaveAttribute('data-editing-step-id', 'group_specimens');
      expect(screen.getByTestId('construction-reshape-editor')).toHaveAttribute('data-editing-operation', 'GROUP');
    });
    expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalledWith(
      expect.objectContaining({ outputId: 'specimens', stageId: 'source_projection' }),
      expect.any(AbortSignal),
    );
  });

  it('does not reconcile a hydrated draft until Preview requests a receipt', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    await waitFor(() => expect(previewButton).toBeEnabled());
    await waitFor(() => expect(resolveContext).toHaveBeenCalledTimes(1));
    expect(resolveContext).toHaveBeenCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      expectedDraftDigest: 'sha256:draft-1',
    }, expect.any(AbortSignal));
    expect(reconcile).not.toHaveBeenCalled();

    fireEvent.click(previewButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  });

  it('clears an interrupted attached-selection load when switching tables', async () => {
    const attachedWorkspace = {
      ...workspace,
      documents: [
        {
          ...workspace.documents[0],
          population: { selectionRevisionId: 'selection-1', route: [] },
        },
        {
          ...workspace.documents[0],
          output: { id: 'patients', title: 'Patients' },
          rootResourceType: 'Patient',
          route: { occurrenceId: 'base', resourceType: 'Patient' },
          columns: [],
        },
      ],
      tabs: [
        ...workspace.tabs,
        {
          id: 'patients-tab',
          title: 'Patients',
          outputId: 'patients',
          order: 1,
          visible: true,
        },
      ],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: attachedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.getSelection.mockImplementation(
      () => new Promise(() => undefined),
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    expect(await screen.findByText('Loading the saved selection…')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Select second table' }));

    await waitFor(() =>
      expect(screen.queryByText('Loading the saved selection…')).not.toBeInTheDocument(),
    );
  });

  it('keeps a handed-off selection active when the current table has another collection attached', async () => {
    const attachedWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        population: { selectionRevisionId: 'selection-1', route: [] },
      }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: attachedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.searchPopulationRoutes.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      selectionRevisionId: 'selection-2',
      choices: [],
      truncated: false,
    });
    const handedOffSelection: SelectionRevision = {
      id: 'selection-2',
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-2',
      ruleDigest: 'rule-2',
      membershipDigest: 'members-2',
      memberCount: 1,
      memberBytes: 32,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    };

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
        populationSelection={handedOffSelection}
      />,
    );

    const panel = await screen.findByRole('region', { name: 'Starting collection' });
    expect(panel).toHaveAttribute('data-selection-revision-id', 'selection-2');
    expect(panel).toHaveAttribute('data-attached-selection-revision-id', 'selection-1');
    expect(mockLoomClient.getSelection).not.toHaveBeenCalled();
  });

  it('assesses and applies a row change without destructive reset', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Change rows' }));

    await waitFor(() => expect(assessRowChange).toHaveBeenCalledWith(expect.objectContaining({
      outputId: 'specimens',
      rootNodeId: 'patient-node',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
    })));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'APPLY_TABLE_ROOT_REBASE',
        rowChange: expect.objectContaining({
          outputId: 'specimens',
          preservedFeatureKeys: ['specimen_identifier'],
        }),
      }],
    })));
  });

  it('reassesses an ambiguous row change with the relationship chosen by the user', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const ready = assessRowChange().unwrap();
    assessRowChange
      .mockReturnValueOnce(resolvedRequest({
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'sha256:draft-1',
        status: 'BLOCKED',
        currentRootResourceType: 'Specimen',
        candidateRootResourceType: 'Patient',
        preservedFeatureKeys: ['specimen_identifier'],
        unresolved: [{
          kind: 'route',
          id: 'base',
          code: 'AMBIGUOUS_ROUTE_REBASE_EDGE',
          message: 'Choose the relationship to the previous rows.',
          alternatives: ['patient-specimen', 'patient-subject'],
        }],
        diagnostics: [],
      }))
      .mockReturnValueOnce({ unwrap: vi.fn(() => ready), abort: vi.fn() });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Change rows' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Use patient-specimen' }));

    await waitFor(() => expect(assessRowChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        rootNodeId: 'patient-node',
        routeRebase: [{ occurrenceId: 'base', edgeId: 'patient-specimen' }],
      }),
    ));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(
      expect.objectContaining({
        commands: [expect.objectContaining({
          type: 'APPLY_TABLE_ROOT_REBASE',
        })],
      }),
    ));
  });

  it('cancels a stale preview when a patient column changes', async () => {
    const pendingPreview = abortableRequest<never>();
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockImplementation(() => {
      const [isLoading, setLoading] = React.useState(false);
      const trigger = React.useCallback(() => {
        setLoading(true);
        void pendingPreview
          .unwrap()
          .then(
            () => setLoading(false),
            () => setLoading(false),
          );
        return pendingPreview;
      }, []);
      return [trigger, { isLoading }];
    });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    fireEvent.click(previewButton);
    await waitFor(() => expect(previewButton).toBeDisabled());

    fireEvent.click(screen.getByRole('button', { name: 'Save column change' }));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(pendingPreview.abort).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(previewButton).toBeEnabled());
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not resume stale preview recovery after a patient column changes', async () => {
    let resolveRefresh: (
      value: { readonly data: typeof builderState },
    ) => void = () => undefined;
    const refresh = new Promise<{ readonly data: typeof builderState }>(
      (resolve) => {
        resolveRefresh = resolve;
      },
    );
    const refetch = vi.fn(() => refresh);
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: builderState,
      isLoading: false,
      refetch,
    });
    preview.mockReturnValueOnce(
      rejectedRequest({
        code: 'STALE_CATALOG_SNAPSHOT',
        message: 'The catalog is stale.',
        retryable: false,
      }),
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    fireEvent.click(previewButton);
    await waitFor(() => expect(refetch).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole('button', { name: 'Save column change' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    resolveRefresh({ data: builderState });

    await waitFor(() => expect(previewButton).toBeEnabled());
    expect(reconcile).toHaveBeenCalledTimes(1);
    expect(preview).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    fireEvent.click(previewButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
  });

  it('keeps Preview disabled without a visible patient column', async () => {
    const hiddenWorkspace = {
      ...workspace,
      documents: [
        {
          ...workspace.documents[0],
          columns: [
            {
              ...column,
              table: { visible: false, order: 0 },
            },
          ],
        },
      ],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: hiddenWorkspace },
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

    expect(
      await screen.findByRole('button', { name: 'Preview' }),
    ).toBeDisabled();
  });

  it('keeps receipt recovery within the current preview generation', async () => {
    preview
      .mockReturnValueOnce(
        rejectedRequest({
          code: 'RECEIPT_RECOMPILE_REQUIRED',
          message: 'The receipt is stale.',
          retryable: false,
        }),
      )
      .mockReturnValueOnce(
        resolvedRequest({
          apiVersion,
          kind: 'ExplorerBuilderPreview',
          receiptId: 'receipt-1',
          outputId: 'specimens',
          columns: receipt.outputs[0].columns,
          rows: [],
          rowCount: 0,
          diagnostics: [],
        }),
      );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Preview' }));

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('publishes a server-persisted draft after the Builder is reloaded', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const publishButton = await screen.findByRole('button', {
      name: 'Publish',
    });
    expect(publishButton).toBeEnabled();

    fireEvent.click(publishButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
  });

  it('resets publish status when switching projects with the same Explorer ID', async () => {
    const view = render(
      <BuilderWorkspace project="project-a" explorerId="test" />,
    );
    const publishButton = await screen.findByRole('button', { name: 'Publish' });
    expect(publishButton).toBeEnabled();
    fireEvent.click(publishButton);
    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled());

    view.rerender(<BuilderWorkspace project="project-b" explorerId="test" />);

    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled());
  });

  it('shows publication progress until the publish response settles', async () => {
    const pending = deferredRequest<Record<string, never>>();
    publish.mockReturnValue(pending.request);
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Publish' }));

    const publishingButton = await screen.findByRole('button', {
      name: 'Publishing…',
    });
    expect(publishingButton).toBeDisabled();
    expect(publishingButton).toHaveAttribute('aria-busy', 'true');
    expect(publish).toHaveBeenCalledTimes(1);

    pending.resolve({});
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Publishing…' }),
      ).not.toBeInTheDocument(),
    );
  });

  it('saves commands without reconciling, then reconciles once before Publish', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(
      await screen.findByRole('button', { name: 'Save column change' }),
    );
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled(),
    );
    expect(reconcile).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
  });

  it('saves an edited traversal relationship in place', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Change relationship' }),
    );

    await waitFor(() =>
      expect(applyCommands).toHaveBeenCalledWith(
        expect.objectContaining({
        project: 'HTAN_INT/BForePC',
        explorerId: 'test',
        authResourcePath: '/programs/HTAN_INT/projects/BForePC',
        commands: [
          {
            type: 'UPDATE_ROUTE_EDGE',
            outputId: 'specimens',
            occurrenceId: 'patient-subject',
            edgeId: 'specimen-patient-participant',
          },
        ],
        }),
      ),
    );
  });

  it('previews after one click when reconciliation returns a normalized builder', async () => {
    reconcile.mockReturnValue(
      resolvedRequest({
        ...receipt,
        builder: {
          ...workspace,
          sharedFilters: {
            identifier: [
              { outputId: 'specimens', column: 'specimen_identifier' },
            ],
          },
        },
      }),
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    await waitFor(() => expect(previewButton).toBeEnabled());
    fireEvent.click(previewButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  });

  it('keeps Preview visible, collapses source setup, supports focusable column selection, and applies only the reviewed receipt command', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    expect(await screen.findByTestId('construction-workspace')).toBeInTheDocument();
    const preview = screen.getByTestId('construction-preview');
    expect(['empty', 'stale']).toContain(preview.getAttribute('data-preview-status'));
    expect(within(preview).getByText('Preview table')).toBeInTheDocument();
    expect(screen.getByTestId('construction-source-setup')).not.toHaveAttribute('open');
    expect(screen.getByTestId('construction-action-add-columns')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-keep-rows')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-calculate')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-reshape')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-combine')).toBeInTheDocument();

    const selectedColumn = screen.getByTestId('construction-column-specimen_identifier_id');
    expect(selectedColumn.tagName).toBe('BUTTON');
    selectedColumn.focus();
    expect(document.activeElement).toBe(selectedColumn);
    fireEvent.click(selectedColumn);
    expect(selectedColumn).toHaveAttribute('aria-pressed', 'true');
    const calculateShortcut = screen.getByTestId('construction-selection-calculate');
    expect(calculateShortcut.tagName).toBe('BUTTON');
    calculateShortcut.focus();
    expect(document.activeElement).toBe(calculateShortcut);
    fireEvent.click(calculateShortcut);
    expect(screen.getByTestId('construction-operation-editor')).toHaveAttribute(
      'data-operation-family',
      'CALCULATE',
    );
    expect(screen.getByTestId('construction-operation-editor').textContent).toContain(
      'Specimen identifier',
    );
    fireEvent.click(screen.getByTestId('construction-close-operation-editor'));

    const settings = await screen.findByTestId('ui04-table-shape-settings');

    fireEvent.click(screen.getByTestId('ui04-open-table-shape-settings'));
    await screen.findByTestId('ui04-reshape-mode');
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Derived column 1 output column' }), { target: { value: 'weight_plus_height' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Derived column 1 output label' }), { target: { value: 'Weight plus height' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Derived column 1 operation' }), { target: { value: 'operator-add' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Derived column 1 first operand' }), { target: { value: 'weight' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Derived column 1 second operand' }), { target: { value: 'height' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Derived column 1 missing-input policy' }), { target: { value: 'missing-propagate' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    await screen.findByTestId('ui04-table-shape-comparison');
    expect(applyCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('ui04-confirm-table-shape'));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{ type: 'APPLY_TABLE_SHAPE_PROPOSAL', outputId: 'specimens', proposalId: 'shape-proposal' }],
    })));
    expect(mockLoomClient.proposeTableShape).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'ADD', catalogId: 'shape-catalog', derivedResolutionIds: ['derived-resolution'],
    }));
  });

  it('restores the server-provided previous draft revision through the normal CAS command path', async () => {
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, previousDraftRevisionId: 'draft-revision-previous' },
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

    const undo = await screen.findByTestId('construction-undo');
    expect(undo).toBeEnabled();
    fireEvent.click(undo);

    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      expectedDraftDigest: 'sha256:draft-1',
      commands: [{
        type: 'RESTORE_DRAFT_REVISION',
        draftRevisionId: 'draft-revision-previous',
      }],
    })));
  });
});
