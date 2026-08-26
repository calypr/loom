package authoringv2

import (
	"fmt"
	"strings"
	"testing"
)

func workspaceDocument(id string) Document {
	return Document{Kind: Kind, Output: Output{ID: id, Title: id}, RootNodeID: "patient", RouteSteps: []RouteStep{}, Selections: []Selection{{CandidateID: "patient-id", OccurrenceID: RootOccurrenceID, ProjectionMode: "SCALAR"}}, Presentation: map[string]Presentation{}}
}

func fiveTableWorkspace() Workspace {
	w := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Documents: []Document{}, Tabs: []Tab{}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("output-%d", i)
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
