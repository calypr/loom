package dataframe

import (
	"strings"
	"testing"
)

func TestOptionalTraversalMatchPreservesRootMembershipAndPostLimitMaterialization(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Specimen",
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compiled.Query, "__match_") || containsOptimizerRule(compiled.OptimizationRules, OptimizerRuleRelationshipSemiJoin) {
		t.Fatalf("optional traversal unexpectedly changed root membership:\n%s\nrules=%#v", compiled.Query, compiled.OptimizationRules)
	}
	if !strings.Contains(compiled.Query, "FOR root IN @@root_collection") {
		t.Fatalf("navigation-only generic request did not use the typed physical renderer:\n%s", compiled.Query)
	}
	if got, want := strings.Index(compiled.Query, "LIMIT @limit"), strings.Index(compiled.Query, "LET __loom_physical_set_1"); got < 0 || want < 0 || got > want {
		t.Fatalf("optional traversal should materialize after the root execution window:\n%s", compiled.Query)
	}
}

func TestRequiredDirectTraversalCompilesTypedChildFilterAsRootSemiJoin(t *testing.T) {
	melanoma := "melanoma"
	builder := Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Condition",
			Alias:          "diagnosis",
			MatchMode:      TraversalMatchRequired,
			Filters: []TypedFilter{{
				FieldRef:  "Condition.id",
				Selector:  "id",
				FieldKind: FilterString,
				Operator:  FilterEquals,
				Values:    []FilterValue{{Kind: FilterString, String: &melanoma}},
			}},
		}},
	}
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		t.Fatal(err)
	}
	if len(semantic.Root.Children) != 1 || semantic.Root.Children[0].MatchMode != TraversalMatchRequired {
		t.Fatalf("semantic required match mode was not preserved: %#v", semantic.Root.Children)
	}
	planned, err := Lower(builder)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.RequiredTraversalMatches) != 1 || len(planned.RequiredTraversalMatches[0].Steps) != 1 {
		t.Fatalf("lowered required matches = %#v", planned.RequiredTraversalMatches)
	}
	compiled, err := Compile(planned, 5)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.PlanProfile != "generic_fhir_graph" {
		t.Fatalf("required match must use generic lowerer, got %q", compiled.PlanProfile)
	}
	if !containsOptimizerRule(compiled.OptimizationRules, OptimizerRuleRelationshipSemiJoin) {
		t.Fatalf("missing semi-join provenance: %#v", compiled.OptimizationRules)
	}
	for _, want := range []string{
		"FOR __match_0_0, __match_edge_0_0 IN 1..1 INBOUND root fhir_edge",
		"__match_edge_0_0.project == @project",
		"__match_edge_0_0.auth_resource_path IN @auth_resource_paths",
		"__match_0_0.auth_resource_path IN @auth_resource_paths",
		"__match_0_0.resourceType == @__match_0_0_to_",
		"__match_0_0.payload",
		"LIMIT 1",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("required semi-join missing %q:\n%s", want, compiled.Query)
		}
	}
	assertBindVarValue(t, compiled.BindVars, "subject_Patient")
	assertBindVarValue(t, compiled.BindVars, "Condition")
	assertBindVarValue(t, compiled.BindVars, melanoma)
	assertRootMatchPrecedesLimit(t, compiled.Query)
}

func TestRequiredNestedTraversalCompilesOneBoundedRoute(t *testing.T) {
	fileID := "melanoma-report"
	compiled, err := CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Specimen",
			Alias:          "specimen",
			Traversals: []TraversalStep{{
				Label:          "subject_Specimen",
				ToResourceType: "DocumentReference",
				Alias:          "file",
				MatchMode:      TraversalMatchRequired,
				Filters: []TypedFilter{{
					FieldRef:  "DocumentReference.id",
					Selector:  "id",
					FieldKind: FilterString,
					Operator:  FilterEquals,
					Values:    []FilterValue{{Kind: FilterString, String: &fileID}},
				}},
			}},
		}},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	first := "FOR __match_0_0, __match_edge_0_0 IN 1..1 INBOUND root fhir_edge"
	second := "FOR __match_0_1, __match_edge_0_1 IN 1..1 INBOUND __match_0_0 fhir_edge"
	if !strings.Contains(compiled.Query, first) || !strings.Contains(compiled.Query, second) {
		t.Fatalf("required nested route was not compiled as one correlated semi-join:\n%s", compiled.Query)
	}
	if strings.Index(compiled.Query, first) > strings.Index(compiled.Query, second) {
		t.Fatalf("nested route order is reversed:\n%s", compiled.Query)
	}
	assertBindVarValue(t, compiled.BindVars, "subject_Specimen")
	assertBindVarValue(t, compiled.BindVars, "DocumentReference")
	assertBindVarValue(t, compiled.BindVars, fileID)
	assertRootMatchPrecedesLimit(t, compiled.Query)
}

func TestRequiredTraversalRejectsUnsafeOrUnknownRoute(t *testing.T) {
	tests := []struct {
		name string
		step TraversalStep
		want string
	}{
		{
			name: "unknown route",
			step: TraversalStep{Label: "not_a_generated_route", ToResourceType: "Condition", Alias: "diagnosis", MatchMode: TraversalMatchRequired},
			want: "not represented by the active generated FHIR schema",
		},
		{
			name: "unsafe route label",
			step: TraversalStep{Label: "subject_Patient RETURN SLEEP(1)", ToResourceType: "Condition", Alias: "diagnosis", MatchMode: TraversalMatchRequired},
			want: "not represented by the active generated FHIR schema",
		},
		{
			name: "unknown match mode",
			step: TraversalStep{Label: "subject_Patient", ToResourceType: "Condition", Alias: "diagnosis", MatchMode: TraversalMatchMode("MUST_MATCH")},
			want: "unsupported traversal match mode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileRequest(Builder{Project: "P1", RootResourceType: "Patient", Traversals: []TraversalStep{test.step}}, 1)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CompileRequest() error = %v, want %q", err, test.want)
			}
		})
	}
	t.Run("manual lowered unsafe route", func(t *testing.T) {
		_, err := Compile(Builder{
			Project:          "P1",
			RootResourceType: "Patient",
			PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
			RequiredTraversalMatches: []RequiredTraversalMatch{{
				Steps: []TraversalMatchStep{{Label: "subject_Patient RETURN SLEEP(1)", ToResourceType: "Condition"}},
			}},
		}, 1)
		if err == nil || !strings.Contains(err.Error(), "not represented by the active generated FHIR schema") {
			t.Fatalf("Compile() error = %v", err)
		}
	})
}

func TestNavigationOnlyPhysicalPlanRejectsRequiredRelationshipMatch(t *testing.T) {
	_, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "P1",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "diagnosis", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: TraversalMatchRequired,
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires a relationship match") {
		t.Fatalf("BuildGenericPhysicalPlan() error = %v", err)
	}
}

func assertRootMatchPrecedesLimit(t *testing.T, query string) {
	t.Helper()
	match := strings.Index(query, "FOR __match_0_0")
	sort := strings.Index(query, "SORT root._key")
	limit := strings.Index(query, "LIMIT @limit")
	if match < 0 || sort < 0 || limit < 0 || match > sort || match > limit || sort > limit {
		t.Fatalf("required match must be a root predicate before SORT/LIMIT:\n%s", query)
	}
}

func assertBindVarValue(t *testing.T, bindVars map[string]any, want any) {
	t.Helper()
	for _, got := range bindVars {
		if got == want {
			return
		}
	}
	t.Fatalf("bind variables do not contain %#v: %#v", want, bindVars)
}
