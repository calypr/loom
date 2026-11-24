package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestStoredWorkspaceMigrationInvalidatesVersion9Digest(t *testing.T) {
	legacy := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: tableShapeSemanticsVersion,
		Explorer:  ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{workspaceDocument("patients")},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	storedDigest, err := legacy.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := legacy.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := ws.MigrateStoredWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Workspace.SemanticsVersion != ws.CurrentSemanticsVersion {
		t.Fatalf("migrated semanticsVersion = %d, want %d", migrated.Workspace.SemanticsVersion, ws.CurrentSemanticsVersion)
	}
	if !contains(migrated.AcceptedOriginalDigests, storedDigest) || !migrated.DigestChanged || migrated.MigratedCanonicalDigest == storedDigest {
		t.Fatalf("old digest invalidation evidence = %#v; old=%q", migrated, storedDigest)
	}
}

func TestMigrateLosslessDefaultsAdvancesV7WithoutInventingTableShape(t *testing.T) {
	legacy, err := ws.DecodeStoredWorkspaceForMigration(persistedWorkspaceWithoutRows(explicitRowsSemanticsVersion - 1))
	if err != nil {
		t.Fatal(err)
	}
	migrated := ws.MigrateLosslessDefaults(legacy, ws.CatalogSnapshot{})
	if migrated.SemanticsVersion != ws.CurrentSemanticsVersion {
		t.Fatalf("semanticsVersion = %d, want %d", migrated.SemanticsVersion, ws.CurrentSemanticsVersion)
	}
	if migrated.Documents[0].ArchiveRetiredShape != nil {
		t.Fatalf("migration invented table shape: %#v", migrated.Documents[0].ArchiveRetiredShape)
	}
}

func TestMigrateLegacyNestedAggregateWhereEmptyEqualsToExists(t *testing.T) {
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":3,"explorer":{"title":"Patients"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_count","label":"Patients","occurrenceId":"base","source":{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"id","equals":""}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	catalog := commandCatalog()
	migrated, err := ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	column := migrated.Documents[0].Columns[0]
	if migrated.SemanticsVersion != contributorSemanticsVersion || column.Source.Aggregate.Where != nil || column.Contributor == nil {
		t.Fatalf("migrated workspace = %#v", migrated)
	}
	if column.Contributor.Operator != ws.ContributorExists || column.Contributor.Value != nil {
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
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(aggregateTemporalPolicySemanticsVersion-1) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"observations","resourceType":"Observation","relationship":"subject_Patient"}]},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"latest","label":"Latest","occurrenceId":"observations","source":{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"direction":"DESC","precision":"INSTANT","tiePolicy":"RESOURCE_KEY"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	workspace, err := ws.DecodeStoredWorkspaceForMigration([]byte(raw))
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
	reloaded, err := ws.DecodeStoredWorkspaceForMigration(canonical)
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
	raw := `{"apiVersion":"` + ws.APIVersion + `","kind":"` + ws.WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(ws.CurrentSemanticsVersion) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + ws.Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"observations","resourceType":"Observation","relationship":"subject_Patient"}]},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"latest","label":"Latest","occurrenceId":"observations","source":{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"direction":"DESC","precision":"INSTANT","tiePolicy":"RESOURCE_KEY"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`
	if _, err := ws.DecodeWorkspace([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("current semantics accepted the legacy temporal field: %v", err)
	}
}

func TestMigrateLosslessDefaultsUpgradesRepeatedFirstOnce(t *testing.T) {
	visible := true
	legacy := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{{Column: "names", Label: "Names", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}, Table: &ws.TablePresentation{Visible: &visible}}}}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := ws.CatalogSnapshot{
		Nodes:      []ws.CatalogNode{{ID: "patient", ResourceType: "Patient"}},
		Candidates: []ws.CatalogCandidate{{ID: "names", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []ws.RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}},
	}

	migrated := ws.MigrateLosslessDefaults(legacy, catalog)
	if migrated.SemanticsVersion != ws.CurrentSemanticsVersion || migrated.Documents[0].Columns[0].Source.Field.ProjectionMode != "INDEXED" {
		t.Fatalf("migrated workspace = %#v", migrated)
	}
	if len(migrated.MigrationDecisions) != 1 || migrated.MigrationDecisions[0] != "semantics-v2:repeated-first-to-indexed:patients:names" {
		t.Fatalf("migration decisions = %#v", migrated.MigrationDecisions)
	}
	legacyRaw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	normalizedLegacy, err := ws.DecodeStoredWorkspaceForMigration(legacyRaw)
	if err != nil {
		t.Fatalf("normalize legacy workspace through stored-workspace decoder: %v", err)
	}
	legacyDigest, err := normalizedLegacy.Digest()
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
	remigrated := ws.MigrateLosslessDefaults(migrated, catalog)
	if remigrated.Documents[0].Columns[0].Source.Field.ProjectionMode != "FIRST" {
		t.Fatal("current explicit FIRST projection was reinterpreted")
	}
}

func TestMigrateSemanticsV2PreservesExplicitRootFirstAndAnnotatesChildReduction(t *testing.T) {
	visible := true
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: 2, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}}, Columns: []ws.Column{
			{Column: "root_name", Label: "Name", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name[].family", ProjectionMode: "FIRST"}}, Table: &ws.TablePresentation{Visible: &visible}},
			{Column: "encounter__code", Label: "Code", OccurrenceID: "encounter", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "code.coding[].code", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}},
			{Column: "encounter__code_indexed", Label: "Indexed code", OccurrenceID: "encounter", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "code.coding[].code", ProjectionMode: "INDEXED"}}, Table: &ws.TablePresentation{Visible: &visible}},
		}}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := ws.CatalogSnapshot{Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient"}, {ID: "encounter", ResourceType: "Encounter"}}, Candidates: []ws.CatalogCandidate{{ID: "name", NodeID: "patient", FieldPath: "name[].family", ProjectionModes: []string{"INDEXED", "FIRST"}, DefaultProjectionMode: "INDEXED", RepeatedBoundaries: []ws.RepeatedBoundary{{Path: "name[]", MaxItems: 2}}}, {ID: "code", NodeID: "encounter", FieldPath: "code.coding[].code", ProjectionModes: []string{"VALUE", "INDEXED"}, DefaultProjectionMode: "VALUE"}}}
	migrated := ws.MigrateLosslessDefaults(workspace, catalog)
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
