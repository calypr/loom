package authoringv2

import (
	"strings"
	"testing"
)

func TestNetworkLegacyRouteUsesExactCatalogIdentity(t *testing.T) {
	d := Document{Output: Output{ID: "subjects"}, Route: RouteNode{OccurrenceID: "base", ResourceType: "ResearchSubject", Children: []RouteNode{{OccurrenceID: "patient", ResourceType: "Patient", Relationship: "subject"}}}, Rows: RecordsRowDefinition()}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "subject", ResourceType: "ResearchSubject", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}, Edges: []CatalogEdge{{ID: "subject-out", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}}
	n, err := NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if n.Occurrences[1].CatalogEdgeID != "subject-out" || n.Occurrences[1].StorageDirection != "OUTBOUND" {
		t.Fatalf("legacy route lost exact catalog identity: %+v", n.Occurrences[1])
	}
	if d.Route.Children[0].CatalogEdgeID != "" {
		t.Fatal("read adapter mutated saved legacy route")
	}
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "subject-in", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "INBOUND"})
	if _, err := NetworkForDocument(d, catalog); err == nil || !strings.Contains(err.Error(), "multiple catalog edges") {
		t.Fatalf("ambiguous legacy route was guessed: %v", err)
	}
	d.Route.Children[0].CatalogEdgeID = "subject-in"
	n, err = NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if n.Occurrences[1].CatalogEdgeID != "subject-in" || n.Occurrences[1].StorageDirection != "INBOUND" {
		t.Fatal("explicit parallel edge was retargeted")
	}
}
