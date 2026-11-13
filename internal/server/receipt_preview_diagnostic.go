package server

import (
	"errors"
	"log/slog"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func logReceiptPreviewContractFailure(logger *slog.Logger, requestID, receiptID, outputID string, err error) {
	if logger == nil || err == nil {
		return
	}
	var mismatch *receiptContractMismatch
	hasMismatch := errors.As(err, &mismatch)
	userErr, hasUserError := dataframeerrors.AsUserError(err)
	contractFailure := hasUserError && userErr.Code() == string(dataframeerrors.CodeRecipeContractViolation)
	if !hasMismatch && !contractFailure {
		return
	}

	attrs := []any{
		"request_id", requestID,
		"receipt_id", receiptID,
		"output_id", outputID,
		"error", err,
		"error_type", "receipt_preview_contract_failure",
	}
	if hasMismatch {
		attrs = append(attrs,
			"component", mismatch.Component,
			"expected", mismatch.Expected,
			"actual", mismatch.Actual,
		)
	}
	if hasUserError {
		attrs = append(attrs, "error_code", userErr.Code())
	}
	logger.Error("Explorer receipt preview execution contract failed", attrs...)
}
