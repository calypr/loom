package authoringv2

import (
	"strings"
	"testing"
)

func TestMigrateLosslessDefaultsUpgradesRepeatedFirstOnce(t *testing.T) {
	visible := true
	legacy := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Columns: []Column{{Column: "names", Label: "Names", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}, Table: &TablePresentation{Visible: &visible}}}}},
		Tabs:      []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := CatalogSnapshot{
		Nodes:      []CatalogNode{{ID: "patient", ResourceType: "Patient"}},
		Candidates: []CatalogCandidate{{ID: "names", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}},
	}

	migrated := MigrateLosslessDefaults(legacy, catalog)
	if migrated.SemanticsVersion != CurrentSemanticsVersion || migrated.Documents[0].Columns[0].Source.Field.ProjectionMode != "INDEXED" {
		t.Fatalf("migrated workspace = %#v", migrated)
	}
	if len(migrated.MigrationDecisions) != 1 || migrated.MigrationDecisions[0] != "semantics-v2:repeated-first-to-indexed:patients:names" {
		t.Fatalf("migration decisions = %#v", migrated.MigrationDecisions)
	}
	legacyDigest, err := legacy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	migratedDigest, err := migrated.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if migratedDigest == legacyDigest {
		t.Fatal("migration decision did not change the workspace digest")
	}
	raw, err := migrated.CanonicalJSON()
	if err != nil || !strings.Contains(string(raw), `"migrationDecisions"`) {
		t.Fatalf("canonical migration decision = %s, err=%v", raw, err)
	}
	if legacy.SemanticsVersion != 0 || legacy.Documents[0].Columns[0].Source.Field.ProjectionMode != "FIRST" {
		t.Fatal("migration mutated its input")
	}

	migrated.Documents[0].Columns[0].Source.Field.ProjectionMode = "FIRST"
	remigrated := MigrateLosslessDefaults(migrated, catalog)
	if remigrated.Documents[0].Columns[0].Source.Field.ProjectionMode != "FIRST" {
		t.Fatal("current explicit FIRST projection was reinterpreted")
	}
}

func TestMigrateSemanticsV2PreservesExplicitRootFirstAndAnnotatesChildReduction(t *testing.T) {
	visible := true
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: 2, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient", Children: []RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}}, Columns: []Column{
			{Column: "root_name", Label: "Name", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}, Table: &TablePresentation{Visible: &visible}},
			{Column: "encounter__code", Label: "Code", OccurrenceID: "encounter", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "code.coding[].code", ProjectionMode: "VALUE"}}, Table: &TablePresentation{Visible: &visible}},
			{Column: "encounter__code_indexed", Label: "Indexed code", OccurrenceID: "encounter", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "code.coding[].code", ProjectionMode: "INDEXED"}}, Table: &TablePresentation{Visible: &visible}},
		}}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "patient", ResourceType: "Patient"}, {ID: "encounter", ResourceType: "Encounter"}}, Candidates: []CatalogCandidate{{ID: "name", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}, {ID: "code", NodeID: "encounter", FieldPath: "code.coding[].code", ProjectionModes: []string{"VALUE", "INDEXED"}, DefaultProjectionMode: "VALUE"}}}
	migrated := MigrateLosslessDefaults(workspace, catalog)
	if migrated.Documents[0].Columns[0].Source.Field.ProjectionMode != "FIRST" {
		t.Fatalf("explicit v2 root FIRST was reinterpreted: %#v", migrated.Documents[0].Columns[0].Source)
	}
	selection := migrated.Documents[0].Columns[1].Source.Field.RelatedSelection
	if selection == nil || selection.Kind != "first-by-resource-key" || selection.Acknowledged {
		t.Fatalf("child VALUE did not receive unacknowledged related selection: %#v", selection)
	}
	indexedSelection := migrated.Documents[0].Columns[2].Source.Field.RelatedSelection
	if indexedSelection == nil || indexedSelection.Kind != "first-by-resource-key" || indexedSelection.Acknowledged {
		t.Fatalf("child INDEXED did not receive unacknowledged related selection: %#v", indexedSelection)
	}
}
