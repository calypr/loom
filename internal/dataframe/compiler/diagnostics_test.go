package compiler

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRichConsumerDiagnosticsClassifiesOnlyIdenticalExpressions(t *testing.T) {
	selector := spec.Selector{Steps: []spec.SelectorStep{{Field: "id"}}}
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient",
			Aggregates: []recipeplan.SemanticAggregate{
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
	diagnostics := ir.PhysicalPlanDiagnostics(plan)
	var identicalCounts, singletonGroups int
	for _, group := range diagnostics.RichConsumerGroups {
		if group.SourceSet != "child_set_1" || group.Kind != ir.PhysicalAggregateExpression {
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
}
