package load

import (
	"errors"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
)

func TestNormalizeIngestionFailures(t *testing.T) {
	preflight := &ingest.PreflightError{Report: ingest.PreflightReport{Issues: []ingest.PreflightIssue{{Code: "invalid_json", File: "Patient.ndjson", ResourceType: "Patient", Row: 2, Message: "raw parser detail"}}}}
	userErr, ok := dataframeerrors.AsUserError(NormalizeError(preflight))
	if !ok || userErr.Code() != "INGEST_PREFLIGHT_FAILED" || userErr.Retryable() {
		t.Fatalf("preflight normalized = %#v", userErr)
	}
	if got := userErr.Details()["issues"].([]any)[0].(map[string]any); got["message"] != nil {
		t.Fatal("preflight parser message leaked into details")
	}

	incomplete := &ingest.GenerationLoadIncompleteError{ValidationErrors: 2, GenerationErrors: 1, EdgeErrors: 3}
	userErr, ok = dataframeerrors.AsUserError(NormalizeError(incomplete))
	if !ok || userErr.Code() != "GENERATION_LOAD_INCOMPLETE" || userErr.Details()["edgeErrors"] != 3 {
		t.Fatalf("incomplete normalized = %#v", userErr)
	}

	activation := &ingest.ActivationOutcomeError{Err: errors.New("pointer storage timeout")}
	userErr, ok = dataframeerrors.AsUserError(NormalizeError(activation))
	if !ok || userErr.Code() != "GENERATION_ACTIVATION_UNKNOWN" || userErr.Retryable() {
		t.Fatalf("activation normalized = %#v", userErr)
	}
}
