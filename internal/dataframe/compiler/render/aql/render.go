package aql

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderedPhysicalPlan is an executable AQL representation of a validated
// PhysicalPlan. BindVars is independent of the input plan and uses Arango's
// required "@name" key form for collection bind variables referenced as
// "@@name" in Query.
//
// This renderer covers generic physical navigation and rich expression
// operators emitted by BuildGenericPhysicalPlan. Projection names, including
// nested object field names, remain bind-backed and never become AQL source.
type RenderedPhysicalPlan struct {
	Query             string
	BindVars          map[string]any
	PartialValidation bool
}

var rootIndexHintPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
var previewSourcePathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// RenderPhysicalPlan renders a validated physical plan to deterministic AQL.
// It keeps data and metadata values out of the generated AQL source.
func RenderPhysicalPlan(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	return renderPhysicalPlan(plan, "")
}

// RenderPhysicalPlanWithRootIndexHint renders one direct root scan with a
// non-forcing persistent-index hint. The hint can improve covering reads, but
// Arango remains free to choose another plan when the index does not help.
func RenderPhysicalPlanWithRootIndexHint(plan ir.PhysicalPlan, indexHint string) (RenderedPhysicalPlan, error) {
	if !rootIndexHintPattern.MatchString(indexHint) {
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint %q is not a safe index name", indexHint)
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp ||
		plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint requires one direct root scan")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{rootIndexHint: indexHint})
}

// RenderPhysicalPlanWithRootIndexDisabled renders a plan with Arango's
// per-FOR full-scan option on its direct root scan. It is reserved for
// compiler-proven preview alternatives; ordinary callers should let Arango
// choose its scan plan.
func RenderPhysicalPlanWithRootIndexDisabled(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp ||
		plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("disabled root index requires one direct root scan")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{disableRootIndex: true})
}

// RenderPhysicalRootScopeCount renders an exact count of a direct source
// plan's canonical project, generation, and authorization scope. It drops
// source projections and is only valid when those are the plan's sole row
// filters.
func RenderPhysicalRootScopeCount(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	if plan.Engine == ir.PhysicalEngineClickHouse || plan.StageSequence == nil || len(plan.Operations) < 5 {
		return RenderedPhysicalPlan{}, fmt.Errorf("root scope count requires a staged AQL source plan")
	}
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate root scope count plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("verify root scope count source: %w", err)
	}
	root := plan.Operations[0]
	if root.Kind != ir.PhysicalRootScanOp || root.RootScan == nil || root.RootScan.Population != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("root scope count requires one direct root scan")
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	renderer := physicalPlanRenderer{
		bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys: collectionKeys,
		reservedVars:   physicalPlanVariableNames(plan),
		internalPrefix: "preview_",
	}
	lines, err := renderer.renderRootScan(*root.RootScan)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render root scope count scan: %w", err)
	}
	filters, authLets, returns := 0, 0, 0
	for index, operation := range plan.Operations[1:] {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			filters++
			rendered, renderErr := renderer.renderScopeOperation(operation, "  ")
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render root scope count filter %d: %w", index, renderErr)
			}
			lines = append(lines, rendered...)
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil || operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
				continue
			}
			authLets++
			rendered, renderErr := renderer.renderScopeOperation(operation, "  ")
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render root scope count authorization: %w", renderErr)
			}
			lines = append(lines, rendered...)
		case ir.PhysicalExpressionLetOp:
			// Source projection calculations do not affect row membership.
		case ir.PhysicalReturnOp:
			if index != len(plan.Operations)-2 || operation.Return == nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("root scope count has an unexpected return operation")
			}
			returns++
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("root scope count does not support source operation %q", operation.Kind)
		}
	}
	if filters != 3 || authLets != 1 || returns != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("root scope count requires exactly three canonical filters, one authorization LET, and one RETURN")
	}
	countVariable := renderer.newInternalVariable("scope_count")
	lines = append(lines, "  COLLECT WITH COUNT INTO "+countVariable, "  RETURN {count: "+countVariable+"}")
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, nil
}

// RenderPhysicalPlanWithRelatedExpandPreviewLimit bounds a terminal
// RELATED_EXPAND's per-parent candidate rows while retaining the complete
// final output ordering. The optimization is ignored unless the plan ends at
// a related expansion whose row identity is the final row identity.
func RenderPhysicalPlanWithRelatedExpandPreviewLimit(plan ir.PhysicalPlan, limit int) (RenderedPhysicalPlan, error) {
	if limit < 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related expansion preview limit must be positive")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{relatedExpandPreviewLimit: limit})
}

// RenderPhysicalPlanWithCategoryScanPresenceMarker keeps the selected value
// projection ordinary and carries its property-presence result in a separate
// internal field. Category scans consume this marker after their source plan
// has finished filtering rows.
func RenderPhysicalPlanWithCategoryScanPresenceMarker(plan ir.PhysicalPlan, columnName, markerColumn string) (RenderedPhysicalPlan, error) {
	if strings.TrimSpace(columnName) == "" || strings.TrimSpace(markerColumn) == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("category scan presence requires a selected column and marker")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
		preserveProjectionPresenceNames: map[string]struct{}{columnName: {}},
		projectionPresenceMarkerColumn:  markerColumn,
	})
}

// RenderPhysicalPlanWithTerminalProjection returns one public column from a
// validated terminal construction stage while preserving every intermediate
// stage projection needed to evaluate the plan.
func RenderPhysicalPlanWithTerminalProjection(plan ir.PhysicalPlan, columnName string) (RenderedPhysicalPlan, error) {
	return renderPhysicalPlanWithTerminalProjection(plan, columnName, false)
}

// RenderPhysicalPlanWithUnorderedTerminalProjection returns one public column
// from a validated terminal construction stage without sorting the terminal
// rows by identity. The caller must impose any ordering its result contract
// requires.
func RenderPhysicalPlanWithUnorderedTerminalProjection(plan ir.PhysicalPlan, columnName string) (RenderedPhysicalPlan, error) {
	return renderPhysicalPlanWithTerminalProjection(plan, columnName, true)
}

// RenderPhysicalPlanWithUnorderedTerminalProjectionPresenceMarker renders a
// category scan projection while carrying property presence separately from
// its nullable value through construction stages.
func RenderPhysicalPlanWithUnorderedTerminalProjectionPresenceMarker(plan ir.PhysicalPlan, columnName, markerColumn string) (RenderedPhysicalPlan, error) {
	if strings.TrimSpace(markerColumn) == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("category scan presence marker is required")
	}
	return renderPhysicalPlanWithTerminalProjectionAndMarker(plan, columnName, true, markerColumn)
}

func renderPhysicalPlanWithTerminalProjection(plan ir.PhysicalPlan, columnName string, omitTerminalRowSort bool) (RenderedPhysicalPlan, error) {
	return renderPhysicalPlanWithTerminalProjectionAndMarker(plan, columnName, omitTerminalRowSort, "")
}

func renderPhysicalPlanWithTerminalProjectionAndMarker(plan ir.PhysicalPlan, columnName string, omitTerminalRowSort bool, markerColumn string) (RenderedPhysicalPlan, error) {
	sequence := plan.StageSequence
	if sequence == nil || columnName == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("terminal projection requires a construction stage and column")
	}
	for _, column := range sequence.FinalColumns {
		if column.Name == columnName && !column.Internal {
			var presenceNames map[string]struct{}
			if markerColumn != "" {
				presenceNames = terminalProjectionPresenceNames(sequence, column.ID)
			}
			return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
				terminalProjectionColumn:        columnName,
				omitTerminalRowSort:             omitTerminalRowSort,
				preserveProjectionPresenceNames: presenceNames,
				projectionPresenceMarkerColumn:  markerColumn,
			})
		}
	}
	return RenderedPhysicalPlan{}, fmt.Errorf("terminal projection column %q is not public in the final stage", columnName)
}

func terminalProjectionPresenceNames(sequence *ir.PhysicalStageSequence, columnID string) map[string]struct{} {
	names := make(map[string]struct{})
	for index := len(sequence.Stages) - 1; index >= 0; index-- {
		stage := sequence.Stages[index]
		output, found := physicalStageColumnByID(stage.OutputColumns, columnID)
		if !found {
			break
		}
		names[output.Name] = struct{}{}
		input, found := physicalStageColumnByID(stage.InputColumns, columnID)
		if !found {
			break
		}
		names[input.Name] = struct{}{}
	}
	for _, column := range sequence.SourceColumns {
		if column.ID == columnID {
			names[column.Name] = struct{}{}
			break
		}
	}
	return names
}

func physicalStageColumnByID(columns []ir.PhysicalStageColumn, id string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return ir.PhysicalStageColumn{}, false
}

// RenderPhysicalPlanWithDynamicCategoryPivotPreview renders a terminal
// nonunique Pivot preview with category specifications supplied as one bind
// array. Full execution continues to use the canonical static renderer.
func RenderPhysicalPlanWithDynamicCategoryPivotPreview(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || len(sequence.Stages) != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("dynamic category preview requires a terminal Pivot preview")
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.GroupedPivot.OneInputRowPerGroup || len(stage.GroupedPivot.GroupKeys) == 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("dynamic category preview requires a nonunique terminal Pivot")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{dynamicPivotPreview: true})
}

// RenderPhysicalPlanWithStreamingMissingCategoryPivotPreview renders a
// direct-root terminal Pivot preview with per-category COLLECT aggregations.
// It avoids retaining every projected source row in a group array while
// preserving the full source scan and exact presence-aware category matches.
func RenderPhysicalPlanWithStreamingMissingCategoryPivotPreview(plan ir.PhysicalPlan) (RenderedPhysicalPlan, error) {
	return renderStreamingMissingCategoryPivotPreview(plan, "", false)
}

// RenderPhysicalPlanWithStreamingMissingCategoryPivotPreviewAndRootIndexHint
// applies a non-forcing root index hint and renders the hidden missing
// category marker directly from its stored parent object. Callers must supply
// a compiler-owned stored-values index matching the source projections.
func RenderPhysicalPlanWithStreamingMissingCategoryPivotPreviewAndRootIndexHint(plan ir.PhysicalPlan, indexHint string) (RenderedPhysicalPlan, error) {
	if !rootIndexHintPattern.MatchString(indexHint) {
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint %q is not a safe index name", indexHint)
	}
	return renderStreamingMissingCategoryPivotPreview(plan, indexHint, true)
}

func renderStreamingMissingCategoryPivotPreview(plan ir.PhysicalPlan, indexHint string, storedPresenceObject bool) (RenderedPhysicalPlan, error) {
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil ||
		sequence.PopulationMappingReturn != nil || sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category preview requires one terminal Pivot preview")
	}
	stage := sequence.Stages[0]
	pivot := stage.GroupedPivot
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || pivot == nil || pivot.OneInputRowPerGroup ||
		pivot.CodedCorrelation != nil || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" || len(pivot.Categories) == 0 ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresence != nil || pivot.CategoryPresenceColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("streaming missing-category preview requires a simple presence-aware Pivot")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
		rootIndexHint: indexHint, streamingMissingCategoryPivotPreview: true,
		storedPresenceObject: storedPresenceObject,
	})
}

// RenderPhysicalPlanWithTwoScanPivotPreview renders a terminal Pivot preview
// by selecting complete group tuples before reading their contributing source
// rows. The caller must prove the source projection is a direct, terminal
// Pivot projection before enabling this path.
func RenderPhysicalPlanWithTwoScanPivotPreview(plan ir.PhysicalPlan, indexHint string, groupKeySourcePaths [][]string) (RenderedPhysicalPlan, error) {
	if !rootIndexHintPattern.MatchString(indexHint) {
		return RenderedPhysicalPlan{}, fmt.Errorf("root index hint %q is not a safe index name", indexHint)
	}
	sequence := plan.StageSequence
	if sequence == nil || !sequence.PreviewTerminalPivotWindow || sequence.PreviewLimitBindKey == "" ||
		sequence.PreviewSourceWindowByRootID || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || len(sequence.Stages) != 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview requires a terminal Pivot preview")
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID ||
		stage.Kind != ir.PhysicalStagePivotOp || stage.GroupedPivot == nil ||
		stage.GroupedPivot.OneInputRowPerGroup || len(stage.GroupedPivot.GroupKeys) == 0 ||
		stage.GroupedPivot.CategoryPresence != nil || stage.GroupedPivot.CategoryPresenceColumn != "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview requires a simple nonunique terminal Pivot")
	}
	if len(groupKeySourcePaths) != len(stage.GroupedPivot.GroupKeys) {
		return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview has %d group-key source paths for %d Pivot keys", len(groupKeySourcePaths), len(stage.GroupedPivot.GroupKeys))
	}
	for index, path := range groupKeySourcePaths {
		if len(path) == 0 {
			return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview group-key source path %d is empty", index)
		}
		for _, segment := range path {
			if segment == "" {
				return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview group-key source path %d has an empty segment", index)
			}
		}
	}
	if len(plan.Operations) == 0 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("two-scan preview requires a direct root source scan")
	}
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{
		rootIndexHint:            indexHint,
		twoScanPivotPreview:      true,
		dynamicPivotPreview:      true,
		pivotGroupKeySourcePaths: groupKeySourcePaths,
	})
}

type physicalRenderOptions struct {
	rootIndexHint                        string
	disableRootIndex                     bool
	internalPrefix                       string
	relatedExpandPreviewLimit            int
	terminalProjectionColumn             string
	omitTerminalRowSort                  bool
	omitTerminalReturn                   bool
	terminalReturnVariable               string
	preserveProjectionPresenceNames      map[string]struct{}
	projectionPresenceMarkerColumn       string
	twoScanPivotPreview                  bool
	dynamicPivotPreview                  bool
	streamingMissingCategoryPivotPreview bool
	storedPresenceObject                 bool
	storedPresenceOutputName             string
	storedPresenceParentPath             []string
	pivotGroupKeySourcePaths             [][]string
	pivotGroupTupleFilter                *pivotGroupTupleFilter
	previewRootKeyWindowVariable         string
	previewGroupIdentityFilter           *previewGroupIdentityFilter
}

type pivotGroupTupleFilter struct {
	SelectedGroupKeysVariable string
	SourcePaths               [][]string
}

func (r *physicalPlanRenderer) renderPivotGroupTupleFilter(filter *pivotGroupTupleFilter, rootVariable string) (string, error) {
	if filter == nil || filter.SelectedGroupKeysVariable == "" || rootVariable == "" || len(filter.SourcePaths) == 0 {
		return "", fmt.Errorf("selected Pivot group tuple filter is incomplete")
	}
	keyValues := make([]string, 0, len(filter.SourcePaths))
	for pathIndex, path := range filter.SourcePaths {
		if len(path) == 0 {
			return "", fmt.Errorf("selected Pivot group tuple source path %d is empty", pathIndex)
		}
		segments := make([]string, 0, len(path))
		for _, segment := range path {
			if !previewSourcePathSegmentPattern.MatchString(segment) {
				return "", fmt.Errorf("selected Pivot group tuple source path %d has an unsafe segment %q", pathIndex, segment)
			}
			segments = append(segments, segment)
		}
		keyValues = append(keyValues, rootVariable+"."+strings.Join(segments, "."))
	}
	if len(keyValues) == 1 {
		return "FILTER " + keyValues[0] + " IN " + filter.SelectedGroupKeysVariable, nil
	}
	return "FILTER POSITION(" + filter.SelectedGroupKeysVariable + ", [" + strings.Join(keyValues, ", ") + "])", nil
}

func renderPhysicalPlan(plan ir.PhysicalPlan, rootIndexHint string) (RenderedPhysicalPlan, error) {
	return renderPhysicalPlanWithOptions(plan, physicalRenderOptions{rootIndexHint: rootIndexHint})
}

func renderPhysicalPlanWithOptions(plan ir.PhysicalPlan, options physicalRenderOptions) (RenderedPhysicalPlan, error) {
	if plan.Engine == ir.PhysicalEngineClickHouse {
		return RenderedPhysicalPlan{}, fmt.Errorf("ClickHouse physical plan cannot be rendered as AQL")
	}
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.StageSequence != nil {
		if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("verify construction source scope: %w", err)
		}
		return renderPhysicalStageSequence(plan, options)
	}
	if len(plan.Operations) == 1 && plan.Operations[0].Kind == ir.PhysicalGroupRowsOp {
		return renderPhysicalGroupRows(plan, *plan.Operations[0].GroupRows)
	}

	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := validateRenderablePhysicalPlan(plan, collectionKeys); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGraphReturnOp {
			return renderGraphPhysicalPlan(plan, collectionKeys)
		}
	}
	// This renderer deliberately supports only BuildGenericPhysicalPlan's
	// navigation contract. Validate its required project/auth windows again at
	// the executable boundary so a manually assembled plan cannot render an
	// unscoped resource scan.
	if err := ir.ValidateGenericPhysicalPlanScope(plan); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("verify renderable generic physical plan scope: %w", err)
	}
	layout, err := buildNavigationRenderLayout(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}

	renderer := physicalPlanRenderer{
		bindVars:                        runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys:                  collectionKeys,
		rootIndexHint:                   options.rootIndexHint,
		disableRootIndex:                options.disableRootIndex,
		internalPrefix:                  options.internalPrefix,
		dynamicPivotPreview:             options.dynamicPivotPreview,
		setVariables:                    map[string]string{},
		reservedVars:                    physicalPlanVariableNames(plan),
		rootVariable:                    layout.root.Variable,
		cellTrace:                       layout.traceReturn,
		tableShapeExclusion:             layout.exclusionReturn,
		preserveProjectionPresenceNames: options.preserveProjectionPresenceNames,
		projectionPresenceMarkerColumn:  options.projectionPresenceMarkerColumn,
		storedPresenceOutputName:        options.storedPresenceOutputName,
		storedPresenceParentPath:        append([]string(nil), options.storedPresenceParentPath...),
		previewRootKeyWindowVariable:    options.previewRootKeyWindowVariable,
		previewGroupIdentityFilter:      options.previewGroupIdentityFilter,
	}
	lines, err := renderer.renderRootScan(layout.root)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render root scan: %w", err)
	}
	for index, operation := range layout.rootScope {
		line, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root scope operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	rootSetIndex := 0
	for index, operation := range layout.rootPredicates {
		var line []string
		if operation.Kind == ir.PhysicalSetOp {
			rootSetIndex++
			line, err = renderer.renderSet(*operation.Set, rootSetIndex)
		} else {
			line, err = renderer.renderScopeOperation(operation, "  ")
		}
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root predicate %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	for index, unnest := range layout.unnests {
		line, err := renderer.renderUnnest(unnest, "  ", index, 0)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render unnest %d: %w", index+1, err)
		}
		lines = append(lines, line...)
	}
	for index, operation := range layout.rootWindow {
		line, err := renderer.renderRootWindowOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render root execution window operation %d (%s): %w", index, operation.Kind, err)
		}
		lines = append(lines, line...)
	}
	traversalIndex, setIndex, expressionLetIndex := 0, rootSetIndex, 0
	for _, item := range layout.postWindow {
		var line []string
		switch item.operation.Kind {
		case ir.PhysicalTraversalOp:
			traversalIndex++
			block := physicalNavigationTraversal{traversal: *item.operation.Traversal, scope: item.traversalScope}
			line, err = renderer.renderTraversalSet(block, layout.root.Variable, traversalIndex)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render traversal %d: %w", traversalIndex, err)
			}
		case ir.PhysicalSetOp:
			setIndex++
			line, err = renderer.renderSet(*item.operation.Set, setIndex)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render child set %d: %w", setIndex, err)
			}
		case ir.PhysicalExpressionLetOp:
			line, err = renderer.renderExpressionLet(item.operation, "  ")
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render expression LET %d: %w", expressionLetIndex, err)
			}
			expressionLetIndex++
		case ir.PhysicalGroupedPivotOp, ir.PhysicalUnpivotOp:
			line, err = renderer.renderTableReshape(item.operation)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render table reshape: %w", err)
			}
		case ir.PhysicalSortOp, ir.PhysicalLimitOp:
			line, err = renderer.renderRootWindowOperation(item.operation, "  ")
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render reshaped execution window: %w", err)
			}
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("render post-window operation %q: unsupported operation", item.operation.Kind)
		}
		lines = append(lines, line...)
	}
	if options.omitTerminalReturn && options.terminalReturnVariable != "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("terminal RETURN cannot be omitted and assigned to a variable")
	}
	if (options.omitTerminalReturn || options.terminalReturnVariable != "") &&
		(layout.returnOp == nil || layout.mappingReturn != nil || layout.traceReturn != nil || layout.exclusionReturn != nil) {
		return RenderedPhysicalPlan{}, fmt.Errorf("terminal RETURN handling requires a generic physical RETURN")
	}
	if layout.mappingReturn != nil {
		mappingLines, mappingErr := renderer.renderPopulationMappingReturn(*layout.mappingReturn)
		if mappingErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render population mapping RETURN: %w", mappingErr)
		}
		lines = append(lines, mappingLines...)
	} else if layout.traceReturn != nil {
		traceLines, traceErr := renderer.renderCellTraceReturn(*layout.traceReturn)
		if traceErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render cell trace RETURN: %w", traceErr)
		}
		lines = append(lines, traceLines...)
	} else if layout.exclusionReturn != nil {
		exclusionLines, exclusionErr := renderer.renderTableShapeExclusionReturn(*layout.exclusionReturn)
		if exclusionErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render table-shape exclusion RETURN: %w", exclusionErr)
		}
		lines = append(lines, exclusionLines...)
	} else if options.omitTerminalReturn {
	} else {
		returnExpression, returnErr := renderer.renderReturn(*layout.returnOp)
		if returnErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render RETURN: %w", returnErr)
		}
		if options.terminalReturnVariable != "" {
			lines = append(lines, fmt.Sprintf("LET %s = %s", options.terminalReturnVariable, returnExpression))
		} else if options.pivotGroupTupleFilter != nil {
			filter, filterErr := renderer.renderPivotGroupTupleFilter(options.pivotGroupTupleFilter, layout.root.Variable)
			if filterErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render selected Pivot group tuple filter: %w", filterErr)
			}
			lines = append(lines, filter)
		}
		if options.terminalReturnVariable == "" {
			lines = append(lines, "RETURN "+returnExpression)
		}
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{
		Query:    query,
		BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query),
	}, nil
}

func (r *physicalPlanRenderer) renderRootScan(root ir.PhysicalRootScan) ([]string, error) {
	if root.PageCandidateSeed != nil && (root.Population != nil || root.CohortSource != nil) {
		return nil, fmt.Errorf("root page candidate seed requires a direct root scan")
	}
	if r.previewRootKeyWindowVariable != "" {
		if root.Population != nil || root.CohortSource != nil || root.PageCandidateSeed != nil {
			return nil, fmt.Errorf("preview root-key window requires a direct root scan")
		}
		rootKeyVariable := r.newInternalVariable("preview_root_key")
		return []string{
			fmt.Sprintf("FOR %s IN %s", rootKeyVariable, r.previewRootKeyWindowVariable),
			fmt.Sprintf("LET %s = DOCUMENT(@@%s, %s)", root.Variable, root.CollectionBindKey, rootKeyVariable),
			fmt.Sprintf("FILTER %s != null", root.Variable),
		}, nil
	}
	if seed := root.PageCandidateSeed; seed != nil {
		candidateKey := r.newInternalVariable("root_page_candidate_key")
		candidateKeys, err := r.renderSubplan(seed.Subplan, "  ", false)
		if err != nil {
			return nil, fmt.Errorf("render root page candidate seed: %w", err)
		}
		return []string{
			fmt.Sprintf("FOR %s IN %s", candidateKey, candidateKeys),
			fmt.Sprintf("LET %s = DOCUMENT(@@%s, %s)", root.Variable, root.CollectionBindKey, candidateKey),
			fmt.Sprintf("FILTER %s != null", root.Variable),
		}, nil
	}
	if root.CohortSource != nil {
		if root.Population != nil {
			return nil, fmt.Errorf("root scan cannot combine cohort and population sources")
		}
		return r.renderCohortRootScan(root)
	}
	if root.Population == nil {
		line := fmt.Sprintf("FOR %s IN @@%s", root.Variable, root.CollectionBindKey)
		if r.disableRootIndex {
			line += " OPTIONS { disableIndex: true }"
		} else if r.rootIndexHint != "" {
			line += fmt.Sprintf(" OPTIONS { indexHint: %q, forceIndexHint: false }", r.rootIndexHint)
		}
		return []string{line}, nil
	}
	population := root.Population
	lines := []string{fmt.Sprintf("FOR %s IN @@%s", population.MemberScan.Variable, population.MemberScan.CollectionBindKey)}
	memberID := ""
	if population.CollectMembersVariable != "" {
		var err error
		memberID, err = r.renderValue(population.MemberID)
		if err != nil {
			return nil, fmt.Errorf("render population member id: %w", err)
		}
	}
	frontierTarget := ""
	frontierPending := false
	collectFrontier := func() {
		frontierKey := r.newInternalVariable("population_frontier_key")
		frontierRows := r.newInternalVariable("population_frontier_rows")
		collect := fmt.Sprintf("  COLLECT %s = %s._key", frontierKey, frontierTarget)
		if memberID != "" {
			frontierMember := r.newInternalVariable("population_frontier_member")
			collect = fmt.Sprintf("  COLLECT %s = %s, %s = %s._key", frontierMember, memberID, frontierKey, frontierTarget)
			memberID = frontierMember
		}
		lines = append(lines,
			fmt.Sprintf("%s INTO %s = %s OPTIONS { method: \"sorted\" }", collect, frontierRows, frontierTarget),
			fmt.Sprintf("  LET %s = FIRST(%s)", frontierTarget, frontierRows),
		)
		frontierPending = false
	}
	for index, filter := range population.MemberFilters {
		rendered, err := r.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &filter}, "  ")
		if err != nil {
			return nil, fmt.Errorf("render member filter %d: %w", index, err)
		}
		lines = append(lines, rendered...)
	}
	for index, operation := range population.ResourceOperations {
		if operation.Kind == ir.PhysicalTraversalOp && frontierPending {
			collectFrontier()
		}
		switch operation.Kind {
		case ir.PhysicalCollectionScanOp:
			lines = append(lines, fmt.Sprintf("  FOR %s IN @@%s", operation.CollectionScan.Variable, operation.CollectionScan.CollectionBindKey))
		case ir.PhysicalTraversalOp:
			lines = append(lines, r.renderTraversalScan(*operation.Traversal, operation.Traversal.SourceVariable, "  ")...)
			frontierTarget = operation.Traversal.TargetVariable
			frontierPending = true
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			rendered, err := r.renderScopeOperation(operation, "    ")
			if err != nil {
				return nil, fmt.Errorf("render resource operation %d (%s): %w", index, operation.Kind, err)
			}
			lines = append(lines, rendered...)
		default:
			return nil, fmt.Errorf("resource operation %d has unsupported kind %q", index, operation.Kind)
		}
	}
	if frontierPending {
		collectFrontier()
	}
	rootKey, err := r.renderValue(population.RootKey)
	if err != nil {
		return nil, fmt.Errorf("render population root key: %w", err)
	}
	rootKeyVariable := r.newInternalVariable("population_root_key")
	if population.CollectMembersVariable == "" {
		lines = append(lines, fmt.Sprintf("  COLLECT %s = %s", rootKeyVariable, rootKey))
	} else {
		memberIDsVariable := r.newInternalVariable("population_member_ids")
		lines = append(lines,
			fmt.Sprintf("  COLLECT %s = %s INTO %s = %s", rootKeyVariable, rootKey, memberIDsVariable, memberID),
			fmt.Sprintf("  LET %s = SORTED_UNIQUE(%s)", population.CollectMembersVariable, memberIDsVariable),
		)
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = DOCUMENT(@@%s, %s)", root.Variable, root.CollectionBindKey, rootKeyVariable),
		fmt.Sprintf("  FILTER %s != null", root.Variable),
	)
	return lines, nil
}

// pruneUnusedRuntimeBindVars is required after physical rewrites. Traversal
// sharing can remove a typed edge predicate while retaining its original
// logical bind in the cloned plan. Arango rejects undeclared bind variables,
// so only values referenced by the final rendered AQL may cross the execution
// boundary.
func pruneUnusedRuntimeBindVars(bindVars map[string]any, query string) map[string]any {
	pruned := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		if strings.Contains(query, "@"+key) {
			pruned[key] = value
		}
	}
	return pruned
}

func (r *physicalPlanRenderer) renderRootWindowOperation(operation ir.PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case ir.PhysicalSortOp:
		keys := make([]string, 0, len(operation.Sort.Keys))
		for _, key := range operation.Sort.Keys {
			value, err := r.renderValue(key)
			if err != nil {
				return nil, err
			}
			keys = append(keys, value+" ASC")
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("SORT requires at least one key")
		}
		return []string{indent + "SORT " + strings.Join(keys, ", ")}, nil
	case ir.PhysicalLimitOp:
		if _, collectionBinding := r.collectionKeys[operation.Limit.BindKey]; collectionBinding {
			return nil, fmt.Errorf("limit bind key %q cannot be a collection bind", operation.Limit.BindKey)
		}
		return []string{indent + "LIMIT @" + operation.Limit.BindKey}, nil
	default:
		return nil, fmt.Errorf("root execution window cannot contain physical operation %q", operation.Kind)
	}
}

type physicalPlanRenderer struct {
	bindVars                             map[string]any
	collectionKeys                       map[string]struct{}
	rootIndexHint                        string
	disableRootIndex                     bool
	setVariables                         map[string]string
	reservedVars                         map[string]struct{}
	internalPrefix                       string
	preparedItem                         string
	rootVariable                         string
	cellTrace                            *ir.PhysicalCellTraceReturn
	tableShapeExclusion                  *ir.PhysicalTableShapeExclusionReturn
	dynamicPivotPreview                  bool
	streamingMissingCategoryPivotPreview bool
	storedPresenceOutputName             string
	storedPresenceParentPath             []string
	preserveProjectionPresenceNames      map[string]struct{}
	projectionPresenceMarkerColumn       string
	projectionPresenceMarkerRows         map[string]struct{}
	previewRootKeyWindowVariable         string
	previewGroupIdentityFilter           *previewGroupIdentityFilter
}

func (r *physicalPlanRenderer) renderExpressionLet(operation ir.PhysicalOperation, indent string) ([]string, error) {
	if operation.ExpressionLet == nil {
		return nil, fmt.Errorf("expression LET is missing payload")
	}
	expression, err := r.renderExpression(operation.ExpressionLet.Expression)
	if err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("%sLET %s = %s", indent, operation.ExpressionLet.Variable, expression)}, nil
}

func (r *physicalPlanRenderer) renderScopeOperation(operation ir.PhysicalOperation, indent string) ([]string, error) {
	switch operation.Kind {
	case ir.PhysicalFilterOp:
		var expression string
		var err error
		if operation.Filter.Expression != nil {
			expression, err = r.renderPredicateExpression(*operation.Filter.Expression, indent)
		} else {
			expression, err = r.renderPredicate(operation.Filter.Predicate)
		}
		if err != nil {
			return nil, err
		}
		return []string{indent + "FILTER " + expression}, nil
	case ir.PhysicalDerivedLetOp:
		expression, err := r.renderDerivedLet(*operation.DerivedLet)
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("%sLET %s = %s", indent, operation.DerivedLet.Variable, expression)}, nil
	case ir.PhysicalExpressionLetOp:
		return r.renderExpressionLet(operation, indent)
	default:
		return nil, fmt.Errorf("navigation scope cannot contain physical operation %q", operation.Kind)
	}
}

// physicalNavigationRenderLayout is the intentionally narrow executable shape
// produced by BuildGenericPhysicalPlan. Post-window operations retain physical
// plan order because sets, traversals, and expression LETs may depend on values
// introduced by any preceding operation.
type physicalNavigationRenderLayout struct {
	root            ir.PhysicalRootScan
	rootScope       []ir.PhysicalOperation
	rootPredicates  []ir.PhysicalOperation
	rootWindow      []ir.PhysicalOperation
	unnests         []ir.PhysicalUnnest
	postWindow      []physicalNavigationRenderItem
	returnOp        *ir.PhysicalReturn
	mappingReturn   *ir.PhysicalPopulationMappingReturn
	traceReturn     *ir.PhysicalCellTraceReturn
	exclusionReturn *ir.PhysicalTableShapeExclusionReturn
}

type physicalNavigationRenderItem struct {
	operation      ir.PhysicalOperation
	traversalScope []ir.PhysicalOperation
}

type physicalNavigationTraversal struct {
	traversal ir.PhysicalTraversal
	scope     []ir.PhysicalOperation
}
