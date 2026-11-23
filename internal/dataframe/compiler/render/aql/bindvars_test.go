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

func TestPruneUnusedRuntimeBindVarsUsesCompleteNames(t *testing.T) {
	binds := map[string]any{"key_6": 6, "key_60": 60, "key_600": 600, "@root": "Patient", "root": "unrelated", "needed": 1}
	got := pruneUnusedRuntimeBindVars(binds, "FOR x IN @@root FILTER x.a == @key_60 AND x.b == @needed RETURN x")
	want := map[string]any{"key_60": 60, "@root": "Patient", "needed": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings=%v, want %v", got, want)
	}
}

func bindReferenceForValue(bindVars map[string]any, want string) string {
	for key, value := range bindVars {
		if actual, ok := value.(string); ok && actual == want {
			return "@" + key
		}
	}
	return "@<missing-bind-for-" + want + ">"
}

func hasBindValue(bindVars map[string]any, want any) bool {
	for _, value := range bindVars {
		switch expected := want.(type) {
		case nil:
			if value == nil {
				return true
			}
		case bool:
			if actual, ok := value.(bool); ok && actual == expected {
				return true
			}
		}
	}
	return false
}
