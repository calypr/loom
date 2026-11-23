package construction

import (
	"encoding/json"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestPivotLabelOnlyEditRejectsChangedInputAncestry(t *testing.T) {
	base := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "pivot_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
			CategoryColumnID: "category_id", ValueColumnID: "value_id",
			Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}}}
	prefix := constructionPivotLabelFilterStep()
	prefix.ID = "upstream"
	prefix.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}
	base.Steps = append([]ws.ConstructionStep{prefix}, base.Steps...)
	base.Steps[1].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "upstream"}}
	clone := func(c ws.Construction) ws.Construction {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var result ws.Construction
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
		mutate func(*ws.Construction)
	}{
		{"upstream membership", func(c *ws.Construction) {
			c.Steps[0].Operation.Filter.Operator = ws.ConstructionFilterMissing
		}},
		{"output name", func(c *ws.Construction) { c.Steps[1].Outputs[1].Name = "different_name" }},
		{"output ID", func(c *ws.Construction) { c.Steps[1].Outputs[1].ID = "different_id" }},
		{"output type", func(c *ws.Construction) { c.Steps[1].Outputs[1].Type = "integer" }},
		{"output nullability", func(c *ws.Construction) { c.Steps[1].Outputs[1].Nullable = !c.Steps[1].Outputs[1].Nullable }},
		{"input stage", func(c *ws.Construction) {
			c.Steps[1].Inputs[0] = ws.ConstructionInputRef{Kind: ws.ConstructionInputSourceProjection}
		}},
		{"source projection", func(c *ws.Construction) {
			c.SourceProjections = append(c.SourceProjections, ws.ConstructionSourceProjection{ColumnID: "different_source"})
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

func constructionPivotLabelFilterStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID: "keep_positive", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot_status"}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: "group_id", Operator: ws.ConstructionFilterExists,
		}},
		Outputs: []ws.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}
}
