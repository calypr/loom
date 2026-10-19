package execution

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/store/arango"
)

const (
	groupScanAssessmentTimeout    = 500 * time.Millisecond
	groupScanFullCollectionCutoff = 0.90
	// Projection assessment and execution use a fixed per-query cap; admission
	// leaves most of that cap available to other query state.
	groupPreviewQueryMemoryLimitBytes   int64 = 256 << 20
	groupPreviewProjectionMemoryShare         = 4
	groupPreviewProjectionSafetyFactor        = 8
	groupPreviewProjectionEntryOverhead       = 256
	groupPreviewProjectionMaxSampleRows       = 512
)

func (e *Engine) chooseGroupPreviewScan(ctx context.Context, query *compiler.CompiledQuery, stream *OutputStream) error {
	if query == nil || stream == nil || stream.stream == nil ||
		(query.PreviewGroupScan == nil && query.PreviewGroupProjection == nil) {
		return contextError(ctx)
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	assessmentCtx, cancel := context.WithTimeout(ctx, groupScanAssessmentTimeout)
	defer cancel()
	ordinaryQuery := query.Query
	ordinaryBinds := cloneBindVars(query.BindVars)
	disableIndex := false
	var selectedProjectionIndex *arango.PreviewCoveringIndex
	var assessedScopeCount int64
	assessedScopeCountKnown := false
	assessedScopeCountAttempted := false
	coveringIndexSelected := false
	if query.PreviewGroupScan != nil && e.previewExplainQuery != nil && query.PreviewCoveringIndex != nil && e.previewCoveringIndexes != nil {
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
				if explainErr != nil || !hasCoveringRootIndexNamed(plan, query.PreviewGroupScan.Collection, index.Name) {
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
				selectedDefinition := index
				selectedDefinition.Fields = append([]string(nil), index.Fields...)
				selectedDefinition.StoredValues = append([]string(nil), index.StoredValues...)
				selectedProjectionIndex = &selectedDefinition
				query.Query = indexedQuery
				query.BindVars = indexedBinds
				stream.query = indexedQuery
				stream.bindVars = indexedBinds
				coveringIndexSelected = true
				break
			}
		}
	}
	if !coveringIndexSelected && query.PreviewGroupScan != nil && e.previewExplainQuery != nil && assessmentCtx.Err() == nil {
		plan, err := e.previewExplainQuery(assessmentCtx, query.Query, query.BindVars)
		if err == nil && assessmentCtx.Err() == nil && hasNonCoveringRootIndex(plan, query.PreviewGroupScan.Collection) {
			if e.previewCollectionCount == nil && query.PreviewGroupProjection == nil {
				// Keep the canonical indexed query when the broad-scan decision lacks exact evidence.
			} else {
				projectionScopeCount := scopeCountMatchesProjection(query)
				assessedScopeCountAttempted = projectionScopeCount
				countCtx := assessmentCtx
				if projectionScopeCount {
					countCtx = arango.WithQueryMemoryLimit(assessmentCtx, groupPreviewQueryMemoryLimitBytes)
				}
				scanScopeCount, scanScopeCountKnown := readGroupPreviewCount(countCtx, stream, query.PreviewGroupScan.ScopeCountQuery, query.PreviewGroupScan.ScopeCountBindVars)
				if projectionScopeCount && scanScopeCountKnown {
					assessedScopeCount = scanScopeCount
					assessedScopeCountKnown = true
				}
				if scanScopeCountKnown && e.previewCollectionCount != nil {
					totalCount, countErr := e.previewCollectionCount(assessmentCtx, query.PreviewGroupScan.Collection)
					if countErr == nil && assessmentCtx.Err() == nil && totalCount > 0 && scanScopeCount >= 0 && scanScopeCount <= totalCount &&
						float64(scanScopeCount)/float64(totalCount) >= groupScanFullCollectionCutoff {
						query.Query = query.PreviewGroupScan.SequentialQuery
						query.BindVars = query.PreviewGroupScan.SequentialBindVars
						stream.query = query.Query
						stream.bindVars = query.BindVars
						disableIndex = true
					}
				}
			}
		}
	}
	if assessmentCtx.Err() != nil {
		return contextError(ctx)
	}
	if query.PreviewGroupProjection != nil && !disableIndex {
		if selectedIndex, selected := e.selectGroupProjectionIndex(assessmentCtx, query, selectedProjectionIndex); selected {
			if err := e.chooseGroupProjectionFactorization(assessmentCtx, query, stream, ordinaryQuery, ordinaryBinds, selectedIndex,
				assessedScopeCount, assessedScopeCountKnown, assessedScopeCountAttempted); err != nil {
				return err
			}
		}
	}
	return contextError(ctx)
}

func readGroupPreviewCount(ctx context.Context, stream *OutputStream, query string, bindVars map[string]any) (int64, bool) {
	if stream == nil || stream.stream == nil || query == "" {
		return 0, false
	}
	var count int64
	seen := false
	err := stream.stream(ctx, query, 1, bindVars, func(row map[string]any) error {
		if seen {
			return errors.New("root scope count returned more than one row")
		}
		value, ok := previewCountValue(row["count"])
		if !ok {
			return errors.New("root scope count returned an invalid count")
		}
		count = value
		seen = true
		return nil
	})
	return count, err == nil && seen && ctx.Err() == nil
}

func scopeCountMatchesProjection(query *compiler.CompiledQuery) bool {
	if query == nil || query.PreviewGroupScan == nil || query.PreviewGroupProjection == nil {
		return false
	}
	return query.PreviewGroupScan.ScopeCountQuery == query.PreviewGroupProjection.ScopeCountQuery &&
		reflect.DeepEqual(query.PreviewGroupScan.ScopeCountBindVars, query.PreviewGroupProjection.ScopeCountBindVars)
}

func (e *Engine) selectGroupProjectionIndex(ctx context.Context, query *compiler.CompiledQuery, preferred *arango.PreviewCoveringIndex) (arango.PreviewCoveringIndex, bool) {
	if query == nil || query.PreviewGroupProjection == nil || query.PreviewGroupProjection.ReadOnlyIndex == nil ||
		query.PreviewGroupProjection.Collection == "" || query.PreviewGroupProjection.ScopeCountQuery == "" ||
		e.previewExplainQuery == nil || ctx.Err() != nil {
		return arango.PreviewCoveringIndex{}, false
	}
	spec := query.PreviewGroupProjection
	var indexes []arango.PreviewCoveringIndex
	if preferred != nil {
		indexes = []arango.PreviewCoveringIndex{*preferred}
	} else {
		if e.previewCoveringIndexes == nil {
			return arango.PreviewCoveringIndex{}, false
		}
		var err error
		indexes, err = e.previewCoveringIndexes(ctx, spec.Collection)
		if err != nil || ctx.Err() != nil {
			return arango.PreviewCoveringIndex{}, false
		}
	}
	for _, index := range compatibleGroupProjectionIndexes(spec.Collection, spec.ReadOnlyIndex, indexes) {
		if ctx.Err() != nil {
			return arango.PreviewCoveringIndex{}, false
		}
		factoredQuery, factoredBinds, renderErr := compiler.RenderPreviewGroupProjectionFactorization(query.PhysicalPlan, index.Name, false)
		if renderErr != nil || factoredQuery == "" {
			continue
		}
		plan, explainErr := e.previewExplainQuery(ctx, factoredQuery, factoredBinds)
		if explainErr != nil || ctx.Err() != nil || !hasCoveringRootIndexNamed(plan, spec.Collection, index.Name) {
			continue
		}
		index.Fields = append([]string(nil), index.Fields...)
		index.StoredValues = append([]string(nil), index.StoredValues...)
		return index, true
	}
	return arango.PreviewCoveringIndex{}, false
}

func compatibleGroupProjectionIndexes(collection string, required *compiler.PreviewGroupProjectionIndexSpec, indexes []arango.PreviewCoveringIndex) []arango.PreviewCoveringIndex {
	if required == nil || len(required.RequiredFields) == 0 || required.ProjectionPath == "" {
		return nil
	}
	requiredFields := make(map[string]struct{}, len(required.RequiredFields))
	for _, field := range required.RequiredFields {
		if field == "" {
			return nil
		}
		requiredFields[field] = struct{}{}
	}
	compatible := make([]arango.PreviewCoveringIndex, 0, len(indexes))
	for _, index := range indexes {
		if index.Collection != collection || !strings.HasPrefix(index.Name, "loom_pivot_preview_") || len(index.Fields) == 0 {
			continue
		}
		indexFields := make(map[string]struct{}, len(index.Fields))
		valid := true
		for _, field := range index.Fields {
			if field == "" {
				valid = false
				break
			}
			if _, duplicate := indexFields[field]; duplicate {
				valid = false
				break
			}
			indexFields[field] = struct{}{}
		}
		if !valid {
			continue
		}
		for field := range requiredFields {
			if _, ok := indexFields[field]; !ok {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		projectionCovered := false
		for _, field := range index.Fields {
			if field == required.ProjectionPath {
				projectionCovered = true
				break
			}
		}
		if !projectionCovered {
			for _, storedValue := range index.StoredValues {
				if storedValue == required.ProjectionPath {
					projectionCovered = true
					break
				}
			}
		}
		if projectionCovered {
			compatible = append(compatible, index)
		}
	}
	sort.Slice(compatible, func(left, right int) bool {
		leftWidth := len(compatible[left].Fields) + len(compatible[left].StoredValues)
		rightWidth := len(compatible[right].Fields) + len(compatible[right].StoredValues)
		if leftWidth != rightWidth {
			return leftWidth < rightWidth
		}
		return compatible[left].Name < compatible[right].Name
	})
	return compatible
}

func hasCoveringRootIndexNamed(result arango.ExplainResult, collection, indexName string) bool {
	plan := singleExplainPlan(result)
	if plan == nil || collection == "" || indexName == "" {
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
			found := false
			for _, index := range node.Indexes {
				if index.Name == indexName && (index.Collection == "" || index.Collection == collection) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return rootIndexes == 1
}

func (e *Engine) chooseGroupProjectionFactorization(ctx context.Context, query *compiler.CompiledQuery, stream *OutputStream,
	ordinaryQuery string, ordinaryBinds map[string]any, selectedIndex arango.PreviewCoveringIndex,
	assessedScopeCount int64, assessedScopeCountKnown, assessedScopeCountAttempted bool) error {
	spec := query.PreviewGroupProjection
	if spec == nil || spec.Collection == "" || spec.ScopeCountQuery == "" || e.previewCollectionRevision == nil {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	revision, revisionErr := e.previewCollectionRevision(ctx, spec.Collection)
	if ctx.Err() != nil {
		return nil
	}
	if revisionErr != nil || revision == "" {
		return nil
	}
	indexName := selectedIndex.Name
	indexIdentity := groupProjectionIndexIdentity(selectedIndex)
	decisionKey, _, keyOK := newGroupPreviewProjectionDecisionKey(ordinaryQuery, ordinaryBinds, spec.Collection, revision, indexIdentity)
	if !keyOK {
		return nil
	}
	if decision, found := e.groupPreviewRowsCache.getDecision(decisionKey); found {
		if decision.Factorized && applyGroupProjectionFactorization(query, stream, indexName) {
			stream.projectionDecisionKey = &decisionKey
			stream.projectionDecision = &decision
		}
		return nil
	}

	if !assessedScopeCountKnown && !assessedScopeCountAttempted {
		countCtx := arango.WithQueryMemoryLimit(ctx, groupPreviewQueryMemoryLimitBytes)
		assessedScopeCount, assessedScopeCountKnown = readGroupPreviewCount(countCtx, stream, spec.ScopeCountQuery, spec.ScopeCountBindVars)
	}
	if !assessedScopeCountKnown || assessedScopeCount <= 0 {
		return nil
	}
	assessmentQuery, assessmentBinds, renderErr := compiler.RenderPreviewGroupProjectionAssessment(query.PhysicalPlan, indexName, false)
	if renderErr != nil || assessmentQuery == "" {
		return nil
	}
	assessmentCtx := arango.WithQueryMemoryLimit(ctx, groupPreviewQueryMemoryLimitBytes)
	var sample groupProjectionSample
	seen := false
	err := stream.stream(assessmentCtx, assessmentQuery, 1, assessmentBinds, func(row map[string]any) error {
		if seen {
			return errors.New("Group projection sample returned more than one row")
		}
		parsed, ok := parseGroupProjectionSample(row)
		if !ok {
			return errors.New("Group projection sample returned invalid metadata")
		}
		sample = parsed
		seen = true
		return nil
	})
	if err != nil || ctx.Err() != nil || !seen {
		return nil
	}
	decision := groupPreviewProjectionDecision{
		SampleRows: sample.sampleRows, DistinctProjections: sample.distinctProjections,
		SerializedCharacters: sample.serializedCharacters,
	}
	if sample.sampleRows == 0 && sample.distinctProjections == 0 && sample.serializedCharacters == 0 {
		e.groupPreviewRowsCache.putDecision(decisionKey, decision)
		return nil
	}
	if !validGroupProjectionSample(sample, assessedScopeCount) {
		return nil
	}
	estimate, estimateOK := estimateGroupProjectionDictionaryBytes(sample, assessedScopeCount)
	if !estimateOK {
		return nil
	}
	decision.EstimatedDictionaryBytes = estimate
	decision.Factorized = sample.distinctProjections*8 <= sample.sampleRows &&
		estimate < uint64(groupPreviewQueryMemoryLimitBytes/groupPreviewProjectionMemoryShare)
	if decision.Factorized {
		if !applyGroupProjectionFactorization(query, stream, indexName) {
			return nil
		}
		stream.projectionDecisionKey = &decisionKey
		stream.projectionDecision = &decision
	} else {
		e.groupPreviewRowsCache.putDecision(decisionKey, decision)
	}
	return nil
}

type groupProjectionSample struct {
	sampleRows           int64
	distinctProjections  int64
	serializedCharacters int64
}

func parseGroupProjectionSample(row map[string]any) (groupProjectionSample, bool) {
	sampleRows, rowsOK := previewCountValue(row["sampleRows"])
	distinct, distinctOK := previewCountValue(row["distinctProjections"])
	serialized, serializedOK := previewCountValue(row["serializedCharacters"])
	return groupProjectionSample{sampleRows: sampleRows, distinctProjections: distinct, serializedCharacters: serialized},
		rowsOK && distinctOK && serializedOK
}

func validGroupProjectionSample(sample groupProjectionSample, scopedRows int64) bool {
	return sample.sampleRows > 0 && sample.sampleRows <= groupPreviewProjectionMaxSampleRows &&
		sample.distinctProjections > 0 && sample.distinctProjections <= sample.sampleRows &&
		sample.serializedCharacters >= sample.distinctProjections && scopedRows >= sample.sampleRows
}

func estimateGroupProjectionDictionaryBytes(sample groupProjectionSample, scopedRows int64) (uint64, bool) {
	if scopedRows <= 0 || !validGroupProjectionSample(sample, scopedRows) {
		return 0, false
	}
	rows, distinct := uint64(sample.sampleRows), uint64(sample.distinctProjections)
	population := uint64(scopedRows)
	quotient, remainder := population/rows, population%rows
	maxUint64 := ^uint64(0)
	if quotient > maxUint64/distinct || remainder > maxUint64/distinct {
		return 0, false
	}
	base := quotient * distinct
	tailNumerator := remainder * distinct
	if tailNumerator > maxUint64-(rows-1) {
		return 0, false
	}
	tail := (tailNumerator + rows - 1) / rows
	if base > maxUint64-tail {
		return 0, false
	}
	base += tail
	if base > population/groupPreviewProjectionSafetyFactor {
		base = population
	} else {
		base *= groupPreviewProjectionSafetyFactor
	}
	if base == 0 {
		base = 1
	}
	avgSerialized := (uint64(sample.serializedCharacters) + distinct - 1) / distinct
	if avgSerialized > (maxUint64-groupPreviewProjectionEntryOverhead)/4 {
		return 0, false
	}
	entryBytes := avgSerialized*4 + groupPreviewProjectionEntryOverhead
	if base > maxUint64/entryBytes {
		return 0, false
	}
	return base * entryBytes, true
}

func groupProjectionIndexIdentity(selected arango.PreviewCoveringIndex) string {
	encoded, err := json.Marshal(selected)
	if err == nil {
		return "index:" + string(encoded)
	}
	return "index:" + selected.Collection + ":" + selected.Name
}

func applyGroupProjectionFactorization(query *compiler.CompiledQuery, stream *OutputStream, indexName string) bool {
	factoredQuery, factoredBinds, err := compiler.RenderPreviewGroupProjectionFactorization(query.PhysicalPlan, indexName, false)
	if err != nil || factoredQuery == "" {
		return false
	}
	query.Query = factoredQuery
	query.BindVars = factoredBinds
	stream.query = factoredQuery
	stream.bindVars = factoredBinds
	stream.queryMemoryLimit = groupPreviewQueryMemoryLimitBytes
	return true
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
