package receipt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
)

func logServerDiagnostic(logger *slog.Logger, level slog.Level, message, requestID, phase string, duration time.Duration, err error, fields ...any) {
	httpctx.LogDiagnostic(logger, level, message, requestID, phase, duration, err, classifyDiagnostic, fields...)
}

func classifyDiagnostic(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	code, cause := "", ""
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		code, cause = userErr.Code(), dataframeerrors.PublicMessage(err)
	} else {
		var lifecycleErr *ports.Error
		if errors.As(err, &lifecycleErr) {
			code, cause = lifecycleErr.Code, lifecycleErr.Message
		}
		var authoringErr *explorer.AuthoringError
		if errors.As(err, &authoringErr) {
			code, cause = authoringErr.Diagnostic.Code, authoringErr.Diagnostic.Message
		}
		var compilationErr *compilation.Error
		if errors.As(err, &compilationErr) {
			code, cause = compilationErr.Code, compilationErr.Message
		}
	}
	if code == "" {
		switch {
		case errors.Is(err, context.Canceled):
			code, cause = "CANCELED", "operation canceled"
		case errors.Is(err, context.DeadlineExceeded):
			code, cause = "DEADLINE_EXCEEDED", "operation deadline exceeded"
		}
	}
	if cause == "" {
		var arangoErr shared.ArangoError
		if errors.As(err, &arangoErr) {
			cause = fmt.Sprintf("ArangoDB error number %d (HTTP %d)", arangoErr.ErrorNum, arangoErr.Code)
			if arangoErr.ErrorMessage != "" {
				cause += ": " + arangoErr.ErrorMessage
			}
		} else {
			cause = err.Error()
		}
	}
	return code, cause
}
