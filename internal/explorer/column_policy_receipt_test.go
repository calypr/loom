package explorer

import "testing"

func TestColumnPolicyProposalBindsExactColumnAndImmutableReceipt(t *testing.T) {
	base := testReceipt()
	base.ColumnPolicyProposal = &ColumnPolicyProposalBinding{RowDefinitionProposalBinding: RowDefinitionProposalBinding{DraftVersion: 7, DraftDigest: "sha256:base", OutputID: "out", BaseDocumentDigest: "sha256:document", CandidateWorkspaceDigest: base.IntentDigest, SnapshotToken: base.SnapshotToken}, ColumnID: "saved-column", ProjectionMode: "ALL"}
	id, err := ReceiptID(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ColumnPolicyProposalBinding){func(b *ColumnPolicyProposalBinding) { b.ColumnID = "other" }, func(b *ColumnPolicyProposalBinding) { b.ProjectionMode = "FIRST" }, func(b *ColumnPolicyProposalBinding) { b.DraftVersion++ }, func(b *ColumnPolicyProposalBinding) { b.DraftDigest = "other" }, func(b *ColumnPolicyProposalBinding) { b.OutputID = "other" }, func(b *ColumnPolicyProposalBinding) { b.BaseDocumentDigest = "other" }, func(b *ColumnPolicyProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *ColumnPolicyProposalBinding) { b.SnapshotToken = "other" }} {
		changed := base
		binding := *base.ColumnPolicyProposal
		mutate(&binding)
		changed.ColumnPolicyProposal = &binding
		changedID, err := ReceiptID(changed)
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
	for _, mutate := range []func(*ColumnPolicyProposalBinding){func(b *ColumnPolicyProposalBinding) { b.ColumnID = "" }, func(b *ColumnPolicyProposalBinding) { b.ProjectionMode = "AUTO" }, func(b *ColumnPolicyProposalBinding) { b.CandidateWorkspaceDigest = "other" }, func(b *ColumnPolicyProposalBinding) { b.SnapshotToken = "other" }} {
		binding := *base.ColumnPolicyProposal
		mutate(&binding)
		if err := binding.Validate(base.IntentDigest, base.SnapshotToken); err == nil {
			t.Fatal("invalid column policy binding accepted")
		}
	}
	base.SourceRelationshipProposal = &SourceRelationshipProposalBinding{}
	if err := base.Validate(); err == nil {
		t.Fatal("column policy receipt shared another proposal")
	}
}
