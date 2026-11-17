package ir

import "testing"

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
