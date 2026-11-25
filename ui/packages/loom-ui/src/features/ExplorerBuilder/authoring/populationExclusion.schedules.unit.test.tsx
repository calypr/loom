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
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePopulationMappingMutation,
  usePublishExplorerAuthoringV2Mutation,
  LoomProvider,
} from '../../../react';
import { useGetExplorerBuilderStateV2Query } from '../session/useBuilderLoad';
import BuilderWorkspace from '../BuilderWorkspace';
import { LoomExplorerBuilder } from '../Builder';
import { ordinaryNetworkFixture } from '../../../../testSupport/ordinaryNetworkFixture';
import type { SelectionRevision } from '../../../contracts/selection';
import type {
  ConstructionCategoryDiscoveryResponse,
  Construction,
  ConstructionProposalResponse,
  ConstructionCapabilitiesResponse,
  ExplorerBuilderCommandsResult,
  ExplorerBuilderCompileResult,
  ExplorerBuilderDocument,
  ExplorerBuilderPreviewResult,
  ExplorerBuilderWorkspace,
  RowChangeAssessment,
} from '../../../contracts/types';
import type { RelatedExpandQueryOwner } from '../session/commandQueue';
import type { ConstructionCandidateIntent } from '../session/useConstructionLifecycle';
import { createLoomClient } from '../../../client/api';
import type { LoomClient } from '../../../client/api';
import type {
  ApplyExplorerBuilderCommandsArgs,
  PreviewExplorerBuilderArgs,
  ProposeConstructionArgs,
  ReconcileExplorerBuilderArgs,
} from '../../../client/api';
import { population_exclusion as schedules } from '../../../../testdata/schedules/population_exclusion';

const mockLoomClient = vi.hoisted(() => ({
  describeAuthoringNetwork: vi.fn(),
  getBuilder: vi.fn(),
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
const mockRelatedExpandOwnerState = vi.hoisted(() => ({
  enabled: false,
  owner: null as null | {
    readonly draftVersion: number;
    readonly draftDigest: string;
    readonly pauseAndDrain: () => Promise<void>;
    readonly resume: () => void;
  },
}));
const mockUseActualReshapeEditor = vi.hoisted(() => ({ enabled: false }));
const mockUseActualReconcile = vi.hoisted(() => ({ enabled: false, isLoading: false }));
const mockReconcileMutation = vi.hoisted(() => vi.fn());
const mockRelatedExpandQueryOwner = vi.hoisted(() => ({
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  pauseAndDrain: vi.fn<RelatedExpandQueryOwner['pauseAndDrain']>(async () => undefined),
  resume: vi.fn(),
}));
const mockUseActualPreviewTable = vi.hoisted(() => ({ enabled: false }));

const savedSemanticBindingId = (step: Construction['steps'][number] | undefined): string => {
  if (!step || step.operation.kind !== 'GROUP') return '';
  for (const key of step.operation.group.keys ?? []) {
    if ('semanticCategory' in key && key.semanticCategory.kind === 'BINDING') {
      return key.semanticCategory.bindingId;
    }
  }
  return '';
};

vi.mock('../session/useBuilderLoad', () => ({
  useGetExplorerBuilderStateV2Query: vi.fn(),
}));

vi.mock('../../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../react')>();
  return {
    ...actual,
    useLoomClient: () => (mockLoomClientReference.current as typeof mockLoomClient | undefined) ?? mockLoomClient,
    useApplyExplorerBuilderCommandsV2Mutation: vi.fn(),
    useAssessExplorerRowChangeMutation: vi.fn(),
    useCreateExplorerAuthoringMutation: vi.fn(),
    useDeleteExplorerAuthoringMutation: vi.fn(),
    useGetExplorerAuthoringCapabilityV2Query: vi.fn(),
    useGetExplorerAuthoringExplorersQuery: vi.fn(),
    useGetExplorerCandidateSuggestionsV2Mutation: vi.fn(),
    usePreviewExplorerAuthoringV2Mutation: vi.fn(),
    usePopulationMappingMutation: vi.fn(),
    usePublishExplorerAuthoringV2Mutation: vi.fn(),
    useReconcileExplorerBuilderV2Mutation: (...args: Parameters<typeof actual.useReconcileExplorerBuilderV2Mutation>) => {
      if (!mockUseActualReconcile.enabled) return mockReconcileMutation(...args);
      const result = actual.useReconcileExplorerBuilderV2Mutation(...args);
      mockUseActualReconcile.isLoading = result[1].isLoading;
      return result;
    },
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

vi.mock('../components/BuilderToolbar', () => ({
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
    readonly tables: ReadonlyArray<{ readonly outputId: string; readonly title: string }>;
    readonly publishDisabled: boolean;
    readonly publishing: boolean;
  }) => (
    <div>
      <output data-testid="workspace-table-identities">{JSON.stringify(tables.map(({ outputId, title }) => ({ outputId, title })))}</output>
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
        <>
          <button type="button" onClick={() => onSelectTable(tables[0].outputId)}>
            Select first table
          </button>
          <button type="button" onClick={() => onSelectTable(tables[1].outputId)}>
            Select second table
          </button>
        </>
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

vi.mock('../components/GuidedGraphWorkspace', () => ({
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

vi.mock('../components/ConceptCatalog', () => ({
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
  }) => {
    const [actionError, setActionError] = React.useState<string>();
    const submit = async (selections: Parameters<NonNullable<typeof onAddSelected>>[0]) => {
      setActionError(undefined);
      try {
        await onAddSelected?.(selections);
      } catch (error) {
        setActionError(error instanceof Error ? error.message : 'Loom could not add the selected features.');
      }
    };
    return (
      <section data-testid="mock-concept-catalog">
        <button
          type="button"
          disabled={disabled}
          onClick={() => void submit([
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
        <button
          type="button"
          disabled={disabled}
          onClick={() => void submit([{
            constructionChoice: { choiceId: 'resource-type-choice', form: 'VALUE' },
            title: 'Resource Type',
          }])}
        >
          Add Group row-value fixture
        </button>
        {disabled && disabledReason ? <p role="status">{disabledReason}</p> : null}
        {actionError ? <p role="alert">{actionError}</p> : null}
      </section>
    );
  },
}));

vi.mock('../components/PreviewTable', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../components/PreviewTable')>();
  return {
    ...actual,
    PreviewTable: (props: Parameters<typeof actual.PreviewTable>[0]) => mockUseActualPreviewTable.enabled
      ? <actual.PreviewTable {...props} />
      : <div>Preview table</div>,
    formatPreviewCell: (value: unknown) => String(value ?? ''),
    previewCellTitle: (value: unknown) => String(value ?? ''),
    sortByPresentationOrder: <T,>(values: ReadonlyArray<T>, orderOf: (value: T, index: number) => number) => values
      .map((value, index) => ({ value, index, order: orderOf(value, index) }))
      .sort((left, right) => left.order - right.order || left.index - right.index)
      .map(({ value }) => value),
  };
});

vi.mock('../constructionOperations/ConstructionReshapeEditor', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../constructionOperations/ConstructionReshapeEditor')>();
  const MockConstructionReshapeEditor = ({
    editingStep,
    initialKind,
    construction,
    capabilities,
    initialEntry,
    hideChangeRowOperation,
    pivotDiscovery,
    onDiscoverCategories,
    onFormKindChange,
    onCandidateChange,
    relatedExpandQueryOwnerRef,
  }: Parameters<typeof actual.ConstructionReshapeEditor>[0]) => {
    React.useImperativeHandle<RelatedExpandQueryOwner | null, RelatedExpandQueryOwner | null>(
      relatedExpandQueryOwnerRef ?? null,
      () => mockRelatedExpandOwnerState.owner ??
        (mockRelatedExpandOwnerState.enabled ? mockRelatedExpandQueryOwner : null),
      [relatedExpandQueryOwnerRef, mockRelatedExpandOwnerState.enabled, mockRelatedExpandOwnerState.owner],
    );
    return (
      <div
        data-testid="construction-reshape-editor"
        data-editing-step-id={editingStep?.id ?? ''}
        data-editing-operation={editingStep?.operation.kind ?? ''}
        data-initial-kind={initialKind ?? ''}
        data-hide-change-row-operation={hideChangeRowOperation ? 'true' : 'false'}
        data-initial-entry-step-id={initialEntry && 'stepId' in initialEntry.form ? initialEntry.form.stepId : ''}
        data-initial-candidate-step-id={initialEntry?.candidateIntent?.changedStepId ?? ''}
        data-construction-step-count={construction.steps.length}
        data-discovery-status={pivotDiscovery?.status ?? 'none'}
        data-capability-stage-id={capabilities.selectedStage.id}
        data-semantic-choice-count={capabilities.semanticGroupChoices?.length ?? 0}
        data-saved-semantic-binding-id={savedSemanticBindingId(editingStep)}
      >
        {onFormKindChange ? <>
          <button type="button" data-testid="mock-reshape-form-choose" onClick={() => onFormKindChange('choose')}>Use reshape chooser</button>
          <button type="button" data-testid="mock-reshape-form-pivot" onClick={() => onFormKindChange('pivot')}>Use Pivot form</button>
          <button type="button" data-testid="mock-reshape-form-group" onClick={() => onFormKindChange('group')}>Use Group form</button>
          <button type="button" data-testid="mock-reshape-form-source-group" onClick={() => onFormKindChange('source-group')}>Use source Group form</button>
          <button type="button" data-testid="mock-reshape-form-coded-group" onClick={() => onFormKindChange('coded-group')}>Use coded Group form</button>
          <button type="button" data-testid="mock-reshape-form-expand" onClick={() => onFormKindChange('expand')}>Use Expand form</button>
          <button type="button" data-testid="mock-reshape-form-related-expand" onClick={() => onFormKindChange('related-expand')}>Use related Expand form</button>
          <button type="button" data-testid="mock-reshape-form-coded-pivot" onClick={() => onFormKindChange('coded-pivot')}>Use coded Pivot form</button>
          <button type="button" data-testid="mock-reshape-form-unpivot" onClick={() => onFormKindChange('unpivot')}>Use Unpivot form</button>
        </> : null}
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
              operation: { kind: 'PIVOT', pivot: {
                constructionId: 'pivot-proposal-step',
                groupKeyIds: ['specimen-id', 'patient-id', 'observation-id', 'value-id', 'null-id'],
                categoryColumnId: 'category-id', valueColumnId: 'quantity-id',
                categories: [{ key: { kind: 'STRING', string: 'd' }, outputColumnId: 'd-output' }],
                duplicatePolicy: 'ERROR', missingCellPolicy: 'NULL', unlistedCategoryPolicy: 'ERROR',
              } },
              outputs: [
                { id: 'd-output', name: 'd', label: 'Observed quantity d', type: 'integer', table: { order: 0 } },
                { id: 'specimen-id', name: 'specimen_id', label: 'Specimen ID', type: 'string', table: { order: 1 } },
                { id: 'patient-id', name: 'patient_id', label: 'Patient FHIR ID', type: 'string', table: { order: 2 } },
                { id: 'observation-id', name: 'observation_id', label: 'Observation FHIR ID', type: 'string', table: { order: 3 } },
                { id: 'value-id', name: 'related_value', label: 'Observation value', type: 'string', table: { order: 4 } },
                { id: 'null-id', name: 'null_value', label: 'Null', type: 'string', table: { order: 5 } },
              ],
            }],
          },
        })}>Emit Pivot presentation proposal</button> : null}
      </div>
    );
  };
  return {
    ...actual,
    ConstructionReshapeEditor: (props: Parameters<typeof actual.ConstructionReshapeEditor>[0]) =>
      mockUseActualReshapeEditor.enabled
        ? <actual.ConstructionReshapeEditor {...props} />
        : <MockConstructionReshapeEditor {...props} />,
  };
});

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

const readyRootChangeAssessment = (_options?: object) => ({
  snapshotToken: 'snapshot-1',
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  status: 'READY' as const,
  currentRootResourceType: 'Patient',
  candidateRootResourceType: 'Patient',
  preservedFeatureKeys: [],
  unresolved: [],
  diagnostics: [],
});
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
    promise,
    resolve,
  };
};

const saveWorkspaceEdit = async () => {
  fireEvent.click(await screen.findByRole('button', { name: 'Change relationship' }));
};
const findRowSettings = () => screen.findByRole('region', { name: 'Row definition settings' });
const getRowSettings = () => screen.getByRole('region', { name: 'Row definition settings' });
const queryRowSettings = () => screen.queryByRole('region', { name: 'Row definition settings' });
const openRowSettings = async () => {
  const closeEditor = screen.queryByRole('button', { name: 'Close operation editor' });
  if (closeEditor) fireEvent.click(closeEditor);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit row definition' }));
  return findRowSettings();
};
describe('population exclusion mounted schedules', () => {
  let applyCommands: Mock;
  let assessRowChange: Mock;
  let reconcile: Mock;
  let preview: Mock;
  let publish: Mock;

  beforeEach(() => {
    mockLoomClient.proposeConstruction.mockReset();
    mockLoomClient.describeAuthoringNetwork.mockReset().mockImplementation(async (args: Parameters<LoomClient['describeAuthoringNetwork']>[0]) => {
      const current = (useGetExplorerBuilderStateV2Query as Mock).mock.results.at(-1)?.value?.data;
      const document = current?.workspace?.documents.find((candidate: { output: { id: string } }) => candidate.output.id === args.outputId);
      if (!document || current.draftVersion !== args.expectedDraftVersion || current.draftDigest !== args.expectedDraftDigest || current.catalog.snapshotToken !== args.snapshotToken) {
        throw new Error('The fixture has no saved document for this requested draft identity.');
      }
      return ordinaryNetworkFixture(document, args);
    });
    window.sessionStorage.clear();
    mockLoomClientReference.current = undefined;
    mockRelatedExpandOwnerState.enabled = false;
    mockUseActualReshapeEditor.enabled = false;
    mockUseActualPreviewTable.enabled = false;
    mockUseActualReconcile.enabled = false;
    mockUseActualReconcile.isLoading = false;
    mockRelatedExpandQueryOwner.pauseAndDrain.mockReset().mockResolvedValue(undefined);
    mockRelatedExpandQueryOwner.resume.mockReset();
    mockLoomClient.getBuilder.mockReset();
    mockLoomClient.getSelection.mockReset();
    mockLoomClient.listRowDefinitionChoices.mockReset();
    mockLoomClient.listRowDefinitionChoices.mockResolvedValue({
      snapshotToken: 'snapshot-1',
      outputId: 'specimens',
      choices: [],
      explicitGroups: [],
    });
    mockLoomClient.proposeConstructionChoices.mockReset();
    mockLoomClient.proposeConstructionChoices.mockImplementation(async (args: Parameters<LoomClient['proposeConstructionChoices']>[0]) => ({
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
    assessRowChange = vi.fn().mockReturnValue(resolvedRequest(readyRootChangeAssessment({rootOccurrenceId:'patient-subject',receiptId:'receipt-1',routeRebase:[{occurrenceId:'base',edgeId:'patient-specimen'}]})));
    reconcile = vi.fn().mockImplementation((args: {
      readonly snapshotToken: string;
      readonly draftVersion: number;
      readonly draftDigest: string;
    }) => resolvedRequest({
      ...receipt,
      snapshotToken: args.snapshotToken,
    }));
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
    (useGetExplorerBuilderStateV2Query as Mock).mockReset().mockReturnValue({
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
    mockReconcileMutation.mockReset().mockReturnValue([
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

  const openPopulationExclusion = async (mode: 'normal' | 'refresh' | 'unrelated' | 'queued-refresh' = 'normal', deferRoutes = false) => {
    const actualReact = await vi.importActual<typeof import('../session/useBuilderLoad')>('../session/useBuilderLoad');
    (useGetExplorerBuilderStateV2Query as Mock).mockImplementation(actualReact.useGetExplorerBuilderStateV2Query);
    mockLoomClient.createSelection.mockReset();
    mockLoomClient.searchPopulationRoutes.mockReset();
    const makeSelection = (id: string): SelectionRevision => ({
      id, project: 'HTAN_INT/BForePC', generation: 'generation-1', resourceType: 'Specimen',
      rule: { kind: 'EXPLICIT' }, source: { kind: 'EXPLICIT_REFS', generation: 'generation-1' },
      scopeDigest: 'scope-1', ruleDigest: `rule-${id}`, membershipDigest: `members-${id}`,
      memberCount: 1, memberBytes: 32, complete: true, createdAt: '2026-09-21T00:00:00Z',
    });
    const selectionA = { ...makeSelection('selection-a'), memberCount: 2, memberBytes: 64 }, selectionB = makeSelection('selection-b');
    const removedRef = { project: 'HTAN_INT/BForePC', generation: 'generation-1', resourceType: 'Specimen', id: 'unmapped-a' };
    const variantA: SelectionRevision = { ...makeSelection('selection-a-excluded'), source: { kind: 'SELECTION_REVISION', revisionId: selectionA.id, generation: 'generation-1' }, exclusions: [removedRef] };
    const populationWorkspace = (id: string): ExplorerBuilderWorkspace => ({
      ...workspace, documents: [{ ...workspace.documents[0], population: { selectionRevisionId: id, route: [] } }],
    });
    const workspaceA = populationWorkspace(selectionA.id), workspaceB = populationWorkspace(selectionB.id);
    let currentServerWorkspace = workspaceA;
    const initial = { ...builderState, workspace: workspaceA };
    const newer = { ...builderState, workspace: workspaceB, draftVersion: 2, draftDigest: 'sha256:draft-2' };
    mockLoomClient.getBuilder.mockResolvedValueOnce(initial).mockImplementation(async () => {
      currentServerWorkspace = workspaceB;
      return newer;
    });
    mockLoomClient.getSelection.mockImplementation(async (args: { selectionRevision: string }) => {
      if (args.selectionRevision === variantA.id) return new Promise(() => undefined);
      return { revision: args.selectionRevision === selectionA.id ? selectionA : selectionB, members: [{ ref: { project: 'HTAN_INT/BForePC', generation: 'generation-1', resourceType: 'Specimen', id: args.selectionRevision === selectionB.id ? 'member-b' : 'member-a-kept' }, ordinal: 0 }] };
    });
    const routesFor = (id: string) => ({
      snapshotToken: 'snapshot-1', outputId: 'specimens', selectionRevisionId: id, complete: true, truncated: false,
      choices: [{ routeChoiceId: `route-${id}`, route: [], presentation: { summary: 'Direct Specimen rows', facts: [] } }],
    });
    let resolveRoutes: ((value: ReturnType<typeof routesFor> & { nextCursor?: string }) => void) | undefined;
    mockLoomClient.searchPopulationRoutes.mockImplementation(async (args: { selectionRevisionId: string }) => {
      if (deferRoutes && args.selectionRevisionId === variantA.id) {
        return new Promise<ReturnType<typeof routesFor> & { nextCursor?: string }>(resolve => { resolveRoutes = resolve; });
      }
      return routesFor(args.selectionRevisionId);
    });
    let resolveVariant!: (value: SelectionRevision) => void;
    mockLoomClient.createSelection.mockImplementation(() => new Promise<SelectionRevision>(resolve => { resolveVariant = resolve; }));
    reconcile.mockImplementation((args: { snapshotToken: string }) => resolvedRequest({ ...receipt, builder: currentServerWorkspace, snapshotToken: args.snapshotToken }));
    const mapping = vi.fn().mockReturnValue(resolvedRequest({
      status: 'COMPLETE', binding: { receiptId: 'receipt-1', outputId: 'specimens', project: selectionA.project, explorerId: 'test', generation: selectionA.generation, scopeDigest: selectionA.scopeDigest, selectionRevisionId: selectionA.id, membershipDigest: selectionA.membershipDigest, resourceType: selectionA.resourceType },
      counts: { selected: 2, mapped: 1, unmapped: 1 }, unmapped: [removedRef],
    }));
    (usePopulationMappingMutation as Mock).mockReturnValue([mapping, { isLoading: false }]);
    const attached = () => {
      currentServerWorkspace = populationWorkspace(variantA.id);
      return resolvedRequest({ commandId: 'attach-variant', workspace: currentServerWorkspace, draftVersion: mode === 'normal' ? 2 : 3, draftDigest: mode === 'normal' ? 'sha256:draft-2' : 'sha256:draft-3', results: [], diagnostics: [] });
    };
    applyCommands.mockImplementation(attached);
    if (mode === 'refresh') applyCommands.mockReturnValueOnce(rejectedRequest({ code: 'DRAFT_CONFLICT', message: 'Another writer attached selection B.', retryable: false }));
    let rejectQueuedCommand: (reason: unknown) => void = () => undefined;
    if (mode === 'queued-refresh') {
      const pending = new Promise<ExplorerBuilderCommandsResult>((_resolve, reject) => { rejectQueuedCommand = reject; });
      applyCommands.mockReturnValueOnce({ unwrap: () => pending, abort: vi.fn() });
    }
    if (mode === 'unrelated') applyCommands.mockReturnValueOnce(resolvedRequest({ commandId: 'save-column', workspace: workspaceA, draftVersion: 2, draftDigest: 'sha256:draft-2', results: [], diagnostics: [] }));
    const view = render(<BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />);
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
    await openRowSettings();
    const dialog = await findRowSettings();
    const panel = await within(dialog).findByRole('region', { name: 'Starting collection' });
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Check selected-resource coverage' })).toBeEnabled());
    fireEvent.click(within(panel).getByRole('button', { name: 'Check selected-resource coverage' }));
    const exclude = await within(panel).findByRole('button', { name: 'Remove from collection' });
    expect(exclude).toBeEnabled();
    fireEvent.click(exclude);
    await waitFor(() => expect(mockLoomClient.createSelection).toHaveBeenCalledTimes(1));
    return { view, selectionA, selectionB, variantA, routesFor, resolveVariant, rejectQueuedCommand, resolveRoutes: (value: ReturnType<typeof routesFor> & { nextCursor?: string }) => resolveRoutes?.(value), dialog, panel };
  };

  const finishPopulationVariant = async (resolve: () => void) => {
    await act(async () => { resolve(); await new Promise(done => setTimeout(done, 25)); });
  };

  const refreshPopulationByColumnEdit = async (dialog: HTMLElement, selectionB: SelectionRevision) => {
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close row settings' }));
    expect(screen.getByRole('button', { name: 'Change relationship' })).toBeEnabled();
    await saveWorkspaceEdit();
    await waitFor(() => expect(mockLoomClient.getBuilder).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-current-draft-version', '2'));
    await openRowSettings();
    const refreshedDialog = await findRowSettings();
    await waitFor(() => expect(within(refreshedDialog).getByRole('region', { name: 'Starting collection' })).toHaveAttribute('data-attached-selection-revision-id', selectionB.id));
  };

  it.each(schedules)('$name', async (schedule) => {
    const mode = schedule.mode === 'current' ? 'normal' : schedule.mode === 'base-change' ? 'refresh' : 'unrelated';
    const { selectionB, variantA, resolveVariant, dialog } = await openPopulationExclusion(mode);
    if (schedule.mode === 'unrelated-save') {
      fireEvent.click(within(dialog).getByRole('button', { name: 'Close row settings' }));
      await saveWorkspaceEdit();
      await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-current-draft-version', '2'));
    } else if (schedule.mode === 'base-change') {
      await refreshPopulationByColumnEdit(dialog, selectionB);
    }
    await finishPopulationVariant(() => resolveVariant(variantA));
    if (!schedule.expectedSubmit) {
      expect(applyCommands).toHaveBeenCalledTimes(1);
      expect(mockLoomClient.searchPopulationRoutes.mock.calls.filter(call => call[0].selectionRevisionId === variantA.id)).toHaveLength(0);
      return;
    }
    const expectedCalls = schedule.mode === 'unrelated-save' ? 2 : 1;
    const expectedDigest = schedule.mode === 'unrelated-save' ? 'sha256:draft-2' : 'sha256:draft-1';
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(expectedCalls));
    const commandCall = applyCommands.mock.calls[expectedCalls - 1][0];
    expect(commandCall).toMatchObject({ expectedDraftVersion: schedule.expectedDraftVersion, expectedDraftDigest: expectedDigest, commands: [{ type: 'SET_TABLE_POPULATION', selectionRevisionId: variantA.id }] });
    if (schedule.mode === 'current') {
      await waitFor(() => expect(screen.getByRole('region', { name: 'Starting collection' })).toHaveAttribute('data-attached-selection-revision-id', variantA.id));
    }
  });

  it('rechecks admission after a queued command changes the server head', async () => {
    const { variantA, selectionB, resolveVariant, rejectQueuedCommand, dialog } = await openPopulationExclusion('queued-refresh');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close row settings' }));
    await saveWorkspaceEdit();
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await finishPopulationVariant(() => resolveVariant(variantA));
    expect(mockLoomClient.searchPopulationRoutes.mock.calls.filter(call => call[0].selectionRevisionId === variantA.id)).toHaveLength(1);
    expect(applyCommands).toHaveBeenCalledTimes(1);
    await finishPopulationVariant(() => rejectQueuedCommand({ code: 'DRAFT_CONFLICT', message: 'Another writer attached B before this queued edit.', retryable: false }));
    await waitFor(() => expect(mockLoomClient.getBuilder).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-current-draft-version', '2'));
    await openRowSettings();
    const refreshedDialog = await findRowSettings();
    await waitFor(() => expect(within(refreshedDialog).getByRole('region', { name: 'Starting collection' })).toHaveAttribute('data-attached-selection-revision-id', selectionB.id));
    expect(applyCommands).toHaveBeenCalledTimes(1);
    expect(mockLoomClient.searchPopulationRoutes.mock.calls.filter(call => call[0].selectionRevisionId === variantA.id)).toHaveLength(1);
    expect(mockLoomClient.createSelection).toHaveBeenCalledTimes(1);
  });

  it('does not continue a pending exclusion after its Builder owner detaches', async () => {
    const { view, variantA, resolveVariant } = await openPopulationExclusion();
    view.unmount();
    await finishPopulationVariant(() => resolveVariant(variantA));
    expect(mockLoomClient.searchPopulationRoutes.mock.calls.filter(call => call[0].selectionRevisionId === variantA.id)).toHaveLength(0);
    expect(applyCommands).not.toHaveBeenCalled();
  });

});
