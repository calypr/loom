package catalog

import (
	"strings"
	"testing"
)

func TestPopulatedFieldFromRowPreservesDriverNumericRepresentations(t *testing.T) {
	field, err := populatedFieldFromRow(map[string]any{
		"resource_type":         "Patient",
		"path":                  "birthDate",
		"doc_count":             int64(7),
		"sample_count":          float64(3),
		"distinct_values":       []any{"1980-01-01"},
		"distinct_truncated":    false,
		"pivot_candidate":       true,
		"pivot_columns":         []string{"value"},
		"pivot_value_selectors": []any{"code"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if field.DocCount != 7 || field.SampleCount != 3 || !field.PivotCandidate || len(field.PivotColumns) != 1 {
		t.Fatalf("decoded field = %#v", field)
	}
}

func TestPopulatedFieldFromRowRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value any
	}{
		{name: "numeric", field: "doc_count", value: "seven"},
		{name: "boolean", field: "pivot_candidate", value: "true"},
		{name: "slice", field: "distinct_values", value: []any{"ok", int64(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := map[string]any{"resource_type": "Patient", "path": "id", test.field: test.value}
			_, err := populatedFieldFromRow(row)
			if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), "Patient/id") {
				t.Fatalf("error = %v, want context for %s", err, test.field)
			}
		})
	}
}
