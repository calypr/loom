package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderPathSeed(seed ir.PhysicalPathSeed) ([]string, error) {
	value, err := r.renderValue(seed.Node.Value)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("{ terminalAlias: %q, terminal: %s, nodes: [{ alias: %q, resourceType: %q, id: %s.id, key: %s._key, payload: %s.payload }], relationships: [], __loom_path_identity: TO_STRING(%s._id), __loom_route_order: %d }", seed.Node.Alias, value, seed.Node.Alias, seed.Node.ResourceType, value, value, value, value, seed.RouteOrder)
	r.setVariables[seed.Variable] = seed.Variable
	return []string{fmt.Sprintf("  LET %s = [%s]", seed.Variable, path)}, nil
}

func (r *physicalPlanRenderer) renderPathExtend(extend ir.PhysicalPathExtend, index int) ([]string, error) {
	parentSet, ok := r.setVariables[extend.SourceVariable]
	if !ok {
		return nil, fmt.Errorf("source path set %q has not been rendered", extend.SourceVariable)
	}
	parent := r.navigation().newInternalVariable(fmt.Sprintf("path_parent_%d", index))
	current := r.navigation().newInternalVariable(fmt.Sprintf("path_current_%d", index))
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
		strategy = ir.PhysicalTraversalNative
	}
	if strategy == ir.PhysicalTraversalEndpointLookup {
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
		lines = append(lines, r.navigation().renderTraversalTypeFilters(&tr, indent)...)
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

func (r *physicalPlanRenderer) renderGraphReturn(graph ir.PhysicalGraphReturn, rows string) ([]string, error) {
	all := r.navigation().newInternalVariable("all_paths")
	path := r.navigation().newInternalVariable("path")
	group := r.navigation().newInternalVariable("path_group")
	row := r.navigation().newInternalVariable("path_row")
	rowSets := make([]string, len(graph.PathSets))
	for index := range graph.PathSets {
		rowSets[index] = fmt.Sprintf("%s[*].s%d", rows, index)
	}
	lines := []string{

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
