import { sameDraftRevision } from '../../../contracts/identity';
import type { ConstructionProposalResponse, ExplorerBuilderPreviewResult } from '../../../contracts/types';

export type ConstructionProposalViewState =
  | { readonly status: 'idle' }
  | { readonly status: 'previewing' }
  | {
      readonly status: 'ready';
      readonly response: ConstructionProposalResponse;
      readonly preview: ExplorerBuilderPreviewResult;
    }
  | {
      readonly status: 'needs-repair';
      readonly response: ConstructionProposalResponse;
    }
  | { readonly status: 'error'; readonly message: string; readonly retryable?: boolean }
  | {
      readonly status: 'applying';
      readonly response: ConstructionProposalResponse;
      readonly preview: ExplorerBuilderPreviewResult;
    };

export const constructionProposalIsApplicable = (
  state: ConstructionProposalViewState,
  identity: {
    readonly outputId: string;
    readonly snapshotToken: string;
    readonly draftVersion: number;
    readonly draftDigest: string;
  },
): state is Extract<ConstructionProposalViewState, { readonly status: 'ready' }> =>
  state.status === 'ready' &&
  Boolean(state.response.proposalId) &&
  state.response.previewStatus === 'READY' &&
  state.response.outputId === identity.outputId &&
  sameDraftRevision(state.response, identity) &&
  state.preview.receiptId === state.response.proposalId &&
  state.preview.outputId === identity.outputId &&
  state.preview.rows !== null;
