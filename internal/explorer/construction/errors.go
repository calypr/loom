package construction

import (
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func failureDetails(class ports.ErrorClass, code, message string, details map[string]any, cause error) error {
	return support.FailureDetails(class, "construction-category-discovery", code, message, details, cause)
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

func notFound(stage, code, message string, cause error) error {
	return support.NotFound(stage, code, message, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return support.Unprocessable(stage, code, message, cause)
}

func internal(stage, code, message string, cause error) error {
	return support.Internal(stage, code, message, cause)
}
