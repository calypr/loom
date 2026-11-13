package postgres

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/source"
)

type ResourceBatch struct {
	ID                string
	Project           string
	DatasetGeneration string
	Rows              []source.Resource
}

type ResourceBatchResult struct {
	BatchID          string
	Digest           string
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

type ResourceWriter struct {
	batch *BatchWriter
}

func NewResourceWriter(src ConnSource, schema string) (*ResourceWriter, error) {
	batch, err := NewBatchWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &ResourceWriter{batch: batch}, nil
}

func (w *ResourceWriter) WriteResourceBatch(ctx context.Context, batch ResourceBatch) (ResourceBatchResult, error) {
	if len(batch.Rows) == 0 {
		return ResourceBatchResult{}, fmt.Errorf("%w: resource batch %q carries no rows",
			ErrInvalidBatch, batch.ID)
	}
	result, err := w.batch.WriteBatch(ctx, Batch{
		ID:                batch.ID,
		Project:           batch.Project,
		DatasetGeneration: batch.DatasetGeneration,
		Resources:         batch.Rows,
	})
	if err != nil {
		return ResourceBatchResult{}, err
	}
	if result.CommittedEdges != 0 || result.SkippedIdenticalEdges != 0 {
		return ResourceBatchResult{}, &BatchAccountingError{
			BatchID:   batch.ID,
			Side:      "edges",
			Expected:  0,
			Committed: result.CommittedEdges,
		}
	}
	return ResourceBatchResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Committed:        result.CommittedResources,
		SkippedIdentical: result.SkippedIdenticalResources,
		Replay:           result.Replay,
	}, nil
}
