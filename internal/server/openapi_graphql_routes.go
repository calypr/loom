package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	loomapi "github.com/calypr/loom/generated/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
)

// serveGraphQL invokes the native GraphQL http.Handler through a bounded
// request/response adapter. GraphQL remains a native HTTP transport; unlike
// the removed REST path this does not create a Fiber context or re-enter a
// Fiber handler.
// nilHandlerGraphQLEnvelope keeps the documented GraphQL response shape even
// on the defensive unwired path: every executed /graphql response, including
// this one, is a GraphQL envelope with top-level data/errors members.
const nilHandlerGraphQLEnvelope = `{"data":null,"errors":[{"message":"GraphQL handler is not configured"}]}`

func serveGraphQL(ctx context.Context, handler http.Handler, body any, method string) (int, []byte, error) {
	if handler == nil {
		return http.StatusNotFound, []byte(nilHandlerGraphQLEnvelope), nil
	}
	var input io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		input = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, "/graphql/graph", input).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if requestID := httpapi.RequestIDFromContext(ctx); requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes(), nil
}

func (r *HTTPRoutes) ExecuteGraphQL(ctx context.Context, request loomapi.ExecuteGraphQLRequestObject) (loomapi.ExecuteGraphQLResponseObject, error) {
	status, body, err := serveGraphQL(ctx, r.graphql.Handler, request.Body, http.MethodPost)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, conversionErr := rawJSONBytes(body)
		return loomapi.ExecuteGraphQL200JSONResponse(value), conversionErr
	}
	if status == http.StatusUnauthorized {
		var value loomapi.ServiceErrorResponse
		decodeErr := json.Unmarshal(body, &value)
		response, err := loomapi.NewExecuteGraphQLErrorResponse(status, value)
		if err != nil {
			return nil, err
		}
		return response, decodeErr
	}
	value, decodeErr := rawJSONBytes(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	return loomapi.NewExecuteGraphQLErrorResponse(status, value)
}

func (r *HTTPRoutes) ExecuteDataframeGraphQL(ctx context.Context, request loomapi.ExecuteDataframeGraphQLRequestObject) (loomapi.ExecuteDataframeGraphQLResponseObject, error) {
	status, body, err := serveGraphQL(ctx, r.graphql.Handler, request.Body, http.MethodPost)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, conversionErr := rawJSONBytes(body)
		return loomapi.ExecuteDataframeGraphQL200JSONResponse(value), conversionErr
	}
	if status == http.StatusUnauthorized {
		var value loomapi.ServiceErrorResponse
		decodeErr := json.Unmarshal(body, &value)
		response, err := loomapi.NewExecuteDataframeGraphQLErrorResponse(status, value)
		if err != nil {
			return nil, err
		}
		return response, decodeErr
	}
	value, decodeErr := rawJSONBytes(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	return loomapi.NewExecuteDataframeGraphQLErrorResponse(status, value)
}

func (r *HTTPRoutes) GetGraphQLPlayground(ctx context.Context, _ loomapi.GetGraphQLPlaygroundRequestObject) (loomapi.GetGraphQLPlaygroundResponseObject, error) {
	status, body, err := serveGraphQL(ctx, r.graphql.Playground, nil, http.MethodGet)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetGraphQLPlayground200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}

func (r *HTTPRoutes) GetApolloSandbox(ctx context.Context, _ loomapi.GetApolloSandboxRequestObject) (loomapi.GetApolloSandboxResponseObject, error) {
	status, body, err := serveGraphQL(ctx, r.graphql.Sandbox, nil, http.MethodGet)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetApolloSandbox200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}

func rawJSONBytes(body []byte) (loomapi.RawJSON, error) {
	var value loomapi.RawJSON
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return value, nil
}
