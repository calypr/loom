package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

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
	workspace.Documents[0].ImportedSource = nil // An existing saved draft predates source steps.
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.EditImportedSource = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
		plan := recipeimport.SourcePlan(output)
		if change != nil {
			for i := range plan.Steps {
				if plan.Steps[i].ID == change.StepID {
					plan.Steps[i].Enabled = change.Enabled
				}
			}
		}
		document.ImportedSource = &plan
		return document, nil
	}
	compileFailure := false
	config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
	request := authoringv2.ApplyCommandsRequest{CommandID: "refresh-source", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandRefreshImportedSource, OutputID: "patients"}}}
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
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "stale-source", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Commands: request.Commands}, "tester"); err == nil {
		t.Fatal("stale source edit was accepted")
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	compileFailure = true
	plan := response.Workspace.Documents[0].ImportedSource
	change := authoringv2.Command{Type: authoringv2.CommandUpdateImportedSourceStep, OutputID: "patients", SourceStep: &authoringv2.ImportedSourceStepChange{StepID: plan.Steps[0].ID, Enabled: false}}
	_, err = service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "broken-source", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: response.DraftVersion, ExpectedDraftDigest: response.DraftDigest, Commands: []authoringv2.Command{change}}, "tester")
	if err == nil || store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatalf("invalid downstream edit changed saved draft: %v", err)
	}
}
