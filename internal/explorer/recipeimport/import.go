// Package recipeimport converts one immutable recipe output into the existing
// graphical Explorer Builder document protocol.
package recipeimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// ImportWorkspace creates one Builder document per recipe output. Schemas are
// supplied by the server after runtime resolution; no recipe AST enters the
// authoring workspace.
func ImportWorkspace(bundle recipe.Bundle, catalog authoringv2.CatalogSnapshot, outputSchemas map[string][]recipe.StageColumn) (authoringv2.Workspace, error) {
	digest, err := bundle.Digest()
	if err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("digest imported recipe: %w", err)
	}
	if strings.TrimSpace(bundle.Name) == "" || strings.TrimSpace(bundle.TranslationVersion) == "" {
		return authoringv2.Workspace{}, fmt.Errorf("imported recipe name and translationVersion are required")
	}
	if len(bundle.Outputs) == 0 {
		return authoringv2.Workspace{}, fmt.Errorf("imported recipe has no outputs")
	}
	workspace := authoringv2.Workspace{
		APIVersion:       authoringv2.APIVersion,
		Kind:             authoringv2.WorkspaceKind,
		SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer:         authoringv2.ExplorerMetadata{Title: bundle.Name},
		Documents:        make([]authoringv2.Document, 0, len(bundle.Outputs)),
		Tabs:             make([]authoringv2.Tab, 0, len(bundle.Outputs)),
	}
	usedIDs := make(map[string]bool, len(bundle.Outputs))
	for outputIndex, output := range bundle.Outputs {
		if strings.TrimSpace(output.Name) == "" || strings.TrimSpace(output.RootResourceType) == "" {
			return authoringv2.Workspace{}, fmt.Errorf("outputs[%d] requires name and rootResourceType", outputIndex)
		}
		schema, ok := outputSchemas[output.Name]
		if !ok {
			return authoringv2.Workspace{}, fmt.Errorf("resolved schema for output %q is missing", output.Name)
		}
		outputID := output.Name
		if !validBuilderID(outputID) {
			return authoringv2.Workspace{}, fmt.Errorf("output name %q cannot be represented as a Builder output id", output.Name)
		}
		if usedIDs[outputID] {
			return authoringv2.Workspace{}, fmt.Errorf("recipe outputs collide on Builder output id %q", outputID)
		}
		usedIDs[outputID] = true
		route, err := routeForOutput(output, catalog)
		if err != nil {
			return authoringv2.Workspace{}, fmt.Errorf("output %q route: %w", output.Name, err)
		}
		columns := make([]authoringv2.Column, 0, len(schema))
		seenIDs, seenNames := map[string]bool{}, map[string]bool{}
		for columnIndex, stageColumn := range schema {
			id := strings.TrimSpace(stageColumn.ID)
			name := strings.TrimSpace(stageColumn.Name)
			if id == "" || name == "" {
				return authoringv2.Workspace{}, fmt.Errorf("output %q schema[%d] requires stable id and name", output.Name, columnIndex)
			}
			if seenIDs[id] || seenNames[name] {
				return authoringv2.Workspace{}, fmt.Errorf("output %q schema has duplicate column id or name", output.Name)
			}
			seenIDs[id], seenNames[name] = true, true
			label := stageColumn.Label
			if strings.TrimSpace(label) == "" {
				label = name
			}
			visible := true
			columns = append(columns, authoringv2.Column{
				ColumnID: id, Column: name, Label: label, LogicalType: stageColumn.Type,
				OccurrenceID: authoringv2.RootOccurrenceID,
				Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: name}},
				Table:        &authoringv2.TablePresentation{Visible: &visible},
			})
		}
		rows := importedRows(output)
		sourcePlan := SourcePlan(output)
		document := authoringv2.Document{
			Kind:             authoringv2.Kind,
			Output:           authoringv2.Output{ID: outputID, Title: output.Name},
			RootResourceType: output.RootResourceType,
			Route:            route,
			Rows:             rows,
			Columns:          columns,
			ImportedRecipe:   &authoringv2.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name},
			ImportedSource:   &sourcePlan,
		}
		workspace.Documents = append(workspace.Documents, document)
		tabID := "tab_" + outputID
		workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: tabID, Title: output.Name, OutputID: outputID, Order: outputIndex, Visible: true})
	}
	if err := workspace.Validate(); err != nil {
		return authoringv2.Workspace{}, fmt.Errorf("validate imported workspace: %w", err)
	}
	return workspace, nil
}

func importedRows(output recipe.Output) authoringv2.RowDefinition {
	if output.Expand == nil {
		return authoringv2.RecordsRowDefinition()
	}
	path := strings.TrimSpace(output.Expand.From.Select)
	if strings.HasPrefix(path, "root.") {
		path = strings.TrimPrefix(path, "root.")
	}
	occurrence := strings.TrimSpace(output.Expand.OwnerOccurrenceID)
	if occurrence == "" {
		occurrence = authoringv2.RootOccurrenceID
	}
	policy := authoringv2.EmptyCollectionExclude
	switch output.Expand.EmptyPolicy.Normalized() {
	case recipe.ExpansionError:
		policy = authoringv2.EmptyCollectionError
	case recipe.ExpansionPreserveParent:
		policy = authoringv2.EmptyCollectionPreserveParent
	}
	return authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{OccurrenceID: occurrence, ScopePath: path, EmptyCollectionPolicy: policy}}
}

// ImportRows returns the row definition represented by the immutable output.
func ImportRows(output recipe.Output) authoringv2.RowDefinition {
	return importedRows(output)
}

func routeForOutput(output recipe.Output, catalog authoringv2.CatalogSnapshot) (authoringv2.RouteNode, error) {
	root := authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: output.RootResourceType}
	var add func(parent *authoringv2.RouteNode, traversals []recipe.Traversal, path string) error
	add = func(parent *authoringv2.RouteNode, traversals []recipe.Traversal, path string) error {
		for index, traversal := range traversals {
			if strings.TrimSpace(traversal.Name) == "" || strings.TrimSpace(traversal.ToResourceType) == "" {
				return fmt.Errorf("%s[%d] requires name and toResourceType", path, index)
			}
			edges := matchingEdges(catalog, parent.ResourceType, traversal.Name, traversal.ToResourceType)
			if len(edges) > 1 {
				return fmt.Errorf("%s[%d] (%s -> %s via %s) matches %d catalog edges; exactly one is required", path, index, parent.ResourceType, traversal.ToResourceType, traversal.Name, len(edges))
			}
			if len(edges) == 0 && traversal.MatchMode.Normalized() != recipe.MatchOptional {
				return fmt.Errorf("%s[%d] (%s -> %s via %s) has no catalog edge and is not optional", path, index, parent.ResourceType, traversal.ToResourceType, traversal.Name)
			}
			occurrenceID := strings.TrimSpace(traversal.OccurrenceID)
			if occurrenceID == "" {
				occurrenceID = stableOccurrence(path, index, traversal.Name)
			}
			child := authoringv2.RouteNode{OccurrenceID: occurrenceID, ResourceType: traversal.ToResourceType, MatchMode: authoringv2.RouteMatchMode(traversal.MatchMode.Normalized())}
			if len(edges) == 1 {
				child.CatalogEdgeID = edges[0].ID
				child.Relationship = edges[0].Label
			} else {
				// The pinned source declares this optional traversal, but current
				// data has no observed edge. Retain the source route label without
				// manufacturing a catalog edge ID.
				child.Relationship = traversal.Name
			}
			if err := add(&child, traversal.Traversals, path+"["+fmt.Sprint(index)+"].traversals"); err != nil {
				return err
			}
			parent.Children = append(parent.Children, child)
		}
		return nil
	}
	if err := add(&root, output.Traversals, "traversals"); err != nil {
		return authoringv2.RouteNode{}, err
	}
	return root, nil
}

// ImportRoute converts an output's traversal graph into the current Builder
// catalog route. It is also used by compilation to reject edited imported
// routes against the exact recipe source.
func ImportRoute(output recipe.Output, catalog authoringv2.CatalogSnapshot) (authoringv2.RouteNode, error) {
	return routeForOutput(output, catalog)
}

func matchingEdges(catalog authoringv2.CatalogSnapshot, fromResource, relationship, toResource string) []authoringv2.CatalogEdge {
	nodes := make(map[string]string, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node.ResourceType
	}
	var matches []authoringv2.CatalogEdge
	for _, edge := range catalog.Edges {
		nameMatches := relationship == edge.Label || relationship == edge.Label+"_"+toResource
		if nodes[edge.FromNodeID] == fromResource && nodes[edge.ToNodeID] == toResource && nameMatches {
			matches = append(matches, edge)
		}
	}
	return matches
}

func validBuilderID(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for index := 1; index < len(value); index++ {
		c := value[index]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func stableOccurrence(path string, index int, name string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", path, index, name)))
	return "import_" + hex.EncodeToString(sum[:6])
}
