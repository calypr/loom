package authoringv2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestApplyConstructionProposalStrictlyAppliesServerResolvedCandidate(t *testing.T) {
	legacy := legacyPivotDocument()
	workspace := constructionWorkspace(legacy)
	original, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := UpgradeDocumentToConstruction(legacy)
	if err != nil {
		t.Fatalf("migrate proposal base: %v", err)
	}
	proposal := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", ProposalID: "receipt-1"}
	if err := proposal.ResolveConstructionProposal(candidate.Construction); err != nil {
		t.Fatalf("resolve proposal: %v", err)
	}
	candidate.Construction.Steps[0].Operation.Pivot.Categories[0].OutputColumnID = "mutated_after_resolution"

	wire, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "construction") || strings.Contains(string(wire), "legacy_pivot") {
		t.Fatalf("resolved construction leaked into browser command: %s", wire)
	}

	request := ApplyCommandsRequest{
		CommandID: "command-1", SemanticsVersion: CurrentSemanticsVersion, SnapshotToken: "snapshot-1",
		ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft", Commands: []Command{proposal},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid proposal request rejected: %v", err)
	}
	got, results, err := ApplyCommands(workspace, commandCatalog(), request.CommandID, request.Commands)
	if err != nil {
		t.Fatalf("apply construction proposal: %v", err)
	}
	want, err := UpgradeDocumentToConstruction(legacy)
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace := constructionWorkspace(want).NormalizePresentationOrders()
	want = wantWorkspace.Documents[0]
	if !reflect.DeepEqual(got.Documents[0], want) {
		t.Fatalf("applied document differs from server candidate\n got: %#v\nwant: %#v", got.Documents[0], want)
	}
	if len(results) != 1 || results[0].Type != CommandResultTableChanged || results[0].OutputID != "patients" {
		t.Fatalf("results = %#v", results)
	}
	if !reflect.DeepEqual(workspace, original) {
		t.Fatal("proposal reducer mutated accepted workspace")
	}
}

func TestApplyConstructionProposalRejectsClientPlanAndUnresolvedApply(t *testing.T) {
	for _, raw := range []string{
		`{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","construction":{"version":1,"steps":[]}}`,
		`{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","removeStepIds":["step-1"]}`,
	} {
		var decoded Command
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatalf("proposal accepted client candidate fields: %s", raw)
		}
	}

	command := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", ProposalID: "receipt-1"}
	if err := command.validate(); err != nil {
		t.Fatalf("closed proposal command invalid: %v", err)
	}
	workspace := constructionWorkspace(legacyPivotDocument())
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "command-1", []Command{command}); err == nil || !strings.Contains(err.Error(), "no lifecycle-resolved construction") {
		t.Fatalf("unresolved apply error = %v", err)
	}
	if err := command.ResolveConstructionProposal(nil); err == nil {
		t.Fatal("nil resolved construction was accepted")
	}
}
