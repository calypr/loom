package resolver

import (
	"context"
	"log/slog"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeapi "github.com/calypr/loom/internal/api/graphql/graph/dataframe"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	materialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	dataset "github.com/calypr/loom/internal/dataset"
)

// RecipeControl is the transport-neutral semantic control plane used by the
// recipe GraphQL fields. It intentionally returns logical plans only; no
// AQL, SQL, table names, or credentials cross this boundary.
type RecipeControl interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (dataframeexecution.PhysicalExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error)
	Run(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error)
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
	ErrorCode            string
	ErrorRetryable       bool
	TranslationVersion   string
	Phase                string
	RequestID            string
}

type RecipeExecutionOutput struct {
	Name           string
	State          string
	RowCount       *int
	Columns        []publication.PhysicalColumn
	Error          string
	ErrorCode      string
	ErrorRetryable bool
	Selector       dataset.DataframeSelector
	Phase          string
}

type RecipeExecutionReader func(context.Context, string) (*RecipeExecution, error)

// RecipeAuthorizer resolves request bindings against the caller's project
// grants. GraphQL operation type is intentionally irrelevant: preview/run are
// reads even though they are mutations, while publication is a write.
type RecipeAuthorizer interface {
	AuthorizeRead(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
	AuthorizeWrite(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
}

type Resolver struct {
	query            *queryapi.Service
	dataframes       *dataframeapi.Service
	recipeControl    RecipeControl
	recipeExecutions RecipeExecutionReader
	recipeAuthorizer RecipeAuthorizer
	recipeRevisions  recipe.RevisionStore
}

type ResolverConfig struct {
	DataframeQuery        queryapi.Config
	MaterializationReader *materialization.Reader
	Logger                *slog.Logger
	RecipeControl         RecipeControl
	RecipeExecutions      RecipeExecutionReader
	RecipeAuthorizer      RecipeAuthorizer
	RecipeRevisions       recipe.RevisionStore
}

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{
		query: queryapi.NewService(cfg.DataframeQuery),
		dataframes: dataframeapi.NewService(dataframeapi.Config{
			Reader:        cfg.MaterializationReader,
			ScopeResolver: cfg.DataframeQuery.ScopeResolver,
			Logger:        cfg.Logger,
		}),
		recipeControl:    cfg.RecipeControl,
		recipeExecutions: cfg.RecipeExecutions,
		recipeAuthorizer: cfg.RecipeAuthorizer,
		recipeRevisions:  cfg.RecipeRevisions,
	}
}

func (r *Resolver) authorizeRecipeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if r.recipeAuthorizer == nil {
		return bindings, nil
	}
	return r.recipeAuthorizer.AuthorizeRead(ctx, bindings)
}

func (r *Resolver) authorizeRecipeWrite(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if r.recipeAuthorizer == nil {
		return bindings, nil
	}
	return r.recipeAuthorizer.AuthorizeWrite(ctx, bindings)
}

func recipeBindings(input model.DataframeRecipeBindingsInput) recipe.RuntimeBindings {
	limit := 25
	if input.PreviewLimit != nil {
		limit = *input.PreviewLimit
	}
	return recipe.RuntimeBindings{
		Project: input.Project, RecipeDigest: valueOrEmpty(input.RecipeDigest), DatasetGeneration: valueOrEmpty(input.DatasetGeneration),
		AuthResourcePaths: append([]string(nil), input.AuthResourcePaths...), PreviewLimit: limit,
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
