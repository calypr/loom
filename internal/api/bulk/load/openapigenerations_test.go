package load

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataset"
)

func TestGenerationResponsesPreserveWireFields(t *testing.T) {
	status := generationStatusResponse(&GenerationStatusResult{
		Project: "project-a", Generation: "generation-a", State: dataset.StateStaged, Reusable: true,
	})
	assertJSON(t, status, "{\"generation\":\"generation-a\",\"project\":\"project-a\",\"reusable\":true,\"state\":\"STAGED\"}")

	activation := generationActivationResponse(&GenerationActivationResult{
		Project: "project-a", Generation: "generation-a", DataframeExecutionID: "execution-a", Activated: true,
	})
	assertJSON(t, activation, "{\"activated\":true,\"dataframeExecutionId\":\"execution-a\",\"generation\":\"generation-a\",\"project\":\"project-a\"}")
}

func assertJSON(t *testing.T, value any, expected string) {
	t.Helper()
	actual, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != expected {
		t.Fatalf("response JSON = %s, want %s", actual, expected)
	}
}
