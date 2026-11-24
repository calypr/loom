package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func inboundPatientObservationRouteFixture(t *testing.T, options ...testServiceOption) (*fakeStore, *Service, capability.Snapshot, ws.CatalogSnapshot, capability.Candidate) {
	t.Helper()
	setup, candidate := inboundPatientObservationRouteSetup(t, options...)
	return setup.Store, newTestServiceFromSetup(t, setup), setup.Snapshot, setup.Catalog, candidate
}

func inboundPatientObservationRouteSetup(t *testing.T, options ...testServiceOption) (testServiceSetup, capability.Candidate) {
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
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}},
		Edges: []ws.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}},
		Candidates: []ws.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"},
			DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
		}},
	}
	store := semanticAuthoringStore(t)
	config := Config{Capability: ports.CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog },
	}}
	setup := testServiceSetup{Config: &config, Store: store, Snapshot: snapshot, Catalog: catalog}
	for _, option := range options {
		option(&setup)
	}
	return setup, candidate
}

func constructionRouteTestReceipt(config *Config, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
	snapshot := request.Authorized.Snapshot
	prepared, err := ws.PrepareWorkspaceForCompilation(request.Workspace, config.Capability.Catalog(snapshot, request.ExplorerID))
	if err != nil {
		return nil, err
	}
	return lifecycleConstructionReceipt(snapshot, prepared), nil
}

func TestConstructionChoiceSearchAndApplyUseCompilerProvedInboundRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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

	before, err := ws.DecodeWorkspace(store.created.DraftConfig)
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

func TestConstructionChoiceAppliesRelatedAllForScalarField(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 1 {
		t.Fatalf("route-bound search = %#v, %v", search, err)
	}
	var allSupported bool
	for _, option := range search.Choices[0].Options {
		if option.Form == capability.ConstructionChoiceAll && option.Support == capability.ConstructionChoiceSupported {
			allSupported = true
		}
	}
	if !allSupported {
		t.Fatalf("related scalar ALL form was not advertised: %#v", search.Choices[0].Options)
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients",
		constructionChoiceRequest(snapshot, "inbound-route-all", search.Choices[0].ChoiceID, capability.ConstructionChoiceAll), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("applying advertised ALL saved the draft %d times, want once", store.saveDraftCalls)
	}
	document := response.Workspace.Documents[0]
	column := document.Columns[len(document.Columns)-1]
	if column.Source.Field == nil || column.Source.Field.Path != "status" || column.Source.Field.ProjectionMode != "ALL" ||
		column.OccurrenceID != document.Route.Children[len(document.Route.Children)-1].OccurrenceID {
		t.Fatalf("applied route-bound ALL source = %#v", column)
	}
}

func TestConstructionChoiceSearchExplainsUnsupportedObjectField(t *testing.T) {
	setup, candidate := inboundPatientObservationRouteSetup(t)
	snapshot := setup.Snapshot
	unsupported := candidate
	unsupported.NodeID = "patient"
	unsupported.ResourceType = "Patient"
	unsupported.FieldPath = "name[]"
	unsupported.LogicalType = "unknown"
	child := capability.Candidate{
		ID: "patient-name-text", NodeID: "patient", ResourceType: "Patient",
		FieldPath: "name[].text", LogicalType: "string", Cardinality: "optional_one",
	}
	snapshot.Candidates = []capability.Candidate{unsupported, child}
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}

	service := newTestServiceFromSetup(t, setup)
	_, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: unsupported.ID},
	})
	apiErr, ok := err.(*ports.Error)
	if !ok || apiErr.Code != "UNSUPPORTED_CONSTRUCTION_SOURCE_TYPE" ||
		!strings.Contains(apiErr.Message, "Patient.name[]") || !strings.Contains(apiErr.Message, "Patient.name[].text") {
		t.Fatalf("unsupported field error = %#v, want field and typed child guidance", err)
	}
}

func TestRelatedExpandChoiceSearchPinsRouteToCompilerSupportedStage(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
			if prepareErr != nil {
				return nil, prepareErr
			}
			capabilities := []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}
			columns := []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}
			anchors := []receiptmodel.ReceiptConstructionRelatedExpandAnchor{{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: anchors},
				{ID: "keep_patients", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "FILTER", Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: anchors},
			}}
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
	})
	result, err := service.SearchRelatedExpandChoices(context.Background(), construction.RelatedExpandChoiceSearchRequest{
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

	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Route.Children = append(workspace.Documents[0].Route.Children, ws.RouteNode{
		OccurrenceID: "saved-for-expansion", ResourceType: "Observation",
		Relationship: "subject_Patient", CatalogEdgeID: "subject-patient", MatchMode: ws.RouteMatchRequired,
	})
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SearchRelatedExpandChoices(context.Background(), construction.RelatedExpandChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "keep_patients", AnchorColumnID: "_key", TargetResourceType: "Observation",
		SavedGraphOnly: true,
	})
	if err != nil || !saved.Complete || saved.NextCursor != "" || len(saved.Choices) != 1 {
		t.Fatalf("saved graph route choices = %#v, %v", saved, err)
	}
	savedRoute := saved.Choices[0].Route
	if len(savedRoute) != 1 || savedRoute[0].EdgeID != "subject-patient" || savedRoute[0].MatchMode != "REQUIRED" {
		t.Fatalf("saved graph choice changed its exact edge or match policy: %#v", savedRoute)
	}

}

func TestRelatedExpandContributorChoicesBindToSelectedRootAndActiveRoutes(t *testing.T) {
	setup, candidate := inboundPatientObservationRouteSetup(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			prepared, prepareErr := ws.PrepareWorkspaceForCompilation(request.Workspace, setup.Catalog)
			if prepareErr != nil {
				return nil, prepareErr
			}
			receipt := lifecycleConstructionReceipt(setup.Snapshot, prepared)
			root := receiptmodel.ReceiptConstructionRelatedExpandAnchor{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}
			active := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: "__terminal_observation_id", Kind: "activeRelatedRecord", NodeID: "observation",
				ResourceType: "Observation", Label: "Current related Observation",
			}
			activeRecord := &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: active.AnchorColumnID,
			}
			supported := []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}
			sourceColumns := []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
			expandedColumns := append(append([]receiptmodel.ReceiptConstructionStageColumn(nil), sourceColumns...), receiptmodel.ReceiptConstructionStageColumn{
				ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string",
			})
			patientObservationRoute := []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}}
			relatedExpand := &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: "_key", AnchorColumn: "_key", AnchorKind: "root", AnchorNodeID: "patient", AnchorResourceType: "Patient",
				RelatedRecordColumnID: "observation_id", ParentIdentityColumnID: "__parent_identity", ParentIdentityColumn: "__parent_identity",
				TerminalIdentityColumn: active.AnchorColumnID, TargetNodeID: "observation", TargetResourceType: "Observation", Route: patientObservationRoute,
			}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: sourceColumns, Capabilities: supported, RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{root}},
				{ID: "expand_observations", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_EXPAND",
					Columns: expandedColumns, Capabilities: supported, RelatedExpand: relatedExpand, ActiveRelatedRecord: activeRecord,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{root, active}},
			}}
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
	})
	store := setup.Store
	snapshot := setup.Snapshot
	catalog := setup.Catalog
	candidate.Observed = true
	candidate.Populated = true
	candidate.ObservedDocumentCount = 7
	candidate.SuggestedValues = []string{"final", "preliminary"}
	candidate.SuggestionsComplete = true
	snapshot.Candidates[0] = candidate
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "observation-id", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "id", Label: "FHIR resource ID", LogicalType: "string", Cardinality: "required_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	})
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "observation-value", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueQuantity.value", Label: "Observation value", LogicalType: "decimal", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	})
	repeatedCode := capability.Candidate{
		ID: "observation-category-code", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "category[].coding[].code", Label: "Observation category code", LogicalType: "code", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4},
		},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SuggestedValues: []string{"d", "t"},
		SuggestionsComplete: true,
	}
	snapshotCandidate := repeatedCode
	snapshotCandidate.ProjectionModes = nil
	snapshot.Candidates = append(snapshot.Candidates, snapshotCandidate)
	repeatedCatalogChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, repeatedCode)
	if err != nil {
		t.Fatal(err)
	}
	catalog.Candidates = append(catalog.Candidates, ws.CatalogCandidate{
		ID: repeatedCode.ID, NodeID: repeatedCode.NodeID, FieldPath: repeatedCode.FieldPath, Label: repeatedCode.Label,
		LogicalType: repeatedCode.LogicalType, Cardinality: repeatedCode.Cardinality, Repeated: true,
		ProjectionModes: []string{"ARRAY"}, DefaultProjectionMode: "ARRAY",
		RepeatedBoundaries: []ws.RepeatedBoundary{{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4}},
		ConstructionChoice: &repeatedCatalogChoice,
	})
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "specimen-id", NodeID: "specimen", ResourceType: "Specimen",
		FieldPath: "id", Label: "FHIR resource ID", LogicalType: "string", Cardinality: "required_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	})
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "specimen", ResourceType: "Specimen"})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "observation-specimen", FromNodeID: "observation", ToNodeID: "specimen",
		SourceResourceType: "Observation", TargetResourceType: "Specimen", Label: "specimen_Specimen", StorageDirection: "OUTBOUND",
	})
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	setup.Config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }

	service := newTestServiceFromSetup(t, setup)
	searchRoute := func(stageID, anchorColumnID, targetResourceType string) construction.RelatedExpandRouteChoice {
		t.Helper()
		result, err := service.SearchRelatedExpandChoices(context.Background(), construction.RelatedExpandChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			OutputID: "patients", StageID: stageID, AnchorColumnID: anchorColumnID, TargetResourceType: targetResourceType,
		})
		if err != nil || !result.Complete || len(result.Choices) != 1 {
			t.Fatalf("route choice from stage %s anchor %s to %s = %#v, %v", stageID, anchorColumnID, targetResourceType, result, err)
		}
		return result.Choices[0]
	}

	searchContributors := func(stageID string, route construction.RelatedExpandRouteChoice, query string, limit int, cursor string) construction.RelatedExpandContributorChoiceSearchResponse {
		t.Helper()
		result, err := service.SearchRelatedExpandContributorChoices(context.Background(), construction.RelatedExpandContributorChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
			ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
			OutputID: "patients", StageID: stageID, RouteChoiceID: route.ChoiceID, Query: query, Limit: limit, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("contributor search for route %s: %v", route.ChoiceID, err)
		}
		return result
	}

	rootRoute := searchRoute(recipe.ConstructionSourceProjectionID, "_key", "Observation")
	rootResult := searchContributors(recipe.ConstructionSourceProjectionID, rootRoute, "status", 10, "")
	if !rootResult.Complete || rootResult.Truncated || rootResult.RouteChoiceID != rootRoute.ChoiceID || len(rootResult.Choices) != 1 {
		t.Fatalf("root-route contributors = %#v", rootResult)
	}
	rootChoice := rootResult.Choices[0]
	if rootChoice.Source.CandidateID != candidate.ID || rootChoice.Source.NodeID != "observation" ||
		rootChoice.Source.ResourceType != "Observation" || rootChoice.Source.Path != "status" ||
		rootChoice.Source.LogicalType != "string" || rootChoice.Label != candidate.Label ||
		len(rootChoice.Operators) != 2 || rootChoice.Operators[0] != "EXISTS" || rootChoice.Operators[1] != "EQUALS" ||
		!rootChoice.SuggestionsComplete || rootChoice.SuggestionsTruncated || rootChoice.SuggestionsSource != "catalog" ||
		len(rootChoice.SuggestedValues) != 2 || rootChoice.SuggestedValues[0] != "final" || rootChoice.SuggestedValues[1] != "preliminary" {
		t.Fatalf("root-route catalog choice = %#v", rootChoice)
	}
	repeatedResult := searchContributors(recipe.ConstructionSourceProjectionID, rootRoute, "category[].coding", 10, "")
	if !repeatedResult.Complete || len(repeatedResult.Choices) != 1 {
		t.Fatalf("repeated scalar contributor choices = %#v", repeatedResult)
	}
	repeatedChoice := repeatedResult.Choices[0]
	if repeatedChoice.Source.CandidateID != repeatedCode.ID || repeatedChoice.Source.Path != repeatedCode.FieldPath ||
		repeatedChoice.Source.Cardinality != "many" || repeatedChoice.Source.LogicalType != "string" ||
		!reflect.DeepEqual(repeatedChoice.Source.RepeatedBoundaries, repeatedCode.RepeatedBoundaries) ||
		len(repeatedChoice.Operators) != 2 || repeatedChoice.Operators[0] != "EXISTS" || repeatedChoice.Operators[1] != "EQUALS" ||
		!reflect.DeepEqual(repeatedChoice.SuggestedValues, []string{"d", "t"}) {
		t.Fatalf("repeated contributor choice = %#v", repeatedChoice)
	}
	repeatedIdentity, err := capability.DecodeConstructionChoiceID(repeatedChoice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	signedRepeatedSource, ok := repeatedIdentity.Source.(capability.FieldChoiceSource)
	if !ok || !reflect.DeepEqual(signedRepeatedSource.RepeatedBoundaries, repeatedChoice.Source.RepeatedBoundaries) ||
		!reflect.DeepEqual(repeatedIdentity.Route, rootRoute.Route) {
		t.Fatalf("repeated contributor choice did not sign exact boundaries and route: source=%#v route=%#v", repeatedIdentity.Source, repeatedIdentity.Route)
	}
	rootIdentity, err := capability.DecodeConstructionChoiceID(rootChoice.ChoiceID)
	if err != nil || !reflect.DeepEqual(rootIdentity.Route, rootRoute.Route) {
		t.Fatalf("contributor choice did not retain the selected route: identity=%#v err=%v", rootIdentity, err)
	}
	firstPage, err := service.SearchRelatedExpandContributorChoices(context.Background(), construction.RelatedExpandContributorChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID, RouteChoiceID: rootRoute.ChoiceID, Limit: 1,
	})
	if err != nil || firstPage.Complete || !firstPage.Truncated || firstPage.NextCursor == "" || len(firstPage.Choices) != 1 {
		t.Fatalf("first contributor page = %#v, %v", firstPage, err)
	}
	secondPage, err := service.SearchRelatedExpandContributorChoices(context.Background(), construction.RelatedExpandContributorChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID, RouteChoiceID: rootRoute.ChoiceID,
		Limit: 1, Cursor: firstPage.NextCursor,
	})
	if err != nil || secondPage.RouteChoiceID != rootRoute.ChoiceID || len(secondPage.Choices) != 1 {
		t.Fatalf("second contributor page = %#v, %v", secondPage, err)
	}
	numericResult := searchContributors(recipe.ConstructionSourceProjectionID, rootRoute, "valueQuantity.value", 10, "")
	if len(numericResult.Choices) != 1 || numericResult.Choices[0].Source.LogicalType != "decimal" ||
		len(numericResult.Choices[0].Operators) != 1 || numericResult.Choices[0].Operators[0] != "EXISTS" {
		t.Fatalf("numeric contributor operators = %#v", numericResult.Choices)
	}

	activeRoute := searchRoute("expand_observations", "__terminal_observation_id", "Specimen")
	activeResult := searchContributors("expand_observations", activeRoute, "id", 10, "")
	if !activeResult.Complete || activeResult.RouteChoiceID != activeRoute.ChoiceID || len(activeResult.Choices) != 1 {
		t.Fatalf("active-route contributors = %#v", activeResult)
	}
	activeChoice := activeResult.Choices[0]
	if activeChoice.Source.CandidateID != "specimen-id" || activeChoice.Source.NodeID != "specimen" ||
		activeChoice.Source.ResourceType != "Specimen" || activeChoice.Source.LogicalType != "string" ||
		len(activeChoice.Operators) != 2 || activeChoice.Operators[0] != "EXISTS" || activeChoice.Operators[1] != "EQUALS" {
		t.Fatalf("active-route contributor = %#v", activeChoice)
	}
	activeIdentity, err := capability.DecodeConstructionChoiceID(activeChoice.ChoiceID)
	if err != nil || !reflect.DeepEqual(activeIdentity.Route, activeRoute.Route) {
		t.Fatalf("active contributor choice did not retain the selected route: identity=%#v err=%v", activeIdentity, err)
	}
	if _, err := service.SearchRelatedExpandContributorChoices(context.Background(), construction.RelatedExpandContributorChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "expand_observations", RouteChoiceID: rootRoute.ChoiceID,
	}); err == nil || lifecycleErrorCode(err) != "INVALID_CONSTRUCTION_CHOICE" {
		t.Fatalf("route token retargeted to another stage: %v", err)
	}
	if _, err := service.SearchRelatedExpandContributorChoices(context.Background(), construction.RelatedExpandContributorChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "expand_observations", RouteChoiceID: activeRoute.ChoiceID, Limit: 1, Cursor: firstPage.NextCursor,
	}); err == nil || lifecycleErrorCode(err) != "STALE_OR_INVALID_CHOICE_CURSOR" {
		t.Fatalf("contributor cursor retargeted to another route: %v", err)
	}
}

func TestRelatedExpandChoiceSearchRequiresCompilerSupportedSelectedStage(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
			if prepareErr != nil {
				return nil, prepareErr
			}
			columns := []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}
			anchors := []receiptmodel.ReceiptConstructionRelatedExpandAnchor{{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}, RelatedExpandAnchors: anchors},
				{ID: "after_group", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP", Columns: columns,
					Capabilities:         []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: false, ReasonCode: "ROOT_KEY_NOT_RETAINED", Reason: "selected stage does not retain a root resource key"}},
					RelatedExpandAnchors: anchors},
			}}
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
	})
	request := construction.RelatedExpandChoiceSearchRequest{
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
	setup, _ := inboundPatientObservationRouteSetup(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
			if prepareErr != nil {
				return nil, prepareErr
			}
			rootAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient",
			}
			activeAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: "__terminal_observation_id", Kind: "activeRelatedRecord", NodeID: "observation",
				ResourceType: "Observation", Label: "Current related Observation",
			}
			patientObservationRoute := []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}}
			active := &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: activeAnchor.AnchorColumnID,
			}
			relatedExpand := &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: "_key", AnchorColumn: "_key", AnchorKind: "root", AnchorResourceType: "Patient",
				RelatedRecordColumnID: "observation_id", ParentIdentityColumnID: "__parent_identity",
				ParentIdentityColumn: "__parent_identity", TerminalIdentityColumn: activeAnchor.AnchorColumnID,
				TargetNodeID: "observation", TargetResourceType: "Observation", Route: patientObservationRoute,
			}
			capabilities := []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_EXPAND", Supported: true}}
			columns := []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID"}}
			expandedColumns := append(append([]receiptmodel.ReceiptConstructionStageColumn(nil), columns...), receiptmodel.ReceiptConstructionStageColumn{
				ID: "observation_id", Name: "observation_id", Label: "Observation ID",
			})
			fieldColumns := append(append([]receiptmodel.ReceiptConstructionStageColumn(nil), expandedColumns...), receiptmodel.ReceiptConstructionStageColumn{
				ID: "observation_status", Name: "observation_status", Label: "Observation Status",
			})
			fieldCapabilities := []receiptmodel.ReceiptConstructionOperationChoice{
				{Kind: "RELATED_EXPAND", Supported: true}, {Kind: "RELATED_FIELD", Supported: true},
			}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: capabilities, RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor}},
				{ID: "expand_observations", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_EXPAND",
					Columns: expandedColumns, Capabilities: capabilities, RelatedExpand: relatedExpand, ActiveRelatedRecord: active,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
				{ID: "keep_observations", InputStageID: "expand_observations", Operation: "FILTER",
					Columns: expandedColumns, Capabilities: capabilities, ActiveRelatedRecord: active,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
				{ID: "add_observation_status", InputStageID: "keep_observations", Operation: "RELATED_FIELD",
					Columns: fieldColumns, Capabilities: fieldCapabilities, ActiveRelatedRecord: active,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
			}}
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
	})
	store := setup.Store
	snapshot := setup.Snapshot
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "specimen", ResourceType: "Specimen"})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "observation-specimen", FromNodeID: "observation", ToNodeID: "specimen",
		SourceResourceType: "Observation", TargetResourceType: "Specimen", Label: "specimen_Specimen", StorageDirection: "OUTBOUND",
	})
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	search := func(anchorColumnID, targetResourceType string) construction.RelatedExpandChoiceSearchResponse {
		t.Helper()
		result, err := service.SearchRelatedExpandChoices(context.Background(), construction.RelatedExpandChoiceSearchRequest{
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
	setup, candidate := inboundPatientObservationRouteSetup(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
			if prepareErr != nil {
				return nil, prepareErr
			}
			route := []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}}
			active := &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: "__terminal_observation_id",
			}
			sourceColumns := []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
			expandedColumns := append(append([]receiptmodel.ReceiptConstructionStageColumn(nil), sourceColumns...), receiptmodel.ReceiptConstructionStageColumn{
				ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string",
			})
			relatedExpand := &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: "_key", AnchorColumn: "_key", AnchorKind: "root", AnchorNodeID: "patient", AnchorResourceType: "Patient", RelatedRecordColumnID: "observation-id",
				ParentIdentityColumnID: "__parent_identity", ParentIdentityColumn: "__parent_identity",
				TerminalIdentityColumn: active.TerminalIdentityColumn, TargetNodeID: active.TargetNodeID,
				TargetResourceType: active.TargetResourceType, Route: route,
			}
			fieldCapability := []receiptmodel.ReceiptConstructionOperationChoice{{Kind: "RELATED_FIELD", Supported: true}}
			rootAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{AnchorColumnID: "_key", Kind: "root", ResourceType: "Patient", Label: "Original Patient"}
			activeAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{AnchorColumnID: active.TerminalIdentityColumn, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related Observation"}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: sourceColumns, Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{
					{Kind: "RELATED_EXPAND", Supported: true},
					{Kind: "RELATED_FIELD", Supported: false, ReasonCode: "NO_ACTIVE_RELATED_RECORD", Reason: "no active related record"},
				}, RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor}},
				{ID: "expand_observations", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_EXPAND",
					Columns: expandedColumns, Capabilities: fieldCapability, RelatedExpand: relatedExpand, ActiveRelatedRecord: active,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
				{ID: "keep_observations", InputStageID: "expand_observations", Operation: "FILTER",
					Columns: expandedColumns, Capabilities: fieldCapability, ActiveRelatedRecord: active,
					RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor, activeAnchor}},
			}}
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
	})
	store := setup.Store
	snapshot := setup.Snapshot
	unsupported := capability.Candidate{
		ID: "observation-indexed-code", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "component[0].code", Label: "Indexed code", LogicalType: "code", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot.Candidates = append(snapshot.Candidates, unsupported)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	response, err := service.SearchRelatedFieldChoices(context.Background(), construction.RelatedFieldChoiceSearchRequest{
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
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t, func(setup *testServiceSetup) {
		setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, prepareErr := constructionRouteTestReceipt(setup.Config, request)
			if prepareErr != nil {
				return nil, prepareErr
			}
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{"patients": {
				{ID: recipe.ConstructionSourceProjectionID, Columns: []receiptmodel.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}},
			}}
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
	})
	request := construction.RelatedFieldChoiceSearchRequest{
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
	setup, candidate := inboundPatientObservationRouteSetup(t)
	store := setup.Store
	snapshot := setup.Snapshot
	catalog := setup.Catalog
	snapshot, _ = addParallelObservationRoute(setup.Config, snapshot, catalog)
	service := newTestServiceFromSetup(t, setup)
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
	initialWorkspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	initialRouteChildren := len(initialWorkspace.Documents[0].Route.Children)

	var document ws.Document
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
	if _, err := ws.DecodeWorkspace(store.created.DraftConfig); err != nil {
		t.Fatal(err)
	}
	for edgeID, occurrenceID := range occurrences {
		result, searchErr := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: occurrenceID,
			Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
		source, sourceErr := service.ColumnSource(context.Background(), semanticbrowse.ColumnSourceRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", Column: columnID,
		})
		if sourceErr != nil || len(source.Route) != 2 || source.Route[1].CatalogEdgeID != edgeID {
			t.Fatalf("reloaded occurrence %q source route = %#v, %v; want %q", occurrenceID, source.Route, sourceErr, edgeID)
		}
	}
}

func TestConstructionChoiceRejectsAmbiguousLegacyPrefix(t *testing.T) {
	setup, candidate := inboundPatientObservationRouteSetup(t)
	store := setup.Store
	snapshot := setup.Snapshot
	catalog := setup.Catalog
	snapshot, _ = addParallelObservationRoute(setup.Config, snapshot, catalog)
	setSavedConstructionRoute(t, store, ws.RouteNode{
		OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
		Children: []ws.RouteNode{{OccurrenceID: "legacy-observation", ResourceType: "Observation", Relationship: "subject_Patient"}},
	})
	service := newTestServiceFromSetup(t, setup)
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
	setSavedConstructionRoute(t, store, ws.RouteNode{
		OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
		Children: []ws.RouteNode{{OccurrenceID: "saved-observation", ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: ws.RouteMatchOptional}},
	})
	result, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "saved-observation",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
	setSavedConstructionRoute(t, store, ws.RouteNode{
		OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
		Children: []ws.RouteNode{{OccurrenceID: "saved-observation", ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: ws.RouteMatchOptional}},
	})
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "saved-observation",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
				workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
				if err != nil {
					t.Fatal(err)
				}
				workspace.Documents = append(workspace.Documents, ws.Document{
					Kind: ws.Kind, Output: ws.Output{ID: "other", Title: "Other"}, RootResourceType: "Patient",
					Route:   ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "outside-output", ResourceType: "Observation", Relationship: "subject_Patient"}}},
					Columns: []ws.Column{}, Rows: ws.RecordsRowDefinition(),
				})
				workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "other-tab", Title: "Other", OutputID: "other", Order: len(workspace.Tabs), Visible: true})
				persistTestWorkspace(t, store, workspace)
			},
			occurrenceID: "outside-output",
		},
		{
			name: "saved occurrence terminates at a different resource type",
			prepare: func(t *testing.T, store *fakeStore) {
				setSavedConstructionRoute(t, store, ws.RouteNode{
					OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
					Children: []ws.RouteNode{{OccurrenceID: "wrong-terminal", ResourceType: "Encounter", Relationship: "encounters"}},
				})
			},
			occurrenceID: "wrong-terminal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
			test.prepare(t, store)
			_, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
				Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: test.occurrenceID,
				Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
			})
			if err == nil || store.saveDraftCalls != 0 {
				t.Fatalf("invalid occurrence was accepted or mutated state: err=%v saves=%d", err, store.saveDraftCalls)
			}
		})
	}
}

func TestConstructionChoiceSearchRejectsAmbiguousOccurrenceEdgeResolution(t *testing.T) {
	setup, candidate := inboundPatientObservationRouteSetup(t)
	store := setup.Store
	snapshot := setup.Snapshot
	setSavedConstructionRoute(t, store, ws.RouteNode{
		OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient",
		Children: []ws.RouteNode{{OccurrenceID: "ambiguous-observation", ResourceType: "Observation", Relationship: "subject_Patient"}},
	})
	snapshot.Edges = append(snapshot.Edges, capability.Edge{
		ID: "subject-patient-parallel", FromNodeID: "patient", ToNodeID: "observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
	})
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service := newTestServiceFromSetup(t, setup)
	_, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "ambiguous-observation",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err == nil || store.saveDraftCalls != 0 {
		t.Fatalf("ambiguous occurrence route was accepted or mutated state: err=%v saves=%d", err, store.saveDraftCalls)
	}
}

func setSavedConstructionRoute(t *testing.T, store *fakeStore, route ws.RouteNode) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Route = route
	persistTestWorkspace(t, store, workspace)
}

func persistTestWorkspace(t *testing.T, store *fakeStore, workspace ws.Workspace) {
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
	setup, candidate := inboundPatientObservationRouteSetup(t)
	store := setup.Store
	snapshot := setup.Snapshot
	catalog := setup.Catalog
	snapshot.Edges[0].Label = "not_a_fhir_relationship"
	catalog.Edges[0].Label = snapshot.Edges[0].Label
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	setup.Config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }
	service := newTestServiceFromSetup(t, setup)
	result, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(result.Choices) != 0 || store.saveDraftCalls != 0 {
		t.Fatalf("unproven route advertised/mutated: result=%#v err=%v saves=%d", result, err, store.saveDraftCalls)
	}
}

func TestConstructionChoiceRejectsTamperedRouteAtomically(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), construction.ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: construction.ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
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
	routes, err := service.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || !routes.Complete || len(routes.Choices) != 1 || routes.Choices[0].Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("population route choices = %#v, %v", routes, err)
	}
	digest := store.created.DraftDigest
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-observation-population", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID}}},
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
	workspace.Documents[0].Route.Children = append(workspace.Documents[0].Route.Children, ws.RouteNode{OccurrenceID: occurrenceID, ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: ws.RouteMatchOptional})
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, ws.Column{
		Column: "observation_status", Label: "Observation status", LogicalType: "code", OccurrenceID: occurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}},
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
	inspected, err := service.ColumnSource(context.Background(), semanticbrowse.ColumnSourceRequest{
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
	workspaceForRoute := func(t *testing.T, route ws.PopulationRouteStep) ws.Workspace {
		t.Helper()
		workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		workspace.Documents[0].Population = &ws.Population{
			SelectionRevisionID: "selection-1",
			Route:               []ws.PopulationRouteStep{route},
		}
		return workspace
	}
	resolve := func(workspace ws.Workspace, current capability.Snapshot) error {
		_, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, current, snapshot.Identity.AuthorizationScopeDigest)
		return err
	}
	snapshotWithEdges := func(edges []capability.Edge) capability.Snapshot {
		return capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated,
			snapshot.Nodes, edges, snapshot.Candidates, snapshot.Diagnostics)
	}
	step := ws.PopulationRouteStep{
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
		resolved, _, err := authoringcore.ResolveRouteEdge(snapshot, routeEdge.FromNodeID, sourceNode.ResourceType, legacyStep)
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
	setup, _ := inboundPatientObservationRouteSetup(t)
	store := setup.Store
	snapshot := setup.Snapshot
	catalog := setup.Catalog
	snapshot, _ = addParallelObservationRoute(setup.Config, snapshot, catalog)
	store.selection = completedTestSelection(snapshot, "Observation")
	service := newTestServiceFromSetup(t, setup)
	routes, err := service.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
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
		CommandID: "set-parallel-population", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: choiceID}}},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(store.created.DraftConfig)
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

func addParallelObservationRoute(config *Config, snapshot capability.Snapshot, catalog ws.CatalogSnapshot) (capability.Snapshot, ws.CatalogSnapshot) {
	edge := snapshot.Edges[0]
	edge.ID = "subject-patient-parallel"
	snapshot.Edges = append(snapshot.Edges, edge)
	catalogEdge := catalog.Edges[0]
	catalogEdge.ID = edge.ID
	catalog.Edges = append(catalog.Edges, catalogEdge)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot {
		return catalog
	}
	return snapshot, catalog
}

func TestPopulationRouteRejectionIsAtomicWhenChoiceIsTampered(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
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
		CommandID: "tampered-population-route", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: strings.Join(parts, ".")}}},
	}, "alice")
	if err == nil || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("tampered population route err=%v saves=%d", err, store.saveDraftCalls)
	}
}

func TestProposeConstructionChoiceCompilesExactBatchWithoutSavingDraft(t *testing.T) {
	setup, candidates := constructionChoiceSetup(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := setup.Store
	snapshot := setup.Snapshot
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	choiceBatch, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	choiceID := "command-choice-proposal"
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
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
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	beforeWorkspace, err := ws.DecodeWorkspace(beforeConfig)
	if err != nil {
		t.Fatal(err)
	}

	request := construction.ConstructionChoiceProposalRequest{
		CommandID: choiceID, Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest, OutputID: "patients",
		ConstructionChoices: []construction.ConstructionChoiceProposalSelection{
			{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue},
			{ChoiceID: choiceBatch.ChoiceID, Form: capability.ConstructionChoiceFirst},
		},
	}
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstructionChoice(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CommandID != choiceID || proposal.SnapshotToken != snapshot.Token || proposal.DraftVersion != beforeVersion || proposal.DraftDigest != beforeDigest || proposal.OutputID != request.OutputID {
		t.Fatalf("proposal identity echo = %#v", proposal)
	}
	if !reflect.DeepEqual(proposal.ConstructionChoices, request.ConstructionChoices) || len(proposal.CandidateColumnIDs) != 2 || proposal.CandidateWorkspaceDigest == "" || proposal.PreviewReceiptID == "" {
		t.Fatalf("proposal candidate identity = %#v", proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, beforeConfig) {
		t.Fatalf("proposal mutated saved draft: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
	if store.receipt == nil || store.receipt.IntentDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("compiled receipt does not match candidate digest: receipt=%#v proposal=%#v", store.receipt, proposal)
	}

	apply, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: choiceID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest,
		Commands: []authoringv2.Command{
			{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}}},
			{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choiceBatch.ChoiceID, Form: capability.ConstructionChoiceFirst}}},
		},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	applyWorkspaceDigest, err := apply.Workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if apply.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("ApplyCommands response digest %q differs from preview candidate %q", apply.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	if applyWorkspaceDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("ApplyCommands workspace digest %q differs from preview candidate %q", applyWorkspaceDigest, proposal.CandidateWorkspaceDigest)
	}
	columns := apply.Workspace.Documents[0].Columns
	if len(columns) != len(beforeWorkspace.Documents[0].Columns)+len(proposal.CandidateColumnIDs) {
		t.Fatalf("ApplyCommands columns=%d base columns=%d candidate columns=%d", len(columns), len(beforeWorkspace.Documents[0].Columns), len(proposal.CandidateColumnIDs))
	}
	for index, candidateColumnID := range proposal.CandidateColumnIDs {
		column := columns[len(beforeWorkspace.Documents[0].Columns)+index]
		if column.Column != candidateColumnID {
			t.Fatalf("candidate column %d = %q, ApplyCommands column = %q", index, candidateColumnID, column.Column)
		}
	}
}

func TestProposeConstructionChoiceRejectsStaleDraftAndSnapshotChoice(t *testing.T) {
	setup, candidates := constructionChoiceSetup(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := setup.Store
	snapshot := setup.Snapshot
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		store.receipt = receipt
		return receipt, nil
	}
	request := construction.ConstructionChoiceProposalRequest{
		CommandID: "command-choice-stale", Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest, OutputID: "patients",
		ConstructionChoices: []construction.ConstructionChoiceProposalSelection{{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
	}
	request.ExpectedDraftVersion++
	service := newTestServiceFromSetup(t, setup)
	if _, err := service.ProposeConstructionChoice(context.Background(), request); !proposalErrorIs(err, ports.ClassConflict, "DRAFT_CONFLICT") {
		t.Fatalf("stale draft error = %v", err)
	}
	request.ExpectedDraftVersion = store.created.DraftVersion
	staleChoice, err := capability.NewFieldConstructionChoice("different-snapshot", candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	request.ConstructionChoices[0].ChoiceID = staleChoice.ChoiceID
	if _, err := service.ProposeConstructionChoice(context.Background(), request); !proposalErrorIs(err, ports.ClassConflict, "STALE_CONSTRUCTION_CHOICE") {
		t.Fatalf("choice from different snapshot error = %v", err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("invalid proposal saved the draft %d times", store.saveDraftCalls)
	}
}

func TestProposeSemanticConstructionChoiceUsesAuthorizedInventoryWithoutSavingDraft(t *testing.T) {
	setup, _, choice := constructionSemanticChoiceSetup(t, nil, nil, "Observation")
	store := setup.Store
	snapshot := setup.Snapshot
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		var compileErr error
		receipt.CompilationKey, compileErr = receiptmodel.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = receiptmodel.ReceiptID(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		store.receipt = receipt
		return receipt, nil
	}
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
	service := newTestServiceFromSetup(t, setup)
	proposal, err := service.ProposeConstructionChoice(context.Background(), construction.ConstructionChoiceProposalRequest{
		CommandID: "semantic-choice-preview", Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: beforeVersion, ExpectedDraftDigest: beforeDigest, OutputID: "patients",
		ConstructionChoices: []construction.ConstructionChoiceProposalSelection{{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.CandidateWorkspaceDigest == "" || len(proposal.CandidateColumnIDs) != 1 || proposal.PreviewReceiptID == "" {
		t.Fatalf("semantic choice proposal = %#v", proposal)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, beforeConfig) {
		t.Fatalf("semantic proposal mutated saved draft: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
}
