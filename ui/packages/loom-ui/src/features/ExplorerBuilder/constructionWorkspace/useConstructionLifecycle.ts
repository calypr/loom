import { useCallback, useEffect, useRef, useState } from 'react';
import type {
  GetConstructionCapabilitiesArgs,
  LoomClient,
  ProposeConstructionArgs,
} from '../../../api';
import type {
  ConstructionCapabilitiesResponse,
  ConstructionProposalRequest,
  ConstructionProposalResponse,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  constructionProposalIsApplicable,
  type ConstructionProposalViewState,
} from './ConstructionProposalPanel';

export type ConstructionLifecycleClient = Pick<
  LoomClient,
  'getConstructionCapabilities' | 'proposeConstruction' | 'preview'
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
  const capabilitiesController = useRef<AbortController | undefined>(undefined);
  const proposalController = useRef<AbortController | undefined>(undefined);
  const proposalTimer = useRef<number | undefined>(undefined);
  const proposalGeneration = useRef(0);
  const candidateIntent = useRef<ConstructionCandidateIntent | undefined>(undefined);
  const capabilitiesRequestRef = useRef(capabilitiesRequest);
  capabilitiesRequestRef.current = capabilitiesRequest;

  const requestKey = capabilitiesIdentity(capabilitiesRequest);

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
  };
};
