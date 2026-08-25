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
	"github.com/gofiber/fiber/v3"
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
	err = encoder.Visit(map[string]any{"value": strings.Repeat("x", 2048)})
	if !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v", err, ErrPreviewResponseTooLarge)
	}
	if _, err := encodeExplorerPreviewResponse(&explorer.CompilationReceipt{ID: "receipt"}, "output", nil, []map[string]any{{"value": strings.Repeat("x", 2048)}}, 1024); !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("legacy encode error = %v, want %v", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewRouteFailurePreservesStableClassifications(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "PREVIEW_TIMEOUT"},
		{"canceled", context.Canceled, 499, "CLIENT_CANCELED"},
		{"oversized", &previewResponseTooLargeError{Limit: 32}, http.StatusRequestEntityTooLarge, "PREVIEW_RESPONSE_TOO_LARGE"},
		{"plan", dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "private"), http.StatusTooManyRequests, "PLAN_TOO_EXPENSIVE"},
		{"backend", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "private", dataframeerrors.WithRetryable(true)), http.StatusServiceUnavailable, "BACKEND_UNAVAILABLE"},
		{"receipt", &receiptPreviewResolutionError{Err: errors.New("private")}, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED"},
		{"unknown", errors.New("private"), http.StatusInternalServerError, "PREVIEW_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error { return previewRouteFailure(c, test.err) })
			response := requestJSON(t, app, http.MethodGet, "/", "")
			if response.StatusCode != test.status || !strings.Contains(response.Body, `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s, want status=%d code=%s", response.StatusCode, response.Body, test.status, test.code)
			}
			if strings.Contains(response.Body, "private") {
				t.Fatalf("private cause leaked: %s", response.Body)
			}
		})
	}
}
