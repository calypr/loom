package execution

import (
	"strings"
)

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
	return b.String()
}

const (
	datasetGenerationBindKey = "dataset_generation"
	datasetGenerationField   = "dataset_generation"
)
