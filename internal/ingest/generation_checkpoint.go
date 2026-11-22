package ingest

import (
	"context"
	"fmt"
	"sync"
)

type generationFileOutcomes struct {
	mu                 sync.Mutex
	files              []string
	metaDir            string
	results            []fileLoadResult
	errors             []error
	finished           []bool
	nextCheckpointFile int
	checkpointRows     int64
	lastCheckpoint     string
	advance            func(context.Context, int64, string) error
}

func newGenerationFileOutcomes(metaDir string, files []string, advance func(context.Context, int64, string) error) *generationFileOutcomes {
	return &generationFileOutcomes{
		files:    files,
		metaDir:  metaDir,
		results:  make([]fileLoadResult, len(files)),
		errors:   make([]error, len(files)),
		finished: make([]bool, len(files)),
		advance:  advance,
	}
}

func (outcomes *generationFileOutcomes) complete(ctx context.Context, index int, result fileLoadResult, fileErr error) error {
	outcomes.mu.Lock()
	defer outcomes.mu.Unlock()
	outcomes.results[index] = result
	outcomes.errors[index] = fileErr
	outcomes.finished[index] = true
	for outcomes.nextCheckpointFile < len(outcomes.files) && outcomes.finished[outcomes.nextCheckpointFile] && outcomes.errors[outcomes.nextCheckpointFile] == nil {
		nextRows := outcomes.checkpointRows + int64(outcomes.results[outcomes.nextCheckpointFile].Rows)
		checkpoint := semanticInventorySourceFileID(outcomes.metaDir, outcomes.files[outcomes.nextCheckpointFile])
		if err := outcomes.advance(ctx, nextRows, checkpoint); err != nil {
			return fmt.Errorf("advance semantic inventory checkpoint: %w", err)
		}
		outcomes.checkpointRows = nextRows
		outcomes.lastCheckpoint = checkpoint
		outcomes.nextCheckpointFile++
	}
	return nil
}
