package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
)

func TestPreviewResponseEncoderProducesAtomicContract(t *testing.T) {
	receipt := &explorer.CompilationReceipt{ID: "same-id"}
	columns := []explorer.EmittedColumn{{EmissionID: "emission", OutputID: "same-id", PublicColumn: "public", LogicalType: "string"}}
	encoder, err := newPreviewResponseEncoder(receipt, "same-id", columns, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{"public": "value"}); err != nil {
		t.Fatal(err)
	}
	raw, err := encoder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ReceiptID string                   `json:"receiptId"`
		OutputID  string                   `json:"outputId"`
		Rows      []map[string]any         `json:"rows"`
		RowCount  int                      `json:"rowCount"`
		Columns   []explorer.EmittedColumn `json:"columns"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	if decoded.ReceiptID != receipt.ID || decoded.OutputID != "same-id" || decoded.RowCount != 1 || len(decoded.Rows) != 1 || len(decoded.Columns) != 1 {
		t.Fatalf("response = %#v", decoded)
	}
}

func TestPreviewResponseEncoderRejectsOverflowWithoutResult(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&explorer.CompilationReceipt{ID: "receipt"}, "output", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{"value": strings.Repeat("x", 2048)}); !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewErrorPreservesStableClassifications(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "PREVIEW_TIMEOUT"},
		{"canceled", context.Canceled, 499, "CLIENT_CANCELED"},
		{"oversized", &previewResponseTooLargeError{Limit: 32}, http.StatusRequestEntityTooLarge, "RESPONSE_TOO_LARGE"},
		{"plan", dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "private"), http.StatusTooManyRequests, "PLAN_TOO_EXPENSIVE"},
		{"backend", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "private", dataframeerrors.WithRetryable(true)), http.StatusServiceUnavailable, "BACKEND_UNAVAILABLE"},
		{"memory-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryMemoryLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_MEMORY_LIMIT_EXCEEDED"},
		{"resource-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_RESOURCE_LIMIT_EXCEEDED"},
		{"out-of-memory", dataframeerrors.NewError(dataframeerrors.CodeQueryBackendOutOfMemory, "private"), http.StatusServiceUnavailable, "QUERY_BACKEND_OUT_OF_MEMORY"},
		{"receipt", &receiptPreviewResolutionError{ReceiptID: "receipt-1", Err: contractMismatch("output_execution", "patients", "private-expected", "private-actual")}, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED"},
		{"unknown", errors.New("private"), http.StatusInternalServerError, "PREVIEW_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got *explorer.AuthoringError
			if !errors.As(previewRouteError(test.err), &got) || got.Status != test.status || got.Diagnostic.Code != test.code {
				t.Fatalf("error=%#v, want status=%d code=%s", got, test.status, test.code)
			}
		})
	}
}

func TestClassifyReceiptPreviewResolutionErrorOnlyMarksContractFailuresForRecompile(t *testing.T) {
	mismatch := classifyReceiptPreviewResolutionError("receipt-1", contractMismatch("output_execution", "patients", "expected", "actual"))
	var resolution *receiptPreviewResolutionError
	if !errors.As(mismatch, &resolution) || resolution.ReceiptID != "receipt-1" {
		t.Fatalf("mismatch classification = %#v", mismatch)
	}
	var authoring *explorer.AuthoringError
	if !errors.As(previewRouteError(mismatch), &authoring) || authoring.Status != http.StatusConflict {
		t.Fatalf("mismatch route error = %#v", authoring)
	}
	if got := authoring.Diagnostic.Details["receiptId"]; got != "receipt-1" {
		t.Fatalf("receiptId detail = %#v", got)
	}
	if got := authoring.Diagnostic.Details["outputId"]; got != "patients" {
		t.Fatalf("outputId detail = %#v", got)
	}

	compileFailure := classifyReceiptPreviewResolutionError("receipt-1", errors.New("compiler failed"))
	if errors.As(compileFailure, &resolution) {
		t.Fatalf("ordinary compiler failure was classified as a receipt mismatch: %v", compileFailure)
	}
	if !errors.As(previewRouteError(compileFailure), &authoring) || authoring.Status != http.StatusInternalServerError || authoring.Diagnostic.Code != "PREVIEW_FAILED" {
		t.Fatalf("compiler route error = %#v", authoring)
	}
}
