package authoringv2

import (
	"reflect"
	"testing"
)

func rowValueGroupedDocument() Document {
	document := workspaceDocument("patients")
	document.Columns = []Column{constructionSourceColumn("status", "status", "Status", "string")}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID: "group_status", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "group_status", Keys: []ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
			Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "records"}},
		}}, Outputs: []StageColumn{{ID: "status_group", Name: "status_group", Label: "Status", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}},
	}}}
	return document
}

func TestPopulateGroupedRowsPreservesIdentityReloadAndRemoval(t *testing.T) {
	document := rowValueGroupedDocument()
	keys := append([]ConstructionGroupKey(nil), document.Construction.Steps[0].Operation.Group.Keys...)
	column := constructionSourceColumn("gender_source", "gender", "Gender", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ConstructionRowValueAll); err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	step := document.Construction.Steps[0]
	if !reflect.DeepEqual(step.Operation.Group.Keys, keys) || len(step.RowValues) != 1 || step.Outputs[2].Type != "array" {
		t.Fatalf("column changed grouping or omitted its value binding: %#v", step)
	}
	raw, err := constructionWorkspace(document).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Documents[0].Construction.Steps[0].RowValues, step.RowValues) {
		t.Fatal("reload lost contributing-record value policy")
	}
	removed, _, err := ApplyCommands(reloaded, commandCatalog(), "remove-gender", []Command{{Type: CommandRemoveColumn, OutputID: "patients", Column: "gender"}})
	if err != nil {
		t.Fatal(err)
	}
	remaining := removed.Documents[0].Construction.Steps[0]
	if len(remaining.RowValues) != 0 || len(remaining.Outputs) != 2 || !reflect.DeepEqual(remaining.Operation.Group.Keys, keys) {
		t.Fatal("removing a column changed row grouping")
	}
}

func TestPopulatePivotRowsCarriesValuesThroughFollowingFilter(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps = document.Construction.Steps[:3]
	column := constructionSourceColumn("extra", "extra", "Extra", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ConstructionRowValueOne); err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	rowValue := document.Construction.Steps[0].RowValues[0]
	if rowValue.Policy != ConstructionRowValueOne {
		t.Fatal("ONE policy was not saved")
	}
	for _, step := range document.Construction.Steps {
		if _, exists := findStageColumnByID(step.Outputs, rowValue.OutputColumnID); !exists {
			t.Fatalf("step %s dropped the new column", step.ID)
		}
	}
}

func TestExplicitGroupsCreateAnchoredEffectiveConstructionStage(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Rows = RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
		Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
	}}
	filterSource := ConstructionStep{
		ID: "filter_status", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "status", Operator: ConstructionFilterExists}},
		Outputs:   []StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
	}
	filterCohorts := ConstructionStep{
		ID: "filter_cohorts", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "group_rows"}},
		Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "group_id", Operator: ConstructionFilterExists}},
		Outputs: []StageColumn{
			{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
			{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
			{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
			{ID: "members", Name: "members", Label: "Members", Type: "array"},
		},
	}
	document.Construction.Steps = []ConstructionStep{filterSource, filterCohorts}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source → cohort → authored filter sequence: %v", err)
	}
	removed, _, err := document.ProposeStepRemoval("filter_status", nil)
	if err != nil {
		t.Fatalf("remove the persisted cohort anchor: %v", err)
	}
	if removed.Rows.Groups.AfterStepID != "" || len(removed.Construction.Steps) != 1 ||
		removed.Construction.Steps[0].Inputs[0] != (ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: "group_rows"}) {
		t.Fatalf("anchor removal did not move the virtual stage to source projection: %#v", removed)
	}
	if document.Rows.Groups.AfterStepID != "filter_status" {
		t.Fatal("proposed anchor repair mutated the accepted document")
	}
	if err := removed.Validate(); err != nil {
		t.Fatalf("reanchored cohort sequence is invalid: %v", err)
	}
	withoutCohortSuffix, _, err := document.ProposeStepRemoval("filter_cohorts", nil)
	if err != nil {
		t.Fatalf("explicitly remove the post-cohort dependent stage: %v", err)
	}
	withoutCohortSuffix.Rows = RecordsRowDefinition()
	if err := withoutCohortSuffix.Validate(); err != nil {
		t.Fatalf("return to records after removing the virtual-stage dependent: %v", err)
	}
	if withoutCohortSuffix.Construction.Steps[0].Inputs[0].StepID == "group_rows" {
		t.Fatal("returning to records left a virtual group_rows reference")
	}

	wrongBoundary := document
	wrongBoundary.Construction = &Construction{Version: ConstructionVersion, Steps: append([]ConstructionStep(nil), document.Construction.Steps...)}
	wrongBoundary.Construction.Steps[1].Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "filter_status"}}
	if err := wrongBoundary.Validate(); err == nil {
		t.Fatal("post-cohort operation accepted the pre-cohort authored step as its input")
	}
}

func TestNormalizeConstructionOutputsUsesAnchoredCohortSchema(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns[0].Source = ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	document.Rows = RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{
		Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
		RowValues:   []ExplicitGroupRowValue{{ColumnID: "status", Policy: ConstructionRowValueAll}},
	}}
	document.Construction.Steps = []ConstructionStep{
		{
			ID: "filter_status", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "status", Operator: ConstructionFilterExists}},
			Outputs:   []StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
		},
		{
			ID: "filter_cohorts", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "group_rows"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "group_label", Operator: ConstructionFilterExists}},
			// This is the UI's visible cohort schema: group_id is an internal identity.
			Outputs: []StageColumn{
				{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
				{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
				{ID: "members", Name: "members", Label: "Members", Type: "array"},
				{ID: "status", Name: "status", Label: "Status", Type: "array"},
			},
		},
	}

	normalizeConstructionOutputOrder(&document)
	outputs := document.Construction.Steps[1].Outputs
	want := []string{"group_id", "group_label", "group_ordinal", "members", "status"}
	if len(outputs) != len(want) {
		t.Fatalf("normalized cohort filter outputs = %#v, want five virtual-stage columns", outputs)
	}
	for index, id := range want {
		if outputs[index].ID != id {
			t.Fatalf("normalized cohort filter output %d = %#v, want column %q", index, outputs[index], id)
		}
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("anchored virtual cohort schema should remain valid after normalization: %v", err)
	}
}

func TestCompoundSourceGroupingOwnsAndPrunesItsInputs(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns = nil
	group := document.Construction.Steps[0].Operation.Group
	group.Keys = []ConstructionGroupKey{{InputColumnID: "active_source", OutputColumnID: "active_group"}, {InputColumnID: "gender_source", OutputColumnID: "gender_group"}}
	document.Construction.SourceProjections = []ConstructionSourceProjection{
		{ColumnID: "active_source", OwnerStepID: "group_status", OccurrenceID: RootOccurrenceID, FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Active"},
		{ColumnID: "gender_source", OwnerStepID: "group_status", OccurrenceID: RootOccurrenceID, FieldPath: "gender", FHIRType: "code", LogicalType: "string", Label: "Gender"},
	}
	document.Construction.Steps[0].Outputs = []StageColumn{{ID: "active_group", Name: "active", Label: "Active", Type: "boolean"}, {ID: "gender_group", Name: "gender", Label: "Gender", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	removed, _, err := document.AnalyzeConstructionCandidate(Construction{Version: ConstructionVersion}, "", []string{"group_status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Construction.SourceProjections) != 0 {
		t.Fatal("removed grouping left source inputs behind")
	}
}

func TestNextTableOrderIncludesGeneratedGroupColumns(t *testing.T) {
	document := rowValueGroupedDocument()
	if got := nextTableOrder(document); got != 2 {
		t.Fatalf("new field order = %d, want 2 after the group key and count", got)
	}
	order := 7
	document.Construction.Steps[0].Outputs[1].Table = &TablePresentation{Order: &order}
	if got := nextTableOrder(document); got != 8 {
		t.Fatalf("new field order = %d, want 8 after the explicitly ordered count", got)
	}
}
