// Package materializationapi adapts published ClickHouse dataframe outputs to
// GraphQL. Dataset discovery and reads are principal-scoped federations; the
// browser supplies only the logical dataType alias.
package materializationapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/materialization"
	"github.com/calypr/loom/internal/dataset"
)

type Service struct {
	reader           *dfmaterialization.Reader
	scopeResolver    *authscope.ScopeResolver
	activeGeneration dataset.ActiveManifestResolver
	maxExportRows    int64
	maxExportBytes   int64
}

type Config struct {
	Reader                 *dfmaterialization.Reader
	ScopeResolver          *authscope.ScopeResolver
	ActiveManifestResolver dataset.ActiveManifestResolver
	MaxExportRows          int64
	MaxExportBytes         int64
}

func NewService(cfg Config) *Service {
	if cfg.MaxExportRows <= 0 {
		cfg.MaxExportRows = 1_000_000
	}
	if cfg.MaxExportBytes <= 0 {
		cfg.MaxExportBytes = 1 << 30
	}
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver, activeGeneration: cfg.ActiveManifestResolver, maxExportRows: cfg.MaxExportRows, maxExportBytes: cfg.MaxExportBytes}
}

func (s *Service) principal(ctx context.Context) (*authscope.Principal, error) {
	principal, ok := authscope.PrincipalFromContext(ctx)
	if !ok || principal == nil {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	}
	return principal, nil
}

func (s *Service) projects(ctx context.Context, principal *authscope.Principal) ([]string, error) {
	if len(principal.Projects) > 0 {
		return append([]string(nil), principal.Projects...), nil
	}
	projects, err := s.reader.PublishedProjects(ctx)
	if err != nil {
		return nil, mapReaderError(err)
	}
	if len(projects) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
	}
	return projects, nil
}

func (s *Service) Datasets(ctx context.Context) ([]dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects(ctx, principal)
	if err != nil {
		return nil, mapReaderError(err)
	}
	aliases, err := s.reader.CurrentFederatedAliases(ctx, projects)
	if err != nil {
		return nil, mapReaderError(err)
	}
	result := make([]dfmaterialization.Materialization, 0, len(aliases))
	for _, alias := range aliases {
		if dataset, _, _, _, resolveErr := s.authorizedFederation(ctx, principal, alias); resolveErr == nil {
			result = append(result, federatedMaterialization(dataset))
		} else if normalized := dataframeerrors.Normalize(resolveErr); normalized.Code() != string(dataframeerrors.CodeDatasetNotFound) {
			return nil, resolveErr
		}
	}
	return result, nil
}

func (s *Service) Dataset(ctx context.Context, input model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	dataset, _, _, _, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return nil, mapReaderError(err)
	}
	value := federatedMaterialization(dataset)
	return &value, nil
}

// Get remains available for internal callers holding a publication ID. It is
// not part of the projectless Explorer contract and still performs source
// authorization before returning metadata.
func (s *Service) Get(ctx context.Context, id string) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	value, err := s.reader.DatasetByPublishedID(ctx, id)
	if err != nil {
		return nil, mapReaderError(err)
	}
	if err := s.authorizePublished(ctx, value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Service) Rows(ctx context.Context, input model.DataframeRowsInput) (dfmaterialization.Page, error) {
	if s.reader == nil {
		return dfmaterialization.Page{}, readerUnavailable()
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			return dfmaterialization.Page{}, mapReaderError(err)
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.Page{}, err
		}
		page, err := s.reader.PagePublishedID(ctx, value.ID, pageRequest(input))
		if err != nil {
			return dfmaterialization.Page{}, mapReaderError(err)
		}
		return page, nil
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	dataset, _, authPaths, unrestricted, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	page, err := s.reader.PageFederatedDataset(ctx, dataset, dfmaterialization.FederatedPageRequest{
		Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After),
		AuthPathsByProject: authPaths, UnrestrictedByProject: unrestricted,
	})
	if err != nil {
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	return dfmaterialization.Page{Materialization: federatedMaterialization(dataset), Columns: page.Columns, Rows: page.Rows, TotalCount: page.TotalCount, HasNext: page.HasNext, NextCursor: page.NextCursor}, nil
}

func (s *Service) AggregateInput(ctx context.Context, input model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error) {
	if s.reader == nil {
		return dfmaterialization.AggregateResult{}, readerUnavailable()
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			return dfmaterialization.AggregateResult{}, mapReaderError(err)
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.AggregateResult{}, err
		}
		result, err := s.reader.AggregatePublishedID(ctx, value.ID, dfmaterialization.AggregateRequest{MaterializationID: value.ID, GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column)})
		if err != nil {
			return dfmaterialization.AggregateResult{}, mapReaderError(err)
		}
		return result, nil
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	dataset, _, authPaths, unrestricted, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	result, err := s.reader.AggregateFederatedDataset(ctx, dataset, dfmaterialization.FederatedAggregateRequest{
		GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column), AuthPathsByProject: authPaths, UnrestrictedByProject: unrestricted,
	})
	if err != nil {
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	result.Dataset = dataset
	return dfmaterialization.AggregateResult{Materialization: federatedMaterialization(result.Dataset), Columns: result.Columns, Rows: result.Rows}, nil
}

func (s *Service) AggregationsInput(ctx context.Context, input model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	if s.reader == nil {
		return dfmaterialization.AggregationsResult{}, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	dataset, _, authPaths, unrestricted, err := s.authorizedFederation(ctx, principal, input.DataType)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	specs := make([]dfmaterialization.AggregationSpec, 0, len(input.Specs))
	for _, spec := range input.Specs {
		if spec == nil {
			continue
		}
		value := dfmaterialization.AggregationSpec{Name: spec.Name, Kind: spec.Kind, Column: spec.Column, ExcludeSelfFilter: spec.ExcludeSelfFilter != nil && *spec.ExcludeSelfFilter}
		if spec.Size != nil {
			value.Size = *spec.Size
		}
		if spec.Interval != nil {
			value.Interval = *spec.Interval
		}
		if spec.DateInterval != nil {
			value.DateInterval = *spec.DateInterval
		}
		specs = append(specs, value)
	}
	result, err := s.reader.AggregateFederatedBatchDataset(ctx, dataset, dfmaterialization.AggregationsRequest{
		Filters: convertFilters(input.Filters), Specs: specs, AuthPathsByProject: authPaths, UnrestrictedByProject: unrestricted,
	})
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	result.Dataset = dataset
	return result, nil
}

func AggregationsJSON(result dfmaterialization.AggregationsResult) (json.RawMessage, error) {
	return json.Marshal(dfmaterialization.NormalizeAggregationResults(result.Aggregations))
}

func (s *Service) authorizedFederation(ctx context.Context, principal *authscope.Principal, alias string) (dfmaterialization.FederatedDataset, []string, map[string][]string, map[string]bool, error) {
	candidates, err := s.projects(ctx, principal)
	if err != nil {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, mapReaderError(err)
	}
	sources, err := s.reader.CurrentFederatedSources(ctx, candidates, alias)
	if err != nil {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, mapReaderError(err)
	}
	paths := make(map[string][]string, len(sources))
	unrestricted := make(map[string]bool, len(sources))
	authorizedProjects := make([]string, 0, len(sources))
	authorizedSources := make([]dfmaterialization.Materialization, 0, len(sources))
	rowCountComplete := true
	for _, source := range sources {
		if s.scopeResolver != nil {
			scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, source.Project, source.DatasetGeneration, nil)
			if err != nil {
				return dfmaterialization.FederatedDataset{}, nil, nil, nil, mapReaderError(err)
			}
			if !scope.Unrestricted() && len(scope.AuthResourcePaths) == 0 {
				continue
			}
			if !scope.Unrestricted() {
				rowCountComplete = false
			}
			paths[source.Project] = append([]string(nil), scope.AuthResourcePaths...)
			unrestricted[source.Project] = scope.Unrestricted()
			authorizedProjects = append(authorizedProjects, source.Project)
			authorizedSources = append(authorizedSources, source)
			continue
		}
		if source.AuthScopeMode == authscope.ReadScopeUnrestricted {
			unrestricted[source.Project] = true
			authorizedProjects = append(authorizedProjects, source.Project)
			authorizedSources = append(authorizedSources, source)
			continue
		}
		if len(principal.AuthResourcePaths) == 0 {
			continue
		}
		rowCountComplete = false
		paths[source.Project] = append([]string(nil), principal.AuthResourcePaths...)
		authorizedProjects = append(authorizedProjects, source.Project)
		authorizedSources = append(authorizedSources, source)
	}
	if len(authorizedProjects) == 0 {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	dataset, err := dfmaterialization.ReconcileFederatedDataset(alias, authorizedSources)
	if err != nil {
		return dfmaterialization.FederatedDataset{}, nil, nil, nil, err
	}
	dataset.RowCountComplete = rowCountComplete
	return dataset, authorizedProjects, paths, unrestricted, nil
}

func mapReaderError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, authscope.ErrUnauthenticated) {
		return dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	}
	if errors.Is(err, authscope.ErrForbidden) {
		return dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
	}
	if errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func readerUnavailable() error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func federatedMaterialization(dataset dfmaterialization.FederatedDataset) dfmaterialization.Materialization {
	return dfmaterialization.Materialization{ID: "federated:" + dataset.Name, Name: dataset.Name, Revision: dataset.Revision, DatasetGeneration: "federated:" + dataset.Revision, State: dfmaterialization.StateReady, Columns: dataset.Columns, RowCount: dataset.RowCount, RowCountKnown: dataset.RowCountComplete}
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
		return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	if s.scopeResolver != nil {
		scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, value.AuthResourcePaths)
		if err != nil {
			return err
		}
		if value.AuthScopeMode != authscope.ReadScopeUnrestricted && scope.Unrestricted() {
			return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
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
			var value any
			if len(filter.Value) > 0 {
				if err := json.Unmarshal(filter.Value, &value); err != nil {
					// DataframeFilterInput.value is a GraphQL JSON scalar, so
					// gqlgen should only provide valid JSON here. Preserve the
					// original value if a custom caller bypasses GraphQL with
					// malformed input; the reader will surface the resulting
					// parameter error instead of silently dropping the filter.
					value = filter.Value
				}
			}
			converted = append(converted, dfmaterialization.Filter{Column: filter.Column, Op: filter.Op, Value: value})
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
