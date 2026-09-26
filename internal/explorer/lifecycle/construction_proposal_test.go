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
			{Kind: "RELATED_EXPAND", Supported: true},
		}
	}
	rootAnchor := explorer.ReceiptConstructionRelatedExpandAnchor{
		AnchorColumnID: "_key", Kind: "root", ResourceType: document.RootResourceType,
		Label: "Original " + document.RootResourceType,
	}
	stages := []explorer.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: allChoices(),
		RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor},
	}}
	var activeAnchor *explorer.ReceiptConstructionActiveRelatedRecord
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
		stage := explorer.ReceiptConstructionStage{
			ID: step.ID, InputStageID: inputStageID, Operation: string(step.Operation.Kind),
			Columns: stageColumns, Capabilities: allChoices(),
		}
		if related := step.Operation.RelatedExpand; related != nil {
			route := make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route))
			for _, hop := range related.Route {
				route = append(route, recipe.ConstructionRelatedRouteStep{
					EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
					FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
					Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
				})
			}
			stage.RelatedExpand = &explorer.ReceiptConstructionRelatedExpand{
				AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumnID,
				AnchorKind: "root", AnchorNodeID: related.Route[0].FromNodeID,
				AnchorResourceType:     related.Route[0].FromResourceType,
				RelatedRecordColumnID:  related.RelatedRecordColumnID,
				ParentIdentityColumnID: "__test_parent_identity", ParentIdentityColumn: "__test_parent_identity",
				TerminalIdentityColumn: "__test_terminal_identity", TargetNodeID: related.TargetNodeID,
				TargetResourceType: related.TargetResourceType, Route: route,
			}
			activeAnchor = &explorer.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
				TerminalIdentityColumn: "__test_terminal_identity",
			}
		} else if step.Operation.Kind != authoringv2.ConstructionOperationFilter &&
			step.Operation.Kind != authoringv2.ConstructionOperationDerive &&
			step.Operation.Kind != authoringv2.ConstructionOperationRelatedSource &&
			step.Operation.Kind != authoringv2.ConstructionOperationRelatedField {
			activeAnchor = nil
		}
		stage.RelatedExpandAnchors = []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor}
		if activeAnchor != nil {
			stage.ActiveRelatedRecord = activeAnchor
			stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, explorer.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: activeAnchor.TerminalIdentityColumn, Kind: "activeRelatedRecord",
				NodeID: activeAnchor.TargetNodeID, ResourceType: activeAnchor.TargetResourceType,
				Label: "Current related " + activeAnchor.TargetResourceType,
			})
		}
		stages = append(stages, stage)
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

func seedConstructionProposalWithRelatedSource(t *testing.T, store *fakeStore, snapshot capability.Snapshot) (authoringv2.Workspace, authoringv2.ConstructionRelatedSource) {
	t.Helper()
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
	if len(columns) == 0 {
		t.Fatal("proposal fixture has no source columns")
	}
	route := []capability.ConstructionRouteStep{{
		EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	candidate := capability.Candidate{
		ID: "observation-status", NodeID: "observation", ResourceType: "Observation", FieldPath: "status",
		Cardinality: "optional_one", LogicalType: "code", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute("expired-choice-snapshot", route, candidate)
	if err != nil {
		t.Fatal(err)
	}
	related := authoringv2.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: candidate.NodeID,
		Source: authoringv2.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
			ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality, LogicalType: candidate.LogicalType,
		},
		Route: route, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
		Form: capability.ConstructionChoiceAll, OutputColumnID: "observation-status",
	}
	filterOutputs := append([]authoringv2.StageColumn(nil), columns...)
	relatedOutputs := append([]authoringv2.StageColumn(nil), columns...)
	relatedOutputs = append(relatedOutputs, authoringv2.StageColumn{ID: related.OutputColumnID, Name: "observation_status", Label: "Observation statuses", Type: related.Source.LogicalType})
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID: "filter_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists}},
			Outputs:   filterOutputs,
		},
		{
			ID: "related_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "filter_step"}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &related},
			Outputs:   relatedOutputs,
		},
	}}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate related proposal base: %v", err)
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
	return workspace, related
}

func seedConstructionProposalWithTwoFilters(t *testing.T, store *fakeStore, snapshot capability.Snapshot) (authoringv2.Workspace, authoringv2.ConstructionRelatedSource) {
	t.Helper()
	workspace, related := seedConstructionProposalWithRelatedSource(t, store, snapshot)
	document := workspace.Documents[0]
	columns := append([]authoringv2.StageColumn(nil), document.Construction.Steps[0].Outputs...)
	document.Construction.Steps[1].Operation = authoringv2.ConstructionOperation{
		Kind:   authoringv2.ConstructionOperationFilter,
		Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
	}
	document.Construction.Steps[1].Outputs = columns
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate two-filter proposal base: %v", err)
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
	return workspace, related
}

func turnProposalStepIntoRelatedSource(workspace authoringv2.Workspace, index int, related authoringv2.ConstructionRelatedSource) authoringv2.ConstructionStep {
	step := workspace.Documents[0].Construction.Steps[index]
	step.Operation = authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &related}
	step.Outputs = append([]authoringv2.StageColumn(nil), workspace.Documents[0].Construction.Steps[0].Outputs...)
	step.Outputs = append(step.Outputs, authoringv2.StageColumn{ID: related.OutputColumnID, Name: "observation_status", Label: "Observation statuses", Type: related.Source.LogicalType})
	return step
}

func relatedSourceProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot, workspace authoringv2.Workspace, related authoringv2.ConstructionRelatedSource, changedStepID string, removeStepIDs ...string) ConstructionProposalRequest {
	document := workspace.Documents[0]
	candidate, _ := authoringv2.UpgradeDocumentToConstruction(document)
	for index := range candidate.Construction.Steps {
		if candidate.Construction.Steps[index].ID == "related_step" {
			candidate.Construction.Steps[index].Operation.RelatedSource = &related
		}
	}
	return ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID, RemoveStepIDs: removeStepIDs,
		CandidateConstruction: authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: append([]authoringv2.ConstructionStep(nil), candidate.Construction.Steps...)},
	}
}

func TestProposeConstructionReauthorizesRelatedSourceChangedOutsideHint(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, related := seedConstructionProposalWithRelatedSource(t, store, snapshot)
	request := relatedSourceProposalRequest(store.created, snapshot, workspace, related, "filter_step")
	for index := range request.CandidateConstruction.Steps {
		if request.CandidateConstruction.Steps[index].ID == "related_step" {
			request.CandidateConstruction.Steps[index].Operation.RelatedSource.Source.Path = "valueQuantity.value"
		}
	}
	_, err := service.ProposeConstruction(context.Background(), request)
	if lifecycleErrorCode(err) != "STALE_CONSTRUCTION_CHOICE" {
		t.Fatalf("tampered related source with unrelated changedStepId error = %v, want stale choice", err)
	}
}

func TestProposeConstructionReauthorizesRelatedSourceWhenChangedStepHintOmitted(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, related := seedConstructionProposalWithRelatedSource(t, store, snapshot)
	request := relatedSourceProposalRequest(store.created, snapshot, workspace, related, "", "filter_step")
	request.CandidateConstruction.Steps = []authoringv2.ConstructionStep{workspace.Documents[0].Construction.Steps[1]}
	request.CandidateConstruction.Steps[0].Operation.RelatedSource.Source.Path = "valueQuantity.value"
	_, err := service.ProposeConstruction(context.Background(), request)
	if lifecycleErrorCode(err) != "STALE_CONSTRUCTION_CHOICE" {
		t.Fatalf("tampered related source with omitted changedStepId error = %v, want stale choice", err)
	}
}

func TestProposeConstructionReauthorizesNewRelatedSourceWhenChangedStepHintIsForged(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, related := seedConstructionProposalWithTwoFilters(t, store, snapshot)
	request := relatedSourceProposalRequest(store.created, snapshot, workspace, related, "filter_step")
	request.CandidateConstruction.Steps[1] = turnProposalStepIntoRelatedSource(workspace, 1, related)
	_, err := service.ProposeConstruction(context.Background(), request)
	if lifecycleErrorCode(err) != "STALE_CONSTRUCTION_CHOICE" {
		t.Fatalf("new related source with forged changedStepId error = %v, want stale choice", err)
	}
}

func TestProposeConstructionReauthorizesNewRelatedSourceWithOmittedChangedStepHint(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, related := seedConstructionProposalWithTwoFilters(t, store, snapshot)
	request := relatedSourceProposalRequest(store.created, snapshot, workspace, related, "", "filter_step")
	request.CandidateConstruction.Steps = []authoringv2.ConstructionStep{turnProposalStepIntoRelatedSource(workspace, 1, related)}
	_, err := service.ProposeConstruction(context.Background(), request)
	if lifecycleErrorCode(err) != "STALE_CONSTRUCTION_CHOICE" {
		t.Fatalf("new related source with omitted changedStepId error = %v, want stale choice", err)
	}
}

func TestProposeConstructionReauthorizesRelatedExpandRouteChoice(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
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
	choices, err := service.SearchRelatedExpandChoices(context.Background(), RelatedExpandChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID, AnchorColumnID: "_key", TargetResourceType: "Observation",
	})
	if err != nil || len(choices.Choices) != 1 {
		t.Fatalf("related expansion route search = %#v, %v", choices, err)
	}
	choice := choices.Choices[0]
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, authoringv2.StageColumn{ID: "observation_id", Name: "observation_id", Label: "FHIR resource ID", Type: "string"})
	makeRequest := func(route []capability.ConstructionRouteStep) ConstructionProposalRequest {
		related := &authoringv2.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, TargetNodeID: choice.TargetNodeID,
			TargetResourceType: choice.TargetResourceType, Route: route,
			ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
			EmptyPolicy:     authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id",
		}
		return ConstructionProposalRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			OutputID: "patients", ChangedStepID: "expand_observations",
			CandidateConstruction: authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
				ID: "expand_observations", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: related}, Outputs: outputs,
			}}},
		}
	}
	valid, err := service.ProposeConstruction(context.Background(), makeRequest(choice.Route))
	if err != nil || valid.PreviewStatus != "PREVIEW_PENDING" || valid.ProposalID == "" {
		t.Fatalf("authorized related expansion proposal = %#v, %v", valid, err)
	}
	forgedRoute := append([]capability.ConstructionRouteStep(nil), choice.Route...)
	forgedRoute[0].EdgeID = "forged-edge"
	_, err = service.ProposeConstruction(context.Background(), makeRequest(forgedRoute))
	if lifecycleErrorCode(err) != "INVALID_CONSTRUCTION_CHOICE" {
		t.Fatalf("route changed after server-issued choice error = %v, want invalid choice", err)
	}
}

func TestProposeConstructionDoesNotReauthorizeUnchangedRelatedSource(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, related := seedConstructionProposalWithRelatedSource(t, store, snapshot)
	request := relatedSourceProposalRequest(store.created, snapshot, workspace, related, "filter_step")
	request.CandidateConstruction.Steps[0].Operation.Filter.Operator = authoringv2.ConstructionFilterMissing
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatalf("unrelated edit with unchanged saved related source: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("unchanged related-source proposal = %#v", proposal)
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
	if capabilities.SelectedStage.ID != recipe.ConstructionSourceProjectionID || len(capabilities.Stages) != 1 || len(capabilities.SelectedStage.Capabilities) != 5 {
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
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" || proposal.ChangedStepID != "" || len(proposal.CandidateConstruction.Steps) != 0 || len(proposal.DependencyImpact.RemovedStepIDs) != 1 || proposal.DependencyImpact.RemovedStepIDs[0] != "only_step" {
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
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" || len(proposal.Stages) != 2 {
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
