package catalog

import (
	"context"
	"fmt"
	"time"

	"arangodb-proto/internal/dbio"
)

const populatedReferencesAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
  FILTER (
    @mode == "builder" && (@node_type == null || e.to_type == @node_type)
  ) || (
    @mode != "builder" && (@from_type == null || e.from_type == @from_type)
  )
  COLLECT
    from_type = (@mode == "builder" ? @node_type : e.from_type),
    label = e.label,
    to_type = (@mode == "builder" ? e.from_type : e.to_type)
    WITH COUNT INTO edge_count
  SORT from_type, edge_count DESC, label, to_type
  RETURN {
    from_type,
    label,
    to_type,
    edge_count
  }
`

const populatedReferencesSurrealQL = `
SELECT
  IF $mode = "builder" THEN $node_type ELSE from_type END AS from_type,
  label,
  IF $mode = "builder" THEN from_type ELSE to_type END AS to_type,
  count() AS edge_count
FROM fhir_edge
WHERE project = $project
  AND ($auth_resource_paths_unrestricted = true OR auth_resource_path INSIDE $auth_resource_paths)
  AND (
    ($mode = "builder" AND ($node_type = "" OR to_type = $node_type))
    OR
    ($mode != "builder" AND ($from_type = "" OR from_type = $from_type))
  )
GROUP BY from_type, label, to_type
ORDER BY from_type ASC, edge_count DESC, label ASC, to_type ASC;
`

const populatedReferencesPostgresSQL = `
SELECT
  CASE WHEN @mode = 'builder' THEN @node_type ELSE src_type END AS from_type,
  edge_type AS label,
  CASE WHEN @mode = 'builder' THEN src_type ELSE dst_type END AS to_type,
  COUNT(*)::bigint AS edge_count
FROM fhir_edge
WHERE project = @project
  AND (@auth_resource_paths_unrestricted = true OR auth_resource_path = ANY(@auth_resource_paths))
  AND (
    (@mode = 'builder' AND (NULLIF(@node_type, '') IS NULL OR dst_type = @node_type))
    OR
    (@mode <> 'builder' AND (NULLIF(@from_type, '') IS NULL OR src_type = @from_type))
  )
GROUP BY
  CASE WHEN @mode = 'builder' THEN @node_type ELSE src_type END,
  edge_type,
  CASE WHEN @mode = 'builder' THEN src_type ELSE dst_type END
ORDER BY from_type ASC, edge_count DESC, label ASC, to_type ASC;
`

const (
	TraversalModeStorage = "storage"
	TraversalModeBuilder = "builder"
)

type PopulatedReferenceOptions struct {
	dbio.ConnectionOptions
	Project           string
	AuthResourcePaths []string
	FromType          string
	NodeType          string
	Mode              string
	CursorBatch       int
}

type PopulatedReference struct {
	FromType  string `json:"from_type"`
	Label     string `json:"label"`
	ToType    string `json:"to_type"`
	EdgeCount int64  `json:"edge_count"`
}

func DiscoverPopulatedReferences(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	client, err := dbio.OpenBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	emit("go_discovery_start", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"from_type":           opts.FromType,
		"node_type":           opts.NodeType,
		"mode":                opts.Mode,
		"auth_resource_paths": opts.AuthResourcePaths,
		"cursor_batch_size":   opts.CursorBatch,
		"query":               "populated_references",
	})

	mode := opts.Mode
	if mode == "" {
		mode = TraversalModeStorage
	}

	query := populatedReferencesAQL
	bindVars := map[string]any{
		"project":                          opts.Project,
		"auth_resource_paths":              cloneStrings(opts.AuthResourcePaths),
		"auth_resource_paths_unrestricted": opts.AuthResourcePaths == nil,
		"mode":                             mode,
	}
	switch dbio.BackendName(opts.Backend) {
	case dbio.BackendSurreal:
		query = populatedReferencesSurrealQL
		bindVars["from_type"] = opts.FromType
		bindVars["node_type"] = opts.NodeType
	case dbio.BackendPostgres:
		query = populatedReferencesPostgresSQL
		bindVars["from_type"] = opts.FromType
		bindVars["node_type"] = opts.NodeType
	default:
		if opts.FromType != "" {
			bindVars["from_type"] = opts.FromType
		} else {
			bindVars["from_type"] = nil
		}
		if opts.NodeType != "" {
			bindVars["node_type"] = opts.NodeType
		} else {
			bindVars["node_type"] = nil
		}
	}

	results := make([]PopulatedReference, 0, 64)
	err = client.QueryRows(ctx, query, opts.CursorBatch, bindVars, func(row map[string]any) error {
		ref := PopulatedReference{
			FromType: stringValue(row["from_type"]),
			Label:    stringValue(row["label"]),
			ToType:   stringValue(row["to_type"]),
		}
		count, err := int64Value(row["edge_count"])
		if err != nil {
			return fmt.Errorf("decode edge_count for %s/%s/%s: %w", ref.FromType, ref.Label, ref.ToType, err)
		}
		ref.EdgeCount = count
		results = append(results, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}

	emit("go_discovery_complete", map[string]any{
		"database":            opts.Database,
		"project":             opts.Project,
		"from_type":           opts.FromType,
		"node_type":           opts.NodeType,
		"mode":                mode,
		"auth_resource_paths": opts.AuthResourcePaths,
		"rows":                len(results),
		"seconds":             secondsSince(start),
	})
	return results, nil
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func int64Value(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}
