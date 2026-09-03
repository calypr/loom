package authoringv2

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func emptyCommandWorkspace() Workspace {
	return Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Builder"}, Documents: []Document{}, Tabs: []Tab{}}
}

func commandCatalog() CatalogSnapshot {
	catalog := testCatalog()
	for index := range catalog.Candidates {
		catalog.Candidates[index].ProjectionModes = []string{"VALUE"}
		catalog.Candidates[index].DefaultProjectionMode = "VALUE"
		catalog.Candidates[index].FieldPath = "id"
	}
	return catalog
}

func TestApplyCommandsCreatesRecipeSafeBackendIdentities(t *testing.T) {
	workspace, results, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "browser-request-with-hyphens", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(workspace.Documents) != 1 || len(workspace.Tabs) != 1 {
		t.Fatalf("workspace=%#v results=%#v", workspace, results)
	}
	safe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	if !safe.MatchString(results[0].OutputID) || results[0].OutputID != workspace.Documents[0].Output.ID {
		t.Fatalf("backend generated invalid output identity %q", results[0].OutputID)
	}
	if results[0].TabID == "" || results[0].OccurrenceID != RootOccurrenceID {
		t.Fatalf("result=%#v", results[0])
	}
	wire, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"columns":[]`) {
		t.Fatalf("empty columns must remain an array on the wire: %s", wire)
	}
}

func TestApplyCommandsOwnsNestedRouteAndColumnIdentities(t *testing.T) {
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, route, err := ApplyCommands(workspace, commandCatalog(), "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, columns, err := ApplyCommands(workspace, commandCatalog(), "column", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: route[0].OccurrenceID, CandidateID: "encounter-id"}})
	if err != nil {
		t.Fatal(err)
	}
	if route[0].OccurrenceID == "" || columns[0].Column == "" || workspace.Documents[0].Columns[0].Column != columns[0].Column {
		t.Fatalf("route=%#v columns=%#v workspace=%#v", route, columns, workspace)
	}
}

func TestApplyCommandsUpdatesRouteEdgeWithoutReplacingOccurrenceState(t *testing.T) {
	catalog := commandCatalog()
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "patient-encounter-secondary", FromNodeID: "patient", ToNodeID: "encounter", Label: "researchEncounters"})
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, route, err := ApplyCommands(workspace, catalog, "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	occurrenceID := route[0].OccurrenceID
	workspace, _, err = ApplyCommands(workspace, catalog, "column", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: occurrenceID, CandidateID: "encounter-id"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Route.Children[0].Children = []RouteNode{{OccurrenceID: "nested", ResourceType: "Encounter", Relationship: "revisits"}}

	updated, _, err := ApplyCommands(workspace, catalog, "update-edge", []Command{{Type: CommandUpdateRouteEdge, OutputID: outputID, OccurrenceID: occurrenceID, EdgeID: "patient-encounter-secondary"}})
	if err != nil {
		t.Fatal(err)
	}
	child := updated.Documents[0].Route.Children[0]
	if child.OccurrenceID != occurrenceID || child.Relationship != "researchEncounters" || len(child.Children) != 1 || child.Children[0].OccurrenceID != "nested" {
		t.Fatalf("updated route=%#v", child)
	}
	if len(updated.Documents[0].Columns) != 1 || updated.Documents[0].Columns[0].OccurrenceID != occurrenceID {
		t.Fatalf("columns were not preserved: %#v", updated.Documents[0].Columns)
	}
}

func TestApplyCommandsRejectsReusingRelationshipFromAnotherOccurrence(t *testing.T) {
	catalog := commandCatalog()
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"})
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, encounter, err := ApplyCommands(workspace, catalog, "encounter", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, patient, err := ApplyCommands(workspace, catalog, "patient", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: encounter[0].OccurrenceID, EdgeID: "encounter-patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, catalog, "repeat", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: patient[0].OccurrenceID, EdgeID: "patient-encounter"}})
	if err == nil || !strings.Contains(err.Error(), "already used in this query") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyCommandsAddsFilterAndChartWithoutMakingColumnTableVisible(t *testing.T) {
	catalog := commandCatalog()
	catalog.Candidates[0].Filterable = true
	catalog.Candidates[0].Chartable = true
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, added, err := ApplyCommands(workspace, catalog, "filter", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationFilter}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Columns) != 1 {
		t.Fatalf("columns=%#v", workspace.Documents[0].Columns)
	}
	column := workspace.Documents[0].Columns[0]
	if column.Column != added[0].Column || column.Table != nil || column.Filter == nil || column.Chart != nil {
		t.Fatalf("filter-only column=%#v", column)
	}

	workspace, _, err = ApplyCommands(workspace, catalog, "chart", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationChart}})
	if err != nil {
		t.Fatal(err)
	}
	column = workspace.Documents[0].Columns[0]
	if len(workspace.Documents[0].Columns) != 1 || column.Table != nil || column.Filter == nil || column.Chart == nil || column.Chart.Type != "bar" {
		t.Fatalf("filter-and-chart column=%#v", column)
	}
}

func TestApplyCommandsRejectsUnsupportedInitialPresentation(t *testing.T) {
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "filter", []Command{{Type: CommandAddColumn, OutputID: created[0].OutputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationFilter}})
	if err == nil || !strings.Contains(err.Error(), "does not support filters") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyCommandsRejectsBatchAtomically(t *testing.T) {
	original := emptyCommandWorkspace()
	_, _, err := ApplyCommands(original, commandCatalog(), "batch", []Command{
		{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"},
		{Type: CommandDeleteTable, OutputID: "missing"},
	})
	if err == nil {
		t.Fatal("invalid second command unexpectedly succeeded")
	}
	if len(original.Documents) != 0 || len(original.Tabs) != 0 {
		t.Fatalf("input mutated after rejected batch: %#v", original)
	}
}
