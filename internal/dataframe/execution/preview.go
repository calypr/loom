package execution

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/store/arango"
)

func (e *Engine) Preview(ctx context.Context, resolved Resolved, limit int) (map[string][]map[string]any, error) {
	limit, err := normalizePreviewLimit(limit)
	if err != nil {
		return nil, err
	}
	selected := resolved.Semantic.SemanticPlan.Bindings.OutputNames
	if len(selected) == 0 {
		selected = make([]string, 0, len(resolved.Compiled.Outputs))
		for _, output := range resolved.Compiled.Outputs {
			selected = append(selected, output.Name)
		}
	}
	for _, name := range selected {
		if !hasCompiledOutput(resolved, name) {
			return nil, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "requested preview output is not available", map[string]any{"output": name})
		}
	}
	result := make(map[string][]map[string]any, len(selected))
	for _, name := range selected {
		rows := make([]map[string]any, 0, limit)
		_, err := e.PreviewOutput(ctx, resolved, PreviewRequest{Output: name, Limit: limit}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			return nil, err
		}
		result[name] = rows
	}
	return result, nil
}

func hasCompiledOutput(resolved Resolved, name string) bool {
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == name {
			return true
		}
	}
	return false
}

func (e *Engine) PreviewOutput(ctx context.Context, resolved Resolved, request PreviewRequest, visit func(map[string]any) error) (PreviewSummary, error) {
	if err := contextError(ctx); err != nil {
		return PreviewSummary{}, err
	}
	limit, err := normalizePreviewLimit(request.Limit)
	if err != nil {
		return PreviewSummary{}, err
	}
	if strings.TrimSpace(request.Output) == "" {
		return PreviewSummary{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "preview output is required", nil)
	}
	if visit == nil {
		return PreviewSummary{}, previewAdmissionError(dataframeerrors.CodeInvalidRequest, "preview visitor is required", nil)
	}
	preview := previewExecutor{
		streamForOutput:           e.outputStreams().streamForOutput,
		preparePreviewIndex:       e.preparePreviewIndex,
		previewQueryRows:          e.previewQueryRows,
		withPreviewReadSnapshot:   e.withPreviewReadSnapshot,
		chooseGroupPreviewScan:    e.chooseGroupPreviewScan,
		previewCollectionRevision: e.previewCollectionRevision,
		groupPreviewRowsCache:     &e.groupPreviewRowsCache,
		scheduleIndexPrewarm: func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) {
			schedulePreviewIndexPrewarm(ctx, e, spec)
		},
	}
	return preview.execute(ctx, resolved, request, visit, limit)
}

type previewExecutor struct {
	streamForOutput           func(Resolved, string, int) (OutputStream, compiler.CompiledQuery, error)
	preparePreviewIndex       func(context.Context, compiler.PreviewCoveringIndexSpec) error
	previewQueryRows          QueryRows
	withPreviewReadSnapshot   WithPreviewReadSnapshot
	chooseGroupPreviewScan    func(context.Context, *compiler.CompiledQuery, *OutputStream) error
	previewCollectionRevision func(context.Context, string) (string, error)
	groupPreviewRowsCache     *groupPreviewRowsCache
	scheduleIndexPrewarm      func(context.Context, compiler.PreviewCoveringIndexSpec)
}

func (preview previewExecutor) execute(ctx context.Context, resolved Resolved, request PreviewRequest, visit func(map[string]any) error, limit int) (PreviewSummary, error) {
	loweringStarted := time.Now()
	stream, query, err := preview.streamForOutput(resolved, request.Output, limit)
	if err != nil {
		if _, ok := dataframeerrors.AsUserError(err); ok {
			return PreviewSummary{}, err
		}
		return PreviewSummary{}, dataframeerrors.Wrap(err, dataframeerrors.CodeRecipeContractViolation, "preview plan compilation failed")
	}
	if err := contextError(ctx); err != nil {
		return PreviewSummary{}, err
	}
	if err := validatePreviewPlan(query, limit, stream.physicalEngine); err != nil {
		return PreviewSummary{}, err
	}
	if query.PreviewCoveringIndex != nil && !query.PreviewCoveringIndex.PrepareAfterPreview && preview.preparePreviewIndex != nil {
		prepareCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		_ = preview.preparePreviewIndex(prepareCtx, *query.PreviewCoveringIndex)
		cancel()
		if err := contextError(ctx); err != nil {
			return PreviewSummary{}, err
		}
	}
	if stream.physicalEngine != ir.PhysicalEngineClickHouse && preview.previewQueryRows != nil {
		stream.stream = preview.previewQueryRows
	}
	stream.withPreviewReadSnapshot = preview.withPreviewReadSnapshot
	if err := preview.chooseGroupPreviewScan(ctx, &query, &stream); err != nil {
		return PreviewSummary{}, err
	}
	loweringDuration := time.Since(loweringStarted)
	var previewCacheKey groupPreviewRowsKey
	var previewCacheKeyWeight int
	var previewCacheEnabled bool
	var previewCacheHitRows []map[string]any
	if query.PreviewCoveringIndex != nil && (query.PreviewCoveringIndex.PrepareAfterPreview || query.PreviewGroupScan != nil) && preview.previewCollectionRevision != nil {
		revision, revisionErr := preview.previewCollectionRevision(ctx, query.PreviewCoveringIndex.Collection)
		if err := contextError(ctx); err != nil {
			return PreviewSummary{}, err
		}
		if revisionErr == nil {
			key, keyWeight, keyOK := newGroupPreviewRowsKey(query.Query, query.BindVars, query.PreviewCoveringIndex.Collection, revision, limit)
			if keyOK {
				previewCacheKey = key
				previewCacheKeyWeight = keyWeight
				if encodedRows, ok := preview.groupPreviewRowsCache.get(key); ok {
					if rows, decoded := decodeGroupPreviewRows(encodedRows); decoded {
						previewCacheHitRows = rows
					} else {
						preview.groupPreviewRowsCache.delete(key)
					}
				}
				previewCacheEnabled = true
			}
		}
	}
	summary := PreviewSummary{Output: stream.Name, Columns: append([]string(nil), stream.Columns...), PlanMode: query.PlanMode, PlanProfile: query.PlanProfile, PlanFingerprint: query.PlanDiagnostics.Fingerprint, TraversalCount: query.TraversalCount, LoweringDuration: loweringDuration, Complete: !stream.sampledAggregate, PartialValidation: query.PartialValidation}
	for _, output := range resolved.Compiled.Outputs {
		if output.Name == request.Output {
			summary.RowLineageCapability = compiler.RowLineageCapabilityForOutput(output)
			break
		}
	}
	count := 0
	var visitorErr error
	queryStarted := time.Now()
	consumeRow := func(row map[string]any) error {
		if err := contextError(ctx); err != nil {
			return err
		}
		if count >= limit {
			return errPreviewLimit
		}
		resolvedRow, err := materializePostQueryRowWithChecks(row, stream.DynamicChecks)
		if err != nil {
			return err
		}
		includeRowIdentity := request.IncludeRowIdentity || resolved.Semantic.SemanticPlan.Bindings.IncludeRowIdentity
		if includeRowIdentity {
			if err := ensureStableRowIdentity(resolvedRow, stream.RowIdentity, query.BindVars); err != nil {
				return err
			}
		}
		public := publicPreviewRow(resolvedRow, stream.Columns, includeRowIdentity)
		if resolved.Semantic.SemanticPlan.Bindings.IncludeSourceIdentity {
			public[previewSourceMetadataKey] = previewRowSource(resolvedRow, query.RootResourceType, stream.sourceIdentityMode)
		}
		if err := visit(public); err != nil {
			visitorErr = err
			return err
		}
		count++
		if count >= limit {
			return errPreviewLimit
		}
		return nil
	}
	var queryErr error
	var capture groupPreviewRowsCapture
	if previewCacheHitRows != nil {
		for _, row := range previewCacheHitRows {
			if err := consumeRow(row); err != nil {
				queryErr = err
				break
			}
		}
	} else {
		if previewCacheEnabled {
			capture = newGroupPreviewRowsCapture(previewCacheKeyWeight)
		}
		queryErr = stream.streamRaw(ctx, func(row map[string]any) error {
			var encoded []byte
			if previewCacheEnabled {
				encoded = capture.encode(row)
			}
			rowErr := consumeRow(row)
			if encoded != nil && (rowErr == nil || errors.Is(rowErr, errPreviewLimit)) && visitorErr == nil {
				capture.append(encoded)
			}
			return rowErr
		})
	}
	summary.RowCount = count
	summary.QueryDuration = time.Since(queryStarted)
	if visitorErr != nil {
		return summary, normalizePreviewError(visitorErr, false)
	}
	if queryErr != nil && !errors.Is(queryErr, errPreviewLimit) {
		preview.rejectGroupProjectionDecisionAfterResourceLimit(stream, queryErr)
		return summary, normalizePreviewError(queryErr, true)
	}
	if errors.Is(queryErr, errPreviewLimit) {
		summary.Complete = false
		summary.Truncated = true
	}
	if err := contextError(ctx); err != nil {
		return summary, err
	}
	if err := preview.confirmGroupProjectionDecision(ctx, stream); err != nil {
		return summary, err
	}
	if previewCacheEnabled && previewCacheHitRows == nil && capture.cacheable &&
		(visitorErr == nil) && (queryErr == nil || errors.Is(queryErr, errPreviewLimit)) {
		revision, revisionErr := preview.previewCollectionRevision(ctx, previewCacheKey.collection)
		if err := contextError(ctx); err != nil {
			return summary, err
		}
		if revisionErr == nil && revision == previewCacheKey.revision {
			preview.groupPreviewRowsCache.put(previewCacheKey, capture.rows)
		}
	}
	if query.PreviewCoveringIndex != nil && query.PreviewCoveringIndex.PrepareAfterPreview {
		preview.scheduleIndexPrewarm(ctx, *query.PreviewCoveringIndex)
	}
	return summary, nil
}

func (preview previewExecutor) rejectGroupProjectionDecisionAfterResourceLimit(stream OutputStream, queryErr error) {
	if queryErr == nil || stream.projectionDecisionKey == nil || stream.projectionDecision == nil || !arango.IsQueryResourceLimitExceeded(queryErr) {
		return
	}
	decision := *stream.projectionDecision
	decision.Factorized = false
	preview.groupPreviewRowsCache.putDecision(*stream.projectionDecisionKey, decision)
}

func (preview previewExecutor) confirmGroupProjectionDecision(ctx context.Context, stream OutputStream) error {
	if stream.projectionDecisionKey == nil || stream.projectionDecision == nil || preview.previewCollectionRevision == nil {
		return nil
	}
	key := *stream.projectionDecisionKey
	revision, err := preview.previewCollectionRevision(ctx, key.collection)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err == nil && revision == key.revision {
		preview.groupPreviewRowsCache.putDecision(key, *stream.projectionDecision)
	}
	return nil
}

const (
	previewSourceMetadataKey = "__loom_preview_source"
	previewSourceSingle      = "SINGLE"
	previewSourceComposite   = "COMPOSITE"
	previewSourceUnavailable = "UNAVAILABLE"
)

func previewSourceIdentityMode(output lower.CompiledRecipeOutput, querySchema []lower.CompiledOutputColumn) string {
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupRowsOp || operation.Kind == ir.PhysicalGroupedPivotOp {
			return previewSourceComposite
		}
	}
	for _, stage := range output.Stages {
		if stage.Operation == string(ir.PhysicalStageCohortGroupOp) || stage.Operation == string(recipe.ConstructionGroupOp) ||
			stage.Operation == string(recipe.ConstructionPivotOp) || stage.Operation == string(recipe.ConstructionCodedGroupOp) ||
			stage.Operation == string(recipe.ConstructionRelatedExpandOp) {
			return previewSourceComposite
		}
	}
	if output.Plan.Engine == ir.PhysicalEngineClickHouse {
		return previewSourceUnavailable
	}
	for _, column := range querySchema {
		if column.Name == ir.PreviewSourceResourceIDColumn && column.Internal {
			return previewSourceSingle
		}
	}
	return previewSourceUnavailable
}

func previewRowSource(row map[string]any, resourceType, mode string) map[string]any {
	switch mode {
	case previewSourceComposite:
		return map[string]any{"kind": previewSourceComposite}
	case previewSourceSingle:
		id, ok := row[ir.PreviewSourceResourceIDColumn].(string)
		if ok && strings.TrimSpace(id) != "" && strings.TrimSpace(resourceType) != "" {
			return map[string]any{"kind": previewSourceSingle, "resourceType": resourceType, "id": id}
		}
	}
	return map[string]any{"kind": previewSourceUnavailable}
}

func normalizePreviewLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultPreviewLimit, nil
	}
	if limit < 1 || limit > MaxPreviewLimit {
		return 0, previewAdmissionError(dataframeerrors.CodeInvalidLimit, "preview limit is outside the supported range", map[string]any{"maximum": MaxPreviewLimit})
	}
	return limit, nil
}

func validatePreviewPlan(query compiler.CompiledQuery, limit int, engine ir.PhysicalEngine) error {
	if engine == ir.PhysicalEngineClickHouse {
		if query.PlanMode != "clickhouse" || query.PlanProfile != "pinned_table_combine" || strings.TrimSpace(query.PlanDiagnostics.Fingerprint) == "" || query.Limit != limit {
			return previewAdmissionError(dataframeerrors.CodePlanTooExpensive, "compiled ClickHouse combine plan is not in the approved preview plan class", nil)
		}
		return nil
	}
	if query.PlanMode != previewPlanMode || query.PlanProfile != previewPlanProfile || strings.TrimSpace(query.PlanDiagnostics.Fingerprint) == "" || query.Limit != limit {
		return previewAdmissionError(dataframeerrors.CodePlanTooExpensive, "compiled preview plan is not in the approved preview plan class", nil)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return dataframeerrors.Normalize(err)
	}
	return nil
}

func previewAdmissionError(code dataframeerrors.ErrorCode, message string, details map[string]any) error {
	options := []dataframeerrors.ErrorOption{dataframeerrors.WithRetryable(false)}
	if details != nil {
		options = append(options, dataframeerrors.WithDetails(details))
	}
	return dataframeerrors.NewError(code, message, options...)
}

func normalizePreviewError(err error, backend bool) error {
	if err == nil {
		return nil
	}
	var drift *DynamicDriftError
	if errors.As(err, &drift) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeDynamicSchemaDrift, "dynamic schema drift", dataframeerrors.WithDetails(map[string]any{"dynamic_map": drift.DynamicName, "frozen_key_count": drift.FrozenKeyCount}))
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		return userErr
	}
	if errors.Is(err, context.Canceled) {
		return dataframeerrors.Normalize(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodePreviewTimeout, dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.CodePreviewTimeout, "")), dataframeerrors.WithRetryable(true))
	}
	if backend {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Normalize(err)
}
