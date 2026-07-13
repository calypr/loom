package compiler

import "strings"

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func normalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

// datasetGenerationBindValue makes the absent-generation case explicit for
// every AQL renderer. Binding nil and using equality yields
// `dataset_generation == null`, so legacy rows are isolated from all
// generation-qualified rows by default.
func datasetGenerationBindValue(generation string) any {
	generation = normalizeDatasetGeneration(generation)
	if generation == "" {
		return nil
	}
	return generation
}
