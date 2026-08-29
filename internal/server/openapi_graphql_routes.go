package server

import (
	"bytes"
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/gofiber/fiber/v3"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
)

func (r *HTTPRoutes) runGraphQL(ctx context.Context, handler http.Handler) (int, []byte, error) {
	if handler == nil {
		return http.StatusNotFound, []byte(`{"error":"not found"}`), nil
	}
	return runFiberHandler(ctx, fiberadaptor.HTTPHandlerWithContext(handler))
}

func (r *HTTPRoutes) ExecuteGraphQL(ctx context.Context, _ loomapi.ExecuteGraphQLRequestObject) (loomapi.ExecuteGraphQLResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Handler)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ExecuteGraphQL200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusUnauthorized {
		value, decodeErr := serviceError(body)
		return loomapi.ExecuteGraphQL401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, decodeErr
	}
	value, decodeErr := decodeResponse[loomapi.RawJSON](body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ExecuteGraphQL400JSONResponse{GraphQLBadRequestJSONResponse: loomapi.GraphQLBadRequestJSONResponse(value)}, nil
	case 422:
		return loomapi.ExecuteGraphQL422JSONResponse{GraphQLUnprocessableJSONResponse: loomapi.GraphQLUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ExecuteGraphQL500JSONResponse{GraphQLInternalErrorJSONResponse: loomapi.GraphQLInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("executeGraphQL", status)
	}
}

func (r *HTTPRoutes) ExecuteDataframeGraphQL(ctx context.Context, _ loomapi.ExecuteDataframeGraphQLRequestObject) (loomapi.ExecuteDataframeGraphQLResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Handler)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ExecuteDataframeGraphQL200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusUnauthorized {
		value, decodeErr := serviceError(body)
		return loomapi.ExecuteDataframeGraphQL401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, decodeErr
	}
	value, decodeErr := decodeResponse[loomapi.RawJSON](body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ExecuteDataframeGraphQL400JSONResponse{GraphQLBadRequestJSONResponse: loomapi.GraphQLBadRequestJSONResponse(value)}, nil
	case 422:
		return loomapi.ExecuteDataframeGraphQL422JSONResponse{GraphQLUnprocessableJSONResponse: loomapi.GraphQLUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ExecuteDataframeGraphQL500JSONResponse{GraphQLInternalErrorJSONResponse: loomapi.GraphQLInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("executeDataframeGraphQL", status)
	}
}

func (r *HTTPRoutes) GetGraphQLPlayground(ctx context.Context, _ loomapi.GetGraphQLPlaygroundRequestObject) (loomapi.GetGraphQLPlaygroundResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Playground)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetGraphQLPlayground200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}

func (r *HTTPRoutes) GetApolloSandbox(ctx context.Context, _ loomapi.GetApolloSandboxRequestObject) (loomapi.GetApolloSandboxResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Sandbox)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetApolloSandbox200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}
