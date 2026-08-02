package schema

// LookupTraversal returns a defensive copy of the generated traversal record.
func LookupTraversal(fromType, edgeLabel, toType string) (TraversalSpec, bool) {
	spec, ok := generatedTraversals[traversalKey(fromType, edgeLabel, toType)]
	if !ok {
		return TraversalSpec{}, false
	}
	return cloneTraversalSpec(spec), true
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}

func cloneTraversalSpec(spec TraversalSpec) TraversalSpec {
	spec.Direction = cloneStrings(spec.Direction)
	spec.Multiplicity = cloneStrings(spec.Multiplicity)
	spec.Backref = cloneStrings(spec.Backref)
	spec.RegexMatch = cloneStrings(spec.RegexMatch)
	return spec
}
