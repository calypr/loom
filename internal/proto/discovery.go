package proto

import (
	"context"
	"fmt"
	"time"
)

const populatedReferencesAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER @from_type == null OR e.from_type == @from_type
  COLLECT from_type = e.from_type, label = e.label, to_type = e.to_type
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
  from_type,
  label,
  to_type,
  count() AS edge_count
FROM fhir_edge
WHERE project = $project
  AND ($from_type = "" OR from_type = $from_type)
GROUP BY from_type, label, to_type
ORDER BY from_type ASC, edge_count DESC, label ASC, to_type ASC;
`

type PopulatedReferenceOptions struct {
	ConnectionOptions
	Project     string
	FromType    string
	CursorBatch int
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
	client, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	Emit("go_discovery_start", map[string]any{
		"database":          opts.Database,
		"project":           opts.Project,
		"from_type":         opts.FromType,
		"cursor_batch_size": opts.CursorBatch,
		"query":             "populated_references",
	})

	query := populatedReferencesAQL
	bindVars := map[string]any{"project": opts.Project}
	switch backendName(opts.Backend) {
	case backendSurreal:
		query = populatedReferencesSurrealQL
		bindVars["from_type"] = opts.FromType
	default:
		if opts.FromType != "" {
			bindVars["from_type"] = opts.FromType
		} else {
			bindVars["from_type"] = nil
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

	Emit("go_discovery_complete", map[string]any{
		"database":  opts.Database,
		"project":   opts.Project,
		"from_type": opts.FromType,
		"rows":      len(results),
		"seconds":   SecondsSince(start),
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
