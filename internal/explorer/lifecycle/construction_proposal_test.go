package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
			ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Cardinality: expression.OptionalOne,
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
				ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Cardinality: expression.OptionalOne,
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

func TestConstructionCapabilitiesNormalizePersistedV9ReceiptWithoutChangingOwnerDraft(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "mismatched"}[forged], func(t *testing.T) {
			service, store, snapshot := constructionProposalService(t)
			workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
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
			store.created.DraftConfig, store.created.DraftDigest = draft, digest
			originalConfig, originalVersion, originalDigest := string(draft), store.created.DraftVersion, digest
			compileReceipt := service.config.CompileReceipt
			catalog := service.config.Capability.Catalog(snapshot, store.created.ExplorerID)
			service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				prepared, migrateErr := authoringv2.MigrateLegacyContributors(request.Workspace, catalog)
				if migrateErr != nil {
					return nil, migrateErr
				}
				prepared = authoringv2.MigrateLosslessDefaults(prepared, catalog).NormalizePresentationOrders()
				if forged {
					prepared.Explorer.Title += " forged"
				}
				request.Workspace = prepared
				return compileReceipt(ctx, request)
			}
			_, err = service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
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

func TestConstructionCodedGroupChoicesRequirePopulatedCodeAndShowPathBreadcrumb(t *testing.T) {
	const populatedPath = "includedStructure[].structure.coding[]"
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1}},
		nil,
		[]capability.Candidate{
			{ID: "populated-code", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: populatedPath + ".code", Observed: true, Populated: true, ObservedDocumentCount: 1},
			{ID: "schema-only-code", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "_active.extension[].valueCodeableConcept.coding[].code", Observed: false, Populated: false},
		},
		nil,
	)
	base := constructionBase{
		snapshot: snapshot,
		document: authoringv2.Document{
			RootResourceType: "BodyStructure",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "BodyStructure"},
		},
	}
	stage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Capabilities: []explorer.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true}},
	}
	choices, err := constructionCodedGroupChoices(base, "body-structures", stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0].CodingPath != populatedPath {
		t.Fatalf("coded group choices = %#v, want only populated path %q", choices, populatedPath)
	}
	if want := "BodyStructure · includedStructure[] › structure › coding[]"; choices[0].Label != want {
		t.Fatalf("coded group choice label = %q, want breadcrumb %q", choices[0].Label, want)
	}
}

func TestConstructionGroupSourceExposesPopulatedGeneratedScalarWhenObserved(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 2}},
		nil,
		[]capability.Candidate{
			{ID: "active", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "active", Label: "Whether this record is in active use", LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
			{ID: "id", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "id", Label: "Logical id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
			{ID: "patient", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "patient", Label: "Patient", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
		},
		nil,
	)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{config: Config{RowChoicePlanner: resolver}}
	base := constructionBase{
		snapshot: snapshot,
		document: authoringv2.Document{
			RootResourceType: "BodyStructure",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "BodyStructure"},
		},
	}
	capability, err := service.constructionGroupSourceCapability(context.Background(), base, "body-structures", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !capability.Supported || capability.ReasonCode != "" || len(capability.Choices) != 1 {
		t.Fatalf("BodyStructure source choices = %#v, want one eligible root scalar", capability)
	}
	choice := capability.Choices[0]
	if choice.FieldPath != "active" || choice.Label != "Active" || choice.FHIRType != "boolean" ||
		choice.LogicalType != "boolean" || choice.ValueType != "BOOLEAN" || choice.IsIdentifier || choice.IsReference || !choice.IsPopulated {
		t.Fatalf("BodyStructure active choice = %#v", choice)
	}
	transformed, err := service.constructionGroupSourceCapability(context.Background(), base, "body-structures", "group-step")
	if err != nil {
		t.Fatal(err)
	}
	if transformed.Supported || transformed.ReasonCode != "TRANSFORMED_STAGE_SOURCE_INPUT_UNSUPPORTED" || len(transformed.Choices) != 0 {
		t.Fatalf("transformed-stage source choices = %#v", transformed)
	}
}

func TestConstructionGroupSourceChoicesExcludeConstantResourceTypeMetadata(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 135}},
		nil,
		[]capability.Candidate{{
			ID: "resource-type", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "resourceType",
			Label: "Resource Type", LogicalType: "string", Cardinality: "required_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 135,
		}},
		nil,
	)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	document := authoringv2.Document{
		RootResourceType: "BodyStructure",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "BodyStructure"},
	}
	rowChoices, err := resolver.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowChoices) != 1 || rowChoices[0].Path != "resourceType" {
		t.Fatalf("fixture row choices = %#v, want only the observed resourceType choice", rowChoices)
	}
	service := &Service{config: Config{RowChoicePlanner: resolver, RowChoiceResolver: resolver}}
	base := constructionBase{snapshot: snapshot, document: document}
	sourceInput, err := service.constructionGroupSourceCapability(context.Background(), base, "body-structures", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if sourceInput.Supported || sourceInput.ReasonCode != "NO_ELIGIBLE_POPULATED_SCALAR_GROUP_FIELDS" || len(sourceInput.Choices) != 0 {
		t.Fatalf("constant resourceType was offered as a useful source group: %#v", sourceInput)
	}
	_, err = service.resolveConstructionGroupSource(context.Background(), base, ConstructionProposalRequest{
		Project: "project-a", ExplorerID: "explorer-a", OutputID: "body-structures",
		GroupSource: &ConstructionGroupSourceSelection{RowChoiceID: rowChoices[0].ChoiceID, ColumnID: "source_resource_type"},
	}, "source_resource_type")
	if lifecycleErrorCode(err) != "RESOURCE_METADATA_GROUP_SOURCE_UNSUPPORTED" {
		t.Fatalf("resourceType group request error = %v, want metadata-field rejection", err)
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
	if proposal.BaseReceiptID == "" {
		t.Fatal("proposal omitted the existing base receipt identity")
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

func TestConstructionGroupEditCannotIntroduceUnsignedSourceProjection(t *testing.T) {
	accepted := authoringv2.ConstructionSourceProjection{ColumnID: "accepted", FieldPath: "status", OwnerStepID: "group"}
	forged := authoringv2.ConstructionSourceProjection{ColumnID: "forged", FieldPath: "secret", OwnerStepID: "group"}
	base := constructionBase{construction: authoringv2.Construction{SourceProjections: []authoringv2.ConstructionSourceProjection{accepted}}}
	service := &Service{}
	candidate, err := service.constructionCandidateWithGroupSource(context.Background(), base, ConstructionProposalRequest{CandidateConstruction: authoringv2.Construction{SourceProjections: []authoringv2.ConstructionSourceProjection{forged}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.SourceProjections) != 1 || candidate.SourceProjections[0].ColumnID != "accepted" {
		t.Fatalf("unsigned source metadata accepted: %#v", candidate.SourceProjections)
	}
}

func TestConstructionWorkspaceInputsUseFinalSchemasAndExcludeTargetDependents(t *testing.T) {
	workspaceOutput := func(id string, steps ...authoringv2.ConstructionStep) authoringv2.Document {
		return authoringv2.Document{Output: authoringv2.Output{ID: id, Title: "Table " + id}, Construction: &authoringv2.Construction{Version: 1, Steps: steps}}
	}
	combineInput := func(outputID string) authoringv2.ConstructionStep {
		return authoringv2.ConstructionStep{Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: outputID}}}
	}
	base := constructionBase{
		workspace: authoringv2.Workspace{Documents: []authoringv2.Document{
			{Output: authoringv2.Output{ID: "upstream", Title: "Upstream"}},
			workspaceOutput("target"),
			workspaceOutput("depends-on-target", combineInput("target")),
			workspaceOutput("transitive-dependent", combineInput("depends-on-target")),
			{Output: authoringv2.Output{ID: "independent", Title: "Independent"}},
		}},
		receipt: &explorer.CompilationReceipt{CompiledOutputSchemas: map[string][]explorer.ReceiptCompiledOutputColumn{
			"upstream": {
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID", LogicalType: "string", Cardinality: "required_one"},
				{ID: "birth-date", Name: "birth_date", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", Nullable: true},
				{ID: "tags", Name: "tags", Label: "Tags", LogicalType: "string", Cardinality: "many"},
				{ID: "__row_id", Name: "__row_id", Label: "Internal row ID", LogicalType: "string", Cardinality: "required_one", Internal: true, Identity: true},
			},
			"target":               {{ID: "target-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"depends-on-target":    {{ID: "dependent-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"transitive-dependent": {{ID: "transitive-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"independent":          {{ID: "independent-col", Name: "id", Label: "Identifier", LogicalType: "uuid", Cardinality: "required_one", Identity: true}},
		}},
	}

	inputs, err := constructionWorkspaceInputs(base, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].OutputID != "upstream" || inputs[1].OutputID != "independent" {
		t.Fatalf("eligible current outputs = %#v, want upstream and independent only", inputs)
	}
	columns := inputs[0].Columns
	if len(columns) != 3 || columns[0].ID != "patient-id" || columns[0].JoinCompatibilityKey != "String" || columns[0].AppendCompatibilityKey != "string:String" {
		t.Fatalf("compiler source field identity/compatibility = %#v", columns)
	}
	if columns[1].ID != "birth-date" || columns[1].Nullable != true || columns[1].JoinCompatibilityKey != "Date" || columns[1].AppendCompatibilityKey != "date:Date" {
		t.Fatalf("nullable scalar compatibility = %#v", columns[1])
	}
	if columns[2].ID != "tags" || columns[2].Cardinality != "many" || columns[2].JoinCompatibilityKey != "" || columns[2].AppendCompatibilityKey != "" {
		t.Fatalf("repeated scalar compatibility = %#v", columns[2])
	}
	if inputs[1].Columns[0].ID != "independent-col" {
		t.Fatalf("visible identity column was omitted from compiler schema: %#v", inputs[1].Columns)
	}
}

func TestConstructionWorkspaceInputsRejectMissingFinalSiblingSchema(t *testing.T) {
	base := constructionBase{
		workspace: authoringv2.Workspace{Documents: []authoringv2.Document{
			{Output: authoringv2.Output{ID: "target"}},
			{Output: authoringv2.Output{ID: "sibling"}},
		}},
		receipt: &explorer.CompilationReceipt{CompiledOutputSchemas: map[string][]explorer.ReceiptCompiledOutputColumn{
			"target": {{ID: "target-id", Name: "target", Label: "Target", LogicalType: "string", Cardinality: "required_one"}},
		}},
	}
	_, err := constructionWorkspaceInputs(base, "target")
	if lifecycleErrorCode(err) != "WORKSPACE_OUTPUT_SCHEMA_UNAVAILABLE" {
		t.Fatalf("missing compiler-final sibling schema error = %v", err)
	}
}

func TestConstructionWorkspaceCompatibilityKeysMatchCompilerCombineTypeRules(t *testing.T) {
	for _, test := range []struct {
		logical     string
		cardinality string
		nullable    bool
		joinKey     string
		appendKey   string
	}{
		{logical: "string", cardinality: "required_one", joinKey: "String", appendKey: "string:String"},
		{logical: "code", cardinality: "optional_one", nullable: true, joinKey: "String", appendKey: "code:String"},
		{logical: "uuid", cardinality: "required_one", appendKey: "uuid:UUID"},
		{logical: "date", cardinality: "required_one", joinKey: "Date", appendKey: "date:Date"},
		{logical: "date_time", cardinality: "required_one", joinKey: "DateTime64(3)", appendKey: "date-time:DateTime64(3)"},
		{logical: "boolean", cardinality: "required_one", joinKey: "Bool", appendKey: "boolean:Bool"},
		{logical: "integer", cardinality: "required_one", joinKey: "Int64", appendKey: "integer:Int64"},
		{logical: "decimal", cardinality: "required_one", appendKey: "decimal:Float64"},
		{logical: "string", cardinality: "many"},
		{logical: "quantity", cardinality: "required_one"},
	} {
		logical, physical, err := lower.ConstructionCombineColumnType(recipe.StageColumn{Type: test.logical})
		if err != nil {
			gotJoin, gotAppend := constructionCombineCompatibilityKeys(test.logical, test.cardinality, test.nullable)
			if gotJoin != "" || gotAppend != "" {
				t.Errorf("unsupported logical type %q received compatibility keys (%q, %q)", test.logical, gotJoin, gotAppend)
			}
			continue
		}
		physicalType := physical
		if test.nullable {
			physicalType = "Nullable(" + physicalType + ")"
		}
		wantJoin, joinOK := ir.ClickHouseCombineScalarBaseType(physicalType, ir.PhysicalCombineKeyJoin)
		wantAppend, appendOK := ir.ClickHouseCombineScalarBaseType(physicalType, ir.PhysicalCombineAppend)
		if test.cardinality == "many" {
			joinOK, appendOK = false, false
		}
		if !joinOK {
			wantJoin = ""
		}
		if !appendOK {
			wantAppend = ""
		} else {
			wantAppend = logical + ":" + wantAppend
		}
		gotJoin, gotAppend := constructionCombineCompatibilityKeys(test.logical, test.cardinality, test.nullable)
		if gotJoin != wantJoin || gotAppend != wantAppend || gotJoin != test.joinKey || gotAppend != test.appendKey {
			t.Errorf("compatibility keys for %s/%s nullable=%t = (%q, %q), want IR (%q, %q) and contract (%q, %q)", test.logical, test.cardinality, test.nullable, gotJoin, gotAppend, wantJoin, wantAppend, test.joinKey, test.appendKey)
		}
	}
}

func TestConstructionCapabilitiesRejectStaleDraftBeforeWorkspaceSchemaProjection(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	proposal := constructionProposalRequest(store.created, snapshot, workspace.Documents[0], "filter_step")
	_, err = service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: proposal.Project, ExplorerID: proposal.ExplorerID, SnapshotToken: proposal.SnapshotToken,
		ExpectedDraftVersion: proposal.ExpectedDraftVersion + 1, ExpectedDraftDigest: proposal.ExpectedDraftDigest,
		OutputID: proposal.OutputID, StageID: recipe.ConstructionSourceProjectionID,
	})
	if lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft capability error = %v, want DRAFT_CONFLICT", err)
	}
}

func TestApplyAppendProposalPreservesExplicitlyEmptyTargetColumns(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	service.config.ConstructionSourceStage = func(_ context.Context, _ ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
		return explorer.ReceiptConstructionStage{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{},
		}, nil
	}
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = []authoringv2.Column{}
	workspace.Documents[0].Construction = nil
	workspace.Documents[0].TableShape = nil
	sibling := workspace.Documents[0]
	sibling.Output = authoringv2.Output{ID: "sibling", Title: "Sibling"}
	sibling.Columns = []authoringv2.Column{{
		ColumnID: "sibling_id", Column: "sibling_id", Label: "Sibling ID", LogicalType: "string",
		OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID},
	}}
	workspace.Documents = append(workspace.Documents, sibling)
	workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: "sibling-tab", Title: "Sibling", OutputID: "sibling", Order: 1, Visible: true})
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

	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "append_three_sources",
		Inputs: []authoringv2.ConstructionInputRef{
			{Kind: authoringv2.ConstructionInputTableRevision, TableID: "table-observations", RevisionID: "rev-source", OutputID: "observations"},
			{Kind: authoringv2.ConstructionInputTableRevision, TableID: "table-reports", RevisionID: "rev-source", OutputID: "reports"},
			{Kind: authoringv2.ConstructionInputTableRevision, TableID: "table-patients", RevisionID: "rev-source", OutputID: "source-patients"},
		},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
			Kind: authoringv2.ConstructionCombineAppend,
			Projections: []authoringv2.ConstructionCombineProjection{
				{OutputColumnID: "record_id", InputIndex: 0, InputColumnID: "observation-id"},
				{OutputColumnID: "record_id", InputIndex: 1, InputColumnID: "report-id"},
				{OutputColumnID: "record_id", InputIndex: 2, InputColumnID: "patient-id"},
				{OutputColumnID: "status", InputIndex: 0, InputColumnID: "observation-status"},
				{OutputColumnID: "status", InputIndex: 1, InputColumnID: "report-status"},
				{OutputColumnID: "patient_gender", InputIndex: 2, InputColumnID: "patient-gender"},
			},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "record_id", Name: "record_id", Label: "Record ID", Type: "string", Nullable: true},
			{ID: "status", Name: "status", Label: "Status", Type: "string", Nullable: true},
			{ID: "patient_gender", Name: "patient_gender", Label: "Patient gender", Type: "string", Nullable: true},
		},
	}}}
	proposal, err := service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
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
	service.config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if err := visit(map[string]any{"record_id": "r1", "status": "final", "patient_gender": nil}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"record_id", "status", "patient_gender"}, RowCount: 1, Complete: true}, nil
	}
	_, err = service.ApplyCommands(context.Background(), store.created.Project, store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "apply-empty-target-append", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply APPEND candidate after canonical receipt round-trip: %v", err)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("successful APPEND proposal saves = %d, want one atomic save", store.saveDraftCalls)
	}
	saved, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
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
