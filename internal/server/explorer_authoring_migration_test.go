package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestMappingAuthoringBundleCanonicalizesIdentityAndDigest(t *testing.T) {
	input := map[string]any{
		"title": "BForePC",
		"bundle": map[string]any{
			"apiVersion": explorer.ExplorerAuthoringV1APIVersion,
			"kind":       explorer.ExplorerAuthoringV1Kind,
			"project":    "HTAN_INT%2FBForePC",
			"explorerId": "default",
			"title":      "BForePC",
			"documents": []any{map[string]any{
				"kind":       "ExplorerBuilderDocument",
				"output":     map[string]any{"id": "groupmember", "title": "GroupMember"},
				"baseNodeId": "n_group",
				"rowNodeId":  "n_group",
			}},
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := mappingAuthoringBundle(raw, "HTAN_INT/BForePC", "default")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Project != "HTAN_INT/BForePC" || bundle.ExplorerID != "default" || bundle.IntentDigest == "" {
		t.Fatalf("unexpected canonical bundle: %#v", bundle)
	}
	if err := bundle.Validate(); err != nil {
		t.Fatalf("canonical bundle does not validate: %v", err)
	}
}

func TestMigrationSourceCreatesMissingRepositoryIdentityAndPreservesExternalConfig(t *testing.T) {
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rawConfig := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"HTAN_INT-BForePC","explorer":{"id":"default","title":"BForePC","management":"repository"},"unknownFrontendField":{"preserve":true}}`)
	owner, config, err := migrationSource(context.Background(), service, ExplorerAuthoringMigrationOptions{
		Project: "HTAN_INT%252FBForePC", ExplorerID: "default", Actor: "test", LegacyConfig: rawConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Project != "HTAN_INT-BForePC" || owner.ExplorerID != "default" || owner.Title != "BForePC" {
		t.Fatalf("unexpected seeded owner: %#v", owner)
	}
	if string(config) != string(rawConfig) {
		t.Fatalf("migration did not preserve original config bytes")
	}
	if _, err := service.Get(context.Background(), "HTAN_INT/BForePC", "default"); err != nil {
		t.Fatalf("seeded repository identity is not discoverable: %v", err)
	}
}

func TestMigrationSourceCanSeedFromMappingWithoutLegacyConfig(t *testing.T) {
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rawMapping := []byte(`{"title":"BForePC","bundle":{"apiVersion":"loom.calypr.org/explorer-authoring/v1","kind":"ExplorerAuthoringBundle","project":"HTAN_INT/BForePC","explorerId":"default","title":"BForePC","documents":[]}}`)
	owner, config, err := migrationSource(context.Background(), service, ExplorerAuthoringMigrationOptions{
		Project: "HTAN_INT/BForePC", ExplorerID: "default", Actor: "test", LegacyMapping: rawMapping,
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Project != "HTAN_INT-BForePC" || owner.Title != "BForePC" {
		t.Fatalf("unexpected seeded owner: %#v", owner)
	}
	if len(config) != 0 {
		t.Fatalf("mapping-only migration unexpectedly returned a legacy config: %q", config)
	}
}

func TestMigrationBundlePrefersExplicitLegacyConfigOverActiveBundle(t *testing.T) {
	rawConfig := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","fieldRef":"Patient.id","expr":{"select":"root.id"}}]}]},"views":[]}`)
	catalog := explorer.Catalog{Nodes: []explorer.CatalogNode{{ID: "n_patient", ResourceType: "Patient"}}, Selections: map[string]explorer.CatalogSelection{
		"s_patient_id": {ID: "s_patient_id", NodeID: "n_patient", FieldRef: "Patient.id", Select: "id"},
	}}
	active := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: "project-a", ExplorerID: "default", Documents: []explorer.ExplorerBuilderDocumentV1{{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "old"}, BaseNodeID: "n_patient", RowNodeID: "n_patient"}}}
	bundle, source, err := migrationBundle(rawConfig, nil, mustAuthoringJSON(t, active), catalog, "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	if source != "legacy-config" || len(bundle.AuthoringDocuments()) != 1 || bundle.AuthoringDocuments()[0].Output.ID != "patient" {
		t.Fatalf("explicit config was not selected: source=%q bundle=%#v", source, bundle)
	}
}

func TestMigrationBundleRepairsEmptyMappingCandidatesFromLegacyConfig(t *testing.T) {
	rawConfig := []byte(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Default","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"file","fields":[{"name":"id","expr":{"select":"root.id"}},{"name":"url","expr":{"call":"first","args":[{"select":"root.content[].attachment.url"}]}}]}]},"views":[{"id":"file","title":"Files","output":"DocumentReference","table":{"columns":[{"column":"document_reference_id","label":"Identifier","visible":true},{"column":"document_reference_url","label":"URL","visible":false},{"column":"project_id","label":"Project","visible":true}]}}]}`)
	mappingBundle := explorer.ExplorerAuthoringBundleV1{
		APIVersion: explorer.ExplorerAuthoringV1APIVersion,
		Kind:       explorer.ExplorerAuthoringV1Kind,
		Project:    "project-a",
		ExplorerID: "default",
		Title:      "Default",
		Documents: []explorer.ExplorerBuilderDocumentV1{{
			Kind:       explorer.ExplorerBuilderV1Kind,
			Output:     explorer.ExplorerOutputIdentityV1{ID: "file", Title: "DocumentReference"},
			BaseNodeID: "n_document",
			RowNodeID:  "n_document",
		}},
	}
	rawMapping, err := json.Marshal(map[string]any{"bundle": mappingBundle})
	if err != nil {
		t.Fatal(err)
	}
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{{ID: "n_document", ResourceType: "DocumentReference"}},
		Selections: map[string]explorer.CatalogSelection{
			"s_document_id":  {ID: "s_document_id", NodeID: "n_document", FieldRef: "DocumentReference.id", Select: "id", LogicalType: "scalar", Filterable: true},
			"s_document_url": {ID: "s_document_url", NodeID: "n_document", FieldRef: "DocumentReference.content_attachment_url", Select: "content[].attachment.url", LogicalType: "scalar", Filterable: true},
		},
	}
	var diagnostics []string
	bundle, source, err := migrationBundleWithDiagnostics(rawConfig, rawMapping, nil, catalog, "project-a", "default", &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if source != "frontend-mapping-repaired" {
		t.Fatalf("source=%q, want frontend-mapping-repaired", source)
	}
	documents := bundle.AuthoringDocuments()
	if len(documents) != 1 || len(documents[0].CandidateIDs) != 2 {
		t.Fatalf("repaired documents=%#v", documents)
	}
	if documents[0].CandidateIDs[0] != "s_document_id" || documents[0].CandidateIDs[1] != "s_document_url" {
		t.Fatalf("candidate IDs=%v", documents[0].CandidateIDs)
	}
	idEmission := explorer.OpaqueID("em_", "file\x00base\x00s_document_id")
	urlEmission := explorer.OpaqueID("em_", "file\x00base\x00s_document_url")
	idPresentation, ok := documents[0].Presentation[idEmission]
	if !ok || idPresentation.Label != "Identifier" || idPresentation.Table == nil || idPresentation.Visible == nil || !*idPresentation.Visible || idPresentation.Order == nil || *idPresentation.Order != 0 {
		t.Fatalf("identifier presentation=%#v", idPresentation)
	}
	urlPresentation, ok := documents[0].Presentation[urlEmission]
	if !ok || urlPresentation.Label != "URL" || urlPresentation.Table == nil || urlPresentation.Visible == nil || *urlPresentation.Visible || urlPresentation.Order == nil || *urlPresentation.Order != 1 {
		t.Fatalf("URL presentation=%#v", urlPresentation)
	}
	if len(diagnostics) != 1 || diagnostics[0] != "views[0].table.columns[2].project_id" {
		t.Fatalf("repair diagnostics=%v", diagnostics)
	}
	snapshot, err := explorer.NewCatalogSnapshot("project-a", "generation-a", "scope-a", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := ResolveAuthoringBundle(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.ResolvedBindings) != 1 || len(compiled.ResolvedBindings[0].CandidateEmissions) != 2 {
		t.Fatalf("resolved bindings=%#v", compiled.ResolvedBindings)
	}
}

func TestResolvedAuthoringBindingSerializesEmptyCandidateEmissionsAsArray(t *testing.T) {
	catalog := explorer.Catalog{Nodes: []explorer.CatalogNode{{ID: "n_patient", ResourceType: "Patient"}}}
	binding := resolvedAuthoringBinding(explorer.ExplorerBuilderDocumentV1{
		Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_patient", RowNodeID: "n_patient",
	}, catalog, nil, nil, nil)
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	emissions, ok := payload["candidateEmissions"].([]any)
	if !ok || len(emissions) != 0 {
		t.Fatalf("candidateEmissions serialized as %s", raw)
	}
}

func mustAuthoringJSON(t *testing.T, bundle explorer.ExplorerAuthoringBundleV1) []byte {
	t.Helper()
	raw, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
