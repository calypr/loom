package lifecycle

import (
	"errors"

	"github.com/calypr/loom/internal/explorer"
)

// ErrorClass lets transports map application failures to their own response
// contracts without making HTTP status codes part of the application layer.
type ErrorClass string

const (
	ClassMalformed     ErrorClass = "malformed"
	ClassForbidden     ErrorClass = "forbidden"
	ClassNotFound      ErrorClass = "not_found"
	ClassConflict      ErrorClass = "conflict"
	ClassUnprocessable ErrorClass = "unprocessable"
	ClassUnavailable   ErrorClass = "unavailable"
	ClassInternal      ErrorClass = "internal"
)

// Error is a typed application failure. Details are safe, structured
// diagnostics; Cause remains available to server-side logging and is never
// serialized by lifecycle.
type Error struct {
	Class   ErrorClass
	Stage   string
	Code    string
	Path    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "Explorer lifecycle error"
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func failure(class ErrorClass, stage, code, message string, cause error) error {
	return &Error{Class: class, Stage: stage, Code: code, Message: message, Cause: cause}
}

func failureDetails(class ErrorClass, stage, code, message string, details map[string]any, cause error) error {
	return &Error{Class: class, Stage: stage, Code: code, Message: message, Details: details, Cause: cause}
}

func malformed(stage, message string, cause error) error {
	return failure(ClassMalformed, stage, "MALFORMED_REQUEST", message, cause)
}

func unavailable(stage, code, message string, cause error) error {
	return failure(ClassUnavailable, stage, code, message, cause)
}

func conflict(stage, code, message string, details map[string]any, cause error) error {
	return failureDetails(ClassConflict, stage, code, message, details, cause)
}

func forbidden(stage, message string, cause error) error {
	return failure(ClassForbidden, stage, "FORBIDDEN", message, cause)
}

func notFound(stage, code, message string, cause error) error {
	return failure(ClassNotFound, stage, code, message, cause)
}

func unprocessable(stage, code, message string, cause error) error {
	return failure(ClassUnprocessable, stage, code, message, cause)
}

func internal(stage, code, message string, cause error) error {
	return failure(ClassInternal, stage, code, message, cause)
}

func wrapReceiptLookup(stage string, err error) error {
	if errors.Is(err, explorer.ErrNotFound) {
		return notFound(stage, "COMPILE_RECEIPT_NOT_FOUND", "compilation receipt was not found", err)
	}
	return unavailable(stage, "RECEIPT_STORE_UNAVAILABLE", "the compilation receipt store is unavailable", err)
}
