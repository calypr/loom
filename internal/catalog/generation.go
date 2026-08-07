package catalog

import "strings"

// NormalizeDatasetGeneration applies the optional opaque-generation convention
// at every catalog boundary. It prevents a whitespace-padded generation from
// using a cache key that differs from the AQL bind value.
func NormalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

// HasDatasetGeneration reports whether a request selected a concrete
// generation. Whitespace-only inputs follow the optional-field convention and
// select the legacy null namespace.
func HasDatasetGeneration(generation string) bool {
	return NormalizeDatasetGeneration(generation) != ""
}
