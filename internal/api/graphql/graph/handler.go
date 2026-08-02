package graphqlapi

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/99designs/gqlgen/graphql"
	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/calypr/loom/generated/graphql/graph/executor"
	"github.com/calypr/loom/generated/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type RouteConfig struct{ Handler, Playground, Sandbox http.Handler }

func RegisterRoutes(router fiber.Router, cfg RouteConfig) {
	if cfg.Playground != nil {
		router.Get("/graphql/graph", fiberadaptor.HTTPHandlerWithContext(cfg.Playground))
	}
	if cfg.Sandbox != nil {
		router.Get("/apollo", fiberadaptor.HTTPHandlerWithContext(cfg.Sandbox))
	}
	if cfg.Handler != nil {
		h := fiberadaptor.HTTPHandlerWithContext(cfg.Handler)
		router.Post("/graphql/graph", h)
		router.Post("/graphql/dataframe", h)
	}
}

func NewHandler(root *resolver.Resolver) http.Handler {
	server := gqlhandler.NewDefaultServer(executor.NewExecutableSchema(executor.Config{
		Resolvers: root,
	}))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		return PresentGraphQLError(err, httpapi.RequestIDFromContext(ctx))
	})
	server.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		return next(root.WithOperationContext(ctx))
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

func NewPlaygroundHandler(endpoint string) http.Handler {
	return playground.Handler("FHIR GraphQL Playground", endpoint)
}

func NewApolloSandboxHandler(endpoint string) http.Handler {
	page := template.Must(template.New("apollo-sandbox").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>FHIR GraphQL Apollo Sandbox</title>
    <style>
      html, body, #embedded-sandbox {
        width: 100%;
        height: 100%;
        margin: 0;
        overflow: hidden;
      }
      body {
        font-family: sans-serif;
        background: #0f172a;
      }
    </style>
  </head>
  <body>
    <div id="embedded-sandbox"></div>
    <script
      src="https://embeddable-sandbox.cdn.apollographql.com/_latest/embeddable-sandbox.umd.production.min.js"
      crossorigin="anonymous">
    </script>
    <script>
      new window.EmbeddedSandbox({
        target: "#embedded-sandbox",
        initialEndpoint: {{.EndpointJSON}},
        includeCookies: false
      });
    </script>
  </body>
</html>`))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpointJSON, err := json.Marshal(endpoint)
		if err != nil {
			http.Error(w, "failed to render apollo sandbox", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, struct {
			EndpointJSON template.JS
		}{
			EndpointJSON: template.JS(endpointJSON),
		})
	})
}
