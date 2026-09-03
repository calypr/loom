package server

import (
	"fmt"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

type HTTPRoutes struct {
	server   *httpapi.HTTPServer
	load     *loadapi.Handler
	releases publication.BundleCatalog
	scopes   *authscope.ScopeResolver
	graphql  graphapi.RouteConfig
	explorer *explorerHTTPHandlers
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)

func serviceErrorResponse(body httpapi.ErrorResponse) loomapi.ServiceErrorResponse {
	details := body.Error.Details
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
	return loomapi.ServiceErrorResponse{Error: loomapi.ServiceErrorBody{Code: body.Error.Code, Message: body.Error.Message, Details: detailsPtr, RequestId: requestIDPtr, Retryable: &retryable}}
}

func legacyErrorFromMap(body loomapi.RawJSON) (loomapi.LegacyErrorResponse, error) {
	value, ok := body["error"].(string)
	if !ok {
		return loomapi.LegacyErrorResponse{}, fmt.Errorf("legacy error response is missing a string error")
	}
	var result loomapi.LegacyErrorResponse
	if err := result.Error.FromLegacyErrorResponseError0(value); err != nil {
		return loomapi.LegacyErrorResponse{}, err
	}
	return result, nil
}

func unexpectedResponseStatus(operation string, status int) error {
	return fmt.Errorf("%s returned undocumented HTTP status %d", operation, status)
}
