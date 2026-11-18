package compilation

import (
	"encoding/json"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func mergeAdditiveOutput(dst *recipe.Output, src recipe.Output) {
	dst.Fields = append(dst.Fields, src.Fields...)
	dst.Filters = append(dst.Filters, src.Filters...)
	dst.Pivots = append(dst.Pivots, src.Pivots...)
	dst.OwnerRecords = append(dst.OwnerRecords, src.OwnerRecords...)
	dst.Aggregates = append(dst.Aggregates, src.Aggregates...)
	dst.Slices = append(dst.Slices, src.Slices...)
	dst.DynamicColumns = append(dst.DynamicColumns, src.DynamicColumns...)
	dst.ExtensionColumns = append(dst.ExtensionColumns, src.ExtensionColumns...)
	dst.CatalogProjections = append(dst.CatalogProjections, src.CatalogProjections...)
	dst.ColumnTransformations = append(dst.ColumnTransformations, src.ColumnTransformations...)
	dst.Traversals = mergeAdditiveTraversals(dst.Traversals, src.Traversals)
}

func mergeAdditiveTraversals(dst []recipe.Traversal, src []recipe.Traversal) []recipe.Traversal {
	for _, incoming := range src {
		index := -1
		for candidate := range dst {
			if dst[candidate].OccurrenceID == incoming.OccurrenceID {
				index = candidate
				break
			}
		}
		if index < 0 {
			dst = append(dst, incoming)
			continue
		}
		dst[index].Fields = append(dst[index].Fields, incoming.Fields...)
		dst[index].Filters = append(dst[index].Filters, incoming.Filters...)
		dst[index].Pivots = append(dst[index].Pivots, incoming.Pivots...)
		dst[index].OwnerRecords = append(dst[index].OwnerRecords, incoming.OwnerRecords...)
		dst[index].Aggregates = append(dst[index].Aggregates, incoming.Aggregates...)
		dst[index].Slices = append(dst[index].Slices, incoming.Slices...)
		dst[index].DynamicColumns = append(dst[index].DynamicColumns, incoming.DynamicColumns...)
		dst[index].ExtensionColumns = append(dst[index].ExtensionColumns, incoming.ExtensionColumns...)
		dst[index].CatalogProjections = append(dst[index].CatalogProjections, incoming.CatalogProjections...)
		dst[index].Traversals = mergeAdditiveTraversals(dst[index].Traversals, incoming.Traversals)
	}
	return dst
}

func importedTraversalAliases(output recipe.Output) map[string]string {
	aliases := map[string]string{"base": "root"}
	var walk func([]recipe.Traversal)
	walk = func(traversals []recipe.Traversal) {
		for _, traversal := range traversals {
			alias := strings.TrimSpace(traversal.Alias)
			if alias == "" {
				alias = strings.TrimSpace(traversal.Name)
			}
			if traversal.OccurrenceID != "" && alias != "" {
				aliases[traversal.OccurrenceID] = alias
			}
			walk(traversal.Traversals)
		}
	}
	walk(output.Traversals)
	return aliases
}

func rewriteAdditiveAliases(output *recipe.Output, aliases map[string]string) {
	rewrite := func(occurrenceID string, expressions ...*recipe.Expression) {
		from := semanticAlias(occurrenceID)
		to := aliases[occurrenceID]
		if to == "" || from == to {
			return
		}
		for _, expression := range expressions {
			rewriteExpressionAlias(expression, from, to)
		}
	}
	rewriteSelector := func(occurrenceID string, selector *string) {
		from := semanticAlias(occurrenceID)
		to := aliases[occurrenceID]
		if to == "" || from == to || selector == nil {
			return
		}
		*selector = rewriteAliasPrefix(*selector, from, to)
	}
	var walk func(occurrenceID string, fields []recipe.Field, filters []recipe.Filter, pivots []recipe.Pivot, owner []recipe.OwnerRecordProjection, aggregates []recipe.Aggregate, dynamics []recipe.DynamicColumn, traversals []recipe.Traversal)
	walk = func(occurrenceID string, fields []recipe.Field, filters []recipe.Filter, pivots []recipe.Pivot, owner []recipe.OwnerRecordProjection, aggregates []recipe.Aggregate, dynamics []recipe.DynamicColumn, traversals []recipe.Traversal) {
		for index := range fields {
			rewrite(occurrenceID, &fields[index].Expr)
			for fallback := range fields[index].Fallbacks {
				rewrite(occurrenceID, &fields[index].Fallbacks[fallback])
			}
		}
		for index := range filters {
			rewriteSelector(occurrenceID, &filters[index].Select)
		}
		for index := range pivots {
			rewrite(occurrenceID, &pivots[index].ColumnExpr, &pivots[index].ValueExpr, &pivots[index].ItemSource)
			for fallback := range pivots[index].ValueFallbacks {
				rewrite(occurrenceID, &pivots[index].ValueFallbacks[fallback])
			}
			rewriteSelector(occurrenceID, &pivots[index].FieldRef)
		}
		for index := range aggregates {
			rewrite(occurrenceID, aggregates[index].Expr)
			if aggregates[index].Where != nil {
				rewriteSelector(occurrenceID, &aggregates[index].Where.Select)
			}
			if aggregates[index].ContributorWindow != nil {
				window := aggregates[index].ContributorWindow
				rewrite(occurrenceID, &window.Timestamp, &window.Anchor)
			}
			if aggregates[index].Ordering != nil {
				rewrite(occurrenceID, &aggregates[index].Ordering.Timestamp)
			}
			if aggregates[index].UnitNormalization != nil {
				rewriteSelector(occurrenceID, &aggregates[index].UnitNormalization.SystemPath)
				rewriteSelector(occurrenceID, &aggregates[index].UnitNormalization.CodePath)
			}
		}
		for index := range dynamics {
			rewrite(occurrenceID, &dynamics[index].Source, dynamics[index].Key, dynamics[index].Value)
		}
		for index := range traversals {
			walk(traversals[index].OccurrenceID, traversals[index].Fields, traversals[index].Filters, traversals[index].Pivots, traversals[index].OwnerRecords, traversals[index].Aggregates, traversals[index].DynamicColumns, traversals[index].Traversals)
		}
	}
	walk("base", output.Fields, output.Filters, output.Pivots, output.OwnerRecords, output.Aggregates, output.DynamicColumns, output.Traversals)
}

func rewriteExpressionAlias(expression *recipe.Expression, from, to string) {
	if expression == nil {
		return
	}
	expression.Select = rewriteAliasPrefix(expression.Select, from, to)
	for index := range expression.Args {
		rewriteExpressionAlias(&expression.Args[index], from, to)
	}
}

func rewriteAliasPrefix(selector, from, to string) string {
	selector = strings.TrimSpace(selector)
	if selector == from {
		return to
	}
	if strings.HasPrefix(selector, from+".") {
		return to + strings.TrimPrefix(selector, from)
	}
	return selector
}

func cloneRecipeOutput(output recipe.Output) (recipe.Output, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return recipe.Output{}, err
	}
	var clone recipe.Output
	if err := json.Unmarshal(raw, &clone); err != nil {
		return recipe.Output{}, err
	}
	return clone, nil
}

func importedCompileInputs(snapshot capability.Snapshot, route authoringv2.RouteNode) (capability.Snapshot, authoringv2.RouteNode) {
	cloned := snapshot.Clone()
	rootID := importedRootNode(&cloned, route.ResourceType)
	walkImportedRoute(&cloned, rootID, &route)
	return cloned, route
}

func importedRootNode(snapshot *capability.Snapshot, resourceType string) string {
	chosen := ""
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].ResourceType != resourceType {
			continue
		}
		if chosen == "" {
			chosen = snapshot.Nodes[index].ID
			snapshot.Nodes[index].RowRootEligible = true
			continue
		}
		snapshot.Nodes[index].RowRootEligible = false
	}
	if chosen == "" {
		chosen = "imported_node_" + shortHash(resourceType)
		snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: chosen, ResourceType: resourceType, RowRootEligible: true})
	}
	return chosen
}

func importedChildNode(snapshot *capability.Snapshot, resourceType string) string {
	for _, node := range snapshot.Nodes {
		if node.ResourceType == resourceType {
			return node.ID
		}
	}
	id := "imported_node_" + shortHash(resourceType)
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: id, ResourceType: resourceType})
	return id
}

func walkImportedRoute(snapshot *capability.Snapshot, parentID string, node *authoringv2.RouteNode) {
	for index := range node.Children {
		child := &node.Children[index]
		childID := importedChildNode(snapshot, child.ResourceType)
		if child.CatalogEdgeID == "" {
			if matches := importedRouteEdges(*snapshot, parentID, child); len(matches) == 1 {
				child.CatalogEdgeID = matches[0].ID
			}
		}
		if child.CatalogEdgeID == "" || !importedEdgeExists(*snapshot, child.CatalogEdgeID) {
			child.CatalogEdgeID = "imported_edge_" + shortHash(parentID+"\x00"+child.OccurrenceID+"\x00"+child.Relationship+"\x00"+childID)
			snapshot.Edges = append(snapshot.Edges, capability.Edge{
				ID: child.CatalogEdgeID, FromNodeID: parentID, ToNodeID: childID, Label: child.Relationship,
				SourceResourceType: node.ResourceType, TargetResourceType: child.ResourceType,
			})
		}
		walkImportedRoute(snapshot, childID, child)
	}
}

func importedRouteEdges(snapshot capability.Snapshot, parentID string, child *authoringv2.RouteNode) []capability.Edge {
	var matches []capability.Edge
	for _, edge := range snapshot.Edges {
		if edge.ID == "" || edge.FromNodeID != parentID || edge.Label != child.Relationship || edge.BlockedReason != "" {
			continue
		}
		target, ok := snapshot.Node(edge.ToNodeID)
		if !ok || target.ResourceType != child.ResourceType {
			continue
		}
		matches = append(matches, edge)
	}
	return matches
}

func importedEdgeExists(snapshot capability.Snapshot, id string) bool {
	for _, edge := range snapshot.Edges {
		if edge.ID == id {
			return true
		}
	}
	return false
}
