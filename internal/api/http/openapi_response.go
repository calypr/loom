package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func ServiceErrorResponse(body ErrorResponse) loomapi.ServiceErrorResponse {
	details := body.Error.Details
	fieldPath := append([]string(nil), body.Error.FieldPath...)
	requestID := body.Error.RequestID
	retryable := body.Error.Retryable
	var detailsPtr *map[string]interface{}
	if details != nil {
		converted := map[string]interface{}(details)
		detailsPtr = &converted
	}
	var requestIDPtr *string
	if requestID != "" {
		requestIDPtr = &requestID
	}
	var fieldPathPtr *[]string
	if len(fieldPath) != 0 {
		fieldPathPtr = &fieldPath
	}
	return loomapi.ServiceErrorResponse{Error: loomapi.ServiceErrorBody{Code: body.Error.Code, Message: body.Error.Message, FieldPath: fieldPathPtr, Details: detailsPtr, RequestId: requestIDPtr, Retryable: &retryable}}
}

func UnexpectedResponseStatus(operation string, status int) error {
	return fmt.Errorf("%s returned undocumented HTTP status %d", operation, status)
}
func MapServiceError(err error, ctx context.Context) (int, loomapi.ServiceErrorResponse) {
	mapped := MapDataframeError(err, RequestIDFromContext(ctx))
	if mapped.Status >= http.StatusInternalServerError {
		LogServerDiagnostic(slog.Default(), slog.LevelError, "data request failed",
			RequestIDFromContext(ctx), "response", time.Duration(0), err)
	}
	return mapped.Status, ServiceErrorResponse(mapped.Body)
}

func RawJSON(value any) (loomapi.RawJSON, error) {
	if value == nil {
		return loomapi.RawJSON{}, nil
	}
	if raw, ok := value.(loomapi.RawJSON); ok {
		return raw, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result loomapi.RawJSON
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func DecodeStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}
