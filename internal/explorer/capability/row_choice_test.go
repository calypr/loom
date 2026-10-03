package capability

import (
	"strings"
	"testing"
)

func TestRowChoicePinsSchemaAndAuthorizedOccurrence(t *testing.T) {
	root := Node{ID: "root", ResourceType: "UnfamiliarRoot", RowRootEligible: true}
	leaf := Node{ID: "leaf", ResourceType: "UnfamiliarMember"}
	route := []ConstructionRouteStep{{
		EdgeID: "edge", FromNodeID: root.ID, ToNodeID: leaf.ID,
		FromResourceType: root.ResourceType, ToResourceType: leaf.ResourceType,
		Relationship: "member", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
	}}
	snapshot := routeSnapshot([]Node{root, leaf}, []Edge{{
		ID: "edge", FromNodeID: root.ID, ToNodeID: leaf.ID,
		SourceResourceType: root.ResourceType, TargetResourceType: leaf.ResourceType,
		Label: "member", StorageDirection: "OUTBOUND",
	}}, 3)
	occurrences := []RowChoiceOccurrence{{OccurrenceID: "member-occurrence", NodeID: leaf.ID, ResourceType: leaf.ResourceType, Route: route}}
	facts := RowChoiceFacts{ResourceType: leaf.ResourceType, CanonicalPath: "q_17", FHIRType: "boolean", Cardinality: RowChoiceOne, Shape: RowChoiceScalar, Title: "A flag"}
	choice, err := NewRowChoice(snapshot, occurrences, "member-occurrence", RowChoiceFieldGroupKey, facts)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRowChoiceID(choice.ChoiceID)
	if err != nil || decoded.SchemaDigest != snapshot.Identity.SchemaDigest || decoded.Path != facts.CanonicalPath || decoded.FHIRType != facts.FHIRType || decoded.Occurrence.OccurrenceID != occurrences[0].OccurrenceID {
		t.Fatalf("decoded row choice identity = %#v, %v", decoded, err)
	}

	occurrences[0].Route = cloneConstructionRoute(occurrences[0].Route)
	occurrences[0].Route[0].EdgeID = "mutated"
	if choice.Route[0].EdgeID != "edge" {
		t.Fatal("choice route aliases the caller's route")
	}
	changedPresentation := facts
	changedPresentation.Title = "Another label"
	stable, err := NewRowChoice(snapshot, []RowChoiceOccurrence{{OccurrenceID: "member-occurrence", NodeID: leaf.ID, ResourceType: leaf.ResourceType, Route: route}}, "member-occurrence", RowChoiceFieldGroupKey, changedPresentation)
	if err != nil || stable.ChoiceID != choice.ChoiceID {
		t.Fatalf("presentation change altered choice identity: %#v, %v", stable, err)
	}

	current := []RowChoiceOccurrence{{OccurrenceID: "member-occurrence", NodeID: leaf.ID, ResourceType: leaf.ResourceType, Route: route}}
	resolved, err := ResolveRowChoiceID(snapshot, current, choice.ChoiceID, func(resourceType, path string) (RowChoiceFacts, error) {
		if resourceType != leaf.ResourceType || path != facts.CanonicalPath {
			t.Fatalf("resolver input = %q %q", resourceType, path)
		}
		return facts, nil
	})
	if err != nil || resolved.ChoiceID != choice.ChoiceID {
		t.Fatalf("revalidated choice = %#v, %v", resolved, err)
	}
	if _, err := ResolveRowChoiceID(snapshot, current, choice.ChoiceID, func(string, string) (RowChoiceFacts, error) {
		changed := facts
		changed.FHIRType = "string"
		return changed, nil
	}); err == nil {
		t.Fatal("choice was accepted after its schema type changed")
	}
	parts := strings.Split(choice.ChoiceID, ".")
	parts[2] = "0" + parts[2][1:]
	if _, err := DecodeRowChoiceID(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered row choice digest was accepted")
	}
	staleIdentity := snapshot.Identity
	staleIdentity.Generation = "next-generation"
	stale := NewSnapshot(staleIdentity, snapshot.Policy, StatusReady, true, false, []Node{root, leaf}, snapshot.Edges, nil, nil)
	if _, err := ResolveRowChoiceID(stale, current, choice.ChoiceID, func(string, string) (RowChoiceFacts, error) { return facts, nil }); err == nil {
		t.Fatal("stale row choice was accepted")
	}
}

func TestRowChoiceRejectsInapplicableShapesAndAmbiguousOccurrences(t *testing.T) {
	root := Node{ID: "root", ResourceType: "UnfamiliarRoot", RowRootEligible: true}
	snapshot := routeSnapshot([]Node{root}, nil, 3)
	occurrence := RowChoiceOccurrence{OccurrenceID: "root-occurrence", NodeID: root.ID, ResourceType: root.ResourceType}
	base := RowChoiceFacts{ResourceType: root.ResourceType, CanonicalPath: "q_17", FHIRType: "string", Cardinality: RowChoiceOne, Shape: RowChoiceScalar}
	if _, err := NewRowChoice(snapshot, []RowChoiceOccurrence{occurrence}, occurrence.OccurrenceID, RowChoiceFieldGroupKey, base); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		kind  RowChoiceKind
		facts RowChoiceFacts
	}{
		{RowChoiceFieldGroupKey, RowChoiceFacts{ResourceType: root.ResourceType, CanonicalPath: "q_17[]", FHIRType: "string", Cardinality: RowChoiceMany, Shape: RowChoiceScalar}},
		{RowChoiceFieldGroupKey, RowChoiceFacts{ResourceType: root.ResourceType, CanonicalPath: "q_17", FHIRType: "Code", Cardinality: RowChoiceOne, Shape: RowChoiceObject}},
		{RowChoiceExpandedScope, base},
		{RowChoiceExpandedScope, RowChoiceFacts{ResourceType: root.ResourceType, CanonicalPath: "q_17[]", FHIRType: "Reference", Cardinality: RowChoiceMany, Shape: RowChoiceArray, Reference: true}},
	}
	for _, test := range invalid {
		if _, err := NewRowChoice(snapshot, []RowChoiceOccurrence{occurrence}, occurrence.OccurrenceID, test.kind, test.facts); err == nil {
			t.Errorf("accepted inapplicable choice %s %#v", test.kind, test.facts)
		}
	}
	if _, err := NewRowChoice(snapshot, []RowChoiceOccurrence{occurrence, occurrence}, occurrence.OccurrenceID, RowChoiceFieldGroupKey, base); err == nil {
		t.Fatal("ambiguous occurrence identity was accepted")
	}
	wrongType := occurrence
	wrongType.ResourceType = "OtherRoot"
	if _, err := NewRowChoice(snapshot, []RowChoiceOccurrence{wrongType}, wrongType.OccurrenceID, RowChoiceFieldGroupKey, RowChoiceFacts{ResourceType: "OtherRoot", CanonicalPath: "q_17", FHIRType: "string", Cardinality: RowChoiceOne, Shape: RowChoiceScalar}); err == nil {
		t.Fatal("occurrence resource type unrelated to its capability node was accepted")
	}
}

func routeSnapshot(nodes []Node, edges []Edge, maxHops int) Snapshot {
	return NewSnapshot(SnapshotIdentity{
		Project: "row-choice-project", Generation: "row-choice-generation", AuthorizationScopeDigest: "scope",
		SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
		FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
		TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
	}, Policy{Route: RoutePolicy{Version: "test-route", MaxHops: maxHops}}, StatusReady, true, false, nodes, edges, nil, nil)
}
