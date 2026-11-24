package recipeimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ImportWorkspace(bundle recipe.Bundle, catalog ws.CatalogSnapshot, outputSchemas map[string][]recipe.StageColumn) (ws.Workspace, error) {
	digest, err := bundle.Digest()
	if err != nil {
		return ws.Workspace{}, fmt.Errorf("digest imported recipe: %w", err)
	}
	if strings.TrimSpace(bundle.Name) == "" || strings.TrimSpace(bundle.TranslationVersion) == "" {
		return ws.Workspace{}, fmt.Errorf("imported recipe name and translationVersion are required")
	}
	if len(bundle.Outputs) == 0 {
		return ws.Workspace{}, fmt.Errorf("imported recipe has no outputs")
	}
	workspace := ws.Workspace{
		APIVersion:       ws.APIVersion,
		Kind:             ws.WorkspaceKind,
		SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer:         ws.ExplorerMetadata{Title: bundle.Name},
		Documents:        make([]ws.Document, 0, len(bundle.Outputs)),
		Tabs:             make([]ws.Tab, 0, len(bundle.Outputs)),
	}
	usedIDs := make(map[string]bool, len(bundle.Outputs))
	for outputIndex, output := range bundle.Outputs {
		if strings.TrimSpace(output.Name) == "" || strings.TrimSpace(output.RootResourceType) == "" {
			return ws.Workspace{}, fmt.Errorf("outputs[%d] requires name and rootResourceType", outputIndex)
		}
		schema, ok := outputSchemas[output.Name]
		if !ok {
			return ws.Workspace{}, fmt.Errorf("resolved schema for output %q is missing", output.Name)
		}
		outputID := output.Name
		if !validBuilderID(outputID) {
			return ws.Workspace{}, fmt.Errorf("output name %q cannot be represented as a Builder output id", output.Name)
		}
		if usedIDs[outputID] {
			return ws.Workspace{}, fmt.Errorf("recipe outputs collide on Builder output id %q", outputID)
		}
		usedIDs[outputID] = true
		route, err := routeForOutput(output, catalog)
		if err != nil {
			return ws.Workspace{}, fmt.Errorf("output %q route: %w", output.Name, err)
		}
		columns := make([]ws.Column, 0, len(schema))
		seenIDs, seenNames := map[string]bool{}, map[string]bool{}
		for columnIndex, stageColumn := range schema {
			id := strings.TrimSpace(stageColumn.ID)
			name := strings.TrimSpace(stageColumn.Name)
			if id == "" || name == "" {
				return ws.Workspace{}, fmt.Errorf("output %q schema[%d] requires stable id and name", output.Name, columnIndex)
			}
			if seenIDs[id] || seenNames[name] {
				return ws.Workspace{}, fmt.Errorf("output %q schema has duplicate column id or name", output.Name)
			}
			seenIDs[id], seenNames[name] = true, true
			label := stageColumn.Label
			if strings.TrimSpace(label) == "" {
				label = name
			}
			visible := true
			columns = append(columns, ws.Column{
				ColumnID: id, Column: name, Label: label, LogicalType: stageColumn.Type,
				OccurrenceID: ws.RootOccurrenceID,
				Source:       ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: name}},
				Table:        &ws.TablePresentation{Visible: &visible},
			})
		}
		rows := importedRows(output)
		sourcePlan := SourcePlan(output)
		document := ws.Document{
			Kind:             ws.Kind,
			Output:           ws.Output{ID: outputID, Title: output.Name},
			RootResourceType: output.RootResourceType,
			Route:            route,
			Rows:             rows,
			Columns:          columns,
			ImportedRecipe:   &ws.ImportedRecipeRef{Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Digest: digest, Output: output.Name},
			ImportedSource:   &sourcePlan,
		}
		workspace.Documents = append(workspace.Documents, document)
		tabID := "tab_" + outputID
		workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: tabID, Title: output.Name, OutputID: outputID, Order: outputIndex, Visible: true})
	}
	if err := workspace.Validate(); err != nil {
		return ws.Workspace{}, fmt.Errorf("validate imported workspace: %w", err)
	}
	return workspace, nil
}

func importedRows(output recipe.Output) ws.RowDefinition {
	if output.GroupRows != nil {
		source := output.GroupRows
		rows := &ws.GroupedRows{Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{RevisionID: source.RevisionID, UnassignedMemberPolicy: ws.UnassignedMemberPolicy(source.UnassignedMemberPolicy)}}, AfterStepID: source.AfterStepID}
		for _, value := range source.RowValues {
			rows.RowValues = append(rows.RowValues, ws.ExplicitGroupRowValue{ColumnID: value.ColumnID, Policy: ws.ConstructionRowValuePolicy(value.Policy)})
		}
		return ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: rows}
	}
	if output.Expand == nil {
		return ws.RecordsRowDefinition()
	}
	path := strings.TrimSpace(output.Expand.From.Select)
	if strings.HasPrefix(path, "root.") {
		path = strings.TrimPrefix(path, "root.")
	}
	occurrence := strings.TrimSpace(output.Expand.OwnerOccurrenceID)
	if occurrence == "" {
		occurrence = ws.RootOccurrenceID
	}
	policy := ws.EmptyCollectionExclude
	switch output.Expand.EmptyPolicy.Normalized() {
	case recipe.ExpansionError:
		policy = ws.EmptyCollectionError
	case recipe.ExpansionPreserveParent:
		policy = ws.EmptyCollectionPreserveParent
	}
	return ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{OccurrenceID: occurrence, ScopePath: path, EmptyCollectionPolicy: policy}}
}

func ImportRows(output recipe.Output) ws.RowDefinition {
	return importedRows(output)
}

func routeForOutput(output recipe.Output, catalog ws.CatalogSnapshot) (ws.RouteNode, error) {
	root := ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: output.RootResourceType}
	var add func(parent *ws.RouteNode, traversals []recipe.Traversal, path string) error
	add = func(parent *ws.RouteNode, traversals []recipe.Traversal, path string) error {
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
			child := ws.RouteNode{OccurrenceID: occurrenceID, ResourceType: traversal.ToResourceType, MatchMode: ws.RouteMatchMode(traversal.MatchMode.Normalized())}
			if len(edges) == 1 {
				child.CatalogEdgeID = edges[0].ID
				child.Relationship = edges[0].Label
			} else {

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
		return ws.RouteNode{}, err
	}
	return root, nil
}

func ImportRoute(output recipe.Output, catalog ws.CatalogSnapshot) (ws.RouteNode, error) {
	return routeForOutput(output, catalog)
}

func matchingEdges(catalog ws.CatalogSnapshot, fromResource, relationship, toResource string) []ws.CatalogEdge {
	nodes := make(map[string]string, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node.ResourceType
	}
	var matches []ws.CatalogEdge
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
