package graphqlapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipecontrol"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type fakeRecipeControl struct {
	validation recipecontrol.Validation
	plan       semantic.ResolvedRecipePlan
}

func (f fakeRecipeControl) Validate(context.Context, string, recipe.RuntimeBindings) (recipecontrol.Validation, error) {
	return f.validation, nil
}
func (f fakeRecipeControl) Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	return f.validation.Plan.Explain(), nil
}
func (f fakeRecipeControl) Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	return f.plan, nil
}
func (f fakeRecipeControl) Preview(context.Context, string, recipe.RuntimeBindings, recipecontrol.ExecuteFunc) (recipecontrol.Preview, error) {
	return recipecontrol.Preview{Plan: f.plan, Rows: map[string][]map[string]any{"rows": {{"id": "p1"}}}}, nil
}

func testRecipeValidation() recipecontrol.Validation {
	plan := semantic.RecipePlan{Version: 1, RecipeDigest: "recipe", TranslationVersion: "legacy", Outputs: []semantic.OutputPlan{{
		Name: "rows", RootResourceType: "Patient", RowGrain: semantic.RowGrain("resource"),
		DeclaredOrder: []string{"id"}, Fields: []semantic.SemanticProjection{{Name: "id", Expr: semantic.SemanticExpression{SourcePath: "$.outputs[0].fields[0]", Type: semanticTypeString().Type}}},
	}}}
	return recipecontrol.Validation{Plan: plan}
}

func semanticTypeString() (value semantic.SemanticExpression) {
	value.Type.Kind = "string"
	value.Type.Cardinality = "required_one"
	return value
}

func TestRecipeControlResolverReturnsLogicalValidation(t *testing.T) {
	validation := testRecipeValidation()
	resolver := NewResolver(ResolverConfig{RecipeControl: fakeRecipeControl{validation: validation, plan: semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}}})
	result, err := resolver.Mutation().ValidateDataframeRecipe(context.Background(), model.ValidateDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecipeDigest != "recipe" || len(result.Outputs) != 1 || result.Outputs[0].FieldNames[0] != "id" {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestRecipeControlPreflightDoesNotExposePhysicalDetails(t *testing.T) {
	validation := testRecipeValidation()
	plan := semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}
	resolver := NewResolver(ResolverConfig{RecipeControl: fakeRecipeControl{validation: validation, plan: plan}})
	result, err := resolver.Query().PreflightDataframeRecipe(context.Background(), model.PreflightDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResolvedSchemaDigest != "schema" || result.SourceGeneration != "g" || result.ScopeDigest != "scope" {
		t.Fatalf("unexpected preflight result: %#v", result)
	}
}
