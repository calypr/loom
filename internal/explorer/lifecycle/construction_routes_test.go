package lifecycle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	catalogdata "github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/spec"
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
		SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND", ObservedEdgeCount: 1,
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
		Edges: []authoringv2.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient", Populated: true}},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"},
			DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
		}},
	}
	store := semanticAuthoringStore(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	workspace.Documents[0].FixedFilters = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Capability: CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog },
	}, HasRouteValue: func(context.Context, catalogdata.RouteCoverageOptions) (bool, error) { return true, nil }}
	return store, newTestService(t, store, config), snapshot, catalog, candidate
}

func TestConstructionChoiceSearchAndApplyUseCompilerProvedInboundRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !search.Complete || search.Truncated || search.NextCursor != "" || len(search.Choices) != 1 {
		t.Fatalf("route-bound search = %#v, %v", search, err)
	}
	choice := search.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].Relationship != "subject_Patient" || choice.Route[0].StorageDirection != "INBOUND" || choice.Route[0].ToResourceType != "Observation" {
		t.Fatalf("compiler-proved inbound route = %#v", choice.Route)
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

func TestParallelConstructionChoicesApplyToDistinctPinnedOccurrences(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{
			{OccurrenceID: "saved-observation-primary", ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: "subject-patient", MatchMode: authoringv2.RouteMatchOptional},
			{OccurrenceID: "saved-observation-parallel", ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: "subject-patient-parallel", MatchMode: authoringv2.RouteMatchOptional},
		},
	})
	initialWorkspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	initialRouteChildren := len(initialWorkspace.Documents[0].Route.Children)

	var document authoringv2.Document
	for index, route := range []struct{ edgeID, occurrenceID string }{
		{edgeID: "subject-patient", occurrenceID: "saved-observation-primary"},
		{edgeID: "subject-patient-parallel", occurrenceID: "saved-observation-parallel"},
	} {
		search, searchErr := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: route.occurrenceID,
			Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
		})
		if searchErr != nil || len(search.Choices) != 1 {
			t.Fatalf("advanced route search for %q = %#v, %v", route.edgeID, search, searchErr)
		}
		identity, decodeErr := capability.DecodeConstructionChoiceID(search.Choices[0].ChoiceID)
		if decodeErr != nil || len(identity.Route) != 1 || identity.Route[0].EdgeID != route.edgeID {
			t.Fatalf("advanced route identity = %#v, %v; want edge %q", identity.Route, decodeErr, route.edgeID)
		}
		request := constructionChoiceRequest(snapshot, "parallel-choice-"+route.edgeID, search.Choices[0].ChoiceID, capability.ConstructionChoiceValue)
		request.ExpectedDraftVersion = store.created.DraftVersion
		response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
		if applyErr != nil {
			t.Fatalf("apply exact route %q: %v", route.edgeID, applyErr)
		}
		document = response.Workspace.Documents[0]
		if index == 1 && len(document.Route.Children) != initialRouteChildren {
			t.Fatalf("advanced route application changed the saved graph: got %d children, want %d", len(document.Route.Children), initialRouteChildren)
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
	if len(persisted.Documents) != 1 || len(persisted.Documents[0].Route.Children) != initialRouteChildren {
		t.Fatalf("reloaded workspace changed the explicitly saved route occurrences: %#v", persisted)
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

func TestConstructionChoiceSearchOffersEqualShortestRoutes(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !search.Complete || search.Truncated || len(search.Choices) != 2 {
		t.Fatalf("equal shortest route choices = %#v, %v; want both pinned edges", search, err)
	}
	if search.Choices[0].Route[0].EdgeID != "subject-patient" || search.Choices[1].Route[0].EdgeID != "subject-patient-parallel" {
		t.Fatalf("equal shortest route order = %#v; want stable edge ordering", search.Choices)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("normal catalog search mutated workspace: saves=%d", store.saveDraftCalls)
	}
}

func TestConstructionChoiceSearchDoesNotTreatGlobalEdgeCountAsRowCoverage(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	snapshot.Edges[0].ObservedEdgeCount = 1
	snapshot.Edges[1].ObservedEdgeCount = 9
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	service.config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog }
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(result.Choices) != 2 || result.Choices[0].Route[0].EdgeID != "subject-patient" || result.Choices[1].Route[0].EdgeID != "subject-patient-parallel" {
		t.Fatalf("row-backed choices must ignore global edge ranking: %#v, %v", result, err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("construction-choice search mutated workspace: saves=%d", store.saveDraftCalls)
	}
}

func TestConstructionChoiceSearchAndApplyPreferRowsWithValuesOverGlobalEdgeCount(t *testing.T) {
	store, service, snapshot, catalogSnapshot, candidate := inboundPatientObservationRouteFixture(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}}
	workspace.Documents[0].FixedFilters = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Edges[0].ObservedEdgeCount = 100
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "specimen", ResourceType: "Specimen"})
	snapshot.Edges = append(snapshot.Edges,
		capability.Edge{ID: "specimen-subject", FromNodeID: "patient", ToNodeID: "specimen", SourceResourceType: "Patient", TargetResourceType: "Specimen", Label: "subject_Patient", StorageDirection: "INBOUND", ObservedEdgeCount: 2},
		capability.Edge{ID: "observation-specimen", FromNodeID: "specimen", ToNodeID: "observation", SourceResourceType: "Specimen", TargetResourceType: "Observation", Label: "specimen_Specimen", StorageDirection: "INBOUND", ObservedEdgeCount: 2},
	)
	catalogSnapshot.Nodes = append(catalogSnapshot.Nodes, authoringv2.CatalogNode{ID: "specimen", ResourceType: "Specimen"})
	catalogSnapshot.Edges = append(catalogSnapshot.Edges,
		authoringv2.CatalogEdge{ID: "specimen-subject", FromNodeID: "patient", ToNodeID: "specimen", Label: "subject_Patient", Populated: true},
		authoringv2.CatalogEdge{ID: "observation-specimen", FromNodeID: "specimen", ToNodeID: "observation", Label: "specimen_Specimen", Populated: true},
	)
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot }
	service.config.HasRouteValue = func(_ context.Context, options catalogdata.RouteCoverageOptions) (bool, error) {
		if options.RootResourceType != "Patient" || options.SourceResourceType != "Observation" || options.Source.Kind != catalogdata.RouteCoverageField || options.Source.FieldPath != "status" {
			t.Fatalf("coverage measured unrelated source: %#v", options)
		}
		if len(options.Route) == 2 && options.Route[0].Relationship == "subject_Patient" && options.Route[1].Relationship == "specimen_Specimen" {
			return true, nil
		}
		return false, nil
	}
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(search.Choices) != 1 {
		t.Fatalf("measured search = %#v, %v", search, err)
	}
	choice := search.Choices[0]
	if len(choice.Route) != 2 || choice.Route[0].EdgeID != "specimen-subject" || choice.Route[1].EdgeID != "observation-specimen" {
		t.Fatalf("selected route = %#v, want the only route with current values", choice.Route)
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "value-backed-route", choice.ChoiceID, capability.ConstructionChoiceValue), "alice")
	if err != nil || store.saveDraftCalls != 1 || len(response.Workspace.Documents[0].Columns) == 0 {
		t.Fatalf("apply measured route: response=%#v saves=%d err=%v", response, store.saveDraftCalls, err)
	}
}

func TestConstructionChoiceSearchRejectsObservedEdgeWithoutRowValues(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}}
	workspace.Documents[0].FixedFilters = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	service.config.HasRouteValue = func(context.Context, catalogdata.RouteCoverageOptions) (bool, error) {
		return false, nil
	}
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || len(result.Choices) != 0 {
		t.Fatalf("globally observed but empty route = %#v, %v", result, err)
	}
}

func TestConstructionChoiceSearchUsesIndexedDirectRootCoverage(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	workspace.Documents[0].FixedFilters = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	rootCandidate := capability.Candidate{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, ObservedDocumentCount: 3}
	snapshot.Candidates = append(snapshot.Candidates, rootCandidate)
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.HasRouteValue = func(_ context.Context, options catalogdata.RouteCoverageOptions) (bool, error) {
		if options.RootResourceType != "Patient" || options.SourceResourceType != "Patient" || len(options.Route) != 0 {
			t.Fatalf("direct root existence options = %#v", options)
		}
		return true, nil
	}
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: rootCandidate.ID},
	})
	if err != nil || len(result.Choices) != 1 || len(result.Choices[0].Route) != 0 {
		t.Fatalf("indexed direct coverage = %#v, %v", result, err)
	}
}

func TestConstructionChoiceSearchPinsDirectAndSharedStudyRoutesAndForms(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "shared-study-route-snapshot", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true},
		{ID: "research-study", ResourceType: "ResearchStudy"},
		{ID: "observation", ResourceType: "Observation"},
	}
	rootToObservation := compilerProvedRouteDirection(t, snapshot, scope, "Specimen", "specimen_Specimen", "Observation")
	rootToStudy := compilerProvedRouteDirection(t, snapshot, scope, "Specimen", "focus_reference_Specimen", "ResearchStudy")
	studyToObservation := compilerProvedRouteDirection(t, snapshot, scope, "ResearchStudy", "focus_ResearchStudy", "Observation")
	snapshot.Edges = []capability.Edge{
		{ID: "specimen-observation", FromNodeID: "specimen", ToNodeID: "observation", SourceResourceType: "Specimen", TargetResourceType: "Observation", Label: "specimen_Specimen", StorageDirection: rootToObservation, ObservedEdgeCount: 1},
		{ID: "specimen-study", FromNodeID: "specimen", ToNodeID: "research-study", SourceResourceType: "Specimen", TargetResourceType: "ResearchStudy", Label: "focus_reference_Specimen", StorageDirection: rootToStudy, ObservedEdgeCount: 2},
		{ID: "study-observation", FromNodeID: "research-study", ToNodeID: "observation", SourceResourceType: "ResearchStudy", TargetResourceType: "Observation", Label: "focus_ResearchStudy", StorageDirection: studyToObservation, ObservedEdgeCount: 2},
	}
	candidate := capability.Candidate{
		ID: "observation-notes", NodeID: "observation", ResourceType: "Observation", FieldPath: "note[].text",
		Label: "Observation note", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "note[]"}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	baseChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		Nodes: []authoringv2.CatalogNode{
			{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true},
			{ID: "research-study", ResourceType: "ResearchStudy"},
			{ID: "observation", ResourceType: "Observation"},
		},
		Edges: []authoringv2.CatalogEdge{
			{ID: "specimen-observation", FromNodeID: "specimen", ToNodeID: "observation", Label: "specimen_Specimen", Populated: true},
			{ID: "specimen-study", FromNodeID: "specimen", ToNodeID: "research-study", Label: "focus_reference_Specimen", Populated: true},
			{ID: "study-observation", FromNodeID: "research-study", ToNodeID: "observation", Label: "focus_ResearchStudy", Populated: true},
		},
		Candidates: []authoringv2.CatalogCandidate{{
			ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
			LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"FIRST", "ALL"},
			DefaultProjectionMode: "ALL", ConstructionChoice: &baseChoice,
		}},
	}
	store := semanticAuthoringStore(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Specimen"
	workspace.Documents[0].Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Specimen"}
	workspace.Documents[0].Rows = authoringv2.RecordsRowDefinition()
	workspace.Documents[0].FixedFilters = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	config := Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		HasRouteValue: func(_ context.Context, options catalogdata.RouteCoverageOptions) (bool, error) {
			checks++
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || !options.AuthResourcePathsUnrestricted {
				t.Fatalf("route value check lost current generation or authorization: %#v", options)
			}
			if options.RootResourceType != "Specimen" || options.SourceResourceType != "Observation" || options.Source.FieldPath != candidate.FieldPath {
				t.Fatalf("route value check used another source: %#v", options)
			}
			if len(options.Route) == 1 && options.Route[0].Relationship == "specimen_Specimen" {
				return true, nil // Specimen A reaches its direct Observation A.
			}
			if len(options.Route) == 2 && options.Route[0].Relationship == "focus_reference_Specimen" && options.Route[1].Relationship == "focus_ResearchStudy" {
				return true, nil // The shared Study also reaches Observation B from another specimen.
			}
			return false, nil
		},
	}
	service := newTestService(t, store, config)
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !search.Complete || search.Truncated || len(search.Choices) != 2 || checks != 2 {
		t.Fatalf("direct and shared-Study route choices = %#v checks=%d err=%v", search, checks, err)
	}
	choicesByFirstEdge := map[string]capability.ConstructionChoice{}
	for _, choice := range search.Choices {
		if len(choice.Route) > 0 {
			choicesByFirstEdge[choice.Route[0].EdgeID] = choice
		}
	}
	direct := choicesByFirstEdge["specimen-observation"]
	shared := choicesByFirstEdge["specimen-study"]
	if len(direct.Route) != 1 || len(shared.Route) != 2 || shared.Route[1].EdgeID != "study-observation" {
		t.Fatalf("routes were not separately pinned: direct=%#v shared=%#v", direct.Route, shared.Route)
	}
	if !hasConstructionForm(direct, capability.ConstructionChoiceFirst) || !hasConstructionForm(shared, capability.ConstructionChoiceAll) {
		t.Fatalf("compiler-proved FIRST/ALL forms missing: direct=%#v shared=%#v", direct.Options, shared.Options)
	}
	beforeFailure := append([]byte(nil), store.created.DraftConfig...)
	beforeFailureVersion, beforeFailureDigest := store.created.DraftVersion, store.created.DraftDigest
	service.config.HasRouteValue = func(context.Context, catalogdata.RouteCoverageOptions) (bool, error) { return false, nil }
	failedRequest := constructionChoiceRequest(snapshot, "reject-empty-route", direct.ChoiceID, capability.ConstructionChoiceFirst)
	failedRequest.ExpectedDraftVersion = beforeFailureVersion
	failedRequest.ExpectedDraftDigest = beforeFailureDigest
	if _, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", failedRequest, "alice"); applyErr == nil || !strings.Contains(applyErr.Error(), "NO_VALUES_ON_TABLE_ROWS") {
		t.Fatalf("route with no current value error = %v", applyErr)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeFailureVersion || store.created.DraftDigest != beforeFailureDigest || string(store.created.DraftConfig) != string(beforeFailure) {
		t.Fatalf("failed value check mutated draft: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
	service.config.HasRouteValue = config.HasRouteValue

	apply := func(commandID string, choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) authoringv2.Document {
		request := constructionChoiceRequest(snapshot, commandID, choice.ChoiceID, form)
		request.ExpectedDraftVersion = store.created.DraftVersion
		request.ExpectedDraftDigest = store.created.DraftDigest
		response, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
		if applyErr != nil {
			t.Fatalf("apply %s route with %s: %v", commandID, form, applyErr)
		}
		return response.Workspace.Documents[0]
	}
	apply("add-direct-source", direct, capability.ConstructionChoiceFirst)
	document := apply("add-shared-study-source", shared, capability.ConstructionChoiceAll)
	if len(document.Columns) != 3 {
		t.Fatalf("two route choices did not add two columns: %#v", document.Columns)
	}
	gotForms := map[string]string{}
	for _, column := range document.Columns[1:] {
		if column.Source.Field == nil {
			t.Fatalf("route choice output is not a field: %#v", column)
		}
		gotForms[column.Source.Field.ProjectionMode] = column.OccurrenceID
	}
	if gotForms["FIRST"] == "" || gotForms["ALL"] == "" || gotForms["FIRST"] == gotForms["ALL"] {
		t.Fatalf("source and output-form choices were not persisted independently: %#v", gotForms)
	}
	if _, err := authoringv2.DecodeWorkspace(store.created.DraftConfig); err != nil {
		t.Fatalf("reload persisted route choices: %v", err)
	}
	for columnID, want := range map[string][]string{
		document.Columns[1].Column: {"specimen-observation"},
		document.Columns[2].Column: {"specimen-study", "study-observation"},
	} {
		resolved, sourceErr := service.ColumnSource(context.Background(), ColumnSourceRequest{
			Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", Column: columnID,
		})
		if sourceErr != nil {
			t.Fatalf("reload pinned column %q: %v", columnID, sourceErr)
		}
		if len(resolved.Route) != len(want)+1 {
			t.Fatalf("reloaded column %q route = %#v; want %v", columnID, resolved.Route, want)
		}
		for index, edgeID := range want {
			if resolved.Route[index+1].CatalogEdgeID != edgeID {
				t.Fatalf("reloaded column %q route = %#v; want edge %q", columnID, resolved.Route, edgeID)
			}
		}
	}
}

func compilerProvedRouteDirection(t *testing.T, snapshot capability.Snapshot, scope authscope.ReadScope, from, label, to string) string {
	t.Helper()
	proof, err := compilerprobe.ProbeTraversal(context.Background(), compilerprobe.TraversalRequest{
		Scope: constructionCompilerScope(AuthorizedCapability{Snapshot: snapshot, Scope: scope}), RootResourceType: from,
		Traversal: compilerprobe.Traversal{FromResourceType: from, EdgeLabel: label, ToResourceType: to, MatchMode: spec.TraversalMatchOptional},
	})
	if err != nil || proof.Traversal == nil {
		t.Fatalf("compiler route proof %s -[%s]-> %s = %#v, %v", from, label, to, proof, err)
	}
	return string(proof.Traversal.StorageDirection)
}

func hasConstructionForm(choice capability.ConstructionChoice, form capability.ConstructionChoiceForm) bool {
	for _, option := range choice.Options {
		if option.Form == form {
			return true
		}
	}
	return false
}

func TestConstructionChoiceRejectsAmbiguousLegacyPrefix(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, _ = addParallelObservationRoute(service, snapshot, catalog)
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "legacy-observation", ResourceType: "Observation", Relationship: "subject_Patient", MatchMode: authoringv2.RouteMatchOptional}},
	})
	route := []capability.ConstructionRouteStep{{
		EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, candidate)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionChoiceRequest(snapshot, "ambiguous-legacy-prefix", choice.ChoiceID, capability.ConstructionChoiceValue)
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil || !strings.Contains(err.Error(), "ambiguous legacy occurrence") {
		t.Fatalf("ambiguous legacy route error = %v, want explicit legacy-route rejection", err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("ambiguous legacy route mutated workspace: saves=%d", store.saveDraftCalls)
	}
}

func TestConstructionChoiceSearchRejectsUnobservedRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	snapshot.Edges[0].ObservedEdgeCount = 0
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	search, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !search.Complete || search.Truncated {
		t.Fatalf("unobserved route search = %#v, %v; want an empty, complete result", search, err)
	}
	if len(search.Choices) != 0 || store.saveDraftCalls != 0 {
		t.Fatalf("unobserved route returned choices or mutated workspace: choices=%d saves=%d", len(search.Choices), store.saveDraftCalls)
	}
}

func TestConstructionChoiceSearchScopesToSavedOccurrenceRoute(t *testing.T) {
	store, service, snapshot, _, candidate := inboundPatientObservationRouteFixture(t)
	snapshot, _ = addParallelObservationRoute(service, snapshot, service.config.Capability.Catalog(snapshot, "patients"))
	setSavedConstructionRoute(t, store, authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient",
		Children: []authoringv2.RouteNode{{OccurrenceID: "saved-observation", ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: "subject-patient-parallel", MatchMode: authoringv2.RouteMatchOptional}},
	})
	result, err := service.SearchConstructionChoices(context.Background(), ConstructionChoiceSearchRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", OccurrenceID: "saved-observation",
		Source: ConstructionChoiceSearchSource{Kind: capability.ConstructionChoiceSourceField, CandidateID: candidate.ID},
	})
	if err != nil || !result.Complete || result.Truncated || result.NextCursor != "" || len(result.Choices) != 1 {
		t.Fatalf("occurrence-scoped search = %#v, %v", result, err)
	}
	choice := result.Choices[0]
	if len(choice.Route) != 1 || choice.Route[0].EdgeID != "subject-patient-parallel" || choice.Route[0].StorageDirection != "INBOUND" {
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

func TestApplyConstructionChoiceRejectsNoncanonicalUnsavedRouteWithoutValueProof(t *testing.T) {
	store, service, snapshot, catalog, candidate := inboundPatientObservationRouteFixture(t)
	service.config.HasRouteValue = nil
	snapshot, catalog = addParallelObservationRoute(service, snapshot, catalog)
	route := []capability.ConstructionRouteStep{{
		EdgeID: "subject-patient-parallel", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, candidate)
	if err != nil {
		t.Fatal(err)
	}
	request := constructionChoiceRequest(snapshot, "noncanonical-unsaved-route", choice.ChoiceID, capability.ConstructionChoiceValue)
	before := append([]byte(nil), store.created.DraftConfig...)
	version, digest := store.created.DraftVersion, store.created.DraftDigest

	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if got := lifecycleErrorCode(err); got != "INVALID_CONSTRUCTION_CHOICE" {
		t.Fatalf("noncanonical unsaved route error = %s (%v), want invalid construction choice", got, err)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(before) != string(store.created.DraftConfig) {
		t.Fatalf("rejected noncanonical route mutated workspace: saves=%d", store.saveDraftCalls)
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
