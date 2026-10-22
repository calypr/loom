package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

func TestExplorerLegacyResponseUnknownErrorIsDescriptive(t *testing.T) {
	private := errors.New("private socket detail")
	status, response := explorerLegacyResponse(private)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	body, err := response.Error.AsLegacyErrorBody()
	if err != nil {
		t.Fatal(err)
	}
	if body.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q, want INTERNAL_ERROR", body.Code)
	}
	if !strings.Contains(body.Message, "unexpected internal error") || !strings.Contains(body.Message, "request ID") {
		t.Fatalf("message = %q, want descriptive internal-error guidance", body.Message)
	}
	if strings.Contains(body.Message, private.Error()) {
		t.Fatalf("response leaked private cause: %q", body.Message)
	}
}

func TestExplorerLegacyResponseFillsEmptyInternalMessage(t *testing.T) {
	status, response := explorerLegacyResponse(legacyOperationFailure(http.StatusInternalServerError, "", "", nil))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	body, err := response.Error.AsLegacyErrorBody()
	if err != nil {
		t.Fatal(err)
	}
	if body.Code != "INTERNAL_ERROR" || !strings.Contains(body.Message, "unexpected internal error") {
		t.Fatalf("body = %#v, want descriptive INTERNAL_ERROR", body)
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
