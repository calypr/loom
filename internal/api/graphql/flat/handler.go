package clickhouse

import (
	"context"
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/calypr/loom/generated/graphql/flat/executor"
	clickhouseresolver "github.com/calypr/loom/generated/graphql/flat/resolver"
	graphqlapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func RegisterRoutes(router fiber.Router, handler http.Handler) {
	if handler != nil {
		router.Post("/graphql/flat", fiberadaptor.HTTPHandlerWithContext(handler))
	}
}

func NewHandler(service clickhouseresolver.MaterializationService) http.Handler {
	server := gqlhandler.NewDefaultServer(executor.NewExecutableSchema(executor.Config{
		Resolvers: clickhouseresolver.NewResolver(service),
	}))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		return graphqlapi.PresentGraphQLError(err, httpapi.RequestIDFromContext(ctx))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ctx, ok := fiberadaptor.LocalContextFromHTTPRequest(r); ok {
			r = r.WithContext(ctx)
		}
		if requestID := httpapi.RequestIDFromContext(r.Context()); r.Header.Get("X-Request-ID") == "" && requestID != "" {
			r.Header.Set("X-Request-ID", requestID)
		}
		r = r.WithContext(httpapi.ContextWithRequestID(r.Context(), r.Header.Get("X-Request-ID")))
		server.ServeHTTP(w, r)
	})
}
