package explorer

import "testing"

func TestSourceRelationshipProposalBindsExactImmutableReceipt(t *testing.T) {
	base := testReceipt()
	base.SourceRelationshipProposal = &SourceRelationshipProposalBinding{DraftVersion: 7, DraftDigest: "sha256:base", OutputID: "out", BaseDocumentDigest: "sha256:document", CandidateWorkspaceDigest: base.IntentDigest, SnapshotToken: base.SnapshotToken}
	id, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SourceRelationshipProposalBinding){func(b *SourceRelationshipProposalBinding) { b.DraftVersion++ }, func(b *SourceRelationshipProposalBinding) { b.DraftDigest = "another" }, func(b *SourceRelationshipProposalBinding) { b.OutputID = "other" }, func(b *SourceRelationshipProposalBinding) { b.BaseDocumentDigest = "other" }, func(b *SourceRelationshipProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *SourceRelationshipProposalBinding) { b.SnapshotToken = "other" }} {
		changed := base
		b := *base.SourceRelationshipProposal
		mutate(&b)
		changed.SourceRelationshipProposal = &b
		changedID, err := ReceiptID(changed)
		if err != nil {
			t.Fatal(err)
		}
		if changedID == id {
			t.Fatal("changed proposal binding retained receipt identity")
		}
	}
	if err := base.SourceRelationshipProposal.Validate(base.IntentDigest, base.SnapshotToken); err != nil {
		t.Fatal(err)
	}
	if err := base.SourceRelationshipProposal.Validate("other", base.SnapshotToken); err == nil {
		t.Fatal("candidate digest mismatch accepted")
	}
}
