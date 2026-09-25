package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionExpandThenGroupUsesTypedIntermediateColumns(t *testing.T) {
	output := recipe.Output{
		Name: "construction_group_expand", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
						ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
						OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
					}},
					Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
				},
				{
					ID: "group_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group_tag_values",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
						Aggregates: []recipe.ConstructionGroupAggregate{
							{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
							{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
							{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
						{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
					},
				},
			},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 2 {
		t.Fatalf("construction stages = %#v, want EXPAND followed by GROUP", sequence)
	}
	if sequence.Stages[0].Kind != ir.PhysicalStageExpandOp || sequence.Stages[1].Kind != ir.PhysicalStageGroupOp {
		t.Fatalf("stage kinds = %q, %q, want EXPAND then GROUP", sequence.Stages[0].Kind, sequence.Stages[1].Kind)
	}
	if sequence.Stages[1].InputStageID != sequence.Stages[0].ID {
		t.Fatalf("group input stage = %q, want prior expand stage %q", sequence.Stages[1].InputStageID, sequence.Stages[0].ID)
	}
	if sequence.Stages[0].Expand.EmptyPolicy != ir.PhysicalUnnestPreserveParent || sequence.Stages[0].Expand.OrdinalColumn != "ordinal" {
		t.Fatalf("expand contract = %#v, want preserve-parent with public ordinal", sequence.Stages[0].Expand)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"tag", "rows", "status_count", "status_distinct", "__loom_row_id"}; !equalStringSlices(got, want) {
		t.Fatalf("final schema = %#v, want %#v", got, want)
	}
	if compiled.OutputSchema[1].Kind != string(expression.KindInteger) || compiled.OutputSchema[2].Kind != string(expression.KindInteger) || compiled.OutputSchema[3].Kind != string(expression.KindInteger) {
		t.Fatalf("group count output types = %#v, want integers", compiled.OutputSchema[1:4])
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"CONSTRUCTION_EXPAND_ARRAY_TYPE_MISMATCH", "CONSTRUCTION_EXPAND_ITEM_TYPE_MISMATCH", "RANGE(0,", "[\"ordinal\",", "COLLECT ",
		"CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH", "SORTED_UNIQUE(", "__loom_construction_stage_1",
	} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("AQL stage sequence is missing %q:\n%s", expected, rendered.Query)
		}
	}
	if _, ok := rendered.BindVars[sequence.Stages[0].Expand.ConstructionIDBindKey]; !ok {
		t.Fatal("expand construction identity was not bound")
	}
}

func TestConstructionGroupAllowsZeroKeysAndExplicitCountSemantics(t *testing.T) {
	output := recipe.Output{
		Name: "construction_summary", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "whole_table_summary",
					Aggregates: []recipe.ConstructionGroupAggregate{
						{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: recipe.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: recipe.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "rows_id", Name: "rows"}, {ID: "status_count_id", Name: "status_count"},
					{ID: "status_distinct_id", Name: "status_distinct"}, {ID: "amount_sum_id", Name: "amount_sum"},
					{ID: "amount_mean_id", Name: "amount_mean"},
				},
			}},
		},
	}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || len(stage.Group.Keys) != 0 {
		t.Fatalf("group stage = %#v, want zero-key table summary", stage)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"rows", "status_count", "status_distinct", "amount_sum", "amount_mean", "__loom_row_id"}; !equalStringSlices(got, want) {
		t.Fatalf("summary schema = %#v, want %#v", got, want)
	}
	if compiled.OutputSchema[3].Kind != string(expression.KindDecimal) && compiled.OutputSchema[3].Kind != string(expression.KindInteger) {
		t.Fatalf("sum output type = %q, want numeric", compiled.OutputSchema[3].Kind)
	}
	if compiled.OutputSchema[4].Kind != string(expression.KindDecimal) {
		t.Fatalf("mean output type = %q, want decimal", compiled.OutputSchema[4].Kind)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "== 0 ? [null] :") {
		t.Fatalf("zero-key summary does not synthesize an empty group:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, " = null INTO ") {
		t.Fatalf("zero-key summary does not COLLECT into one constant group:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "COUNT") && !strings.Contains(rendered.Query, "LENGTH(") {
		t.Fatalf("summary AQL has no explicit row count:\n%s", rendered.Query)
	}
}
