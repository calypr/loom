package compiler

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileTableShapeExclusionsUsesCanonicalGroupedPivotInputs(t *testing.T) {
	output := reshapeOracleOutput("Observations", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "exclusion-test", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "derived-after-pivot", Name: "after", Label: "After", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	compiledOutput, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	originalPlan := ir.ClonePhysicalPlan(compiledOutput.Plan)
	query, err := CompileTableShapeExclusionsWithPolicy(compiledOutput, 7, 12, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiledOutput.Plan, originalPlan) {
		t.Fatal("exclusion compilation mutated the canonical published plan")
	}
	for _, want := range []string{
		"FILTER NOT (", "TYPENAME(", "GROUPED_PIVOT",
		"LIMIT @table_shape_exclusion_offset, @table_shape_exclusion_fetch_limit",
		"__loom_physical_exclusion_output_row_id", "@__loom_physical_exclusion_category_column",
	} {
		if !strings.Contains(testAQLText(t, query.Command), want) {
			t.Fatalf("exclusion query missing %q:\n%s", want, testAQLText(t, query.Command))
		}
	}
	if strings.Contains(testAQLText(t, query.Command), "derived-after-pivot") || strings.Contains(testAQLText(t, query.Command), "LET after =") {
		t.Fatalf("diagnostic terminal evaluated work after the grouped pivot:\n%s", testAQLText(t, query.Command))
	}
	if query.Offset != 7 || query.Limit != 12 || testAQLBinds(t, query.Command)["table_shape_exclusion_offset"] != 7 || testAQLBinds(t, query.Command)["table_shape_exclusion_fetch_limit"] != 13 {
		t.Fatalf("unexpected bounded page metadata: %#v %#v", query, testAQLBinds(t, query.Command))
	}
	if !containsBindValue(testAQLBinds(t, query.Command), ir.PhysicalCellTraceSourceDocumentField) || !containsBindValue(testAQLBinds(t, query.Command), "string_category") || !containsBindValue(testAQLBinds(t, query.Command), "known") || !containsBindValue(testAQLBinds(t, query.Command), ir.PhysicalTableShapeExclusionCategoryPresentField) || !containsBindValue(testAQLBinds(t, query.Command), ir.PhysicalTableShapeExclusionReasonUnlistedCategory) {
		t.Fatalf("query does not bind exact source document and canonical category identity: %#v", testAQLBinds(t, query.Command))
	}
	if !strings.Contains(testAQLText(t, query.Command), "HAS(") || !containsBindValue(testAQLBinds(t, query.Command), "__loom_exclusion_category_present") {
		t.Fatalf("exclusion diagnostic lost source presence for ordinary value categories: %s", testAQLText(t, query.Command))
	}
	if strings.Contains(testAQLText(t, query.Command), `"known"`) {
		t.Fatalf("category value was interpolated rather than retained as a typed canonical bind:\n%s", testAQLText(t, query.Command))
	}
}

func TestCompileTableShapeExclusionsRefusesUnsupportedOrUnconfiguredOutputs(t *testing.T) {
	unsupported := lowerOutputForExclusionTest(t, recipe.Output{
		Name: "Unpivot", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		TableReshape: &recipe.TableReshape{Kind: recipe.TableReshapeUnpivot, Unpivot: &recipe.Unpivot{
			ConstructionID: "u", Inputs: []recipe.UnpivotInput{{Column: "id", Key: reshapeOracleString("id")}},
			KeyOutput: "key", KeyLabel: "Key", ValueOutput: "value", ValueLabel: "Value", NullRowPolicy: recipe.UnpivotNullPreserve,
		}},
	})
	_, err := CompileTableShapeExclusionsWithPolicy(unsupported, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionUnsupported)

	noPolicy := lowerOutputForExclusionTest(t, recipe.Output{
		Name: "NoPolicy", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "category", Expr: recipe.Expression{Select: "root.status"}}, {Name: "value", Expr: recipe.Expression{Select: "root.valueInteger"}}},
		TableReshape: &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "p", GroupKeys: []string{"id"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
	})
	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionNoPolicy)

	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, -1, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionInvalidOffset)
	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, 0, MaxTableShapeExclusionLimit+1, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionInvalidLimit)
}

func lowerOutputForExclusionTest(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertExclusionRefusal(t *testing.T, err error, want TableShapeExclusionRefusalCode) {
	t.Helper()
	var refusal *TableShapeExclusionRefusal
	if !errors.As(err, &refusal) || refusal.Code != want {
		t.Fatalf("refusal = %#v, want %s", err, want)
	}
}
