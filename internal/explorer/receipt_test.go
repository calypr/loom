package explorer

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func TestColumnPolicyProposalBindsExactColumnAndImmutableReceipt(t *testing.T) {
	base := testReceipt()
	base.ColumnPolicyProposal = &receiptmodel.ColumnPolicyProposalBinding{RowDefinitionProposalBinding: receiptmodel.RowDefinitionProposalBinding{DraftVersion: 7, DraftDigest: "sha256:base", OutputID: "out", BaseDocumentDigest: "sha256:document", CandidateWorkspaceDigest: base.IntentDigest, SnapshotToken: base.SnapshotToken}, ColumnID: "saved-column", ProjectionMode: "ALL"}
	id, err := receiptmodel.ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*receiptmodel.ColumnPolicyProposalBinding){func(b *receiptmodel.ColumnPolicyProposalBinding) { b.ColumnID = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.ProjectionMode = "FIRST" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.DraftVersion++ }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.DraftDigest = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.OutputID = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.BaseDocumentDigest = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.SnapshotToken = "other" }} {
		changed := base
		binding := *base.ColumnPolicyProposal
		mutate(&binding)
		changed.ColumnPolicyProposal = &binding
		changedID, err := receiptmodel.ReceiptID(changed)
		if err != nil {
			t.Fatal(err)
		}
		if changedID == id {
			t.Fatal("changed column policy binding retained receipt identity")
		}
	}
	if err := base.ColumnPolicyProposal.Validate(base.IntentDigest, base.SnapshotToken); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*receiptmodel.ColumnPolicyProposalBinding){func(b *receiptmodel.ColumnPolicyProposalBinding) { b.ColumnID = "" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.ProjectionMode = "AUTO" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *receiptmodel.ColumnPolicyProposalBinding) { b.SnapshotToken = "other" }} {
		binding := *base.ColumnPolicyProposal
		mutate(&binding)
		if err := binding.Validate(base.IntentDigest, base.SnapshotToken); err == nil {
			t.Fatal("invalid column policy binding accepted")
		}
	}
	base.SourceRelationshipProposal = &receiptmodel.SourceRelationshipProposalBinding{}
	if err := base.Validate(); err == nil {
		t.Fatal("column policy receipt shared another proposal")
	}
}

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
