package construction

import (
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func failure(class ports.ErrorClass, stage, code, message string, cause error) error {
	return support.Failure(class, stage, code, message, cause)
}

func failureDetails(class ports.ErrorClass, stage, code, message string, details map[string]any, cause error) error {
	return support.FailureDetails(class, stage, code, message, details, cause)
}

func malformed(stage, message string, cause error) error {
	return support.Malformed(stage, message, cause)
}

func unavailable(stage, code, message string, cause error) error {
	return support.Unavailable(stage, code, message, cause)
}

func conflict(stage, code, message string, details map[string]any, cause error) error {
	return support.Conflict(stage, code, message, details, cause)
}

func forbidden(stage, message string, cause error) error {
	return support.Forbidden(stage, message, cause)
}

func notFound(stage, code, message string, cause error) error {
	return support.NotFound(stage, code, message, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return support.Unprocessable(stage, code, message, cause)
}

func internal(stage, code, message string, cause error) error {
	return support.Internal(stage, code, message, cause)
}

func persistenceFailure(stage, message string, err error) error {
	return support.PersistenceFailure(stage, message, err)
}

func capabilityFailure(stage, code, message string, err error) error {
	return support.CapabilityFailure(stage, code, message, err)
}

func receiptCapabilityFailure(stage, message string, err error) error {
	return support.ReceiptCapabilityFailure(stage, message, err)
}
