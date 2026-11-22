package generation

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
