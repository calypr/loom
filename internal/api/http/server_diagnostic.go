package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
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
	return capDiagnosticCause(redactDiagnosticCause(diagnosticErrorCauseValue(err)))
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

const maxDiagnosticCauseBytes = 256

func capDiagnosticCause(cause string) string {
	if len(cause) <= maxDiagnosticCauseBytes {
		return cause
	}
	return strings.ToValidUTF8(cause[:maxDiagnosticCauseBytes-3], "") + "..."
}

func arangoDiagnosticCause(err shared.ArangoError) string {
	cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", err.ErrorNum, err.Code)
	if err.ErrorMessage != "" {
		cause += ": " + err.ErrorMessage
	}
	return cause
}

var (
	diagnosticCredentialAssignment = regexp.MustCompile(`(?i)((?:"|')?(?:authorization|proxy-authorization|access[_ -]?token|refresh[_ -]?token|token|password|secret|api[_ -]?key|apikey|credential)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}\]]+)`)
	diagnosticBearerCredential     = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/-]+=*`)
	diagnosticURLCredential        = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
	diagnosticPayloadAssignment    = regexp.MustCompile(`(?i)\b((?:"|')?(?:query|bind[_ -]?vars?|bind[_ -]?values?|(?:request|response)[_ -]?body|body)(?:"|')?\s*[:=]\s*)`)
)

func redactDiagnosticCause(cause string) string {
	cause = diagnosticCredentialAssignment.ReplaceAllString(cause, "${1}[redacted]")
	cause = diagnosticBearerCredential.ReplaceAllString(cause, "[redacted credential]")
	cause = diagnosticURLCredential.ReplaceAllString(cause, "${1}[redacted]@")
	if match := diagnosticPayloadAssignment.FindStringIndex(cause); match != nil {
		return cause[:match[0]] + cause[match[0]:match[1]] + "[redacted]"
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
	if logger == nil {
		return
	}
	attrs := []any{"request_id", requestID, "phase", phase, "duration_ms", duration.Milliseconds()}
	attrs = append(attrs, diagnosticErrorAttrs(err)...)
	attrs = append(attrs, fields...)
	logger.Log(context.Background(), level, message, attrs...)
}
