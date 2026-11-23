package explorer

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func TestSourceRelationshipProposalBindsExactImmutableReceipt(t *testing.T) {
	base := testReceipt()
	base.SourceRelationshipProposal = &receiptmodel.SourceRelationshipProposalBinding{DraftVersion: 7, DraftDigest: "sha256:base", OutputID: "out", BaseDocumentDigest: "sha256:document", CandidateWorkspaceDigest: base.IntentDigest, SnapshotToken: base.SnapshotToken}
	id, err := receiptmodel.ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*receiptmodel.SourceRelationshipProposalBinding){func(b *receiptmodel.SourceRelationshipProposalBinding) { b.DraftVersion++ }, func(b *receiptmodel.SourceRelationshipProposalBinding) { b.DraftDigest = "another" }, func(b *receiptmodel.SourceRelationshipProposalBinding) { b.OutputID = "other" }, func(b *receiptmodel.SourceRelationshipProposalBinding) { b.BaseDocumentDigest = "other" }, func(b *receiptmodel.SourceRelationshipProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *receiptmodel.SourceRelationshipProposalBinding) { b.SnapshotToken = "other" }} {
		changed := base
		b := *base.SourceRelationshipProposal
		mutate(&b)
		changed.SourceRelationshipProposal = &b
		changedID, err := receiptmodel.ReceiptID(changed)
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
