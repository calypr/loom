package dataframe

import (
	"fmt"
	"strings"
)

// compileRequiredTraversalMatch emits a bounded root-correlated existence
// subquery. Each step is rechecked against the compiler-owned stored-edge
// route contract, so a manually assembled lowered Builder cannot turn an
// unproven schema-valid forward FHIR reference into an incorrect semi-join.
func (c *compiler) compileRequiredTraversalMatch(rootVar string, matchIndex int, match RequiredTraversalMatch) (string, error) {
	if len(match.Steps) == 0 {
		return "", fmt.Errorf("required traversal match %d has no route steps", matchIndex)
	}

	lines := make([]string, 0, len(match.Steps)*6+2)
	parentVar := rootVar
	parentResourceType := c.builder.RootResourceType
	for stepIndex, step := range match.Steps {
		route, err := resolveStorageRoute(parentResourceType, step.Label, step.ToResourceType)
		if err != nil {
			return "", fmt.Errorf("compile required traversal match %d step %d: %w", matchIndex, stepIndex, err)
		}
		nodeVar := fmt.Sprintf("__match_%d_%d", matchIndex, stepIndex)
		edgeVar := fmt.Sprintf("__match_edge_%d_%d", matchIndex, stepIndex)
		labelBind := c.newBind(fmt.Sprintf("match_%d_%d_label", matchIndex, stepIndex), step.Label)
		toBind := c.newBind(fmt.Sprintf("match_%d_%d_to", matchIndex, stepIndex), step.ToResourceType)
		edgeTypeField := route.targetEdgeTypeField()
		if edgeTypeField == "" {
			return "", fmt.Errorf("compile required traversal match %d step %d: %w: route direction %q has no fhir_edge target type field", matchIndex, stepIndex, ErrUnsupportedStorageRoute, route.Direction)
		}
		edgeTypeBind := c.newBind(fmt.Sprintf("match_%d_%d_edge_target_type", matchIndex, stepIndex), step.ToResourceType)
		lines = append(lines, fmt.Sprintf("FOR %s, %s IN 1..1 %s %s fhir_edge", nodeVar, edgeVar, route.Direction, parentVar))
		lines = append(lines, fmt.Sprintf("  FILTER %s.project == @project", edgeVar))
		lines = append(lines, fmt.Sprintf("  FILTER %s.project == @project", nodeVar))
		lines = append(lines, fmt.Sprintf("  FILTER %s.%s == @%s", edgeVar, datasetGenerationField, datasetGenerationBindKey))
		lines = append(lines, fmt.Sprintf("  FILTER %s.%s == @%s", nodeVar, datasetGenerationField, datasetGenerationBindKey))
		lines = append(lines, fmt.Sprintf("  FILTER @auth_resource_paths_unrestricted == true OR (%s.auth_resource_path IN @auth_resource_paths AND %s.auth_resource_path IN @auth_resource_paths)", edgeVar, nodeVar))
		lines = append(lines, fmt.Sprintf("  FILTER %s.label == @%s", edgeVar, labelBind))
		lines = append(lines, fmt.Sprintf("  FILTER %s.%s == @%s", edgeVar, edgeTypeField, edgeTypeBind))
		lines = append(lines, fmt.Sprintf("  FILTER %s.resourceType == @%s", nodeVar, toBind))
		filters, err := c.compileTypedFilters(nodeVar+".payload", step.Filters)
		if err != nil {
			return "", fmt.Errorf("compile required traversal match %d step %d filters: %w", matchIndex, stepIndex, err)
		}
		if filters != "true" {
			lines = append(lines, "  FILTER ("+filters+")")
		}
		parentVar = nodeVar
		parentResourceType = step.ToResourceType
	}
	lines = append(lines, "  LIMIT 1", "  RETURN 1")
	return "LENGTH(\n    " + strings.Join(lines, "\n    ") + "\n  ) > 0", nil
}

func (c *compiler) compileRequiredTraversalMatches(rootVar string, matches []RequiredTraversalMatch) ([]string, error) {
	filters := make([]string, 0, len(matches))
	for matchIndex, match := range matches {
		expr, err := c.compileRequiredTraversalMatch(rootVar, matchIndex, match)
		if err != nil {
			return nil, err
		}
		filters = append(filters, expr)
	}
	return filters, nil
}
