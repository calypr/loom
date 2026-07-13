package catalog

import (
	"context"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// RelationshipRebuildOptions describes the explicit repair path. Normal
// discovery never uses this scan; operators call it after an old dataset was
// loaded before the relationship catalog existed or after a repair.
type RelationshipRebuildOptions struct {
	arangostore.ConnectionOptions
	Project                       string
	DatasetGeneration             string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted *bool
	CursorBatch                   int
	BatchSize                     int
	WriteAPI                      string
}

type RelationshipRebuildSummary struct {
	Project           string  `json:"project"`
	DatasetGeneration string  `json:"dataset_generation,omitempty"`
	Rows              int     `json:"rows"`
	EdgeCount         int64   `json:"edge_count"`
	Seconds           float64 `json:"seconds"`
}

const relationshipRebuildAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER e.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
  COLLECT
    project = e.project,
    dataset_generation = e.dataset_generation,
    auth_resource_path = e.auth_resource_path,
    from_type = e.from_type,
    label = e.label,
    to_type = e.to_type
    WITH COUNT INTO edge_count
  RETURN {
    project,
    dataset_generation,
    auth_resource_path,
    from_type,
    label,
    to_type,
    edge_count
  }
`

const relationshipClearAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  REMOVE d IN fhir_relationship_catalog
  RETURN 1
`

// RebuildRelationshipCatalog performs the only supported direct fhir_edge
// aggregation. It clears the selected namespace first, then writes a
// deterministic compact catalog, making repeated rebuilds idempotent.
func RebuildRelationshipCatalog(ctx context.Context, opts RelationshipRebuildOptions) (RelationshipRebuildSummary, error) {
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.WriteAPI == "" {
		opts.WriteAPI = "import"
	}
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return RelationshipRebuildSummary{}, err
	}
	defer client.Close(ctx)
	if err := client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name: RelationshipCatalogCollection,
		Indexes: [][]string{
			{"project", "dataset_generation", "to_type"},
			{"project", "dataset_generation", "auth_resource_path", "to_type"},
			{"project", "dataset_generation", "from_type"},
			{"project", "dataset_generation", "auth_resource_path", "from_type"},
		},
	}}}); err != nil {
		return RelationshipRebuildSummary{}, err
	}
	bindVars := relationshipRebuildBindVars(opts)
	if err := client.QueryRows(ctx, relationshipClearAQL, opts.CursorBatch, bindVars, func(map[string]any) error { return nil }); err != nil {
		return RelationshipRebuildSummary{}, err
	}
	counts := make(map[RelationshipKey]int64)
	start := time.Now()
	if err := client.QueryRows(ctx, relationshipRebuildAQL, opts.CursorBatch, bindVars, func(row map[string]any) error {
		count, err := int64Value(row["edge_count"])
		if err != nil {
			return err
		}
		key := RelationshipKey{
			Project:           stringValue(row["project"]),
			DatasetGeneration: stringValue(row["dataset_generation"]),
			AuthResourcePath:  stringValue(row["auth_resource_path"]),
			FromType:          stringValue(row["from_type"]),
			Label:             stringValue(row["label"]),
			ToType:            stringValue(row["to_type"]),
		}
		counts[key] = count
		return nil
	}); err != nil {
		return RelationshipRebuildSummary{}, err
	}
	docs := RelationshipCatalogDocuments(counts)
	if err := WriteRelationshipCatalog(ctx, client, docs, opts.BatchSize, true, opts.WriteAPI, nil); err != nil {
		return RelationshipRebuildSummary{}, err
	}
	var edgeCount int64
	for _, count := range counts {
		edgeCount += count
	}
	return RelationshipRebuildSummary{
		Project:           opts.Project,
		DatasetGeneration: NormalizeDatasetGeneration(opts.DatasetGeneration),
		Rows:              len(docs),
		EdgeCount:         edgeCount,
		Seconds:           time.Since(start).Seconds(),
	}, nil
}

func relationshipRebuildBindVars(opts RelationshipRebuildOptions) map[string]any {
	return map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               DatasetGenerationBindValue(opts.DatasetGeneration),
		"auth_resource_paths":              cloneStrings(opts.AuthResourcePaths),
		"auth_resource_paths_unrestricted": EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
	}
}
