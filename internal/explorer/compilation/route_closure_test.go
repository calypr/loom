package compilation

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestCompileRejectsStaleRouteBranchUsedByColumn(t *testing.T) {
	document := staleRouteDocument(authoringv2.RouteNode{OccurrenceID: "stale", ResourceType: "Encounter", CatalogEdgeID: "e_removed", Relationship: "encounters"})
	document.Columns = append(document.Columns, authoringv2.Column{
		Column: "encounter_code", Label: "Encounter code", OccurrenceID: "stale",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "FIRST"}},
	})

	_, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	assertCompilationCode(t, err, "STALE_ROUTE_EDGE")
}

func TestCompileRejectsStaleRequiredRouteBranchWithoutColumn(t *testing.T) {
	document := staleRouteDocument(authoringv2.RouteNode{
		OccurrenceID: "stale", ResourceType: "Encounter", CatalogEdgeID: "e_removed", Relationship: "encounters", MatchMode: authoringv2.RouteMatchRequired,
	})

	_, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	assertCompilationCode(t, err, "STALE_ROUTE_EDGE")
}

func TestCompileRejectsStaleExpandedRowOwner(t *testing.T) {
	document := staleRouteDocument(authoringv2.RouteNode{OccurrenceID: "stale", ResourceType: "Encounter", CatalogEdgeID: "e_removed", Relationship: "encounters"})
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
		OccurrenceID: "stale", ScopePath: "code.coding[].code", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
	}}

	_, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	assertCompilationCode(t, err, "STALE_ROUTE_EDGE")
}

func TestCompileWorkspaceKeepsStaleOptionalRouteInAuthoredWorkspace(t *testing.T) {
	snapshot := interpretationSnapshot()
	revision := prepareInterpretation(t, explorer.InterpretationRevision{
		ID: "revision-stale-route", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "stale route closure",
		Rules: []explorer.InterpretationRule{{ID: "rule", Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("gender")}},
	})
	workspace := workspaceWithRevision(string(revision.ID))
	workspace.Documents[0].Route.Children = []authoringv2.RouteNode{{
		OccurrenceID: "stale", ResourceType: "Encounter", CatalogEdgeID: "e_removed", Relationship: "encounters",
	}}

	inputs, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision})
	if err != nil {
		t.Fatalf("resolve pinned interpretation: %v", err)
	}
	compiled, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, inputs)
	if err != nil {
		t.Fatalf("compile workspace with unrelated stale route: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 1 || len(compiled.Bundle.Outputs[0].Traversals) != 0 {
		t.Fatalf("compiled stale optional traversals = %#v, want none", compiled.Bundle.Outputs[0].Traversals)
	}
	if len(compiled.Workspace.Documents) != 1 || len(compiled.Workspace.Documents[0].Route.Children) != 1 || compiled.Workspace.Documents[0].Route.Children[0].OccurrenceID != "stale" {
		t.Fatalf("authored workspace route = %#v, want stale branch retained", compiled.Workspace.Documents)
	}
}

func staleRouteDocument(child authoringv2.RouteNode) authoringv2.Document {
	return authoringv2.Document{
		Rows: authoringv2.RecordsRowDefinition(), Kind: authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{child}},
		Columns: []authoringv2.Column{{
			Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func assertCompilationCode(t *testing.T, err error, want string) {
	t.Helper()
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != want {
		t.Fatalf("compilation error = %v, want %s", err, want)
	}
}
