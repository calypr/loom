package construction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestConstructionNodeOccurrencesKeepsRootAndOmitsAmbiguousNodes(t *testing.T) {
	route := ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{
		{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters", CatalogEdgeID: "edge-p-e", Children: []ws.RouteNode{
			{OccurrenceID: "patient_again", ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "edge-e-p"},
			{OccurrenceID: "specimen_a", ResourceType: "Specimen", Relationship: "specimens", CatalogEdgeID: "edge-e-s"},
		}},
		{OccurrenceID: "condition", ResourceType: "Condition", Relationship: "conditions", CatalogEdgeID: "edge-p-c", Children: []ws.RouteNode{
			{OccurrenceID: "specimen_b", ResourceType: "Specimen", Relationship: "specimens", CatalogEdgeID: "edge-c-s"},
		}},
		{OccurrenceID: "observation", ResourceType: "Observation", Relationship: "observations", CatalogEdgeID: "edge-p-o", Children: []ws.RouteNode{
			{OccurrenceID: "specimen_c", ResourceType: "Specimen", Relationship: "specimens", CatalogEdgeID: "edge-o-s"},
		}},
	}}
	doc := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: route, Rows: ws.RecordsRowDefinition(),
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("authored route is invalid: %v", err)
	}
	nodes := []ws.CatalogNode{
		{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "n_encounter", ResourceType: "Encounter"},
		{ID: "n_condition", ResourceType: "Condition"},
		{ID: "n_observation", ResourceType: "Observation"},
		{ID: "n_specimen", ResourceType: "Specimen"},
	}
	edges := []ws.CatalogEdge{
		{ID: "edge-p-e", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters"},
		{ID: "edge-e-p", FromNodeID: "n_encounter", ToNodeID: "n_patient", Label: "subject_Patient"},
		{ID: "edge-e-s", FromNodeID: "n_encounter", ToNodeID: "n_specimen", Label: "specimens"},
		{ID: "edge-p-c", FromNodeID: "n_patient", ToNodeID: "n_condition", Label: "conditions"},
		{ID: "edge-c-s", FromNodeID: "n_condition", ToNodeID: "n_specimen", Label: "specimens"},
		{ID: "edge-p-o", FromNodeID: "n_patient", ToNodeID: "n_observation", Label: "observations"},
		{ID: "edge-o-s", FromNodeID: "n_observation", ToNodeID: "n_specimen", Label: "specimens"},
	}
	catalog := ws.CatalogSnapshot{APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Nodes: nodes, Edges: edges}
	if _, err := ws.NetworkForDocument(doc, catalog); err != nil {
		t.Fatalf("authored route does not resolve against its catalog: %v", err)
	}
	snapshotNodes := make([]capability.Node, 0, len(nodes))
	for _, node := range nodes {
		snapshotNodes = append(snapshotNodes, capability.Node{ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible})
	}
	snapshotEdges := make([]capability.Edge, 0, len(edges))
	for _, edge := range edges {
		snapshotEdges = append(snapshotEdges, capability.Edge{ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, Label: edge.Label})
	}
	snapshot := capability.Snapshot{Status: capability.StatusReady, Complete: true, Nodes: snapshotNodes, Edges: snapshotEdges}
	sum := sha256.Sum256(snapshot.CanonicalPayload())
	snapshot.Token = "sha256:" + hex.EncodeToString(sum[:])
	if err := capability.ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("capability snapshot is invalid: %v", err)
	}

	got := constructionNodeOccurrences(route, snapshot)
	want := map[string]string{
		"n_patient": "base", "n_encounter": "encounter", "n_condition": "condition", "n_observation": "observation",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node occurrences=%v want=%v", got, want)
	}
	rootCatalogKey := fmt.Sprintf("%s|Patient|id", got["n_patient"])
	if rootCatalogKey != "base|Patient|id" {
		t.Fatalf("root catalog source key=%q want root occurrence key %q", rootCatalogKey, "base|Patient|id")
	}
	if _, found := got["n_specimen"]; found {
		t.Fatalf("ambiguous catalog node was assigned an occurrence after its third appearance: %v", got)
	}
}
