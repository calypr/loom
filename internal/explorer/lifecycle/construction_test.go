package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/construction"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func constructionProposalService(t *testing.T, options ...testServiceOption) (*Service, *fakeStore, capability.Snapshot) {
	setup := constructionProposalSetup(t, options...)
	service := newTestServiceFromSetup(t, setup)
	return service, setup.Store, setup.Snapshot
}

func constructionProposalSetup(t *testing.T, options ...testServiceOption) testServiceSetup {
	t.Helper()
	constructionCompiler := func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt := lifecycleConstructionReceipt(setup.Snapshot, request.Workspace)
			receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
				"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
			}
			var err error
			receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
			if err != nil {
				return nil, err
			}
			receipt.ID, err = receiptmodel.ReceiptID(*receipt)
			if err != nil {
				return nil, err
			}
			setup.Store.receipt = receipt
			return receipt, nil
		}
	}
	setup := rowProposalSetup(t)
	constructionCompiler(&setup)
	for _, option := range options {
		option(&setup)
	}
	return setup
}

func testConstructionStageDescriptors(workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 {
		return nil
	}
	document := workspace.Documents[index]
	columns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, receiptmodel.ReceiptConstructionStageColumn{
			ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Cardinality: expression.OptionalOne,
		})
	}
	allChoices := func() []receiptmodel.ReceiptConstructionOperationChoice {
		return []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "PIVOT", Supported: true}, {Kind: "DERIVE", Supported: true},
			{Kind: "FILTER", Supported: true}, {Kind: "UNPIVOT", Supported: true},
			{Kind: "RELATED_EXPAND", Supported: true},
		}
	}
	rootAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
		AnchorColumnID: "_key", Kind: "root", ResourceType: document.RootResourceType,
		Label: "Original " + document.RootResourceType,
	}
	stages := []receiptmodel.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: allChoices(),
		RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor},
	}}
	var activeAnchor *receiptmodel.ReceiptConstructionActiveRelatedRecord
	if document.Construction == nil {
		return stages
	}
	for _, step := range document.Construction.Steps {
		stageColumns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(step.Outputs))
		for _, column := range step.Outputs {
			stageColumns = append(stageColumns, receiptmodel.ReceiptConstructionStageColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Cardinality: expression.OptionalOne,
			})
		}
		inputStageID := recipe.ConstructionSourceProjectionID
		if len(stages) > 1 {
			inputStageID = stages[len(stages)-1].ID
		}
		stage := receiptmodel.ReceiptConstructionStage{
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
			stage.RelatedExpand = &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumnID,
				AnchorKind: "root", AnchorNodeID: related.Route[0].FromNodeID,
				AnchorResourceType:     related.Route[0].FromResourceType,
				RelatedRecordColumnID:  related.RelatedRecordColumnID,
				ParentIdentityColumnID: "__test_parent_identity", ParentIdentityColumn: "__test_parent_identity",
				TerminalIdentityColumn: "__test_terminal_identity", TargetNodeID: related.TargetNodeID,
				TargetResourceType: related.TargetResourceType, Route: route,
			}
			activeAnchor = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
				TerminalIdentityColumn: "__test_terminal_identity",
			}
		} else if step.Operation.Kind != ws.ConstructionOperationFilter &&
			step.Operation.Kind != ws.ConstructionOperationDerive &&
			step.Operation.Kind != ws.ConstructionOperationRelatedSource &&
			step.Operation.Kind != ws.ConstructionOperationRelatedField {
			activeAnchor = nil
		}
		stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor}
		if activeAnchor != nil {
			stage.ActiveRelatedRecord = activeAnchor
			stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: activeAnchor.TerminalIdentityColumn, Kind: "activeRelatedRecord",
				NodeID: activeAnchor.TargetNodeID, ResourceType: activeAnchor.TargetResourceType,
				Label: "Current related " + activeAnchor.TargetResourceType,
			})
		}
		stages = append(stages, stage)
	}
	return stages
}

func constructionProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot, document ws.Document, changedStepID string) construction.ConstructionProposalRequest {
	upgraded, _ := ws.StartDocumentConstruction(document)
	columns := make([]ws.StageColumn, 0, len(upgraded.Columns))
	for _, column := range upgraded.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	return construction.ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID,
		CandidateConstruction: ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{{
				ID:     changedStepID,
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{
					Kind:   ws.ConstructionOperationFilter,
					Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
				},
				Outputs: columns,
			}},
		},
	}
}

func seedConstructionProposalWithRelatedSource(t *testing.T, store *fakeStore, snapshot capability.Snapshot) (ws.Workspace, ws.ConstructionRelatedSource) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
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
	related := ws.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: candidate.NodeID,
		Source: ws.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
			ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality, LogicalType: candidate.LogicalType,
		},
		Route: route, ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
		Form: capability.ConstructionChoiceAll, OutputColumnID: "observation-status",
	}
	filterOutputs := append([]ws.StageColumn(nil), columns...)
	relatedOutputs := append([]ws.StageColumn(nil), columns...)
	relatedOutputs = append(relatedOutputs, ws.StageColumn{ID: related.OutputColumnID, Name: "observation_status", Label: "Observation statuses", Type: related.Source.LogicalType})
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: "filter_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists}},
			Outputs:   filterOutputs,
		},
		{
			ID: "related_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter_step"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedSource, RelatedSource: &related},
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

func seedConstructionProposalWithTwoFilters(t *testing.T, store *fakeStore, snapshot capability.Snapshot) (ws.Workspace, ws.ConstructionRelatedSource) {
	t.Helper()
	workspace, related := seedConstructionProposalWithRelatedSource(t, store, snapshot)
	document := workspace.Documents[0]
	columns := append([]ws.StageColumn(nil), document.Construction.Steps[0].Outputs...)
	document.Construction.Steps[1].Operation = ws.ConstructionOperation{
		Kind:   ws.ConstructionOperationFilter,
		Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
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

func turnProposalStepIntoRelatedSource(workspace ws.Workspace, index int, related ws.ConstructionRelatedSource) ws.ConstructionStep {
	step := workspace.Documents[0].Construction.Steps[index]
	step.Operation = ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedSource, RelatedSource: &related}
	step.Outputs = append([]ws.StageColumn(nil), workspace.Documents[0].Construction.Steps[0].Outputs...)
	step.Outputs = append(step.Outputs, ws.StageColumn{ID: related.OutputColumnID, Name: "observation_status", Label: "Observation statuses", Type: related.Source.LogicalType})
	return step
}

func relatedSourceProposalRequest(owner *explorer.Explorer, snapshot capability.Snapshot, workspace ws.Workspace, related ws.ConstructionRelatedSource, changedStepID string, removeStepIDs ...string) construction.ConstructionProposalRequest {
	document := workspace.Documents[0]
	candidate, _ := ws.StartDocumentConstruction(document)
	for index := range candidate.Construction.Steps {
		if candidate.Construction.Steps[index].ID == "related_step" {
			candidate.Construction.Steps[index].Operation.RelatedSource = &related
		}
	}
	return construction.ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID, RemoveStepIDs: removeStepIDs,
		CandidateConstruction: ws.Construction{Version: ws.ConstructionVersion, Steps: append([]ws.ConstructionStep(nil), candidate.Construction.Steps...)},
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
	request.CandidateConstruction.Steps = []ws.ConstructionStep{workspace.Documents[0].Construction.Steps[1]}
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
	request.CandidateConstruction.Steps = []ws.ConstructionStep{turnProposalStepIntoRelatedSource(workspace, 1, related)}
	_, err := service.ProposeConstruction(context.Background(), request)
	if lifecycleErrorCode(err) != "STALE_CONSTRUCTION_CHOICE" {
		t.Fatalf("new related source with omitted changedStepId error = %v, want stale choice", err)
	}
}

func TestProposeConstructionReauthorizesRelatedExpandRouteChoice(t *testing.T) {
	setup, _ := inboundPatientObservationRouteSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
		if prepareErr != nil {
			return nil, prepareErr
		}
		receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
		receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestServiceFromSetup(t, setup)
	choices, err := service.SearchRelatedExpandChoices(context.Background(), construction.RelatedExpandChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID, AnchorColumnID: "_key", TargetResourceType: "Observation",
	})
	if err != nil || len(choices.Choices) != 1 {
		t.Fatalf("related expansion route search = %#v, %v", choices, err)
	}
	choice := choices.Choices[0]
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]ws.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		outputs = append(outputs, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, ws.StageColumn{ID: "observation_id", Name: "observation_id", Label: "FHIR resource ID", Type: "string"})
	makeRequest := func(route []capability.ConstructionRouteStep) construction.ConstructionProposalRequest {
		related := &ws.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, TargetNodeID: choice.TargetNodeID,
			TargetResourceType: choice.TargetResourceType, Route: route,
			ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
			EmptyPolicy:     ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id",
		}
		return construction.ConstructionProposalRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			OutputID: "patients", ChangedStepID: "expand_observations",
			CandidateConstruction: ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
				ID: "expand_observations", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: related}, Outputs: outputs,
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
	request.CandidateConstruction.Steps[0].Operation.Filter.Operator = ws.ConstructionFilterMissing
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
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	capabilities, err := service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
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
	if availability := capabilities.SourceProjectionAvailability; availability == nil || !availability.Supported || availability.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("source-stage projection availability = %#v, want supported and bound to source_projection", availability)
	}
	if len(capabilities.SelectedStage.Columns) == 0 || capabilities.SelectedStage.Columns[0].ID == "" {
		t.Fatalf("source descriptor omitted stable column identity: %#v", capabilities.SelectedStage.Columns)
	}
	stageID := "frontend-invented-stage"
	_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, StageID: stageID,
	})
	if lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("invented stage reference error = %v", err)
	}
}

func TestConstructionCapabilitiesAfterStoredV9MigrationPreserveOwnerAndVerifyReceipt(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "mismatched"}[forged], func(t *testing.T) {
			setup := constructionProposalSetup(t)
			store, snapshot := setup.Store, setup.Snapshot
			workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
			if err != nil {
				t.Fatal(err)
			}
			workspace.SemanticsVersion = 9
			draft, err := workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := workspace.Digest()
			if err != nil {
				t.Fatal(err)
			}
			migration := migrateStoredWorkspaceFixture(t, draft, setup.Config.Capability.Catalog(snapshot, store.created.ExplorerID))
			draft, digest = migration.CanonicalJSON, migration.MigratedCanonicalDigest
			store.created.DraftConfig, store.created.DraftDigest = draft, digest
			originalConfig, originalVersion, originalDigest := string(draft), store.created.DraftVersion, digest
			compileReceipt := setup.Config.CompileReceipt
			catalog := setup.Config.Capability.Catalog(snapshot, store.created.ExplorerID)
			setup.Config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				prepared, migrateErr := ws.PrepareWorkspaceForCompilation(request.Workspace, catalog)
				if migrateErr != nil {
					return nil, migrateErr
				}
				prepared = prepared.NormalizePresentationOrders()
				if forged {
					prepared.Explorer.Title += " forged"
				}
				request.Workspace = prepared
				return compileReceipt(ctx, request)
			}
			service := newTestServiceFromSetup(t, setup)
			_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
				Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
				ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
				OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
			})
			if forged {
				if lifecycleErrorCode(err) != "INVALID_COMPILATION_RECEIPT" {
					t.Fatalf("mismatched normalized receipt error = %v, want INVALID_COMPILATION_RECEIPT", err)
				}
			} else if err != nil {
				t.Fatalf("matching v9 workspace receipt: %v", err)
			}
			if string(store.created.DraftConfig) != originalConfig || store.created.DraftVersion != originalVersion || store.created.DraftDigest != originalDigest {
				t.Fatalf("construction capability read changed owner draft: version=%d digest=%q", store.created.DraftVersion, store.created.DraftDigest)
			}
		})
	}
}

func TestConstructionGroupSourceCandidateRequiresPopulatedField(t *testing.T) {
	choice := capability.RowChoice{Kind: capability.RowChoiceFieldGroupKey, NodeID: "body-structure", ResourceType: "BodyStructure", Path: "active"}
	snapshot := capability.Snapshot{Candidates: []capability.Candidate{{
		ID: "active", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "active",
		LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed: false, Populated: false,
	}}}
	if _, ok := constructionGroupSourceCandidate(snapshot, choice); ok {
		t.Fatal("offered an unpopulated field as a source grouping key")
	}
}

func TestProposeConstructionKeepsAcceptedDraftWhenDependencyRepairIsNeeded(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
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
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []ws.ConstructionStep{{
		ID: "only_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
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

	proposal, err := service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", RemoveStepIDs: []string{"only_step"},
		CandidateConstruction: ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" || proposal.ChangedStepID != "" || len(proposal.CandidateConstruction.Steps) != 0 || len(proposal.DependencyImpact.RemovedStepIDs) != 1 || proposal.DependencyImpact.RemovedStepIDs[0] != "only_step" {
		t.Fatalf("remove-only proposal = %#v", proposal)
	}
}

func TestApplyConstructionProposalPreviewsExactReceiptBeforeAtomicSave(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	previewCalls := 0
	setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" || len(proposal.Stages) != 2 {
		t.Fatalf("ready proposal = %#v", proposal)
	}
	if proposal.BaseReceiptID == "" {
		t.Fatal("proposal omitted the existing base receipt identity")
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	var capturedRows []map[string]any
	sinkFactoryCalls := 0
	apply := authoringv2.ApplyCommandsRequest{
		CommandID: "apply-construction", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}
	result, err := service.ApplyCommandsWithConstructionPreview(context.Background(), "project-a", "patients", apply, "alice", func(receipt *receiptmodel.CompilationReceipt, _ []viewer.EmittedColumn) (func(map[string]any) error, error) {
		sinkFactoryCalls++
		if receipt == nil || receipt.ID != proposal.ProposalID {
			t.Fatalf("preview sink receipt = %#v, want exact proposal %q", receipt, proposal.ProposalID)
		}
		return func(row map[string]any) error {
			capturedRows = append(capturedRows, row)
			return nil
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	response := result.Commands
	if previewCalls != 1 {
		t.Fatalf("apply preview calls = %d, want exact server-side preview", previewCalls)
	}
	if response.Workspace.Documents[0].Construction == nil || len(response.Workspace.Documents[0].Construction.Steps) != 1 {
		t.Fatalf("applied construction = %#v", response.Workspace.Documents[0].Construction)
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != version+1 || store.created.DraftDigest == digest || string(before) == string(store.created.DraftConfig) {
		t.Fatalf("proposal was not saved atomically after preview: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
	if result.Accepted == nil {
		t.Fatal("successful construction apply omitted the captured accepted preview")
	}
	if result.Accepted.Preview.Receipt == nil || result.Accepted.Preview.Receipt.ID != proposal.ProposalID || result.Accepted.Preview.Receipt.IntentDigest != response.DraftDigest || result.Accepted.Preview.Summary.RowCount != 1 || result.Accepted.PreviewLimit != store.receipt.ConstructionProposal.PreviewLimit {
		t.Fatalf("accepted construction preview does not match the saved candidate: %#v", result.Accepted)
	}
	if sinkFactoryCalls != 1 || len(capturedRows) != 1 || capturedRows[0]["patient_id"] != "p1" {
		t.Fatalf("captured preview rows = %#v, sink factory calls = %d", capturedRows, sinkFactoryCalls)
	}
	if bytes.Contains(store.created.LastAuthoringCommandResults, []byte("p1")) || bytes.Contains(store.created.DraftConfig, []byte("p1")) {
		t.Fatal("preview rows were persisted in the command replay record or draft")
	}
	replayed, err := service.ApplyCommandsWithConstructionPreview(context.Background(), "project-a", "patients", apply, "alice", func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		t.Fatal("command replay reran the preview sink factory")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Accepted != nil || replayed.Commands.DraftDigest != response.DraftDigest || previewCalls != 1 || sinkFactoryCalls != 1 {
		t.Fatalf("replay must omit request-local preview data: replay=%#v previews=%d factories=%d", replayed, previewCalls, sinkFactoryCalls)
	}
}

func TestApplyConstructionProposalCaptureFailureDoesNotChangeApply(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	previewCalls := 0
	setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", RowCount: 1, Complete: true}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatal(err)
	}
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	result, err := service.ApplyCommandsWithConstructionPreview(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-construction-capture-failure", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice", func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		return nil, errors.New("bounded response encoder is unavailable")
	})
	if err != nil {
		t.Fatalf("capture failure changed Apply result: %v", err)
	}
	if result.Accepted != nil || previewCalls != 1 || store.saveDraftCalls != 1 || store.created.DraftVersion != version+1 {
		t.Fatalf("capture failure did not fall back to ordinary successful Apply: result=%#v previews=%d saves=%d owner=%#v", result, previewCalls, store.saveDraftCalls, store.created)
	}
}

func TestApplyConstructionProposalZeroRowPreviewCanBeCaptured(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	setup.Config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Output: "patients", RowCount: 0, Complete: true}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatal(err)
	}
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	result, err := service.ApplyCommandsWithConstructionPreview(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-construction-empty-preview", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice", func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted == nil || result.Accepted.Preview.Summary.RowCount != 0 || store.saveDraftCalls != 1 || store.created.DraftVersion != version+1 {
		t.Fatalf("zero-row preview did not remain a valid captured Apply result: result=%#v saves=%d owner=%#v", result, store.saveDraftCalls, store.created)
	}
}

func TestApplyConstructionProposalSaveFailureReturnsNoAcceptedPreview(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	previewCalls := 0
	setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", RowCount: 1, Complete: true}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatal(err)
	}
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	store.applyErr = errors.New("draft compare-and-swap failed")
	result, err := service.ApplyCommandsWithConstructionPreview(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-construction-cas-failure", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice", func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
		return func(map[string]any) error { return nil }, nil
	})
	if err == nil || result != nil {
		t.Fatalf("failed SaveDraft returned a response or accepted preview: result=%#v err=%v", result, err)
	}
	if previewCalls != 1 || store.saveDraftCalls != 1 || store.created.DraftVersion != version || store.created.DraftDigest != digest {
		t.Fatalf("save failure boundary changed: previews=%d saves=%d owner=%#v", previewCalls, store.saveDraftCalls, store.created)
	}
}

func TestApplyConstructionProposalPreviewFailureLeavesAcceptedDraftIntact(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	setup.Config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{}, errors.New("preview backend failed")
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step"))
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "failed-apply-construction", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice")
	if lifecycleErrorCode(err) != "CONSTRUCTION_PREVIEW_FAILED" {
		t.Fatalf("preview failure error = %v", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("failed exact preview mutated accepted draft: saves=%d owner=%#v", store.saveDraftCalls, store.created)
	}
}

func TestConstructionCapabilitiesRejectStaleDraftBeforeWorkspaceSchemaProjection(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	proposal := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: proposal.Project, ExplorerID: proposal.ExplorerID, SnapshotToken: proposal.SnapshotToken,
		ExpectedDraftVersion: proposal.ExpectedDraftVersion + 1, ExpectedDraftDigest: proposal.ExpectedDraftDigest,
		OutputID: proposal.OutputID, StageID: recipe.ConstructionSourceProjectionID,
	})
	if lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft capability error = %v, want DRAFT_CONFLICT", err)
	}
}

func TestApplyAppendProposalPreservesExplicitlyEmptyTargetColumns(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	setup.Config.ConstructionSourceStage = func(_ context.Context, _ ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
		return receiptmodel.ReceiptConstructionStage{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []receiptmodel.ReceiptConstructionStageColumn{},
		}, nil
	}
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = []ws.Column{}
	workspace.Documents[0].Construction = nil
	workspace.Documents[0].ArchiveRetiredShape = nil
	sibling := workspace.Documents[0]
	sibling.Output = ws.Output{ID: "sibling", Title: "Sibling"}
	sibling.Columns = []ws.Column{{
		ColumnID: "sibling_id", Column: "sibling_id", Label: "Sibling ID", LogicalType: "string",
		OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID},
	}}
	workspace.Documents = append(workspace.Documents, sibling)
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "sibling-tab", Title: "Sibling", OutputID: "sibling", Order: 1, Visible: true})
	baseRaw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	baseDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = baseRaw
	store.created.DraftDigest = baseDigest

	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "append_three_sources",
		Inputs: []ws.ConstructionInputRef{
			{Kind: ws.ConstructionInputTableRevision, TableID: "table-observations", RevisionID: "rev-source", OutputID: "observations"},
			{Kind: ws.ConstructionInputTableRevision, TableID: "table-reports", RevisionID: "rev-source", OutputID: "reports"},
			{Kind: ws.ConstructionInputTableRevision, TableID: "table-patients", RevisionID: "rev-source", OutputID: "source-patients"},
		},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
			Kind: ws.ConstructionCombineAppend,
			Projections: []ws.ConstructionCombineProjection{
				{OutputColumnID: "record_id", InputIndex: 0, InputColumnID: "observation-id"},
				{OutputColumnID: "record_id", InputIndex: 1, InputColumnID: "report-id"},
				{OutputColumnID: "record_id", InputIndex: 2, InputColumnID: "patient-id"},
				{OutputColumnID: "status", InputIndex: 0, InputColumnID: "observation-status"},
				{OutputColumnID: "status", InputIndex: 1, InputColumnID: "report-status"},
				{OutputColumnID: "patient_gender", InputIndex: 2, InputColumnID: "patient-gender"},
			},
		}},
		Outputs: []ws.StageColumn{
			{ID: "record_id", Name: "record_id", Label: "Record ID", Type: "string", Nullable: true},
			{ID: "status", Name: "status", Label: "Status", Type: "string", Nullable: true},
			{ID: "patient_gender", Name: "patient_gender", Label: "Patient gender", Type: "string", Nullable: true},
		},
	}}}
	setup.Config.PreviewReceipt = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if err := visit(map[string]any{"record_id": "r1", "status": "final", "patient_gender": nil}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"record_id", "status", "patient_gender"}, RowCount: 1, Complete: true}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "append_three_sources", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("append proposal = %#v", proposal)
	}
	_, err = service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "apply-empty-target-append", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply APPEND candidate after canonical receipt round-trip: %v", err)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("successful APPEND proposal saves = %d, want one atomic save", store.saveDraftCalls)
	}
	saved, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	applied := saved.Documents[0]
	if applied.Columns == nil || len(applied.Columns) != 0 {
		t.Fatalf("applied APPEND changed the empty authored source column list: %#v", applied.Columns)
	}
	if applied.Construction == nil || !reflect.DeepEqual(*applied.Construction, candidate) {
		t.Fatalf("saved APPEND construction differs from the exact proposal: got %#v want %#v", applied.Construction, candidate)
	}
}
