// @vitest-environment jsdom
import React from 'react';
import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { act, fireEvent, render as renderUI, screen, waitFor, within } from '@testing-library/react';
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
  LoomProvider,
} from '../../react';
import BuilderWorkspace from './BuilderWorkspace';
import type { SelectionRevision } from '../../selection';
import type {
  ConstructionCategoryDiscoveryResponse,
  Construction,
  ConstructionProposalResponse,
  ExplorerBuilderDocument,
  ExplorerBuilderWorkspace,
} from '../../types';
import type { ConstructionCandidateIntent } from './constructionWorkspace/useConstructionLifecycle';
import type { LoomClient } from '../../api';
import type {
  ProposeConstructionArgs,
  ProposeConstructionChoicesArgs,
} from '../../api';

const mockLoomClient = vi.hoisted(() => ({
  getSelection: vi.fn(),
  listRowDefinitionChoices: vi.fn(),
  searchPopulationRoutes: vi.fn(),
  createSelection: vi.fn(),
  configuredColumnContextsQuery: vi.fn(),
  getTableShapeCapabilities: vi.fn(),
  discoverTableShapeCategories: vi.fn(),
  resolveTableShape: vi.fn(),
  proposeTableShape: vi.fn(),
  getConstructionCapabilities: vi.fn(),
  discoverConstructionCategories: vi.fn(),
  proposeConstruction: vi.fn(),
  proposeConstructionChoices: vi.fn(),
  preview: vi.fn(),
  browseSemanticInventory: vi.fn(),
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
  };
});


const render = (
  ui: React.ReactNode,
  options?: Parameters<typeof renderUI>[1],
) => renderUI(ui, {
  ...options,
  wrapper: ({ children }) => (
    <LoomProvider client={mockLoomClient as unknown as LoomClient}>{children}</LoomProvider>
  ),
});

vi.mock('./components/BuilderToolbar', () => ({
  BuilderToolbar: ({
    onReview,
    onPublish,
    onSelectTable,
    tables,
    publishDisabled,
    publishing,
  }: {
    readonly onReview: () => void;
    readonly onPublish: () => void;
    readonly onSelectTable: (outputId: string) => void;
    readonly tables: ReadonlyArray<{ readonly outputId: string }>;
    readonly publishDisabled: boolean;
    readonly publishing: boolean;
  }) => (
    <div>
      <button type="button" onClick={onReview}>
        Review dataset
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
              projectionMode: 'FIRST' as const,
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
  formatPreviewCell: (value: unknown) => String(value ?? ''),
  previewCellTitle: (value: unknown) => String(value ?? ''),
  sortByPresentationOrder: <T,>(values: ReadonlyArray<T>, orderOf: (value: T, index: number) => number) => values
    .map((value, index) => ({ value, index, order: orderOf(value, index) }))
    .sort((left, right) => left.order - right.order || left.index - right.index)
    .map(({ value }) => value),
}));

vi.mock('./constructionOperations/ConstructionReshapeEditor', () => ({
  ConstructionReshapeEditor: ({
    editingStep,
    construction,
    capabilities,
    pivotDiscovery,
    onDiscoverCategories,
    onCandidateChange,
  }: {
    readonly editingStep?: { readonly id: string; readonly operation: { readonly kind: string } };
    readonly construction: { readonly steps: ReadonlyArray<unknown> };
    readonly capabilities: { readonly selectedStage: { readonly id: string } };
    readonly pivotDiscovery?: { readonly status: string };
    readonly onDiscoverCategories?: (request: { readonly stageId: string; readonly categoryColumnId: string; readonly valueColumnId: string }) => void;
    readonly onCandidateChange?: (intent: ConstructionCandidateIntent | undefined) => void;
  }) => (
    <div
      data-testid="construction-reshape-editor"
      data-editing-step-id={editingStep?.id ?? ''}
      data-editing-operation={editingStep?.operation.kind ?? ''}
      data-construction-step-count={construction.steps.length}
      data-discovery-status={pivotDiscovery?.status ?? 'none'}
    >
      {onDiscoverCategories ? <button type="button" onClick={() => onDiscoverCategories({
        stageId: capabilities.selectedStage.id,
        categoryColumnId: 'status-id',
        valueColumnId: 'value-id',
      })}>Find category values in construction editor</button> : null}
      {onCandidateChange ? <button type="button" onClick={() => onCandidateChange({
        changedStepId: 'pivot-proposal-step',
        candidateConstruction: {
          version: 1,
          steps: [{
            id: 'pivot-proposal-step',
            inputs: [{ kind: 'SOURCE_PROJECTION' }],
            operation: { kind: 'PIVOT' },
            outputs: [
              { id: 'd-output', name: 'd', label: 'Observed quantity d', type: 'integer', table: { order: 0 } },
              { id: 'specimen-output', name: 'specimen_id', label: 'Specimen ID', type: 'string', table: { order: 1 } },
              { id: 'patient-output', name: 'patient_id', label: 'Patient FHIR ID', type: 'string', table: { order: 2 } },
              { id: 'observation-output', name: 'observation_id', label: 'Observation FHIR ID', type: 'string', table: { order: 3 } },
              { id: 'value-output', name: 'related_value', label: 'Observation value', type: 'string', table: { order: 4 } },
              { id: 'null-output', name: 'null_value', label: 'Null', type: 'string', table: { order: 5 } },
            ],
          }],
        } as unknown as Construction,
      })}>Emit Pivot presentation proposal</button> : null}
    </div>
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
      projectionMode: 'FIRST' as const,
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
  const abort = vi.fn(() => reject(Object.assign(new Error('CLIENT_CANCELLED'), {
    code: 'CLIENT_CANCELLED',
  })));
  return { unwrap: vi.fn(() => promise), abort };
};

describe('BuilderWorkspace on-demand reconciliation', () => {
  let applyCommands: Mock;
  let assessRowChange: Mock;
  let reconcile: Mock;
  let preview: Mock;
  let publish: Mock;

  beforeEach(() => {
    mockLoomClient.getSelection.mockReset();
    mockLoomClient.listRowDefinitionChoices.mockReset();
    mockLoomClient.listRowDefinitionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      choices: [],
      explicitGroups: [],
    });
    mockLoomClient.proposeConstructionChoices.mockReset();
    mockLoomClient.proposeConstructionChoices.mockImplementation(async (args: ProposeConstructionChoicesArgs) => ({
      commandId: args.commandId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      constructionChoices: args.constructionChoices,
      candidateColumnIds: ['field-column-a', 'semantic-column-a'],
      candidateWorkspaceDigest: 'sha256:candidate-choice-1',
      previewStatus: 'READY' as const,
      previewDurationMs: 4,
      preview: {
        apiVersion,
        kind: 'ExplorerBuilderPreview' as const,
        rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
        receiptId: 'choice-preview-1',
        outputId: args.outputId,
        columns: [
          { column: 'field-column-a', label: 'Field A', logicalType: 'string', filterable: false, chartable: false },
          { column: 'semantic-column-a', label: 'Feature A', logicalType: 'string', filterable: false, chartable: false },
        ],
        rows: [{ 'field-column-a': 'field value', 'semantic-column-a': 'feature value' }],
        rowCount: 1,
        diagnostics: [],
      },
    }));
    mockLoomClient.preview.mockReset();
    mockLoomClient.preview.mockResolvedValue({
      apiVersion,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: 'receipt-1',
      outputId: 'specimens',
      columns: receipt.outputs[0].columns,
      rows: [],
      rowCount: 0,
      diagnostics: [],
    });
    mockLoomClient.browseSemanticInventory.mockResolvedValue({ state: 'complete', entries: [] });
    mockLoomClient.configuredColumnContextsQuery.mockReset().mockReturnValue({
      data: undefined,
      error: undefined,
      isLoading: true,
      isFetching: true,
      refetch: vi.fn(),
    });
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
    mockLoomClient.discoverConstructionCategories.mockImplementation(async (args: {
      readonly snapshotToken: string;
      readonly expectedDraftVersion: number;
      readonly expectedDraftDigest: string;
      readonly outputId: string;
      readonly stageId: string;
      readonly categoryColumnId: string;
      readonly valueColumnId: string;
    }): Promise<ConstructionCategoryDiscoveryResponse> => ({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: args.stageId,
      categoryColumnId: args.categoryColumnId,
      valueColumnId: args.valueColumnId,
      outcome: 'COMPLETE' as const,
      complete: true,
      proofFingerprint: 'proof-1',
      categories: [{ key: { kind: 'STRING' as const, string: 'final' }, label: 'final' }],
    }));
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
      candidateReceiptId: 'receipt-1',
      unresolved: [],
      diagnostics: [],
    }));
    reconcile = vi.fn().mockReturnValue(resolvedRequest(receipt));
    preview = vi.fn().mockReturnValue(
      resolvedRequest({
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
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

    fireEvent.change(screen.getByLabelText('Table name (optional)'), {
      target: { value: 'Patient features' },
    });
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

    await screen.findByRole('button', { name: 'Apply columns' });
    expect(mockLoomClient.proposeConstructionChoices).toHaveBeenCalledWith(expect.objectContaining({
      outputId: 'specimens',
      constructionChoices: [
        { choiceId: 'field-choice-a', form: 'VALUE', title: 'Field A' },
        { choiceId: 'semantic-choice-a', form: 'ALL', title: 'Feature A' },
      ],
    }), expect.any(AbortSignal));
    expect(applyCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply columns' }));

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
    fireEvent.click(await screen.findByRole('button', { name: 'Advanced graph' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Change relationship' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{
        type: 'UPDATE_ROUTE_EDGE',
        outputId: 'specimens',
        occurrenceId: 'patient-subject',
        edgeId: 'specimen-patient-participant',
      }],
    }));

    expect(await screen.findByText(
      'Loom is finishing the previous table update. Field selection will return when the draft refresh completes.',
    )).toHaveAttribute('role', 'status');
    expect(screen.getByRole('button', { name: 'Add catalog fixture' })).toBeDisabled();

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

    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    fireEvent.click(within(rowSettings).getByTestId('construction-row-edit-group_specimens'));

    await waitFor(() => {
      expect(screen.getByTestId('construction-reshape-editor')).toHaveAttribute('data-editing-step-id', 'group_specimens');
      expect(screen.getByTestId('construction-reshape-editor')).toHaveAttribute('data-editing-operation', 'GROUP');
    });
    expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalledWith(
      expect.objectContaining({ outputId: 'specimens', stageId: 'source_projection' }),
      expect.any(AbortSignal),
    );
  });

  it('opens stage-scoped pivot discovery from a source-only Reshape editor', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-id',
      columns: [
        { id: 'specimen-id', name: 'specimen_identifier', label: 'Specimen identifier', type: 'string' },
        { id: 'status-id', name: 'status', label: 'Status', type: 'string' },
        { id: 'value-id', name: 'value', label: 'Value', type: 'decimal' },
      ],
      capabilities: [
        { kind: 'PIVOT' as const, supported: true },
        { kind: 'FILTER' as const, supported: true },
      ],
    };
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
      baseConstruction: { version: 1, steps: [] },
      stages: [sourceStage],
      selectedStage: sourceStage,
    }));

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    fireEvent.click(within(rowSettings).getByTestId('construction-action-pivot-rows'));

    const editor = await screen.findByTestId('construction-reshape-editor');
    expect(editor).toHaveAttribute('data-construction-step-count', '0');
    expect(screen.queryByTestId('ui04-table-shape-settings')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Find category values in construction editor' }));
    await waitFor(() => expect(mockLoomClient.discoverConstructionCategories).toHaveBeenCalledWith(
      expect.objectContaining({
        outputId: 'specimens',
        stageId: 'source_projection',
        categoryColumnId: 'status-id',
        valueColumnId: 'value-id',
      }),
      expect.any(AbortSignal),
    ));
    await waitFor(() => expect(screen.getByTestId('construction-reshape-editor')).toHaveAttribute('data-discovery-status', 'complete'));
  });

  it('uses candidate Pivot output presentation when rendering the construction proposal preview', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-id',
      columns: [
        { id: 'specimen-id', name: 'specimen_id', label: 'Specimen ID', type: 'string' },
        { id: 'patient-id', name: 'patient_id', label: 'Patient FHIR ID', type: 'string' },
        { id: 'observation-id', name: 'observation_id', label: 'Observation FHIR ID', type: 'string' },
        { id: 'value-id', name: 'related_value', label: 'Observation value', type: 'string' },
      ],
      capabilities: [
        { kind: 'PIVOT' as const, supported: true },
        { kind: 'FILTER' as const, supported: true },
      ],
    };
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
      baseConstruction: { version: 1, steps: [] },
      stages: [sourceStage],
      selectedStage: sourceStage,
    }));
    mockLoomClient.proposeConstruction.mockImplementation(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
      proposalId: 'pivot-presentation-proposal',
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-workspace-1',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage],
      previewStatus: 'READY',
      previewDurationMs: 3,
    }));
    mockLoomClient.preview.mockResolvedValue({
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: 'pivot-presentation-proposal',
      outputId: 'specimens',
      columns: [
        { column: 'specimen_id', label: 'Specimen ID', logicalType: 'string', filterable: true, chartable: false },
        { column: 'patient_id', label: 'Patient FHIR ID', logicalType: 'string', filterable: true, chartable: false },
        { column: 'observation_id', label: 'Observation FHIR ID', logicalType: 'string', filterable: true, chartable: false },
        { column: 'related_value', label: 'Observation value', logicalType: 'string', filterable: true, chartable: false },
        { column: 'null_value', label: 'Null', logicalType: 'string', filterable: true, chartable: false },
        { column: 'd', label: 'd', logicalType: 'integer', filterable: true, chartable: false },
      ],
      rows: [{
        specimen_id: 'specimen-1',
        patient_id: 'patient-1',
        observation_id: 'observation-1',
        related_value: 'value-1',
        null_value: null,
        d: 7,
      }],
      rowCount: 1,
      diagnostics: [],
    });

    const view = render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    fireEvent.click(within(rowSettings).getByTestId('construction-action-pivot-rows'));
    fireEvent.click(await screen.findByRole('button', { name: 'Emit Pivot presentation proposal' }));

    const proposalPreview = await screen.findByTestId('construction-proposal-preview');
    expect(within(proposalPreview).getAllByRole('columnheader').map((header) => header.firstElementChild?.textContent)).toEqual([
      'Observed quantity d',
      'Specimen ID',
      'Patient FHIR ID',
      'Observation FHIR ID',
      'Observation value',
      'Null',
    ]);
    expect(within(proposalPreview).getAllByRole('cell').map((cell) => cell.textContent)).toEqual([
      '7',
      'specimen-1',
      'patient-1',
      'observation-1',
      'value-1',
      '',
    ]);
    view.unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));
    mockLoomClient.proposeConstruction.mockClear();
  });

  it('keeps a gender filter proposal alive across BuilderWorkspace rerenders', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-id',
      columns: [
        { id: 'specimen-id', name: 'specimen_identifier', label: 'Specimen identifier', type: 'string' },
        { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string' },
      ],
      capabilities: [
        { kind: 'FILTER' as const, supported: true },
        { kind: 'DERIVE' as const, supported: true },
        { kind: 'PIVOT' as const, supported: false, reason: 'Not needed by this test.' },
        { kind: 'UNPIVOT' as const, supported: false, reason: 'Not needed by this test.' },
      ],
    };
    mockLoomClient.getConstructionCapabilities.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
      outputId: 'specimens',
      stageId: 'source_projection',
      baseConstruction: { version: 1, steps: [] },
      stages: [sourceStage],
      selectedStage: sourceStage,
    });
    mockLoomClient.proposeConstruction.mockImplementation(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
      proposalId: 'gender-filter-proposal',
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'candidate-workspace-2',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage],
      previewStatus: 'READY',
      previewDurationMs: 7,
    }));
    mockLoomClient.preview.mockResolvedValue({
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: 'gender-filter-proposal',
      outputId: 'specimens',
      columns: [{ column: 'specimen_identifier', label: 'Specimen identifier', logicalType: 'string', filterable: true, chartable: false }],
      rows: [{ specimen_identifier: 'specimen-1' }],
      rowCount: 1,
      diagnostics: [],
    });

    const view = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByTestId('construction-action-keep-rows'));
    await screen.findByRole('combobox', { name: 'Column' });
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'gender-id' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });

    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    expect(mockLoomClient.proposeConstruction).toHaveBeenCalledWith(expect.objectContaining({
      outputId: 'specimens',
      expectedDraftVersion: 1,
      expectedDraftDigest: 'sha256:draft-1',
      candidateConstruction: {
        version: 1,
        steps: [expect.objectContaining({
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'FILTER',
            filter: {
              columnId: 'gender-id',
              operator: 'EQUALS',
              values: [{ kind: 'STRING', string: 'female' }],
            },
          },
        })],
      },
    }), expect.any(AbortSignal));
    expect(await screen.findByTestId('construction-proposal-ready')).toBeTruthy();
    expect(screen.getByTestId('construction-apply-proposal').hasAttribute('disabled')).toBe(false);
    view.unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  it('reconciles a new draft version when its content digest stays the same', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-id',
      columns: [
        { id: 'specimen-id', name: 'specimen_identifier', label: 'Specimen identifier', type: 'string' },
        { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string' },
      ],
      capabilities: [
        { kind: 'FILTER' as const, supported: true },
        { kind: 'DERIVE' as const, supported: true },
        { kind: 'PIVOT' as const, supported: false, reason: 'Not needed by this test.' },
        { kind: 'UNPIVOT' as const, supported: false, reason: 'Not needed by this test.' },
      ],
    };
    let savedConstruction: ProposeConstructionArgs['candidateConstruction'] | undefined;
    let latestCandidate: ProposeConstructionArgs['candidateConstruction'] | undefined;
    const initialDocument: ExplorerBuilderDocument = workspace.documents[0]!;
    let savedWorkspace: ExplorerBuilderWorkspace = { ...workspace, documents: [initialDocument] };
    let nextDraftVersion = 1;
    let proposalNumber = 0;
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
      baseConstruction: savedConstruction ?? { version: 1, steps: [] },
      stages: [sourceStage],
      selectedStage: sourceStage,
    }));
    mockLoomClient.proposeConstruction.mockImplementation(async (
      args: ProposeConstructionArgs,
    ): Promise<ConstructionProposalResponse> => {
      latestCandidate = args.candidateConstruction;
      proposalNumber += 1;
      return {
        proposalId: `stable-content-proposal-${proposalNumber}`,
        outputId: args.outputId,
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        baseDocumentDigest: 'document-1',
        candidateWorkspaceDigest: 'sha256:stable-content-candidate',
        changedStepId: args.changedStepId ?? '',
        candidateConstruction: args.candidateConstruction,
        dependencyImpact: { affectedStepIds: [] },
        stages: [sourceStage],
        previewStatus: 'READY',
        previewDurationMs: 1,
      };
    });
    mockLoomClient.preview.mockImplementation(async (args: {
      readonly outputId: string;
      readonly receiptId: string;
    }) => ({
      apiVersion,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: args.receiptId,
      outputId: args.outputId,
      columns: receipt.outputs[0].columns,
      rows: [{ specimen_identifier: `candidate-${args.receiptId}` }],
      rowCount: 1,
      diagnostics: [],
    }));
    applyCommands.mockImplementation((args: { readonly commandId: string }) => {
      if (!latestCandidate) throw new Error('Expected a construction proposal before Apply.');
      nextDraftVersion += 1;
      savedConstruction = latestCandidate;
      const savedDocument: ExplorerBuilderDocument = {
        ...initialDocument,
        construction: latestCandidate,
      };
      savedWorkspace = { ...workspace, documents: [savedDocument] };
      return resolvedRequest({
        commandId: args.commandId,
        workspace: savedWorkspace,
        draftVersion: nextDraftVersion,
        // Reapplying the same construction creates a new saved revision with the same content digest.
        draftDigest: 'sha256:draft-2',
        results: [{ type: 'TABLE_CHANGED', outputId: 'specimens', column: 'specimen_identifier' }],
        diagnostics: [],
      });
    });
    reconcile.mockImplementation((args: {
      readonly snapshotToken: string;
      readonly draftVersion: number;
      readonly draftDigest: string;
    }) => resolvedRequest({
      ...receipt,
      receiptId: `receipt-v${args.draftVersion}`,
      snapshotToken: args.snapshotToken,
      builder: savedWorkspace,
    }));
    preview.mockImplementation((args: { readonly outputId: string; readonly receiptId: string }) =>
      resolvedRequest({
        apiVersion,
        kind: 'ExplorerBuilderPreview' as const,
        rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
        receiptId: args.receiptId,
        outputId: args.outputId,
        columns: receipt.outputs[0].columns,
        rows: [{ specimen_identifier: `preview-${args.receiptId}` }],
        rowCount: 1,
        diagnostics: [],
      }),
    );

    const view = render(
      <BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />,
    );
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-preview-receipt-id', 'receipt-v1'));

    fireEvent.click(await screen.findByTestId('construction-action-keep-rows'));
    await screen.findByRole('combobox', { name: 'Column' });
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'gender-id' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Condition' }), { target: { value: 'EQUALS' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });
    await screen.findByTestId('construction-proposal-ready');
    await waitFor(() => expect(latestCandidate?.steps).toHaveLength(1));
    const firstCandidate = latestCandidate;
    const firstStepId = firstCandidate?.steps[0]?.id;
    expect(firstStepId).toBeTruthy();
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(reconcile.mock.calls[1]?.[0]).toMatchObject({
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
    });
    expect(preview.mock.calls[1]?.[0]).toMatchObject({ outputId: 'specimens', receiptId: 'receipt-v2' });
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-preview-receipt-id', 'receipt-v2'));

    fireEvent.click(screen.getByTestId(`construction-history-step-${firstStepId}`));
    fireEvent.click(await screen.findByTestId(`construction-edit-step-${firstStepId}`));
    const condition = await screen.findByRole('combobox', { name: 'Condition' });
    const proposalCountBeforeEdit = mockLoomClient.proposeConstruction.mock.calls.length;
    fireEvent.change(condition, { target: { value: 'MISSING' } });
    fireEvent.change(await screen.findByRole('combobox', { name: 'Condition' }), { target: { value: 'EQUALS' } });
    fireEvent.change(await screen.findByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });
    await waitFor(() => expect(mockLoomClient.proposeConstruction.mock.calls.length).toBeGreaterThan(proposalCountBeforeEdit));
    await screen.findByTestId('construction-proposal-ready');
    expect(latestCandidate).toEqual(firstCandidate);
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(3));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(3));
    expect(reconcile.mock.calls[2]?.[0]).toMatchObject({
      draftVersion: 3,
      draftDigest: 'sha256:draft-2',
    });
    expect(preview.mock.calls[2]?.[0]).toMatchObject({ outputId: 'specimens', receiptId: 'receipt-v3' });
    await waitFor(() => {
      const previewPanel = screen.getByTestId('construction-preview');
      expect(previewPanel).toHaveAttribute('data-preview-receipt-id', 'receipt-v3');
      expect(previewPanel).toHaveAttribute('data-current-draft-version', '3');
    });

    view.rerender(
      <BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />,
    );
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 300));
    });
    expect(reconcile).toHaveBeenCalledTimes(3);
    expect(preview).toHaveBeenCalledTimes(3);
  });

  it('automatically previews a hydrated draft and opens Review with its compile receipt and sample', async () => {
    preview.mockReturnValueOnce(
      resolvedRequest({
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
        receiptId: 'receipt-1',
        outputId: 'specimens',
        columns: receipt.outputs[0].columns,
        rows: [{ specimen_identifier: 'SP-1' }],
        rowCount: 1,
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
    expect(screen.queryByRole('button', { name: 'Preview' })).not.toBeInTheDocument();

    const reviewButton = await screen.findByRole('button', {
      name: 'Review dataset',
    });
    await waitFor(() => expect(reviewButton).toBeEnabled());
    await waitFor(() => expect(mockLoomClient.configuredColumnContextsQuery.mock.calls.some(([args]) => args !== undefined)).toBe(true));
    const configuredContextCall = mockLoomClient.configuredColumnContextsQuery.mock.calls.find((call) => call[0] !== undefined);
    expect(configuredContextCall?.[0]).toEqual({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      expectedDraftDigest: 'sha256:draft-1',
    });
    expect(configuredContextCall?.[1]).toBe(JSON.stringify([
      JSON.stringify(['HTAN_INT/BForePC', '/programs/HTAN_INT/projects/BForePC', 'test']),
      'snapshot-1',
      1,
      'sha256:draft-1',
      0,
    ]));
    expect(configuredContextCall).toBeDefined();
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    fireEvent.click(reviewButton);
    expect(await screen.findByText('Compiler result matches the saved draft.')).toBeTruthy();
    expect(await screen.findByText(/Preview returned 1 sample rows/)).toBeTruthy();

    fireEvent.click(reviewButton);
    await waitFor(() => expect(screen.queryByText('Compiler result matches the saved draft.')).toBeNull());
    fireEvent.click(reviewButton);
    expect(await screen.findByText('Compiler result matches the saved draft.')).toBeTruthy();
    expect(reconcile).toHaveBeenCalledTimes(1);
    expect(preview).toHaveBeenCalledTimes(1);
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
    let loadSignal: AbortSignal | undefined;
    let resolveSelection: (value: { readonly revision: SelectionRevision; readonly members: ReadonlyArray<never> }) => void = () => undefined;
    const staleSelection: SelectionRevision = {
      id: 'selection-1',
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-1',
      ruleDigest: 'rule-1',
      membershipDigest: 'membership-1',
      memberCount: 1,
      memberBytes: 32,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    };
    mockLoomClient.getSelection.mockImplementation((_args: unknown, signal: AbortSignal) => {
      loadSignal = signal;
      return new Promise((resolve) => {
        resolveSelection = resolve;
      });
    });

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(await within(rowSettings).findByText('Loading the saved selection…')).toBeInTheDocument();
    fireEvent.click(within(rowSettings).getByRole('button', { name: 'Back to table' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Row definition settings' })).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Select second table' }));
    await waitFor(() => expect(loadSignal?.aborted).toBe(true));
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const patientSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(await within(patientSettings).findByText('All authorized Patient records')).toBeInTheDocument();
    expect(within(patientSettings).queryByText('Loading the saved selection…')).not.toBeInTheDocument();

    await act(async () => {
      resolveSelection({ revision: staleSelection, members: [] });
      await Promise.resolve();
    });
    expect(within(patientSettings).getByText('All authorized Patient records')).toBeInTheDocument();
    expect(within(patientSettings).queryByText('1 Specimen selected, not attached')).not.toBeInTheDocument();
  });

  it('restores the source selection from a saved cohort after the starting collection is cleared', async () => {
    const cohortWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        rows: {
          kind: 'GROUPS' as const,
          groups: {
            source: {
              kind: 'EXPLICIT' as const,
              explicit: { revisionId: 'group-revision-1', unassignedMemberPolicy: 'EXCLUDE' as const },
            },
          },
        },
      }],
    };
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: cohortWorkspace }));
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: cohortWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    const sourceSelection: SelectionRevision = {
      id: 'selection-cohort-source',
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-cohort-source',
      ruleDigest: 'rule-cohort-source',
      membershipDigest: 'membership-cohort-source',
      memberCount: 2,
      memberBytes: 64,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    };
    mockLoomClient.listRowDefinitionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      choices: [],
      explicitGroups: [{
        revisionId: 'group-revision-1',
        sourceSelectionRevisionId: sourceSelection.id,
        groupCount: 2,
        memberCount: 2,
        createdAt: '2026-09-21T00:00:00Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
      }],
    });
    mockLoomClient.getSelection.mockResolvedValue({ revision: sourceSelection, members: [] });
    mockLoomClient.searchPopulationRoutes.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      selectionRevisionId: sourceSelection.id,
      complete: true,
      truncated: false,
      choices: [],
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);

    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(await within(dialog).findByText('2 Specimen selected, not attached')).toBeInTheDocument();
    await waitFor(() => expect(mockLoomClient.listRowDefinitionChoices).toHaveBeenCalledTimes(2));
    expect(mockLoomClient.listRowDefinitionChoices).toHaveBeenCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
    }, expect.any(AbortSignal));
    expect(mockLoomClient.getSelection).toHaveBeenCalledWith({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      selectionRevision: sourceSelection.id,
      limit: 1,
    }, expect.any(AbortSignal));
  });

  it('reports missing saved-cohort selection metadata without retaining another selection', async () => {
    const cohortWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        rows: {
          kind: 'GROUPS' as const,
          groups: {
            source: {
              kind: 'EXPLICIT' as const,
              explicit: { revisionId: 'group-revision-1', unassignedMemberPolicy: 'EXCLUDE' as const },
            },
          },
        },
      }],
    };
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: cohortWorkspace }));
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: cohortWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    mockLoomClient.listRowDefinitionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1', outputId: 'specimens', choices: [],
      explicitGroups: [{
        revisionId: 'group-revision-1', groupCount: 2, memberCount: 2,
        createdAt: '2026-09-21T00:00:00Z',
        unassignedMemberPolicies: ['ERROR', 'EXCLUDE', 'GROUP_AS_UNASSIGNED'],
      }],
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);

    fireEvent.click(screen.getByRole('button', { name: 'Configure rows' }));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    expect(await within(dialog).findByText('The saved cohort does not expose its source selection in this authorized catalog snapshot.')).toBeInTheDocument();
    expect(mockLoomClient.getSelection).not.toHaveBeenCalled();
    expect(screen.queryByText(/selected, not attached/)).not.toBeInTheDocument();
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

    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const panel = await within(rowSettings).findByRole('region', { name: 'Starting collection' });
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
    await screen.findByRole('button', { name: 'Apply row change' });
    expect(applyCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply row change' }));
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
    fireEvent.click(await screen.findByRole('button', { name: 'Match through patient-specimen' }));

    await waitFor(() => expect(assessRowChange).toHaveBeenLastCalledWith(
      expect.objectContaining({
        rootNodeId: 'patient-node',
        routeRebase: [{ occurrenceId: 'base', edgeId: 'patient-specimen' }],
      }),
    ));
    await screen.findByRole('button', { name: 'Apply row change' });
    expect(applyCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply row change' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(
      expect.objectContaining({
        commands: [expect.objectContaining({
          type: 'APPLY_TABLE_ROOT_REBASE',
        })],
      }),
    ));
  });

  it('cancels an automatic preview when a patient column changes', async () => {
    const pendingPreview = abortableRequest<never>();
    const automaticPreview = vi.fn();
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockImplementation(() => {
      const [isLoading, setLoading] = React.useState(false);
      const trigger = React.useCallback(() => {
        automaticPreview();
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

    await waitFor(() => expect(automaticPreview).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole('button', { name: 'Save column change' }));

    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(pendingPreview.abort).toHaveBeenCalled());
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

    await waitFor(() => expect(refetch).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole('button', { name: 'Save column change' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    resolveRefresh({ data: builderState });

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not automatically preview without a visible patient column', async () => {
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

    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 300));
    });
    expect(reconcile).not.toHaveBeenCalled();
    expect(preview).not.toHaveBeenCalled();
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
          rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
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

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('preserves the selected table when automatic preview refreshes builder state', async () => {
    const patientColumn = {
      ...column,
      column: 'patient_identifier',
      label: 'Patient identifier',
    };
    const patientDocument = {
      ...workspace.documents[0],
      output: { id: 'patients', title: 'Patients' },
      rootResourceType: 'Patient',
      route: { occurrenceId: 'base', resourceType: 'Patient' },
      columns: [patientColumn],
    };
    const multiTableWorkspace = {
      ...workspace,
      documents: [workspace.documents[0], patientDocument],
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
    const refreshedState = { ...builderState, workspace: multiTableWorkspace };
    const refetch = vi.fn().mockResolvedValue({ data: refreshedState });
    const patientPreviewColumns = [{
      column: 'patient_identifier',
      label: 'Patient identifier',
      logicalType: 'string',
      filterable: true,
      chartable: false,
    }];
    const patientPreview = {
      apiVersion,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: 'receipt-1',
      outputId: 'patients',
      columns: patientPreviewColumns,
      rows: [],
      rowCount: 0,
      diagnostics: [],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: refreshedState,
      isLoading: false,
      refetch,
    });
    reconcile.mockReturnValue(resolvedRequest({
      ...receipt,
      builder: multiTableWorkspace,
      outputs: [
        ...receipt.outputs,
        { outputId: 'patients', columns: patientPreviewColumns },
      ],
    }));

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByTestId('construction-preview')).toHaveAttribute(
        'data-preview-status',
        'ready',
      ),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Select second table' }));
    await waitFor(() =>
      expect(screen.getByTestId('construction-table-patients')).toHaveAttribute(
        'aria-current',
        'page',
      ),
    );
    preview
      .mockReturnValueOnce(
        rejectedRequest({
          code: 'STALE_CATALOG_SNAPSHOT',
          message: 'The catalog is stale.',
          retryable: false,
        }),
      )
      .mockReturnValueOnce(resolvedRequest(patientPreview));

    await waitFor(() => expect(refetch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(3));
    expect(preview).toHaveBeenNthCalledWith(2, expect.objectContaining({
      outputId: 'patients',
    }));
    expect(preview).toHaveBeenNthCalledWith(3, expect.objectContaining({
      outputId: 'patients',
    }));
    await waitFor(() =>
      {
        const previewPanel = screen.getByTestId('construction-preview');
        expect(previewPanel).toHaveAttribute('data-preview-output-id', 'patients');
        expect(previewPanel).toHaveAttribute('data-preview-status', 'ready');
      },
    );
    expect(screen.getByTestId('construction-table-patients')).toHaveAttribute(
      'aria-current',
      'page',
    );
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

  it('automatically previews saved commands and publishes the resulting receipt', async () => {
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
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled(),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
    expect(reconcile).toHaveBeenCalledTimes(1);
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

  it('automatically previews when reconciliation returns a normalized builder', async () => {
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

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  });

  it('keeps the preview visible, collapses source setup, supports focusable column selection, and applies only the reviewed receipt command', async () => {
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
    expect(screen.getByTestId('construction-action-keep-rows')).toHaveTextContent('Filter rows');
    expect(screen.getByTestId('construction-rows-settings-trigger')).toBeInTheDocument();
    expect(screen.queryByTestId('construction-action-calculate')).not.toBeInTheDocument();
    expect(screen.queryByTestId('construction-action-combine')).not.toBeInTheDocument();

    const selectedColumn = screen.getByTestId('construction-column-specimen_identifier_id');
    expect(selectedColumn.tagName).toBe('BUTTON');
    selectedColumn.focus();
    expect(document.activeElement).toBe(selectedColumn);
    fireEvent.click(selectedColumn);
    expect(selectedColumn).toHaveAttribute('aria-pressed', 'true');
    expect(screen.queryByTestId('construction-selection-calculate')).not.toBeInTheDocument();
    const keepRowsShortcut = screen.getByTestId('construction-selection-keep-rows');
    expect(keepRowsShortcut).toHaveTextContent('Filter rows');
    expect(keepRowsShortcut.tagName).toBe('BUTTON');
    keepRowsShortcut.focus();
    expect(document.activeElement).toBe(keepRowsShortcut);
    fireEvent.click(keepRowsShortcut);
    expect(screen.getByTestId('construction-operation-editor')).toHaveAttribute(
      'data-operation-family',
      'KEEP_ROWS',
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
