package recipeimport

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestImportDefaultDataframerKeepsExactPinAndUsesResolvedSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/default-dataframer.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundle recipe.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode default recipe: %v", err)
	}
	var group recipe.Output
	for _, output := range bundle.Outputs {
		if output.Name == "GroupMember" {
			group = output
			break
		}
	}
	if group.Name == "" {
		t.Fatal("fixture has no GroupMember output")
	}
	stage := []recipe.StageColumn{{ID: "stable_group_id", Name: "group_id", Label: "Group", Type: "string"}, {ID: "stable_member_id", Name: "member_id", Type: "string"}, {ID: "dynamic_abc", Name: "dynamic_identifier_abc", Type: "string"}}
	schemas := make(map[string][]recipe.StageColumn, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		schemas[output.Name] = []recipe.StageColumn{{ID: "stable_" + output.Name, Name: "id", Type: "string"}}
	}
	schemas["GroupMember"] = stage
	catalog := catalogForBundle(bundle)
	workspace, err := ImportWorkspace(bundle, catalog, schemas)
	if err != nil {
		t.Fatalf("ImportWorkspace: %v", err)
	}
	canonical, encodeErr := workspace.CanonicalJSON()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	restored, decodeErr := authoringv2.DecodeWorkspace(canonical)
	if decodeErr != nil {
		t.Fatalf("decode imported workspace: %v", decodeErr)
	}
	for index, doc := range restored.Documents {
		if doc.ImportedRecipe == nil || *doc.ImportedRecipe != *workspace.Documents[index].ImportedRecipe {
			t.Fatalf("source pin lost at output %d", index)
		}
		if len(doc.Columns) != len(workspace.Documents[index].Columns) {
			t.Fatalf("source schema lost at output %d", index)
		}
	}
	if len(workspace.Documents) != len(bundle.Outputs) {
		t.Fatalf("documents = %d, want %d", len(workspace.Documents), len(bundle.Outputs))
	}
	var document authoringv2.Document
	for _, candidate := range workspace.Documents {
		if candidate.ImportedRecipe.Output == "GroupMember" {
			document = candidate
			break
		}
	}
	if document.ImportedRecipe == nil {
		t.Fatal("GroupMember document not imported")
	}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if document.ImportedRecipe == nil || document.ImportedRecipe.Name != bundle.Name || document.ImportedRecipe.TranslationVersion != bundle.TranslationVersion || document.ImportedRecipe.Digest != digest || document.ImportedRecipe.Output != "GroupMember" {
		t.Fatalf("import pin = %#v", document.ImportedRecipe)
	}
	if len(document.Columns) != len(stage) {
		t.Fatalf("columns = %d, want %d", len(document.Columns), len(stage))
	}
	for i, column := range document.Columns {
		if column.ColumnID != stage[i].ID || column.Source.Kind != authoringv2.SourceImported || column.Source.Imported == nil || column.Source.Imported.OutputColumn != stage[i].Name {
			t.Errorf("column %d does not preserve stable resolved source identity: %#v", i, column)
		}
	}
	if document.Construction != nil || document.TableShape != nil {
		t.Fatal("fresh import unexpectedly has an overlay")
	}
	if got, err := bundle.Digest(); err != nil || got != digest {
		t.Fatalf("original recipe changed during import: digest=%q err=%v", got, err)
	}
	withoutObservedSubject := catalogForBundle(bundle)
	nodeTypes := make(map[string]string, len(withoutObservedSubject.Nodes))
	for _, node := range withoutObservedSubject.Nodes {
		nodeTypes[node.ID] = node.ResourceType
	}
	keptEdges := withoutObservedSubject.Edges[:0]
	for _, edge := range withoutObservedSubject.Edges {
		if nodeTypes[edge.FromNodeID] == "DocumentReference" && nodeTypes[edge.ToNodeID] == "ResearchStudy" && edge.Label == "subject" {
			continue
		}
		keptEdges = append(keptEdges, edge)
	}
	withoutObservedSubject.Edges = keptEdges
	workspace, err = ImportWorkspace(bundle, withoutObservedSubject, schemas)
	if err != nil {
		t.Fatalf("import default recipe without its unobserved optional edge: %v", err)
	}
	for _, imported := range workspace.Documents {
		if imported.Output.ID != "DocumentReference" {
			continue
		}
		found := false
		for _, child := range imported.Route.Children {
			if child.ResourceType == "ResearchStudy" && child.Relationship == "subject_ResearchStudy" {
				found = true
				if child.CatalogEdgeID != "" || child.MatchMode != authoringv2.RouteMatchOptional {
					t.Fatalf("unobserved optional traversal has fake catalog authority or wrong mode: %#v", child)
				}
			}
		}
		if !found {
			t.Fatal("default recipe import dropped subject_ResearchStudy traversal")
		}
	}
}

func catalogForBundle(bundle recipe.Bundle) authoringv2.CatalogSnapshot {
	resourceIDs := map[string]string{}
	for _, output := range bundle.Outputs {
		resourceIDs[output.RootResourceType] = "node_" + output.RootResourceType
	}
	var addResourceTypes func([]recipe.Traversal)
	addResourceTypes = func(traversals []recipe.Traversal) {
		for _, traversal := range traversals {
			resourceIDs[traversal.ToResourceType] = "node_" + traversal.ToResourceType
			addResourceTypes(traversal.Traversals)
		}
	}
	for _, output := range bundle.Outputs {
		addResourceTypes(output.Traversals)
	}
	var catalog authoringv2.CatalogSnapshot
	for resource, id := range resourceIDs {
		catalog.Nodes = append(catalog.Nodes, authoringv2.CatalogNode{ID: id, ResourceType: resource})
	}
	seen := map[string]bool{}
	var add func(from string, traversals []recipe.Traversal)
	add = func(from string, traversals []recipe.Traversal) {
		for _, traversal := range traversals {
			label := traversal.Name
			if suffix := "_" + traversal.ToResourceType; strings.HasSuffix(label, suffix) {
				label = strings.TrimSuffix(label, suffix)
			}
			key := from + "\x00" + label + "\x00" + traversal.ToResourceType
			if !seen[key] {
				seen[key] = true
				catalog.Edges = append(catalog.Edges, authoringv2.CatalogEdge{ID: "edge_" + label + "_" + traversal.ToResourceType, FromNodeID: resourceIDs[from], ToNodeID: resourceIDs[traversal.ToResourceType], Label: label})
			}
			add(traversal.ToResourceType, traversal.Traversals)
		}
	}
	for _, output := range bundle.Outputs {
		add(output.RootResourceType, output.Traversals)
	}
	return catalog
}

func TestImportRejectsAmbiguousTraversalEdgeAndAllowsSchemaOnlyEdge(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Patient", RowGrain: "RECORD", Traversals: []recipe.Traversal{{Name: "subject", ToResourceType: "Observation"}}}}}
	base := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient"}, {ID: "observation", ResourceType: "Observation"}}, Edges: []authoringv2.CatalogEdge{{ID: "e1", FromNodeID: "patient", ToNodeID: "observation", Label: "subject"}}}
	_, err := ImportWorkspace(bundle, base, map[string][]recipe.StageColumn{"out": {}})
	if err != nil {
		t.Fatalf("schema-only edge was rejected: %v", err)
	}
	base.Edges = append(base.Edges, authoringv2.CatalogEdge{ID: "e2", FromNodeID: "patient", ToNodeID: "observation", Label: "subject"})
	if _, err := ImportWorkspace(bundle, base, map[string][]recipe.StageColumn{"out": {}}); err == nil {
		t.Fatal("ambiguous traversal edges accepted")
	}
}

func TestImportRetainsOptionalConfiguredTraversalWithoutObservedEdge(t *testing.T) {
	output := recipe.Output{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "RECORD", Traversals: []recipe.Traversal{{Name: "subject_ResearchStudy", ToResourceType: "ResearchStudy"}}}
	catalog := authoringv2.CatalogSnapshot{Nodes: []authoringv2.CatalogNode{{ID: "document-reference", ResourceType: "DocumentReference"}, {ID: "research-study", ResourceType: "ResearchStudy"}}}
	route, err := ImportRoute(output, catalog)
	if err != nil {
		t.Fatalf("ImportRoute with absent optional catalog edge: %v", err)
	}
	if len(route.Children) != 1 {
		t.Fatalf("route dropped the configured traversal: %#v", route)
	}
	child := route.Children[0]
	if child.CatalogEdgeID != "" || child.Relationship != "subject_ResearchStudy" || child.ResourceType != "ResearchStudy" || child.MatchMode != authoringv2.RouteMatchOptional {
		t.Fatalf("missing edge was replaced with catalog authority or source semantics were lost: %#v", child)
	}
	output.Traversals[0].MatchMode = recipe.MatchRequired
	if _, err := ImportRoute(output, catalog); err == nil {
		t.Fatal("missing required traversal was treated as an optional source hint")
	}
}

func TestImportRejectsMissingSchemaAndUnstableColumnIdentity(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "seed", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Patient", RowGrain: "RECORD"}}}
	if _, err := ImportWorkspace(bundle, authoringv2.CatalogSnapshot{}, nil); err == nil {
		t.Fatal("missing schema accepted")
	}
	if _, err := ImportWorkspace(bundle, authoringv2.CatalogSnapshot{}, map[string][]recipe.StageColumn{"out": {{Name: "id"}}}); err == nil {
		t.Fatal("schema without stable id accepted")
	}
}
