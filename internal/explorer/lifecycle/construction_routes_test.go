package lifecycle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func inboundPatientObservationRouteFixture(t *testing.T) (*fakeStore, *Service, capability.Snapshot, authoringv2.CatalogSnapshot, capability.Candidate) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "construction-route-snapshot", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "observation", ResourceType: "Observation"},
	}
	snapshot.Edges = []capability.Edge{{
		ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
	}}
	candidate := capability.Candidate{
		ID: "observation-status", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Observation status", LogicalType: "code", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalog := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}},
		Edges: []authoringv2.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"},
			DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
		}},
	}
	store := semanticAuthoringStore(t)
	config := Config{Capability: CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog },
	}}
	return store, newTestService(t, store, config), snapshot, catalog, candidate
}

func TestConstructionChoiceSearchAndApplyUseCompilerProvedInboundRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !search.Complete || len(search.Choices) != 1 {
		t.Fatalf("route-bound search = %#v, %v", search, err)
	}
	choice := search.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].Relationship != "subject_Patient" || choice.Route[0].StorageDirection != "INBOUND" || choice.Route[0].ToResourceType != "Observation" {
		t.Fatalf("compiler-proved inbound route = %#v", choice.Route)
	}
	var allOption *capability.ConstructionChoiceOption
	for index := range choice.Options {
		if choice.Options[index].Form == capability.ConstructionChoiceAll {
			allOption = &choice.Options[index]
		}
	}
	if allOption == nil || allOption.Shape != capability.ConstructionChoiceList || allOption.Preservation != capability.ConstructionChoicePreserving {
		t.Fatalf("related scalar ALL form = %#v", allOption)
	}
	if choice.Presentation.Summary == "" || len(choice.Presentation.Facts) == 0 || choice.Presentation.Facts[0].Label != "FHIR field" {
		t.Fatalf("generic route choice presentation = %#v", choice.Presentation)
	}

	before, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionChoiceRequest(snapshot, "inbound-route-choice", choice.ChoiceID, capability.ConstructionChoiceValue)
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	document := response.Workspace.Documents[0]
	if len(document.Route.Children) != len(before.Documents[0].Route.Children)+1 || document.Route.Children[0].OccurrenceID != before.Documents[0].Route.Children[0].OccurrenceID {
		t.Fatalf("apply did not preserve explicit branch while materializing route: before=%#v after=%#v", before.Documents[0].Route, document.Route)
	}
	column := document.Columns[len(document.Columns)-1]
	if column.Source.Field == nil || column.Source.Field.Path != "status" || column.OccurrenceID != document.Route.Children[1].OccurrenceID {
		t.Fatalf("route-bound field column = %#v", column)
	}
}

func TestRelatedExpandChoiceSearchPinsRouteToCompilerSupportedStage(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		capabilities := []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}
		columns := []explorer.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}
		anchors := []explorer.ReceiptConstructionRelatedExpandAnchor{{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}}
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{"patients": {
			{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: anchors},
			{ID: "keep_patients", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "FILTER", Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: anchors},
		}}
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
	result, err := service.SearchRelatedExpandChoices(context.Background(), RelatedExpandChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "keep_patients", AnchorColumnID: "_key", TargetResourceType: "Observation",
	})
	if err != nil || !result.Complete || len(result.Choices) != 1 {
		t.Fatalf("related expand route choices = %#v, %v", result, err)
	}
	choice := result.Choices[0]
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := identity.Source.(capability.RelatedResourceChoiceSource)
	if !ok || source.StageID != "keep_patients" || source.AnchorColumnID != "_key" || source.AnchorKind != "root" ||
		source.AnchorNodeID != "patient" || source.AnchorResourceType != "Patient" || source.NodeID != "observation" || source.ResourceType != "Observation" ||
		identity.SnapshotToken != snapshot.Token || len(identity.Route) != 1 || identity.Route[0].EdgeID != "subject-patient" {
		t.Fatalf("route choice did not bind the selected stage and exact route: source=%#v route=%#v", identity.Source, identity.Route)
	}
}

func TestRelatedExpandChoiceSearchRequiresCompilerSupportedSelectedStage(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		columns := []explorer.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}
		anchors := []explorer.ReceiptConstructionRelatedExpandAnchor{{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}}
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{"patients": {
			{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}, RelatedExpandAnchors: anchors},
			{ID: "after_group", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP", Columns: columns,
				Capabilities:         []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: false, ReasonCode: "ROOT_KEY_NOT_RETAINED", Reason: "selected stage does not retain a root resource key"}},
				RelatedExpandAnchors: anchors},
		}}
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
	request := RelatedExpandChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "after_group", AnchorColumnID: "_key", TargetResourceType: "Observation",
	}
	if _, err := service.SearchRelatedExpandChoices(context.Background(), request); err == nil || lifecycleErrorCode(err) != "NO_SOURCE_ROW_ANCHOR" {
		t.Fatalf("unsupported selected stage error = %v, want NO_SOURCE_ROW_ANCHOR", err)
	}
	request.StageID = "missing_stage"
	if _, err := service.SearchRelatedExpandChoices(context.Background(), request); err == nil || lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("missing selected stage error = %v, want STALE_STAGE_REFERENCE", err)
	}
}

func TestRelatedExpandChoicesCanStartAtRootOrCurrentRelatedRecord(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "specimen", ResourceType: "Specimen"})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "observation-specimen", FromNodeID: "observation", ToNodeID: "specimen",
		SourceResourceType: "Observation", TargetResourceType: "Specimen", Label: "specimen_Specimen", StorageDirection: "OUTBOUND",
	})
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		rootAnchor := explorer.ReceiptConstructionRelatedExpandAnchor{
			AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient",
		}
		activeAnchor := explorer.ReceiptConstructionRelatedExpandAnchor{
			AnchorColumnID: "__terminal_observation_id", Kind: "activeRelatedRecord", NodeID: "observation",
			ResourceType: "Observation", Label: "Current related Observation",
		}
		patientObservationRoute := []recipe.ConstructionRelatedRouteStep{{
			EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
			StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}
		active := &explorer.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: activeAnchor.AnchorColumnID,
		}
		relatedExpand := &explorer.ReceiptConstructionRelatedExpand{
			AnchorColumnID: "_key", AnchorColumn: "_key", AnchorKind: "root", AnchorResourceType: "Patient",
			RelatedRecordColumnID: "observation_id", ParentIdentityColumnID: "__parent_identity",
			ParentIdentityColumn: "__parent_identity", TerminalIdentityColumn: activeAnchor.AnchorColumnID,
			TargetNodeID: "observation", TargetResourceType: "Observation", Route: patientObservationRoute,
		}
		capabilities := []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}
		columns := []explorer.ReceiptConstructionStageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID"}}
		expandedColumns := append(append([]explorer.ReceiptConstructionStageColumn(nil), columns...), explorer.ReceiptConstructionStageColumn{
			ID: "observation_id", Name: "observation_id", Label: "Observation ID",
		})
		fieldColumns := append(append([]explorer.ReceiptConstructionStageColumn(nil), expandedColumns...), explorer.ReceiptConstructionStageColumn{
			ID: "observation_status", Name: "observation_status", Label: "Observation Status",
		})
		fieldCapabilities := []explorer.ReceiptConstructionOperationChoice{
			{Kind: "RELATED_EXPAND", Supported: true}, {Kind: "RELATED_FIELD", Supported: true},
		}
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{"patients": {
			{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor}},
			{ID: "expand_observations", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_EXPAND",
				Columns: expandedColumns, Capabilities: capabilities, RelatedExpand: relatedExpand, ActiveRelatedRecord: active,
				RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
			{ID: "keep_observations", InputStageID: "expand_observations", Operation: "FILTER",
				Columns: expandedColumns, Capabilities: capabilities, ActiveRelatedRecord: active,
				RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
			{ID: "add_observation_status", InputStageID: "keep_observations", Operation: "RELATED_FIELD",
				Columns: fieldColumns, Capabilities: fieldCapabilities, ActiveRelatedRecord: active,
				RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
		}}
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
	search := func(anchorColumnID, targetResourceType string) RelatedExpandChoiceSearchResponse {
		t.Helper()
		result, err := service.SearchRelatedExpandChoices(context.Background(), RelatedExpandChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			OutputID: "patients", StageID: "add_observation_status", AnchorColumnID: anchorColumnID,
			TargetResourceType: targetResourceType,
		})
		if err != nil || !result.Complete || len(result.Choices) != 1 {
			t.Fatalf("related expansion choices from %q to %s = %#v, %v", anchorColumnID, targetResourceType, result, err)
		}
		return result
	}

	activeResult := search("__terminal_observation_id", "Specimen")
	activeChoice := activeResult.Choices[0]
	if activeChoice.AnchorColumnID != "__terminal_observation_id" || activeChoice.AnchorKind != "activeRelatedRecord" ||
		activeChoice.NodeID != "observation" || activeChoice.ResourceType != "Observation" ||
		activeChoice.AnchorLabel != "Current related Observation" || len(activeChoice.Route) != 1 ||
		activeChoice.Route[0].FromNodeID != "observation" || activeChoice.Route[0].ToNodeID != "specimen" {
		t.Fatalf("active-record route was not pinned to the current related resource: %#v", activeChoice)
	}
	rootResult := search("_key", "Observation")
	rootChoice := rootResult.Choices[0]
	if rootChoice.AnchorColumnID != "_key" || rootChoice.AnchorKind != "root" || rootChoice.NodeID != "patient" ||
		rootChoice.ResourceType != "Patient" || rootChoice.AnchorLabel != "Original Patient" || len(rootChoice.Route) != 1 ||
		rootChoice.Route[0].FromNodeID != "patient" || rootChoice.Route[0].ToNodeID != "observation" {
		t.Fatalf("root sibling route was not retained at the same stage: %#v", rootChoice)
	}
}

func TestRelatedFieldChoicesAreStageBoundAndOnlyOfferLowerablePaths(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	unsupported := capability.Candidate{
		ID: "observation-indexed-code", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "component[0].code", Label: "Indexed code", LogicalType: "code", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot.Candidates = append(snapshot.Candidates, unsupported)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		route := []recipe.ConstructionRelatedRouteStep{{
			EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
			StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}
		active := &explorer.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: "__terminal_observation_id",
		}
		sourceColumns := []explorer.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
		expandedColumns := append(append([]explorer.ReceiptConstructionStageColumn(nil), sourceColumns...), explorer.ReceiptConstructionStageColumn{
			ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string",
		})
		relatedExpand := &explorer.ReceiptConstructionRelatedExpand{
			AnchorColumnID: "_key", AnchorColumn: "_key", AnchorKind: "root", AnchorNodeID: "patient", AnchorResourceType: "Patient", RelatedRecordColumnID: "observation-id",
			ParentIdentityColumnID: "__parent_identity", ParentIdentityColumn: "__parent_identity",
			TerminalIdentityColumn: active.TerminalIdentityColumn, TargetNodeID: active.TargetNodeID,
			TargetResourceType: active.TargetResourceType, Route: route,
		}
		fieldCapability := []explorer.ReceiptConstructionOperationChoice{{Kind: "RELATED_FIELD", Supported: true}}
		rootAnchor := explorer.ReceiptConstructionRelatedExpandAnchor{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}
		activeAnchor := explorer.ReceiptConstructionRelatedExpandAnchor{AnchorColumnID: active.TerminalIdentityColumn, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related Observation"}
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{"patients": {
			{ID: recipe.ConstructionSourceProjectionID, Columns: sourceColumns, Capabilities: []explorer.ReceiptConstructionOperationChoice{
				{Kind: "RELATED_EXPAND", Supported: true},
				{Kind: "RELATED_FIELD", Supported: false, ReasonCode: "NO_ACTIVE_RELATED_RECORD", Reason: "no active related record"},
			}, RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor}},
			{ID: "expand_observations", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_EXPAND",
				Columns: expandedColumns, Capabilities: fieldCapability, RelatedExpand: relatedExpand, ActiveRelatedRecord: active,
				RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
			{ID: "keep_observations", InputStageID: "expand_observations", Operation: "FILTER",
				Columns: expandedColumns, Capabilities: fieldCapability, ActiveRelatedRecord: active,
				RelatedExpandAnchors: []explorer.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
		}}
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
	response, err := service.SearchRelatedFieldChoices(context.Background(), RelatedFieldChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "keep_observations", Query: "status", Limit: 20,
	})
	if err != nil || !response.Complete || response.SnapshotToken != snapshot.Token || response.OutputID != "patients" ||
		response.StageID != "keep_observations" || response.DraftVersion != store.created.DraftVersion ||
		response.DraftDigest != store.created.DraftDigest || len(response.Choices) != 1 {
		t.Fatalf("stage-bound exact related field choices = %#v, %v", response, err)
	}
	choice := response.Choices[0]
	if choice.Label != candidate.Label || choice.Source.Kind != capability.ConstructionChoiceSourceField || choice.Source.CandidateID != candidate.ID ||
		choice.Source.NodeID != "observation" || choice.Source.ResourceType != "Observation" {
		t.Fatalf("related field identity = %#v", choice)
	}
	if choice.Source.Path != "status" {
		t.Fatalf("related field path = %q", choice.Source.Path)
	}
	if choice.Source.Cardinality != "optional_one" {
		t.Fatalf("related field cardinality = %q", choice.Source.Cardinality)
	}
	if choice.Source.LogicalType != "string" {
		t.Fatalf("related field logical type = %q", choice.Source.LogicalType)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	issued, ok := identity.Source.(capability.RelatedFieldChoiceSource)
	if !ok || identity.SnapshotToken != snapshot.Token || issued.StageID != "keep_observations" ||
		issued.CandidateID != candidate.ID || issued.NodeID != "observation" || issued.ResourceType != "Observation" ||
		issued.Path != "status" || issued.LogicalType != "string" {
		t.Fatalf("related field choice was not bound to snapshot, stage, and exact candidate: %#v", identity)
	}
	for _, got := range response.Choices {
		if got.Source.CandidateID == unsupported.ID {
			t.Fatalf("unsupported indexed path was offered as executable choice: %#v", got)
		}
	}
}

func TestRelatedFieldChoicesRequireActiveStageIdentity(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{"patients": {
			{ID: recipe.ConstructionSourceProjectionID, Columns: []explorer.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}},
		}}
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
	request := RelatedFieldChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	}
	if _, err := service.SearchRelatedFieldChoices(context.Background(), request); err == nil || lifecycleErrorCode(err) != "NO_ACTIVE_RELATED_RECORD" {
		t.Fatalf("source-stage related field choices error = %v, want NO_ACTIVE_RELATED_RECORD", err)
	}
	request.StageID = "missing-stage"
	if _, err := service.SearchRelatedFieldChoices(context.Background(), request); err == nil || lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("missing-stage related field choices error = %v, want STALE_STAGE_REFERENCE", err)
	}
}

func TestParallelConstructionChoicesApplyToDistinctPinnedOccurrences(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 2 {
		t.Fatalf("parallel route choices = %#v, %v", search.Choices, err)
	}
	choices := map[string]string{}
	for _, choice := range search.Choices {
		identity, decodeErr := capability.DecodeConstructionChoiceID(choice.ChoiceID)
		if decodeErr != nil || len(identity.Route) != 1 {
			t.Fatalf("route choice identity = %#v, %v", identity, decodeErr)
		}
		choices[identity.Route[0].EdgeID] = choice.ChoiceID
	}
	if choices["subject-patient"] == "" || choices["subject-patient-parallel"] == "" {
		t.Fatalf("parallel choices did not retain both exact edge IDs: %#v", choices)
	}
	initialWorkspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	initialRouteChildren := len(initialWorkspace.Documents[0].Route.Children)

	var document authoringv2.Document
	for index, edgeID := range []string{"subject-patient", "subject-patient-parallel"} {
		request := constructionChoiceRequest(snapshot, "parallel-choice-"+edgeID, choices[edgeID], capability.ConstructionChoiceValue)
		request.ExpectedDraftVersion = store.created.DraftVersion
		response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
		if applyErr != nil {
			t.Fatalf("apply exact route %q: %v", edgeID, applyErr)
		}
		document = response.Workspace.Documents[0]
		if index == 1 && len(document.Route.Children) != initialRouteChildren+2 {
			t.Fatalf("parallel routes produced %d total occurrences after starting with %d, want two new siblings", len(document.Route.Children), initialRouteChildren)
		}
	}
	if len(document.Columns) < 3 || document.Columns[len(document.Columns)-2].OccurrenceID == document.Columns[len(document.Columns)-1].OccurrenceID {
		t.Fatalf("parallel route columns share an occurrence: %#v", document.Columns)
	}

	var persisted struct {
		Documents []struct {
			Route struct {
				Children []struct {
					OccurrenceID  string `json:"occurrenceId"`
					CatalogEdgeID string `json:"catalogEdgeId"`
				} `json:"children"`
			} `json:"route"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(store.created.DraftConfig, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Documents) != 1 || len(persisted.Documents[0].Route.Children) != initialRouteChildren+2 {
		t.Fatalf("reloaded workspace lost parallel route occurrences: %#v", persisted)
	}
	occurrences := map[string]string{}
	for _, child := range persisted.Documents[0].Route.Children {
		if child.CatalogEdgeID != "" {
			occurrences[child.CatalogEdgeID] = child.OccurrenceID
		}
	}
	if occurrences["subject-patient"] == "" || occurrences["subject-patient-parallel"] == "" || occurrences["subject-patient"] == occurrences["subject-patient-parallel"] {
		t.Fatalf("reloaded route did not retain distinct edge identities: %#v", occurrences)
	}
	if _, err := authoringv2.DecodeWorkspace(store.created.DraftConfig); err != nil {
		t.Fatal(err)
	}
	for edgeID, occurrenceID := range occurrences {
		result, searchErr := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: occurrenceID,
			Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
		})
		if searchErr != nil || len(result.Choices) != 1 {
			t.Fatalf("reloaded occurrence %q route = %#v, %v", occurrenceID, result, searchErr)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(result.Choices[0].ChoiceID)
		if decodeErr != nil || len(identity.Route) != 1 || identity.Route[0].EdgeID != edgeID {
			t.Fatalf("reloaded occurrence %q resolved to %#v, %v; want %q", occurrenceID, identity.Route, decodeErr, edgeID)
		}
		var columnID string
		for _, column := range document.Columns {
			if column.OccurrenceID == occurrenceID {
				columnID = column.Column
				break
			}
		}
		if columnID == "" {
			t.Fatalf("reloaded occurrence %q has no authored column", occurrenceID)
		}
		source, sourceErr := service.ColumnSource(context.Background(), ColumnSourceRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", Column: columnID,
		})
		if sourceErr != nil || len(source.Route) != 2 || source.Route[1].CatalogEdgeID != edgeID {
			t.Fatalf("reloaded occurrence %q source route = %#v, %v; want %q", occurrenceID, source.Route, sourceErr, edgeID)
		}
	}
}

func TestConstructionChoiceRejectsAmbiguousLegacyPrefix(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "legacy-observation", ResourceType: "Observation", Relationship: "subject_Patient"}},
	})
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 2 {
		t.Fatalf("route search = %#v, %v", search, err)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	request := constructionChoiceRequest(snapshot, "ambiguous-legacy-prefix", search.Choices[0].ChoiceID, capability.ConstructionChoiceValue)
	request.ExpectedDraftVersion = version
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("construction choice reused an ambiguous legacy occurrence")
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("rejected ambiguous legacy route mutated workspace: saves=%d", store.saveDraftCalls)
	}
}

func TestConstructionChoiceSearchScopesToSavedOccurrenceRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "saved-observation", ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional}},
	})
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "saved-observation",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !result.Complete || result.Truncated || result.NextCursor != "" || len(result.Choices) != 1 {
		t.Fatalf("occurrence-scoped search = %#v, %v", result, err)
	}
	choice := result.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].EdgeID != "subject-patient" || choice.Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("occurrence did not resolve to exact saved inbound route: %#v", choice.Route)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("read-only occurrence search saved a draft %d times", store.saveDraftCalls)
	}
}

func TestApplyConstructionChoiceReusesTheSavedOccurrencePrefix(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "saved-observation", ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional}},
	})
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "saved-observation",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 1 {
		t.Fatalf("occurrence-scoped search = %#v, %v", search, err)
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "reuse-saved-route", search.Choices[0].ChoiceID, capability.ConstructionChoiceValue), "alice")
	if err != nil {
		t.Fatal(err)
	}
	document := response.Workspace.Documents[0]
	if len(document.Route.Children) != 1 || document.Route.Children[0].OccurrenceID != "saved-observation" || len(document.Columns) != 2 || document.Columns[1].OccurrenceID != "saved-observation" {
		t.Fatalf("apply did not reuse the selected authored occurrence: route=%#v columns=%#v", document.Route, document.Columns)
	}
}

func TestConstructionChoiceSearchRejectsOccurrenceOutsideOutputOrWrongTerminal(t *testing.T) {
	tests := []struct {
		name         string
		prepare      func(*testing.T, *fakeStore)
		occurrenceID string
	}{
		{
			name: "occurrence belongs to a different output",
			prepare: func(t *testing.T, store *fakeStore) {
				workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
				if err != nil {
					t.Fatal(err)
				}
				workspace.Documents = append(workspace.Documents, authoringv2.Document{
					Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "other", Title: "Other"}, RootResourceType: "Patient",
					Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "outside-output", ResourceType: "Observation", Relationship: "subject_Patient"}}},
					Columns: []authoringv2.Column{},
				})
				workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: "other-tab", Title: "Other", OutputID: "other", Order: len(workspace.Tabs), Visible: true})
				persistTestWorkspace(t, store, workspace)
			},
			occurrenceID: "outside-output",
		},
		{
			name: "saved occurrence terminates at a different resource type",
			prepare: func(t *testing.T, store *fakeStore) {
				setSavedConstructionRoute(t, store, authoringv2.RouteNode{
					OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
					Children: []authoringv2.RouteNode{{OccurrenceID: "wrong-terminal", ResourceType: "Encounter", Relationship: "encounters"}},
				})
			},
			occurrenceID: "wrong-terminal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
			test.prepare(t, store)
			_, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
				Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: test.occurrenceID,
				Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
			})
			if err == nil || store.saveDraftCalls != 0 {
				t.Fatalf("invalid occurrence was accepted or mutated state: err=%v saves=%d", err, store.saveDraftCalls)
			}
		})
	}
}

func TestConstructionChoiceSearchRejectsAmbiguousOccurrenceEdgeResolution(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "ambiguous-observation", ResourceType: "Observation", Relationship: "subject_Patient"}},
	})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "subject-patient-parallel", FromNodeID: "patient", ToNodeID: "observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
	})
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	_, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "ambiguous-observation",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err == nil || store.saveDraftCalls != 0 {
		t.Fatalf("ambiguous occurrence route was accepted or mutated state: err=%v saves=%d", err, store.saveDraftCalls)
	}
}

func setSavedConstructionRoute(t *testing.T, store *fakeStore, route authoringv2.RouteNode) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Route = route
	persistTestWorkspace(t, store, workspace)
}

func persistTestWorkspace(t *testing.T, store *fakeStore, workspace authoringv2.Workspace) {
	t.Helper()
	data, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = data
	store.created.DraftDigest = digest
}

func TestConstructionChoiceSearchRequiresCompleteRouteCompilerProof(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot.Edges[0].Label = "not_a_fhir_relationship"
	catalog.Edges[0].Label = snapshot.Edges[0].Label
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog }
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(result.Choices) != 0 || store.saveDraftCalls != 0 {
		t.Fatalf("unproven route advertised/mutated: result=%#v err=%v saves=%d", result, err, store.saveDraftCalls)
	}
}

func TestConstructionChoiceRejectsTamperedRouteAtomically(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 1 {
		t.Fatalf("route search = %#v, %v", search, err)
	}
	choiceID := search.Choices[0].ChoiceID
	parts := strings.Split(choiceID, ".")
	parts[2] = "0" + parts[2][1:]
	request := constructionChoiceRequest(snapshot, "tampered-route-choice", strings.Join(parts, "."), capability.ConstructionChoiceValue)
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("tampered route choice was applied")
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("tampered route choice mutated draft: saves=%d", store.saveDraftCalls)
	}
}

func TestPopulationRoutesApplyAndColumnSourceAreGenericAndReadOnly(t *testing.T) {
	store, service, snapshot, catalog, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || !routes.Complete || len(routes.Choices) != 1 || routes.Choices[0].Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("population route choices = %#v, %v", routes, err)
	}
	digest := store.created.DraftDigest
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-observation-population", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID}},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	population := response.Workspace.Documents[0].Population
	if population == nil || len(population.Route) != 1 || population.Route[0].ResourceType != "Observation" || population.Route[0].Relationship != "subject_Patient" {
		t.Fatalf("applied population route = %#v", population)
	}

	workspace := response.Workspace
	occurrenceID := "observation-source"
	workspace.Documents[0].Route.Children = append(workspace.Documents[0].Route.Children, authoringv2.RouteNode{OccurrenceID: occurrenceID, ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional})
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
		Column: "observation_status", Label: "Observation status", LogicalType: "code", OccurrenceID: occurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "status", ProjectionMode: "VALUE"}},
	})
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	inspected, err := service.ColumnSource(context.Background(), ColumnSourceRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", Column: "observation_status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Summary == "" || len(inspected.Route) != 2 || inspected.Route[1].OccurrenceID != occurrenceID || inspected.Route[1].StorageDirection != "INBOUND" {
		t.Fatalf("column-source route and summary = %#v", inspected)
	}
	if len(inspected.Facts) == 0 || inspected.Facts[0].Label != "Value type" || inspected.Facts[0].Value != "code" {
		t.Fatalf("column-source facts = %#v", inspected.Facts)
	}
	if string(before) != string(store.created.DraftConfig) || store.saveDraftCalls != 1 {
		t.Fatalf("column-source read mutated workspace: saves=%d", store.saveDraftCalls)
	}
	if catalog.SnapshotToken != snapshot.Token {
		t.Fatalf("fixture catalog snapshot changed unexpectedly")
	}
}

func TestPopulationRouteResolutionPreservesPinnedEdgesAndRejectsAmbiguousLegacyRoutes(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	if len(snapshot.Edges) == 0 {
		t.Fatal("fixture has no capability edges")
	}
	routeEdge := snapshot.Edges[0]
	sourceNode, ok := snapshot.Node(routeEdge.FromNodeID)
	if !ok {
		t.Fatalf("fixture edge source %q is missing", routeEdge.FromNodeID)
	}
	targetNode, ok := snapshot.Node(routeEdge.ToNodeID)
	if !ok {
		t.Fatalf("fixture edge target %q is missing", routeEdge.ToNodeID)
	}
	selectionResourceType := targetNode.ResourceType
	store.selection = completedTestSelection(snapshot, selectionResourceType)
	workspaceForRoute := func(t *testing.T, route authoringv2.PopulationRouteStep) authoringv2.Workspace {
		t.Helper()
		workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		workspace.Documents[0].Population = &authoringv2.Population{
			SelectionRevisionID: "selection-1",
			Route:               []authoringv2.PopulationRouteStep{route},
		}
		return workspace
	}
	resolve := func(workspace authoringv2.Workspace, current capability.Snapshot) error {
		_, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, current, snapshot.Identity.AuthorizationScopeDigest)
		return err
	}
	snapshotWithEdges := func(edges []capability.Edge) capability.Snapshot {
		return capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated,
			snapshot.Nodes, edges, snapshot.Candidates, snapshot.Diagnostics)
	}
	step := authoringv2.PopulationRouteStep{
		ResourceType: targetNode.ResourceType, Relationship: routeEdge.Label, CatalogEdgeID: routeEdge.ID,
	}

	t.Run("stale pin does not fall back to a same-labeled replacement", func(t *testing.T) {
		replacement := routeEdge
		replacement.ID = "replacement-edge"
		current := snapshotWithEdges([]capability.Edge{replacement})
		want := `catalog edge "` + routeEdge.ID + `" is unavailable`
		if err := resolve(workspaceForRoute(t, step), current); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("stale pinned population route error = %v", err)
		}
	})

	t.Run("pinned id must still identify the same route step", func(t *testing.T) {
		edges := append([]capability.Edge(nil), snapshot.Edges...)
		edges[0].Label = "substituted-relationship"
		current := snapshotWithEdges(edges)
		want := `catalog edge "` + routeEdge.ID + `" no longer identifies this route step`
		if err := resolve(workspaceForRoute(t, step), current); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("substituted pinned population route error = %v", err)
		}
	})

	t.Run("unique legacy route remains resolvable", func(t *testing.T) {
		legacyStep := step
		legacyStep.CatalogEdgeID = ""
		resolved, _, err := resolvePopulationRouteEdge(snapshot, routeEdge.FromNodeID, sourceNode.ResourceType, legacyStep)
		if err != nil || resolved.ID != routeEdge.ID {
			t.Fatalf("unique legacy route resolved edge=%#v err=%v", resolved, err)
		}
		if err := resolve(workspaceForRoute(t, legacyStep), snapshotWithEdges(snapshot.Edges)); err != nil {
			t.Fatalf("unique legacy population route was rejected: %v", err)
		}
	})

	t.Run("ambiguous legacy route fails explicitly", func(t *testing.T) {
		parallel := routeEdge
		parallel.ID = "replacement-edge"
		edges := append(append([]capability.Edge(nil), snapshot.Edges...), parallel)
		current := snapshotWithEdges(edges)
		legacyStep := step
		legacyStep.CatalogEdgeID = ""
		if err := resolve(workspaceForRoute(t, legacyStep), current); err == nil || !strings.Contains(err.Error(), "semantic tuple resolves to multiple catalog edges") {
			t.Fatalf("ambiguous legacy population route error = %v", err)
		}
	})
}

func TestPopulationRouteChoicePersistsExactParallelEdgeAndReloads(t *testing.T) {
	store, service, snapshot, catalog, _ := inboundPatientObservationRouteFixture(t)
	snapshot, _ = addParallelObservationRoute(service, snapshot, catalog)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || len(routes.Choices) != 2 {
		t.Fatalf("parallel population route choices = %#v, %v", routes, err)
	}
	choiceID := ""
	for _, choice := range routes.Choices {
		identity, decodeErr := capability.DecodePopulationRouteChoiceID(choice.RouteChoiceID)
		if decodeErr == nil && len(identity.Route) == 1 && identity.Route[0].EdgeID == "subject-patient-parallel" {
			choiceID = choice.RouteChoiceID
		}
	}
	if choiceID == "" {
		t.Fatal("population search did not issue the parallel exact edge choice")
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-parallel-population", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: "selection-1", RouteChoiceID: choiceID}},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveWorkspacePopulations(context.Background(), "project-a", reloaded, snapshot, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		t.Fatalf("reloaded exact population route failed validation: %v", err)
	}
	var persisted struct {
		Documents []struct {
			Population struct {
				Route []struct {
					CatalogEdgeID string `json:"catalogEdgeId"`
				} `json:"route"`
			} `json:"population"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(store.created.DraftConfig, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Documents) != 1 || len(persisted.Documents[0].Population.Route) != 1 || persisted.Documents[0].Population.Route[0].CatalogEdgeID != "subject-patient-parallel" {
		t.Fatalf("reloaded population route lost exact edge identity: %#v", persisted)
	}
}

func addParallelObservationRoute(service *Service, snapshot capability.Snapshot, catalog authoringv2.CatalogSnapshot) (capability.Snapshot, authoringv2.CatalogSnapshot) {
	edge := snapshot.Edges[0]
	edge.ID = "subject-patient-parallel"
	snapshot.Edges = append(snapshot.Edges, edge)
	catalogEdge := catalog.Edges[0]
	catalogEdge.ID = edge.ID
	catalog.Edges = append(catalog.Edges, catalogEdge)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service.config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return catalog
	}
	return snapshot, catalog
}

func TestPopulationRouteRejectionIsAtomicWhenChoiceIsTampered(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || len(routes.Choices) != 1 {
		t.Fatalf("population route search = %#v, %v", routes, err)
	}
	parts := strings.Split(routes.Choices[0].RouteChoiceID, ".")
	parts[2] = "0" + parts[2][1:]
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "tampered-population-route", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", SelectionRevisionID: "selection-1", RouteChoiceID: strings.Join(parts, ".")}},
	}, "alice")
	if err == nil || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("tampered population route err=%v saves=%d", err, store.saveDraftCalls)
	}
}
