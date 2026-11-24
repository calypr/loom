package rowsource

import (
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func unavailable(stage, code, message string, cause error) error {
	return support.Unavailable(stage, code, message, cause)
}

func conflict(stage, code, message string, details map[string]any, cause error) error {
	return support.Conflict(stage, code, message, details, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return support.Unprocessable(stage, code, message, cause)
}

func failure(class ports.ErrorClass, stage, code, message string, cause error) error {
	return support.Failure(class, stage, code, message, cause)
}

func notFound(stage, code, message string, cause error) error {
	return support.NotFound(stage, code, message, cause)
}
