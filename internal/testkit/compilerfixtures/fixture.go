package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func CurrentOracleSource(name string) recipe.Output {
	names := []string{"source_id", "group_text", "group_number", "string_category", "numeric_category", "numeric_value", "flag_value", "text_value", "unrelated"}
	selectors := []string{"root.id", "root.status", "root.valueInteger", "root.code.text", "root.valueInteger", "root.valueQuantity.value", "root.valueBoolean", "root.valueString", "root.issued"}
	output := recipe.Output{Name: name, RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact, Construction: &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{}}}
	for i, name := range names {
		output.Fields = append(output.Fields, recipe.Field{Name: name, ColumnID: name, Expr: recipe.Expression{Select: selectors[i]}})
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, recipe.StageColumn{ID: name, Name: name})
	}
	return output
}

func CurrentOraclePivotOutput(policy recipe.PivotUnlistedCategoryPolicy) recipe.Output {
	output := CurrentOracleSource("current_s04_pivot")
	alpha, beta, zero, empty := "alpha", "beta", "zero", ""
	output.Construction.Steps = []recipe.ConstructionStep{{ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
		ConstructionID: "s04-pivot-sum", GroupKeyIDs: []string{"group_text", "group_number"}, CategoryColumnID: "string_category", ValueColumnID: "numeric_value",
		Categories:      []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}, OutputColumnID: "beta"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &zero}, OutputColumnID: "zero"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &empty}, OutputColumnID: "empty_category"}},
		DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: policy,
	}}, Outputs: []recipe.StageColumn{{ID: "group_text", Name: "group_text"}, {ID: "group_number", Name: "group_number"}, {ID: "alpha", Name: "alpha"}, {ID: "beta", Name: "beta"}, {ID: "zero", Name: "zero"}, {ID: "empty_category", Name: "empty_category"}}}}
	return output
}

func CurrentOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy) recipe.Output {
	output := CurrentOracleSource("current_s04_unpivot")
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

func (compile OutputCompiler) CompileCurrentConstructionOracle(output recipe.Output, project, generation string, limit int) (lower.CompiledRecipeOutput, model.CompiledQuery, error) {
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
	query, err := compile(compiled.Outputs[0], bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	return compiled.Outputs[0], query, err
}
