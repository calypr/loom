package authoringv2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestApplyTableShapeProposalValidatesClosedCommand(t *testing.T) {
	base := ApplyCommandsRequest{
		CommandID: "command-1", SemanticsVersion: CurrentSemanticsVersion,
		SnapshotToken: "snapshot-1", ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft",
		Commands: []Command{{Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: "receipt-1"}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid proposal command rejected: %v", err)
	}

	for _, test := range []struct {
		name    string
		command Command
	}{
		{name: "missing output", command: Command{Type: CommandApplyTableShapeProposal, ProposalID: "receipt-1"}},
		{name: "missing proposal", command: Command{Type: CommandApplyTableShapeProposal, OutputID: "patients"}},
		{name: "trimmed output", command: Command{Type: CommandApplyTableShapeProposal, OutputID: " patients ", ProposalID: "receipt-1"}},
		{name: "trimmed proposal", command: Command{Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: " receipt-1 "}},
		{name: "extra column", command: Command{Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: "receipt-1", Column: "patient-id"}},
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
	if err := json.Unmarshal([]byte(`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients","proposalId":"receipt-1","column":"client-controlled"}`), &decoded); err == nil {
		t.Fatal("proposal command accepted a client-supplied extra field")
	}
}

func TestApplyTableShapeProposalChangesOnlyTableShape(t *testing.T) {
	catalog := commandCatalog()
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	workspace = MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	original, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	integer := int64(2)
	shape := &TableShape{Derived: []DerivedConstruction{{
		ConstructionID: "derived_1", Output: ColumnOutput{Column: "patient_id_twice", Label: "Patient ID twice"}, Operation: "ADD",
		Left:               ArithmeticOperand{Kind: "COLUMN", Column: "patient_id"},
		Right:              ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: "INTEGER", Integer: &integer}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
	command := Command{Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: "receipt-1"}
	if err := command.ResolveTableShapeProposal(shape); err != nil {
		t.Fatal(err)
	}
	shape.Derived[0].Output.Column = "mutated_after_resolution"

	wire, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "tableShape") || strings.Contains(string(wire), "patient_id_twice") {
		t.Fatalf("resolved table shape leaked into browser command: %s", wire)
	}

	got, results, err := ApplyCommands(workspace, catalog, "command-1", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := cloneWorkspace(original)
	if err != nil {
		t.Fatal(err)
	}
	wantShape := shape
	wantShape.Derived[0].Output.Column = "patient_id_twice"
	expected.Documents[0].TableShape = wantShape
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("proposal changed more than Document.TableShape\n got: %#v\nwant: %#v", got, expected)
	}
	if len(results) != 1 || results[0].Type != CommandResultTableChanged || results[0].OutputID != "patients" {
		t.Fatalf("command results = %#v", results)
	}
	if !reflect.DeepEqual(workspace, original) {
		t.Fatal("pure reducer mutated the input workspace")
	}
}

func TestApplyTableShapeProposalResolvesRemoval(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	integer := int64(1)
	workspace.Documents[0].TableShape = &TableShape{Derived: []DerivedConstruction{{
		ConstructionID: "derived_1", Output: ColumnOutput{Column: "copy", Label: "Copy"}, Operation: "ADD",
		Left: ArithmeticOperand{Kind: "COLUMN", Column: "patient_id"}, Right: ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: "INTEGER", Integer: &integer}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
	command := Command{Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: "receipt-remove"}
	if err := command.ResolveTableShapeProposal(nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := ApplyCommands(workspace, commandCatalog(), "command-remove", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	if got.Documents[0].TableShape != nil {
		t.Fatalf("table shape removal retained %#v", got.Documents[0].TableShape)
	}
}

func TestApplyTableShapeProposalRequiresLifecycleResolution(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(RecordsRowDefinition())
	before, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "command-1", []Command{{
		Type: CommandApplyTableShapeProposal, OutputID: "patients", ProposalID: "receipt-1",
	}})
	if err == nil || !strings.Contains(err.Error(), "no lifecycle-resolved table shape") {
		t.Fatalf("unprepared proposal command error = %v", err)
	}
	if !reflect.DeepEqual(workspace, before) {
		t.Fatal("failed proposal reducer mutated the input workspace")
	}
}
