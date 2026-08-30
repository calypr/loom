package compilation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type semanticOccurrence struct {
	node  authoringv2.RouteNode
	graph capability.Node
	edge  *capability.Edge
}

type semanticRecipeNode struct {
	fields   []recipe.Field
	dynamics []recipe.DynamicColumn
	pivots   []recipe.Pivot
}

func compileSemanticDocument(ctx context.Context, project, explorerID string, document authoringv2.Document, snapshot capability.Snapshot) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	occurrences, order, err := resolveSemanticRoute(document, snapshot)
	if err != nil {
		return Result{}, err
	}
	root := occurrences[authoringv2.RootOccurrenceID]
	rowGrain, ok := spec.InferRowGrain(root.graph.ResourceType)
	if !ok || !root.graph.RowRootEligible {
		return Result{}, fail("lower", "UNSUPPORTED_ROW_ROOT", "$.rootResourceType", "root resource type is not an eligible recipe row root", map[string]any{"resourceType": root.graph.ResourceType}, nil)
	}

	nodes := make(map[string]*semanticRecipeNode, len(occurrences))
	for id := range occurrences {
		nodes[id] = &semanticRecipeNode{}
	}
	emitted := make([]explorer.EmittedColumn, 0, len(document.Columns))
	mappings := make([]explorer.IdentityMapping, 0, len(document.Columns))
	presentation := PresentationConfig{OutputID: document.Output.ID, Title: document.Output.Title, Columns: make([]PresentationColumn, 0, len(document.Columns))}
	contract := explorer.PublicOutputContract{OutputID: document.Output.ID, Columns: make([]explorer.PublicOutputColumn, 0, len(document.Columns))}

	for index, column := range document.Columns {
		occurrence := occurrences[column.OccurrenceID]
		alias := semanticAlias(column.OccurrenceID)
		leaf, err := semanticColumnLeaf(column.Column, column.OccurrenceID)
		if err != nil {
			return Result{}, fail("lower", "COLUMN_ROUTE_PREFIX_MISMATCH", fmt.Sprintf("$.columns[%d].column", index), err.Error(), map[string]any{"column": column.Column, "occurrenceId": column.OccurrenceID}, err)
		}
		logicalType := firstNonEmpty(column.LogicalType, "string")
		filterable, chartable := column.Filter != nil, column.Chart != nil
		candidateID := "source_" + shortHash(column.OccurrenceID+"\x00"+column.Source.Kind+"\x00"+column.Source.FieldPath+"\x00"+column.Source.Match)
		projectionMode := firstNonEmpty(strings.ToUpper(column.Source.ProjectionMode), "FIRST")

		switch column.Source.Kind {
		case authoringv2.SourceField:
			candidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, column.Source.FieldPath)
			if !found {
				return Result{}, fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.fieldPath", index), "field is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": column.Source.FieldPath}, nil)
			}
			candidateID = candidate.ID
			logicalType = firstNonEmpty(column.LogicalType, candidate.LogicalType, "string")
			filterable = filterable && supportsOperation(candidate.SupportedOperations, capability.OperationFilter)
			chartable = chartable && supportsOperation(candidate.SupportedOperations, capability.OperationChart)
			path := strings.TrimPrefix(strings.TrimSpace(column.Source.FieldPath), "root.")
			nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, FieldRef: column.Source.FieldPath, Expr: recipe.Expression{Select: alias + "." + path}, ValueMode: projectionValueMode(projectionMode)})
		case authoringv2.SourceProjectID:
			literal, _ := json.Marshal(project)
			nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, FieldRef: "project.id", Expr: recipe.Expression{Literal: literal}, ValueMode: recipe.ValueModeFirst})
		case authoringv2.SourceObservationComponentByCode:
			pivot, pivotErr := semanticObservationPivot(column, alias, leaf)
			if pivotErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
			}
			nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
		default:
			dynamic, dynamicErr := semanticFixedLookup(column, alias, leaf, logicalType)
			if dynamicErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
			}
			nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
		}

		visible := true
		orderValue := index
		pinned := false
		if column.Table != nil {
			if column.Table.Visible != nil {
				visible = *column.Table.Visible
			}
			if column.Table.Order != nil {
				orderValue = *column.Table.Order
			}
			pinned = column.Table.Pinned
		} else {
			visible = false
		}
		emissionID := column.Column
		emission := explorer.EmittedColumn{EmissionID: emissionID, OutputID: document.Output.ID, NodeID: occurrence.graph.ID, SelectionID: candidateID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, PublicColumn: column.Column, Label: column.Label, LogicalType: logicalType, Filterable: filterable, Chartable: chartable}
		emitted = append(emitted, emission)
		mappings = append(mappings, explorer.IdentityMapping{OutputID: document.Output.ID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, EmissionIDs: []string{emissionID}})
		presented := PresentationColumn{EmissionID: emissionID, PublicColumn: column.Column, Label: column.Label, Visible: visible, Order: orderValue, Pinned: pinned}
		if column.Filter != nil {
			presented.FilterLabel = firstNonEmpty(column.Filter.Label, column.Label)
			presented.FilterOrder = index
			if column.Filter.Order != nil {
				presented.FilterOrder = *column.Filter.Order
			}
		}
		if column.Chart != nil {
			presented.ChartType, presented.ChartTitle = column.Chart.Type, column.Chart.Title
			presented.ChartOrder = index
			if column.Chart.Order != nil {
				presented.ChartOrder = *column.Chart.Order
			}
		}
		presentation.Columns = append(presentation.Columns, presented)
		contract.Columns = append(contract.Columns, explorer.PublicOutputColumn{Column: column.Column, Label: column.Label, LogicalType: logicalType, Filterable: filterable, Chartable: chartable})
	}

	output := recipe.Output{Name: document.Output.ID, RootResourceType: root.graph.ResourceType, RowGrain: string(rowGrain), TraversalColumnNaming: recipe.TraversalColumnNamingAlias, Fields: nodes[authoringv2.RootOccurrenceID].fields, Pivots: nodes[authoringv2.RootOccurrenceID].pivots, DynamicColumns: nodes[authoringv2.RootOccurrenceID].dynamics, CollisionPolicy: "error"}
	output.Traversals = semanticTraversals(document.Route, occurrences, nodes)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeName(project) + "_" + safeName(explorerID), TranslationVersion: TranslationVersion, Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		return Result{}, fail("lower", "INVALID_RECIPE", "$.recipe", err.Error(), nil, err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Result{}, fail("lower", "RECIPE_DIGEST_FAILED", "$.recipe", "recipe digest could not be calculated", nil, err)
	}
	_ = order
	return Result{Bundle: bundle, RecipeDigest: digest, EmittedColumns: emitted, IdentityMappings: mappings, Presentation: presentation, OutputContract: contract}, nil
}

func resolveSemanticRoute(document authoringv2.Document, snapshot capability.Snapshot) (map[string]semanticOccurrence, []string, error) {
	byType := map[string][]capability.Node{}
	for _, node := range snapshot.Nodes {
		byType[node.ResourceType] = append(byType[node.ResourceType], node)
	}
	result := map[string]semanticOccurrence{}
	order := []string{}
	usedEdges := map[string]bool{}
	var walk func(authoringv2.RouteNode, *semanticOccurrence, string, int) error
	walk = func(route authoringv2.RouteNode, parent *semanticOccurrence, path string, depth int) error {
		if snapshot.Policy.Route.MaxHops > 0 && depth > snapshot.Policy.Route.MaxHops {
			return fail("route", "ROUTE_TOO_LONG", path, "route exceeds capability route policy", map[string]any{"maxHops": snapshot.Policy.Route.MaxHops, "hops": depth}, nil)
		}
		var graph capability.Node
		var edge *capability.Edge
		if parent == nil {
			matches := byType[route.ResourceType]
			eligible := matches[:0]
			for _, match := range matches {
				if match.RowRootEligible {
					eligible = append(eligible, match)
				}
			}
			if len(eligible) != 1 {
				return fail("route", "AMBIGUOUS_ROOT_RESOURCE", path+".resourceType", "root resource type must resolve to exactly one eligible capability node", map[string]any{"resourceType": route.ResourceType, "matches": len(eligible)}, nil)
			}
			graph = eligible[0]
		} else {
			matches := []capability.Edge{}
			for _, candidate := range snapshot.Edges {
				target, ok := snapshot.Node(candidate.ToNodeID)
				if candidate.FromNodeID == parent.graph.ID && ok && target.ResourceType == route.ResourceType && candidate.Label == route.Relationship {
					matches = append(matches, candidate)
				}
			}
			if len(matches) != 1 {
				return fail("route", "AMBIGUOUS_RELATIONSHIP", path+".relationship", "relationship must resolve to exactly one capability edge", map[string]any{"fromResourceType": parent.graph.ResourceType, "relationship": route.Relationship, "toResourceType": route.ResourceType, "matches": len(matches)}, nil)
			}
			selected := matches[0]
			if usedEdges[selected.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
				return fail("route", "REPEATED_EDGE_NOT_ALLOWED", path+".relationship", "route policy does not allow repeated edges", nil, nil)
			}
			if selected.FromNodeID == selected.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
				return fail("route", "SELF_LOOP_NOT_ALLOWED", path+".relationship", "route policy does not allow self loops", nil, nil)
			}
			usedEdges[selected.ID] = true
			edge = &selected
			graph, _ = snapshot.Node(selected.ToNodeID)
		}
		current := semanticOccurrence{node: route, graph: graph, edge: edge}
		result[route.OccurrenceID] = current
		order = append(order, route.OccurrenceID)
		children := append([]authoringv2.RouteNode(nil), route.Children...)
		sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
		for i := range children {
			if err := walk(children[i], &current, fmt.Sprintf("%s.children[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Route, nil, "$.route", 0); err != nil {
		return nil, nil, err
	}
	return result, order, nil
}

func semanticTraversals(route authoringv2.RouteNode, occurrences map[string]semanticOccurrence, nodes map[string]*semanticRecipeNode) []recipe.Traversal {
	children := append([]authoringv2.RouteNode(nil), route.Children...)
	sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
	result := make([]recipe.Traversal, 0, len(children))
	for _, child := range children {
		occurrence := occurrences[child.OccurrenceID]
		node := nodes[child.OccurrenceID]
		result = append(result, recipe.Traversal{Name: recipeName(occurrence.edge.Label, occurrence.edge.ID), Alias: semanticAlias(child.OccurrenceID), ToResourceType: occurrence.graph.ResourceType, MatchMode: recipe.MatchOptional, Fields: node.fields, Pivots: node.pivots, DynamicColumns: node.dynamics, Traversals: semanticTraversals(child, occurrences, nodes)})
	}
	return result
}

func semanticAlias(occurrenceID string) string {
	if occurrenceID == authoringv2.RootOccurrenceID {
		return "root"
	}
	// Semantic Builder occurrence IDs are globally unique output namespaces.
	// Keep the complete identifier so ALIAS traversal naming reproduces the
	// authored public-column prefix even when an older client encoded route
	// ancestry into the occurrence ID itself.
	return safeName(occurrenceID)
}

func semanticColumnLeaf(column, occurrenceID string) (string, error) {
	if occurrenceID == authoringv2.RootOccurrenceID {
		return column, nil
	}
	// The traversal alias retains this complete globally unique occurrence ID;
	// strip it here so physical lowering can add it exactly once.
	prefix := safeName(occurrenceID) + "__"
	if !strings.HasPrefix(column, prefix) || strings.TrimPrefix(column, prefix) == "" {
		return "", fmt.Errorf("column for occurrence %q must begin with %q", occurrenceID, prefix)
	}
	return strings.TrimPrefix(column, prefix), nil
}

func semanticFieldCandidate(snapshot capability.Snapshot, nodeID, fieldPath string) (capability.Candidate, bool) {
	want := strings.TrimPrefix(strings.TrimSpace(fieldPath), "root.")
	for _, candidate := range snapshot.Candidates {
		actual := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
		if candidate.NodeID == nodeID && actual == want {
			return candidate, true
		}
	}
	return capability.Candidate{}, false
}

func semanticFixedLookup(column authoringv2.Column, alias, leaf, logicalType string) (recipe.DynamicColumn, error) {
	empty := ""
	fieldPath := strings.Trim(strings.TrimSpace(column.Source.FieldPath), ".")
	sourcePath, keyPath := "", ""
	var value recipe.Expression
	switch column.Source.Kind {
	case authoringv2.SourceIdentifierBySystem:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "identifier[]"), "item.system"
		value = recipe.Expression{Select: "item.value"}
	case authoringv2.SourceExtensionByURL:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "extension[]"), "item.url"
		value = coalesceString("item.valueString", "item.valueCode", "item.valueInteger", "item.valueDecimal", "item.valueBoolean", "item.valueDate", "item.valueDateTime", "item.valueUri")
	case authoringv2.SourceCodingBySystem:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "code.coding[]"), "item.system"
		value = coalesceString("item.display", "item.code")
	default:
		return recipe.DynamicColumn{}, fmt.Errorf("unsupported source kind %q", column.Source.Kind)
	}
	key := recipe.Expression{Select: keyPath}
	return recipe.DynamicColumn{Name: "fixed_" + shortHash(column.Column), ColumnPrefix: &empty, Source: recipe.Expression{Select: alias + "." + sourcePath}, Key: &key, Value: &value, Columns: []string{leaf}, MaxColumns: 1, ColumnTypes: map[string]string{leaf: logicalType}, ColumnSourceKeys: map[string]string{leaf: column.Source.Match}}, nil
}

func semanticObservationPivot(column authoringv2.Column, alias, leaf string) (recipe.Pivot, error) {
	separator := "__" + column.Source.Match
	if !strings.HasSuffix(leaf, separator) || strings.TrimSuffix(leaf, separator) == "" {
		return recipe.Pivot{}, fmt.Errorf("observation component column %q must end with %q", column.Column, separator)
	}
	name := strings.TrimSuffix(leaf, separator)
	sourcePath := firstNonEmpty(strings.Trim(strings.TrimSpace(column.Source.FieldPath), "."), "component[]")
	prefix := alias + "." + sourcePath
	return recipe.Pivot{
		Name:             name,
		ColumnExpr:       recipe.Expression{Select: prefix + ".code.coding[].code"},
		ValueExpr:        recipe.Expression{Select: prefix + ".valueString"},
		ValueFallbacks:   []recipe.Expression{{Select: prefix + ".valueCodeableConcept.text"}, {Select: prefix + ".valueQuantity.value"}, {Select: prefix + ".valueInteger"}},
		ItemSource:       recipe.Expression{Select: alias + "." + sourcePath},
		ItemResourceType: "ObservationComponent",
		Columns:          []string{column.Source.Match},
	}, nil
}

func appendSemanticPivot(pivots []recipe.Pivot, pivot recipe.Pivot) []recipe.Pivot {
	for i := range pivots {
		if pivots[i].Name == pivot.Name {
			pivots[i].Columns = append(pivots[i].Columns, pivot.Columns...)
			return pivots
		}
	}
	return append(pivots, pivot)
}

func coalesceString(paths ...string) recipe.Expression {
	args := make([]recipe.Expression, 0, len(paths))
	for _, path := range paths {
		args = append(args, recipe.Expression{Select: path})
	}
	return recipe.Expression{Call: "coalesce_string", Args: args}
}
