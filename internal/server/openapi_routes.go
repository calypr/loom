package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
)

type strictFiberContextKey struct{}

type HTTPRoutes struct {
	server   *httpapi.HTTPServer
	load     *loadapi.Handler
	recipe   httpapi.RecipeExecutionHandler
	graphql  graphapi.RouteConfig
	explorer *explorerHTTPHandlers
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)

func strictFiberContextMiddleware(next loomapi.StrictHandlerFunc, _ string) loomapi.StrictHandlerFunc {
	return func(c fiber.Ctx, args any) (any, error) {
		c.SetContext(context.WithValue(c.Context(), strictFiberContextKey{}, c))
		return next(c, args)
	}
}

func fiberContext(ctx context.Context) (fiber.Ctx, error) {
	c, ok := ctx.Value(strictFiberContextKey{}).(fiber.Ctx)
	if !ok || c == nil {
		return nil, fmt.Errorf("generated route is missing Fiber request context")
	}
	return c, nil
}

func runFiberHandler(ctx context.Context, handler fiber.Handler) (int, []byte, error) {
	c, err := fiberContext(ctx)
	if err != nil {
		return 0, nil, err
	}
	if handler == nil {
		return http.StatusNotFound, []byte(`{"error":"not found"}`), nil
	}
	if err := handler(c); err != nil {
		return 0, nil, err
	}
	status := c.Response().StatusCode()
	body := append([]byte(nil), c.Response().Body()...)
	c.Response().ResetBody()
	return status, body, nil
}

func decodeResponse[T any](body []byte) (T, error) {
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		return value, fmt.Errorf("decode generated response: %w", err)
	}
	return value, nil
}

func authoringError(body []byte, status int) (loomapi.ErrorResponse, int, error) {
	value, err := decodeResponse[loomapi.ErrorResponse](body)
	return value, status, err
}

func serviceError(body []byte) (loomapi.ServiceErrorResponse, error) {
	return decodeResponse[loomapi.ServiceErrorResponse](body)
}

func legacyError(body []byte) (loomapi.LegacyErrorResponse, error) {
	return decodeResponse[loomapi.LegacyErrorResponse](body)
}

func unexpectedResponseStatus(operation string, status int) error {
	return fmt.Errorf("%s returned undocumented HTTP status %d", operation, status)
}
