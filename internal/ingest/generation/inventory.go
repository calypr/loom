package generation

import (
	"fmt"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/bootstrap"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"sort"
	"time"
)

import (
	"context"
	"encoding/json"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/semantic"
)

func spillWorkerInventoryRuns(runs []string, workers []*semantic.EntryAggregator) ([]string, error) {
	for _, worker := range workers {
		path, err := writeSemanticInventoryEntryRun(worker)
		if err != nil {
			return runs, err
		}
		if path != "" {
			runs = append(runs, path)
		}
	}
	return runs, nil
}

type sourceFileInventory struct {
	project    string
	generation string
	batchSize  int
}

func (inventory sourceFileInventory) enqueueEntries(ctx context.Context, runs []string, workers []*semantic.EntryAggregator, writes chan<- fileWriteTask) error {
	batch := make([]json.RawMessage, 0, inventory.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		select {
		case writes <- fileWriteTask{collection: semantic.EntryCollection, docs: batch, replace: true}:
			batch = make([]json.RawMessage, 0, inventory.batchSize)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	mergeErr := mergeWorkerSemanticInventoryEntries(ctx, runs, workers, func(partial semantic.EntryPartial) error {
		document := semantic.EntryDocument(partial, inventory.project, catalog.NormalizeDatasetGeneration(inventory.generation), semantic.BuildID(inventory.project, inventory.generation), semantic.SourceFile)
		encoded, encodeErr := json.Marshal(document)
		if encodeErr != nil {
			return encodeErr
		}
		batch = append(batch, encoded)
		if len(batch) == inventory.batchSize {
			return flush()
		}
		return nil
	})
	if mergeErr == nil {
		mergeErr = flush()
	}
	return mergeErr
}

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
