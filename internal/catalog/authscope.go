package catalog

func ExplicitAuthResourcePathsUnrestricted(unrestricted bool) *bool {
	return &unrestricted
}

func EffectiveAuthResourcePathsUnrestricted(paths []string, explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	return len(paths) == 0
}
