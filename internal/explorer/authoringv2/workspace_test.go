package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func workspaceDocument(id string) Document {
	return Document{Kind: Kind, Output: Output{ID: id, Title: id}, RootNodeID: "patient", RouteSteps: []RouteStep{}, Selections: []Selection{{CandidateID: "patient-id", OccurrenceID: RootOccurrenceID, ProjectionMode: "SCALAR"}}, Presentation: map[string]Presentation{}}
}

func TestWorkspaceCanonicalizesDuplicateTableOrdersByStableColumnIdentity(t *testing.T) {
	visible, duplicateOrder := true, 4
	column := func(name string) Column {
		return Column{Column: name, Label: name, OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}, Table: &TablePresentation{Visible: &visible, Order: &duplicateOrder}, Filter: &FilterPresentation{Order: &duplicateOrder}, Chart: &ChartPresentation{Type: "bar", Order: &duplicateOrder}}
	}
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Stable"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "out", Title: "Out"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Columns: []Column{column("zeta"), column("alpha"), column("middle")}}},
		Tabs:      []Tab{{ID: "tab", Title: "Out", OutputID: "out", Order: 0, Visible: true}},
	}
	reversed := workspace
	reversed.Documents = append([]Document(nil), workspace.Documents...)
	reversed.Documents[0].Columns = []Column{column("middle"), column("zeta"), column("alpha")}

	first, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reversed.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical workspaces differ:\n%s\n%s", first, second)
	}
	var normalized Workspace
	if err := json.Unmarshal(first, &normalized); err != nil {
		t.Fatal(err)
	}
	columns := normalized.Documents[0].Columns
	for index, want := range []string{"alpha", "middle", "zeta"} {
		if columns[index].Column != want || columns[index].Table == nil || columns[index].Table.Order == nil || *columns[index].Table.Order != index || columns[index].Filter == nil || columns[index].Filter.Order == nil || *columns[index].Filter.Order != index || columns[index].Chart == nil || columns[index].Chart.Order == nil || *columns[index].Chart.Order != index {
			t.Fatalf("column[%d]=%#v, want %q at normalized order %d", index, columns[index], want, index)
		}
	}
	if *workspace.Documents[0].Columns[0].Table.Order != duplicateOrder || *workspace.Documents[0].Columns[0].Filter.Order != duplicateOrder || *workspace.Documents[0].Columns[0].Chart.Order != duplicateOrder {
		t.Fatal("normalization mutated the caller's workspace")
	}
}

func fiveTableWorkspace() Workspace {
	w := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Documents: []Document{}, Tabs: []Tab{}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("output_%d", i)
		w.Documents = append(w.Documents, workspaceDocument(id))
		w.Tabs = append(w.Tabs, Tab{ID: "tab-" + id, Title: id, OutputID: id, Order: i, Visible: true})
	}
	return w
}

func TestWorkspaceValidatesFiveOrderedDocumentsAndTabs(t *testing.T) {
	w := fiveTableWorkspace()
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Workspace: &w, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"apiVersion":"`+APIVersion+`","kind":"ExplorerBuilderDocument"`) {
		t.Fatalf("documents must not carry apiVersion: %s", raw)
	}
}

func TestWorkspaceRejectsDuplicateAndMissingMappings(t *testing.T) {
	tests := []struct {
		name, code string
		mutate     func(*Workspace)
	}{
		{"duplicate output", "DUPLICATE_OUTPUT_ID", func(w *Workspace) { w.Documents[1].Output.ID = w.Documents[0].Output.ID }},
		{"duplicate tab", "DUPLICATE_TAB_ID", func(w *Workspace) { w.Tabs[1].ID = w.Tabs[0].ID }},
		{"duplicate mapping", "INVALID_TAB_OUTPUT_MAPPING", func(w *Workspace) { w.Tabs[1].OutputID = w.Tabs[0].OutputID }},
		{"missing mapping", "INVALID_TAB_OUTPUT_MAPPING", func(w *Workspace) { w.Tabs = w.Tabs[:4] }},
		{"duplicate order", "INVALID_TAB_ORDER", func(w *Workspace) { w.Tabs[1].Order = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := fiveTableWorkspace()
			tt.mutate(&w)
			if err := w.Validate(); err == nil || !strings.Contains(err.Error(), tt.code) {
				t.Fatalf("error=%v, want %s", err, tt.code)
			}
		})
	}
}

func TestDerivedOccurrencesStayOmittedAndAreUnique(t *testing.T) {
	d := workspaceDocument("out")
	d.RouteSteps = []RouteStep{{EdgeID: "patient-encounter"}, {EdgeID: "encounter-self"}}
	occ, err := d.Occurrences(testCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if occ[1].ID != "step-1" || occ[2].ID != "step-2" || d.RouteSteps[0].OccurrenceID != "" {
		t.Fatalf("occurrences=%#v document=%#v", occ, d)
	}
	d.RouteSteps[1].OccurrenceID = "step-1"
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate route occurrence") {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkspacePresentationCapabilitiesAndVisibleColumns(t *testing.T) {
	w := fiveTableWorkspace()
	catalog := testCatalog()
	catalog.Candidates[0].Filterable = false
	catalog.Candidates[0].Chartable = false
	key := PresentationKey("patient-id", "base", "SCALAR")
	w.Documents[0].Presentation[key] = Presentation{Filter: &FilterPresentation{Label: "ID"}}
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Workspace: &w, Catalog: catalog}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_FILTER") {
		t.Fatalf("error=%v", err)
	}
	w.Documents[0].Presentation[key] = Presentation{Chart: &ChartPresentation{Type: "bar"}}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_CHART") {
		t.Fatalf("error=%v", err)
	}
	hidden := false
	w.Documents[0].Presentation[key] = Presentation{Visible: &hidden}
	if err := state.Validate(); err != nil {
		t.Fatalf("mutable Builder state rejected an all-hidden table: %v", err)
	}
	if err := w.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "NO_VISIBLE_COLUMNS") {
		t.Fatalf("publication validation error=%v", err)
	}
}

func TestBuilderAllowsVisibleTableWithNoSelectionsUntilPublication(t *testing.T) {
	w := fiveTableWorkspace()
	w.Documents[0].Selections = []Selection{}
	w.Documents[0].Presentation = map[string]Presentation{}
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Workspace: &w, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatalf("transitional empty table rejected by Builder: %v", err)
	}
	if err := w.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "NO_VISIBLE_COLUMNS: documents[0]") {
		t.Fatalf("publication validation error=%v", err)
	}
}

func TestDecodeWorkspaceRejectsUnknownFields(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","documents":[],"tabs":[],"recipe":{}}`
	if _, err := DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error=%v", err)
	}
}

func TestDecodeWorkspaceRejectsDuplicateAndTrailingJSON(t *testing.T) {
	duplicate := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","documents":[],"tabs":[],"tabs":[]}`
	if _, err := DecodeWorkspace([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate field error=%v", err)
	}
	trailing := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","documents":[],"tabs":[]} {}`
	if _, err := DecodeWorkspace([]byte(trailing)); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing value error=%v", err)
	}
}

func TestDecodeWorkspaceRepairsPreviouslyOmittedEmptyColumns(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","explorer":{"title":"Patients"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"}}],"tabs":[{"id":"tab-patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := DecodeWorkspace([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"columns":[]`) {
		t.Fatalf("decoded legacy draft did not restore required columns array: %s", encoded)
	}
}
