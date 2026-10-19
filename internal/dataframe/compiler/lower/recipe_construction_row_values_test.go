package lower

import (
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"strings"
	"testing"
)

func TestGroupedSourceColumnPolicies(t *testing.T) {
	for _, policy := range []recipe.ConstructionRowValuePolicy{recipe.ConstructionRowValueAll, recipe.ConstructionRowValueOne} {
		t.Run(string(policy), func(t *testing.T) {
			valueType := "string"
			if policy == recipe.ConstructionRowValueAll {
				valueType = "array"
			}
			output := recipe.Output{Name: "grouped", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
				{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
				{Name: "status", ColumnID: "status", Expr: recipe.Expression{Select: "root.active"}},
			}, Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender", Name: "gender"}, {ID: "status", Name: "status"}}, Steps: []recipe.ConstructionStep{{
				ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{ConstructionID: "group", Keys: []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}}, Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}}, MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup}},
				Outputs:   []recipe.StageColumn{{ID: "status_group", Name: "active"}, {ID: "records", Name: "records"}, {ID: "gender_values", Name: "genders", Type: valueType}},
				RowValues: []recipe.ConstructionRowValue{{InputColumnID: "gender", OutputColumnID: "gender_values", Policy: policy}},
			}}}}
			compiled := compileDerivedTestOutput(t, output)
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rendered.Query, "SORTED_UNIQUE") || !strings.Contains(rendered.Query, "COLLECT") {
				t.Fatal("missing contributor aggregation")
			}
			one := strings.Contains(rendered.Query, "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES")
			if one != (policy == recipe.ConstructionRowValueOne) {
				t.Fatal("multiplicity policy not enforced")
			}
			if compiled.OutputSchema[2].Name != "genders" {
				t.Fatalf("source column not retained: %#v", compiled.OutputSchema)
			}
		})
	}
}

func TestCodedShapesCarryContributingSourceValues(t *testing.T) {
	for _, output := range []recipe.Output{codedGroupOutput("Observation", "component[].code.coding[]", recipe.ConstructionGroupMissingKeyGroup), codedPivotOutput()} {
		t.Run(output.Name, func(t *testing.T) {
			step := &output.Construction.Steps[0]
			step.RowValues = []recipe.ConstructionRowValue{{InputColumnID: "resource_id", OutputColumnID: "contributor_ids", Policy: recipe.ConstructionRowValueAll}}
			step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "contributor_ids", Name: "source_ids", Type: "array"})
			compiled := compileDerivedTestOutput(t, output)
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rendered.Query, "SORTED_UNIQUE") {
				t.Fatal("source values not aggregated")
			}
			found := false
			for _, column := range compiled.OutputSchema {
				if column.Name == "source_ids" {
					found = true
				}
			}
			if !found {
				t.Fatal("source column not retained")
			}
		})
	}
}

func TestContributingObjectsKeepTheirType(t *testing.T) {
	values, columns, err := lowerConstructionRowValues([]recipe.ConstructionRowValue{{InputColumnID: "source", OutputColumnID: "values", Policy: recipe.ConstructionRowValueAll}}, map[string]CompiledOutputColumn{"source": {ID: "source", Name: "source", Kind: "object", Cardinality: "many"}}, map[string]recipe.StageColumn{"values": {ID: "values", Name: "values", Type: "array"}}, "group", map[string]bool{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].InputKind != "OBJECT" || !values[0].InputMany || columns[0].Kind != "object" || columns[0].Cardinality != "many" {
		t.Fatal("object array type lost")
	}
}

func TestPivotCarriesContributingSourceValues(t *testing.T) {
	output := constructionTestOutput()
	output.Construction.Steps = output.Construction.Steps[:1]
	step := &output.Construction.Steps[0]
	step.RowValues = []recipe.ConstructionRowValue{{InputColumnID: "amount_id", OutputColumnID: "source_values", Policy: recipe.ConstructionRowValueAll}}
	step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "source_values", Name: "source_values", Type: "array"})
	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "SORTED_UNIQUE") {
		t.Fatal("source values not aggregated")
	}
	if compiled.OutputSchema[3].Name != "source_values" || compiled.OutputSchema[3].Cardinality != "many" {
		t.Fatal("source column missing from Pivot schema")
	}
}
