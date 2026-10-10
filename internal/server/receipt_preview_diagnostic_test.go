package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestLogReceiptPreviewContractFailureIncludesInternalMismatchFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	err := &receiptPreviewResolutionError{
		ReceiptID: "receipt-123",
		Err:       contractMismatch("output_execution", "patients", "expected-fingerprint", "actual-fingerprint"),
	}

	logReceiptPreviewContractFailure(logger, "request-456", "receipt-123", "patients", err)
	logged := output.String()
	for _, want := range []string{
		"request_id=request-456",
		"receipt_id=receipt-123",
		"output_id=patients",
		"component=output_execution",
		"expected=expected-fingerprint",
		"actual=actual-fingerprint",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("diagnostic log missing %q: %s", want, logged)
		}
	}
}

func TestLogReceiptPreviewContractFailureDoesNotLogOtherPreviewErrors(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logReceiptPreviewContractFailure(logger, "request-456", "receipt-123", "patients", context.DeadlineExceeded)
	if output.Len() != 0 {
		t.Fatalf("non-contract preview error produced receipt diagnostic: %s", output.String())
	}
}

func TestLogReceiptPreviewContractFailureTagsRecipeContractViolation(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logReceiptPreviewContractFailure(logger, "request-456", "receipt-123", "patients", dataframeerrors.NewError(dataframeerrors.CodeRecipeContractViolation, "private cause"))
	logged := output.String()
	for _, want := range []string{"request_id=request-456", "receipt_id=receipt-123", "output_id=patients", "error_code=RECIPE_CONTRACT_VIOLATION"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("diagnostic log missing %q: %s", want, logged)
		}
	}
	if strings.Contains(logged, "expected=") || strings.Contains(logged, "actual=") {
		t.Fatalf("diagnostic invented fingerprint fields without a typed mismatch: %s", logged)
	}
}
