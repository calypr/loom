package load

import (
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/dataset"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
)

func TestNormalizeIngestionFailures(t *testing.T) {
	preflight := &ingest.PreflightError{Report: ingest.PreflightReport{Issues: []ingest.PreflightIssue{{Code: "invalid_json", File: "Patient.ndjson", ResourceType: "Patient", Row: 2, Message: "raw parser detail"}}}}
	userErr, ok := dataframeerrors.AsUserError(normalizeError(preflight))
	if !ok || userErr.Code() != "INGEST_PREFLIGHT_FAILED" || userErr.Retryable() {
		t.Fatalf("preflight normalized = %#v", userErr)
	}
	if got := userErr.Details()["issues"].([]any)[0].(map[string]any); got["message"] != nil {
		t.Fatal("preflight parser message leaked into details")
	}

	incomplete := &ingest.GenerationLoadIncompleteError{ValidationErrors: 2, GenerationErrors: 1, EdgeErrors: 3}
	userErr, ok = dataframeerrors.AsUserError(normalizeError(incomplete))
	if !ok || userErr.Code() != "GENERATION_LOAD_INCOMPLETE" || userErr.Details()["edgeErrors"] != 3 {
		t.Fatalf("incomplete normalized = %#v", userErr)
	}

	activation := &ingest.ActivationOutcomeError{Err: errors.New("pointer storage timeout")}
	userErr, ok = dataframeerrors.AsUserError(normalizeError(activation))
	if !ok || userErr.Code() != "GENERATION_ACTIVATION_UNKNOWN" || userErr.Retryable() {
		t.Fatalf("activation normalized = %#v", userErr)
	}
}

func TestNormalizeNotReadyActivationStateConflict(t *testing.T) {
	for _, wrapped := range []error{
		fmt.Errorf("activate generation: %w: gen-a is loading", dataset.ErrGenerationNotReady),
		fmt.Errorf("%w: gen-a is failed", dataset.ErrGenerationNotReady),
	} {
		userErr, ok := dataframeerrors.AsUserError(normalizeError(wrapped))
		if !ok {
			t.Fatalf("not-ready activation state conflict escaped untyped (route serves opaque 500 despite documented 409 arm): %v", wrapped)
		}
		if userErr.Code() != "GENERATION_NOT_READY" || userErr.Retryable() {
			t.Fatalf("not-ready normalized = code %s retryable %v", userErr.Code(), userErr.Retryable())
		}
	}
}
