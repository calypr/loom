package ir

import (
	"strings"
	"testing"
)

func populationRootPlan() PhysicalPlan {
	right := PhysicalValue{Variable: "member", Path: []string{"id"}}
	return PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection": "Specimen",
			"members":         "loom_explorer_selection_members",
			"source":          "Specimen",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{
				Variable: "root", CollectionBindKey: "root_collection",
				Population: &PhysicalPopulationRootSource{
					MemberScan: PhysicalCollectionScan{Variable: "member", CollectionBindKey: "members"},
					ResourceOperations: []PhysicalOperation{
						{Kind: PhysicalCollectionScanOp, CollectionScan: &PhysicalCollectionScan{Variable: "source", CollectionBindKey: "source"}},
						{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: "source", Path: []string{"id"}}, Right: &right}}},
					},
					RootKey:  PhysicalValue{Variable: "source", Path: []string{"_key"}},
					MemberID: PhysicalValue{Variable: "member", Path: []string{"id"}},
				},
			}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}}},
		},
	}
}

func TestPhysicalPopulationRootSourceValidatesAndClonesDeeply(t *testing.T) {
	plan := populationRootPlan()
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := ClonePhysicalPlan(plan)
	clone.Operations[0].RootScan.Population.ResourceOperations[0].CollectionScan.Variable = "changed"
	if got := plan.Operations[0].RootScan.Population.ResourceOperations[0].CollectionScan.Variable; got != "source" {
		t.Fatalf("population root clone mutated source plan: %q", got)
	}
}

func TestPhysicalPopulationRootSourceRejectsMissingResourceScan(t *testing.T) {
	plan := populationRootPlan()
	plan.Operations[0].RootScan.Population.ResourceOperations = nil
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "must begin with COLLECTION_SCAN") {
		t.Fatalf("Validate() = %v, want missing resource scan error", err)
	}
}
