package authoringv2

import (
	"encoding/json"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/columntransform"
)

func rowValueGroupedDocument() ws.Document {
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{constructionSourceColumn("status", "status", "Status", "string")}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "group_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group_status", Keys: []ws.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "records"}},
		}}, Outputs: []ws.StageColumn{{ID: "status_group", Name: "status_group", Label: "Status", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}},
	}}}
	return document
}

func TestPopulateGroupedRowsPreservesIdentityReloadAndRemoval(t *testing.T) {
	document := rowValueGroupedDocument()
	keys := append([]ws.ConstructionGroupKey(nil), document.Construction.Steps[0].Operation.Group.Keys...)
	column := constructionSourceColumn("gender_source", "gender", "Gender", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueAll); err != nil {
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
	reloaded, err := ws.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Documents[0].Construction.Steps[0].RowValues, step.RowValues) {
		t.Fatal("reload lost contributing-record value policy")
	}
	removed, _, err := ApplyCommands(reloaded, commandCatalog(), "remove-gender", []Command{{Type: CommandRemoveColumn, OutputID: "patients", Payload: &ColumnPayload{Column: "gender"}}})
	if err != nil {
		t.Fatal(err)
	}
	remaining := removed.Documents[0].Construction.Steps[0]
	if len(remaining.RowValues) != 0 || len(remaining.Outputs) != 2 || !reflect.DeepEqual(remaining.Operation.Group.Keys, keys) {
		t.Fatal("removing a column changed row grouping")
	}
}

func TestRemoveProjectedFieldUsesStableIDAndExistingDownstreamCleanup(t *testing.T) {
	document := rowValueGroupedDocument()
	column := constructionSourceColumn("gender_source", "gender", "Gender", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueAll); err != nil {
		t.Fatal(err)
	}
	workspace := constructionWorkspace(document)
	removed, _, err := ApplyCommands(workspace, commandCatalog(), "remove-projected-gender", []Command{{Type: CommandRemoveProjectedField, OutputID: "patients", Payload: &ColumnPayload{ColumnID: "gender_source"}}})
	if err != nil {
		t.Fatal(err)
	}
	result := removed.Documents[0]
	if len(result.Columns) != 1 || result.Columns[0].ColumnID != "status" {
		t.Fatalf("stable-ID removal left the wrong source columns: %#v", result.Columns)
	}
	if len(result.Construction.Steps[0].RowValues) != 0 || len(result.Construction.Steps[0].Outputs) != 2 {
		t.Fatalf("removal did not clean dependent row-value outputs: %#v", result.Construction.Steps[0])
	}
	if result.Construction.Steps[0].Operation.Group.Keys[0].InputColumnID != "status" {
		t.Fatal("removing a projected field changed the unrelated grouping key")
	}
}

func TestRemoveProjectedFieldRejectsClientOwnershipClaims(t *testing.T) {
	var command Command
	if err := json.Unmarshal([]byte(`{"type":"REMOVE_PROJECTED_FIELD","outputId":"patients","columnId":"column-a","owner":"PINNED_SOURCE"}`), &command); err == nil {
		t.Fatal("accepted client-supplied projected field ownership")
	}
}

func TestPopulatePivotRowsCarriesValuesThroughFollowingFilter(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps = document.Construction.Steps[:3]
	column := constructionSourceColumn("extra", "extra", "Extra", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueOne); err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	rowValue := document.Construction.Steps[0].RowValues[0]
	if rowValue.Policy != ws.ConstructionRowValueOne {
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
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
	}}
	filterSource := ws.ConstructionStep{
		ID: "filter_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "status", Operator: ws.ConstructionFilterExists}},
		Outputs:   []ws.StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
	}
	filterCohorts := ws.ConstructionStep{
		ID: "filter_cohorts", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "group_id", Operator: ws.ConstructionFilterExists}},
		Outputs: []ws.StageColumn{
			{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
			{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
			{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
			{ID: "members", Name: "members", Label: "Members", Type: "array"},
		},
	}
	document.Construction.Steps = []ws.ConstructionStep{filterSource, filterCohorts}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source → cohort → authored filter sequence: %v", err)
	}
	removed, _, err := document.ProposeStepRemoval("filter_status", nil)
	if err != nil {
		t.Fatalf("remove the persisted cohort anchor: %v", err)
	}
	if removed.Rows.Groups.AfterStepID != "" || len(removed.Construction.Steps) != 1 ||
		removed.Construction.Steps[0].Inputs[0] != (ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}) {
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
	withoutCohortSuffix.Rows = ws.RecordsRowDefinition()
	if err := withoutCohortSuffix.Validate(); err != nil {
		t.Fatalf("return to records after removing the virtual-stage dependent: %v", err)
	}
	if withoutCohortSuffix.Construction.Steps[0].Inputs[0].StepID == "group_rows" {
		t.Fatal("returning to records left a virtual group_rows reference")
	}

	wrongBoundary := document
	wrongBoundary.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: append([]ws.ConstructionStep(nil), document.Construction.Steps...)}
	wrongBoundary.Construction.Steps[1].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter_status"}}
	if err := wrongBoundary.Validate(); err == nil {
		t.Fatal("post-cohort operation accepted the pre-cohort authored step as its input")
	}
}

func TestNormalizeConstructionOutputsUsesAnchoredCohortSchema(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
		RowValues:   []ws.ExplicitGroupRowValue{{ColumnID: "status", Policy: ws.ConstructionRowValueAll}},
	}}
	document.Construction.Steps = []ws.ConstructionStep{
		{
			ID: "filter_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "status", Operator: ws.ConstructionFilterExists}},
			Outputs:   []ws.StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
		},
		{
			ID: "filter_cohorts", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "group_label", Operator: ws.ConstructionFilterExists}},

			Outputs: []ws.StageColumn{
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

func TestExplicitGroupStageAcceptsExactRecodedRootStringRowValue(t *testing.T) {
	transformation := columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings:      []columntransform.CategoryMapping{{From: "recorded", To: "shared"}},
			UnknownPolicy: columntransform.UnknownKeepOriginal,
		},
	}
	column := constructionSourceColumn("status-id", "status", "Status", "string")
	column.Source = ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	column.ValueTransformation = &transformation
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{column}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		RowValues: []ws.ExplicitGroupRowValue{{ColumnID: column.ColumnID, Policy: ws.ConstructionRowValueOne}},
	}}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}
	if err := document.Validate(); err != nil {
		t.Fatalf("validate transformed exact-category group row value: %v", err)
	}
}

func TestCompoundSourceGroupingOwnsAndPrunesItsInputs(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns = nil
	group := document.Construction.Steps[0].Operation.Group
	group.Keys = []ws.ConstructionGroupKey{{InputColumnID: "active_source", OutputColumnID: "active_group"}, {InputColumnID: "gender_source", OutputColumnID: "gender_group"}}
	document.Construction.SourceProjections = []ws.ConstructionSourceProjection{
		{ColumnID: "active_source", OwnerStepID: "group_status", OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Active"},
		{ColumnID: "gender_source", OwnerStepID: "group_status", OccurrenceID: ws.RootOccurrenceID, FieldPath: "gender", FHIRType: "code", LogicalType: "string", Label: "Gender"},
	}
	document.Construction.Steps[0].Outputs = []ws.StageColumn{{ID: "active_group", Name: "active", Label: "Active", Type: "boolean"}, {ID: "gender_group", Name: "gender", Label: "Gender", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	removed, _, err := document.AnalyzeConstructionCandidate(ws.Construction{Version: ws.ConstructionVersion}, "", []string{"group_status"})
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
	document.Construction.Steps[0].Outputs[1].Table = &ws.TablePresentation{Order: &order}
	if got := nextTableOrder(document); got != 8 {
		t.Fatalf("new field order = %d, want 8 after the explicitly ordered count", got)
	}
}
