package execution

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
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
		e.previewExplainQuery == nil {
		return contextError(ctx)
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	assessmentCtx, cancel := context.WithTimeout(ctx, groupScanAssessmentTimeout)
	defer cancel()
	if query.PreviewCoveringIndex != nil && e.previewCoveringIndexes != nil {
		indexes, indexErr := e.previewCoveringIndexes(assessmentCtx, query.PreviewGroupScan.Collection)
		if err := contextError(ctx); err != nil {
			return err
		}
		if indexErr == nil {
			for _, index := range compatibleGroupPreviewIndexes(query.PreviewCoveringIndex, indexes) {
				if assessmentCtx.Err() != nil {
					return contextError(ctx)
				}
				indexedQuery, indexedBinds, renderErr := compiler.RenderPreviewPhysicalPlanWithRootIndexHint(query.PhysicalPlan, index.Name)
				if renderErr != nil {
					continue
				}
				plan, explainErr := e.previewExplainQuery(assessmentCtx, indexedQuery, indexedBinds)
				if assessmentCtx.Err() != nil {
					return contextError(ctx)
				}
				if explainErr != nil || !hasCoveringRootIndex(plan, query.PreviewGroupScan.Collection) {
					continue
				}
				selected := *query.PreviewCoveringIndex
				selected.Collection = index.Collection
				selected.Name = index.Name
				selected.Fields = append([]string(nil), index.Fields...)
				selected.StoredValues = append([]string(nil), index.StoredValues...)
				selected.Supersedes = nil
				selected.PrepareAfterPreview = false
				query.PreviewCoveringIndex = &selected
				query.Query = indexedQuery
				query.BindVars = indexedBinds
				stream.query = indexedQuery
				stream.bindVars = indexedBinds
				return contextError(ctx)
			}
		}
	}
	if assessmentCtx.Err() != nil {
		return contextError(ctx)
	}
	plan, err := e.previewExplainQuery(assessmentCtx, query.Query, query.BindVars)
	if err != nil {
		return contextError(ctx)
	}
	if assessmentCtx.Err() != nil || !hasNonCoveringRootIndex(plan, query.PreviewGroupScan.Collection) {
		return contextError(ctx)
	}
	if e.previewCollectionCount == nil {
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

func compatibleGroupPreviewIndexes(required *compiler.PreviewCoveringIndexSpec, indexes []arango.PreviewCoveringIndex) []arango.PreviewCoveringIndex {
	if required == nil || len(required.Fields) == 0 {
		return nil
	}
	requiredFields := make(map[string]struct{}, len(required.Fields))
	for _, field := range required.Fields {
		if field == "" {
			return nil
		}
		requiredFields[field] = struct{}{}
	}
	compatible := make([]arango.PreviewCoveringIndex, 0, len(indexes))
	for _, index := range indexes {
		if index.Collection != required.Collection || !strings.HasPrefix(index.Name, "loom_pivot_preview_") ||
			len(index.Fields) < len(requiredFields) {
			continue
		}
		indexFields := make(map[string]struct{}, len(index.Fields))
		for _, field := range index.Fields {
			indexFields[field] = struct{}{}
		}
		covers := len(indexFields) == len(index.Fields)
		for field := range requiredFields {
			if _, ok := indexFields[field]; !ok {
				covers = false
				break
			}
		}
		if covers {
			compatible = append(compatible, index)
		}
	}
	sort.Slice(compatible, func(left, right int) bool {
		leftExact := compatible[left].Name == required.Name
		if leftExact != (compatible[right].Name == required.Name) {
			return leftExact
		}
		leftExtra := len(compatible[left].Fields) - len(requiredFields)
		rightExtra := len(compatible[right].Fields) - len(requiredFields)
		if leftExtra != rightExtra {
			return leftExtra < rightExtra
		}
		return compatible[left].Name < compatible[right].Name
	})
	return compatible
}

func hasCoveringRootIndex(result arango.ExplainResult, collection string) bool {
	plan := singleExplainPlan(result)
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
			if node.IndexCoversProjections == nil || !*node.IndexCoversProjections ||
				node.IndexCoversOutProjections == nil || !*node.IndexCoversOutProjections {
				return false
			}
		}
	}
	return rootIndexes == 1
}

func hasNonCoveringRootIndex(result arango.ExplainResult, collection string) bool {
	plan := singleExplainPlan(result)
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

func singleExplainPlan(result arango.ExplainResult) *arango.ExplainPlan {
	switch {
	case result.Plan != nil && len(result.Plans) == 0:
		return result.Plan
	case result.Plan == nil && len(result.Plans) == 1:
		return &result.Plans[0]
	default:
		return nil
	}
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
