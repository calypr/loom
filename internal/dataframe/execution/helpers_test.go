package execution

import "testing"

func TestSanitizeColumnNamePreservesCompilerShape(t *testing.T) {
	if got, want := sanitizeColumnName("field__--value--"), "field____value__"; got != want {
		t.Fatalf("sanitizeColumnName() = %q, want %q", got, want)
	}
}
