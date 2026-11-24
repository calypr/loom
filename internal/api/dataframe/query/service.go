package queryapi

import (
	"context"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"time"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	publication "github.com/calypr/loom/internal/dataset"
)

type Service struct {
	discoverReferencesFn   func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFieldsFn       func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error)
	dataframes             *dataframeexecution.Service
	scopeResolver          *authscope.ScopeResolver
	activeManifestResolver publication.ActiveResolver
	explain                func(context.Context, dataframeexecution.CompiledQuery) error

	now func() time.Time
}

type Config struct {
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error)
	Dataframes         *dataframeexecution.Service
	ScopeResolver      *authscope.ScopeResolver

	ActiveManifestResolver publication.ActiveResolver
	Explain                func(context.Context, dataframeexecution.CompiledQuery) error
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
		service.dataframes = dataframeexecution.NewService(dataframeexecution.ServiceConfig{})
	}
	return service
}

func catalogUnavailable() error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func (s *Service) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) discoverFields(ctx context.Context, opts fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
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

func (s *Service) resolveRecipe(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipeplan.ResolvedRecipePlan, error) {
	resolvedBundle, err := s.resolveRecipeBundle(ctx, bundle, bindings)
	if err != nil {
		return recipeplan.ResolvedRecipePlan{}, err
	}
	plan, err := recipeplan.BuildRecipePlan(resolvedBundle, bindings)
	if err != nil {
		return recipeplan.ResolvedRecipePlan{}, err
	}
	return recipeplan.ResolveRecipePlan(plan, "", bindings.DatasetGeneration)
}

func compileSingleQuery(resolved recipeplan.ResolvedRecipePlan, limit int) (dataframeexecution.CompiledQuery, error) {
	queries, err := compiler.CompileResolvedRecipePlanWithPolicy(resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return dataframeexecution.CompiledQuery{}, err
	}
	if len(queries) != 1 {
		return dataframeexecution.CompiledQuery{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	return queries[0], nil
}

func (s *Service) RunPreview(ctx context.Context, input dataframemodel.FhirDataframeInput, limit *int) (*dataframeexecution.Result, error) {
	if limit == nil {
		defaultLimit := 25
		limit = &defaultLimit
	}
	return s.Run(ctx, input, limit)
}

func (s *Service) Run(ctx context.Context, input dataframemodel.FhirDataframeInput, limit *int) (*dataframeexecution.Result, error) {
	started := s.clockNow()
	normalizedInput, scope, generation, err := s.prepareRunInput(ctx, input)
	if err != nil {
		return nil, err
	}
	inputResolutionDuration := s.clockNow().Sub(started)
	rowLimit := 0
	if limit != nil {
		rowLimit = *limit
	} else if normalizedInput.Limit != nil {
		rowLimit = *normalizedInput.Limit
	}
	if rowLimit <= 0 {
		rowLimit = 25
	}
	preparationStarted := s.clockNow()
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
	requestPreparationDuration := s.clockNow().Sub(preparationStarted)
	compileStarted := s.clockNow()
	compiled, err := compileSingleQuery(resolved, rowLimit)
	if err != nil {
		return nil, queryInvalidErrorOrBackend(err)
	}

	compilationDuration := s.clockNow().Sub(compileStarted)
	result, err := s.dataframes.RunCompiled(ctx, compiled)
	if err != nil {
		return nil, queryBackend(err)
	}
	result.Diagnostics.Compilation = compilationDuration
	result.Diagnostics.InputResolution = inputResolutionDuration
	result.Diagnostics.RequestPreparation = requestPreparationDuration
	result.Diagnostics.Total = s.clockNow().Sub(started)
	return result, nil
}
