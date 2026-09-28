package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderRelatedEligibilityCountRows reverses one exact EXISTS route so the
// terminal COUNT_ROWS sees distinct eligible root identities without first
// materializing every source row. Every scope predicate is matched against
// the canonical typed plan before this renderer relocates it.
func renderRelatedEligibilityCountRows(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) (RenderedPhysicalPlan, bool, error) {
	if !terminalRelatedEligibilityCountRowsOnly(plan, sequence, options) {
		return RenderedPhysicalPlan{}, false, nil
	}

	stage := sequence.Stages[0]
	exists := stage.Filter.Expression.Exists
	traversals, ok := relatedCountRoute(plan, stage, exists)
	if !ok || len(traversals) == 0 {
		return RenderedPhysicalPlan{}, false, nil
	}
	terminal := traversals[len(traversals)-1]
	terminalType := plan.BindVars[terminal.TargetTypeBindKey].(string)

	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, false, err
	}
	bindValues := make(map[string]any, len(plan.BindVars)+1)
	for key, value := range plan.BindVars {
		bindValues[key] = value
	}
	terminalCollectionBind := "__loom_related_count_terminal_collection"
	for suffix := 2; ; suffix++ {
		if _, exists := bindValues[terminalCollectionBind]; !exists {
			break
		}
		terminalCollectionBind = fmt.Sprintf("__loom_related_count_terminal_collection_%d", suffix)
	}
	bindValues[terminalCollectionBind] = terminalType
	collectionKeys[terminalCollectionBind] = struct{}{}
	bindVars := runtimePhysicalBindVars(bindValues, collectionKeys)
	renderer := physicalPlanRenderer{
		bindVars:       bindVars,
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   stageSequenceVariableNames(plan),
		internalPrefix: "related_count_",
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
			return RenderedPhysicalPlan{}, false, nil
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

	grouped := renderer.newInternalVariable("grouped_rows")
	lines = append(lines, fmt.Sprintf("LET %s = (", grouped))
	groupLines, err := renderer.renderConstructionGroupStage(sequence.Stages[1], frontier)
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

func terminalRelatedEligibilityCountRowsOnly(plan ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, options physicalRenderOptions) bool {
	if sequence == nil || sequence.CellTraceReturn != nil || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow ||
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
