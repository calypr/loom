package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
)

func diagnosticErrorAttrs(err error) []any {
	if err == nil {
		return []any{"success", true}
	}
	attrs := []any{"success", false, "error_type", fmt.Sprintf("%T", err)}
	if code := diagnosticErrorCode(err); code != "" {
		attrs = append(attrs, "error_code", code)
	}
	attrs = append(attrs, "cause", diagnosticErrorCause(err))
	return attrs
}

func diagnosticErrorCause(err error) string {
	return httpctx.CapCause(httpctx.RedactCause(diagnosticErrorCauseValue(err)))
}

func diagnosticErrorCauseValue(err error) string {
	if err == nil {
		return ""
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return dataframeerrors.PublicMessage(err)
	}
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Message != "" {
		return lifecycleErr.Message
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) && authoringErr.Diagnostic.Message != "" {
		return authoringErr.Diagnostic.Message
	}
	var compilationErr *explorercompilation.Error
	if errors.As(err, &compilationErr) && compilationErr.Message != "" {
		return compilationErr.Message
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "operation deadline exceeded"
	}
	var arangoErr shared.ArangoError
	if errors.As(err, &arangoErr) {
		return arangoDiagnosticCause(arangoErr)
	}
	var arangoErrPointer *shared.ArangoError
	if errors.As(err, &arangoErrPointer) && arangoErrPointer != nil {
		return arangoDiagnosticCause(*arangoErrPointer)
	}
	return err.Error()
}

func arangoDiagnosticCause(err shared.ArangoError) string {
	cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", err.ErrorNum, err.Code)
	if err.ErrorMessage != "" {
		cause += ": " + err.ErrorMessage
	}
	return cause
}

func diagnosticErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		return userErr.Code()
	}
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	var compilationErr *explorercompilation.Error
	if errors.As(err, &compilationErr) {
		return compilationErr.Code
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		return authoringErr.Diagnostic.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEADLINE_EXCEEDED"
	default:
		return ""
	}
}

func LogServerDiagnostic(logger *slog.Logger, level slog.Level, message, requestID, phase string, duration time.Duration, err error, fields ...any) {
	httpctx.LogDiagnostic(logger, level, message, requestID, phase, duration, err, func(err error) (string, string) {
		return diagnosticErrorCode(err), diagnosticErrorCause(err)
	}, fields...)
}
