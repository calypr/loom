package catalog

// ExplicitAuthResourcePathsUnrestricted returns an independent pointer for
// catalog options. The pointer distinguishes a deliberately restricted empty
// scope from legacy callers that provide only an empty path slice.
func ExplicitAuthResourcePathsUnrestricted(unrestricted bool) *bool {
	return &unrestricted
}

// EffectiveAuthResourcePathsUnrestricted resolves the compatibility rule for
// catalog callers. New request paths should always pass an explicit value;
// direct legacy callers retain their historical empty-means-unrestricted
// behavior until migrated.
func EffectiveAuthResourcePathsUnrestricted(paths []string, explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	return len(paths) == 0
}
