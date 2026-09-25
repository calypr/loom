package lifecycle

import (
	"context"
	"errors"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func constructionProposalService(t *testing.T) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	service, store, snapshot, _ := rowProposalService(t)
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	return service, store, snapshot
}

func testConstructionStageDescriptors(workspace authoringv2.Workspace, outputID string) []explorer.ReceiptConstructionStage {
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 {
		return nil
	}
	document := workspace.Documents[index]
	columns := make([]explorer.ReceiptConstructionStageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, explorer.ReceiptConstructionStageColumn{
			ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType,
		})
	}
	allChoices := func() []explorer.ReceiptConstructionOperationChoice {
		return []explorer.ReceiptConstructionOperationChoice{
			{Kind: "PIVOT", Supported: true}, {Kind: "DERIVE", Supported: true},
			{Kind: "FILTER", Supported: true}, {Kind: "UNPIVOT", Supported: true},
		}
	}
	stages := []explorer.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: allChoices(),
	}}
	if document.Construction == nil {
		return stages
	}
	for _, step := range document.Construction.Steps {
		stageColumns := make([]explorer.ReceiptConstructionStageColumn, 0, len(step.Outputs))
		for _, column := range step.Outputs {
			stageColumns = append(stageColumns, explorer.ReceiptConstructionStageColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type,
			})
		}
		inputStageID := recipe.ConstructionSourceProjectionID
		if len(stages) > 1 {
			inputStageID = stages[len(stages)-1].ID
		}
		stages = append(stages, explorer.ReceiptConstructionStage{
			ID: step.ID, InputStageID: inputStageID, Operation: string(step.Operation.Kind),
			Columns: stageColumns, Capabilities: allChoices(),
		})
	}
	return stages
}

func constructionProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot, document authoringv2.Document, changedStepID string) ConstructionProposalRequest {
	upgraded, _ := authoringv2.UpgradeDocumentToConstruction(document)
	columns := make([]authoringv2.StageColumn, 0, len(upgraded.Columns))
	for _, column := range upgraded.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	return ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID,
		CandidateConstruction: authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			Steps: []authoringv2.ConstructionStep{{
				ID:     changedStepID,
				Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{
					Kind:   authoringv2.ConstructionOperationFilter,
					Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
				},
				Outputs: columns,
			}},
		},
	}
}

func TestConstructionCapabilitiesReturnOnlyReceiptBoundStableStages(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	capabilities, err := service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.ID != recipe.ConstructionSourceProjectionID || len(capabilities.Stages) != 1 || len(capabilities.SelectedStage.Capabilities) != 4 {
		t.Fatalf("source-stage capabilities = %#v", capabilities)
	}
	if len(capabilities.SelectedStage.Columns) == 0 || capabilities.SelectedStage.Columns[0].ID == "" {
		t.Fatalf("source descriptor omitted stable column identity: %#v", capabilities.SelectedStage.Columns)
	}
	stageID := "frontend-invented-stage"
	_, err = service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, StageID: stageID,
	})
	if lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("invented stage reference error = %v", err)
	}
}

func TestProposeConstructionKeepsAcceptedDraftWhenDependencyRepairIsNeeded(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	request := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "broken_filter")
	request.CandidateConstruction.Steps[0].Operation.Filter.ColumnID = "missing_stable_column"
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "NEEDS_REPAIR" || proposal.ProposalID != "" || len(proposal.DependencyImpact.MissingInputs) == 0 {
		t.Fatalf("missing-input proposal = %#v", proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("repair proposal mutated the accepted draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestProposeConstructionAllowsRemovalOfOnlyStepWithoutChangedStepID(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "only_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate one-step construction workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = draft
	store.created.DraftDigest = digest

	proposal, err := service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", RemoveStepIDs: []string{"only_step"},
		CandidateConstruction: authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "READY" || proposal.ProposalID == "" || proposal.ChangedStepID != "" || len(proposal.CandidateConstruction.Steps) != 0 || len(proposal.DependencyImpact.RemovedStepIDs) != 1 || proposal.DependencyImpact.RemovedStepIDs[0] != "only_step" {
		t.Fatalf("remove-only proposal = %#v", proposal)
	}
}

func TestApplyConstructionProposalPreviewsExactReceiptBeforeAtomicSave(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "READY" || proposal.ProposalID == "" || len(proposal.Stages) != 2 {
		t.Fatalf("ready proposal = %#v", proposal)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	previewCalls := 0
	service.config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	apply := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-construction", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", apply, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if previewCalls != 1 {
		t.Fatalf("apply preview calls = %d, want exact server-side preview", previewCalls)
	}
	if response.Workspace.Documents[0].Construction == nil || len(response.Workspace.Documents[0].Construction.Steps) != 1 {
		t.Fatalf("applied construction = %#v", response.Workspace.Documents[0].Construction)
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != version+1 || store.created.DraftDigest == digest || string(before) == string(store.created.DraftConfig) {
		t.Fatalf("proposal was not saved atomically after preview: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestApplyConstructionProposalPreviewFailureLeavesAcceptedDraftIntact(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	service.config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{}, errors.New("preview backend failed")
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "failed-apply-construction", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}, "alice")
	if lifecycleErrorCode(err) != "CONSTRUCTION_PREVIEW_FAILED" {
		t.Fatalf("preview failure error = %v", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("failed exact preview mutated accepted draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}
