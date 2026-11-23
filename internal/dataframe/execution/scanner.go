package execution

import (
	"context"

	model "github.com/calypr/loom/internal/dataframe/compiler/model"
)

func (e *Engine) chooseGroupPreviewScan(ctx context.Context, query *model.CompiledQuery, stream *OutputStream) error {
	planner := groupPreviewPlanner{
		previewCoveringIndexes: e.previewCoveringIndexes, previewCollectionRevision: e.previewCollectionRevision,
		previewExplainQuery: e.previewExplainQuery, previewCollectionCount: e.previewCollectionCount,
		groupPreviewRowsCache: &e.groupPreviewRowsCache,
	}
	return planner.chooseGroupPreviewScan(ctx, query, stream)
}
