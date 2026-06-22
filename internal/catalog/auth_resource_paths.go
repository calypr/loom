package catalog

import (
	"context"
	"time"

	"arangodb-proto/internal/dbio"
)

type AuthResourcePathOptions struct {
	dbio.ConnectionOptions
	Project     string
	CursorBatch int
}

const existingAuthResourcePathsAQL = `
FOR d IN fhir_field_catalog
  FILTER d.project == @project
  FILTER d.auth_resource_path != null AND d.auth_resource_path != ""
  COLLECT auth_resource_path = d.auth_resource_path
  SORT auth_resource_path
  RETURN auth_resource_path
`

const existingAuthResourcePathsSurrealQL = `
SELECT VALUE auth_resource_path
FROM fhir_field_catalog
WHERE project = $project
  AND auth_resource_path != NONE
  AND auth_resource_path != ""
GROUP ALL BY auth_resource_path
ORDER BY auth_resource_path ASC;
`

const existingAuthResourcePathsPostgresSQL = `
SELECT DISTINCT auth_resource_path
FROM fhir_field_catalog
WHERE project = @project
  AND auth_resource_path IS NOT NULL
  AND auth_resource_path <> ''
ORDER BY auth_resource_path ASC;
`

func DiscoverExistingAuthResourcePaths(ctx context.Context, opts AuthResourcePathOptions) ([]string, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := dbio.OpenBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	emit("go_discovery_start", map[string]any{
		"database":          opts.Database,
		"project":           opts.Project,
		"cursor_batch_size": opts.CursorBatch,
		"query":             "existing_auth_resource_paths",
	})

	query := existingAuthResourcePathsAQL
	switch dbio.BackendName(opts.Backend) {
	case dbio.BackendSurreal:
		query = existingAuthResourcePathsSurrealQL
	case dbio.BackendPostgres:
		query = existingAuthResourcePathsPostgresSQL
	}
	results := make([]string, 0, 16)
	err = client.QueryRows(ctx, query, opts.CursorBatch, map[string]any{"project": opts.Project}, func(row map[string]any) error {
		if path := stringValue(row["auth_resource_path"]); path != "" {
			results = append(results, path)
			return nil
		}
		if value := stringValue(row["value"]); value != "" {
			results = append(results, value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	emit("go_discovery_complete", map[string]any{
		"database": opts.Database,
		"project":  opts.Project,
		"rows":     len(results),
		"seconds":  secondsSince(start),
		"query":    "existing_auth_resource_paths",
	})
	return results, nil
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
