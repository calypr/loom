package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestSelectedRowCohortCannotBorrowDefaultGenerationWitnesses(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Policy.Route.AllowsRepeatedEdges = true
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{{
		Output: authoringv2.Output{ID: "selected"}, RootResourceType: "Patient",
		Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
			OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "name[]", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
		}},
	}}}
	service := &Service{config: Config{AvailableColumns: func(context.Context, catalog.AvailabilityOptions) (catalog.AvailabilityResult, error) {
		t.Fatal("selected rows queried the unrestricted default witnesses")
		return catalog.AvailabilityResult{}, nil
	}}}
	_, _, err := service.availableColumnRoutes(context.Background(), AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, workspace, "selected")
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "AVAILABILITY_COHORT_UNAVAILABLE" {
		t.Fatalf("selected cohort error = %v, want AVAILABILITY_COHORT_UNAVAILABLE", err)
	}
}

func TestLiveCatalogWaitsForPreparationAndOnlyOffersPopulatedRoutes(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Policy.Route.AllowsRepeatedEdges = true
	snapshot.Policy.Route.AllowsSelfLoops = true
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Edges = []capability.Edge{{ID: "subject", FromNodeID: "patient", ToNodeID: "observation", SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND", ObservedEdgeCount: 100}}
	snapshot.Candidates = []capability.Candidate{
		{ID: "present", NodeID: "observation", ResourceType: "Observation", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		{ID: "disconnected-value", NodeID: "observation", ResourceType: "Observation", FieldPath: "status", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
	}
	state := catalog.SemanticInventoryRunning
	service := &Service{store: featureCatalogTestStore(t, "Patient"), config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		AvailableColumns: func(_ context.Context, opts catalog.AvailabilityOptions) (catalog.AvailabilityResult, error) {
			if !opts.Query.AllRoots || opts.Query.RootResourceType != "Patient" || !opts.Query.Unrestricted || len(opts.Query.Relations) != 1 {
				t.Fatalf("availability query lost root/scope/relations: %+v", opts)
			}
			return catalog.AvailabilityResult{State: state, Witnesses: []catalog.AvailabilityWitness{{
				Feature: catalog.AvailabilityFeature{Kind: "FIELD", ResourceType: "Observation", FieldPath: "id"},
				RootID:  "Patient/p1", SourceID: "Observation/o1", Route: opts.Query.Relations,
			}}}, nil
		},
	}}
	request := BrowseFeatureCatalogRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", OutputID: "test_output", Section: FeatureCatalogFields, Limit: 1}
	preparing, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil || preparing.State != catalog.SemanticInventoryRunning || len(preparing.Entries) != 0 || preparing.NextCursor != "" {
		t.Fatalf("preparing = %+v, %v", preparing, err)
	}
	state = catalog.SemanticInventoryComplete
	ready, err := service.BrowseFeatureCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Entries) != 1 || ready.Entries[0].Source.CandidateID != "present" || ready.NextCursor != "" {
		t.Fatalf("catalog advertised a source without a witness: %+v", ready)
	}
	item := ready.Entries[0]
	if item.Coverage.State != "VERIFIED" || item.Coverage.RowsWithValue != nil || item.ConstructionChoice == nil || len(item.ConstructionChoice.Route) != 1 || item.ConstructionChoice.Route[0].EdgeID != "subject" {
		t.Fatalf("live item lost its exact route or invented a row count: %+v", item)
	}
}
