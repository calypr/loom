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

func TestFilterColumnReferencesFollowNestedPhysicalChildren(t *testing.T) {
	leaf := func(variable string, path ...string) PhysicalExpression {
		return PhysicalExpression{Kind: PhysicalValueExpression, Value: &PhysicalValue{Variable: variable, Path: path}}
	}
	tests := []struct {
		name   string
		filter PhysicalFilter
		want   bool
	}{
		{"direct", PhysicalFilter{Predicate: PhysicalPredicate{Left: PhysicalValue{Variable: "row", Path: []string{"contributors"}}}}, true},
		{"exists return", PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalExistsPredicate, Exists: &PhysicalSubplan{Return: leaf("row", "contributors", "id")}}}, true},
		{"comparison expression", PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalComparisonPredicate, Comparison: &PhysicalPredicate{LeftExpression: func() *PhysicalExpression { x := leaf("row", "contributors"); return &x }()}}}, true},
		{"other variable", PhysicalFilter{Expression: &PhysicalPredicateExpression{Kind: PhysicalExistsPredicate, Exists: &PhysicalSubplan{Return: leaf("other", "contributors")}}}, false},
		{"later field", PhysicalFilter{Predicate: PhysicalPredicate{Left: PhysicalValue{Variable: "row", Path: []string{"public", "contributors"}}}}, false},
		{"empty path", PhysicalFilter{Predicate: PhysicalPredicate{Left: PhysicalValue{Variable: "row"}}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := physicalFilterReferencesColumn(test.filter, "row", "contributors"); got != test.want {
				t.Fatalf("reference=%v want%v", got, test.want)
			}
		})
	}
}
