// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type {
  DiscoverConstructionCategoriesArgs,
  GetConstructionCapabilitiesArgs,
  ProposeConstructionArgs,
} from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type ConstructionCategoryDiscoveryResponse,
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
    groupFieldChoices: [],
    groupRowValueChoices: [],
    workspaceInputs: [],
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
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
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

const categoriesFor = (args: DiscoverConstructionCategoriesArgs): ConstructionCategoryDiscoveryResponse => ({
  snapshotToken: args.snapshotToken,
  draftVersion: args.expectedDraftVersion,
  draftDigest: args.expectedDraftDigest,
  outputId: args.outputId,
  stageId: args.stageId,
  categoryColumnId: args.categoryColumnId,
  valueColumnId: args.valueColumnId,
  outcome: 'COMPLETE',
  complete: true,
  proofFingerprint: 'category-proof',
  categories: [{ key: { kind: 'STRING', string: 'final' }, label: 'final' }],
});

afterEach(() => vi.useRealTimers());

describe('useConstructionLifecycle', () => {
  it('keeps the candidate callback stable when the same draft request is rebuilt during preview', async () => {
    vi.useFakeTimers();
    const proposeConstruction = vi.fn(async () => proposalResponse());
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs) => categoriesFor(args)),
      proposeConstruction,
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, rerender } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: { ...request },
    }));
    await act(async () => { await Promise.resolve(); });
    const candidateCallback = result.current.onCandidateChange;
    act(() => result.current.onCandidateChange({ candidateConstruction: { version: 1, steps: [] } }));
    rerender();
    expect(result.current.onCandidateChange).toBe(candidateCallback);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    expect(proposeConstruction).toHaveBeenCalledTimes(1);
    expect(result.current.proposal.status).toBe('ready');
  });

  it('loads an exact candidate preview before enabling Apply and sends removal-only requests without changedStepId', async () => {
    vi.useFakeTimers();
    const proposeConstruction = vi.fn(async (
      _args: ProposeConstructionArgs,
      _signal?: AbortSignal,
    ) => proposalResponse());
    const preview = vi.fn(async () => proposalPreview);
    const client = {
      getConstructionCapabilities: vi.fn(async (args) => capabilitiesFor(args)),
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs) => categoriesFor(args)),
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

  it('awaits each proposal so a related-field selection can be repaired and resubmitted', async () => {
    vi.useFakeTimers();
    let attempts = 0;
    const proposeConstruction = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new Error('ONE has multiple distinct values.');
      return proposalResponse();
    });
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs) => categoriesFor(args)),
      proposeConstruction,
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: request,
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    let firstAttempt!: ReturnType<typeof result.current.proposeCandidateAndWait>;
    act(() => {
      firstAttempt = result.current.proposeCandidateAndWait({
        candidateConstruction: { version: 1, steps: [] },
        changedStepId: 'related-one',
      });
    });
    const failed = expect(firstAttempt).rejects.toThrow('ONE has multiple distinct values.');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
      await failed;
    });
    expect(result.current.proposal).toMatchObject({ status: 'error', message: 'ONE has multiple distinct values.' });

    let repairedAttempt!: ReturnType<typeof result.current.proposeCandidateAndWait>;
    act(() => {
      repairedAttempt = result.current.proposeCandidateAndWait({
        candidateConstruction: { version: 1, steps: [] },
        changedStepId: 'related-all',
      });
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
      await expect(repairedAttempt).resolves.toMatchObject({ status: 'ready', response: { proposalId: 'proposal-1' } });
    });
    expect(proposeConstruction).toHaveBeenCalledTimes(2);
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);
    let proposalId: string | undefined;
    await act(async () => {
      const ready = await repairedAttempt;
      proposalId = result.current.beginApply(ready);
    });
    expect(proposalId).toBe('proposal-1');
    expect(result.current.proposal.status).toBe('applying');
    unmount();
  });

  it('rejects an awaited proposal when its draft owner changes or the hook unmounts', async () => {
    vi.useFakeTimers();
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs) => categoriesFor(args)),
      proposeConstruction: vi.fn(async () => proposalResponse()),
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, rerender, unmount } = renderHook(
      ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
        useConstructionLifecycle({ client, capabilitiesRequest }),
      { initialProps: { capabilitiesRequest: request } },
    );
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    let navigated!: ReturnType<typeof result.current.proposeCandidateAndWait>;
    act(() => {
      navigated = result.current.proposeCandidateAndWait({
        candidateConstruction: { version: 1, steps: [] },
        changedStepId: 'related-before-navigation',
      });
    });
    const navigationAbort = expect(navigated).rejects.toMatchObject({ name: 'AbortError' });
    await act(async () => {
      rerender({ capabilitiesRequest: { ...request, expectedDraftVersion: 4, expectedDraftDigest: 'draft-4' } });
      await Promise.resolve();
      await Promise.resolve();
    });
    await navigationAbort;

    let unmounted!: ReturnType<typeof result.current.proposeCandidateAndWait>;
    act(() => {
      unmounted = result.current.proposeCandidateAndWait({
        candidateConstruction: { version: 1, steps: [] },
        changedStepId: 'related-before-unmount',
      });
    });
    const unmountAbort = expect(unmounted).rejects.toMatchObject({ name: 'AbortError' });
    unmount();
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    await unmountAbort;
    expect(client.proposeConstruction).not.toHaveBeenCalled();
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
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs) => categoriesFor(args)),
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

  it('binds category results to the current stage pair and draft identity', async () => {
    let resolveFirst: ((response: ConstructionCategoryDiscoveryResponse) => void) | undefined;
    let resolveSecond: ((response: ConstructionCategoryDiscoveryResponse) => void) | undefined;
    const first = new Promise<ConstructionCategoryDiscoveryResponse>((resolve) => { resolveFirst = resolve; });
    const second = new Promise<ConstructionCategoryDiscoveryResponse>((resolve) => { resolveSecond = resolve; });
    let discoveryCalls = 0;
    const discoverConstructionCategories = vi.fn((_args: DiscoverConstructionCategoriesArgs, _signal?: AbortSignal) => {
      discoveryCalls += 1;
      return discoveryCalls === 1 ? first : second;
    });
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories,
      proposeConstruction: vi.fn(async () => proposalResponse()),
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, rerender, unmount } = renderHook(
      ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
        useConstructionLifecycle({ client, capabilitiesRequest }),
      { initialProps: { capabilitiesRequest: request } },
    );
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(result.current.capabilities.status).toBe('ready');

    const firstPair = { stageId: request.stageId, categoryColumnId: 'status-id', valueColumnId: 'value-id' };
    const secondPair = { stageId: request.stageId, categoryColumnId: 'kind-id', valueColumnId: 'value-id' };
    const firstAccepted = vi.fn();
    const secondAccepted = vi.fn();
    act(() => result.current.onDiscoverCategories(firstPair, firstAccepted));
    expect(result.current.pivotDiscovery?.status).toBe('loading');
    act(() => result.current.onDiscoverCategories(secondPair, secondAccepted));
    expect(result.current.pivotDiscovery).toMatchObject({ ...secondPair, status: 'loading' });

    await act(async () => {
      resolveFirst?.(categoriesFor({ ...request, ...firstPair }));
      await Promise.resolve();
    });
    expect(firstAccepted).not.toHaveBeenCalled();
    expect(secondAccepted).not.toHaveBeenCalled();
    expect(result.current.pivotDiscovery).toMatchObject({ ...secondPair, status: 'loading' });
    await act(async () => {
      resolveSecond?.(categoriesFor({ ...request, ...secondPair }));
      await Promise.resolve();
    });
    expect(result.current.pivotDiscovery).toMatchObject({ ...secondPair, status: 'complete' });
    expect(firstAccepted).not.toHaveBeenCalled();
    expect(secondAccepted).toHaveBeenCalledWith(expect.objectContaining({ ...secondPair, status: 'complete' }));

    rerender({ capabilitiesRequest: { ...request, expectedDraftVersion: request.expectedDraftVersion + 1 } });
    expect(result.current.pivotDiscovery).toBeUndefined();
    unmount();
  });

  it('delivers only the current source-backed Pivot identity to its accepted-result callback', async () => {
    let resolveFirst: ((response: ConstructionCategoryDiscoveryResponse) => void) | undefined;
    let resolveSecond: ((response: ConstructionCategoryDiscoveryResponse) => void) | undefined;
    const first = new Promise<ConstructionCategoryDiscoveryResponse>((resolve) => { resolveFirst = resolve; });
    const second = new Promise<ConstructionCategoryDiscoveryResponse>((resolve) => { resolveSecond = resolve; });
    let discoveryCalls = 0;
    const discoverConstructionCategories = vi.fn((_args: DiscoverConstructionCategoriesArgs, _signal?: AbortSignal) => {
      discoveryCalls += 1;
      return discoveryCalls === 1 ? first : second;
    });
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories,
      proposeConstruction: vi.fn(async () => proposalResponse()),
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({ client, capabilitiesRequest: request }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const firstRequest = {
      stageId: request.stageId,
      categoryColumnId: 'category-id',
      valueColumnId: 'value-id',
      pivotStepId: 'pivot-step-one',
      groupKeyIds: ['row-one'],
      pivotSources: [{ choiceId: 'category-source', columnId: 'category-id' }],
      candidateConstruction: { version: 1, steps: [] } satisfies NonNullable<DiscoverConstructionCategoriesArgs['candidateConstruction']>,
    };
    const secondRequest = { ...firstRequest, groupKeyIds: ['row-two'] };
    const firstAccepted = vi.fn();
    const secondAccepted = vi.fn();
    act(() => result.current.onDiscoverCategories(firstRequest, firstAccepted));
    act(() => result.current.onDiscoverCategories(secondRequest, secondAccepted));
    expect(result.current.pivotDiscovery).toMatchObject({ ...secondRequest, status: 'loading' });

    await act(async () => {
      resolveFirst?.(categoriesFor({ ...request, ...firstRequest }));
      await Promise.resolve();
    });
    expect(firstAccepted).not.toHaveBeenCalled();
    expect(secondAccepted).not.toHaveBeenCalled();
    expect(result.current.pivotDiscovery).toMatchObject({ ...secondRequest, status: 'loading' });

    await act(async () => {
      resolveSecond?.(categoriesFor({ ...request, ...secondRequest }));
      await Promise.resolve();
    });
    expect(firstAccepted).not.toHaveBeenCalled();
    expect(secondAccepted).toHaveBeenCalledWith(expect.objectContaining({
      ...secondRequest,
      status: 'complete',
    }));
    unmount();
  });

  it('terminates a synchronous category discovery failure and permits a retry', async () => {
    const failureMessage = 'The category request could not be constructed.';
    const rejectedMessage = 'The category service rejected the request.';
    let attempts = 0;
    const discoverConstructionCategories = vi.fn((args: DiscoverConstructionCategoriesArgs) => {
      attempts += 1;
      if (attempts === 1) throw new Error(failureMessage);
      if (attempts === 2) return Promise.reject(new Error(rejectedMessage));
      return Promise.resolve(categoriesFor(args));
    });
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories,
      proposeConstruction: vi.fn(async () => proposalResponse()),
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: request,
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const pair = {
      stageId: request.stageId,
      categoryColumnId: 'category-id',
      valueColumnId: 'value-id',
    };
    let synchronousError: unknown;
    act(() => {
      try {
        result.current.onDiscoverCategories(pair);
      } catch (error) {
        synchronousError = error;
      }
    });

    expect(result.current.pivotDiscovery).toMatchObject({
      ...pair,
      status: 'failed',
      reason: failureMessage,
    });
    expect(synchronousError).toBeUndefined();

    act(() => result.current.onDiscoverCategories(pair));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(result.current.pivotDiscovery).toMatchObject({
      ...pair,
      status: 'failed',
      reason: rejectedMessage,
    });

    act(() => result.current.onDiscoverCategories(pair));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(discoverConstructionCategories).toHaveBeenCalledTimes(3);
    expect(result.current.pivotDiscovery).toMatchObject({
      ...pair,
      status: 'complete',
      categories: categoriesFor({ ...request, ...pair }).categories,
    });
    unmount();
  });

  it('surfaces the structured category limit outcome without treating it as a failed request', async () => {
    const message = 'This field has more than 256 category values in the current rows. Choose another category field or filter rows before pivoting.';
    const client = {
      getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
      discoverConstructionCategories: vi.fn(async (args: DiscoverConstructionCategoriesArgs): Promise<ConstructionCategoryDiscoveryResponse> => ({
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: args.stageId,
        categoryColumnId: args.categoryColumnId,
        valueColumnId: args.valueColumnId,
        outcome: 'LIMIT_EXCEEDED' as const,
        complete: false as const,
        categories: [],
        limit: 256,
        message,
      })),
      proposeConstruction: vi.fn(async () => proposalResponse()),
      preview: vi.fn(async () => proposalPreview),
    } satisfies ConstructionLifecycleClient;
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client,
      capabilitiesRequest: request,
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    act(() => result.current.onDiscoverCategories({
      stageId: request.stageId,
      categoryColumnId: 'category-id',
      valueColumnId: 'value-id',
    }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.pivotDiscovery).toMatchObject({
      status: 'limit-exceeded',
      limit: 256,
      reason: message,
    });
    unmount();
  });
});
