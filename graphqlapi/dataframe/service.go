package dataframeapi

import (
	"context"
	"time"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type Service struct {
	connOpts                arangostore.ConnectionOptions
	discoverReferences      func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFields          func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	dataframes              *dataframe.Service
	scopeResolver           *authscope.ScopeResolver
	activeManifestResolver  dataset.ActiveManifestResolver
	discoverDatasets        func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error)
	datasetProjectAllowlist []string
}

type Config struct {
	ConnectionOptions  arangostore.ConnectionOptions
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	Dataframes         *dataframe.Service
	ScopeResolver      *authscope.ScopeResolver
	// ActiveManifestResolver is optional. When present, builder catalog
	// discovery and recipe preparation resolve one READY active generation
	// before inspecting any fields or relationship routes.
	ActiveManifestResolver dataset.ActiveManifestResolver
	// DatasetProjectAllowlist is the explicit project source used when a
	// principal does not carry a project list. An empty value never triggers an
	// unrestricted catalog scan.
	DatasetProjectAllowlist []string
	DiscoverDatasets        func(context.Context, catalog.DatasetSummaryOptions) ([]catalog.DatasetSummary, error)
}

func NewService(cfg Config) *Service {
	service := &Service{
		connOpts:                cfg.ConnectionOptions,
		scopeResolver:           cfg.ScopeResolver,
		activeManifestResolver:  cfg.ActiveManifestResolver,
		datasetProjectAllowlist: cloneStrings(cfg.DatasetProjectAllowlist),
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
		service.dataframes = dataframe.NewService(dataframe.ServiceConfig{
			ConnectionOptions:      cfg.ConnectionOptions,
			DiscoverReferences:     service.discoverReferences,
			DiscoverFields:         service.discoverFields,
			ScopeResolver:          cfg.ScopeResolver,
			ActiveManifestResolver: cfg.ActiveManifestResolver,
		})
	}
	return service
}

func (s *Service) Run(ctx context.Context, input model.FhirDataframeInput, limit *int) (*dataframe.Result, error) {
	started := time.Now()
	normalizedInput, scope, generation, err := s.prepareRunInput(ctx, input)
	if err != nil {
		return nil, err
	}
	rowLimit := 0
	if limit != nil {
		rowLimit = *limit
	} else if normalizedInput.Limit != nil {
		rowLimit = *normalizedInput.Limit
	}
	builder := BuilderFromInput(normalizedInput)
	builder.DatasetGeneration = generation
	// Preserve the scope mode that resolved the catalog references above. This
	// matters when no catalog paths survive a restricted caller's intersection:
	// an empty list alone would otherwise be legacy-unrestricted downstream.
	builder.AuthScopeMode = scope.Mode
	result, err := s.dataframes.Run(ctx, dataframe.RunRequest{
		Builder: builder,
		Limit:   rowLimit,
	})
	if err != nil {
		return nil, err
	}
	result.Diagnostics.InputResolution = time.Since(started) - result.Diagnostics.Total
	if result.Diagnostics.InputResolution < 0 {
		result.Diagnostics.InputResolution = 0
	}
	result.Diagnostics.Total = time.Since(started)
	return result, nil
}
