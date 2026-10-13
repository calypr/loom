package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/catalog"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

var ErrResourceInventoryBuildUnavailable = errors.New("resource inventory build is unavailable; run arango-fhir-proto rebuild-resource-inventory --generation ID")

func (s *Store) RebuildResourceInventory(ctx context.Context, opts catalog.ResourceInventoryRebuildOptions) (catalog.ResourceInventoryRebuildSummary, error) {
	if opts.Project == "" || catalog.NormalizeDatasetGeneration(opts.DatasetGeneration) == "" {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild requires project and exact dataset generation")
	}
	resourceTypes, diagnostics := evidenceResourceTypes(opts.ResourceTypes)
	if len(diagnostics) > 0 {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild has invalid resource types: %s", diagnostics[0].ResourceType)
	}
	if len(resourceTypes) == 0 {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("resource inventory rebuild requires generated resource types")
	}
	if opts.CursorBatch <= 0 {
		opts.CursorBatch = 1000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if err := s.client.Bootstrap(ctx, arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name:    catalog.ResourceInventoryCollection,
		Indexes: [][]string{{"project", "dataset_generation", "record_type", "resource_type", "auth_resource_path"}, {"project", "dataset_generation", "build_id", "record_type"}},
	}}}); err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("bootstrap resource inventory collection: %w", err)
	}

	counts := make([]catalog.ResourceInventoryCount, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		if err := ctx.Err(); err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, err
		}
		exists, err := s.client.CollectionExists(ctx, resourceType)
		if err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("check resource collection %s: %w", resourceType, err)
		}
		if !exists {
			counts = append(counts, catalog.ResourceInventoryCount{
				Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
				ResourceType: resourceType, DocumentCount: 0,
			})
			continue
		}
		rowsSeen := false
		err = s.client.QueryRows(ctx, rebuildResourceInventoryCountsAQL, opts.CursorBatch, map[string]interface{}{
			"@resource_collection": resourceType,
			"project":              opts.Project,
			"dataset_generation":   catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
		}, func(row map[string]any) error {
			rowsSeen = true
			documentCount, err := decodeInt64(row["document_count"])
			if err != nil {
				return fmt.Errorf("decode %s resource inventory count: %w", resourceType, err)
			}
			if documentCount < 0 {
				return fmt.Errorf("decode %s resource inventory count: negative count", resourceType)
			}
			counts = append(counts, catalog.ResourceInventoryCount{
				Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
				AuthResourcePath: stringValue(row["auth_resource_path"]), ResourceType: resourceType, DocumentCount: documentCount,
			})
			return nil
		})
		if err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("scan retained %s vertices for resource inventory: %w", resourceType, err)
		}
		if !rowsSeen {
			counts = append(counts, catalog.ResourceInventoryCount{
				Project: opts.Project, DatasetGeneration: catalog.NormalizeDatasetGeneration(opts.DatasetGeneration),
				ResourceType: resourceType, DocumentCount: 0,
			})
		}
	}
	build, countRows, err := catalog.NewResourceInventoryBuild(opts.Project, opts.DatasetGeneration, resourceTypes, counts)
	if err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("prepare resource inventory build: %w", err)
	}
	for start := 0; start < len(countRows); start += opts.BatchSize {
		end := min(start+opts.BatchSize, len(countRows))
		docs := make([]json.RawMessage, 0, end-start)
		for i := start; i < end; i++ {
			doc, err := json.Marshal(countRows[i])
			if err != nil {
				return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("encode resource inventory count: %w", err)
			}
			docs = append(docs, doc)
		}
		if err := s.client.InsertBatchRaw(ctx, catalog.ResourceInventoryCollection, docs, true, opts.WriteAPI); err != nil {
			return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("write resource inventory counts: %w", err)
		}
	}
	marker, err := json.Marshal(build)
	if err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("encode resource inventory build marker: %w", err)
	}
	if err := s.client.InsertBatchRaw(ctx, catalog.ResourceInventoryCollection, []json.RawMessage{marker}, true, opts.WriteAPI); err != nil {
		return catalog.ResourceInventoryRebuildSummary{}, fmt.Errorf("publish resource inventory build marker: %w", err)
	}
	var documentCount int64
	for _, count := range countRows {
		documentCount += count.DocumentCount
	}
	return catalog.ResourceInventoryRebuildSummary{
		BuildID: build.BuildID, CoverageDigest: build.CoverageDigest, ResourceTypeCount: build.ResourceTypeCount,
		CountRowCount: build.CountRowCount, DocumentCount: documentCount,
	}, nil
}

func (s *Store) readResourceInventoryBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error) {
	var build catalog.ResourceInventoryBuild
	found := false
	err := s.client.QueryRows(ctx, readResourceInventoryBuildAQL, 1, map[string]interface{}{
		"key":     catalog.ResourceInventoryBuildKey(project, datasetGeneration),
		"project": project, "dataset_generation": catalog.NormalizeDatasetGeneration(datasetGeneration),
	}, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &build); err != nil {
			return err
		}
		found = true
		return nil
	})
	return build, found, err
}

const readResourceInventoryBuildAQL = `
FOR d IN fhir_resource_inventory
  FILTER d._key == @key
  FILTER d.record_type == "build"
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  LIMIT 1
  RETURN {
    _key: d._key,
    record_type: d.record_type,
    project: d.project,
    dataset_generation: d.dataset_generation,
    build_id: d.build_id,
    coverage_digest: d.coverage_digest,
    resource_types: d.resource_types,
    resource_type_count: d.resource_type_count,
    count_row_count: d.count_row_count,
    complete: d.complete
  }`

const readResourceInventoryCountsAQL = `
FOR d IN fhir_resource_inventory
  FILTER d.record_type == "count"
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  FILTER d.build_id == @build_id
  FILTER d.resource_type IN @resource_types
  FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths
  COLLECT resource_type = d.resource_type AGGREGATE document_count = SUM(d.document_count)
  SORT resource_type
  RETURN { resource_type, document_count }`

const rebuildResourceInventoryCountsAQL = `
FOR d IN @@resource_collection
  FILTER d.project == @project
  FILTER d.dataset_generation == @dataset_generation
  COLLECT auth_resource_path = NOT_NULL(d.auth_resource_path, "") WITH COUNT INTO document_count
  SORT auth_resource_path
  RETURN { auth_resource_path, document_count }`
