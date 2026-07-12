package dataframe

import (
	"strings"
	"testing"
)

func TestGenericLoweringSharesIdenticalTraversalPrefixes(t *testing.T) {
	planned, err := Lower(Builder{
		Project: "P1", RootResourceType: "Specimen",
		Traversals: []TraversalStep{
			{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file_primary", Fields: []FieldSelect{{Name: "title", Select: "content[].attachment.title"}}},
			{Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file_secondary", Fields: []FieldSelect{{Name: "url", Select: "content[].attachment.url"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Sets) != 1 {
		t.Fatalf("identical generic traversal sets = %#v, want one shared set", planned.Sets)
	}
	if len(planned.DerivedFields) != 2 || planned.DerivedFields[0].Source != planned.Sets[0].Name || planned.DerivedFields[1].Source != planned.Sets[0].Name {
		t.Fatalf("derived fields did not reuse the shared set: %#v / %#v", planned.Sets, planned.DerivedFields)
	}
	if planned.PlanHint == nil || len(planned.PlanHint.AppliedRules) != 1 || planned.PlanHint.AppliedRules[0] != OptimizerRuleTraversalSharing {
		t.Fatalf("applied optimizer rules = %#v", planned.PlanHint)
	}
	compiled, err := Compile(planned, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.OptimizationRules) != 1 || compiled.OptimizationRules[0] != OptimizerRuleTraversalSharing {
		t.Fatalf("compiled optimizer rules = %#v", compiled.OptimizationRules)
	}
}

func TestGenericLoweringDoesNotShareTraversalsWithDifferentPredicates(t *testing.T) {
	first := "first"
	second := "second"
	planned, err := Lower(Builder{
		Project: "P1", RootResourceType: "Specimen",
		Traversals: []TraversalStep{
			{
				Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "first_file",
				Filters: []TypedFilter{{FieldRef: "DocumentReference.title", Selector: "content[].attachment.title", FieldKind: FilterString, Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &first}}}},
			},
			{
				Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "second_file",
				Filters: []TypedFilter{{FieldRef: "DocumentReference.title", Selector: "content[].attachment.title", FieldKind: FilterString, Repeated: true, Quantifier: QuantifierAny, Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &second}}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Sets) != 2 {
		t.Fatalf("predicate-distinct traversal sets = %#v, want two sets", planned.Sets)
	}
	if planned.PlanHint == nil || len(planned.PlanHint.AppliedRules) != 1 || planned.PlanHint.AppliedRules[0] != OptimizerRuleFilterPushdown {
		t.Fatalf("unexpected optimizer rules = %#v", planned.PlanHint)
	}
}

func TestGenericLoweringSharesSiblingPrefixAcrossTargetTypes(t *testing.T) {
	conditionID := "condition-1"
	specimenID := "specimen-1"
	observationID := "observation-1"
	planned, err := Lower(Builder{
		Project: "P1", RootResourceType: "Patient",
		Traversals: []TraversalStep{
			{
				Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
				Fields: []FieldSelect{{Name: "id", Select: "id", ValueMode: "ALL"}},
				Filters: []TypedFilter{{
					FieldRef: "Condition.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals,
					Values: []FilterValue{{Kind: FilterString, String: &conditionID}},
				}},
			},
			{
				Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen",
				Fields: []FieldSelect{{Name: "id", Select: "id", ValueMode: "ALL"}},
				Filters: []TypedFilter{{
					FieldRef: "Specimen.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals,
					Values: []FilterValue{{Kind: FilterString, String: &specimenID}},
				}},
			},
			{
				Label: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
				Fields: []FieldSelect{{Name: "id", Select: "id", ValueMode: "ALL"}},
				Filters: []TypedFilter{{
					FieldRef: "Observation.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals,
					Values: []FilterValue{{Kind: FilterString, String: &observationID}},
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("expected generic plan, got %#v", planned.PlanHint)
	}
	if !containsOptimizerRule(planned.PlanHint.AppliedRules, OptimizerRuleTraversalSharing) || !containsOptimizerRule(planned.PlanHint.AppliedRules, OptimizerRuleFilterPushdown) {
		t.Fatalf("expected sibling sharing and filter pushdown rules, got %#v", planned.PlanHint)
	}

	base, ok := allTargetTraversal(planned.Sets)
	if !ok {
		t.Fatalf("missing shared sibling traversal: %#v", planned.Sets)
	}
	if base.Kind != SetKindTraverse || base.Direction != "INBOUND" || base.Label != "subject_Patient" || !base.AllTargetTypes || base.ToResourceType != "Condition" {
		t.Fatalf("unexpected shared traversal = %#v", base)
	}
	if got := countNamedSetKind(planned.Sets, SetKindTraverse); got != 1 {
		t.Fatalf("physical generic traversals = %d, want one shared prefix: %#v", got, planned.Sets)
	}

	wantSubsets := map[string]string{
		"generic_condition_set":   "Condition",
		"generic_specimen_set":    "Specimen",
		"generic_observation_set": "Observation",
	}
	for name, resourceType := range wantSubsets {
		subset, ok := namedSetByName(planned.Sets, name)
		if !ok {
			t.Fatalf("missing typed subset %q: %#v", name, planned.Sets)
		}
		if subset.Kind != SetKindFilter || subset.Source != base.Name || subset.MatchResourceType != resourceType || len(subset.Filters) != 1 || subset.Unique || subset.SortField != "_key" {
			t.Fatalf("typed subset %q = %#v", name, subset)
		}
	}
	for _, field := range planned.DerivedFields {
		if _, ok := wantSubsets[field.Source]; !ok {
			t.Fatalf("derived field %q unexpectedly escaped its typed sibling subset: %#v", field.Name, field)
		}
	}

	compiled, err := Compile(planned, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(compiled.Query, "1..1 INBOUND root fhir_edge"); got != 1 {
		t.Fatalf("shared sibling query emitted %d root traversals, want one:\n%s", got, compiled.Query)
	}
	if strings.Contains(compiled.Query, base.Name+"_to") {
		t.Fatalf("shared base accidentally retained its validation-anchor type predicate:\n%s", compiled.Query)
	}
	if got := strings.Count(compiled.Query, "FILTER __item.resourceType == @"); got != 3 {
		t.Fatalf("typed sibling subsets = %d, want three:\n%s", got, compiled.Query)
	}
	if got := strings.Count(compiled.Query, "__edge.dataset_generation == @dataset_generation"); got != 1 {
		t.Fatalf("shared traversal generation scope count = %d, want one:\n%s", got, compiled.Query)
	}
	if got := strings.Count(compiled.Query, "__edge.auth_resource_path IN @auth_resource_paths"); got != 1 {
		t.Fatalf("shared traversal auth scope count = %d, want one:\n%s", got, compiled.Query)
	}
	for _, want := range []string{conditionID, specimenID, observationID} {
		assertBindVarValue(t, compiled.BindVars, want)
	}
}

func TestGenericSiblingPrefixSharingRetainsRequiredMatch(t *testing.T) {
	conditionID := "required-condition"
	compiled, err := CompileRequest(Builder{
		Project: "P1", RootResourceType: "Patient",
		Traversals: []TraversalStep{
			{
				Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition", MatchMode: TraversalMatchRequired,
				Filters: []TypedFilter{{
					FieldRef: "Condition.id", Selector: "id", FieldKind: FilterString, Operator: FilterEquals,
					Values: []FilterValue{{Kind: FilterString, String: &conditionID}},
				}},
			},
			{Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen"},
			{Label: "subject_Patient", ToResourceType: "Observation", Alias: "observation"},
		},
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !containsOptimizerRule(compiled.OptimizationRules, OptimizerRuleTraversalSharing) || !containsOptimizerRule(compiled.OptimizationRules, OptimizerRuleRelationshipSemiJoin) {
		t.Fatalf("missing shared-prefix/required-match provenance: %#v", compiled.OptimizationRules)
	}
	if !strings.Contains(compiled.Query, "LET generic_root_subject_Patient_neighbors_set") {
		t.Fatalf("missing shared materialized sibling set:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "FOR __match_0_0, __match_edge_0_0 IN 1..1 INBOUND root fhir_edge") {
		t.Fatalf("required sibling match was lost:\n%s", compiled.Query)
	}
	assertRootMatchPrecedesLimit(t, compiled.Query)
}

func TestGenericSiblingPrefixSharingSupportsNestedChildRoutes(t *testing.T) {
	gender := "female"
	planned, err := Lower(Builder{
		Project: "P1", RootResourceType: "Patient",
		Filters: []TypedFilter{{
			FieldRef: "Patient.gender", Selector: "gender", FieldKind: FilterString, Operator: FilterEquals,
			Values: []FilterValue{{Kind: FilterString, String: &gender}},
		}},
		Traversals: []TraversalStep{
			{Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition"},
			{
				Label: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen",
				Traversals: []TraversalStep{{
					Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file",
					Fields: []FieldSelect{{Name: "id", Select: "id", ValueMode: "ALL"}},
				}},
			},
			{Label: "subject_Patient", ToResourceType: "Observation", Alias: "observation"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fileSet, ok := namedSetByName(planned.Sets, "generic_file_set")
	if !ok || fileSet.Kind != SetKindTraverse || fileSet.Source != "generic_specimen_set" || fileSet.ToResourceType != "DocumentReference" {
		t.Fatalf("nested route did not retain typed specimen parent: %#v", planned.Sets)
	}
	compiled, err := Compile(planned, 1)
	if err != nil {
		t.Fatalf("compile shared nested route: %v", err)
	}
	if got := strings.Count(compiled.Query, "1..1 INBOUND root fhir_edge"); got != 1 {
		t.Fatalf("shared root prefix emitted %d root traversals, want one:\n%s", got, compiled.Query)
	}
	if !strings.Contains(compiled.Query, "FOR __parent IN generic_specimen_set FOR __node, __edge IN 1..1 INBOUND __parent fhir_edge") {
		t.Fatalf("nested traversal did not use the typed subset parent:\n%s", compiled.Query)
	}
}

func namedSetByName(sets []NamedSet, name string) (NamedSet, bool) {
	for _, set := range sets {
		if set.Name == name {
			return set, true
		}
	}
	return NamedSet{}, false
}

func countNamedSetKind(sets []NamedSet, kind string) int {
	count := 0
	for _, set := range sets {
		if set.Kind == kind {
			count++
		}
	}
	return count
}

func allTargetTraversal(sets []NamedSet) (NamedSet, bool) {
	for _, set := range sets {
		if set.Kind == SetKindTraverse && set.AllTargetTypes {
			return set, true
		}
	}
	return NamedSet{}, false
}

func filterResourceTypes(sets []NamedSet, source string) map[string]struct{} {
	types := map[string]struct{}{}
	for _, set := range sets {
		if set.Kind == SetKindFilter && set.Source == source && set.MatchResourceType != "" {
			types[set.MatchResourceType] = struct{}{}
		}
	}
	return types
}

func sameStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}
