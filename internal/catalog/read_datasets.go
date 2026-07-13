package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// datasetSummariesAQL deliberately reads only the ingest-owned field catalog.
// The first COLLECT removes duplicate rows for the same visible resource
// type/path (for example, one row per auth resource path); the second groups
// those field facts by resource type without multiplying counts.
const datasetSummariesAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  COLLECT resource_type = d.resource_type, path = d.path
    AGGREGATE document_count = MAX(d.doc_count), pivot_candidate = MAX(d.pivot_candidate ? 1 : 0)
  COLLECT resource_type = resource_type
    AGGREGATE document_count = MAX(document_count), populated_field_count = COUNT(), pivot_candidate_count = SUM(pivot_candidate)
  SORT resource_type
  RETURN {
    resource_type,
    document_count,
    populated_field_count,
    pivot_candidate_count
  }
`

type datasetRowsQuery func(context.Context, string, int, map[string]any, arangostore.RowVisitor) error

// DiscoverDatasetSummaries returns only projects explicitly supplied by the
// caller. It opens one Arango client for the whole read, while each project
// retains its own generation and authorization bind values.
func DiscoverDatasetSummaries(ctx context.Context, opts DatasetSummaryOptions) ([]DatasetSummary, error) {
	projects := normalizedProjectAllowlist(opts.ProjectAllowlist)
	if len(projects) == 0 {
		return []DatasetSummary{}, nil
	}
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return nil, err
	}
	defer client.Close(ctx)

	results := make([]DatasetSummary, 0, len(projects))
	for _, project := range projects {
		generation := NormalizeDatasetGeneration(opts.DatasetGenerationByProject[project])
		scope, hasScope := opts.AuthScopesByProject[project]
		if !hasScope {
			// Discovery callers must pass a scope for every project. Keeping this
			// default explicit preserves compatibility for direct catalog users
			// without turning an omitted path list into an implicit restriction.
			scope = DatasetAuthScope{Unrestricted: true}
		}
		state := strings.TrimSpace(opts.DatasetStateByProject[project])
		if state == "" {
			if generation == "" {
				state = "LEGACY"
			} else {
				state = "READY"
			}
		}

		start := time.Now()
		emit("go_discovery_start", map[string]any{
			"database":            opts.Database,
			"project":             project,
			"dataset_generation":  DatasetGenerationBindValue(generation),
			"auth_resource_paths": scope.AuthResourcePaths,
			"cursor_batch_size":   opts.CursorBatch,
			"query":               "dataset_summaries",
		})

		bindVars := map[string]any{
			"project":                          project,
			"dataset_generation":               DatasetGenerationBindValue(generation),
			"auth_resource_paths":              cloneStrings(scope.AuthResourcePaths),
			"auth_resource_paths_unrestricted": scope.Unrestricted,
		}
		rows, err := readDatasetResourceTypes(ctx, client.QueryRows, opts.CursorBatch, bindVars, project)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			emit("go_discovery_complete", map[string]any{
				"database":            opts.Database,
				"project":             project,
				"dataset_generation":  DatasetGenerationBindValue(generation),
				"auth_resource_paths": scope.AuthResourcePaths,
				"rows":                0,
				"seconds":             secondsSince(start),
				"query":               "dataset_summaries",
			})
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ResourceType < rows[j].ResourceType })
		results = append(results, DatasetSummary{
			Project:           project,
			DatasetGeneration: generation,
			State:             state,
			ResourceTypes:     rows,
		})
		emit("go_discovery_complete", map[string]any{
			"database":            opts.Database,
			"project":             project,
			"dataset_generation":  DatasetGenerationBindValue(generation),
			"auth_resource_paths": scope.AuthResourcePaths,
			"rows":                len(rows),
			"seconds":             secondsSince(start),
			"query":               "dataset_summaries",
		})
	}
	return results, nil
}

func readDatasetResourceTypes(ctx context.Context, query datasetRowsQuery, batchSize int, bindVars map[string]any, project string) ([]ResourceTypeSummary, error) {
	rows := make([]ResourceTypeSummary, 0, 16)
	if err := query(ctx, datasetSummariesAQL, batchSize, bindVars, func(row map[string]any) error {
		resourceType := stringValue(row["resource_type"])
		if resourceType == "" {
			return fmt.Errorf("dataset summary returned an empty resource type for project %q", project)
		}
		documentCount, err := int64Value(row["document_count"])
		if err != nil {
			return fmt.Errorf("decode document_count for %s/%s: %w", project, resourceType, err)
		}
		populatedFields, err := int64Value(row["populated_field_count"])
		if err != nil {
			return fmt.Errorf("decode populated_field_count for %s/%s: %w", project, resourceType, err)
		}
		pivotCandidates, err := int64Value(row["pivot_candidate_count"])
		if err != nil {
			return fmt.Errorf("decode pivot_candidate_count for %s/%s: %w", project, resourceType, err)
		}
		rows = append(rows, ResourceTypeSummary{
			ResourceType:        resourceType,
			DocumentCount:       documentCount,
			PopulatedFieldCount: int(populatedFields),
			PivotCandidateCount: int(pivotCandidates),
		})
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ResourceType < rows[j].ResourceType })
	return rows, nil
}

func normalizedProjectAllowlist(projects []string) []string {
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
