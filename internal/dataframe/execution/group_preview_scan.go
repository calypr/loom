package execution

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/store/arango"
)

const (
	groupScanAssessmentTimeout    = 500 * time.Millisecond
	groupScanFullCollectionCutoff = 0.90
)

func (e *Engine) chooseGroupPreviewScan(ctx context.Context, query *compiler.CompiledQuery, stream *OutputStream) error {
	if query == nil || stream == nil || query.PreviewGroupScan == nil || stream.stream == nil ||
		e.previewExplainQuery == nil || e.previewCollectionCount == nil {
		return contextError(ctx)
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	assessmentCtx, cancel := context.WithTimeout(ctx, groupScanAssessmentTimeout)
	defer cancel()
	plan, err := e.previewExplainQuery(assessmentCtx, query.Query, query.BindVars)
	if err != nil {
		return contextError(ctx)
	}
	if assessmentCtx.Err() != nil || !hasNonCoveringRootIndex(plan, query.PreviewGroupScan.Collection) {
		return contextError(ctx)
	}

	var scopedCount int64
	countSeen := false
	err = stream.stream(assessmentCtx, query.PreviewGroupScan.ScopeCountQuery, 1, query.PreviewGroupScan.ScopeCountBindVars, func(row map[string]any) error {
		if countSeen {
			return errors.New("root scope count returned more than one row")
		}
		value, ok := previewCountValue(row["count"])
		if !ok {
			return errors.New("root scope count returned an invalid count")
		}
		scopedCount = value
		countSeen = true
		return nil
	})
	if err != nil || assessmentCtx.Err() != nil || !countSeen {
		return contextError(ctx)
	}
	totalCount, err := e.previewCollectionCount(assessmentCtx, query.PreviewGroupScan.Collection)
	if err != nil || assessmentCtx.Err() != nil || totalCount <= 0 || scopedCount < 0 || scopedCount > totalCount {
		return contextError(ctx)
	}
	if float64(scopedCount)/float64(totalCount) < groupScanFullCollectionCutoff {
		return contextError(ctx)
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	query.Query = query.PreviewGroupScan.SequentialQuery
	query.BindVars = query.PreviewGroupScan.SequentialBindVars
	stream.query = query.Query
	stream.bindVars = query.BindVars
	return nil
}

func hasNonCoveringRootIndex(result arango.ExplainResult, collection string) bool {
	var plan *arango.ExplainPlan
	switch {
	case result.Plan != nil && len(result.Plans) == 0:
		plan = result.Plan
	case result.Plan == nil && len(result.Plans) == 1:
		plan = &result.Plans[0]
	default:
		return false
	}
	if plan == nil {
		return false
	}
	rootIndexes := 0
	for _, node := range plan.Nodes {
		if node.Collection != collection {
			continue
		}
		switch node.Type {
		case "EnumerateCollectionNode":
			return false
		case "IndexNode":
			rootIndexes++
			if node.IndexCoversProjections == nil || *node.IndexCoversProjections ||
				node.IndexCoversOutProjections == nil || *node.IndexCoversOutProjections {
				return false
			}
		}
	}
	return rootIndexes == 1
}

func previewCountValue(value any) (int64, bool) {
	switch count := value.(type) {
	case int:
		return int64(count), count >= 0
	case int32:
		return int64(count), count >= 0
	case int64:
		return count, count >= 0
	case uint:
		if uint64(count) > math.MaxInt64 {
			return 0, false
		}
		return int64(count), true
	case uint32:
		return int64(count), true
	case uint64:
		if count > math.MaxInt64 {
			return 0, false
		}
		return int64(count), true
	case float64:
		if count < 0 || count >= float64(math.MaxInt64) || math.Trunc(count) != count {
			return 0, false
		}
		return int64(count), true
	default:
		return 0, false
	}
}
