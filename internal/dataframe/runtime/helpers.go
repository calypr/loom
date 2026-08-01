package runtime

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func normalizeDatasetGeneration(generation string) string {
	return strings.TrimSpace(generation)
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)

func cloneRowIdentity(in *spec.RowIdentity) *spec.RowIdentity {
	if in == nil {
		return nil
	}
	out := *in
	out.Fields = cloneStrings(in.Fields)
	return &out
}
