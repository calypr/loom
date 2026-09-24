package capability

import (
	"errors"
	"strings"
	"testing"
)

func routeSnapshot(nodes []Node, edges []Edge, maxHops int) Snapshot {
	return NewSnapshot(testIdentity(), Policy{Route: RoutePolicy{Version: "r1", MaxHops: maxHops, AllowsRepeatedEdges: true, AllowsSelfLoops: true}}, StatusReady, true, false, nodes, edges, nil, nil)
}

func TestPlanConstructionRoutesPreservesZeroHopAndExactDirectedSteps(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	observation := Node{ID: "observation", ResourceType: "Observation"}
	snapshot := routeSnapshot([]Node{root, observation}, []Edge{{
		ID: "subject-inbound", FromNodeID: root.ID, ToNodeID: observation.ID,
		SourceResourceType: root.ResourceType, TargetResourceType: observation.ResourceType,
		Label: "subject_Patient", StorageDirection: "INBOUND",
	}}, 5)
	zero, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: "Patient", TargetNodeID: root.ID, SourceKey: "field:patient-id"})
	if err != nil || !zero.Complete || len(zero.Routes) != 1 || len(zero.Routes[0]) != 0 {
		t.Fatalf("zero-hop route = %#v, %v", zero, err)
	}
	page, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: "Patient", TargetNodeID: observation.ID, SourceKey: "field:observation-status"})
	if err != nil || !page.Complete || len(page.Routes) != 1 || len(page.Routes[0]) != 1 {
		t.Fatalf("inbound route = %#v, %v", page, err)
	}
	step := page.Routes[0][0]
	if step.EdgeID != "subject-inbound" || step.FromNodeID != root.ID || step.ToNodeID != observation.ID || step.StorageDirection != "INBOUND" || step.MatchMode != "OPTIONAL" {
		t.Fatalf("route step lost exact directed identity: %#v", step)
	}
}

func TestPlanConstructionRoutesKeepsDistinctAlternativesAndPages(t *testing.T) {
	root := Node{ID: "root", ResourceType: "Patient", RowRootEligible: true}
	left := Node{ID: "left", ResourceType: "Encounter"}
	right := Node{ID: "right", ResourceType: "ResearchSubject"}
	target := Node{ID: "target", ResourceType: "Observation"}
	nodes := []Node{root, left, right, target}
	edges := []Edge{
		{ID: "a", FromNodeID: root.ID, ToNodeID: left.ID, SourceResourceType: root.ResourceType, TargetResourceType: left.ResourceType, Label: "encounter", StorageDirection: "OUTBOUND"},
		{ID: "b", FromNodeID: left.ID, ToNodeID: target.ID, SourceResourceType: left.ResourceType, TargetResourceType: target.ResourceType, Label: "result", StorageDirection: "OUTBOUND"},
		{ID: "c", FromNodeID: root.ID, ToNodeID: right.ID, SourceResourceType: root.ResourceType, TargetResourceType: right.ResourceType, Label: "research", StorageDirection: "INBOUND"},
		{ID: "d", FromNodeID: right.ID, ToNodeID: target.ID, SourceResourceType: right.ResourceType, TargetResourceType: target.ResourceType, Label: "observation", StorageDirection: "OUTBOUND"},
	}
	snapshot := routeSnapshot(nodes, edges, 5)
	first, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: "Patient", TargetNodeID: target.ID, SourceKey: "field:obs-value", Limit: 1})
	if err != nil || first.Complete || !first.Truncated || first.NextCursor == "" || len(first.Routes) != 1 {
		t.Fatalf("first route page = %#v, %v", first, err)
	}
	second, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: "Patient", TargetNodeID: target.ID, SourceKey: "field:obs-value", Limit: 1, Cursor: first.NextCursor})
	if err != nil || !second.Complete || second.Truncated || len(second.Routes) != 1 {
		t.Fatalf("second route page = %#v, %v", second, err)
	}
	if len(first.Routes[0]) != 2 || len(second.Routes[0]) != 2 || first.Routes[0][0].EdgeID == second.Routes[0][0].EdgeID {
		t.Fatalf("planner collapsed meaningful alternatives: first=%#v second=%#v", first.Routes, second.Routes)
	}
}

func TestPlanConstructionRoutesProvesFiveEdgesAndReportsHorizonCutoff(t *testing.T) {
	nodes := make([]Node, 7)
	edges := make([]Edge, 6)
	for index := range nodes {
		nodes[index] = Node{ID: string(rune('a' + index)), ResourceType: "Resource" + string(rune('A'+index)), RowRootEligible: index == 0}
	}
	for index := range edges {
		edges[index] = Edge{
			ID: string(rune('A' + index)), FromNodeID: nodes[index].ID, ToNodeID: nodes[index+1].ID,
			SourceResourceType: nodes[index].ResourceType, TargetResourceType: nodes[index+1].ResourceType,
			Label: "edge", StorageDirection: "OUTBOUND",
		}
	}
	snapshot := routeSnapshot(nodes, edges, 0)
	page, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: nodes[0].ResourceType, TargetNodeID: nodes[5].ID, SourceKey: "field:deep"})
	if err != nil || len(page.Routes) != 1 || len(page.Routes[0]) != 5 || !page.Truncated || page.Complete {
		t.Fatalf("five-hop route/horizon result = %#v, %v", page, err)
	}
}

func TestPlanConstructionRoutesAtExplicitMaxHopsIsComplete(t *testing.T) {
	root := Node{ID: "root", ResourceType: "Patient", RowRootEligible: true}
	first := Node{ID: "first", ResourceType: "Encounter"}
	second := Node{ID: "second", ResourceType: "Observation"}
	third := Node{ID: "third", ResourceType: "Specimen"}
	snapshot := routeSnapshot([]Node{root, first, second, third}, []Edge{
		{ID: "one", FromNodeID: root.ID, ToNodeID: first.ID, SourceResourceType: root.ResourceType, TargetResourceType: first.ResourceType, Label: "encounter", StorageDirection: "OUTBOUND"},
		{ID: "two", FromNodeID: first.ID, ToNodeID: second.ID, SourceResourceType: first.ResourceType, TargetResourceType: second.ResourceType, Label: "observation", StorageDirection: "OUTBOUND"},
		{ID: "three", FromNodeID: second.ID, ToNodeID: third.ID, SourceResourceType: second.ResourceType, TargetResourceType: third.ResourceType, Label: "specimen", StorageDirection: "OUTBOUND"},
	}, 2)

	page, err := PlanConstructionRoutes(ConstructionRouteSearch{
		Snapshot: snapshot, RootResource: root.ResourceType, TargetNodeID: second.ID, SourceKey: "field:observation-value",
	})
	if err != nil || len(page.Routes) != 1 || len(page.Routes[0]) != 2 || page.Truncated || !page.Complete || page.NextCursor != "" {
		t.Fatalf("route at explicit maxHops was reported incomplete: page=%#v err=%v", page, err)
	}
}

func TestPlanConstructionRoutesRejectsCursorForAnotherExactSource(t *testing.T) {
	root := Node{ID: "root", ResourceType: "Patient", RowRootEligible: true}
	target := Node{ID: "target", ResourceType: "Observation"}
	snapshot := routeSnapshot([]Node{root, target}, []Edge{
		{ID: "one", FromNodeID: root.ID, ToNodeID: target.ID, SourceResourceType: root.ResourceType, TargetResourceType: target.ResourceType, Label: "a", StorageDirection: "OUTBOUND"},
		{ID: "two", FromNodeID: root.ID, ToNodeID: target.ID, SourceResourceType: root.ResourceType, TargetResourceType: target.ResourceType, Label: "b", StorageDirection: "OUTBOUND"},
	}, 3)
	first, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: root.ResourceType, TargetNodeID: target.ID, SourceKey: "field:a", Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page has no continuation: %#v, %v", first, err)
	}
	if _, err := PlanConstructionRoutes(ConstructionRouteSearch{Snapshot: snapshot, RootResource: root.ResourceType, TargetNodeID: target.ID, SourceKey: "field:b", Limit: 1, Cursor: first.NextCursor}); err == nil {
		t.Fatal("route cursor was accepted for a different exact source")
	}
}

func TestPlanConstructionRoutesSupportsSeveralExactTerminalNodes(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	first := Node{ID: "observation-a", ResourceType: "Observation"}
	second := Node{ID: "observation-b", ResourceType: "Observation"}
	snapshot := routeSnapshot([]Node{root, first, second}, []Edge{
		{ID: "a", FromNodeID: root.ID, ToNodeID: first.ID, SourceResourceType: root.ResourceType, TargetResourceType: first.ResourceType, Label: "subject_Patient", StorageDirection: "INBOUND"},
		{ID: "b", FromNodeID: root.ID, ToNodeID: second.ID, SourceResourceType: root.ResourceType, TargetResourceType: second.ResourceType, Label: "focus_Patient", StorageDirection: "INBOUND"},
	}, 3)
	page, err := PlanConstructionRoutes(ConstructionRouteSearch{
		Snapshot: snapshot, RootResource: "Patient", TargetNodeIDs: []string{second.ID, first.ID}, SourceKey: "population:patients\x00selection-1",
	})
	if err != nil || !page.Complete || len(page.Routes) != 2 {
		t.Fatalf("multiple terminal nodes = %#v, %v", page, err)
	}
	if page.Routes[0][0].EdgeID != "a" || page.Routes[1][0].EdgeID != "b" {
		t.Fatalf("terminal alternatives are not deterministic: %#v", page.Routes)
	}
}

func TestPlanCanonicalConstructionRouteReturnsShortestObservedPath(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	observation := Node{ID: "observation", ResourceType: "Observation"}
	specimen := Node{ID: "specimen", ResourceType: "Specimen"}
	snapshot := routeSnapshot([]Node{root, observation, specimen}, []Edge{
		{ID: "subject-inbound", FromNodeID: root.ID, ToNodeID: observation.ID, SourceResourceType: root.ResourceType, TargetResourceType: observation.ResourceType, Label: "subject_Patient", StorageDirection: "INBOUND", ObservedEdgeCount: 4},
		{ID: "specimen-outbound", FromNodeID: observation.ID, ToNodeID: specimen.ID, SourceResourceType: observation.ResourceType, TargetResourceType: specimen.ResourceType, Label: "specimen", StorageDirection: "OUTBOUND", ObservedEdgeCount: 2},
		{ID: "specimen-return", FromNodeID: specimen.ID, ToNodeID: observation.ID, SourceResourceType: specimen.ResourceType, TargetResourceType: observation.ResourceType, Label: "observation", StorageDirection: "OUTBOUND", ObservedEdgeCount: 2},
	}, 0)

	got, err := PlanCanonicalConstructionRoute(snapshot, root.ResourceType, observation.ID)
	if err != nil || len(got) != 1 || got[0].EdgeID != "subject-inbound" || got[0].StorageDirection != "INBOUND" {
		t.Fatalf("canonical shortest route = %#v, %v", got, err)
	}
}

func TestPlanCanonicalConstructionRouteRanksObservedCountsAcrossMedicationStyleRoute(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	medication := Node{ID: "medication", ResourceType: "MedicationRequest"}
	medicationValue := Node{ID: "medication-value", ResourceType: "Medication"}
	snapshot := routeSnapshot([]Node{root, medication, medicationValue}, []Edge{
		{ID: "patient-medication-weak", FromNodeID: root.ID, ToNodeID: medication.ID, SourceResourceType: root.ResourceType, TargetResourceType: medication.ResourceType, Label: "medicationRequest", StorageDirection: "OUTBOUND", ObservedEdgeCount: 1},
		{ID: "patient-medication-strong", FromNodeID: root.ID, ToNodeID: medication.ID, SourceResourceType: root.ResourceType, TargetResourceType: medication.ResourceType, Label: "medicationRequest", StorageDirection: "OUTBOUND", ObservedEdgeCount: 9},
		{ID: "medication-value", FromNodeID: medication.ID, ToNodeID: medicationValue.ID, SourceResourceType: medication.ResourceType, TargetResourceType: medicationValue.ResourceType, Label: "medication", StorageDirection: "OUTBOUND", ObservedEdgeCount: 1},
	}, 0)

	got, err := PlanCanonicalConstructionRoute(snapshot, root.ResourceType, medicationValue.ID)
	if err != nil || len(got) != 2 || got[0].EdgeID != "patient-medication-strong" || got[1].EdgeID != "medication-value" {
		t.Fatalf("ranked medication route = %#v, %v", got, err)
	}
}

func TestPlanCanonicalConstructionRouteUsesLaterObservedCountAfterPrefixTie(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	left := Node{ID: "left", ResourceType: "Encounter"}
	right := Node{ID: "right", ResourceType: "ResearchSubject"}
	target := Node{ID: "observation", ResourceType: "Observation"}
	snapshot := routeSnapshot([]Node{root, left, right, target}, []Edge{
		{ID: "root-left", FromNodeID: root.ID, ToNodeID: left.ID, SourceResourceType: root.ResourceType, TargetResourceType: left.ResourceType, Label: "left", StorageDirection: "OUTBOUND", ObservedEdgeCount: 5},
		{ID: "root-right", FromNodeID: root.ID, ToNodeID: right.ID, SourceResourceType: root.ResourceType, TargetResourceType: right.ResourceType, Label: "right", StorageDirection: "OUTBOUND", ObservedEdgeCount: 5},
		{ID: "left-target", FromNodeID: left.ID, ToNodeID: target.ID, SourceResourceType: left.ResourceType, TargetResourceType: target.ResourceType, Label: "observation", StorageDirection: "OUTBOUND", ObservedEdgeCount: 2},
		{ID: "right-target", FromNodeID: right.ID, ToNodeID: target.ID, SourceResourceType: right.ResourceType, TargetResourceType: target.ResourceType, Label: "observation", StorageDirection: "OUTBOUND", ObservedEdgeCount: 9},
	}, 0)

	got, err := PlanCanonicalConstructionRoute(snapshot, root.ResourceType, target.ID)
	if err != nil || len(got) != 2 || got[0].EdgeID != "root-right" || got[1].EdgeID != "right-target" {
		t.Fatalf("later-count route = %#v, %v", got, err)
	}
}

func TestPlanCanonicalConstructionRouteBreaksExactCountTiesByStableIdentity(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	left := Node{ID: "left", ResourceType: "Encounter"}
	right := Node{ID: "right", ResourceType: "ResearchSubject"}
	target := Node{ID: "observation", ResourceType: "Observation"}
	edges := []Edge{
		{ID: "root-left", FromNodeID: root.ID, ToNodeID: left.ID, SourceResourceType: root.ResourceType, TargetResourceType: left.ResourceType, Label: "left", StorageDirection: "OUTBOUND", ObservedEdgeCount: 5},
		{ID: "root-right", FromNodeID: root.ID, ToNodeID: right.ID, SourceResourceType: root.ResourceType, TargetResourceType: right.ResourceType, Label: "right", StorageDirection: "OUTBOUND", ObservedEdgeCount: 5},
		{ID: "left-target", FromNodeID: left.ID, ToNodeID: target.ID, SourceResourceType: left.ResourceType, TargetResourceType: target.ResourceType, Label: "observation", StorageDirection: "OUTBOUND", ObservedEdgeCount: 3},
		{ID: "right-target", FromNodeID: right.ID, ToNodeID: target.ID, SourceResourceType: right.ResourceType, TargetResourceType: target.ResourceType, Label: "observation", StorageDirection: "OUTBOUND", ObservedEdgeCount: 3},
	}
	want := "left-target"
	for _, ordered := range [][]Edge{edges, {edges[3], edges[2], edges[1], edges[0]}} {
		got, err := PlanCanonicalConstructionRoute(routeSnapshot([]Node{root, left, right, target}, ordered, 0), root.ResourceType, target.ID)
		if err != nil || len(got) != 2 || got[1].EdgeID != want {
			t.Fatalf("stable tie route = %#v, %v; want final edge %q", got, err, want)
		}
	}
}

func TestPlanCanonicalConstructionRouteRejectsUnobservedEdges(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	observation := Node{ID: "observation", ResourceType: "Observation"}
	snapshot := routeSnapshot([]Node{root, observation}, []Edge{{
		ID: "subject-inbound", FromNodeID: root.ID, ToNodeID: observation.ID,
		SourceResourceType: root.ResourceType, TargetResourceType: observation.ResourceType,
		Label: "subject_Patient", StorageDirection: "INBOUND",
	}}, 0)

	_, err := PlanCanonicalConstructionRoute(snapshot, root.ResourceType, observation.ID)
	if !errors.Is(err, ErrCanonicalConstructionRouteUnavailable) {
		t.Fatalf("unobserved route error = %v, want unavailable", err)
	}
}

func TestPlanCanonicalConstructionRouteAllowsZeroHop(t *testing.T) {
	root := Node{ID: "patient", ResourceType: "Patient", RowRootEligible: true}
	snapshot := routeSnapshot([]Node{root}, nil, 0)

	got, err := PlanCanonicalConstructionRoute(snapshot, root.ResourceType, root.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("zero-hop canonical route = %#v, %v", got, err)
	}
}

func TestPopulationRouteChoiceIDPinsSelectionOutputAndRoute(t *testing.T) {
	route := []ConstructionRouteStep{{
		EdgeID: "subject", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation",
		Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	identity := PopulationRouteChoiceIdentity{SnapshotToken: "snapshot", OutputID: "patients", SelectionRevisionID: "selection-1", Route: route}
	choiceID, err := NewPopulationRouteChoiceID(identity)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePopulationRouteChoiceID(choiceID)
	if err != nil || decoded.SnapshotToken != identity.SnapshotToken || decoded.OutputID != identity.OutputID || decoded.SelectionRevisionID != identity.SelectionRevisionID || len(decoded.Route) != 1 || decoded.Route[0].EdgeID != "subject" {
		t.Fatalf("decoded route choice = %#v, %v", decoded, err)
	}
	parts := strings.Split(choiceID, ".")
	parts[2] = "0" + parts[2][1:]
	if _, err := DecodePopulationRouteChoiceID(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered population route token was accepted")
	}
}
