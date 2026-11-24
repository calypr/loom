package compiler

import (
	"encoding/json"
	"testing"
)

func decodeCompilerOccurrenceKey(t *testing.T, label string, raw any, want string) map[string]any {
	t.Helper()
	if key, ok := raw.(map[string]any); ok {
		return key
	}
	if encoded, ok := raw.(string); ok {
		var key map[string]any
		if err := json.Unmarshal([]byte(encoded), &key); err == nil {
			return key
		}
	}
	t.Fatalf("%s occurrence key = %#v, want %s", label, raw, want)
	return nil
}
