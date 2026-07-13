package dataframe_test

import (
	"strings"
	"testing"

	dataframe "github.com/calypr/loom/internal/dataframe"
)

func TestRootSiblingEndpointStrategyIsTypedAndAblatable(t *testing.T) {
	endpoint := compileActualGDCWithRules(t, true, true)
	if !strings.Contains(endpoint.Query, "FOR child_set_1_edge IN @@child_set_1_edge_collection") {
		t.Fatalf("endpoint policy did not lower the root sibling group:\n%s", endpoint.Query)
	}
	for _, want := range []string{
		"FILTER child_set_1_edge._to == root._id",
		"FILTER POSITION(@shared_root_subject_Patient_neighbors_target_types, child_set_1_edge.from_type)",
		"LET child_set_1_node = DOCUMENT(child_set_1_edge._from)",
		"POSITION(@shared_root_subject_Patient_neighbors_target_types, child_set_1_node.resourceType)",
	} {
		if !strings.Contains(endpoint.Query, want) {
			t.Fatalf("root endpoint policy omitted %q:\n%s", want, endpoint.Query)
		}
	}

	native := compileActualGDCWithRules(t, false, true)
	if !strings.Contains(native.Query, "IN 1..1 INBOUND root @@child_set_1_edge_collection") {
		t.Fatalf("endpoint policy off did not retain native root sibling traversal:\n%s", native.Query)
	}
	if strings.Contains(native.Query, "FOR child_set_1_edge IN @@child_set_1_edge_collection") {
		t.Fatalf("endpoint policy off still rendered root endpoint lookup:\n%s", native.Query)
	}
}

func TestRootSiblingEndpointStrategyUsesGenericRouteMetadata(t *testing.T) {
	// The production strategy must not be selected by a resource-specific
	// branch. This generic two-sibling plan exercises the same generated route
	// contract used by the GDC request without naming a FHIR type in production.
	policy := dataframe.DefaultPhysicalOptimizationPolicy()
	semantic, err := dataframe.BuildSemanticPlan(dataframe.Builder{
		Project:          "ARANGODB_PROTO",
		RootResourceType: "ResearchSubject",
		Traversals:       []dataframe.TraversalStep{{Label: "study", ToResourceType: "ResearchStudy", Alias: "study"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	physical, err := dataframe.BuildGenericPhysicalPlanWithPolicy(semantic, policy)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range physical.Operations {
		if operation.Traversal == nil {
			continue
		}
		found = true
		if operation.Traversal.Strategy != dataframe.PhysicalTraversalEndpointLookup {
			t.Fatalf("proven outbound route strategy = %q", operation.Traversal.Strategy)
		}
		if operation.Traversal.EndpointField != "_from" || operation.Traversal.EndpointJoinField != "_to" {
			t.Fatalf("outbound endpoint fields = %q/%q", operation.Traversal.EndpointField, operation.Traversal.EndpointJoinField)
		}
	}
	if !found {
		t.Fatal("generic outbound route produced no traversal")
	}
}
