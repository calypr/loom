package queryapi

import (
	"context"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/dataframe/semantic"
	publication "github.com/calypr/loom/internal/dataset"
)

type Service struct {
	discoverReferences      func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFields          func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	dataframes              *runtime.Service
	scopeResolver           *authscope.ScopeResolver
	activeManifestResolver  publication.ActiveResolver
	discoverDatasets        func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error)
	datasetProjectAllowlist []string
	explain                 func(context.Context, runtime.CompiledQuery) error
}

type Config struct {
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	Dataframes         *runtime.Service
	ScopeResolver      *authscope.ScopeResolver
	// ActiveManifestResolver is optional. When present, builder catalog
	// discovery and recipe preparation resolve one READY active generation
	// before inspecting any fields or relationship routes.
	ActiveManifestResolver publication.ActiveResolver
	// DatasetProjectAllowlist is the explicit project source used when a
	// principal does not carry a project list. An empty value never triggers an
	// unrestricted catalog scan.
	DatasetProjectAllowlist []string
	DiscoverDatasets        func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error)
	Explain                 func(context.Context, runtime.CompiledQuery) error
}

func NewService(cfg Config) *Service {
	service := &Service{
		scopeResolver:           cfg.ScopeResolver,
		activeManifestResolver:  cfg.ActiveManifestResolver,
		datasetProjectAllowlist: cloneStrings(cfg.DatasetProjectAllowlist),
		explain:                 cfg.Explain,
	}
	if cfg.DiscoverDatasets != nil {
		service.discoverDatasets = cfg.DiscoverDatasets
	} else {
		service.discoverDatasets = catalog.DiscoverDatasetSummaries
	}
	if cfg.DiscoverReferences != nil {
		service.discoverReferences = cfg.DiscoverReferences
	} else {
		service.discoverReferences = catalog.DiscoverPopulatedReferences
	}
	if cfg.DiscoverFields != nil {
		service.discoverFields = cfg.DiscoverFields
	} else {
		service.discoverFields = catalog.DiscoverPopulatedFields
	}
	if cfg.Dataframes != nil {
		service.dataframes = cfg.Dataframes
	} else {
		service.dataframes = runtime.NewService(runtime.ServiceConfig{
			ScopeResolver:          cfg.ScopeResolver,
			ActiveManifestResolver: cfg.ActiveManifestResolver,
		})
	}
	return service
}

func (s *Service) Run(ctx context.Context, input model.FhirDataframeInput, limit *int) (*runtime.Result, error) {
	started := time.Now()
	normalizedInput, scope, generation, err := s.prepareRunInput(ctx, input)
	if err != nil {
		return nil, err
	}
	inputResolutionDuration := time.Since(started)
	rowLimit := 0
	if limit != nil {
		rowLimit = *limit
	} else if normalizedInput.Limit != nil {
		rowLimit = *normalizedInput.Limit
	}
	if rowLimit <= 0 {
		rowLimit = 25
	}
	preparationStarted := time.Now()
	bundle, err := RecipeBundleFromInput(normalizedInput)
	if err != nil {
		return nil, queryInvalid(dataframeerrors.CodeInvalidRequest, err)
	}
	bindings := recipe.RuntimeBindings{
		Project:           normalizedInput.Project,
		DatasetGeneration: generation,
		AuthResourcePaths: cloneStrings(scope.AuthResourcePaths),
		AuthScopeMode:     scope.Mode,
		PreviewLimit:      rowLimit,
	}
	bundle, err = s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, "", generation)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	requestPreparationDuration := time.Since(preparationStarted)
	compileStarted := time.Now()
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, rowLimit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	if len(queries) != 1 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	result, err := s.dataframes.RunCompiled(ctx, queries[0])
	if err != nil {
		return nil, queryBackend(err)
	}
	result.Diagnostics.Compilation = time.Since(compileStarted)
	result.Diagnostics.InputResolution = inputResolutionDuration
	result.Diagnostics.RequestPreparation = requestPreparationDuration
	result.Diagnostics.Total = time.Since(started)
	return result, nil
}
