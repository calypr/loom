package dataframe

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const defaultRowLimit = 25

type ServiceConfig struct {
	ConnectionOptions  arangostore.ConnectionOptions
	DiscoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	DiscoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	ExecuteRows        func(context.Context, ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	ScopeResolver      *authscope.ScopeResolver
}

type Service struct {
	connOpts           arangostore.ConnectionOptions
	discoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
	discoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	executeRows        func(context.Context, ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	scopeResolver      *authscope.ScopeResolver
}

func NewService(cfg ServiceConfig) *Service {
	svc := &Service{
		connOpts:      cfg.ConnectionOptions,
		scopeResolver: cfg.ScopeResolver,
	}
	if cfg.DiscoverReferences != nil {
		svc.discoverReferences = cfg.DiscoverReferences
	} else {
		svc.discoverReferences = catalog.DiscoverPopulatedReferences
	}
	if cfg.DiscoverFields != nil {
		svc.discoverFields = cfg.DiscoverFields
	} else {
		svc.discoverFields = catalog.DiscoverPopulatedFields
	}
	if cfg.ExecuteRows != nil {
		svc.executeRows = cfg.ExecuteRows
	} else {
		svc.executeRows = ExecuteQueryRows
	}
	return svc
}

func (s *Service) Run(ctx context.Context, req RunRequest) (*Result, error) {
	spec, err := s.prepareSpec(ctx, req.Builder)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultRowLimit
	}
	compiled, err := Compile(spec, limit)
	if err != nil {
		return nil, err
	}
	return s.runQuery(ctx, compiled)
}

func (s *Service) prepareSpec(ctx context.Context, builder Builder) (Builder, error) {
	if builder.Project == "" {
		return Builder{}, fmt.Errorf("project is required")
	}
	if builder.RootResourceType == "" {
		return Builder{}, fmt.Errorf("rootResourceType is required")
	}

	principal, _ := authscope.PrincipalFromContext(ctx)
	resolvedPaths, err := s.resolveAuthResourcePaths(ctx, principal, builder.Project, builder.AuthResourcePaths)
	if err != nil {
		return Builder{}, err
	}
	if err := authorizeProject(principal, builder.Project, s.scopeResolver != nil); err != nil {
		return Builder{}, err
	}

	builder.AuthResourcePaths = resolvedPaths
	if err := s.validateBuilder(ctx, builder); err != nil {
		return Builder{}, err
	}
	expanded, err := s.expandPivotColumns(ctx, builder)
	if err != nil {
		return Builder{}, err
	}
	planned, err := lowerGraphQLBuilder(expanded)
	if err != nil {
		return Builder{}, err
	}
	if err := validateLoweredBuilder(planned); err != nil {
		return Builder{}, err
	}
	return planned, nil
}
