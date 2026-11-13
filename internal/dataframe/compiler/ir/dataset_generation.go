package ir

import "strings"

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func normalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

func datasetGenerationBindValue(generation string) any {
	generation = normalizeDatasetGeneration(generation)
	if generation == "" {
		return nil
	}
	return generation
}

func NormalizeDatasetGeneration(generation string) string {
	return normalizeDatasetGeneration(generation)
}
func DatasetGenerationBindValue(generation string) any { return datasetGenerationBindValue(generation) }
