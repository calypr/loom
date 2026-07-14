package graphqlapi

import (
	"context"

	materializationapi "github.com/calypr/loom/graphqlapi/materialization"
	"github.com/calypr/loom/graphqlapi/model"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/dataframe/materialization"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipecontrol"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// RecipeControl is the transport-neutral semantic control plane used by the
// recipe GraphQL fields. It intentionally returns logical plans only; no
// AQL, SQL, table names, or credentials cross this boundary.
type RecipeControl interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (recipecontrol.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings, recipecontrol.ExecuteFunc) (recipecontrol.Preview, error)
}

// RecipeExecution is the stable logical execution record exposed by GraphQL.
// Implementations may persist additional physical metadata, but adapters must
// not return it here.
type RecipeExecution struct {
	ID                   string
	Name                 string
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	State                string
	Outputs              []RecipeExecutionOutput
	Error                string
}

type RecipeExecutionOutput struct {
	Name     string
	State    string
	RowCount *int
	Error    string
}

type RecipeMaterializeFunc func(context.Context, string, recipe.RuntimeBindings, semantic.ResolvedRecipePlan) (RecipeExecution, error)
type RecipeExecutionReader func(context.Context, string) (*RecipeExecution, error)

type Resolver struct {
	query             *queryapi.Service
	materializations  *materializationapi.Service
	recipeControl     RecipeControl
	previewExecute    recipecontrol.ExecuteFunc
	recipeMaterialize RecipeMaterializeFunc
	recipeExecutions  RecipeExecutionReader
}

type ResolverConfig struct {
	DataframeQuery        queryapi.Config
	MaterializationReader *materialization.Reader
	RecipeControl         RecipeControl
	RecipePreviewExecute  recipecontrol.ExecuteFunc
	RecipeMaterialize     RecipeMaterializeFunc
	RecipeExecutions      RecipeExecutionReader
}

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{
		query: queryapi.NewService(cfg.DataframeQuery),
		materializations: materializationapi.NewService(materializationapi.Config{
			Reader:        cfg.MaterializationReader,
			ScopeResolver: cfg.DataframeQuery.ScopeResolver,
		}),
		recipeControl:     cfg.RecipeControl,
		previewExecute:    cfg.RecipePreviewExecute,
		recipeMaterialize: cfg.RecipeMaterialize,
		recipeExecutions:  cfg.RecipeExecutions,
	}
}

func recipeBindings(input model.DataframeRecipeBindingsInput) recipe.RuntimeBindings {
	limit := 25
	if input.PreviewLimit != nil {
		limit = *input.PreviewLimit
	}
	return recipe.RuntimeBindings{
		Project: input.Project, DatasetGeneration: valueOrEmpty(input.DatasetGeneration),
		AuthResourcePaths: append([]string(nil), input.AuthResourcePaths...), PreviewLimit: limit,
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
