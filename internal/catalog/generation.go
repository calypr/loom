package catalog

import "strings"

func NormalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

func HasDatasetGeneration(generation string) bool {
	return NormalizeDatasetGeneration(generation) != ""
}
