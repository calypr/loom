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
	if err := proposal.ResolveConstructionProposal(&candidate); err != nil {
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
		t.Fatal("nil resolved candidate was accepted")
	}
}

func TestApplyConstructionProposalCarriesValidatedCohortAnchorRepair(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns[0].Source = ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	document.Rows = RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
		Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: UnassignedMemberExclude,
		}},
		AfterStepID: "source_filter",
		RowValues:   []ExplicitGroupRowValue{{ColumnID: "status", Policy: ConstructionRowValueAll}},
	}}
	document.Construction.Steps = []ConstructionStep{
		{
			ID: "source_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "status", Operator: ConstructionFilterExists}},
			Outputs:   []StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
		},
		{
			ID: "cohort_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "group_rows"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "group_label", Operator: ConstructionFilterExists}},
			Outputs: []StageColumn{
				{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
				{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
				{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
				{ID: "members", Name: "members", Label: "Members", Type: "array"},
				{ID: "status", Name: "status", Label: "Status", Type: "array"},
			},
		},
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source-filter/cohort-filter base: %v", err)
	}

	candidate, _, err := document.ProposeStepRemoval("source_filter", nil)
	if err != nil {
		t.Fatalf("propose source-filter removal: %v", err)
	}
	if candidate.Rows.Groups.AfterStepID != "" {
		t.Fatalf("candidate anchor was not rebased to source projection: %#v", candidate.Rows.Groups)
	}

	command := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", ProposalID: "receipt-1"}
	if err := command.ResolveConstructionProposal(&candidate); err != nil {
		t.Fatalf("resolve exact candidate: %v", err)
	}
	wantRows, err := cloneRowDefinition(candidate.Rows)
	if err != nil {
		t.Fatal(err)
	}
	wantConstruction, err := cloneConstruction(candidate.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Rows.Groups.Source.Explicit.RevisionID = "mutated-after-resolution"
	candidate.Rows.Groups.RowValues[0].ColumnID = "mutated-after-resolution"
	candidate.Construction.Steps[0].ID = "mutated-after-resolution"
	workspace, results, err := ApplyCommands(constructionWorkspace(document), commandCatalog(), "apply-anchor-removal", []Command{command})
	if err != nil {
		t.Fatalf("apply anchor removal: %v", err)
	}
	got := workspace.Documents[0]
	if !reflect.DeepEqual(got.Rows, wantRows) {
		t.Fatalf("apply lost validated cohort rebase/source/values: got=%#v want=%#v", got.Rows, wantRows)
	}
	if !reflect.DeepEqual(got.Construction, wantConstruction) || len(results) != 1 {
		t.Fatalf("applied construction/results = %#v / %#v, want exact candidate", got.Construction, results)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("applied rebased cohort document is invalid: %v", err)
	}
}
