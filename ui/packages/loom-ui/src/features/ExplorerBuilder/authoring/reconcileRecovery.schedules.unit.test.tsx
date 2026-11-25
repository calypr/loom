// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LoomProvider } from '../../../react';
import { createLoomClient } from '../../../client/api';
import BuilderWorkspace from '../BuilderWorkspace';
import { reconcileAcceptance } from '../model/reconcileAcceptance';
import { reconcileRecoveryAction } from '../model/reconcileRecovery';
import { EXPLORER_AUTHORING_API_VERSION, explorerBuilderCompileResultSchema, type ExplorerBuilderCompileResult } from '../../../contracts/types';
import { reconcile_recovery as cases, reconcile_callback_schedules as callbackSchedules } from '../../../../testdata/schedules/reconcile_recovery';

const harness = vi.hoisted(() => ({
  state: undefined as unknown,
  refetch: vi.fn(),
  publish: vi.fn(),
  reconcile: vi.fn(),
  preview: vi.fn(),
  client: {} as Record<string, unknown>,
}));

vi.mock('../session/useBuilderLoad', () => ({
  useGetExplorerBuilderStateV2Query: vi.fn(() => ({ data: harness.state, isLoading: false, refetch: harness.refetch })),
}));

vi.mock('../../../data/builderQueries', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../../data/builderQueries')>(),
  useResolvePopulationSelectionQuery: () => ({ data: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
  useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
}));

vi.mock('../../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../react')>();
  return {
    ...actual,
    useLoomClient: () => harness.client,
    useGetExplorerAuthoringExplorersQuery: () => ({ data: [{ explorerId: 'patients', title: 'Patients' }], isLoading: false, refetch: vi.fn() }),
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: { deleteExplorer: false } } }),
    useBuilderMutation: (method: string) => {
      const overrides: Readonly<Record<string, () => unknown>> = {
        reconcile: () => [harness.reconcile, { isLoading: false }],
        preview: () => [harness.preview, { isLoading: false }],
        publish: vi.fn(() => [harness.publish, { isLoading: false }]),
        applyCommands: () => [vi.fn(), { isLoading: false }],
        assessRowChange: () => [vi.fn(), { isLoading: false }],
        createExplorer: () => [vi.fn(), { isLoading: false }],
        deleteExplorer: () => [vi.fn(), { isLoading: false }],
        suggestions: () => [vi.fn(), { isLoading: false }],
        populationMapping: () => [vi.fn(), { isLoading: false }],
      };
      const override = overrides[method];
      return override ? override() : actual.useBuilderMutation(method as Parameters<typeof actual.useBuilderMutation>[0]);
    },
  };
});

vi.mock('../components/BuilderToolbar', () => ({
  BuilderToolbar: ({ onPublish, onReview, publishDisabled }: { readonly onPublish: () => void; readonly onReview: () => void; readonly publishDisabled: boolean }) =>
    <><button type="button" onClick={onReview}>Review dataset</button><button type="button" disabled={publishDisabled} onClick={onPublish}>Publish</button></>,
}));

const column = {
  column: 'patient_id', label: 'Patient ID', occurrenceId: 'base',
  source: { kind: 'field' as const, field: { path: 'id', projectionMode: 'FIRST' as const } },
  table: { visible: true, order: 0 },
};
const workspace = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION, kind: 'ExplorerBuilderWorkspace' as const, explorer: { title: 'Patients' },
  documents: [{ kind: 'ExplorerBuilderDocument' as const, output: { id: 'patients', title: 'Patients' }, rootResourceType: 'Patient', route: { occurrenceId: 'base', resourceType: 'Patient' }, rows: { kind: 'RECORDS' as const, records: {} }, columns: [column] }],
  tabs: [{ id: 'patients-tab', title: 'Patients', outputId: 'patients', order: 0, visible: true }],
};
const catalog = {
  snapshotToken: 'snapshot-1', generation: 'generation-1', routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
  nodes: [{ nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 }],
  edges: [], candidates: [{ candidateId: 'patient-id', nodeId: 'patient-node', fieldPath: 'id', label: 'Patient ID', logicalType: 'string', projectionModes: ['FIRST'], defaultProjectionMode: 'FIRST', filterable: true, chartable: false }],
};
const state = { apiVersion: EXPLORER_AUTHORING_API_VERSION, kind: 'ExplorerBuilderState' as const, lifecycleState: 'READY' as const, draftVersion: 1, draftDigest: 'draft-1', workspace, catalog };
const receipt = { apiVersion: EXPLORER_AUTHORING_API_VERSION, kind: 'ExplorerBuilderReceipt' as const, receiptId: 'receipt-1', snapshotToken: 'snapshot-1', builder: workspace, outputs: [{ outputId: 'patients', columns: [{ column: 'patient_id', label: 'Patient ID', logicalType: 'string', filterable: true, chartable: false }] }], diagnostics: [] };
const resolved = <T,>(value: T) => ({ unwrap: vi.fn().mockResolvedValue(value), abort: vi.fn() });
const rejectedRequest = (error: unknown) => ({ unwrap: vi.fn().mockRejectedValue(error), abort: vi.fn() });
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>((yes) => { resolve = yes; }); return { request: { unwrap: () => promise, abort: vi.fn() }, resolve }; };
const deferredRequest = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { request: { unwrap: () => promise, abort: vi.fn() }, resolve, reject };
};

beforeEach(() => {
  harness.state = state;
  harness.refetch.mockReset().mockResolvedValue({ data: state });
  harness.publish.mockReset().mockReturnValue(resolved({}));
  harness.reconcile.mockReset().mockReturnValue(resolved(receipt));
  harness.preview.mockReset().mockReturnValue(resolved({ apiVersion: EXPLORER_AUTHORING_API_VERSION, kind: 'ExplorerBuilderPreview', rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST' }, receiptId: 'receipt-1', outputId: 'patients', columns: [], rows: [], rowCount: 0, diagnostics: [] }));
  harness.client = {
    describeAuthoringNetwork: vi.fn().mockResolvedValue({ nodes: [], edges: [] }),
    getConstructionCapabilities: vi.fn().mockImplementation(async (args: { snapshotToken: string; expectedDraftVersion: number; expectedDraftDigest: string; outputId: string; stageId: string }) => {
      const selectedStage = { id: args.stageId, inputStageId: '', rowIdentityColumn: 'row-id', columns: [], capabilities: [] };
      return { snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest, outputId: args.outputId, stageId: args.stageId, baseConstruction: { version: 1, steps: [] }, stages: [selectedStage], selectedStage };
    }),
    listRowDefinitionChoices: vi.fn().mockResolvedValue({ snapshotToken: 'snapshot-1', outputId: 'patients', choices: [], explicitGroups: [] }),
    browseSemanticInventory: vi.fn().mockResolvedValue({ state: 'complete', entries: [] }),
  };
});

describe('reconcile acceptance and recovery schedules', () => {
  it.each(cases.acceptance)('$name', (schedule) => {
    const value = explorerBuilderCompileResultSchema.parse({
      apiVersion: EXPLORER_AUTHORING_API_VERSION,
      kind: 'ExplorerBuilderReceipt',
      receiptId: 'receipt-1',
      snapshotToken: schedule.value.snapshotToken,
      builder: workspace,
      outputs: schedule.value.outputs.map((output) => ({ ...output, columns: [] })),
      diagnostics: schedule.value.diagnostics.map((diagnostic) => ({ ...diagnostic, message: diagnostic.code })),
    });
    expect(reconcileAcceptance(value, schedule.identity, schedule.request)).toBe(schedule.expected);
  });

  it.each(cases.recovery)('$name', (schedule) => {
    expect(reconcileRecoveryAction({ status: 409, message: schedule.error.code, ...schedule.error }, schedule.options)).toBe(schedule.expected);
  });
});

const multiWorkspace = {
  ...workspace,
  documents: [workspace.documents[0], { ...workspace.documents[0], output: { id: 'observations', title: 'Observations' }, rootResourceType: 'Observation' }],
  tabs: [...workspace.tabs, { id: 'observations-tab', title: 'Observations', outputId: 'observations', order: 1, visible: true }],
};
const multiState = { ...state, workspace: multiWorkspace };
const multiReceipt = { ...receipt, builder: multiWorkspace };
const refreshedState = { ...multiState, catalog: { ...catalog, snapshotToken: 'snapshot-2' } };
const refreshedReceipt = { ...multiReceipt, snapshotToken: 'snapshot-2', outputs: [...receipt.outputs, { ...receipt.outputs[0], outputId: 'observations' }] };

describe('mounted reconcileCurrent callback schedules', () => {
  it.each(callbackSchedules)('$name', async (schedule) => {
    harness.state = multiState;
    harness.reconcile.mockReset().mockReturnValue(resolved(multiReceipt));
    harness.refetch.mockReset().mockResolvedValue({ data: refreshedState });

    if (schedule.mode === 'automatic-cancel') {
      const pending = deferredRequest<ExplorerBuilderCompileResult>();
      harness.reconcile.mockReturnValue(pending.request);
      const view = render(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
      await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(1));
      view.unmount();
      await act(async () => pending.reject({ code: 'CLIENT_CANCELLED', message: 'owner detached', retryable: false }));
      expect(harness.reconcile).toHaveBeenCalledTimes(schedule.expectedRequests);
      expect(harness.refetch).toHaveBeenCalledTimes(schedule.expectedRefetches);
      expect(pending.request.abort).toHaveBeenCalled();
      return;
    }

    const view = render(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
    await waitFor(() => expect(harness.preview).toHaveBeenCalled());
    const baseline = harness.reconcile.mock.calls.length;
    harness.reconcile.mockReset();

    if (schedule.mode === 'stale-refresh-retry') {
      harness.reconcile.mockReturnValueOnce(rejectedRequest({ code: 'STALE_CATALOG_SNAPSHOT', message: 'stale receipt', retryable: false })).mockReturnValueOnce(resolved(refreshedReceipt));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.refetch).toHaveBeenCalledWith({ reload: true }));
      await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(2));
      expect(harness.reconcile.mock.calls.map((call) => call[0].snapshotToken)).toEqual(['snapshot-1', 'snapshot-2']);
      expect(harness.refetch).toHaveBeenCalledTimes(1);
      return;
    }

    if (schedule.mode === 'conflict-refresh-stop') {
      harness.reconcile.mockReturnValueOnce(rejectedRequest({ code: 'DRAFT_CONFLICT', message: 'draft conflict', retryable: false }));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.refetch).toHaveBeenCalledWith({ reload: true }));
      expect(harness.reconcile).toHaveBeenCalledTimes(1);
      expect(harness.refetch).toHaveBeenCalledTimes(1);
      return;
    }

    if (schedule.mode === 'transient-retry') {
      harness.reconcile.mockReturnValueOnce(rejectedRequest({ code: 'TRANSIENT_FAILURE', message: 'try later', retryable: true })).mockReturnValueOnce(resolved(multiReceipt));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(2), { timeout: 1500 });
      expect(harness.refetch).not.toHaveBeenCalled();
      return;
    }

    if (schedule.mode === 'retry-exhaustion') {
      harness.reconcile.mockReturnValueOnce(rejectedRequest({ code: 'TRANSIENT_FAILURE', message: 'one', retryable: true })).mockReturnValueOnce(rejectedRequest({ code: 'TRANSIENT_FAILURE', message: 'two', retryable: true })).mockReturnValueOnce(rejectedRequest({ code: 'TRANSIENT_FAILURE', message: 'three', retryable: true }));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(3), { timeout: 2000 });
      expect(harness.refetch).not.toHaveBeenCalled();
      expect(screen.getByText('Builder needs attention')).toBeInTheDocument();
      return;
    }

    if (schedule.mode === 'stale-refetch-invalidated' || schedule.mode === 'conflict-refetch-invalidated' || schedule.mode === 'stale-refetch-draft-changed') {
      const pendingRefresh = deferred<{ readonly data: typeof refreshedState }>();
      harness.refetch.mockReturnValue(pendingRefresh.request.unwrap());
      const failure = schedule.mode !== 'conflict-refetch-invalidated'
        ? { code: 'STALE_CATALOG_SNAPSHOT', message: 'stale receipt', retryable: false }
        : { code: 'DRAFT_CONFLICT', message: 'draft conflict', retryable: false };
      harness.reconcile.mockReturnValueOnce(rejectedRequest(failure));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.refetch).toHaveBeenCalledTimes(1));
      const refreshedWithNewHead = { ...refreshedState, draftVersion: 9, draftDigest: 'draft-9' };
      if (schedule.mode === 'stale-refetch-invalidated') {
        view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-b" explorerId="patients" /></LoomProvider>);
      } else if (schedule.mode === 'stale-refetch-draft-changed') {
        harness.state = { ...multiState, draftVersion: 2, draftDigest: 'draft-2', catalog: { ...catalog, snapshotToken: 'snapshot-fresh' } };
        harness.reconcile.mockImplementation((args: { readonly snapshotToken: string }) => resolved({ ...multiReceipt, snapshotToken: args.snapshotToken, receiptId: 'receipt-fresh' }));
        view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
        await waitFor(() => expect(screen.getByTestId('construction-workspace')).toHaveAttribute('data-draft-version', '2'));
        await waitFor(() => expect(harness.reconcile.mock.calls.some((call) => call[0].snapshotToken === 'snapshot-fresh')).toBe(true));
        await waitFor(() => expect((harness.client.getConstructionCapabilities as ReturnType<typeof vi.fn>).mock.calls.some((call) => call[0].snapshotToken === 'snapshot-fresh' && call[0].expectedDraftVersion === 2)).toBe(true));
      } else {
        view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="observations" /></LoomProvider>);
      }
      const callsBeforeResume = harness.reconcile.mock.calls.length;
      await act(async () => pendingRefresh.resolve({ data: refreshedWithNewHead }));
      expect(harness.reconcile).toHaveBeenCalledTimes(callsBeforeResume);
      expect(harness.refetch).toHaveBeenCalledTimes(schedule.expectedRefetches);
      expect(screen.getByTestId('construction-workspace')).not.toHaveAttribute('data-draft-version', '9');
      if (schedule.mode === 'stale-refetch-draft-changed') {
        expect(screen.getByTestId('construction-workspace')).toHaveAttribute('data-draft-digest', 'draft-2');
        expect(harness.reconcile.mock.calls.some((call) => call[0].snapshotToken === 'snapshot-2')).toBe(false);
        const previewCallsBeforeAction = harness.preview.mock.calls.length;
        fireEvent.change(screen.getByRole('combobox', { name: 'Preview row limit' }), { target: { value: '50' } });
        await waitFor(() => expect(harness.preview.mock.calls.length).toBeGreaterThan(previewCallsBeforeAction));
        expect(harness.preview.mock.calls.at(-1)?.[0]).toMatchObject({ receiptId: 'receipt-fresh', outputId: 'patients' });
      }
      return;
    }

    if (schedule.mode === 'conflict-no-data') {
      harness.refetch.mockResolvedValueOnce({ data: undefined });
      harness.reconcile.mockReturnValueOnce(rejectedRequest({ code: 'DRAFT_CONFLICT', message: 'draft conflict', retryable: false }));
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.refetch).toHaveBeenCalledTimes(1));
      expect(harness.reconcile).toHaveBeenCalledTimes(schedule.expectedRequests);
      expect(screen.getByText('Builder needs attention')).toBeInTheDocument();
      return;
    }

    if (schedule.mode === 'backoff-detach') {
      const pending = deferredRequest<ExplorerBuilderCompileResult>();
      let resumeBackoff: (() => void) | undefined;
      harness.reconcile.mockReturnValueOnce(pending.request);
      fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
      await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(1));
      vi.spyOn(window, 'setTimeout').mockImplementation(((callback: TimerHandler) => {
        resumeBackoff = callback as () => void;
        return 1;
      }) as typeof window.setTimeout);
      await act(async () => {
        pending.reject({ code: 'TRANSIENT_FAILURE', message: 'retry later', retryable: true });
        for (let turn = 0; turn < 10 && !resumeBackoff; turn += 1) await Promise.resolve();
      });
      expect(resumeBackoff).toBeDefined();
      view.unmount();
      await act(async () => resumeBackoff?.());
      vi.restoreAllMocks();
      expect(harness.reconcile).toHaveBeenCalledTimes(schedule.expectedRequests);
      expect(harness.refetch).toHaveBeenCalledTimes(schedule.expectedRefetches);
      return;
    }

    const pending = deferredRequest<ExplorerBuilderCompileResult>();
    harness.reconcile.mockReturnValueOnce(pending.request);
    fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
    await waitFor(() => expect(harness.reconcile).toHaveBeenCalledTimes(1));
    if (schedule.mode === 'owner-detach') view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-b" explorerId="patients" /></LoomProvider>);
    else if (schedule.mode === 'identity-advance') {
      harness.state = { ...multiState, draftVersion: 2, draftDigest: 'draft-2' };
      view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
      await waitFor(() => expect(screen.getByTestId('construction-workspace')).toHaveAttribute('data-draft-version', '2'));
    }
    const callsBeforeStaleFailure = harness.reconcile.mock.calls.length;
    const refetches = harness.refetch.mock.calls.length;
    const previewCalls = harness.preview.mock.calls.length;
    await act(async () => pending.reject({ code: 'STALE_CATALOG_SNAPSHOT', message: 'stale after navigation', retryable: false }));
    expect(harness.refetch).toHaveBeenCalledTimes(refetches);
    expect(harness.preview).toHaveBeenCalledTimes(previewCalls);
    expect(harness.reconcile).toHaveBeenCalledTimes(callsBeforeStaleFailure);
    expect(baseline).toBeGreaterThan(0);
  });
});
