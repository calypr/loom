package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderRelatedEligibilityCountRows reverses one exact EXISTS route for
// terminal count and category scans. Every scope predicate is matched against
// the canonical typed plan before this renderer relocates it.
func renderRelatedEligibilityCountRows(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) (RenderedPhysicalPlan, bool, error) {
	if stage, sourceProjection, rootVariable, ok := terminalRelatedEligibilityCategoryScanOnly(plan, sequence, options); ok {
		return renderRelatedEligibilityCategoryScan(plan, sequence, options, stage, sourceProjection, rootVariable)
	}
	if !terminalRelatedEligibilityCountRowsOnly(plan, sequence, options) {
		return RenderedPhysicalPlan{}, false, nil
	}

	stage := sequence.Stages[0]
	reverse, ok, err := buildRelatedEligibilityReverseFrontier(plan, stage, "related_count", "__loom_related_count_terminal_collection")
	if err != nil || !ok {
		return RenderedPhysicalPlan{}, false, err
	}
	renderer, lines, frontier := reverse.renderer, reverse.lines, reverse.frontier

	grouped := renderer.newInternalVariable("grouped_rows")
	lines = append(lines, fmt.Sprintf("LET %s = (", grouped))
	groupLines, err := renderer.renderConstructionGroupStage(sequence.Stages[1], constructionGroupInput{RowsVariable: frontier})
	if err != nil {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("render reverse related COUNT_ROWS group: %w", err)
	}
	lines = append(lines, groupLines...)
	lines = append(lines, ")")
	finalRow := "__loom_construction_final_row"
	lines = append(lines, fmt.Sprintf("FOR %s IN %s", finalRow, grouped))
	if sequence.PreviewLimitBindKey != "" {
		lines = append(lines, "LIMIT @"+sequence.PreviewLimitBindKey)
	}
	projections := make([]ir.PhysicalProjection, 0, len(sequence.FinalColumns))
	for _, column := range sequence.FinalColumns {
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Hidden: column.Internal,
			Value: ir.PhysicalValue{Variable: finalRow, Path: []string{column.Name}},
		})
	}
	returned, err := renderer.renderReturn(ir.PhysicalReturn{Projections: projections})
	if err != nil {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("render reverse related final projection: %w", err)
	}
	lines = append(lines, "RETURN "+returned)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, true, nil
}

// relatedEligibilityCategoryScanOverflowSentinel is one above the compiler's
// maximum category scan size (currently 256). Reaching it proves overflow;
// returning fewer rows is complete, and returning this many cannot be
// mistaken for a complete category set.
const relatedEligibilityCategoryScanOverflowSentinel = 257

type relatedEligibilityReverseFrontier struct {
	renderer   physicalPlanRenderer
	lines      []string
	frontier   string
	traversals []ir.PhysicalTraversal
}

// buildRelatedEligibilityReverseFrontier is shared by COUNT_ROWS and the
// category terminal. It only moves an EXISTS filter whose correlated subplan
// passes the same exact endpoint-route and scope proof used by Count.
func buildRelatedEligibilityReverseFrontier(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, internalPrefix, terminalCollectionPrefix string) (relatedEligibilityReverseFrontier, bool, error) {
	if stage.Filter == nil || stage.Filter.Expression == nil || stage.Filter.Expression.Kind != ir.PhysicalExistsPredicate || stage.Filter.Expression.Exists == nil {
		return relatedEligibilityReverseFrontier{}, false, nil
	}
	traversals, ok := relatedCountRoute(plan, stage, stage.Filter.Expression.Exists)
	if !ok || len(traversals) == 0 {
		return relatedEligibilityReverseFrontier{}, false, nil
	}
	terminal := traversals[len(traversals)-1]
	terminalType, ok := plan.BindVars[terminal.TargetTypeBindKey].(string)
	if !ok || terminalType == "" {
		return relatedEligibilityReverseFrontier{}, false, nil
	}

	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return relatedEligibilityReverseFrontier{}, false, err
	}
	bindValues := make(map[string]any, len(plan.BindVars)+1)
	for key, value := range plan.BindVars {
		bindValues[key] = value
	}
	terminalCollectionBind := terminalCollectionPrefix
	for suffix := 2; ; suffix++ {
		if _, exists := bindValues[terminalCollectionBind]; !exists {
			break
		}
		terminalCollectionBind = fmt.Sprintf("%s_%d", terminalCollectionPrefix, suffix)
	}
	bindValues[terminalCollectionBind] = terminalType
	collectionKeys[terminalCollectionBind] = struct{}{}
	renderer := physicalPlanRenderer{
		bindVars:       runtimePhysicalBindVars(bindValues, collectionKeys),
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   stageSequenceVariableNames(plan),
		internalPrefix: internalPrefix,
	}

	lines := make([]string, 0, 16+len(traversals)*14)
	frontier := renderer.newInternalVariable("terminal_frontier")
	terminalDoc := renderer.newInternalVariable("terminal")
	terminalAuth := renderer.newInternalVariable("terminal_scope_allowed")
	lines = append(lines,
		fmt.Sprintf("LET %s = (", frontier),
		fmt.Sprintf("  FOR %s IN @@%s", terminalDoc, terminalCollectionBind),
		fmt.Sprintf("  FILTER %s.resourceType == @%s", terminalDoc, terminal.TargetTypeBindKey),
		fmt.Sprintf("  FILTER %s.project == @project", terminalDoc),
		fmt.Sprintf("  FILTER %s.dataset_generation == @dataset_generation", terminalDoc),
		fmt.Sprintf("  LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", terminalAuth, terminalDoc),
		fmt.Sprintf("  FILTER %s == @scope_allowed", terminalAuth),
		fmt.Sprintf("  RETURN { _id: %s._id, auth_resource_path: %s.auth_resource_path }", terminalDoc, terminalDoc),
		")",
	)

	for index := len(traversals) - 1; index >= 0; index-- {
		traversal := traversals[index]
		targetTypeField := traversal.EdgeTargetTypeField
		sourceTypeField := "from_type"
		if targetTypeField == "from_type" {
			sourceTypeField = "to_type"
		}
		inverseEndpoint := traversal.EndpointJoinField
		previousEndpoint := traversal.EndpointField
		if inverseEndpoint == "" || previousEndpoint == "" {
			return relatedEligibilityReverseFrontier{}, false, nil
		}
		nextFrontier := renderer.newInternalVariable(fmt.Sprintf("route_frontier_%d", index+1))
		current := renderer.newInternalVariable(fmt.Sprintf("route_target_%d", index+1))
		edgeAuth := renderer.newInternalVariable(fmt.Sprintf("route_edge_scope_allowed_%d", index+1))
		previous := traversal.SourceVariable
		lines = append(lines,
			fmt.Sprintf("LET %s = (", nextFrontier),
			fmt.Sprintf("  FOR %s IN %s", current, frontier),
			fmt.Sprintf("  FOR %s IN @@%s", traversal.EdgeVariable, traversal.EdgeCollectionBindKey),
			fmt.Sprintf("    FILTER %s.%s == %s._id", traversal.EdgeVariable, inverseEndpoint, current),
			fmt.Sprintf("    FILTER %s.label == @%s", traversal.EdgeVariable, traversal.EdgeLabelBindKey),
			fmt.Sprintf("    FILTER %s.%s == @%s", traversal.EdgeVariable, targetTypeField, traversal.TargetTypeBindKey),
			fmt.Sprintf("    FILTER %s.%s == @%s", traversal.EdgeVariable, sourceTypeField, traversal.SourceTypeBindKey),
			fmt.Sprintf("    FILTER %s.project == @project", traversal.EdgeVariable),
			fmt.Sprintf("    FILTER %s.dataset_generation == @dataset_generation", traversal.EdgeVariable),
			fmt.Sprintf("    LET %s = @auth_resource_paths_unrestricted == true OR (%s.auth_resource_path IN @auth_resource_paths AND %s.auth_resource_path IN @auth_resource_paths)", edgeAuth, traversal.EdgeVariable, current),
			fmt.Sprintf("    FILTER %s == @scope_allowed", edgeAuth),
			fmt.Sprintf("    LET %s = DOCUMENT(%s.%s)", previous, traversal.EdgeVariable, previousEndpoint),
			fmt.Sprintf("    FILTER %s != null", previous),
			fmt.Sprintf("    FILTER %s.resourceType == @%s", previous, traversal.SourceTypeBindKey),
			fmt.Sprintf("    FILTER %s.project == @project", previous),
			fmt.Sprintf("    FILTER %s.dataset_generation == @dataset_generation", previous),
		)
		previousAuth := renderer.newInternalVariable(fmt.Sprintf("route_source_scope_allowed_%d", index+1))
		lines = append(lines,
			fmt.Sprintf("    LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", previousAuth, previous),
			fmt.Sprintf("    FILTER %s == @scope_allowed", previousAuth),
			fmt.Sprintf("  COLLECT _id = %s._id, auth_resource_path = %s.auth_resource_path", previous, previous),
			"  RETURN { _id, auth_resource_path }",
			")",
		)
		frontier = nextFrontier
	}
	return relatedEligibilityReverseFrontier{renderer: renderer, lines: lines, frontier: frontier, traversals: traversals}, true, nil
}

func terminalRelatedEligibilityCategoryScanOnly(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) (ir.PhysicalConstructionStage, ir.PhysicalProjection, string, bool) {
	if sequence == nil || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) != 1 || options.terminalProjectionColumn == "" ||
		options.projectionPresenceMarkerColumn == "" || len(options.preserveProjectionPresenceNames) != 1 || options.twoScanPivotPreview ||
		options.dynamicPivotPreview || options.pivotGroupTupleFilter != nil || sequence.SourceRowIdentity != "_key" {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	if _, preserved := options.preserveProjectionPresenceNames[options.terminalProjectionColumn]; !preserved {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	stage := sequence.Stages[0]
	if stage.ID != sequence.FinalStageID || stage.InputStageID != sequence.SourceStageID || stage.Kind != ir.PhysicalStageRelatedEligibilityOp ||
		stage.Filter == nil || stage.Filter.Expression == nil || stage.Filter.Expression.Kind != ir.PhysicalExistsPredicate ||
		stage.Filter.Expression.Exists == nil || len(stage.DerivedLets) != 0 || stage.RowIdentityColumn != "_key" {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	if !hasStageIdentity(sequence.SourceColumns, "_key") || !hasStageIdentity(stage.InputColumns, "_key") || !hasStageIdentity(stage.OutputColumns, "_key") {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	selectedColumn, selectedColumnOK := publicScalarCategoryStageColumn(sequence.FinalColumns, options.terminalProjectionColumn)
	if !selectedColumnOK {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	stageProjection, found := stageOutputProjection(stage.OutputProjections, selectedColumn.Name)
	if !found || stageProjection.Hidden || stageProjection.Expression != nil || stageProjection.Value.Variable != stage.InputRowVariable ||
		stageProjection.Value.BindKey != "" || len(stageProjection.Value.Path) != 1 {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	rootColumn := stageProjection.Value.Path[0]

	scopeAllowed, scopeAllowedOK := plan.BindVars["scope_allowed"].(bool)
	_, unrestrictedOK := plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	_, pathsOK := plan.BindVars["auth_resource_paths"].([]string)
	if !scopeAllowedOK || !scopeAllowed || !unrestrictedOK || !pathsOK || len(plan.Operations) < 6 ||
		plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population != nil ||
		plan.Operations[0].RootScan.CollectionBindKey != "root_collection" || plan.Source.ResourceType == "" ||
		plan.Operations[0].Source.ResourceType != plan.Source.ResourceType || plan.BindVars["root_collection"] != plan.Source.ResourceType ||
		!matchesCategoryScanSourceOperations(plan.Operations, plan.Operations[0].RootScan.Variable) {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	rootVariable := plan.Operations[0].RootScan.Variable
	if rootVariable == "" {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	var sourceProjection ir.PhysicalProjection
	selectedFound, identityFound := false, false
	returnOperation := plan.Operations[len(plan.Operations)-1]
	for _, projection := range returnOperation.Return.Projections {
		switch projection.Name {
		case rootColumn:
			if selectedFound || projection.Hidden || !directRootCategoryProjection(projection, rootVariable) {
				return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
			}
			if projection.Expression == nil && (projection.Value.Variable != rootVariable || projection.Value.BindKey != "" || len(projection.Value.Path) == 0) {
				return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
			}
			sourceProjection = projection
			selectedFound = true
		case "_key":
			if identityFound || projection.Expression != nil || projection.Value.Variable != rootVariable ||
				projection.Value.BindKey != "" || !reflect.DeepEqual(projection.Value.Path, []string{"_key"}) {
				return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
			}
			identityFound = true
		}
	}
	if !selectedFound || !identityFound {
		return ir.PhysicalConstructionStage{}, ir.PhysicalProjection{}, "", false
	}
	return stage, sourceProjection, rootVariable, true
}

func matchesCategoryScanSourceOperations(operations []ir.PhysicalOperation, rootVariable string) bool {
	if len(operations) < 6 || rootVariable == "" || operations[len(operations)-1].Kind != ir.PhysicalReturnOp ||
		operations[len(operations)-1].Return == nil || !matchesSingleDocumentScope(operations[1:5], rootVariable) {
		return false
	}
	for _, operation := range operations[5 : len(operations)-1] {
		switch operation.Kind {
		case ir.PhysicalSetOp:
			if operation.Set == nil {
				return false
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return false
			}
		case ir.PhysicalExpressionLetOp:
			if operation.ExpressionLet == nil {
				return false
			}
		default:
			// Any later filter, sort, limit, or row-changing operation could
			// affect category membership or its completeness.
			return false
		}
	}
	return true
}

func publicScalarCategoryStageColumn(columns []ir.PhysicalStageColumn, name string) (ir.PhysicalStageColumn, bool) {
	for _, column := range columns {
		if column.Name != name {
			continue
		}
		if column.Internal || column.Identity || (column.Cardinality != "required_one" && column.Cardinality != "optional_one") {
			return ir.PhysicalStageColumn{}, false
		}
		switch column.Kind {
		case "boolean", "integer", "decimal", "string", "date", "date_time", "code", "uuid":
			return column, true
		default:
			return ir.PhysicalStageColumn{}, false
		}
	}
	return ir.PhysicalStageColumn{}, false
}

func stageOutputProjection(projections []ir.PhysicalProjection, name string) (ir.PhysicalProjection, bool) {
	for _, projection := range projections {
		if projection.Name == name {
			return projection, true
		}
	}
	return ir.PhysicalProjection{}, false
}

func renderRelatedEligibilityCategoryScan(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions, stage ir.PhysicalConstructionStage, sourceProjection ir.PhysicalProjection, rootVariable string) (RenderedPhysicalPlan, bool, error) {
	reverse, ok, err := buildRelatedEligibilityReverseFrontier(plan, stage, "related_category_", "__loom_related_category_terminal_collection")
	if err != nil || !ok {
		return RenderedPhysicalPlan{}, false, err
	}
	renderer, lines, frontier := reverse.renderer, reverse.lines, reverse.frontier
	rootFrontier := renderer.newInternalVariable("category_root_frontier")
	rootDocument := renderer.newInternalVariable("category_root")
	rootAuth := renderer.newInternalVariable("category_root_scope_allowed")
	categoryPresent := renderer.newInternalVariable("category_present")
	categoryValue := renderer.newInternalVariable("category_value")
	groupPresent := renderer.newInternalVariable("group_present")
	groupValue := renderer.newInternalVariable("group_value")
	categoryRows := renderer.newInternalVariable("category_rows")
	categoryResult := renderer.newInternalVariable("category_result")
	categoryNameBind := renderer.newInternalBindKey("category_column_name")
	renderer.bindVars[categoryNameBind] = options.terminalProjectionColumn
	categoryName := fmt.Sprintf("[@%s]", categoryNameBind)
	markerName := fmt.Sprintf("%q", options.projectionPresenceMarkerColumn)
	var valueExpression string
	if sourceProjection.Expression != nil {
		expression := ir.ClonePhysicalExpression(*sourceProjection.Expression)
		if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
			return RenderedPhysicalPlan{}, false, nil
		}
		source := expression.Extract.Source
		source.Variable = rootDocument
		rebindPhysicalExtractSource(expression.Extract, source)
		valueExpression, err = renderer.renderExpression(expression)
	} else {
		rootValue := sourceProjection.Value
		rootValue.Variable = rootDocument
		valueExpression, err = renderer.renderValue(rootValue)
	}
	if err != nil {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("render direct root category value: %w", err)
	}
	presentExpression := "true"
	if sourceProjection.Presence != nil {
		presence := *sourceProjection.Presence
		presence.Source.Variable = rootDocument
		presentExpression, err = renderer.renderProjectionPresence(presence)
		if err != nil {
			return RenderedPhysicalPlan{}, false, fmt.Errorf("render direct root category presence: %w", err)
		}
	}
	rootTypeBindKey := reverse.traversals[0].SourceTypeBindKey
	lines = append(lines,
		fmt.Sprintf("LET %s = (", categoryRows),
		fmt.Sprintf("  FOR %s IN %s", rootFrontier, frontier),
		fmt.Sprintf("    LET %s = DOCUMENT(%s._id)", rootDocument, rootFrontier),
		fmt.Sprintf("    FILTER %s != null", rootDocument),
		fmt.Sprintf("    FILTER %s.resourceType == @%s", rootDocument, rootTypeBindKey),
		fmt.Sprintf("    FILTER %s.project == @project", rootDocument),
		fmt.Sprintf("    FILTER %s.dataset_generation == @dataset_generation", rootDocument),
		fmt.Sprintf("    FILTER %s.auth_resource_path == %s.auth_resource_path", rootDocument, rootFrontier),
		fmt.Sprintf("    LET %s = @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths", rootAuth, rootDocument),
		fmt.Sprintf("    FILTER %s == @scope_allowed", rootAuth),
		fmt.Sprintf("    LET %s = %s", categoryPresent, presentExpression),
		fmt.Sprintf("    LET %s = %s ? (%s) : null", categoryValue, categoryPresent, valueExpression),
		fmt.Sprintf("    COLLECT %s = %s, %s = %s", groupPresent, categoryPresent, groupValue, categoryValue),
		fmt.Sprintf("    LIMIT %d", relatedEligibilityCategoryScanOverflowSentinel),
		fmt.Sprintf("    RETURN { %s: %s, %s: %s }", categoryName, groupValue, markerName, groupPresent),
		")",
		fmt.Sprintf("FOR %s IN %s", categoryResult, categoryRows),
		fmt.Sprintf("RETURN %s", categoryResult),
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, true, nil
}

// RenderRelatedEligibilityCategoryOverflowWitness samples qualifying source
// rows from the same exact route used by the full category scan. The caller
// may conclude overflow when the sample contains more distinct categories
// than the supported limit.
func RenderRelatedEligibilityCategoryOverflowWitness(plan ir.PhysicalPlan, columnName, markerColumn string, sampleSize int) (RenderedPhysicalPlan, bool, error) {
	if sampleSize < 1 {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("category overflow sample size must be positive")
	}
	options := physicalRenderOptions{
		terminalProjectionColumn:        columnName,
		projectionPresenceMarkerColumn:  markerColumn,
		preserveProjectionPresenceNames: map[string]struct{}{columnName: {}},
	}
	stage, projection, root, ok := terminalRelatedEligibilityCategoryScanOnly(plan, plan.StageSequence, options)
	if !ok {
		return RenderedPhysicalPlan{}, false, nil
	}
	if _, ok := relatedCountRoute(plan, stage, stage.Filter.Expression.Exists); !ok {
		return RenderedPhysicalPlan{}, false, nil
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, false, err
	}
	renderer := physicalPlanRenderer{
		bindVars: runtimePhysicalBindVars(plan.BindVars, collectionKeys), collectionKeys: collectionKeys,
		setVariables: map[string]string{}, reservedVars: stageSequenceVariableNames(plan), internalPrefix: "category_witness_",
	}
	lines := []string{fmt.Sprintf("FOR %s IN @@root_collection", root)}
	for _, operation := range plan.Operations[1:5] {
		scope, err := renderer.renderScopeOperation(operation, "  ")
		if err != nil {
			return RenderedPhysicalPlan{}, false, fmt.Errorf("render category witness source scope: %w", err)
		}
		lines = append(lines, scope...)
	}
	lines = append(lines, fmt.Sprintf("LET %s = { _key: %s._key }", stage.InputRowVariable, root))
	predicate, err := renderer.renderPredicateExpression(*stage.Filter.Expression, "")
	if err != nil {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("render category witness eligibility: %w", err)
	}
	lines = append(lines, "FILTER "+predicate, fmt.Sprintf("LIMIT %d", sampleSize))
	var valueExpression string
	if projection.Expression != nil {
		expression := ir.ClonePhysicalExpression(*projection.Expression)
		source := expression.Extract.Source
		source.Variable = root
		rebindPhysicalExtractSource(expression.Extract, source)
		valueExpression, err = renderer.renderExpression(expression)
	} else {
		valueExpression, err = renderer.renderValue(projection.Value)
	}
	if err != nil {
		return RenderedPhysicalPlan{}, false, fmt.Errorf("render category witness value: %w", err)
	}
	presenceExpression := "true"
	if projection.Presence != nil {
		presenceExpression, err = renderer.renderProjectionPresence(*projection.Presence)
		if err != nil {
			return RenderedPhysicalPlan{}, false, fmt.Errorf("render category witness presence: %w", err)
		}
	}
	present := renderer.newInternalVariable("present")
	value := renderer.newInternalVariable("value")
	lines = append(lines,
		fmt.Sprintf("LET %s = %s", present, presenceExpression),
		fmt.Sprintf("LET %s = %s ? (%s) : null", value, present, valueExpression),
		fmt.Sprintf("RETURN { %q: %s, %q: %s }", markerColumn, present, columnName, value),
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}, true, nil
}

func terminalRelatedEligibilityCountRowsOnly(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) bool {
	if sequence == nil || sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
		sequence.OutputAuthResourcePathBindKey != "" || len(sequence.Stages) != 2 ||
		options.terminalProjectionColumn != "" || options.projectionPresenceMarkerColumn != "" ||
		len(options.preserveProjectionPresenceNames) != 0 || options.twoScanPivotPreview || options.dynamicPivotPreview || options.pivotGroupTupleFilter != nil {
		return false
	}
	eligibility, group := sequence.Stages[0], sequence.Stages[1]
	if eligibility.ID == "" || eligibility.InputStageID != sequence.SourceStageID || eligibility.Kind != ir.PhysicalStageRelatedEligibilityOp ||
		eligibility.Filter == nil || eligibility.Filter.Expression == nil || eligibility.Filter.Expression.Kind != ir.PhysicalExistsPredicate ||
		eligibility.Filter.Expression.Exists == nil || len(eligibility.DerivedLets) != 0 || eligibility.RowIdentityColumn != "_key" ||
		group.ID != sequence.FinalStageID || group.InputStageID != eligibility.ID || group.Kind != ir.PhysicalStageGroupOp ||
		group.Group == nil || len(group.Group.Keys) != 0 || !constructionGroupCountsOnlyRows(group.Group) ||
		sequence.SourceRowIdentity != "_key" {
		return false
	}
	if !hasStageIdentity(sequence.SourceColumns, "_key") || !hasStageIdentity(eligibility.InputColumns, "_key") || !hasStageIdentity(eligibility.OutputColumns, "_key") {
		return false
	}
	scopeAllowed, scopeAllowedOK := plan.BindVars["scope_allowed"].(bool)
	_, unrestrictedOK := plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	_, pathsOK := plan.BindVars["auth_resource_paths"].([]string)
	if !scopeAllowedOK || !scopeAllowed || !unrestrictedOK || !pathsOK {
		return false
	}
	if len(plan.Operations) != 6 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil || plan.Operations[0].RootScan.CollectionBindKey != "root_collection" ||
		plan.Source.ResourceType == "" || plan.Operations[0].Source.ResourceType != plan.Source.ResourceType ||
		plan.BindVars["root_collection"] != plan.Source.ResourceType ||
		!matchesSingleDocumentScope(plan.Operations[1:5], plan.Operations[0].RootScan.Variable) ||
		plan.Operations[5].Kind != ir.PhysicalReturnOp || plan.Operations[5].Return == nil {
		return false
	}
	return true
}

func hasStageIdentity(columns []ir.PhysicalStageColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return column.Internal && column.Identity && column.Kind == "string" && column.Cardinality == "required_one"
		}
	}
	return false
}

func relatedCountRoute(plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage, subplan *ir.PhysicalSubplan) ([]ir.PhysicalTraversal, bool) {
	if subplan == nil || subplan.Unique || subplan.Sort != nil || len(subplan.Captures) != 1 || subplan.Captures[0] != stage.InputRowVariable || len(subplan.Operations) < 12 {
		return nil, false
	}
	anchor := subplan.Operations[0]
	if anchor.Kind != ir.PhysicalCollectionScanOp || anchor.CollectionScan == nil || anchor.CollectionScan.CollectionBindKey != "root_collection" ||
		anchor.CollectionScan.Variable == "" || anchor.Source.ResourceType != plan.Source.ResourceType {
		return nil, false
	}
	if !physicalFilterEquals(subplan.Operations[1], ir.PhysicalValue{Variable: anchor.CollectionScan.Variable, Path: []string{"_key"}}, ir.PhysicalValue{Variable: stage.InputRowVariable, Path: []string{"_key"}}) ||
		!matchesSingleDocumentScope(subplan.Operations[2:6], anchor.CollectionScan.Variable) {
		return nil, false
	}
	traversals := make([]ir.PhysicalTraversal, 0, (len(subplan.Operations)-6)/7)
	position := 6
	currentType := plan.Source.ResourceType
	currentVariable := anchor.CollectionScan.Variable
	for position < len(subplan.Operations) {
		operation := subplan.Operations[position]
		if operation.Kind != ir.PhysicalTraversalOp || operation.Traversal == nil {
			return nil, false // Reject contributor predicates and any other extra subplan work.
		}
		traversal := *operation.Traversal
		sourceType, sourceOK := plan.BindVars[traversal.SourceTypeBindKey].(string)
		targetType, targetOK := plan.BindVars[traversal.TargetTypeBindKey].(string)
		label, labelOK := plan.BindVars[traversal.EdgeLabelBindKey].(string)
		edgeCollection, edgeCollectionOK := plan.BindVars[traversal.EdgeCollectionBindKey].(string)
		if !sourceOK || !targetOK || !labelOK || !edgeCollectionOK || sourceType != currentType || traversal.SourceVariable != currentVariable ||
			targetType == "" || label == "" || edgeCollection != "fhir_edge" || traversal.EdgeVariable == "" ||
			traversal.Strategy != ir.PhysicalTraversalEndpointLookup {
			return nil, false
		}
		wantEndpoint, wantJoin, wantTargetField := "_to", "_from", "from_type"
		if traversal.Direction == ir.PhysicalOutbound {
			wantEndpoint, wantJoin, wantTargetField = "_from", "_to", "to_type"
		} else if traversal.Direction != ir.PhysicalInbound {
			return nil, false
		}
		if traversal.EndpointField != wantEndpoint || traversal.EndpointJoinField != wantJoin || traversal.EdgeTargetTypeField != wantTargetField ||
			len(traversal.EndpointIndexFields) != 5 || !reflect.DeepEqual(traversal.EndpointIndexFields, []string{wantEndpoint, "project", "dataset_generation", "label", wantTargetField}) ||
			position+7 > len(subplan.Operations) || !matchesTraversalScope(subplan.Operations[position+1:position+7], traversal) {
			return nil, false
		}
		traversals = append(traversals, traversal)
		currentType, currentVariable = targetType, traversal.TargetVariable
		position += 7
	}
	if len(traversals) == 0 || subplan.Return.Kind != ir.PhysicalValueExpression || subplan.Return.Value == nil ||
		subplan.Return.Value.Variable != currentVariable || !reflect.DeepEqual(subplan.Return.Value.Path, []string{"_id"}) {
		return nil, false
	}
	return traversals, true
}

func matchesSingleDocumentScope(operations []ir.PhysicalOperation, variable string) bool {
	if len(operations) != 4 || !physicalFilterEquals(operations[0], ir.PhysicalValue{Variable: variable, Path: []string{"project"}}, ir.PhysicalValue{BindKey: "project"}) ||
		!physicalFilterEquals(operations[1], ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}}, ir.PhysicalValue{BindKey: "dataset_generation"}) {
		return false
	}
	let := operations[2].DerivedLet
	if operations[2].Kind != ir.PhysicalDerivedLetOp || let == nil || !strings.EqualFold(strings.TrimSpace(let.Operator), "AUTH_RESOURCE_PATH_ALLOWED") ||
		len(let.Inputs) != 3 || !samePhysicalValue(let.Inputs[0], ir.PhysicalValue{Variable: variable, Path: []string{"auth_resource_path"}}) ||
		!samePhysicalValue(let.Inputs[1], ir.PhysicalValue{BindKey: "auth_resource_paths"}) ||
		!samePhysicalValue(let.Inputs[2], ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"}) {
		return false
	}
	return physicalFilterEquals(operations[3], ir.PhysicalValue{Variable: let.Variable}, ir.PhysicalValue{BindKey: "scope_allowed"})
}

func matchesTraversalScope(operations []ir.PhysicalOperation, traversal ir.PhysicalTraversal) bool {
	if len(operations) != 6 || !physicalFilterEquals(operations[0], ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"project"}}, ir.PhysicalValue{BindKey: "project"}) ||
		!physicalFilterEquals(operations[1], ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"project"}}, ir.PhysicalValue{BindKey: "project"}) ||
		!physicalFilterEquals(operations[2], ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"dataset_generation"}}, ir.PhysicalValue{BindKey: "dataset_generation"}) ||
		!physicalFilterEquals(operations[3], ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"dataset_generation"}}, ir.PhysicalValue{BindKey: "dataset_generation"}) {
		return false
	}
	let := operations[4].DerivedLet
	if operations[4].Kind != ir.PhysicalDerivedLetOp || let == nil || !strings.EqualFold(strings.TrimSpace(let.Operator), "AUTH_RESOURCE_PATH_ALLOWED") ||
		len(let.Inputs) != 4 || !samePhysicalValue(let.Inputs[0], ir.PhysicalValue{Variable: traversal.EdgeVariable, Path: []string{"auth_resource_path"}}) ||
		!samePhysicalValue(let.Inputs[1], ir.PhysicalValue{Variable: traversal.TargetVariable, Path: []string{"auth_resource_path"}}) ||
		!samePhysicalValue(let.Inputs[2], ir.PhysicalValue{BindKey: "auth_resource_paths"}) ||
		!samePhysicalValue(let.Inputs[3], ir.PhysicalValue{BindKey: "auth_resource_paths_unrestricted"}) {
		return false
	}
	return physicalFilterEquals(operations[5], ir.PhysicalValue{Variable: let.Variable}, ir.PhysicalValue{BindKey: "scope_allowed"})
}

func physicalFilterEquals(operation ir.PhysicalOperation, left, right ir.PhysicalValue) bool {
	if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil || operation.Filter.Expression != nil {
		return false
	}
	predicate := operation.Filter.Predicate
	return strings.EqualFold(strings.TrimSpace(predicate.Operator), "EQUALS") && predicate.Right != nil &&
		samePhysicalValue(predicate.Left, left) && samePhysicalValue(*predicate.Right, right)
}

func samePhysicalValue(left, right ir.PhysicalValue) bool {
	return reflect.DeepEqual(left, right)
}
