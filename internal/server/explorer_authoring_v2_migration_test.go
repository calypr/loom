package server

import (
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestMigrateV1WorkspaceMapsLegacyCandidateOccurrencePresentation(t *testing.T) {
	visible := true
	order := 0
	legacyCandidateID := explorer.OpaqueID("s_", "Patient\x00id")
	snapshot := capability.Snapshot{
		Identity: capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a"},
		Policy:   capability.Policy{Route: capability.RoutePolicy{AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		Status:   capability.StatusReady, Complete: true, Token: "snapshot-a",
		Nodes: []capability.Node{{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", Populated: true, DocumentCount: 3}},
		Candidates: []capability.Candidate{{
			ID: "patient-id", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "id", Label: "Identifier", LogicalType: "string", Cardinality: "scalar",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, FilterOperators: []capability.FilterOperator{capability.FilterEquals}, ChartAggregations: []capability.ChartAggregation{capability.ChartCount}, Populated: true,
		}},
	}
	wire := authoringV2Catalog(snapshot, "default")
	bundle := explorer.ExplorerAuthoringBundleV1{
		APIVersion: explorer.ExplorerAuthoringV1APIVersion, Kind: explorer.ExplorerAuthoringV1Kind,
		Project: "project-a", ExplorerID: "default", Title: "Default",
		Documents: []explorer.ExplorerBuilderDocumentV1{{
			Kind:       explorer.ExplorerBuilderV1Kind,
			Output:     explorer.ExplorerOutputIdentityV1{ID: "patients", Title: "Patients"},
			BaseNodeID: explorer.OpaqueID("n_", "Patient"), RowNodeID: explorer.OpaqueID("n_", "Patient"),
			CandidateIDs:         []string{legacyCandidateID},
			CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{{CandidateID: legacyCandidateID, OccurrenceID: "base", ProjectionMode: "SCALAR"}},
			Presentation: map[string]explorer.ExplorerPresentationBindingV1{
				legacyCandidateID + "\x00base": {
					Label: "Patient identifier", Visible: &visible, Order: &order,
					Table:  &explorer.ExplorerTableBindingV1{Pinned: true},
					Filter: &explorer.ExplorerFilterBindingV1{Label: "Patient ID"},
					Chart:  &explorer.ExplorerChartBindingV1{Type: "bar", Title: "Patients"},
				},
			},
		}},
		Tabs: []explorer.ExplorerTabV1{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: &visible}},
	}

	workspace, err := migrateV1WorkspaceToCapability(bundle, snapshot, wire, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents) != 1 || len(workspace.Tabs) != 1 {
		t.Fatalf("workspace = %#v", workspace)
	}
	document := workspace.Documents[0]
	if len(document.Selections) != 1 || document.Selections[0].CandidateID != "patient-id" || document.Selections[0].ProjectionMode != "VALUE" {
		t.Fatalf("selections = %#v", document.Selections)
	}
	key := authoringv2.PresentationKey("patient-id", "base", "VALUE")
	presentation, ok := document.Presentation[key]
	if !ok || presentation.Label != "Patient identifier" || presentation.Table == nil || !presentation.Table.Pinned || presentation.Filter == nil || presentation.Filter.Label != "Patient ID" || presentation.Chart == nil || presentation.Chart.Type != "bar" || presentation.Chart.Title != "Patients" {
		t.Fatalf("presentation = %#v", document.Presentation)
	}
}
