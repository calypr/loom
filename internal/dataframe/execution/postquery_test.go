package execution

import (
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
)

func TestDynamicChecksRetainsEveryFamilySharingACompatibilityColumn(t *testing.T) {
	checks := dynamicChecks([]lower.DynamicColumnMetadata{
		{Name: "HTAN_DATA_FILE_ID", DynamicName: "identifier_keys", SourceKey: "HTAN_DATA_FILE_ID", ValueType: "string"},
		{Name: "HTAN_DATA_FILE_ID", DynamicName: "category_keys", SourceKey: "HTAN_DATA_FILE_ID", ValueType: "string"},
	})
	if _, ok := checks["identifier_keys"]["HTAN_DATA_FILE_ID"]; !ok {
		t.Fatalf("identifier compatibility key was discarded: %#v", checks)
	}
	if _, ok := checks["category_keys"]["HTAN_DATA_FILE_ID"]; !ok {
		t.Fatalf("category compatibility key was discarded: %#v", checks)
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
	_, err := materializePostQueryRowWithChecks(row, checks)
	if err == nil {
		t.Fatal("unexpected dynamic key was accepted")
	}
	var drift *DynamicDriftError
	if !errors.As(err, &drift) || drift.DynamicName != "code" || drift.Key != "unexpected" || drift.FrozenKeyCount != 1 {
		t.Fatalf("dynamic drift = %#v, want code/unexpected/1", drift)
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

func TestMaterializePostQueryExtensionFamilyIgnoresSiblingKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"attachment_extension_keys__checksum_sha256": {
			"checksum_sha256": {ColumnName: "checksum_sha256", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{
		"checksum_sha256": "abc123",
		"__loom_dynamic_runtime_keys": map[string]any{
			"attachment_extension_keys__checksum_sha256": []any{"checksum_sha256", "sha256"},
		},
	}, checks)
	if err != nil {
		t.Fatal(err)
	}
	if row["checksum_sha256"] != "abc123" {
		t.Fatalf("projected extension value = %#v", row["checksum_sha256"])
	}
}

func TestMaterializePostQueryExtensionFamilyStillRejectsKnownKeyTypeDrift(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"attachment_extension_keys__checksum_sha256": {
			"checksum_sha256": {ColumnName: "checksum_sha256", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	_, err := materializePostQueryRowWithChecks(map[string]any{
		"checksum_sha256": 42,
		"__loom_dynamic_runtime_keys": map[string]any{
			"attachment_extension_keys__checksum_sha256": []any{"checksum_sha256", "sha256"},
		},
	}, checks)
	if err == nil {
		t.Fatal("known extension key type drift was accepted")
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
