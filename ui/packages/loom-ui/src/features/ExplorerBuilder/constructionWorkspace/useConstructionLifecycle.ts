import { abortError, constructionProposalResource, errorIsProposalInactive } from '../session/constructionProposal';
import {useCallback, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import { resourceFor } from '../../../data/resource';
import { RequestOwner } from '../../../data/requests';
import { constructionCapabilitiesResource } from '../session/constructionCapabilities';
import { LoomRequestError } from '../../../client/api';
import type {
  DiscoverConstructionCategoriesArgs,
  GetConstructionCapabilitiesArgs,
  LoomClient,
  ProposeConstructionArgs,
} from '../../../client/api';
import type {
  ConstructionCategoryDiscoveryResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ExplorerBuilderPreviewResult,
} from '../../../contracts/types';
import type {
  CompletePivotDiscovery,
  ConstructionReshapePivotDiscovery,
  ConstructionReshapePivotDiscoveryRequest,
} from '../model/reshapeForms';
import {
  constructionProposalIsApplicable,
  type ConstructionProposalViewState,
} from '../model/constructionProposal';

import { beginConstructionApply, type ConstructionApplyAttempt } from '../session/constructionApplyOwnership';

type ConstructionLifecycleClient = Pick<
  LoomClient,
  'getConstructionCapabilities' | 'discoverConstructionCategories' | 'proposeConstruction' | 'preview'
>;

export type ConstructionCandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'groupSources' | 'pivotSources' | 'filterSources' | 'deriveSources' | 'unpivotSources'
>;

interface ConstructionEditingAttempt {
  ready(response: ConstructionProposalResponse, preview: ExplorerBuilderPreviewResult): boolean;
  failed(message: string): void;
  cancel(): boolean;
  beginApply(): boolean;
  finishApply(applied: boolean): void;
}
interface ConstructionEditingOwner {
  onStart(intent: ConstructionCandidateIntent, request: GetConstructionCapabilitiesArgs): ConstructionEditingAttempt | undefined;
}

type ConstructionRequest = NonNullable<ReturnType<RequestOwner['start']>>;
type ProposalOutcome = Extract<ConstructionProposalViewState, { readonly status: 'ready' | 'needs-repair' }>;
type ReadyProposalOutcome = Extract<ProposalOutcome, { readonly status: 'ready' }>;
type ProposalResource = ReturnType<typeof resourceFor<ProposalOutcome>>;
type ProposalOwner = {
  readonly id: number;
  readonly requestKey: string;
  readonly resource: ProposalResource;
  readonly request: ConstructionRequest;
  readonly editingAttempt?: ConstructionEditingAttempt;
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
      args.includeGroupChoices === true,
    ])
  : '';

const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message.trim() ? error.message : fallback;

const categoryDiscoveryErrorMessage = (error: unknown): string => {
  if (error instanceof LoomRequestError && error.code === 'CATEGORY_SCAN_TIMEOUT') {
    return `Finding category values timed out. Try filtering the source rows and retrying.${error.requestId ? ` Request ID: ${error.requestId}` : ''}`;
  }
  return errorMessage(error, 'Loom could not find category values for this pair.');
};

const errorIsRetryable = (error: unknown): boolean =>
  typeof error === 'object' && error !== null && 'retryable' in error && Reflect.get(error, 'retryable') === true;

export const useConstructionLifecycle = ({
  client,
  capabilitiesRequest,
  previewLimit = 25,
  editingOwner,
}: {
  readonly client: ConstructionLifecycleClient;
  readonly capabilitiesRequest?: GetConstructionCapabilitiesArgs;
  readonly previewLimit?: number;
  readonly editingOwner?: ConstructionEditingOwner;
}) => {
  const requestKey = capabilitiesIdentity(capabilitiesRequest);
  const requestOwnerIdentity = 'construction';
  const [requests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity(requestOwnerIdentity);
    return owner;
  });
  const [proposalOwner, setProposalOwner] = useState<ProposalOwner | undefined>();
  const [proposalOverride, setProposalOverride] = useState<ProposalOverride | undefined>();
  const [pivotDiscoveryLoad, setPivotDiscoveryLoad] = useState<PivotDiscoveryLoad>({ key: '', state: undefined });
  const proposalOwnerSequence = useRef(0);
  const proposalOwnerRef = useRef<ProposalOwner | undefined>(undefined);
  const applyingOwnerRef = useRef<ConstructionApplyAttempt<ProposalOwner> | undefined>(undefined);
  const candidateIntent = useRef<ConstructionCandidateIntent | undefined>(undefined);
  const editingOwnerRef = useRef(editingOwner);
  editingOwnerRef.current = editingOwner;
  const capabilitiesRequestRef = useRef(capabilitiesRequest);
  capabilitiesRequestRef.current = capabilitiesRequest;

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
        ? errorIsProposalInactive(proposalSnapshot.error)
          ? { status: 'idle' }
          : { status: 'error', message: errorMessage(proposalSnapshot.error, 'Loom could not check this construction change.'), retryable: errorIsRetryable(proposalSnapshot.error) }
        : proposalSnapshot.data ?? { status: 'error', message: 'Loom did not return a construction proposal.' };
  const activeProposal = proposalOverride && proposalOverride.ownerId === activeProposalOwner?.id
    ? proposalOverride.state
    : snapshotProposal;

  const invalidatePivotDiscovery = useCallback(() => {
    requests.cancel('category-discovery');
  }, [requests]);

  const capabilities = useMemo(
    () => constructionCapabilitiesResource(client, () => capabilitiesRequestRef.current, Boolean(requestKey)),
    [client, requestKey],
  );
  const subscribeCapabilities = useCallback((listener: () => void) => {
    invalidatePivotDiscovery();
    setPivotDiscoveryLoad({ key: '', state: undefined });
    const unsubscribe = capabilities.subscribe(listener);
    return () => {
      unsubscribe();
      invalidatePivotDiscovery();
    };
  }, [capabilities, invalidatePivotDiscovery]);
  const capabilitiesSnapshot = useSyncExternalStore(subscribeCapabilities, capabilities.getSnapshot, capabilities.getSnapshot);
  const activeCapabilities = useMemo(() => capabilitiesSnapshot.data ?? (
    requestKey ? { status: 'loading' as const } : { status: 'idle' as const }
  ), [capabilitiesSnapshot.data, requestKey]);

  const invalidateProposalRequest = useCallback(() => {
    requests.cancel('proposal');
    proposalOwnerRef.current = undefined;
    setProposalOwner(undefined);
    setProposalOverride(undefined);
  }, [requests]);

  const startCandidateProposal = useCallback((intent: ConstructionCandidateIntent) => {
    const currentRequest = capabilitiesRequestRef.current;
    if (!currentRequest) return Promise.reject(new Error('The current table is still loading.'));
    if (capabilitiesIdentity(currentRequest) !== requestKey) return Promise.reject(abortError());
    let editingAttempt: ConstructionEditingAttempt | undefined;
    try {
      editingAttempt = editingOwnerRef.current?.onStart(intent, currentRequest);
      if (editingOwnerRef.current && !editingAttempt) return Promise.reject(new Error('Apply or Cancel the active recipe change before starting another edit.'));
    } catch (error) { return Promise.reject(error); }

    invalidateProposalRequest();
    candidateIntent.current = intent;

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
    const proposalRequest = requests.start('proposal', requestOwnerIdentity);
    if (!proposalRequest) return Promise.reject(abortError());
    const resource = constructionProposalResource({
      client, args, proposalRequest, previewLimit,
      onReady: editingAttempt ? (response, preview) => editingAttempt.ready(response, preview) : undefined,
    });
    const owner: ProposalOwner = {
      id: ++proposalOwnerSequence.current,
      requestKey,
      resource,
      request: proposalRequest,
      editingAttempt,
    };
    proposalOwnerRef.current = owner;
    setProposalOwner(owner);
    setProposalOverride(undefined);
    const request = resource.refresh();
    let rejectAbort: ((reason: unknown) => void) | undefined;
    const onAbort = () => rejectAbort?.(abortError());
    const aborted = new Promise<never>((_resolve, reject) => {
      rejectAbort = reject;
      if (proposalRequest.signal.aborted) onAbort();
      else proposalRequest.signal.addEventListener('abort', onAbort, { once: true });
    });
    return Promise.race([
      request.then(({ data, error }) => {
        if (error) throw error;
        if (!data) throw new Error('Loom did not return a construction proposal.');
        return data;
      }),
      aborted,
    ]).then((outcome) => {
      if (!proposalRequest.isCurrent() ||
        capabilitiesIdentity(capabilitiesRequestRef.current) !== requestKey || proposalOwnerRef.current !== owner) {
        throw abortError();
      }
      if (outcome.status !== 'ready') throw new Error('This construction change needs repair before it can be applied.');
      return outcome;
    }).catch((error: unknown) => {
      if (!errorIsProposalInactive(error) && proposalRequest.isCurrent() &&
          capabilitiesIdentity(capabilitiesRequestRef.current) === requestKey && proposalOwnerRef.current === owner) {
        editingAttempt?.failed(errorMessage(error, 'Loom could not check this construction change.'));
      }
      throw error;
    }).finally(() => proposalRequest.signal.removeEventListener('abort', onAbort));
  }, [client, invalidateProposalRequest, previewLimit, requests, requestKey]);

  const onCandidateChange = useCallback((intent: ConstructionCandidateIntent | undefined) => {
    if (!intent) {
      if (applyingOwnerRef.current) return;
      if (proposalOwnerRef.current?.editingAttempt?.cancel() === false) return;
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
    if (applyingOwnerRef.current) return;
    proposalOwnerRef.current?.editingAttempt?.cancel();
    applyingOwnerRef.current = undefined;
    invalidateProposalRequest();
    invalidatePivotDiscovery();
    setPivotDiscoveryLoad({ key: '', state: undefined });
    candidateIntent.current = undefined;
  }, [invalidateProposalRequest, invalidatePivotDiscovery]);

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
    if (activeCapabilities.status !== 'ready' || activeCapabilities.response.selectedStage.id !== request.stageId) {
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: { ...request, status: 'failed', reason: 'The current stage is still loading. Try finding category values again.' },
      });
      return;
    }

    const discovery = requests.start('category-discovery', requestOwnerIdentity);
    if (!discovery) return;
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
      discoveryRequest = client.discoverConstructionCategories(discoveryArgs, discovery.signal);
    } catch (error: unknown) {
      if (!discovery.isCurrent()) return;
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: {
          ...request,
          status: 'failed',
          reason: categoryDiscoveryErrorMessage(error),
        },
      });
      return;
    }
    void discoveryRequest.then(
      (response: ConstructionCategoryDiscoveryResponse) => {
        if (!discovery.isCurrent()) return;
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
            : response.outcome === 'NO_VALUES'
              ? { ...request, status: 'no-values', reason: response.message }
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
      if (!discovery.isCurrent()) return;
      setPivotDiscoveryLoad({
        key: discoveryKey,
        state: {
          ...request,
          status: 'failed',
          reason: categoryDiscoveryErrorMessage(error),
        },
      });
    });
  }, [activeCapabilities, client, invalidatePivotDiscovery, requests, requestKey]);

  const beginApply = useCallback((expectedProposal?: ReadyProposalOutcome) => {
    if (applyingOwnerRef.current) return undefined;
    const owner = proposalOwnerRef.current;
    const currentRequest = capabilitiesRequestRef.current;
    if (!owner || !currentRequest || owner.requestKey !== capabilitiesIdentity(currentRequest) ||
      !owner.request.isCurrent()) return undefined;
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
    const proposalId = state.response.proposalId;
    if (!proposalId || !constructionProposalIsApplicable(state, identity)) return undefined;
    if (owner.editingAttempt && !owner.editingAttempt.beginApply()) return undefined;
    const finish = beginConstructionApply(
      applyingOwnerRef,
      { owner, requestKey: owner.requestKey, generation: owner.id },
      () => {
        const liveOwner = proposalOwnerRef.current;
        const liveRequest = capabilitiesRequestRef.current;
        return liveOwner && liveRequest && liveOwner.request.isCurrent() ? {
          owner: liveOwner,
          requestKey: capabilitiesIdentity(liveRequest),
          generation: liveOwner.id,
        } : undefined;
      },
      (applied) => {
        owner.editingAttempt?.finishApply(applied);
        if (applied) {
          applyingOwnerRef.current = undefined;
          invalidateProposalRequest();
          candidateIntent.current = undefined;
        }
        else setProposalOverride({
          ownerId: owner.id,
          state: {
            status: 'error',
            message: 'Loom could not save this proposal. Review the Builder message and try again if the draft is unchanged.',
          },
        });
      },
    );
    if (!finish) { owner.editingAttempt?.finishApply(false); return undefined; }
    setProposalOverride({ ownerId: owner.id,
      state: { status: 'applying', response: state.response, preview: state.preview } });
    return { proposalId, finish };
  }, [activeProposal, activeProposalOwner, invalidateProposalRequest]);

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
    pivotDiscovery: activePivotDiscovery,
    onDiscoverCategories,
  };
};
