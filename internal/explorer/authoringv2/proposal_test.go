package authoringv2

import (
	"encoding/json"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestApplyPopulationMemberProposalIsServerResolvedAndChangesOnlySelectionRevision(t *testing.T) {
	catalog := commandCatalog()
	workspace, _, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create-population-table", CommandsWithoutResolution([]Command{{Type: CommandCreateTable, Title: "Patients", Payload: &TablePayload{RootNodeID: "patient"}}}))
	if err != nil {
		t.Fatal(err)
	}
	outputID := workspace.Documents[0].Output.ID
	workspace.Documents[0].Population = &ws.Population{SelectionRevisionID: "selection-before", Route: []ws.PopulationRouteStep{}}
	before := workspace.Documents[0]
	command := Command{Type: CommandApplyPopulationMemberProposal, OutputID: outputID, Payload: &ProposalPayload{ProposalID: "receipt-population-change"}}
	if _, _, err := ApplyCommands(workspace, catalog, "unresolved-proposal", CommandsWithoutResolution([]Command{command})); err == nil {
		t.Fatal("unresolved population proposal applied")
	}
	resolution, err := ResolvePopulationMemberProposal(command, "selection-after", before.Population.Route)
	if err != nil {
		t.Fatal(err)
	}
	updated, results, err := ApplyCommands(workspace, catalog, "resolved-proposal", []PreparedCommand{{Command: command, Resolution: resolution}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].OutputID != outputID || results[0].Type != CommandResultTableChanged {
		t.Fatalf("apply results = %#v", results)
	}
	want := before
	want.Population = &ws.Population{SelectionRevisionID: "selection-after", Route: []ws.PopulationRouteStep{}}
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
		CommandID: "apply-population", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: "snapshot-1", ExpectedDraftVersion: 4, ExpectedDraftDigest: "sha256:draft",
		Commands: []Command{{Type: CommandApplyPopulationMemberProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("closed proposal apply request rejected: %v", err)
	}
	request.Commands = append(request.Commands, Command{Type: CommandRenameTable, OutputID: "patients", Title: "other", Payload: &TablePayload{}})
	if err := request.Validate(); err == nil {
		t.Fatal("proposal apply was allowed to share an atomic command batch")
	}
}
