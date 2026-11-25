import type { ProposeConstructionArgs, PreviewExplorerBuilderArgs } from '../../../client/api';
import { sameDraftRevision } from '../../../contracts/identity';
import type { ConstructionProposalResponse, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { createQueryResource } from '../../../data/queryResource';
import type { RequestOwner } from '../../../data/requests';
import type { ConstructionProposalViewState } from '../model/constructionProposal';

type ProposalOutcome = Extract<ConstructionProposalViewState, { readonly status: 'ready' | 'needs-repair' }>;

export const abortError = () => new DOMException('The construction proposal was canceled.', 'AbortError');
const proposalInactiveError = () => new DOMException('This construction change is no longer active.', 'ConstructionProposalInactive');
export const errorIsProposalInactive = (error: unknown): boolean =>
  typeof error === 'object' && error !== null && 'name' in error && Reflect.get(error, 'name') === 'ConstructionProposalInactive';

const delayFor = (milliseconds: number, signal: AbortSignal): Promise<void> => new Promise((resolve, reject) => {
  if (signal.aborted) {
    reject(abortError());
    return;
  }
  const timer = setTimeout(() => {
    signal.removeEventListener('abort', abort);
    resolve();
  }, milliseconds);
  const abort = () => {
    clearTimeout(timer);
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

export function constructionProposalResource({
  client,
  args,
  proposalRequest,
  previewLimit,
  onReady,
}: {
  readonly client: {
    proposeConstruction(args: ProposeConstructionArgs, signal?: AbortSignal): Promise<ConstructionProposalResponse>;
    preview(args: PreviewExplorerBuilderArgs, signal?: AbortSignal): Promise<ExplorerBuilderPreviewResult>;
  };
  readonly args: ProposeConstructionArgs;
  readonly proposalRequest: NonNullable<ReturnType<RequestOwner['start']>>;
  readonly previewLimit: number;
  readonly onReady?: (response: ConstructionProposalResponse, preview: ExplorerBuilderPreviewResult) => boolean;
}): ReturnType<typeof createQueryResource<ProposalOutcome>> {
  return createQueryResource<ProposalOutcome>(async (resourceSignal) => {
    const abortFromResource = () => proposalRequest.cancel();
    if (resourceSignal.aborted) proposalRequest.cancel();
    else resourceSignal.addEventListener('abort', abortFromResource, { once: true });
    try {
      await delayFor(300, proposalRequest.signal);
      const response = await client.proposeConstruction(args, proposalRequest.signal);
      if (!proposalRequest.isCurrent()) throw abortError();
      const matchesBase = response.outputId === args.outputId && sameDraftRevision(response, {
        snapshotToken: args.snapshotToken,
        draftVersion: args.expectedDraftVersion,
        draftDigest: args.expectedDraftDigest,
      });
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
          requestId: `construction-preview-${globalThis.crypto.randomUUID()}`,
        }, proposalRequest.signal);
      }
      if (!proposalRequest.isCurrent()) throw abortError();
      if (!previewMatchesProposal(preview, proposalId, args.outputId)) {
        throw new Error('Loom did not return rows for this proposal receipt, so it cannot be applied yet.');
      }
      if (onReady && !onReady(response, preview)) throw proposalInactiveError();
      return { status: 'ready', response, preview };
    } finally {
      resourceSignal.removeEventListener('abort', abortFromResource);
    }
  });
}
