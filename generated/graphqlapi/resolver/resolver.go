package resolver

import (
	"context"

	materializationapi "github.com/calypr/loom/internal/graphqlapi/materialization"
	"github.com/calypr/loom/generated/graphqlapi/model"
	queryapi "github.com/calypr/loom/internal/graphqlapi/query"
	"github.com/calypr/loom/internal/dataframe/materialization"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/control"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// RecipeControl is the transport-neutral semantic control plane used by the
// recipe GraphQL fields. It intentionally returns logical plans only; no
// AQL, SQL, table names, or credentials cross this boundary.
type RecipeControl interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (control.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings) (control.Preview, error)
}

// RecipeRunControl is an optional extension for the explicit full-data
// GraphQL operation. Preview and materialization remain separate contracts;
// this extension is only implemented by the canonical recipe engine.
type RecipeRunControl interface {
	Run(context.Context, string, recipe.RuntimeBindings) (control.Preview, error)
}

// RecipeExplainEvidenceControl is an optional extension implemented by the
// canonical recipe engine. Keeping it separate preserves compatibility with
// existing semantic-only controls and test doubles while allowing Explain to
// expose sanitized compiler/Arango evidence when configured.
type RecipeExplainEvidenceControl interface {
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (control.PhysicalExplanation, error)
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
}

type RecipeExecutionOutput struct {
	Name           string
	State          string
	RowCount       *int
	Error          string
	ErrorCode      string
	ErrorRetryable bool
}

// RecipeMaterializeFunc owns the complete materialization operation. The
// callback resolves the recipe exactly once and publishes the resulting
// physical plan; GraphQL does not pre-resolve it and then ask the callback to
// resolve it again.
type RecipeMaterializeFunc func(context.Context, string, recipe.RuntimeBindings) (RecipeExecution, error)
type RecipeExecutionReader func(context.Context, string) (*RecipeExecution, error)

// RecipeAuthorizer resolves request bindings against the caller's project
// grants. GraphQL operation type is intentionally irrelevant: preview/run are
// reads even though they are mutations, while publication is a write.
type RecipeAuthorizer interface {
	AuthorizeRead(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
	AuthorizeWrite(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
}

type Resolver struct {
	query             *queryapi.Service
	materializations  *materializationapi.Service
	recipeControl     RecipeControl
	recipeMaterialize RecipeMaterializeFunc
	recipeExecutions  RecipeExecutionReader
	recipeAuthorizer  RecipeAuthorizer
}

type ResolverConfig struct {
	DataframeQuery        queryapi.Config
	MaterializationReader *materialization.Reader
	RecipeControl         RecipeControl
	RecipeMaterialize     RecipeMaterializeFunc
	RecipeExecutions      RecipeExecutionReader
	RecipeAuthorizer      RecipeAuthorizer
}

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{
		query: queryapi.NewService(cfg.DataframeQuery),
		materializations: materializationapi.NewService(materializationapi.Config{
			Reader:                 cfg.MaterializationReader,
			ScopeResolver:          cfg.DataframeQuery.ScopeResolver,
			ActiveManifestResolver: cfg.DataframeQuery.ActiveManifestResolver,
		}),
		recipeControl:     cfg.RecipeControl,
		recipeMaterialize: cfg.RecipeMaterialize,
		recipeExecutions:  cfg.RecipeExecutions,
		recipeAuthorizer:  cfg.RecipeAuthorizer,
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
