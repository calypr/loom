package authoringv2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRetiredTableShapeProposalCannotBeValidated(t *testing.T) {
	for _, raw := range []string{
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients","proposalId":"receipt-1"}`,
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients","proposalId":"receipt-1","column":"client-controlled"}`,
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients"}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			if err := command.validate(); err == nil {
				t.Fatalf("retired command validated: %s", raw)
			}
		}
	}
	request := ApplyCommandsRequest{CommandID: "command-1", SemanticsVersion: CurrentSemanticsVersion, SnapshotToken: "snapshot-1", ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft", Commands: []Command{{Type: "APPLY_TABLE_SHAPE_PROPOSAL", OutputID: "patients", ProposalID: "receipt-1"}}}
	if err := request.Validate(); err == nil {
		t.Fatal("retired command validated")
	}
}
func TestRetiredTableShapeProposalCannotMutateCurrentWorkspace(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	before, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, proposal := range []string{"receipt-1", "receipt-removal", ""} {
		_, _, err := ApplyCommands(workspace, commandCatalog(), "command-1", []Command{{Type: "APPLY_TABLE_SHAPE_PROPOSAL", OutputID: "patients", ProposalID: proposal}})
		if err == nil {
			t.Fatal("retired proposal reached reducer")
		}
		if !reflect.DeepEqual(workspace, before) {
			t.Fatal("failed retired command mutated current workspace")
		}
	}
}
