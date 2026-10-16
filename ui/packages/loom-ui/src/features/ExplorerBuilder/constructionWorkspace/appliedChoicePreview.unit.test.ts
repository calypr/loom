import type {
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import { matchesAcceptedChoicePreview, type AppliedChoicePreview } from './appliedChoicePreview';

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

const candidate: AppliedChoicePreview = {
  ownerKey: 'owner-a',
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

const accepted: Parameters<typeof matchesAcceptedChoicePreview>[1] = {
  ownerKey: 'owner-a',
  outputId: 'specimens',
  limit: 25,
  snapshotToken: 'snapshot-a',
  receipt,
};

describe('applied choice preview identity', () => {
  it('reuses the unmodified candidate rows only for the accepted receipt and request identity', () => {
    expect(matchesAcceptedChoicePreview(candidate, accepted)).toBe(true);
  });

  it('rejects a changed owner, output, limit, snapshot, receipt, or candidate workspace', () => {
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, ownerKey: 'owner-b' })).toBe(false);
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, outputId: 'patients' })).toBe(false);
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, limit: 50 })).toBe(false);
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, snapshotToken: 'snapshot-b' })).toBe(false);
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, receipt: { ...receipt, receiptId: 'other-receipt' } })).toBe(false);
    expect(matchesAcceptedChoicePreview(candidate, { ...accepted, receipt: { ...receipt, intentDigest: 'other-workspace' } })).toBe(false);
  });

  it('rejects a candidate preview for another table or without rows', () => {
    expect(matchesAcceptedChoicePreview({ ...candidate, preview: { ...preview, outputId: 'patients' } }, accepted)).toBe(false);
    expect(matchesAcceptedChoicePreview({ ...candidate, preview: { ...preview, rows: null } }, accepted)).toBe(false);
  });
});
