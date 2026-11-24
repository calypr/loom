package lifecycle

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

type constructionChoiceNegativeCase struct {
	name          string
	root          string
	context       string
	build         string
	candidateID   string
	duplicatePath bool
	form          capability.ConstructionChoiceForm
	mutateEntry   func(*semantic.Entry)
}

func TestApplyConstructionChoiceMapsRepeatedFieldForms(t *testing.T) {
	for _, test := range []struct {
		form capability.ConstructionChoiceForm
	}{
		{form: capability.ConstructionChoiceAll},
		{form: capability.ConstructionChoiceDistinct},
		{form: capability.ConstructionChoiceFirst},
	} {
		t.Run(string(test.form), func(t *testing.T) {
			store, service, snapshot, candidates := constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
			choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[1])
			if err != nil {
				t.Fatal(err)
			}
			before, err := ws.DecodeWorkspace(store.created.DraftConfig)
			if err != nil {
				t.Fatal(err)
			}
			response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "field-"+strings.ToLower(string(test.form)), choice.ChoiceID, test.form), "alice")
			if err != nil {
				t.Fatal(err)
			}
			document := response.Workspace.Documents[0]
			column := document.Columns[len(document.Columns)-1]
			if column.Source.Kind != ws.SourceField || column.Source.Field == nil || column.Source.Field.Path != "name[].family" || column.Source.Field.ProjectionMode != string(test.form) || column.LogicalType != "string" || column.Label != "Patient family" {
				t.Fatalf("applied column = %#v", column)
			}
			if response.Results[0].Type != authoringv2.CommandResultColumnAdded || response.Results[0].Column != column.Column || column.OccurrenceID != ws.RootOccurrenceID {
				t.Fatalf("apply result or occurrence = %#v, column=%#v", response.Results[0], column)
			}
			if !reflect.DeepEqual(document.Route, before.Documents[0].Route) || len(document.Columns) != len(before.Documents[0].Columns)+1 {
				t.Fatalf("construction choice edited a route or changed unrelated columns: before=%#v after=%#v", before.Documents[0], document)
			}
		})
	}
}

func TestApplyConstructionChoiceMapsScalarValueField(t *testing.T) {
	store, service, snapshot, candidates := constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "field-scalar", choice.ChoiceID, capability.ConstructionChoiceValue), "alice")
	if err != nil {
		t.Fatal(err)
	}
	column := response.Workspace.Documents[0].Columns[len(response.Workspace.Documents[0].Columns)-1]
	if column.Source.Kind != ws.SourceField || column.Source.Field == nil || column.Source.Field.Path != "id" || column.Source.Field.ProjectionMode != "VALUE" || column.LogicalType != "string" {
		t.Fatalf("applied scalar column = %#v", column)
	}
	if store.saveDraftCalls != 1 {
		t.Fatalf("draft saves = %d, want 1", store.saveDraftCalls)
	}
}

func TestApplyConstructionChoiceMapsSemanticObservationAtTheDocumentRoot(t *testing.T) {
	store, service, snapshot, entry, choice := constructionSemanticChoiceFixture(t, nil, nil, "Observation")
	request := constructionChoiceRequest(snapshot, "semantic-choice", choice.ChoiceID, capability.ConstructionChoiceValue)
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	document := response.Workspace.Documents[0]
	if len(response.Results) != 1 || response.Results[0].Type != authoringv2.CommandResultColumnAdded || store.saveDraftCalls != 1 || len(document.Columns) != 1 {
		t.Fatalf("semantic choice result=%#v saves=%d document=%#v", response.Results, store.saveDraftCalls, document)
	}
	column := document.Columns[0]
	if column.Source.Kind != ws.SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil || column.Source.Lookup.Key == nil ||
		column.Source.Lookup.Key.System != entry.Observation.Key.System || column.Source.Lookup.Key.Code != entry.Observation.Key.Code ||
		column.Source.Lookup.Binding.ValuePath != entry.Observation.Value.Selector || column.Source.Lookup.ProjectionMode != "VALUE" ||
		column.LogicalType != "decimal" || column.OccurrenceID != ws.RootOccurrenceID || document.RootResourceType != "Observation" ||
		document.Route.ResourceType != "Observation" || len(document.Route.Children) != 0 {
		t.Fatalf("semantic source did not map to the compiler's root binding: %#v document=%#v", column, document)
	}
}

func TestApplyConstructionChoiceRejectsInvalidSemanticChoicesAtomically(t *testing.T) {
	for _, test := range []constructionChoiceNegativeCase{
		{name: "stale context", root: "Observation", context: "old-context", form: capability.ConstructionChoiceValue},
		{name: "stale build", root: "Observation", build: "old-build", form: capability.ConstructionChoiceValue},
		{name: "unsupported form", root: "Observation", form: capability.ConstructionChoiceFirst},
		{name: "wrong root", root: "Patient", form: capability.ConstructionChoiceValue},
		{name: "unrelated candidate", root: "Observation", candidateID: "other-candidate", form: capability.ConstructionChoiceValue},
		{name: "ambiguous compiler match", root: "Observation", duplicatePath: true, form: capability.ConstructionChoiceValue},
		{name: "changed observation source", root: "Observation", form: capability.ConstructionChoiceValue, mutateEntry: func(entry *semantic.Entry) { entry.Observation.Key.Code = "changed-code" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, service, snapshot, _, choice := constructionSemanticChoiceFixture(t, test.mutateEntry, &test, test.root)
			request := constructionChoiceRequest(snapshot, "reject-semantic-"+strings.ReplaceAll(test.name, " ", "-"), choice.ChoiceID, test.form)
			before := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
				t.Fatal("invalid semantic construction choice was accepted")
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, before) {
				t.Fatalf("rejected semantic choice mutated workspace: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
			}
		})
	}
}

func TestApplyConstructionChoiceBatchUsesOneAtomicColumnFlow(t *testing.T) {
	store, service, snapshot, candidates := constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	repeatedChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	request := constructionChoiceRequest(snapshot, "field-batch", fieldChoice.ChoiceID, capability.ConstructionChoiceValue)
	request.Commands = append(request.Commands, authoringv2.Command{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", InitialPresentation: authoringv2.InitialPresentationFilter, Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: repeatedChoice.ChoiceID, Form: capability.ConstructionChoiceAll}}})
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if store.saveDraftCalls != 1 || len(response.Results) != 2 || len(response.Workspace.Documents[0].Columns) != 3 {
		t.Fatalf("batch did not commit once with two columns: saves=%d results=%#v workspace=%#v", store.saveDraftCalls, response.Results, response.Workspace)
	}
	added := response.Workspace.Documents[0].Columns[1:]
	if added[0].Source.Field == nil || added[0].Source.Field.Path != "id" || added[0].Source.Field.ProjectionMode != "VALUE" ||
		added[1].Source.Field == nil || added[1].Source.Field.Path != "name[].family" || added[1].Source.Field.ProjectionMode != "ALL" || added[1].Filter == nil {
		t.Fatalf("batch column mappings = %#v", added)
	}
	for _, result := range response.Results {
		if result.Type != authoringv2.CommandResultColumnAdded {
			t.Fatalf("batch result = %#v", result)
		}
	}

	store, service, snapshot, candidates = constructionChoiceFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	fieldChoice, err = capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	repeatedChoice, err = capability.NewFieldConstructionChoice(snapshot.Token, candidates[1])
	if err != nil {
		t.Fatal(err)
	}
	request = constructionChoiceRequest(snapshot, "field-batch-reject", fieldChoice.ChoiceID, capability.ConstructionChoiceValue)
	request.Commands = append(request.Commands, authoringv2.Command{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: repeatedChoice.ChoiceID, Form: capability.ConstructionChoiceValue}}})
	before := append([]byte(nil), store.created.DraftConfig...)
	beforeDigest, beforeVersion := store.created.DraftDigest, store.created.DraftVersion
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err == nil {
		t.Fatal("batch accepted a form not advertised for its second choice")
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(before, store.created.DraftConfig) || store.created.DraftDigest != beforeDigest || store.created.DraftVersion != beforeVersion {
		t.Fatalf("invalid batch mutated workspace: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
	}
}

func TestApplyConstructionChoiceRejectsInvalidFieldChoicesAtomically(t *testing.T) {
	for _, test := range []struct {
		name          string
		form          capability.ConstructionChoiceForm
		choiceID      func(capability.Snapshot, []capability.Candidate) string
		resolverScope authscope.ReadScope
	}{
		{name: "stale snapshot", form: capability.ConstructionChoiceValue, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice("old-snapshot", candidates[0])
			return choice.ChoiceID
		}},
		{name: "tampered token", form: capability.ConstructionChoiceValue, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
			parts := strings.Split(choice.ChoiceID, ".")
			last := parts[2][0]
			if last == '0' {
				parts[2] = "1" + parts[2][1:]
			} else {
				parts[2] = "0" + parts[2][1:]
			}
			return strings.Join(parts, ".")
		}},
		{name: "unsupported form", form: capability.ConstructionChoiceAll, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
			return choice.ChoiceID
		}},
		{name: "wrong root", form: capability.ConstructionChoiceValue, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice(snapshot.Token, candidates[2])
			return choice.ChoiceID
		}},
		{name: "unrelated candidate", form: capability.ConstructionChoiceValue, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice(snapshot.Token, capability.Candidate{ID: "outside", NodeID: "patient", ResourceType: "Patient", FieldPath: "active", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}})
			return choice.ChoiceID
		}},
		{name: "authorization scope changed", form: capability.ConstructionChoiceValue, resolverScope: authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/patients"}}, choiceID: func(snapshot capability.Snapshot, candidates []capability.Candidate) string {
			choice, _ := capability.NewFieldConstructionChoice(snapshot.Token, candidates[0])
			return choice.ChoiceID
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			initialScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
			resolverScope := test.resolverScope
			if resolverScope.Mode == "" {
				resolverScope = initialScope
			}
			store, service, snapshot, candidates := constructionChoiceFixtureWithScopes(t, initialScope, resolverScope)
			choiceID := test.choiceID(snapshot, candidates)
			form := test.form
			before := append([]byte(nil), store.created.DraftConfig...)
			beforeVersion, beforeDigest := store.created.DraftVersion, store.created.DraftDigest
			_, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "reject-"+strings.ReplaceAll(test.name, " ", "-"), choiceID, form), "alice")
			if err == nil {
				t.Fatal("invalid construction choice was accepted")
			}
			if store.saveDraftCalls != 0 || store.created.DraftVersion != beforeVersion || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, before) {
				t.Fatalf("rejected choice mutated workspace: saves=%d version=%d digest=%q", store.saveDraftCalls, store.created.DraftVersion, store.created.DraftDigest)
			}
		})
	}
}

func constructionChoiceFixture(t *testing.T, resolverScope authscope.ReadScope) (*fakeStore, *Service, capability.Snapshot, []capability.Candidate) {
	t.Helper()
	setup, candidates := constructionChoiceSetup(t, resolverScope)
	return setup.Store, newTestServiceFromSetup(t, setup), setup.Snapshot, candidates
}

func constructionChoiceSetup(t *testing.T, resolverScope authscope.ReadScope) (testServiceSetup, []capability.Candidate) {
	return constructionChoiceSetupWithScopes(t, resolverScope, resolverScope)
}

func constructionChoiceFixtureWithScopes(t *testing.T, snapshotScope, resolverScope authscope.ReadScope) (*fakeStore, *Service, capability.Snapshot, []capability.Candidate) {
	t.Helper()
	setup, candidates := constructionChoiceSetupWithScopes(t, snapshotScope, resolverScope)
	return setup.Store, newTestServiceFromSetup(t, setup), setup.Snapshot, candidates
}

func constructionChoiceSetupWithScopes(t *testing.T, snapshotScope, resolverScope authscope.ReadScope) (testServiceSetup, []capability.Candidate) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "choice-snapshot", snapshotScope)
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter"},
		{ID: "observation", ResourceType: "Observation"},
	}
	candidates := []capability.Candidate{
		{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		{ID: "patient-family", NodeID: "patient", ResourceType: "Patient", FieldPath: "name[].family", Label: "Patient family", LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 4}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}, SupportedOperations: []capability.Operation{capability.OperationFilter, capability.OperationChart}},
		{ID: "encounter-id", NodeID: "encounter", ResourceType: "Encounter", FieldPath: "id", Label: "Encounter ID", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
	}
	snapshot.Candidates = candidates
	catalogCandidates := []ws.CatalogCandidate{
		{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
		{ID: "patient-family", NodeID: "patient", FieldPath: "name[].family", Cardinality: "many", Repeated: true, RepeatedBoundaries: []ws.RepeatedBoundary{{Path: "name[]", MaxItems: 4}}, Label: "Patient family", LogicalType: "string", ProjectionModes: []string{"FIRST", "ALL", "DISTINCT"}, DefaultProjectionMode: "ALL", Filterable: true, Chartable: true, ConstructionChoice: fieldChoicePointer(t, snapshot, candidates[1])},
		{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "Encounter ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, candidates[2])},
	}
	catalogSnapshot := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
		Nodes:      []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "encounter", ResourceType: "Encounter"}, {ID: "observation", ResourceType: "Observation"}},
		Edges:      []ws.CatalogEdge{{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"}, {ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"}},
		Candidates: catalogCandidates, RoutePolicy: ws.RoutePolicy{Unbounded: true},
	}
	store := semanticAuthoringStore(t)
	config := Config{Capability: ports.CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: resolverScope}, nil
		},
		Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
	}}
	return testServiceSetup{Config: &config, Store: store, Snapshot: snapshot}, candidates
}

func fieldChoicePointer(t *testing.T, snapshot capability.Snapshot, candidate capability.Candidate) *capability.ConstructionChoice {
	t.Helper()
	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	return &choice
}

func constructionChoiceRequest(snapshot capability.Snapshot, commandID, choiceID string, form capability.ConstructionChoiceForm) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choiceID, Form: form}}}},
	}
}

func constructionSemanticChoiceFixture(t *testing.T, mutateEntry func(*semantic.Entry), test *constructionChoiceNegativeCase, root string) (*fakeStore, *Service, capability.Snapshot, semantic.Entry, capability.ConstructionChoice) {
	t.Helper()
	setup, entry, choice := constructionSemanticChoiceSetup(t, mutateEntry, test, root)
	return setup.Store, newTestServiceFromSetup(t, setup), setup.Snapshot, entry, choice
}

func constructionSemanticChoiceSetup(t *testing.T, mutateEntry func(*semantic.Entry), test *constructionChoiceNegativeCase, root string) (testServiceSetup, semantic.Entry, capability.ConstructionChoice) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "semantic-choice-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}
	candidate := capability.Candidate{ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value", Label: "Observed value", LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := *capabilityfixtures.SemanticSelectionEntry("concept-a", "binding-a", "urn:system:a", "code-a", "")
	choiceEntry := entry
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	semanticContext := ""
	if test != nil {
		if test.build != "" {
			semanticContext, buildID = "stale-build-context", test.build
		} else if test.context != "" {
			semanticContext = test.context
		}
		if test.candidateID != "" {
			candidate.ID = test.candidateID
		}
		if test.duplicatePath {
			snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{ID: "observation-value-duplicate", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value", Label: "Duplicate", LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}})
		}
	}
	if semanticContext == "" {
		var err error
		semanticContext, err = semanticbrowse.SemanticInventoryContextToken(snapshot, "patients", "Observation", buildID)
		if err != nil {
			t.Fatal(err)
		}
	}
	choice, err := semanticInventoryConstructionChoice(snapshot, semanticContext, buildID, choiceEntry, candidate, false)
	if err != nil {
		t.Fatal(err)
	}
	if mutateEntry != nil {
		mutateEntry(&entry)
	}
	store := semanticAuthoringStore(t)
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = root
	document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: root, Children: []ws.RouteNode{}}
	document.Columns = []ws.Column{}
	document.FixedFilters = []ws.FixedFilter{}
	document.Actions = []ws.Action{}
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := semanticAuthoringCatalog(snapshot)
	catalogSnapshot.Nodes = []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}
	catalogSnapshot.Edges = []ws.CatalogEdge{}
	catalogSnapshot.Candidates = []ws.CatalogCandidate{{ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Cardinality: candidate.Cardinality, Label: candidate.Label, LogicalType: candidate.LogicalType, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, candidate)}}
	if len(snapshot.Candidates) > 1 {
		duplicate := snapshot.Candidates[1]
		catalogSnapshot.Candidates = append(catalogSnapshot.Candidates, ws.CatalogCandidate{ID: duplicate.ID, NodeID: duplicate.NodeID, FieldPath: duplicate.FieldPath, Cardinality: duplicate.Cardinality, Label: duplicate.Label, LogicalType: duplicate.LogicalType, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, duplicate)})
	}
	config := Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error) {
			return semanticAuthoringInventory(snapshot, []semantic.Entry{entry}), nil
		},
	}
	return testServiceSetup{Config: &config, Store: store, Snapshot: snapshot}, entry, choice
}

func semanticInventoryConstructionChoice(snapshot capability.Snapshot, contextToken, buildID string, entry semantic.Entry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	observation := entry.Observation
	logicalType := observation.LogicalType
	if logicalType == "" {
		logicalType = observation.Value.Type
	}
	source := capability.SemanticBindingChoiceSource{
		ConceptID: entry.ConceptID, BindingID: entry.BindingID, ResourceType: observation.Source.Type,
		SourcePath: observation.Source.Path, SourceCanonical: observation.Source.Canonical,
		SourceProfile: observation.Source.Profile, FieldPath: candidate.FieldPath,
		OwningScope: observation.OwningScope, ExtensionURLPath: observation.ExtensionURLPath,
		KeySelector: observation.Key.Selector, System: observation.Key.System, Version: observation.Key.Version,
		Code: observation.Key.Code, ValueSelector: observation.Value.Selector,
		ValueFallbacks:        append([]string(nil), observation.Value.FallbackSelectors...),
		FallbackOnBlankString: observation.Value.FallbackOnBlankString, ChoiceArm: observation.ChoiceArm,
		LogicalType: logicalType, RuleHint: observation.RuleHint, RuleVersion: observation.RuleVersion,
		SchemaVersion: observation.SchemaVersion,
	}
	return capability.NewSemanticConstructionChoiceForRoute(snapshot.Token, contextToken, buildID, nil, source, candidate, ownerRecordsProved)
}
