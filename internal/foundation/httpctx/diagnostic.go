package httpctx

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

type ErrorClassifier func(error) (code, cause string)

const MaxCauseBytes = 256

var (
	diagnosticCredentialAssignment = regexp.MustCompile(`(?i)((?:"|')?(?:authorization|proxy-authorization|access[_ -]?token|refresh[_ -]?token|token|password|secret|api[_ -]?key|apikey|credential)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}\]]+)`)
	diagnosticBearerCredential     = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/-]+=*`)
	diagnosticURLCredential        = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
	diagnosticPayloadAssignment    = regexp.MustCompile(`(?i)\b((?:"|')?(?:query|bind[_ -]?vars?|bind[_ -]?values?|(?:request|response)[_ -]?body|body)(?:"|')?\s*[:=]\s*)`)
)

func RedactCause(cause string) string {
	cause = diagnosticCredentialAssignment.ReplaceAllString(cause, "${1}[redacted]")
	cause = diagnosticBearerCredential.ReplaceAllString(cause, "[redacted credential]")
	cause = diagnosticURLCredential.ReplaceAllString(cause, "${1}[redacted]@")
	if match := diagnosticPayloadAssignment.FindStringIndex(cause); match != nil {
		return cause[:match[0]] + cause[match[0]:match[1]] + "[redacted]"
	}
	return cause
}

func CapCause(cause string) string {
	if len(cause) <= MaxCauseBytes {
		return cause
	}
	return strings.ToValidUTF8(cause[:MaxCauseBytes-3], "") + "..."
}

func LogDiagnostic(logger *slog.Logger, level slog.Level, message, requestID, phase string, duration time.Duration, err error, classify ErrorClassifier, fields ...any) {
	if logger == nil {
		return
	}
	attrs := []any{"request_id", requestID, "phase", phase, "duration_ms", duration.Milliseconds()}
	if err == nil {
		attrs = append(attrs, "success", true)
	} else {
		code, cause := "", err.Error()
		if classify != nil {
			code, cause = classify(err)
		}
		attrs = append(attrs, "success", false, "error_type", typeName(err))
		if code != "" {
			attrs = append(attrs, "error_code", code)
		}
		attrs = append(attrs, "cause", CapCause(RedactCause(cause)))
	}
	attrs = append(attrs, fields...)
	logger.Log(context.Background(), level, message, attrs...)
}

func typeName(err error) string { return fmt.Sprintf("%T", err) }
