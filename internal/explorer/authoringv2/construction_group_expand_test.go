package authoringv2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func groupExpandDocument() Document {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("status_id", "status", "Status", "string"),
		constructionSourceColumn("amount_id", "amount", "Amount", "decimal"),
		constructionSourceColumn("tags_id", "tags", "Tags", "array"),
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID: "expand_tags", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationExpand, Expand: &ConstructionExpand{
				ConstructionID: "expand_tags", InputColumnID: "tags_id", OutputColumnID: "tag_value_id",
				OrdinalColumnID: "tag_position_id", EmptyPolicy: ConstructionExpandEmptyPreserveParent,
			}},
			Outputs: []StageColumn{
				{ID: "person_id", Name: "person_id", Label: "Person ID", Type: "integer"},
				{ID: "status_id", Name: "status", Label: "Status", Type: "string"},
				{ID: "amount_id", Name: "amount", Label: "Amount", Type: "decimal"},
				{ID: "tag_value_id", Name: "tag_value", Label: "Tag value", Type: "string"},
				{ID: "tag_position_id", Name: "tag_position", Label: "Tag position", Type: "integer"},
			},
		},
		{
			ID: "group_tags", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "expand_tags"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
				ConstructionID:   "group_tags",
				MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
				Keys:             []ConstructionGroupKey{{InputColumnID: "tag_value_id", OutputColumnID: "grouped_tag_id"}},
				Aggregates: []ConstructionGroupAggregate{
					{Operation: ConstructionGroupCountRows, OutputColumnID: "row_count_id"},
					{Operation: ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
					{Operation: ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
					{Operation: ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
					{Operation: ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
				},
			}},
			Outputs: []StageColumn{
				{ID: "grouped_tag_id", Name: "tag", Label: "Tag", Type: "string"},
				{ID: "row_count_id", Name: "row_count", Label: "Row count", Type: "integer"},
				{ID: "status_count_id", Name: "status_count", Label: "Status count", Type: "integer"},
				{ID: "status_distinct_id", Name: "status_distinct", Label: "Distinct statuses", Type: "integer"},
				{ID: "amount_sum_id", Name: "amount_sum", Label: "Amount sum", Type: "decimal"},
				{ID: "amount_mean_id", Name: "amount_mean", Label: "Amount mean", Type: "decimal"},
			},
		},
	}}
	return document
}

func TestConstructionGroupMissingKeyPolicyDefaultsAndRoundTrips(t *testing.T) {
	var legacy ConstructionGroup
	if err := json.Unmarshal([]byte(`{"constructionId":"group","keys":[],"aggregates":[]}`), &legacy); err != nil {
		t.Fatalf("decode legacy group without missingKeyPolicy: %v", err)
	}
	if legacy.MissingKeyPolicy != ConstructionGroupMissingKeyGroup {
		t.Fatalf("legacy missingKeyPolicy = %q, want GROUP", legacy.MissingKeyPolicy)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal normalized legacy group: %v", err)
	}
	if !strings.Contains(string(encoded), `"missingKeyPolicy":"GROUP"`) {
		t.Fatalf("normalized group did not save explicit GROUP policy: %s", encoded)
	}

	for _, policy := range []ConstructionGroupMissingKeyPolicy{
		ConstructionGroupMissingKeyGroup,
		ConstructionGroupMissingKeyExclude,
		ConstructionGroupMissingKeyError,
	} {
		document := groupExpandDocument()
		document.Construction.Steps[1].Operation.Group.MissingKeyPolicy = policy
		if err := document.Validate(); err != nil {
			t.Fatalf("validate %s group policy: %v", policy, err)
		}
		encoded, err := constructionWorkspace(document).CanonicalJSON()
		if err != nil {
			t.Fatalf("canonicalize %s group policy: %v", policy, err)
		}
		decoded, err := DecodeWorkspace(encoded)
		if err != nil {
			t.Fatalf("reload %s group policy: %v", policy, err)
		}
		got := decoded.Documents[0].Construction.Steps[1].Operation.Group.MissingKeyPolicy
		if got != policy {
			t.Fatalf("reloaded missingKeyPolicy = %q, want %q", got, policy)
		}
	}

	document := groupExpandDocument()
	document.Construction.Steps[1].Operation.Group.MissingKeyPolicy = "SILENT_DEFAULT"
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "missingKeyPolicy") {
		t.Fatalf("unsupported missingKeyPolicy error = %v", err)
	}
}

func TestConstructionValidatesAndRoundTripsGroupAndExpand(t *testing.T) {
	document := groupExpandDocument()
	if err := document.Validate(); err != nil {
		t.Fatalf("valid GROUP/EXPAND construction rejected: %v", err)
	}

	workspace := constructionWorkspace(document)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode canonical workspace: %v", err)
	}
	want := workspace.NormalizePresentationOrders().Documents[0].Construction
	if got := decoded.Documents[0].Construction; !reflect.DeepEqual(got, want) {
		t.Fatalf("construction changed during save/reload:\n got: %#v\nwant: %#v", got, document.Construction)
	}
}

func TestConstructionGroupSupportsZeroKeysAndSummaryOperations(t *testing.T) {
	document := groupExpandDocument()
	step := document.Construction.Steps[1]
	step.ID = "whole_table_summary"
	step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
	step.Operation.Group.ConstructionID = step.ID
	step.Operation.Group.Keys = nil
	step.Operation.Group.Aggregates = []ConstructionGroupAggregate{
		{Operation: ConstructionGroupCountRows, OutputColumnID: "row_count_id"},
		{Operation: ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
		{Operation: ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
		{Operation: ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
		{Operation: ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
	}
	step.Outputs = []StageColumn{
		{ID: "row_count_id", Name: "row_count", Label: "Row count", Type: "integer"},
		{ID: "status_count_id", Name: "status_count", Label: "Status count", Type: "integer"},
		{ID: "status_distinct_id", Name: "status_distinct", Label: "Distinct statuses", Type: "integer"},
		{ID: "amount_sum_id", Name: "amount_sum", Label: "Amount sum", Type: "decimal"},
		{ID: "amount_mean_id", Name: "amount_mean", Label: "Amount mean", Type: "decimal"},
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{step}}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid zero-key summary rejected: %v", err)
	}
}

func TestConstructionGroupExpandEditsAndRemovalRepairDependencies(t *testing.T) {
	accepted := groupExpandDocument()
	construction, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatalf("clone accepted construction: %v", err)
	}
	replacement := construction.Steps[0]
	replacement.Operation.Expand.OutputColumnID = "tag_value_v2_id"
	replacement.Outputs[3].ID = "tag_value_v2_id"
	proposal, impact, err := accepted.AnalyzeStepEdit(replacement)
	if err != nil {
		t.Fatalf("analyze expand edit: %v", err)
	}
	if len(impact.MissingInputs) != 1 || impact.MissingInputs[0] != (ConstructionDependencyIssue{StepID: "group_tags", ColumnID: "tag_value_id"}) {
		t.Fatalf("edit dependency repair = %#v, want one missing GROUP key", impact.MissingInputs)
	}
	if accepted.Construction.Steps[0].Operation.Expand.OutputColumnID != "tag_value_id" {
		t.Fatal("proposed expand edit mutated the accepted construction")
	}
	if proposal.Construction.Steps[0].Outputs[3].ID != "tag_value_v2_id" {
		t.Fatal("proposed stage schema did not retain the new stable item ID")
	}

	removed, removalImpact, err := accepted.ProposeStepRemoval("expand_tags", nil)
	if err != nil {
		t.Fatalf("propose expand removal: %v", err)
	}
	if len(removed.Construction.Steps) != 1 || removed.Construction.Steps[0].Inputs[0].Kind != ConstructionInputSourceProjection {
		t.Fatalf("surviving GROUP stage was not rewired to source: %#v", removed.Construction.Steps)
	}
	if len(removalImpact.MissingInputs) != 1 || removalImpact.MissingInputs[0] != (ConstructionDependencyIssue{StepID: "group_tags", ColumnID: "tag_value_id"}) {
		t.Fatalf("removal dependency repair = %#v, want one missing GROUP key", removalImpact.MissingInputs)
	}
}
