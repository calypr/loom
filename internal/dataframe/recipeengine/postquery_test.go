package recipeengine

import "testing"

func TestMaterializePostQueryUUIDMarker(t *testing.T) {
	row, err := materializePostQueryRow(map[string]any{
		"id": map[string]any{
			exactUUIDOperationKey: "uuid5",
			exactUUIDArgsKey:      []any{"aced-idp.org", "group-1", ",member-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := row["id"], "bffec5ae-0bb6-5f30-b09b-43a5ab3a0181"; got != want {
		t.Fatalf("UUID marker = %v, want %v", got, want)
	}
}

func TestMaterializeNestedUUIDCallMarker(t *testing.T) {
	row, err := materializePostQueryRow(map[string]any{
		"id": map[string]any{
			postQueryCallKey:   "concat",
			postQueryTargetKey: "",
			postQueryArgsKey: []any{
				map[string]any{exactUUIDOperationKey: "uuid5", exactUUIDArgsKey: []any{"aced-idp.org", "group-1", ",member-1"}},
				"-suffix",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := row["id"], "bffec5ae-0bb6-5f30-b09b-43a5ab3a0181-suffix"; got != want {
		t.Fatalf("nested UUID call = %v, want %v", got, want)
	}
}

func TestMaterializePostQueryRejectsDynamicKeyDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row := map[string]any{
		"code_a":                      "value",
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"unexpected"}},
	}
	if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
		t.Fatal("unexpected dynamic key was accepted")
	}
}

func TestMaterializePostQueryRejectsDynamicTypeDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row := map[string]any{
		"code_a":                      42,
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"a"}},
	}
	if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
		t.Fatal("dynamic type drift was accepted")
	}
}

func TestMaterializePostQueryStripsDynamicMetadata(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"code": {"a": {ColumnName: "code_a", ValueType: "string"}},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{
		"code_a":                      "value",
		"__loom_dynamic_runtime_keys": map[string]any{"code": []any{"a"}},
	}, checks)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := row["__loom_dynamic_runtime_keys"]; ok {
		t.Fatal("dynamic metadata leaked into logical row")
	}
}
