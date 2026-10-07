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
  ExplorerBuilderCompileResult,
  ExplorerBuilderDocument,
  ExplorerBuilderWorkspace,
} from '../../types';
import type { RelatedExpandQueryOwner } from './constructionOperations/RelatedExpandEditor';
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
  getConstructionInputs: vi.fn(),
  discoverConstructionCategories: vi.fn(),
  proposeConstruction: vi.fn(),
  proposeConstructionChoices: vi.fn(),
  preview: vi.fn(),
  browseSemanticInventory: vi.fn(),
}));
const mockLoomClientReference = vi.hoisted(() => ({ current: undefined as unknown }));
const mockRelatedExpandOwnerState = vi.hoisted(() => ({ enabled: false }));
const mockRelatedExpandQueryOwner = vi.hoisted(() => ({
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  pauseAndDrain: vi.fn<RelatedExpandQueryOwner['pauseAndDrain']>(async () => undefined),
  resume: vi.fn(),
}));

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  return {
    ...actual,
    useLoomClient: () => (mockLoomClientReference.current as typeof mockLoomClient | undefined) ?? mockLoomClient,
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
    onDuplicateTable,
    onDeleteTable,
    tables,
    publishDisabled,
    publishing,
  }: {
    readonly onReview: () => void;
    readonly onPublish: () => void;
    readonly onSelectTable: (outputId: string) => void;
    readonly onDuplicateTable: () => void;
    readonly onDeleteTable: () => void;
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
      {tables.length > 1 ? (
        <>
          <button type="button" onClick={onDuplicateTable}>Duplicate selected table</button>
          <button type="button" onClick={onDeleteTable}>Delete selected table</button>
        </>
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

vi.mock('./constructionOperations/ConstructionReshapeEditor', async (importOriginal) => ({
  ...await importOriginal<typeof import('./constructionOperations/ConstructionReshapeEditor')>(),
  ConstructionReshapeEditor: ({
    editingStep,
    initialKind,
    construction,
    capabilities,
    initialEntry,
    pivotDiscovery,
    onDiscoverCategories,
    onCandidateChange,
    relatedExpandQueryOwnerRef,
  }: {
    readonly editingStep?: { readonly id: string; readonly operation: { readonly kind: string } };
    readonly initialKind?: string;
    readonly construction: { readonly steps: ReadonlyArray<unknown> };
    readonly capabilities: { readonly selectedStage: { readonly id: string } };
    readonly initialEntry?: {
      readonly form: { readonly kind: string; readonly stepId?: string };
      readonly candidateIntent: ConstructionCandidateIntent;
    };
    readonly pivotDiscovery?: { readonly status: string };
    readonly onDiscoverCategories?: (request: { readonly stageId: string; readonly categoryColumnId: string; readonly valueColumnId: string }) => void;
    readonly onCandidateChange?: (intent: ConstructionCandidateIntent | undefined) => void;
    readonly relatedExpandQueryOwnerRef?: React.RefObject<RelatedExpandQueryOwner | null>;
  }) => {
    React.useImperativeHandle<RelatedExpandQueryOwner | null, RelatedExpandQueryOwner | null>(
      relatedExpandQueryOwnerRef ?? null,
      () => mockRelatedExpandOwnerState.enabled ? mockRelatedExpandQueryOwner : null,
      [relatedExpandQueryOwnerRef, mockRelatedExpandOwnerState.enabled],
    );
    return (
      <div
        data-testid="construction-reshape-editor"
        data-editing-step-id={editingStep?.id ?? ''}
        data-editing-operation={editingStep?.operation.kind ?? ''}
        data-initial-kind={initialKind ?? ''}
        data-initial-entry-step-id={initialEntry?.form.stepId ?? ''}
        data-initial-candidate-step-id={initialEntry?.candidateIntent.changedStepId ?? ''}
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
    );
  },
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

const openPivotPresentationProposal = async () => {
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

  mockRelatedExpandOwnerState.enabled = true;
  const view = render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
  const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
  fireEvent.click(within(rowSettings).getByTestId('construction-action-pivot-rows'));
  fireEvent.click(await screen.findByRole('button', { name: 'Emit Pivot presentation proposal' }));
  const proposalPreview = await screen.findByTestId('construction-proposal-preview');
  return { view, proposalPreview };
};

describe('BuilderWorkspace on-demand reconciliation', () => {
  let applyCommands: Mock;
  let assessRowChange: Mock;
  let reconcile: Mock;
  let preview: Mock;
  let publish: Mock;

  beforeEach(() => {
    window.sessionStorage.clear();
    mockLoomClientReference.current = undefined;
    mockRelatedExpandOwnerState.enabled = false;
    mockRelatedExpandQueryOwner.pauseAndDrain.mockReset().mockResolvedValue(undefined);
    mockRelatedExpandQueryOwner.resume.mockReset();
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
        workspaceInputs: [],
      };
    });
    mockLoomClient.getConstructionInputs.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1',
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      datasetGeneration: 'generation-1',
      entries: [],
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
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: groupWorkspace }));

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

  it('waits for capability support before offering generic source Pivot with one materialized column', async () => {
    let resolveCapabilities: ((value: unknown) => void) | undefined;
    mockLoomClient.getConstructionCapabilities.mockReturnValue(new Promise((resolve) => {
      resolveCapabilities = resolve;
    }));

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );
    await waitFor(() => expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalled());
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const categoryEntry = within(rowSettings).getByTestId('construction-action-pivot-rows');
    expect(categoryEntry).toBeDisabled();
    expect(within(rowSettings).queryByTestId('construction-action-table-pivot-rows')).toBeNull();

    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source-row-id',
      columns: [{ id: 'specimen-id', name: 'specimen_identifier', label: 'Specimen identifier', type: 'string' }],
      capabilities: [
        { kind: 'PIVOT' as const, supported: false, reasonCode: 'INSUFFICIENT_SCALAR_COLUMNS', reason: 'Pivot needs three table columns.' },
        { kind: 'CODED_PIVOT' as const, supported: true },
        { kind: 'FILTER' as const, supported: true },
      ],
    };
    const pivotSourceChoices = [
      { choiceId: 'source-observation-id', columnId: 'source-observation-id', occurrenceId: 'base', fieldPath: 'id', label: 'Observation.id', fhirType: 'id', logicalType: 'string', valueType: 'STRING', isIdentifier: true, isReference: false, isPopulated: true },
      { choiceId: 'source-quantity-code', columnId: 'source-quantity-code', occurrenceId: 'base', fieldPath: 'valueQuantity.code', label: 'Observation.valueQuantity.code', fhirType: 'string', logicalType: 'string', valueType: 'STRING', isIdentifier: false, isReference: false, isPopulated: true },
      { choiceId: 'source-quantity-value', columnId: 'source-quantity-value', occurrenceId: 'base', fieldPath: 'valueQuantity.value', label: 'Observation.valueQuantity.value', fhirType: 'decimal', logicalType: 'decimal', valueType: 'NUMBER', isIdentifier: false, isReference: false, isPopulated: true },
    ];
    await act(async () => {
      resolveCapabilities?.({
        snapshotToken: 'snapshot-1',
        draftVersion: 1,
        draftDigest: 'sha256:draft-1',
        outputId: 'specimens',
        stageId: 'source_projection',
        baseConstruction: { version: 1, steps: [] },
        stages: [sourceStage],
        selectedStage: sourceStage,
        workspaceInputs: [],
        sourceInput: { supported: true, stageId: 'source_projection', choices: pivotSourceChoices },
        pivotSourceInput: { supported: true, stageId: 'source_projection', choices: pivotSourceChoices },
      });
    });

    const genericPivot = await within(rowSettings).findByTestId('construction-action-table-pivot-rows');
    expect(genericPivot).toBeEnabled();
    expect(categoryEntry).toBeEnabled();
    fireEvent.click(genericPivot);
    const editor = await screen.findByTestId('construction-reshape-editor');
    expect(editor).toHaveAttribute('data-initial-kind', 'pivot');
  });

  it('opens authored list expansion directly for the selected append stage and previews once', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'cohort-row-id',
      columns: [
        { id: 'cohort-member-ids', name: 'id', label: 'Cohort member IDs', type: 'string', cardinality: 'many' as const },
        { id: 'cohort-name', name: 'cohort_name', label: 'Cohort name', type: 'string' },
      ],
      capabilities: [
        { kind: 'EXPAND' as const, supported: true },
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
    mockLoomClient.proposeConstruction.mockClear();
    mockLoomClient.proposeConstruction.mockImplementation(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
      proposalId: 'expand-entry-proposal',
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'expand-candidate-1',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage],
      previewStatus: 'READY',
      previewDurationMs: 2,
      preview: {
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
        receiptId: 'expand-entry-proposal',
        outputId: args.outputId,
        columns: [],
        rows: [],
        rowCount: 0,
        diagnostics: [],
      },
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
    const expandAction = await within(rowSettings).findByTestId('construction-action-expand-rows');
    expect(expandAction).toBeEnabled();
    fireEvent.click(expandAction);

    const editor = await screen.findByTestId('construction-reshape-editor');
    expect(editor).toHaveAttribute('data-initial-kind', 'expand');
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls[0]?.[0] as ProposeConstructionArgs | undefined;
    const expandStep = proposalArgs?.candidateConstruction.steps.at(-1);
    expect(expandStep?.operation.kind).toBe('EXPAND');
    expect(expandStep?.id).toBe(editor.getAttribute('data-initial-entry-step-id'));
    expect(editor.getAttribute('data-initial-candidate-step-id')).toBe(expandStep?.id);
    fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
    expect(await screen.findByRole('region', { name: 'Dataset review' })).toBeInTheDocument();
    await new Promise((resolve) => setTimeout(resolve, 350));
    expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce();
    expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalledWith(
      expect.objectContaining({ outputId: 'specimens', stageId: 'source_projection' }),
      expect.any(AbortSignal),
    );
  });

  it('aborts an authored expansion preview when its Builder owner changes', async () => {
    const sourceStage = {
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'cohort-row-id',
      columns: [
        { id: 'cohort-member-ids', name: 'id', label: 'Cohort member IDs', type: 'string', cardinality: 'many' as const },
        { id: 'cohort-name', name: 'cohort_name', label: 'Cohort name', type: 'string' },
      ],
      capabilities: [
        { kind: 'EXPAND' as const, supported: true },
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
    const pendingProposal = deferredRequest<ConstructionProposalResponse>();
    let proposalSignal: AbortSignal | undefined;
    mockLoomClient.proposeConstruction.mockClear();
    mockLoomClient.proposeConstruction.mockImplementation((_args: ProposeConstructionArgs, signal?: AbortSignal) => {
      proposalSignal = signal;
      return pendingProposal.request.unwrap();
    });
    mockLoomClient.preview.mockClear();

    const view = render(
      <BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    fireEvent.click(await within(rowSettings).findByTestId('construction-action-expand-rows'));
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    expect(proposalSignal).toBeDefined();

    view.rerender(
      <BuilderWorkspace organization="HTAN_INT" project="DifferentProject" explorerId="test" />,
    );
    await waitFor(() => expect(proposalSignal?.aborted).toBe(true));
    const staleArgs = mockLoomClient.proposeConstruction.mock.calls[0]?.[0] as ProposeConstructionArgs;
    pendingProposal.resolve({
      proposalId: 'stale-expand-proposal',
      outputId: staleArgs.outputId,
      snapshotToken: staleArgs.snapshotToken,
      draftVersion: staleArgs.expectedDraftVersion,
      draftDigest: staleArgs.expectedDraftDigest,
      baseDocumentDigest: 'document-1',
      candidateWorkspaceDigest: 'stale-candidate',
      changedStepId: staleArgs.changedStepId ?? '',
      candidateConstruction: staleArgs.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [sourceStage],
      previewStatus: 'READY',
      previewDurationMs: 1,
    });
    await act(async () => Promise.resolve());
    expect(mockLoomClient.preview).not.toHaveBeenCalled();
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
    const { view, proposalPreview } = await openPivotPresentationProposal();
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
  });

  it('waits for the route query owner before applying a construction proposal', async () => {
    const { view } = await openPivotPresentationProposal();
    const commandOrder: string[] = [];
    let releaseQueryDrain: (() => void) | undefined;
    const queryDrain = new Promise<void>((resolve) => { releaseQueryDrain = resolve; });
    mockRelatedExpandQueryOwner.pauseAndDrain.mockImplementation(() => {
      commandOrder.push('route-page-drained');
      return queryDrain;
    });
    mockRelatedExpandQueryOwner.resume.mockImplementation(() => commandOrder.push('route-query-resumed'));
    applyCommands.mockImplementation((args: { readonly commandId: string }) => {
      commandOrder.push('mutation-started');
      return rejectedRequest(Object.assign(new Error('Temporary command failure.'), { code: 'TEST_FAILURE' }));
    });
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));
    await waitFor(() => expect(mockRelatedExpandQueryOwner.pauseAndDrain).toHaveBeenCalledOnce());
    expect(applyCommands).not.toHaveBeenCalled();
    expect(commandOrder).toEqual(['route-page-drained']);
    await act(async () => releaseQueryDrain?.());
    await waitFor(() => expect(applyCommands).toHaveBeenCalledOnce());
    await waitFor(() => expect(mockRelatedExpandQueryOwner.resume).toHaveBeenCalledOnce());
    expect(commandOrder).toEqual(['route-page-drained', 'mutation-started', 'route-query-resumed']);

    view.unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));
    mockLoomClient.proposeConstruction.mockClear();
  });

  it('pauses related-path pagination before closing a canceled construction proposal', async () => {
    const { view } = await openPivotPresentationProposal();
    let finishRouteDrain: (() => void) | undefined;
    const routeDrain = new Promise<void>((resolve) => { finishRouteDrain = resolve; });
    mockRelatedExpandQueryOwner.pauseAndDrain.mockImplementation(() => {
      expect(screen.getByTestId('construction-reshape-editor')).toBeInTheDocument();
      expect(screen.getByTestId('construction-proposal-panel')).toBeInTheDocument();
      return routeDrain;
    });

    fireEvent.click(screen.getByTestId('construction-cancel-proposal'));

    expect(mockRelatedExpandQueryOwner.pauseAndDrain).toHaveBeenCalledOnce();
    expect(screen.getByTestId('construction-reshape-editor')).toBeInTheDocument();
    expect(screen.getByTestId('construction-proposal-panel')).toBeInTheDocument();
    expect(screen.getByTestId('construction-apply-proposal')).toBeDisabled();
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));
    expect(applyCommands).not.toHaveBeenCalled();
    await act(async () => finishRouteDrain?.());
    await waitFor(() => {
      expect(screen.queryByTestId('construction-reshape-editor')).not.toBeInTheDocument();
      expect(screen.queryByTestId('construction-proposal-panel')).not.toBeInTheDocument();
    });
    expect(mockRelatedExpandQueryOwner.resume).not.toHaveBeenCalled();
    expect(applyCommands).not.toHaveBeenCalled();
    view.unmount();
  });

  it('retires the route query owner after a successful command advances the draft', async () => {
    const { view } = await openPivotPresentationProposal();
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));

    await waitFor(() => expect(mockRelatedExpandQueryOwner.pauseAndDrain).toHaveBeenCalledOnce());
    await waitFor(() => expect(applyCommands).toHaveBeenCalledOnce());
    expect(mockRelatedExpandQueryOwner.resume).not.toHaveBeenCalled();

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

  it('waits for automatic compilation before Add columns but leaves row filters available', async () => {
    const pendingCompile = deferredRequest<ExplorerBuilderCompileResult>();
    reconcile.mockReturnValueOnce(pendingCompile.request);

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    const addColumns = screen.getByRole('button', {
      name: 'Add columns: Bring more information into each row.',
    });
    const filterRows = screen.getByRole('button', {
      name: 'Filter rows: Choose which rows appear in the table output.',
    });
    expect(addColumns).toBeDisabled();
    expect(filterRows).toBeEnabled();
    fireEvent.click(filterRows);
    const filterEditor = await screen.findByTestId('construction-filter-editor');
    expect(filterEditor).toHaveAttribute('aria-label', 'Filter output rows by condition');
    expect(within(filterEditor).getByRole('heading', { name: 'Filter output rows' })).toBeInTheDocument();
    expect(within(filterEditor).getByRole('combobox', { name: 'Column' })).toHaveProperty('value', 'specimen_identifier_id');

    await act(async () => pendingCompile.resolve(receipt));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      const previewPanel = screen.getByTestId('construction-preview');
      expect(previewPanel).toHaveAttribute('data-preview-status', 'ready');
      expect(previewPanel).toHaveAttribute('data-preview-receipt-id', 'receipt-1');
      expect(previewPanel).toHaveAttribute('data-preview-output-id', 'specimens');
    });
    expect(preview).toHaveBeenCalledWith(expect.objectContaining({
      receiptId: 'receipt-1',
      outputId: 'specimens',
      limit: 25,
    }));
    fireEvent.click(screen.getByRole('button', { name: 'Close operation editor' }));
    expect(screen.getByRole('button', {
      name: 'Add columns: Bring more information into each row.',
    })).toBeEnabled();
  });

  it('recompiles and previews when retrying after an automatic compile failure', async () => {
    const recoveredReceipt = { ...receipt, receiptId: 'receipt-recovered' };
    reconcile
      .mockReturnValueOnce(rejectedRequest(Object.assign(
        new Error('Controlled compilation rejection for the Recompile regression.'),
        { status: 422, code: 'VERIFY_COMPILE_REJECTED' },
      )))
      .mockReturnValueOnce(resolvedRequest(recoveredReceipt));
    preview.mockReturnValueOnce(resolvedRequest({
      apiVersion,
      kind: 'ExplorerBuilderPreview',
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
      receiptId: recoveredReceipt.receiptId,
      outputId: 'specimens',
      columns: receipt.outputs[0].columns,
      rows: [{ specimen_identifier: 'PATIENT-1' }],
      rowCount: 1,
      diagnostics: [],
    }));

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByRole('button', { name: 'Recompile' }));

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    expect(reconcile.mock.calls[1]?.[0]).toMatchObject({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'sha256:draft-1',
    });
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    expect(preview.mock.calls[0]?.[0]).toMatchObject({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      receiptId: recoveredReceipt.receiptId,
      outputId: 'specimens',
    });
    await waitFor(() => {
      expect(screen.getByTestId('construction-preview')).toHaveAttribute(
        'data-preview-status',
        'ready',
      );
      expect(screen.getByTestId('construction-preview')).toHaveAttribute(
        'data-preview-receipt-id',
        recoveredReceipt.receiptId,
      );
    });
  });

  it('retains a validated source selection after clearing it and lets a different attachment take precedence', async () => {
    const selection = (id: string): SelectionRevision => ({
      id,
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: `scope-${id}`,
      ruleDigest: `rule-${id}`,
      membershipDigest: `membership-${id}`,
      memberCount: 1,
      memberBytes: 32,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    });
    const selectionA = selection('selection-clear-a');
    const selectionB = selection('selection-clear-b');
    const attachedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        population: { selectionRevisionId: selectionA.id, route: [] },
      }],
    };
    const detachedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0] }],
    };
    const workspaceAttachedToB: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        population: { selectionRevisionId: selectionB.id, route: [] },
      }],
    };
    let currentServerWorkspace: ExplorerBuilderWorkspace = attachedWorkspace;
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: attachedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockImplementation(() => resolvedRequest({ ...receipt, builder: currentServerWorkspace }));
    mockLoomClient.getSelection.mockImplementation(async (args: { readonly selectionRevision: string }) => ({
      revision: args.selectionRevision === selectionA.id ? selectionA : selectionB,
      members: [],
    }));
    mockLoomClient.searchPopulationRoutes.mockImplementation(async (args: { readonly selectionRevisionId: string }) => ({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      selectionRevisionId: args.selectionRevisionId,
      complete: true,
      truncated: false,
      choices: [{
        routeChoiceId: `route-${args.selectionRevisionId}`,
        route: [],
        presentation: { summary: 'Use selected Specimen records', facts: [] },
      }],
    }));
    applyCommands.mockImplementation((args: { readonly commands: ReadonlyArray<{ readonly type: string }> }) => {
      const command = args.commands[0];
      const nextWorkspace = command?.type === 'SET_TABLE_POPULATION'
        ? workspaceAttachedToB
        : detachedWorkspace;
      currentServerWorkspace = nextWorkspace;
      return resolvedRequest({
        commandId: 'population-command',
        workspace: nextWorkspace,
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [{ type: 'TABLE_CHANGED', outputId: 'specimens' }],
        diagnostics: [],
      });
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const panel = await within(dialog).findByRole('region', { name: 'Starting collection' });
    await waitFor(() => expect(panel).toHaveAttribute('data-selection-revision-id', selectionA.id));
    expect(panel).toHaveAttribute('data-attached-selection-revision-id', selectionA.id);

    fireEvent.click(within(panel).getByRole('button', { name: 'Use all authorized rows' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{ type: 'CLEAR_TABLE_POPULATION', outputId: 'specimens' }],
    })));
    await waitFor(() => expect(
      within(screen.getByRole('dialog', { name: 'Row definition settings' }))
        .getByRole('region', { name: 'Starting collection' }),
    ).not.toHaveAttribute('data-attached-selection-revision-id'));
    const detachedPanel = within(screen.getByRole('dialog', { name: 'Row definition settings' }))
      .getByRole('region', { name: 'Starting collection' });
    expect(detachedPanel).toHaveAttribute('data-selection-revision-id', selectionA.id);
    fireEvent.click(within(detachedPanel).getByRole('button', { name: 'Use selected resources' }));

    await waitFor(() => expect(
      within(screen.getByRole('dialog', { name: 'Row definition settings' }))
        .getByRole('region', { name: 'Starting collection' }),
    ).toHaveAttribute('data-selection-revision-id', selectionB.id));
    const attachedBPanel = within(screen.getByRole('dialog', { name: 'Row definition settings' }))
      .getByRole('region', { name: 'Starting collection' });
    expect(attachedBPanel).toHaveAttribute('data-attached-selection-revision-id', selectionB.id);
    expect(mockLoomClient.getSelection).toHaveBeenCalledWith(expect.objectContaining({
      selectionRevision: selectionB.id,
    }), expect.any(AbortSignal));
  });

  it('does not carry a cleared selection into another table context', async () => {
    const selection: SelectionRevision = {
      id: 'selection-context-a',
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-context-a',
      ruleDigest: 'rule-context-a',
      membershipDigest: 'membership-context-a',
      memberCount: 1,
      memberBytes: 32,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    };
    const secondDocument: ExplorerBuilderWorkspace['documents'][number] = {
      ...workspace.documents[0],
      output: { id: 'patients', title: 'Patients' },
      rootResourceType: 'Patient',
      route: { occurrenceId: 'base', resourceType: 'Patient' },
      columns: [],
    };
    const multiTableWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [
        { ...workspace.documents[0], population: { selectionRevisionId: selection.id, route: [] } },
        secondDocument,
      ],
      tabs: [
        ...workspace.tabs,
        { id: 'patients-tab', title: 'Patients', outputId: 'patients', order: 1, visible: true },
      ],
    };
    const clearedMultiTableWorkspace: ExplorerBuilderWorkspace = {
      ...multiTableWorkspace,
      documents: [{ ...workspace.documents[0] }, secondDocument],
    };
    let currentServerWorkspace: ExplorerBuilderWorkspace = multiTableWorkspace;
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: multiTableWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockImplementation(() => resolvedRequest({ ...receipt, builder: currentServerWorkspace }));
    mockLoomClient.getSelection.mockResolvedValue({ revision: selection, members: [] });
    applyCommands.mockImplementation(() => {
      currentServerWorkspace = clearedMultiTableWorkspace;
      return resolvedRequest({
      commandId: 'clear-selection-command',
      workspace: clearedMultiTableWorkspace,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [{ type: 'TABLE_CHANGED', outputId: 'specimens' }],
      diagnostics: [],
      });
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const specimenPanel = await within(dialog).findByRole('region', { name: 'Starting collection' });
    await waitFor(() => expect(specimenPanel).toHaveAttribute('data-selection-revision-id', selection.id));
    fireEvent.click(within(specimenPanel).getByRole('button', { name: 'Use all authorized rows' }));
    await waitFor(() => expect(specimenPanel).not.toHaveAttribute('data-attached-selection-revision-id'));
    const selectionCallsBeforeTableSwitch = mockLoomClient.getSelection.mock.calls.length;

    fireEvent.click(within(dialog).getByRole('button', { name: 'Back to table' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Select second table' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const patientDialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const patientPanel = await within(patientDialog).findByRole('region', { name: 'Starting collection' });
    expect(patientPanel).not.toHaveAttribute('data-selection-revision-id');
    expect(within(patientDialog).queryByText(/selected, not attached/)).not.toBeInTheDocument();
    expect(mockLoomClient.getSelection).toHaveBeenCalledTimes(selectionCallsBeforeTableSwitch);
  });

  it('does not retain a cleared selection when the Loom client identity changes', async () => {
    const selection: SelectionRevision = {
      id: 'selection-client-a',
      project: 'HTAN_INT/BForePC',
      generation: 'generation-1',
      resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' },
      source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-client-a',
      ruleDigest: 'rule-client-a',
      membershipDigest: 'membership-client-a',
      memberCount: 1,
      memberBytes: 32,
      complete: true,
      createdAt: '2026-09-21T00:00:00Z',
    };
    const attachedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{
        ...workspace.documents[0],
        population: { selectionRevisionId: selection.id, route: [] },
      }],
    };
    const detachedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0] }],
    };
    let currentServerWorkspace: ExplorerBuilderWorkspace = attachedWorkspace;
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: attachedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockImplementation(() => resolvedRequest({ ...receipt, builder: currentServerWorkspace }));
    mockLoomClient.getSelection.mockResolvedValue({ revision: selection, members: [] });
    applyCommands.mockImplementation(() => {
      currentServerWorkspace = detachedWorkspace;
      return resolvedRequest({
      commandId: 'clear-selection-command',
      workspace: detachedWorkspace,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [{ type: 'TABLE_CHANGED', outputId: 'specimens' }],
      diagnostics: [],
      });
    });

    const view = render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const dialog = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const panel = await within(dialog).findByRole('region', { name: 'Starting collection' });
    await waitFor(() => expect(panel).toHaveAttribute('data-selection-revision-id', selection.id));
    fireEvent.click(within(panel).getByRole('button', { name: 'Use all authorized rows' }));
    await waitFor(() => expect(
      within(screen.getByRole('dialog', { name: 'Row definition settings' }))
        .getByRole('region', { name: 'Starting collection' }),
    ).not.toHaveAttribute('data-attached-selection-revision-id'));
    const detachedPanel = within(screen.getByRole('dialog', { name: 'Row definition settings' }))
      .getByRole('region', { name: 'Starting collection' });
    expect(detachedPanel).toHaveAttribute('data-selection-revision-id', selection.id);
    const selectionCallsBeforeClientSwap = mockLoomClient.getSelection.mock.calls.length;

    mockLoomClientReference.current = { ...mockLoomClient };
    view.rerender(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    await waitFor(() => expect(
      within(screen.getByRole('dialog', { name: 'Row definition settings' }))
        .getByRole('region', { name: 'Starting collection' }),
    ).not.toHaveAttribute('data-selection-revision-id'));
    expect(mockLoomClient.getSelection).toHaveBeenCalledTimes(selectionCallsBeforeClientSwap);
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
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: attachedWorkspace }));
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
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: attachedWorkspace }));
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

    const view = render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
        populationSelectionLoading
      />,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Configure rows' }));
    const rowSettings = await screen.findByRole('dialog', { name: 'Row definition settings' });
    const panel = await within(rowSettings).findByRole('region', { name: 'Starting collection' });
    expect(await within(panel).findByText('Loading the saved selection…')).toBeInTheDocument();
    expect(mockLoomClient.getSelection).not.toHaveBeenCalled();

    view.rerender(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
        populationSelection={handedOffSelection}
      />,
    );

    await waitFor(() => expect(panel).toHaveAttribute('data-selection-revision-id', 'selection-2'));
    expect(within(panel).queryByText('Loading the saved selection…')).not.toBeInTheDocument();
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
    window.sessionStorage.clear();
    const selectedTableStorageKey = `loom.builder.selected-table:${JSON.stringify([
      'HTAN_INT/BForePC',
      '/programs/HTAN_INT/projects/BForePC',
      'test',
    ])}`;
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
    const duplicatedWorkspace = {
      ...multiTableWorkspace,
      documents: [
        ...multiTableWorkspace.documents,
        {
          ...patientDocument,
          output: { id: 'patients-copy', title: 'Patients copy' },
        },
      ],
      tabs: [
        ...multiTableWorkspace.tabs,
        {
          id: 'patients-copy-tab',
          title: 'Patients copy',
          outputId: 'patients-copy',
          order: 2,
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
    let commandWorkspace = multiTableWorkspace;
    reconcile.mockImplementation(() => resolvedRequest({
      ...receipt,
      builder: commandWorkspace,
      outputs: [
        ...receipt.outputs,
        { outputId: 'patients', columns: patientPreviewColumns },
        ...(commandWorkspace === duplicatedWorkspace
          ? [{ outputId: 'patients-copy', columns: patientPreviewColumns }]
          : []),
      ],
    }));
    applyCommands.mockImplementation((request: {
      readonly commands: ReadonlyArray<{ readonly type: string }>;
    }) => {
      const command = request.commands[0];
      commandWorkspace = command?.type === 'DUPLICATE_TABLE'
        ? duplicatedWorkspace
        : multiTableWorkspace;
      return resolvedRequest({
        commandId: 'table-command',
        workspace: commandWorkspace,
        draftVersion: command?.type === 'DUPLICATE_TABLE' ? 2 : 3,
        draftDigest: command?.type === 'DUPLICATE_TABLE'
          ? 'sha256:duplicate'
          : 'sha256:delete',
        results: command?.type === 'DUPLICATE_TABLE'
          ? [{ type: 'TABLE_CREATED', outputId: 'patients-copy', tabId: 'patients-copy-tab', occurrenceId: 'base' }]
          : [],
        diagnostics: [],
      });
    });

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
    expect(window.sessionStorage.getItem(selectedTableStorageKey)).toBeNull();
    preview
      .mockReturnValueOnce(
        rejectedRequest({
          code: 'STALE_CATALOG_SNAPSHOT',
          message: 'The catalog is stale.',
          retryable: false,
        }),
      )
      .mockReturnValueOnce(resolvedRequest(patientPreview));
    fireEvent.click(screen.getByRole('button', { name: 'Select second table' }));
    expect(window.sessionStorage.getItem(selectedTableStorageKey)).toBe('patients');
    await waitFor(() =>
      expect(screen.getByTestId('construction-table-patients')).toHaveAttribute(
        'aria-current',
        'page',
      ),
    );

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

    fireEvent.click(screen.getByRole('button', { name: 'Duplicate selected table' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{ type: 'DUPLICATE_TABLE', sourceOutputId: 'patients', title: 'Patients copy' }],
    })));
    await waitFor(() => expect(screen.getByTestId('construction-table-patients-copy')).toHaveAttribute(
      'aria-current',
      'page',
    ));
    expect(window.sessionStorage.getItem(selectedTableStorageKey)).toBe('patients-copy');

    vi.spyOn(window, 'confirm').mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: 'Delete selected table' }));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      commands: [{ type: 'DELETE_TABLE', outputId: 'patients-copy' }],
    })));
    await waitFor(() => expect(screen.getByTestId('construction-table-specimens')).toHaveAttribute(
      'aria-current',
      'page',
    ));
    expect(window.sessionStorage.getItem(selectedTableStorageKey)).toBe('specimens');
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

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready'));

    fireEvent.click(
      await screen.findByRole('button', { name: 'Save column change' }),
    );
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(2));
    expect(reconcile.mock.calls[0]?.[0]).toMatchObject({ draftVersion: 1, draftDigest: 'sha256:draft-1' });
    expect(reconcile.mock.calls[1]?.[0]).toMatchObject({ draftVersion: 2, draftDigest: 'sha256:draft-2' });
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-current-draft-version', '2'));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled(),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
    expect(reconcile).toHaveBeenCalledTimes(2);
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

  it('opens the calculation editor for a Group output when its final stage supports DERIVE', async () => {
    const groupedConstruction: Construction = {
      version: 1,
      steps: [{
        id: 'group_rows',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'GROUP', group: {
          constructionId: 'group_rows',
          missingKeyPolicy: 'GROUP',
          keys: [],
          aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'row_count' }],
        } },
        outputs: [{ id: 'row_count', name: 'row_count', label: 'Row count', type: 'integer' }],
      }],
    };
    const groupedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0], construction: groupedConstruction }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: groupedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockReturnValue(resolvedRequest({ ...receipt, builder: groupedWorkspace }));
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
        rowIdentityColumn: 'group-row-id',
        columns: [{ id: 'row_count', name: 'row_count', label: 'Row count', type: 'integer' }],
        capabilities: [{ kind: 'DERIVE' as const, supported: true }],
      };
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: groupedConstruction,
        stages: [selectedStage],
        selectedStage,
        workspaceInputs: [],
      };
    });

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);

    const calculate = await screen.findByTestId('construction-action-calculate');
    expect(calculate).toHaveTextContent('Add a calculated column');
    expect(calculate).toBeEnabled();
    expect(mockLoomClient.getConstructionCapabilities).toHaveBeenCalledWith(
      expect.objectContaining({ outputId: 'specimens', stageId: 'group_rows' }),
      expect.any(AbortSignal),
    );
    fireEvent.click(calculate);
    expect(await screen.findByTestId('construction-calculate-editor')).toBeInTheDocument();
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
    await waitFor(() => expect(preview).toHaveAttribute('data-preview-status', 'ready'));
    expect(within(preview).getByText('Preview table')).toBeInTheDocument();
    expect(screen.getByTestId('construction-source-setup')).not.toHaveAttribute('open');
    expect(screen.getByTestId('construction-action-add-columns')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-keep-rows')).toBeInTheDocument();
    expect(screen.getByTestId('construction-action-keep-rows')).toHaveTextContent('Filter rows');
    expect(screen.getByTestId('construction-rows-settings-trigger')).toBeInTheDocument();
    expect(screen.queryByTestId('construction-action-calculate')).not.toBeInTheDocument();
    expect(screen.getByTestId('construction-action-combine')).toBeEnabled();

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

  it('creates an empty rooted Combine target from a populated table and opens its published-version editor', async () => {
    const combineOutputId = 'specimens-combined';
    const combineWorkspace = {
      ...workspace,
      documents: [
        ...workspace.documents,
        {
          ...workspace.documents[0],
          output: { id: combineOutputId, title: 'Specimens combined' },
          columns: [],
        },
      ],
      tabs: [
        ...workspace.tabs,
        {
          id: 'specimens-combined-tab',
          title: 'Specimens combined',
          outputId: combineOutputId,
          order: 1,
          visible: true,
        },
      ],
    };
    applyCommands.mockReturnValue(resolvedRequest({
      commandId: 'create-combine-target',
      workspace: combineWorkspace,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [{ type: 'TABLE_CREATED', outputId: combineOutputId, occurrenceId: 'base' }],
      diagnostics: [],
    }));

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const action = await screen.findByTestId('construction-action-combine');
    expect(action).toBeEnabled();
    fireEvent.click(action);

    await waitFor(() => expect(applyCommands).toHaveBeenCalledWith(expect.objectContaining({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      commands: [{ type: 'CREATE_TABLE', title: 'Specimens combined', rootNodeId: 'specimen-node' }],
    })));
    await screen.findByTestId('construction-combine-editor');
    expect(screen.getByTestId('construction-operation-editor')).toHaveAttribute(
      'data-output-id',
      combineOutputId,
    );
    await waitFor(() => expect(mockLoomClient.getConstructionInputs).toHaveBeenCalledWith(
      expect.objectContaining({
        project: 'HTAN_INT/BForePC',
        explorerId: 'test',
        authResourcePath: '/programs/HTAN_INT/projects/BForePC',
        snapshotToken: 'snapshot-1',
        expectedDraftVersion: 2,
        expectedDraftDigest: 'sha256:draft-2',
        limit: 100,
      }),
      expect.any(AbortSignal),
    ));
    expect(applyCommands.mock.calls[0]?.[0].commands).toEqual([
      { type: 'CREATE_TABLE', title: 'Specimens combined', rootNodeId: 'specimen-node' },
    ]);
  });

  it('automatically proposes a current-draft Combine edit and loads its exact preview without a Preview action', async () => {
    const combineOutputId = 'specimens-combined';
    const draftInput = (outputId: string, title: string) => ({
      ...workspace.documents[0]!,
      output: { id: outputId, title },
      columns: [{
        ...column,
        column: `${outputId}_record_key`,
        label: 'Record key',
      }],
    });
    const baseWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [
        workspace.documents[0]!,
        draftInput('draft-left', 'Left draft table'),
        draftInput('draft-right', 'Right draft table'),
      ],
      tabs: [
        ...workspace.tabs,
        { id: 'draft-left-tab', title: 'Left draft table', outputId: 'draft-left', order: 1, visible: true },
        { id: 'draft-right-tab', title: 'Right draft table', outputId: 'draft-right', order: 2, visible: true },
      ],
    };
    const combineWorkspace: ExplorerBuilderWorkspace = {
      ...baseWorkspace,
      documents: [
        ...baseWorkspace.documents,
        {
          ...workspace.documents[0]!,
          output: { id: combineOutputId, title: 'Specimens combined' },
          columns: [],
        },
      ],
      tabs: [
        ...baseWorkspace.tabs,
        { id: 'specimens-combined-tab', title: 'Specimens combined', outputId: combineOutputId, order: 3, visible: true },
      ],
    };
    const workspaceInputs = [
      {
        outputId: 'draft-left',
        title: 'Left draft table',
        columns: [{
          id: 'left-record-key',
          name: 'record_key',
          label: 'Record key',
          logicalType: 'string',
          cardinality: 'required_one' as const,
          nullable: false,
          appendCompatibilityKey: 'string:String',
        }],
      },
      {
        outputId: 'draft-right',
        title: 'Right draft table',
        columns: [{
          id: 'right-record-key',
          name: 'record_key',
          label: 'Record key',
          logicalType: 'string',
          cardinality: 'required_one' as const,
          nullable: false,
          appendCompatibilityKey: 'string:String',
        }],
      },
    ];
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: baseWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockImplementation((args: { readonly draftVersion: number }) => resolvedRequest({
      ...receipt,
      receiptId: `receipt-v${args.draftVersion}`,
      builder: args.draftVersion >= 2 ? combineWorkspace : baseWorkspace,
    }));
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
        columns: [{ id: 'record-key', name: 'record_key', label: 'Record key', type: 'string' }],
        capabilities: [{ kind: 'FILTER' as const, supported: true }],
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
        workspaceInputs,
      };
    });
    mockLoomClient.getConstructionInputs.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1',
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      datasetGeneration: 'generation-1',
      entries: [],
    });
    mockLoomClient.proposeConstruction.mockReset();
    mockLoomClient.preview.mockReset().mockImplementation(async (args: {
      readonly receiptId: string;
      readonly outputId: string;
    }) => ({
      apiVersion,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: args.receiptId,
      outputId: args.outputId,
      columns: [{ column: 'record_key', label: 'Record key', logicalType: 'string', filterable: false, chartable: false }],
      rows: [{ record_key: 'previewed-row' }],
      rowCount: 1,
      diagnostics: [],
    }));
    mockLoomClient.proposeConstruction.mockImplementation(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
      proposalId: 'workspace-combine-proposal',
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      baseDocumentDigest: 'combined-document-v2',
      candidateWorkspaceDigest: 'sha256:combine-candidate',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [] },
      stages: [],
      previewStatus: 'READY',
      previewDurationMs: 2,
    }));
    applyCommands.mockImplementation((args: { readonly commandId: string }) => resolvedRequest({
      commandId: args.commandId,
      workspace: combineWorkspace,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [{ type: 'TABLE_CREATED', outputId: combineOutputId, occurrenceId: 'base' }],
      diagnostics: [],
    }));

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    await waitFor(() => expect(reconcile).toHaveBeenCalled());
    const combineAction = await screen.findByTestId('construction-action-combine');
    expect(combineAction).toBeEnabled();
    fireEvent.click(combineAction);
    await screen.findByTestId('construction-combine-editor');
    fireEvent.click(screen.getByTestId('construction-combine-choice-append'));
    fireEvent.change(screen.getByRole('combobox', { name: 'Input table 1' }), {
      target: { value: JSON.stringify(['WORKSPACE_OUTPUT', 'draft-left']) },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'Input table 2' }), {
      target: { value: JSON.stringify(['WORKSPACE_OUTPUT', 'draft-right']) },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add output field' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Output field 1 name' }), {
      target: { value: 'record_key' },
    });
    fireEvent.change(screen.getByRole('textbox', { name: 'Output field 1 label' }), {
      target: { value: 'Record key' },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'Output field 1 matching field in input 1' }), {
      target: { value: 'column:left-record-key' },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'Output field 1 matching field in input 2' }), {
      target: { value: 'column:right-record-key' },
    });

    await screen.findByTestId('construction-proposal-ready');
    const proposalPanel = screen.getByTestId('construction-proposal-panel');
    expect(proposalPanel).toHaveAttribute('data-proposal-id', 'workspace-combine-proposal');
    expect(screen.getByTestId('construction-proposal-preview')).toHaveAttribute('data-preview-receipt-id', 'workspace-combine-proposal');
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledOnce());
    const proposalArgs = mockLoomClient.proposeConstruction.mock.calls[0]?.[0] as ProposeConstructionArgs;
    expect(proposalArgs).toMatchObject({
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 2,
      expectedDraftDigest: 'sha256:draft-2',
      outputId: combineOutputId,
    });
    expect(proposalArgs.candidateConstruction.steps).toHaveLength(1);
    const combineStep = proposalArgs.candidateConstruction.steps[0]!;
    expect(combineStep.inputs).toEqual([
      { kind: 'WORKSPACE_OUTPUT', outputId: 'draft-left' },
      { kind: 'WORKSPACE_OUTPUT', outputId: 'draft-right' },
    ]);
    expect(combineStep.operation).toMatchObject({ kind: 'COMBINE', combine: { kind: 'APPEND' } });
    expect(combineStep.operation.kind === 'COMBINE' ? combineStep.operation.combine.projections : []).toEqual([
      { outputColumnId: combineStep.outputs[0]?.id, inputIndex: 0, inputColumnId: 'left-record-key' },
      { outputColumnId: combineStep.outputs[0]?.id, inputIndex: 1, inputColumnId: 'right-record-key' },
    ]);
    await waitFor(() => expect(mockLoomClient.preview).toHaveBeenCalledWith(
      expect.objectContaining({ receiptId: 'workspace-combine-proposal', outputId: combineOutputId }),
      expect.any(AbortSignal),
    ));
    expect(screen.queryByRole('button', { name: /preview/i })).not.toBeInTheDocument();
  });

  it('automatically previews history removal, lets Cancel preserve the saved step, and applies only the exact proposal receipt', async () => {
    const savedConstruction: Construction = {
      version: 1,
      steps: [{
        id: 'saved-filter',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: 'specimen_identifier_id', operator: 'EXISTS' } },
        outputs: [{ id: 'specimen_identifier_id', name: 'specimen_identifier', label: 'Specimen identifier' }],
      }],
    };
    const savedWorkspace: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]!, construction: savedConstruction }],
    };
    const workspaceAfterRemoval: ExplorerBuilderWorkspace = {
      ...workspace,
      documents: [{ ...workspace.documents[0]! }],
    };
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: { ...builderState, workspace: savedWorkspace },
      isLoading: false,
      refetch: vi.fn(),
    });
    reconcile.mockImplementation((args: { readonly draftVersion: number }) => resolvedRequest({
      ...receipt,
      receiptId: `receipt-v${args.draftVersion}`,
      builder: args.draftVersion > 1 ? workspaceAfterRemoval : savedWorkspace,
    }));
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
        capabilities: [{ kind: 'FILTER' as const, supported: true }],
      };
      return {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        baseConstruction: savedConstruction,
        stages: [selectedStage],
        selectedStage,
      };
    });
    mockLoomClient.proposeConstruction.mockReset();
    mockLoomClient.proposeConstruction.mockImplementation(async (
      args: ProposeConstructionArgs,
    ): Promise<ConstructionProposalResponse> => {
      const proposalNumber = mockLoomClient.proposeConstruction.mock.calls.length;
      const proposalId = `remove-filter-proposal-${proposalNumber}`;
      return {
        proposalId,
        outputId: args.outputId,
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        baseDocumentDigest: 'filter-document-v1',
        candidateWorkspaceDigest: `sha256:remove-filter-${proposalNumber}`,
        changedStepId: args.changedStepId ?? '',
        candidateConstruction: args.candidateConstruction,
        dependencyImpact: { affectedStepIds: [], removedStepIds: args.removeStepIds ?? [] },
        stages: [],
        previewStatus: 'READY',
        previewDurationMs: 1,
        preview: {
          apiVersion,
          kind: 'ExplorerBuilderPreview',
          rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
          receiptId: proposalId,
          outputId: args.outputId,
          columns: receipt.outputs[0]!.columns,
          rows: [{ specimen_identifier: `without-filter-${proposalNumber}` }],
          rowCount: 1,
          diagnostics: [],
        },
      };
    });
    applyCommands.mockImplementation((args: { readonly commandId: string }) => resolvedRequest({
      commandId: args.commandId,
      workspace: workspaceAfterRemoval,
      draftVersion: 2,
      draftDigest: 'sha256:draft-2',
      results: [{ type: 'TABLE_CHANGED', outputId: 'specimens', column: 'specimen_identifier' }],
      diagnostics: [],
    }));

    render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    await screen.findByTestId('construction-history-step-saved-filter');
    fireEvent.click(screen.getByTestId('construction-history-step-saved-filter'));
    fireEvent.click(screen.getByTestId('construction-remove-step-saved-filter'));
    await screen.findByTestId('construction-proposal-ready');
    expect(screen.getByTestId('construction-proposal-panel')).toHaveAttribute('data-proposal-id', 'remove-filter-proposal-1');
    expect(screen.getByTestId('construction-proposal-ready')).toHaveTextContent('Removal preview');
    expect(screen.getByTestId('construction-proposal-panel')).toHaveAttribute('data-proposal-id', 'remove-filter-proposal-1');
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledTimes(1));
    const firstRemoval = mockLoomClient.proposeConstruction.mock.calls[0]?.[0] as ProposeConstructionArgs;
    expect(firstRemoval.removeStepIds).toEqual(['saved-filter']);
    expect(firstRemoval.candidateConstruction.steps).toEqual([]);
    expect(Object.hasOwn(firstRemoval, 'changedStepId')).toBe(false);
    expect(applyCommands).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('construction-cancel-proposal'));
    expect(screen.queryByTestId('construction-proposal-panel')).not.toBeInTheDocument();
    expect(screen.getByTestId('construction-history-step-saved-filter')).toBeInTheDocument();
    expect(applyCommands).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('construction-history-step-saved-filter'));
    fireEvent.click(screen.getByTestId('construction-remove-step-saved-filter'));
    await screen.findByTestId('construction-proposal-ready');
    await waitFor(() => expect(mockLoomClient.proposeConstruction).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('construction-proposal-panel')).toHaveAttribute('data-proposal-id', 'remove-filter-proposal-2');
    expect(screen.getByTestId('construction-proposal-preview')).toHaveAttribute('data-preview-receipt-id', 'remove-filter-proposal-2');
    expect(screen.getByTestId('construction-apply-proposal')).toHaveTextContent('Apply removal');
    expect(screen.getByTestId('construction-apply-proposal')).toBeEnabled();
    expect(applyCommands).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));
    await waitFor(() => expect(applyCommands).toHaveBeenCalledOnce());
    expect(applyCommands.mock.calls[0]?.[0].commands).toEqual([{
      type: 'APPLY_CONSTRUCTION_PROPOSAL',
      outputId: 'specimens',
      proposalId: 'remove-filter-proposal-2',
    }]);
    await waitFor(() => expect(screen.queryByTestId('construction-history-step-saved-filter')).not.toBeInTheDocument());
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
