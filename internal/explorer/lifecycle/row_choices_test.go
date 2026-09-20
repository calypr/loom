package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestSchemaRowChoiceResolverListsEveryAuthoredOccurrenceAndPinsItsRoute(t *testing.T) {
	index, err := fhirschema.NewIndex([]fhirschema.Definition{
		{Name: "SignalPacket", Elements: []fhirschema.Element{{Name: "values", JSONType: fhirschema.JSONTypeArray, ItemJSONType: "string", Title: "Root values"}}},
		{Name: "SignalRecord", Elements: []fhirschema.Element{{Name: "labels", JSONType: fhirschema.JSONTypeArray, ItemJSONType: "string", Title: "Record labels"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := rowChoiceCapabilitySnapshot()
	route := authoringv2.RouteNode{
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "SignalPacket",
		Children: []authoringv2.RouteNode{{
			OccurrenceID: "record-occurrence", ResourceType: "SignalRecord", CatalogEdgeID: "edge-one",
			Relationship: "contains", MatchMode: authoringv2.RouteMatchOptional,
		}},
	}
	choices, err := resolver.ListRowChoices(context.Background(), snapshot, authoringv2.Document{
		RootResourceType: "SignalPacket", Route: route,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 2 {
		t.Fatalf("choices=%#v, want one choice at each authored occurrence", choices)
	}
	var childChoice *capability.RowChoice
	for index := range choices {
		if choices[index].OccurrenceID == "record-occurrence" {
			childChoice = &choices[index]
		}
	}
	if childChoice == nil || len(childChoice.Route) != 1 || childChoice.Route[0].EdgeID != "edge-one" {
		t.Fatalf("child route identity=%#v, want exact edge-one route", childChoice)
	}
	resolved, err := resolver.ResolveRowChoiceID(context.Background(), RowChoiceResolveRequest{
		Snapshot: snapshot, Route: route, RowChoiceID: childChoice.ChoiceID, ExpectedKind: RowChoiceExpanded,
	})
	if err != nil || resolved.OccurrenceID != "record-occurrence" || resolved.ScopePath != "labels[]" {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	substitutedRoute := route
	substitutedRoute.Children = append([]authoringv2.RouteNode(nil), route.Children...)
	substitutedRoute.Children[0].CatalogEdgeID = "edge-two"
	if _, err := resolver.ResolveRowChoiceID(context.Background(), RowChoiceResolveRequest{
		Snapshot: snapshot, Route: substitutedRoute, RowChoiceID: childChoice.ChoiceID, ExpectedKind: RowChoiceExpanded,
	}); err == nil {
		t.Fatal("accepted a choice after its authored occurrence changed to a different physical edge")
	}
}

func TestRowDefinitionComparisonPreservesScalarDistinctionsWithoutReturningValues(t *testing.T) {
	base := rowDefinitionPreviewRows{
		Rows: map[string]map[string]any{"row-1": {
			"zero": 0, "boolean": false, "empty": "", "nullable": nil, "array": []any{},
		}},
		Summary: dataframeexecution.PreviewSummary{Columns: []string{"zero", "boolean", "empty", "nullable", "array"}, RowCount: 1, Complete: true},
	}
	candidate := rowDefinitionPreviewRows{
		Rows: map[string]map[string]any{"row-1": {
			"zero": 1, "boolean": true, "empty": "non-empty", "nullable": "set", "array": []any{"item"},
		}},
		Summary: dataframeexecution.PreviewSummary{Columns: []string{"zero", "boolean", "empty", "nullable", "array"}, RowCount: 1, Complete: false, Truncated: true},
	}
	comparison := compareRowDefinitionPreviewRows(base, candidate)
	wantColumns := []string{"array", "boolean", "empty", "nullable", "zero"}
	if comparison.Status != RowDefinitionComparisonAvailable || !reflect.DeepEqual(comparison.AffectedColumns, wantColumns) || comparison.Base == nil || comparison.Base.Sampled || comparison.Candidate == nil || !comparison.Candidate.Sampled {
		t.Fatalf("comparison=%#v", comparison)
	}
	if len(comparison.Examples) != 1 || comparison.Examples[0].RowIdentity != "row-1" || !comparison.Examples[0].BasePresent || !comparison.Examples[0].CandidatePresent {
		t.Fatalf("examples=%#v", comparison.Examples)
	}
	encoded, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"non-empty"`, `"item"`, `"set"`} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("comparison exposed sampled row content %s: %s", value, encoded)
		}
	}
}

func rowChoiceCapabilitySnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-digest", SchemaDigest: "schema-digest",
			ResourceInventoryDigest: "inventory", RelationshipDigest: "relationship", FieldDigest: "field", ShapeDigest: "shape",
			ProtocolVersion: "protocol", CompilerVersion: "compiler", TraversalPolicyVersion: "route-policy", ProjectionPolicyVersion: "projection-policy",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "route-policy"}}, capability.StatusReady, true, false,
		[]capability.Node{
			{ID: "node-root", ResourceType: "SignalPacket", RowRootEligible: true, Populated: true},
			{ID: "node-record", ResourceType: "SignalRecord", Populated: true},
		},
		[]capability.Edge{
			{ID: "edge-one", FromNodeID: "node-root", ToNodeID: "node-record", Label: "contains", StorageDirection: "OUTBOUND", SourceResourceType: "SignalPacket", TargetResourceType: "SignalRecord"},
			{ID: "edge-two", FromNodeID: "node-root", ToNodeID: "node-record", Label: "contains", StorageDirection: "OUTBOUND", SourceResourceType: "SignalPacket", TargetResourceType: "SignalRecord"},
		},
		[]capability.Candidate{
			{ID: "candidate-root", NodeID: "node-root", ResourceType: "SignalPacket", FieldPath: "values[]", Label: "Root values"},
			{ID: "candidate-record", NodeID: "node-record", ResourceType: "SignalRecord", FieldPath: "labels[]", Label: "Record labels"},
		}, nil,
	)
}
