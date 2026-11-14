package lifecycle

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestRowDefinitionProposalProjectsExactCandidateWithoutSaving(t *testing.T) {
	service, store, snapshot, _ := rowProposalService(t)
	enableRowDefinitionPreview(service)
	before := append([]byte(nil), store.created.DraftConfig...)
	request := rowProposalRequest(store.created, snapshot)
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := authoringv2.DecodeWorkspace(store.receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proposal.CandidateDocument, verified.Documents[0]) {
		t.Fatal("candidate document did not come from the verified receipt")
	}
	if proposal.CandidateDocument.Rows.Kind != authoringv2.RowDefinitionExpanded || proposal.CandidateNetwork.SourceRows.Kind != authoringv2.RowDefinitionExpanded || proposal.CandidateNetwork.OutputID != request.OutputID || proposal.CandidateNetwork.Owner != "DOCUMENT" {
		t.Fatalf("candidate source rows missing or stale: %#v", proposal)
	}
	identity := proposal.CandidateRowIdentity
	if identity.OutputID != request.OutputID || len(identity.Stages) != 1 || identity.Stages[0].Identity.Kind != "SOURCE_ITEM" || identity.Stages[0].Identity.ScopePath != "component[]" || identity.Stages[0].Identity.MissingPolicy != string(authoringv2.EmptyCollectionPreserveParent) {
		t.Fatalf("candidate item identity was guessed from mode: %#v", identity)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(store.created.DraftConfig, before) {
		t.Fatal("projecting a row proposal saved or rewrote the draft")
	}
	applied, err := service.ApplyCommands(context.Background(), "project-a", "patients", rowProposalApplyRequest(store.created, snapshot, proposal.ProposalID, "apply-projection"), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied.Workspace.Documents[0], proposal.CandidateDocument) {
		t.Fatal("projected document differs from receipt-applied document")
	}
}

func TestRowDefinitionProposalProjectsNamedGroupAfterSavedFilter(t *testing.T) {
	service, store, snapshot, workspace := rowProposalService(t)
	addRowProposalSourceFilter(t, store, workspace)
	enableRowDefinitionPreview(service)
	service.config.ExplicitGroupResolver = &rowProposalExplicitGroupResolver{}
	request := rowProposalRequest(store.created, snapshot)
	request.Selection = RowDefinitionSelection{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{RevisionID: "group-revision-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberExclude}}
	proposal, err := service.ProposeRowDefinition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CandidateDocument.Construction == nil || len(proposal.CandidateDocument.Construction.Steps) != 1 || proposal.CandidateDocument.Construction.Steps[0].ID != "filter_source" || proposal.CandidateDocument.Rows.Groups == nil || proposal.CandidateDocument.Rows.Groups.AfterStepID != "filter_source" {
		t.Fatalf("candidate dropped downstream work or changed insertion anchor: %#v", proposal.CandidateDocument)
	}
	stages := proposal.CandidateRowIdentity.Stages
	if len(stages) != 3 || stages[1].OperationID != "filter_source" || stages[2].Identity.Kind != "NAMED_GROUP" || stages[2].Identity.InputStageID != "filter_source" || stages[2].Identity.GroupRevisionID != "group-revision-1" || stages[2].Identity.MissingPolicy != string(authoringv2.UnassignedMemberExclude) {
		t.Fatalf("named group projection lost exact scope or empty policy: %#v", stages)
	}
	if store.saveDraftCalls != 0 {
		t.Fatal("candidate projection saved the draft")
	}
}
