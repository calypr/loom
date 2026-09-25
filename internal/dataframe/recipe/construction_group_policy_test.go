package recipe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstructionGroupMissingKeyPolicyDefaultsAndPersists(t *testing.T) {
	var legacy ConstructionGroup
	if err := json.Unmarshal([]byte(`{"constructionId":"group","keys":[{"inputColumnId":"input","outputColumnId":"output"}]}`), &legacy); err != nil {
		t.Fatalf("decode legacy group without missingKeyPolicy: %v", err)
	}
	if legacy.MissingKeyPolicy != ConstructionGroupMissingKeyGroup {
		t.Fatalf("legacy missingKeyPolicy = %q, want GROUP", legacy.MissingKeyPolicy)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy group: %v", err)
	}
	if !strings.Contains(string(encoded), `"missingKeyPolicy":"GROUP"`) {
		t.Fatalf("legacy group did not persist explicit GROUP policy: %s", encoded)
	}

	input := map[string]StageColumn{"input": {ID: "input"}}
	output := map[string]StageColumn{"output": {ID: "output"}}
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}); err != nil {
		t.Fatalf("validate legacy group: %v", err)
	}
	legacy.MissingKeyPolicy = "SILENT_DEFAULT"
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}); err == nil || !strings.Contains(err.Error(), "missingKeyPolicy") {
		t.Fatalf("unsupported missingKeyPolicy error = %v", err)
	}
}
