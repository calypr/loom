package semantic

import (
	"encoding/json"
	"testing"
)

func TestSemanticCoverageBuildStateJSONRoundTrip(t *testing.T) {
	states := []SemanticCoverageBuildState{
		SemanticCoverageBuildBuilding,
		SemanticCoverageBuildFailed,
		SemanticCoverageBuildUnavailable,
		SemanticCoverageBuildComplete,
		SemanticCoverageBuildPartial,
		SemanticCoverageBuildMissing,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `"`+string(state)+`"` {
				t.Fatalf("encoded state = %s, want %q", encoded, state)
			}
			var decoded SemanticCoverageBuildState
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded != state {
				t.Fatalf("decoded state = %q, want %q", decoded, state)
			}
		})
	}
}
