package queryapi

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
)

// DiscoverDatasets returns the projects and populated FHIR resource types
// visible to the request principal. Project discovery is intentionally
// allowlist-based: the service never scans the catalog to invent project
// names for a caller that has not supplied an explicit project source.
func (s *Service) DiscoverDatasets(ctx context.Context) ([]DatasetSummary, error) {
	if s == nil || s.discoverDatasetsFn == nil {
		return []DatasetSummary{}, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	projects := datasetDiscoveryProjects(principal, s.datasetProjectAllowlist)
	if len(projects) == 0 {
		return []DatasetSummary{}, nil
	}

	generations := make(map[string]string, len(projects))
	states := make(map[string]string, len(projects))
	selectedProjects := make([]string, 0, len(projects))
	for _, project := range projects {
		selectedGeneration := ""
		state := "LEGACY"
		if s.activeManifestResolver != nil {
			manifest, err := publication.ResolveActive(ctx, s.activeManifestResolver, project)
			if err != nil {
				// Absence is a valid per-project state. Any other resolver or
				// storage failure must remain visible so callers can retry rather
				// than observing a misleading empty dataset list.
				if errors.Is(err, publication.ErrNoActiveGeneration) {
					continue
				}
				return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "the dataframe backend is temporarily unavailable", dataframeerrors.WithRetryable(true))
			}
			selectedGeneration = manifest.Dataset.Generation
			state = string(manifest.State)
		}
		generations[project] = selectedGeneration
		states[project] = state
		selectedProjects = append(selectedProjects, project)
	}
	if len(selectedProjects) == 0 {
		return []DatasetSummary{}, nil
	}

	scopes := make(map[string]catalog.DatasetAuthScope, len(selectedProjects))
	for _, project := range selectedProjects {
		scope, err := s.resolveReadScopeForGeneration(ctx, principal, project, generations[project], nil)
		if err != nil {
			return nil, classifyError(err)
		}
		scopes[project] = catalog.DatasetAuthScope{
			AuthResourcePaths: cloneStrings(scope.AuthResourcePaths),
			Unrestricted:      scope.Unrestricted(),
		}
	}

	catalogSummaries, err := s.discoverDatasets(ctx, catalog.DatasetSummaryOptions{
		ProjectAllowlist:           selectedProjects,
		DatasetGenerationByProject: generations,
		AuthScopesByProject:        scopes,
		DatasetStateByProject:      states,
		CursorBatch:                1000,
	})
	if err != nil {
		return nil, queryBackend(err)
	}
	result := make([]DatasetSummary, 0, len(catalogSummaries))
	for _, summary := range catalogSummaries {
		resourceTypes := make([]ResourceTypeSummary, 0, len(summary.ResourceTypes))
		for _, resource := range summary.ResourceTypes {
			resourceTypes = append(resourceTypes, ResourceTypeSummary{
				ResourceType:        resource.ResourceType,
				DocumentCount:       resource.DocumentCount,
				PopulatedFieldCount: resource.PopulatedFieldCount,
				PivotCandidateCount: resource.PivotCandidateCount,
			})
		}
		sort.Slice(resourceTypes, func(i, j int) bool { return resourceTypes[i].ResourceType < resourceTypes[j].ResourceType })
		result = append(result, DatasetSummary{
			Project:           summary.Project,
			DatasetGeneration: summary.DatasetGeneration,
			State:             summary.State,
			ResourceTypes:     resourceTypes,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Project < result[j].Project })
	return result, nil
}

func datasetDiscoveryProjects(principal *authscope.Principal, configured []string) []string {
	configured = normalizedProjects(configured)
	if principal == nil || len(principal.Projects) == 0 {
		return configured
	}
	principalProjects := normalizedProjects(principal.Projects)
	if len(configured) == 0 {
		return principalProjects
	}
	allowed := make(map[string]struct{}, len(principalProjects))
	for _, project := range principalProjects {
		allowed[project] = struct{}{}
	}
	result := make([]string, 0, len(configured))
	for _, project := range configured {
		if _, ok := allowed[project]; ok {
			result = append(result, project)
		}
	}
	return result
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
