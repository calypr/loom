package load

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/fhir/model"
	httpapi "github.com/calypr/loom/internal/api/http"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/generation"
	"github.com/calypr/loom/internal/ingest/row"
	"github.com/calypr/loom/internal/ingest/source"
	"github.com/calypr/loom/internal/ingest/sourceload"
)

func TestRowValidationResponseExplainsFileAndField(t *testing.T) {
	cause := &fhir.MissingRequiredFieldError{Field: "includedStructure"}
	err := fmt.Errorf("stage source: %w", &row.RowError{
		File: "BodyStructure.ndjson", Row: 1, ResourceType: "BodyStructure", Category: "validation", Cause: cause,
	})
	mapped := httpapi.MapDataframeError(normalizeError(err), "row-test")
	if mapped.Status != http.StatusUnprocessableEntity || mapped.Body.Error.Code != "INVALID_DATA" {
		t.Fatalf("response = %#v", mapped)
	}
	for _, text := range []string{"BodyStructure.ndjson", "row 1", "includedStructure"} {
		if !strings.Contains(mapped.Body.Error.Message, text) {
			t.Errorf("message %q omits %q", mapped.Body.Error.Message, text)
		}
	}
	if !errors.Is(mapped.Cause, cause) || mapped.Body.Error.RequestID != "row-test" {
		t.Fatal("cause or request ID lost")
	}
}

func TestRowValidationResponseDoesNotExposeOpaqueCause(t *testing.T) {
	err := &row.RowError{File: "Patient.ndjson", Row: 2, ResourceType: "Patient", Category: "validation",
		Cause: errors.New("password=private patient-payload")}
	mapped := httpapi.MapDataframeError(normalizeError(err), "safe-row")
	if mapped.Status != http.StatusUnprocessableEntity || strings.Contains(mapped.Body.Error.Message, "private") ||
		strings.Contains(mapped.Body.Error.Message, "patient-payload") {
		t.Fatalf("unsafe response = %#v", mapped.Body)
	}
}

func TestInternalRowFailuresKeepServerStatus(t *testing.T) {
	for _, category := range []string{"generation", "edge"} {
		err := &row.RowError{File: "Patient.ndjson", Row: 2, ResourceType: "Patient", Category: category,
			Cause: errors.New("internal invariant failed")}
		mapped := httpapi.MapDataframeError(normalizeError(err), "internal-row")
		if mapped.Status != http.StatusInternalServerError || mapped.Body.Error.Code != "INTERNAL_ERROR" ||
			!strings.Contains(mapped.Body.Error.Message, "Patient.ndjson row 2") ||
			strings.Contains(mapped.Body.Error.Message, "Check this row") {
			t.Fatalf("internal error misclassified: %#v", mapped)
		}
	}
}

func TestSourceLimitResponseNamesItsBudget(t *testing.T) {
	mapped := httpapi.MapDataframeError(normalizeError(&sourceload.SourcePublishLimitError{
		Limit: "document count", Maximum: 25000,
	}), "limit-test")
	if mapped.Status != http.StatusRequestEntityTooLarge ||
		!strings.Contains(mapped.Body.Error.Message, "document count") ||
		!strings.Contains(mapped.Body.Error.Message, "25000") ||
		mapped.Body.Error.Details["maximum"] != int64(25000) {
		t.Fatalf("response = %#v", mapped)
	}
}

func TestNormalizeIngestionFailures(t *testing.T) {
	preflight := &source.PreflightError{Report: source.PreflightReport{Issues: []source.PreflightIssue{{Code: "invalid_json", File: "Patient.ndjson", ResourceType: "Patient", Row: 2, Message: "raw parser detail"}}}}
	userErr, ok := dataframeerrors.AsUserError(normalizeError(preflight))
	if !ok || userErr.Code() != "INGEST_PREFLIGHT_FAILED" || userErr.Retryable() {
		t.Fatalf("preflight normalized = %#v", userErr)
	}
	if got := userErr.Details()["issues"].([]any)[0].(map[string]any); got["message"] != nil {
		t.Fatal("preflight parser message leaked into details")
	}

	incomplete := &generation.GenerationLoadIncompleteError{ValidationErrors: 2, GenerationErrors: 1, EdgeErrors: 3}
	userErr, ok = dataframeerrors.AsUserError(normalizeError(incomplete))
	if !ok || userErr.Code() != "GENERATION_LOAD_INCOMPLETE" || userErr.Details()["edgeErrors"] != 3 {
		t.Fatalf("incomplete normalized = %#v", userErr)
	}

	activation := &generation.ActivationOutcomeError{Err: errors.New("pointer storage timeout")}
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
