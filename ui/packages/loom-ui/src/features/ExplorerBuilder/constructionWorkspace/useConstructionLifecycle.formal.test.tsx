// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterAll, afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerAuthoringApiError, GetConstructionCapabilitiesArgs, ProposeConstructionArgs } from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  useConstructionLifecycle,
  type ConstructionCandidateIntent,
  type ConstructionLifecycleClient,
} from './useConstructionLifecycle';
import {
  finishTrace,
  startTrace,
  traceAction,
  traceEmbeddedPreviewExpansion,
  traceInitialObservation,
  traceInitialObservationValue,
  traceObservedAction,
  traceUnmodeled,
  writeTraces,
} from '../../../../testSupport/formalTrace';
import {
  reconcileReceiptMatchesRequest,
  reconcileRecoveryAction,
  reconcileResultIsCurrent,
} from '../authoring/reconcileRecovery';

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

type ControlledClient = {
  readonly client: ConstructionLifecycleClient;
  readonly proposals: ProposalCall[];
  readonly previews: PreviewCall[];
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
  const stage: ConstructionCapabilitiesResponse['selectedStage'] = {
    id: args.stageId,
    inputStageId: '',
    rowIdentityColumn: 'source-row-id',
    columns: [{ id: 'value-id', name: 'value', label: 'Value', type: 'decimal' }],
    capabilities: [
      { kind: 'FILTER', supported: true },
      { kind: 'DERIVE', supported: true },
      { kind: 'PIVOT', supported: false, reason: 'Not available.' },
      { kind: 'UNPIVOT', supported: false, reason: 'Not available.' },
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

const controlledClient = (): ControlledClient => {
  const proposals: ProposalCall[] = [];
  const previews: PreviewCall[] = [];
  const client = {
    getConstructionCapabilities: vi.fn(async (args: GetConstructionCapabilitiesArgs) => capabilitiesFor(args)),
    discoverConstructionCategories: vi.fn(async () => {
      throw new Error('Category discovery is outside this conformance schedule.');
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
  return { client, proposals, previews };
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
  finishTrace();
  vi.useRealTimers();
});

afterAll(() => writeTraces());

describe('useConstructionLifecycle real-controller conformance schedules', () => {
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
          startTrace();
          vi.useFakeTimers();
          const controlled = controlledClient();
          const { result, unmount } = renderHook(() => useConstructionLifecycle({
            client: controlled.client,
            capabilitiesRequest: baseRequest,
          }));
          await act(async () => { await flushMicrotasks(); });
          traceInitialObservation(result);

          const oldSettlement = startAttempt(result, 'step-A');
          await advanceProposalDelay();
          expect(controlled.proposals).toHaveLength(1);
          const oldCall = controlled.proposals[0];
          traceAction('StartProposal', result, 'propose-construction', 'start A');

          const currentSettlement = startAttempt(result, 'step-B');
          await advanceProposalDelay();
          expect(controlled.proposals).toHaveLength(2);
          const currentCall = controlled.proposals[1];
          traceAction('ReplaceProposal', result, 'propose-construction', 'start B replaces A');
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
          traceUnmodeled(
            oldMode === 'transport error' ? 'LateProposalErrorIgnored' : 'LateProposalCompleteIgnored',
            `stale A settlement after B replaced it (A=${oldMode})`,
          );

          if (currentMode === 'ready') {
            expect(currentOutcome.kind).toBe('resolved');
            expect(result.current.proposal).toMatchObject({
              status: 'ready',
              response: { proposalId: 'proposal-B', outputId: baseRequest.outputId },
              preview: { receiptId: 'proposal-B', outputId: baseRequest.outputId, rows: [{ value: 12 }] },
            });
            expect(result.current.canApply).toBe(true);
            traceEmbeddedPreviewExpansion(result);
            if (currentOutcome.kind === 'resolved') {
              let appliedProposalId: string | undefined;
              act(() => { appliedProposalId = result.current.beginApply(currentOutcome.value); });
              expect(appliedProposalId).toBe('proposal-B');
              expect(result.current.proposal.status).toBe('applying');
              traceAction('BeginApply', result, 'begin-apply');
            }
          } else {
            expect(currentOutcome.kind).toBe('rejected');
            expect(result.current.canApply).toBe(false);
            expect(result.current.beginApply()).toBeUndefined();
            traceAction('ProposalError', result, 'proposal-response', `B=${currentMode}`);
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
      startTrace();
      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, unmount } = renderHook(() => useConstructionLifecycle({
        client: controlled.client,
        capabilitiesRequest: baseRequest,
      }));
      await act(async () => { await flushMicrotasks(); });
      traceInitialObservation(result);

      const settlement = startAttempt(result, `bad-${field}`);
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;
      traceAction('StartProposal', result, 'propose-construction');
      await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'bad-proposal', mode)));

      expect((await settlement).kind).toBe('rejected');
      traceAction('ProposalError', result, 'proposal-response', `${field} mismatch`);
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
        startTrace();
        vi.useFakeTimers();
        const controlled = controlledClient();
        const { result, unmount } = renderHook(() => useConstructionLifecycle({
          client: controlled.client,
          capabilitiesRequest: baseRequest,
        }));
        await act(async () => { await flushMicrotasks(); });
        traceInitialObservation(result);

        const oldSettlement = startAttempt(result, 'fallback-A');
        await advanceProposalDelay();
        const oldProposal = controlled.proposals[0];
        expect(oldProposal).toBeDefined();
        if (!oldProposal) return;
        traceAction('StartProposal', result, 'propose-construction', 'start fallback A');
        await deliver(() => oldProposal.transport.resolve(proposalFor(oldProposal.args, 'fallback-proposal-A', 'ready', false)));
        expect(controlled.previews).toHaveLength(1);
        traceAction('ProposalComplete', result, 'proposal-response', 'fallback A proposal accepted without embedded preview');
        traceAction('StartPreview', result, 'preview', 'fallback A preview started');

        const currentSettlement = startAttempt(result, 'fallback-B');
        await advanceProposalDelay();
        const currentProposal = controlled.proposals[1];
        expect(currentProposal).toBeDefined();
        if (!currentProposal) return;
        traceAction('ReplaceProposal', result, 'propose-construction', 'start fallback B replaces A');
        expect(oldProposal.signal?.aborted).toBe(true);
        expectAbort(await oldSettlement);
        await deliver(() => currentProposal.transport.resolve(proposalFor(currentProposal.args, 'fallback-proposal-B', 'ready', false)));
        expect(controlled.previews).toHaveLength(2);
        traceAction('ProposalComplete', result, 'proposal-response', 'fallback B proposal accepted without embedded preview');
        traceAction('StartPreview', result, 'preview', 'fallback B preview started');

        const oldPreview = controlled.previews[0];
        const currentPreview = controlled.previews[1];
        expect(oldPreview).toBeDefined();
        expect(currentPreview).toBeDefined();
        if (!oldPreview || !currentPreview) return;

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
        traceUnmodeled('LatePreviewCompleteIgnored', 'stale fallback A preview settlement after B replaced it');

        if (previewMode.mode === 'ready') {
          expect(currentOutcome.kind).toBe('resolved');
          expect(result.current.proposal).toMatchObject({
            status: 'ready',
            response: { proposalId: 'fallback-proposal-B' },
            preview: { receiptId: 'fallback-proposal-B', outputId: baseRequest.outputId, rows: [{ value: 12 }] },
          });
          expect(result.current.canApply).toBe(true);
          traceAction('PreviewComplete', result, 'preview-response', 'fallback B preview accepted');
          act(() => { expect(result.current.beginApply()).toBe('fallback-proposal-B'); });
          traceAction('BeginApply', result, 'begin-apply');
        } else {
          expect(currentOutcome.kind).toBe('rejected');
          expect(result.current.proposal.status).toBe('error');
          expect(result.current.canApply).toBe(false);
          expect(result.current.beginApply()).toBeUndefined();
          traceAction('PreviewComplete', result, 'preview-response', `fallback B preview ${previewMode.label}`);
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
      startTrace();
      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, unmount } = renderHook(() => useConstructionLifecycle({
        client: controlled.client,
        capabilitiesRequest: baseRequest,
      }));
      await act(async () => { await flushMicrotasks(); });
      traceInitialObservation(result);

      const settlement = startAttempt(result, 'cancel-A');
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;
      traceAction('StartProposal', result, 'propose-construction', 'start A before cancel');
      act(() => result.current.cancel());
      expect(proposalCall.signal?.aborted).toBe(true);
      expectAbort(await settlement);
      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();
      traceAction('CancelProposal', result, 'cancel', 'explicit cancel');

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
      traceUnmodeled(
        outcome.mode === 'transport error' ? 'LateProposalErrorIgnored' : 'LateProposalCompleteIgnored',
        `canceled A settlement after explicit cancel (${outcome.label})`,
      );
      unmount();
    });
  }

  for (const change of identityChanges) {
    const schedule = `start A under base identity; change ${change.label}; resolve A; check mismatched owner stays hidden and cannot beginApply`;
    it(`hides an old owner after request identity change: ${schedule}`, async () => {
      startTrace();
      vi.useFakeTimers();
      const controlled = controlledClient();
      const { result, rerender, unmount } = renderHook(
        ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
          useConstructionLifecycle({ client: controlled.client, capabilitiesRequest }),
        { initialProps: { capabilitiesRequest: baseRequest } },
      );
      await act(async () => { await flushMicrotasks(); });
      traceInitialObservation(result);

      const settlement = startAttempt(result, `identity-${change.label}`);
      await advanceProposalDelay();
      const proposalCall = controlled.proposals[0];
      expect(proposalCall).toBeDefined();
      if (!proposalCall) return;
      traceAction('StartProposal', result, 'propose-construction', 'start A under base identity');
      rerender({ capabilitiesRequest: change.request });
      await act(async () => { await flushMicrotasks(); });
      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();
      traceAction('ChangeDraft', result, 'identity-change', `change ${change.label}`);

      await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'hidden-proposal')));
      expectAbort(await settlement);
      traceUnmodeled('LateProposalCompleteIgnored', `old owner A settlement after ${change.label} identity change`);
      expect(result.current.proposal.status).toBe('idle');
      expect(result.current.canApply).toBe(false);
      expect(result.current.beginApply()).toBeUndefined();
      unmount();
    });
  }

  it('replays A→B→A: start proposal at A; briefly render B; return to exact A before unsubscribe cleanup; settle and beginApply', async () => {
    startTrace('ConstructionLifecycle');
    vi.useFakeTimers();
    const controlled = controlledClient();
    const otherIdentity = { ...baseRequest, outputId: 'observations' };
    const { result, rerender, unmount } = renderHook(
      ({ capabilitiesRequest }: { readonly capabilitiesRequest: GetConstructionCapabilitiesArgs }) =>
        useConstructionLifecycle({ client: controlled.client, capabilitiesRequest }),
      { initialProps: { capabilitiesRequest: baseRequest } },
    );
    await act(async () => { await flushMicrotasks(); });
    traceInitialObservation(result);

    const settlement = startAttempt(result, 'A-B-A');
    await advanceProposalDelay();
    const proposalCall = controlled.proposals[0];
    expect(proposalCall).toBeDefined();
    if (!proposalCall) return;
    traceAction('StartProposal(P1)', result, 'propose-construction', 'start A');
    rerender({ capabilitiesRequest: otherIdentity });
    expect(result.current.proposal.status).toBe('idle');
    expect(result.current.canApply).toBe(false);
    traceAction('ChangeDraftIdentity(A-to-B)', result, 'identity-change', 'render B');
    rerender({ capabilitiesRequest: baseRequest });
    await act(async () => { await flushMicrotasks(); });
    expect(proposalCall.signal?.aborted).toBe(false);
    traceAction('ChangeDraftIdentity(B-to-A)', result, 'identity-change', 'return to exact A');
    await deliver(() => proposalCall.transport.resolve(proposalFor(proposalCall.args, 'resurfaced-proposal')));
    expect((await settlement).kind).toBe('resolved');
    expect(result.current.proposal).toMatchObject({
      status: 'ready',
      response: { proposalId: 'resurfaced-proposal', outputId: baseRequest.outputId },
    });
    expect(result.current.canApply).toBe(true);
    traceAction('ProposalComplete(P1)', result, 'proposal-response', 'P1 accepted with embedded preview');
    act(() => { expect(result.current.beginApply()).toBe('resurfaced-proposal'); });
    traceAction('BeginApply(accepted)', result, 'begin-apply');
    unmount();
  });

  it('replays retry: fail A; retry the same candidate; resolve retry proposal and matching preview; apply only retry receipt', async () => {
    startTrace();
    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });
    traceInitialObservation(result);

    const firstSettlement = startAttempt(result, 'retry-candidate');
    await advanceProposalDelay();
    const firstCall = controlled.proposals[0];
    expect(firstCall).toBeDefined();
    if (!firstCall) return;
    traceAction('StartProposal', result, 'propose-construction', 'first attempt');
    await deliver(() => firstCall.transport.reject(new Error('First proposal transport failed.')));
    expect((await firstSettlement).kind).toBe('rejected');
    expect(result.current.proposal.status).toBe('error');
    expect(result.current.canApply).toBe(false);
    expect(result.current.beginApply()).toBeUndefined();
    traceAction('ProposalError', result, 'proposal-response', 'first attempt transport error');

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
    traceAction('RetryProposal', result, 'retry', 'retry same candidate');

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
    traceAction('ProposalComplete', result, 'proposal-response', 'retry proposal accepted without embedded preview');
    traceAction('StartPreview', result, 'preview', 'retry fallback preview started');
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
    traceAction('PreviewComplete', result, 'preview-response', 'retry preview accepted');
    act(() => { expect(result.current.beginApply()).toBe('retry-proposal'); });
    traceAction('BeginApply', result, 'begin-apply');
    unmount();
  });

  it('replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B', async () => {
    startTrace();
    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });
    traceInitialObservation(result);

    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;
    traceAction('StartProposal', result, 'propose-construction', 'start ready A');
    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));
    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;
    traceEmbeddedPreviewExpansion(result);

    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    expect(result.current.proposal.status).toBe('previewing');
    expect(result.current.beginApply(oldOutcome.value)).toBeUndefined();
    traceAction('ReplaceProposal', result, 'propose-construction', 'start B replaces A');
    traceUnmodeled('BeginApply(rejected)', 'beginApply(oldOutcome A) rejected while B previews');

    const currentCall = controlled.proposals[1];
    expect(currentCall).toBeDefined();
    if (!currentCall) return;
    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));
    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.beginApply(oldOutcome.value)).toBeUndefined();
    traceEmbeddedPreviewExpansion(result);
    traceUnmodeled('BeginApply(rejected)', 'beginApply(oldOutcome A) rejected after B became ready');
    if (currentOutcome.kind === 'resolved') {
      act(() => { expect(result.current.beginApply(currentOutcome.value)).toBe('current-B'); });
      traceAction('BeginApply', result, 'begin-apply', 'apply B');
    }
    unmount();
  });

  // Regressions for Apply completion targeting the owner that began Apply.
  it('regression: old Apply success after replacement must not cancel the replacement owner', async () => {
    startTrace();
    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });
    traceInitialObservation(result);

    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;
    traceAction('StartProposal', result, 'propose-construction', 'start ready A');
    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));
    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;
    traceEmbeddedPreviewExpansion(result);
    act(() => { expect(result.current.beginApply(oldOutcome.value)).toBe('already-ready-A'); });
    traceAction('BeginApply', result, 'begin-apply', 'apply A');

    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    const currentCall = controlled.proposals[1];
    if (!currentCall) return;
    traceAction('ReplaceProposal', result, 'propose-construction', 'start B replaces applying A');
    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));
    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');
    traceEmbeddedPreviewExpansion(result);

    act(() => { result.current.finishApply(true); });
    traceAction('ApplyAck', result, 'finish-apply', 'actual state after finishApply(true)');
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);
    unmount();
  });

  it('regression: old Apply failure after replacement must not mark the replacement owner as an error', async () => {
    startTrace();
    vi.useFakeTimers();
    const controlled = controlledClient();
    const { result, unmount } = renderHook(() => useConstructionLifecycle({
      client: controlled.client,
      capabilitiesRequest: baseRequest,
    }));
    await act(async () => { await flushMicrotasks(); });
    traceInitialObservation(result);

    const oldSettlement = startAttempt(result, 'ready-A');
    await advanceProposalDelay();
    const oldCall = controlled.proposals[0];
    expect(oldCall).toBeDefined();
    if (!oldCall) return;
    traceAction('StartProposal', result, 'propose-construction', 'start ready A');
    await deliver(() => oldCall.transport.resolve(proposalFor(oldCall.args, 'already-ready-A')));
    const oldOutcome = await oldSettlement;
    expect(oldOutcome.kind).toBe('resolved');
    if (oldOutcome.kind !== 'resolved') return;
    traceEmbeddedPreviewExpansion(result);
    act(() => { expect(result.current.beginApply(oldOutcome.value)).toBe('already-ready-A'); });
    traceAction('BeginApply', result, 'begin-apply', 'apply A');

    const currentSettlement = startAttempt(result, 'replacement-B');
    await advanceProposalDelay();
    const currentCall = controlled.proposals[1];
    if (!currentCall) return;
    traceAction('ReplaceProposal', result, 'propose-construction', 'start B replaces applying A');
    await deliver(() => currentCall.transport.resolve(proposalFor(currentCall.args, 'current-B')));
    const currentOutcome = await currentSettlement;
    expect(currentOutcome.kind).toBe('resolved');
    expect(result.current.proposal.status).toBe('ready');
    traceEmbeddedPreviewExpansion(result);

    act(() => { result.current.finishApply(false); });
    traceAction('ApplyAck', result, 'finish-apply', 'actual state after finishApply(false)');
    expect(result.current.proposal.status).toBe('ready');
    expect(result.current.canApply).toBe(true);
    unmount();
  });
});


type ReconcilePhase = 'idle' | 'requesting' | 'succeeded' | 'stale' | 'conflict' | 'invalid' | 'failed';

const reconcileObservation = (phase: ReconcilePhase, receiptPresent: boolean) => ({
  status: phase,
  canApply: false,
  proposalIdPresent: false,
  previewReceiptPresent: false,
  rowsPresent: false,
  receiptPresent,
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
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
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

    traceObservedAction('ReconcileSuccess', 'reconcile-response', reconcileObservation('succeeded', true));
    traceObservedAction('ClearReceipt', 'identity-change', reconcileObservation('idle', false));
    traceObservedAction('AuthoredChange', 'identity-change', reconcileObservation('idle', false));
  });

  it('reconcile stale snapshots refresh and retry twice, then repair', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileStale', 'reconcile-response', reconcileObservation('stale', false));
    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions())).toBe('refresh-and-retry');

    traceObservedAction('RetryReconcile', 'reconcile-retry', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileStale', 'reconcile-response', reconcileObservation('stale', false));
    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions({ staleRefreshes: 1 }))).toBe('refresh-and-retry');

    traceObservedAction('RetryReconcile', 'reconcile-retry', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileStale', 'reconcile-response', reconcileObservation('stale', false));
    expect(reconcileRecoveryAction(reconcileError('STALE_SNAPSHOT'), reconcileOptions({ staleRefreshes: 2 }))).toBe('repair');
  });

  it('reconcile draft conflict refreshes and stops', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileConflict', 'reconcile-response', reconcileObservation('conflict', false));
    expect(reconcileRecoveryAction(reconcileError('DRAFT_CONFLICT'), reconcileOptions())).toBe('refresh-and-stop');
  });

  it('reconcile invalid output repairs', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileInvalid', 'reconcile-response', reconcileObservation('invalid', false));
    expect(reconcileRecoveryAction(reconcileError('VALIDATION_FAILED'), reconcileOptions())).toBe('repair');
  });

  it('reconcile retryable failure retries and then succeeds', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileFailure(retryable)', 'reconcile-response', reconcileObservation('failed', false));
    expect(reconcileRecoveryAction(reconcileError('BACKEND_UNAVAILABLE', true), reconcileOptions())).toBe('retry');
    expect(reconcileRecoveryAction(reconcileError('BACKEND_UNAVAILABLE', true), reconcileOptions({ attempt: 3 }))).toBe('repair');

    traceObservedAction('RetryReconcile', 'reconcile-retry', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileSuccess', 'reconcile-response', reconcileObservation('succeeded', true));
  });

  it('reconcile fatal failure repairs', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('ReconcileFailure(fatal)', 'reconcile-response', reconcileObservation('failed', false));
    expect(reconcileRecoveryAction(reconcileError('VALIDATION_FAILED'), reconcileOptions())).toBe('repair');
  });

  it('reconcile rejects stale results and mismatched receipts', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
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

    traceObservedAction('ReconcileSuccess', 'reconcile-response', reconcileObservation('succeeded', true));
  });

  it('reconcile ignores a late result after the workspace identity changes', () => {
    startTrace('ReconcileLifecycle');
    traceInitialObservationValue(reconcileObservation('idle', false));

    traceObservedAction('RequestReconcile', 'reconcile-request', reconcileObservation('requesting', false));
    traceObservedAction('AuthoredChange', 'identity-change', reconcileObservation('idle', false));
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
    traceUnmodeled('late-reconcile-ignored', 'late reconcile result from the old workspace fingerprint is ignored');
  });
});
