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
	"reflect"
	"testing"
)

func TestSourceRelationshipProposalCancelApplyReloadAndStaleDraft(t *testing.T) {
	for _, afterRemoval := range []bool{false, true} {
		name := "without-construction"
		if afterRemoval {
			name = "after-last-step-removal"
		}
		t.Run(name, func(t *testing.T) { runSourceRelationshipProposalLifecycle(t, afterRemoval) })
	}
}

func runSourceRelationshipProposalLifecycle(t *testing.T, afterRemoval bool) {
	ctx := context.Background()
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	workspace, err := recipeimport.ImportWorkspace(bundle, config.Capability.Catalog(snapshot, "patients"), map[string][]recipe.StageColumn{"patients": {{ID: "patient-id", Name: "id", Label: "ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	if afterRemoval {
		document := workspace.Documents[0]
		column := document.Columns[0]
		document.Construction = &authoringv2.Construction{Version: 1, Steps: []authoringv2.ConstructionStep{{ID: "last-filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: column.ColumnID, Operator: authoringv2.ConstructionFilterExists}}, Outputs: []authoringv2.StageColumn{{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: "string"}}}}}
		removed, impact, err := document.ProposeStepRemoval("last-filter", nil)
		if err != nil {
			t.Fatal(err)
		}
		if removed.Construction == nil || len(removed.Construction.Steps) != 0 || len(impact.RemovedStepIDs) != 1 {
			t.Fatal("last operation removal did not retain an explicit empty construction")
		}
		workspace.Documents[0] = removed
	}
	workspace.Documents = append(workspace.Documents, authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "other", Title: "Unrelated"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{{ColumnID: "other-project", Column: "project", Label: "Project", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}}}})
	workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: "other-tab", OutputID: "other", Title: "Unrelated", Order: 1, Visible: true})
	raw, _ := workspace.CanonicalJSON()
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.EditImportedRelationship = func(_ context.Context, _ string, _ AuthorizedCapability, d authoringv2.Document, _ authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
		plan := recipeimport.SourcePlan(output)
		setLifecycleSourceStepEnabled(t, &plan, "fields", false)
		d.ImportedSource = &plan
		return d, nil
	}
	config.AuthoringNetwork = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
		network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
		network.Occurrences = append(network.Occurrences, authoringv2.NetworkOccurrence{OccurrenceID: "base", ResourceType: "Patient", Enabled: true, PreservesRowIdentity: true})
		return network, nil
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
	config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if previewFailure {
			return dataframeexecution.PreviewSummary{}, errors.New("native source preview execution failed")
		}
		for i := 0; i < 3; i++ {
			if err := visit(map[string]any{"value": i}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
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
	if proposal.NativePreview == nil || proposal.NativePreview.Receipt.ID != proposal.CandidateReceiptID || proposal.NativePreview.Summary.RowCount != 3 {
		t.Fatal("proposal lost its exact native preview")
	}
	for _, failure := range []string{"none", "factory", "row"} {
		t.Run("transport-sink-"+failure, func(t *testing.T) {
			requestWithSink := request
			requestWithSink.IncludePreview = true
			rows := 0
			beforeCalls := previewCalls
			sentinel := errors.New("transport sink failure")
			requestWithSink.PreviewSinkFactory = func(receipt *explorer.CompilationReceipt, _ []explorer.EmittedColumn) (func(map[string]any) error, error) {
				if receipt.ID != proposal.CandidateReceiptID {
					t.Fatal("sink received another receipt")
				}
				if failure == "factory" {
					return nil, sentinel
				}
				return func(map[string]any) error {
					rows++
					if failure == "row" {
						return sentinel
					}
					return nil
				}, nil
			}
			got, err := service.ProposeSourceRelationship(ctx, requestWithSink)
			if failure != "none" {
				if !errors.Is(err, sentinel) || got.ProposalID != "" {
					t.Fatalf("sink failure produced proposal: %v", err)
				}
				return
			}
			if err != nil || rows != 3 || previewCalls-beforeCalls != 1 || got.NativePreview == nil {
				t.Fatalf("native execution was not reused exactly once: rows=%d calls=%d err=%v", rows, previewCalls-beforeCalls, err)
			}
		})
	}
	if proposal.CandidateDocument.Output.ID != request.OutputID || proposal.CandidateDocument.ImportedSource == nil || lifecycleSourceStepEnabled(t, proposal.CandidateDocument.ImportedSource, "fields") || proposal.CandidateNetwork.OutputID != request.OutputID || proposal.CandidateRowIdentity.OutputID != request.OutputID {
		t.Fatalf("candidate projection lost the proposed source edit: %#v", proposal)
	}
	if *proposal.CandidateDocument.ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("candidate projection changed the immutable pin")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("Preview or Cancel mutated saved source")
	}

	projectNetwork := service.config.AuthoringNetwork
	service.config.AuthoringNetwork = func(context.Context, string, AuthorizedCapability, authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
		return authoringv2.AuthoringNetwork{}, errors.New("candidate source provenance is unavailable")
	}
	if failed, err := service.ProposeSourceRelationship(ctx, request); err == nil || failed.ProposalID != "" {
		t.Fatal("failed candidate network produced a ready proposal")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("failed candidate projection changed saved source")
	}
	service.config.AuthoringNetwork = projectNetwork

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

	forgedWorkspace, err := authoringv2.DecodeWorkspace(candidateReceipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	forgedWorkspace.Documents[1].Output.Title = "Forged unrelated title"
	forgedBinding := *candidateReceipt.SourceRelationshipProposal
	forgedBinding.CandidateWorkspaceDigest, err = forgedWorkspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	forgedReceipt, err := config.CompileReceipt(ctx, CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: forgedWorkspace, SnapshotToken: snapshot.Token, SourceRelationshipProposal: &forgedBinding})
	if err != nil {
		t.Fatal(err)
	}
	forgedApply := apply
	forgedApply.CommandID = "forged-other-output"
	forgedApply.Commands = []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", ProposalID: forgedReceipt.ID}}
	_, err = service.ApplyCommands(ctx, "project-a", "patients", forgedApply, "tester")
	var rejected *Error
	if !errors.As(err, &rejected) || rejected.Code != "INVALID_SOURCE_RELATIONSHIP_PROPOSAL" {
		t.Fatalf("unrelated output forgery was not rejected by whole-workspace validation: %v", err)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("forged receipt changed saved authoring work")
	}
	store.receipt = candidateReceipt
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
	if applied.Workspace.Documents[1].Output.Title != "Unrelated" {
		t.Fatal("source Apply changed unrelated output")
	}
	if applied.DraftVersion != 2 || store.saveDraftCalls != 1 || *applied.Workspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("Apply lost immutable source pin or draft revision")
	}
	reloaded, err := newTestService(t, store, config).Builder(ctx, BuilderRequest{Project: "project-a", ExplorerID: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Workspace == nil || lifecycleSourceStepEnabled(t, reloaded.Workspace.Documents[0].ImportedSource, "fields") {
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

	service.config.EditImportedSource = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
		if change == nil || change.StepID != "fields" || !change.Enabled {
			t.Fatal("bounded source-step intent was lost")
		}
		plan := recipeimport.SourcePlan(output)
		setLifecycleSourceStepEnabled(t, &plan, "fields", change.Enabled)
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
	if restored.DraftVersion != 3 || store.saveDraftCalls != 2 || !lifecycleSourceStepEnabled(t, restored.Workspace.Documents[0].ImportedSource, "fields") || *restored.Workspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("bounded source-step restore lost pin, settings, or revision")
	}

	parameters := &authoringv2.ImportedSourceParameters{Dynamic: &authoringv2.ImportedDynamicParameters{
		Name: "identifiers", Source: recipe.Expression{Select: "identifier[]"},
		Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"},
	}}
	service.config.CreateImportedSourceRule = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document, rule authoringv2.SourceRuleCreate) (authoringv2.Document, error) {
		if rule.Kind != "DYNAMIC_COLUMN" || rule.Parameters.Dynamic.Name != "identifiers" {
			t.Fatalf("unexpected source rule intent: %#v", rule)
		}
		plan, err := recipeimport.EditPlan(output, *document.ImportedSource, authoringv2.CatalogSnapshot{}, recipeimport.PlanEdit{RuleCreate: &rule}, recipeimport.PlanEditInput{})
		if err == nil {
			document.ImportedSource = &plan
		}
		return document, err
	}
	ruleRequest := stepRequest
	ruleRequest.ExpectedDraftVersion, ruleRequest.ExpectedDraftDigest = restored.DraftVersion, restored.DraftDigest
	ruleRequest.SourceStep = nil
	ruleRequest.SourceRule = &authoringv2.SourceRuleCreate{Kind: "DYNAMIC_COLUMN", Parameters: parameters}
	rawBeforeRule := append([]byte(nil), store.created.DraftConfig...)
	ruleProposal, err := service.ProposeSourceRelationship(ctx, ruleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ruleProposal.AffectedColumns) != 0 || !bytes.Equal(rawBeforeRule, store.created.DraftConfig) {
		t.Fatal("rule preview changed the draft or widened affected columns")
	}
	applyRule := func(version int64, digest, proposalID, commandID string) *authoringv2.ApplyCommandsResponse {
		t.Helper()
		result, err := service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{
			CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", ProposalID: proposalID}},
		}, "tester")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	savedRule := applyRule(restored.DraftVersion, restored.DraftDigest, ruleProposal.ProposalID, "create-rule")
	additions := savedRule.Workspace.Documents[0].ImportedSource.RuleAdditions
	if len(additions) != 1 || !additions[0].Enabled {
		t.Fatal("Apply did not save the enabled rule")
	}

	service.config.EditImportedSource = func(_ context.Context, _ string, _ AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
		plan, err := recipeimport.EditPlan(output, *document.ImportedSource, authoringv2.CatalogSnapshot{}, recipeimport.PlanEdit{
			StepChanges: []authoringv2.ImportedSourceStepChange{*change},
		}, recipeimport.PlanEditInput{})
		if err == nil {
			document.ImportedSource = &plan
		}
		return document, err
	}
	editedParams := *parameters
	editedDynamic := *parameters.Dynamic
	prefix := "external"
	editedDynamic.ColumnPrefix = &prefix
	editedParams.Dynamic = &editedDynamic
	editRequest := ruleRequest
	editRequest.ExpectedDraftVersion, editRequest.ExpectedDraftDigest = savedRule.DraftVersion, savedRule.DraftDigest
	editRequest.SourceRule = nil
	editRequest.SourceStep = &authoringv2.ImportedSourceStepChange{StepID: additions[0].StepID, Enabled: true, Parameters: &editedParams}
	rawBeforeEdit := append([]byte(nil), store.created.DraftConfig...)
	editProposal, err := service.ProposeSourceRelationship(ctx, editRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawBeforeEdit, store.created.DraftConfig) {
		t.Fatal("rule edit preview changed the saved draft")
	}
	editedRule := applyRule(savedRule.DraftVersion, savedRule.DraftDigest, editProposal.ProposalID, "edit-rule")
	got := editedRule.Workspace.Documents[0].ImportedSource.RuleAdditions
	if len(got) != 1 || got[0].StepID != additions[0].StepID || !reflect.DeepEqual(got[0].Parameters.Dynamic.ColumnPrefix, &prefix) {
		t.Fatalf("rule edit did not retain identity or save parameters: %#v", got)
	}

}

func setLifecycleSourceStepEnabled(t *testing.T, plan *authoringv2.ImportedSourcePlan, id string, enabled bool) {
	t.Helper()
	for i := range plan.Steps {
		if plan.Steps[i].ID == id {
			plan.Steps[i].Enabled = enabled
			return
		}
	}
	t.Fatalf("missing source step %s", id)
}
func lifecycleSourceStepEnabled(t *testing.T, plan *authoringv2.ImportedSourcePlan, id string) bool {
	t.Helper()
	if plan != nil {
		for _, step := range plan.Steps {
			if step.ID == id {
				return step.Enabled
			}
		}
	}
	t.Fatalf("missing source step %s", id)
	return false
}
