package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestExplorerErrorResponsePreservesLifecycleDetails(t *testing.T) {
	status, response := explorerErrorResponse(legacyOperationFailureDetails(http.StatusConflict, "COMPILATION_FAILED", "catalog field is unavailable", map[string]any{"retryable": false}, nil))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if response.Error.Code != "COMPILATION_FAILED" || response.Error.Message != "catalog field is unavailable" {
		t.Fatalf("error = %#v", response.Error)
	}
	if response.Error.Details == nil || (*response.Error.Details)["retryable"] != false {
		t.Fatalf("details = %#v, want explicit retryable=false", response.Error.Details)
	}
}

func TestExplorerLegacyResponseUnknownErrorIsDescriptive(t *testing.T) {
	private := errors.New("private socket detail")
	status, response := explorerErrorResponse(private)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q, want INTERNAL_ERROR", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, "unexpected internal error") || !strings.Contains(response.Error.Message, "request ID") {
		t.Fatalf("message = %q, want descriptive internal-error guidance", response.Error.Message)
	}
	if strings.Contains(response.Error.Message, private.Error()) {
		t.Fatalf("response leaked private cause: %q", response.Error.Message)
	}
}

func TestExplorerLegacyResponseFillsEmptyInternalMessage(t *testing.T) {
	status, response := explorerErrorResponse(legacyOperationFailure(http.StatusInternalServerError, "", "", nil))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" || !strings.Contains(response.Error.Message, "unexpected internal error") {
		t.Fatalf("body = %#v, want descriptive INTERNAL_ERROR", response.Error)
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
	if response.Error.Details == nil {
		t.Fatal("error omitted publication details")
	}
	if got := (*response.Error.Details)["executionId"]; got != "execution-a" {
		t.Fatalf("executionId = %v, want execution-a", got)
	}
	if got := (*response.Error.Diagnostic.Details)["retryable"]; got != true {
		t.Fatalf("retryable = %v, want true", got)
	}
}
