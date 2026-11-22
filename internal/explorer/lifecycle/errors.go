package lifecycle

import (
	"errors"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func failure(class ports.ErrorClass, stage, code, message string, cause error) error {
	return &ports.Error{Class: class, Stage: stage, Code: code, Message: message, Cause: cause}
}

func failureDetails(class ports.ErrorClass, stage, code, message string, details map[string]any, cause error) error {
	return &ports.Error{Class: class, Stage: stage, Code: code, Message: message, Details: details, Cause: cause}
}

func malformed(stage, message string, cause error) error {
	return failure(ports.ClassMalformed, stage, "MALFORMED_REQUEST", message, cause)
}

func unavailable(stage, code, message string, cause error) error {
	return failure(ports.ClassUnavailable, stage, code, message, cause)
}

func conflict(stage, code, message string, details map[string]any, cause error) error {
	if code == "STALE_CATALOG_SNAPSHOT" && cause != nil && !errors.Is(cause, capability.ErrStaleSnapshot) && !errors.Is(cause, capability.ErrSnapshotUnavailable) && !errors.Is(cause, capability.ErrNotFound) {
		return failureDetails(ports.ClassUnavailable, stage, "CAPABILITY_UNAVAILABLE", message, details, cause)
	}
	var typed *ports.Error
	if errors.As(cause, &typed) && typed.Class == ports.ClassUnavailable {
		return typed
	}
	return failureDetails(ports.ClassConflict, stage, code, message, details, cause)
}

func forbidden(stage, message string, cause error) error {
	return failure(ports.ClassForbidden, stage, "FORBIDDEN", message, cause)
}

func notFound(stage, code, message string, cause error) error {
	return failure(ports.ClassNotFound, stage, code, message, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return failure(ports.ClassUnprocessable, stage, code, message, cause)
}

func internal(stage, code, message string, cause error) error {
	return failure(ports.ClassInternal, stage, code, message, cause)
}

func persistenceFailure(stage, message string, err error) error {
	var typed *ports.Error
	if errors.As(err, &typed) {
		return typed
	}
	if errors.Is(err, explorer.ErrNotFound) {
		return notFound(stage, "NOT_FOUND", message, err)
	}
	return unavailable(stage, "PERSISTENCE_UNAVAILABLE", message, err)
}

func wrapReceiptLookup(stage string, err error) error {
	if errors.Is(err, explorer.ErrNotFound) {
		return notFound(stage, "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", err)
	}
	return unavailable(stage, "RECEIPT_STORE_UNAVAILABLE", "the compilation receipt store is unavailable", err)
}
