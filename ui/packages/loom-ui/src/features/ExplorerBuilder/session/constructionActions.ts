import {
  LoomRequestError,
  type DiscoverConstructionCategoriesArgs,
  type GetConstructionCapabilitiesArgs,
  type ProposeConstructionArgs,
} from '../../../client/api';
import { sameDraftRevision } from '../../../contracts/identity';
import type { ConstructionCategoryDiscoveryResponse } from '../../../contracts/types';
import { constructionProposalIsApplicable, type ConstructionProposalViewState } from '../model/constructionProposal';
import type { CompletePivotDiscovery, ConstructionReshapePivotDiscovery, ConstructionReshapePivotDiscoveryRequest } from '../model/reshapeForms';
import { beginConstructionApply } from './constructionApplyOwnership';
import type { ConstructionCapabilitiesViewState } from './constructionCapabilities';
import { abortError, constructionProposalResource, errorIsProposalInactive } from './constructionProposal';
import type {
  ConstructionCandidateIntent, ConstructionEditingAttempt, ConstructionEditingOwner, ConstructionLifecycleClient,
  ProposalOwner, ReadyProposalOutcome, createConstructionSessionState,
} from './constructionSessionState';

type Workflow = ReturnType<typeof createConstructionSessionState>;
type ReadRequest = () => GetConstructionCapabilitiesArgs | undefined;

export const pivotDiscoveryLoadIdentity = (
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

export const capabilitiesIdentity = (
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

export const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message.trim() ? error.message : fallback;

const categoryDiscoveryErrorMessage = (error: unknown): string => {
  if (error instanceof LoomRequestError && error.code === 'CATEGORY_SCAN_TIMEOUT') {
    return `Finding category values timed out. Try filtering the source rows and retrying.${error.requestId ? ` Request ID: ${error.requestId}` : ''}`;
  }
  return errorMessage(error, 'Loom could not find category values for this pair.');
};

export function startConstructionProposalFor(
  { requests, getProposalOwner, setProposalOwner, candidateIntent, nextProposalId }: Workflow,
  { client, requestKey, previewLimit, readRequest, readEditor, invalidateProposalRequest }: {
    readonly client: ConstructionLifecycleClient;
    readonly requestKey: string;
    readonly previewLimit: number;
    readonly readRequest: ReadRequest;
    readonly readEditor: () => ConstructionEditingOwner | undefined;
    readonly invalidateProposalRequest: () => void;
  },
) {
  return function startCandidateProposal(intent: ConstructionCandidateIntent) {
    const currentRequest = readRequest();
    if (!currentRequest) return Promise.reject(new Error('The current table is still loading.'));
    if (capabilitiesIdentity(currentRequest) !== requestKey) return Promise.reject(abortError());
    let editingAttempt: ConstructionEditingAttempt | undefined;
    try {
      editingAttempt = readEditor()?.onStart(intent, currentRequest);
      if (readEditor() && !editingAttempt) return Promise.reject(new Error('Apply or Cancel the active recipe change before starting another edit.'));
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
      requestId: `construction-proposal-${globalThis.crypto.randomUUID()}`,
    };
    const proposalRequest = requests.start('proposal');
    if (!proposalRequest) return Promise.reject(abortError());
    const resource = constructionProposalResource({
      client, args, proposalRequest, previewLimit,
      onReady: editingAttempt ? (response, preview) => editingAttempt.ready(response, preview) : undefined,
    });
    const owner: ProposalOwner = {
      id: nextProposalId(),
      requestKey,
      resource,
      request: proposalRequest,
      editingAttempt,
    };
    const request = resource.refresh();
    setProposalOwner(owner);
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
        capabilitiesIdentity(readRequest()) !== requestKey || getProposalOwner() !== owner) {
        throw abortError();
      }
      if (outcome.status !== 'ready') throw new Error('This construction change needs repair before it can be applied.');
      return outcome;
    }).catch((error: unknown) => {
      if (!errorIsProposalInactive(error) && proposalRequest.isCurrent() &&
          capabilitiesIdentity(readRequest()) === requestKey && getProposalOwner() === owner) {
        editingAttempt?.failed(errorMessage(error, 'Loom could not check this construction change.'));
      }
      throw error;
    }).finally(() => proposalRequest.signal.removeEventListener('abort', onAbort));

  };
}

export function discoverConstructionCategoriesFor(
  { requests, setPivotDiscoveryLoad }: Workflow,
  { client, requestKey, activeCapabilities, readRequest, invalidatePivotDiscovery }: {
    readonly client: ConstructionLifecycleClient;
    readonly requestKey: string;
    readonly activeCapabilities: ConstructionCapabilitiesViewState;
    readonly readRequest: ReadRequest;
    readonly invalidatePivotDiscovery: () => void;
  },
) {
  return function onDiscoverCategories(
    request: ConstructionReshapePivotDiscoveryRequest,
    onComplete?: (discovery: CompletePivotDiscovery) => void,
  ) {
    invalidatePivotDiscovery();
    const args = readRequest();
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

    const discovery = requests.start('category-discovery');
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
        requestId: `construction-categories-${globalThis.crypto.randomUUID()}`,
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
          sameDraftRevision(response, {
            snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
          }) &&
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

  };
}

export function applyConstructionProposalFor(
  { applyingOwnerRef, candidateIntent, getProposalOwner, setProposalOverride }: Workflow,
  { activeProposal, activeProposalOwner, readRequest, invalidateProposalRequest }: {
    readonly activeProposal: ConstructionProposalViewState;
    readonly activeProposalOwner: ProposalOwner | undefined;
    readonly readRequest: ReadRequest;
    readonly invalidateProposalRequest: () => void;
  },
): (expectedProposal?: ReadyProposalOutcome) => { proposalId: string; finish(applied: boolean): boolean } | undefined {
  return function beginApply(expectedProposal?: ReadyProposalOutcome) {
    if (applyingOwnerRef.current) return undefined;
    const owner = getProposalOwner();
    const currentRequest = readRequest();
    if (!owner || !currentRequest || owner.requestKey !== capabilitiesIdentity(currentRequest) ||
      !owner.request.isCurrent()) return undefined;
    let state: ConstructionProposalViewState;
    if (expectedProposal) {
      const snapshot = owner.resource.getSnapshot();
      const current = snapshot.loading ? undefined : snapshot.data;
      if (current?.status !== 'ready' ||
        current.response.proposalId !== expectedProposal.response.proposalId ||
        current.response.outputId !== expectedProposal.response.outputId ||
        !sameDraftRevision(current.response, expectedProposal.response) ||
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
        const liveOwner = getProposalOwner();
        const liveRequest = readRequest();
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

  };
}
