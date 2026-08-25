package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/gofiber/fiber/v3"
)

func TestCompiledExplorerConfigV2UsesCompilerOwnedPresentation(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "compiled", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_one", Expr: recipe.Expression{Select: "root.id"}}}}}}
	compiled := explorercompilation.Result{
		Bundle:         bundle,
		EmittedColumns: []explorercompilation.EmittedColumn{{EmissionID: "em_one", OutputID: "patients", PublicColumn: "c_one"}},
		Presentation:   explorercompilation.PresentationConfig{OutputID: "patients", Title: "Patients", Columns: []explorercompilation.PresentationColumn{{EmissionID: "em_one", PublicColumn: "c_one", Label: "Patient ID", Visible: true}}},
	}
	raw, err := compiledExplorerConfigV2("project-a", "custom", compiled)
	if err != nil {
		t.Fatal(err)
	}
	config, decoded, err := explorer.DecodeInteractiveConfigV2(raw, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Name != bundle.Name || len(config.Views) != 1 || config.Views[0].Table.Columns[0].Label != "Patient ID" {
		t.Fatalf("compiled config = %#v, recipe = %#v", config, decoded)
	}
}

func TestNativeV2RouteUsesAuthorizedPersistedReceipt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	executionScope := scope
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "custom", "Custom", "test"); err != nil {
		t.Fatal(err)
	}
	compileCalls := 0
	previewCalls := 0
	config := ExplorerV2LifecycleConfig{
		Capability:      func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		CapabilityToken: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		AuthorizedCapabilityCompile: func(_ context.Context, _, token string) (AuthorizedCapability, error) {
			if err := snapshot.ValidateToken(token); err != nil {
				return AuthorizedCapability{}, err
			}
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		AuthorizedCapabilityExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: executionScope}, nil
		},
		ReceiptLookup: service.CompilationReceiptForExplorer,
		CompileReceipt: func(ctx context.Context, request ExplorerV2ReceiptCompileRequest) (*explorer.CompilationReceipt, error) {
			compileCalls++
			if request.Authorized.Snapshot.Token != snapshot.Token || !request.Authorized.Scope.Unrestricted() {
				t.Fatalf("compile lost authorized capability: %#v", request.Authorized)
			}
			return persistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (engine.PreviewSummary, error) {
			previewCalls++
			if receipt == nil || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted || bindings.IncludeAuthResourcePath {
				t.Fatalf("preview bindings widened or requested publication metadata: receipt=%#v bindings=%#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return engine.PreviewSummary{}, err
			}
			return engine.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe", PlanFingerprint: "test", TraversalCount: 0}, nil
		},
	}
	app := fiber.New()
	RegisterExplorerAuthoringV2Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	body := `{"document":{"apiVersion":"` + authoringv2.APIVersion + `","kind":"` + authoringv2.Kind + `","output":{"id":"patients","title":"Patients"},"rootNodeId":"n_patient","routeSteps":[],"selections":[{"candidateId":"c_patient_id","occurrenceId":"base","projectionMode":"FIRST"}],"presentation":{}},"snapshotToken":"` + snapshot.Token + `"}`
	compiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/compile", body)
	if compiled.StatusCode != http.StatusOK {
		t.Fatalf("compile status=%d body=%s", compiled.StatusCode, compiled.Body)
	}
	var result struct {
		ReceiptID string `json:"receiptId"`
	}
	if err := json.Unmarshal([]byte(compiled.Body), &result); err != nil {
		t.Fatal(err)
	}
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, preview.Body)
	}
	if compileCalls != 1 || previewCalls != 1 {
		t.Fatalf("compile calls=%d preview calls=%d", compileCalls, previewCalls)
	}
	unknown := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"missing","limit":5}`)
	if unknown.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body, `"code":"UNKNOWN_AUTHORING_OUTPUT"`) || previewCalls != 1 {
		t.Fatalf("unknown output status=%d calls=%d body=%s", unknown.StatusCode, previewCalls, unknown.Body)
	}
	for _, path := range []string{
		"/api/v1/projects/project-b/explorers/custom/authoring/v2/preview",
		"/api/v1/projects/project-a/explorers/other/authoring/v2/preview",
	} {
		foreign := requestJSON(t, app, http.MethodPost, path, `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
		if foreign.StatusCode != http.StatusNotFound || !strings.Contains(foreign.Body, `"code":"RECEIPT_NOT_FOUND"`) || previewCalls != 1 {
			t.Fatalf("foreign receipt path=%s status=%d calls=%d body=%s", path, foreign.StatusCode, previewCalls, foreign.Body)
		}
	}
	executionScope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	widened := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
	if widened.StatusCode != http.StatusConflict || !strings.Contains(widened.Body, `"code":"RECEIPT_INPUT_UNAVAILABLE"`) || previewCalls != 1 {
		t.Fatalf("scope mismatch status=%d calls=%d body=%s", widened.StatusCode, previewCalls, widened.Body)
	}
	stats, err := service.CompilationReceiptStats(context.Background(), "project-a")
	if err != nil || stats.Count != 1 {
		t.Fatalf("receipt stats=%+v err=%v", stats, err)
	}
}

func TestBuilderReadRestoresPublishedNativeV2Document(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "custom", "Custom", "test"); err != nil {
		t.Fatal(err)
	}
	document := authoringv2.Document{APIVersion: authoringv2.APIVersion, Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootNodeID: "n_patient", RouteSteps: []authoringv2.RouteStep{}, Selections: []authoringv2.Selection{{CandidateID: "c_patient_id", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "FIRST"}}, Presentation: map[string]authoringv2.Presentation{}}
	receipt, err := persistTestNativeReceipt(context.Background(), t, service, ExplorerV2ReceiptCompileRequest{Project: "project-a", ExplorerID: "custom", Document: document, SnapshotToken: snapshot.Token}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := service.PublishAuthoring(context.Background(), *receipt, explorer.Revision{ID: "native-v2-revision", Project: "project-a", ExplorerID: "custom", CompilationReceiptID: receipt.ID, IntentDigest: receipt.IntentDigest, AuthoringBundle: receipt.NormalizedBundle, Recipe: receipt.Bundle, RecipeDigest: receipt.RecipeDigest, ResolvedSchemaDigest: receipt.ResolvedSchemaDigest, SourceGeneration: receipt.SourceGeneration, Status: explorer.RevisionReady, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	RegisterExplorerAuthoringV2Routes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{Capability: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom/authoring/v2/builder", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder status=%d body=%s", response.StatusCode, response.Body)
	}
	var state authoringv2.BuilderState
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Document == nil || state.Document.Output.ID != document.Output.ID || state.Document.Selections[0].CandidateID != document.Selections[0].CandidateID {
		t.Fatalf("restored document = %#v", state.Document)
	}
}

func persistTestNativeReceipt(ctx context.Context, t *testing.T, service *explorer.Service, request ExplorerV2ReceiptCompileRequest, snapshot capability.Snapshot) (*explorer.CompilationReceipt, error) {
	t.Helper()
	normalized, err := request.Document.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	intentDigest, err := request.Document.Digest()
	if err != nil {
		return nil, err
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "native-test", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		return nil, err
	}
	contract := json.RawMessage(`{"outputId":"patients","columns":[{"emissionId":"em_patient","publicColumn":"c_patient","candidateId":"c_patient_id","occurrenceId":"base","logicalType":"string","filterable":true,"chartable":true}]}`)
	contractDigest, err := explorer.CompilationArtifactDigest(contract)
	if err != nil {
		return nil, err
	}
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion: explorer.CurrentReceiptFormatVersion, CompilerContractVersion: explorer.CurrentCompilerContractVersion,
		Project: snapshot.Identity.Project, ExplorerID: request.ExplorerID, IntentDigest: intentDigest, SnapshotToken: snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest,
		SourceGeneration: snapshot.Identity.Generation, RecipeDigest: bundleDigest, ResolvedRecipeDigest: bundleDigest,
		ResolvedSchemaDigest: "resolved-schema", OutputContractDigest: contractDigest,
		NormalizedBundle: normalized, Bundle: bundle, PublicOutputContract: contract,
		EmittedColumns:     []explorer.EmittedColumn{{EmissionID: "em_patient", OutputID: "patients", CandidateID: "c_patient_id", OccurrenceID: authoringv2.RootOccurrenceID, PublicColumn: "c_patient", LogicalType: "string", Filterable: true, Chartable: true}},
		OutputFingerprints: map[string]string{"patients": "fingerprint"}, CreatedAt: time.Now().UTC(),
	}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	return service.StoreCompilationReceipt(ctx, receipt)
}
