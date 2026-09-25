// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type {
  GetConstructionCapabilitiesArgs,
  ProposeConstructionArgs,
} from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  useConstructionLifecycle,
  type ConstructionLifecycleClient,
} from './useConstructionLifecycle';

const capabilitiesFor = (
  args: GetConstructionCapabilitiesArgs,
): ConstructionCapabilitiesResponse => {
  const stage = {
    id: args.stageId,
    inputStageId: '',
    rowIdentityColumn: 'source-row-id',
    columns: [{ id: 'value-id', name: 'value', label: 'Value', type: 'decimal' }],
    capabilities: [
      { kind: 'FILTER' as const, supported: true },
      { kind: 'DERIVE' as const, supported: true },
      { kind: 'PIVOT' as const, supported: false, reason: 'Not available.' },
      { kind: 'UNPIVOT' as const, supported: false, reason: 'Not available.' },
    ],
  };
  return {
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    outputId: args.outputId,
    stageId: args.stageId,
    baseConstruction: { version: 1, steps: [] },
    stages: [stage],
    selectedStage: stage,
  };
};

const proposalResponse = (): ConstructionProposalResponse => ({
  proposalId: 'proposal-1',
  outputId: 'patients',
  snapshotToken: 'snapshot-1',
  draftVersion: 3,
  draftDigest: 'draft-3',
  baseDocumentDigest: 'document-3',
  candidateWorkspaceDigest: 'candidate-4',
  changedStepId: '',
  candidateConstruction: { version: 1, steps: [] },
  dependencyImpact: { affectedStepIds: [] },
  stages: [],
  previewStatus: 'READY',
  previewDurationMs: 15,
});

const proposalPreview: ExplorerBuilderPreviewResult = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview',
  receiptId: 'proposal-1',
  outputId: 'patients',
  columns: [{
    column: 'value',
    label: 'Value',
    logicalType: 'decimal',
    filterable: true,
    chartable: false,
  }],
  rows: [{ value: 12 }],
  rowCount: 1,
  diagnostics: [],
};

const request: GetConstructionCapabilitiesArgs = {
  project: 'org/project',
  explorerId: 'explorer-1',
  authResourcePath: '/programs/org/projects/project',
  snapshotToken: 'snapshot-1',
  expectedDraftVersion: 3,
  expectedDraftDigest: 'draft-3',
  outputId: 'patients',
  stageId: 'source_projection',
};

afterEach(() => vi.useRealTimers());

describe('useConstructionLifecycle', () => {
  it('loads an exact candidate preview before enabling Apply and sends removal-only requests without changedStepId', async () => {
    vi.useFakeTimers();
    const proposeConstruction = vi.fn(async (
      _args: ProposeConstructionArgs,
      _signal?: AbortSignal,
    ) => proposalResponse());
    const preview = vi.fn(async () => proposalPreview);
    const client = {
      getConstructionCapabilities: vi.fn(async (args) => capabilitiesFor(args)),
      proposeConstruction,
      preview,
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: request,
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(result.current.capabilities.status).toBe('ready');

    act(() => result.current.onCandidateChange({
      candidateConstruction: { version: 1, steps: [] },
      removeStepIds: ['derive-bmi'],
    }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
      await Promise.resolve();
    });

    expect(proposeConstruction).toHaveBeenCalledWith(
      expect.objectContaining({
        outputId: 'patients',
        candidateConstruction: { version: 1, steps: [] },
        removeStepIds: ['derive-bmi'],
      }),
      expect.any(AbortSignal),
    );
    const proposalArgs = proposeConstruction.mock.calls[0]?.[0];
    expect(proposalArgs).toBeDefined();
    expect(Object.hasOwn(proposalArgs ?? {}, 'changedStepId')).toBe(false);
    expect(preview).toHaveBeenCalledWith(
      expect.objectContaining({ receiptId: 'proposal-1', outputId: 'patients' }),
      expect.any(AbortSignal),
    );
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);

    let proposalId: string | undefined;
    act(() => {
      proposalId = result.current.beginApply();
    });
    expect(proposalId).toBe('proposal-1');
    expect(result.current.proposal.status).toBe('applying');
    expect(result.current.canApply).toBe(false);
    act(() => result.current.finishApply(true));
    expect(result.current.proposal.status).toBe('idle');
    unmount();
  });

  it('does not install an older proposal after a newer candidate replaces it', async () => {
    vi.useFakeTimers();
    let resolveOld: ((response: ConstructionProposalResponse) => void) | undefined;
    let resolveNew: ((response: ConstructionProposalResponse) => void) | undefined;
    const pendingOld = new Promise<ConstructionProposalResponse>((resolve) => {
      resolveOld = resolve;
    });
    const pendingNew = new Promise<ConstructionProposalResponse>((resolve) => {
      resolveNew = resolve;
    });
    let proposalCallCount = 0;
    const proposeConstruction = vi.fn((
      _args: ProposeConstructionArgs,
      _signal?: AbortSignal,
    ) => {
      proposalCallCount += 1;
      return proposalCallCount === 1 ? pendingOld : pendingNew;
    });
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      proposeConstruction,
      preview: vi.fn(async (args) => ({ ...proposalPreview, receiptId: args.receiptId })),
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: request,
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    act(() => result.current.onCandidateChange({
      candidateConstruction: { version: 1, steps: [] },
      changedStepId: 'old-step',
    }));
    await act(async () => vi.advanceTimersByTimeAsync(300));
    expect(proposeConstruction).toHaveBeenCalledOnce();

    act(() => result.current.onCandidateChange({
      candidateConstruction: { version: 1, steps: [] },
      changedStepId: 'new-step',
    }));
    await act(async () => vi.advanceTimersByTimeAsync(300));
    expect(proposeConstruction).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveNew?.({ ...proposalResponse(), proposalId: 'new-proposal' });
      await Promise.resolve();
      await Promise.resolve();
    });
    await act(async () => {
      resolveOld?.({ ...proposalResponse(), proposalId: 'old-proposal' });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.proposal.status).toBe('ready');
    if (result.current.proposal.status === 'ready') {
      expect(result.current.proposal.response.proposalId).toBe('new-proposal');
    }
    expect(result.current.canApply).toBe(true);
    unmount();
  });
});
