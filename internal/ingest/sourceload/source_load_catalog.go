package sourceload

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/ingest/source"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/semantic"
)

func (p *RepositorySourceLoader) prepareCandidateSemanticInventory(ctx context.Context, record sourceLoadRecord) error {
	store, err := catalogarango.New(p.client)
	if err != nil {
		return err
	}
	build := semantic.NewBuild(record.Project, record.Generation, record.Plan.AuthResourcePath)
	if record.SourceGeneration != "" {
		version, err := p.sourceSemanticInventoryEntryIndexVersion(ctx, record.Project, record.SourceGeneration)
		if err != nil {
			return err
		}
		if version >= semantic.EntryIndexVersion {
			build.EntryIndexVersion = version
		}
	}
	return store.BeginSemanticInventoryBuild(ctx, build)
}

func (p *RepositorySourceLoader) sourceSemanticInventoryEntryIndexVersion(ctx context.Context, project, generation string) (int, error) {
	exists, err := p.client.CollectionExists(ctx, semantic.BuildCollection)
	if err != nil || !exists {
		return 0, err
	}
	version := 0
	key := semantic.BuildKey(project, generation)
	err = p.client.QueryRows(ctx, `FOR build IN fhir_semantic_inventory_builds
FILTER build._key == @key
LIMIT 1
RETURN {entry_index_version: NOT_NULL(build.entry_index_version, 0)}`, 1, map[string]any{"key": key}, func(row map[string]any) error {
		version = int(numericInt64(row["entry_index_version"]))
		return nil
	})
	return version, err
}

func (p *RepositorySourceLoader) finalizeSourceLoadCandidate(ctx context.Context, record sourceLoadRecord) (int64, error) {
	identity, err := source.LoadSchemaSnapshot()
	if err != nil {
		return 0, err
	}
	selectedTypes := make([]string, 0, len(record.Plan.Files))
	for _, file := range record.Plan.Files {
		resourceType, err := canonicalSourceResourceType(file.Path)
		if err != nil {
			return 0, err
		}
		selectedTypes = append(selectedTypes, resourceType)
	}
	if err := p.rebuildCandidateRelationshipCatalog(ctx, record); err != nil {
		return 0, err
	}
	store, err := catalogarango.New(p.client)
	if err != nil {
		return 0, err
	}
	summary, err := store.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
		Project: record.Project, DatasetGeneration: record.Generation,
		ResourceTypes: identity.GeneratedResourceTypes, CursorBatch: p.batchSize,
		BatchSize: p.batchSize, WriteAPI: p.writeAPI,
	})
	if err != nil {
		return 0, fmt.Errorf("rebuild candidate resource inventory: %w", err)
	}
	return summary.DocumentCount, nil
}

func (p *RepositorySourceLoader) rebuildCandidateRelationshipCatalog(ctx context.Context, record sourceLoadRecord) error {
	query := `FOR edge IN fhir_edge
  FILTER edge.project == @project AND edge.dataset_generation == @generation
  FILTER edge.from_type IN @resource_types AND edge.to_type IN @resource_types
  COLLECT project = edge.project, dataset_generation = edge.dataset_generation,
    auth_resource_path = NOT_NULL(edge.auth_resource_path, ""),
    from_type = edge.from_type, label = edge.label, to_type = edge.to_type
    WITH COUNT INTO edge_count
  SORT project, dataset_generation, auth_resource_path, from_type, label, to_type
  RETURN {project, dataset_generation, auth_resource_path, from_type, label, to_type, edge_count}`
	identity, err := source.LoadSchemaSnapshot()
	if err != nil {
		return err
	}
	batch := make([]json.RawMessage, 0, p.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := p.client.InsertBatchRaw(ctx, catalog.RelationshipCatalogCollection, batch, true, p.writeAPI)
		batch = batch[:0]
		return err
	}
	if err := p.client.QueryRows(ctx, query, p.batchSize, map[string]any{
		"project": record.Project, "generation": record.Generation,
		"resource_types": identity.GeneratedResourceTypes,
	}, func(row map[string]any) error {
		key := catalog.RelationshipKey{
			Project: sourceLoadString(row["project"]), DatasetGeneration: sourceLoadString(row["dataset_generation"]),
			AuthResourcePath: sourceLoadString(row["auth_resource_path"]), FromType: sourceLoadString(row["from_type"]),
			Label: sourceLoadString(row["label"]), ToType: sourceLoadString(row["to_type"]),
		}
		count := sourceLoadNumber(row["edge_count"])
		docs := catalog.RelationshipCatalogDocuments(map[catalog.RelationshipKey]int64{key: count})
		if len(docs) != 1 {
			return fmt.Errorf("relationship aggregate has no stable catalog identity")
		}
		encoded, err := json.Marshal(docs[0])
		if err != nil {
			return err
		}
		batch = append(batch, encoded)
		if len(batch) >= p.batchSize {
			return flush()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("aggregate candidate relationship catalog: %w", err)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("write candidate relationship catalog: %w", err)
	}
	return nil
}
