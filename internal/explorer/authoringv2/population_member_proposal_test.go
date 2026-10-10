package authoringv2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyPopulationMemberProposalIsServerResolvedAndChangesOnlySelectionRevision(t *testing.T) {
	catalog := commandCatalog()
	workspace, _, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create-population-table", []Command{{
		Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient",
	}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := workspace.Documents[0].Output.ID
	workspace.Documents[0].Population = &Population{SelectionRevisionID: "selection-before", Route: []PopulationRouteStep{}}
	before := workspace.Documents[0]
	command := Command{Type: CommandApplyPopulationMemberProposal, OutputID: outputID, ProposalID: "receipt-population-change"}
	if _, _, err := ApplyCommands(workspace, catalog, "unresolved-proposal", []Command{command}); err == nil {
		t.Fatal("unresolved population proposal applied")
	}
	if err := command.ResolvePopulationMemberProposal("selection-after", before.Population.Route); err != nil {
		t.Fatal(err)
	}
	updated, results, err := ApplyCommands(workspace, catalog, "resolved-proposal", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].OutputID != outputID || results[0].Type != CommandResultTableChanged {
		t.Fatalf("apply results = %#v", results)
	}
	want := before
	want.Population = &Population{SelectionRevisionID: "selection-after", Route: []PopulationRouteStep{}}
	if !reflect.DeepEqual(updated.Documents[0], want) {
		t.Fatalf("population proposal changed fields outside the revision:\n got %#v\nwant %#v", updated.Documents[0], want)
	}
}

func TestApplyPopulationMemberProposalWireIsClosedAndCASBound(t *testing.T) {
	var command Command
	if err := json.Unmarshal([]byte(`{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":"receipt-1"}`), &command); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"APPLY_POPULATION_MEMBER_PROPOSAL","outputId":"patients","proposalId":"receipt-1","selectionRevisionId":"forged"}`), &command); err == nil {
		t.Fatal("proposal accepted browser-supplied selection data")
	}
	request := ApplyCommandsRequest{
		CommandID: "apply-population", SemanticsVersion: CurrentSemanticsVersion,
		SnapshotToken: "snapshot-1", ExpectedDraftVersion: 4, ExpectedDraftDigest: "sha256:draft",
		Commands: []Command{{Type: CommandApplyPopulationMemberProposal, OutputID: "patients", ProposalID: "receipt-1"}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("closed proposal apply request rejected: %v", err)
	}
	request.Commands = append(request.Commands, Command{Type: CommandRenameTable, OutputID: "patients", Title: "other"})
	if err := request.Validate(); err == nil {
		t.Fatal("proposal apply was allowed to share an atomic command batch")
	}
}
