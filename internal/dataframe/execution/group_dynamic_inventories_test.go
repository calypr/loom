package execution

import (
	"errors"
	"testing"
)

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
