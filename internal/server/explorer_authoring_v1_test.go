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
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func authoringTestSnapshot(t *testing.T) explorer.CatalogSnapshot {
	t.Helper()
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{{ID: "n_base", ResourceType: "Patient"}, {ID: "n_child", ResourceType: "Observation"}},
		Edges: []explorer.CatalogEdge{{ID: "e_forward", FromNodeID: "n_base", ToNodeID: "n_child", Label: "observation"}},
		Selections: map[string]explorer.CatalogSelection{
			"s_base":  {ID: "s_base", NodeID: "n_base", FieldRef: "Patient.id", Select: "id", LogicalType: "string", Filterable: true, Chartable: true},
			"s_child": {ID: "s_child", NodeID: "n_child", FieldRef: "Observation.status", Select: "status", LogicalType: "string", Filterable: true, Chartable: true},
		},
	}
	snapshot, err := explorer.NewCatalogSnapshot("project-a", "generation-a", "scope-a", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func authoringTestBundle(document explorer.ExplorerBuilderDocumentV1) explorer.ExplorerAuthoringBundleV1 {
	if len(document.CandidateOccurrences) == 0 {
		for _, candidateID := range document.CandidateIDs {
			document.CandidateOccurrences = append(document.CandidateOccurrences, explorer.ExplorerCandidateOccurrenceV1{CandidateID: candidateID, OccurrenceID: "base"})
		}
	}
	return explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: "project-a", ExplorerID: "custom", Document: document}
}

func TestAuthoringCatalogResponseIncludesCandidatePath(t *testing.T) {
	response := authoringCatalogResponse(authoringTestSnapshot(t))
	candidates, ok := response["candidates"].([]fiber.Map)
	if !ok || len(candidates) != 2 {
		t.Fatalf("candidates=%#v", response["candidates"])
	}
	for _, candidate := range candidates {
		if candidate["path"] == "" || candidate["path"] != candidate["fieldRef"] {
			t.Fatalf("candidate path compatibility alias is missing: %#v", candidate)
		}
	}
}

func TestCompileAuthoringV1ZeroHopAndPresentationByEmission(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	visible := false
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}}
	emission := explorer.OpaqueID("em_", "patient\x00base\x00s_base")
	document.Presentation = map[string]explorer.ExplorerPresentationBindingV1{emission: {Label: "Patient ID", Visible: &visible, Filter: &explorer.ExplorerFilterBindingV1{Label: "Filter ID"}, Chart: &explorer.ExplorerChartBindingV1{Type: "bar", Title: "ID chart"}}}
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Receipt.EmittedColumns) != 1 || result.Receipt.EmittedColumns[0].EmissionID != emission {
		t.Fatalf("emissions=%#v", result.Receipt.EmittedColumns)
	}
	publicColumn := result.Receipt.EmittedColumns[0].PublicColumn
	if want := generatedFieldName("s_base", "base"); publicColumn != want {
		t.Fatalf("public column=%q, want lowered recipe field %q", publicColumn, want)
	}
	if got := result.Receipt.Bundle.Outputs[0].Fields[0].Name; got != publicColumn {
		t.Fatalf("lowered recipe field=%q, want emitted public column %q", got, publicColumn)
	}
	repeated, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil || repeated.Receipt.ID != result.Receipt.ID {
		t.Fatalf("compilation is not deterministic: first=%q second=%q err=%v", result.Receipt.ID, repeated.Receipt.ID, err)
	}
	if strings.Contains(string(result.Receipt.NormalizedBundle), "select") || strings.Contains(string(result.Receipt.NormalizedBundle), "expr") || strings.Contains(string(result.Receipt.NormalizedBundle), "children") {
		t.Fatalf("intent leaked recipe syntax: %s", result.Receipt.NormalizedBundle)
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(result.Receipt.CompiledConfig, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Views) != 1 || len(config.Views[0].Table.Columns) != 1 || config.Views[0].Table.Columns[0].Column != publicColumn || config.Views[0].Table.Columns[0].Label != "Patient ID" || config.Views[0].Table.Columns[0].Visible || len(config.Views[0].Filters) != 1 || config.Views[0].Filters[0].Column != publicColumn || len(config.Views[0].Charts) != 1 || config.Views[0].Charts[0].Column != publicColumn {
		t.Fatalf("presentation was not lowered by emission ID: %#v", config.Views)
	}
}

func TestCompileAuthoringV1UsesCatalogRowGrain(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	snapshot.Catalog.Nodes = append(snapshot.Catalog.Nodes, explorer.CatalogNode{ID: "n_file", ResourceType: "DocumentReference"})
	snapshot.Catalog.Selections["s_file"] = explorer.CatalogSelection{ID: "s_file", NodeID: "n_file", FieldRef: "DocumentReference.id", Select: "id"}
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "files"}, BaseNodeID: "n_file", RowNodeID: "n_file", CandidateIDs: []string{"s_file"}}
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Receipt.Bundle.Outputs[0].RowGrain; got != "file" {
		t.Fatalf("row grain=%q, want file", got)
	}
}

func TestCompileAuthoringV1CanonicalizesLowercaseCatalogResourceType(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	snapshot.Catalog.Nodes = []explorer.CatalogNode{{ID: "n_specimen", ResourceType: "specimen"}}
	snapshot.Catalog.Edges = nil
	snapshot.Catalog.Selections = map[string]explorer.CatalogSelection{}
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "specimens"}, BaseNodeID: "n_specimen", RowNodeID: "n_specimen"}
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Receipt.Bundle.Outputs[0]
	if output.RootResourceType != "Specimen" || output.RowGrain != "specimen" {
		t.Fatalf("lowered output root=%q grain=%q, want Specimen/specimen", output.RootResourceType, output.RowGrain)
	}
}

func TestCompileAuthoringV1RejectsLegacyLowercaseNodeID(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	canonicalNodeID := explorer.OpaqueID("n_", "Specimen")
	legacyNodeID := explorer.OpaqueID("n_", "specimen")
	snapshot.Catalog.Nodes = []explorer.CatalogNode{{ID: canonicalNodeID, ResourceType: "Specimen"}}
	snapshot.Catalog.Edges = nil
	snapshot.Catalog.Selections = map[string]explorer.CatalogSelection{}
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "specimen"}, BaseNodeID: legacyNodeID, RowNodeID: legacyNodeID}
	_, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err == nil || !strings.Contains(err.Error(), "STALE_BASE_NODE") {
		t.Fatalf("legacy node identity was accepted: %v", err)
	}
}

func TestCompileAuthoringV1NormalizesLegacyCandidateOccurrence(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}}
	bundle := explorer.ExplorerAuthoringBundleV1{APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind, Project: "project-a", ExplorerID: "custom", Documents: []explorer.ExplorerBuilderDocumentV1{document}}
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	documents := result.Bundle.AuthoringDocuments()
	if len(documents) != 1 || len(documents[0].CandidateOccurrences) != 1 || documents[0].CandidateOccurrences[0].CandidateID != "s_base" || documents[0].CandidateOccurrences[0].OccurrenceID != "base" {
		t.Fatalf("legacy candidate was not normalized: %#v", documents)
	}
}

func TestResolveAuthoringBundleUsesBaseNodeResourceTypesForGroupMemberAndFile(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	snapshot.Catalog.Nodes = []explorer.CatalogNode{{ID: "n_group", ResourceType: "Group"}, {ID: "n_file", ResourceType: "DocumentReference"}}
	snapshot.Catalog.Edges = nil
	snapshot.Catalog.Selections = map[string]explorer.CatalogSelection{}
	bundle := authoringTestBundle(explorer.ExplorerBuilderDocumentV1{})
	bundle.Document = explorer.ExplorerBuilderDocumentV1{}
	bundle.Documents = []explorer.ExplorerBuilderDocumentV1{
		{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "groupmember", Title: "Members"}, BaseNodeID: "n_group", RowNodeID: "n_group"},
		{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "file", Title: "Files"}, BaseNodeID: "n_file", RowNodeID: "n_file"},
	}
	result, err := ResolveAuthoringBundle(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ResolvedBindings) != 2 || result.ResolvedBindings[0].BaseResourceType != "Group" || result.ResolvedBindings[0].RouteKind != "ZERO_HOP" || result.ResolvedBindings[1].BaseResourceType != "DocumentReference" {
		t.Fatalf("resolved bindings=%#v", result.ResolvedBindings)
	}
}

func TestCompileAuthoringV1PreservesMultipleOutputsAndTabs(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	patient := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "patient", Title: "Patients"},
		BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"},
		CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "s_base", OccurrenceID: "base"}},
	}
	observations := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "observations", Title: "Observations"},
		BaseNodeID: "n_child", RowNodeID: "n_child", CandidateIDs: []string{"s_child"},
		CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "s_child", OccurrenceID: "base"}},
	}
	bundle := authoringTestBundle(patient)
	bundle.Document = explorer.ExplorerBuilderDocumentV1{}
	bundle.Documents = []explorer.ExplorerBuilderDocumentV1{patient, observations}
	bundle.Tabs = []explorer.ExplorerTabV1{
		{ID: "observations", Title: "Observations", OutputID: "observations", Order: 1},
		{ID: "patient", Title: "Patients", OutputID: "patient", Order: 0},
	}
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: bundle, SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Receipt.Bundle.Outputs) != 2 || len(result.Receipt.OutputFingerprints) != 2 {
		t.Fatalf("compiled outputs=%#v fingerprints=%#v", result.Receipt.Bundle.Outputs, result.Receipt.OutputFingerprints)
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(result.Receipt.CompiledConfig, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Views) != 2 || config.Views[0].ID != "patient" || config.Views[1].ID != "observations" || config.Views[0].Output != "patient" || config.Views[1].Output != "observations" {
		t.Fatalf("tabs were not preserved: %#v", config.Views)
	}
	if strings.Contains(string(result.Receipt.NormalizedBundle), `"document"`) || !strings.Contains(string(result.Receipt.NormalizedBundle), `"documents"`) {
		t.Fatalf("multi-output bundle was not canonicalized: %s", result.Receipt.NormalizedBundle)
	}
}

func TestCompileAuthoringV1MultiHopInboundAndRepeatedCandidateEmissions(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	// Catalog edges are already normalized into Builder direction. The
	// compiler preserves the target occurrence while the lowerer independently
	// proves that this Builder edge uses an inbound stored traversal.
	snapshot.Catalog.Edges = []explorer.CatalogEdge{{ID: "e_inbound", FromNodeID: "n_base", ToNodeID: "n_child", Label: "subject"}}
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_child", RouteEdgeIDs: []string{"e_inbound"}, RouteOccurrences: []explorer.ExplorerRouteOccurrenceV1{{ID: "child-1", Index: 0, NodeID: "n_child"}}, CandidateIDs: []string{"s_base", "s_child"}, CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "s_base", OccurrenceID: "base"}, {CandidateID: "s_child", OccurrenceID: "child-1"}, {CandidateID: "s_child", OccurrenceID: "child-1"}}}
	if _, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token}); err == nil || !strings.Contains(err.Error(), "DUPLICATE_CANDIDATE_OCCURRENCE") {
		t.Fatalf("duplicate candidate occurrence error=%v", err)
	}
	document.CandidateOccurrences = document.CandidateOccurrences[:2]
	result, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Receipt.Bundle.Outputs[0].Traversals) != 1 || result.Receipt.Bundle.Outputs[0].Traversals[0].ToResourceType != "Observation" {
		t.Fatalf("bundle=%#v", result.Receipt.Bundle)
	}
	// The same candidate can intentionally emit once at two exact route
	// occurrences when a resource type repeats.
	snapshot.Catalog.Edges = append(snapshot.Catalog.Edges, explorer.CatalogEdge{ID: "e_back", FromNodeID: "n_child", ToNodeID: "n_base", Label: "patient"})
	repeated := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", RouteEdgeIDs: []string{"e_inbound", "e_back"}, RouteOccurrences: []explorer.ExplorerRouteOccurrenceV1{{ID: "child-1", Index: 0, NodeID: "n_child"}, {ID: "base-2", Index: 1, NodeID: "n_base"}}, CandidateIDs: []string{"s_base"}, CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "s_base", OccurrenceID: "base"}, {CandidateID: "s_base", OccurrenceID: "base-2"}}}
	repeatedResult, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(repeated), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, mapping := range repeatedResult.Receipt.IdentityMappings {
		if mapping.CandidateID == "s_base" {
			count += len(mapping.EmissionIDs)
		}
	}
	if count != 2 {
		t.Fatalf("candidate-to-emission mapping=%#v", repeatedResult.Receipt.IdentityMappings)
	}
}

func TestCompileAuthoringV1ReportsStaleCandidateBeforeRecipeValidation(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"stale"}}
	_, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err == nil || !strings.Contains(err.Error(), "STALE_CANDIDATE_ID") {
		t.Fatalf("error=%v", err)
	}
}

func TestCompileAuthoringV1ReportsCandidateNotOnRoute(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_child"}}
	_, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err == nil || !strings.Contains(err.Error(), "SELECTION_NODE_MISMATCH") {
		t.Fatalf("error=%v", err)
	}
}

func TestCompileAuthoringV1ReportsStaleRouteIdentities(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	baseStale := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "stale", RowNodeID: "stale"}
	if _, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(baseStale), SnapshotToken: snapshot.Token}); err == nil || !strings.Contains(err.Error(), "STALE_BASE_NODE") {
		t.Fatalf("stale base node error=%v", err)
	}
	staleEdge := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_child", RouteEdgeIDs: []string{"stale-edge"}, RouteOccurrences: []explorer.ExplorerRouteOccurrenceV1{{ID: "child", Index: 0, NodeID: "n_child"}}}
	if _, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(staleEdge), SnapshotToken: snapshot.Token}); err == nil || !strings.Contains(err.Error(), "STALE_ROUTE_EDGE") {
		t.Fatalf("stale route edge error=%v", err)
	}
}

func TestCompileAuthoringV1ReportsSnapshotConflict(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base"}
	_, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: "sha256:stale"})
	if err == nil || !strings.Contains(err.Error(), "STALE_CATALOG_SNAPSHOT") {
		t.Fatalf("snapshot conflict error=%v", err)
	}
}

func TestCompileAuthoringV1RejectsStaleEmissionBeforeRecipeValidation(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}, Presentation: map[string]explorer.ExplorerPresentationBindingV1{"em_missing": {Label: "bad"}}}
	_, err := compileExplorerAuthoringV1(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err == nil || !strings.Contains(err.Error(), "STALE_EMISSION") {
		t.Fatalf("error=%v", err)
	}
}

func TestAuthoringV1ReceiptBoundLifecycleAndExport(t *testing.T) {
	t.Skip("receipt/draft lifecycle removed by publish-only Builder cutover")
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", ManagementMode: explorer.ManagementInteractive, DraftVersion: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot := authoringTestSnapshot(t)
	compilerCalls := 0
	compiler := func(ctx context.Context, request ExplorerAuthoringV1CompileRequest) (ExplorerAuthoringV1CompileResult, error) {
		compilerCalls++
		return compileExplorerAuthoringV1(ctx, nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, request)
	}
	previewCalls := 0
	materializeCalls := 0
	preview := func(_ context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (map[string][]map[string]any, error) {
		previewCalls++
		if bindings.OutputNames[0] != bundle.Outputs[0].Name {
			t.Fatalf("preview output=%v", bindings.OutputNames)
		}
		return map[string][]map[string]any{"patient": {{"id": "p1"}}}, nil
	}
	materialize := func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		materializeCalls++
		return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: "generation-a", Outputs: []graphresolver.RecipeExecutionOutput{{Name: bundle.Outputs[0].Name, State: "PUBLISHED", Columns: nil}}}, nil
	}
	app := fiber.New()
	RegisterExplorerAuthoringV1Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{AuthoringCompile: compiler, Catalog: func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, Preview: preview, Materialize: materialize, ValidateReleaseGeneration: func(context.Context, string, string) error { return nil }, ActivateRelease: func(context.Context, string, string, []dataset.DataframeSelector) error { return nil }})
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base", CandidateIDs: []string{"s_base"}}
	bundle := authoringTestBundle(document)
	legacyCompileRequest := map[string]any{"apiVersion": bundle.APIVersion, "kind": bundle.Kind, "project": bundle.Project, "explorerId": bundle.ExplorerID, "document": bundle.Document, "snapshotToken": snapshot.Token}
	legacyCompileRaw, _ := json.Marshal(legacyCompileRequest)
	legacyCompile := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/compile", string(legacyCompileRaw))
	if legacyCompile.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy compile route status=%d body=%s", legacyCompile.StatusCode, legacyCompile.Body)
	}
	var compiledBody struct {
		ReceiptID    string `json:"receiptId"`
		IntentDigest string `json:"intentDigest"`
	}
	directDraftRaw, _ := json.Marshal(map[string]any{"apiVersion": bundle.APIVersion, "kind": bundle.Kind, "project": bundle.Project, "explorerId": bundle.ExplorerID, "document": bundle.Document, "snapshotToken": snapshot.Token, "expectedDraftVersion": 1})
	saved := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom/authoring/v1/draft", string(directDraftRaw))
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("save=%d %s", saved.StatusCode, saved.Body)
	}
	decodeBody(t, saved.Body, &compiledBody)
	saveRaw, _ := json.Marshal(map[string]any{"receiptId": compiledBody.ReceiptID, "expectedDraftVersion": 1})
	staleSave := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom/authoring/v1/draft", string(saveRaw))
	if staleSave.StatusCode != http.StatusConflict || !strings.Contains(staleSave.Body, "DRAFT_CONFLICT") {
		t.Fatalf("stale save=%d %s", staleSave.StatusCode, staleSave.Body)
	}
	draftExport := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/bundle/draft", "")
	if draftExport.StatusCode != http.StatusOK || !strings.Contains(draftExport.Body, compiledBody.IntentDigest) {
		t.Fatalf("draft export=%d body=%s", draftExport.StatusCode, draftExport.Body)
	}
	previewRaw := `{"receiptId":"` + compiledBody.ReceiptID + `","outputId":"patient","limit":1}`
	previewResponse := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/preview", previewRaw)
	if previewResponse.StatusCode != http.StatusOK || previewCalls != 1 {
		t.Fatalf("preview=%d calls=%d %s", previewResponse.StatusCode, previewCalls, previewResponse.Body)
	}
	directPreviewRequest := map[string]any{"apiVersion": bundle.APIVersion, "kind": bundle.Kind, "project": bundle.Project, "explorerId": bundle.ExplorerID, "document": bundle.Document, "snapshotToken": snapshot.Token, "outputId": "patient", "limit": 1}
	directPreviewRaw, _ := json.Marshal(directPreviewRequest)
	directPreview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/preview", string(directPreviewRaw))
	if directPreview.StatusCode != http.StatusOK || previewCalls != 2 || compilerCalls != 2 || !strings.Contains(directPreview.Body, "candidateId") {
		t.Fatalf("direct preview=%d previewCalls=%d compilerCalls=%d %s", directPreview.StatusCode, previewCalls, compilerCalls, directPreview.Body)
	}
	directDraftRequest := map[string]any{"apiVersion": bundle.APIVersion, "kind": bundle.Kind, "project": bundle.Project, "explorerId": bundle.ExplorerID, "document": bundle.Document, "snapshotToken": snapshot.Token, "expectedDraftVersion": 2}
	directDraftRaw, _ = json.Marshal(directDraftRequest)
	directDraft := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom/authoring/v1/draft", string(directDraftRaw))
	if directDraft.StatusCode != http.StatusOK || compilerCalls != 3 {
		t.Fatalf("direct draft=%d compilerCalls=%d %s", directDraft.StatusCode, compilerCalls, directDraft.Body)
	}
	directPublished := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", `{"expectedDraftVersion":3}`)
	if directPublished.StatusCode != http.StatusOK || materializeCalls != 1 {
		t.Fatalf("direct publish=%d materializeCalls=%d %s", directPublished.StatusCode, materializeCalls, directPublished.Body)
	}
	if got := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/preview", `{"receiptId":"`+compiledBody.ReceiptID+`","outputId":"patient","bundle":{}}`); got.StatusCode != http.StatusBadRequest {
		t.Fatalf("browser recipe packet status=%d body=%s", got.StatusCode, got.Body)
	}
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", `{"receiptId":"`+compiledBody.ReceiptID+`","requestId":"retry-a"}`)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish=%d %s", published.StatusCode, published.Body)
	}
	retried := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", `{"receiptId":"`+compiledBody.ReceiptID+`","requestId":"retry-a"}`)
	if retried.StatusCode != http.StatusOK || materializeCalls != 1 {
		t.Fatalf("idempotent publish=%d %s", retried.StatusCode, retried.Body)
	}
	exported := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v1/bundle/active", "")
	if exported.StatusCode != http.StatusOK || exported.Body == "" || !strings.Contains(exported.Body, compiledBody.IntentDigest) {
		t.Fatalf("export=%d body=%s", exported.StatusCode, exported.Body)
	}
}

func TestAuthoringV1MaterializationFailureRetainsActiveRevision(t *testing.T) {
	t.Skip("receipt-based publish request removed by publish-only Builder cutover")
	ctx := context.Background()
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
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
	document := explorer.ExplorerBuilderDocumentV1{Kind: explorer.ExplorerBuilderV1Kind, Output: explorer.ExplorerOutputIdentityV1{ID: "patient"}, BaseNodeID: "n_base", RowNodeID: "n_base"}
	compiled, err := compileExplorerAuthoringV1(ctx, nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) { return snapshot, nil }, ExplorerAuthoringV1CompileRequest{Bundle: authoringTestBundle(document), SnapshotToken: snapshot.Token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StoreCompilationReceipt(ctx, compiled.Receipt); err != nil {
		t.Fatal(err)
	}
	releaseCalls := 0
	app := fiber.New()
	RegisterExplorerAuthoringV1Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{
		Materialize: func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
			return graphresolver.RecipeExecution{}, errors.New("materialization unavailable")
		},
		ValidateReleaseGeneration: func(context.Context, string, string) error { return nil },
		ActivateRelease:           func(context.Context, string, string, []dataset.DataframeSelector) error { releaseCalls++; return nil },
	})
	request := `{"receiptId":"` + compiled.Receipt.ID + `","requestId":"materialize-failure"}`
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v1/publish", request)
	if response.StatusCode != http.StatusConflict || !strings.Contains(response.Body, "MATERIALIZATION_FAILED") {
		t.Fatalf("publish failure=%d %s", response.StatusCode, response.Body)
	}
	if releaseCalls != 0 {
		t.Fatalf("release activation happened after materialization failure")
	}
	active, err := service.ActiveRevision(ctx, "project-a", "custom")
	if err != nil || active.ID != "prior" {
		t.Fatalf("active revision changed after failed materialization: %#v err=%v", active, err)
	}
}
