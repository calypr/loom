package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func currentOracleSource(name string) recipe.Output {
	names := []string{"source_id", "group_text", "group_number", "string_category", "numeric_category", "numeric_value", "flag_value", "text_value", "unrelated"}
	selectors := []string{"root.id", "root.status", "root.valueInteger", "root.code.text", "root.valueInteger", "root.valueQuantity.value", "root.valueBoolean", "root.valueString", "root.issued"}
	output := recipe.Output{Name: name, RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact, Construction: &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{}}}
	for i, name := range names {
		output.Fields = append(output.Fields, recipe.Field{Name: name, ColumnID: name, Expr: recipe.Expression{Select: selectors[i]}})
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, recipe.StageColumn{ID: name, Name: name})
	}
	return output
}

func currentOraclePivotOutput(policy recipe.PivotUnlistedCategoryPolicy) recipe.Output {
	output := currentOracleSource("current_s04_pivot")
	alpha, beta, zero, empty := "alpha", "beta", "zero", ""
	output.Construction.Steps = []recipe.ConstructionStep{{ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
		ConstructionID: "s04-pivot-sum", GroupKeyIDs: []string{"group_text", "group_number"}, CategoryColumnID: "string_category", ValueColumnID: "numeric_value",
		Categories:      []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}, OutputColumnID: "beta"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &zero}, OutputColumnID: "zero"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &empty}, OutputColumnID: "empty_category"}},
		DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: policy,
	}}, Outputs: []recipe.StageColumn{{ID: "group_text", Name: "group_text"}, {ID: "group_number", Name: "group_number"}, {ID: "alpha", Name: "alpha"}, {ID: "beta", Name: "beta"}, {ID: "zero", Name: "zero"}, {ID: "empty_category", Name: "empty_category"}}}}
	return output
}

func currentOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy) recipe.Output {
	output := currentOracleSource("current_s04_unpivot")
	integerKey, decimalKey := "z-integer", "a-decimal"
	columns := []recipe.StageColumn{}
	for _, column := range output.Construction.SourceColumns {
		if column.ID != "group_number" && column.ID != "numeric_value" {
			columns = append(columns, column)
		}
	}
	columns = append(columns, recipe.StageColumn{ID: "measure", Name: "measure", Label: "Measure"}, recipe.StageColumn{ID: "amount", Name: "amount", Label: "Amount"})
	output.Construction.Steps = []recipe.ConstructionStep{{ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
		ConstructionID: "s04-unpivot", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "group_number", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &integerKey}}, {ColumnID: "numeric_value", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &decimalKey}}}, KeyOutputColumnID: "measure", ValueOutputColumnID: "amount", NullRowPolicy: policy,
	}}, Outputs: columns}}
	return output
}

func TestCurrentConstructionOracleFixturesCompileWithoutLegacyReshape(t *testing.T) {
	for _, test := range []struct {
		name   string
		output recipe.Output
		kind   ir.PhysicalStageOperationKind
	}{
		{"pivot", currentOraclePivotOutput(recipe.PivotUnlistedCategoryError), ir.PhysicalStagePivotOp},
		{"unpivot_preserve", currentOracleUnpivotOutput(recipe.UnpivotNullPreserve), ir.PhysicalStageUnpivotOp},
		{"unpivot_drop", currentOracleUnpivotOutput(recipe.UnpivotNullDrop), ir.PhysicalStageUnpivotOp},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, query, err := compileCurrentConstructionOracle(test.output, "project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 1 || compiled.Plan.StageSequence.Stages[0].Kind != test.kind {
				t.Fatalf("fixture did not use one current typed construction stage: %#v", compiled.Plan.StageSequence)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := ir.ValidateGenericPhysicalPlanScope(compiled.Plan); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(query.Query, "@project") || query.BindVars["project"] != "project" {
				t.Fatal("compiled fixture lost project scope")
			}
			if currentOracleOutputColumnKind(compiled.OutputSchema, "amount") != "decimal" && test.kind == ir.PhysicalStageUnpivotOp {
				t.Fatalf("INTEGER+DECIMAL widening changed: %#v", compiled.OutputSchema)
			}
			t.Logf("stage=%s outputColumns=%v", test.kind, compiled.OutputSchema)
		})
	}
}

func TestCurrentConstructionSentinelPivotFixtureCompiles(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	step := &output.Construction.Steps[0]
	step.Operation.Pivot.Categories = append(step.Operation.Pivot.Categories, recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "recorded_null"}, recipe.ConstructionPivotCategory{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing"})
	step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "recorded_null", Name: "recorded_null"}, recipe.StageColumn{ID: "missing", Name: "missing"})
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || (pivot.CategoryPresence == nil && !pivot.CategoryPresenceFromInput) || pivot.CategoryPresenceColumn == "" {
		t.Fatalf("NULL/MISSING pivot lacks property-presence contract: %#v", pivot)
	}
	if pivot.Categories[4].MatchKind != ir.PhysicalPivotCategoryNullMatch || pivot.Categories[5].MatchKind != ir.PhysicalPivotCategoryMissingMatch {
		t.Fatalf("sentinel matches changed: %#v", pivot.Categories)
	}
}
func TestCurrentConstructionDirectIDPivotFixtureRetainsFastPath(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	step := &output.Construction.Steps[0]
	step.Operation.Pivot.GroupKeyIDs = []string{"source_id"}
	step.Outputs = append([]recipe.StageColumn{{ID: "source_id", Name: "source_id"}}, step.Outputs[2:]...)
	compiled, query, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || !pivot.OneInputRowPerGroup {
		t.Fatalf("direct source-id Pivot lost one-input-per-group proof: %#v", pivot)
	}
	if strings.Contains(query.Query, "COLLECT") || strings.Contains(query.Query, "FOR __loom_physical_reshape_cell_") {
		t.Fatalf("direct ID Pivot retained group materialization: %s", query.Query)
	}
}

func compileCurrentConstructionOracle(output recipe.Output, project, generation string, limit int) (lower.CompiledRecipeOutput, model.CompiledQuery, error) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "current-construction-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return lower.CompiledRecipeOutput{}, model.CompiledQuery{}, err
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		return lower.CompiledRecipeOutput{}, model.CompiledQuery{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return lower.CompiledRecipeOutput{}, model.CompiledQuery{}, err
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	return compiled.Outputs[0], query, err
}
func currentOracleOutputColumnKind(columns []lower.CompiledOutputColumn, name string) any {
	for _, column := range columns {
		if column.Name == name {
			return column.Kind
		}
	}
	return nil
}
