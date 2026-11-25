import { useCallback, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import {
  applyConstructionProposalFor, capabilitiesIdentity, discoverConstructionCategoriesFor, errorMessage,
  pivotDiscoveryLoadIdentity, startConstructionProposalFor,
} from './constructionActions';
import { errorIsProposalInactive } from './constructionProposal';
import { resourceFor } from '../../../data/resource';
import { constructionCapabilitiesResource } from './constructionCapabilities';
import type { GetConstructionCapabilitiesArgs } from '../../../client/api';
import {
  constructionProposalIsApplicable,
  type ConstructionProposalViewState,
} from '../model/constructionProposal';
import {
  createConstructionSessionState,
  type ConstructionCandidateIntent,
  type ConstructionEditingOwner,
  type ConstructionLifecycleClient,
  type ReadyProposalOutcome,
  type ProposalOutcome,
} from './constructionSessionState';
export type { ConstructionCandidateIntent } from './constructionSessionState';

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
  const [workflow] = useState(createConstructionSessionState);
  const { requests, getProposalOwner, setProposalOwner,
    setPivotDiscoveryLoad, applyingOwnerRef, candidateIntent, nextProposalId } = workflow;
  const { proposalOwner, proposalOverride, pivotDiscoveryLoad } = useSyncExternalStore(
    workflow.subscribe, workflow.getSnapshot, workflow.getSnapshot,
  );
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
    setProposalOwner(undefined);
  }, [requests, setProposalOwner]);

  const startCandidateProposal = useCallback(startConstructionProposalFor(workflow, {
    client, requestKey, previewLimit, readRequest: () => capabilitiesRequestRef.current,
    readEditor: () => editingOwnerRef.current, invalidateProposalRequest,
  }), [client, getProposalOwner, invalidateProposalRequest, nextProposalId, previewLimit, requests, requestKey, setProposalOwner]);

  const onCandidateChange = useCallback((intent: ConstructionCandidateIntent | undefined) => {
    if (!intent) {
      if (applyingOwnerRef.current) return;
      if (getProposalOwner()?.editingAttempt?.cancel() === false) return;
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
    if (candidateIntent.current && getProposalOwner()?.requestKey === requestKey) {
      void startCandidateProposal(candidateIntent.current).catch(() => undefined);
    }
  }, [requestKey, startCandidateProposal]);

  const cancel = useCallback(() => {
    if (applyingOwnerRef.current) return;
    getProposalOwner()?.editingAttempt?.cancel();
    applyingOwnerRef.current = undefined;
    invalidateProposalRequest();
    invalidatePivotDiscovery();
    setPivotDiscoveryLoad({ key: '', state: undefined });
    candidateIntent.current = undefined;
  }, [invalidateProposalRequest, invalidatePivotDiscovery]);

  const onDiscoverCategories = useCallback(discoverConstructionCategoriesFor(workflow, {
    client, requestKey, activeCapabilities, readRequest: () => capabilitiesRequestRef.current,
    invalidatePivotDiscovery,
  }), [activeCapabilities, client, invalidatePivotDiscovery, requests, requestKey]);

  const beginApply = useCallback(applyConstructionProposalFor(workflow, {
    activeProposal, activeProposalOwner, readRequest: () => capabilitiesRequestRef.current, invalidateProposalRequest,
  }), [activeProposal, activeProposalOwner, invalidateProposalRequest]);

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
