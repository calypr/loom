package aql

import (
	"reflect"
	"testing"
)

func TestPruneUnusedRuntimeBindVarsMatchesWholeTokens(t *testing.T) {
	binds := map[string]any{"@docs": "collection", "docs": "unused scalar", "key_1": 1, "key_10": 10}
	got := pruneUnusedRuntimeBindVars(binds, "FOR d IN @@docs FILTER d.key == @key_10 RETURN d")
	want := map[string]any{"@docs": "collection", "key_10": 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("binds = %v, want %v", got, want)
	}
}
