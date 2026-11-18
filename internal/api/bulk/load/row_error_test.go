package load

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	fhir "github.com/calypr/loom/generated/fhir"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/ingest"
)

func TestRowValidationResponseExplainsFileAndField(t *testing.T) {
	cause := &fhir.MissingRequiredFieldError{Field: "includedStructure"}
	err := fmt.Errorf("stage source: %w", &ingest.RowError{
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
	err := &ingest.RowError{File: "Patient.ndjson", Row: 2, ResourceType: "Patient", Category: "validation",
		Cause: errors.New("password=private patient-payload")}
	mapped := httpapi.MapDataframeError(normalizeError(err), "safe-row")
	if mapped.Status != http.StatusUnprocessableEntity || strings.Contains(mapped.Body.Error.Message, "private") ||
		strings.Contains(mapped.Body.Error.Message, "patient-payload") {
		t.Fatalf("unsafe response = %#v", mapped.Body)
	}
}

func TestInternalRowFailuresKeepServerStatus(t *testing.T) {
	for _, category := range []string{"generation", "edge"} {
		err := &ingest.RowError{File: "Patient.ndjson", Row: 2, ResourceType: "Patient", Category: category,
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
	mapped := httpapi.MapDataframeError(normalizeError(&ingest.SourcePublishLimitError{
		Limit: "document count", Maximum: 25000,
	}), "limit-test")
	if mapped.Status != http.StatusRequestEntityTooLarge ||
		!strings.Contains(mapped.Body.Error.Message, "document count") ||
		!strings.Contains(mapped.Body.Error.Message, "25000") ||
		mapped.Body.Error.Details["maximum"] != int64(25000) {
		t.Fatalf("response = %#v", mapped)
	}
}
