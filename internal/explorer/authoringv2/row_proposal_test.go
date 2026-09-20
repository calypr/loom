package authoringv2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestApplyRowDefinitionProposalValidatesClosedCommand(t *testing.T) {
	base := ApplyCommandsRequest{
		CommandID: "command-1", SemanticsVersion: CurrentSemanticsVersion,
		SnapshotToken: "snapshot-1", ExpectedDraftVersion: 3,
		Commands: []Command{{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: "receipt-1"}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid proposal command rejected: %v", err)
	}

	for _, test := range []struct {
		name    string
		command Command
	}{
		{name: "missing output", command: Command{Type: CommandApplyRowDefinitionProposal, ProposalID: "receipt-1"}},
		{name: "missing proposal", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients"}},
		{name: "trimmed proposal", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: " receipt-1 "}},
		{name: "extra column", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: "receipt-1", Column: "patient-id"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.Commands = []Command{test.command}
			if err := request.Validate(); err == nil {
				t.Fatal("invalid proposal command was accepted")
			}
		})
	}

	request := base
	request.Commands = append(request.Commands, Command{Type: CommandRenameTable, OutputID: "patients", Title: "Renamed"})
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be the only command") {
		t.Fatalf("proposal was not required to be alone: %v", err)
	}

	var decoded Command
	if err := json.Unmarshal([]byte(`{"type":"APPLY_ROW_DEFINITION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","column":"client-controlled"}`), &decoded); err == nil {
		t.Fatal("proposal command accepted a client-supplied extra field")
	}
}

func TestApplyRowDefinitionProposalChangesOnlyRows(t *testing.T) {
	catalog := commandCatalog()
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	workspace, err := MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	original, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	rows := RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
		Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
			OccurrenceID: RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: MissingKeyError,
		}},
	}}
	command := Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: "receipt-1"}
	if err := command.ResolveRowDefinitionProposal(rows); err != nil {
		t.Fatal(err)
	}
	rows.Groups.Source.Field.FieldPath = "mutated-after-resolution"

	wire, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "fieldPath") || strings.Contains(string(wire), "mutated-after-resolution") {
		t.Fatalf("resolved row definition leaked into browser command: %s", wire)
	}

	got, _, err := ApplyCommands(workspace, catalog, "command-1", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := cloneWorkspace(original)
	if err != nil {
		t.Fatal(err)
	}
	expected.Documents[0].Rows = RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
		Source: GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{
			OccurrenceID: RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: MissingKeyError,
		}},
	}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("proposal changed more than Document.Rows\n got: %#v\nwant: %#v", got, expected)
	}
	if !reflect.DeepEqual(workspace, original) {
		t.Fatal("pure reducer mutated the input workspace")
	}
}

func TestApplyRowDefinitionProposalRequiresLifecycleResolution(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	before, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "command-1", []Command{{
		Type: CommandApplyRowDefinitionProposal, OutputID: "patients", ProposalID: "receipt-1",
	}})
	if err == nil || !strings.Contains(err.Error(), "no lifecycle-resolved row definition") {
		t.Fatalf("unprepared proposal command error = %v", err)
	}
	if !reflect.DeepEqual(workspace, before) {
		t.Fatal("failed proposal reducer mutated the input workspace")
	}
}
