package server

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

// artifactDelayReader makes export/publication races deterministic in the
// isolated no-auth development stack. Production configuration cannot enable
// it because the delay is populated only by applyDevFaultOverrides.
type artifactDelayReader struct {
	next  lifecycle.ArtifactPublishedReader
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

var _ lifecycle.ArtifactPublishedReader = artifactDelayReader{}
