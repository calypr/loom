package httpapi

import (
	"errors"
	"net/http"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/gofiber/fiber/v3"
)

func TestMapDataframeErrorRedactsUnknownCause(t *testing.T) {
	mapped := MapDataframeError(errors.New("arango password=secret collection=private"), "req-1")
	if mapped.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", mapped.Status)
	}
	if mapped.Body.Error.Code != "INTERNAL_ERROR" || mapped.Body.Error.Message != "internal server error" {
		t.Fatalf("body = %#v", mapped.Body.Error)
	}
	if mapped.Body.Error.RequestID != "req-1" {
		t.Fatalf("request id = %q", mapped.Body.Error.RequestID)
	}
}

func TestMapDataframeErrorBackendIsRetryable(t *testing.T) {
	err := dataframeerrors.Wrap(errors.New("clickhouse tcp://secret"), dataframeerrors.CodeBackendUnavailable, "")
	mapped := MapDataframeError(err, "req-2")
	if mapped.Status != http.StatusServiceUnavailable || mapped.Body.Error.Code != "BACKEND_UNAVAILABLE" || !mapped.Body.Error.Retryable {
		t.Fatalf("mapped = %#v", mapped)
	}
	if mapped.Body.Error.Message == "clickhouse tcp://secret" {
		t.Fatal("backend cause leaked into response")
	}
}

func TestMapDataframeErrorPreviewClassifications(t *testing.T) {
	for _, test := range []struct {
		code      dataframeerrors.ErrorCode
		status    int
		retryable bool
	}{
		{dataframeerrors.CodePlanTooExpensive, http.StatusTooManyRequests, false},
		{dataframeerrors.CodeReceiptStoreUnavailable, http.StatusServiceUnavailable, true},
		{dataframeerrors.CodePreviewTimeout, http.StatusGatewayTimeout, true},
		{dataframeerrors.CodePreviewResponseTooLarge, http.StatusRequestEntityTooLarge, false},
		{dataframeerrors.CodeQueryMemoryLimitExceeded, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeQueryResourceLimitExceeded, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeQueryBackendOutOfMemory, http.StatusServiceUnavailable, false},
		{dataframeerrors.CodeDynamicSchemaDrift, http.StatusConflict, false},
		{dataframeerrors.CodeRecipeContractViolation, http.StatusConflict, false},
	} {
		mapped := MapDataframeError(dataframeerrors.NewError(test.code, "private", dataframeerrors.WithRetryable(test.retryable)), "req-preview")
		if mapped.Status != test.status || mapped.Body.Error.Code != string(test.code) || mapped.Body.Error.Retryable != test.retryable {
			t.Errorf("%s mapped = %#v", test.code, mapped)
		}
		if mapped.Body.Error.Message == "private" {
			t.Errorf("%s leaked private message", test.code)
		}
	}
}

func TestMapDataframeErrorFiberStatus(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
		want int
	}{
		{fiber.ErrNotFound, "NOT_FOUND", http.StatusNotFound},
		{fiber.ErrMethodNotAllowed, "METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed},
		{fiber.ErrRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", http.StatusRequestEntityTooLarge},
	} {
		mapped := MapDataframeError(test.err, "")
		if mapped.Status != test.want || mapped.Body.Error.Code != test.code {
			t.Errorf("%v => %#v, want %s/%d", test.err, mapped, test.code, test.want)
		}
	}
}

func TestGenerationFileErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{dataframeerrors.NewError(dataframeerrors.CodeInvalidGenerationFile, ""), "INVALID_GENERATION_FILE"},
		{dataframeerrors.NewError(dataframeerrors.CodeDuplicateGenerationFile, ""), "DUPLICATE_GENERATION_FILE"},
	} {
		mapped := MapDataframeError(test.err, "req-file")
		if mapped.Status != http.StatusBadRequest || mapped.Body.Error.Code != test.code || mapped.Body.Error.Retryable {
			t.Errorf("mapped = %#v, want %s/400", mapped, test.code)
		}
	}
}

func TestMapDataframeErrorPreservesLimitAndPublicationCodes(t *testing.T) {
	for _, test := range []struct {
		code      dataframeerrors.ErrorCode
		status    int
		retryable bool
	}{
		{dataframeerrors.CodeInvalidLimit, http.StatusBadRequest, false},
		{dataframeerrors.CodePublicationFailed, http.StatusServiceUnavailable, true},
	} {
		mapped := MapDataframeError(dataframeerrors.NewError(test.code, ""), "req-publication")
		if mapped.Status != test.status || mapped.Body.Error.Code != string(test.code) || mapped.Body.Error.Retryable != test.retryable {
			t.Errorf("%s mapped = %#v, want %s/%d retryable=%t", test.code, mapped, test.code, test.status, test.retryable)
		}
	}
}
