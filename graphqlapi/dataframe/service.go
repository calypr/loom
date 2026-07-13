package dataframeapi

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/dataframe/materialization"
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
	materializations        *materialization.Service
	materializationReader   *materialization.Reader
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
	Materializations        *materialization.Service
	MaterializationReader   *materialization.Reader
}

func NewService(cfg Config) *Service {
	service := &Service{
		connOpts:                cfg.ConnectionOptions,
		scopeResolver:           cfg.ScopeResolver,
		activeManifestResolver:  cfg.ActiveManifestResolver,
		datasetProjectAllowlist: cloneStrings(cfg.DatasetProjectAllowlist),
		materializations:        cfg.Materializations,
		materializationReader:   cfg.MaterializationReader,
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

func (s *Service) GetMaterialization(ctx context.Context, id string) (*materialization.Materialization, error) {
	if s.materializationReader == nil {
		return nil, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.materializationReader.Registry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeMaterialization(ctx, value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) ReadMaterialization(ctx context.Context, input model.DataframeRowsInput) (materialization.Page, error) {
	if s.materializationReader == nil {
		return materialization.Page{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	materialized, err := s.materializationReader.Registry.Get(ctx, input.MaterializationID)
	if err != nil {
		return materialization.Page{}, err
	}
	if err := s.authorizeMaterialization(ctx, materialized); err != nil {
		return materialization.Page{}, err
	}
	filters := make([]materialization.Filter, 0, len(input.Filters))
	for _, filter := range input.Filters {
		if filter == nil {
			continue
		}
		filters = append(filters, materialization.Filter{Column: filter.Column, Op: filter.Op, Value: filter.Value})
	}
	var sortInput *materialization.Sort
	if input.Sort != nil {
		sortInput = &materialization.Sort{Column: input.Sort.Column, Desc: input.Sort.Desc != nil && *input.Sort.Desc}
	}
	first := 0
	if input.First != nil {
		first = *input.First
	}
	return s.materializationReader.Page(ctx, materialization.PageRequest{MaterializationID: input.MaterializationID, Columns: input.Columns, Filters: filters, Sort: sortInput, First: first, After: derefString(input.After)})
}

func (s *Service) AggregateMaterialization(ctx context.Context, id string, groupBy []string, filters []*model.DataframeFilterInput, operation, column string) (materialization.AggregateResult, error) {
	if s.materializationReader == nil {
		return materialization.AggregateResult{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.materializationReader.Registry.Get(ctx, id)
	if err != nil {
		return materialization.AggregateResult{}, err
	}
	if err := s.authorizeMaterialization(ctx, value); err != nil {
		return materialization.AggregateResult{}, err
	}
	converted := make([]materialization.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter == nil {
			continue
		}
		converted = append(converted, materialization.Filter{Column: filter.Column, Op: filter.Op, Value: filter.Value})
	}
	return s.materializationReader.Aggregate(ctx, materialization.AggregateRequest{MaterializationID: id, GroupBy: groupBy, Filters: converted, Operation: operation, Column: column})
}

func (s *Service) authorizeMaterialization(ctx context.Context, value materialization.Materialization) error {
	principal, _ := authscope.PrincipalFromContext(ctx)
	if principal != nil && len(principal.Projects) > 0 {
		allowed := false
		for _, project := range principal.Projects {
			if project == value.Project {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("principal is not authorized for project %q", value.Project)
		}
	}
	if value.AuthScopeMode == authscope.ReadScopeUnrestricted {
		if s.scopeResolver != nil {
			scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, nil)
			if err != nil {
				return err
			}
			if !scope.Unrestricted() {
				return fmt.Errorf("materialization %q requires unrestricted authorization", value.ID)
			}
		} else if principal != nil && len(principal.AuthResourcePaths) > 0 {
			return fmt.Errorf("materialization %q requires unrestricted authorization", value.ID)
		}
		return nil
	}
	if s.scopeResolver != nil {
		scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, value.AuthResourcePaths)
		if err != nil {
			return err
		}
		if len(scope.AuthResourcePaths) != len(value.AuthResourcePaths) {
			return fmt.Errorf("materialization %q is outside caller scope", value.ID)
		}
		return nil
	}
	if principal == nil {
		return fmt.Errorf("materialization %q requires an authorized principal", value.ID)
	}
	for _, requested := range value.AuthResourcePaths {
		found := false
		for _, allowed := range principal.AuthResourcePaths {
			if requested == allowed {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("materialization %q is outside caller scope", value.ID)
		}
	}
	return nil
}
