package catalog

import (
	"context"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const existingAuthResourcePathsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.auth_resource_path != null AND d.auth_resource_path != ""
  COLLECT auth_resource_path = d.auth_resource_path
  SORT auth_resource_path
  RETURN { auth_resource_path: auth_resource_path }
`

func DiscoverExistingAuthResourcePaths(ctx context.Context, opts AuthResourcePathOptions) ([]string, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return nil, err
	}
	defer client.Close(ctx)
	start := time.Now()
	emit("go_discovery_start", map[string]any{
		"database":           opts.Database,
		"project":            opts.Project,
		"dataset_generation": DatasetGenerationBindValue(opts.DatasetGeneration),
		"cursor_batch_size":  opts.CursorBatch,
		"query":              "existing_auth_resource_paths",
	})

	results := make([]string, 0, 16)
	err = client.QueryRows(ctx, existingAuthResourcePathsAQL, opts.CursorBatch, map[string]any{
		"project":            opts.Project,
		"dataset_generation": DatasetGenerationBindValue(opts.DatasetGeneration),
	}, func(row map[string]any) error {
		if path := stringValue(row["auth_resource_path"]); path != "" {
			results = append(results, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	emit("go_discovery_complete", map[string]any{
		"database":           opts.Database,
		"project":            opts.Project,
		"dataset_generation": DatasetGenerationBindValue(opts.DatasetGeneration),
		"rows":               len(results),
		"seconds":            secondsSince(start),
		"query":              "existing_auth_resource_paths",
	})
	return results, nil
}
