package compilerfixtures

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ConstructionTraceRecipeOutput(name string) recipe.Output {
	two := int64(2)
	zero := int64(0)
	return recipe.Output{
		Name: name, RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "calc_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "keep_positive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive_total"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterThan,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &zero}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
			},
		},
	}
}

func AssertCategoryScanBindVarsMatchQuery(t *testing.T, query string, bindVars map[string]any) {
	t.Helper()
	want := map[string]struct{}{}
	for index := 0; index < len(query); index++ {
		if query[index] != '@' {
			continue
		}
		collection := false
		if index+1 < len(query) && query[index+1] == '@' {
			collection = true
			index++
		}
		start := index + 1
		end := start
		for end < len(query) && categoryScanIdentifierByte(query[end]) {
			end++
		}
		if end > start {
			key := query[start:end]
			if collection {
				key = "@" + key
			}
			want[key] = struct{}{}
			index = end - 1
		}
	}
	got := make(map[string]struct{}, len(bindVars))
	for key := range bindVars {
		got[key] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query bind vars = %v, query references = %v\n%s", got, want, query)
	}
}

func categoryScanIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func AssertCurrentUnpivotAuthoredSlots(t *testing.T, output lower.CompiledRecipeOutput, query string, bindings map[string]any) {
	t.Helper()
	if output.Plan.StageSequence == nil || len(output.Plan.StageSequence.Stages) != 1 || output.Plan.StageSequence.Stages[0].Unpivot == nil {
		t.Fatal("missing current Unpivot stage")
	}
	u := output.Plan.StageSequence.Stages[0].Unpivot
	if len(u.Inputs) != 2 || u.Inputs[0].Column != "group_number" || u.Inputs[1].Column != "numeric_value" || bindings[u.Inputs[0].KeyBindKey] != "z-integer" || bindings[u.Inputs[1].KeyBindKey] != "a-decimal" || u.Inputs[0].KeyKind != "STRING" || u.Inputs[1].KeyKind != "STRING" {
		t.Fatalf("Unpivot authored slot order changed: %#v, bindings=%#v", u.Inputs, bindings)
	}
	first := strings.Index(query, "{key: @"+u.Inputs[0].KeyBindKey+",")
	second := strings.Index(query, "{key: @"+u.Inputs[1].KeyBindKey+",")
	if first < 0 || second <= first {
		t.Fatalf("AQL slot enumeration lost authored [z-integer, a-decimal] order: %s", query)
	}
	if !strings.Contains(query, "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("current Unpivot lost canonical identity ordering: %s", query)
	}
}

func SortedAnyStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	sort.Strings(result)
	return result
}
