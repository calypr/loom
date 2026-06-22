package querysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"arangodb-proto/internal/dbio"
	postgresstore "arangodb-proto/internal/experimental/store/postgres"
	"arangodb-proto/internal/store"
)

type BuildScalarIndexOptions struct {
	dbio.ConnectionOptions
	Project       string
	ResourceType  string
	BatchSize     int
	ProgressEvery int
	Truncate      bool
}

type BuildScalarIndexSummary struct {
	Project          string  `json:"project,omitempty"`
	ResourceType     string  `json:"resource_type,omitempty"`
	ResourcesScanned int     `json:"resources_scanned"`
	ScalarRows       int     `json:"scalar_rows"`
	Seconds          float64 `json:"seconds"`
}

func BuildScalarIndex(ctx context.Context, opts BuildScalarIndexOptions) (BuildScalarIndexSummary, error) {
	if dbio.BackendName(opts.Backend) != dbio.BackendPostgres {
		return BuildScalarIndexSummary{}, fmt.Errorf("build-scalar-index currently supports only the postgres backend")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 5000
	}

	rawClient, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return BuildScalarIndexSummary{}, err
	}
	defer rawClient.Close(ctx)

	client, ok := rawClient.(*postgresstore.Client)
	if !ok {
		return BuildScalarIndexSummary{}, fmt.Errorf("postgres backend did not return a postgres client")
	}

	spec := helperBootstrapSpec([]store.CollectionSpec{
		{
			Name:    scalarIndexCollection,
			Indexes: [][]string{{"project", "resource_type", "path", "value_text"}, {"project", "resource_type", "path", "system", "code", "display"}},
		},
	}, false)

	start := time.Now()
	emit("go_scalar_index_start", map[string]any{
		"project":       opts.Project,
		"resource_type": opts.ResourceType,
		"truncate":      opts.Truncate,
		"batch_size":    opts.BatchSize,
	})
	if err := client.Bootstrap(ctx, spec); err != nil {
		return BuildScalarIndexSummary{}, err
	}
	if err := client.ResetScalarIndex(ctx, opts.Project, opts.ResourceType, opts.Truncate); err != nil {
		return BuildScalarIndexSummary{}, err
	}

	query := `
SELECT
  project,
  resource_key,
  resource_type,
  body
FROM fhir_resource
WHERE (NULLIF(@project, '') IS NULL OR project = @project)
  AND (NULLIF(@resource_type, '') IS NULL OR resource_type = @resource_type)
ORDER BY resource_key ASC;
`
	bindVars := map[string]any{
		"project":       opts.Project,
		"resource_type": opts.ResourceType,
	}

	scanned := 0
	totalScalarRows := 0
	batch := make([]postgresstore.ScalarIndexRow, 0, opts.BatchSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := client.InsertScalarIndexRows(ctx, batch); err != nil {
			return err
		}
		totalScalarRows += len(batch)
		batch = make([]postgresstore.ScalarIndexRow, 0, opts.BatchSize)
		return nil
	}

	err = client.QueryRows(ctx, query, opts.BatchSize, bindVars, func(row map[string]any) error {
		project := stringValue(row["project"])
		resourceKey := stringValue(row["resource_key"])
		resourceType := stringValue(row["resource_type"])
		payload, err := bodyPayload(row["body"])
		if err != nil {
			return fmt.Errorf("decode body for %s: %w", resourceKey, err)
		}
		batch = append(batch, postgresstore.ScalarIndexRows(project, resourceKey, resourceType, payload)...)
		scanned++
		if scanned%opts.ProgressEvery == 0 {
			emit("go_scalar_index_progress", map[string]any{
				"resources_scanned": scanned,
				"scalar_rows":       totalScalarRows + len(batch),
				"seconds":           secondsSince(start),
			})
		}
		if len(batch) >= opts.BatchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return BuildScalarIndexSummary{}, err
	}
	if err := flush(); err != nil {
		return BuildScalarIndexSummary{}, err
	}

	summary := BuildScalarIndexSummary{
		Project:          opts.Project,
		ResourceType:     opts.ResourceType,
		ResourcesScanned: scanned,
		ScalarRows:       totalScalarRows,
		Seconds:          secondsSince(start),
	}
	emit("go_scalar_index_complete", map[string]any{
		"resources_scanned": scanned,
		"scalar_rows":       totalScalarRows,
		"seconds":           summary.Seconds,
	})
	return summary, nil
}

func bodyPayload(value any) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case string:
		return decodePayloadString(typed)
	case []byte:
		return decodePayloadBytes(typed)
	default:
		return nil, fmt.Errorf("unsupported body type %T", value)
	}
}

func decodePayloadString(value string) (map[string]any, error) {
	return decodePayloadBytes([]byte(value))
}

func decodePayloadBytes(value []byte) (map[string]any, error) {
	payload := make(map[string]any)
	if len(strings.TrimSpace(string(value))) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(value, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
