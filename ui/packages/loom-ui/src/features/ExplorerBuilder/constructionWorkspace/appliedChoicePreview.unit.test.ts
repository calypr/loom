import type {
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import { matchesAcceptedPreviewHandoff, type AppliedPreviewHandoff } from './appliedChoicePreview';

const preview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  receiptId: 'candidate-receipt',
  outputId: 'specimens',
  columns: [],
  rows: [{ value: 'candidate' }],
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
  rowCount: 1,
  diagnostics: [],
};

const candidate: AppliedPreviewHandoff = {
  ownerKey: 'owner-a',
  draftVersion: 2,
  previewRequestVersion: 0,
  outputId: 'specimens',
  limit: 25,
  snapshotToken: 'snapshot-a',
  candidateWorkspaceDigest: 'workspace-digest',
  preview,
};

const receipt: Pick<ExplorerBuilderCompileResult, 'receiptId' | 'snapshotToken' | 'intentDigest'> = {
  receiptId: 'candidate-receipt',
  snapshotToken: 'snapshot-a',
  intentDigest: 'workspace-digest',
};
const directReceipt: ExplorerBuilderCompileResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderReceipt',
  receiptId: 'candidate-receipt',
  snapshotToken: 'snapshot-a',
  intentDigest: 'workspace-digest',
  builder: {
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    kind: 'ExplorerBuilderWorkspace',
    explorer: { title: 'Test' },
    documents: [],
    tabs: [],
  },
  outputs: [{ outputId: 'specimens', columns: [] }],
  diagnostics: [],
};

const accepted: Parameters<typeof matchesAcceptedPreviewHandoff>[1] = {
  ownerKey: 'owner-a',
  draftVersion: 2,
  previewRequestVersion: 0,
  outputId: 'specimens',
  limit: 25,
  snapshotToken: 'snapshot-a',
  receipt,
};

describe('accepted preview handoff identity', () => {
  it('reuses the unmodified candidate rows only for the accepted receipt and request identity', () => {
    expect(matchesAcceptedPreviewHandoff(candidate, accepted)).toBe(true);
  });

  it('rejects a changed owner, output, limit, snapshot, receipt, or candidate workspace', () => {
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, ownerKey: 'owner-b' })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, draftVersion: 3 })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, previewRequestVersion: 1 })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, outputId: 'patients' })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, limit: 50 })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, snapshotToken: 'snapshot-b' })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, receipt: { ...receipt, receiptId: 'other-receipt' } })).toBe(false);
    expect(matchesAcceptedPreviewHandoff(candidate, { ...accepted, receipt: { ...receipt, intentDigest: 'other-workspace' } })).toBe(false);
  });

  it('rejects a candidate preview for another table or without rows', () => {
    expect(matchesAcceptedPreviewHandoff({ ...candidate, preview: { ...preview, outputId: 'patients' } }, accepted)).toBe(false);
    expect(matchesAcceptedPreviewHandoff({ ...candidate, preview: { ...preview, rows: null } }, accepted)).toBe(false);
  });

  it('requires a direct accepted receipt handoff to carry the same receipt identity', () => {
    const directCandidate: AppliedPreviewHandoff = {
      ...candidate,
      receipt: directReceipt,
    };
    expect(matchesAcceptedPreviewHandoff(directCandidate, accepted)).toBe(true);
    expect(matchesAcceptedPreviewHandoff({
      ...directCandidate,
      receipt: { ...directReceipt, receiptId: 'other-receipt' },
    }, accepted)).toBe(false);
  });
});
