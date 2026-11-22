package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type artifactDelayReader struct {
	next  ports.ArtifactPublishedReader
	delay time.Duration
}

func (r artifactDelayReader) ExactExecutionMaterialization(ctx context.Context, executionID, outputID string) (published.Materialization, error) {
	return r.next.ExactExecutionMaterialization(ctx, executionID, outputID)
}

func (r artifactDelayReader) StreamExactExport(ctx context.Context, request published.ExactExportRequest, visit published.ExactExportVisitor) (published.ExactExportResult, error) {
	return r.next.StreamExactExport(ctx, request, func(row map[string]any) error {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return visit(row)
		}
	})
}

var _ ports.ArtifactPublishedReader = artifactDelayReader{}

func DelayArtifactRows(next ports.ArtifactPublishedReader, delay time.Duration) ports.ArtifactPublishedReader {
	return artifactDelayReader{next: next, delay: delay}
}
