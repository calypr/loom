package authoringv2

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestMigrateV1DropsRowAndExecutionFields(t *testing.T) {
	visible := true
	order := 2
	input := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "out", Title: "Output"},
		BaseNodeID: "patient", RowNodeID: "legacy-row",
		RouteEdgeIDs:         []string{"patient-encounter"},
		RouteOccurrences:     []explorer.ExplorerRouteOccurrenceV1{{ID: "encounter-occurrence", NodeID: "encounter", IncomingEdgeID: "patient-encounter"}},
		CandidateIDs:         []string{"encounter-id"},
		CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: "encounter-id", OccurrenceID: "encounter-occurrence"}},
		Presentation:         map[string]explorer.ExplorerPresentationBindingV1{"encounter-id": {Label: "Encounter", Visible: &visible, Order: &order}},
	}
	migrated, err := MigrateV1Document(input)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.RootNodeID != "patient" || len(migrated.RouteSteps) != 1 || migrated.RouteSteps[0].OccurrenceID != "encounter-occurrence" {
		t.Fatalf("unexpected migrated route: %#v", migrated)
	}
	if migrated.Selections[0].ProjectionMode != "SCALAR" || migrated.Selections[0].CandidateID != "encounter-id" {
		t.Fatalf("unexpected migrated selection: %#v", migrated.Selections)
	}
	if migrated.Output.ID != "out" || migrated.Output.Title != "Output" {
		t.Fatalf("output was not migrated: %#v", migrated.Output)
	}
	raw, err := migrated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"rowNodeId", "legacy-row", "selector", "aql", "physicalCollection"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forbidden V1/backend field leaked: %q", forbidden)
		}
	}
}
