// Package materializationapi adapts published ClickHouse dataframe outputs to
// GraphQL. Dataset discovery and reads are principal-scoped federations; the
// browser supplies only the logical dataType alias.
package materializationapi

import (
	"context"
	"fmt"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/materialization"
	"github.com/calypr/loom/internal/dataset"
)

type Service struct {
	reader           *dfmaterialization.Reader
	scopeResolver    *authscope.ScopeResolver
	activeGeneration dataset.ActiveManifestResolver
}

type Config struct {
	Reader                 *dfmaterialization.Reader
	ScopeResolver          *authscope.ScopeResolver
	ActiveManifestResolver dataset.ActiveManifestResolver
}

func NewService(cfg Config) *Service {
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver, activeGeneration: cfg.ActiveManifestResolver}
}

func (s *Service) principal(ctx context.Context) (*authscope.Principal, error) {
	principal, ok := authscope.PrincipalFromContext(ctx)
	if !ok || principal == nil {
		return nil, fmt.Errorf("an authenticated principal is required")
	}
	return principal, nil
}

func (s *Service) projects(ctx context.Context, principal *authscope.Principal) ([]string, error) {
	if len(principal.Projects) > 0 {
		return append([]string(nil), principal.Projects...), nil
	}
	projects, err := s.reader.PublishedProjects(ctx)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("principal has no authorized projects")
	}
	return projects, nil
}

func (s *Service) Datasets(ctx context.Context) ([]dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, fmt.Errorf("dataframe materialization reads are not configured")
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects(ctx, principal)
	if err != nil {
		return nil, err
	}
	datasets, err := s.reader.FederatedDatasets(ctx, projects)
	if err != nil {
		return nil, err
	}
	result := make([]dfmaterialization.Materialization, 0, len(datasets))
	for _, dataset := range datasets {
		result = append(result, federatedMaterialization(dataset))
	}
	return result, nil
}

func (s *Service) Dataset(ctx context.Context, input model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, fmt.Errorf("dataframe materialization reads are not configured")
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects(ctx, principal)
	if err != nil {
		return nil, err
	}
	dataset, err := s.reader.ResolveFederatedDataset(ctx, projects, input.DataType)
	if err != nil {
		return nil, err
	}
	value := federatedMaterialization(dataset)
	return &value, nil
}

// Get remains available for internal callers holding a publication ID. It is
// not part of the projectless Explorer contract and still performs source
// authorization before returning metadata.
func (s *Service) Get(ctx context.Context, id string) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, fmt.Errorf("dataframe materialization reads are not configured")
	}
	value, err := s.reader.DatasetByPublishedID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.authorizePublished(ctx, value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) Rows(ctx context.Context, input model.DataframeRowsInput) (dfmaterialization.Page, error) {
	if s.reader == nil {
		return dfmaterialization.Page{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			return dfmaterialization.Page{}, err
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.Page{}, err
		}
		return s.reader.PagePublishedID(ctx, value.ID, pageRequest(input))
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.Page{}, err
	}
	dataset, projects, authPaths, unrestricted, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return dfmaterialization.Page{}, err
	}
	page, err := s.reader.PageFederated(ctx, projects, input.DataType, dfmaterialization.FederatedPageRequest{
		Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After),
		AuthPathsByProject: authPaths, UnrestrictedByProject: unrestricted,
	})
	if err != nil {
		return dfmaterialization.Page{}, err
	}
	return dfmaterialization.Page{Materialization: federatedMaterialization(dataset), Columns: page.Columns, Rows: page.Rows, TotalCount: page.TotalCount, HasNext: page.HasNext, NextCursor: page.NextCursor}, nil
}

func (s *Service) AggregateInput(ctx context.Context, input model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error) {
	if s.reader == nil {
		return dfmaterialization.AggregateResult{}, fmt.Errorf("dataframe materialization reads are not configured")
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			return dfmaterialization.AggregateResult{}, err
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.AggregateResult{}, err
		}
		return s.reader.AggregatePublishedID(ctx, value.ID, dfmaterialization.AggregateRequest{MaterializationID: value.ID, GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column)})
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	dataset, projects, authPaths, unrestricted, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	result, err := s.reader.AggregateFederated(ctx, projects, input.DataType, dfmaterialization.FederatedAggregateRequest{
		GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column), AuthPathsByProject: authPaths, UnrestrictedByProject: unrestricted,
	})
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	result.Dataset = dataset
	return dfmaterialization.AggregateResult{Materialization: federatedMaterialization(result.Dataset), Columns: result.Columns, Rows: result.Rows}, nil
}

func (s *Service) authorizedFederation(ctx context.Context, principal *authscope.Principal, alias string) (dfmaterialization.FederatedDataset, []string, map[string][]string, map[string]bool, error) {
	candidates, err := s.projects(ctx, principal)
	if err != nil {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, err
	}
	dataset, err := s.reader.ResolveFederatedDataset(ctx, candidates, alias)
	if err != nil {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, err
	}
	paths := make(map[string][]string, len(dataset.Sources))
	unrestricted := make(map[string]bool, len(dataset.Sources))
	authorizedProjects := make([]string, 0, len(dataset.Sources))
	for _, source := range dataset.Sources {
		if s.scopeResolver != nil {
			scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, source.Project, source.DatasetGeneration, nil)
			if err != nil {
				if len(principal.Projects) == 0 {
					continue
				}
				return dfmaterialization.FederatedDataset{}, nil, nil, nil, err
			}
			paths[source.Project] = append([]string(nil), scope.AuthResourcePaths...)
			unrestricted[source.Project] = scope.Unrestricted()
			authorizedProjects = append(authorizedProjects, source.Project)
			continue
		}
		if source.AuthScopeMode == authscope.ReadScopeUnrestricted {
			unrestricted[source.Project] = true
			authorizedProjects = append(authorizedProjects, source.Project)
			continue
		}
		paths[source.Project] = append([]string(nil), principal.AuthResourcePaths...)
		authorizedProjects = append(authorizedProjects, source.Project)
	}
	if len(authorizedProjects) == 0 {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, fmt.Errorf("published dataset %q is not authorized", alias)
	}
	if len(authorizedProjects) != len(dataset.Sources) {
		dataset, err = s.reader.ResolveFederatedDataset(ctx, authorizedProjects, alias)
		if err != nil {
			return dfmaterialization.FederatedDataset{}, nil, nil, nil, err
		}
	}
	return dataset, authorizedProjects, paths, unrestricted, nil
}

func federatedMaterialization(dataset dfmaterialization.FederatedDataset) dfmaterialization.Materialization {
	return dfmaterialization.Materialization{ID: "federated:" + dataset.Name, Name: dataset.Name, Revision: dataset.Revision, DatasetGeneration: "federated:" + dataset.Revision, State: dfmaterialization.StateReady, Columns: dataset.Columns, RowCount: dataset.RowCount}
}

func (s *Service) authorizePublished(ctx context.Context, value dfmaterialization.Materialization) error {
	principal, err := s.principal(ctx)
	if err != nil {
		return err
	}
	allowed := false
	for _, project := range principal.Projects {
		if project == value.Project {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("principal is not authorized for published dataset")
	}
	if s.scopeResolver != nil {
		scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, value.AuthResourcePaths)
		if err != nil {
			return err
		}
		if value.AuthScopeMode != authscope.ReadScopeUnrestricted && scope.Unrestricted() {
			return fmt.Errorf("published dataset is outside caller scope")
		}
	}
	return nil
}

func pageRequest(input model.DataframeRowsInput) dfmaterialization.PageRequest {
	return dfmaterialization.PageRequest{Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After)}
}

func convertFilters(filters []*model.DataframeFilterInput) []dfmaterialization.Filter {
	converted := make([]dfmaterialization.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter != nil {
			converted = append(converted, dfmaterialization.Filter{Column: filter.Column, Op: filter.Op, Value: filter.Value})
		}
	}
	return converted
}

func convertSort(input model.DataframeRowsInput) *dfmaterialization.Sort {
	if input.Sort == nil {
		return nil
	}
	return &dfmaterialization.Sort{Column: input.Sort.Column, Desc: input.Sort.Desc != nil && *input.Sort.Desc}
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
