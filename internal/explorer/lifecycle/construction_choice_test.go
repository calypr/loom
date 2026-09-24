package lifecycle

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type constructionChoiceNegativeCase struct {
	name          string
	root          string
	context       string
	build         string
	candidateID   string
	duplicatePath bool
	form          capability.ConstructionChoiceForm
	mutateEntry   func(*catalog.SemanticInventoryEntry)
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
			before, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
			if err != nil {
				t.Fatal(err)
			}
			response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "field-"+strings.ToLower(string(test.form)), choice.ChoiceID, test.form), "alice")
			if err != nil {
				t.Fatal(err)
			}
			document := response.Workspace.Documents[0]
			column := document.Columns[len(document.Columns)-1]
			if column.Source.Kind != authoringv2.SourceField || column.Source.Field == nil || column.Source.Field.Path != "name[].family" || column.Source.Field.ProjectionMode != string(test.form) || column.LogicalType != "string" || column.Label != "Patient family" {
				t.Fatalf("applied column = %#v", column)
			}
			if response.Results[0].Type != authoringv2.CommandResultColumnAdded || response.Results[0].Column != column.Column || column.OccurrenceID != authoringv2.RootOccurrenceID {
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
	if column.Source.Kind != authoringv2.SourceField || column.Source.Field == nil || column.Source.Field.Path != "id" || column.Source.Field.ProjectionMode != "VALUE" || column.LogicalType != "string" {
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
	if column.Source.Kind != authoringv2.SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil || column.Source.Lookup.Key == nil ||
		column.Source.Lookup.Key.System != entry.Observation.Key.System || column.Source.Lookup.Key.Code != entry.Observation.Key.Code ||
		column.Source.Lookup.Binding.ValuePath != entry.Observation.Value.Selector || column.Source.Lookup.ProjectionMode != "VALUE" ||
		column.LogicalType != "decimal" || column.OccurrenceID != authoringv2.RootOccurrenceID || document.RootResourceType != "Observation" ||
		document.Route.ResourceType != "Observation" || len(document.Route.Children) != 0 {
		t.Fatalf("semantic source did not map to the compiler's root binding: %#v document=%#v", column, document)
	}
}

func TestApplyConstructionChoicePreservesCategoricalNamespace(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "categorical-slot-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "condition", ResourceType: "Condition", RowRootEligible: true}}
	candidate := capability.Candidate{
		ID: "condition-code", NodeID: "condition", ResourceType: "Condition",
		FieldPath: "code.coding[].code", Label: "Diagnosis code", LogicalType: "code",
		Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]"}},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := catalog.SemanticInventoryEntry{ConceptID: "condition-code-slot", BindingID: "condition-code-binding", Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion, Role: catalog.SemanticRoleCategoricalSlot,
		SlotLabel: "Diagnosis", Source: catalog.SemanticObservationSource{Type: "Condition", Path: "code"},
		Key: catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:diagnosis"}, Value: catalog.SemanticObservationValue{Selector: "code.coding[].code", Type: "code"},
		LogicalType: "code", Completeness: catalog.SemanticComplete, Status: "SUPPORTED",
		RuleHint: catalog.SemanticRuleHintCategoricalSlotV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}}
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	store := semanticAuthoringStore(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].RootResourceType = "Condition"
	workspace.Documents[0].Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Condition"}
	workspace.Documents[0].Columns = nil
	workspace.Documents[0].FixedFilters = nil
	workspace.Documents[0].Actions = nil
	workspace.Documents[0].Population = nil
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	rowContext, err := savedCatalogRowContext(workspace, "patients")
	if err != nil {
		t.Fatal(err)
	}
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Condition", buildID, rowContext.Digest)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoice(snapshot, contextToken, buildID, entry, candidate, false)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := semanticAuthoringCatalog(snapshot)
	catalogSnapshot.Nodes = []authoringv2.CatalogNode{{ID: "condition", ResourceType: "Condition", RowRootEligible: true}}
	catalogSnapshot.Edges = nil
	catalogSnapshot.Candidates = []authoringv2.CatalogCandidate{{
		ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Cardinality: candidate.Cardinality,
		Label: candidate.Label, LogicalType: candidate.LogicalType, Repeated: true,
		RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "code.coding[]"}}, ProjectionModes: []string{"FIRST", "ALL"},
		DefaultProjectionMode: "ALL", ConstructionChoice: fieldChoicePointer(t, snapshot, candidate),
	}}
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{entry}), nil
		},
	})
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", constructionChoiceRequest(snapshot, "categorical-slot", choice.ChoiceID, capability.ConstructionChoiceAll), "alice")
	if err != nil {
		t.Fatal(err)
	}
	column := response.Workspace.Documents[0].Columns[0]
	if column.Label != "Diagnosis" || column.LogicalType != "code" || column.Source.Kind != authoringv2.SourceCategoricalBySystem || column.Source.Categorical == nil || column.Source.Categorical.System != "urn:diagnosis" || column.Source.Categorical.ProjectionMode != "ALL" {
		t.Fatalf("categorical slot column = %#v", column)
	}
}

func TestResolveSemanticConstructionChoiceBuildsOwnerRecordSourceAfterCompilerProof(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "owner-record-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	candidate := capability.Candidate{
		ID: "component-value", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "component[].valueQuantity.value", Label: "Component value", LogicalType: "decimal",
		Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]"}},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := catalog.SemanticInventoryEntry{ConceptID: "height", BindingID: "height-binding", Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "component[].code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "http://loinc.org", Code: "8302-2", Display: "Body height"},
		Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		OwningScope:   "component[]", ChoiceArm: "valueQuantity", LogicalType: "decimal",
		Completeness: catalog.SemanticComplete, Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1,
		RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}}
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Observation", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoice(snapshot, contextToken, buildID, entry, candidate, true)
	if err != nil {
		t.Fatal(err)
	}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []authoringv2.CatalogCandidate{{
		ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Cardinality: candidate.Cardinality,
		ConstructionChoice: &fieldChoice,
	}}}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
		"patients", "Observation", authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"}, "", contextToken, buildID, buildID,
		map[string]catalog.SemanticInventoryEntry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry},
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceOwnerRecords},
		identity,
		identity.Source.(capability.SemanticBindingChoiceSource),
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source.Kind != authoringv2.SourceOwnerRecords || resolved.Source.OwnerRecords == nil || resolved.Source.Lookup != nil || resolved.LogicalType != "object" {
		t.Fatalf("resolved owner-record source = %#v", resolved)
	}
	if resolved.Source.OwnerRecords.Binding.OwnerPath != "component[]" || resolved.Source.OwnerRecords.Key.System != "http://loinc.org" || resolved.Source.OwnerRecords.Key.Code != "8302-2" {
		t.Fatalf("resolved owner-record identity = %#v", resolved.Source.OwnerRecords)
	}
}

func TestResolveRelatedCodedChoicePreservesAllMatchingValues(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "related-coded-choice", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "observation", ResourceType: "Observation"},
	}
	snapshot.Edges = []capability.Edge{{
		ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
		SourceResourceType: "Patient", TargetResourceType: "Observation",
		Label: "subject_Patient", StorageDirection: "INBOUND", ObservedEdgeCount: 2,
	}}
	candidate := capability.Candidate{
		ID: "height-value", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueQuantity.value", Label: "Height", LogicalType: "decimal",
		Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := catalog.SemanticInventoryEntry{ConceptID: "height", BindingID: "height-binding", Observation: catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source:        catalog.SemanticObservationSource{Type: "Observation", Path: "code"},
		Key:           catalog.SemanticObservationKey{Selector: "code.coding[]", System: "http://loinc.org", Code: "8302-2", Display: "Body height"},
		Value:         catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: catalog.SemanticComplete,
		Status: "SUPPORTED", RuleHint: catalog.SemanticRuleHintCodedValueV1,
		RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion),
	}}
	route := []capability.ConstructionRouteStep{{
		EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	authorized := AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	provenCandidate, err := proveConstructionCandidate(context.Background(), authorized, "Patient", candidate, route)
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "patients", "Patient", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, entry, provenCandidate, false)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []authoringv2.CatalogCandidate{{
		ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
		Cardinality: candidate.Cardinality, ConstructionChoice: &fieldChoice,
	}}}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), authorized, snapshot, catalogSnapshot, "patients", "Patient",
		authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		"", contextToken, buildID, buildID,
		map[string]catalog.SemanticInventoryEntry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry},
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceAll},
		identity, identity.Source.(capability.SemanticBindingChoiceSource), true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source.Kind != authoringv2.SourceCodedValue || resolved.Source.Lookup == nil ||
		resolved.Source.Lookup.ProjectionMode != "ALL" || len(resolved.Route) != 1 {
		t.Fatalf("related coded choice = %#v", resolved)
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
		{name: "changed observation source", root: "Observation", form: capability.ConstructionChoiceValue, mutateEntry: func(entry *catalog.SemanticInventoryEntry) { entry.Observation.Key.Code = "changed-code" }},
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
	request.Commands = append(request.Commands, authoringv2.Command{
		Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", InitialPresentation: authoringv2.InitialPresentationFilter,
		ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: repeatedChoice.ChoiceID, Form: capability.ConstructionChoiceAll},
	})
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
	request.Commands = append(request.Commands, authoringv2.Command{
		Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients",
		ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: repeatedChoice.ChoiceID, Form: capability.ConstructionChoiceValue},
	})
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
	return constructionChoiceFixtureWithScopes(t, resolverScope, resolverScope)
}

func constructionChoiceFixtureWithScopes(t *testing.T, snapshotScope, resolverScope authscope.ReadScope) (*fakeStore, *Service, capability.Snapshot, []capability.Candidate) {
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
	catalogCandidates := []authoringv2.CatalogCandidate{
		{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "Patient ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
		{ID: "patient-family", NodeID: "patient", FieldPath: "name[].family", Cardinality: "many", Repeated: true, RepeatedBoundaries: []authoringv2.RepeatedBoundary{{Path: "name[]", MaxItems: 4}}, Label: "Patient family", LogicalType: "string", ProjectionModes: []string{"FIRST", "ALL", "DISTINCT"}, DefaultProjectionMode: "ALL", Filterable: true, Chartable: true, ConstructionChoice: fieldChoicePointer(t, snapshot, candidates[1])},
		{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "Encounter ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, candidates[2])},
	}
	catalogSnapshot := authoringv2.CatalogSnapshot{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: snapshot.Identity.Project, ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
		Nodes:      []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "encounter", ResourceType: "Encounter"}, {ID: "observation", ResourceType: "Observation"}},
		Edges:      []authoringv2.CatalogEdge{{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"}, {ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"}},
		Candidates: catalogCandidates, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
	}
	store := semanticAuthoringStore(t)
	service := newTestService(t, store, Config{Capability: CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: resolverScope}, nil
		},
		Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
	}})
	return store, service, snapshot, candidates
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
		CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: choiceID, Form: form}}},
	}
}

func constructionSemanticChoiceFixture(t *testing.T, mutateEntry func(*catalog.SemanticInventoryEntry), test *constructionChoiceNegativeCase, root string) (*fakeStore, *Service, capability.Snapshot, catalog.SemanticInventoryEntry, capability.ConstructionChoice) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "semantic-choice-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}
	candidate := capability.Candidate{ID: "observation-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "valueQuantity.value", Label: "Observed value", LogicalType: "decimal", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
	snapshot.Candidates = []capability.Candidate{candidate}
	entry := *semanticAuthoringEntry("concept-a", "binding-a", "urn:system:a", "code-a", "")
	choiceEntry := entry
	buildID := catalog.SemanticInventoryBuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
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
	if mutateEntry != nil {
		mutateEntry(&entry)
	}
	store := semanticAuthoringStore(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := &workspace.Documents[0]
	document.RootResourceType = root
	document.Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: root, Children: []authoringv2.RouteNode{}}
	document.Columns = []authoringv2.Column{}
	document.FixedFilters = []authoringv2.FixedFilter{}
	document.Actions = []authoringv2.Action{}
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if semanticContext == "" {
		rowContext, contextErr := savedCatalogRowContext(workspace, "patients")
		if contextErr != nil {
			t.Fatal(contextErr)
		}
		semanticContext, err = semanticInventoryContextToken(snapshot, "patients", "Observation", buildID, rowContext.Digest)
		if err != nil {
			t.Fatal(err)
		}
	}
	choice, err := semanticInventoryConstructionChoice(snapshot, semanticContext, buildID, choiceEntry, candidate, false)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := semanticAuthoringCatalog(snapshot)
	catalogSnapshot.Nodes = []authoringv2.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}
	catalogSnapshot.Edges = []authoringv2.CatalogEdge{}
	catalogSnapshot.Candidates = []authoringv2.CatalogCandidate{{ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Cardinality: candidate.Cardinality, Label: candidate.Label, LogicalType: candidate.LogicalType, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, candidate)}}
	if len(snapshot.Candidates) > 1 {
		duplicate := snapshot.Candidates[1]
		catalogSnapshot.Candidates = append(catalogSnapshot.Candidates, authoringv2.CatalogCandidate{ID: duplicate.ID, NodeID: duplicate.NodeID, FieldPath: duplicate.FieldPath, Cardinality: duplicate.Cardinality, Label: duplicate.Label, LogicalType: duplicate.LogicalType, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: fieldChoicePointer(t, snapshot, duplicate)})
	}
	service := newTestService(t, store, Config{
		Capability: CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
				return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalogSnapshot },
		},
		ResolveSemanticInventorySelections: func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error) {
			return semanticAuthoringInventory(snapshot, []catalog.SemanticInventoryEntry{entry}), nil
		},
	})
	return store, service, snapshot, entry, choice
}
