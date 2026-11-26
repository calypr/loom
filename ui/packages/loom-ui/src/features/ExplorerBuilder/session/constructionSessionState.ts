import type { GetConstructionCapabilitiesArgs, LoomClient } from '../../../client/api';
import type { ConstructionProposalRequest, ConstructionProposalResponse, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { createStore } from '../../../data/createStore';
import { RequestOwner } from '../../../data/requests';
import type { ConstructionProposalViewState } from '../model/constructionProposal';
import type { ConstructionReshapePivotDiscovery } from '../model/reshapeForms';
import type { ConstructionApplyAttempt } from './constructionApplyOwnership';
import type { constructionProposalResource } from './constructionProposal';

export type ConstructionCandidateIntent = Pick<
  ConstructionProposalRequest,
  'candidateConstruction' | 'changedStepId' | 'removeStepIds' | 'groupSource' | 'groupSources' | 'pivotSources' | 'filterSources' | 'deriveSources' | 'unpivotSources'
>;

export interface ConstructionEditingAttempt {
  ready(response: ConstructionProposalResponse, preview: ExplorerBuilderPreviewResult): boolean;
  failed(message: string): void;
  cancel(): boolean;
  beginApply(): boolean;
  finishApply(applied: boolean): void;
}
type ConstructionRequest = NonNullable<ReturnType<RequestOwner['start']>>;
export type ProposalOutcome = Extract<ConstructionProposalViewState, { readonly status: 'ready' | 'needs-repair' }>;
type ProposalResource = ReturnType<typeof constructionProposalResource>;
export type ProposalOwner = {
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

export type ConstructionLifecycleClient = Pick<
  LoomClient,
  'getConstructionCapabilities' | 'discoverConstructionCategories' | 'proposeConstruction' | 'preview'
>;

export interface ConstructionEditingOwner {
  onStart(intent: ConstructionCandidateIntent, request: GetConstructionCapabilitiesArgs): ConstructionEditingAttempt | undefined;
}

export type ReadyProposalOutcome = Extract<ProposalOutcome, { readonly status: 'ready' }>;

type ConstructionSessionSnapshot = {
  readonly proposalOwner?: ProposalOwner;
  readonly proposalOverride?: ProposalOverride;
  readonly pivotDiscoveryLoad: PivotDiscoveryLoad;
};

export function createConstructionSessionState() {
  const store = createStore<ConstructionSessionSnapshot>({ pivotDiscoveryLoad: { key: '', state: undefined } });
  const requests = new RequestOwner();
  const identity = 'construction';
  requests.setIdentity(identity);
  let ownerSequence = 0;
  return {
    getSnapshot: store.getSnapshot,
    subscribe: store.subscribe,
    requests: {
      start: (name: 'proposal' | 'category-discovery') => requests.start(name, identity),
      cancel: (name: 'proposal' | 'category-discovery') => requests.cancel(name),
    },
    applyingOwnerRef: { current: undefined } as { current: ConstructionApplyAttempt<ProposalOwner> | undefined },
    candidateIntent: { current: undefined } as { current: ConstructionCandidateIntent | undefined },
    nextProposalId: () => ++ownerSequence,
    getProposalOwner: () => store.getSnapshot().proposalOwner,
    setProposalOwner(proposalOwner: ProposalOwner | undefined) {
      store.setSnapshot({ ...store.getSnapshot(), proposalOwner, proposalOverride: undefined });
    },
    setProposalOverride(proposalOverride: ProposalOverride) {
      store.setSnapshot({ ...store.getSnapshot(), proposalOverride });
    },
    setPivotDiscoveryLoad(pivotDiscoveryLoad: PivotDiscoveryLoad) {
      store.setSnapshot({ ...store.getSnapshot(), pivotDiscoveryLoad });
    },
  };
}
