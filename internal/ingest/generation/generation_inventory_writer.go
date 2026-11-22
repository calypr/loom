package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	"sort"
	"time"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type generationInventoryWriter struct {
	client    *arangostore.Client
	dataset   publication.Ref
	batchSize int
	writeAPI  string
	stats     *generationWriteStats
}

func (writer generationInventoryWriter) writeRuns(ctx context.Context, inventoryRunsByResource map[string][]string, summary *LoadSummary) error {
	entryTypes := make([]string, 0, len(inventoryRunsByResource))
	for resourceType := range inventoryRunsByResource {
		entryTypes = append(entryTypes, resourceType)
	}
	sort.Strings(entryTypes)
	entryMergeStart := time.Now()
	for _, resourceType := range entryTypes {
		batch := make([]json.RawMessage, 0, writer.batchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			insertStart := time.Now()
			insertCtx := arangostore.WithRawInsertObserver(ctx, func(bytes int64, duration time.Duration) {
				writer.stats.record("semantic_inventory", generationWriteSample{bytes: bytes, duration: duration})
			})
			if err := bootstrap.InsertRawDocuments(insertCtx, writer.client, semantic.EntryCollection, batch, true, writer.writeAPI); err != nil {
				return err
			}
			summary.StageSeconds["semantic_inventory_insert"] += time.Since(insertStart).Seconds()
			summary.BatchCounts["semantic_inventory"]++
			batch = make([]json.RawMessage, 0, writer.batchSize)
			return nil
		}
		mergeErr := mergeSemanticInventoryEntryRuns(ctx, inventoryRunsByResource[resourceType], func(partial semantic.EntryPartial) error {
			document := semantic.EntryDocument(partial, writer.dataset.Project, catalog.NormalizeDatasetGeneration(writer.dataset.Generation),
				semantic.BuildID(writer.dataset.Project, writer.dataset.Generation), semantic.SourceFile)
			encoded, encodeErr := json.Marshal(document)
			if encodeErr != nil {
				return encodeErr
			}
			batch = append(batch, encoded)
			if len(batch) == writer.batchSize {
				return flush()
			}
			return nil
		})
		if mergeErr != nil {
			return fmt.Errorf("merge semantic inventory entries for %s: %w", resourceType, mergeErr)
		}
		if err := flush(); err != nil {
			return fmt.Errorf("write semantic inventory entries for %s: %w", resourceType, err)
		}
	}
	summary.StageSeconds["semantic_inventory_aggregate_encode"] += time.Since(entryMergeStart).Seconds()
	return nil
}
