package compiler

import "testing"

func TestRichConsumerDiagnosticsClassifiesOnlyIdenticalExpressions(t *testing.T) {
	selector := Selector{Steps: []SelectorStep{{Field: "id"}}}
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "p",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
				Aggregates: []SemanticAggregate{
					{Name: "count_a", Operation: "COUNT"},
					{Name: "count_b", Operation: "COUNT"},
					{Name: "distinct_ids", Operation: "DISTINCT_VALUES", Selector: &selector},
				},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := physicalPlanDiagnostics(plan)
	var identicalCounts, singletonGroups int
	for _, group := range diagnostics.RichConsumerGroups {
		if group.SourceSet != "child_set_1" || group.Kind != PhysicalAggregateExpression {
			continue
		}
		if group.Eligible && group.Consumers == 2 {
			identicalCounts++
		}
		if !group.Eligible && group.Consumers == 1 {
			singletonGroups++
		}
	}
	if identicalCounts != 1 {
		t.Fatalf("identical aggregate group count = %d, diagnostics=%#v", identicalCounts, diagnostics.RichConsumerGroups)
	}
	if singletonGroups != 1 {
		t.Fatalf("non-identical aggregate singleton count = %d, diagnostics=%#v", singletonGroups, diagnostics.RichConsumerGroups)
	}
	for _, state := range diagnostics.OptimizationPolicy.RuleStates {
		if state.Rule == PhysicalOptimizationRuleRichConsumerFusion && state.Enabled {
			t.Fatalf("rich-consumer fusion unexpectedly enabled: %#v", state)
		}
	}
}
