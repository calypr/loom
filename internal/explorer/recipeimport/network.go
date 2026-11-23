package recipeimport

import (
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringnetwork"
)

func HydrateNetwork(d ws.Document, pinned recipe.Output, catalog ws.CatalogSnapshot) (authoringnetwork.AuthoringNetwork, error) {
	if d.ImportedRecipe == nil {
		return authoringnetwork.NetworkForDocument(d, catalog)
	}
	if pinned.Name != d.ImportedRecipe.Output {
		return authoringnetwork.AuthoringNetwork{}, fmt.Errorf("network source output does not match imported pin")
	}
	if err := ValidateRelationshipOverlay(pinned, d.ImportedSource, catalog); err != nil {
		return authoringnetwork.AuthoringNetwork{}, err
	}
	effective, err := ApplySourcePlan(pinned, d.ImportedSource)
	if err != nil {
		return authoringnetwork.AuthoringNetwork{}, err
	}
	effective = freezeOccurrences(effective)
	route, err := ImportRoute(effective, catalog)
	if err != nil {
		return authoringnetwork.AuthoringNetwork{}, err
	}
	n := authoringnetwork.NewAuthoringNetwork(d, "IMPORTED_SOURCE")
	n.SourceRows = ImportRows(effective)
	stepIDs := map[string]string{ws.RootOccurrenceID: ""}
	original := freezeOccurrences(pinned)
	var index func([]recipe.Traversal, string)
	index = func(items []recipe.Traversal, parent string) {
		for i, t := range items {
			path := sourceIndexPath(parent, "traversals", i)
			stepIDs[t.OccurrenceID] = path
			index(t.Traversals, path)
		}
	}
	index(original.Traversals, "")
	if d.ImportedSource != nil && d.ImportedSource.Relationships != nil {
		for _, a := range d.ImportedSource.Relationships.Additions {
			stepIDs[a.OccurrenceID] = "relationships." + a.OccurrenceID
		}
	}
	edges := map[string]ws.CatalogEdge{}
	for _, e := range catalog.Edges {
		edges[e.ID] = e
	}
	var walk func(ws.RouteNode, string, bool)
	walk = func(r ws.RouteNode, parent string, requiredPrefix bool) {
		e := edges[r.CatalogEdgeID]
		n.Occurrences = append(n.Occurrences, authoringnetwork.NetworkOccurrence{OccurrenceID: r.OccurrenceID, ParentOccurrenceID: parent, SourceStepID: stepIDs[r.OccurrenceID], ResourceType: r.ResourceType, Label: r.ResourceType, CatalogEdgeID: r.CatalogEdgeID, Relationship: r.Relationship, StorageDirection: e.StorageDirection, MatchMode: r.MatchMode.Normalized(), Enabled: true, PreservesRowIdentity: true, CanExcludeRows: parent != "" && requiredPrefix && r.MatchMode == ws.RouteMatchRequired})
		for _, c := range r.Children {
			walk(c, r.OccurrenceID, requiredPrefix && (parent == "" || r.MatchMode == ws.RouteMatchRequired))
		}
	}
	walk(route, "", true)

	bindings := map[string]authoringnetwork.NetworkColumnBinding{}
	var bind func([]recipe.Field, []recipe.Traversal, string)
	bind = func(fields []recipe.Field, items []recipe.Traversal, occurrence string) {
		for _, f := range fields {
			if f.ColumnID != "" {
				bindings[f.ColumnID] = authoringnetwork.NetworkColumnBinding{ColumnID: f.ColumnID, OccurrenceID: occurrence, SourceStepID: sourcePath(stepIDs[occurrence], "fields"), ValueMode: string(f.ValueMode.Normalized()), Status: "EXACT"}
			}
		}
		for _, t := range items {
			bind(t.Fields, t.Traversals, t.OccurrenceID)
		}
	}
	bind(effective.Fields, effective.Traversals, ws.RootOccurrenceID)
	for _, c := range d.Columns {
		b, ok := bindings[c.ColumnID]
		if !ok {
			b = authoringnetwork.NetworkColumnBinding{ColumnID: c.ColumnID, Status: "UNKNOWN"}
		}
		n.Bindings = append(n.Bindings, b)
	}
	return n, nil
}

func freezeOccurrences(output recipe.Output) recipe.Output {

	clone, err := cloneOutput(output)
	if err != nil {
		return output
	}
	if clone.RootOccurrenceID == "" {
		clone.RootOccurrenceID = ws.RootOccurrenceID
	}
	var walk func([]recipe.Traversal, string)
	walk = func(items []recipe.Traversal, path string) {
		for i := range items {
			if items[i].OccurrenceID == "" {
				items[i].OccurrenceID = stableOccurrence(path, i, items[i].Name)
			}
			walk(items[i].Traversals, fmt.Sprintf("%s[%d].traversals", path, i))
		}
	}
	walk(clone.Traversals, "traversals")
	return clone
}

func ValidateRelationshipOverlay(output recipe.Output, plan *ws.ImportedSourcePlan, catalog ws.CatalogSnapshot) error {
	if plan == nil || plan.Relationships == nil {
		return nil
	}
	if err := plan.Relationships.Validate(); err != nil {
		return err
	}
	route, err := ImportRoute(output, catalog)
	if err != nil {
		return err
	}
	nodes := map[string]string{}
	edges := map[string]ws.CatalogEdge{}
	rootNodeID := ""
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node.ResourceType
		if node.ResourceType == output.RootResourceType {
			if rootNodeID != "" {
				return fmt.Errorf("SOURCE_PARENT_AMBIGUOUS: pinned root %q has multiple catalog nodes", output.RootResourceType)
			}
			rootNodeID = node.ID
		}
	}
	if rootNodeID == "" {
		return fmt.Errorf("SOURCE_PARENT_UNAVAILABLE: pinned root %q has no catalog node", output.RootResourceType)
	}
	for _, edge := range catalog.Edges {
		edges[edge.ID] = edge
	}
	parents := map[string]string{}
	routeEdges := map[string]string{}
	types := map[string]string{}
	nodeForOccurrence := map[string]string{}
	var indexRoute func(ws.RouteNode, string, string) error
	indexRoute = func(r ws.RouteNode, parent, nodeID string) error {
		parents[r.OccurrenceID] = parent
		routeEdges[r.OccurrenceID] = r.CatalogEdgeID
		types[r.OccurrenceID] = r.ResourceType
		nodeForOccurrence[r.OccurrenceID] = nodeID
		for _, child := range r.Children {
			childNodeID := ""
			if child.CatalogEdgeID != "" && nodeID != "" {
				edge, ok := edges[child.CatalogEdgeID]
				if !ok || edge.FromNodeID != nodeID || nodes[edge.ToNodeID] != child.ResourceType {
					return fmt.Errorf("SOURCE_PARENT_MISMATCH: pinned relationship %q does not extend its exact parent catalog node", child.OccurrenceID)
				}
				childNodeID = edge.ToNodeID
			}
			if err := indexRoute(child, r.OccurrenceID, childNodeID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := indexRoute(route, "", rootNodeID); err != nil {
		return err
	}
	candidates := map[string]ws.CatalogCandidate{}
	for _, c := range catalog.Candidates {
		candidates[c.ID] = c
	}
	for _, a := range plan.Relationships.Additions {
		parent, ok := types[a.ParentOccurrenceID]
		if !ok {
			return fmt.Errorf("relationship %q parent %q is unavailable or not earlier in the source overlay", a.OccurrenceID, a.ParentOccurrenceID)
		}
		e, ok := edges[a.CatalogEdgeID]
		parentNodeID := nodeForOccurrence[a.ParentOccurrenceID]
		if parentNodeID == "" {
			return fmt.Errorf("SOURCE_PARENT_UNAVAILABLE: relationship %q parent %q has no exact catalog node", a.OccurrenceID, a.ParentOccurrenceID)
		}
		if !ok || e.FromNodeID != parentNodeID || nodes[e.FromNodeID] != parent || nodes[e.ToNodeID] != a.ResourceType || e.Label != a.Relationship || e.StorageDirection != a.StorageDirection {
			return fmt.Errorf("relationship %q does not match its authorized catalog edge", a.OccurrenceID)
		}
		if _, exists := types[a.OccurrenceID]; exists {
			return fmt.Errorf("relationship %q duplicates an existing occurrence", a.OccurrenceID)
		}
		depth := 1
		for ancestor := a.ParentOccurrenceID; ancestor != ""; ancestor = parents[ancestor] {
			if types[ancestor] == a.ResourceType {
				return fmt.Errorf("relationship %q would create a cyclic resource path", a.OccurrenceID)
			}
			if !catalog.RoutePolicy.AllowRepeatedEdges && routeEdges[ancestor] == a.CatalogEdgeID {
				return fmt.Errorf("relationship %q repeats an edge on its source path", a.OccurrenceID)
			}
			if ancestor != ws.RootOccurrenceID {
				depth++
			}
		}
		if catalog.RoutePolicy.MaxHops != nil && depth > *catalog.RoutePolicy.MaxHops {
			return fmt.Errorf("ROUTE_TOO_LONG: relationship %q exceeds catalog maxSteps", a.OccurrenceID)
		}
		if !catalog.RoutePolicy.AllowSelfLoops && e.FromNodeID == e.ToNodeID {
			return fmt.Errorf("relationship %q is a disallowed self loop", a.OccurrenceID)
		}
		parents[a.OccurrenceID] = a.ParentOccurrenceID
		routeEdges[a.OccurrenceID] = a.CatalogEdgeID

		if len(matchingEdges(catalog, parent, a.Relationship, a.ResourceType)) != 1 {
			return fmt.Errorf("relationship %q is ambiguous in the current catalog", a.OccurrenceID)
		}
		for _, f := range a.Fields {
			c, ok := candidates[f.CandidateID]
			if !ok || c.NodeID != e.ToNodeID || c.FieldPath != f.FieldPath || c.Label != f.Label {
				return fmt.Errorf("relationship field %q does not match its authorized catalog candidate", f.ColumnID)
			}
			supported := false
			for _, m := range c.ProjectionModes {
				if m == f.ValueMode {
					supported = true
				}
			}

			if !supported && !c.Repeated && c.LogicalType != "object" {
				for _, m := range c.ProjectionModes {
					if m == "VALUE" {
						supported = true
					}
				}
			}
			if !supported {
				return fmt.Errorf("relationship field %q does not support %s", f.ColumnID, f.ValueMode)
			}
		}
		types[a.OccurrenceID] = a.ResourceType
		nodeForOccurrence[a.OccurrenceID] = e.ToNodeID
	}
	return nil
}

func indexTraversalOccurrences(items []recipe.Traversal) map[string]*recipe.Traversal {
	index := map[string]*recipe.Traversal{}
	var visit func([]recipe.Traversal)
	visit = func(items []recipe.Traversal) {
		for i := range items {
			traversal := &items[i]
			index[traversal.OccurrenceID] = traversal
			visit(traversal.Traversals)
		}
	}
	visit(items)
	return index
}

func applyRelationshipOverlay(output recipe.Output, overlay *ws.ImportedRelationshipOverlay) (recipe.Output, error) {
	if overlay == nil {
		return output, nil
	}
	if err := overlay.Validate(); err != nil {
		return recipe.Output{}, err
	}

	removed := map[string]bool{}
	for _, id := range overlay.Removals {
		removed[id] = true
	}
	matches := map[string]ws.RouteMatchMode{}
	for _, m := range overlay.MatchOverrides {
		matches[m.OccurrenceID] = m.MatchMode
	}
	occurrences := indexTraversalOccurrences(output.Traversals)
	for _, a := range overlay.Additions {
		if occurrences[a.OccurrenceID] != nil || a.OccurrenceID == ws.RootOccurrenceID {
			return recipe.Output{}, fmt.Errorf("relationship addition %q collides with an existing occurrence", a.OccurrenceID)
		}
		parent := &output.Traversals
		if a.ParentOccurrenceID != ws.RootOccurrenceID {
			traversal := occurrences[a.ParentOccurrenceID]
			if traversal == nil || removed[a.ParentOccurrenceID] {
				return recipe.Output{}, fmt.Errorf("relationship parent %q is unavailable", a.ParentOccurrenceID)
			}
			parent = &traversal.Traversals
		}

		t := recipe.Traversal{Name: a.Relationship, Alias: a.OccurrenceID, OccurrenceID: a.OccurrenceID, ToResourceType: a.ResourceType, MatchMode: recipe.TraversalMatchMode(a.MatchMode)}
		for _, f := range a.Fields {
			path := strings.TrimPrefix(f.FieldPath, a.ResourceType+".")
			if strings.TrimSpace(path) == "" || strings.ContainsAny(path, " (){};\n\r") {
				return recipe.Output{}, fmt.Errorf("relationship field %q has unsupported selector", f.ColumnID)
			}
			t.Fields = append(t.Fields, recipe.Field{Name: f.Column, ColumnID: f.ColumnID, Label: f.Label, FieldRef: f.FieldPath, ValueMode: recipe.ValueMode(f.ValueMode), Expr: recipe.Expression{Select: a.OccurrenceID + "." + path}})
		}
		*parent = append(*parent, t)
		occurrences = indexTraversalOccurrences(output.Traversals)
	}
	for id := range removed {
		if id != ws.RootOccurrenceID && occurrences[id] == nil {
			return recipe.Output{}, fmt.Errorf("removed relationship %q is unavailable", id)
		}
	}
	for id := range matches {
		if id != ws.RootOccurrenceID && occurrences[id] == nil {
			return recipe.Output{}, fmt.Errorf("match override relationship %q is unavailable", id)
		}
	}
	var prune func([]recipe.Traversal) []recipe.Traversal
	prune = func(items []recipe.Traversal) []recipe.Traversal {
		result := make([]recipe.Traversal, 0, len(items))
		for _, t := range items {
			if removed[t.OccurrenceID] {
				continue
			}
			if mode, ok := matches[t.OccurrenceID]; ok {
				t.MatchMode = recipe.TraversalMatchMode(mode)
			}
			t.Traversals = prune(t.Traversals)
			result = append(result, t)
		}
		return result
	}
	output.Traversals = prune(output.Traversals)
	return output, nil
}
