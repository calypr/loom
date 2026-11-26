import { sameDraftVersion, sameCatalogSnapshot } from '../../../contracts/identity';
import type {
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
} from '../../../contracts/types';
import type { PreviewLimit } from '../model/previewRecovery';

export type AppliedPreviewHandoff = {
  readonly ownerKey: string;
  readonly draftVersion: number;
  readonly previewRequestVersion: number;
  readonly outputId: string;
  readonly limit: PreviewLimit;
  readonly snapshotToken: string;
  readonly candidateWorkspaceDigest: string;
  readonly receipt?: ExplorerBuilderCompileResult;
  readonly preview: ExplorerBuilderPreviewResult;
};

type AcceptedPreviewReceipt = Pick<
  ExplorerBuilderCompileResult,
  'receiptId' | 'snapshotToken' | 'intentDigest'
>;

export const matchesAcceptedPreviewHandoff = (
  candidate: AppliedPreviewHandoff | undefined,
  accepted: {
    readonly ownerKey: string;
    readonly draftVersion: number;
    readonly previewRequestVersion: number;
    readonly outputId: string;
    readonly limit: PreviewLimit;
    readonly snapshotToken: string;
    readonly receipt: AcceptedPreviewReceipt;
  },
): candidate is AppliedPreviewHandoff => Boolean(
  candidate &&
  candidate.ownerKey === accepted.ownerKey &&
  sameDraftVersion(candidate.draftVersion, accepted.draftVersion) &&
  candidate.previewRequestVersion === accepted.previewRequestVersion &&
  candidate.outputId === accepted.outputId &&
  candidate.preview.outputId === accepted.outputId &&
  candidate.limit === accepted.limit &&
  sameCatalogSnapshot(candidate.snapshotToken, accepted.snapshotToken) &&
  sameCatalogSnapshot(candidate.snapshotToken, accepted.receipt.snapshotToken) &&
  candidate.preview.receiptId === accepted.receipt.receiptId &&
  candidate.candidateWorkspaceDigest === accepted.receipt.intentDigest &&
  (!candidate.receipt || (
    candidate.receipt.receiptId === accepted.receipt.receiptId &&
    sameCatalogSnapshot(candidate.receipt.snapshotToken, accepted.receipt.snapshotToken) &&
    candidate.receipt.intentDigest === accepted.receipt.intentDigest &&
    candidate.receipt.outputs.some((output) => output.outputId === accepted.outputId)
  )) &&
  candidate.preview.rows !== null,
);
