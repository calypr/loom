package aql

import (
	"fmt"
	"strings"
)

// renderGraphPhysicalPlan is the graph/path counterpart to the dataframe
// navigation layout. It deliberately shares the same root scope operations
// and traversal renderer primitives, but keeps path rows correlated until a
// single final union/dedupe/sort/limit operation.
func renderGraphPhysicalPlan(plan PhysicalPlan, collectionKeys map[string]struct{}) (RenderedPhysicalPlan, error) {
	if len(plan.Operations) < 3 || plan.Operations[0].Kind != PhysicalRootScanOp {
		return RenderedPhysicalPlan{}, fmt.Errorf("graph renderer requires ROOT_SCAN followed by path operations")
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != PhysicalGraphReturnOp || last.GraphReturn == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("graph renderer requires GRAPH_RETURN as final operation")
	}
	r := &physicalPlanRenderer{
		bindVars:       runtimePhysicalBindVars(plan.BindVars, collectionKeys),
		collectionKeys: collectionKeys,
		setVariables:   map[string]string{},
		reservedVars:   physicalPlanVariableNames(plan),
	}
	root := plan.Operations[0].RootScan
	inner := []string{fmt.Sprintf("FOR %s IN @@%s", root.Variable, root.CollectionBindKey)}
	pathStart := 1
	for pathStart < len(plan.Operations)-1 && plan.Operations[pathStart].Kind != PhysicalPathSeedOp && plan.Operations[pathStart].Kind != PhysicalPathExtendOp {
		operation := plan.Operations[pathStart]
		if operation.Kind == PhysicalFilterOp || operation.Kind == PhysicalDerivedLetOp || operation.Kind == PhysicalExpressionLetOp {
			rendered, err := r.renderScopeOperation(operation, "  ")
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render graph root scope %d: %w", pathStart, err)
			}
			inner = append(inner, rendered...)
			pathStart++
			continue
		}
		return RenderedPhysicalPlan{}, fmt.Errorf("unsupported graph root operation %d (%s)", pathStart, operation.Kind)
	}
	for index := pathStart; index < len(plan.Operations)-1; index++ {
		op := plan.Operations[index]
		var rendered []string
		var err error
		switch op.Kind {
		case PhysicalPathSeedOp:
			rendered, err = r.renderPathSeed(*op.PathSeed)
		case PhysicalPathExtendOp:
			rendered, err = r.renderPathExtend(*op.PathExtend, index)
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("unsupported graph path operation %d (%s)", index, op.Kind)
		}
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render graph path operation %d: %w", index, err)
		}
		inner = append(inner, rendered...)
	}
	rowFields := make([]string, len(last.GraphReturn.PathSets))
	for i, set := range last.GraphReturn.PathSets {
		rowFields[i] = fmt.Sprintf("s%d: %s", i, set)
	}
	inner = append(inner, "  RETURN { "+strings.Join(rowFields, ", ")+" }", ")")
	rows := r.newInternalVariable("graph_rows")
	lines := []string{fmt.Sprintf("LET %s = (", rows)}
	for _, line := range inner {
		lines = append(lines, "  "+line)
	}
	result, err := r.renderGraphReturn(*last.GraphReturn, rows)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render graph return: %w", err)
	}
	lines = append(lines, result...)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderPathSeed(seed PhysicalPathSeed) ([]string, error) {
	value, err := r.renderValue(seed.Node.Value)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("{ terminalAlias: %q, terminal: %s, nodes: [{ alias: %q, resourceType: %q, id: %s.id, key: %s._key, payload: %s.payload }], relationships: [], __loom_path_identity: TO_STRING(%s._id), __loom_route_order: %d }", seed.Node.Alias, value, seed.Node.Alias, seed.Node.ResourceType, value, value, value, value, seed.RouteOrder)
	r.setVariables[seed.Variable] = seed.Variable
	return []string{fmt.Sprintf("  LET %s = [%s]", seed.Variable, path)}, nil
}

func (r *physicalPlanRenderer) renderPathExtend(extend PhysicalPathExtend, index int) ([]string, error) {
	parentSet, ok := r.setVariables[extend.SourceVariable]
	if !ok {
		return nil, fmt.Errorf("source path set %q has not been rendered", extend.SourceVariable)
	}
	parent := r.newInternalVariable(fmt.Sprintf("path_parent_%d", index))
	current := r.newInternalVariable(fmt.Sprintf("path_current_%d", index))
	indent := "    "
	lines := []string{fmt.Sprintf("  LET %s = (", extend.Variable), fmt.Sprintf("    FOR %s IN %s", parent, parentSet)}
	if len(extend.SourcePath) == 0 {
		lines = append(lines, fmt.Sprintf("      LET %s = %s.terminal", current, parent))
	} else {
		path := parent
		for _, segment := range extend.SourcePath {
			path += "." + segment
		}
		lines = append(lines, fmt.Sprintf("      LET %s = %s", current, path))
	}
	tr := extend.Traversal
	target := tr.TargetVariable
	edge := tr.EdgeVariable
	strategy := tr.Strategy
	if strategy == "" {
		strategy = PhysicalTraversalNative
	}
	if strategy == PhysicalTraversalEndpointLookup {
		lines = append(lines,
			fmt.Sprintf("%sFOR %s IN @@%s", indent, edge, tr.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == %s._id", indent, edge, tr.EndpointField, current),
			fmt.Sprintf("%s  FILTER %s.label == @%s", indent, edge, tr.EdgeLabelBindKey),
			fmt.Sprintf("%s  FILTER %s.%s == @%s", indent, edge, tr.EdgeTargetTypeField, tr.TargetTypeBindKey),
			fmt.Sprintf("%s  LET %s = DOCUMENT(%s.%s)", indent, target, edge, tr.EndpointJoinField),
			fmt.Sprintf("%s  FILTER %s != null", indent, target),
			fmt.Sprintf("%s  FILTER %s.resourceType == @%s", indent, target, tr.TargetTypeBindKey),
		)
	} else {
		lines = append(lines,
			fmt.Sprintf("%sFOR %s, %s IN 1..1 %s %s @@%s", indent, target, edge, tr.Direction, current, tr.EdgeCollectionBindKey),
			fmt.Sprintf("%s  FILTER %s.label == @%s", indent, edge, tr.EdgeLabelBindKey),
		)
		lines = append(lines, r.renderTraversalTypeFilters(&tr, indent)...)
	}
	for scopeIndex, operation := range extend.Scope {
		rendered, err := r.renderScopeOperation(operation, indent+"  ")
		if err != nil {
			return nil, fmt.Errorf("path scope %d: %w", scopeIndex, err)
		}
		lines = append(lines, rendered...)
	}
	nodeValue, err := r.renderValue(extend.Node.Value)
	if err != nil {
		return nil, err
	}
	node := fmt.Sprintf("{ alias: %q, resourceType: %q, id: %s.id, key: %s._key, payload: %s.payload }", extend.Node.Alias, extend.Node.ResourceType, nodeValue, nodeValue, nodeValue)
	identity := fmt.Sprintf("CONCAT(%s.__loom_path_identity, \"|\", TO_STRING(%s._id), \"|\", TO_STRING(@%s))", parent, target, extend.Relationship.LabelBindKey)
	relationship := fmt.Sprintf("{ alias: %q, label: @%s, fromResourceType: %q, toResourceType: %q }", extend.Relationship.Alias, extend.Relationship.LabelBindKey, extend.Relationship.FromResourceType, extend.Relationship.ToResourceType)
	path := fmt.Sprintf("{ terminalAlias: %q, terminal: %s, nodes: APPEND(%s.nodes, [%s]), relationships: APPEND(%s.relationships, [%s]), __loom_path_identity: %s, __loom_route_order: %d }", extend.Node.Alias, target, parent, node, parent, relationship, identity, extend.RouteOrder)
	lines = append(lines, indent+"  RETURN "+path, "  )")
	r.setVariables[extend.Variable] = extend.Variable
	return lines, nil
}

func (r *physicalPlanRenderer) renderGraphReturn(graph PhysicalGraphReturn, rows string) ([]string, error) {
	all := r.newInternalVariable("all_paths")
	path := r.newInternalVariable("path")
	group := r.newInternalVariable("path_group")
	row := r.newInternalVariable("path_row")
	rowSets := make([]string, len(graph.PathSets))
	for index := range graph.PathSets {
		rowSets[index] = fmt.Sprintf("%s[*].s%d", rows, index)
	}
	lines := []string{
		// rows[*].sN is one array per root; the outer list adds the route-set
		// dimension, so flatten two levels to obtain individual path objects.
		fmt.Sprintf("LET %s = FLATTEN([%s], 2)", all, strings.Join(rowSets, ", ")),
		fmt.Sprintf("  FOR %s IN %s", path, all),
		fmt.Sprintf("    COLLECT __loom_path_identity = %s.__loom_path_identity INTO %s", path, group),
		fmt.Sprintf("    LET %s = FIRST(%s[*].%s)", row, group, path),
		fmt.Sprintf("    SORT %s.__loom_route_order, __loom_path_identity", row),
		fmt.Sprintf("    LIMIT @%s", graph.LimitBindKey),
		fmt.Sprintf("    RETURN UNSET(%s, \"terminal\", \"__loom_path_identity\", \"__loom_route_order\")", row),
	}
	return lines, nil
}
