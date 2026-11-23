package authoringv2

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func workspaceDocument(id string) ws.Document {
	visible := true
	return ws.Document{Kind: ws.Kind, Output: ws.Output{ID: id, Title: id}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}}}}
}

func TestWorkspaceCanonicalizesDuplicateTableOrdersByStableColumnIdentity(t *testing.T) {
	visible, duplicateOrder := true, 4
	column := func(name string) ws.Column {
		return ws.Column{Column: name, Label: name, OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}, Table: &ws.TablePresentation{Visible: &visible, Order: &duplicateOrder}, Filter: &ws.FilterPresentation{Order: &duplicateOrder}, Chart: &ws.ChartPresentation{Type: "bar", Order: &duplicateOrder}}
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Stable"},
		Documents: []ws.Document{{Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "out", Title: "Out"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{column("zeta"), column("alpha"), column("middle")}}},
		Tabs:      []ws.Tab{{ID: "tab", Title: "Out", OutputID: "out", Order: 0, Visible: true}},
	}
	reversed := workspace
	reversed.Documents = append([]ws.Document(nil), workspace.Documents...)
	reversed.Documents[0].Columns = []ws.Column{column("middle"), column("zeta"), column("alpha")}

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
	var normalized ws.Workspace
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

func fiveTableWorkspace() ws.Workspace {
	w := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Five tables"}, Documents: []ws.Document{}, Tabs: []ws.Tab{}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("output_%d", i)
		w.Documents = append(w.Documents, workspaceDocument(id))
		w.Tabs = append(w.Tabs, ws.Tab{ID: "tab-" + id, Title: id, OutputID: id, Order: i, Visible: true})
	}
	return w
}

func TestWorkspaceValidatesFiveOrderedDocumentsAndTabs(t *testing.T) {
	w := fiveTableWorkspace()
	state := ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &w, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"apiVersion":"`+ws.APIVersion+`","kind":"ExplorerBuilderDocument"`) {
		t.Fatalf("documents must not carry apiVersion: %s", raw)
	}
}

func TestWorkspaceRejectsDuplicateAndMissingMappings(t *testing.T) {
	tests := []struct {
		name, code string
		mutate     func(*ws.Workspace)
	}{
		{"duplicate output", "DUPLICATE_OUTPUT_ID", func(w *ws.Workspace) { w.Documents[1].Output.ID = w.Documents[0].Output.ID }},
		{"duplicate tab", "DUPLICATE_TAB_ID", func(w *ws.Workspace) { w.Tabs[1].ID = w.Tabs[0].ID }},
		{"duplicate mapping", "INVALID_TAB_OUTPUT_MAPPING", func(w *ws.Workspace) { w.Tabs[1].OutputID = w.Tabs[0].OutputID }},
		{"missing mapping", "INVALID_TAB_OUTPUT_MAPPING", func(w *ws.Workspace) { w.Tabs = w.Tabs[:4] }},
		{"duplicate order", "INVALID_TAB_ORDER", func(w *ws.Workspace) { w.Tabs[1].Order = 0 }},
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

func TestWorkspacePublicationRequiresVisibleColumns(t *testing.T) {
	w := fiveTableWorkspace()
	hidden := false
	w.Documents[0].Columns[0].Table.Visible = &hidden
	state := ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &w, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatalf("mutable Builder state rejected an all-hidden table: %v", err)
	}
	if err := w.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "NO_VISIBLE_COLUMNS") {
		t.Fatalf("publication validation error=%v", err)
	}
}

func TestBuilderAllowsVisibleTableWithNoColumnsUntilPublication(t *testing.T) {
	w := fiveTableWorkspace()
	w.Documents[0].Columns = []ws.Column{}
	state := ws.BuilderState{APIVersion: ws.APIVersion, Kind: ws.StateKind, Workspace: &w, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatalf("transitional empty table rejected by Builder: %v", err)
	}
	if err := w.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "NO_VISIBLE_COLUMNS: documents[0]") {
		t.Fatalf("publication validation error=%v", err)
	}
}

func TestWorkspacePublicationAcceptsFinalConstructionOutputsWithoutPublicSourceColumns(t *testing.T) {
	w := fiveTableWorkspace()
	document := &w.Documents[0]
	document.RootResourceType = "BodyStructure"
	document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "BodyStructure"}
	document.Columns = []ws.Column{}
	document.Construction = &ws.Construction{
		Version: ws.ConstructionVersion,
		SourceProjections: []ws.ConstructionSourceProjection{{
			ColumnID: "source_active", OccurrenceID: ws.RootOccurrenceID, FieldPath: "active",
			FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
		}},
		Steps: []ws.ConstructionStep{{
			ID: "group_by_active", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group_by_active",
				Keys:           []ws.ConstructionGroupKey{{InputColumnID: "source_active", OutputColumnID: "active_key"}},
				Aggregates:     []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "record_count"}},
			}},
			Outputs: []ws.StageColumn{
				{ID: "active_key", Name: "active", Label: "Whether this record is in active use", Type: "boolean"},
				{ID: "record_count", Name: "record_count", Label: "Record count", Type: "integer"},
			},
		}},
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("row-first grouped workspace is invalid: %v", err)
	}
	if err := w.ValidateForPublication(); err != nil {
		t.Fatalf("publication rejected final GROUP outputs without public source columns: %v", err)
	}
	hidden := false
	for index := range document.Construction.Steps[0].Outputs {
		document.Construction.Steps[0].Outputs[index].Table = &ws.TablePresentation{Visible: &hidden}
	}
	if err := w.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "NO_VISIBLE_COLUMNS: documents[0]") {
		t.Fatalf("publication accepted a GROUP with all final outputs hidden: %v", err)
	}
}

func TestDecodeWorkspaceRejectsUnknownFields(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","documents":[],"tabs":[],"recipe":{}}`
	if _, err := ws.DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error=%v", err)
	}
}

func TestDecodeWorkspaceRejectsDuplicateAndTrailingJSON(t *testing.T) {
	duplicate := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","documents":[],"tabs":[],"tabs":[]}`
	if _, err := ws.DecodeWorkspace([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate field error=%v", err)
	}
	trailing := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","documents":[],"tabs":[]} {}`
	if _, err := ws.DecodeWorkspace([]byte(trailing)); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing value error=%v", err)
	}
}

func TestDecodeWorkspaceRepairsPreviouslyOmittedEmptyColumns(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","explorer":{"title":"Patients"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"}}],"tabs":[{"id":"tab-patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
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

func TestDecodeWorkspacePreservesArrayProjectionModesAcrossPersistedVersions(t *testing.T) {
	visible := true
	for _, semanticsVersion := range []int{0, ws.CurrentSemanticsVersion} {
		t.Run(fmt.Sprintf("semantics-%d", semanticsVersion), func(t *testing.T) {
			workspace := ws.Workspace{
				APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: semanticsVersion,
				Explorer: ws.ExplorerMetadata{Title: "ws.Projection modes"},
				Documents: []ws.Document{{
					Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
					Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
					Rows:  ws.RecordsRowDefinition(),
					Columns: []ws.Column{
						{Column: "all_names", Label: "All names", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[]", ProjectionMode: "ALL"}}, Table: &ws.TablePresentation{Visible: &visible}},
						{Column: "distinct_names", Label: "Distinct names", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[]", ProjectionMode: "DISTINCT"}}, Table: &ws.TablePresentation{Visible: &visible}},
					},
				}},
				Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
			}
			raw, err := json.Marshal(workspace)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ws.Workspace
			if semanticsVersion == ws.CurrentSemanticsVersion {
				decoded, err = ws.DecodeWorkspace(raw)
			} else {
				decoded, err = ws.DecodeStoredWorkspaceForMigration(raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			columns := decoded.Documents[0].Columns
			if columns[0].Source.ProjectionMode() != "ALL" || columns[1].Source.ProjectionMode() != "DISTINCT" {
				t.Fatalf("projection modes after persisted round trip = %#v, want ALL and DISTINCT", columns)
			}
		})
	}
}

func TestDecodeWorkspaceMigratesRetiredCodedLookupKinds(t *testing.T) {
	for _, kind := range []string{"codingBySystem", "observationComponentByCode"} {
		t.Run(kind, func(t *testing.T) {
			raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(ws.CurrentSemanticsVersion) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"observations","title":"Observations"},"rootResourceType":"Observation","route":{"occurrenceId":"base","resourceType":"Observation"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"height","label":"Height","occurrenceId":"base","source":{"kind":"` + kind + `","lookup":{"binding":{"ownerPath":"","keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","choiceArms":["valueQuantity"],"logicalType":"decimal"},"key":{"system":"urn:study","code":"height"},"projectionMode":"VALUE"}}}]}],"tabs":[{"id":"observations","title":"Observations","outputId":"observations","order":0,"visible":true}]}`
			workspace, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			column := workspace.Documents[0].Columns[0]
			if column.Source.Kind != ws.SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Key == nil || column.Source.Lookup.Key.Code != "height" {
				t.Fatalf("decoded source = %#v", column.Source)
			}
			canonical, err := workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(canonical), `"kind":"codedValue"`) || strings.Contains(string(canonical), kind) {
				t.Fatalf("canonical persisted source = %s", canonical)
			}
		})
	}
}

func TestDecodeWorkspaceRejectsProjectIDPayload(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"project_id","label":"Project","occurrenceId":"base","source":{"kind":"projectId","field":{"path":"id"}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	if _, err := ws.DecodeWorkspace([]byte(raw)); err == nil {
		t.Fatal("projectId payload was accepted")
	}
}

func TestDecodeWorkspaceRejectsUnknownNestedSourceFields(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":3,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_id","label":"Patient","occurrenceId":"base","source":{"kind":"field","field":{"path":"id","projectionMode":"VALUE","unknown":true}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	if _, err := ws.DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error=%v, want unknown nested source field", err)
	}
}

func TestDecodeWorkspaceRejectsLegacyAggregateWhereAtCurrentSemantics(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(ws.CurrentSemanticsVersion) + `,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"patient_count","label":"Patients","occurrenceId":"base","source":{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"id","equals":"active"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","visible":true}]}`
	if _, err := ws.DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), `unknown field "where"`) {
		t.Fatalf("error=%v, want current semantics aggregate where rejection", err)
	}
}

func TestDecodeWorkspaceRejectsUnsupportedFutureSemanticsVersion(t *testing.T) {
	w := workspaceDocument("patients")
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion + 1, Explorer: ws.ExplorerMetadata{Title: "Future"}, Documents: []ws.Document{w}, Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}}}
	raw, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.DecodeStoredWorkspaceForMigration(raw); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_SEMANTICS_VERSION") {
		t.Fatalf("error=%v, want unsupported semantics version", err)
	}
}

func TestDecodeLegacySourceMigratesIdempotentlyAndAddsRelatedDecision(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":0,"explorer":{"title":"Persisted"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"names","label":"Names","occurrenceId":"base","source":{"kind":"field","fieldPath":"name[].family","projectionMode":"FIRST"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	decoded, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Documents[0].Columns[0].Source.Field == nil || decoded.Documents[0].Columns[0].Source.Field.Path != "name[].family" {
		t.Fatalf("legacy source=%#v", decoded.Documents[0].Columns[0].Source)
	}
	catalog := ws.CatalogSnapshot{Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient"}}, Candidates: []ws.CatalogCandidate{{ID: "names", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []ws.RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}}}
	first := ws.MigrateLosslessDefaults(decoded, catalog)
	second := ws.MigrateLosslessDefaults(first, catalog)
	firstJSON, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) || len(second.MigrationDecisions) != 1 {
		t.Fatalf("migration was not idempotent:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
}

func TestAggregateColumnSourceAcceptsClosedAggregateShape(t *testing.T) {
	visible := true
	document := workspaceDocument("patients")
	document.Route.Children = []ws.RouteNode{{OccurrenceID: "condition", ResourceType: "Condition", Relationship: "subject_Patient"}}
	document.Columns = append(document.Columns, ws.Column{
		Column: "condition_count", Label: "Condition count", OccurrenceID: "condition",
		Source: ws.ColumnSource{Kind: ws.SourceAggregate, Aggregate: &ws.AggregateSource{Operation: "COUNT"}},
		Table:  &ws.TablePresentation{Visible: &visible},
	})
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}

	document.Columns[1].Source.Aggregate.Operation = "NOT_AN_AGGREGATE"
	if err := document.Validate(); err == nil {
		t.Fatal("unsupported aggregate operation was accepted")
	}
}

func TestEmptyConstructionPreparationMatchesCanonicalReceiptWithoutChangingAuthoringJSON(t *testing.T) {
	for _, steps := range [][]ws.ConstructionStep{nil, {}} {
		workspace := fiveTableWorkspace()
		workspace.SemanticsVersion = ws.CurrentSemanticsVersion
		workspace.Documents[0].Columns[0].ColumnID = "patient-id"
		workspace.Documents[0].Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: steps}
		raw, err := workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := ws.DecodeWorkspace(raw)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := ws.PrepareWorkspaceForCompilation(receipt, testCatalog())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(prepared, receipt) {
			t.Fatal("prepared authoring state differs from its exact canonical receipt")
		}
		again, err := prepared.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != string(again) {
			t.Fatal("empty construction normalization changed authoritative JSON")
		}
		before, _ := receipt.Digest()
		after, _ := prepared.Digest()
		if before != after {
			t.Fatal("empty construction normalization changed authoritative digest")
		}
		if prepared.Documents[0].Construction.Steps == nil {
			t.Fatal("explicit empty operation sequence became nil")
		}
		for _, document := range prepared.Documents[1:] {
			if document.Construction != nil {
				t.Fatal("normalization invented an unrelated construction")
			}
		}
	}
}
