package dataframebuilder

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/graphqlapi/model"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type Service struct {
	connOpts           arangostore.ConnectionOptions
	discoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	dataframes         *dataframe.Service
	scopeResolver      *authscope.ScopeResolver
}

type Config struct {
	ConnectionOptions  arangostore.ConnectionOptions
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	Dataframes         *dataframe.Service
	ScopeResolver      *authscope.ScopeResolver
}

func NewService(cfg Config) *Service {
	service := &Service{
		connOpts:      cfg.ConnectionOptions,
		scopeResolver: cfg.ScopeResolver,
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
			ConnectionOptions:  cfg.ConnectionOptions,
			DiscoverReferences: service.discoverReferences,
			DiscoverFields:     service.discoverFields,
			ScopeResolver:      cfg.ScopeResolver,
		})
	}
	return service
}

func (s *Service) Run(ctx context.Context, input model.FhirDataframeInput, limit *int) (*dataframe.Result, error) {
	normalizedInput, err := s.PrepareRunInput(ctx, input)
	if err != nil {
		return nil, err
	}
	rowLimit := 0
	if limit != nil {
		rowLimit = *limit
	} else if normalizedInput.Limit != nil {
		rowLimit = *normalizedInput.Limit
	}
	return s.dataframes.Run(ctx, dataframe.RunRequest{
		Builder: BuilderFromInput(normalizedInput),
		Limit:   rowLimit,
	})
}
