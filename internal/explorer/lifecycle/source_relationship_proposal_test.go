package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"testing"
)

func TestSourceRelationshipProposalCancelApplyReloadAndStaleDraft(t *testing.T) {
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
	raw, _ := workspace.CanonicalJSON()
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.EditImportedRelationship = func(_ context.Context, _ string, _ AuthorizedCapability, d authoringv2.Document, _ authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
		plan := recipeimport.SourcePlan(output)
		plan.Steps[0].Enabled = false
		d.ImportedSource = &plan
		return d, nil
	}
	compileFailure := false
	config.CompileReceipt = func(ctx context.Context, r CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		if compileFailure {
			return nil, errors.New("downstream dependency would be removed")
		}
		r.ResolvedInputs.ImportedRecipes = []explorercompilation.ResolvedImportedRecipe{{Reference: *r.Workspace.Documents[0].ImportedRecipe, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []explorercompilation.ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "patient-id", Name: "id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "root.id"}}}}
		receipt, err := compileTraceReceipt(ctx, r, snapshot, config.Capability.Catalog(snapshot, r.ExplorerID))
		if err != nil {
			return nil, err
		}
		receipt.SourceRelationshipProposal = cloneSourceRelationshipProposalBinding(r.SourceRelationshipProposal)
		receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
		receipt.ID, _ = explorer.ReceiptID(*receipt)
		store.receipt = receipt
		return receipt, nil
	}
	previewFailure := false
	previewCalls := 0
	config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if previewFailure {
			return dataframeexecution.PreviewSummary{}, errors.New("native source preview execution failed")
		}
		return dataframeexecution.PreviewSummary{RowCount: 3, Complete: true}, nil
	}
	service := newTestService(t, store, config)
	request := SourceRelationshipProposalRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, OutputID: "patients", Change: authoringv2.ImportedRelationshipChange{Action: "ADD", ParentOccurrenceID: "base", EdgeID: "authorized-edge", MatchMode: authoringv2.RouteMatchOptional}}
	proposal, err := service.ProposeSourceRelationship(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.SampledRows != 3 || proposal.Sampled || previewCalls != 1 {
		t.Fatalf("proposal did not execute its candidate preview: %#v", proposal)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("Preview or Cancel mutated saved source")
	}
	// A compile failure cannot create an applicable proposal or mutate the draft.
	candidateReceipt := store.receipt
	compileFailure = true
	if _, err := service.ProposeSourceRelationship(ctx, request); err == nil {
		t.Fatal("failed compile produced a proposal")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("failed proposal changed saved source")
	}
	compileFailure = false
	store.receipt = candidateReceipt
	apply := authoringv2.ApplyCommandsRequest{CommandID: "relationship-apply", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", ProposalID: proposal.ProposalID}}}
	previewFailure = true
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", apply, "tester"); err == nil {
		t.Fatal("runtime preview failure saved a source edit")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("runtime preview failure changed saved source")
	}
	previewFailure = false
	applied, err := service.ApplyCommands(ctx, "project-a", "patients", apply, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if applied.DraftVersion != 2 || store.saveDraftCalls != 1 || *applied.Workspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("Apply lost immutable source pin or draft revision")
	}
	reloaded, err := newTestService(t, store, config).Builder(ctx, BuilderRequest{Project: "project-a", ExplorerID: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Workspace == nil || reloaded.Workspace.Documents[0].ImportedSource.Steps[0].Enabled {
		t.Fatal("fresh reload lost project-local source change")
	}
	store.receipt = candidateReceipt
	apply.CommandID = "second-editor-old-preview"
	before := append([]byte(nil), store.created.DraftConfig...)
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", apply, "tester"); err == nil {
		t.Fatal("second editor silently overwrote saved source")
	}
	if !bytes.Equal(before, store.created.DraftConfig) || store.saveDraftCalls != 1 {
		t.Fatal("stale Apply changed saved source")
	}
	// Earlier source-step settings use the same receipt-backed no-write review.
	service.config.EditImportedSource = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
		if change == nil || change.StepID != "fields" || !change.Enabled {
			t.Fatal("bounded source-step intent was lost")
		}
		plan := recipeimport.SourcePlan(output)
		plan.Steps[0].Enabled = change.Enabled
		document.ImportedSource = &plan
		return document, nil
	}
	stepRequest := SourceRelationshipProposalRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest, OutputID: "patients", SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true}}
	compileFailure = true
	if _, err := service.ProposeSourceRelationship(ctx, stepRequest); err == nil {
		t.Fatal("downstream source-step failure produced a proposal")
	}
	if store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatal("failed source-step preview changed saved work")
	}
	compileFailure = false
	stepProposal, err := service.ProposeSourceRelationship(ctx, stepRequest)
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatal("source-step Preview/Cancel wrote the draft")
	}
	restored, err := service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "source-step-restore", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", ProposalID: stepProposal.ProposalID}}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 3 || store.saveDraftCalls != 2 || !restored.Workspace.Documents[0].ImportedSource.Steps[0].Enabled || *restored.Workspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("bounded source-step restore lost pin, settings, or revision")
	}

}
