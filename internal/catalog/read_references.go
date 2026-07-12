package catalog

import (
	"context"
	"fmt"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

const populatedReferencesAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project
  FILTER e.dataset_generation == @dataset_generation
  FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
  FILTER (
    @mode == "builder" && (@node_type == null || e.to_type == @node_type)
  ) || (
    @mode != "builder" && (@from_type == null || e.from_type == @from_type)
  )
  COLLECT
    dataset_generation = e.dataset_generation,
    from_type = (@mode == "builder" ? @node_type : e.from_type),
    label = e.label,
    to_type = (@mode == "builder" ? e.from_type : e.to_type)
    WITH COUNT INTO edge_count
  SORT from_type, edge_count DESC, label, to_type
  RETURN {
    dataset_generation,
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

	bindVars := populatedReferencesBindVars(opts, mode)

	results := make([]PopulatedReference, 0, 64)
	err = client.QueryRows(ctx, populatedReferencesAQL, opts.CursorBatch, bindVars, func(row map[string]any) error {
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
		"mode":                             mode,
	}
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
	return bindVars
}
