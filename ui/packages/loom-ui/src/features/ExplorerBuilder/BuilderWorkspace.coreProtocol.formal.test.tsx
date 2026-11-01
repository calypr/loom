// @vitest-environment jsdom
/// <reference types="node" />
import { appendFileSync, writeFileSync } from 'node:fs';
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
  // Set by the mocked trigger's abort contract; observable boundary evidence
  // that BuilderWorkspace detached a request (react.tsx useMutation returns
  // { unwrap, abort } and BuilderWorkspace owns every abort call site).
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
  // Builder-query refetch control: production refetchBuilder({ reload: true })
  // callers (conflict/stale forced refresh at BuilderWorkspace.tsx:900 etc.)
  // must be able to resolve real data; the default keeps the legacy
  // undefined-resolving behaviour so existing cases are unaffected.
  refetchCalls: [] as unknown[],
  refetchImpl: undefined as ((args?: unknown) => Promise<{ data?: unknown } | undefined>) | undefined,
  client: {
    assessRowChange: vi.fn(() => new Promise<never>(() => undefined)),
    configuredColumnContextsQuery: vi.fn(() => new Promise<never>(() => undefined)),
    browseInventory: vi.fn(() => new Promise<never>(() => undefined)),
    browseSemanticInventory: vi.fn(() => new Promise<never>(() => undefined)),
    getBuilder: vi.fn(() => new Promise<never>(() => undefined)),
    // Resolves identity-echoed capabilities like the served flow:
    // useConstructionLifecycle.ts:203-216 treats an identity-matched echo as
    // 'ready'. A never-settling stub here deadlocks awaited act() whenever a
    // state update schedules this load (observed: stale-generation case hang).
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

// The mock triggers stand in for react.tsx mutation triggers; their payload and
// response types remain the real ones (schemas are parsed at construction).
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

let dbgTick: ReturnType<typeof setInterval> | undefined;
let dbgTag = 'untagged';
const tr = (m: string) => process.stderr.write(`M2J ${m}\n`);
const dbgWatch = (label: string) => {
  dbgTag = label;
  const t0 = Date.now();
  let n = 0;
  clearInterval(dbgTick);
  dbgTick = setInterval(() => {
    const statuses = Array.from(document.querySelectorAll('[role="status"]')).map((e) => e.textContent?.trim().slice(0, 90));
    const stubCalls = Object.entries(harness.client).filter(([, fn]) => typeof (fn as { mock?: { calls?: unknown } }).mock?.calls === 'object')
      .map(([name, fn]) => `${name}:${(fn as { mock: { calls: unknown[] } }).mock.calls.length}`)
      .filter((entry) => !entry.endsWith(':0')).join(',');
    const line = `${label} n=${n++} t=${Date.now() - t0}ms r=${harness.reconcileCalls.length} p=${harness.previewCalls.length} s=${harness.applyCalls.length} rg=${harness.reconcileGates.length} pg=${harness.previewGates.length} stubs=${stubCalls} statuses=${JSON.stringify(statuses)}\n`;
    appendFileSync('/tmp/opencode/dbg-spec-watch.log', line);
  }, 400);
};
const dbgDump = (name: string, extra?: unknown) => {
  const buttons = Array.from(document.querySelectorAll('button')).map((b) => ({ t: b.textContent?.trim().slice(0, 40), al: b.getAttribute('aria-label'), exp: b.getAttribute('aria-expanded'), testid: b.getAttribute('data-testid') }));
  const statuses = Array.from(document.querySelectorAll('[role="status"]')).map((e) => e.textContent?.trim().slice(0, 120));
  const previewContents = Array.from(document.querySelectorAll('[data-testid="preview-table-content"]')).map((e) => (e as HTMLElement).outerHTML.slice(0, 500));
  writeFileSync(`/tmp/opencode/dbg-spec-${name}.json`, JSON.stringify({ buttons, statuses, previewContents, extra: extra ?? null }, null, 1));
};
const renameFirstColumn = (label: string) => {
  try {
    fireEvent.click(screen.getByRole('button', { name: 'Columns' }));
  } catch (error) {
    dbgDump('renameFirstColumn-columns', String(error).slice(0, 200));
    throw error;
  }
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
    dbgTag = 'an';
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

    // The save commits while reconcile of head 1 is still in flight.
    harness.applyGates[0]!.resolve(ackForHead(2));

    // The current identity (head 2) starts its own reconcile; superseded refreshes
    // may re-issue it, and every such request carries the new head identity.
    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(2), { timeout: 5000 });
    for (const call of harness.reconcileCalls.slice(1)) {
      expect(call).toEqual(expect.objectContaining({
        snapshotToken,
        draftVersion: 2,
        draftDigest: headDigests[2],
        outputId,
      }));
    }

    // Head 1's compile finally completes late — after the head advanced to B.
    // Any live refresh keeps re-issuing the head-2 compile; answer them all.
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

    // Resolve previews; only a live refresh holding receipt-core-2 may be admitted,
    // and the stale head-1 receipt never reaches the preview boundary.
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
    dbgTag = 'preview';
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls).toHaveLength(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));

    await waitFor(() => expect(harness.previewCalls).toHaveLength(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({
      receiptId: 'receipt-core-1',
      outputId,
    }));
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-foreign'));

    // The mismatched preview never becomes the table's current preview.
    await new Promise((resolve) => window.setTimeout(resolve, 250));
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });

  // ---- C12/C13/C14 additions (CORE_PROTOCOL_VERIFICATION_PLAN.md rows) ----
  // PreviewTable admission semantics traced from source (asserted through the
  // rendered output, never through refs): while the automatic preview query
  // holds an error the surface renders <p role="status"> "Preview did not
  // complete for this draft: …" (PreviewTable.tsx:794-798); with no preview and
  // no error it renders "Loading your table…" (PreviewTable.tsx:800-805); rows
  // only exist inside data-testid="preview-table-content" (PreviewTable.tsx:808)
  // which BuilderWorkspace feeds exclusively from state.preview whose outputId
  // matches the selected table (BuilderWorkspace.tsx:1231-1232) and which the
  // reducer only populates through the full-identity previewAccepted gate
  // (authoring/reducer.ts:525-546).

  const flushMicrotasks = () => new Promise((resolve) => window.setTimeout(resolve, 0));
  const settleDelay = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

  const explorerError = (message: string, code: string): unknown => ({ message, code, retryable: false });
  // Terminal preview code: absent from previewRecovery.ts receipt/catalog/retry
  // sets, so executePreview publishes it instead of taking a recovery branch.
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

  // Receipt whose outputs exclude `encounters` so the switched output must
  // earn its own compile (cache reuse in BuilderWorkspace.tsx:3036-3040 is
  // gated on receipt.outputs containing the identity outputId).
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
    let toggle: HTMLElement | null = null;
    try {
      toggle = screen.getByRole('button', { name: 'Columns' });
    } catch (error) {
      dbgDump(`renameColumnLabel-columns-${from}-to-${to}`, String(error).slice(0, 200));
      throw error;
    }
    for (let attempt = 0; attempt < 5 && !screen.queryByTestId('preview-table-column-settings'); attempt += 1) {
      fireEvent.click(toggle);
      await new Promise((r) => window.setTimeout(r, 20));
      dbgDump(`renameColumnLabel-click-${to}-${attempt}`, { exp: toggle.getAttribute('aria-expanded') });
    }
    dbgDump(`renameColumnLabel-menu-${to}`, { menu: screen.queryByTestId('preview-table-column-settings') !== null, inputs: Array.from(document.querySelectorAll('input')).map((i) => i.getAttribute('aria-label')).filter((l) => l && l.startsWith('Column name')).join('|') });
    const input = screen.getByLabelText(`Column name for ${from}`);
    fireEvent.change(input, { target: { value: to } });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.blur(input);
  };

  afterEach(() => {
    dbgDump(`after-1-${dbgTag}`);
    clearInterval(dbgTick);
    // Vitest 4 does not run RTL auto-cleanup under this config (globals are
    // injected but the cleanup side effect is not), so unmount explicitly;
    // tab selection also persists through sessionStorage and must not leak.
    cleanup();
    dbgDump(`after-2-${dbgTag}`);
    window.sessionStorage.clear();
  });

  it('keeps a stale preview generation out of the current table and keeps the current preview error visible when the old generation finishes with rows', async () => {
    dbgWatch('stale-gen');
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewCalls[0]).toEqual(expect.objectContaining({ receiptId: 'receipt-core-1', outputId }));

    // Generation A (head 1) is parked in its preview await while the save
    // promotes head 2, which starts generation B.
    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));

    let resolvedCompiles = 1;
    let deliveredPreviews = 1; // gate 0 is generation A, held for the late success.
    await waitFor(() => {
      while (resolvedCompiles < harness.reconcileGates.length) {
        harness.reconcileGates[resolvedCompiles]!.resolve(compileForHead(2));
        resolvedCompiles += 1;
        if (resolvedCompiles > 15) { dbgDump('sg-compile-spin', resolvedCompiles); throw new Error('reconcile resolve spin'); }
      }
      while (deliveredPreviews < harness.previewGates.length) {
        harness.previewGates[deliveredPreviews]!.reject(explorerError('core-preview-error-from-B', previewErrorCode));
        deliveredPreviews += 1;
        if (deliveredPreviews > 15) { dbgDump('sg-preview-spin', deliveredPreviews); throw new Error('preview settle spin'); }
      }
      expect(harness.previewGates.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
    dbgDump('sg-looped');
    dbgDump('sg-1');
    await settleDelay(100);
    while (deliveredPreviews < harness.previewGates.length) {
      harness.previewGates[deliveredPreviews]!.reject(explorerError('core-preview-error-from-B', previewErrorCode));
      deliveredPreviews += 1;
    }

    // B is the active generation carrying its own error; A was detached at the
    // request boundary when the save took over.
    dbgDump('sg-2');
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent('core-preview-error-from-B');
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(harness.previewGates[0]!.aborted).toBe(true);
    for (const call of harness.previewCalls.slice(1)) {
      expect(call).toEqual(expect.objectContaining({ receiptId: 'receipt-core-2', outputId }));
    }
    const previewCallsAtHandoff = harness.previewCalls.length;
    const reconcileCallsAtHandoff = harness.reconcileCalls.length;

    // A's late success must neither publish rows nor clear B's error state.
    dbgDump('sg-3');
    // Late delivery outside act, matching real timer arrival; a resolved
    // never-settling gate awaited inside act() never returns.
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await settleDelay(25);
    dbgDump('sg-4');
    tr('sg-4-done');
    await settleDelay(150);
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent('core-preview-error-from-B');
    expect(harness.previewCalls.length).toBe(previewCallsAtHandoff);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsAtHandoff);
    dbgDump('sg-5');
    tr('sg-5-done');

    // Boundary note: "B error then B success" through the same generation is
    // unreachable through real controls here — no affordance refetches the
    // automatic preview query, and the Recompile button
    // (BuilderWorkspace.tsx:4368-4376) only appears while reconciliation is
    // 'stale'/'repair', which a pure preview failure never sets. B completing
    // is therefore witnessed in the sibling case below (C12 plan row: current
    // generation can finish).
  });

  it('keeps a stale preview error from surfacing on the current generation while the current generation completes with rows', async () => {
    dbgTag = 'stale';
    dbgWatch('stale-err');
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
        if (resolvedCompiles > 15) { dbgDump('se-spin', resolvedCompiles); throw new Error('reconcile resolve spin'); }
      }
      expect(harness.previewCalls.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
    dbgDump('se-looped');

    // B (head 2) is loading, holding no preview yet.
    dbgDump('se-1');
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();

    // A's late error is delivered after A was detached by the save. Gate
    // settlement stays outside act(): awaiting a settled gate inside act()
    // deadlocks; late arrivals legitimately land on the test's own timer.
    harness.previewGates[0]!.reject(explorerError('core-error-from-A-before-B', previewErrorCode));
    await settleDelay(25);
    // The old error cannot be shown as B's state: no error surface appears and
    // B keeps its loading state.
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByText(/core-error-from-A-before-B/)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();

    // The current generation finishes with its own rows: B's own preview
    // settles with success (outside act; awaiting gates in act deadlocks).
    let deliveredB = 1;
    await waitFor(() => {
      while (deliveredB < harness.previewGates.length) {
        harness.previewGates[deliveredB]!.resolve(previewFor(2, 'receipt-core-2'));
        deliveredB += 1;
        if (deliveredB > 15) { dbgDump('se-preview-spin', deliveredB); throw new Error('B preview settle spin'); }
      }
      expect(harness.previewGates.length).toBeGreaterThanOrEqual(2);
    }, { timeout: 5000 });
    dbgDump('se-2');
    tr('se-2-done');
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });
    dbgDump('se-3');
    tr('se-3-done');
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByText(/core-error-from-A-before-B/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
  });

  it('keeps an abandoned output preview out of the switched output and completes the switched output preview', async () => {
    dbgTag = 'abandoned';
    // Real output affordance: ConstructionTableNavigation's tab button
    // (constructionWorkspace/ConstructionWorkspace.tsx:259-276) is unstubbed
    // in this mount and drives selectTableFromUser → 'selectTable'.
    harness.builderState = hydratedStateWithTwoOutputs();
    dbgWatch('switch');
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

    // Switch outputs while generation A (patients preview) is still in flight.
    dbgDump('sw-1');
    fireEvent.click(screen.getByRole('button', { name: 'Encounters' }));
    dbgDump('sw-m');
    await flushMicrotasks();
    dbgDump('sw-2');
    tr('sw-2-done');
    // The switch detaches A at the request boundary via the keyed query's
    // controller.abort() → refresh cancel() → execution.previewRequest.abort().
    expect(harness.previewGates[0]!.aborted).toBe(true);
    dbgDump('sw-a');
    tr('sw-a-done');
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    dbgDump('sw-b');
    expect(harness.reconcileCalls[1]).toEqual(expect.objectContaining({
      snapshotToken, draftVersion: 1, draftDigest: headDigests[1], outputId: outputId2,
    }));
    harness.reconcileGates[1]!.resolve(switchCompileBothOutputs('receipt-switch-encounters'));
    dbgDump('sw-c');
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });
    dbgDump('sw-d');
    expect(harness.previewCalls[1]).toEqual(expect.objectContaining({
      receiptId: 'receipt-switch-encounters', outputId: outputId2,
    }));
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    dbgDump('sw-e');

    // A's completion must not populate the switched output's preview area.
    dbgDump('sw-3');
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-switch-baseline'));
    await settleDelay(25);
    dbgDump('sw-4');
    await settleDelay(150);
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewCalls.length).toBe(2);

    // The switched output finishes with its own rows.
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[1]!.resolve(previewForSecondOutput('receipt-switch-encounters'));
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(encountersMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('aborts the in-flight preview request on unmount and admits nothing from the late preview success', async () => {
    dbgTag = 'in-flight';
    // C13 detach semantics only: abort cancels the transport request, it is
    // not evidence of a server-side rollback (plan C13 acceptance row).
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
      // Gate settlement outside act(): awaiting a settling promise inside
      // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
      harness.reconcileGates[0]!.resolve(compileForHead(1));
      await flushMicrotasks();
      await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

      dbgDump('au-1');
      view.unmount();
      // react.tsx resourceFor subscribe cleanup aborts on a microtask.
      await flushMicrotasks();
      dbgDump('au-2');
    tr('au-2-done');
      // The owned preview request was actually aborted through the mock
      // boundary (execution.previewRequest.abort() at BuilderWorkspace.tsx:2996).
      expect(harness.previewGates[0]!.aborted).toBe(true);
      dbgDump('au-a');
    tr('au-a-done');
      // Late success after detach: it must not surface anywhere and must not
      // be retried. An unhandled rejection would fail this suite (vitest
      // reports it), so a green run is the "no rejection surface" evidence.
      dbgDump('au-3');
      // Gate settlement outside act(): awaiting a settling promise inside
      // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
      harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
      await settleDelay(25);
      dbgDump('au-4');
      expect(harness.previewCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(1);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        // 'not wrapped in act' is the accepted artifact of delivering late
        // responses outside act(): awaiting a settling gate inside act()
        // deadlocks the flush; genuine failure signals below stay caught.
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      dbgDump('compile-unmount-leaks', leaks);
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('aborts the in-flight compile request on unmount and admits nothing from the late compile error', async () => {
    dbgTag = 'in-flight';
    // C13 boundary: no user-facing cancel control exists for a pending
    // command, compile, or preview await in this mount — cancelAutomaticPreview
    // is only reached through saves, Explorer switches, and query detach
    // (BuilderWorkspace.tsx:755, :813, :2979) — so unmount is the observable
    // detach path; command-await unmount shares the same trigger-abort chain
    // through queueCommands and is not asserted here.
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    dbgWatch('abort-preview');
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });

      view.unmount();
      await flushMicrotasks();
      // execution.compileRequest.abort() fired at BuilderWorkspace.tsx:2995.
      expect(harness.reconcileGates[0]!.aborted).toBe(true);

      // Late error after detach: reconcileCurrent's owner-cancelled guard
      // (BuilderWorkspace.tsx:2671) returns before the recovery counters, so
      // nothing is admitted and no retry is issued.
      // Gate settlement outside act(): awaiting a settling promise inside
      // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
      harness.reconcileGates[0]!.reject(explorerError('core-compile-failed-while-detached', 'CORE_PROTOCOL_COMPILE_FAILED'));
      await settleDelay(25);
      expect(harness.reconcileCalls.length).toBe(1);
      expect(harness.previewCalls.length).toBe(0);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        // 'not wrapped in act' is the accepted artifact of delivering late
        // responses outside act(): awaiting a settling gate inside act()
        // deadlocks the flush; genuine failure signals below stay caught.
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('lets only the current attempt complete when a reused receipt label spans three A-B-A preview attempts', async () => {
    dbgTag = 'the';
    dbgDump('cp-start');
    // C14: A→B→A with the identical receipt label reused for the first A and
    // A''' (snapshotToken is identical throughout). The late stale-A preview
    // and the current A''' preview are the very same payload object, so an
    // outcome difference can only come from the identity machinery, not from
    // values matching.
    renderMountedBuilder();

    dbgDump('cp-a');
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    dbgDump('cp-b', harness.reconcileCalls.length);
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    dbgDump('cp-c');
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    dbgDump('cp-d', harness.previewCalls.length);
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await flushMicrotasks();
    dbgDump('cp-e');
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });
    dbgDump('cp-f');

    // Attempt A: rename to the reused-A state; park its preview in flight.
    await renameColumnLabel(headLabels[1], attemptALabel);
    dbgDump('cp-g');
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[0]!.resolve(attemptAck(2));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    const staleCompile = attemptCompile(reusedReceiptLabel, attemptALabel);
    harness.reconcileGates[1]!.resolve(staleCompile);
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });

    // Attempt B.
    await renameColumnLabel(attemptALabel, attemptBLabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[1]!.resolve(attemptAck(3));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    harness.reconcileGates[2]!.resolve(attemptCompile(secondAttemptReceiptLabel, attemptBLabel));
    await waitFor(() => expect(harness.previewCalls.length).toBe(3), { timeout: 5000 });

    // Attempt A''': identical intent to A, and the mock reuses A's receipt
    // label so the request label matches A's preview request exactly.
    await renameColumnLabel(attemptBLabel, attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[2]!.resolve(attemptAck(4));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(4), { timeout: 5000 });
    harness.reconcileGates[3]!.resolve(staleCompile); // byte-identical to the stale A compile
    await waitFor(() => expect(harness.previewCalls.length).toBe(4), { timeout: 5000 });
    expect((harness.previewCalls[1] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);
    expect((harness.previewCalls[3] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);

    // The stale first-A preview arrives after A''' started. Values match the
    // current attempt exactly (same payload, same receipt label); what saves
    // the UI is the attempt machinery: executePreview's owner?.cancelled /
    // generation guards (BuilderWorkspace.tsx:2742, :2759-2760 — the receipt
    // equality at :2761 passes for both attempts here), refresh's
    // identityIsCurrent over draftVersion/draftDigest/previewRequestVersion
    // (BuilderWorkspace.tsx:3000-3008, :3011), and the reducer's full-identity
    // previewAccepted gate (authoring/reducer.ts:527-538). Only this
    // generation difference can separate the two deliveries.
    const sharedPreview = attemptPreview(reusedReceiptLabel, attemptALabel);
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[1]!.resolve(sharedPreview);
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(attemptAMarker)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewCalls.length).toBe(4);
    expect(harness.reconcileCalls.length).toBe(4);

    // The valid new A completes with the same payload the stale attempt was
    // rejected for.
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[3]!.resolve(sharedPreview);
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(attemptAMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('lets only the current attempt compile when a reused receipt label spans A-B-A reconcile attempts', async () => {
    dbgTag = 'the';
    // C14 reconcile arm ("deliver the first A's stale ... reconcile after A'''
    // started"). The stale first-A compile is the byte-identical payload the
    // current attempt accepts. reconcileRecovery.ts reconcileResultIsCurrent
    // (line 62) compares generation, explorerId, AND intent fingerprint — A'''
    // and A share intent, so the fingerprint branch is satisfied by both; the
    // equality that keeps the stale compile out is the generation counter plus
    // reconcileCurrent's owner?.cancelled guards (BuilderWorkspace.tsx:2626,
    // :2644), advanced by every save (compileGeneration.current += 1 at
    // :817) and by the superseded-run cancel (:2989-2991).
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await flushMicrotasks();
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    // Attempt A: park its compile (never resolve gate 1 yet).
    await renameColumnLabel(headLabels[1], attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[0]!.resolve(attemptAck(2));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });

    // Attempt B.
    await renameColumnLabel(attemptALabel, attemptBLabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(2), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[1]!.resolve(attemptAck(3));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    harness.reconcileGates[2]!.resolve(attemptCompile(secondAttemptReceiptLabel, attemptBLabel));
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });

    // Attempt A''' with the reused receipt label, identical compile payload.
    await renameColumnLabel(attemptBLabel, attemptALabel);
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.applyGates[2]!.resolve(attemptAck(4));
    await flushMicrotasks();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(4), { timeout: 5000 });
    const sharedCompile = attemptCompile(reusedReceiptLabel, attemptALabel);
    harness.reconcileGates[3]!.resolve(sharedCompile);
    await waitFor(() => expect(harness.previewCalls.length).toBe(3), { timeout: 5000 });
    expect((harness.previewCalls[2] as { receiptId?: string }).receiptId).toBe(reusedReceiptLabel);

    // Stale first-A compile lands after A''' started: it must neither publish
    // a receipt-backed preview, start a preview of its own, nor retry.
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.reconcileGates[1]!.resolve(sharedCompile);
    await settleDelay(25);
    await settleDelay(150);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(attemptAMarker)).toBeNull();
    expect(harness.reconcileCalls.length).toBe(4);
    expect(harness.previewCalls.length).toBe(3);

    // The valid new A completes with the same payload the stale attempt was
    // rejected for.
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
    harness.previewGates[2]!.resolve(attemptPreview(reusedReceiptLabel, attemptALabel));
    await settleDelay(25);
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(attemptAMarker), { timeout: 5000 });
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  // ---- C12 / C13 remainder cases (CORE_PROTOCOL_VERIFICATION_PLAN.md:381-382) ----
  // C12 row: "Start preview A; edit, switch output, change context or start B;
  // finish A and B in both orders with success and error variants." Acceptance:
  // "Old completion cannot replace current rows, clear B's loading state, show
  // its error as B's, or release B's owner. Current B can finish."
  // C13 row: "Cancel or unmount during command, compile, recovery and preview
  // awaits; deliver late success/error." Acceptance: "No obsolete visible state
  // admission or retry. Observe actual detach semantics; do not assert transport
  // cancellation implies server rollback. Owned pending resources settle."
  //
  // Ownership-state trace driving these cases (file:line, BuilderWorkspace.tsx):
  // - executePreview loop guard :2742 (owner?.cancelled || generation !==
  //   previewGeneration.current); success echo-drop :2758-2764 (receiptId at
  //   :2761); catch guard :2769-2774 (owner?.cancelled / generation /
  //   CLIENT_CANCELLED) returns BEFORE previewRecoveryAction is consulted at
  //   :2775; transient retry branch :2778-2782 (transientRetries += 1 at :2781,
  //   counter declared at :2740).
  // - reconcileCurrent counters :2620-2623 (attempt/staleRefreshes, maxAttempts
  //   3, maxStaleRefreshes 2); its catch guard :2670-2675 returns before
  //   reconcileRecoveryAction at :2676.
  // - preview recovery budgets: authoring/previewRecovery.ts:40-43 (one receipt
  //   refresh per class) and :44 (exactly one transient retry for adapter-
  //   marked retryable errors). A non-cancelled 500 therefore DOES get one
  //   re-issue; a non-cancelled 409 does not (409 is not retryable at all and
  //   DRAFT_CONFLICT is in neither refresh set :10-21). The zero-retry
  //   assertions below hold for detached owners only because the :2769-2774 /
  //   :2670-2675 guards short-circuit before those counters can move.
  // - adapter error typing mirrored by typedPreviewError: ui/packages/loom-ui/
  //   src/api.ts requestError (:904-926); retryable = 408 | 429 | >=500 at
  //   :925, so a 500 arrives retryable:true and a 409 arrives retryable:false.
  // - the real cancel path: applyCommandsWithResult calls cancelAutomaticPreview
  //   synchronously at :813 (definition :649-651), selectExplorer at :755;
  //   cancel() flips execution.cancelled and aborts the owned gate
  //   (:2969-2989). Test-side aborted evidence: mock trigger's abort contract.
  // - late preview errors can never move the CAS head: serverDraft.current is
  //   written only by hydration/sync (:696-701, :777-780) and a save ack
  //   (:842-845). No preview-error handler touches it, asserted live in the C13
  //   409-cancel case via the next command's expectedDraft* fields.

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
    // Generation A (head 1) now parks on previewGates[0].unwrap().
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
        if (resolvedCompiles > 15) { dbgDump('c1213-compile-spin', resolvedCompiles); throw new Error('reconcile resolve spin'); }
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
        if (delivered > 15) { dbgDump('c1213-preview-spin', delivered); throw new Error('preview settle spin'); }
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
        if (delivered > 15) { dbgDump('c1213-preview-reject-spin', delivered); throw new Error('preview reject spin'); }
      }
      expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(message);
    }, { timeout: 5000 });
  };

  // Late preview deliveries land outside act(): awaiting a settling gate
  // inside act() deadlocks the flush (proven on the stale-generation case).
  const actLatePreviewSettlement = async (settle: () => void) => {
    settle();
    await settleDelay(25);
    await settleDelay(100);
  };

  it('C12 "finish A and B in both orders with success and error variants": B-success-then-A-late-success cannot replace current rows or header and never releases B owner', async () => {
    dbgTag = 'c12-b-success-a-late-success';
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    // Order one: generation B finishes with success first; its rows and header
    // reach PreviewTable (rows live only inside data-testid
    // "preview-table-content", header labels are the authored current head's).
    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.getByTestId('preview-table-content')).toHaveTextContent(headLabels[2]);
    const previewCallsAtSettled = harness.previewCalls.length;
    const reconcileCallsAtSettled = harness.reconcileCalls.length;

    // Order two completes when stale generation A finally succeeds late.
    await actLatePreviewSettlement(() => harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1')));

    // Rendered proof of "Old completion cannot replace current rows ... or
    // release B's owner.": B's rows/header stay, A's marker never appears, no
    // error surface sneaks in, and B's own request was never aborted.
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
    dbgTag = 'c12-b-loading-a-late-success';
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    // B is the active generation with no preview yet.
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    const previewCallsBefore = harness.previewCalls.length;
    const reconcileCallsBefore = harness.reconcileCalls.length;

    // A completes late with success while still waiting on B.
    await actLatePreviewSettlement(() => harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1')));

    // "clear B's loading state" is denied: the indicator survives and A's rows
    // are nowhere on the surface; B has not been released.
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
    expect(harness.previewCalls.length).toBe(previewCallsBefore);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsBefore);

    // "Current B can finish."
    await settleAllHeadBPreviewsWithSuccess();
    expect(screen.queryByText(rowMarkers[1])).toBeNull();
  });

  it('C12 "finish A and B in both orders with success and error variants": B-error-then-A-late-error keeps the displayed error B-owned and never releases B owner', async () => {
    dbgTag = 'c12-b-error-a-late-error';
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    // Order one: B completes first with an error (terminal code, so
    // previewRecoveryAction returns fail and the query holds B's own error).
    await rejectAllHeadBPreviews(bLateErrorLabel);
    const previewCallsBefore = harness.previewCalls.length;
    const reconcileCallsBefore = harness.reconcileCalls.length;

    // Order two: A completes late with its own error.
    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(explorerError(aLateErrorLabel, previewErrorCode)));

    // "show its error as B's" is denied: the status line still reads B's
    // message verbatim, A's error text never mounted, rows stayed unadmitted.
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(bLateErrorLabel);
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(previewErrorCode);
    expect(harness.previewGates[1]!.aborted).toBe(false);
    expect(harness.previewCalls.length).toBe(previewCallsBefore);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsBefore);
  });

  it('C12 "finish A and B in both orders with success and error variants": A-late-error-before-B-finish never enters the error slot and B still finishes with its own error', async () => {
    dbgTag = 'c12-a-late-error-b-error';
    await startGenerationAWaitingOnPreview();
    await startSaveSwitchingToHeadB();
    await driveHeadBCompile();

    // The opposite schedule: A errors late while B holds no preview yet; the
    // error surface must stay closed and B keeps loading.
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(explorerError(aLateErrorLabel, previewErrorCode)));
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.getByText('Loading your table…')).toBeInTheDocument();
    expect(harness.previewGates[1]!.aborted).toBe(false);

    // Then B finishes with its own error; the slot is filled exactly once, by B.
    await rejectAllHeadBPreviews(bLateErrorLabel);
    expect(screen.getByText(/Preview did not complete for this draft/)).toHaveTextContent(bLateErrorLabel);
    expect(screen.queryByText(new RegExp(aLateErrorLabel))).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
    expect(harness.previewGates[1]!.aborted).toBe(false);
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": unmount during preview await then late 500 admits nothing and issues no retry', async () => {
    dbgTag = 'c13-unmount-late-500';
    // Observed adapter-call counts for THIS case: preview 1 -> 1 and reconcile
    // 1 -> 1 across the late 500 delivery. A non-cancelled 500 is retryable at
    // the adapter (api.ts:925) and previewRecovery.ts:44 would spend one
    // transient retry via executePreview :2778-2782; the zero here is produced
    // by the executePreview catch guard :2769-2774 (owner?.cancelled short-
    // circuits before previewRecoveryAction at :2775), not by the status.
    // Detach semantics only: abort is transport cancellation, not a server
    // rollback claim (plan C13 acceptance sentence).
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = await startGenerationAWaitingOnPreview();
      view.unmount();
      await flushMicrotasks();
      expect(harness.previewGates[0]!.aborted).toBe(true);

      await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
        typedPreviewError(500, 'INTERNAL_SERVER_ERROR', 'C13-late-500-after-unmount'),
      ));

      // No obsolete admission and no automatic retry after detach: the owned
      // execution settled (gate aborted, no re-issue, no unhandled rejection —
      // vitest fails the suite on those, so a green run is that evidence).
      expect(harness.previewCalls.length).toBe(1);
      expect(harness.reconcileCalls.length).toBe(1);

      const leaks = consoleErrors.mock.calls
        .map((entry) => entry.map((item) => String(item)).join(' '))
        // 'not wrapped in act' is the accepted artifact of delivering late
        // responses outside act(): awaiting a settling gate inside act()
        // deadlocks the flush; genuine failure signals below stay caught.
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": unmount during preview await then late 409 DRAFT_CONFLICT with advanced head admits nothing and issues no retry', async () => {
    dbgTag = 'c13-unmount-late-409';
    // Observed adapter-call counts for THIS case: preview 1 -> 1 and reconcile
    // 1 -> 1 across the late 409 delivery. Trace: a 409 is not retryable at
    // the adapter (api.ts:925 — only 408/429/>=500) and DRAFT_CONFLICT is in
    // neither previewRecovery refresh set (:10-21), so even a live owner would
    // see 'fail' with exactly one request; the detached owner additionally
    // short-circuits at executePreview :2769-2774 (and reconcileCurrent
    // :2670-2675 for the compile branch). Zero is the genuine behavior for
    // this status, not an assumption. The conflict body's advanced head cannot
    // be absorbed as current state here: serverDraft.current only moves via
    // hydration/sync/ack (:696-701, :777-780, :842-845); the live-proof of
    // that non-adoption through the next command CAS is the 409-cancel case.
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
        // 'not wrapped in act' is the accepted artifact of delivering late
        // responses outside act(): awaiting a settling gate inside act()
        // deadlocks the flush; genuine failure signals below stay caught.
        .filter((line) => /unmounted|Maximum update depth|Cannot update during render|Cannot read properties of undefined/i.test(line)
          && !/not wrapped in act/.test(line));
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  it('C13 "Cancel or unmount during command, compile, recovery and preview awaits; deliver late success/error.": cancel via the real save path during preview await then late 500 admits nothing and B still finishes', async () => {
    dbgTag = 'c13-save-cancel-late-500';
    // Real cancel path: applyCommandsWithResult calls cancelAutomaticPreview
    // synchronously (BuilderWorkspace.tsx:813, definition :649-651) before
    // queueing the command; execution.cancelled + gate abort follow at
    // :2969-2989. Observed adapter-call counts for THIS case: preview 1 -> 1
    // and reconcile 1 -> 1 across the late 500 while the save is pending —
    // zero retries because the catch guard :2769-2774 returns before
    // previewRecoveryAction (:2775), even though an attached 500 would earn
    // one transient retry (previewRecovery.ts:44, executePreview :2778-2782).
    await startGenerationAWaitingOnPreview();

    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    expect(harness.previewGates[0]!.aborted).toBe(true);

    await actLatePreviewSettlement(() => harness.previewGates[0]!.reject(
      typedPreviewError(500, 'INTERNAL_SERVER_ERROR', 'C13-late-500-after-save-cancel'),
    ));

    // No obsolete visible state admitted while the save owns the surface.
    expect(harness.previewCalls.length).toBe(1);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(screen.queryByText(/C13-late-500-after-save-cancel/)).toBeNull();
    expect(screen.queryByText(/Preview did not complete for this draft/)).toBeNull();
    expect(screen.queryByTestId('preview-table-content')).toBeNull();

    // The save then lands and current B finishes cleanly through the real
    // hooks — the preview-error slot stays uncontaminated by the dropped A 500.
    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
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
    dbgTag = 'c13-save-cancel-late-409';
    // Observed adapter-call counts for THIS case: preview 1 -> 1 and reconcile
    // 1 -> 1 across the late 409 delivery (same guard evidence as the 500-
    // cancel case; the 409 additionally has no retry budget in production at
    // all: api.ts:925 marks it non-retryable, previewRecovery.ts:10-21 omits
    // DRAFT_CONFLICT from the receipt/catalog refresh sets, so :40-44 yield
    // 'fail'). The conflict body forges an advanced head (draftVersion 99);
    // after the real ack advances the head to 2, the next command must bind to
    // the ack head (serverDraft only moves at :696-701/:777-780/:842-845),
    // proving the dropped 409 body never became current intent.
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

    // Gate settlement outside act(): awaiting a settling promise inside
    // act() deadlocks the flush; deliveries legitimately arrive on the test timer.
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

  // C13 remainder: a detached attempt whose late delivery would enter the
  // transient-retry recovery loop must not resurrect that loop (plan row
  // "cancel during command/recovery awaits"). Transient trigger: retryable
  // error outside the receipt/catalog refresh sets (previewRecovery.ts
  // `error.retryable && transientRetries < 1 → return 'retry'`).
  it('stops the preview recovery retry loop once the attempt is detached by a save handoff', async () => {
    dbgTag = 'retry-detach';
    (globalThis as { __probe?: (m: string) => void }).__probe = (m) => process.stderr.write(`PROBE ${m}\n`);
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

    // Save promotes head 2; generation B takes over and detaches A at the
    // request boundary before A's preview settles.
    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
    let resolvedCompiles = 1;
    let settledPreviews = 1; // gate 0 = A, held for the late transient error.
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

    // A's late transient error arrives after detachment: 'retry' would issue
    // another preview request; the detached loop must not continue.
    harness.previewGates[0]!.reject({ message: 'core-transient-after-detach', code: 'CORE_PROTOCOL_TRANSIENT', retryable: true });
    await settleDelay(25);
    await settleDelay(200);
    expect(harness.previewCalls.length).toBe(previewCallsAtHandoff);
    expect(harness.reconcileCalls.length).toBe(reconcileCallsAtHandoff);

    // Positive control: B remains fully functional (the client is not stuck).
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[2]), { timeout: 5000 });
  });

  // C13 budget arm: the transient retry is real exactly once for the CURRENT
  // attempt (previewRecovery.ts `error.retryable && transientRetries < 1`),
  // not zero and not a storm.
  it('reissues the preview exactly once for a transient error on the current attempt and then renders rows', async () => {
    dbgTag = 'retry-budget';
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });

    // explorerError() is for terminal codes (retryable:false); the transient
    // recovery branch needs retryable:true outside the refresh sets
    // (previewRecovery.ts).
    harness.previewGates[0]!.reject({ message: 'core-transient-1', code: 'CORE_PROTOCOL_TRANSIENT', retryable: true });
    // Retry must re-issue the preview (second call) and the transient budget
    // must allow exactly one re-issue.
    await waitFor(() => expect(harness.previewCalls.length).toBe(2), { timeout: 5000 });
    expect(harness.previewCalls[1]).toEqual(expect.objectContaining({
      receiptId: 'receipt-core-1', outputId,
    }));
    harness.previewGates[1]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });
    await settleDelay(150);
    // Budget boundary: no third request.
    expect(harness.previewCalls.length).toBe(2);
  });

  // C13 remainder closed: the prior cases only asserted compile/preview
  // detach; the command (apply) await is the third await named by the plan.
  it('admits nothing from a late command ack after unmount during the command await', async () => {
    dbgTag = 'command-detach';
    const consoleErrors = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    try {
      const view = renderMountedBuilder();
      await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
      harness.reconcileGates[0]!.resolve(compileForHead(1));
      await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
      harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
      await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

      // Command in flight when the surface goes away.
      renameFirstColumn(headLabels[2]);
      await waitFor(() => expect(harness.applyCalls.length).toBe(1), { timeout: 5000 });
      const reconcileCallsBeforeDetach = harness.reconcileCalls.length;
      const previewCallsBeforeDetach = harness.previewCalls.length;

      view.unmount();
      await flushMicrotasks();

      // Late ack after detach: adoption would start head-2 follow-up work
      // (reconcile/preview) and surface head-2 rows; neither may happen.
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
      dbgDump('command-unmount-leaks', leaks);
      expect(leaks).toEqual([]);
    } finally {
      consoleErrors.mockRestore();
    }
  });

  // C01: within one ownership context, a late Builder GET that re-delivers the
  // last-seen (pre-save) draft cannot regress the admitted head or bind the
  // next command to it, while a GET carrying a legitimately newer identity is
  // still adopted. Pins under test: render-phase authoritative sync
  // (BuilderWorkspace.tsx:695-702 after shouldSyncServerDraft) and the
  // post-ack pin (BuilderWorkspace.tsx:850).
  it('cannot regress an admitted save with a late same-context Builder GET, and still adopts a genuinely newer draft identity', async () => {
    dbgTag = 'c01-late-get';
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

    // Late GET A: a fresh object byte-identical to the last-seen payload.
    // Different object identity forces reads; deep-equal content keeps the
    // data key equal to the post-ack pin (BuilderWorkspace.tsx:850).
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

    // Settle the check command: busy gating disables triggers while a command
    // is pending (BuilderWorkspace.tsx:3082-3090).
    harness.applyGates[1]!.resolve(ackForHead(2));
    await settleDelay(25);

    // Legitimate newer identity from the same context still hydrates: the pin
    // must not freeze the draft at the acked head. The payload carries the
    // current head-2 workspace so the visible surface stays B-coherent.
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

    // The published payload carries head-2's original labels (the un-acked
    // local rename is not in it), so the third rename reads from headLabels[2].
    renameColumnLabel(headLabels[2], 'C01 newer-identity check');
    await waitFor(() => expect(harness.applyCalls.length).toBe(3), { timeout: 5000 });
    expect(harness.applyCalls[2]).toEqual(expect.objectContaining({
      snapshotToken,
      expectedDraftVersion: 3,
      expectedDraftDigest: 'sha256:core-head-3',
    }));
  });

  // C03: a command conflict must take the production forced-refresh branch
  // (refetchBuilder({ reload: true }) at BuilderWorkspace.tsx:900), hydrate
  // the refreshed draft through the render-phase sync (no replay of the
  // failed intent), and the next visible command must carry the exact
  // refreshed identity.
  it('forced-refreshes on a command conflict, never replays the stale intent, and binds the next command to the exact refreshed identity', async () => {
    dbgTag = 'c03-conflict-refresh';
    renderMountedBuilder();

    await waitFor(() => expect(harness.reconcileCalls.length).toBeGreaterThanOrEqual(1), { timeout: 5000 });
    harness.reconcileGates[0]!.resolve(compileForHead(1));
    await waitFor(() => expect(harness.previewCalls.length).toBe(1), { timeout: 5000 });
    harness.previewGates[0]!.resolve(previewFor(1, 'receipt-core-1'));
    await waitFor(() => expect(screen.getByTestId('preview-table-content')).toHaveTextContent(rowMarkers[1]), { timeout: 5000 });

    // Server-side reality: the head advanced to 2 underneath; the refreshed
    // GET carries that coherent head.
    const refreshedV2 = explorerBuilderStateSchema.parse({
      ...structuredClone(harness.builderState),
      workspace: workspaceForHead(2),
      draftVersion: 2,
      draftDigest: headDigests[2],
    });
    // A reload refetch publishes into the same resource slot the Builder GET
    // query reads (react.tsx refresh path); simulate both halves together or
    // the render-phase sync legitimately reverts to the un-refreshed slot.
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

    // No replay of the failed command (BuilderWorkspace.tsx:903 returns after
    // hydrating the refreshed draft).
    await settleDelay(100);
    expect(harness.applyCalls.length).toBe(1);

    // The render-phase sync (BuilderWorkspace.tsx:696-702) adopts the
    // refreshed identity without waiting for any follow-up request; settle an
    // automatic head-2 compile/preview if the flow issues one.
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

    // The published head-2 draft shows its own label; the refreshed identity
    // must reach the next command's CAS exactly.
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

  // C02 probe: what does the mounted surface actually do when a second
  // command is submitted while the first is still queued behind its await?
  // (Queue identity-at-drain is asserted where the queue is actually formable;
  // this probe records the observable gating through the harness.)
  it('gates a second UI command submission while the first command await is pending', async () => {
    dbgTag = 'c02-queue-probe';
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

    // Same-tick double submit attempt: fresh label while command 1 is held.
    renameColumnLabel(headLabels[1], 'C02 second attempt');
    await settleDelay(75);

    // Drain: the held command completes normally either way. A queued second
    // command reaches the transport only after this drain (serial queue), so
    // its CAS — read after the first ack at BuilderWorkspace.tsx:826-830 —
    // must be the drained identity, not any submit-time value.
    harness.applyGates[0]!.resolve(ackForHead(2));
    await settleDelay(120);
    const submittedWhilePending = harness.applyCalls.length;
    dbgDump('c02-gate-observation', { submittedWhilePending });

    if (submittedWhilePending > 1) {
      expect(harness.applyCalls[1]).toEqual(expect.objectContaining({
        snapshotToken, expectedDraftVersion: 2, expectedDraftDigest: headDigests[2],
      }));
      harness.applyGates[1]!.resolve(ackForHead(3 as unknown as 1 | 2));
      // An ordinary (non-admitted) rename ack does not open a new reconcile
      // round: the effect fires per admitted command (admitCommand === undefined
      // for renames), and the server returns the bytes the command produced
      // (identity-equivalent), so the revision-keyed reconcile dedups.
      await settleDelay(120);
      expect(harness.reconcileCalls.length).toBe(2);
      // The acked commands supersede the committed preview round; the table
      // shell stays mounted (rows unmount until a fresh receipt lands — no
      // stale-row bleed under the advanced head, and no re-preview because
      // renames do not invalidate the preview query themselves).
      expect(screen.getByTestId('preview-table-scroll')).toBeInTheDocument();
    } else {
      // Queue-surface gate: while a command await is pending the second
      // rename must not reach the transport (busy per BuilderWorkspace.tsx:3083).
      expect(submittedWhilePending).toBe(1);
    }
    // No permanent busy wedge: with both commands acked and the third ack
    // still unwired (a rename ack while the GET view stays head-1 re-renders
    // nothing new), the harness records an accepted queue drain and the
    // surface keeps rendering committed rows.
    expect(harness.applyCalls.length).toBe(submittedWhilePending);
    expect(harness.applyGates.length).toBe(submittedWhilePending);
    expect(harness.applyCalls[0]).toEqual(expect.objectContaining({
      snapshotToken, expectedDraftVersion: 1, expectedDraftDigest: headDigests[1],
    }));
  });

  // ---- C08/C09: reconcile recovery budgets inside the mounted core flow ----
  // Decision source authoring/reconcileRecovery.ts:38-50; counter loop
  // BuilderWorkspace.tsx reconcileCurrent (:2621-2624 maxAttempts 3,
  // maxStaleRefreshes 2; recovery branches :2680-2760). attempt increments on
  // EVERY continued iteration (both the stale refresh branch and the ordinary
  // retry branch), while staleRefreshes only gates the stale branch — so a
  // mixed retryable/stale sequence can issue up to five HTTP calls, exactly
  // the counter interaction the plan (docs/verification, section :390) demands
  // be modeled rather than assumed to stop at three. Gate rejects are
  // delivered outside act() (settling a pending gate inside act() deadlocks
  // the flush; established convention in this file).
  const staleCompileError = () =>
    explorerError('core-compile-stale-snapshot', 'STALE_SNAPSHOT');
  const transientCompileError = () => ({
    message: 'core-compile-transient-500',
    code: 'SERVER_ERROR',
    retryable: true,
  });
  // A published catalog refresh: only the snapshot token moves, so recovery
  // must carry the NEW token without perturbing the intent fingerprint
  // (workspaceFromState covers stage/column intent, not the catalog token).
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
    dbgTag = 'c08-stale-budget';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    harness.reconcileGates[0]!.reject(staleCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    // The permitted retry carries the REFRESHED catalog CAS, not the
    // submit-time token: refreshed values feed the retry (plan C08 wording).
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

    // Third stale error: staleRefreshes 2 === max — recovery is 'repair';
    // stale refreshes stop at two, the repair banner reports the stale code,
    // and no fourth compile goes out.
    harness.reconcileGates[2]!.reject(staleCompileError());
    await settleDelay(300);
    expect(harness.reconcileCalls.length).toBe(3);
    expect(harness.refetchCalls.length).toBe(2);
    expect(screen.getByRole('alert')).toHaveTextContent('core-compile-stale-snapshot');

    // Surface is not wedged: a fresh user edit still reaches the transport.
    renameFirstColumn('C08 recovery edit');
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
  });

  it('C08 a stale failure whose forced refresh yields no data stops without a second compile attempt', async () => {
    dbgTag = 'c08-nodata';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    // refetchImpl stays undefined → the reload refetch resolves data-less,
    // the documented missing/failed refresh data (plan C08) variant.

    harness.reconcileGates[0]!.reject(staleCompileError());
    await settleDelay(250);
    expect(harness.reconcileCalls.length).toBe(1);
    expect(harness.refetchCalls).toEqual([{ reload: true }]);
    expect(harness.previewCalls.length).toBe(0);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });

  it('C08 a draft-desynchronized failure forces one refresh and stops without any retry', async () => {
    dbgTag = 'c08-draft-conflict';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    harness.reconcileGates[0]!.reject(explorerError('core-compile-draft-conflict', 'DRAFT_CONFLICT'));
    await settleDelay(300);
    // refresh-and-stop: one reload hydrates the client, but no further
    // compile issues — the head CAS moved underneath this intent.
    expect(harness.reconcileCalls.length).toBe(1);
    expect(harness.refetchCalls).toEqual([{ reload: true }]);
    expect(screen.queryByTestId('preview-table-content')).toBeNull();
  });

  it('C09 ordinary retry budget: exactly three attempts, no forced refresh, then repair and a recoverable surface', async () => {
    dbgTag = 'c09-retry-budget';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });

    harness.reconcileGates[0]!.reject(transientCompileError());
    // Ordinary retry re-issues after the retry delay with the SAME catalog
    // CAS: no forced refresh opens (stale codes alone refresh the catalog).
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

    // Third failure: attempt 3 is not < maxAttempts, so recovery is 'repair'
    // — stops at three HTTP requests, banner reports the failure.
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
    dbgTag = 'c09-ownership';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    const firstRequest = harness.reconcileCalls[0] as { requestId: string };

    harness.reconcileGates[0]!.reject(transientCompileError());
    // The ordinary retry re-issue must be observable before the save lands,
    // so the save demonstrably races a LIVE attempt-2 (not a pre-decision
    // stop that never reached the transport).
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect((harness.reconcileCalls[1] as { draftVersion?: number }).draftVersion).toBe(1);
    // A save commits to head 2: the command path cancels the current
    // automatic execution (BuilderWorkspace.tsx:813, :2978) and the head-2
    // intent takes ownership. The loop-top guard (:2626 owner.cancelled /
    // !requestIsCurrent) must end the old round before it re-issues again.
    await settleDelay(60);
    renameFirstColumn(headLabels[2]);
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));

    // The stale round must now be forced THROUGH the recovery decisions
    // with ownership already lost: rejecting attempt 2 makes the catch
    // branch evaluate cancel/identity guards and the recovery counters.
    // A discarded late success would spare the recovery path entirely.
    harness.reconcileGates[1]!.reject(transientCompileError());
    await settleDelay(600);

    // The advanced head's own round takes over and completes (a later
    // allowed attempt can finish, plan C09).
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

    // The superseded round re-issued at most once (the attempt-2 call was
    // already in flight against the transport before the save); it must
    // never continue past the guard into attempt 3 with head-1 identity.
    const staleRoundCalls = harness.reconcileCalls.filter(
      (call) => (call as { requestId: string }).requestId === firstRequest.requestId,
    );
    expect(staleRoundCalls.length).toBe(1);
    const lateHeadOneCalls = harness.reconcileCalls.filter(
      (call, index) => index > 0 && (call as { draftVersion?: number }).draftVersion === 1,
    );
    if (lateHeadOneCalls.length === 1) {
      // An already-dispatched attempt may still be recorded; no further
      // head-1 attempt follows it.
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
    dbgTag = 'c09-mixed-5';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    let refreshRound = 0;
    harness.refetchImpl = async () => {
      refreshRound += 1;
      return { data: refreshedCatalogState(refreshRound) };
    };

    // ordinary, ordinary, stale, stale, ordinary.
    harness.reconcileGates[0]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(2), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[1])).toBe('2');

    harness.reconcileGates[1]!.reject(transientCompileError());
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(3), { timeout: 5000 });
    expect(attemptSuffix(harness.reconcileCalls[2])).toBe('3');

    // attempt 3 stale: the stale branch is gated by staleRefreshes, NOT by
    // maxAttempts, so the loop continues (attempt 4) — beyond three HTTP calls.
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

    // Fifth failure is ordinary: the retry branch demands attempt < 3, and
    // with attempt=5 the decision falls through to repair — no sixth call.
    harness.reconcileGates[4]!.reject(transientCompileError());
    await settleDelay(700);
    expect(harness.reconcileCalls.length).toBe(5);
    expect(screen.getByRole('alert')).toHaveTextContent('core-compile-transient-500');
    // All five attempts belong to ONE round (same generation prefix).
    const prefixes = new Set(harness.reconcileCalls.map((c) => (c as { requestId?: string }).requestId!.replace(/-\d+$/, '')));
    expect(prefixes.size).toBe(1);
  });

  // ---- C07: conflict forced-refresh across an owner/context change ----
  // Same refresh-and-stop branch as the DRAFT_CONFLICT budget case, but the
  // forced refresh await is held open while ownership changes, so the two
  // recovery shapes are told apart by OBSERVABLES (hydration publication,
  // follow-up requests), per the plan row: 'change owner/context during that
  // await … whether state is hydrated, whether another request is sent'.
  const deferredRefresh = () => {
    const deferred: { resolve: (value: { data?: unknown }) => void } = { resolve: () => undefined };
    const promise = new Promise<{ data?: unknown }>((resolve) => {
      deferred.resolve = resolve;
    });
    return { promise, deferred };
  };

  it(    'C07 refresh-and-stop: a stale post-refresh payload during a mid-await context change is refused through the request channel (no hydrate side-effect, no head-1 re-issue)', async () => {
    dbgTag = 'c07-stop-ownerchange';
    renderMountedBuilder();
    await waitFor(() => expect(harness.reconcileCalls.length).toBe(1), { timeout: 5000 });
    const staleRefresh = deferredRefresh();
    harness.refetchImpl = () => staleRefresh.promise;
    // Identity matches the POST-change head so the only reason hydration could
    // be refused is the post-refetch freshness guard itself (digest/version
    // mismatch would be an independent veto and would make the control weak).
    const stalePayload = {
      ...hydratingState(),
      draftVersion: 2,
      draftDigest: headDigests[2],
      workspace: workspaceForHead(1),
    };

    harness.reconcileGates[0]!.reject(explorerError('core-conflict-forced-refresh', 'DRAFT_CONFLICT'));
    await waitFor(() => expect(harness.refetchCalls).toEqual([{ reload: true }]), { timeout: 5000 });

    // Context changes while the forced refresh is still in flight: a save
    // advances the head through the command path (the owner-taking action).
    renameFirstColumn('C07 saved head two');
    await waitFor(() => expect(harness.applyCalls).toHaveLength(1), { timeout: 5000 });
    harness.applyGates[0]!.resolve(ackForHead(2));
    await waitFor(() => expect(
      harness.reconcileCalls.some((c) => (c as { draftVersion?: number }).draftVersion === 2),
    ).toBe(true), { timeout: 5000 });

    // The obsolete refresh FINISHES after the context change.
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

    // refresh-and-stop distinguishes itself from retry: no compile follows
    // the refresh in the OLD round (that round stays at one attempt) even
    // though the refresh resolved successfully.
    const oldRound = harness.reconcileCalls.filter(
      (c) => (c as { draftVersion?: number }).draftVersion === 1,
    );
    expect(oldRound.length).toBe(1);
    // A follow-up request exists only from the NEW owner's round (head 2),
    // never a re-issue of the head-1 intent.
    const headOneRounds = harness.reconcileCalls.filter(
      (c) => (c as { requestId?: string }).requestId!.endsWith('-1')
        && (c as { draftVersion?: number }).draftVersion === 1,
    );
    expect(headOneRounds.length).toBe(1);
    // Hydration refusal is witnessed through the request channel, not the DOM:
    // an accepted syncBuilderData here stores localState under the payload's
    // own builder data key, which cannot equal the live query key after the
    // context change, so render falls back to the obsolete query head and the
    // head-1 intent RE-ISSUES — exactly the extra reconcile call the two count
    // assertions above forbid. Column labels only reach the DOM as input
    // values/aria labels (PreviewTable.tsx), so no text assertion could
    // witness this; the guard's work is proven by the fault run
    // (deleting the post-refetch guard reddens the head-1 count).
  });


});