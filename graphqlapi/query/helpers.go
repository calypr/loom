package queryapi

import "github.com/calypr/loom/internal/catalog"

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func cloneTraversals(in []catalog.PopulatedReference) []catalog.PopulatedReference {
	if len(in) == 0 {
		return []catalog.PopulatedReference{}
	}
	return append([]catalog.PopulatedReference(nil), in...)
}
