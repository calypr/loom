// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LoomProvider } from '../../../react';
import { createLoomClient } from '../../../api';
import BuilderWorkspace from '../BuilderWorkspace';
import { reconcileResultIsCurrent } from './reconcileRecovery';
import { EXPLORER_AUTHORING_API_VERSION } from '../../../types';
import { publication_acknowledgement as cases, publication_identity_guards as identityGuards } from '../../../../testdata/schedules/publication_acknowledgement';

const harness = vi.hoisted(() => ({
  state: undefined as unknown,
  refetch: vi.fn(),
  publish: vi.fn(),
  reconcile: vi.fn(),
  preview: vi.fn(),
  client: {} as Record<string, unknown>,
}));

vi.mock('../../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../react')>();
  return {
    ...actual,
    useLoomClient: () => harness.client,
    useGetExplorerAuthoringExplorersQuery: () => ({ data: [{ explorerId: 'patients', title: 'Patients' }], isLoading: false, refetch: vi.fn() }),
    useResolvePopulationSelectionQuery: () => ({ data: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
    useGetExplorerBuilderStateV2Query: vi.fn(() => ({ data: harness.state, isLoading: false, refetch: harness.refetch })),
    useGetExplorerAuthoringCapabilityV2Query: () => ({ data: { features: { deleteExplorer: false } } }),
    useReconcileExplorerBuilderV2Mutation: () => [harness.reconcile, { isLoading: false }],
    usePreviewExplorerAuthoringV2Mutation: () => [harness.preview, { isLoading: false }],
    usePublishExplorerAuthoringV2Mutation: vi.fn(() => [harness.publish, { isLoading: false }]),
    useApplyExplorerBuilderCommandsV2Mutation: () => [vi.fn(), { isLoading: false }],
    useAssessExplorerRowChangeMutation: () => [vi.fn(), { isLoading: false }],
    useCreateExplorerAuthoringMutation: () => [vi.fn(), { isLoading: false }],
    useDeleteExplorerAuthoringMutation: () => [vi.fn(), { isLoading: false }],
    useGetExplorerCandidateSuggestionsV2Mutation: () => [vi.fn(), { isLoading: false }],
    usePopulationMappingMutation: () => [vi.fn(), { isLoading: false }],
    useResolveConfiguredColumnContextsQuery: () => ({ data: undefined, isLoading: false, isFetching: false, refetch: vi.fn() }),
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
const rejected = (error: unknown) => ({ unwrap: vi.fn().mockRejectedValue(error), abort: vi.fn() });
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>((yes) => { resolve = yes; }); return { request: { unwrap: () => promise, abort: vi.fn() }, resolve }; };

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

describe('publication acknowledgement mounted schedules', () => {
  it.each(cases.filter((schedule) => schedule.mode !== 'explorer-mismatch'))('$name', async (schedule) => {
    const inputMatches = Object.entries(schedule.input).filter(([field]) => field !== 'previewRecompiled').every(([, matches]) => matches);
    expect(inputMatches).toBe(schedule.expectedAccepted);
    const pending = deferred<Record<string, never>>();
    harness.publish.mockReturnValueOnce(pending.request);
    const view = render(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
    await waitFor(() => expect(harness.preview).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByTestId('construction-preview')).toHaveAttribute('data-preview-status', 'ready'));
    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));
    await waitFor(() => expect(harness.publish).toHaveBeenCalledTimes(1));

    if (schedule.mode === 'preview-recompiled') {
      harness.preview.mockReturnValueOnce(rejected({ code: 'COMPILE_RECEIPT_NOT_FOUND', message: 'preview receipt expired', retryable: false }));
      const before = harness.preview.mock.calls.length;
      const reconcilesBeforeRefresh = harness.reconcile.mock.calls.length;
      fireEvent.change(screen.getByRole('combobox', { name: 'Preview row limit' }), { target: { value: '50' } });
      await waitFor(() => expect(harness.preview.mock.calls.length).toBeGreaterThan(before));
      await waitFor(() => expect(harness.reconcile.mock.calls.length).toBeGreaterThan(reconcilesBeforeRefresh));
      expect(harness.reconcile.mock.calls.at(-1)?.[0]).toMatchObject({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1' });
      expect(harness.reconcile.mock.calls.map((call) => call[0].draftDigest)).toEqual(Array(harness.reconcile.mock.calls.length).fill('draft-1'));
    } else if (schedule.mode === 'draft-digest') {
      harness.state = { ...state, draftDigest: 'draft-2' };
      view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
      await waitFor(() => expect(screen.getByTestId('construction-workspace')).toHaveAttribute('data-draft-digest', 'draft-2'));
    } else if (schedule.mode === 'intent') {
      harness.state = { ...state, workspace: { ...workspace, explorer: { title: 'Edited intent' } } };
      view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
      await act(async () => undefined);
    } else if (schedule.mode === 'snapshot') {
      harness.reconcile.mockImplementation((args: { readonly snapshotToken: string }) => resolved({ ...receipt, snapshotToken: args.snapshotToken }));
      harness.state = { ...state, catalog: { ...catalog, snapshotToken: 'snapshot-2' } };
      view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-a" explorerId="patients" /></LoomProvider>);
      await waitFor(() => expect(harness.reconcile.mock.calls.some((call) => call[0].snapshotToken === 'snapshot-2')).toBe(true));
    } else if (schedule.mode === 'owner-switch') {
      view.rerender(<LoomProvider client={createLoomClient()}><BuilderWorkspace project="project-b" explorerId="patients" /></LoomProvider>);
    } else if (schedule.mode === 'workspace-detached') {
      view.unmount();
    }
    await act(async () => pending.resolve({}));
    expect(harness.publish).toHaveBeenCalledWith(expect.objectContaining({ receiptId: 'receipt-1' }));
    if (schedule.expectedAccepted) await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled());
    else if (schedule.mode !== 'workspace-detached') await waitFor(() => expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled());
    if (schedule.mode === 'workspace-detached') expect(harness.publish).toHaveBeenCalledTimes(1);
  });

  it.each(identityGuards)('$name', (guard) => {

    expect(reconcileResultIsCurrent({
      generation: 1,
      currentGeneration: 1,
      submittedExplorerId: 'patients',
      currentExplorerId: guard.input.explorerMatches ? 'patients' : 'observations',
      submittedFingerprint: 'intent-1',
      currentFingerprint: 'intent-1',
    })).toBe(guard.input.explorerMatches);
  });
});
