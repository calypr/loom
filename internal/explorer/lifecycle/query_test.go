package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func migrateStoredWorkspaceFixture(t *testing.T, raw []byte, catalog ws.CatalogSnapshot) ws.StoredWorkspaceMigration {
	t.Helper()
	if _, err := ws.DecodeWorkspace(raw); err == nil {
		t.Fatal("legacy fixture unexpectedly accepted by strict runtime decoder")
	}
	migrated, err := ws.MigrateStoredWorkspace(raw, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.DecodeWorkspace(migrated.CanonicalJSON); err != nil {
		t.Fatalf("offline migration did not produce strict current workspace: %v", err)
	}
	return migrated
}

func TestBuilderRequiresExplicitStoredMigrationForLegacyNilSteps(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	setup.Config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service := newTestServiceFromSetup(t, setup)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		if workspace.Documents[0].Columns[index].ColumnID == "" {
			workspace.Documents[0].Columns[index].ColumnID = workspace.Documents[0].Columns[index].Column
		}
	}
	workspace.Documents[0].Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}}
	current, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(current, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	if bytes.Equal(current, legacy) {
		t.Fatal("fixture did not install legacy nil steps")
	}
	migration := migrateStoredWorkspaceFixture(t, legacy, service.core.Catalog(snapshot, store.created.ExplorerID))
	if migration.OriginalNilConstructionStepLists != 1 || migration.OriginalLegacyNilStepsDigest == "" {
		t.Fatalf("missing exact nil-step migration proof: %#v", migration)
	}
	store.created.DraftConfig, store.created.DraftDigest = legacy, migration.OriginalLegacyNilStepsDigest
	beforeVersion := store.created.DraftVersion
	receiptBefore, err := json.Marshal(store.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID}); lifecycleErrorCode(err) != "DRAFT_STATE_INVALID" {
		t.Fatalf("legacy Builder read=%v, want DRAFT_STATE_INVALID without lazy write", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || !bytes.Equal(store.created.DraftConfig, legacy) {
		t.Fatal("Builder lazily rewrote a legacy draft")
	}

	store.created.DraftConfig, store.created.DraftDigest = migration.CanonicalJSON, migration.MigratedCanonicalDigest
	for i := 0; i < 2; i++ {
		state, err := service.Builder(context.Background(), BuilderRequest{Project: store.created.Project, ExplorerID: store.created.ExplorerID})
		if err != nil {
			t.Fatal(err)
		}
		if state.Workspace == nil || state.Workspace.Documents[0].Construction.Steps == nil {
			t.Fatal("migrated Builder returned null construction steps")
		}
	}
	receiptAfter, err := json.Marshal(store.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || !bytes.Equal(receiptBefore, receiptAfter) {
		t.Fatal("repeat migrated Builder read wrote owner or changed immutable receipt")
	}
}

func TestLegacyReceiptRemainsReadableButCannotPreviewOrPublish(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	historical, err := ws.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	historical.SemanticsVersion = ws.CurrentSemanticsVersion - 1
	receipt.NormalizedBundle, err = historical.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	receipt.IntentDigest, err = historical.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = receiptmodel.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatalf("historical receipt original identity is invalid: %v", err)
	}
	original, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	previewCalls, materializeCalls, releaseCalls, sinkCalls := 0, 0, 0, 0
	config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		return dataframeexecution.PreviewSummary{}, nil
	}
	config.MaterializeReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) (ports.Execution, error) {
		materializeCalls++
		return ports.Execution{}, nil
	}
	config.ValidateReleaseGeneration = func(context.Context, string, string) error { return nil }
	config.PrepareRelease = func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		releaseCalls++
		return dataset.ProjectRelease{}, 0, nil
	}
	service := newTestService(t, store, config)
	_, err = service.Preview(context.Background(), PreviewRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, OutputID: "patients", Limit: 10, SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		sinkCalls++
		return func(map[string]any) error { return nil }, nil
	}})
	if lifecycleErrorCode(err) != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("legacy Preview error=%v, want RECEIPT_RECOMPILE_REQUIRED", err)
	}
	_, err = service.Publish(context.Background(), PublishRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, Actor: "alice"})
	if lifecycleErrorCode(err) != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("legacy Publish error=%v, want RECEIPT_RECOMPILE_REQUIRED", err)
	}
	if previewCalls != 0 || materializeCalls != 0 || releaseCalls != 0 || sinkCalls != 0 || store.published {
		t.Fatalf("legacy receipt reached execution/activation: preview=%d materialize=%d release=%d sink=%d published=%v", previewCalls, materializeCalls, releaseCalls, sinkCalls, store.published)
	}

	raw, err := service.store.CompilationReceiptForExplorer(context.Background(), receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.core.ValidateReceiptRoute(raw, receipt.Project, receipt.ExplorerID); err != nil {
		t.Fatalf("historical raw artifact admission failed: %v", err)
	}
	if err := raw.ValidateID(); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("execution rejection or raw lookup mutated historical receipt")
	}
}

func TestImportedSourceRefreshSavesReloadsAndRejectsStaleOrInvalidEdit(t *testing.T) {
	ctx := context.Background()
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	workspace, err := recipeimport.ImportWorkspace(bundle, config.Capability.Catalog(snapshot, "patients"), map[string][]recipe.StageColumn{"patients": {{ID: "patient-id", Name: "id", Label: "ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].ImportedSource = nil
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.EditImportedSource = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document, change *authoringv2.ImportedSourceStepChange) (ws.Document, error) {
		plan := recipeimport.SourcePlan(output)
		if change != nil {
			edited, err := recipeimport.EditPlan(output, plan, ws.CatalogSnapshot{}, recipeimport.PlanEdit{
				StepChanges: []authoringv2.ImportedSourceStepChange{*change},
			}, recipeimport.PlanEditInput{})
			if err != nil {
				return document, err
			}
			plan = edited
		}
		document.ImportedSource = &plan
		return document, nil
	}
	compileFailure := false
	config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		if compileFailure {
			return nil, errors.New("downstream column would be missing")
		}
		request.ResolvedInputs.ImportedRecipes = []explorercompilation.ResolvedImportedRecipe{{Reference: *request.Workspace.Documents[0].ImportedRecipe, Bundle: bundle, ResolvedBundle: bundle,
			SourceColumns: []explorercompilation.ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "patient-id", Name: "id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "root.id"}}}}
		receipt, err := compileTraceReceipt(ctx, request, snapshot, config.Capability.Catalog(snapshot, request.ExplorerID))
		store.receipt = receipt
		return receipt, err
	}
	service := newTestService(t, store, config)
	request := authoringv2.ApplyCommandsRequest{CommandID: "refresh-source", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandRefreshImportedSource, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{}}}}
	response, err := service.ApplyCommands(ctx, "project-a", "patients", request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if response.DraftVersion != 2 || response.Workspace.Documents[0].ImportedSource == nil || store.saveDraftCalls != 1 {
		t.Fatalf("refresh not saved: %#v", response)
	}
	reloadedService := newTestService(t, store, config)
	reloaded, err := reloadedService.Builder(ctx, BuilderRequest{Project: "project-a", ExplorerID: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Workspace == nil || reloaded.Workspace.Documents[0].ImportedSource == nil || reloaded.DraftDigest != response.DraftDigest {
		t.Fatal("source steps lost on fresh Builder load")
	}
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "stale-source", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Commands: request.Commands}, "tester"); err == nil {
		t.Fatal("stale source edit was accepted")
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	compileFailure = true
	plan := response.Workspace.Documents[0].ImportedSource
	if !lifecycleHasSourceStep(plan, "fields") {
		t.Fatal("fixture requires source fields before removal")
	}
	change := authoringv2.Command{Type: authoringv2.CommandUpdateImportedSourceStep, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Remove: true}}}
	_, err = service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "broken-source", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: response.DraftVersion, ExpectedDraftDigest: response.DraftDigest, Commands: []authoringv2.Command{change}}, "tester")
	if err == nil || store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatalf("invalid downstream edit changed saved draft: %v", err)
	}
}
