package resolver

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/calypr/loom/generated/graphql/graph/model"
	materializationapi "github.com/calypr/loom/internal/api/graphql/graph/materialization"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	materialization "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataframe/semantic"
	dataset "github.com/calypr/loom/internal/dataset"
)

// RecipeControl is the transport-neutral semantic control plane used by the
// recipe GraphQL fields. It intentionally returns logical plans only; no
// AQL, SQL, table names, or credentials cross this boundary.
type RecipeControl interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (engine.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (engine.PhysicalExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings) (engine.Preview, error)
	Run(context.Context, string, recipe.RuntimeBindings) (engine.Preview, error)
}

// RecipeBundleControl is the inline authoring seam. It is optional so legacy
// server configurations can continue exposing only registered recipes.
type RecipeBundleControl interface {
	ValidateBundle(context.Context, recipe.Bundle, recipe.RuntimeBindings) (semantic.RecipePlan, error)
	PreviewBundle(context.Context, recipe.Bundle, recipe.RuntimeBindings) (engine.Preview, error)
}

// ExportDataframe exposes the same authenticated materialization service used
// by GraphQL rows and aggregates to the HTTP export adapter.
func (r *Resolver) ExportDataframe(ctx context.Context, request materialization.ExportRequest, out io.Writer) error {
	if r == nil || r.materializations == nil {
		return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return r.materializations.ExportDataframe(ctx, request, out)
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

// RecipeMaterializeFunc owns the complete materialization operation. The
// callback resolves the recipe exactly once and publishes the resulting
// physical plan; GraphQL does not pre-resolve it and then ask the callback to
// resolve it again.
type RecipeMaterializeFunc func(context.Context, string, recipe.RuntimeBindings) (RecipeExecution, error)
type RecipeExecutionReader func(context.Context, string) (*RecipeExecution, error)
type ExactMaterializationStarter func(context.Context, dataset.DataframeSelector, recipe.RuntimeBindings) (RecipeExecution, error)
type ProjectRelease struct{ ID, Project, Generation, Revision, State string }
type ProjectReleaseActivator func(context.Context, string, string, string) (ProjectRelease, error)
type DataframeContract struct{ Recipe, TranslationVersion, PromotedAt string }
type DataframeContractPromoter func(context.Context, string, string) (DataframeContract, error)
type DataframeContractAuthorizer func(context.Context) error

type ProjectRecipePublisher interface {
	Publish(context.Context, string, int64, string, []string) (recipe.RecipeRevision, error)
}

// RecipeAuthorizer resolves request bindings against the caller's project
// grants. GraphQL operation type is intentionally irrelevant: preview/run are
// reads even though they are mutations, while publication is a write.
type RecipeAuthorizer interface {
	AuthorizeRead(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
	AuthorizeWrite(context.Context, recipe.RuntimeBindings) (recipe.RuntimeBindings, error)
}

type Resolver struct {
	query                       *queryapi.Service
	materializations            *materializationapi.Service
	recipeControl               RecipeControl
	recipeMaterialize           RecipeMaterializeFunc
	recipeExecutions            RecipeExecutionReader
	recipeAuthorizer            RecipeAuthorizer
	exactMaterializationStarter ExactMaterializationStarter
	projectReleaseActivator     ProjectReleaseActivator
	dataframeContractPromoter   DataframeContractPromoter
	dataframeContractAuthorizer DataframeContractAuthorizer
	recipeRevisions             recipe.RevisionStore
	recipeBundleControl         RecipeBundleControl
	projectRecipeDrafts         recipe.DraftStore
	defaultRecipeBundle         func() (recipe.Bundle, error)
	projectRecipePublisher      ProjectRecipePublisher
}

type ResolverConfig struct {
	DataframeQuery              queryapi.Config
	MaterializationReader       *materialization.Reader
	Logger                      *slog.Logger
	RecipeControl               RecipeControl
	RecipeMaterialize           RecipeMaterializeFunc
	RecipeExecutions            RecipeExecutionReader
	RecipeAuthorizer            RecipeAuthorizer
	DefaultRecipe               string
	DefaultTranslationVersion   string
	DefaultContract             func() (string, string)
	ExactMaterializationStarter ExactMaterializationStarter
	ProjectReleaseActivator     ProjectReleaseActivator
	DataframeContractPromoter   DataframeContractPromoter
	DataframeContractAuthorizer DataframeContractAuthorizer
	CandidateProjects           func(context.Context) ([]string, error)
	RecipeRevisions             recipe.RevisionStore
	RecipeBundleControl         RecipeBundleControl
	ProjectRecipeDrafts         recipe.DraftStore
	DefaultRecipeBundle         func() (recipe.Bundle, error)
	ProjectRecipePublisher      ProjectRecipePublisher
}

func NewResolver(cfg ResolverConfig) *Resolver {
	if cfg.DefaultRecipe == "" {
		cfg.DefaultRecipe = os.Getenv("LOOM_DEFAULT_RECIPE")
	}
	if cfg.DefaultTranslationVersion == "" {
		cfg.DefaultTranslationVersion = os.Getenv("LOOM_DEFAULT_TRANSLATION_VERSION")
	}
	return &Resolver{
		query: queryapi.NewService(cfg.DataframeQuery),
		materializations: materializationapi.NewService(materializationapi.Config{
			Reader:                    cfg.MaterializationReader,
			ScopeResolver:             cfg.DataframeQuery.ScopeResolver,
			Logger:                    cfg.Logger,
			DefaultRecipe:             cfg.DefaultRecipe,
			DefaultTranslationVersion: cfg.DefaultTranslationVersion,
			DefaultContract:           cfg.DefaultContract,
			CandidateProjects:         cfg.CandidateProjects,
		}),
		recipeControl:               cfg.RecipeControl,
		recipeMaterialize:           cfg.RecipeMaterialize,
		recipeExecutions:            cfg.RecipeExecutions,
		recipeAuthorizer:            cfg.RecipeAuthorizer,
		exactMaterializationStarter: cfg.ExactMaterializationStarter,
		projectReleaseActivator:     cfg.ProjectReleaseActivator,
		dataframeContractPromoter:   cfg.DataframeContractPromoter,
		dataframeContractAuthorizer: cfg.DataframeContractAuthorizer,
		recipeRevisions:             cfg.RecipeRevisions,
		recipeBundleControl:         cfg.RecipeBundleControl,
		projectRecipeDrafts:         cfg.ProjectRecipeDrafts,
		defaultRecipeBundle:         cfg.DefaultRecipeBundle,
		projectRecipePublisher:      cfg.ProjectRecipePublisher,
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
