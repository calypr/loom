package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	proposalpkg "github.com/calypr/loom/internal/explorer/proposal"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
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
		document.Construction = &ws.Construction{Version: 1, Steps: []ws.ConstructionStep{{ID: "last-filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: column.ColumnID, Operator: ws.ConstructionFilterExists}}, Outputs: []ws.StageColumn{{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: "string"}}}}}
		removed, impact, err := document.ProposeStepRemoval("last-filter", nil)
		if err != nil {
			t.Fatal(err)
		}
		if removed.Construction == nil || len(removed.Construction.Steps) != 0 || len(impact.RemovedStepIDs) != 1 {
			t.Fatal("last operation removal did not retain an explicit empty construction")
		}
		workspace.Documents[0] = removed
	}
	workspace.Documents = append(workspace.Documents, ws.Document{Kind: ws.Kind, Output: ws.Output{ID: "other", Title: "Unrelated"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{{ColumnID: "other-project", Column: "project", Label: "Project", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}}}})
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "other-tab", OutputID: "other", Title: "Unrelated", Order: 1, Visible: true})
	raw, _ := workspace.CanonicalJSON()
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.EditImportedRelationship = func(_ context.Context, _ string, _ ports.AuthorizedCapability, d ws.Document, _ authoringv2.ImportedRelationshipChange) (ws.Document, error) {
		plan := recipeimport.SourcePlan(output)
		d.ImportedSource = &plan
		return d, nil
	}
	networkFailure := false
	config.AuthoringNetwork = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document) (authoringnetwork.AuthoringNetwork, error) {
		if networkFailure {
			return authoringnetwork.AuthoringNetwork{}, errors.New("candidate source provenance is unavailable")
		}
		network := authoringnetwork.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
		network.Occurrences = append(network.Occurrences, authoringnetwork.NetworkOccurrence{OccurrenceID: "base", ResourceType: "Patient", Enabled: true, PreservesRowIdentity: true})
		return network, nil
	}
	compileFailure := false
	config.CompileReceipt = func(ctx context.Context, r ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		if compileFailure {
			return nil, errors.New("downstream dependency would be removed")
		}
		r.ResolvedInputs.ImportedRecipes = []explorercompilation.ResolvedImportedRecipe{{Reference: *r.Workspace.Documents[0].ImportedRecipe, Bundle: bundle, ResolvedBundle: bundle, SourceColumns: []explorercompilation.ResolvedImportedSourceColumn{{Column: recipe.StageColumn{ID: "patient-id", Name: "id", Type: "string"}, Cardinality: "required_one", Shape: "scalar", SemanticPath: "root.id"}}}}
		receipt, err := compileTraceReceipt(ctx, r, snapshot, config.Capability.Catalog(snapshot, r.ExplorerID))
		if err != nil {
			return nil, err
		}
		receipt.SourceRelationshipProposal = cloneSourceRelationshipProposalBinding(r.SourceRelationshipProposal)
		receipt.CompilationKey, _ = receiptmodel.CompilationKey(*receipt)
		receipt.ID, _ = receiptmodel.ReceiptID(*receipt)
		store.receipt = receipt
		return receipt, nil
	}
	previewFailure := false
	previewCalls := 0
	config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	removingSourceStep := true
	config.EditImportedSource = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document, change *authoringv2.ImportedSourceStepChange) (ws.Document, error) {
		if removingSourceStep && (change == nil || change.StepID != "fields" || !change.Remove) {
			t.Fatal("source removal intent was lost")
		}
		plan, err := recipeimport.EditPlan(output, *document.ImportedSource, ws.CatalogSnapshot{},
			recipeimport.PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{*change}}, recipeimport.PlanEditInput{})
		if err == nil {
			document.ImportedSource = &plan
		}
		return document, err
	}
	config.CreateImportedSourceRule = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document, rule ws.SourceRuleCreate) (ws.Document, error) {
		if rule.Kind != "DYNAMIC_COLUMN" || rule.Parameters.Dynamic.Name != "identifiers" {
			t.Fatalf("unexpected source rule intent: %#v", rule)
		}
		plan, err := recipeimport.EditPlan(output, *document.ImportedSource, ws.CatalogSnapshot{}, recipeimport.PlanEdit{RuleCreate: &rule}, recipeimport.PlanEditInput{})
		if err == nil {
			document.ImportedSource = &plan
		}
		return document, err
	}
	service := newTestService(t, store, config)
	request := proposalpkg.SourceRelationshipProposalRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, OutputID: "patients", Change: authoringv2.ImportedRelationshipChange{Action: "ADD", ParentOccurrenceID: "base", EdgeID: "authorized-edge", MatchMode: ws.RouteMatchOptional}}
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
			requestWithSink.PreviewSinkFactory = func(receipt *receiptmodel.CompilationReceipt, _ []viewer.EmittedColumn) (func(map[string]any) error, error) {
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
	if proposal.CandidateDocument.Output.ID != request.OutputID || proposal.CandidateDocument.ImportedSource == nil || !lifecycleHasSourceStep(proposal.CandidateDocument.ImportedSource, "fields") || proposal.CandidateNetwork.OutputID != request.OutputID || proposal.CandidateRowIdentity.OutputID != request.OutputID {
		t.Fatalf("candidate projection lost the proposed source edit: %#v", proposal)
	}
	if *proposal.CandidateDocument.ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("candidate projection changed the immutable pin")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("Preview or Cancel mutated saved source")
	}

	networkFailure = true
	if failed, err := service.ProposeSourceRelationship(ctx, request); err == nil || failed.ProposalID != "" {
		t.Fatal("failed candidate network produced a ready proposal")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(raw, store.created.DraftConfig) {
		t.Fatal("failed candidate projection changed saved source")
	}
	networkFailure = false

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
	apply := authoringv2.ApplyCommandsRequest{CommandID: "relationship-apply", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{ProposalID: proposal.ProposalID}}}}

	forgedWorkspace, err := ws.DecodeWorkspace(candidateReceipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	forgedWorkspace.Documents[1].Output.Title = "Forged unrelated title"
	forgedBinding := *candidateReceipt.SourceRelationshipProposal
	forgedBinding.CandidateWorkspaceDigest, err = forgedWorkspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	forgedReceipt, err := config.CompileReceipt(ctx, ports.CompileReceiptRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: forgedWorkspace, SnapshotToken: snapshot.Token, SourceRelationshipProposal: &forgedBinding})
	if err != nil {
		t.Fatal(err)
	}
	forgedApply := apply
	forgedApply.CommandID = "forged-other-output"
	forgedApply.Commands = []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{ProposalID: forgedReceipt.ID}}}
	_, err = service.ApplyCommands(ctx, "project-a", "patients", forgedApply, "tester")
	var rejected *ports.Error
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
	if reloaded.Workspace == nil || !lifecycleHasSourceStep(reloaded.Workspace.Documents[0].ImportedSource, "fields") {
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

	stepRequest := proposalpkg.SourceRelationshipProposalRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest, OutputID: "patients", SourceStep: &authoringv2.ImportedSourceStepChange{StepID: "fields", Remove: true}}
	compileFailure = true
	if _, err := service.ProposeSourceRelationship(ctx, stepRequest); err == nil {
		t.Fatal("downstream source-step removal failure produced a proposal")
	}
	if store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatal("failed source-step removal preview changed saved work")
	}
	compileFailure = false
	stepProposal, err := service.ProposeSourceRelationship(ctx, stepRequest)
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 1 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatal("source-step removal Preview wrote the draft")
	}
	removed, err := service.ApplyCommands(ctx, "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "source-step-remove", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{ProposalID: stepProposal.ProposalID}}}}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if removed.DraftVersion != 3 || store.saveDraftCalls != 2 || lifecycleHasSourceStep(removed.Workspace.Documents[0].ImportedSource, "fields") || *removed.Workspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("confirmed source removal did not delete the step while retaining its immutable pin")
	}

	parameters := &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
		Name: "identifiers", Source: recipe.Expression{Select: "identifier[]"},
		Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"},
	}}
	ruleRequest := stepRequest
	ruleRequest.ExpectedDraftVersion, ruleRequest.ExpectedDraftDigest = removed.DraftVersion, removed.DraftDigest
	ruleRequest.SourceStep = nil
	ruleRequest.SourceRule = &ws.SourceRuleCreate{Kind: "DYNAMIC_COLUMN", Parameters: parameters}
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
			CommandID: commandID, SemanticsVersion: ws.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyImportedRelationshipProposal, OutputID: "patients", Payload: &authoringv2.ImportedSourcePayload{ProposalID: proposalID}}},
		}, "tester")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	savedRule := applyRule(removed.DraftVersion, removed.DraftDigest, ruleProposal.ProposalID, "create-rule")
	additions := savedRule.Workspace.Documents[0].ImportedSource.RuleAdditions
	if len(additions) != 1 {
		t.Fatal("Apply did not save the created rule")
	}

	removingSourceStep = false
	editedParams := *parameters
	editedDynamic := *parameters.Dynamic
	prefix := "external"
	editedDynamic.ColumnPrefix = &prefix
	editedParams.Dynamic = &editedDynamic
	editRequest := ruleRequest
	editRequest.ExpectedDraftVersion, editRequest.ExpectedDraftDigest = savedRule.DraftVersion, savedRule.DraftDigest
	editRequest.SourceRule = nil
	editRequest.SourceStep = &authoringv2.ImportedSourceStepChange{StepID: additions[0].StepID, Parameters: &editedParams}
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

func lifecycleHasSourceStep(plan *ws.ImportedSourcePlan, id string) bool {
	if plan != nil {
		for _, step := range plan.Steps {
			if step.ID == id {
				return true
			}
		}
	}
	return false
}
