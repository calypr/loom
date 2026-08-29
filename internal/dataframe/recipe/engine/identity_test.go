package engine

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestEnsureStableRowIdentityIsDeterministicAndScoped(t *testing.T) {
	identity := &spec.RowIdentity{Grain: spec.RowGrainPatient, Fields: []string{"project", "_key"}}
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

func TestOutputStreamStripsCompilerOnlyColumnsAfterIdentity(t *testing.T) {
	stream := OutputStream{
		Name:        "DocumentReference",
		Columns:     []string{"id"},
		RowIdentity: &spec.RowIdentity{Fields: []string{"_key"}},
		bindVars:    map[string]any{"project": "P1"},
		stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{
				"_key":                        "internal-key",
				"__loom_dynamic_runtime_keys": map[string]any{"family": []string{"x"}},
				"auth_resource_path":          "HTAN_INT-BForePC",
				"id":                          "document-1",
			})
		},
	}
	var got map[string]any
	if _, err := stream.Stream(context.Background(), func(row map[string]any) error {
		got = row
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "document-1" || got["auth_resource_path"] != "HTAN_INT-BForePC" || got["__loom_row_id"] == nil {
		t.Fatalf("public row lost required values: %#v", got)
	}
	if _, ok := got["_key"]; ok {
		t.Fatalf("public row leaked _key: %#v", got)
	}
	if _, ok := got["__loom_dynamic_runtime_keys"]; ok {
		t.Fatalf("public row leaked dynamic runtime keys: %#v", got)
	}
}
