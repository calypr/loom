package ir

import (
	"strings"
	"testing"
)

func rootPageCandidateSeedPlan() PhysicalPlan {
	binds := map[string]any{
		"root_collection":                  "Patient",
		"child_collection":                 "Observation",
		"edges":                            "fhir_edge",
		"edge_label":                       "subject_Patient",
		"child_type":                       "Observation",
		"root_type":                        "Patient",
		"project":                          "project-1",
		"dataset_generation":               "generation-1",
		"auth_resource_paths":              []string{"/programs/p1"},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
		"selected_value":                   "active",
		"loom_root_page_after_key":         "root-key-0",
		"root_limit":                       20,
	}

	child := "candidate_child"
	root := "candidate_root"
	edge := "candidate_edge"
	seedOperations := []PhysicalOperation{
		{Kind: PhysicalCollectionScanOp, Source: PhysicalSource{ResourceType: "Observation"}, CollectionScan: &PhysicalCollectionScan{Variable: child, CollectionBindKey: "child_collection"}},
	}
	seedOperations = append(seedOperations, physicalPageCandidateScope(child, "child_scope")...)
	seedOperations = append(seedOperations, PhysicalOperation{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
		Operator: "EQUALS", Left: PhysicalValue{Variable: child, Path: []string{"payload", "status"}}, Right: &PhysicalValue{BindKey: "selected_value"},
	}}})
	seedOperations = append(seedOperations, PhysicalOperation{Kind: PhysicalTraversalOp, Traversal: &PhysicalTraversal{
		SourceVariable: child, TargetVariable: root, EdgeVariable: edge, Direction: PhysicalOutbound,
		EdgeCollectionBindKey: "edges", EdgeLabelBindKey: "edge_label", SourceTypeBindKey: "child_type", TargetTypeBindKey: "root_type", EdgeTargetTypeField: "to_type",
	}})
	seedOperations = append(seedOperations, physicalPageCandidateTraversalScope(edge, root, "traversal_scope")...)
	seedOperations = append(seedOperations, PhysicalOperation{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
		Operator: "GT", Left: PhysicalValue{Variable: root, Path: []string{"_key"}}, Right: &PhysicalValue{BindKey: "loom_root_page_after_key"},
	}}})
	rootKey := PhysicalValue{Variable: root, Path: []string{"_key"}}
	seed := &PhysicalRootPageCandidateSeed{Subplan: PhysicalSubplan{
		Operations: seedOperations,
		Return:     PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &rootKey},
		Sort:       &rootKey,
		Unique:     true,
	}}

	operations := []PhysicalOperation{{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{
		Variable: "root", CollectionBindKey: "root_collection", PageCandidateSeed: seed,
	}}}
	operations = append(operations, physicalPageCandidateScope("root", "root_scope")...)
	operations = append(operations,
		PhysicalOperation{Kind: PhysicalSortOp, Sort: &PhysicalSort{Keys: []PhysicalValue{{Variable: "root", Path: []string{"_key"}}}}},
		PhysicalOperation{Kind: PhysicalLimitOp, Limit: &PhysicalLimit{BindKey: "root_limit"}},
		PhysicalOperation{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}}},
	)
	return PhysicalPlan{Version: 1, Source: PhysicalSource{ResourceType: "Patient"}, BindVars: binds, Operations: operations}
}

func physicalPageCandidateScope(variable, authVariable string) []PhysicalOperation {
	return []PhysicalOperation{
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: variable, Path: []string{"project"}}, Right: &PhysicalValue{BindKey: "project"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}}, Right: &PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: PhysicalDerivedLetOp, DerivedLet: &PhysicalDerivedLet{Variable: authVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []PhysicalValue{{Variable: variable, Path: []string{"auth_resource_path"}}, {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: authVariable}, Right: &PhysicalValue{BindKey: "scope_allowed"}}}},
	}
}

func physicalPageCandidateTraversalScope(edge, target, authVariable string) []PhysicalOperation {
	return []PhysicalOperation{
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: edge, Path: []string{"project"}}, Right: &PhysicalValue{BindKey: "project"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: target, Path: []string{"project"}}, Right: &PhysicalValue{BindKey: "project"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: edge, Path: []string{"dataset_generation"}}, Right: &PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: target, Path: []string{"dataset_generation"}}, Right: &PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: PhysicalDerivedLetOp, DerivedLet: &PhysicalDerivedLet{Variable: authVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []PhysicalValue{{Variable: edge, Path: []string{"auth_resource_path"}}, {Variable: target, Path: []string{"auth_resource_path"}}, {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}}}},
		{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{Operator: "EQUALS", Left: PhysicalValue{Variable: authVariable}, Right: &PhysicalValue{BindKey: "scope_allowed"}}}},
	}
}

func TestPhysicalRootPageCandidateSeedClonesDeeplyAndValidatesUncorrelatedScope(t *testing.T) {
	plan := rootPageCandidateSeedPlan()
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		t.Fatalf("ValidateGenericPhysicalPlanScope() error = %v", err)
	}

	clone := ClonePhysicalPlan(plan)
	seed := &clone.Operations[0].RootScan.PageCandidateSeed.Subplan
	seed.Operations[0].CollectionScan.Variable = "changed_child"
	seed.Operations[5].Filter.Predicate.Right.BindKey = "changed_value"
	seed.Sort.Path[0] = "changed_key"
	if got := plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations[0].CollectionScan.Variable; got != "candidate_child" {
		t.Fatalf("clone mutated original collection scan variable: %q", got)
	}
	if got := plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations[5].Filter.Predicate.Right.BindKey; got != "selected_value" {
		t.Fatalf("clone mutated original filter bind: %q", got)
	}
	if got := plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Sort.Path[0]; got != "_key" {
		t.Fatalf("clone mutated original sort path: %q", got)
	}
}

func TestPhysicalRootPageCandidateSeedUsesSharedSubplanValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalPlan)
		want   string
	}{
		{"invalid traversal", func(plan *PhysicalPlan) {
			plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations[6].Traversal.Direction = "SIDEWAYS"
		}, "invalid traversal direction"},
		{"invalid resource collection", func(plan *PhysicalPlan) { plan.BindVars["child_collection"] = 17 }, "must have a non-empty string value"},
		{"invalid sort scope", func(plan *PhysicalPlan) {
			plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Sort = &PhysicalValue{Variable: "missing", Path: []string{"_key"}}
		}, "out of scope"},
		{"limit is forbidden", func(plan *PhysicalPlan) {
			plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations = append(plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations, PhysicalOperation{Kind: PhysicalLimitOp, Limit: &PhysicalLimit{BindKey: "root_limit"}})
		}, "cannot be LIMIT"},
		{"captures are forbidden", func(plan *PhysicalPlan) {
			plan.Operations[0].RootScan.PageCandidateSeed.Subplan.Captures = []string{"root"}
		}, "cannot declare captures"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := rootPageCandidateSeedPlan()
			test.mutate(&plan)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestPhysicalRootPageCandidateSeedScopeIsCheckedAndMembershipSourcesStaySeparate(t *testing.T) {
	seeded := rootPageCandidateSeedPlan()
	seeded.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations = append(
		seeded.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations[:1],
		seeded.Operations[0].RootScan.PageCandidateSeed.Subplan.Operations[2:]...,
	)
	if err := ValidateGenericPhysicalPlanScope(seeded); err == nil || !strings.Contains(err.Error(), "AUTH_RESOURCE_PATH_ALLOWED LET") {
		t.Fatalf("ValidateGenericPhysicalPlanScope() error = %v, want candidate child scope error", err)
	}

	population := populationRootPlan()
	if err := population.Validate(); err != nil {
		t.Fatalf("Population fallback Validate() error = %v", err)
	}
	population.Operations[0].RootScan.PageCandidateSeed = &PhysicalRootPageCandidateSeed{}
	if err := population.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be combined with population or cohort") {
		t.Fatalf("Population plus seed Validate() error = %v, want exclusivity error", err)
	}

	cohort := PhysicalPlan{
		Version: 1,
		Source:  PhysicalSource{ResourceType: "Patient"},
		BindVars: map[string]any{
			"root_collection": "Patient", "revisions": "revisions", "selections": "selections",
			"selection_members": "selection-members", "memberships": "memberships",
			"revision_id": "revision-1", "project": "project-1", "cohort_project": "project-1", "resource_project": "project-1",
			"generation": "generation-1", "resource_type": "Patient", "policy": "ALL",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection", CohortSource: &PhysicalCohortRootSource{
				CohortStageID: "group_rows", CohortInputStageID: "input", RootIdentityColumn: "_key", RootResourceType: "Patient",
				RevisionCollectionBindKey: "revisions", SelectionCollectionBindKey: "selections", SelectionMembersCollectionBindKey: "selection_members", MembershipsCollectionBindKey: "memberships",
				RevisionIDBindKey: "revision_id", ProjectBindKey: "cohort_project", ResourceProjectBindKey: "resource_project", DatasetGenerationBindKey: "generation", ResourceTypeBindKey: "resource_type", PolicyBindKey: "policy",
			}}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}}}}},
		},
	}
	if err := cohort.Validate(); err != nil {
		t.Fatalf("CohortSource fallback Validate() error = %v", err)
	}
	cohort.Operations[0].RootScan.PageCandidateSeed = &PhysicalRootPageCandidateSeed{}
	if err := cohort.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be combined with population or cohort") {
		t.Fatalf("CohortSource plus seed Validate() error = %v, want exclusivity error", err)
	}
}
