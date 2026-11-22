package catalog

import "github.com/calypr/loom/internal/projectid"

func NormalizeDatasetGeneration(generation string) string {
	return projectid.NormalizeGeneration(generation)
}

func HasDatasetGeneration(generation string) bool {
	return projectid.HasGeneration(generation)
}
