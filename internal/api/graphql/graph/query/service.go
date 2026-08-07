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
	discoverReferencesFn   func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFieldsFn       func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	dataframes             *runtime.Service
	scopeResolver          *authscope.ScopeResolver
	activeManifestResolver publication.ActiveResolver
	explain                func(context.Context, runtime.CompiledQuery) error
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
	Explain                func(context.Context, runtime.CompiledQuery) error
}

func NewService(cfg Config) *Service {
	service := &Service{
		scopeResolver:          cfg.ScopeResolver,
		activeManifestResolver: cfg.ActiveManifestResolver,
		explain:                cfg.Explain,
	}
	if cfg.DiscoverReferences != nil {
		service.discoverReferencesFn = cfg.DiscoverReferences
	}
	if cfg.DiscoverFields != nil {
		service.discoverFieldsFn = cfg.DiscoverFields
	}
	if cfg.Dataframes != nil {
		service.dataframes = cfg.Dataframes
	} else {
		service.dataframes = runtime.NewService(runtime.ServiceConfig{})
	}
	return service
}

func catalogUnavailable() error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func (s *Service) discoverFields(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
	if s == nil || s.discoverFieldsFn == nil {
		return nil, catalogUnavailable()
	}
	return s.discoverFieldsFn(ctx, opts)
}

func (s *Service) discoverReferences(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
	if s == nil || s.discoverReferencesFn == nil {
		return nil, catalogUnavailable()
	}
	return s.discoverReferencesFn(ctx, opts)
}

func (s *Service) resolveRecipe(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	resolvedBundle, err := s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return semantic.ResolvedRecipePlan{}, err
	}
	plan, err := semantic.BuildRecipePlan(resolvedBundle, bindings)
	if err != nil {
		return semantic.ResolvedRecipePlan{}, err
	}
	return semantic.ResolveRecipePlan(plan, "", bindings.DatasetGeneration)
}

func compileSingleQuery(resolved semantic.ResolvedRecipePlan, limit int) (runtime.CompiledQuery, error) {
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return runtime.CompiledQuery{}, err
	}
	if len(queries) != 1 {
		return runtime.CompiledQuery{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	return queries[0], nil
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
	resolved, err := s.resolveRecipe(ctx, bundle, bindings)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	requestPreparationDuration := time.Since(preparationStarted)
	compileStarted := time.Now()
	compiled, err := compileSingleQuery(resolved, rowLimit)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}
	result, err := s.dataframes.RunCompiled(ctx, compiled)
	if err != nil {
		return nil, queryBackend(err)
	}
	result.Diagnostics.Compilation = time.Since(compileStarted)
	result.Diagnostics.InputResolution = inputResolutionDuration
	result.Diagnostics.RequestPreparation = requestPreparationDuration
	result.Diagnostics.Total = time.Since(started)
	return result, nil
}
