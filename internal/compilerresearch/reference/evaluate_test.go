package reference

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestEvaluatePivotDeriveFilterUnpivot(t *testing.T) {
	alpha, beta := "alpha", "beta"
	threshold := 10.0
	output := recipe.Output{
		Name: "summary", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{
			{ID: "status_id", Name: "status"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
		}, Steps: []recipe.ConstructionStep{
			{ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"status_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
				Categories:      []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}, OutputColumnID: "beta_id"}},
				DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
			}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}}},
			{ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
				ConstructionID: "total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd, Left: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha_id"}, Right: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "beta_id"}, MissingInputPolicy: recipe.MissingInputPropagateNull,
			}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"}}},
			{ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "total_id", Operator: recipe.FilterGreaterEq, Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &threshold}}}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"}}},
			{ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{ConstructionID: "unpivot", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "alpha_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}}, {ColumnID: "beta_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}}}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id", NullRowPolicy: recipe.UnpivotNullPreserve}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "value_id", Name: "value"}}},
		}},
	}
	documents := []Document{
		{"status": "final", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 2.5}},
		{"status": "final", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 4.5}},
		{"status": "final", "code": map[string]any{"text": "beta"}, "valueQuantity": map[string]any{"value": 3.5}},
		{"status": "small", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 1.0}},
	}
	rows, err := Evaluate(output, documents)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want two unpivot rows after the aggregate threshold filter", rows)
	}
	want := map[string]float64{"alpha": 7, "beta": 3.5}
	for _, row := range rows {
		measure := row["measure"]
		if !measure.Present {
			t.Fatalf("measure = %#v", measure)
		}
		value := row["value"]
		if !value.Present || value.Data != want[measure.Data.(string)] {
			t.Fatalf("row = %#v, want measure value %#v", row, want[measure.Data.(string)])
		}
		if row["total"].Data != 10.5 {
			t.Fatalf("total = %#v, want 10.5", row["total"])
		}
	}
}

func TestEvaluateCodedGroupAndCodedPivotFromFHIRCodingJSON(t *testing.T) {
	documents := []Document{
		{"id": "a", "component": []any{map[string]any{"code": map[string]any{"coding": []any{map[string]any{"system": "loinc", "code": "sys"}, map[string]any{"system": "loinc", "code": "dia"}}}, "valueQuantity": map[string]any{"value": 120.0}}}},
		{"id": "b", "component": []any{map[string]any{"code": map[string]any{"coding": []any{map[string]any{"system": "loinc", "code": "sys"}}}, "valueQuantity": map[string]any{"value": 130.0}}}},
	}
	group := recipe.Output{Name: "coded_group", RootResourceType: "Observation", RowGrain: "group", Construction: &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{ConstructionID: "codes", Source: recipe.ConstructionCodedGroupSource{OccurrenceID: "root", ResourceType: "Observation", CodingPath: "component[].code.coding[]", FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY"}, MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup, SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id", CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id"}},
		Outputs:   []recipe.StageColumn{{ID: "system_id", Name: "system"}, {ID: "version_id", Name: "version"}, {ID: "code_id", Name: "code"}, {ID: "count_id", Name: "count"}},
	}}}}
	groupRows, err := Evaluate(group, documents)
	if err != nil {
		t.Fatal(err)
	}
	if len(groupRows) != 2 {
		t.Fatalf("CODED_GROUP rows = %#v", groupRows)
	}
	counts := map[any]any{}
	for _, row := range groupRows {
		counts[row["code"].Data] = row["count"].Data
	}
	if counts["sys"] != float64(2) || counts["dia"] != float64(1) {
		t.Fatalf("CODED_GROUP distinct source counts = %#v", counts)
	}

	pivot := recipe.Output{Name: "coded_pivot", RootResourceType: "Observation", RowGrain: "summary", Construction: &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedPivotOp, CodedPivot: &recipe.ConstructionCodedPivot{ConstructionID: "coded_pivot", Source: recipe.ConstructionCodedPivotSource{BindingID: "binding", ResourceType: "Observation", SourcePath: "Observation.component[].valueQuantity.value", OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: "valueQuantity.value", LogicalType: "decimal", RuleVersion: "test", SchemaVersion: 1, CandidateID: "candidate", NodeID: "component", FieldPath: "Observation.component[].valueQuantity.value"}, Categories: []recipe.ConstructionCodedPivotCategory{{System: "loinc", Code: "sys", OutputColumnID: "sys_id"}, {System: "loinc", Code: "dia", OutputColumnID: "dia_id"}}, DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull}},
		Outputs:   []recipe.StageColumn{{ID: "sys_id", Name: "sys"}, {ID: "dia_id", Name: "dia"}},
	}}}}
	pivotRows, err := Evaluate(pivot, documents)
	if err != nil {
		t.Fatal(err)
	}
	if len(pivotRows) != 1 || pivotRows[0]["sys"].Data != float64(250) || pivotRows[0]["dia"].Data != float64(120) {
		t.Fatalf("CODED_PIVOT rows = %#v", pivotRows)
	}
}
