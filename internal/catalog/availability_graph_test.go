package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func mustAddVertex(t *testing.T, b *AvailabilityGraphBuilder, vertex AvailabilityVertex, fields ...string) {
	t.Helper()
	if err := b.AddVertex(vertex, fields); err != nil {
		t.Fatalf("AddVertex(%q): %v", vertex.ID, err)
	}
}

func mustAddEdge(t *testing.T, b *AvailabilityGraphBuilder, from, to, relationship, authPath string) {
	t.Helper()
	if err := b.AddEdge(from, to, relationship, authPath); err != nil {
		t.Fatalf("AddEdge(%q, %q): %v", from, to, err)
	}
}

func mustFinish(t *testing.T, b *AvailabilityGraphBuilder) *AvailabilityGraph {
	t.Helper()
	graph, err := b.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish(): %v", err)
	}
	return graph
}

func mustFind(t *testing.T, graph *AvailabilityGraph, query AvailabilityQuery) []AvailabilityWitness {
	t.Helper()
	witnesses, err := graph.Find(context.Background(), query)
	if err != nil {
		t.Fatalf("Find(): %v", err)
	}
	return witnesses
}

func outbound(from, to, relationship string) AvailabilityRelation {
	return AvailabilityRelation{
		FromResourceType: from,
		ToResourceType:   to,
		Relationship:     relationship,
		StorageDirection: "OUTBOUND",
	}
}

func TestFindUnionsRootsAndKeepsShortestWitness(t *testing.T) {
	b := NewAvailabilityGraphBuilder(5, 3)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root-a", ResourceType: "Root", AuthResourcePath: "scope"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "root-z", ResourceType: "Root", AuthResourcePath: "scope"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "middle", ResourceType: "Bridge", AuthResourcePath: "scope"}, "bridge.value")
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source", AuthResourcePath: "scope"}, "source.code")
	mustAddVertex(t, b, AvailabilityVertex{ID: "orphan", ResourceType: "Bridge", AuthResourcePath: "scope"}, "orphan.value")
	mustAddEdge(t, b, "root-a", "middle", "has", "scope")
	mustAddEdge(t, b, "middle", "source", "has", "scope")
	mustAddEdge(t, b, "root-z", "source", "direct", "scope")
	graph := mustFinish(t, b)

	witnesses := mustFind(t, graph, AvailabilityQuery{
		RootResourceType: "Root",
		RootIDs:          []string{"root-z", "root-a"},
		Unrestricted:     true,
		Relations: []AvailabilityRelation{
			outbound("Root", "Bridge", "has"),
			outbound("Bridge", "Source", "has"),
			outbound("Root", "Source", "direct"),
		},
	})
	if len(witnesses) != 2 {
		t.Fatalf("got %d witnesses, want 2: %#v", len(witnesses), witnesses)
	}
	byPath := make(map[string]AvailabilityWitness, len(witnesses))
	for _, witness := range witnesses {
		byPath[witness.Feature.FieldPath] = witness
	}
	if got := byPath["bridge.value"]; got.RootID != "root-a" || got.SourceID != "middle" || len(got.Route) != 1 {
		t.Fatalf("middle witness = %#v, want root-a via one hop", got)
	}
	if got := byPath["source.code"]; got.RootID != "root-z" || got.SourceID != "source" || len(got.Route) != 1 {
		t.Fatalf("source witness = %#v, want shortest root-z route", got)
	}
}

func TestFindUsesDeterministicTieAndTerminatesCycles(t *testing.T) {
	build := func(reverse bool) *AvailabilityGraph {
		b := NewAvailabilityGraphBuilder(4, 4)
		vertices := []AvailabilityVertex{
			{ID: "root-b", ResourceType: "Root"},
			{ID: "root-a", ResourceType: "Root"},
			{ID: "via-b", ResourceType: "Bridge"},
			{ID: "via-a", ResourceType: "Bridge"},
		}
		edges := [][2]string{{"root-b", "via-b"}, {"via-b", "root-b"}, {"root-a", "via-a"}, {"via-a", "root-a"}}
		if reverse {
			for left, right := 0, len(vertices)-1; left < right; left, right = left+1, right-1 {
				vertices[left], vertices[right] = vertices[right], vertices[left]
			}
			for left, right := 0, len(edges)-1; left < right; left, right = left+1, right-1 {
				edges[left], edges[right] = edges[right], edges[left]
			}
		}
		for _, vertex := range vertices {
			fields := []string(nil)
			if vertex.ID == "via-a" || vertex.ID == "via-b" {
				fields = []string{"value"}
				vertex.ResourceType = "Bridge"
			}
			if vertex.ID == "root-a" || vertex.ID == "root-b" {
				vertex.ResourceType = "Root"
			}
			mustAddVertex(t, b, vertex, fields...)
		}
		for _, edge := range edges {
			mustAddEdge(t, b, edge[0], edge[1], "cycle", "")
		}
		return mustFinish(t, b)
	}

	query := AvailabilityQuery{
		RootResourceType: "Root",
		AllRoots:         true,
		Unrestricted:     true,
		Relations: []AvailabilityRelation{
			outbound("Root", "Bridge", "cycle"),
			outbound("Bridge", "Root", "cycle"),
			{FromResourceType: "Root", ToResourceType: "Bridge", Relationship: "cycle", StorageDirection: "INBOUND"},
			{FromResourceType: "Bridge", ToResourceType: "Root", Relationship: "cycle", StorageDirection: "INBOUND"},
		},
	}
	first := mustFind(t, build(false), query)
	second := mustFind(t, build(true), query)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent graph insertions produced different witnesses:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if len(first) != 1 || first[0].RootID != "root-a" || first[0].SourceID != "via-a" || len(first[0].Route) != 1 {
		t.Fatalf("tie witness = %#v, want stable shortest root-a route", first)
	}
}

func TestFindChoosesShortestWitnessInGraphWithCycles(t *testing.T) {
	b := NewAvailabilityGraphBuilder(4, 5)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "a", ResourceType: "Node"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "b", ResourceType: "Node"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source"}, "value")
	mustAddEdge(t, b, "root", "a", "starts", "")
	mustAddEdge(t, b, "a", "b", "loops", "")
	mustAddEdge(t, b, "b", "a", "loops", "")
	mustAddEdge(t, b, "b", "source", "finishes", "")
	mustAddEdge(t, b, "root", "source", "direct", "")
	graph := mustFinish(t, b)
	witnesses := mustFind(t, graph, AvailabilityQuery{
		RootResourceType: "Root",
		RootIDs:          []string{"root"},
		Unrestricted:     true,
		Relations: []AvailabilityRelation{
			outbound("Root", "Node", "starts"),
			outbound("Node", "Node", "loops"),
			outbound("Node", "Source", "finishes"),
			outbound("Root", "Source", "direct"),
		},
	})
	if len(witnesses) != 1 || len(witnesses[0].Route) != 1 || witnesses[0].Route[0].Relationship != "direct" {
		t.Fatalf("witness = %#v, want one-hop direct path despite a cyclic longer path", witnesses)
	}
}

func TestFindChecksDeniedEdgeBeforeVisitedTarget(t *testing.T) {
	b := NewAvailabilityGraphBuilder(2, 2)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root", AuthResourcePath: "allowed"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source", AuthResourcePath: "allowed"}, "value")
	// The denied copy sorts before the allowed copy. It must not mark source
	// visited or hide the authorized physical edge.
	mustAddEdge(t, b, "root", "source", "has", "a-denied")
	mustAddEdge(t, b, "root", "source", "has", "z-allowed")
	graph := mustFinish(t, b)
	witnesses := mustFind(t, graph, AvailabilityQuery{
		RootResourceType:  "Root",
		RootIDs:           []string{"root"},
		AuthResourcePaths: []string{"allowed", "z-allowed"},
		Relations:         []AvailabilityRelation{outbound("Root", "Source", "has")},
	})
	if len(witnesses) != 1 || witnesses[0].SourceID != "source" {
		t.Fatalf("Find() = %#v, want the authorized alternate edge", witnesses)
	}
}

func TestFindDoesNotTraverseDeniedIntermediateOrRestrictedEmpty(t *testing.T) {
	b := NewAvailabilityGraphBuilder(3, 2)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root", AuthResourcePath: "scope"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "middle", ResourceType: "Bridge", AuthResourcePath: "denied"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source", AuthResourcePath: "scope"}, "value")
	mustAddEdge(t, b, "root", "middle", "next", "scope")
	mustAddEdge(t, b, "middle", "source", "next", "scope")
	graph := mustFinish(t, b)
	query := AvailabilityQuery{
		RootResourceType:  "Root",
		AllRoots:          true,
		AuthResourcePaths: []string{"scope"},
		Relations: []AvailabilityRelation{
			outbound("Root", "Bridge", "next"),
			outbound("Bridge", "Source", "next"),
		},
	}
	if got := mustFind(t, graph, query); len(got) != 0 {
		t.Fatalf("denied intermediate leaked a feature: %#v", got)
	}
	query.AuthResourcePaths = nil
	if got := mustFind(t, graph, query); len(got) != 0 {
		t.Fatalf("restricted-empty query returned features: %#v", got)
	}
}

func TestFindSupportsLongRoutesAndExplicitHopLimit(t *testing.T) {
	b := NewAvailabilityGraphBuilder(12, 11)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"})
	previous := "root"
	for hop := 1; hop <= 11; hop++ {
		id := "node-"
		if hop < 10 {
			id += "0"
		}
		id += string(rune('0' + hop))
		typeName := "Node"
		if hop == 11 {
			typeName = "Source"
			mustAddVertex(t, b, AvailabilityVertex{ID: id, ResourceType: typeName}, "deep.value")
		} else {
			mustAddVertex(t, b, AvailabilityVertex{ID: id, ResourceType: typeName})
		}
		mustAddEdge(t, b, previous, id, "next", "")
		previous = id
	}
	graph := mustFinish(t, b)
	query := AvailabilityQuery{
		RootResourceType: "Root",
		RootIDs:          []string{"root"},
		Unrestricted:     true,
		Relations: []AvailabilityRelation{
			outbound("Root", "Node", "next"),
			outbound("Node", "Node", "next"),
			outbound("Node", "Source", "next"),
		},
	}
	if got := mustFind(t, graph, query); len(got) != 1 || len(got[0].Route) != 11 {
		t.Fatalf("unlimited route = %#v, want one witness at 11 hops", got)
	}
	query.MaxHops = 10
	if got := mustFind(t, graph, query); len(got) != 0 {
		t.Fatalf("10-hop limit admitted 11-hop witness: %#v", got)
	}
	query.MaxHops = 11
	if got := mustFind(t, graph, query); len(got) != 1 || len(got[0].Route) != 11 {
		t.Fatalf("11-hop limit rejected witness: %#v", got)
	}
}

func TestBuilderDeduplicatesSourceAssociationsAndReportsStats(t *testing.T) {
	b := NewAvailabilityGraphBuilder(1, 0)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"}, "value", "value")
	field := AvailabilityFeature{Kind: "FIELD", ResourceType: "Root", FieldPath: "value"}
	semantic := AvailabilityFeature{Kind: "SEMANTIC", ResourceType: "Root", ConceptID: "concept", BindingID: "binding"}
	if err := b.AddFeature("root", field); err != nil {
		t.Fatal(err)
	}
	if err := b.AddFeature("root", semantic); err != nil {
		t.Fatal(err)
	}
	if err := b.AddFeature("root", semantic); err != nil {
		t.Fatal(err)
	}
	graph := mustFinish(t, b)
	stats := graph.Stats()
	if stats.Vertices != 1 || stats.Edges != 0 || stats.Features != 2 || stats.Associations != 2 || stats.Bytes == 0 {
		t.Fatalf("Stats() = %+v, want one vertex, two features/associations, and retained bytes", stats)
	}
	witnesses := mustFind(t, graph, AvailabilityQuery{RootResourceType: "Root", AllRoots: true, Unrestricted: true})
	if len(witnesses) != 2 || len(witnesses[0].Route) != 0 || len(witnesses[1].Route) != 0 {
		t.Fatalf("zero-hop features = %#v, want both direct associations", witnesses)
	}
	if got := mustFind(t, graph, AvailabilityQuery{RootResourceType: "Missing", AllRoots: true, Unrestricted: true}); len(got) != 0 {
		t.Fatalf("empty row cohort returned witnesses: %#v", got)
	}
}

func TestBuilderRejectsInvalidRecordsAndStaleRoots(t *testing.T) {
	b := NewAvailabilityGraphBuilder(1, 0)
	if b.HasVertex("root") {
		t.Fatal("HasVertex returned true before a vertex was added")
	}
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"})
	if !b.HasVertex("root") || b.HasVertex("missing") {
		t.Fatal("HasVertex did not reflect the loaded vertex IDs")
	}
	if err := b.AddVertex(AvailabilityVertex{ID: "root", ResourceType: "Root"}, nil); err == nil {
		t.Fatal("duplicate vertex ID was accepted")
	}
	if err := b.AddFeature("missing", AvailabilityFeature{ResourceType: "Root"}); err == nil {
		t.Fatal("feature with unresolved source vertex was accepted")
	}
	if err := b.AddFeature("root", AvailabilityFeature{ResourceType: "Other"}); err == nil {
		t.Fatal("feature resource type mismatch was accepted")
	}
	mustAddEdge(t, b, "root", "missing", "has", "")
	if _, err := b.Finish(context.Background()); err == nil {
		t.Fatal("edge with unresolved endpoint was accepted")
	}
	if err := b.AddVertex(AvailabilityVertex{ID: "missing", ResourceType: "Target"}, []string{"value"}); err != nil {
		t.Fatalf("builder should remain retryable after failed Finish: %v", err)
	}
	graph := mustFinish(t, b)
	_, err := graph.Find(context.Background(), AvailabilityQuery{RootResourceType: "Root", RootIDs: []string{"stale"}, Unrestricted: true})
	if err == nil {
		t.Fatal("unknown explicit root ID was silently dropped")
	}
}

func TestFindCancellationReturnsErrorWithoutPartialResult(t *testing.T) {
	b := NewAvailabilityGraphBuilder(1, 0)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"}, "value")
	graph := mustFinish(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	witnesses, err := graph.Find(ctx, AvailabilityQuery{RootResourceType: "Root", AllRoots: true, Unrestricted: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Find() error = %v, want context.Canceled", err)
	}
	if witnesses != nil {
		t.Fatalf("canceled Find() returned partial results: %#v", witnesses)
	}
}

func TestFindPreservesInboundDirectionAndRequiresAllowedRelation(t *testing.T) {
	b := NewAvailabilityGraphBuilder(2, 1)
	mustAddVertex(t, b, AvailabilityVertex{ID: "root", ResourceType: "Root"})
	mustAddVertex(t, b, AvailabilityVertex{ID: "source", ResourceType: "Source"}, "value")
	mustAddEdge(t, b, "source", "root", "owner", "")
	graph := mustFinish(t, b)
	query := AvailabilityQuery{RootResourceType: "Root", RootIDs: []string{"root"}, Unrestricted: true}
	if got := mustFind(t, graph, query); len(got) != 0 {
		t.Fatalf("empty relation policy traversed an edge: %#v", got)
	}
	query.Relations = []AvailabilityRelation{{
		FromResourceType: "Root", ToResourceType: "Source", Relationship: "owner", StorageDirection: "INBOUND",
	}}
	witnesses := mustFind(t, graph, query)
	if len(witnesses) != 1 || len(witnesses[0].Route) != 1 || witnesses[0].Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("inbound witness = %#v, want preserved INBOUND step", witnesses)
	}
}

func TestRelationshipTraversalCandidatesExposeBothStoredDirections(t *testing.T) {
	observed := RelationshipObservation{
		StorageFromType: "Observation", StorageToType: "Patient", Label: "subject_Patient",
	}
	got := RelationshipTraversalCandidates(observed)
	want := [2]AvailabilityRelation{
		{FromResourceType: "Observation", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND"},
		{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"},
	}
	if got != want {
		t.Fatalf("candidate routes = %#v, want %#v", got, want)
	}
}
