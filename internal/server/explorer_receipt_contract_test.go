package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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

func TestCompileValidatedReceiptResolutionChecksAllOutputsForScopedPreview(t *testing.T) {
	recipeEngine, err := engine.New(engine.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "multi-output-preview",
		TranslationVersion:  explorercompilation.TranslationVersion,
		Outputs: []recipe.Output{
			{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
				{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "patient_gender", Expr: recipe.Expression{Select: "root.gender"}},
			}},
			{Name: "specimens", RootResourceType: "Specimen", RowGrain: "resource", Fields: []recipe.Field{{Name: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}}},
		},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{
		Bundle:               bundle,
		RecipeDigest:         compiled.StoredRecipeDigest,
		ResolvedRecipeDigest: resolvedDigest,
		ResolvedSchemaDigest: compiled.ResolvedSchemaDigest,
		OutputFingerprints:   resolvedOutputFingerprints(compiled),
		EmittedColumns: []explorer.EmittedColumn{
			// Presentation order is intentionally different from compiler field
			// order. It must not invalidate an otherwise identical receipt.
			{OutputID: "patients", PublicColumn: "patient_gender"},
			{OutputID: "patients", PublicColumn: "patient_id"},
			{OutputID: "specimens", PublicColumn: "specimen_id"},
		},
	}
	previewBindings := bindings
	previewBindings.OutputNames = []string{"patients"}
	resolved, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, receipt, previewBindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Compiled.Outputs) != 2 {
		t.Fatalf("validated outputs=%d, want complete receipt with 2 outputs", len(resolved.Compiled.Outputs))
	}
	if len(previewBindings.OutputNames) != 1 || previewBindings.OutputNames[0] != "patients" {
		t.Fatalf("preview execution selection was mutated: %#v", previewBindings.OutputNames)
	}
}

func TestCompiledExplorerWorkspaceConfigUsesIndependentStablePresentationOrders(t *testing.T) {
	compiled := explorercompilation.WorkspaceResult{
		Workspace: authoringv2.Workspace{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
			Explorer:  authoringv2.ExplorerMetadata{Title: "Stable"},
			Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Out"}}},
			Tabs:      []authoringv2.Tab{{ID: "tab", Title: "Out", OutputID: "out", Order: 0, Visible: true}},
		},
		Bundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Outputs: []recipe.Output{{Name: "out"}}},
		EmittedColumns: []explorercompilation.EmittedColumn{
			{EmissionID: "column_b", OutputID: "out", PublicColumn: "column_b"},
			{EmissionID: "column_a", OutputID: "out", PublicColumn: "column_a"},
		},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: "out", Columns: []explorercompilation.PresentationColumn{
			{EmissionID: "column_b", PublicColumn: "column_b", Label: "B", Visible: false, Order: 0, FilterLabel: "B", FilterOrder: 0, ChartType: "bar", ChartOrder: 0},
			{EmissionID: "column_a", PublicColumn: "column_a", Label: "A", Visible: true, Order: 0, FilterLabel: "A", FilterOrder: 0, ChartType: "line", ChartOrder: 1},
		}}},
	}
	raw, err := compiledExplorerWorkspaceConfigV2("project-a", "explorer-a", compiled)
	if err != nil {
		t.Fatal(err)
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	view := config.Views[0]
	if view.Table.Columns[0].Column != "column_a" || view.Table.Columns[1].Column != "column_b" {
		t.Fatalf("table order=%#v", view.Table.Columns)
	}
	if view.Filters[0].Column != "column_a" || view.Filters[1].Column != "column_b" {
		t.Fatalf("filter order=%#v", view.Filters)
	}
	if view.Table.Columns[1].Visible || view.Filters[1].Column != view.Table.Columns[1].Column {
		t.Fatalf("hidden filter-only column was not preserved: table=%#v filters=%#v", view.Table.Columns, view.Filters)
	}
	if view.Charts[0].Column != "column_b" || view.Charts[1].Column != "column_a" {
		t.Fatalf("chart order=%#v", view.Charts)
	}
}

func TestNativeV2RouteUsesAuthorizedPersistedReceipt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	executionScope := scope
	service, err := explorer.NewService(newTestExplorerStore())
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
	registerTestExplorerAuthoringRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	body := `{"workspace":` + string(baselineExplorerWorkspaceV2()) + `,"snapshotToken":"` + snapshot.Token + `"}`
	compiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/builder", body)
	if compiled.StatusCode != http.StatusOK {
		t.Fatalf("compile status=%d body=%s", compiled.StatusCode, compiled.Body)
	}
	ownerAfterCompile, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if ownerAfterCompile.ActiveRevisionID != "" || len(ownerAfterCompile.DraftConfig) != 0 {
		t.Fatalf("compile mutated active or draft state: %#v", ownerAfterCompile)
	}
	var result struct {
		ReceiptID       string `json:"receiptId"`
		CompilerVersion string `json:"compilerVersion"`
	}
	if err := json.Unmarshal([]byte(compiled.Body), &result); err != nil {
		t.Fatal(err)
	}
	wantCompilerVersion := explorer.CurrentCompilerContractVersion + "+" + explorercompilation.TranslationVersion
	if result.CompilerVersion != wantCompilerVersion {
		t.Fatalf("compilerVersion=%q, want %q", result.CompilerVersion, wantCompilerVersion)
	}
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, preview.Body)
	}
	ownerAfterPreview, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if ownerAfterPreview.ActiveRevisionID != "" || len(ownerAfterPreview.DraftConfig) != 0 {
		t.Fatalf("preview mutated active or draft state: %#v", ownerAfterPreview)
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
		if foreign.StatusCode != http.StatusNotFound || !strings.Contains(foreign.Body, `"code":"COMPILE_RECEIPT_NOT_FOUND"`) || previewCalls != 1 {
			t.Fatalf("foreign receipt path=%s status=%d calls=%d body=%s", path, foreign.StatusCode, previewCalls, foreign.Body)
		}
	}
	executionScope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	widened := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+result.ReceiptID+`","outputId":"patients","limit":5}`)
	if widened.StatusCode != http.StatusConflict || !strings.Contains(widened.Body, `"code":"RECEIPT_STALE"`) || previewCalls != 1 {
		t.Fatalf("scope mismatch status=%d calls=%d body=%s", widened.StatusCode, previewCalls, widened.Body)
	}
}

func TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEmptyInteractive(context.Background(), "project-a", "custom", "Custom", "test"); err != nil {
		t.Fatal(err)
	}
	config := ExplorerV2LifecycleConfig{
		Capability:      func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		CapabilityToken: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		AuthorizedCapabilityCompile: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		CompileReceipt: func(ctx context.Context, request ExplorerV2ReceiptCompileRequest) (*explorer.CompilationReceipt, error) {
			return persistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
	}
	app := fiber.New()
	registerTestExplorerAuthoringRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)

	commandBody := `{"commandId":"browser-command-1","snapshotToken":"` + snapshot.Token + `","expectedDraftVersion":0,"commands":[{"type":"CREATE_TABLE","title":"Patients","rootNodeId":"n_patient"}]}`
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if created.StatusCode != http.StatusOK {
		t.Fatalf("command status=%d body=%s", created.StatusCode, created.Body)
	}
	if !strings.Contains(created.Body, `"columns":[]`) {
		t.Fatalf("command response omitted required empty columns array: %s", created.Body)
	}
	var response authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(created.Body), &response); err != nil {
		t.Fatal(err)
	}
	if response.DraftVersion != 1 || len(response.Workspace.Documents) != 1 || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(response.Workspace.Documents[0].Output.ID) {
		t.Fatalf("command response=%#v", response)
	}
	replayed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if replayed.StatusCode != http.StatusOK || !strings.Contains(replayed.Body, `"draftVersion":1`) {
		t.Fatalf("replay status=%d body=%s", replayed.StatusCode, replayed.Body)
	}
	conflicting := strings.Replace(commandBody, `"Patients"`, `"Different"`, 1)
	conflict := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", conflicting)
	if conflict.StatusCode != http.StatusConflict || !strings.Contains(conflict.Body, `"code":"COMMAND_ID_CONFLICT"`) {
		t.Fatalf("command ID conflict status=%d body=%s", conflict.StatusCode, conflict.Body)
	}
	reconcileBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q}`, snapshot.Token, response.DraftVersion, response.DraftDigest)
	reconciled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/reconcile", reconcileBody)
	if reconciled.StatusCode != http.StatusOK || !strings.Contains(reconciled.Body, `"kind":"ExplorerBuilderReceipt"`) {
		t.Fatalf("reconcile status=%d body=%s", reconciled.StatusCode, reconciled.Body)
	}
}

func TestCreateExplorerFromCurrentClonesWorkspaceOnServer(t *testing.T) {
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.CreateInteractive(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "source", Title: "Source", ManagementMode: explorer.ManagementInteractive, DraftConfig: canonical, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerTestExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Cloned Explorer","title":"Cloned Explorer","sourceExplorerId":"source"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", created.StatusCode, created.Body)
	}
	clone, err := service.Get(context.Background(), "project-a", explorer.StableExplorerID("Cloned Explorer"))
	if err != nil {
		t.Fatal(err)
	}
	clonedWorkspace, err := authoringv2.DecodeWorkspace(clone.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if clone.DraftVersion != 1 || clonedWorkspace.Explorer.Title != "Cloned Explorer" || len(clonedWorkspace.Documents) != len(workspace.Documents) || clonedWorkspace.Documents[0].Output.ID != workspace.Documents[0].Output.ID {
		t.Fatalf("cloned Explorer=%#v workspace=%#v", clone, clonedWorkspace)
	}
}

func persistTestNativeReceipt(ctx context.Context, t *testing.T, service *explorer.Service, request ExplorerV2ReceiptCompileRequest, snapshot capability.Snapshot) (*explorer.CompilationReceipt, error) {
	t.Helper()
	workspace := request.Workspace
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "native-test", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		return nil, err
	}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"c_patient","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
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
		NormalizedBundle: normalized, Bundle: bundle, CompiledConfig: json.RawMessage(`{}`), PublicOutputContract: contract,
		EmittedColumns:     []explorer.EmittedColumn{{EmissionID: "em_patient", OutputID: "patients", CandidateID: "c_patient_id", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "FIRST", PublicColumn: "c_patient", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: true}},
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
