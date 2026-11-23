package construction

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestPivotLabelOnlyEditRejectsChangedInputAncestry(t *testing.T) {
	base := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "pivot_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
			ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
			CategoryColumnID: "category_id", ValueColumnID: "value_id",
			Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}}}
	prefix := constructionPivotLabelFilterStep()
	prefix.ID = "upstream"
	prefix.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}
	base.Steps = append([]authoringv2.ConstructionStep{prefix}, base.Steps...)
	base.Steps[1].Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "upstream"}}
	clone := func(c authoringv2.Construction) authoringv2.Construction {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var result authoringv2.Construction
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	candidate := clone(base)
	candidate.Steps[1].Outputs[1].Label = "Renamed"
	if !constructionPivotLabelOnlyEdit(base, candidate, "pivot_status") {
		t.Fatal("unchanged ancestry label edit refused")
	}

	for _, tc := range []struct {
		name   string
		mutate func(*authoringv2.Construction)
	}{
		{"upstream membership", func(c *authoringv2.Construction) {
			c.Steps[0].Operation.Filter.Operator = authoringv2.ConstructionFilterMissing
		}},
		{"output name", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Name = "different_name" }},
		{"output ID", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].ID = "different_id" }},
		{"output type", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Type = "integer" }},
		{"output nullability", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Nullable = !c.Steps[1].Outputs[1].Nullable }},
		{"input stage", func(c *authoringv2.Construction) {
			c.Steps[1].Inputs[0] = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
		}},
		{"source projection", func(c *authoringv2.Construction) {
			c.SourceProjections = append(c.SourceProjections, authoringv2.ConstructionSourceProjection{ColumnID: "different_source"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := clone(candidate)
			tc.mutate(&changed)
			if constructionPivotLabelOnlyEdit(base, changed, "pivot_status") {
				t.Fatal("semantic change classified as label-only")
			}
		})
	}
}

func constructionPivotLabelFilterStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID: "keep_positive", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "pivot_status"}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: "group_id", Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}
}
