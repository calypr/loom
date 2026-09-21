package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMigrateLosslessDefaultsRemovesRawStructuralColumnsAndDependents(t *testing.T) {
	visible := true
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: 9,
		Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{
			Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: RecordsRowDefinition(),
			Columns: []Column{
				{Column: "raw_name", Label: "Raw identifier value", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "identifier[].value", ProjectionMode: "ALL"}}, Table: &TablePresentation{Visible: &visible}},
				{Column: "birth_date", Label: "Birth date", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}, Table: &TablePresentation{Visible: &visible}},
			},
			TableShape: &TableShape{Derived: []DerivedConstruction{{
				ConstructionID: "derived-from-raw", Output: ColumnOutput{Column: "bad_feature", Label: "Bad feature"}, Operation: "ADD",
				Left: ArithmeticOperand{Kind: "COLUMN", Column: "raw_name"}, Right: ArithmeticOperand{Kind: "COLUMN", Column: "birth_date"}, MissingInputPolicy: "PROPAGATE_NULL",
			}}},
			FixedFilters: []FixedFilter{{Column: "raw_name", Values: []string{"x"}}, {Column: "birth_date", Values: []string{"2000-01-01"}}},
			Actions:      []Action{{Type: "download", Title: "Download", Columns: []ActionColumn{{Column: "raw_name"}, {Column: "birth_date"}}}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
		SharedFilters: map[string][]SharedFilterBinding{
			"raw":   {{OutputID: "patients", Column: "raw_name"}},
			"birth": {{OutputID: "patients", Column: "birth_date"}},
		},
	}

	migrated := MigrateLosslessDefaults(workspace, CatalogSnapshot{})
	if migrated.SemanticsVersion != 10 {
		t.Fatalf("semanticsVersion = %d, want 10", migrated.SemanticsVersion)
	}
	document := migrated.Documents[0]
	if len(document.Columns) != 1 || document.Columns[0].Column != "birth_date" {
		t.Fatalf("columns = %#v, want only birth_date", document.Columns)
	}
	if document.TableShape != nil {
		t.Fatalf("tableShape = %#v, want dependent shape removed", document.TableShape)
	}
	if len(document.FixedFilters) != 1 || document.FixedFilters[0].Column != "birth_date" {
		t.Fatalf("fixedFilters = %#v", document.FixedFilters)
	}
	if len(document.Actions) != 1 || len(document.Actions[0].Columns) != 1 || document.Actions[0].Columns[0].Column != "birth_date" {
		t.Fatalf("actions = %#v", document.Actions)
	}
	if len(migrated.SharedFilters) != 1 || len(migrated.SharedFilters["birth"]) != 1 {
		t.Fatalf("sharedFilters = %#v", migrated.SharedFilters)
	}
	if !contains(migrated.MigrationDecisions, "semantics-v10:remove-raw-structural-column:patients:raw_name:Patient:identifier[].value") ||
		!contains(migrated.MigrationDecisions, "semantics-v10:remove-dependent-table-shape:patients") {
		t.Fatalf("migrationDecisions = %#v", migrated.MigrationDecisions)
	}
	if len(workspace.Documents[0].Columns) != 2 || workspace.Documents[0].TableShape == nil {
		t.Fatalf("migration mutated input: %#v", workspace)
	}
	first, err := migrated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := MigrateLosslessDefaults(migrated, CatalogSnapshot{}).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("migration is not idempotent:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestMigrateLosslessDefaultsAdvancesV7WithoutInventingTableShape(t *testing.T) {
	legacy, err := DecodeWorkspace(persistedWorkspaceWithoutRows(explicitRowsSemanticsVersion - 1))
	if err != nil {
		t.Fatal(err)
	}
	migrated := MigrateLosslessDefaults(legacy, CatalogSnapshot{})
	if migrated.SemanticsVersion != CurrentSemanticsVersion {
		t.Fatalf("semanticsVersion = %d, want %d", migrated.SemanticsVersion, CurrentSemanticsVersion)
	}
	if migrated.Documents[0].TableShape != nil {
		t.Fatalf("migration invented table shape: %#v", migrated.Documents[0].TableShape)
	}
}

func TestMigrateLegacyNestedAggregateWhereEmptyEqualsToExists(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":3,"explorer":{"title":"Patients"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_count","label":"Patients","occurrenceId":"base","source":{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"id","equals":""}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := DecodeWorkspace([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	catalog := commandCatalog()
	migrated, err := MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	column := migrated.Documents[0].Columns[0]
	if migrated.SemanticsVersion != contributorSemanticsVersion || column.Source.Aggregate.Where != nil || column.Contributor == nil {
		t.Fatalf("migrated workspace = %#v", migrated)
	}
	if column.Contributor.Operator != ContributorExists || column.Contributor.Value != nil {
		t.Fatalf("legacy empty equals did not become EXISTS: %#v", column.Contributor)
	}
	if len(migrated.MigrationDecisions) != 1 {
		t.Fatalf("migration decisions = %#v", migrated.MigrationDecisions)
	}
	if _, err := json.Marshal(migrated); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeWorkspaceMigratesPersistedFirstOrderedTemporalPolicy(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(aggregateTemporalPolicySemanticsVersion-1) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"observations","resourceType":"Observation","relationship":"subject_Patient"}]},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"latest","label":"Latest","occurrenceId":"observations","source":{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"direction":"DESC","precision":"INSTANT","tiePolicy":"RESOURCE_KEY"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := DecodeWorkspace([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	aggregate := workspace.Documents[0].Columns[0].Source.Aggregate
	if workspace.SemanticsVersion != aggregateTemporalPolicySemanticsVersion || aggregate == nil || aggregate.ContributorWindow == nil || aggregate.Ordering == nil {
		t.Fatalf("migrated workspace version=%d aggregate=%#v", workspace.SemanticsVersion, aggregate)
	}
	if aggregate.ContributorWindow.TimestampPath != "effectiveDateTime" || aggregate.ContributorWindow.AnchorPath != "meta.lastUpdated" || aggregate.Ordering.TimestampPath != "effectiveDateTime" || aggregate.Ordering.Direction != "DESC" || aggregate.Ordering.TiePolicy != "RESOURCE_KEY" {
		t.Fatalf("migrated temporal policy = window %#v ordering %#v", aggregate.ContributorWindow, aggregate.Ordering)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), `"temporal":`) || !strings.Contains(string(canonical), `"contributorWindow":`) || !strings.Contains(string(canonical), `"ordering":`) {
		t.Fatalf("canonical persisted source did not use the new contract: %s", canonical)
	}
	reloaded, err := DecodeWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	reloadedCanonical, err := reloaded.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(reloadedCanonical) != string(canonical) {
		t.Fatalf("persisted migration was not idempotent:\nfirst=%s\nsecond=%s", canonical, reloadedCanonical)
	}
}

func TestDecodeWorkspaceRejectsLegacyTemporalAtCurrentSemanticsVersion(t *testing.T) {
	raw := `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(CurrentSemanticsVersion) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"observations","resourceType":"Observation","relationship":"subject_Patient"}]},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"latest","label":"Latest","occurrenceId":"observations","source":{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"direction":"DESC","precision":"INSTANT","tiePolicy":"RESOURCE_KEY"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	if _, err := DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("current semantics accepted the legacy temporal field: %v", err)
	}
}

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
		Documents: []Document{{Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient", Children: []RouteNode{{OccurrenceID: "administration", ResourceType: "MedicationAdministration", Relationship: "administrations"}}}, Columns: []Column{
			{Column: "root_name", Label: "Name", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}, Table: &TablePresentation{Visible: &visible}},
			{Column: "administration__status", Label: "Status", OccurrenceID: "administration", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "status", ProjectionMode: "VALUE"}}, Table: &TablePresentation{Visible: &visible}},
			{Column: "administration__status_indexed", Label: "Indexed status", OccurrenceID: "administration", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "status", ProjectionMode: "INDEXED"}}, Table: &TablePresentation{Visible: &visible}},
		}}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "patient", ResourceType: "Patient"}, {ID: "administration", ResourceType: "MedicationAdministration"}}, Candidates: []CatalogCandidate{{ID: "name", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}, {ID: "status", NodeID: "administration", FieldPath: "status", ProjectionModes: []string{"VALUE", "INDEXED"}, DefaultProjectionMode: "VALUE"}}}
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
