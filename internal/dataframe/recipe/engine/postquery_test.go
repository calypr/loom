package engine

import "testing"

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
