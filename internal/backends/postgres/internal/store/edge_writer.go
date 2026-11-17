package postgres

import (
	"context"
	"fmt"
)

type EdgeBatch struct {
	ID                string
	Project           string
	DatasetGeneration string
	Rows              []EdgeOccurrenceRow
}

type EdgeBatchResult struct {
	BatchID          string
	Digest           string
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

type EdgeWriter struct {
	batch *BatchWriter
}

func NewEdgeWriter(src ConnSource, schema string) (*EdgeWriter, error) {
	batch, err := NewBatchWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &EdgeWriter{batch: batch}, nil
}

func (w *EdgeWriter) WriteEdgeBatch(ctx context.Context, batch EdgeBatch) (EdgeBatchResult, error) {
	if len(batch.Rows) == 0 {
		return EdgeBatchResult{}, fmt.Errorf("%w: edge batch %q carries no rows",
			ErrInvalidBatch, batch.ID)
	}
	result, err := w.batch.WriteBatch(ctx, Batch{
		ID:                batch.ID,
		Project:           batch.Project,
		DatasetGeneration: batch.DatasetGeneration,
		Edges:             batch.Rows,
	})
	if err != nil {
		return EdgeBatchResult{}, err
	}
	if result.CommittedResources != 0 || result.SkippedIdenticalResources != 0 {
		return EdgeBatchResult{}, &BatchAccountingError{
			BatchID:   batch.ID,
			Side:      "resources",
			Expected:  0,
			Committed: result.CommittedResources,
		}
	}
	return EdgeBatchResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Committed:        result.CommittedEdges,
		SkippedIdentical: result.SkippedIdenticalEdges,
		Replay:           result.Replay,
	}, nil
}
