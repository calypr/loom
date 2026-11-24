package aql

import (
	"reflect"
	"testing"
)

func TestPruneUnusedRuntimeBindVarsUsesCompleteNames(t *testing.T) {
	binds := map[string]any{"key_6": 6, "key_60": 60, "key_600": 600, "@root": "Patient", "root": "unrelated", "needed": 1}
	got := pruneUnusedRuntimeBindVars(binds, "FOR x IN @@root FILTER x.a == @key_60 AND x.b == @needed RETURN x")
	want := map[string]any{"key_60": 60, "@root": "Patient", "needed": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings=%v, want %v", got, want)
	}
}
