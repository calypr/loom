package ir

import (
	"strings"
	"testing"
)

func TestValidatePhysicalPredicateExpressionRejectsRecursiveSubplan(t *testing.T) {
	var predicate PhysicalPredicateExpression
	subplan := PhysicalSubplan{
		Captures: []string{"outer"},
		Operations: []PhysicalOperation{{
			Kind:   PhysicalFilterOp,
			Filter: &PhysicalFilter{Expression: &predicate},
		}},
	}
	predicate = PhysicalPredicateExpression{Kind: PhysicalExistsPredicate, Exists: &subplan}

	err := ValidateGenericPhysicalPlanScope(PhysicalPlan{
		Version: 1,
		Operations: []PhysicalOperation{{
			Kind:   PhysicalFilterOp,
			Filter: &PhysicalFilter{Expression: &predicate},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "physical subplan expression contains a recursive cycle") {
		t.Fatalf("recursive EXISTS subplan error = %v", err)
	}
}
