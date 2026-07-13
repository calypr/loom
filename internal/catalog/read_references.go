package catalog

import (
	"context"
	"fmt"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// relationshipCatalogBuilderAQL is the indexed runtime discovery path. The
// builder asks for edges entering a node type, so the persisted edge
// orientation is reversed in the dataframe-facing result (the same contract
// as the direct repair query above).
const relationshipCatalogBuilderAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @node_type == null OR d.to_type == @node_type
  COLLECT
    from_type = d.to_type,
    label = d.label,
    to_type = d.from_type
    AGGREGATE edge_count = SUM(d.edge_count)
  SORT from_type, edge_count DESC, label, to_type
  RETURN {
    dataset_generation: @dataset_generation,
    from_type,
    label,
    to_type,
    edge_count
  }
`

// relationshipCatalogStorageAQL keeps the physical edge orientation for
// storage traversal discovery. Both runtime paths read only the compact
// ingest-owned catalog; fhir_edge aggregation remains an explicit rebuild.
const relationshipCatalogStorageAQL = `
FOR d IN fhir_relationship_catalog
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  FILTER @from_type == null OR d.from_type == @from_type
  COLLECT
    from_type = d.from_type,
    label = d.label,
    to_type = d.to_type
    AGGREGATE edge_count = SUM(d.edge_count)
  SORT from_type, edge_count DESC, label, to_type
  RETURN {
    dataset_generation: @dataset_generation,
    from_type,
    label,
    to_type,
    edge_count
  }
`

func DiscoverPopulatedReferences(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
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
		"database":            opts.Database,
		"project":             opts.Project,
		"dataset_generation":  DatasetGenerationBindValue(opts.DatasetGeneration),
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
	query := relationshipCatalogStorageAQL
	if mode == TraversalModeBuilder {
		query = relationshipCatalogBuilderAQL
	}

	bindVars := populatedReferencesBindVars(opts, mode)

	results := make([]PopulatedReference, 0, 64)
	err = client.QueryRows(ctx, query, opts.CursorBatch, bindVars, func(row map[string]any) error {
		ref := PopulatedReference{
			DatasetGeneration: stringValue(row["dataset_generation"]),
			FromType:          stringValue(row["from_type"]),
			Label:             stringValue(row["label"]),
			ToType:            stringValue(row["to_type"]),
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
		"dataset_generation":  DatasetGenerationBindValue(opts.DatasetGeneration),
		"from_type":           opts.FromType,
		"node_type":           opts.NodeType,
		"mode":                mode,
		"auth_resource_paths": opts.AuthResourcePaths,
		"rows":                len(results),
		"seconds":             secondsSince(start),
	})
	return results, nil
}

func populatedReferencesBindVars(opts PopulatedReferenceOptions, mode string) map[string]any {
	bindVars := map[string]any{
		"project":                          opts.Project,
		"dataset_generation":               DatasetGenerationBindValue(opts.DatasetGeneration),
		"auth_resource_paths":              cloneStrings(opts.AuthResourcePaths),
		"auth_resource_paths_unrestricted": EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted),
	}
	if mode == TraversalModeBuilder {
		if opts.NodeType != "" {
			bindVars["node_type"] = opts.NodeType
		} else {
			bindVars["node_type"] = nil
		}
	} else {
		if opts.FromType != "" {
			bindVars["from_type"] = opts.FromType
		} else {
			bindVars["from_type"] = nil
		}
	}
	return bindVars
}
