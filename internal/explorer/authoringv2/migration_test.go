package authoringv2

import "testing"

func TestMigrateLosslessDefaultsUpgradesRepeatedFirstOnce(t *testing.T) {
	visible := true
	legacy := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Columns: []Column{{Column: "names", Label: "Names", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, FieldPath: "name[].family", ProjectionMode: "FIRST"}, Table: &TablePresentation{Visible: &visible}}}}},
		Tabs:      []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := CatalogSnapshot{
		Nodes:      []CatalogNode{{ID: "patient", ResourceType: "Patient"}},
		Candidates: []CatalogCandidate{{ID: "names", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}},
	}

	migrated := MigrateLosslessDefaults(legacy, catalog)
	if migrated.SemanticsVersion != CurrentSemanticsVersion || migrated.Documents[0].Columns[0].Source.ProjectionMode != "INDEXED" {
		t.Fatalf("migrated workspace = %#v", migrated)
	}
	if legacy.SemanticsVersion != 0 || legacy.Documents[0].Columns[0].Source.ProjectionMode != "FIRST" {
		t.Fatal("migration mutated its input")
	}

	migrated.Documents[0].Columns[0].Source.ProjectionMode = "FIRST"
	remigrated := MigrateLosslessDefaults(migrated, catalog)
	if remigrated.Documents[0].Columns[0].Source.ProjectionMode != "FIRST" {
		t.Fatal("current explicit FIRST projection was reinterpreted")
	}
}
