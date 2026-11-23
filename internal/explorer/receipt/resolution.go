package receipt

import (
	"errors"
	"net/http"

	"github.com/calypr/loom/internal/explorer"
)

type PreviewResolutionError struct {
	ReceiptID string
	Err       error
}

func (e *PreviewResolutionError) Error() string { return e.Err.Error() }
func (e *PreviewResolutionError) Unwrap() error { return e.Err }

func ClassifyPreviewResolutionError(receiptID string, err error) error {
	var mismatch *ContractMismatch
	if errors.As(err, &mismatch) || errors.Is(err, explorer.ErrReceiptRecompileRequired) {
		return &PreviewResolutionError{ReceiptID: receiptID, Err: err}
	}
	return err
}

func PreviewConflict(cause error) error {
	receiptID := ""
	var resolution *PreviewResolutionError
	if errors.As(cause, &resolution) {
		receiptID = resolution.ReceiptID
	}
	return &explorer.AuthoringError{
		Status: http.StatusConflict,
		Diagnostic: explorer.AuthoringDiagnostic{
			Severity: "ERROR",
			Stage:    "preview",
			Code:     "RECEIPT_RECOMPILE_REQUIRED",
			Message:  "receipt deterministic lowering no longer matches the stored artifact",
			Details:  MismatchDetails(receiptID, cause),
		},
		Cause: cause,
	}
}

func CategoryScanResolutionError(receiptID string, err error) error {
	classified := ClassifyPreviewResolutionError(receiptID, err)
	var resolution *PreviewResolutionError
	if errors.As(classified, &resolution) {
		return PreviewConflict(classified)
	}
	return classified
}
