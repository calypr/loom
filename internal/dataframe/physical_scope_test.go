package dataframe

import (
	"strings"
	"testing"
)

func TestValidateGenericPhysicalPlanScope(t *testing.T) {
	plan := genericScopePhysicalPlan(t)
	if err := ValidateGenericPhysicalPlanScope(plan); err != nil {
		t.Fatalf("ValidateGenericPhysicalPlanScope() error = %v", err)
	}
}

func TestValidateGenericPhysicalPlanScopeRejectsMissingMisboundAndReorderedScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, plan *PhysicalPlan)
		want   string
	}{
		{
			name: "missing root project filter",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				removePhysicalOperation(plan, findProjectFilter(t, *plan, "root"))
			},
			want: "project scope filter root.project == @project",
		},
		{
			name: "misbound root project filter",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				index := findProjectFilter(t, *plan, "root")
				plan.Operations[index].Filter.Predicate.Right = &PhysicalValue{BindKey: physicalScopeAllowedBind}
			},
			want: "project scope filter at operation",
		},
		{
			name: "missing traversal target project filter",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				removePhysicalOperation(plan, findProjectFilter(t, *plan, "node_1"))
			},
			want: "project scope filter node_1.project == @project",
		},
		{
			name: "project scope reordered after auth let",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				projectIndex := findProjectFilter(t, *plan, "root")
				authIndex := findTestAuthScopeLet(t, *plan, "root_scope_allowed")
				plan.Operations[projectIndex], plan.Operations[authIndex] = plan.Operations[authIndex], plan.Operations[projectIndex]
			},
			want: "AUTH_RESOURCE_PATH_ALLOWED LET",
		},
		{
			name: "missing root auth scope",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				authLet := findTestAuthScopeLet(t, *plan, "root_scope_allowed")
				authEquality := findTestAuthScopeEquality(t, *plan, "root_scope_allowed")
				removePhysicalOperation(plan, authEquality)
				removePhysicalOperation(plan, authLet)
			},
			want: "missing AUTH_RESOURCE_PATH_ALLOWED LET",
		},
		{
			name: "misbound root auth allowed marker",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				index := findTestAuthScopeEquality(t, *plan, "root_scope_allowed")
				plan.Operations[index].Filter.Predicate.Right = &PhysicalValue{BindKey: physicalScopeProjectBind}
			},
			want: "auth scope equality",
		},
		{
			name: "traversal auth scope omits target path",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				index := findTestAuthScopeLet(t, *plan, "traversal_1_scope_allowed")
				inputs := plan.Operations[index].DerivedLet.Inputs
				filtered := inputs[:0]
				for _, input := range inputs {
					if input.Variable == "node_1" && physicalPathEquals(input.Path, []string{physicalScopeAuthPathField}) {
						continue
					}
					filtered = append(filtered, input)
				}
				plan.Operations[index].DerivedLet.Inputs = filtered
			},
			want: "must include node_1.auth_resource_path",
		},
		{
			name: "auth equality reordered before its let",
			mutate: func(t *testing.T, plan *PhysicalPlan) {
				authLet := findTestAuthScopeLet(t, *plan, "root_scope_allowed")
				authEquality := findTestAuthScopeEquality(t, *plan, "root_scope_allowed")
				plan.Operations[authLet], plan.Operations[authEquality] = plan.Operations[authEquality], plan.Operations[authLet]
			},
			want: "validate physical plan before verifying generic scope",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := genericScopePhysicalPlan(t)
			test.mutate(t, &plan)
			if err := ValidateGenericPhysicalPlanScope(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateGenericPhysicalPlanScope() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func genericScopePhysicalPlan(t *testing.T) PhysicalPlan {
	t.Helper()
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []SemanticNode{{
					Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildGenericPhysicalPlan() error = %v", err)
	}
	return plan
}

func findProjectFilter(t *testing.T, plan PhysicalPlan, variable string) int {
	t.Helper()
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalFilterOp && isProjectScopePredicate(operation.Filter.Predicate, variable) {
			return index
		}
	}
	t.Fatalf("project scope filter for %q not found", variable)
	return 0
}

func findTestAuthScopeLet(t *testing.T, plan PhysicalPlan, variable string) int {
	t.Helper()
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" && operation.DerivedLet.Variable == variable {
			return index
		}
	}
	t.Fatalf("auth scope LET %q not found", variable)
	return 0
}

func findTestAuthScopeEquality(t *testing.T, plan PhysicalPlan, variable string) int {
	t.Helper()
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalFilterOp && isScopeAllowedPredicate(operation.Filter.Predicate, variable) {
			return index
		}
	}
	t.Fatalf("auth scope equality for %q not found", variable)
	return 0
}

func removePhysicalOperation(plan *PhysicalPlan, index int) {
	plan.Operations = append(plan.Operations[:index], plan.Operations[index+1:]...)
}
