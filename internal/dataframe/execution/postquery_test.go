package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/google/uuid"
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

func TestMaterializePostQueryAllowsExcludedFrozenKeysButRejectsNewKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category": {
			"visible":  {ColumnName: "category_visible", ValueType: "string"},
			"excluded": {ColumnName: ""},
		},
	}
	row := map[string]any{
		"category_visible":            "value",
		"__loom_dynamic_runtime_keys": map[string]any{"category": []any{"visible", "excluded"}},
	}
	if _, err := materializePostQueryRowWithChecks(row, checks); err != nil {
		t.Fatalf("known excluded key was treated as schema drift: %v", err)
	}

	row["__loom_dynamic_runtime_keys"] = map[string]any{"category": []any{"visible", "excluded", "new"}}
	var drift *DynamicDriftError
	if _, err := materializePostQueryRowWithChecks(row, checks); !errors.As(err, &drift) || drift.Key != "new" || drift.FrozenKeyCount != 2 {
		t.Fatalf("new key drift = %#v, want new key with two frozen keys", err)
	}
}

func TestMaterializePostQueryValidatesAllExcludedFamilyInventory(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category": {"a": {ColumnName: ""}, "b": {ColumnName: ""}},
	}
	row := map[string]any{"__loom_dynamic_runtime_keys": map[string]any{"category": []any{"a", "b"}}}
	if _, err := materializePostQueryRowWithChecks(row, checks); err != nil {
		t.Fatalf("all-excluded known keys were rejected: %v", err)
	}
	row["__loom_dynamic_runtime_keys"] = map[string]any{"category": []any{"new"}}
	var drift *DynamicDriftError
	if _, err := materializePostQueryRowWithChecks(row, checks); !errors.As(err, &drift) || drift.Key != "new" || drift.FrozenKeyCount != 2 {
		t.Fatalf("all-excluded new key drift = %#v", err)
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

func TestMaterializePostQueryValidatesEveryPresentColumnWhenRuntimeKeysCoverAnotherFamily(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category_keys": {"stage": {ColumnName: "category_stage", ValueType: "string"}},
		"identifier_keys": {
			"count": {ColumnName: "identifier_count", ValueType: "integer"},
			"tags":  {ColumnName: "identifier_tags", ValueType: "string", Many: true},
		},
	}

	newRow := func(dynamicValues map[string]any) map[string]any {
		row := map[string]any{
			"category_stage":              "stage-a",
			"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
		}
		for key, value := range dynamicValues {
			row[key] = value
		}
		return row
	}

	mismatches := map[string]map[string]any{
		"integer mismatch": newRow(map[string]any{"identifier_count": "not-an-integer"}),
		"array mismatch":   newRow(map[string]any{"identifier_tags": "tag"}),
		"both mismatch":    newRow(map[string]any{"identifier_count": "not-an-integer", "identifier_tags": []any{"tag", 42}}),
	}
	for name, row := range mismatches {
		if _, err := materializePostQueryRowWithChecks(row, checks); err == nil {
			t.Fatalf("%s in an unenumerated family was accepted: %#v", name, row)
		}
	}

	valid := newRow(map[string]any{"identifier_count": 7, "identifier_tags": []any{"tag", "other"}})
	if _, err := materializePostQueryRowWithChecks(valid, checks); err != nil {
		t.Fatalf("valid row across both families rejected: %v", err)
	}

	if _, err := materializePostQueryRowWithChecks(newRow(map[string]any{"identifier_count": 7}), checks); err != nil {
		t.Fatalf("valid row with a missing unselected column rejected: %v", err)
	}
}

func TestMaterializePostQueryValidatesPresentColumnsWithoutRuntimeKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"category_keys": {"stage": {ColumnName: "category_stage", ValueType: "string", AllowUnknownKeys: true}},
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"category_stage": "stage-a"}, checks); err != nil {
		t.Fatalf("valid row without runtime keys rejected: %v", err)
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"category_stage": 42}, checks); err == nil {
		t.Fatal("type drift without runtime keys was accepted")
	}
}

func TestOutputStreamRejectsMalformedRowFromUnenumeratedDynamicFamily(t *testing.T) {
	newStream := func(rows ...map[string]any) OutputStream {
		index := 0
		return OutputStream{
			Name:    "Condition",
			Columns: []string{"condition", "category_stage", "identifier_count"},
			DynamicChecks: map[string]map[string]DynamicColumnCheck{
				"category_keys":   {"stage": {ColumnName: "category_stage", ValueType: "string"}},
				"identifier_keys": {"count": {ColumnName: "identifier_count", ValueType: "integer"}},
			},
			stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				if err := visit(rows[index]); err != nil {
					return err
				}
				index++
				return nil
			},
		}
	}
	malformed := map[string]any{
		"__loom_row_id":               "row-1",
		"condition":                   "C50.9",
		"category_stage":              "stage-a",
		"identifier_count":            "not-an-integer",
		"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
	}
	var visited []map[string]any
	if _, err := newStream(malformed).Stream(context.Background(), func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	}); err == nil {
		t.Fatal("malformed row from an unenumerated dynamic family reached the public visitor")
	}
	if len(visited) != 0 {
		t.Fatalf("public visitor received malformed rows: %#v", visited)
	}

	valid := map[string]any{
		"__loom_row_id":               "row-2",
		"condition":                   "C50.9",
		"category_stage":              "stage-a",
		"identifier_count":            float64(7),
		"__loom_dynamic_runtime_keys": map[string]any{"category_keys": []any{"stage"}},
	}
	if _, err := newStream(valid).Stream(context.Background(), func(row map[string]any) error {
		visited = append(visited, row)
		return nil
	}); err != nil {
		t.Fatalf("valid stream: %v", err)
	}
	if len(visited) != 1 || visited[0]["identifier_count"] != float64(7) {
		t.Fatalf("public rows = %#v", visited)
	}
	if _, ok := visited[0]["__loom_dynamic_runtime_keys"]; ok {
		t.Fatalf("public row leaked dynamic runtime keys: %#v", visited[0])
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

func TestMaterializePostQueryAllowsFixedLookupAfterConstructionDropsRuntimeKeys(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"condition_identifier": {
			"https://cda.readthedocs.io/diagnosis": {ColumnName: "diagnosis", ValueType: "string", AllowUnknownKeys: true},
		},
	}
	row, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": "C50.9"}, checks)
	if err != nil || row["diagnosis"] != "C50.9" {
		t.Fatalf("fixed lookup after construction = %#v, %v", row, err)
	}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": 42}, checks); err == nil {
		t.Fatal("fixed lookup with wrong value type was accepted")
	}
	checks["frozen"] = map[string]DynamicColumnCheck{"a": {ColumnName: "frozen_a", ValueType: "string"}}
	if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": "C50.9"}, checks); err == nil {
		t.Fatal("frozen dynamic family without runtime keys was accepted")
	}
}

func TestMaterializePostQueryValidatesAllValuesAfterConstruction(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{
		"condition_identifier": {
			"https://cda.readthedocs.io/diagnosis": {ColumnName: "diagnosis", ValueType: "string", Many: true, AllowUnknownKeys: true},
		},
	}
	for _, values := range []any{[]any{}, []any{"C50.9", "C50.8"}} {
		if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": values}, checks); err != nil {
			t.Fatalf("valid diagnosis list %#v rejected: %v", values, err)
		}
	}
	for _, values := range []any{"C50.9", nil, []any{"C50.9", 42}} {
		if _, err := materializePostQueryRowWithChecks(map[string]any{"diagnosis": values}, checks); err == nil {
			t.Fatalf("invalid diagnosis list %#v accepted", values)
		}
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

func TestGroupedDynamicInventoriesValidateEveryContributor(t *testing.T) {
	checks := map[string]map[string]DynamicColumnCheck{"identifier": {"known": {ColumnName: "identifier_known", ValueType: "string"}}}
	inventory := func(keys any) map[string]any { return map[string]any{"identifier": keys} }
	for _, tc := range []struct {
		name                 string
		metadata             any
		wantError, wantDrift bool
	}{
		{name: "known members", metadata: []any{inventory([]any{"known"}), inventory([]any{})}},
		{name: "nested groups", metadata: []any{[]any{inventory([]any{"known"})}, []any{inventory([]any{})}}},
		{name: "empty group", metadata: []any{}},
		{name: "earlier unknown survives later known", metadata: []any{inventory([]any{"unexpected"}), inventory([]any{"known"})}, wantError: true, wantDrift: true},
		{name: "nested earlier unknown", metadata: []any{[]any{inventory([]any{"unexpected"})}, []any{inventory([]any{"known"})}}, wantError: true, wantDrift: true},
		{name: "missing earlier inventory", metadata: []any{nil, inventory([]any{"known"})}, wantError: true},
		{name: "malformed earlier inventory", metadata: []any{inventory("known"), inventory([]any{"known"})}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := materializePostQueryRowWithChecks(map[string]any{"row_count": 2, "__loom_dynamic_runtime_keys": tc.metadata}, checks)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%t", err, tc.wantError)
			}
			if tc.wantDrift {
				var drift *DynamicDriftError
				if !errors.As(err, &drift) || drift.Key != "unexpected" {
					t.Fatalf("wrong drift error: %v", err)
				}
			}
			if err == nil {
				if _, leaked := row["__loom_dynamic_runtime_keys"]; leaked {
					t.Fatal("hidden inventory exposed")
				}
			}
		})
	}
}

func TestComputeNamedUUID(t *testing.T) {
	name := "example"
	for _, operation := range []string{"uuid3", "uuid5"} {
		got, err := computeNamedUUID(operation, []any{uuid.NameSpaceDNS.String(), name})
		if err != nil || got == nil {
			t.Fatalf("%s: got %v, err %v", operation, got, err)
		}
	}
	got, err := computeNamedUUID("uuid5", []any{"textual", name})
	if err != nil || got == nil {
		t.Fatalf("text namespace: got %v, err %v", got, err)
	}
	if got, err := computeNamedUUID("uuid5", []any{uuid.NameSpaceDNS.String(), nil}); err != nil || got != nil {
		t.Fatalf("nil propagation: got %v, err %v", got, err)
	}
	if _, err := computeNamedUUID("uuid5", []any{"only"}); err == nil {
		t.Fatal("expected insufficient arguments error")
	}
	if _, err := computeNamedUUID("uuid4", []any{"ns", name}); err == nil {
		t.Fatal("expected unsupported operation error")
	}
}
