import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { resourceFor } from '../../../react';
import type {
  DiscoverConstructionCategoriesArgs,
  GetConstructionCapabilitiesArgs,
  LoomClient,
  ProposeConstructionArgs,
} from '../../../api';
import type {
  ConstructionCapabilitiesResponse,
  ConstructionCategoryDiscoveryResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import type {
  CompletePivotDiscovery,
  ConstructionReshapePivotDiscovery,
  ConstructionReshapePivotDiscoveryRequest,
} from '../constructionOperations/ConstructionReshapeEditor';
import {
  constructionProposalIsApplicable,
  type ConstructionProposalViewState,
} from './ConstructionProposalPanel';

export type ConstructionLifecycleClient = Pick<
  LoomClient,
  'getConstructionCapabilities' | 'discoverConstructionCategories' | 'proposeConstruction' | 'preview'
>;

export type ConstructionCandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'groupSources' | 'pivotSources'
>;

export type ConstructionCapabilitiesViewState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly response: ConstructionCapabilitiesResponse }
  | { readonly status: 'error'; readonly message: string };

type CapabilitiesLoad = {
  readonly key: string;
  readonly state: ConstructionCapabilitiesViewState;
};

type ProposalOutcome = Extract<ConstructionProposalViewState, { readonly status: 'ready' | 'needs-repair' }>;
type ReadyProposalOutcome = Extract<ProposalOutcome, { readonly status: 'ready' }>;
type ProposalResource = ReturnType<typeof resourceFor<ProposalOutcome>>;
type ProposalOwner = {
  readonly id: number;
  readonly requestKey: string;
  readonly generation: number;
  readonly resource: ProposalResource;
  readonly abortController: AbortController;
};
type ProposalOverride = {
  readonly ownerId: number;
  readonly state: ConstructionProposalViewState;
};

type PivotDiscoveryLoad = {
  readonly key: string;
  readonly state: ConstructionReshapePivotDiscovery | undefined;
};

const pivotDiscoveryLoadIdentity = (
  requestKey: string,
  request: ConstructionReshapePivotDiscoveryRequest,
): string => JSON.stringify([
  requestKey,
  request.stageId,
  request.categoryColumnId,
  request.valueColumnId,
  request.pivotSources?.length
    ? { pivotStepId: request.pivotStepId, groupKeyIds: request.groupKeyIds, pivotSources: request.pivotSources }
    : undefined,
]);

const capabilitiesIdentity = (
  args: GetConstructionCapabilitiesArgs | undefined,
): string => args
  ? JSON.stringify([
      args.project,
      args.authResourcePath?.trim() ?? '',
      args.explorerId,
      args.snapshotToken,
      args.expectedDraftVersion,
      args.expectedDraftDigest,
      args.outputId,
      args.stageId,
      args.groupStepId ?? '',
    ])
  : '';

const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message.trim() ? error.message : fallback;

const errorIsRetryable = (error: unknown): boolean =>
  typeof error === 'object' && error !== null && 'retryable' in error && Reflect.get(error, 'retryable') === true;

const abortError = () => new DOMException('The construction proposal was canceled.', 'AbortError');

const delayFor = (milliseconds: number, signal: AbortSignal): Promise<void> => new Promise((resolve, reject) => {
  if (signal.aborted) {
    reject(abortError());
    return;
  }
  const timer = window.setTimeout(() => {
    signal.removeEventListener('abort', abort);
    resolve();
  }, milliseconds);
  const abort = () => {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', abort);
    reject(abortError());
  };
  signal.addEventListener('abort', abort, { once: true });
});

const previewMatchesProposal = (
  preview: ExplorerBuilderPreviewResult | undefined,
  proposalId: string,
  outputId: string,
): preview is ExplorerBuilderPreviewResult => Boolean(
  preview &&
  preview.receiptId === proposalId &&
  preview.outputId === outputId &&
  preview.rows !== null,
);

export const useConstructionLifecycle = ({
  client,
  capabilitiesRequest,
  previewLimit = 25,
}: {
  readonly client: ConstructionLifecycleClient;
  readonly capabilitiesRequest?: GetConstructionCapabilitiesArgs;
  readonly previewLimit?: number;
}) => {
  const [capabilitiesLoad, setCapabilitiesLoad] = useState<CapabilitiesLoad>({
    key: '',
    state: { status: 'idle' },
  });
  const [proposalOwner, setProposalOwner] = useState<ProposalOwner | undefined>();
  const [proposalOverride, setProposalOverride] = useState<ProposalOverride | undefined>();
  const [pivotDiscoveryLoad, setPivotDiscoveryLoad] = useState<PivotDiscoveryLoad>({ key: '', state: undefined });
  const capabilitiesController = useRef<AbortController | undefined>(undefined);
  const pivotDiscoveryController = useRef<AbortController | undefined>(undefined);
  const proposalGeneration = useRef(0);
  const proposalOwnerSequence = useRef(0);
  const proposalOwnerRef = useRef<ProposalOwner | undefined>(undefined);
  const pivotDiscoveryGeneration = useRef(0);
  const candidateIntent = useRef<ConstructionCandidateIntent | undefined>(undefined);
  const capabilitiesRequestRef = useRef(capabilitiesRequest);
  capabilitiesRequestRef.current = capabilitiesRequest;

  const requestKey = capabilitiesIdentity(capabilitiesRequest);
  const idleProposalResource = useMemo(
    () => resourceFor<ProposalOutcome>(async () => { throw new Error('No construction proposal is active.'); }, false),
    [],
  );
  const activeProposalOwner = proposalOwner?.requestKey === requestKey ? proposalOwner : undefined;
  const activeProposalResource = activeProposalOwner?.resource ?? idleProposalResource;
  const proposalSnapshot = useSyncExternalStore(
    activeProposalResource.subscribe,
    activeProposalResource.getSnapshot,
    activeProposalResource.getSnapshot,
  );
  const snapshotProposal: ConstructionProposalViewState = !activeProposalOwner
    ? { status: 'idle' }
    : proposalSnapshot.loading
      ? { status: 'previewing' }
      : proposalSnapshot.error
        ? { status: 'error', message: errorMessage(proposalSnapshot.error, 'Loom could not check this construction change.'), retryable: errorIsRetryable(proposalSnapshot.error) }
        : proposalSnapshot.data ?? { status: 'error', message: 'Loom did not return a construction proposal.' };
  const activeProposal = proposalOverride && proposalOverride.ownerId === activeProposalOwner?.id
    ? proposalOverride.state
    : snapshotProposal;

  const invalidatePivotDiscovery = useCallback(() => {
    pivotDiscoveryGeneration.current += 1;
    pivotDiscoveryController.current?.abort();
    pivotDiscoveryController.current = undefined;
  }, []);

  useEffect(() => {
    capabilitiesController.current?.abort();
    capabilitiesController.current = undefined;
    if (!requestKey) {
      setCapabilitiesLoad({ key: '', state: { status: 'idle' } });
      return;
    }

    const args = capabilitiesRequestRef.current;
    if (!args) return;
    const controller = new AbortController();
    capabilitiesController.current = controller;
    setCapabilitiesLoad({ key: requestKey, state: { status: 'loading' } });
    void client.getConstructionCapabilities(args, controller.signal).then(
      (response) => {
        if (controller.signal.aborted) return;
        const matchesRequest =
          response.snapshotToken === args.snapshotToken &&
          response.draftVersion === args.expectedDraftVersion &&
          response.draftDigest === args.expectedDraftDigest &&
          response.outputId === args.outputId &&
          response.stageId === args.stageId;
        setCapabilitiesLoad({
          key: requestKey,
          state: matchesRequest
            ? { status: 'ready', response }
            : {
                status: 'error',
                message: 'Loom returned capabilities for a different draft or table.',
              },
        });
      },
    ).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setCapabilitiesLoad({
        key: requestKey,
        state: {
          status: 'error',
          message: errorMessage(error, 'Loom could not load construction capabilities.'),
        },
      });
    });

    return () => controller.abort();
  }, [client, requestKey]);

  useEffect(() => {
    invalidatePivotDiscovery();
    setPivotDiscoveryLoad({ key: '', state: undefined });
    return invalidatePivotDiscovery;
  }, [invalidatePivotDiscovery, requestKey]);

  const invalidateProposalRequest = useCallback(() => {
    proposalGeneration.current += 1;
    proposalOwnerRef.current?.abortController.abort();
    proposalOwnerRef.current = undefined;
    setProposalOwner(undefined);
    setProposalOverride(undefined);
  }, []);

  const startCandidateProposal = useCallback((intent: ConstructionCandidateIntent) => {
    invalidateProposalRequest();
    candidateIntent.current = intent;
    const currentRequest = capabilitiesRequestRef.current;
    if (!currentRequest) return Promise.reject(new Error('The current table is still loading.'));

    const generation = proposalGeneration.current;
    const args: ProposeConstructionArgs = {
      project: currentRequest.project,
      explorerId: currentRequest.explorerId,
      ...(currentRequest.authResourcePath ? { authResourcePath: currentRequest.authResourcePath } : {}),
      snapshotToken: currentRequest.snapshotToken,
      expectedDraftVersion: currentRequest.expectedDraftVersion,
      expectedDraftDigest: currentRequest.expectedDraftDigest,
      outputId: currentRequest.outputId,
      ...intent,
      limit: previewLimit,
      requestId: `construction-proposal-${window.crypto.randomUUID()}`,
    };
    const abortController = new AbortController();
    const resource = resourceFor<ProposalOutcome>(async (resourceSignal) => {
      const abortFromResource = () => abortController.abort();
      if (resourceSignal.aborted) abortController.abort();
      else resourceSignal.addEventListener('abort', abortFromResource, { once: true });
      try {
        await delayFor(300, abortController.signal);
        const response = await client.proposeConstruction(args, abortController.signal);
        if (abortController.signal.aborted || proposalGeneration.current !== generation) throw abortError();
        const matchesBase = response.outputId === args.outputId &&
          response.snapshotToken === args.snapshotToken &&
          response.draftVersion === args.expectedDraftVersion &&
          response.draftDigest === args.expectedDraftDigest;
        if (!matchesBase) throw new Error('The table draft changed while Loom checked this proposal. Review the current draft and try again.');
        if (response.previewStatus !== 'READY') return { status: 'needs-repair', response };
        const proposalId = response.proposalId;
        if (!proposalId) throw new Error('Loom did not return a proposal receipt for this change.');
        let preview = response.preview;
        if (!previewMatchesProposal(preview, proposalId, args.outputId)) {
          preview = await client.preview({
            project: args.project,
            explorerId: args.explorerId,
            ...(args.authResourcePath ? { authResourcePath: args.authResourcePath } : {}),
            receiptId: proposalId,
            outputId: args.outputId,
            limit: previewLimit,
            requestId: `construction-preview-${window.crypto.randomUUID()}`,
          }, abortController.signal);
        }
        if (abortController.signal.aborted || proposalGeneration.current !== generation) throw abortError();
        if (!previewMatchesProposal(preview, proposalId, args.outputId)) {
          throw new Error('Loom did not return rows for this proposal receipt, so it cannot be applied yet.');
        }
        return { status: 'ready', response, preview };
      } finally {
        resourceSignal.removeEventListener('abort', abortFromResource);
      }
    });
    const owner: ProposalOwner = {
      id: ++proposalOwnerSequence.current,
      requestKey,
      generation,
      resource,
      abortController,
    };
    proposalOwnerRef.current = owner;
    setProposalOwner(owner);
    setProposalOverride(undefined);
    const request = resource.refresh();
    let rejectAbort: ((reason: unknown) => void) | undefined;
    const onAbort = () => rejectAbort?.(abortError());
    const aborted = new Promise<never>((_resolve, reject) => {
      rejectAbort = reject;
      if (abortController.signal.aborted) onAbort();
      else abortController.signal.addEventListener('abort', onAbort, { once: true });
    });
    return Promise.race([
      request.then(({ data, error }) => {
        if (error) throw error;
        if (!data) throw new Error('Loom did not return a construction proposal.');
        return data;
      }),
      aborted,
    ]).then((outcome) => {
      if (abortController.signal.aborted || proposalGeneration.current !== generation ||
        capabilitiesIdentity(capabilitiesRequestRef.current) !== requestKey || proposalOwnerRef.current !== owner) {
        throw abortError();
      }
      if (outcome.status !== 'ready') throw new Error('This construction change needs repair before it can be applied.');
      return outcome;
    }).finally(() => abortController.signal.removeEventListener('abort', onAbort));
  }, [client, invalidateProposalRequest, previewLimit, requestKey]);

  const onCandidateChange = useCallback((intent: ConstructionCandidateIntent | undefined) => {
    if (!intent) {
      invalidateProposalRequest();
      candidateIntent.current = undefined;
      return;
    }
    void startCandidateProposal(intent).catch(() => undefined);
  }, [invalidateProposalRequest, startCandidateProposal]);

  const proposeCandidateAndWait = useCallback(async (intent: ConstructionCandidateIntent): Promise<ReadyProposalOutcome> => {
    const outcome = await startCandidateProposal(intent);
    if (outcome.status !== 'ready') throw new Error('This construction change needs repair before it can be applied.');
    return outcome;
  }, [startCandidateProposal]);

  const retry = useCallback(() => {
    if (candidateIntent.current && proposalOwnerRef.current?.requestKey === requestKey) {
      void startCandidateProposal(candidateIntent.current).catch(() => undefined);
    }
  }, [requestKey, startCandidateProposal]);

  const cancel = useCallback(() => {
    invalidateProposalRequest();
    candidateIntent.current = undefined;
  }, [invalidateProposalRequest]);

  const onDiscoverCategories = useCallback((
    request: ConstructionReshapePivotDiscoveryRequest,
    onComplete?: (discovery: CompletePivotDiscovery) => void,
  ) => {
    invalidatePivotDiscovery();
    const args = capabilitiesRequestRef.current;
    const discoveryKey = pivotDiscoveryLoadIdentity(requestKey, request);
    if (!args || request.stageId !== args.stageId) {
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: { ...request, status: 'failed', reason: 'The selected stage changed. Reload its columns and find the category values again.' },
      });
      return;
    }
    const activeCapabilities = capabilitiesLoad.key === requestKey ? capabilitiesLoad.state : { status: 'loading' as const };
    if (activeCapabilities.status !== 'ready' || activeCapabilities.response.selectedStage.id !== request.stageId) {
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: { ...request, status: 'failed', reason: 'The current stage is still loading. Try finding category values again.' },
      });
      return;
    }

    const generation = pivotDiscoveryGeneration.current;
    const controller = new AbortController();
    pivotDiscoveryController.current = controller;
    setPivotDiscoveryLoad({ key: discoveryKey, state: { ...request, status: 'loading' } });
    let discoveryRequest: Promise<ConstructionCategoryDiscoveryResponse>;
    try {
      const discoveryArgs: DiscoverConstructionCategoriesArgs = {
        project: args.project,
        explorerId: args.explorerId,
        ...(args.authResourcePath ? { authResourcePath: args.authResourcePath } : {}),
        snapshotToken: args.snapshotToken,
        expectedDraftVersion: args.expectedDraftVersion,
        expectedDraftDigest: args.expectedDraftDigest,
        outputId: args.outputId,
        stageId: request.stageId,
        pivotStepId: request.pivotStepId,
        groupKeyIds: request.groupKeyIds,
        categoryColumnId: request.categoryColumnId,
        valueColumnId: request.valueColumnId,
        ...(request.pivotSources?.length ? { pivotSources: request.pivotSources, candidateConstruction: request.candidateConstruction } : {}),
        requestId: `construction-categories-${window.crypto.randomUUID()}`,
      };
      discoveryRequest = client.discoverConstructionCategories(discoveryArgs, controller.signal);
    } catch (error: unknown) {
      if (controller.signal.aborted || pivotDiscoveryGeneration.current !== generation) return;
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: {
          ...request,
          status: 'failed',
          reason: errorMessage(error, 'Loom could not find category values for this pair.'),
        },
      });
      return;
    }
    void discoveryRequest.then(
      (response: ConstructionCategoryDiscoveryResponse) => {
        if (controller.signal.aborted || pivotDiscoveryGeneration.current !== generation) return;
        const matchesRequest =
          response.snapshotToken === args.snapshotToken &&
          response.draftVersion === args.expectedDraftVersion &&
          response.draftDigest === args.expectedDraftDigest &&
          response.outputId === args.outputId &&
          response.stageId === request.stageId &&
          response.categoryColumnId === request.categoryColumnId &&
          response.valueColumnId === request.valueColumnId;
        const nextState: ConstructionReshapePivotDiscovery = !matchesRequest
          ? { ...request, status: 'failed', reason: 'Loom returned category values for a different stage or field pair.' }
          : response.outcome === 'LIMIT_EXCEEDED'
            ? { ...request, status: 'limit-exceeded', limit: response.limit, reason: response.message }
            : response.outcome === 'MISSING_UNSUPPORTED'
              ? { ...request, status: 'missing-unsupported', reason: response.message }
            : response.outcome === 'COMPLETE' && response.complete && response.proofFingerprint.trim() !== ''
              ? { ...request, status: 'complete', categories: response.categories }
              : { ...request, status: 'failed', reason: 'Loom returned category values without a complete compiler proof.' };
        setPivotDiscoveryLoad({
          key: discoveryKey,
          state: nextState,
        });
        if (nextState.status === 'complete') onComplete?.(nextState);
      },
    ).catch((error: unknown) => {
      if (controller.signal.aborted || pivotDiscoveryGeneration.current !== generation) return;
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: {
          ...request,
          status: 'failed',
          reason: errorMessage(error, 'Loom could not find category values for this pair.'),
        },
      });
    });
  }, [capabilitiesLoad, client, invalidatePivotDiscovery, requestKey]);

  const beginApply = useCallback((expectedProposal?: ReadyProposalOutcome) => {
    const owner = proposalOwnerRef.current;
    const currentRequest = capabilitiesRequestRef.current;
    if (!owner || !currentRequest || owner.requestKey !== capabilitiesIdentity(currentRequest) ||
      owner.generation !== proposalGeneration.current) return undefined;
    let state: ConstructionProposalViewState;
    if (expectedProposal) {
      const snapshot = owner.resource.getSnapshot();
      const current = snapshot.loading ? undefined : snapshot.data;
      if (current?.status !== 'ready' ||
        current.response.proposalId !== expectedProposal.response.proposalId ||
        current.response.outputId !== expectedProposal.response.outputId ||
        current.response.snapshotToken !== expectedProposal.response.snapshotToken ||
        current.response.draftVersion !== expectedProposal.response.draftVersion ||
        current.response.draftDigest !== expectedProposal.response.draftDigest ||
        current.preview.receiptId !== expectedProposal.preview.receiptId) return undefined;
      state = current;
    } else {
      if (activeProposalOwner?.id !== owner.id) return undefined;
      state = activeProposal;
    }
    if (state.status !== 'ready') return undefined;
    const identity = {
      outputId: currentRequest.outputId,
      snapshotToken: currentRequest.snapshotToken,
      draftVersion: currentRequest.expectedDraftVersion,
      draftDigest: currentRequest.expectedDraftDigest,
    };
    if (!constructionProposalIsApplicable(state, identity)) return undefined;
    setProposalOverride({ ownerId: owner.id,
      state: { status: 'applying', response: state.response, preview: state.preview } });
    return state.response.proposalId;
  }, [activeProposal, activeProposalOwner]);

  const finishApply = useCallback((applied: boolean) => {
    if (applied) {
      cancel();
      return;
    }
    if (activeProposalOwner) setProposalOverride({
      ownerId: activeProposalOwner.id,
      state: {
        status: 'error',
        message: 'Loom could not save this proposal. Review the Builder message and try again if the draft is unchanged.',
      },
    });
  }, [activeProposalOwner, cancel]);

  const activeCapabilities = capabilitiesLoad.key === requestKey
    ? capabilitiesLoad.state
    : requestKey
      ? { status: 'loading' as const }
      : { status: 'idle' as const };
  const activePivotDiscoveryKey = pivotDiscoveryLoad.state
    ? pivotDiscoveryLoadIdentity(requestKey, pivotDiscoveryLoad.state)
    : '';
  const activePivotDiscovery = pivotDiscoveryLoad.key === activePivotDiscoveryKey
    ? pivotDiscoveryLoad.state
    : undefined;
  const canApply = Boolean(
    capabilitiesRequest &&
    constructionProposalIsApplicable(activeProposal, {
      outputId: capabilitiesRequest.outputId,
      snapshotToken: capabilitiesRequest.snapshotToken,
      draftVersion: capabilitiesRequest.expectedDraftVersion,
      draftDigest: capabilitiesRequest.expectedDraftDigest,
    }),
  );

  return {
    capabilities: activeCapabilities,
    proposal: activeProposal,
    canApply,
    onCandidateChange,
    proposeCandidateAndWait,
    retry,
    cancel,
    beginApply,
    finishApply,
    pivotDiscovery: activePivotDiscovery,
    onDiscoverCategories,
  };
};
