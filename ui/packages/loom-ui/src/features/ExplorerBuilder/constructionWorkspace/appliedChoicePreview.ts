import type {
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import type { PreviewLimit } from '../authoring/previewRecovery';

export type AppliedChoicePreview = {
  readonly ownerKey: string;
  readonly outputId: string;
  readonly limit: PreviewLimit;
  readonly snapshotToken: string;
  readonly candidateWorkspaceDigest: string;
  readonly preview: ExplorerBuilderPreviewResult;
};

type AcceptedPreviewReceipt = Pick<
  ExplorerBuilderCompileResult,
  'receiptId' | 'snapshotToken' | 'intentDigest'
>;

export const matchesAcceptedChoicePreview = (
  candidate: AppliedChoicePreview | undefined,
  accepted: {
    readonly ownerKey: string;
    readonly outputId: string;
    readonly limit: PreviewLimit;
    readonly snapshotToken: string;
    readonly receipt: AcceptedPreviewReceipt;
  },
): candidate is AppliedChoicePreview => Boolean(
  candidate &&
  candidate.ownerKey === accepted.ownerKey &&
  candidate.outputId === accepted.outputId &&
  candidate.preview.outputId === accepted.outputId &&
  candidate.limit === accepted.limit &&
  candidate.snapshotToken === accepted.snapshotToken &&
  candidate.snapshotToken === accepted.receipt.snapshotToken &&
  candidate.preview.receiptId === accepted.receipt.receiptId &&
  candidate.candidateWorkspaceDigest === accepted.receipt.intentDigest &&
  candidate.preview.rows !== null,
);
