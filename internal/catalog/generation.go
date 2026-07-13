package catalog

import "strings"

// NormalizeDatasetGeneration applies the optional opaque-generation convention
// at every catalog boundary. It prevents a whitespace-padded generation from
// using a cache key that differs from the AQL bind value.
func NormalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

// DatasetGenerationBindValue returns the only safe generation bind value for
// catalog queries. Generation-qualified callers match one exact opaque value;
// callers without one deliberately match only the legacy null namespace.
//
// The return type is any so the legacy case is carried to the Arango driver as
// JSON null rather than as an empty string, which would otherwise select an
// unrelated namespace (or accidentally drop legacy documents).
func DatasetGenerationBindValue(generation string) any {
	generation = NormalizeDatasetGeneration(generation)
	if generation == "" {
		return nil
	}
	return generation
}

// HasDatasetGeneration reports whether a request selected a concrete
// generation. Whitespace-only inputs follow the optional-field convention and
// select the legacy null namespace.
func HasDatasetGeneration(generation string) bool {
	return NormalizeDatasetGeneration(generation) != ""
}
