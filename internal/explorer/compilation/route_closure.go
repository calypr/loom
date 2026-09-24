package compilation

import "github.com/calypr/loom/internal/explorer/authoringv2"

func executableRoute(document authoringv2.Document) authoringv2.RouteNode {
	needed := map[string]struct{}{
		authoringv2.RootOccurrenceID: {},
	}
	for _, column := range document.Columns {
		needed[column.OccurrenceID] = struct{}{}
	}
	if document.Rows.Kind == authoringv2.RowDefinitionExpanded && document.Rows.Expanded != nil {
		needed[document.Rows.Expanded.OccurrenceID] = struct{}{}
	}

	var prune func(authoringv2.RouteNode) (authoringv2.RouteNode, bool)
	prune = func(node authoringv2.RouteNode) (authoringv2.RouteNode, bool) {
		children := make([]authoringv2.RouteNode, 0, len(node.Children))
		included := false
		for _, child := range node.Children {
			pruned, keep := prune(child)
			if keep {
				children = append(children, pruned)
				included = true
			}
		}

		_, directlyNeeded := needed[node.OccurrenceID]
		keep := directlyNeeded || node.MatchMode.Normalized() == authoringv2.RouteMatchRequired || included
		if !keep {
			return authoringv2.RouteNode{}, false
		}
		node.Children = children
		return node, true
	}

	pruned, _ := prune(document.Route)
	return pruned
}
