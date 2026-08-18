package published

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// FederatedDataset is the logical reader view of one FHIR resource type across all
// authorized project publications. project_id is a public, stable row column;
// legacy outputs that predate it are filled from the source publication project.
type FederatedDataset struct {
	Selector         DataframeSelector
	Name             string
	Revision         string
	Sources          []Materialization
	Columns          []Column
	RowCount         int64
	RowCountComplete bool
	Availability     FederationAvailability
	ExpectedProjects int
	ProjectStatuses  []ProjectStatus
}

type FederationAvailability string

const (
	FederationAvailable   FederationAvailability = "AVAILABLE"
	FederationDegraded    FederationAvailability = "DEGRADED"
	FederationUnavailable FederationAvailability = "UNAVAILABLE"
)

type ProjectState string

const (
	ProjectCurrent  ProjectState = "CURRENT"
	ProjectStale    ProjectState = "STALE"
	ProjectBuilding ProjectState = "BUILDING"
	ProjectFailed   ProjectState = "FAILED"
	ProjectMissing  ProjectState = "MISSING"
	ProjectExcluded ProjectState = "EXCLUDED"
)

type ProjectStatus struct {
	ProjectID   string
	State       ProjectState
	Generation  string
	ExecutionID string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ErrorCode   string
	Retryable   bool
}

// ExecutionSelectorResolver is the narrow integration contract implemented by
// version-aware publication catalogs. It lets federation consume exact
// identities without depending on publication persistence internals.
type ExecutionSelectorResolver interface {
	DataframeSelectorForExecution(context.Context, string, string) (DataframeSelector, error)
}

// ProjectStatusResolver is implemented by the project-release store. The
// caller always passes an authorization-filtered project list.
type ProjectStatusResolver interface {
	DataframeProjectStatuses(context.Context, []string, DataframeSelector) ([]ProjectStatus, error)
}

// ReleaseExecutionResolver selects the immutable execution recorded by each
// active project release. A present project with an empty execution ID means
// the active release intentionally does not expose that selector.
type ReleaseExecutionResolver interface {
	ActiveReleaseExecutionIDs(context.Context, []string, DataframeSelector) (map[string]string, error)
	ActiveReleaseSelectors(context.Context, []string) ([]DataframeSelector, map[string]bool, error)
}

// FederationSnapshotResolver resolves active release execution IDs and their
// project statuses together. This lets interactive reads avoid independently
// rediscovering the same release and publication lifecycle metadata.
type FederationSnapshotResolver interface {
	ResolveFederationSnapshot(context.Context, []string, DataframeSelector) (map[string]string, []ProjectStatus, bool, error)
}

func (r *Reader) FederationProjectStatuses(ctx context.Context, projects []string, selector DataframeSelector) ([]ProjectStatus, error) {
	if r != nil && r.ProjectStatusResolver != nil {
		return r.ProjectStatusResolver.DataframeProjectStatuses(ctx, append([]string(nil), projects...), selector)
	}
	return nil, nil
}

// CurrentFederatedSnapshot resolves sources and project status through the
// combined release fast path when available. The legacy discovery methods are
// retained as a fallback for alternate catalogs and tests.
func (r *Reader) CurrentFederatedSnapshot(ctx context.Context, projects []string, selector DataframeSelector) ([]Materialization, []ProjectStatus, error) {
	projects = normalizedProjects(projects)
	if r != nil && r.FederationSnapshotResolver != nil {
		if r.Catalog == nil {
			return nil, nil, fmt.Errorf("bundle catalog dependency is required")
		}
		executionIDs, statuses, complete, err := r.FederationSnapshotResolver.ResolveFederationSnapshot(ctx, projects, selector)
		if err != nil {
			return nil, nil, err
		}
		if !complete {
			sources, err := r.CurrentFederatedSources(ctx, projects, selector)
			if err != nil {
				return nil, nil, err
			}
			statuses, err := r.FederationProjectStatuses(ctx, projects, selector)
			return sources, statuses, err
		}
		sources := make([]Materialization, 0, len(executionIDs))
		for _, project := range projects {
			executionID := strings.TrimSpace(executionIDs[project])
			if executionID == "" {
				continue
			}
			execution, err := r.Catalog.GetExecution(ctx, executionID)
			if err != nil {
				return nil, nil, err
			}
			if execution.Project != project || !execution.State.Successful() {
				continue
			}
			for _, output := range execution.Outputs {
				if output.Name != selector.Output {
					continue
				}
				executionSelector := output.Selector
				if !executionSelector.Valid() {
					executionSelector = execution.Selector(selector.Output)
				}
				if executionSelector != selector {
					continue
				}
				materialization := publishedMaterialization(execution, output, selector.Output)
				materialization.Selector = selector
				sources = append(sources, materialization)
				break
			}
		}
		return sources, statuses, nil
	}
	sources, err := r.CurrentFederatedSources(ctx, projects, selector)
	if err != nil {
		return nil, nil, err
	}
	statuses, err := r.FederationProjectStatuses(ctx, projects, selector)
	return sources, statuses, err
}

// SourceAccess is the already-resolved visibility for one published project.
// Missing entries are intentionally treated as restricted with no paths.
type SourceAccess struct {
	ResourcePaths []string
	Unrestricted  bool
}

// FederatedPageRequest is the projectless reader request. The caller supplies
// the already-resolved effective scope; this type never accepts a project
// selector from the browser.
type FederatedPageRequest struct {
	Columns         []string
	Filters         []Filter
	Sort            *Sort
	First           int
	After           string
	AccessByProject map[string]SourceAccess
}

// FederatedPage is the projectless row response. Source metadata is retained
// only for internal cursor/revision handling.
type FederatedPage struct {
	Dataset    FederatedDataset
	Columns    []string
	Rows       []map[string]any
	TotalCount int64
	HasNext    bool
	NextCursor string
}

// FederatedStreamRequest describes a bounded-memory scan over the same
// authorized source union used by interactive row reads.
type FederatedStreamRequest struct {
	Columns         []string
	Filters         []Filter
	Sort            *Sort
	AccessByProject map[string]SourceAccess
}

func normalizedProjects(projects []string) []string {
	seen := make(map[string]struct{}, len(projects))
	result := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		result = append(result, project)
	}
	sort.Strings(result)
	return result
}

func hasOutputResourceType(execution bundlepublication.BundleExecution, resourceType string) bool {
	for _, output := range execution.Outputs {
		if output.Name == resourceType {
			return true
		}
	}
	return false
}

func reconcileFederatedColumns(sources []Materialization) ([]Column, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("federation has no sources")
	}
	type parsedType struct {
		base            string
		array, nullable bool
	}
	parse := func(value string) (parsedType, error) {
		value = strings.TrimSpace(value)
		result := parsedType{}
		if strings.HasPrefix(value, "Nullable(") && strings.HasSuffix(value, ")") {
			result.nullable = true
			value = value[len("Nullable(") : len(value)-1]
		}
		if strings.HasPrefix(value, "Array(") && strings.HasSuffix(value, ")") {
			result.array = true
			value = value[len("Array(") : len(value)-1]
		}
		if value == "" || strings.HasPrefix(value, "Nullable(") || strings.HasPrefix(value, "Array(") {
			return result, fmt.Errorf("invalid ClickHouse type %q", value)
		}
		result.base = value
		return result, nil
	}
	all := map[string]parsedType{}
	typeSources := map[string]map[string][]string{}
	presenceCount := map[string]int{}
	firstOrder := make([]string, 0)
	firstIndex := map[string]int{}
	for sourceIndex, source := range sources {
		seen := map[string]struct{}{}
		for _, column := range source.Columns {
			if column.Name == "__loom_row_id" {
				continue
			}
			if _, ok := seen[column.Name]; ok {
				return nil, fmt.Errorf("duplicate column %q in source %q", column.Name, source.ID)
			}
			seen[column.Name] = struct{}{}
			presenceCount[column.Name]++
			parsed, err := parse(column.ClickHouse)
			if err != nil {
				return nil, err
			}
			if sourceIndex == 0 {
				firstIndex[column.Name] = len(firstOrder)
				firstOrder = append(firstOrder, column.Name)
			}
			if previous, ok := all[column.Name]; ok {
				if previous.base != parsed.base || previous.array != parsed.array {
					projects := make([]string, 0)
					types := make([]string, 0)
					for typ, values := range typeSources[column.Name] {
						types = append(types, typ)
						projects = append(projects, values...)
					}
					types = append(types, column.ClickHouse)
					projects = append(projects, source.Project)
					sort.Strings(types)
					sort.Strings(projects)
					return nil, dataframeerrors.NewError(dataframeerrors.CodeFederationIncompatible, "", dataframeerrors.WithDetails(map[string]any{"column": column.Name, "projects": projects, "types": types}))
				}
				previous.nullable = previous.nullable || parsed.nullable
				all[column.Name] = previous
			} else {
				all[column.Name] = parsed
			}
			if typeSources[column.Name] == nil {
				typeSources[column.Name] = map[string][]string{}
			}
			typeSources[column.Name][column.ClickHouse] = append(typeSources[column.Name][column.ClickHouse], source.Project)
		}
	}
	newNames := make([]string, 0)
	// project_id is part of Loom's dataframe contract. Keep it available when
	// reconciling legacy publications whose physical table predates the column.
	all[projectIDColumn] = parsedType{base: "String"}
	for name := range all {
		if _, ok := firstIndex[name]; !ok {
			newNames = append(newNames, name)
		}
	}
	sort.Strings(newNames)
	order := append(firstOrder, newNames...)
	result := make([]Column, 0, len(order))
	for _, name := range order {
		parsed := all[name]
		if name != projectIDColumn && presenceCount[name] < len(sources) && !parsed.array {
			parsed.nullable = true
		}
		typ := parsed.base
		if parsed.array {
			typ = "Array(" + typ + ")"
		}
		if parsed.nullable && !parsed.array {
			typ = "Nullable(" + typ + ")"
		}
		result = append(result, Column{Name: name, ClickHouse: typ})
	}
	return result, nil
}

func nowPlusSecond() time.Time {
	return time.Now().UTC().Add(time.Second)
}

func validFederationClickHouseType(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "Nullable(") && strings.HasSuffix(value, ")") {
		value = value[len("Nullable(") : len(value)-1]
	}
	if strings.HasPrefix(value, "Array(") && strings.HasSuffix(value, ")") {
		value = value[len("Array(") : len(value)-1]
	}
	return value != "" && !strings.HasPrefix(value, "Nullable(") && !strings.HasPrefix(value, "Array(")
}

// CurrentFederatedSources returns the exact READY pointer-backed publications
// without touching schema reconciliation. It is the authorization snapshot
// boundary used by GraphQL and export callers.
func (r *Reader) CurrentFederatedSources(ctx context.Context, projects []string, selector DataframeSelector) ([]Materialization, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	projects = normalizedProjects(projects)
	if len(projects) == 0 || !selector.Valid() {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	executions, err := r.Catalog.ListExecutions(ctx, bundlepublication.BundlePublished, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	activeGenerations := map[string]string{}
	releaseExecutions := map[string]string{}
	if r.ReleaseExecutionResolver != nil {
		releaseExecutions, err = r.ReleaseExecutionResolver.ActiveReleaseExecutionIDs(ctx, projects, selector)
		if err != nil {
			return nil, err
		}
	}
	if r.ActiveManifestResolver != nil {
		for _, project := range projects {
			manifest, resolveErr := publication.ResolveActive(ctx, r.ActiveManifestResolver, project)
			if errors.Is(resolveErr, publication.ErrNoActiveGeneration) {
				continue
			}
			if resolveErr != nil {
				return nil, resolveErr
			}
			activeGenerations[project] = manifest.Dataset.Generation
		}
	}
	allowed := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		allowed[project] = struct{}{}
	}
	selected := map[string]bundlepublication.BundleExecution{}
	for _, execution := range executions {
		if !execution.State.Successful() {
			continue
		}
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		releaseExecutionID, releaseControlled := releaseExecutions[execution.Project]
		if releaseControlled {
			if releaseExecutionID == "" || releaseExecutionID != execution.ID {
				continue
			}
		} else if r.ActiveManifestResolver != nil {
			generation, ok := activeGenerations[execution.Project]
			if ok {
				if execution.DatasetGeneration != generation {
					continue
				}
			} else if execution.DatasetGeneration != "" {
				continue
			}
		}
		if !releaseControlled {
			pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
			if pointerErr != nil {
				return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
			}
			if pointer.ExecutionID != execution.ID {
				continue
			}
		}
		// Output absence is a normal selector miss, not a catalog failure. In
		// particular, a newer recipe version may replace Patient with
		// ResearchSubject while both executions remain in publication history.
		// Do not ask version-aware catalogs to resolve metadata for an output the
		// execution never published.
		if !hasOutputResourceType(execution, selector.Output) {
			continue
		}
		executionSelector, selectorErr := r.selectorForExecution(ctx, execution, selector.Output)
		if selectorErr != nil {
			// A stale active-release pointer must degrade that project, not make
			// every other authorized project's valid publication unreadable.
			// Status resolution still reports the missing project to callers.
			if errors.Is(selectorErr, bundlepublication.ErrBundleNotFound) {
				continue
			}
			return nil, selectorErr
		}
		if executionSelector != selector {
			continue
		}
		if current, ok := selected[execution.Project]; ok && current.ID != execution.ID {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeFederationIncompatible, "", dataframeerrors.WithDetails(map[string]any{"project": execution.Project, "selector": selector.Key(), "executions": []string{current.ID, execution.ID}}))
		}
		selected[execution.Project] = execution
	}
	result := make([]Materialization, 0, len(selected))
	for _, project := range projects {
		execution, ok := selected[project]
		if !ok {
			continue
		}
		for _, output := range execution.Outputs {
			if output.Name == selector.Output {
				materialization := publishedMaterialization(execution, output, selector.Output)
				materialization.Selector = selector
				result = append(result, materialization)
				break
			}
		}
	}
	if len(result) == 0 {
		return []Materialization{}, nil
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Project != result[j].Project {
			return result[i].Project < result[j].Project
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Reader) selectorForExecution(ctx context.Context, execution bundlepublication.BundleExecution, output string) (DataframeSelector, error) {
	if resolver, ok := r.Catalog.(ExecutionSelectorResolver); ok {
		selector, err := resolver.DataframeSelectorForExecution(ctx, execution.ID, output)
		if err != nil {
			return DataframeSelector{}, err
		}
		if selector.Valid() {
			return selector, nil
		}
		return DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	selector := DataframeSelector{Recipe: execution.Name, TranslationVersion: strings.TrimSpace(execution.TranslationVersion), Output: output}
	if !selector.Valid() {
		return DataframeSelector{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidSelector, "")
	}
	return selector, nil
}

func (r *Reader) CurrentFederatedResourceTypes(ctx context.Context, projects []string) ([]string, error) {
	selectors, err := r.CurrentFederatedSelectors(ctx, projects)
	if err != nil {
		return nil, err
	}
	resourceTypes := map[string]struct{}{}
	for _, selector := range selectors {
		if fhirschema.HasResource(selector.Output) {
			resourceTypes[selector.Output] = struct{}{}
		}
	}
	result := make([]string, 0, len(resourceTypes))
	for resourceType := range resourceTypes {
		result = append(result, resourceType)
	}
	sort.Strings(result)
	return result, nil
}

// CurrentFederatedSelectors discovers pointer-backed datasets by their full
// identity. Output-name-only discovery is intentionally not used here.
func (r *Reader) CurrentFederatedSelectors(ctx context.Context, projects []string) ([]DataframeSelector, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	projects = normalizedProjects(projects)
	allowed := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		allowed[project] = struct{}{}
	}
	activeGenerations := map[string]string{}
	controlledProjects := map[string]bool{}
	selectors := map[string]DataframeSelector{}
	if r.ReleaseExecutionResolver != nil {
		releaseSelectors, controlled, releaseErr := r.ReleaseExecutionResolver.ActiveReleaseSelectors(ctx, projects)
		if releaseErr != nil {
			return nil, releaseErr
		}
		controlledProjects = controlled
		for _, selector := range releaseSelectors {
			selectors[selector.Key()] = selector
		}
	}
	if r.ActiveManifestResolver != nil {
		for _, project := range projects {
			manifest, resolveErr := publication.ResolveActive(ctx, r.ActiveManifestResolver, project)
			if errors.Is(resolveErr, publication.ErrNoActiveGeneration) {
				continue
			}
			if resolveErr != nil {
				return nil, resolveErr
			}
			activeGenerations[project] = manifest.Dataset.Generation
		}
	}
	executions, err := r.Catalog.ListExecutions(ctx, bundlepublication.BundlePublished, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	for _, execution := range executions {
		if _, ok := allowed[execution.Project]; !ok {
			continue
		}
		if controlledProjects[execution.Project] {
			continue
		}
		if r.ActiveManifestResolver != nil {
			generation, ok := activeGenerations[execution.Project]
			if (ok && execution.DatasetGeneration != generation) || (!ok && execution.DatasetGeneration != "") {
				continue
			}
		}
		pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return nil, pointerErr
		}
		if pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			selector, selectorErr := r.selectorForExecution(ctx, execution, output.Name)
			if selectorErr != nil {
				return nil, selectorErr
			}
			if !selector.Valid() {
				continue
			}
			selectors[selector.Key()] = selector
		}
	}
	result := make([]DataframeSelector, 0, len(selectors))
	for _, selector := range selectors {
		result = append(result, selector)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result, nil
}

// CurrentProjectDatasets returns the current output materializations for one
// project without merging them with other projects.
func (r *Reader) CurrentProjectDatasets(ctx context.Context, project string) ([]Materialization, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	selectors, err := r.CurrentFederatedSelectors(ctx, []string{project})
	if err != nil {
		return nil, err
	}
	result := make([]Materialization, 0, len(selectors))
	for _, selector := range selectors {
		sources, sourceErr := r.CurrentFederatedSources(ctx, []string{project}, selector)
		if sourceErr != nil {
			return nil, sourceErr
		}
		result = append(result, sources...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	if len(result) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	return result, nil
}

func ReconcileFederatedDataset(selector DataframeSelector, expectedProjects []string, sources []Materialization) (FederatedDataset, error) {
	if len(sources) == 0 {
		statuses := make([]ProjectStatus, 0, len(expectedProjects))
		for _, project := range expectedProjects {
			statuses = append(statuses, ProjectStatus{ProjectID: project, State: ProjectMissing})
		}
		return FederatedDataset{Selector: selector, Name: selector.Output, Availability: FederationUnavailable, ExpectedProjects: len(expectedProjects), ProjectStatuses: statuses}, nil
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Project != sources[j].Project {
			return sources[i].Project < sources[j].Project
		}
		return sources[i].ID < sources[j].ID
	})
	validSources := make([]Materialization, 0, len(sources))
	excluded := map[string]string{}
	for _, source := range sources {
		seen := map[string]struct{}{}
		valid := strings.TrimSpace(source.PhysicalTable) != ""
		for _, column := range source.Columns {
			if strings.TrimSpace(column.Name) == "" || !validFederationClickHouseType(column.ClickHouse) {
				valid = false
				break
			}
			if _, ok := seen[column.Name]; ok {
				valid = false
				break
			}
			seen[column.Name] = struct{}{}
		}
		if !valid {
			excluded[source.Project] = string(dataframeerrors.CodeRecipeContractViolation)
			continue
		}
		validSources = append(validSources, source)
	}
	if len(validSources) == 0 {
		statuses := make([]ProjectStatus, 0, len(expectedProjects))
		for _, project := range expectedProjects {
			statuses = append(statuses, ProjectStatus{ProjectID: project, State: ProjectExcluded, ErrorCode: excluded[project]})
		}
		return FederatedDataset{Selector: selector, Name: selector.Output, Availability: FederationUnavailable, ExpectedProjects: len(expectedProjects), ProjectStatuses: statuses}, nil
	}
	sources = validSources
	columns, err := reconcileFederatedColumns(sources)
	if err != nil {
		if _, ok := dataframeerrors.AsUserError(err); ok {
			return FederatedDataset{}, err
		}
		return FederatedDataset{}, dataframeerrors.Wrap(err, dataframeerrors.CodeFederationIncompatible, "")
	}
	parts := make([]string, 0, len(sources))
	var rowCount int64
	for _, source := range sources {
		rowCount += source.RowCount
		parts = append(parts, source.ID, source.DatasetGeneration, source.PhysicalTable)
	}
	for _, column := range columns {
		parts = append(parts, column.Name, column.ClickHouse)
	}
	sort.Strings(parts)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	statuses := make([]ProjectStatus, 0, len(expectedProjects))
	byProject := make(map[string]Materialization, len(sources))
	for _, source := range sources {
		byProject[source.Project] = source
	}
	for _, project := range expectedProjects {
		status := ProjectStatus{ProjectID: project, State: ProjectMissing}
		if code, ok := excluded[project]; ok {
			status.State, status.ErrorCode = ProjectExcluded, code
		}
		if source, ok := byProject[project]; ok {
			status.State, status.Generation, status.ExecutionID, status.CreatedAt, status.UpdatedAt = ProjectCurrent, source.DatasetGeneration, source.ID, source.CreatedAt, source.UpdatedAt
			if status.UpdatedAt.IsZero() && source.ReadyAt != nil {
				status.UpdatedAt = *source.ReadyAt
			}
		}
		statuses = append(statuses, status)
	}
	availability := FederationAvailable
	if len(sources) < len(expectedProjects) {
		availability = FederationDegraded
	}
	return FederatedDataset{Selector: selector, Name: selector.Output, Revision: hex.EncodeToString(digest[:]), Sources: append([]Materialization(nil), sources...), Columns: columns, RowCount: rowCount, Availability: availability, ExpectedProjects: len(expectedProjects), ProjectStatuses: statuses}, nil
}

// PublishedProjects returns project identities that have a current READY
// generation. It is used only as a candidate set when the authenticator does
// not embed project claims; row/source authorization still happens per source.
func (r *Reader) PublishedProjects(ctx context.Context) ([]string, error) {
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	executions, err := r.Catalog.ListExecutions(ctx, bundlepublication.BundlePublished, nowPlusSecond())
	if err != nil {
		return nil, err
	}
	projects := make(map[string]struct{})
	for _, execution := range executions {
		if !execution.State.Successful() || execution.Project == "" {
			continue
		}
		pointer, pointerErr := r.Catalog.GetPointer(ctx, execution.PointerName())
		if pointerErr != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", pointerErr)
		}
		if pointer.ExecutionID == execution.ID {
			projects[execution.Project] = struct{}{}
		}
	}
	result := make([]string, 0, len(projects))
	for project := range projects {
		result = append(result, project)
	}
	sort.Strings(result)
	return result, nil
}

func (r *Reader) PageFederatedDataset(ctx context.Context, dataset FederatedDataset, req FederatedPageRequest) (FederatedPage, error) {
	if r == nil || r.ClickHouse == nil {
		return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	columns, allowed, err := federatedColumns(dataset, req.Columns, req.Sort)
	if err != nil {
		return FederatedPage{}, err
	}
	first := req.First
	if first <= 0 {
		first = 100
	}
	if r.MaxPage > 0 && first > r.MaxPage {
		first = r.MaxPage
	}
	cursor, err := decodeCursor(req.After)
	if err != nil {
		return FederatedPage{}, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidCursor, "")
	}
	unionColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(unionColumns, req.Sort.Column) {
		unionColumns = append(unionColumns, req.Sort.Column)
	}
	for _, filter := range req.Filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, unionArgs, err := federatedNormalizedUnion(dataset, unionColumns, req.AccessByProject)
	if err != nil {
		return FederatedPage{}, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return FederatedPage{}, err
	}
	queryColumns := append([]string(nil), unionColumns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	selectColumns := quotedColumns(queryColumns) + ", count() OVER() AS `__loom_total`"
	readColumns := append(append([]string(nil), queryColumns...), "__loom_total")
	counted := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", selectColumns, union)
	queryArgs := append([]any(nil), unionArgs...)
	queryArgs = append(queryArgs, whereArgs...)
	if len(where) > 0 {
		counted += " WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_counted", quotedColumns(readColumns), counted)
	if cursor != nil {
		cursorWhere, cursorArgs, err := federatedCursorPredicate(cursor, req.Sort)
		if err != nil {
			return FederatedPage{}, err
		}
		query += " WHERE " + cursorWhere
		queryArgs = append(queryArgs, cursorArgs...)
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_global_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_global_row_id` ASC"
	}
	query += fmt.Sprintf(" LIMIT %d", first+1)
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, readColumns, queryArgs...)
	if err != nil {
		return FederatedPage{}, backendCallError(err)
	}
	var count int64
	if len(rows) > 0 {
		count, err = numericCount(rows[0]["__loom_total"])
		if err != nil {
			return FederatedPage{}, err
		}
	} else {
		// A window has no carrier row for an empty page (including an after
		// cursor beyond the end), so only that case needs the count fallback.
		count, err = r.federatedCountDataset(ctx, dataset, allowed, req)
		if err != nil {
			return FederatedPage{}, err
		}
	}
	hasNext := len(rows) > first
	if hasNext {
		rows = rows[:first]
	}
	next := ""
	if hasNext && len(rows) > 0 {
		last := rows[len(rows)-1]
		rowID := fmt.Sprint(last["__loom_global_row_id"])
		var sortValue any
		if req.Sort != nil {
			sortValue = last[req.Sort.Column]
			if sortValue == nil {
				return FederatedPage{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
			}
		}
		next = encodeCursor(rowID, sortValue)
	}
	for _, row := range rows {
		delete(row, "__loom_total")
		delete(row, "__loom_row_id")
		delete(row, "__loom_global_row_id")
		for _, column := range unionColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
	}
	return FederatedPage{Dataset: dataset, Columns: columns, Rows: rows, TotalCount: count, HasNext: hasNext, NextCursor: next}, nil
}

func (r *Reader) StreamFederatedDataset(ctx context.Context, dataset FederatedDataset, req FederatedStreamRequest, visit func(map[string]any) error) (FederatedDataset, error) {
	if r == nil || r.ClickHouse == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if visit == nil {
		return FederatedDataset{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	columns, allowed, err := federatedColumns(dataset, req.Columns, req.Sort)
	if err != nil {
		return FederatedDataset{}, err
	}
	unionColumns := append([]string(nil), columns...)
	if req.Sort != nil && !contains(unionColumns, req.Sort.Column) {
		unionColumns = append(unionColumns, req.Sort.Column)
	}
	for _, filter := range req.Filters {
		if !contains(unionColumns, filter.Column) {
			unionColumns = append(unionColumns, filter.Column)
		}
	}
	union, args, err := federatedNormalizedUnion(dataset, unionColumns, req.AccessByProject)
	if err != nil {
		return FederatedDataset{}, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return FederatedDataset{}, err
	}
	args = append(args, whereArgs...)
	queryColumns := append([]string(nil), unionColumns...)
	queryColumns = append(queryColumns, "__loom_row_id", "__loom_global_row_id")
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_federated", quotedColumns(queryColumns), union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if req.Sort != nil {
		direction := "ASC"
		if req.Sort.Desc {
			direction = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY `%s` %s, `__loom_global_row_id` ASC", req.Sort.Column, direction)
	} else {
		query += " ORDER BY `__loom_global_row_id` ASC"
	}
	err = r.ClickHouse.QueryRowsArgsVisit(ctx, query, queryColumns, func(row map[string]any) error {
		delete(row, "__loom_row_id")
		delete(row, "__loom_global_row_id")
		for _, column := range unionColumns {
			if !contains(columns, column) {
				delete(row, column)
			}
		}
		return visit(row)
	}, args...)
	if err != nil {
		return FederatedDataset{}, backendCallError(err)
	}
	return dataset, nil
}

func (r *Reader) federatedCountDataset(ctx context.Context, dataset FederatedDataset, allowed map[string]struct{}, req FederatedPageRequest) (int64, error) {
	union, args, err := federatedNormalizedUnion(dataset, datasetVisibleColumns(dataset), req.AccessByProject)
	if err != nil {
		return 0, err
	}
	where, whereArgs, err := buildWhere(req.Filters, allowed)
	if err != nil {
		return 0, err
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("SELECT count() AS `__loom_total` FROM (%s) AS __loom_federated", union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := r.ClickHouse.QueryRowsArgs(ctx, query, []string{"__loom_total"}, args...)
	if err != nil {
		return 0, backendCallError(err)
	}
	var total int64
	for _, row := range rows {
		value, err := numericCount(row["__loom_total"])
		if err != nil {
			return 0, err
		}
		total += value
	}
	return total, nil
}

func datasetVisibleColumns(dataset FederatedDataset) []string {
	result := make([]string, 0, len(dataset.Columns))
	for _, column := range dataset.Columns {
		if column.Name != authResourcePathColumn && column.Name != "__loom_row_id" {
			result = append(result, column.Name)
		}
	}
	return result
}

func federatedColumns(dataset FederatedDataset, requested []string, sort *Sort) ([]string, map[string]struct{}, error) {
	for _, column := range requested {
		if column == authResourcePathColumn {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	allowed := make(map[string]struct{}, len(dataset.Columns))
	columns := append([]string(nil), requested...)
	for _, column := range dataset.Columns {
		if column.Name == "__loom_row_id" {
			continue
		}
		allowed[column.Name] = struct{}{}
		if len(requested) == 0 {
			if column.Name != authResourcePathColumn {
				columns = append(columns, column.Name)
			}
		}
	}
	if err := validateReaderColumns(columns, allowed); err != nil {
		return nil, nil, err
	}
	if sort != nil {
		if sort.Column == authResourcePathColumn {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		if err := validateReaderColumns([]string{sort.Column}, allowed); err != nil {
			return nil, nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidRequest, "")
		}
	}
	return columns, allowed, nil
}

// federatedNormalizedUnion creates the single typed source union used by rows,
// counts and aggregates. User predicates are deliberately applied by callers
// around this union; only per-source authorization remains inside branches.
func federatedNormalizedUnion(dataset FederatedDataset, columns []string, accessByProject map[string]SourceAccess) (string, []any, error) {
	branches := make([]string, 0, len(dataset.Sources))
	args := make([]any, 0)
	for _, source := range dataset.Sources {
		present := make(map[string]Column, len(source.Columns))
		for _, column := range source.Columns {
			present[column.Name] = column
		}
		selects := make([]string, 0, len(columns)+2)
		for _, column := range columns {
			target, ok := findColumn(dataset.Columns, column)
			if !ok {
				return "", nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
			}
			if column == projectIDColumn {
				// The catalog project is authoritative, including for legacy tables
				// without a physical project_id column.
				selects = append(selects, fmt.Sprintf("CAST(? AS %s) AS `project_id`", target.ClickHouse))
				args = append(args, source.Project)
			} else if sourceColumn, exists := present[column]; exists {
				selects = append(selects, fmt.Sprintf("CAST(`%s` AS %s) AS `%s`", sourceColumn.Name, target.ClickHouse, column))
			} else if strings.HasPrefix(target.ClickHouse, "Array(") {
				selects = append(selects, fmt.Sprintf("CAST([] AS %s) AS `%s`", target.ClickHouse, column))
			} else {
				typ := target.ClickHouse
				if !strings.HasPrefix(typ, "Nullable(") {
					typ = "Nullable(" + typ + ")"
				}
				selects = append(selects, fmt.Sprintf("CAST(NULL AS %s) AS `%s`", typ, column))
			}
		}
		selects = append(selects, "toString(`__loom_row_id`) AS `__loom_row_id`", "concat(?, ':', toString(`__loom_row_id`)) AS `__loom_global_row_id`")
		branch := "SELECT " + strings.Join(selects, ", ") + " FROM `" + source.PhysicalTable + "`"
		access := accessByProject[source.Project]
		if !access.Unrestricted {
			if _, ok := present[authResourcePathColumn]; ok {
				branch += " WHERE `auth_resource_path` IN ?"
				args = append(args, source.ID, access.ResourcePaths)
			} else {
				branch += " WHERE 0"
				args = append(args, source.ID)
			}
		} else {
			args = append(args, source.ID)
		}
		branches = append(branches, branch)
	}
	return strings.Join(branches, " UNION ALL "), args, nil
}

func validateReaderColumns(columns []string, allowed map[string]struct{}) error {
	for _, column := range columns {
		if column == "__loom_row_id" || column == "__loom_global_row_id" {
			return fmt.Errorf("column %q is internal to dataframe pagination", column)
		}
		if _, ok := allowed[column]; !ok {
			return fmt.Errorf("column %q is not in federated dataset schema", column)
		}
	}
	return nil
}

func quotedColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = fmt.Sprintf("`%s`", column)
	}
	return strings.Join(quoted, ", ")
}

func shortQueryID(query string) string {
	digest := sha256.Sum256([]byte(query))
	return hex.EncodeToString(digest[:8])
}

func federatedCursorPredicate(cursor *pageCursor, sort *Sort) (string, []any, error) {
	if cursor == nil || cursor.RowID == "" {
		return "", nil, fmt.Errorf("invalid federated cursor")
	}
	if sort == nil {
		return "`__loom_global_row_id` > ?", []any{cursor.RowID}, nil
	}
	if cursor.SortValue == nil {
		return "", nil, fmt.Errorf("federated keyset cursor is missing sort value")
	}
	op := ">"
	if sort.Desc {
		op = "<"
	}
	return fmt.Sprintf("(`%s` %s ? OR (`%s` = ? AND `__loom_global_row_id` > ?))", sort.Column, op, sort.Column), []any{cursor.SortValue, cursor.SortValue, cursor.RowID}, nil
}
