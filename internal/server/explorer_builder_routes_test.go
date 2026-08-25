package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func publishOnlyAuthoringApp(t *testing.T, store *explorer.MemoryStore, snapshot explorer.CatalogSnapshot, preview ExplorerV2Previewer, materialize ExplorerV2Materializer) (*fiber.App, *explorer.Service) {
	t.Helper()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	compiler := func(ctx context.Context, request ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error) {
		return ResolveAuthoringBundle(ctx, nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, request)
	}
	app := fiber.New()
	RegisterExplorerAuthoringV1Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{
		AuthoringCompile: compiler,
		Catalog:          func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil },
		Preview:          preview, Materialize: materialize,
		ValidateReleaseGeneration: func(context.Context, string, string) error { return nil },
		ActivateRelease:           func(context.Context, string, string, []dataset.DataframeSelector) error { return nil },
	})
	return app, service
}

func TestBuilderReadReturnsCombinedEmptyModel(t *testing.T) {
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	app, _ := publishOnlyAuthoringApp(t, store, authoringTestSnapshot(t), nil, nil)
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/builder", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder=%d %s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerBuilderStateV1
	decodeBody(t, response.Body, &state)
	if state.Kind != explorer.ExplorerBuilderStateV1Kind || state.Bundle.ExplorerID != "custom" || len(state.Bundle.AuthoringDocuments()) != 0 || len(state.Catalog.Nodes) == 0 || state.Catalog.SnapshotToken == "" || len(state.Bindings) != 0 {
		t.Fatalf("unexpected empty Builder state: %#v", state)
	}
	for _, removed := range []string{"draftVersion", "draftDigest", "receiptId"} {
		if strings.Contains(response.Body, removed) {
			t.Fatalf("Builder response leaked removed field %q: %s", removed, response.Body)
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/projects/project-a/explorers/custom/authoring/v1/draft"},
		{http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/bundle/draft"},
		{http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/catalog"},
		{http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/compile"},
	} {
		response := requestJSON(t, app, route.method, route.path, `{}`)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("removed authoring route %s %s returned %d", route.method, route.path, response.StatusCode)
		}
	}
}

func TestBuilderReadRejectsUnmigratedLegacyAuthoring(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	owner, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "default", Title: "Biospecimens", ManagementMode: explorer.ManagementInteractive})
	if err != nil {
		t.Fatal(err)
	}
	canonicalNodeID := explorer.OpaqueID("n_", "Specimen")
	legacyNodeID := explorer.OpaqueID("n_", "specimen")
	candidateID := explorer.OpaqueID("s_", "Specimen\x00id")
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{{ID: canonicalNodeID, ResourceType: "Specimen"}},
		Selections: map[string]explorer.CatalogSelection{
			candidateID: {ID: candidateID, NodeID: canonicalNodeID, FieldRef: "Specimen.id", Select: "id", LogicalType: "scalar", Filterable: true},
		},
	}
	snapshot, err := explorer.NewCatalogSnapshot("project-a", "generation-a", "scope-a", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	broken := authoringTestBundle(explorer.ExplorerBuilderDocumentV1{
		Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "specimen", Title: "Biospecimens"},
		BaseNodeID: legacyNodeID, RowNodeID: legacyNodeID,
		Presentation: map[string]explorer.ExplorerPresentationBindingV1{
			explorer.OpaqueID("em_", "specimen\x00base\x00legacy-candidate"): {Label: "stale"},
		},
	})
	broken.Project, broken.ExplorerID, broken.Title = "project-a", "default", "Biospecimens"
	brokenJSON, err := broken.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	legacyConfig := json.RawMessage(`{"apiVersion":"loom.calypr.org/explorer-config/v2","kind":"ExplorerConfig","project":"project-a","explorer":{"id":"default","title":"Biospecimens","management":"repository"},"recipe":{"recipeSchemaVersion":1,"name":"legacy","translationVersion":"v1","outputs":[{"name":"specimen","rootResourceType":"Specimen","rowGrain":"specimen","fields":[{"name":"id","fieldRef":"Specimen.id","expr":{"select":"root.id"}}]}]},"views":[{"id":"specimen","title":"Biospecimens","output":"specimen","table":{"columns":[{"column":"id","label":"Identifier","visible":true}]}}]}`)
	revision := explorer.Revision{
		ID: "legacy-specimen", Project: owner.Project, ExplorerID: owner.ExplorerID,
		AuthoringBundle: brokenJSON, SourceGeneration: snapshot.Generation, Status: explorer.RevisionReady,
		Migration: &explorer.MigrationMetadata{Kind: "ExplorerAuthoringMigration", Source: "frontend-mapping", OriginalConfig: legacyConfig},
	}
	if _, err := store.InsertRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateInteractive(ctx, owner.Project, owner.ExplorerID, revision.ID); err != nil {
		t.Fatal(err)
	}

	app, _ := publishOnlyAuthoringApp(t, store, snapshot, nil, nil)
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v1/builder", "")
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(response.Body, "STALE_BASE_NODE") {
		t.Fatalf("unmigrated authoring was accepted: status=%d body=%s", response.StatusCode, response.Body)
	}
}

func TestBuilderReadReturnsCanonicalCandidateOccurrences(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	owner, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	legacy := authoringTestBundleWithoutCandidateOccurrences(explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "patient"},
		BaseNodeID: "n_base",
		RowNodeID:  "n_base",
		CandidateIDs: []string{
			"s_base",
		},
	})
	legacy.IntentDigest = "sha256:legacy-before-candidate-occurrences"
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertRevision(ctx, explorer.Revision{ID: "legacy", Project: owner.Project, ExplorerID: owner.ExplorerID, AuthoringBundle: raw, SourceGeneration: snapshot.Generation, Status: explorer.RevisionReady}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateInteractive(ctx, owner.Project, owner.ExplorerID, "legacy"); err != nil {
		t.Fatal(err)
	}
	app, _ := publishOnlyAuthoringApp(t, store, snapshot, nil, nil)
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/builder", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder=%d %s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerBuilderStateV1
	decodeBody(t, response.Body, &state)
	documents := state.Bundle.AuthoringDocuments()
	if len(documents) != 1 || len(documents[0].CandidateOccurrences) != 1 || documents[0].CandidateOccurrences[0].CandidateID != "s_base" || documents[0].CandidateOccurrences[0].OccurrenceID != "base" {
		t.Fatalf("Builder did not return canonical candidate occurrences: %#v", state.Bundle)
	}
}

func TestExplorerCollectionIsSummaryOnlyAndLegacyMutationsAreAbsent(t *testing.T) {
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive, DraftConfig: json.RawMessage(`{"legacy":true}`), DraftVersion: 7, DraftDigest: "draft"}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{})
	list := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers", "")
	if list.StatusCode != http.StatusOK || strings.Contains(list.Body, "draft") || strings.Contains(list.Body, "bundle") || strings.Contains(list.Body, "runtime") {
		t.Fatalf("collection is not summary-only: %d %s", list.StatusCode, list.Body)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/projects/project-a/explorers/custom/draft"},
		{http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/compile"},
		{http.MethodPost, "/api/v1/projects/project-a/explorers/custom/preview"},
		{http.MethodPost, "/api/v1/projects/project-a/explorers/custom/publish"},
		{http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/catalog"},
	} {
		response := requestJSON(t, app, route.method, route.path, `{}`)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("legacy route %s %s remains registered: %d %s", route.method, route.path, response.StatusCode, response.Body)
		}
	}
}

func TestSelectedRuntimeReadProjectsActiveRevisionDirectly(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	owner, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive})
	if err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "recipe", TranslationVersion: "authoring-v1", Output: "patient"}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "recipe", TranslationVersion: "authoring-v1", Outputs: []recipe.Output{{Name: "patient", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{{Name: "c_id", FieldRef: "Patient.id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	config, err := json.Marshal(explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "custom", Title: "Custom", Management: "interactive"},
		Recipe:     mustJSON(bundle),
		Views: []explorer.ConfigView{{
			ID: "patient", Title: "Patients", Output: "patient",
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "c_id", Visible: true}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := explorer.Revision{
		ID: "revision-1", Project: owner.Project, ExplorerID: owner.ExplorerID, Config: config,
		Recipe: bundle, RecipeDigest: "recipe-digest", ResolvedSchemaDigest: "schema-digest", SourceGeneration: "generation-a",
		Materializations: []explorer.Materialization{{OutputID: "patient", Output: "patient", MaterializationID: "execution-a", Selector: &selector, Columns: []publication.PhysicalColumn{{Name: "patient_c_id", LogicalType: "String"}}}},
		EmittedColumns:   []explorer.EmittedColumn{{EmissionID: "em_id", OutputID: "patient", PublicColumn: "c_id", LogicalType: "string"}},
		Dataset:          explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{Name: "patient", State: "PUBLISHED", Queryable: true, Selector: &selector, Columns: []publication.PhysicalColumn{{Name: "patient_c_id", LogicalType: "String"}}}}},
		Publication:      explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: "generation-a"}, Status: explorer.RevisionReady,
	}
	if _, err := store.InsertRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateInteractive(ctx, owner.Project, owner.ExplorerID, revision.ID); err != nil {
		t.Fatal(err)
	}

	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runtime read status=%d body=%s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerStateV1
	decodeBody(t, response.Body, &state)
	if state.Active.RevisionID != revision.ID || state.Runtime == nil || len(state.Runtime.Outputs) != 1 || state.Runtime.Outputs[0].OutputID != "patient" {
		t.Fatalf("runtime state=%#v body=%s", state, response.Body)
	}
	if strings.Contains(response.Body, "draftConfig") || strings.Contains(response.Body, "activeConfig") {
		t.Fatalf("runtime response leaked legacy state: %s", response.Body)
	}
}

func TestPreviewUsesIntentAndSnapshotWithoutWrites(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	previewCalls := 0
	app, service := publishOnlyAuthoringApp(t, store, snapshot, func(_ context.Context, _ recipe.Bundle, _ recipe.RuntimeBindings) (map[string][]map[string]any, error) {
		previewCalls++
		return map[string][]map[string]any{"patient": {{"id": "p1"}}}, nil
	}, nil)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}}
	bundle := authoringTestBundle(document)
	resolved, err := ResolveAuthoringBundle(ctx, nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token, "outputId": "patient", "limit": 25})
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/preview", string(raw))
	if response.StatusCode != http.StatusOK || previewCalls != 1 {
		t.Fatalf("preview=%d calls=%d %s", response.StatusCode, previewCalls, response.Body)
	}
	if _, err := service.CompilationReceipt(ctx, resolved.Receipt.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("preview persisted receipt: %v", err)
	}
	if _, err := service.ActiveRevision(ctx, "project-a", "custom"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("preview changed active revision: %v", err)
	}
}

func TestPreviewAndPublishNormalizeLegacyCandidateSelection(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	app, _ := publishOnlyAuthoringApp(t, store, snapshot,
		func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (map[string][]map[string]any, error) {
			return map[string][]map[string]any{"patient": {{"id": "p1"}}}, nil
		},
		func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
			return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: snapshot.Generation, Outputs: []graphresolver.RecipeExecutionOutput{{Name: bundle.Outputs[0].Name, State: "PUBLISHED"}}}, nil
		},
	)
	bundle := authoringTestBundleWithoutCandidateOccurrences(explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "patient"},
		BaseNodeID: "n_base",
		RowNodeID:  "n_base",
		CandidateIDs: []string{
			"s_base",
		},
	})
	raw, err := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token, "outputId": "patient", "limit": 25})
	if err != nil {
		t.Fatal(err)
	}
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/preview", string(raw))
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview=%d %s", preview.StatusCode, preview.Body)
	}
	publishRaw, err := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	publish := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", string(publishRaw))
	if publish.StatusCode != http.StatusOK {
		t.Fatalf("publish=%d %s", publish.StatusCode, publish.Body)
	}
}

func TestPublishReturnsReloadableResolvedBuilderState(t *testing.T) {
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	materializeCalls := 0
	app, _ := publishOnlyAuthoringApp(t, store, snapshot, nil, func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		materializeCalls++
		return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: snapshot.Generation, Outputs: []graphresolver.RecipeExecutionOutput{{Name: bundle.Outputs[0].Name, State: "PUBLISHED"}}}, nil
	})
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient", Title: "Patients"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}}
	bundle := authoringTestBundle(document)
	raw, _ := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token})
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", string(raw))
	if published.StatusCode != http.StatusOK || materializeCalls != 1 {
		t.Fatalf("publish=%d calls=%d %s", published.StatusCode, materializeCalls, published.Body)
	}
	var first explorer.ExplorerBuilderStateV1
	decodeBody(t, published.Body, &first)
	if first.Active.RevisionID == "" || len(first.Bindings) != 1 || first.Bindings[0].BaseResourceType != "Patient" || first.Bindings[0].RouteKind != "ZERO_HOP" {
		t.Fatalf("published Builder state=%#v", first)
	}
	reloaded := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/builder", "")
	if reloaded.StatusCode != http.StatusOK {
		t.Fatalf("reload=%d %s", reloaded.StatusCode, reloaded.Body)
	}
	var second explorer.ExplorerBuilderStateV1
	decodeBody(t, reloaded.Body, &second)
	if second.Active.RevisionID != first.Active.RevisionID || len(second.Bindings) != 1 || second.Bindings[0].CandidateEmissions[0].EmissionID != first.Bindings[0].CandidateEmissions[0].EmissionID {
		t.Fatalf("reload did not preserve authoritative bindings: first=%#v second=%#v", first, second)
	}
}

func TestFailedPublishPreservesActiveRevision(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertRevision(ctx, explorer.Revision{ID: "prior", Project: "project-a", ExplorerID: "custom", Status: explorer.RevisionReady}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateInteractive(ctx, "project-a", "custom", "prior"); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	app, service := publishOnlyAuthoringApp(t, store, snapshot, nil, func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		return graphresolver.RecipeExecution{}, errors.New("offline")
	})
	bundle := authoringTestBundle(explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base"})
	raw, _ := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token})
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", string(raw))
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, "MATERIALIZATION_FAILED") {
		t.Fatalf("publish=%d %s", response.StatusCode, response.Body)
	}
	active, err := service.ActiveRevision(ctx, "project-a", "custom")
	if err != nil || active.ID != "prior" {
		t.Fatalf("active revision changed: %#v err=%v", active, err)
	}
}

func TestPostBuilderReconcilesIntentWithoutWrites(t *testing.T) {
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	app, service := publishOnlyAuthoringApp(t, store, snapshot, nil, nil)
	document := explorer.ExplorerBuilderDocumentV1{
		Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient", Title: "Patients"},
		BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"},
		CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "s_base", OccurrenceID: "base"}},
	}
	bundle := authoringTestBundle(document)
	raw, _ := json.Marshal(map[string]any{"bundle": bundle, "snapshotToken": snapshot.Token})
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/builder", string(raw))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder reconcile=%d %s", response.StatusCode, response.Body)
	}
	var state explorer.ExplorerBuilderStateV1
	decodeBody(t, response.Body, &state)
	if len(state.Bindings) != 1 || len(state.Bindings[0].CandidateEmissions) != 1 || state.Bindings[0].CandidateEmissions[0].PublicColumn == "" {
		t.Fatalf("resolved builder binding=%#v", state.Bindings)
	}
	if _, err := service.ActiveRevision(ctx, "project-a", "custom"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("POST Builder changed active revision: %v", err)
	}
}

func TestTableIdentityIsServerAllocatedAndIdempotent(t *testing.T) {
	store := explorer.NewMemoryStore()
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Custom", ManagementMode: explorer.ManagementInteractive}); err != nil {
		t.Fatal(err)
	}
	app, _ := publishOnlyAuthoringApp(t, store, authoringTestSnapshot(t), nil, nil)
	body := `{"title":"Biospecimens","requestId":"new-table-1","occupiedOutputIds":["biospecimens"],"occupiedTabIds":["biospecimens"]}`
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/table-identity", body)
	repeated := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/table-identity", body)
	if response.StatusCode != http.StatusOK || response.Body != repeated.Body || !strings.Contains(response.Body, `"kind":"ExplorerTableIdentity"`) || strings.Contains(response.Body, `"id":"biospecimens`) {
		t.Fatalf("table identity=%d %s", response.StatusCode, response.Body)
	}
}
