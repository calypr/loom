package compiler

import (
	"testing"
)

func lineageContributorRows(t *testing.T, result map[string]any) []map[string]any {
	t.Helper()
	values, ok := result["contributors"].([]any)
	if !ok {
		t.Fatalf("row-lineage response contributors are %T, want array: %#v", result["contributors"], result)
	}
	contributors := make([]map[string]any, 0, len(values))
	for _, value := range values {
		contributor, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("row-lineage contributor is %T, want object: %#v", value, value)
		}
		contributors = append(contributors, contributor)
	}
	return contributors
}
