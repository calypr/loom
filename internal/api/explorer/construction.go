package explorerapi

import (
	"context"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
)

func constructionProposalPreviewBudget(ctx context.Context, candidate ws.Construction) (context.Context, time.Duration) {
	for _, step := range candidate.Steps {
		if step.Operation.Kind == ws.ConstructionOperationPivot {
			return executionarango.WithPivotPreviewQueryBudget(ctx), executionarango.PivotCategoryScanTimeout
		}
	}
	return ctx, executionarango.ExplorerPreviewTimeout
}
