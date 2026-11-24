package explorerapi

import (
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type explorerLegacyOperationError struct {
	status  int
	code    string
	message string
	details map[string]any
	cause   error
}

func (e *explorerLegacyOperationError) Error() string {
	if e == nil {
		return "Explorer operation failed"
	}
	return e.message
}

func (e *explorerLegacyOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func legacyOperationFailure(status int, code, message string, cause error) error {
	return &explorerLegacyOperationError{status: status, code: code, message: message, cause: cause}
}

func lifecycleLegacyFailure(err error) error {
	if err == nil {
		return nil
	}
	var value *ports.Error
	if !errors.As(err, &value) {
		return err
	}
	status := lifecycleErrorStatus(value.Class)
	return legacyOperationFailureDetails(status, value.Code, value.Message, value.Details, err)
}

func legacyOperationFailureDetails(status int, code, message string, details map[string]any, cause error) error {
	return &explorerLegacyOperationError{status: status, code: code, message: message, details: details, cause: cause}
}

func explorerErrorResponse(err error) (int, loomapi.ErrorResponse) {
	var operation *explorerLegacyOperationError
	if errors.As(err, &operation) {
		code, message := operation.code, operation.message
		if operation.status >= http.StatusInternalServerError {
			if strings.TrimSpace(code) == "" {
				code = "INTERNAL_ERROR"
			}
			if strings.TrimSpace(message) == "" {
				message = "unexpected internal error in Explorer operation; inspect the server log for the request ID"
			}
		}
		body := loomapi.ErrorBody{Code: code, Message: message}
		if len(operation.details) > 0 {
			details := make(map[string]interface{}, len(operation.details))
			for key, value := range operation.details {
				details[key] = value
			}
			body.Details = &details
		}
		return operation.status, loomapi.ErrorResponse{Error: body}
	}
	return http.StatusInternalServerError, loomapi.ErrorResponse{Error: loomapi.ErrorBody{
		Code:    "INTERNAL_ERROR",
		Message: "unexpected internal error in Explorer operation; inspect the server log for the request ID",
	}}
}
