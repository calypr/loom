package engine

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

func TestEnsureStableRowIdentityIsDeterministicAndScoped(t *testing.T) {
	identity := &compiler.RowIdentity{Grain: compiler.RowGrainPatient, Fields: []string{"project", "_key"}}
	first := map[string]any{"_key": "p1"}
	second := map[string]any{"_key": "p1"}
	binds := map[string]any{"project": "P1"}
	if err := ensureStableRowIdentity(first, identity, binds); err != nil {
		t.Fatal(err)
	}
	if err := ensureStableRowIdentity(second, identity, binds); err != nil {
		t.Fatal(err)
	}
	if first["__loom_row_id"] != second["__loom_row_id"] {
		t.Fatalf("identity is not deterministic: %v != %v", first["__loom_row_id"], second["__loom_row_id"])
	}
	third := map[string]any{"_key": "p1"}
	if err := ensureStableRowIdentity(third, identity, map[string]any{"project": "P2"}); err != nil {
		t.Fatal(err)
	}
	if first["__loom_row_id"] == third["__loom_row_id"] {
		t.Fatal("different project scopes produced the same identity")
	}
}

func TestEnsureStableRowIdentityPreservesCompiledIdentity(t *testing.T) {
	row := map[string]any{"__loom_row_id": "recipe-id"}
	if err := ensureStableRowIdentity(row, nil, nil); err != nil {
		t.Fatal(err)
	}
	if row["__loom_row_id"] != "recipe-id" {
		t.Fatalf("compiled identity changed: %#v", row)
	}
}
