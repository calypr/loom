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

func TestPhysicalScopeWalkerKeepsNestedChildOwner(t *testing.T) {
	var predicate PhysicalPredicateExpression
	subplan := PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Expression: &predicate}}}}
	predicate = PhysicalPredicateExpression{Kind: PhysicalExistsPredicate, Exists: &subplan}
	operation := PhysicalOperation{Kind: PhysicalExpressionLetOp, ExpressionLet: &PhysicalExpressionLet{Variable: "value", Expression: PhysicalExpression{Kind: PhysicalAggregateExpression, Aggregate: &PhysicalAggregate{Predicate: &predicate}}}}
	walker := physicalScopeWalker{activeSubplans: make(map[*PhysicalSubplan]bool), visitedSubplans: make(map[*PhysicalSubplan]bool)}
	err := walker.operationChildren(&operation, "physical plan operation 0 (EXPRESSION_LET)")
	want := "physical plan operation 0 (EXPRESSION_LET) expression LET aggregate predicate EXISTS subplan operation 0 (FILTER) filter EXISTS subplan contains a recursive physical subplan"
	if err == nil || err.Error() != want {
		t.Fatalf("recursive subplan error=%v, want %q", err, want)
	}
}

func TestScopedOperationChildInventoryCountsAliasedOccurrences(t *testing.T) {
	shared := []PhysicalOperation{{Kind: PhysicalLimitOp, Limit: &PhysicalLimit{BindKey: "limit"}}}
	operation := PhysicalOperation{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{Owner: PhysicalUnnestOwner{Route: []PhysicalUnnestRouteStep{{Scope: shared}, {Scope: shared}}}}}
	if err := validateScopedOperationChildren(&operation, "aliased routes"); err != nil {
		t.Fatalf("two scope occurrences sharing backing storage were rejected: %v", err)
	}
}
