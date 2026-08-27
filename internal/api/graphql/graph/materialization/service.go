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
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type Service struct {
	reader            *dfmaterialization.Reader
	scopeResolver     *authscope.ScopeResolver
	logger            *slog.Logger
	maxExportRows     int64
	maxExportBytes    int64
	candidateProjects func(context.Context) ([]string, error)
}

type Config struct {
	Reader            *dfmaterialization.Reader
	ScopeResolver     *authscope.ScopeResolver
	Logger            *slog.Logger
	MaxExportRows     int64
	MaxExportBytes    int64
	CandidateProjects func(context.Context) ([]string, error)
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
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver, logger: cfg.Logger, maxExportRows: cfg.MaxExportRows, maxExportBytes: cfg.MaxExportBytes, candidateProjects: cfg.CandidateProjects}
}

func (s *Service) principal(ctx context.Context) (*authscope.Principal, error) {
	principal, ok := authscope.PrincipalFromContext(ctx)
	if !ok || principal == nil {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	}
	return principal, nil
}

func (s *Service) projectsUncached(ctx context.Context, principal *authscope.Principal) ([]string, error) {
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

func (s *Service) Dataset(ctx context.Context, input model.DataframeDatasetInput) (*dfmaterialization.Materialization, error) {
	if s.reader == nil {
		return nil, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	selector, err := resolveSelector(input.Selector)
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
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.Page{}, err
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
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.AggregateResult{}, err
	}
	result := s.submitAggregateCall(ctx, &aggregateCall{kind: aggregateCallLegacy, selector: selector, filters: convertFilters(input.Filters), legacy: input})
	if result.err != nil {
		s.logReadFailure(ctx, "federated_clickhouse_aggregate", selector.Output, result.err)
	}
	return result.legacy, result.err
}

func (s *Service) AggregationsInput(ctx context.Context, input model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error) {
	selector, err := resolveSelector(input.Selector)
	if err != nil {
		return dfmaterialization.AggregationsResult{}, err
	}
	result := s.submitAggregateCall(ctx, &aggregateCall{kind: aggregateCallRich, selector: selector, filters: convertFilters(input.Filters), rich: input})
	return result.rich, result.err
}

func AggregationsJSON(result dfmaterialization.AggregationsResult) (json.RawMessage, error) {
	return json.Marshal(dfmaterialization.NormalizeAggregationResults(result.Aggregations))
}

func (s *Service) authorizedFederationUncached(ctx context.Context, principal *authscope.Principal, selector dfmaterialization.DataframeSelector, candidates []string) (dfmaterialization.FederatedDataset, map[string]dfmaterialization.SourceAccess, error) {
	started := time.Now()
	sources, statuses, err := s.reader.CurrentFederatedSnapshot(ctx, candidates, selector)
	snapshotElapsed := time.Since(started)
	if err != nil {
		s.logReadFailure(ctx, "published_source_discovery", selector.Output, err, "projects", candidates)
		return dfmaterialization.FederatedDataset{}, nil, mapReaderError(err)
	}
	if len(sources) == 0 {
		dataset, reconcileErr := dfmaterialization.ReconcileFederatedDataset(selector, candidates, nil)
		if len(statuses) > 0 {
			dataset.ProjectStatuses = filterAuthorizedStatuses(candidates, statuses)
		}
		return dataset, map[string]dfmaterialization.SourceAccess{}, reconcileErr
	}
	s.logger.Debug("dataframe publication sources discovered", "request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(), "projects", candidates, "source_count", len(sources), "source_ids", materializationIDs(sources), "physical_tables", physicalTables(sources))
	access := make(map[string]dfmaterialization.SourceAccess, len(sources))
	authorizedSources := make([]dfmaterialization.Materialization, 0, len(sources))
	rowCountComplete := true
	authorizationStarted := time.Now()
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
	authorizationElapsed := time.Since(authorizationStarted)
	reconcileStarted := time.Now()
	dataset, err := dfmaterialization.ReconcileFederatedDataset(selector, candidates, authorizedSources)
	if err != nil {
		s.logReadFailure(ctx, "published_schema_reconciliation", selector.Output, err, "source_ids", materializationIDs(authorizedSources), "physical_tables", physicalTables(authorizedSources))
		return dfmaterialization.FederatedDataset{}, nil, err
	}
	if len(statuses) > 0 {
		dataset.ProjectStatuses = filterAuthorizedStatuses(candidates, statuses)
	}
	reconciliationElapsed := time.Since(reconcileStarted)
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
	refreshAvailability(&dataset)
	s.logger.Info("dataframe federation metadata resolved",
		"request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(),
		"candidate_project_count", len(candidates), "source_count", len(sources),
		"snapshot_ms", snapshotElapsed.Milliseconds(),
		"authorization_ms", authorizationElapsed.Milliseconds(),
		"reconciliation_ms", reconciliationElapsed.Milliseconds(),
	)
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

func resolveSelector(input *model.DataframeSelectorInput) (dfmaterialization.DataframeSelector, error) {
	if input == nil {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input", "selector"))
	}
	selector := dfmaterialization.DataframeSelector{Recipe: strings.TrimSpace(input.Recipe), TranslationVersion: strings.TrimSpace(input.TranslationVersion), Output: strings.TrimSpace(input.Output)}
	if !selector.Valid() {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "", dataframeerrors.WithFieldPath("input", "selector"))
	}
	return selector, nil
}

func resolvePublishedSelector(input *dfmaterialization.DataframeSelector) (dfmaterialization.DataframeSelector, error) {
	if input == nil {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	selector := *input
	selector.Recipe, selector.TranslationVersion, selector.Output = strings.TrimSpace(selector.Recipe), strings.TrimSpace(selector.TranslationVersion), strings.TrimSpace(selector.Output)
	if !selector.Valid() {
		return dfmaterialization.DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	return selector, nil
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

func federatedMaterialization(dataset dfmaterialization.FederatedDataset) dfmaterialization.Materialization {
	return dfmaterialization.Materialization{ID: "federated:" + dataset.Selector.Key(), Name: dataset.Name, Revision: dataset.Revision, DatasetGeneration: "federated:" + dataset.Revision, State: dfmaterialization.StateReady, Columns: dataset.Columns, RowCount: dataset.RowCount, RowCountKnown: dataset.RowCountComplete, Selector: dataset.Selector, Availability: dataset.Availability, IncludedProjects: len(dataset.Sources), ExpectedProjects: dataset.ExpectedProjects, ProjectStatuses: append([]dfmaterialization.ProjectStatus(nil), dataset.ProjectStatuses...)}
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
