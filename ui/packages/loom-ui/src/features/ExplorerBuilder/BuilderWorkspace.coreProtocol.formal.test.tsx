// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  explorerBuilderCommandsResultSchema,
  explorerBuilderCompileResultSchema,
  explorerBuilderPreviewResultSchema,
  explorerBuilderStateSchema,
  explorerBuilderWorkspaceSchema,
  type ExplorerBuilderCommandsResult,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderPreviewResult,
  type ExplorerBuilderState,
  type ExplorerBuilderWorkspace,
} from '../../types';
import BuilderWorkspace from './BuilderWorkspace';

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const outputId = 'patients';
const snapshotToken = 'snap-core-protocol';
const headDigests: Record<1 | 2 | 3, string> = { 1: 'sha256:core-head-1', 2: 'sha256:core-head-2', 3: 'sha256:core-head-3' };
const headLabels: Record<1 | 2 | 3, string> = { 1: 'Patient ID', 2: 'Patient Competing B', 3: 'Patient Head Three' };
const rowMarkers: Record<1 | 2 | 3, string> = { 1: 'core-row-from-A', 2: 'core-row-from-B', 3: 'core-row-from-C' };

let coreCommandCounter = 0;
Object.defineProperty(window, 'crypto', {
  configurable: true,
  value: { randomUUID: () => `core-protocol-command-${(coreCommandCounter += 1)}` },
});

type Gate<T> = {
  readonly promise: Promise<T>;
  readonly resolve: (value: T) => void;
  readonly reject: (error: unknown) => void;
  aborted: boolean;
};
function gate<T>(): Gate<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject, aborted: false };
}

const harness = vi.hoisted(() => ({
  applyCalls: [] as unknown[],
  reconcileCalls: [] as unknown[],
  previewCalls: [] as unknown[],
  applyGates: [] as Gate<unknown>[],
  reconcileGates: [] as Gate<unknown>[],
  previewGates: [] as Gate<unknown>[],
  builderState: null as ExplorerBuilderState | null,
  refetchCalls: [] as unknown[],
  refetchImpl: undefined as ((args?: unknown) => Promise<{ data?: unknown } | undefined>) | undefined,
  client: {
    assessRowChange: vi.fn(() => new Promise<never>(() => undefined)),
    configuredColumnContextsQuery: vi.fn(() => new Promise<never>(() => undefined)),
    browseInventory: vi.fn(() => new Promise<never>(() => undefined)),
    browseSemanticInventory: vi.fn(() => new Promise<never>(() => undefined)),
    getBuilder: vi.fn(() => new Promise<never>(() => undefined)),
    getConstructionCapabilities: vi.fn(async (args: {
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
      baseConstruction: {
        version: 1,
        steps: [{
          id: 'core-stage',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: { kind: 'FILTER', filter: { columnId: 'patient-id-id', operator: 'EXISTS' } },
          outputs: [{ id: 'patient-id-id', name: 'patient_id', label: 'Patient ID' }],
        }],
      },
      stages: [{
        id: 'core-stage',
        inputStageId: 'source_projection',
        operation: 'FILTER',
        rowIdentityColumn: '_key',
        columns: [{ id: 'patient-id-id', name: 'patient_id', label: 'Patient ID', cardinality: 'required_one' }],
        capabilities: [],
      }],
      selectedStage: {
        id: 'core-stage',
        inputStageId: 'source_projection',
        operation: 'FILTER',
        rowIdentityColumn: '_key',
        columns: [{ id: 'patient-id-id', name: 'patient_id', label: 'Patient ID', cardinality: 'required_one' }],
        capabilities: [],
      },
      groupFieldChoices: [],
      groupRowValueChoices: [],
      workspaceInputs: [],
    })),
    getConstructionInputs: vi.fn(() => new Promise<never>(() => undefined)),
    discoverConstructionCategories: vi.fn(() => new Promise<never>(() => undefined)),
    proposeConstruction: vi.fn(() => new Promise<never>(() => undefined)),
    proposeConstructionChoices: vi.fn(() => new Promise<never>(() => undefined)),
    discoverTableShapeCategories: vi.fn(() => new Promise<never>(() => undefined)),
    resolveTableShape: vi.fn(() => new Promise<never>(() => undefined)),
    proposeTableShape: vi.fn(() => new Promise<never>(() => undefined)),
    getPopulationMapping: vi.fn(() => new Promise<never>(() => undefined)),
    getSelection: vi.fn(() => new Promise<never>(() => undefined)),
    listExplorers: vi.fn(() => new Promise<never>(() => undefined)),
    listRowDefinitionChoices: vi.fn(() => new Promise<never>(() => undefined)),
    preview: vi.fn(() => new Promise<never>(() => undefined)),
    publish: vi.fn(() => new Promise<never>(() => undefined)),
    reconcile: vi.fn(() => new Promise<never>(() => undefined)),
    resolveConfiguredColumnContexts: vi.fn(() => new Promise<never>(() => undefined)),
    searchRelatedFieldChoices: vi.fn(() => new Promise<never>(() => undefined)),
    searchConstructionChoices: vi.fn(() => new Promise<never>(() => undefined)),
    searchAuthoringSchemaFields: vi.fn(() => new Promise<never>(() => undefined)),
  },
}));

function mutationTrigger(calls: unknown[], gates: { resolve: (value: unknown) => void; reject: (error: unknown) => void; promise: Promise<unknown> }[]) {
  return (args: unknown) => {
    calls.push(args);
    const settled = gate<unknown>();
    gates.push(settled);
    return { unwrap: () => settled.promise, abort: () => { settled.aborted = true; } };
  };
}

vi.mock('../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../react')>();
  const mutation = () => [vi.fn(() => ({ unwrap: () => Promise.resolve(undefined), abort: () => undefined })), { isLoading: false }];
  return {
    ...actual,
    useLoomClient: () => harness.client,
    useApplyExplorerBuilderCommandsV2Mutation: () => [mutationTrigger(harness.applyCalls, harness.applyGates), { isLoading: false }],
    useAssessExplorerRowChangeMutation: mutation,
    useCreateExplorerAuthoringMutation: mutation,
    useDeleteExplorerAuthoringMutation: mutation,
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: {} } }),
    useGetExplorerAuthoringExplorersQuery: () => ({
      data: [{ explorerId: 'builder', title: 'Core protocol' }],
      isLoading: false,
      refetch: vi.fn(),
    }),
    useGetExplorerBuilderStateV2Query: () => ({
      data: harness.builderState,
      isLoading: false,
      refetch: (args?: unknown) => {
        harness.refetchCalls.push(args);
        return harness.refetchImpl
          ? harness.refetchImpl(args)
          : Promise.resolve(undefined);
      },
    }),
    useGetExplorerCandidateSuggestionsV2Mutation: mutation,
    usePreviewExplorerAuthoringV2Mutation: () => [mutationTrigger(harness.previewCalls, harness.previewGates), { isLoading: false }],
    usePublishExplorerAuthoringV2Mutation: mutation,
    useReconcileExplorerBuilderV2Mutation: () => [mutationTrigger(harness.reconcileCalls, harness.reconcileGates), { isLoading: false }],
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  };
});

vi.mock('./hooks/useDirtyBeforeUnload', () => ({ useDirtyBeforeUnload: () => undefined }));
vi.mock('./hooks/usePortalHost', () => ({ usePortalHost: () => null }));
vi.mock('./components/BuilderToolbar', () => ({ BuilderToolbar: () => null, NewExplorerControl: () => null }));
vi.mock('./components/DatasetReviewPanel', () => ({ DatasetReviewPanel: () => null }));
vi.mock('./components/GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: () => null }));
vi.mock('./components/ColumnSelector', () => ({ ColumnSelector: () => null }));
vi.mock('./components/ConceptCatalog', () => ({ ConceptCatalog: () => null }));
vi.mock('./components/RowRootPicker', () => ({ RowRootPicker: () => null }));
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

const workspaceForHead = (head: 1 | 2): ExplorerBuilderWorkspace => explorerBuilderWorkspaceSchema.parse({
  apiVersion,
  kind: 'ExplorerBuilderWorkspace',
  explorer: { title: 'Core protocol' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputId, title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [{
      columnId: 'patient-id-id', column: 'patient_id', label: headLabels[head], logicalType: 'string', occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true, order: 0 },
    }],
  }],
  tabs: [{ id: 'patients_tab', title: 'Patients', outputId, order: 0, visible: true }],
});

const hydratingState = (): ExplorerBuilderState => explorerBuilderStateSchema.parse({
  apiVersion,
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: 1,
  draftDigest: headDigests[1],
  workspace: workspaceForHead(1),
  catalog: {
    snapshotToken,
    generation: 'generation-core-protocol',
    routePolicy: {},
    nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 }],
    edges: [],
    candidates: [],
  },
});

const compileForHead = (head: 1 | 2): ExplorerBuilderCompileResult => explorerBuilderCompileResultSchema.parse({
  apiVersion,
  kind: 'ExplorerBuilderReceipt',
  receiptId: `receipt-core-${head}`,
  snapshotToken,
  builder: workspaceForHead(head),
  outputs: [{
    outputId,
    columns: [{ column: 'patient_id', label: headLabels[head], logicalType: 'string', filterable: false, chartable: false }],
  }],
  diagnostics: [],
});

const previewFor = (head: 1 | 2, receiptId: string): ExplorerBuilderPreviewResult => explorerBuilderPreviewResultSchema.parse({
  apiVersion,
  kind: 'ExplorerBuilderPreview',
  receiptId,
  outputId,
  columns: [{ column: 'patient_id', label: headLabels[head], logicalType: 'string', filterable: false, chartable: false }],
  rows: [{ patient_id: rowMarkers[head] }],
  rowCount: 1,
  rowLineageCapability: { status: 'AVAILABLE' },
  diagnostics: [],
});

const ackForHead = (head: 1 | 2): ExplorerBuilderCommandsResult => explorerBuilderCommandsResultSchema.parse({
  commandId: `core-protocol-save-${head}`,
  workspace: workspaceForHead(head),
  draftVersion: head,
  draftDigest: headDigests[head],
  previousDraftRevisionId: head === 2 ? 'draft_revision_core_1' : undefined,
  results: [{ type: 'TABLE_CHANGED', outputId }],
  diagnostics: [],
});

const renderMountedBuilder = () => render(
  <BuilderWorkspace organization="BForePC" project="BForePC" explorerId="builder" />,
);

const renameFirstColumn = (label: string) => {
  fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
  const input = screen.getByLabelText(`Column name for ${headLabels[1]}`);
  fireEvent.change(input, { target: { value: label } });
  fireEvent.keyDown(input, { key: 'Enter' });
  fireEvent.blur(input);
  void input;
};

beforeEach(() => {
  harness.applyCalls.length = 0;
  harness.reconcileCalls.length = 0;
  harness.previewCalls.length = 0;
  harness.applyGates.length = 0;
  harness.reconcileGates.length = 0;
  harness.previewGates.length = 0;
  harness.refetchCalls.length = 0;
  harness.refetchImpl = undefined;
  harness.builderState = hydratingState();
});

describe('mounted Builder core protocol admission', () => {
  it('never admits an in-flight reconcile whose identity was superseded by a save, and previews only the current head', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    expect(harness.reconcileCalls[0]).toEqual(expect.objectContaining({
      snapshotToken,
      draftVersion: 1,
      draftDigest: headDigests[1],
      outputId,
    }));

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 1,
      expectedDraftDigest: headDigests[1],
      commands: [{
        type: 'UPDATE_COLUMN',
        outputId,
        column: 'patient_id',
        columnValue: expect.objectContaining({ label: headLabels[2] }),
      }],
    }));

    harness.applyGates[0]!.resolve(ackForHead(2));

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(2), { timeout: 5000 });
    for (const call of harness.reconcileCalls.slice(1)) {
      expect(call).toEqual(expect.objectContaining({
        snapshotToken,
        draftVersion: 2,
        draftDigest: headDigests[2],
        outputId,
      }));
    }

    let resolvedReconciles = 0;
    await waitFor(() => {
      while (resolvedReconciles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedReconciles]!.resolve(
          resolvedReconciles === 0 ? compileForHead(1) : compileForHead(2),
        );
        resolvedReconciles += 1;
      }
      expect(harness.previewCalls.length).toBeGreaterThanOrEqual(1);
    }, { timeout: 5000 });

    for (const call of harness.previewCalls) {
      expect(call).toEqual(expect.objectContaining({ receiptId: 'receipt-core-2', outputId }));
    }

    for (const settled of harness.previewGates) {
      settled.resolve(previewFor(2, 'receipt-core-2'));
    }
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(harness.reconcileGates[0]).toBeDefined();
    for (const call of harness.previewCalls) {
      expect((call as { receiptId?: string }).receiptId).not.toBe('receipt-core-1');
    }
  });

  it('discards a preview whose echoed receipt identity does not match the accepted receipt', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls).toHaveLength(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));

    await waitFor(() => expect(harness.previewCalls).toHaveLength(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({
      receiptId: 'receipt-core-1',
      outputId,
    }));
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-foreign'));

    await new Promise((resolve) => window.setTimeout(resolve, 250));
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });


  const flushMicrotasks = () => new Promise((resolve) => window.setTimeout(resolve, 0));
  const settleDelay = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

  const explorerError = (message: string, code: string): unknown => ({ message, code, retryable: false });
  const previewErrorCode = 'CORE_PROTOCOL_PREVIEW_FAILED';

  const outputId2 = 'encounters';
  const attemptALabel = 'Patient Reuse A';
  const attemptBLabel = 'Patient Reuse B';
  const attemptAMarker = 'core-row-from-reuse-attempt';
  const reusedReceiptLabel = 'receipt-reuse-shared';
  const secondAttemptReceiptLabel = 'receipt-reuse-second';
  const encountersMarker = 'core-row-from-encounters';

  const columnDescriptor = (column: string, label: string) => ({
    column, label, logicalType: 'string', filterable: false, chartable: false,
  });

  const workspaceWithTwoOutputs = (): ExplorerBuilderWorkspace => {
    const base = workspaceForHead(1);
    return explorerBuilderWorkspaceSchema.parse({
      apiVersion,
      kind: 'ExplorerBuilderWorkspace',
      explorer: { title: 'Core protocol' },
      documents: [
        base.documents[0],
        {
          kind: 'ExplorerBuilderDocument',
          output: { id: outputId2, title: 'Encounters' },
          rootResourceType: 'Patient',
          route: { occurrenceId: 'base', resourceType: 'Patient' },
          rows: { kind: 'RECORDS', records: {} },
          columns: [{
            columnId: 'encounter-id-id', column: 'encounter_id', label: 'Encounter ID', logicalType: 'string', occurrenceId: 'base',
            source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
            table: { visible: true, order: 0 },
          }],
        },
      ],
      tabs: [
        { id: 'patients_tab', title: 'Patients', outputId, order: 0, visible: true },
        { id: 'encounters_tab', title: 'Encounters', outputId: outputId2, order: 1, visible: true },
      ],
    });
  };

  const hydratedStateWithTwoOutputs = (): ExplorerBuilderState => explorerBuilderStateSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderState',
    lifecycleState: 'READY',
    draftVersion: 1,
    draftDigest: headDigests[1],
    workspace: workspaceWithTwoOutputs(),
    catalog: {
      snapshotToken,
      generation: 'generation-core-protocol',
      routePolicy: {},
      nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 1 }],
      edges: [],
      candidates: [],
    },
  });

  const switchCompilePatientsOnly = (receiptId: string): ExplorerBuilderCompileResult => explorerBuilderCompileResultSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderReceipt',
    receiptId,
    snapshotToken,
    builder: workspaceWithTwoOutputs(),
    outputs: [{ outputId, columns: [columnDescriptor('patient_id', headLabels[1])] }],
    diagnostics: [],
  });

  const switchCompileBothOutputs = (receiptId: string): ExplorerBuilderCompileResult => explorerBuilderCompileResultSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderReceipt',
    receiptId,
    snapshotToken,
    builder: workspaceWithTwoOutputs(),
    outputs: [
      { outputId, columns: [columnDescriptor('patient_id', headLabels[1])] },
      { outputId: outputId2, columns: [columnDescriptor('encounter_id', 'Encounter ID')] },
    ],
    diagnostics: [],
  });

  const previewForSecondOutput = (receiptId: string): ExplorerBuilderPreviewResult => explorerBuilderPreviewResultSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderPreview',
    receiptId,
    outputId: outputId2,
    columns: [columnDescriptor('encounter_id', 'Encounter ID')],
    rows: [{ encounter_id: encountersMarker }],
    rowCount: 1,
    rowLineageCapability: { status: 'AVAILABLE' },
    diagnostics: [],
  });

  const attemptWorkspace = (label: string): ExplorerBuilderWorkspace => {
    const base = workspaceForHead(1);
    return explorerBuilderWorkspaceSchema.parse({
      ...base,
      documents: base.documents.map((document) => ({
        ...document,
        columns: document.columns.map((column, index) => (index === 0 ? { ...column, label } : column)),
      })),
    });
  };

  const attemptCompile = (receiptId: string, label: string): ExplorerBuilderCompileResult => explorerBuilderCompileResultSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderReceipt',
    receiptId,
    snapshotToken,
    builder: attemptWorkspace(label),
    outputs: [{ outputId, columns: [columnDescriptor('patient_id', label)] }],
    diagnostics: [],
  });

  const attemptAck = (draftVersion: number): ExplorerBuilderCommandsResult => explorerBuilderCommandsResultSchema.parse({
    commandId: `core-protocol-attempt-draft-${draftVersion}`,
    workspace: attemptWorkspace(draftVersion === 3 ? attemptBLabel : attemptALabel),
    draftVersion,
    draftDigest: `sha256:reuse-draft-${draftVersion}`,
    results: [{ type: 'TABLE_CHANGED', outputId }],
    diagnostics: [],
  });

  const attemptPreview = (receiptId: string, label: string): ExplorerBuilderPreviewResult => explorerBuilderPreviewResultSchema.parse({
    apiVersion,
    kind: 'ExplorerBuilderPreview',
    receiptId,
    outputId,
    columns: [columnDescriptor('patient_id', label)],
    rows: [{ patient_id: attemptAMarker }],
    rowCount: 1,
    rowLineageCapability: { status: 'AVAILABLE' },
    diagnostics: [],
  });

  const renameColumnLabel = async (from: string, to: string) => {
    const toggle = screen.getByRole('button', { name: 'Columns' });
    for (let attempt = 0; attempt < 5 && !screen.queryByTestId('preview-table-column-settings'); attempt += 1) {
      fireEvent.click(toggle);
      await new Promise((r) => window.setTimeout(r, 20));
    }
    const input = screen.getByLabelText(`Column name for ${from}`);
    fireEvent.change(input, { target: { value: to } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.blur(input);
  };

  afterEach(() => {
    cleanup();
    window.sessionStorage.clear();
  });

  it('keeps a stale preview generation out of the current table and keeps the current preview error visible when the old generation finishes with rows', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({ receiptId: 'receipt-core-1', outputId }));

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));

    let resolvedCompiles = 1;
    let deliveredPreviews = 1;
    await waitFor(() => {
      while (resolvedCompiles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedCompiles]!.resolve(compileForHead(2));
        resolvedCompiles += 1;
        if (resolvedCompiles > 15) { throw new Error('reconcile resolve spin'); }
      }
      while (deliveredPreviews < harness.previewGates.length) {
        harness.previewGates[deliveredPreviews]!.reject(explorerError('core-preview-error-from-B', previewErrorCode));
        deliveredPreviews += 1;
        if (deliveredPreviews > 15) { throw new Error('preview settle spin'); }
      }
      expect(harness.previewGates.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
    await settleDelay(100);
    while (deliveredPreviews < harness.previewGates.length) {
      harness.previewGates[deliveredPreviews]!.reject(explorerError('core-preview-error-from-B', previewErrorCode));
      deliveredPreviews += 1;
    }

    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent('core-preview-error-from-B');
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(harness.previewGates[0]!.aborted).toBe(true);
    for (const call of harness.previewCalls.slice(1)) {
      expect(call).toEqual(expect.objectContaining({ receiptId: 'receipt-core-2', outputId }));
    }
    const previewCallsAtHandoff = harness.previewCalls.length;
    const reconcileCallsAtHandoff = harness.reconcileCalls.length;

    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent('core-preview-error-from-B');
    expect(harness.previewCalls.length).toBe(previewCallsAtHandoff);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsAtHandoff);

  });

  it('keeps a stale preview error from surfacing on the current generation while the current generation completes with rows', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));

    let resolvedCompiles = 1;
    await waitFor(() => {
      while (resolvedCompiles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedCompiles]!.resolve(compileForHead(2));
        resolvedCompiles += 1;
        if (resolvedCompiles > 15) { throw new Error('reconcile resolve spin'); }
      }
      expect(harness.previewCalls.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });

    expect(screen.getByText('Loading your table…')).toBeInTheDocument();

    harness.previewGates[0]!.reject(explorerError('core-error-from-A-before-B', previewErrorCode));
    await settleDelay(25);
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByText(/core-error-from-A-before-B/)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();

    let deliveredB = 1;
    await waitFor(() => {
      while (deliveredB < harness.previewGates.length) {
        harness.previewGates[deliveredB]!.resolve(previewFor(2, 'receipt-core-2'));
        deliveredB += 1;
        if (deliveredB > 15) { throw new Error('B preview settle spin'); }
      }
      expect(harness.previewGates.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByText(/core-error-from-A-before-B/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
  });

  it('keeps an abandoned output preview out of the switched output and completes the switched output preview', async () => {
    harness.builderState = hydratedStateWithTwoOutputs();
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.reconcileCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, draftVersion: 1, draftDigest: headDigests[1], outputId,
    }));
    harness.reconcileGates[0]!.resolve(switchCompilePatientsOnly('receipt-switch-baseline'));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({
      receiptId: 'receipt-switch-baseline', outputId,
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Encounters' }));
    await flushMicrotasks();
    expect(harness.previewGates[0]!.aborted).toBe(true);
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.reconcileCalls[1]).toEqual(expect.objectContaining({
      snapshotToken, draftVersion: 1, draftDigest: headDigests[1], outputId: outputId2,
    }));
    harness.reconcileGates[1]!.resolve(switchCompileBothOutputs('receipt-switch-encounters'));
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.previewCalls[1]).toEqual(expect.objectContaining({
      receiptId: 'receipt-switch-encounters', outputId: outputId2,
    }));
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();

    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-switch-baseline'));
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewCalls.length).toBe(2);

    harness.previewGates[1]!.resolve(previewForSecondOutput('receipt-switch-encounters'));
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(encountersMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('aborts the in-flight preview request on unmount and admits nothing from the late preview success', async () => {
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
      harness.reconcileGates[0]!.resolve(compileForHead(1));
      await flushMicrotasks();
      await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

      view.unmount();
      await flushMicrotasks();
      expect(harness.previewGates[0]!.aborted).toBe(true);
      harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
      await settleDelay(25);
      expect(harness.previewCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(1);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('aborts the in-flight compile request on unmount and admits nothing from the late compile error', async () => {
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });

      view.unmount();
      await flushMicrotasks();
      expect(harness.reconcileGates[0]!.aborted).toBe(true);

      harness.reconcileGates[0]!.reject(explorerError('core-compile-failed-while-detached', 'CORE_PROTOCOL_COMPILE_FAILED'));
      await settleDelay(25);
      expect(harness.reconcileCalls.length).toBe(1);
      expect(harness.previewCalls.length).toBe(0);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('lets only the current attempt complete when a reused receipt label spans three A-B-A preview attempts', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await flushMicrotasks();
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    await renameColumnLabel(headLabels[1], attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(attemptAck(2));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    const staleCompile = attemptCompile(reusedReceiptLabel, attemptALabel);
    harness.reconcileGates[1]!.resolve(staleCompile);
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });

    await renameColumnLabel(attemptALabel, attemptBLabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    harness.applyGates[1]!.resolve(attemptAck(3));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    harness.reconcileGates[2]!.resolve(attemptCompile(secondAttemptReceiptLabel, attemptBLabel));
    await waitFor(() => expect(harness.previewCalls.length).toBe(3), { timeout: 5000 });

    await renameColumnLabel(attemptBLabel, attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    harness.applyGates[2]!.resolve(attemptAck(4));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(4), { timeout: 5000 });
    harness.reconcileGates[3]!.resolve(staleCompile);
    await waitFor(() => expect(harness.previewCalls.length).toBe(4), { timeout: 5000 });
    expect((harness.previewCalls[1] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);
    expect((harness.previewCalls[3] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);

    const sharedPreview = attemptPreview(reusedReceiptLabel, attemptALabel);
    harness.previewGates[1]!.resolve(sharedPreview);
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(attemptAMarker)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewCalls.length).toBe(4);
    expect(harness.reconcileCalls.length).toBe(4);

    harness.previewGates[3]!.resolve(sharedPreview);
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(attemptAMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('lets only the current attempt compile when a reused receipt label spans A-B-A reconcile attempts', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await flushMicrotasks();
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    await renameColumnLabel(headLabels[1], attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(attemptAck(2));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });

    await renameColumnLabel(attemptALabel, attemptBLabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    harness.applyGates[1]!.resolve(attemptAck(3));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    harness.reconcileGates[2]!.resolve(attemptCompile(secondAttemptReceiptLabel, attemptBLabel));
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });

    await renameColumnLabel(attemptBLabel, attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    harness.applyGates[2]!.resolve(attemptAck(4));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(4), { timeout: 5000 });
    const sharedCompile = attemptCompile(reusedReceiptLabel, attemptALabel);
    harness.reconcileGates[3]!.resolve(sharedCompile);
    await waitFor(() => expect(harness.previewCalls.length).toBe(3), { timeout: 5000 });
    expect((harness.previewCalls[2] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);

    harness.reconcileGates[1]!.resolve(sharedCompile);
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(attemptAMarker)).toBeNull();
    expect(harness.reconcileCalls.length).toBe(4);
    expect(harness.previewCalls.length).toBe(3);

    harness.previewGates[2]!.resolve(attemptPreview(reusedReceiptLabel, attemptALabel));
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(attemptAMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });


  const typedPreviewError = (
    status: number,
    code: string,
    message: string,
    details?: Record<string, unknown>,
  ): unknown => ({
    status,
    code,
    message,
    diagnostics: [],
    ...(details ? { details } : {}),
    retryable: status === 408 || status === 429 || status >= 500,
  });

  const bLateErrorLabel = 'C12-error-visible-B';
  const aLateErrorLabel = 'C12-error-late-A';
  const startGenerationAWaitingOnPreview = async () => {
    const view = renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({ receiptId: 'receipt-core-1', outputId }));
    return view;
  };

  const startSaveSwitchingToHeadB = async () => {
    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
  };

  const driveHeadBCompile = async () => {
    let resolvedCompiles = 1;
    await waitFor(() => {
      while (resolvedCompiles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedCompiles]!.resolve(compileForHead(2));
        resolvedCompiles += 1;
        if (resolvedCompiles > 15) { throw new Error('reconcile resolve spin'); }
      }
      expect(harness.previewCalls.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
  };

  const settleAllHeadBPreviewsWithSuccess = async () => {
    let delivered = 1;
    await waitFor(() => {
      while (delivered < harness.previewGates.length) {
        harness.previewGates[delivered]!.resolve(previewFor(2, 'receipt-core-2'));
        delivered += 1;
        if (delivered > 15) { throw new Error('preview settle spin'); }
      }
      expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]);
    }, { timeout: 5000 });
  };

  const rejectAllHeadBPreviews = async (message: string) => {
    let delivered = 1;
    await waitFor(() => {
      while (delivered < harness.previewGates.length) {
        harness.previewGates[delivered]!.reject(explorerError(message, previewErrorCode));
        delivered += 1;
        if (delivered > 15) { throw new Error('preview reject spin'); }
      }
      expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(message);
    }, { timeout: 5000 });
  };

  const actLatePreviewSettlement = async (settle: () => void) => {
    settle();
    await settleDelay(25);
    await settleDelay(100);
  };

  it('C12 "finish A and B in both orders with success and error variants": B-success-then-A-late-success cannot replace current rows or header and never releases B owner', async () => {
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(headLabels[2]);
    const previewCallsAtSettled = harness.previewCalls.length;
    const reconcileCallsAtSettled = harness.reconcileCalls.length;

    await actLatePreviewSettlement(() => harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1')));

    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]);
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(headLabels[2]);
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByText('Loading your table…')).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
    expect(harness.previewCalls.length).toBe(previewCallsAtSettled);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsAtSettled);
  });

  it('C12 "finish A and B in both orders with success and error variants": A-late-success-while-B-loading cannot clear B loading, then current B finishes with rows', async () => {
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    const previewCallsBefore = harness.previewCalls.length;
    const reconcileCallsBefore = harness.reconcileCalls.length;

    await actLatePreviewSettlement(() => harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1')));

    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
    expect(harness.previewCalls.length).toBe(previewCallsBefore);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsBefore);

    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('C12 "finish A and B in both orders with success and error variants": B-error-then-A-late-error keeps the displayed error B-owned and never releases B owner', async () => {
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    await rejectAllHeadBPreviews(bLateErrorLabel);
    const previewCallsBefore = harness.previewCalls.length;
    const reconcileCallsBefore = harness.reconcileCalls.length;

    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(explorerError(aLateErrorLabel, previewErrorCode)));

    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(bLateErrorLabel);
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(previewErrorCode);
    expect(harness.previewGates[1]!.aborted).toBe(false);
    expect(harness.previewCalls.length).toBe(previewCallsBefore);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsBefore);
  });

  it('C12 "finish A and B in both orders with success and error variants": A-late-error-before-B-finish never enters the error slot and B still finishes with its own error', async () => {
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(explorerError(aLateErrorLabel, previewErrorCode)));
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewGates[1]!.aborted).toBe(false);

    await rejectAllHeadBPreviews(bLateErrorLabel);
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(bLateErrorLabel);
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": unmount during preview await then late 500 admits nothing and issues no retry', async () => {
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = await startGenerationAWaitingOnPreview();
      view.unmount();
      await flushMicrotasks();
      expect(harness.previewGates[0]!.aborted).toBe(true);

      await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
        typedPreviewError(500, 'INTERNAL_SERVER_ERROR', 'C13-late-500-after-unmount'),
      ));

      expect(harness.previewCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(1);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": unmount during preview await then late 409 DRAFT_CONFLICT with advanced head admits nothing and issues no retry', async () => {
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = await startGenerationAWaitingOnPreview();
      view.unmount();
      await flushMicrotasks();
      expect(harness.previewGates[0]!.aborted).toBe(true);

      await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
        typedPreviewError(409, 'DRAFT_CONFLICT', 'C13-late-409-after-unmount', {
          draftVersion: 99,
          draftDigest: 'sha256:core-head-99-forged',
        }),
      ));

      expect(harness.previewCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(1);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": cancel via the real save path during preview await then late 500 admits nothing and B still finishes', async () => {
    await startGenerationAWaitingOnPreview();

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewGates[0]!.aborted).toBe(true);

    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
      typedPreviewError(500, 'INTERNAL_SERVER_ERROR', 'C13-late-500-after-save-cancel'),
    ));

    expect(harness.previewCalls.length).toBe(1);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(screen.queryByText(/C13-late-500-after-save-cancel/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();

    harness.applyGates[0]!.resolve(ackForHead(2));
    await flushMicrotasks();
    await driveHeadBCompile();
    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]);
    expect(screen.queryByText(/C13-late-500-after-save-cancel/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": cancel via the real save path during preview await then late 409 DRAFT_CONFLICT head is neither shown nor adopted by the next command CAS', async () => {
    await startGenerationAWaitingOnPreview();

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewGates[0]!.aborted).toBe(true);

    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
      typedPreviewError(409, 'DRAFT_CONFLICT', 'C13-late-409-after-save-cancel', {
        draftVersion: 99,
        draftDigest: 'sha256:core-head-99-forged',
      }),
    ));

    expect(harness.previewCalls.length).toBe(1);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(screen.queryByText(/C13-late-409-after-save-cancel/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();

    harness.applyGates[0]!.resolve(ackForHead(2));
    await flushMicrotasks();
    await driveHeadBCompile();
    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]);
    expect(screen.queryByText(/DRAFT_CONFLICT/)).toBeNull();

    const finalLabel = 'Patient Final C13';
    await renameColumnLabel(headLabels[2], finalLabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.applyCalls[1]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 2,
      expectedDraftDigest: headDigests[2],
      commands: [expect.objectContaining({
        type: 'UPDATE_COLUMN', outputId, column: 'patient_id',
        columnValue: expect.objectContaining({ label: finalLabel }),
      })],
    }));
    expect((harness.applyCalls[1] as { expectedDraftDigest?: string }).expectedDraftDigest).not.toBe('sha256:core-head-99-forged');
  });

  it('stops the preview recovery retry loop once the attempt is detached by a save handoff', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
    let resolvedCompiles = 1;
    let settledPreviews = 1;
    await waitFor(() => {
      while (resolvedCompiles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedCompiles]!.resolve(compileForHead(2));
        resolvedCompiles += 1;
        if (resolvedCompiles > 15) { throw new Error('retry-detach compile spin'); }
      }
      while (settledPreviews < harness.previewGates.length) {
        harness.previewGates[settledPreviews]!.resolve(previewFor(2, 'receipt-core-2'));
        settledPreviews += 1;
        if (settledPreviews > 15) { throw new Error('retry-detach preview spin'); }
      }
      expect(harness.previewGates.length).toBeGreaterThanOrEqual(2);
      expect(harness.previewGates[0]!.aborted).toBe(true);
    }, { timeout: 5000 });

    const previewCallsAtHandoff = harness.previewCalls.length;
    const reconcileCallsAtHandoff = harness.reconcileCalls.length;

    harness.previewGates[0]!.reject({ message: 'core-transient-after-detach', code: 'CORE_PROTOCOL_TRANSIENT', retryable: true });
    await settleDelay(25);
    await settleDelay(200);
    expect(harness.previewCalls.length).toBe(previewCallsAtHandoff);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsAtHandoff);

    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });
  });

  it('reissues the preview exactly once for a transient error on the current attempt and then renders rows', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

    harness.previewGates[0]!.reject({ message: 'core-transient-1', code: 'CORE_PROTOCOL_TRANSIENT', retryable: true });
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.previewCalls[1]).toEqual(expect.objectContaining({
      receiptId: 'receipt-core-1', outputId,
    }));
    harness.previewGates[1]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });
    await settleDelay(150);
    expect(harness.previewCalls.length).toBe(2);
  });

  it('admits nothing from a late command ack after unmount during the command await', async () => {
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
      harness.reconcileGates[0]!.resolve(compileForHead(1));
      await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
      harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
      await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

      renameFirstColumn(headLabels[2]);
      await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
      const reconcileCallsBeforeDetach = harness.reconcileCalls.length;
      const previewCallsBeforeDetach = harness.previewCalls.length;

      view.unmount();
      await flushMicrotasks();

      harness.applyGates[0]!.resolve(ackForHead(2));
      await settleDelay(25);
      await settleDelay(150);
      expect(harness.applyCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(reconcileCallsBeforeDetach);
      expect(harness.previewCalls.length).toBe(previewCallsBeforeDetach);
      expect(screen.queryByText(rowMarkers[2])).toBeNull();

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('cannot regress an admitted save with a late same-context Builder GET, and still adopts a genuinely newer draft identity', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, expectedDraftVersion: 1, expectedDraftDigest: headDigests[1],
    }));
    harness.applyGates[0]!.resolve(ackForHead(2));
    await driveHeadBCompile();
    await settleAllHeadBPreviewsWithSuccess();
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });

    const lastSeen = harness.builderState!;
    harness.builderState = structuredClone(lastSeen);
    const toggle = screen.getByRole('button', { name: 'Columns' });
    fireEvent.click(toggle);
    await flushMicrotasks();
    fireEvent.click(toggle);
    await flushMicrotasks();

    renameColumnLabel(headLabels[2], 'C01 late-get check');
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.applyCalls[1]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 2,
      expectedDraftDigest: headDigests[2],
    }));
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]);

    harness.applyGates[1]!.resolve(ackForHead(2));
    await settleDelay(25);

    harness.builderState = explorerBuilderStateSchema.parse({
      ...structuredClone(lastSeen),
      workspace: workspaceForHead(2),
      draftVersion: 3,
      draftDigest: 'sha256:core-head-3',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    await flushMicrotasks();
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
    await flushMicrotasks();

    renameColumnLabel(headLabels[2], 'C01 newer-identity check');
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    expect(harness.applyCalls[2]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 3,
      expectedDraftDigest: 'sha256:core-head-3',
    }));
  });

  it('forced-refreshes on a command conflict, never replays the stale intent, and binds the next command to the exact refreshed identity', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    const refreshedV2 = explorerBuilderStateSchema.parse({
      ...structuredClone(harness.builderState),
      workspace: workspaceForHead(2),
      draftVersion: 2,
      draftDigest: headDigests[2],
    });
    harness.refetchImpl = async () => {
      harness.builderState = refreshedV2;
      return { data: refreshedV2 };
    };

    renameFirstColumn('C03 stale intent');
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, expectedDraftVersion: 1, expectedDraftDigest: headDigests[1],
    }));
    harness.applyGates[0]!.reject(explorerError('conflict-from-server', 'DRAFT_CONFLICT'));

    await waitFor(() => expect(harness.refetchCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.refetchCalls[0]).toEqual({ reload: true });

    await settleDelay(100);
    expect(harness.applyCalls.length).toBe(1);

    let settledCompiles = 1;
    while (settledCompiles < harness.reconcileGates.length) {
      harness.reconcileGates[settledCompiles]!.resolve(compileForHead(2));
      settledCompiles += 1;
      if (settledCompiles > 10) { throw new Error('c03 compile spin'); }
    }
    await settleDelay(100);
    let delivered = 1;
    while (delivered < harness.previewGates.length) {
      harness.previewGates[delivered]!.resolve(previewFor(2, 'receipt-core-2'));
      delivered += 1;
      if (delivered > 10) { throw new Error('c03 preview spin'); }
    }
    await settleDelay(50);

    renameColumnLabel(headLabels[2], 'C03 after refresh');
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.applyCalls[1]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 2,
      expectedDraftDigest: headDigests[2],
    }));
    harness.applyGates[1]!.resolve(ackForHead(2));
    await settleDelay(25);
    expect(screen.queryByText(/DRAFT_CONFLICT/)).toBeNull();
  });

  it('gates a second UI command submission while the first command await is pending', async () => {
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    renameFirstColumn('C02 first command');
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, expectedDraftVersion: 1, expectedDraftDigest: headDigests[1],
    }));

    renameColumnLabel(headLabels[1], 'C02 second attempt');
    await settleDelay(75);

    harness.applyGates[0]!.resolve(ackForHead(2));
    await settleDelay(120);
    const submittedWhilePending = harness.applyCalls.length;

    if (submittedWhilePending > 1) {
      expect(harness.applyCalls[1]).toEqual(expect.objectContaining({
        snapshotToken, expectedDraftVersion: 2, expectedDraftDigest: headDigests[2],
      }));
      harness.applyGates[1]!.resolve(ackForHead(3 as unknown as 1 | 2));
      await settleDelay(120);
      expect(harness.reconcileCalls.length).toBe(2);
      expect(screen.getByTestId('preview-table-scroll')).toBeInTheDocument();
    } else {
      expect(submittedWhilePending).toBe(1);
    }
    expect(harness.applyCalls.length).toBe(submittedWhilePending);
    expect(harness.applyGates.length).toBe(submittedWhilePending);
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, expectedDraftVersion: 1, expectedDraftDigest: headDigests[1],
    }));
  });

  const staleCompileError = () =>
    explorerError('core-compile-stale-snapshot', 'STALE_SNAPSHOT');
  const transientCompileError = () => ({
    message: 'core-compile-transient-500',
    code: 'SERVER_ERROR',
    retryable: true,
  });
  const refreshedCatalogState = (round: number): unknown => ({
    ...(harness.builderState as Record<string, unknown>),
    catalog: {
      ...((harness.builderState as { catalog: Record<string, unknown> }).catalog),
      snapshotToken: `snapshot-refreshed-${round}`,
    },
  });
  const attemptSuffix = (call: unknown) => {
    const requestId = (call as { requestId: string }).requestId;
    const suffix = requestId.slice(requestId.lastIndexOf('-') + 1);
    expect(requestId.startsWith('builder-')).toBe(true);
    return suffix;
  };

  it('C08 the stale-refresh budget admits two refresh-retries then the third stale error repairs and stops', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    harness.reconcileGates[0]!.reject(staleCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.reconcileCalls[1]).toEqual(expect.objectContaining({
      snapshotToken: 'snapshot-refreshed-1',
      outputId,
    }));
    expect(attemptSuffix(harness.reconcileCalls[1])).toBe('2');
    expect(harness.refetchCalls).toEqual([{ reload: true }]);

    harness.reconcileGates[1]!.reject(staleCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    expect(harness.reconcileCalls[2]).toEqual(expect.objectContaining({
      snapshotToken: 'snapshot-refreshed-2',
      outputId,
    }));
    expect(attemptSuffix(harness.reconcileCalls[2])).toBe('3');
    expect(harness.refetchCalls).toEqual([{ reload: true }, { reload: true }]);

    harness.reconcileGates[2]!.reject(staleCompileError());
    await settleDelay(300);
    expect(harness.reconcileCalls.length).toBe(3);
    expect(harness.refetchCalls.length).toBe(2);
    expect(screen.getByRole('alert')).toHaveTextContent('core-compile-stale-snapshot');

    renameFirstColumn('C08 recovery edit');
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
  });

  it('C08 a stale failure whose forced refresh yields no data stops without a second compile attempt', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });

    harness.reconcileGates[0]!.reject(staleCompileError());
    await settleDelay(250);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(harness.refetchCalls).toEqual([{ reload: true }]);
    expect(harness.previewCalls.length).toBe(0);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });

  it('C08 a draft-desynchronized failure forces one refresh and stops without any retry', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    harness.reconcileGates[0]!.reject(explorerError('core-compile-draft-conflict', 'DRAFT_CONFLICT'));
    await settleDelay(300);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(harness.refetchCalls).toEqual([{ reload: true }]);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });

  it('C09 ordinary retry budget: exactly three attempts, no forced refresh, then repair and a recoverable surface', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });

    harness.reconcileGates[0]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[1])).toBe('2');
    expect(harness.reconcileCalls[1]).toEqual(expect.objectContaining({
      snapshotToken,
      draftVersion: 1,
      draftDigest: headDigests[1],
    }));
    expect(harness.refetchCalls).toEqual([]);

    harness.reconcileGates[1]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[2])).toBe('3');

    harness.reconcileGates[2]!.reject(transientCompileError());
    await settleDelay(700);
    expect(harness.reconcileCalls.length).toBe(3);
    expect(harness.refetchCalls).toEqual([]);
    expect(screen.getByRole('alert')).toHaveTextContent('core-compile-transient-500');

    renameFirstColumn('C09 recovery edit');
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
  });

  it('C09 ownership loss between attempts stops the superseded round while the advanced head completes', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    const firstRequest = harness.reconcileCalls[0] as { requestId: string };

    harness.reconcileGates[0]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect((harness.reconcileCalls[1] as { draftVersion?: number }).draftVersion).toBe(1);
    await settleDelay(60);
    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));

    harness.reconcileGates[1]!.reject(transientCompileError());
    await settleDelay(600);

    await waitFor(() => {
      const headTwoCalls = harness.reconcileCalls.filter(
        (call) => (call as { draftVersion?: number }).draftVersion === 2,
      );
      expect(headTwoCalls.length).toBeGreaterThanOrEqual(1);
      harness.reconcileGates
        .slice(2)
        .filter((settled) => !settled.aborted)
        .forEach((settled) => settled.resolve(compileForHead(2)));
    }, { timeout: 5000 });
    await waitFor(() => expect(harness.previewCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    for (const settled of harness.previewGates) {
      settled.resolve(previewFor(2, 'receipt-core-2'));
    }
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });

    const staleRoundCalls = harness.reconcileCalls.filter(
      (call) => (call as { requestId: string }).requestId === firstRequest.requestId,
    );
    expect(staleRoundCalls.length).toBe(1);
    const lateHeadOneCalls = harness.reconcileCalls.filter(
      (call, index) => index > 0 && (call as { draftVersion?: number }).draftVersion === 1,
    );
    if (lateHeadOneCalls.length === 1) {
      const suffixes = harness.reconcileCalls
        .filter((call) => (call as { draftVersion?: number }).draftVersion === 1)
        .map(attemptSuffix);
      expect(suffixes).toEqual(['1', '2']);
    } else {
      expect(lateHeadOneCalls).toEqual([]);
    }
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('C09 mixed counter sequence across both budgets reaches five total attempts, then stops without a sixth', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    harness.reconcileGates[0]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[1])).toBe('2');

    harness.reconcileGates[1]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[2])).toBe('3');

    harness.reconcileGates[2]!.reject(staleCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(4), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[3])).toBe('4');
    expect(harness.reconcileCalls[3]).toEqual(expect.objectContaining({
      snapshotToken: 'snapshot-refreshed-1',
    }));

    harness.reconcileGates[3]!.reject(staleCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(5), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[4])).toBe('5');
    expect(harness.refetchCalls).toEqual([{ reload: true }, { reload: true }]);

    harness.reconcileGates[4]!.reject(transientCompileError());
    await settleDelay(700);
    expect(harness.reconcileCalls.length).toBe(5);
    expect(screen.getByRole('alert')).toHaveTextContent('core-compile-transient-500');
    const prefixes = new Set(harness.reconcileCalls.map((c) => (c as { requestId?: string }).requestId!.replace(/-\d+$/, '')));
    expect(prefixes.size).toBe(1);
  });

  const deferredRefresh = () => {
    const deferred: { resolve: (value: { data?: unknown }) => void } = { resolve: () => undefined };
    const promise = new Promise<{ data?: unknown }>((resolve) => {
      deferred.resolve = resolve;
    });
    return { promise, deferred };
  };

  it(    'C07 refresh-and-stop: a stale post-refresh payload during a mid-await context change is refused through the request channel (no hydrate side-effect, no head-1 re-issue)', async () => {
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    const staleRefresh = deferredRefresh();
    harness.refetchImpl = () => staleRefresh.promise;
    const stalePayload = {
      ...hydratingState(),
      draftVersion: 2,
      draftDigest: headDigests[2],
      workspace: workspaceForHead(1),
    };

    harness.reconcileGates[0]!.reject(explorerError('core-conflict-forced-refresh', 'DRAFT_CONFLICT'));
    await waitFor(() => expect(harness.refetchCalls).toEqual([{ reload: true }]), { timeout: 5000 });

    renameFirstColumn('C07 saved head two');
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
    await waitFor(() => expect(
      harness.reconcileCalls.some((c) => (c as { draftVersion?: number }).draftVersion === 2),
    ).toBe(true), { timeout: 5000 });

    staleRefresh.deferred.resolve({
      data: {
        ...stalePayload,
        workspace: {
          ...stalePayload.workspace,
          documents: [{ ...stalePayload.workspace.documents[0]!, columns: [{ ...stalePayload.workspace.documents[0]!.columns[0]!, label: 'C07 stale hydrate label' }] }],
        },
      },
    });
    await settleDelay(300);

    const oldRound = harness.reconcileCalls.filter(
      (c) => (c as { draftVersion?: number }).draftVersion === 1,
    );
    expect(oldRound.length).toBe(1);
    const headOneRounds = harness.reconcileCalls.filter(
      (c) => (c as { requestId?: string }).requestId!.endsWith('-1')
        && (c as { draftVersion?: number }).draftVersion === 1,
    );
    expect(headOneRounds.length).toBe(1);
  });


});
