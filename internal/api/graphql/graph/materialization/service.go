// Package materializationapi adapts published ClickHouse dataframe outputs to
// GraphQL. Dataset discovery and reads are principal-scoped federations; the
// browser supplies only the FHIR resource type.
package materializationapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/calypr/loom/generated/graphql/graph/model"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type Service struct {
	reader                    *dfmaterialization.Reader
	scopeResolver             *authscope.ScopeResolver
	logger                    *slog.Logger
	maxExportRows             int64
	maxExportBytes            int64
	defaultRecipe             string
	defaultTranslationVersion string
	defaultContract           func() (string, string)
	candidateProjects         func(context.Context) ([]string, error)
}

type Config struct {
	Reader                    *dfmaterialization.Reader
	ScopeResolver             *authscope.ScopeResolver
	Logger                    *slog.Logger
	MaxExportRows             int64
	MaxExportBytes            int64
	DefaultRecipe             string
	DefaultTranslationVersion string
	DefaultContract           func() (string, string)
	CandidateProjects         func(context.Context) ([]string, error)
}

func NewService(cfg Config) *Service {
	if cfg.MaxExportRows <= 0 {
		cfg.MaxExportRows = 1_000_000
	}
	if cfg.MaxExportBytes <= 0 {
		cfg.MaxExportBytes = 1 << 30
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Reader != nil {
		cfg.Reader.LegacyTranslationVersion = strings.TrimSpace(cfg.DefaultTranslationVersion)
	}
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver, logger: cfg.Logger, maxExportRows: cfg.MaxExportRows, maxExportBytes: cfg.MaxExportBytes, defaultRecipe: strings.TrimSpace(cfg.DefaultRecipe), defaultTranslationVersion: strings.TrimSpace(cfg.DefaultTranslationVersion), defaultContract: cfg.DefaultContract, candidateProjects: cfg.CandidateProjects}
}

func (s *Service) contract() (string, string) {
	if s.defaultContract != nil {
		recipe, version := s.defaultContract()
		if strings.TrimSpace(recipe) != "" && strings.TrimSpace(version) != "" {
			return strings.TrimSpace(recipe), strings.TrimSpace(version)
		}
	}
	return s.defaultRecipe, s.defaultTranslationVersion
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
	if s.candidateProjects != nil {
		projects, err := s.candidateProjects(ctx)
		if err != nil {
			return nil, mapReaderError(err)
		}
		if len(projects) == 0 {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
		}
		return projects, nil
	}
	projects, err := s.reader.PublishedProjects(ctx)
	if err != nil {
		return nil, mapReaderError(err)
	}
	if len(projects) == 0 {
		// An empty publication catalog is an expected data state, not an
		// authorization failure. Callers can present a useful "not published"
		// state while real source-level access checks still fail closed below.
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
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
	selectors, err := s.reader.CurrentFederatedSelectors(ctx, projects)
	if err != nil {
		return nil, mapReaderError(err)
	}
	result := make([]dfmaterialization.Materialization, 0, len(selectors))
	for _, selector := range selectors {
		if dataset, _, resolveErr := s.authorizedFederation(ctx, principal, selector, nil); resolveErr == nil {
			result = append(result, federatedMaterialization(dataset))
		} else if normalized := dataframeerrors.Normalize(resolveErr); normalized.Code() != string(dataframeerrors.CodeDatasetNotFound) {
			return nil, resolveErr
		}
	}
	return result, nil
}

// ProjectDatasets exposes the exact active-generation schemas for one
// authorized project. It intentionally does not federate across projects.
func (s *Service) ProjectDatasets(ctx context.Context, projectID string) ([]dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	if len(principal.Projects) > 0 && !containsProject(principal.Projects, projectID) {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeUnauthorizedProject, "")
	}
	values, err := s.reader.CurrentProjectDatasets(ctx, projectID)
	if err != nil {
		return nil, mapReaderError(err)
	}
	for _, value := range values {
		if s.scopeResolver != nil {
			if _, scopeErr := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, nil); scopeErr != nil {
				return nil, mapReaderError(scopeErr)
			}
		}
	}
	return values, nil
}

func containsProject(projects []string, project string) bool {
	for _, value := range projects {
		if strings.TrimSpace(value) == project {
			return true
		}
	}
	return false
}

// Get preserves the legacy published-materialization lookup by ID.
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

func (s *Service) Dataset(ctx context.Context, input model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	selector, err := s.resolveSelector(input.Selector, input.DataType)
	if err != nil {
		return nil, err
	}
	dataset, _, err := s.authorizedFederation(ctx, principal, selector, nil)
	if err != nil {
		return nil, mapReaderError(err)
	}
	value := federatedMaterialization(dataset)
	return &value, nil
}

func (s *Service) Rows(ctx context.Context, input model.DataframeRowsInput) (dfmaterialization.Page, error) {
	if s.reader == nil {
		return dfmaterialization.Page{}, readerUnavailable()
	}
	selector, err := s.resolveSelector(input.Selector, input.DataType)
	if err != nil {
		return dfmaterialization.Page{}, err
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		s.logger.Debug("dataframe rows start", "path", "published", "materialization_id", *input.MaterializationID)
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			s.logReadFailure(ctx, "published_materialization_lookup", selector.Output, err, "materialization_id", *input.MaterializationID)
			return dfmaterialization.Page{}, mapReaderError(err)
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.Page{}, err
		}
		page, err := s.reader.PagePublishedID(ctx, value.ID, pageRequest(input))
		if err != nil {
			s.logReadFailure(ctx, "published_clickhouse_rows", selector.Output, err, "materialization_id", value.ID, "physical_table", value.PhysicalTable)
			return dfmaterialization.Page{}, mapReaderError(err)
		}
		return page, nil
	}
	principal, err := s.principal(ctx)
	if err != nil {
		s.logReadFailure(ctx, "principal_resolution", selector.Output, err)
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	dataset, access, err := s.authorizedFederation(ctx, principal, selector, convertFilters(input.Filters))
	if err != nil {
		s.logReadFailure(ctx, "federated_publication_resolution", selector.Output, err)
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	if len(dataset.Sources) == 0 {
		return dfmaterialization.Page{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	page, err := s.reader.PageFederatedDataset(ctx, dataset, dfmaterialization.FederatedPageRequest{
		Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After),
		AccessByProject: access,
	})
	if err != nil {
		s.logReadFailure(ctx, "federated_clickhouse_rows", selector.Output, err, "source_ids", materializationIDs(dataset.Sources), "physical_tables", physicalTables(dataset.Sources))
		return dfmaterialization.Page{}, mapReaderError(err)
	}
	return dfmaterialization.Page{Materialization: federatedMaterialization(dataset), Columns: page.Columns, Rows: page.Rows, TotalCount: page.TotalCount, HasNext: page.HasNext, NextCursor: page.NextCursor}, nil
}

func (s *Service) AggregateInput(ctx context.Context, input model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error) {
	if s.reader == nil {
		return dfmaterialization.AggregateResult{}, readerUnavailable()
	}
	selector, err := s.resolveSelector(input.Selector, input.DataType)
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	if input.MaterializationID != nil && *input.MaterializationID != "" {
		s.logger.Debug("dataframe aggregate start", "path", "published", "materialization_id", *input.MaterializationID, "operation", input.Operation, "group_by", input.GroupBy)
		value, err := s.reader.DatasetByPublishedID(ctx, *input.MaterializationID)
		if err != nil {
			s.logReadFailure(ctx, "published_materialization_lookup", selector.Output, err, "materialization_id", *input.MaterializationID)
			return dfmaterialization.AggregateResult{}, mapReaderError(err)
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return dfmaterialization.AggregateResult{}, err
		}
		result, err := s.reader.AggregatePublishedID(ctx, value.ID, dfmaterialization.AggregateRequest{
			GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column),
		})
		if err != nil {
			s.logReadFailure(ctx, "published_clickhouse_aggregate", selector.Output, err, "materialization_id", value.ID, "physical_table", value.PhysicalTable)
			return dfmaterialization.AggregateResult{}, mapReaderError(err)
		}
		return result, nil
	}
	principal, err := s.principal(ctx)
	if err != nil {
		s.logReadFailure(ctx, "principal_resolution", selector.Output, err)
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	dataset, access, err := s.authorizedFederation(ctx, principal, selector, convertFilters(input.Filters))
	if err != nil {
		s.logReadFailure(ctx, "federated_publication_resolution", selector.Output, err)
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	if len(dataset.Sources) == 0 {
		return dfmaterialization.AggregateResult{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	result, err := s.reader.AggregateFederatedDataset(ctx, dataset, dfmaterialization.FederatedAggregateRequest{
		GroupBy: input.GroupBy, Filters: convertFilters(input.Filters), Operation: input.Operation, Column: stringValue(input.Column), AccessByProject: access,
	})
	if err != nil {
		s.logReadFailure(ctx, "federated_clickhouse_aggregate", selector.Output, err, "source_ids", materializationIDs(dataset.Sources), "physical_tables", physicalTables(dataset.Sources))
		return dfmaterialization.AggregateResult{}, mapReaderError(err)
	}
	result.Dataset = dataset
	return dfmaterialization.AggregateResult{Materialization: federatedMaterialization(result.Dataset), Columns: result.Columns, Rows: result.Rows}, nil
}

func (s *Service) AggregationsInput(ctx context.Context, input model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	if s.reader == nil {
		return dfmaterialization.AggregationsResult{}, readerUnavailable()
	}
	selector, err := s.resolveSelector(input.Selector, input.DataType)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	dataset, access, err := s.authorizedFederation(ctx, principal, selector, convertFilters(input.Filters))
	if err != nil {
		return dfmaterialization.AggregationsResult{}, mapReaderError(err)
	}
	if len(dataset.Sources) == 0 {
		return dfmaterialization.AggregationsResult{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
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
		Filters: convertFilters(input.Filters), Specs: specs, AccessByProject: access,
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

func (s *Service) authorizedFederation(ctx context.Context, principal *authscope.Principal, selector dfmaterialization.DataframeSelector, filters []dfmaterialization.Filter) (dfmaterialization.FederatedDataset, map[string]dfmaterialization.SourceAccess, error) {
	candidates, err := s.projects(ctx, principal)
	if err != nil {
		s.logReadFailure(ctx, "authorized_project_discovery", selector.Output, err)
		return dfmaterialization.FederatedDataset{}, nil, mapReaderError(err)
	}
	candidates = filterProjects(candidates, filters)
	sources, err := s.reader.CurrentFederatedSources(ctx, candidates, selector)
	if err != nil {
		s.logReadFailure(ctx, "published_source_discovery", selector.Output, err, "projects", candidates)
		return dfmaterialization.FederatedDataset{}, nil, mapReaderError(err)
	}
	if len(sources) == 0 {
		dataset, reconcileErr := dfmaterialization.ReconcileFederatedDataset(selector, candidates, nil)
		if statuses, statusErr := s.reader.FederationProjectStatuses(ctx, candidates, selector); statusErr != nil {
			return dfmaterialization.FederatedDataset{}, nil, mapReaderError(statusErr)
		} else if len(statuses) > 0 {
			dataset.ProjectStatuses = filterAuthorizedStatuses(candidates, statuses)
		}
		_, dataset.ActiveContractVersion = s.contract()
		return dataset, map[string]dfmaterialization.SourceAccess{}, reconcileErr
	}
	s.logger.Debug("dataframe publication sources discovered", "request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(), "projects", candidates, "source_count", len(sources), "source_ids", materializationIDs(sources), "physical_tables", physicalTables(sources))
	access := make(map[string]dfmaterialization.SourceAccess, len(sources))
	authorizedSources := make([]dfmaterialization.Materialization, 0, len(sources))
	rowCountComplete := true
	for _, source := range sources {
		if s.scopeResolver != nil {
			scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, source.Project, source.DatasetGeneration, nil)
			if err != nil {
				s.logReadFailure(ctx, "read_scope_resolution", selector.Output, err, "project", source.Project, "dataset_generation", source.DatasetGeneration, "materialization_id", source.ID)
				return dfmaterialization.FederatedDataset{}, nil, mapReaderError(err)
			}
			if !scope.Unrestricted() && len(scope.AuthResourcePaths) == 0 {
				continue
			}
			if !scope.Unrestricted() {
				rowCountComplete = false
			}
			access[source.Project] = dfmaterialization.SourceAccess{ResourcePaths: append([]string(nil), scope.AuthResourcePaths...), Unrestricted: scope.Unrestricted()}
			authorizedSources = append(authorizedSources, source)
			continue
		}
		if source.ScopeUnrestricted {
			access[source.Project] = dfmaterialization.SourceAccess{Unrestricted: true}
			authorizedSources = append(authorizedSources, source)
			continue
		}
		if len(principal.AuthResourcePaths) == 0 {
			continue
		}
		rowCountComplete = false
		access[source.Project] = dfmaterialization.SourceAccess{ResourcePaths: append([]string(nil), principal.AuthResourcePaths...)}
		authorizedSources = append(authorizedSources, source)
	}
	if len(access) == 0 {
		return dfmaterialization.FederatedDataset{}, nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	dataset, err := dfmaterialization.ReconcileFederatedDataset(selector, candidates, authorizedSources)
	if err != nil {
		s.logReadFailure(ctx, "published_schema_reconciliation", selector.Output, err, "source_ids", materializationIDs(authorizedSources), "physical_tables", physicalTables(authorizedSources))
		return dfmaterialization.FederatedDataset{}, nil, err
	}
	if statuses, statusErr := s.reader.FederationProjectStatuses(ctx, candidates, selector); statusErr != nil {
		return dfmaterialization.FederatedDataset{}, nil, mapReaderError(statusErr)
	} else if len(statuses) > 0 {
		dataset.ProjectStatuses = filterAuthorizedStatuses(candidates, statuses)
	}
	discovered := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		discovered[source.Project] = struct{}{}
	}
	for index := range dataset.ProjectStatuses {
		status := &dataset.ProjectStatuses[index]
		if _, ok := discovered[status.ProjectID]; !ok {
			continue
		}
		if _, ok := access[status.ProjectID]; !ok {
			status.State = dfmaterialization.ProjectExcluded
		}
	}
	dataset.RowCountComplete = rowCountComplete
	_, dataset.ActiveContractVersion = s.contract()
	refreshAvailability(&dataset)
	return dataset, access, nil
}

func refreshAvailability(dataset *dfmaterialization.FederatedDataset) {
	if dataset == nil || len(dataset.Sources) == 0 {
		if dataset != nil {
			dataset.Availability = dfmaterialization.FederationUnavailable
		}
		return
	}
	dataset.Availability = dfmaterialization.FederationAvailable
	for _, status := range dataset.ProjectStatuses {
		if status.State != dfmaterialization.ProjectCurrent {
			dataset.Availability = dfmaterialization.FederationDegraded
			return
		}
	}
}

func filterAuthorizedStatuses(projects []string, statuses []dfmaterialization.ProjectStatus) []dfmaterialization.ProjectStatus {
	allowed := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		allowed[project] = struct{}{}
	}
	result := make([]dfmaterialization.ProjectStatus, 0, len(statuses))
	for _, status := range statuses {
		if _, ok := allowed[status.ProjectID]; ok {
			result = append(result, status)
		}
	}
	return result
}

func (s *Service) resolveSelector(input *model.DataframeSelectorInput, dataType *string) (dfmaterialization.DataframeSelector, error) {
	legacy := ""
	if dataType != nil {
		legacy = strings.TrimSpace(*dataType)
	}
	if input != nil && dataType != nil {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input"))
	}
	if input != nil {
		selector := dfmaterialization.DataframeSelector{Recipe: strings.TrimSpace(input.Recipe), TranslationVersion: strings.TrimSpace(input.TranslationVersion), Output: strings.TrimSpace(input.Output)}
		if !selector.Valid() {
			return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input", "selector"))
		}
		return selector, nil
	}
	defaultRecipe, defaultVersion := s.contract()
	if legacy == "" || defaultRecipe == "" || defaultVersion == "" {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input"))
	}
	return dfmaterialization.DataframeSelector{Recipe: defaultRecipe, TranslationVersion: defaultVersion, Output: legacy}, nil
}

func (s *Service) resolvePublishedSelector(input *dfmaterialization.DataframeSelector, dataType string) (dfmaterialization.DataframeSelector, error) {
	legacy := strings.TrimSpace(dataType)
	if input != nil && legacy != "" {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	if input != nil {
		selector := *input
		selector.Recipe, selector.TranslationVersion, selector.Output = strings.TrimSpace(selector.Recipe), strings.TrimSpace(selector.TranslationVersion), strings.TrimSpace(selector.Output)
		if !selector.Valid() {
			return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
		}
		return selector, nil
	}
	defaultRecipe, defaultVersion := s.contract()
	if legacy == "" || defaultRecipe == "" || defaultVersion == "" {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	return dfmaterialization.DataframeSelector{Recipe: defaultRecipe, TranslationVersion: defaultVersion, Output: legacy}, nil
}

// filterProjects applies authoritative project_id equality/IN constraints
// before source discovery and schema reconciliation. Other predicates remain
// in ClickHouse where clauses.
func filterProjects(projects []string, filters []dfmaterialization.Filter) []string {
	var selected map[string]struct{}
	for _, filter := range filters {
		if filter.Column != "project_id" {
			continue
		}
		op := strings.ToUpper(strings.TrimSpace(filter.Op))
		values := make([]string, 0)
		switch op {
		case "=", "EQ":
			if value, ok := filter.Value.(string); ok {
				values = append(values, value)
			}
		case "IN":
			switch typed := filter.Value.(type) {
			case []string:
				values = append(values, typed...)
			case []any:
				for _, item := range typed {
					if value, ok := item.(string); ok {
						values = append(values, value)
					}
				}
			}
		default:
			continue
		}
		current := make(map[string]struct{}, len(values))
		for _, value := range values {
			current[strings.TrimSpace(value)] = struct{}{}
		}
		if selected == nil {
			selected = current
		} else {
			for value := range selected {
				if _, ok := current[value]; !ok {
					delete(selected, value)
				}
			}
		}
	}
	if selected == nil {
		return append([]string(nil), projects...)
	}
	result := make([]string, 0, len(projects))
	for _, project := range projects {
		if _, ok := selected[project]; ok {
			result = append(result, project)
		}
	}
	return result
}

func (s *Service) logReadFailure(ctx context.Context, phase, resourceType string, err error, attrs ...any) {
	fields := []any{"request_id", httpapi.RequestIDFromContext(ctx), "phase", phase, "resource_type", resourceType, "error_chain", errorChain(err)}
	fields = append(fields, attrs...)
	s.logger.Error("dataframe read failed", fields...)
}

func errorChain(err error) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func materializationIDs(values []dfmaterialization.Materialization) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.ID)
	}
	return result
}

func physicalTables(values []dfmaterialization.Materialization) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.PhysicalTable)
	}
	return result
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

func (s *Service) authorizePublished(ctx context.Context, value dfmaterialization.Materialization) error {
	principal, err := s.principal(ctx)
	if err != nil {
		return err
	}
	if err := authscope.AuthorizeProject(principal, value.Project, false); err != nil {
		return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	if s.scopeResolver != nil {
		scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, value.AuthResourcePaths)
		if err != nil {
			return err
		}
		if !value.ScopeUnrestricted && scope.Unrestricted() {
			return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
		}
	}
	return nil
}

func pageRequest(input model.DataframeRowsInput) dfmaterialization.PageRequest {
	return dfmaterialization.PageRequest{Columns: input.Columns, Filters: convertFilters(input.Filters), Sort: convertSort(input), First: intValue(input.First), After: stringValue(input.After)}
}

func federatedMaterialization(dataset dfmaterialization.FederatedDataset) dfmaterialization.Materialization {
	return dfmaterialization.Materialization{ID: "federated:" + dataset.Selector.Key(), Name: dataset.Name, Revision: dataset.Revision, DatasetGeneration: "federated:" + dataset.Revision, State: dfmaterialization.StateReady, Columns: dataset.Columns, RowCount: dataset.RowCount, RowCountKnown: dataset.RowCountComplete, Selector: dataset.Selector, Availability: dataset.Availability, IncludedProjects: len(dataset.Sources), ExpectedProjects: dataset.ExpectedProjects, ProjectStatuses: append([]dfmaterialization.ProjectStatus(nil), dataset.ProjectStatuses...), ActiveContractVersion: dataset.ActiveContractVersion}
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
