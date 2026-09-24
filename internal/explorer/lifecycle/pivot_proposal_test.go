package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestPivotProposalRejectsStaleDraftAndMixedFamilyWithoutWriting(t *testing.T) {
	store, service, snapshot, _, choice := constructionSemanticChoiceFixture(t, nil, nil, "Observation")
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok {
		t.Fatalf("choice source = %T, want semantic binding", identity.Source)
	}
	familyID, err := pivotFamilyID(source, identity.Route, capability.ConstructionChoiceValue)
	if err != nil {
		t.Fatal(err)
	}
	request := PivotProposalRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", FamilyID: familyID, CommandID: "pivot-candidate",
		Selections: []PivotProposalSelection{{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
	}
	before := string(store.created.DraftConfig)
	request.ExpectedDraftDigest = "sha256:stale"
	if _, err := service.ProposePivot(context.Background(), request); !proposalErrorIs(err, ClassConflict, "DRAFT_CONFLICT") {
		t.Fatalf("stale draft error = %v, want DRAFT_CONFLICT", err)
	}
	request.ExpectedDraftDigest = store.created.DraftDigest
	request.Selections[0].Form = capability.ConstructionChoiceFirst
	if _, err := service.ProposePivot(context.Background(), request); !proposalErrorIs(err, ClassUnprocessable, "INVALID_PIVOT_FORM") {
		t.Fatalf("reducing form error = %v, want INVALID_PIVOT_FORM", err)
	}
	request.Selections[0].Form = capability.ConstructionChoiceValue
	request.FamilyID = "different-family"
	if _, err := service.ProposePivot(context.Background(), request); !proposalErrorIs(err, ClassUnprocessable, "INVALID_PIVOT_FAMILY") {
		t.Fatalf("mixed family error = %v, want INVALID_PIVOT_FAMILY", err)
	}
	if store.saveDraftCalls != 0 || string(store.created.DraftConfig) != before {
		t.Fatalf("invalid proposal changed saved draft: saves=%d", store.saveDraftCalls)
	}
}
