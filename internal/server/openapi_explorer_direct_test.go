package server

import (
	"context"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestLegacyErrorMessagePreservesStructuredLifecycleMessage(t *testing.T) {
	var response loomapi.LegacyErrorResponse
	if err := response.Error.FromLegacyErrorBody(loomapi.LegacyErrorBody{Code: "COMPILATION_FAILED", Message: "catalog field is unavailable"}); err != nil {
		t.Fatal(err)
	}
	if got := legacyErrorMessage(response); got != "catalog field is unavailable" {
		t.Fatalf("legacy error message = %q", got)
	}
}

func TestAuthoringErrorMapsPublicationInProgressToConflict(t *testing.T) {
	status, response := authoringErrorForOpenAPI(context.Background(), "publish", &lifecycle.Error{
		Class:   lifecycle.ClassConflict,
		Stage:   "materialize",
		Code:    "PUBLICATION_IN_PROGRESS",
		Message: "Explorer publication is already in progress; retry after it completes",
		Details: map[string]any{"executionId": "execution-a", "retryable": true},
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if response.Error.Code != "PUBLICATION_IN_PROGRESS" || response.Error.Diagnostic == nil {
		t.Fatalf("error response = %#v", response)
	}
	if got := response.Error.AdditionalProperties["details"].(map[string]any)["executionId"]; got != "execution-a" {
		t.Fatalf("executionId = %v, want execution-a", got)
	}
	if got := (*response.Error.Diagnostic.Details)["retryable"]; got != true {
		t.Fatalf("retryable = %v, want true", got)
	}
}
