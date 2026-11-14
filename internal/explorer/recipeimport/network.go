package recipeimport

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// HydrateNetwork reads the exact pinned output, then derives its effective
// network from the project-local source plan. It does not reinterpret the
// projected document route as imported authoring intent.
func HydrateNetwork(d authoringv2.Document, pinned recipe.Output, catalog authoringv2.CatalogSnapshot) (authoringv2.AuthoringNetwork, error) {
	if d.ImportedRecipe == nil {
		return authoringv2.NetworkForDocument(d, catalog)
	}
	if pinned.Name != d.ImportedRecipe.Output {
		return authoringv2.AuthoringNetwork{}, fmt.Errorf("network source output does not match imported pin")
	}
	if err := ValidateRelationshipOverlay(pinned, d.ImportedSource, catalog); err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	effective, err := ApplySourcePlan(pinned, d.ImportedSource)
	if err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	effective = freezeOccurrences(effective)
	route, err := ImportRoute(effective, catalog)
	if err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	n := authoringv2.NewAuthoringNetwork(d, "IMPORTED_SOURCE")
	n.SourceRows = ImportRows(effective)
	stepIDs := map[string]string{authoringv2.RootOccurrenceID: ""}
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
	edges := map[string]authoringv2.CatalogEdge{}
	for _, e := range catalog.Edges {
		edges[e.ID] = e
	}
	var walk func(authoringv2.RouteNode, string, bool)
	walk = func(r authoringv2.RouteNode, parent string, requiredPrefix bool) {
		e := edges[r.CatalogEdgeID]
		n.Occurrences = append(n.Occurrences, authoringv2.NetworkOccurrence{OccurrenceID: r.OccurrenceID, ParentOccurrenceID: parent, SourceStepID: stepIDs[r.OccurrenceID], ResourceType: r.ResourceType, Label: r.ResourceType, CatalogEdgeID: r.CatalogEdgeID, Relationship: r.Relationship, StorageDirection: e.StorageDirection, MatchMode: r.MatchMode.Normalized(), Enabled: true, PreservesRowIdentity: true, CanExcludeRows: parent != "" && requiredPrefix && r.MatchMode == authoringv2.RouteMatchRequired})
		for _, c := range r.Children {
			walk(c, r.OccurrenceID, requiredPrefix && (parent == "" || r.MatchMode == authoringv2.RouteMatchRequired))
		}
	}
	walk(route, "", true)
	// Only stable structural field IDs establish exact ownership. Old resolved
	// schemas without source provenance remain UNKNOWN; never guess from titles,
	// output names, or the historical base occurrence on imported columns.
	bindings := map[string]authoringv2.NetworkColumnBinding{}
	var bind func([]recipe.Field, []recipe.Traversal, string)
	bind = func(fields []recipe.Field, items []recipe.Traversal, occurrence string) {
		for _, f := range fields {
			if f.ColumnID != "" {
				bindings[f.ColumnID] = authoringv2.NetworkColumnBinding{ColumnID: f.ColumnID, OccurrenceID: occurrence, SourceStepID: sourcePath(stepIDs[occurrence], "fields"), ValueMode: string(f.ValueMode.Normalized()), Status: "EXACT"}
			}
		}
		for _, t := range items {
			bind(t.Fields, t.Traversals, t.OccurrenceID)
		}
	}
	bind(effective.Fields, effective.Traversals, authoringv2.RootOccurrenceID)
	for _, c := range d.Columns {
		b, ok := bindings[c.ColumnID]
		if !ok {
			b = authoringv2.NetworkColumnBinding{ColumnID: c.ColumnID, Status: "UNKNOWN"}
		}
		n.Bindings = append(n.Bindings, b)
	}
	return n, nil
}

// freezeOccurrences assigns the historical occurrence mapping before edits can
// prune positional recipe arrays. It clones first and never mutates the pin.
func freezeOccurrences(output recipe.Output) recipe.Output {
	// Callers have already validated that recipe expressions marshal correctly.
	clone, err := cloneOutput(output)
	if err != nil {
		return output
	}
	if clone.RootOccurrenceID == "" {
		clone.RootOccurrenceID = authoringv2.RootOccurrenceID
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

// ValidateRelationshipOverlay reauthorizes the persisted project-local overlay
// against this exact catalog snapshot. The caller owns project/generation and
// draft checks. Catalog identity is never reconstructed from a label.
func ValidateRelationshipOverlay(output recipe.Output, plan *authoringv2.ImportedSourcePlan, catalog authoringv2.CatalogSnapshot) error {
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
	edges := map[string]authoringv2.CatalogEdge{}
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
	var indexRoute func(authoringv2.RouteNode, string, string) error
	indexRoute = func(r authoringv2.RouteNode, parent, nodeID string) error {
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
	candidates := map[string]authoringv2.CatalogCandidate{}
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
			if ancestor != authoringv2.RootOccurrenceID {
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
		// The legacy traversal compiler resolves by label and target. Until it can
		// consume an exact edge ID, ambiguous parallel edges cannot be authored.
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
			// Scalar VALUE describes each record. ALL/FIRST/DISTINCT here
			// reduce matching records, not repeated items within one record.
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

func applyRelationshipOverlay(output recipe.Output, overlay *authoringv2.ImportedRelationshipOverlay) (recipe.Output, error) {
	if overlay == nil {
		return output, nil
	}
	if err := overlay.Validate(); err != nil {
		return recipe.Output{}, err
	}
	// Input has already been cloned and frozen before source-step pruning.
	removed := map[string]bool{}
	for _, id := range overlay.Removals {
		removed[id] = true
	}
	matches := map[string]authoringv2.RouteMatchMode{}
	for _, m := range overlay.MatchOverrides {
		matches[m.OccurrenceID] = m.MatchMode
	}
	scopes := map[string]*[]recipe.Traversal{authoringv2.RootOccurrenceID: &output.Traversals}
	found := map[string]bool{authoringv2.RootOccurrenceID: true}
	var index func([]recipe.Traversal)
	index = func(items []recipe.Traversal) {
		for i := range items {
			t := &items[i]
			found[t.OccurrenceID] = true
			scopes[t.OccurrenceID] = &t.Traversals
			index(t.Traversals)
		}
	}
	index(output.Traversals)
	for _, a := range overlay.Additions {
		if found[a.OccurrenceID] {
			return recipe.Output{}, fmt.Errorf("relationship addition %q collides with an existing occurrence", a.OccurrenceID)
		}
		parent, ok := scopes[a.ParentOccurrenceID]
		if !ok || removed[a.ParentOccurrenceID] {
			return recipe.Output{}, fmt.Errorf("relationship parent %q is unavailable", a.ParentOccurrenceID)
		}
		// Pointers into slices can move after append. Reindex the complete tree
		// after every addition; later additions can safely address an earlier one.
		t := recipe.Traversal{Name: a.Relationship, Alias: a.OccurrenceID, OccurrenceID: a.OccurrenceID, ToResourceType: a.ResourceType, MatchMode: recipe.TraversalMatchMode(a.MatchMode)}
		for _, f := range a.Fields {
			path := strings.TrimPrefix(f.FieldPath, a.ResourceType+".")
			if strings.TrimSpace(path) == "" || strings.ContainsAny(path, " (){};\n\r") {
				return recipe.Output{}, fmt.Errorf("relationship field %q has unsupported selector", f.ColumnID)
			}
			t.Fields = append(t.Fields, recipe.Field{Name: f.Column, ColumnID: f.ColumnID, Label: f.Label, FieldRef: f.FieldPath, ValueMode: recipe.ValueMode(f.ValueMode), Expr: recipe.Expression{Select: a.OccurrenceID + "." + path}})
		}
		*parent = append(*parent, t)
		found[a.OccurrenceID] = true
		scopes = map[string]*[]recipe.Traversal{authoringv2.RootOccurrenceID: &output.Traversals}
		index(output.Traversals)
	}
	for id := range removed {
		if !found[id] {
			return recipe.Output{}, fmt.Errorf("removed relationship %q is unavailable", id)
		}
	}
	for id := range matches {
		if !found[id] {
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
