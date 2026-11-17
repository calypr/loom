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
	server     *httpapi.HTTPServer
	load       *loadapi.Handler
	releases   publication.BundleCatalog
	scopes     *authscope.ScopeResolver
	graphql    graphapi.RouteConfig
	explorer   *explorerHTTPHandlers
	dataframes dataframeReadRoutes
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)

func serviceErrorResponse(body httpapi.ErrorResponse) loomapi.ServiceErrorResponse {
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

func unexpectedResponseStatus(operation string, status int) error {
	return fmt.Errorf("%s returned undocumented HTTP status %d", operation, status)
}
