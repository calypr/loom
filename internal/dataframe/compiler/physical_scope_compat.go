package compiler

// These small test-facing predicates preserve the historical same-package
// test vocabulary while scope validation now lives in compiler/ir.
func physicalPathEquals(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func isProjectScopePredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" && predicate.Left.Variable == variable && physicalPathEquals(predicate.Left.Path, []string{"project"}) && predicate.Right != nil && predicate.Right.BindKey == "project"
}

func isScopeAllowedPredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" && predicate.Left.Variable == variable && predicate.Right != nil && predicate.Right.BindKey == "scope_allowed"
}
