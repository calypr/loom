import { useCallback, useEffect, useRef, useState } from 'react';
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
  'candidateConstruction' | 'changedStepId' | 'removeStepIds'
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

type ProposalLoad = {
  readonly key: string;
  readonly state: ConstructionProposalViewState;
};

type PivotDiscoveryLoad = {
  readonly key: string;
  readonly state: ConstructionReshapePivotDiscovery | undefined;
};

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
    ])
  : '';

const errorMessage = (error: unknown, fallback: string): string =>
  error instanceof Error && error.message.trim() ? error.message : fallback;

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
  const [proposalLoad, setProposalLoad] = useState<ProposalLoad>({
    key: '',
    state: { status: 'idle' },
  });
  const [pivotDiscoveryLoad, setPivotDiscoveryLoad] = useState<PivotDiscoveryLoad>({ key: '', state: undefined });
  const capabilitiesController = useRef<AbortController | undefined>(undefined);
  const proposalController = useRef<AbortController | undefined>(undefined);
  const pivotDiscoveryController = useRef<AbortController | undefined>(undefined);
  const proposalTimer = useRef<number | undefined>(undefined);
  const proposalGeneration = useRef(0);
  const pivotDiscoveryGeneration = useRef(0);
  const candidateIntent = useRef<ConstructionCandidateIntent | undefined>(undefined);
  const capabilitiesRequestRef = useRef(capabilitiesRequest);
  capabilitiesRequestRef.current = capabilitiesRequest;

  const requestKey = capabilitiesIdentity(capabilitiesRequest);

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
    if (proposalTimer.current !== undefined) {
      window.clearTimeout(proposalTimer.current);
      proposalTimer.current = undefined;
    }
    proposalController.current?.abort();
    proposalController.current = undefined;
  }, []);

  useEffect(() => {
    invalidateProposalRequest();
    candidateIntent.current = undefined;
    setProposalLoad({ key: requestKey, state: { status: 'idle' } });
    return invalidateProposalRequest;
  }, [invalidateProposalRequest, requestKey]);

  const getPreview = useCallback(async (
    response: ConstructionProposalResponse,
    args: GetConstructionCapabilitiesArgs,
    controller: AbortController,
  ) => {
    if (response.previewStatus !== 'READY') {
      setProposalLoad({ key: requestKey, state: { status: 'needs-repair', response } });
      return;
    }
    const proposalId = response.proposalId;
    if (!proposalId) {
      setProposalLoad({
        key: requestKey,
        state: { status: 'error', message: 'Loom did not return a proposal receipt for this change.' },
      });
      return;
    }

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
      }, controller.signal);
    }
    if (controller.signal.aborted) return;
    if (!previewMatchesProposal(preview, proposalId, args.outputId)) {
      setProposalLoad({
        key: requestKey,
        state: {
          status: 'error',
          message: 'Loom did not return rows for this proposal receipt, so it cannot be applied yet.',
        },
      });
      return;
    }
    setProposalLoad({ key: requestKey, state: { status: 'ready', response, preview } });
  }, [client, previewLimit, requestKey]);

  const onCandidateChange = useCallback((intent: ConstructionCandidateIntent | undefined) => {
    invalidateProposalRequest();
    candidateIntent.current = intent;
    if (!intent || !capabilitiesRequest) {
      setProposalLoad({ key: requestKey, state: { status: 'idle' } });
      return;
    }

    const generation = proposalGeneration.current;
    const args: ProposeConstructionArgs = {
      project: capabilitiesRequest.project,
      explorerId: capabilitiesRequest.explorerId,
      ...(capabilitiesRequest.authResourcePath
        ? { authResourcePath: capabilitiesRequest.authResourcePath }
        : {}),
      snapshotToken: capabilitiesRequest.snapshotToken,
      expectedDraftVersion: capabilitiesRequest.expectedDraftVersion,
      expectedDraftDigest: capabilitiesRequest.expectedDraftDigest,
      outputId: capabilitiesRequest.outputId,
      ...intent,
      limit: previewLimit,
      requestId: `construction-proposal-${window.crypto.randomUUID()}`,
    };
    const controller = new AbortController();
    proposalController.current = controller;
    setProposalLoad({ key: requestKey, state: { status: 'previewing' } });
    proposalTimer.current = window.setTimeout(() => {
      proposalTimer.current = undefined;
      void client.proposeConstruction(args, controller.signal).then(
        async (response) => {
          if (
            controller.signal.aborted ||
            proposalGeneration.current !== generation
          ) return;
          const matchesBase =
            response.outputId === args.outputId &&
            response.snapshotToken === args.snapshotToken &&
            response.draftVersion === args.expectedDraftVersion &&
            response.draftDigest === args.expectedDraftDigest;
          if (!matchesBase) {
            setProposalLoad({
              key: requestKey,
              state: {
                status: 'error',
                message: 'The table draft changed while Loom checked this proposal. Review the current draft and try again.',
              },
            });
            return;
          }
          await getPreview(response, capabilitiesRequest, controller);
        },
      ).catch((error: unknown) => {
        if (
          controller.signal.aborted ||
          proposalGeneration.current !== generation
        ) return;
        setProposalLoad({
          key: requestKey,
          state: {
            status: 'error',
            message: errorMessage(error, 'Loom could not check this construction change.'),
          },
        });
      });
    }, 300);
  }, [capabilitiesRequest, client, getPreview, invalidateProposalRequest, previewLimit, requestKey]);

  const retry = useCallback(() => {
    if (candidateIntent.current) onCandidateChange(candidateIntent.current);
  }, [onCandidateChange]);

  const cancel = useCallback(() => {
    invalidateProposalRequest();
    candidateIntent.current = undefined;
    setProposalLoad({ key: requestKey, state: { status: 'idle' } });
  }, [invalidateProposalRequest, requestKey]);

  const onDiscoverCategories = useCallback((request: ConstructionReshapePivotDiscoveryRequest) => {
    invalidatePivotDiscovery();
    const args = capabilitiesRequestRef.current;
    const discoveryKey = JSON.stringify([requestKey, request.stageId, request.categoryColumnId, request.valueColumnId]);
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
    const discoveryArgs: DiscoverConstructionCategoriesArgs = {
      project: args.project,
      explorerId: args.explorerId,
      ...(args.authResourcePath ? { authResourcePath: args.authResourcePath } : {}),
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      expectedDraftDigest: args.expectedDraftDigest,
      outputId: args.outputId,
      stageId: request.stageId,
      categoryColumnId: request.categoryColumnId,
      valueColumnId: request.valueColumnId,
      requestId: `construction-categories-${window.crypto.randomUUID()}`,
    };
    void client.discoverConstructionCategories(discoveryArgs, controller.signal).then(
      (response: ConstructionCategoryDiscoveryResponse) => {
        if (controller.signal.aborted || pivotDiscoveryGeneration.current !== generation) return;
        const matchesRequest =
          response.snapshotToken === args.snapshotToken &&
          response.draftVersion === args.expectedDraftVersion &&
          response.draftDigest === args.expectedDraftDigest &&
          response.outputId === args.outputId &&
          response.stageId === request.stageId &&
          response.categoryColumnId === request.categoryColumnId &&
          response.valueColumnId === request.valueColumnId &&
          response.complete === true &&
          response.proofFingerprint.trim() !== '';
        setPivotDiscoveryLoad({
          key: discoveryKey,
          state: matchesRequest
            ? { ...request, status: 'complete', categories: response.categories }
            : { ...request, status: 'failed', reason: 'Loom returned category values for a different stage or field pair.' },
        });
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

  const beginApply = useCallback(() => {
    const state = proposalLoad.key === requestKey ? proposalLoad.state : { status: 'idle' as const };
    if (!capabilitiesRequest || state.status !== 'ready') return undefined;
    const identity = {
      outputId: capabilitiesRequest.outputId,
      snapshotToken: capabilitiesRequest.snapshotToken,
      draftVersion: capabilitiesRequest.expectedDraftVersion,
      draftDigest: capabilitiesRequest.expectedDraftDigest,
    };
    if (!constructionProposalIsApplicable(state, identity)) return undefined;
    setProposalLoad({
      key: requestKey,
      state: { status: 'applying', response: state.response, preview: state.preview },
    });
    return state.response.proposalId;
  }, [capabilitiesRequest, proposalLoad, requestKey]);

  const finishApply = useCallback((applied: boolean) => {
    if (applied) {
      cancel();
      return;
    }
    setProposalLoad({
      key: requestKey,
      state: {
        status: 'error',
        message: 'Loom could not save this proposal. Review the Builder message and try again if the draft is unchanged.',
      },
    });
  }, [cancel, requestKey]);

  const activeCapabilities = capabilitiesLoad.key === requestKey
    ? capabilitiesLoad.state
    : requestKey
      ? { status: 'loading' as const }
      : { status: 'idle' as const };
  const activeProposal = proposalLoad.key === requestKey
    ? proposalLoad.state
    : { status: 'idle' as const };
  const activePivotDiscoveryKey = pivotDiscoveryLoad.state
    ? JSON.stringify([
        requestKey,
        pivotDiscoveryLoad.state.stageId,
        pivotDiscoveryLoad.state.categoryColumnId,
        pivotDiscoveryLoad.state.valueColumnId,
      ])
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
    retry,
    cancel,
    beginApply,
    finishApply,
    pivotDiscovery: activePivotDiscovery,
    onDiscoverCategories,
  };
};
