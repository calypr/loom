package compiler

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	renderpostgres "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
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
	query, err := CompileTableShapeExclusions(ArangoTarget(), compiledOutput, 7, 12, ir.DefaultPhysicalOptimizationPolicy())
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
	_, err := CompileTableShapeExclusions(ArangoTarget(), unsupported, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
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
	_, err = CompileTableShapeExclusions(ArangoTarget(), noPolicy, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionNoPolicy)

	_, err = CompileTableShapeExclusions(ArangoTarget(), noPolicy, -1, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionInvalidOffset)
	_, err = CompileTableShapeExclusions(ArangoTarget(), noPolicy, 0, MaxTableShapeExclusionLimit+1, ir.DefaultPhysicalOptimizationPolicy())
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

func exclusionPostgreSQLTarget(t *testing.T) Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func exclusionCanonicalPGOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	output := reshapeOracleOutput("Observations", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "exclusion-pg", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestCompileTableShapeExclusionsPostgreSQLTargetRendersTheTerminal(t *testing.T) {
	compiled := exclusionCanonicalPGOutput(t)
	originalPlan := ir.ClonePhysicalPlan(compiled.Plan)
	query, err := CompileTableShapeExclusions(exclusionPostgreSQLTarget(t), compiled, 7, 12, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("postgresql exclusion compile: %v", err)
	}
	if !reflect.DeepEqual(compiled.Plan, originalPlan) {
		t.Fatal("postgresql exclusion compilation mutated the canonical published plan")
	}
	if query.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("selected PostgreSQL target produced %q", query.Command.Backend())
	}
	if _, _, err := query.Command.AQLParameters(); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("the AQL adapter must refuse the PostgreSQL evidence command: %v", err)
	}
	sql, args, err := query.Command.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("postgresql parameters: %v", err)
	}
	for _, want := range []string{
		"WITH px_src AS", `COLLATE "C" ASC`, "NOT COALESCE(", "OFFSET $", " LIMIT $",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("evidence statement missing %q:\n%s", want, sql)
		}
	}
	if len(args) < 2 {
		t.Fatalf("statement carries only %d parameters", len(args))
	}
	if offset, ok := args[len(args)-2].Value.(int64); !ok || offset != 7 {
		t.Fatalf("offset parameter = %#v, want int64 7", args[len(args)-2])
	}
	if fetch, ok := args[len(args)-1].Value.(int64); !ok || fetch != 13 {
		t.Fatalf("fetch-limit parameter = %#v, want int64 13", args[len(args)-1])
	}
	aqlQuery, err := CompileTableShapeExclusions(ArangoTarget(), compiled, 7, 12, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("aql control compile: %v", err)
	}
	aqlText, _, err := aqlQuery.Command.AQLParameters()
	if err != nil {
		t.Fatalf("aql control parameters: %v", err)
	}
	if aqlQuery.Command.Backend() != command.CompiledBackendAQL || !strings.Contains(aqlText, "FILTER NOT (") ||
		!strings.Contains(aqlText, "LIMIT @table_shape_exclusion_offset, @table_shape_exclusion_fetch_limit") {
		t.Fatalf("aql control drifted from canonical AQL rendering:\n%s", aqlText)
	}
}

func TestCompileTableShapeExclusionsPostgreSQLTargetRefusalsStayTyped(t *testing.T) {
	unsupported := lowerOutputForExclusionTest(t, recipe.Output{
		Name: "UnpivotPG", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		TableReshape: &recipe.TableReshape{Kind: recipe.TableReshapeUnpivot, Unpivot: &recipe.Unpivot{
			ConstructionID: "u", Inputs: []recipe.UnpivotInput{{Column: "id", Key: reshapeOracleString("id")}},
			KeyOutput: "key", KeyLabel: "Key", ValueOutput: "value", ValueLabel: "Value", NullRowPolicy: recipe.UnpivotNullPreserve,
		}},
	})
	_, err := CompileTableShapeExclusions(exclusionPostgreSQLTarget(t), unsupported, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionUnsupported)

	filtered := reshapeOracleOutput("ObservationsFiltered", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "exclusion-pg-filter", GroupKeys: []string{"group_text"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	filterStatus := "final"
	filtered.Filters = append(filtered.Filters, recipe.Filter{
		Select: "root.status", Operator: recipe.FilterEquals,
		Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterStatus}},
	})
	compiled, _, err := compileReshapeOracle(filtered, "project", "generation", 10)
	if err != nil {
		t.Fatalf("compile filtered output: %v", err)
	}
	aqlQuery, aqlErr := CompileTableShapeExclusions(ArangoTarget(), compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if aqlErr != nil || aqlQuery.Command.Backend() != command.CompiledBackendAQL {
		t.Fatalf("the filtered shape is registered at AQL and must render: %v", aqlErr)
	}
	_, err = CompileTableShapeExclusions(exclusionPostgreSQLTarget(t), compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !errors.Is(err, renderpostgres.ErrUnsupportedPostgresPlan) {
		t.Fatalf("outside-slice shape must fail closed with the typed renderer error, got %v", err)
	}
}
