package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRenderRejectsUnsupportedPreviewIndexCombinations(t *testing.T) {
	for _, test := range []struct {
		name    string
		options Options
		message string
	}{
		{"paging with hint", Options{RootIndex: RootIndexHint("safe"), Preview: RootPageKeysPreview{}}, "root page key preview does not support a root index override"},
		{"streaming with disabled index", Options{RootIndex: RootIndexDisabled(), Preview: StreamingMissingCategoryPivotPreview{}}, "streaming missing-category preview does not support a disabled root index"},
		{"empty index hint", Options{RootIndex: RootIndexHint("")}, "is not a safe index name"},
		{"absent preview pointer", Options{Preview: (*RootPageKeysPreview)(nil)}, "unsupported preview option"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Render(ir.PhysicalPlan{}, test.options)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Render() error = %v, want %q", err, test.message)
			}
		})
	}
}
