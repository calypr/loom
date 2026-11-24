package semantic

import "testing"

func TestStringValueAcceptsOnlyStrings(t *testing.T) {
	if got := StringValue("value"); got != "value" {
		t.Fatalf("StringValue(string) = %q", got)
	}
	for _, value := range []any{nil, 42, true} {
		if got := StringValue(value); got != "" {
			t.Errorf("StringValue(%T) = %q, want empty", value, got)
		}
	}
}
