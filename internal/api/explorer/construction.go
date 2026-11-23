package explorerapi

import (
	"context"
	"time"

	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func constructionProposalPreviewBudget(ctx context.Context, candidate authoringv2.Construction) (context.Context, time.Duration) {
	for _, step := range candidate.Steps {
		if step.Operation.Kind == authoringv2.ConstructionOperationPivot {
			return executionarango.WithPivotPreviewQueryBudget(ctx), executionarango.PivotCategoryScanTimeout
		}
	}
	return ctx, executionarango.ExplorerPreviewTimeout
}
