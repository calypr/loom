package support

import (
	"errors"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func Failure(class ports.ErrorClass, stage, code, message string, cause error) error {
	return &ports.Error{Class: class, Stage: stage, Code: code, Message: message, Cause: cause}
}

func FailureDetails(class ports.ErrorClass, stage, code, message string, details map[string]any, cause error) error {
	return &ports.Error{Class: class, Stage: stage, Code: code, Message: message, Details: details, Cause: cause}
}

func Malformed(stage, message string, cause error) error {
	return Failure(ports.ClassMalformed, stage, "MALFORMED_REQUEST", message, cause)
}

func Unavailable(stage, code, message string, cause error) error {
	return Failure(ports.ClassUnavailable, stage, code, message, cause)
}

func Conflict(stage, code, message string, details map[string]any, cause error) error {
	if code == "STALE_CATALOG_SNAPSHOT" && cause != nil && !errors.Is(cause, capability.ErrStaleSnapshot) && !errors.Is(cause, capability.ErrSnapshotUnavailable) && !errors.Is(cause, capability.ErrNotFound) {
		return FailureDetails(ports.ClassUnavailable, stage, "CAPABILITY_UNAVAILABLE", message, details, cause)
	}
	var typed *ports.Error
	if errors.As(cause, &typed) && typed.Class == ports.ClassUnavailable {
		return typed
	}
	return FailureDetails(ports.ClassConflict, stage, code, message, details, cause)
}

func Forbidden(stage, message string, cause error) error {
	return Failure(ports.ClassForbidden, stage, "FORBIDDEN", message, cause)
}

func NotFound(stage, code, message string, cause error) error {
	return Failure(ports.ClassNotFound, stage, code, message, cause)
}

func Unprocessable(stage, code, message string, cause error) error {
	return Failure(ports.ClassUnprocessable, stage, code, message, cause)
}

func Internal(stage, code, message string, cause error) error {
	return Failure(ports.ClassInternal, stage, code, message, cause)
}

func PersistenceFailure(stage, message string, err error) error {
	var typed *ports.Error
	if errors.As(err, &typed) {
		return typed
	}
	if errors.Is(err, explorer.ErrNotFound) {
		return NotFound(stage, "NOT_FOUND", message, err)
	}
	return Unavailable(stage, "PERSISTENCE_UNAVAILABLE", message, err)
}

func WrapReceiptLookup(stage string, err error) error {
	if errors.Is(err, explorer.ErrNotFound) {
		return NotFound(stage, "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", err)
	}
	return Unavailable(stage, "RECEIPT_STORE_UNAVAILABLE", "the compilation receipt store is unavailable", err)
}
