// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DiscoverConstructionCategoriesArgs, ExplorerAuthoringApiError, GetConstructionCapabilitiesArgs, ProposeConstructionArgs } from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type ConstructionCategoryDiscoveryResponse,
  type ConstructionProposalResponse,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
  type ConstructionLifecycleClient,
} from './useConstructionLifecycle';

import { resourceFor } from '../../../react';
import {
  reconcileReceiptMatchesRequest,
  reconcileRecoveryAction,
  reconcileResultIsCurrent,
} from '../authoring/reconcileRecovery';
import { capabilitiesFor as makeCapabilities, sourceStage } from '../../../../testSupport/constructionFixtures';

type Lifecycle = ReturnType<typeof useConstructionLifecycle>;
type ReadyProposal = Awaited<ReturnType<Lifecycle['proposeCandidateAndWait']>>;
type Settlement<T> =
  | { readonly kind: 'resolved'; readonly value: T }
  | { readonly kind: 'rejected'; readonly error: unknown };

type Deferred<T> = {
  readonly promise: Promise<T>;
  readonly resolve: (value: T) => void;
  readonly reject: (reason: unknown) => void;
};

type ProposalCall = {
  readonly args: ProposeConstructionArgs;
  readonly signal: AbortSignal | undefined;
  readonly transport: Deferred<ConstructionProposalResponse>;
};

type PreviewArgs = Parameters<ConstructionLifecycleClient['preview']>[0];

type PreviewCall = {
  readonly args: PreviewArgs;
  readonly signal: AbortSignal | undefined;
  readonly transport: Deferred<ExplorerBuilderPreviewResult>;
};

type DiscoveryCall = {
  readonly args: DiscoverConstructionCategoriesArgs;
  readonly signal: AbortSignal | undefined;
  readonly transport: Deferred<ConstructionCategoryDiscoveryResponse>;
};

type ControlledClient = {
  readonly client: ConstructionLifecycleClient;
  readonly proposals: ProposalCall[];
  readonly previews: PreviewCall[];
  readonly discoveries: DiscoveryCall[];
};

type ProposalResponseMode =
  | 'ready'
  | 'wrong-output'
  | 'wrong-snapshot'
  | 'wrong-version'
  | 'wrong-digest'
  | 'missing-receipt'
  | 'needs-repair';

type PreviewResponseMode = 'ready' | 'wrong-receipt' | 'wrong-output' | 'null-rows';

const baseRequest: GetConstructionCapabilitiesArgs = {
  project: 'org/project',
  explorerId: 'explorer-1',
  authResourcePath: '/programs/org/projects/project',
  snapshotToken: 'snapshot-1',
  expectedDraftVersion: 3,
  expectedDraftDigest: 'draft-3',
  outputId: 'patients',
  stageId: 'source_projection',
};

const capabilitiesFor = (
  args: GetConstructionCapabilitiesArgs,
): ConstructionCapabilitiesResponse => {
  const stage: ConstructionCapabilitiesResponse['selectedStage'] = sourceStage(
    [{ id: 'value-id', name: 'value', label: 'Value', type: 'decimal' }],
    {
      id: args.stageId,
      omitOperation: true,
      rowIdentityColumn: 'source-row-id',
      capabilities: [
      { kind: 'FILTER', supported: true },
      { kind: 'DERIVE', supported: true },
      { kind: 'PIVOT', supported: false, reason: 'Not available.' },
      { kind: 'UNPIVOT', supported: false, reason: 'Not available.' },
      ],
    },
  );
  return makeCapabilities({
    selectedStage: stage,
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    outputId: args.outputId,
  });
};

const deferred = <T,>(): Deferred<T> => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const observe = <T,>(promise: Promise<T>): Promise<Settlement<T>> => promise.then(
  (value) => ({ kind: 'resolved', value }),
  (error: unknown) => ({ kind: 'rejected', error }),
);

const previewFor = (
  receiptId: string,
  outputId: string,
  mode: PreviewResponseMode = 'ready',
): ExplorerBuilderPreviewResult => ({
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview',
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'FORMAL_TEST' },
  receiptId: mode === 'wrong-receipt' ? `${receiptId}-other` : receiptId,
  outputId: mode === 'wrong-output' ? `${outputId}-other` : outputId,
  columns: [],
  rows: mode === 'null-rows' ? null : [{ value: 12 }],
  rowCount: mode === 'null-rows' ? 0 : 1,
  diagnostics: [],
});

const proposalFor = (
  args: ProposeConstructionArgs,
  proposalId: string,
  mode: ProposalResponseMode = 'ready',
  includePreview = true,
): ConstructionProposalResponse => {
  const response: ConstructionProposalResponse = {
    ...(mode === 'missing-receipt' ? {} : { proposalId }),
    outputId: args.outputId,
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    baseDocumentDigest: `document-${proposalId}`,
    candidateWorkspaceDigest: `candidate-${proposalId}`,
    changedStepId: args.changedStepId ?? '',
    candidateConstruction: args.candidateConstruction,
    dependencyImpact: { affectedStepIds: [] },
    stages: [],
    previewStatus: mode === 'needs-repair' ? 'NEEDS_REPAIR' : 'READY',
    previewDurationMs: 15,
    ...(includePreview ? { preview: previewFor(proposalId, args.outputId) } : {}),
  };

  switch (mode) {
    case 'wrong-output':
      return { ...response, outputId: `${args.outputId}-other` };
    case 'wrong-snapshot':
      return { ...response, snapshotToken: `${args.snapshotToken}-other` };
    case 'wrong-version':
      return { ...response, draftVersion: args.expectedDraftVersion + 1 };
    case 'wrong-digest':
      return { ...response, draftDigest: `${args.expectedDraftDigest}-other` };
    case 'ready':
    case 'missing-receipt':
    case 'needs-repair':
      return response;
  }
};

const discoveryFor = (
  args: DiscoverConstructionCategoriesArgs,
): ConstructionCategoryDiscoveryResponse => ({
  snapshotToken: args.snapshotToken,
  draftVersion: args.expectedDraftVersion,
  draftDigest: args.expectedDraftDigest,
  outputId: args.outputId,
  stageId: args.stageId,
  categoryColumnId: args.categoryColumnId,
  valueColumnId: args.valueColumnId,
  outcome: 'COMPLETE',
  complete: true,
  proofFingerprint: 'proof-discovery',
  categories: [{ key: { kind: 'STRING', string: 'A' }, label: 'A' }],
});

const controlledClient = (): ControlledClient => {
  const proposals: ProposalCall[] = [];
  const previews: PreviewCall[] = [];
  const discoveries: DiscoveryCall[] = [];
  const client = {
    getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
    discoverConstructionCategories: vi.fn((args: DiscoverConstructionCategoriesArgs, signal?: AbortSignal) => {
      const transport = deferred<ConstructionCategoryDiscoveryResponse>();
      discoveries.push({ args, signal, transport });
      return transport.promise;
    }),
    proposeConstruction: vi.fn((args: ProposeConstructionArgs, signal?: AbortSignal) => {
      const transport = deferred<ConstructionProposalResponse>();
      proposals.push({ args, signal, transport });
      return transport.promise;
    }),
    preview: vi.fn((args: PreviewArgs, signal?: AbortSignal) => {
      const transport = deferred<ExplorerBuilderPreviewResult>();
      previews.push({ args, signal, transport });
      return transport.promise;
    }),
  } satisfies ConstructionLifecycleClient;
  return { client, proposals, previews, discoveries };
};

const intentFor = (stepId: string): ConstructionCandidateIntent => ({
  candidateConstruction: { version: 1, steps: [] },
  changedStepId: stepId,
});

const startAttempt = (
  lifecycle: { readonly current: Lifecycle },
  stepId: string,
): Promise<Settlement<ReadyProposal>> => {
  let pending!: Promise<ReadyProposal>;
  act(() => {
    pending = lifecycle.current.proposeCandidateAndWait(intentFor(stepId));
  });
  return observe(pending);
};

const flushMicrotasks = async () => {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
};

const advanceProposalDelay = async () => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(300);
    await flushMicrotasks();
  });
};

const deliver = async (resolve: () => void) => {
  await act(async () => {
    resolve();
    await flushMicrotasks();
  });
};

const expectAbort = (settlement: Settlement<unknown>) => {
  expect(settlement.kind).toBe('rejected');
  if (settlement.kind !== 'rejected') return;
  expect(settlement.error).toMatchObject({ name: 'AbortError' });
};

const identityChanges: ReadonlyArray<{
  readonly label: string;
  readonly request: GetConstructionCapabilitiesArgs;
}> = [
  { label: 'project', request: { ...baseRequest, project: 'org/other-project' } },
  { label: 'authorization context', request: { ...baseRequest, authResourcePath: '/programs/other/projects/project' } },
  { label: 'Explorer context', request: { ...baseRequest, explorerId: 'explorer-2' } },
  { label: 'snapshot token', request: { ...baseRequest, snapshotToken: 'snapshot-2' } },
  { label: 'draft version', request: { ...baseRequest, expectedDraftVersion: 4 } },
  { label: 'draft digest', request: { ...baseRequest, expectedDraftDigest: 'draft-4' } },
  { label: 'output', request: { ...baseRequest, outputId: 'observations' } },
  { label: 'stage', request: { ...baseRequest, stageId: 'stage-2' } },
  { label: 'group step', request: { ...baseRequest, groupStepId: 'group-step-1' } },
];

afterEach(() => {

  vi.useRealTimers();
});



describe('useConstructionLifecycle real-controller conformance schedules', () => {
  it('replays category discovery schedule: mount pair; schedule; complete; keep categories', async () => {

    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });


    const request = {
      stageId: baseRequest.stageId,
      categoryColumnId: 'category-id',
      valueColumnId: 'value-id',
    };
    act(() => { result.current.onDiscoverCategories(request); });
    expect(controlled.discoveries).toHaveLength(1);
    const discovery = controlled.discoveries[0];
    expect(discovery?.args.stageId).toBe(baseRequest.stageId);


    if (!discovery) throw new Error('category discovery request was not sent');

    await act(async () => {
      discovery.transport.resolve(discoveryFor(discovery.args));
      await flushMicrotasks();
    });

    expect(result.current.pivotDiscovery).toMatchObject({
      status: 'complete',
      categories: [{ label: 'A' }],
    });
    unmount();
  });

  // Finite coverage: 2 proposal orders × 3 stale results × 5 current results,
  // 2 fallback-preview orders × 5 current preview results, and the listed
  // one-field identity, cancellation, retry, A→B→A, and stale-beginApply schedules.
  const proposalOrders: ReadonlyArray<'old response first' | 'current response first'> = [
    'old response first',
    'current response first',
  ];
  const oldProposalModes: ReadonlyArray<'ready' | 'wrong base output' | 'transport error'> = [
    'ready',
    'wrong base output',
    'transport error',
  ];
  const currentProposalModes: ReadonlyArray<'ready' | 'wrong base output' | 'needs repair' | 'missing receipt' | 'transport error'> = [
    'ready',
    'wrong base output',
    'needs repair',
    'missing receipt',
    'transport error',
  ];

  for (const order of proposalOrders) {
    for (const oldMode of oldProposalModes) {
      for (const currentMode of currentProposalModes) {
        const schedule = `start A; start B replaces A; ${order}; A=${oldMode}; B=${currentMode}; check B readiness and beginApply`;
        it(`replays proposal ordering schedule: ${schedule}`, async () => {

          vi.useFakeTimers();
          const controlled = controlledClient();
          const { result, unmount } = renderHook(() => useConstructionLifecycle({
            client: controlled.client,
            capabilitiesRequest: baseRequest,
          }));
          await act(async () => { await flushMicrotasks(); });


          const oldSettlement = startAttempt(result, 'step-A');
          await advanceProposalDelay();
          expect(controlled.proposals).toHaveLength(1);
          const oldCall = controlled.proposals[0];
          if (!oldCall) throw new Error('proposal A request was not sent');


          const currentSettlement = startAttempt(result, 'step-B');
          await advanceProposalDelay();
          expect(controlled.proposals).toHaveLength(2);
          const currentCall = controlled.proposals[1];
          if (!currentCall) throw new Error('proposal B request was not sent');

          expect(oldCall?.signal?.aborted).toBe(true);
          expectAbort(await oldSettlement);

          const resolveOld = () => {
            if (oldMode === 'transport error') {
              oldCall?.transport.reject(new Error('Old proposal transport failed.'));
              return;
            }
            oldCall?.transport.resolve(proposalFor(
              oldCall.args,
              'proposal-A',
              oldMode === 'wrong base output' ? 'wrong-output' : 'ready',
            ));
          };
          const resolveCurrent = () => {
            if (currentMode === 'transport error') {
              currentCall?.transport.reject(new Error('Current proposal transport failed.'));
              return;
            }
            const responseMode: ProposalResponseMode = currentMode === 'wrong base output'
              ? 'wrong-output'
              : currentMode === 'needs repair'
                ? 'needs-repair'
                : currentMode === 'missing receipt'
                  ? 'missing-receipt'
                  : 'ready';
            currentCall?.transport.resolve(proposalFor(currentCall.args, 'proposal-B', responseMode));
          };

          if (order === 'old response first') {
            await deliver(resolveOld);

            expect(result.current.proposal.status).toBe('previewing');
            expect(result.current.canApply).toBe(false);
            expect(result.current.beginApply()).toBeUndefined();

            await deliver(resolveCurrent);

          } else {
            await deliver(resolveCurrent);

            await deliver(resolveOld);

          }
          const currentOutcome = await currentSettlement;

          if (currentMode === 'ready') {
            expect(currentOutcome.kind).toBe('resolved');
            expect(result.current.proposal).toMatchObject({
              status: 'ready',
              response: { proposalId: 'proposal-B', outputId: baseRequest.outputId },
              preview: { receiptId: 'proposal-B', outputId: baseRequest.outputId, rows: [{ value: 12 }] },
            });
            expect(result.current.canApply).toBe(true);
            if (currentOutcome.kind === 'resolved') {
              let appliedProposalId: string | undefined;
              act(() => { appliedProposalId = result.current.beginApply(currentOutcome.value)?.proposalId; });
              expect(appliedProposalId).toBe('proposal-B');
              expect(result.current.proposal.status).toBe('applying');

            }
          } else {
            expect(currentOutcome.kind).toBe('rejected');
            expect(result.current.canApply).toBe(false);
            expect(result.current.beginApply()).toBeUndefined();

            if (currentMode === 'needs repair') {
              expect(result.current.proposal.status).toBe('needs-repair');
            } else {
              expect(result.current.proposal.status).toBe('error');
            }
          }
          unmount();
        });
      }
    }
  }

  const proposalIdentityMismatches: ReadonlyArray<{
    readonly field: string;
    readonly mode: ProposalResponseMode;
  }> = [
    { field: 'outputId', mode: 'wrong-output' },
    { field: 'snapshotToken', mode: 'wrong-snapshot' },
    { field: 'expectedDraftVersion', mode: 'wrong-version' },
    { field: 'expectedDraftDigest', mode: 'wrong-digest' },
  ];

  for (const { field, mode } of proposalIdentityMismatches) {
    it(`replays identity schedule: start A; resolve A with ${field} mismatch; inspect state and beginApply`, async () => {

      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, unmount } = renderHook(() => useConstructionLifecycle({
        client: controlled.client,
        capabilitiesRequest: baseRequest,
      }));
      await act(async () => { await flushMicrotasks(); });


      const settlement = startAttempt(result, `bad-${field}`);
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;

      await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'bad-proposal', mode)));


      expect((await settlement).kind).toBe('rejected');
      expect(result.current.proposal.status).toBe('error');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();

      unmount();
    });
  }

  const previewOrders: ReadonlyArray<'old preview first' | 'current preview first'> = [
    'old preview first',
    'current preview first',
  ];
  const currentPreviewModes: ReadonlyArray<{ readonly label: string; readonly mode: PreviewResponseMode | 'transport error' }> = [
    { label: 'matching receipt, output, and rows', mode: 'ready' },
    { label: 'wrong receipt', mode: 'wrong-receipt' },
    { label: 'wrong output', mode: 'wrong-output' },
    { label: 'null rows', mode: 'null-rows' },
    { label: 'transport error', mode: 'transport error' },
  ];

  for (const order of previewOrders) {
    for (const previewMode of currentPreviewModes) {
      const schedule = `start A; resolve A proposal to fallback preview; start B replaces A; resolve B proposal to fallback preview; ${order}; B preview=${previewMode.label}; check beginApply`;
      it(`replays fallback preview schedule: ${schedule}`, async () => {

        vi.useFakeTimers();
        const controlled = controlledClient();
        const { result, unmount } = renderHook(() => useConstructionLifecycle({
          client: controlled.client,
          capabilitiesRequest: baseRequest,
        }));
        await act(async () => { await flushMicrotasks(); });


        const oldSettlement = startAttempt(result, 'fallback-A');
        await advanceProposalDelay();
        const oldProposal = controlled.proposals[0];
        expect(oldProposal).toBeDefined();
        if (!oldProposal) return;

        await deliver(() => oldProposal.transport.resolve(proposalFor(oldProposal.args, 'fallback-proposal-A', 'ready', false)));
        expect(controlled.previews).toHaveLength(1);

        const oldPreview = controlled.previews[0];
        expect(oldPreview).toBeDefined();
        if (!oldPreview) return;


        const currentSettlement = startAttempt(result, 'fallback-B');
        await advanceProposalDelay();
        const currentProposal = controlled.proposals[1];
        expect(currentProposal).toBeDefined();
        if (!currentProposal) return;

        expect(oldProposal.signal?.aborted).toBe(true);
        expectAbort(await oldSettlement);
        await deliver(() => currentProposal.transport.resolve(proposalFor(currentProposal.args, 'fallback-proposal-B', 'ready', false)));
        expect(controlled.previews).toHaveLength(2);


        const currentPreview = controlled.previews[1];
        expect(currentPreview).toBeDefined();
        if (!currentPreview) return;


        const resolveOldPreview = () => oldPreview.transport.resolve(previewFor(oldPreview.args.receiptId, oldPreview.args.outputId));
        const resolveCurrentPreview = () => {
          if (previewMode.mode === 'transport error') {
            currentPreview.transport.reject(new Error('Current preview transport failed.'));
            return;
          }
          currentPreview.transport.resolve(previewFor(
            currentPreview.args.receiptId,
            currentPreview.args.outputId,
            previewMode.mode,
          ));
        };

        if (order === 'old preview first') {
          expect(oldPreview.signal?.aborted).toBe(true);
          await deliver(resolveOldPreview);

          expect(result.current.proposal.status).toBe('previewing');
          expect(result.current.canApply).toBe(false);
          expect(result.current.beginApply()).toBeUndefined();

          await deliver(resolveCurrentPreview);

        } else {
          await deliver(resolveCurrentPreview);

          await deliver(resolveOldPreview);

        }
        const currentOutcome = await currentSettlement;

        if (previewMode.mode === 'ready') {
          expect(currentOutcome.kind).toBe('resolved');
          expect(result.current.proposal).toMatchObject({
            status: 'ready',
            response: { proposalId: 'fallback-proposal-B' },
            preview: { receiptId: 'fallback-proposal-B', outputId: baseRequest.outputId, rows: [{ value: 12 }] },
          });
          expect(result.current.canApply).toBe(true);
          act(() => { expect(result.current.beginApply()?.proposalId).toBe('fallback-proposal-B'); });

        } else {
          expect(currentOutcome.kind).toBe('rejected');
          expect(result.current.proposal.status).toBe('error');
          expect(result.current.canApply).toBe(false);
          expect(result.current.beginApply()).toBeUndefined();

        }
        unmount();
      });
    }
  }

  const canceledProposalModes: ReadonlyArray<{ readonly label: string; readonly mode: ProposalResponseMode | 'transport error' }> = [
    { label: 'valid late response', mode: 'ready' },
    { label: 'bad late response', mode: 'wrong-output' },
    { label: 'late transport error', mode: 'transport error' },
  ];

  for (const outcome of canceledProposalModes) {
    const schedule = `start A; wait for proposal transport; explicit cancel; resolve after abort with ${outcome.label}; check idle and beginApply`;
    it(`replays explicit cancellation schedule: ${schedule}`, async () => {

      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, unmount } = renderHook(() => useConstructionLifecycle({
        client: controlled.client,
        capabilitiesRequest: baseRequest,
      }));
      await act(async () => { await flushMicrotasks(); });


      const settlement = startAttempt(result, 'cancel-A');
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;

      expect(result.current.beginApply()).toBeUndefined();

      act(() => result.current.cancel());
      expect(proposalCall.signal?.aborted).toBe(true);
      expectAbort(await settlement);
      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);

      expect(result.current.beginApply()).toBeUndefined();


      await deliver(() => {
        if (outcome.mode === 'transport error') {
          proposalCall.transport.reject(new Error('Canceled proposal transport failed late.'));
          return;
        }
        proposalCall.transport.resolve(proposalFor(
          proposalCall.args,
          'canceled-proposal',
          outcome.mode,
        ));
      });

      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();

      unmount();
    });
  }

  for (const change of identityChanges) {
    const schedule = `start A under base identity; change ${change.label}; resolve A; check mismatched owner stays hidden and cannot beginApply`;
    it(`hides an old owner after request identity change: ${schedule}`, async () => {

      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, rerender, unmount } = renderHook(
        ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
          useConstructionLifecycle({ client: controlled.client, capabilitiesRequest }),
        { initialProps: { capabilitiesRequest: baseRequest } },
      );
      await act(async () => { await flushMicrotasks(); });


      const settlement = startAttempt(result, `identity-${change.label}`);
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;

      rerender({ capabilitiesRequest: change.request });
      await act(async () => { await flushMicrotasks(); });
      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);


      expect(result.current.beginApply()).toBeUndefined();


      await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'hidden-proposal')));
      expectAbort(await settlement);

      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();

      unmount();
    });
  }

  it('replays A→B→A: start proposal at A; briefly render B; return to exact A before unsubscribe cleanup; settle and beginApply', async () => {

    vi.useFakeTimers();
    const controlled = controlledClient();
    const otherIdentity = { ...baseRequest, outputId: 'observations' };
    const { result, rerender, unmount } = renderHook(
      ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
        useConstructionLifecycle({ client: controlled.client, capabilitiesRequest }),
      { initialProps: { capabilitiesRequest: baseRequest } },
    );
    await act(async () => { await flushMicrotasks(); });


    const settlement = startAttempt(result, 'A-B-A');
    await advanceProposalDelay();
    const proposalCall = controlled.proposals[0];
    expect(proposalCall).toBeDefined();
    if (!proposalCall) return;

    rerender({ capabilitiesRequest: otherIdentity });
    expect(result.current.proposal.status).toBe('idle');
    expect(result.current.canApply).toBe(false);


    rerender({ capabilitiesRequest: baseRequest });
    await act(async () => { await flushMicrotasks(); });
    expect(proposalCall.signal?.aborted).toBe(false);


    await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'resurfaced-proposal')));
    expect((await settlement).kind).toBe('resolved');
    expect(result.current.proposal).toMatchObject({
      status: 'ready',
      response: { proposalId: 'resurfaced-proposal', outputId: baseRequest.outputId },
    });
    expect(result.current.canApply).toBe(true);

    act(() => { expect(result.current.beginApply()?.proposalId).toBe('resurfaced-proposal'); });

    unmount();
  });

  it('replays retry: fail A; retry the same candidate; resolve retry proposal and matching preview; apply only retry receipt', async () => {

    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });


    const firstSettlement = startAttempt(result, 'retry-candidate');
    await advanceProposalDelay();
    const firstCall = controlled.proposals[0];
    expect(firstCall).toBeDefined();
    if (!firstCall) return;

    await deliver(() => firstCall.transport.reject(new Error('First proposal transport failed.')));

    expect((await firstSettlement).kind).toBe('rejected');
    expect(result.current.proposal.status).toBe('error');
    expect(result.current.canApply).toBe(false);
    expect(result.current.beginApply()).toBeUndefined();


    act(() => result.current.retry());
    await advanceProposalDelay();
    expect(controlled.proposals).toHaveLength(2);
    const retryCall = controlled.proposals[1];
    expect(retryCall).toBeDefined();
    if (!retryCall) return;
    expect(retryCall.args.changedStepId).toBe('retry-candidate');
    expect(result.current.proposal.status).toBe('previewing');

    expect(result.current.canApply).toBe(false);
    expect(result.current.beginApply()).toBeUndefined();


    await deliver(() => retryCall.transport.resolve(proposalFor(
      retryCall.args,
      'retry-proposal',
      'ready',
      false,
    )));
    expect(controlled.previews).toHaveLength(1);
    const retryPreview = controlled.previews[0];
    expect(retryPreview).toBeDefined();
    if (!retryPreview) return;
    expect(retryPreview.args.receiptId).toBe('retry-proposal');
    expect(retryPreview.args.outputId).toBe(baseRequest.outputId);


    await deliver(() => retryPreview.transport.resolve(previewFor(
      'retry-proposal',
      baseRequest.outputId,
    )));


    expect(result.current.proposal).toMatchObject({
      status: 'ready',
      response: { proposalId: 'retry-proposal', outputId: baseRequest.outputId },
      preview: { receiptId: 'retry-proposal', outputId: baseRequest.outputId, rows: [{ value: 12 }] },
    });
    expect(result.current.canApply).toBe(true);
    act(() => { expect(result.current.beginApply()?.proposalId).toBe('retry-proposal'); });

    unmount();
  });

  it('replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B', async () => {

    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });


    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;

    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));

    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;

    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    const currentCall = controlled.proposals[1];
    expect(currentCall).toBeDefined();
    if (!currentCall) return;

    expect(result.current.proposal.status).toBe('previewing');
    expect(result.current.beginApply(oldOutcome.value)).toBeUndefined();

    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));

    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.beginApply(oldOutcome.value)).toBeUndefined();

    if (currentOutcome.kind === 'resolved') {
      act(() => { expect(result.current.beginApply(currentOutcome.value)?.proposalId).toBe('current-B'); });

    }
    unmount();
  });

  // Regressions for Apply completion targeting the owner that began Apply.
  it('regression: old Apply success after replacement must not cancel the replacement owner', async () => {

    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });


    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;

    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));
    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;

    let applyA: ReturnType<typeof result.current.beginApply>;
    act(() => { applyA = result.current.beginApply(oldOutcome.value); });
    expect(applyA?.proposalId).toBe('already-ready-A');


    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    const currentCall = controlled.proposals[1];
    if (!currentCall) return;

    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));
    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');


    act(() => { applyA?.finish(true); });

    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);
    unmount();
  });

  it('regression: old Apply failure after replacement must not mark the replacement owner as an error', async () => {

    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });


    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;

    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));
    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;

    let applyA: ReturnType<typeof result.current.beginApply>;
    act(() => { applyA = result.current.beginApply(oldOutcome.value); });
    expect(applyA?.proposalId).toBe('already-ready-A');


    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    const currentCall = controlled.proposals[1];
    if (!currentCall) return;

    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));
    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');


    act(() => { applyA?.finish(false); });

    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);
    unmount();
  });

  const completionIdentityCases = [
    ...identityChanges.map((change) => ({ ...change, ownsCompletion: false })),
    { label: 'equivalent authorization formatting', request: { ...baseRequest,
      authResourcePath: `  ${baseRequest.authResourcePath}  ` }, ownsCompletion: true },
  ];
  for (const change of completionIdentityCases) for (const applied of [true, false]) {
    it(`Apply completion after ${change.label} identity change: owns current editor=${change.ownsCompletion} (success=${applied})`, async () => {

      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, rerender, unmount } = renderHook(
        ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
          useConstructionLifecycle({ client: controlled.client, capabilitiesRequest }),
        { initialProps: { capabilitiesRequest: baseRequest } },
      );
      await act(async () => { await flushMicrotasks(); });

      const pending = startAttempt(result, 'identity-owned-apply');
      await advanceProposalDelay();
      const call = controlled.proposals[0];
      if (!call) throw new Error('Expected real proposal request');

      await deliver(() => call.transport.resolve(proposalFor(call.args, 'identity-apply')));
      const ready = await pending;
      if (ready.kind !== 'resolved') throw new Error('Expected reviewed proposal');

      let apply: ReturnType<typeof result.current.beginApply>;
      act(() => { apply = result.current.beginApply(ready.value); });
      if (!apply) throw new Error('Expected Apply handle');

      rerender({ capabilitiesRequest: change.request });


      const before = result.current.proposal;
      let accepted: boolean | undefined;
      act(() => { accepted = apply?.finish(applied); });

      expect(accepted).toBe(change.ownsCompletion);
      if (change.ownsCompletion) expect(result.current.proposal.status).toBe(applied ? 'idle' : 'error');
      else expect(result.current.proposal).toEqual(before);
      unmount();
    });
  }

  describe('bounded Apply ownership exploration', () => {
    for (const applied of [true, false]) for (const reuseReceipt of [false, true]) {
      for (const order of ['old-first', 'current-first'] as const) {
        it(`old Apply ${applied ? 'success' : 'failure'} after cancel and new Apply: ${order}, reused receipt=${reuseReceipt}`, async () => {

          vi.useFakeTimers();
          const controlled = controlledClient();
          const { result, unmount } = renderHook(() => useConstructionLifecycle({
            client: controlled.client, capabilitiesRequest: baseRequest,
          }));
          await act(async () => { await flushMicrotasks(); });

          const startReady = async (index: number, proposalId: string, beforeResolve?: () => void) => {
            const pending = startAttempt(result, `step-${index}`);
            await advanceProposalDelay();
            const call = controlled.proposals[index];
            if (!call) throw new Error('Expected real proposal request');

            beforeResolve?.();
            await deliver(() => call.transport.resolve(proposalFor(call.args, proposalId)));
            const settled = await pending;
            if (settled.kind !== 'resolved') throw new Error('Expected reviewed proposal');

            return { call, settled: settled.value };
          };
          const begin = (ready: { call: ProposalCall; settled: ReadyProposal }, proposalId: string) => {
            let apply: ReturnType<typeof result.current.beginApply>;
            act(() => { apply = result.current.beginApply(ready.settled); });
            expect(apply?.proposalId).toBe(proposalId);
            if (!apply) throw new Error('Expected Apply handle');

            return apply;
          };
          try {
            const oldReady = await startReady(0, 'apply-A');
            const old = { call: oldReady.call, apply: begin(oldReady, 'apply-A') };
            act(() => { result.current.cancel(); });

            const currentProposalId = reuseReceipt ? 'apply-A' : 'apply-B';
            const finishOld = () => {
              const before = result.current.proposal;
              let accepted: boolean | undefined;
              act(() => { accepted = old.apply.finish(applied); });

              expect(accepted).toBe(false);
              expect(result.current.proposal).toEqual(before);
              act(() => { expect(old.apply.finish(!applied)).toBe(false); });
              expect(result.current.proposal).toEqual(before);
            };
            const finishCurrent = (current: { call: ProposalCall; apply: NonNullable<ReturnType<typeof result.current.beginApply>> }) => {
              let accepted: boolean | undefined;
              act(() => { accepted = current.apply.finish(applied); });

              expect(accepted).toBe(true);
              expect(result.current.proposal.status).toBe(applied ? 'idle' : 'error');
              act(() => { expect(current.apply.finish(!applied)).toBe(false); });
              expect(result.current.proposal.status).toBe(applied ? 'idle' : 'error');
            };
            if (order === 'old-first') {
              const currentReady = await startReady(1, currentProposalId, finishOld);
              finishCurrent({ call: currentReady.call, apply: begin(currentReady, currentProposalId) });
            } else {
              const currentReady = await startReady(1, currentProposalId);
              act(() => { expect(result.current.beginApply(currentReady.settled)).toBeUndefined(); });
              finishOld();
              finishCurrent({ call: currentReady.call, apply: begin(currentReady, currentProposalId) });
            }
          } finally {
            unmount();
          }
        });
      }
    }
  });

  it('formal property Inv_ApplyStartedFromReviewedCurrentPreview rejects missing preview rows at beginApply', async () => {
    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({ client: controlled.client, capabilitiesRequest: baseRequest }));
    try {
      await act(async () => { await flushMicrotasks(); });
      const pending = startAttempt(result, 'bad-preview');
      await advanceProposalDelay();
      const call = controlled.proposals[0];
      if (!call) throw new Error('Expected proposal request');
      await deliver(() => call.transport.resolve(proposalFor(call.args, 'bad-preview', 'ready', false)));
      const preview = controlled.previews[0];
      if (!preview) throw new Error('Expected fallback preview request');
      await deliver(() => preview.transport.resolve(previewFor(preview.args.receiptId, preview.args.outputId, 'null-rows')));
      const outcome = await pending;
      let apply: ReturnType<typeof result.current.beginApply>;
      act(() => { apply = result.current.beginApply(outcome.kind === 'resolved' ? outcome.value : undefined); });
      expect(apply).toBeUndefined();
    } finally { unmount(); }
  });

  it('formal property Inv_DiscoveryAdoptedOnlyForCurrentIdentity ignores a replaced discovery response', async () => {
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({ client: controlled.client, capabilitiesRequest: baseRequest }));
    try {
      await act(async () => { await flushMicrotasks(); });
      const callbacks: string[] = [];
      const request = { stageId: baseRequest.stageId, categoryColumnId: 'category-A', valueColumnId: 'value-id' };
      act(() => result.current.onDiscoverCategories(request, () => callbacks.push('A')));
      act(() => result.current.onDiscoverCategories({ ...request, categoryColumnId: 'category-B' }, () => callbacks.push('B')));
      const [a, b] = controlled.discoveries;
      if (!a || !b) throw new Error('Expected two real discovery requests');
      await deliver(() => b.transport.resolve({ ...discoveryFor(b.args), categories: [{ key: { kind: 'STRING', string: 'B' }, label: 'B' }] }));
      await deliver(() => a.transport.resolve({ ...discoveryFor(a.args), categories: [{ key: { kind: 'STRING', string: 'A' }, label: 'A' }] }));
      expect(callbacks).toEqual(['B']);
      expect(result.current.pivotDiscovery).toMatchObject({ status: 'complete', categoryColumnId: 'category-B', categories: [{ label: 'B' }] });
    } finally { unmount(); }
  });

  it('formal property Inv_LateResponseDoesNotReplaceOwner keeps the latest resource refresh', async () => {
    const a = deferred<string>(), b = deferred<string>();
    let calls = 0;
    const resource = resourceFor(async () => (++calls === 1 ? a.promise : b.promise));
    const first = resource.refresh(), second = resource.refresh();
    b.resolve('B'); await second;
    expect(resource.getSnapshot().data).toBe('B');
    a.resolve('A'); await first;
    expect(resource.getSnapshot().data).toBe('B');
  });

});


const reconcileError = (code: string, retryable = false): ExplorerAuthoringApiError => ({
  status: 409,
  code,
  message: code,
  retryable,
});

const reconcileOptions = (overrides: Partial<{
  readonly attempt: number;
  readonly maxAttempts: number;
  readonly staleRefreshes: number;
  readonly maxStaleRefreshes: number;
}> = {}) => ({
  attempt: 1,
  maxAttempts: 3,
  staleRefreshes: 0,
  maxStaleRefreshes: 2,
  ...overrides,
});

const reconcileReceipt = (
  overrides: Partial<ExplorerBuilderCompileResult> = {},
): ExplorerBuilderCompileResult => ({
  snapshotToken: 'S1',
  draftVersion: 3,
  draftDigest: 'D1',
  outputs: [{ outputId: 'patients' }],
  ...overrides,
} as ExplorerBuilderCompileResult);

describe('reconcile recovery formal schedule', () => {
  it('reconcile success binds the receipt and an authored change clears it', () => {
    expect(reconcileResultIsCurrent({
      generation: 4,
      currentGeneration: 4,
      submittedExplorerId: 'patients',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F1',
      currentFingerprint: 'F1',
    })).toBe(true);
    expect(reconcileReceiptMatchesRequest(reconcileReceipt(), {
      snapshotToken: 'S1',
      outputId: 'patients',
    })).toBe(true);

  });

  it('reconcile stale snapshots refresh and retry twice, then repair', () => {
    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions())).toBe('refresh-and-retry');

    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions({ staleRefreshes: 1 }))).toBe('refresh-and-retry');

    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions({ staleRefreshes: 2 }))).toBe('repair');
  });

  it('reconcile draft conflict refreshes and stops', () => {
    expect(reconcileRecoveryAction(reconcileError('DRAFT_CONFLICT'), reconcileOptions())).toBe('refresh-and-stop');
  });

  it('reconcile invalid output repairs', () => {
    expect(reconcileRecoveryAction(reconcileError('VALIDATION_FAILED'), reconcileOptions())).toBe('repair');
  });

  it('reconcile retryable failure retries and then succeeds', () => {
    expect(reconcileRecoveryAction(reconcileError('BACKEND_UNAVAILABLE', true), reconcileOptions())).toBe('retry');
    expect(reconcileRecoveryAction(reconcileError('BACKEND_UNAVAILABLE', true), reconcileOptions({ attempt: 3 }))).toBe('repair');
  });

  it('reconcile fatal failure repairs', () => {
    expect(reconcileRecoveryAction(reconcileError('VALIDATION_FAILED'), reconcileOptions())).toBe('repair');
  });

  it('reconcile rejects stale results and mismatched receipts', () => {
    expect(reconcileResultIsCurrent({
      generation: 3,
      currentGeneration: 4,
      submittedExplorerId: 'patients',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F1',
      currentFingerprint: 'F1',
    })).toBe(false);
    expect(reconcileResultIsCurrent({
      generation: 4,
      currentGeneration: 4,
      submittedExplorerId: 'other',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F1',
      currentFingerprint: 'F1',
    })).toBe(false);
    expect(reconcileResultIsCurrent({
      generation: 4,
      currentGeneration: 4,
      submittedExplorerId: 'patients',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F0',
      currentFingerprint: 'F1',
    })).toBe(false);
    expect(reconcileReceiptMatchesRequest(reconcileReceipt({ snapshotToken: 'S2' }), {
      snapshotToken: 'S1',
      outputId: 'patients',
    })).toBe(false);
    expect(reconcileReceiptMatchesRequest(reconcileReceipt({ outputs: [{ outputId: 'other', columns: [] }] }), {
      snapshotToken: 'S1',
      outputId: 'patients',
    })).toBe(false);

  });

  it('reconcile ignores a late result after the workspace identity changes', () => {
    expect(reconcileResultIsCurrent({
      generation: 4,
      currentGeneration: 4,
      submittedExplorerId: 'patients',
      currentExplorerId: 'patients',
      submittedFingerprint: 'F1',
      currentFingerprint: 'F2',
    })).toBe(false);
    expect(reconcileReceiptMatchesRequest(reconcileReceipt({ snapshotToken: 'S2' }), {
      snapshotToken: 'S1',
      outputId: 'patients',
    })).toBe(false);
  });
});
