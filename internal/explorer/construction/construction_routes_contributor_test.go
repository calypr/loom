package construction

import (
	"context"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestReauthorizeRelatedExpandContributorRejectsTamperedRepeatedBoundaries(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "construction-route-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}}
	snapshot.Edges = []capability.Edge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND"}}
	status := capability.Candidate{ID: "observation-status", NodeID: "observation", ResourceType: "Observation", FieldPath: "status", Label: "Observation status", LogicalType: "code", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}}
	repeated := capability.Candidate{
		ID: "observation-category-code", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "category[].coding[].code", Label: "Observation category code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "category[]", MaxItems: 2}, {Path: "category[].coding[]", MaxItems: 4}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, SuggestedValues: []string{"d", "t"}, SuggestionsComplete: true,
	}
	snapshot.Candidates = []capability.Candidate{status, repeated}
	route := []capability.ConstructionRouteStep{{EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}}
	rootChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, status)
	if err != nil {
		t.Fatal(err)
	}
	contributorChoice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, route, repeated)
	if err != nil {
		t.Fatal(err)
	}
	contributorValue := "d"
	related := ws.ConstructionRelatedExpand{
		ChoiceID: rootChoice.ChoiceID, TargetNodeID: "observation", TargetResourceType: "Observation", Route: route,
		ContributorChoiceID: contributorChoice.ChoiceID,
		ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches, Predicate: &ws.ContributorPredicate{
			CandidateID: repeated.ID, Operator: ws.ContributorEquals, Quantifier: ws.ContributorAny,
			Value: &ws.ContributorValue{Kind: ws.ContributorString, String: &contributorValue},
		}},
	}
	related.ContributorSource = &ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: repeated.ID, NodeID: repeated.NodeID,
		ResourceType: repeated.ResourceType, Path: repeated.FieldPath, Cardinality: repeated.Cardinality,
		LogicalType: repeated.LogicalType, RepeatedBoundaries: append([]capability.RepeatedBoundary(nil), repeated.RepeatedBoundaries...),
	}
	catalog := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients",
		SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		Nodes: []ws.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation"}},
		Edges: []ws.CatalogEdge{{ID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"}},
		Candidates: []ws.CatalogCandidate{
			{ID: status.ID, NodeID: status.NodeID, FieldPath: status.FieldPath, Label: status.Label, LogicalType: status.LogicalType, Cardinality: status.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &rootChoice},
			{ID: repeated.ID, NodeID: repeated.NodeID, FieldPath: repeated.FieldPath, Label: repeated.Label, LogicalType: repeated.LogicalType, Cardinality: repeated.Cardinality, ProjectionModes: []string{"ARRAY"}, DefaultProjectionMode: "ARRAY", ConstructionChoice: &contributorChoice},
		},
	}
	base := Base{Snapshot: snapshot, Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, Catalog: catalog, Document: ws.Document{RootResourceType: "Patient", Rows: ws.RecordsRowDefinition()}}
	if err := reauthorizeRelatedExpandContributor(context.Background(), base, "Patient", related); err != nil {
		t.Fatalf("repeated contributor signed choice reauthorization: %v", err)
	}
	tampered := *related.ContributorSource
	tampered.RepeatedBoundaries = append([]capability.RepeatedBoundary(nil), related.ContributorSource.RepeatedBoundaries...)
	tampered.RepeatedBoundaries[1].MaxItems++
	related.ContributorSource = &tampered
	if err := reauthorizeRelatedExpandContributor(context.Background(), base, "Patient", related); err == nil || lifecycleErrorCode(err) != "INVALID_CONSTRUCTION_CHOICE" {
		t.Fatalf("tampered repeated boundaries were not rejected as an invalid signed choice: %v", err)
	}
}
