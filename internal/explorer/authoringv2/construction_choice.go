package authoringv2

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
)

func applyColumnSource(workspace *Workspace, catalog CatalogSnapshot, commandID string, index int, command Command, source ColumnSource, logicalType, presentation string) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	source = editableSource(command.OccurrenceID, source)
	if err := validateResolvedConstructionChoiceSource(workspace.Documents[documentPos], catalog, command, source); err != nil {
		return result, err
	}
	var contributor *ContributorPredicate
	if command.Contributor != nil {
		normalized := command.Contributor.Normalized()
		if err := ValidateContributorForCatalog(workspace.Documents[documentPos], catalog, command.OccurrenceID, source, normalized); err != nil {
			return result, err
		}
		contributor = &normalized
	}
	columnID := commandGeneratedID("col_", command.OutputID, commandID, index, command.Type)
	for _, existing := range workspace.Documents[documentPos].Columns {
		if existing.Column != columnID {
			continue
		}
		if existing.OccurrenceID != command.OccurrenceID || !sourceEqual(existing.Source, source) || !contributorEqual(existing.Contributor, contributor) {
			return result, fmt.Errorf("generated column identity %q conflicts with a different feature", columnID)
		}
		return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: existing.Column}, nil
	}
	label := strings.TrimSpace(command.Title)
	if label == "" {
		label = strings.TrimSpace(source.fieldPath())
	}
	if label == "" {
		label = source.Kind
	}
	if strings.TrimSpace(logicalType) == "" {
		logicalType = inferredSourceLogicalType(workspace.Documents[documentPos], catalog, command.OccurrenceID, source, "string")
	}
	column := Column{Column: columnID, Label: label, LogicalType: logicalType, OccurrenceID: command.OccurrenceID, Source: source, Contributor: contributor}
	column.ColumnID = stagedSourceColumnID(workspace.Documents[documentPos].Output.ID, commandID, index, command.Type, column.Column)
	applyInitialPresentation(&column, presentation, nextTableOrder(workspace.Documents[documentPos]))
	workspace.Documents[documentPos].Columns = append(workspace.Documents[documentPos].Columns, column)
	var policy ConstructionRowValuePolicy
	if command.ConstructionChoice != nil {
		policy = command.ConstructionChoice.RowValuePolicy
	}
	document := &workspace.Documents[documentPos]
	if document.Rows.Kind == RowDefinitionGroups && document.Rows.Groups != nil &&
		document.Rows.Groups.Source.Kind == GroupSourceExplicit {
		if command.ConstructionChoice != nil {
			if err := populateExplicitGroupRowValue(document, column, policy); err != nil {
				return result, err
			}
		}
	} else if err := populateConstructionColumn(document, column, policy); err != nil {
		return result, err
	}
	return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: columnID}, nil
}

func validateResolvedConstructionChoiceSource(document Document, catalog CatalogSnapshot, command Command, source ColumnSource) error {
	validate := func(candidateSource ColumnSource) error {
		return validateEditableSource(document, catalog, command.OccurrenceID, candidateSource)
	}
	selection, resolved := command.ConstructionChoice, command.ResolvedChoice
	if selection == nil || resolved == nil || selection.Form != capability.ConstructionChoiceAll ||
		command.OccurrenceID == RootOccurrenceID || source.Kind != SourceField || source.Field == nil ||
		!strings.EqualFold(strings.TrimSpace(source.Field.ProjectionMode), string(capability.ConstructionChoiceAll)) ||
		!sourceEqual(source, resolved.Source) || len(resolved.Route) == 0 {
		return validate(source)
	}

	candidate, found := catalogCandidate(catalog, resolved.CandidateID)
	occurrence := findRoute(&document.Route, command.OccurrenceID)
	lastHop := resolved.Route[len(resolved.Route)-1]
	if !found || occurrence == nil || occurrence.ResourceType != lastHop.ToResourceType ||
		candidate.NodeID != lastHop.ToNodeID ||
		strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") != strings.TrimPrefix(strings.TrimSpace(source.FieldPath()), "root.") ||
		capability.IsRepeatedCardinality(candidate.Cardinality) ||
		!contains(candidate.ProjectionModes, "VALUE") {
		return validate(source)
	}
	if contains(candidate.ProjectionModes, string(capability.ConstructionChoiceAll)) {
		return validate(source)
	}

	// Route-level ALL is compiler-supported for scalar fields even though the
	// terminal field candidate itself advertises only scalar VALUE. The choice
	// has already been re-authorized against its exact route and form by the
	// lifecycle; validate its underlying field identity using that advertised
	// scalar mode while preserving ALL in the saved source.
	valueSource := source
	field := *source.Field
	field.ProjectionMode = "VALUE"
	field.RelatedSelection = nil
	valueSource.Field = &field
	return validate(valueSource)
}

func ensureConstructionRoute(document *Document, catalog CatalogSnapshot, commandID string, commandIndex int, route []capability.ConstructionRouteStep, candidateID string) (string, error) {
	if document == nil || document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return "", fmt.Errorf("table root route is invalid")
	}
	if catalog.RoutePolicy.MaxHops != nil && len(route) > *catalog.RoutePolicy.MaxHops {
		return "", fmt.Errorf("ROUTE_TOO_LONG: construction route exceeds capability route policy")
	}
	rootNode, found := catalogNodeForConstructionRoot(catalog, document.RootResourceType)
	if !found {
		return "", fmt.Errorf("table root is not available in the capability catalog")
	}
	candidate, found := catalogCandidate(catalog, candidateID)
	if !found {
		return "", fmt.Errorf("construction candidate %q is not in the current catalog", candidateID)
	}
	targetNode, found := catalogNode(catalog, candidate.NodeID)
	if !found {
		return "", fmt.Errorf("construction candidate %q has no current catalog node", candidateID)
	}
	current := &document.Route
	currentNodeID := rootNode.ID
	usedEdges := make(map[string]bool, len(route))
	for edgeIndex, step := range route {
		if step.EdgeID == "" || step.FromNodeID != currentNodeID || step.FromResourceType != current.ResourceType ||
			step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return "", fmt.Errorf("construction route step %d does not extend the current route", edgeIndex)
		}
		edge, edgeFound := catalogEdge(catalog, step.EdgeID)
		from, fromFound := catalogNode(catalog, step.FromNodeID)
		to, toFound := catalogNode(catalog, step.ToNodeID)
		if !edgeFound || !fromFound || !toFound || edge.FromNodeID != step.FromNodeID || edge.ToNodeID != step.ToNodeID ||
			edge.Label != step.Relationship || from.ResourceType != step.FromResourceType || to.ResourceType != step.ToResourceType {
			return "", fmt.Errorf("construction route step %d is not in the current catalog", edgeIndex)
		}
		if !catalog.RoutePolicy.AllowSelfLoops && step.FromNodeID == step.ToNodeID {
			return "", fmt.Errorf("construction route step %d is a disallowed self-loop", edgeIndex)
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges && usedEdges[step.EdgeID] {
			return "", fmt.Errorf("construction route repeats an edge disallowed by policy")
		}
		if !catalog.RoutePolicy.AllowRepeatedEdges {
			used, err := routePathUsesCatalogEdge(document.Route, current.OccurrenceID, step.EdgeID, catalog)
			if err != nil {
				return "", fmt.Errorf("construction route parent does not resolve to an exact path: %w", err)
			}
			if used {
				return "", fmt.Errorf("construction route repeats an edge disallowed by policy")
			}
		}
		matched := -1
		for childIndex := range current.Children {
			child := &current.Children[childIndex]
			if child.ResourceType != to.ResourceType || child.Relationship != edge.Label || child.MatchMode.Normalized() != RouteMatchMode(step.MatchMode) {
				continue
			}
			existingEdgeID := child.CatalogEdgeID
			if existingEdgeID == "" {
				legacyEdge, err := resolveCatalogRouteEdge(catalog, currentNodeID, *current, *child)
				if err != nil {
					return "", fmt.Errorf("construction route step %d has an ambiguous legacy occurrence: %w", edgeIndex, err)
				}
				child.CatalogEdgeID = legacyEdge.ID
				existingEdgeID = legacyEdge.ID
			}
			if existingEdgeID != step.EdgeID {
				continue
			}
			if matched >= 0 {
				return "", fmt.Errorf("construction route step %d matches multiple existing branches", edgeIndex)
			}
			matched = childIndex
		}
		if matched < 0 {
			occurrenceID := commandGeneratedID("occ_", "construction-route/v1", document.Output.ID, commandID, commandIndex, edgeIndex, step.EdgeID)
			current.Children = append(current.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, CatalogEdgeID: edge.ID, Relationship: edge.Label, MatchMode: RouteMatchMode(step.MatchMode)})
			matched = len(current.Children) - 1
		}
		current = &current.Children[matched]
		currentNodeID = step.ToNodeID
		usedEdges[step.EdgeID] = true
	}
	if currentNodeID != candidate.NodeID || current.ResourceType != targetNode.ResourceType {
		return "", fmt.Errorf("construction route terminal does not match the selected candidate")
	}
	return current.OccurrenceID, nil
}

func catalogNodeForConstructionRoot(catalog CatalogSnapshot, resourceType string) (CatalogNode, bool) {
	var result CatalogNode
	for _, node := range catalog.Nodes {
		if !node.RowRootEligible || node.ResourceType != resourceType {
			continue
		}
		if result.ID != "" {
			return CatalogNode{}, false
		}
		result = node
	}
	return result, result.ID != ""
}
