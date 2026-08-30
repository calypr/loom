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
		{"receipt", &receiptPreviewResolutionError{Err: contractMismatch("output_execution", "patients", "private-expected", "private-actual")}, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED"},
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
