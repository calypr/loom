package construction

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
)

func TestResolveSemanticConstructionChoiceUsesFallbackCandidateOptionsAcrossOptionalRoute(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "semantic-fallback-route", scope)
	snapshot.Nodes = []capability.Node{
		{ID: "medication", ResourceType: "Medication", RowRootEligible: true},
		{ID: "administration", ResourceType: "MedicationAdministration"},
		{ID: "patient", ResourceType: "Patient"},
		{ID: "observation", ResourceType: "Observation", RowRootEligible: true},
	}
	route := []capability.ConstructionRouteStep{
		{EdgeID: "medication-administration", FromNodeID: "medication", ToNodeID: "administration", FromResourceType: "Medication", ToResourceType: "MedicationAdministration", Relationship: "medication", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
		{EdgeID: "administration-patient", FromNodeID: "administration", ToNodeID: "patient", FromResourceType: "MedicationAdministration", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
		{EdgeID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "focus_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
	}
	snapshot.Edges = []capability.Edge{
		{ID: "medication-administration", FromNodeID: "medication", ToNodeID: "administration", SourceResourceType: "Medication", TargetResourceType: "MedicationAdministration", Label: "medication", StorageDirection: "INBOUND"},
		{ID: "administration-patient", FromNodeID: "administration", ToNodeID: "patient", SourceResourceType: "MedicationAdministration", TargetResourceType: "Patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
		{ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "focus_Patient", StorageDirection: "INBOUND"},
	}
	primary := capability.Candidate{
		ID: "observation-value-text", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.text", Label: "Value text", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	fallback := capability.Candidate{
		ID: "observation-coding-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.coding[].display", Label: "Value coding display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "valueCodeableConcept.coding[]", MaxItems: 1}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}
	snapshot.Candidates = []capability.Candidate{primary, fallback}
	entry := semantic.Entry{ConceptID: "concept-snomed-1222590007", BindingID: "binding-snomed", Observation: semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "http://snomed.info/sct", Code: "1222590007"},
		Value: semantic.Value{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
		ChoiceArm: "valueCodeableConcept", LogicalType: "string", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}}
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
	contextToken, err := semanticInventoryContextToken(snapshot, "explorer-a", "Medication", buildID)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, entry, primary, false)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	semanticSource, ok := identity.Source.(capability.SemanticBindingChoiceSource)
	if !ok {
		t.Fatalf("semantic choice source = %#v", identity.Source)
	}
	catalogSnapshot := ws.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []ws.CatalogCandidate{{
		ID: primary.ID, NodeID: primary.NodeID, FieldPath: primary.FieldPath, Cardinality: primary.Cardinality,
	}}}
	for _, option := range choice.Options {
		if option.Form == capability.ConstructionChoiceOwnerRecords {
			continue
		}
		resolved, _, err := resolveSemanticConstructionChoice(
			context.Background(), ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
			"explorer-a", "Medication", contextToken, buildID, buildID,
			map[string]semantic.Entry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry}, nil,
			authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: option.Form}, identity,
			semanticSource,
		)
		if err != nil {
			t.Fatalf("resolve fallback semantic choice form %s across OPTIONAL route: %v", option.Form, err)
		}
		if resolved.CandidateID != primary.ID || resolved.Source.Lookup == nil || resolved.Source.Lookup.Binding == nil ||
			resolved.Source.Lookup.Binding.ValuePath != "valueCodeableConcept.text" || resolved.Source.Lookup.ProjectionMode != string(option.Form) ||
			!reflect.DeepEqual(resolved.Route, route) {
			t.Fatalf("resolved fallback semantic choice form %s = %#v", option.Form, resolved)
		}
	}

	scalarEntry := entry
	scalarEntry.Observation.Value.FallbackSelectors = nil
	scalarEntry.Observation.Value.FallbackOnBlankString = false
	scalarChoice, err := semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, route, scalarEntry, primary, false)
	if err != nil {
		t.Fatal(err)
	}
	scalarFieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scalarChoice.Options, scalarFieldChoice.Options) {
		t.Fatalf("routed scalar semantic options differ from the exact compiler candidate: semantic=%#v field=%#v", scalarChoice.Options, scalarFieldChoice.Options)
	}
	scalarIdentity, err := capability.DecodeConstructionChoiceID(scalarChoice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	scalarSource, ok := scalarIdentity.Source.(capability.SemanticBindingChoiceSource)
	if !ok {
		t.Fatalf("routed scalar semantic source = %#v", scalarIdentity.Source)
	}
	for _, option := range scalarFieldChoice.Options {
		resolved, _, err := resolveSemanticConstructionChoice(
			context.Background(), ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
			"explorer-a", "Medication", contextToken, buildID, buildID,
			map[string]semantic.Entry{semanticChoiceKey(scalarEntry.ConceptID, scalarEntry.BindingID): scalarEntry}, nil,
			authoringv2.ConstructionChoiceSelection{ChoiceID: scalarChoice.ChoiceID, Form: option.Form}, scalarIdentity,
			scalarSource,
		)
		if err != nil {
			t.Fatalf("resolve routed scalar semantic choice form %s: %v", option.Form, err)
		}
		if resolved.CandidateID != primary.ID || resolved.Source.Lookup == nil || resolved.Source.Lookup.ProjectionMode != string(option.Form) || !reflect.DeepEqual(resolved.Route, route) {
			t.Fatalf("resolved routed scalar semantic choice form %s = %#v", option.Form, resolved)
		}
	}

	rootContextToken, err := semanticInventoryContextToken(snapshot, "explorer-a", "Observation", buildID)
	if err != nil {
		t.Fatal(err)
	}
	rootChoice, err := semanticInventoryConstructionChoiceForRoute(snapshot, rootContextToken, buildID, nil, entry, primary, false)
	if err != nil {
		t.Fatal(err)
	}
	rootIdentity, err := capability.DecodeConstructionChoiceID(rootChoice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	rootSemanticSource, ok := rootIdentity.Source.(capability.SemanticBindingChoiceSource)
	if !ok {
		t.Fatalf("root semantic choice source = %#v", rootIdentity.Source)
	}
	rootFieldChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, primary)
	if err != nil {
		t.Fatal(err)
	}
	rootCatalogSnapshot := ws.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []ws.CatalogCandidate{{
		ID: primary.ID, NodeID: primary.NodeID, FieldPath: primary.FieldPath, Cardinality: primary.Cardinality,
		ConstructionChoice: &rootFieldChoice,
	}}}
	rootResolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, rootCatalogSnapshot,
		"explorer-a", "Observation", rootContextToken, buildID, buildID,
		map[string]semantic.Entry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry}, nil,
		authoringv2.ConstructionChoiceSelection{ChoiceID: rootChoice.ChoiceID, Form: capability.ConstructionChoiceValue}, rootIdentity,
		rootSemanticSource,
	)
	if err != nil {
		t.Fatalf("resolve root semantic choice with catalog primary consistency: %v", err)
	}
	if rootResolved.CandidateID != primary.ID || rootResolved.Source.Lookup == nil || rootResolved.Source.Lookup.Binding == nil ||
		rootResolved.Source.Lookup.Binding.ValuePath != "valueCodeableConcept.text" || rootResolved.Source.Lookup.ProjectionMode != "VALUE" ||
		len(rootResolved.Route) != 0 {
		t.Fatalf("resolved root semantic choice = %#v", rootResolved)
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
	entry := semantic.Entry{ConceptID: "height", BindingID: "height-binding", Observation: semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "component[].code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "http://loinc.org", Code: "8302-2", Display: "Body height"},
		Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
		OwningScope:   "component[]", ChoiceArm: "valueQuantity", LogicalType: "decimal",
		Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1,
		RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}}
	buildID := semantic.BuildID(snapshot.Identity.Project, snapshot.Identity.Generation)
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
	catalogSnapshot := ws.CatalogSnapshot{SnapshotToken: snapshot.Token, Candidates: []ws.CatalogCandidate{{
		ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Cardinality: candidate.Cardinality,
		ConstructionChoice: &fieldChoice,
	}}}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := resolveSemanticConstructionChoice(
		context.Background(), ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, snapshot, catalogSnapshot,
		"patients", "Observation", contextToken, buildID, buildID,
		map[string]semantic.Entry{semanticChoiceKey(entry.ConceptID, entry.BindingID): entry}, nil,
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: capability.ConstructionChoiceOwnerRecords},
		identity,
		identity.Source.(capability.SemanticBindingChoiceSource),
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source.Kind != ws.SourceOwnerRecords || resolved.Source.OwnerRecords == nil || resolved.Source.Lookup != nil || resolved.LogicalType != "object" {
		t.Fatalf("resolved owner-record source = %#v", resolved)
	}
	if resolved.Source.OwnerRecords.Binding.OwnerPath != "component[]" || resolved.Source.OwnerRecords.Key.System != "http://loinc.org" || resolved.Source.OwnerRecords.Key.Code != "8302-2" {
		t.Fatalf("resolved owner-record identity = %#v", resolved.Source.OwnerRecords)
	}
}

func TestSchemaFieldCandidateChoiceRecompilesWithoutObservedCatalogEntry(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(scope, nil)
	snapshot.Nodes[0].RowRootEligible = true
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	candidate, ok := uniqueCapabilityCandidate(snapshot, schemaFieldCandidateID(snapshot.Token, "patient-node", "gender"))
	if !ok {
		t.Fatal("generated field identity was not resolved")
	}
	proved, err := proveConstructionCandidate(context.Background(), authorized, "Patient", candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, proved)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	field := choice.Source.(capability.FieldChoiceSource)
	resolved, _, err := resolveFieldConstructionChoice(context.Background(), authorized, snapshot,
		ws.CatalogSnapshot{SnapshotToken: snapshot.Token}, "Patient",
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: "VALUE"}, identity, field)
	if err != nil || resolved.CandidateID != candidate.ID || resolved.LogicalType != "string" {
		t.Fatalf("resolved generated choice = %#v, error=%v", resolved, err)
	}
}

func TestSchemaFieldCandidateMissingGenderHasNoObservedOrOperationClaims(t *testing.T) {
	snapshot := schemaFieldsTestSnapshot(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil)
	id := schemaFieldCandidateID(snapshot.Token, "patient-node", "gender")
	candidate, ok := resolveSchemaFieldCandidate(snapshot, id)
	if !ok || candidate.FieldPath != "gender" || candidate.LogicalType != "string" || candidate.Cardinality != "optional_one" {
		t.Fatalf("generated candidate = %#v, found=%v", candidate, ok)
	}
	if candidate.Observed || candidate.Populated || len(candidate.ProjectionModes) != 0 || len(candidate.SupportedOperations) != 0 {
		t.Fatalf("schema metadata must not claim population evidence or compiler proof: %#v", candidate)
	}
}

func TestSchemaFieldCandidateRejectsWrongSnapshotNodeAndPath(t *testing.T) {
	snapshot := schemaFieldsTestSnapshot(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil)
	for _, id := range []string{
		schemaFieldCandidateID("another-snapshot", "patient-node", "gender"),
		schemaFieldCandidateID(snapshot.Token, "outside-node", "gender"),
		schemaFieldCandidateID(snapshot.Token, "patient-node", "notAField"),
		"schema_field.invalid",
	} {
		if candidate, ok := resolveSchemaFieldCandidate(snapshot, id); ok {
			t.Fatalf("accepted unavailable identity %q: %#v", id, candidate)
		}
	}
	snapshot.Nodes[0].Populated = false
	if _, ok := resolveSchemaFieldCandidate(snapshot, schemaFieldCandidateID(snapshot.Token, "patient-node", "gender")); ok {
		t.Fatal("accepted an unpopulated node")
	}
}
