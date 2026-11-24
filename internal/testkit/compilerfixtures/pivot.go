package compilerfixtures

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/testkit/recipefixtures"

	store "github.com/calypr/loom/internal/store/arango"
)

type ReshapeOracleGroupTuple struct {
	Text    recipe.TableScalar
	Numeric recipe.TableScalar
}

type ReshapeOracleSourceRow struct {
	SourceID        string
	Group           ReshapeOracleGroupTuple
	StringCategory  recipe.TableScalar
	NumericCategory recipe.TableScalar
	Number          *float64
	Flag            bool
	Text            string
	Unrelated       string
}

var ReshapeOracleSourceRows = []ReshapeOracleSourceRow{
	{
		SourceID: "alpha-a", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("alpha"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(2.5),
		Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a",
	},
	{
		SourceID: "alpha-b", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("alpha"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(4.5),
		Flag: true, Text: "alpha-b", Unrelated: "keep-alpha-b",
	},
	{
		SourceID: "beta", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("beta"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(3.5),
		Flag: false, Text: "beta", Unrelated: "keep-beta",
	},
	{
		SourceID: "zero", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("zero"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(0),
		Flag: false, Text: "zero", Unrelated: "keep-zero",
	},
	{
		SourceID: "unlisted", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("unlisted"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(9),
		Flag: true, Text: "unlisted", Unrelated: "keep-unlisted",
	},
	{
		SourceID: "final-two", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("final"), Numeric: ReshapeOracleInteger(2)},
		StringCategory: ReshapeOracleString("alpha"), NumericCategory: ReshapeOracleInteger(2), Number: ReshapeOracleFloat(12),
		Flag: true, Text: "final-two", Unrelated: "keep-final-two",
	},
	{
		SourceID: "preliminary-one", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("preliminary"), Numeric: ReshapeOracleInteger(1)},
		StringCategory: ReshapeOracleString("alpha"), NumericCategory: ReshapeOracleInteger(1), Number: ReshapeOracleFloat(8),
		Flag: false, Text: "preliminary-one", Unrelated: "keep-preliminary-one",
	},
	{
		SourceID: "empty-and-null", Group: ReshapeOracleGroupTuple{Text: ReshapeOracleString("preliminary"), Numeric: ReshapeOracleInteger(2)},
		StringCategory: ReshapeOracleString(""), NumericCategory: ReshapeOracleInteger(2), Number: nil,
		Flag: false, Text: "", Unrelated: "keep-empty-and-null",
	},
}

func ReshapeOracleFloat(value float64) *float64 { return &value }

func ReshapeOracleString(value string) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarString, String: &value}
}

func ReshapeOracleInteger(value int64) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &value}
}

func ReshapeOracleOutput(name string, reshape *recipefixtures.OracleReshape) recipe.Output {
	output := recipe.Output{
		Name: name, RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "source_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "group_text", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "group_number", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "string_category", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "numeric_category", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "numeric_value", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
			{Name: "flag_value", Expr: recipe.Expression{Select: "root.valueBoolean"}},
			{Name: "text_value", Expr: recipe.Expression{Select: "root.valueString"}},
			{Name: "unrelated", Expr: recipe.Expression{Select: "root.issued"}},
		},
	}
	return recipefixtures.BindOracleCurrentConstruction(output, reshape)
}

func (compile OutputCompiler) CompileReshapeOracle(output recipe.Output, project, generation string, limit int) (lower.CompiledRecipeOutput, model.CompiledQuery, error) {
	output = BindOracleCurrentDerived(output)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-table-reshape-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
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
	if err != nil {
		return lower.CompiledRecipeOutput{}, model.CompiledQuery{}, err
	}
	return compiled.Outputs[0], query, nil
}

func ExecuteReshapeOracleQuery(t *testing.T, ctx context.Context, client *store.Client, query model.CompiledQuery) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute compiler-generated reshape query: %v\n%s", err, query.Query)
	}
	return rows
}
